// Package pbx is BitVoice's call engine: the registrar, the back-to-back user agent that connects calls,
// routing to extensions, ring groups and trunks, transfers, the echo test, emergency notification and
// call records.
//
// Every call is two or more legs joined at BitVoice, never a proxied dialog. That is what lets a call be
// recorded, measured, transferred or have a leg replaced without the other party noticing, and what keeps
// one misbehaving phone from confusing another.
package pbx

import (
	"context"
	"crypto/tls"
	"errors"
	"github.com/biodream-llc/bitvoice/internal/i3"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/screen"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Recorder receives finished call records.
type Recorder interface {
	AddCDR(store.CDR) error
}

// Engine runs the phone system.
type Engine struct {
	loadToken atomic.Value // string: see SetLoadToken
	linkToken atomic.Value // string: see SetLinkToken
	// Mobile, when set, wakes the BitVoice apps of extensions it has devices for.
	Mobile Mobile
	// SetEmergencyMode, set by the API server, switches emergency mode from a phone (the feature code).
	SetEmergencyMode func(on bool, by string) error
	late             lateRings
	presence         presences
	Log              *slog.Logger
	Bus              *events.Bus
	Recorder         Recorder
	// TLS, when set, also serves SIP over TLS on the port after the SIP port (5061 next to 5060).
	TLS *tls.Config
	// TLSClient is used when connecting out over TLS: it carries the certificate to present.
	TLSClient *tls.Config
	// Cluster links this server with the others sharing its database; nil when it runs alone.
	Cluster ClusterLink
	// HandOver, when set, gives this server's connected calls to another server as it stops, and
	// returns those given; they are not hung up.
	HandOver func() map[string][]byte
	// CertPin is the SHA-256 (hex) of the certificate this server presents on TLS, for the others.
	CertPin func() string
	// HoldsAddress says whether this server holds the shared address now.
	HoldsAddress func() bool
	// OnFax takes each fax received (or failed), to keep and send on.
	OnFax func(FaxIn)
	// OnEmergencyAnswer hears each 911 call when a call-taker answers it (for CAD and the i3 log).
	OnEmergencyAnswer func(EmergencyAnswer)
	// FindService is the ESRP's ECRF.
	FindService FindService

	bcf bcfState

	radioOnce sync.Once
	radio     *radio
	// Mail stores voicemail; nil switches voicemail off.
	Mail Mailbox
	// Dialer keeps the outbound dialer's contacts; nil switches the dialer off.
	Dialer DialerStore
	// Music stores hold music tracks; nil plays the hold tone everywhere.
	Music MusicLibrary
	// WakeUps keeps the wake-up calls; nil switches them off.
	WakeUps WakeUpBook
	// SaveGuests keeps the hotel's checked-in guests across restarts.
	SaveGuests func([]Guest) error
	// PMSEvent, when set, tells the hotel system what happened (wake-up set and result, room status).
	PMSEvent func(kind string, data map[string]string)
	guests   guests
	sign     signers
	// WebBase is this server's https:// web address (no trailing slash), for the certificate a
	// trunk's STIR/SHAKEN signatures point to; nil or empty when not known.
	WebBase func() string
	// SeenDevice, when set, is told the User-Agent of each phone that registers (the compatibility list).
	SeenDevice func(userAgent, ext string)
	// Remote gives remote workers' confirmed addresses, for emergency calls.
	Remote RemoteAddresses
	// Callback, when set, stores a callback an AI receptionist booked (and tells the staff).
	Callback func(store.Callback) error
	// NameFor names an outside caller from the company directory, for phones' displays; "" if unknown.
	NameFor func(number string) string
	// SaveHotDesks keeps hot desk sign-ins across restarts.
	SaveHotDesks func([]HotDesk) error
	// SaveDiverts keeps call forwarding and hand-set do not disturb across restarts.
	SaveDiverts func(DivertState) error
	// LastAgent is the extension that last answered this caller since a time, for sticky queues; "" if none.
	LastAgent func(number string, since time.Time) string
	// Brief makes a briefing for the person a call is passed to (an AI summary), when briefings are on.
	Brief func(BriefRequest)

	checker       atomic.Pointer[screen.Checker]
	checkerRoots  string
	away          aways
	lastEmergency atomic.Int64 // unix seconds of the last emergency call, so a call back is never blocked

	hot      hotDesks
	div      diverts
	tog      toggles
	dial     dialers
	streamMu sync.Mutex
	streams  map[string]*musicStream
	lookups  lookupCache
	conf     conferences
	// Recordings stores call recordings; nil switches recording off.
	Recordings Recordings
	// Trace sees every SIP message (the SIP trace screen).
	Trace func(dir string, p sip.Peer, m *sip.Message)
	// Dispatch, when set before Start, receives every new request instead of this engine (a Switch).
	Dispatch func(tx *sip.ServerTx, in *sip.Incoming)
	// UserPrefix is a hosted tenant's prefix on SIP user names (acme.), for phones without the
	// tenant's own domain: acme.100 is extension 100.
	UserPrefix string
	// iaxLink joins IAX2 trunks to the engine through loopback SIP (SetIAX); unset when there are none.
	iaxLink atomic.Pointer[IAXLink]
	// AIAllow, when set, is asked before each AI request the engine makes (a tenant's daily allowance).
	AIAllow func() error
	// MaxCalls caps this engine's calls at once (a tenant's allowance); emergency calls always go through.
	MaxCalls atomic.Int32
	// Suspended lets only emergency calls (and calls back after one) through.
	Suspended atomic.Bool
	shared    bool // the SIP stack and media ports belong to another engine
	// T1 and T2 override the SIP timers (tests use short ones); zero keeps RFC 3261's 500 ms and 4 s.
	T1, T2 time.Duration

	stack *sip.Stack
	auth  *sip.Authenticator
	pool  *media.Pool
	cfg   atomic.Pointer[config.Config]
	reg   *Registrar
	bans  *FailBan

	mu      sync.Mutex
	calls   map[string]*Call
	legs    map[string]*Leg  // by Call-ID and our tag, for in-dialog requests
	pending map[string]*Call // by INVITE branch, for CANCEL
	subs    map[string]*subscription
	queues  map[string]*queueState
	parked  map[string]*Call // park slot -> call
	// presenceMu keeps busy-lamp updates in order.
	presenceMu sync.Mutex
	trunks     map[string]*trunkState
	started    time.Time
	ctx        context.Context
	cancel     context.CancelFunc
}

// New creates an engine with a configuration. Call Start to listen.
func New(cfg config.Config, bus *events.Bus, rec Recorder, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	e := &Engine{Log: log, Bus: bus, Recorder: rec, calls: map[string]*Call{}, legs: map[string]*Leg{}, subs: map[string]*subscription{}, queues: map[string]*queueState{}, parked: map[string]*Call{},
		pending: map[string]*Call{}, trunks: map[string]*trunkState{}}
	e.cfg.Store(&cfg)
	e.auth = sip.NewAuthenticator("bitvoice")
	e.reg = newRegistrar(e)
	e.bans = newFailBan()
	e.bans.apply(cfg.Security)
	applyG723(cfg)
	return e
}

// Config returns the configuration in force.
func (e *Engine) Config() *config.Config { return e.cfg.Load() }

// Start opens the SIP and media ports and begins serving.
func (e *Engine) Start(ctx context.Context, listen string) error {
	cfg := e.Config()
	e.ctx, e.cancel = context.WithCancel(ctx)
	e.pool = &media.Pool{Min: cfg.System.RTPMin, Max: cfg.System.RTPMax}
	h := e.handle
	if e.Dispatch != nil {
		h = e.Dispatch // one SIP port for several hosted tenants: the switch picks the engine
	}
	e.stack = sip.NewStack(h)
	e.stack.TLSClient = e.TLSClient
	e.stack.Log = e.Log
	e.stack.Host = cfg.System.PublicAddress
	e.stack.Trace = e.Trace
	if e.T1 > 0 {
		e.stack.T1, e.stack.T2 = e.T1, e.T2
	}
	if listen == "" {
		listen = ":" + strconv.Itoa(cfg.System.SIPPort)
	}
	// With a shared address the main sockets share their port with the shared address's.
	e.stack.SharePort = e.Cluster != nil && cfg.System.Cluster.SharedAddress != ""
	if err := e.stack.Listen(listen); err != nil {
		e.cancel()
		return err
	}
	if e.Cluster != nil {
		if pr, err := netip.ParsePrefix(cfg.System.Cluster.SharedAddress); err == nil {
			// Answer on the shared address too, from before this server holds it.
			if err := e.stack.ListenExtra(pr.Addr()); err != nil {
				e.Log.Warn("cannot listen on the shared address", "address", pr.Addr().String(), "err", err)
			}
		}
	}
	if e.TLS != nil {
		host, _, _ := net.SplitHostPort(listen)
		addr := net.JoinHostPort(host, strconv.Itoa(e.stack.Port()+1))
		if err := e.stack.ListenTLS(addr, e.TLS); err != nil {
			e.Log.Warn("SIP over TLS is off: its port is in use", "addr", addr, "err", err)
		}
	}
	e.stack.Serve(e.ctx)
	e.started = time.Now()
	go e.reg.run(e.ctx)
	go e.runTrunks(e.ctx)
	go e.watchdog(e.ctx)
	go e.wakeUpLoop(e.ctx)
	go e.dialerLoop(e.ctx)
	rcfg := e.Config()
	e.applyRadio(rcfg)
	return nil
}

// Stop hangs up every call and closes the ports.
func (e *Engine) Stop() {
	defer e.stopRadio()
	handed := map[string]bool{}
	if e.HandOver != nil {
		// Servers sharing the database carry on this server's connected calls: none is hung up.
		for id := range e.HandOver() {
			handed[id] = true
		}
	}
	for _, c := range e.Calls() {
		if handed[c.ID] {
			continue
		}
		c.call.hangup("system shutdown", "system")
	}
	if e.cancel != nil {
		e.cancel()
	}
	if e.stack != nil && !e.shared {
		e.stack.Close()
	}
}

// SIPPort returns the port SIP listens on.
// TLSPort returns the SIP over TLS port, 0 when TLS is off.
func (e *Engine) TLSPort() int {
	if e.stack == nil {
		return 0
	}
	return e.stack.TLSPort()
}

// PhoneAddresses lists the addresses phones can reach SIP on: the bound one, or all of local's.
func (e *Engine) PhoneAddresses(local []string) []string {
	if e.stack != nil {
		if ip := e.stack.BindIP(); ip != nil {
			return []string{ip.String()}
		}
	}
	return local
}

// LocalSIP is where a program on this computer reaches BitVoice's SIP port: the bound address, or
// 127.0.0.1 when listening on every address.
func (e *Engine) LocalSIP() netip.AddrPort {
	ip := netip.MustParseAddr("127.0.0.1")
	if e.stack != nil {
		if b := e.stack.BindIP(); b != nil {
			if a, ok := netip.AddrFromSlice(b); ok {
				ip = a.Unmap()
			}
		}
	}
	return netip.AddrPortFrom(ip, uint16(e.SIPPort()))
}

func (e *Engine) SIPPort() int {
	if e.stack == nil {
		return 0
	}
	return e.stack.Port()
}

// Apply puts a new configuration into force. Calls in progress continue under the configuration they
// started with; new calls use the new one.
// applyG723 switches G.723.1 conversion on or off as the settings say (ffmpeg must be there).
func applyG723(cfg config.Config) {
	if ff := cfg.System.FFmpeg; cfg.System.G723Convert && media.FFmpegHas(ff) {
		if ff == "" {
			ff = "ffmpeg"
		}
		media.SetG723Converter(ff)
	} else {
		media.SetG723Converter("")
	}
}

func (e *Engine) Apply(cfg config.Config) {
	e.cfg.Store(&cfg)
	e.bans.apply(cfg.Security)
	applyG723(cfg)
	if e.stack != nil {
		e.stack.Host = cfg.System.PublicAddress
	}
	e.reg.prune(cfg)
	e.pruneHotDesks()
	e.pruneDiverts()
	e.applyScreening(cfg)
	e.syncTrunks(cfg)
	if e.ctx != nil {
		e.applyRadio(&cfg)
	}
	e.Bus.Publish("config.applied", map[string]any{})
}

func (e *Engine) publish(typ string, data any) {
	if e.Bus != nil {
		e.Bus.Publish(typ, data)
	}
	if strings.HasPrefix(typ, "call.") {
		go e.presenceChanged()
	}
	if e.Cluster != nil && (strings.HasPrefix(typ, "call.") || typ == "registrations.changed" && data != nil && !emptyMap(data)) {
		e.Cluster.Poke() // the other servers hear of it at once
	}
}

// clientIP is where a request really comes from: its source, or, for one passed on by a gateway on
// this computer (the browser phone, SIP over WebSocket), the address the gateway says. Bans and rate
// limits then count each softphone, never this computer.
func clientIP(in *sip.Incoming) netip.Addr {
	ip := in.From.Addr.Addr().Unmap()
	if ip.IsLoopback() {
		if a, err := netip.ParseAddr(in.Msg.Get("X-BitVoice-Client")); err == nil {
			return a.Unmap()
		}
	}
	return ip
}

// handle receives every new request from the stack.
func (e *Engine) handle(tx *sip.ServerTx, in *sip.Incoming) {
	m := in.Msg
	ip := clientIP(in)
	if e.bans.banned(ip) {
		return // silently: a scanner gets nothing to learn from
	}
	if !e.bans.allow(ip) {
		if tx != nil {
			tx.Respond(sip.NewResponse(m, 503, "Rate Limited"))
		}
		return
	}
	to, _ := sip.ParseAddress(m.Get("To"))
	if to != nil && to.Tag() != "" {
		e.inDialog(tx, in, to.Tag())
		return
	}
	if tx == nil {
		return // an ACK outside a dialog
	}
	switch m.Method {
	case sip.REGISTER:
		e.reg.handle(tx, in)
	case sip.INVITE:
		e.newCall(tx, in)
	case sip.CANCEL:
		e.cancelPending(tx, in)
	case sip.OPTIONS:
		r := sip.NewResponse(m, 200, "")
		r.Set("Allow", allowed)
		r.Set("Accept", "application/sdp")
		r.Set("To", withTag(m.Get("To")))
		tx.Respond(r)
	case sip.SUBSCRIBE:
		e.subscribe(tx, in)
	case sip.MESSAGE:
		if m.Get(nodeHeader) != "" && e.Cluster != nil {
			if !e.clusterMessage(m) {
				tx.Respond(sip.NewResponse(m, 403, "Not A Cluster Member"))
				return
			}
		}
		tx.Respond(sip.NewResponse(m, 200, ""))
	case sip.NOTIFY:
		tx.Respond(sip.NewResponse(m, 200, ""))
	default:
		r := sip.NewResponse(m, 405, "")
		r.Set("Allow", allowed)
		tx.Respond(r)
	}
}

const allowed = "INVITE, ACK, BYE, CANCEL, OPTIONS, INFO, REFER, NOTIFY, UPDATE, MESSAGE, SUBSCRIBE"

func withTag(to string) string {
	a, err := sip.ParseAddress(to)
	if err != nil {
		return to
	}
	if a.Tag() == "" {
		a.Params.Set("tag", sip.NewTag())
	}
	return a.String()
}

func legKey(callID, tag string) string { return callID + "|" + tag }

func (e *Engine) inDialog(tx *sip.ServerTx, in *sip.Incoming, tag string) {
	m := in.Msg
	e.mu.Lock()
	leg := e.legs[legKey(m.CallID(), tag)]
	sub := e.subs[legKey(m.CallID(), tag)]
	e.mu.Unlock()
	if leg == nil && sub != nil {
		e.subscriptionRequest(tx, in, sub)
		return
	}
	if leg == nil {
		if tx != nil {
			tx.Respond(sip.NewResponse(m, 481, ""))
		}
		return
	}
	leg.receive(tx, in)
}

func (e *Engine) addLeg(l *Leg) {
	e.mu.Lock()
	e.legs[legKey(l.callID, l.localTag)] = l
	e.mu.Unlock()
}

func (e *Engine) removeLeg(l *Leg) {
	e.mu.Lock()
	if e.legs[legKey(l.callID, l.localTag)] == l {
		delete(e.legs, legKey(l.callID, l.localTag))
	}
	e.mu.Unlock()
}

func (e *Engine) cancelPending(tx *sip.ServerTx, in *sip.Incoming) {
	inv := e.stack.FindInvite(in.Msg)
	var c *Call
	if inv != nil {
		e.mu.Lock()
		c = e.pending[inv.Branch()]
		e.mu.Unlock()
	}
	if c == nil {
		tx.Respond(sip.NewResponse(in.Msg, 481, ""))
		return
	}
	tx.Respond(sip.NewResponse(in.Msg, 200, ""))
	c.callerCancelled()
}

// localContact is BitVoice's Contact for messages to a peer.
func (e *Engine) localContact(p sip.Peer, user string) string {
	if user == "" {
		user = "bitvoice"
	}
	if p.Transport == "TLS" {
		if t := e.teamsTrunkFor(p); t != nil && t.SBCName != "" {
			return e.teamsContact(t, user)
		}
	}
	u := &sip.URI{Scheme: "sip", User: user, Host: e.stack.LocalHost(p), Port: e.stack.LocalPort(p.Transport)}
	if p.Transport != "UDP" && p.Transport != "" {
		u.Params.Set("transport", strings.ToLower(p.Transport))
	}
	return "<" + u.String() + ">"
}

// mediaHost is the address put in SDP for media to and from a peer.
func (e *Engine) mediaHost(p sip.Peer) string {
	return e.stack.LocalHost(p)
}

// watchdog ends calls that have gone silent: a phone that lost power or network never sends BYE, and a
// call left up holds a trunk channel and runs up a bill.
func (e *Engine) watchdog(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, c := range e.allCalls() {
			c.checkMedia()
		}
	}
}

func (e *Engine) allCalls() []*Call {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Call, 0, len(e.calls))
	for _, c := range e.calls {
		out = append(out, c)
	}
	return out
}

// CallInfo is a snapshot of a call for the interface.
type CallInfo struct {
	ID        string `json:"id"`
	State     string `json:"state"` // ringing, answered, held
	Direction string `json:"direction"`
	From      string `json:"from"`
	FromName  string `json:"fromName"`
	To        string `json:"to"`
	ToName    string `json:"toName"`
	Trunk     string `json:"trunk,omitempty"`
	// Screen is what spam screening found about an incoming call.
	Screen *screen.Verdict `json:"screen,omitempty"`
	Start  time.Time       `json:"start"`
	Answer time.Time       `json:"answer,omitzero"`
	MOS    float64         `json:"mos"`
	Legs   []LegInfo       `json:"legs"`
	// Door is the door station's extension when the call is with one (so the screen offers Open door).
	Door string `json:"door,omitempty"`
	// Recording is "recording" or "paused" while the call is recorded.
	Recording string `json:"recording,omitempty"`
	// Supervision is listen, whisper or barge while a supervisor is on the call.
	Supervision string `json:"supervision,omitempty"`
	// Emergency is what a 911 call brought: the caller's location and Additional Data.
	Emergency *Emergency `json:"emergency,omitempty"`
	call      *Call
}

// LegInfo describes one side of a call.
type LegInfo struct {
	Party   string      `json:"party"`
	Peer    string      `json:"peer"`
	Codec   string      `json:"codec"`
	Media   media.Stats `json:"media"`
	OnHold  bool        `json:"onHold"`
	Ringing bool        `json:"ringing"`
	// Transport is how signalling reaches the party (UDP, TCP, TLS); Secure is true when media is SRTP.
	Transport string `json:"transport"`
	Secure    bool   `json:"secure"`
	// Ext or Trunk says which side this is (for quality by site and by provider).
	Ext   string `json:"ext,omitempty"`
	Trunk string `json:"trunk,omitempty"`
	// Video is true while the party's video passes; Fax while it sends a fax over T.38.
	Video bool `json:"video,omitempty"`
	Fax   bool `json:"fax,omitempty"`
}

// Calls returns every call in progress.
func (e *Engine) Calls() []CallInfo {
	calls := e.allCalls()
	out := make([]CallInfo, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.info())
	}
	return out
}

// Hangup ends a call from the interface.
func (e *Engine) Hangup(id, by string) bool {
	e.mu.Lock()
	c := e.calls[id]
	e.mu.Unlock()
	if c == nil {
		return false
	}
	c.hangup("ended by "+by, "admin")
	return true
}

// Registrations returns the current registrations.
func (e *Engine) Registrations() []Binding { return e.reg.list() }

// Banned returns the addresses currently blocked by fail-to-ban.
func (e *Engine) Banned() []BanInfo { return e.bans.list() }

// IsBanned says whether an address is banned now.
func (e *Engine) IsBanned(ip netip.Addr) bool { return e.bans.banned(ip.Unmap()) }

// Unban lifts a ban.
func (e *Engine) Unban(ip string) {
	if a, err := netip.ParseAddr(ip); err == nil {
		e.bans.unban(a)
	}
}

// Stats is a summary for the dashboard.
type Stats struct {
	Uptime        int64 `json:"uptimeSeconds"`
	Calls         int   `json:"calls"`
	Registrations int   `json:"registrations"`
	Extensions    int   `json:"extensions"`
	Online        int   `json:"online"` // extensions with at least one registration
	Trunks        int   `json:"trunks"`
	TrunksUp      int   `json:"trunksUp"`
	Banned        int   `json:"banned"`
	SIPPort       int   `json:"sipPort"`
}

// Stats returns live counts.
func (e *Engine) Stats() Stats {
	cfg := e.Config()
	regs := e.reg.list()
	online := map[string]bool{}
	for _, r := range regs {
		online[r.Extension] = true
	}
	ts := e.TrunkStatus()
	up := 0
	for _, t := range ts {
		if t.State == "registered" || t.State == "reachable" {
			up++
		}
	}
	return Stats{Uptime: int64(time.Since(e.started).Seconds()), Calls: len(e.allCalls()), Registrations: len(regs),
		Extensions: len(cfg.Extensions), Online: len(online), Trunks: len(cfg.Trunks), TrunksUp: up,
		Banned: len(e.bans.list()), SIPPort: e.SIPPort()}
}

// userOf extracts the user part of a URI string, or of a To/From header value.
func userOf(s string) string {
	if a, err := sip.ParseAddress(s); err == nil && a.URI != nil {
		return a.URI.User
	}
	if u, err := sip.ParseURI(s); err == nil {
		return u.User
	}
	return ""
}

// cleanNumber strips visual separators people and phones put in numbers.
func cleanNumber(n string) string {
	// A SIP user part may carry parameters after the number (+12125550100;verstat=TN-Validation-Passed).
	n, _, _ = strings.Cut(n, ";")
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '(', ')', '.':
			return -1
		}
		return r
	}, n)
}

// NewMediaEndpoint allocates a media endpoint from the pool (for the browser phone).
func (e *Engine) NewMediaEndpoint() (*media.Endpoint, error) {
	if e.pool == nil {
		return nil, errors.New("not started")
	}
	return e.pool.Allocate()
}

// Emergency is a 911 call's data for the call-taker.
type Emergency struct {
	Location    *i3.Location  `json:"location,omitempty"`
	LocationURI string        `json:"locationUri,omitempty"` // by reference: rebid fetches it again
	Rebid       time.Time     `json:"rebid,omitzero"`        // when the location was last fetched
	Additional  i3.Additional `json:"additional"`
	Service     string        `json:"service,omitempty"`
	Problems    []string      `json:"problems,omitempty"`
	ESInet      bool          `json:"esinet"`            // came from the emergency services network
	TakenBy     string        `json:"takenBy,omitempty"` // the call-taker's extension, once one answers
	TakenName   string        `json:"takenName,omitempty"`
	TakenAt     time.Time     `json:"takenAt,omitzero"`
	// PIDF is the location document as it came, for CAD and transfers (not sent to screens).
	PIDF []byte `json:"-"`
}
