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
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"
	_ "time/tzdata" // time zones for opening hours, whatever the host has installed

	"github.com/biodream-llc/bitvoice/internal/api"
	"github.com/biodream-llc/bitvoice/internal/certs"
	"github.com/biodream-llc/bitvoice/internal/cluster"
	"github.com/biodream-llc/bitvoice/internal/compat"
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
	"github.com/biodream-llc/bitvoice/internal/upgrade"
	"github.com/biodream-llc/bitvoice/internal/web"
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
	fs.StringVar(&o.local, "http", "127.0.0.1:8280", "plain HTTP address for the browser on this computer")
	fs.StringVar(&o.secure, "https", ":8443", "HTTPS address for the web interface from other computers")
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
	eng := pbx.New(cfg, bus, st, log)
	eng.Mail = st
	eng.Recordings = st
	eng.SeenDevice = st.NoteDevice
	eng.Remote = st
	eng.Trace = trace.Add
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
	ami := &compat.AMI{PBX: eng, Bus: bus, Log: log, Audit: srv.AuditSystem}
	ami.Apply(cfg.System.Compat)
	st.OnChange(func(c config.Config) { ami.Apply(c.System.Compat) })
	defer ami.Close()
	srv.AMIAddr = ami.Addr
	eng.Callback = srv.BookCallback
	eng.NameFor = srv.NameFor
	eng.LastAgent = srv.LastAgent
	eng.Brief = srv.Brief
	var desks []pbx.HotDesk
	if err := st.LoadState("hotdesk", &desks); err != nil {
		log.Warn("could not read hot desk sign-ins", "err", err)
	}
	eng.LoadHotDesks(desks)
	eng.SaveHotDesks = func(h []pbx.HotDesk) error { return st.SaveState("hotdesk", h) }
	srv.ARI = &compat.ARI{PBX: eng, Bus: bus, Audit: srv.AuditSystem, Started: time.Now()}
	eng.Mobile = srv // wakes the BitVoice apps for incoming calls
	// The engine runs on the configuration with vault references resolved.
	eng.Apply(srv.Resolved(cfg))
	st.OnChange(func(c config.Config) { eng.Apply(srv.Resolved(c)) })
	eng.SetEmergencyMode = srv.SetEmergencyMode
	if web.Available() {
		h, err := web.Handler()
		if err != nil {
			return err
		}
		srv.Web = h
	}
	handler := srv.Handler()
	go srv.RunAI(ctx)
	go srv.RunCRM(ctx)
	go srv.RunMetrics(ctx)
	go srv.RunCosts(ctx)
	go srv.RunCalendars(ctx)
	eng.HoldsAddress = srv.HoldsSharedAddress
	eng.OnFax = srv.FaxReceived
	eng.OnEmergencyAnswer = srv.EmergencyAnswered
	eng.FindService = srv.FindService
	go srv.RunSharedAddress(ctx)
	go srv.RunArchive(ctx)
	go srv.RunEmail(ctx)
	go srv.RunFaxes(ctx)
	go srv.RunFaxMail(ctx)
	go srv.RunPSAPLog(ctx)
	go srv.RunAudit(ctx)
	go srv.RunRadioCAD(ctx)
	go srv.RunGitOps(ctx)
	go srv.RunEmergencyEmail(ctx)

	localLn, err := listenFirstFree(o.local, 20)
	if err != nil {
		return fmt.Errorf("web interface: %w", err)
	}
	localURL := "http://" + localLn.Addr().String()
	httpLocal := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = httpLocal.Serve(localLn) }()

	var secureURLs []string
	secureLabel := "From your network: "
	if certErr != nil {
		log.Warn("no HTTPS: could not create a certificate", "err", certErr)
	} else if ln, err := listenFirstFree(o.secure, 20); err != nil {
		log.Warn("no HTTPS: port unavailable", "addr", o.secure, "err", err)
	} else {
		httpsSrv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second,
			TLSConfig: &tls.Config{GetCertificate: holder.Get, MinVersion: tls.VersionTLS12}}
		go func() { _ = httpsSrv.ServeTLS(ln, "", "") }()
		defer httpsSrv.Close()
		host, port, _ := net.SplitHostPort(ln.Addr().String())
		if n, err := strconv.Atoi(port); err == nil {
			srv.HTTPSPort.Store(int32(n))
		}
		// Listening on one address (-https 10.0.0.5:8443) is reachable only there; list every address
		// only when listening on all of them.
		reach := addresses
		if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
			reach = []string{host}
			if ip.IsLoopback() {
				secureLabel = "Also, with HTTPS:  "
			}
		}
		for _, a := range reach {
			secureURLs = append(secureURLs, "https://"+net.JoinHostPort(a, port))
		}
	}
	defer httpLocal.Close()

	fmt.Fprintf(stdout, "BitVoice %s is running.\n", version)
	fmt.Fprintf(stdout, "  On this computer:   %s\n", localURL)
	for _, u := range secureURLs {
		fmt.Fprintf(stdout, "  %s %s\n", secureLabel, u)
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
	_ = httpLocal.Shutdown(shut)
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
