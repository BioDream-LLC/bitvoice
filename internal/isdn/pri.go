package isdn

import (
	"errors"
	"strings"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// Q.931 timers.
var (
	T303 = 4 * time.Second  // SETUP sent, nothing back
	T305 = 30 * time.Second // DISCONNECT sent, no RELEASE
	T308 = 4 * time.Second  // RELEASE sent, no RELEASE COMPLETE
	T310 = 30 * time.Second // CALL PROCEEDING heard, nothing more (ten seconds for some switches: kept generous)
	// T302 is how long the network side waits for the next digit of a number dialled digit by digit
	// (overlap sending, as ISDN phones dial). Q.931 allows 10 to 15 s; phones on a PBX want less.
	T302 = 4 * time.Second
)

// Config.Channels for a basic rate interface.
var BRIChannels = []int{1, 2}

// Config describes one PRI or BRI.
type Config struct {
	Network  bool  // BitVoice is the network side (another PBX connects to it); else the user side
	Channels []int // the B-channels in use, numbered as Q.931 numbers them (1-23 on T1; 1-15 and 17-31 on E1; 1 and 2 on a BRI)
	// BRI is a basic rate interface (one-octet call references, B1 and B2). PTMP makes it point to
	// multipoint: several terminals on the line, each with a TEI the network assigns, calls to them
	// offered to all at once (a European "multi-device" line, or ISDN phones on BitVoice's network
	// side); else point to point (TEI 0, a PBX line).
	BRI  bool
	PTMP bool
	ALaw     bool  // E1: G.711 A-law; T1: µ-law
	Log      *slog.Logger
	// Switch is the switch type: ni2 (National ISDN-2, the default), 5ess, dms100, euroisdn or qsig.
	// It decides how names and transfers are sent.
	Switch string
	// RequestCharge (EuroISDN, user side) asks the network for advice of charge during and at the end
	// of each outgoing call. OfferCharge (network side) agrees to such requests; the charges are given
	// with Call.SendCharge and Call.SetFinalCharge.
	RequestCharge bool
	OfferCharge   bool
	// Trace, when set, is given every Q.931 message, in (true) or out.
	Trace func(in bool, msg []byte)
}

// Event is what happens to a call.
type Event struct {
	Kind   string // proceeding, alerting, progress, connected, held, retrieved, transfer-asked, charge, disconnected, released
	Cause  int
	Charge *Charge // for charge: the advice of charge the network gave
}

// Call states (Q.931 table 3-1, the ones used).
const (
	stNull          = 0
	stCallInit      = 1
	stOutProceeding = 3
	stDelivered     = 4
	stCallPresent   = 6
	stReceived      = 7
	stConnectReq    = 8
	stInProceeding  = 9
	stActive        = 10
	stDisconnectReq = 11
	stDisconnectInd = 12
	stReleaseReq    = 19
	stOverlapRecv   = 25
)

// Call is one call on the PRI.
type Call struct {
	p        *PRI
	ref      int
	ours     bool // we allocated the call reference (an outgoing call)
	Channel  int
	Called   string
	Calling  string
	Outgoing bool
	// Name is the caller's name (an incoming call's, as the network sent it). Redirecting is the number
	// an incoming call was forwarded from, with RedirectReason. Connected is the number that answered
	// an outgoing call, when the network says.
	Name           string
	Redirecting    string
	RedirectReason int
	Connected      string
	// Charge is the last advice of charge the network gave (EuroISDN); ChargeAsked, on the network
	// side, what the caller asked to be told (AskDuring, AskAtTheEnd).
	Charge      *Charge
	ChargeAsked int
	Events      chan Event

	finalCharge     *Charge
	rltID           int        // DMS-100: the switch's identifier for the call, for transfers
	afterProceeding []*Message // replies to SETUP's facilities, sent after CALL PROCEEDING

	state   int
	timer   *time.Timer
	retried bool
	setup   *Message // the SETUP, for T303's one resend
	done    bool

	// tei is the terminal the call is with on the network side of a multipoint BRI; a call offered
	// to every terminal is teiBroadcast until one answers, responders the terminals that replied.
	tei        int
	responders map[int]bool
	declined   int // the cause a terminal declined with

	held       bool
	waitFor    byte // the acknowledgement a HOLD or RETRIEVE waits for
	waitDone   chan error
	invokeWait bool
	invokeID   int
	invokeDone chan error
}

type callKey struct {
	ref  int
	ours bool
	tei  int // the network side of a multipoint BRI: each terminal numbers its own calls
}

// PRI is call control on one D-channel.
type PRI struct {
	cfg Config
	dl  *datalink

	// Incoming is told of each call arriving (in its own goroutine), after CALL PROCEEDING went back.
	Incoming func(*Call)
	// Up is told when the D-channel comes up or goes down.
	Up func(bool)
	// OnTransfer, on the network side, is asked when the user side asks for two of its calls to be
	// joined (Two B-Channel Transfer, or ETSI explicit call transfer on a held call). True accepts:
	// the result goes back, and the caller then joins the two and clears both. Unset refuses, and
	// HOLD is refused too.
	OnTransfer func(a, b *Call) bool

	mu    sync.Mutex
	calls map[callKey]*Call
	busy  map[int]*Call
	next  int
	isUp  bool

	invokes int
	rltNext int
}

// New makes call control over a link; Run starts it.
func New(link Link, cfg Config) *PRI {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	p := &PRI{cfg: cfg, calls: map[callKey]*Call{}, busy: map[int]*Call{}, next: 1}
	if cfg.BRI && len(cfg.Channels) == 0 {
		p.cfg.Channels = BRIChannels
	}
	p.dl = newDatalink(link, cfg.Network, cfg.BRI && cfg.PTMP, p.receive, p.linkUp)
	return p
}

// Run brings the D-channel up and serves it until the link fails.
func (p *PRI) Run() error {
	p.dl.start()
	return p.dl.run()
}

// Close stops the timers.
func (p *PRI) Close() { p.dl.close() }

// multipointNet says whether this is the network side of a multipoint BRI.
func (p *PRI) multipointNet() bool { return p.cfg.BRI && p.cfg.PTMP && p.cfg.Network }

func (p *PRI) chanID(ch int, exclusive bool) []byte {
	if p.cfg.BRI {
		return briChannelID(ch, exclusive)
	}
	return channelID(ch, exclusive)
}

// IsUp says whether the D-channel is up.
func (p *PRI) IsUp() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.isUp
}

// Busy lists the B-channels in calls.
func (p *PRI) Busy() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []int
	for ch := range p.busy {
		out = append(out, ch)
	}
	slices.Sort(out)
	return out
}

func (p *PRI) linkUp(up bool) {
	p.mu.Lock()
	p.isUp = up
	var gone []*Call
	if !up {
		for _, c := range p.calls {
			gone = append(gone, c)
		}
	}
	p.mu.Unlock()
	for _, c := range gone {
		c.free(CauseTemporaryFailure)
	}
	if p.Up != nil {
		p.Up(up)
	}
}

func (p *PRI) send(m *Message) error {
	m.Short = p.cfg.BRI
	b := m.Bytes()
	if p.cfg.Trace != nil {
		p.cfg.Trace(false, b)
	}
	if m.broadcast {
		return p.dl.Broadcast(b)
	}
	return p.dl.Send(m.tei, b)
}

func (c *Call) msg(t byte) *Message {
	return &Message{CallRef: c.ref, FromSide: !c.ours, Type: t, tei: c.tei}
}

func (c *Call) event(e Event) {
	select {
	case c.Events <- e:
	default:
	}
}

// arm runs f after d unless the state changes first. Called with p.mu held.
func (c *Call) arm(d time.Duration, f func()) {
	if c.timer != nil {
		c.timer.Stop()
	}
	st := c.state
	c.timer = time.AfterFunc(d, func() {
		c.p.mu.Lock()
		same := c.state == st && !c.done
		c.p.mu.Unlock()
		if same {
			f()
		}
	})
}

// free forgets the call and its channel, and says it is released.
func (c *Call) free(cause int) {
	p := c.p
	p.mu.Lock()
	if c.done {
		p.mu.Unlock()
		return
	}
	c.done = true
	c.state = stNull
	if c.timer != nil {
		c.timer.Stop()
	}
	delete(p.calls, c.key())
	if p.busy[c.Channel] == c {
		delete(p.busy, c.Channel)
	}
	p.mu.Unlock()
	c.event(Event{Kind: "released", Cause: cause})
}

// freeChannel is the lowest B-channel not in a call. Called with p.mu held.
func (p *PRI) freeChannel() int {
	for _, ch := range p.cfg.Channels {
		if p.busy[ch] == nil {
			return ch
		}
	}
	return 0
}

// ErrNoChannel is returned when every B-channel is in use.
var ErrNoChannel = errors.New("isdn: every B-channel is busy")

// Dial places a call: SETUP on a free B-channel, with the numbers.
func (p *PRI) Dial(called, calling string) (*Call, error) {
	return p.DialWith(Setup{Called: called, Calling: calling})
}

// DialWith places a call with a name and a redirecting number too.
func (p *PRI) DialWith(s Setup) (*Call, error) {
	called, calling := s.Called, s.Calling
	p.mu.Lock()
	if !p.isUp {
		p.mu.Unlock()
		return nil, ErrDown
	}
	ch := p.freeChannel()
	if ch == 0 {
		p.mu.Unlock()
		return nil, ErrNoChannel
	}
	max := 0x7FFF
	if p.cfg.BRI {
		max = 0x7F
	}
	ref := p.next
	for p.calls[callKey{ref, true, 0}] != nil {
		ref = ref%max + 1
	}
	p.next = ref%max + 1
	name := clipName(s.Name)
	c := &Call{p: p, ref: ref, ours: true, Channel: ch, Called: called, Calling: calling, Outgoing: true, Name: name,
		Events: make(chan Event, 16), state: stCallInit}
	if p.multipointNet() {
		c.tei, c.responders = teiBroadcast, map[int]bool{}
	}
	p.calls[c.key()] = c
	p.busy[ch] = c
	m := c.msg(MsgSetup)
	m.broadcast = c.tei == teiBroadcast
	m.add(ieBearer, bearer(p.cfg.ALaw))
	m.add(ieChannelID, p.chanID(ch, true))
	if p.dms() && !p.cfg.Network {
		m.add(ieFacility, rltAbility())
	}
	if p.euro() && p.cfg.RequestCharge && !p.cfg.Network {
		m.add(ieFacility, chargingRequest(p.invokeID(), chargeDuring))
		m.add(ieFacility, chargingRequest(p.invokeID(), chargeAtTheEnd))
	}
	if name != "" {
		if p.nameFac() {
			m.add(ieFacility, nameFacility(p.invokeID(), name))
		}
		if p.euro() {
			m.add(ieDisplay, []byte(name))
		} else {
			m.add(ieDisplay, append([]byte{0xB1}, name...)) // with the character set, as North American switches want
		}
	}
	if calling != "" {
		m.add(ieCallingNumber, numberIE(calling, true))
	}
	m.add(ieCalledNumber, numberIE(called, false))
	if s.Redirecting != "" {
		m.add(ieRedirectingNum, redirectingIE(s.Redirecting, s.RedirectReason))
	}
	m.IEs = append([]IE{{ID: ieSendingComplete}}, m.IEs...) // a single-octet element: first, as libpri sends it
	c.setup = m
	c.arm(T303, c.onT303)
	p.mu.Unlock()
	if err := p.send(m); err != nil {
		c.free(CauseTemporaryFailure)
		return nil, err
	}
	return c, nil
}

func (c *Call) key() callKey {
	if c.ours {
		return callKey{c.ref, true, 0}
	}
	return callKey{c.ref, false, c.tei}
}

func (c *Call) onT303() {
	c.p.mu.Lock()
	if c.responders != nil && len(c.responders) == 0 && c.declined != 0 {
		cause := c.declined
		c.p.mu.Unlock()
		c.free(cause) // every terminal that answered declined
		return
	}
	if c.retried {
		c.p.mu.Unlock()
		c.free(CauseNoCircuit)
		return
	}
	c.retried = true
	c.arm(T303, c.onT303)
	setup := c.setup
	c.p.mu.Unlock()
	_ = c.p.send(setup)
}

// PRI says which D-channel the call is on.
func (c *Call) PRI() *PRI { return c.p }

// Proceeding, Alerting and Answer move an incoming call on.
func (c *Call) Alerting() error {
	c.p.mu.Lock()
	if c.state != stInProceeding {
		c.p.mu.Unlock()
		return nil
	}
	c.state = stReceived
	c.p.mu.Unlock()
	m := c.msg(MsgAlerting)
	m.add(ieProgress, []byte{0x82, 0x88}) // in-band information (ringback) is available
	return c.p.send(m)
}

// Answer connects an incoming call.
func (c *Call) Answer() error {
	c.p.mu.Lock()
	if c.state != stInProceeding && c.state != stReceived {
		c.p.mu.Unlock()
		return errors.New("isdn: the call cannot be answered now")
	}
	c.state = stConnectReq
	c.p.mu.Unlock()
	m := c.msg(MsgConnect)
	if c.Called != "" {
		m.add(ieConnectedNum, numberIE(c.Called, true)) // the number that answered
	}
	return c.p.send(m)
}

// Hangup clears the call with a cause: DISCONNECT, then RELEASE and RELEASE COMPLETE.
func (c *Call) Hangup(cause int) {
	p := c.p
	p.mu.Lock()
	if c.tei == teiBroadcast && c.ours {
		// Offered to every terminal and taken by none yet: release the ones that replied.
		var to []int
		for t := range c.responders {
			to = append(to, t)
		}
		p.mu.Unlock()
		for _, t := range to {
			r := c.msg(MsgRelease)
			r.tei = t
			r.add(ieCause, causeIE(cause, p.cfg.Network))
			_ = p.send(r)
		}
		c.free(cause)
		return
	}
	st := c.state
	if c.done || st == stDisconnectReq || st == stReleaseReq {
		p.mu.Unlock()
		return
	}
	if st == stDisconnectInd {
		// They disconnected first: we release.
		c.state = stReleaseReq
		c.arm(T308, func() { c.free(CauseRecoveryTimer) })
		p.mu.Unlock()
		m := c.msg(MsgRelease)
		c.addFinalCharge(m)
		_ = p.send(m)
		return
	}
	c.state = stDisconnectReq
	c.arm(T305, func() {
		p.mu.Lock()
		c.state = stReleaseReq
		c.arm(T308, func() { c.free(CauseRecoveryTimer) })
		p.mu.Unlock()
		r := c.msg(MsgRelease)
		r.add(ieCause, causeIE(cause, p.cfg.Network))
		_ = p.send(r)
	})
	p.mu.Unlock()
	m := c.msg(MsgDisconnect)
	m.add(ieCause, causeIE(cause, p.cfg.Network))
	c.addFinalCharge(m)
	_ = p.send(m)
}

// receive handles one Q.931 message from the D-channel, from a terminal's TEI (127: broadcast).
func (p *PRI) receive(tei int, b []byte) {
	if p.cfg.Trace != nil {
		p.cfg.Trace(true, b)
	}
	m, err := Parse(b)
	if err != nil {
		return
	}
	m.tei = tei
	if m.CallRef == 0 {
		if tei != teiBroadcast {
			p.global(m)
		}
		return
	}
	key := callKey{m.CallRef, m.FromSide, 0}
	if p.multipointNet() && !m.FromSide {
		key.tei = tei
	}
	p.mu.Lock()
	c := p.calls[key]
	p.mu.Unlock()
	if c == nil {
		switch m.Type {
		case MsgSetup:
			if !m.FromSide {
				p.setup(m)
			}
		case MsgReleaseComplete:
		case MsgRelease:
			p.reply(m, MsgReleaseComplete, CauseNoCallReference)
		case MsgStatus:
		default:
			p.reply(m, MsgReleaseComplete, CauseNoCallReference)
		}
		return
	}
	c.message(m)
}

// reply answers a message with no call behind it.
func (p *PRI) reply(to *Message, t byte, cause int) {
	if to.tei == teiBroadcast {
		return // a broadcast SETUP refused by staying silent: another terminal may take it
	}
	r := &Message{CallRef: to.CallRef, FromSide: !to.FromSide, Type: t, tei: to.tei}
	r.add(ieCause, causeIE(cause, p.cfg.Network))
	_ = p.send(r)
}

// global handles the dummy call reference: RESTART.
func (p *PRI) global(m *Message) {
	if m.Type != MsgRestart {
		return
	}
	class := byte(0x87)
	if d, ok := m.Get(ieRestartInd); ok && len(d) > 0 {
		class = d[0]
	}
	var gone []*Call
	p.mu.Lock()
	if class&7 == 0 { // the channels named
		if d, ok := m.Get(ieChannelID); ok {
			if ch, _, ok := parseChannel(d); ok && ch != 0 {
				if c := p.busy[ch]; c != nil {
					gone = append(gone, c)
				}
			}
		}
	} else {
		for _, c := range p.calls {
			gone = append(gone, c)
		}
	}
	p.mu.Unlock()
	for _, c := range gone {
		c.free(CauseTemporaryFailure)
	}
	r := &Message{CallRef: 0, FromSide: true, Type: MsgRestartAck, tei: m.tei}
	for _, ie := range m.IEs {
		if ie.ID == ieChannelID || ie.ID == ieRestartInd {
			r.IEs = append(r.IEs, ie)
		}
	}
	_ = p.send(r)
}

// setup takes a call arriving: a B-channel, CALL PROCEEDING, then Incoming.
func (p *PRI) setup(m *Message) {
	ch, exclusive := 0, false
	if d, ok := m.Get(ieChannelID); ok {
		ch, exclusive, _ = parseChannel(d)
	}
	p.mu.Lock()
	usable := slices.Contains(p.cfg.Channels, ch) && p.busy[ch] == nil
	if !usable {
		if exclusive && ch != 0 {
			p.mu.Unlock()
			p.reply(m, MsgReleaseComplete, CauseChannelUnavailable)
			return
		}
		ch = p.freeChannel()
	}
	if ch == 0 {
		p.mu.Unlock()
		p.reply(m, MsgReleaseComplete, CauseNoCircuit)
		return
	}
	c := &Call{p: p, ref: m.CallRef, ours: false, Channel: ch, Events: make(chan Event, 16), state: stCallPresent}
	if p.multipointNet() {
		c.tei = m.tei
	}
	if d, ok := m.Get(ieCalledNumber); ok {
		c.Called = parseNumber(d)
	}
	if d, ok := m.Get(ieCallingNumber); ok {
		c.Calling = parseNumber(d)
	}
	if d, ok := m.Get(ieFacility); ok {
		c.Name = facilityName(d)
	}
	if d, ok := m.Get(ieDisplay); ok && c.Name == "" {
		c.Name = clipName(displayText(d))
	}
	if d, ok := m.Get(ieRedirectingNum); ok {
		c.Redirecting, c.RedirectReason = parseRedirecting(d)
	}
	p.calls[c.key()] = c
	p.busy[ch] = c
	p.mu.Unlock()
	// What SETUP's facilities ask (a DMS-100 transfer identifier, advice of charge) is answered after
	// CALL PROCEEDING.
	for _, d := range m.GetAll(ieFacility) {
		c.facility(d, true)
	}
	// The network side takes a number dialled digit by digit (ISDN phones, and PBXes set to overlap
	// dialling): SETUP ACKNOWLEDGE, then INFORMATION messages until the number is complete.
	_, complete := m.Get(ieSendingComplete)
	if p.cfg.Network && !complete && (p.cfg.BRI || c.Called == "") {
		p.mu.Lock()
		c.state = stOverlapRecv
		later := c.afterProceeding
		c.afterProceeding = nil
		c.arm(T302, c.overlapDone)
		p.mu.Unlock()
		r := c.msg(MsgSetupAck)
		r.add(ieChannelID, p.chanID(ch, true))
		_ = p.send(r)
		for _, x := range later {
			_ = p.send(x)
		}
		return
	}
	c.proceed()
}

// proceed sends CALL PROCEEDING (and the replies SETUP asked for) and hands the call on.
func (c *Call) proceed() {
	p := c.p
	p.mu.Lock()
	c.state = stInProceeding
	later := c.afterProceeding
	c.afterProceeding = nil
	p.mu.Unlock()
	r := c.msg(MsgCallProceeding)
	r.add(ieChannelID, p.chanID(c.Channel, true))
	_ = p.send(r)
	for _, x := range later {
		_ = p.send(x)
	}
	if p.Incoming != nil {
		go p.Incoming(c)
	} else {
		c.Hangup(CauseRejected)
	}
}

// overlapDone ends a number dialled digit by digit (T302 ran out, or it was marked complete).
func (c *Call) overlapDone() {
	c.p.mu.Lock()
	ok := c.state == stOverlapRecv
	if c.timer != nil {
		c.timer.Stop()
	}
	c.p.mu.Unlock()
	if ok {
		c.proceed()
	}
}

// information takes digits dialled one by one: from the called number or keypad elements; a
// sending-complete element or a closing # ends the number.
func (c *Call) information(m *Message) {
	p := c.p
	p.mu.Lock()
	if c.state != stOverlapRecv {
		p.mu.Unlock()
		return
	}
	if d, ok := m.Get(ieCalledNumber); ok {
		c.Called += parseNumber(d)
	}
	if d, ok := m.Get(ieKeypad); ok {
		c.Called += string(d)
	}
	_, done := m.Get(ieSendingComplete)
	if strings.HasSuffix(c.Called, "#") {
		c.Called, done = strings.TrimSuffix(c.Called, "#"), true
	}
	if !done {
		c.arm(T302, c.overlapDone)
	}
	p.mu.Unlock()
	if done {
		c.overlapDone()
	}
}

const ieKeypad = 0x2C

// message handles a message for a call that exists.
func (c *Call) message(m *Message) {
	p := c.p
	cause := CauseNormal
	if d, ok := m.Get(ieCause); ok {
		cause = parseCause(d)
	}
	if c.multipoint(m) {
		return
	}
	if c.supplementary(m) {
		return
	}
	switch m.Type {
	case MsgInformation:
		c.information(m)
	case MsgCallProceeding, MsgSetupAck:
		p.mu.Lock()
		if c.state == stCallInit {
			c.state = stOutProceeding
			if d, ok := m.Get(ieChannelID); ok {
				if ch, _, ok := parseChannel(d); ok && ch != 0 && ch != c.Channel && p.busy[ch] == nil {
					delete(p.busy, c.Channel)
					c.Channel = ch
					p.busy[ch] = c
				}
			}
			c.arm(T310, func() { c.Hangup(CauseRecoveryTimer) })
		}
		p.mu.Unlock()
		c.event(Event{Kind: "proceeding"})
	case MsgAlerting:
		p.mu.Lock()
		if c.state == stCallInit || c.state == stOutProceeding {
			c.state = stDelivered
			if c.timer != nil {
				c.timer.Stop()
			}
		}
		p.mu.Unlock()
		c.event(Event{Kind: "alerting"})
	case MsgProgress:
		c.event(Event{Kind: "progress"})
	case MsgConnect:
		p.mu.Lock()
		ok := c.Outgoing && c.state != stActive && c.state < stDisconnectReq
		if d, has := m.Get(ieConnectedNum); has {
			c.Connected = parseNumber(d)
		}
		if ok {
			c.state = stActive
			if c.timer != nil {
				c.timer.Stop()
			}
		}
		p.mu.Unlock()
		if ok {
			_ = p.send(c.msg(MsgConnectAck))
			c.event(Event{Kind: "connected"})
		}
	case MsgConnectAck:
		p.mu.Lock()
		ok := c.state == stConnectReq
		if ok {
			c.state = stActive
		}
		p.mu.Unlock()
		if ok {
			c.event(Event{Kind: "connected"})
		}
	case MsgDisconnect:
		p.mu.Lock()
		st := c.state
		if st == stDisconnectReq {
			// Both hung up at once: release.
			c.state = stReleaseReq
			c.arm(T308, func() { c.free(CauseRecoveryTimer) })
			p.mu.Unlock()
			_ = p.send(c.msg(MsgRelease))
			return
		}
		c.state = stDisconnectInd
		p.mu.Unlock()
		c.event(Event{Kind: "disconnected", Cause: cause})
		c.Hangup(cause) // RELEASE
	case MsgRelease:
		_ = p.send(c.msg(MsgReleaseComplete))
		c.free(cause)
	case MsgReleaseComplete:
		c.free(cause)
	case MsgStatusEnquiry:
		p.mu.Lock()
		st := c.state
		p.mu.Unlock()
		r := c.msg(MsgStatus)
		r.add(ieCause, causeIE(CauseStatusEnquiry, p.cfg.Network))
		r.add(ieCallState, []byte{byte(st)})
		_ = p.send(r)
	case MsgStatus:
		if d, ok := m.Get(ieCallState); ok && len(d) > 0 && d[0]&0x3F == stNull {
			c.free(cause) // the other side has no such call any more
		}
	}
}
