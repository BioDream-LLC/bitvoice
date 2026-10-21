// Command bitvoice is the BitVoice phone system: one program with the phone engine and its web interface.
//
// Double-click it (or run it with no arguments) and a browser opens on the setup wizard.
package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	_ "time/tzdata" // time zones for opening hours, whatever the host has installed

	"github.com/biodream-llc/bitvoice/internal/acme"
	"github.com/biodream-llc/bitvoice/internal/api"
	"github.com/biodream-llc/bitvoice/internal/certs"
	"github.com/biodream-llc/bitvoice/internal/cluster"
	"github.com/biodream-llc/bitvoice/internal/compat"
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/h323"
	"github.com/biodream-llc/bitvoice/internal/iax"
	"github.com/biodream-llc/bitvoice/internal/inbox"
	"github.com/biodream-llc/bitvoice/internal/mgcp"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/reason"
	"github.com/biodream-llc/bitvoice/internal/sccp"
	"github.com/biodream-llc/bitvoice/internal/store"
	"github.com/biodream-llc/bitvoice/internal/turn"
	"github.com/biodream-llc/bitvoice/internal/upgrade"
	"github.com/biodream-llc/bitvoice/internal/web"
	"github.com/biodream-llc/bitvoice/internal/webphone"
	"github.com/biodream-llc/bitvoice/internal/whisper"
)

// version is set at build time by the Makefile (-X main.version=...).
var version = "dev"

// releaseKey is BioDream's release public key (base64 ed25519), set in release builds; with it, only
// signed upgrades are accepted.
var releaseKey = ""

// errRestart asks run to start the program again (after an upgrade or a rollback).
var errRestart = errors.New("restart")

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type options struct {
	data      string
	local     string // loopback HTTP address for the browser on this computer
	secure    string // HTTPS address for every other computer
	sip       string
	noBrowser bool
	database  string // a PostgreSQL address; empty reads the data directory's setting
}

// run is main without the process exit, so it can be tested.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bitvoice", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.StringVar(&o.data, "data", defaultDataDir(), "directory for configuration, call records and certificates")
	fs.StringVar(&o.local, "http", "", "no longer used: the web interface is HTTPS only (see -https)")
	fs.StringVar(&o.secure, "https", ":8443", "HTTPS address of the web interface")
	fs.StringVar(&o.sip, "sip", "", "SIP listen address (default: all addresses, port from settings)")
	fs.BoolVar(&o.noBrowser, "no-browser", false, "do not open a browser at startup")
	fs.StringVar(&o.database, "database", os.Getenv("BITVOICE_DATABASE"), "PostgreSQL address (postgres://user:password@host/db); default: the SQLite file in the data directory, or the database chosen in Settings")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "bitvoice %s\n", version)
		return 0
	}
	self, _ := os.Executable()
	// The installed program is the launcher: if an upgrade made another version current, start it.
	if act := upgrade.Decide(o.data, self, os.Getenv("BITVOICE_LAUNCHED") == "1"); act.Exec != "" {
		if err := relaunch(act.Exec, args); err != nil {
			fmt.Fprintln(stderr, "bitvoice: could not start", act.Exec+":", err, "- running this version instead")
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := serve(ctx, o, stdout, stderr)
	if errors.Is(err, errRestart) {
		stop()
		next := upgrade.Load(o.data).Current
		if next == "" {
			next = self
		}
		fmt.Fprintln(stdout, "BitVoice is restarting as", next)
		if err := relaunch(next, args); err != nil {
			fmt.Fprintln(stderr, "bitvoice: restart failed:", err)
			return 1
		}
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "bitvoice:", err)
		return 1
	}
	return 0
}

func defaultDataDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "BitVoice")
	}
	return "bitvoice-data"
}

func serve(ctx context.Context, o options, stdout, stderr io.Writer) error {
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	spec, fixed := o.database, o.database != ""
	if spec == "" {
		var err error
		if spec, err = store.DatabaseSetting(o.data); err != nil {
			return err
		}
	}
	st, err := store.OpenWith(o.data, spec)
	if err != nil {
		return fmt.Errorf("opening the data: %w", err)
	}
	defer st.Close()
	log.Info("data", "database", st.Where())
	if st.DB().Postgres {
		// Servers sharing the database pick up each other's settings changes.
		go func() {
			t := time.NewTicker(5 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					st.Sync()
				}
			}
		}()
	}
	cfg, _ := st.Config()
	bus := events.New()
	trace := api.NewTraceBuffer(2000)
	// The judges' rulebooks are checked in every combination of their facts before any call is judged.
	for _, b := range reason.Books {
		if err := b.Validate(); err != nil {
			return err
		}
		if rep := b.Check(); len(rep.Problems) > 0 {
			log.Warn("a rulebook has problems", "rulebook", b.Name, "problems", len(rep.Problems), "first", rep.Problems[0].Says)
		}
	}
	eng := pbx.New(cfg, bus, st, log)
	eng.Trace = trace.Add
	// Speech to text on this server: whisper-server, started and watched while the AI settings ask for it.
	eng.Speech = whisper.New(filepath.Join(o.data, "whisper"), log)
	eng.Speech.OnChange = func(s whisper.Status) { bus.Publish("speech.changed", s) }
	applySpeech := func(c config.Config) {
		eng.Speech.Apply(c.System.AI.Enabled && c.System.AI.LocalSpeech, c.System.AI.LocalModel)
	}
	applySpeech(cfg)
	st.OnChange(applySpeech)
	defer eng.Speech.Close()
	sw := pbx.NewSwitch(eng) // one SIP port for the main system and every hosted tenant
	addresses := lanAddresses()
	cert, certErr := certs.Load(filepath.Join(o.data, "tls"), addresses)
	var holder *certs.Holder
	if certErr == nil {
		holder = certs.NewHolder(filepath.Join(o.data, "tls"), cert)
		eng.TLS = &tls.Config{GetCertificate: holder.Get, MinVersion: tls.VersionTLS12}
		// Present the same certificate when connecting out over TLS (Teams Direct Routing checks it).
		eng.TLSClient = &tls.Config{GetClientCertificate: holder.Client, MinVersion: tls.VersionTLS12}
		eng.CertPin = holder.Pin
	}
	var cl *cluster.Cluster
	if st.DB().Postgres {
		// Servers sharing the database share their phones: calls reach a phone whichever server it is on.
		if cl, err = cluster.New(st.DB(), o.data, version, ""); err != nil {
			return fmt.Errorf("joining the other servers: %w", err)
		}
		eng.Cluster = cl
	}
	if err := startSIP(ctx, eng, o.sip, cfg.System.SIPPort, log); err != nil {
		return err
	}
	if cl != nil {
		host := ""
		if h, _, err := net.SplitHostPort(o.sip); err == nil && h != "" && h != "0.0.0.0" && h != "::" {
			host = h
		} else if as := lanAddresses(); len(as) > 0 {
			host = as[0]
		}
		cl.SetDefaultAddr(net.JoinHostPort(host, strconv.Itoa(eng.SIPPort())))
		left := make(chan struct{})
		cl.OnChange = eng.ClusterChanged
		cl.Slow = func(d time.Duration) {
			log.Warn("checking in with the other servers was slow", "took", d.Round(time.Millisecond))
		}
		cl.TakeOver, cl.LetGo = eng.TakeOver, eng.LetGo
		eng.HandOver = func() map[string][]byte { return cl.HandOver(eng.CallSnapshots()) }
		go func() { cl.Run(ctx, eng.ClusterState); close(left) }()
		defer func() {
			// Tell the others this server is gone before exiting, so they stop sending it calls at once.
			select {
			case <-left:
			case <-time.After(4 * time.Second):
			}
		}()
		log.Info("sharing phones with the other servers", "server", cl.Self().Name, "address", cl.Self().SIPAddr)
	}
	defer eng.Stop()
	go pruneRecordings(ctx, st, log)

	restart := make(chan struct{}, 1)
	srv := &api.Server{Cluster: cl, DatabaseFixed: fixed, Certs: holder, Store: st, Engine: eng, Bus: bus, Log: log, Version: version, Trace: trace, SetupCode: setupCode(),
		DataDir: o.data, ReleaseKey: releaseKey, Restart: func() {
			select {
			case restart <- struct{}{}:
			default:
			}
		}}
	// Asterisk compatibility: AMI on its own port, ARI under /ari.
	ami := &compat.AMI{PBX: eng, Bus: bus, Log: log, Audit: srv.AuditSystem, Started: time.Now()}
	ami.Apply(cfg.System.Compat)
	st.OnChange(func(c config.Config) { ami.Apply(c.System.Compat) })
	defer ami.Close()
	srv.AMIAddr = ami.Addr
	ari := &compat.ARI{PBX: eng, Bus: bus, Audit: srv.AuditSystem, Started: time.Now(), Recordings: filepath.Join(o.data, "ari-recordings")}
	srv.ARI = ari
	eng.Stasis = ari // calls sent to ARI applications
	// The engine runs on the configuration with vault references resolved.
	srv.WireEngine()
	eng.HoldsAddress = srv.HoldsSharedAddress
	if web.Available() {
		h, err := web.Handler()
		if err != nil {
			return err
		}
		srv.Web = h
	}
	// IAX2 trunks (Asterisk's own protocol): the port opens with the first IAX2 trunk.
	iaxSvc := iax.NewService(ctx, iaxAddr(o.sip), eng.LocalSIP, log)
	// H.323 trunks (port 1720) go through a loopback gateway too, with the same secret.
	h323Svc := h323.NewService(ctx, portAddr(o.sip, "1720"), eng.LocalSIP, iaxSvc.Token(), log)
	eng.SetIAX(loopbackTrunks{iax: iaxSvc, h323: h323Svc})
	applyIAX := func(c config.Config) {
		r := srv.Resolved(c)
		iaxSvc.Apply(iaxTrunks(r))
		h323Svc.Apply(h323Trunks(r))
	}
	applyIAX(cfg)
	st.OnChange(applyIAX)
	defer iaxSvc.Close()
	defer h323Svc.Close()
	// Skinny (SCCP) desk phones and MGCP gateway ports: each is a device of its extension, bridged
	// through loopback SIP as browser phones are.
	if desk, err := webphone.New(ctx, eng.SIPPort(), eng); err != nil {
		log.Error("desk phones: no loopback bridge", "err", err)
	} else {
		var resolved atomic.Pointer[config.Config]
		secret := func(ext string) string {
			if c := resolved.Load(); c != nil {
				if e := c.FindExtension(ext); e != nil {
					return e.Secret
				}
			}
			return ""
		}
		mg := mgcp.New(ctx, desk, eng, secret, log)
		sc := sccp.New(ctx, desk, eng, secret, log)
		applyDesk := func(c config.Config) {
			r := srv.Resolved(c)
			resolved.Store(&r)
			mg.Apply(r.System.Desk.MGCP)
			sc.Apply(r.System.Desk.SCCP, "")
		}
		applyDesk(cfg)
		st.OnChange(applyDesk)
		srv.DeskStatus = func() map[string]map[string]string {
			return map[string]map[string]string{"sccp": sc.Status(), "mgcp": mg.Status()}
		}
		defer mg.Close()
		defer sc.Close()
	}
	// Other organisations hosted here, each with its own data, sharing the SIP and media ports.
	ty := &api.Tenancy{Root: srv, Switch: sw, DataDir: o.data, DBSpec: spec, Log: log, Version: version, Web: srv.Web}
	srv.Tenancy = ty
	handler := ty.Wrap(srv.Handler())
	ty.Start(ctx)
	defer ty.Stop()
	srv.RunBackground(ctx, true)

	// The web interface is HTTPS only, on this computer too. Plain HTTP to the port is answered with
	// a redirect to HTTPS, except the desk phones' settings files when phones may use plain HTTP.
	if certErr != nil {
		return fmt.Errorf("web interface: no certificate for HTTPS: %w", certErr)
	}
	if o.local != "" {
		log.Warn("-http is no longer used: the web interface is HTTPS only", "https", o.secure)
	}
	ln, err := listenFirstFree(o.secure, 20)
	if err != nil {
		return fmt.Errorf("web interface: %w", err)
	}
	// Let's Encrypt (or another ACME authority): gets and renews the certificate, answering http-01 on
	// port 80 and on the web port's plain HTTP, and tls-alpn-01 on the web port.
	acm := &acme.Manager{Dir: filepath.Join(o.data, "tls"), Log: log,
		Settings: func() acme.Settings {
			c, _ := st.Config()
			a := c.System.ACME
			dir := a.Directory
			if dir == "staging" {
				dir = acme.LetsEncryptStaging
			}
			return acme.Settings{Enabled: a.Enabled, Domains: a.Domains, Email: a.Email, Directory: dir,
				Challenge: a.Challenge, HTTPPort: a.HTTPPort, HTTPSPort: int(srv.HTTPSPort.Load())}
		},
		Current: func() acme.Current {
			i := holder.Info()
			return acme.Current{Names: i.Names, NotAfter: i.NotAfter, SelfSigned: i.SelfSigned}
		},
		Install: func(certPEM, keyPEM string) error { _, err := holder.Install(certPEM, keyPEM); return err }}
	// Meta's Graph API address: a newer version, or a stand-in for tests.
	if g := os.Getenv("BITVOICE_META_GRAPH"); strings.HasPrefix(g, "https://") || strings.HasPrefix(g, "http://127.0.0.1:") {
		inbox.GraphBase = strings.TrimRight(g, "/")
	}
	srv.ACMEStatus, srv.ACMEWake, srv.ACMECheck = acm.Status, acm.Wake, acm.CheckReach
	tlsCfg := &tls.Config{GetCertificate: holder.Get, MinVersion: tls.VersionTLS12}
	tlsCfg.GetConfigForClient = acm.ConfigForClient(tlsCfg)
	httpsSrv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, TLSConfig: tlsCfg}
	webPort := newSplitPort(ln)
	plainSrv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: acm.Handler(plainHandler(handler, func() bool {
		c, _ := st.Config()
		return !c.System.Provisioning.HTTPSOnly
	}))}
	go func() { _ = httpsSrv.ServeTLS(webPort.tls, "", "") }()
	go func() { _ = plainSrv.Serve(webPort.plain) }()
	defer webPort.Close()
	defer plainSrv.Close()
	defer httpsSrv.Close()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	if n, err := strconv.Atoi(port); err == nil {
		srv.HTTPSPort.Store(int32(n))
	}
	go acm.Run(ctx)
	st.OnChange(func(config.Config) { acm.Wake() })

	// TURN for browser and WebRTC phones that cannot reach the media ports directly: relays only to
	// this computer's own addresses.
	publicIP := func() netip.Addr {
		c, _ := st.Config()
		a, _ := netip.ParseAddr(c.System.PublicAddress)
		return a
	}
	turnSrv := &turn.Server{Realm: "bitvoice", Log: log, Route: turn.LocalRoute(publicIP)}
	turnSvc := &turn.Service{Server: turnSrv, TLS: &tls.Config{GetCertificate: holder.Get, MinVersion: tls.VersionTLS12}}
	applyTURN := func(c config.Config) {
		t := c.System.TURN
		if a := publicIP(); a.IsValid() {
			turnSrv.Advertise = a
		} else if len(addresses) > 0 {
			turnSrv.Advertise, _ = netip.ParseAddr(addresses[0])
		}
		turnSvc.Apply(turn.Settings{Enabled: t.Enabled, Port: t.Port, TLSPort: t.TLSPort, RelayMin: t.RelayMin, RelayMax: t.RelayMax, Secret: t.Secret})
	}
	turnCfg, _ := st.Config()
	applyTURN(turnCfg)
	st.OnChange(applyTURN)
	defer turnSvc.Close()
	srv.TURNStatus = turnSvc.Status
	srv.ICEServers = func(host string) []api.ICEServer {
		c, _ := st.Config()
		tc := c.System.TURN
		if !tc.Enabled || tc.Secret == "" || host == "" {
			return nil
		}
		user, pass := turn.Credentials([]byte(tc.Secret), "web", 12*time.Hour)
		hp := net.JoinHostPort(host, strconv.Itoa(tc.Port))
		urls := []string{"turn:" + hp + "?transport=udp", "turn:" + hp + "?transport=tcp"}
		// TURN over TLS only with a certificate browsers can trust for this name: otherwise they
		// try it, fail quietly and wait.
		if tc.TLSPort > 0 && holder != nil && !holder.Info().SelfSigned && holder.Covers(host) {
			urls = append(urls, "turns:"+net.JoinHostPort(host, strconv.Itoa(tc.TLSPort))+"?transport=tcp")
		}
		return []api.ICEServer{{URLs: urls, Username: user, Credential: pass}}
	}
	// The browser on this computer: the loopback address, which the certificate covers.
	localURL := "https://" + net.JoinHostPort("127.0.0.1", port)
	var secureURLs []string
	// Listening on one address (-https 10.0.0.5:8443) is reachable only there; list every address
	// only when listening on all of them.
	reach := addresses
	if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
		reach = nil
		if !ip.IsLoopback() {
			reach = []string{host}
			localURL = "https://" + net.JoinHostPort(host, port)
		}
	}
	for _, a := range reach {
		secureURLs = append(secureURLs, "https://"+net.JoinHostPort(a, port))
	}

	fmt.Fprintf(stdout, "BitVoice %s is running.\n", version)
	fmt.Fprintf(stdout, "  On this computer:   %s\n", localURL)
	for _, u := range secureURLs {
		fmt.Fprintf(stdout, "  From your network:  %s\n", u)
	}
	fmt.Fprintf(stdout, "  Phones connect to port %d (SIP, UDP and TCP).\n", eng.SIPPort())
	if !st.State().SetupComplete {
		fmt.Fprintf(stdout, "  Setup code for another computer: %s\n", srv.SetupCode)
	}
	if runtime.GOOS == "windows" {
		fmt.Fprintln(stdout, "  Keep this window open: closing it stops BitVoice.")
	}
	if !o.noBrowser && hasDisplay() {
		openBrowser(localURL)
	}
	// A new version that has stayed up for a minute is confirmed; until then a crash goes back.
	go func() {
		select {
		case <-time.After(time.Minute):
			upgrade.Confirm(o.data, version)
		case <-ctx.Done():
		}
	}()
	restarting := false
	select {
	case <-ctx.Done():
	case <-restart:
		restarting = true
	}
	fmt.Fprintln(stdout, "BitVoice is stopping.")
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpsSrv.Shutdown(shut)
	if restarting {
		return errRestart
	}
	return nil
}

// startSIP listens on the configured SIP port, or the next free one if another phone system has it, so a
// double-click never fails just because port 5060 is taken. The health page shows the port in use.
func startSIP(ctx context.Context, eng *pbx.Engine, listen string, port int, log *slog.Logger) error {
	if listen != "" {
		return eng.Start(ctx, listen)
	}
	var last error
	for p := port; p < port+40; p += 2 {
		if err := eng.Start(ctx, ":"+strconv.Itoa(p)); err != nil {
			last = err
			continue
		}
		if p != port {
			log.Warn("SIP port in use by another program; using another", "wanted", port, "using", p)
		}
		return nil
	}
	return fmt.Errorf("no free SIP port near %d: %w", port, last)
}

func listenFirstFree(addr string, tries int) (net.Listener, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, _ := strconv.Atoi(portStr)
	var last error
	for i := 0; i < tries; i++ {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port+i)))
		if err == nil {
			return ln, nil
		}
		last = err
		if port == 0 {
			break
		}
	}
	return nil, last
}

func lanAddresses() []string {
	var out []string
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLinkLocalUnicast() {
				out = append(out, ipn.IP.String())
			}
		}
	}
	return out
}

func setupCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%06d", n.Int64())
}

// hasDisplay reports whether a browser could be shown: always on macOS and Windows, and on Linux only in a
// desktop session (a server over SSH has none).
func hasDisplay() bool {
	if runtime.GOOS != "linux" && runtime.GOOS != "freebsd" {
		return true
	}
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}

// pruneRecordings deletes recordings past the retention setting, at start and then hourly.
func pruneRecordings(ctx context.Context, st *store.Store, log *slog.Logger) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		cfg, _ := st.Config()
		if n := st.PruneRecordings(cfg.System.Recording.KeepDays); n > 0 {
			log.Info("deleted old call recordings", "count", n, "keepDays", cfg.System.Recording.KeepDays)
		}
		if n := st.PruneCDRs(cfg.System.Retention.CallRecordDays); n > 0 {
			log.Info("deleted old call records", "count", n, "keepDays", cfg.System.Retention.CallRecordDays)
		}
		if n := st.PruneVoicemail(cfg.System.Retention.VoicemailDays); n > 0 {
			log.Info("deleted old voicemail", "count", n, "keepDays", cfg.System.Retention.VoicemailDays)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// iaxTrunks are the enabled IAX2 trunks of a configuration, as the IAX2 gateway needs them.
func iaxTrunks(c config.Config) []iax.Trunk {
	var out []iax.Trunk
	for _, t := range c.Trunks {
		if t.Enabled && t.Protocol == "iax2" {
			out = append(out, iax.Trunk{ID: t.ID, Name: t.Name, Host: t.Host, Port: t.Port, Username: t.Username,
				Secret: t.Password, Register: t.Register, Formats: t.Codecs})
		}
	}
	return out
}

func h323Trunks(c config.Config) []h323.Trunk {
	var out []h323.Trunk
	for _, t := range c.Trunks {
		if t.Enabled && t.Protocol == "h323" {
			out = append(out, h323.Trunk{ID: t.ID, Name: t.Name, Host: t.Host, Port: t.Port, Codecs: t.Codecs})
		}
	}
	return out
}

// loopbackTrunks is the IAX2 and H.323 gateways, as the engine's one link to trunks it reaches
// through loopback SIP.
type loopbackTrunks struct {
	iax  *iax.Service
	h323 *h323.Service
}

func (l loopbackTrunks) SIPPort(id string) int {
	if p := l.h323.SIPPort(id); p != 0 {
		return p
	}
	return l.iax.SIPPort(id)
}

func (l loopbackTrunks) Token() string { return l.iax.Token() }

func (l loopbackTrunks) Status(id string) string {
	if l.h323.SIPPort(id) != 0 {
		return l.h323.Status(id)
	}
	return l.iax.Status(id)
}

// portAddr is a port on the address SIP was given (every address by default).
func portAddr(sipListen, port string) string {
	host, _, err := net.SplitHostPort(sipListen)
	if err != nil {
		host = ""
	}
	return net.JoinHostPort(host, port)
}

// iaxAddr is where IAX2 listens: port 4569 on the address SIP was given (every address by default).
func iaxAddr(sipListen string) string {
	host, _, err := net.SplitHostPort(sipListen)
	if err != nil {
		host = ""
	}
	return net.JoinHostPort(host, "4569")
}
