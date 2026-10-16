package ss7

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// ISUP message types (Q.763 table 4; T1.113).
const (
	msgIAM  = 0x01
	msgSAM  = 0x02
	msgACM  = 0x06
	msgCON  = 0x07
	msgANM  = 0x09
	msgREL  = 0x0C
	msgRLC  = 0x10
	msgRSC  = 0x12
	msgBLO  = 0x13
	msgUBL  = 0x14
	msgBLA  = 0x15
	msgUBA  = 0x16
	msgGRS  = 0x17
	msgCGB  = 0x18
	msgCGU  = 0x19
	msgCGBA = 0x1A
	msgCGUA = 0x1B
	msgGRA  = 0x29
	msgCPG  = 0x2C
	msgUCIC = 0x2E
)

// Optional parameters.
const (
	parEnd          = 0x00
	parCallingNum   = 0x0A
	parRedirecting  = 0x0B
	parRedirectInfo = 0x13
	parConnectedNum = 0x21
	parGenericName  = 0xC7 // ANSI
)

// Q.850 causes.
const (
	CauseUnallocated = 1
	CauseNormal      = 16
	CauseBusy        = 17
	CauseNoAnswer    = 19
	CauseRejected    = 21
	CauseNoCircuit   = 34
	CauseTemporary   = 41
	CauseCongestion  = 42
	CauseRecovery    = 102
)

// ISUP timers (Q.764 annex A).
var (
	T1ISUP  = 15 * time.Second // REL sent, waiting for RLC
	T5ISUP  = 5 * time.Minute  // REL sent: give up and reset the circuit
	T7ISUP  = 25 * time.Second // IAM sent, waiting for ACM
	T9ISUP  = 90 * time.Second // ACM heard, waiting for ANM
	T16ISUP = 15 * time.Second // RSC sent, waiting for RLC
	T22ISUP = 15 * time.Second // GRS sent, waiting for GRA
)

// Event is what happens to a call.
type Event struct {
	Kind  string // proceeding (ACM), alerting (ACM with subscriber free, or CPG alerting), progress, connected, released
	Cause int
}

// Call is one call on one circuit.
type Call struct {
	s        *SS7
	CIC      int
	Called   string
	Calling  string
	Name     string // the caller's name (ANSI generic name)
	Outgoing bool
	// Redirecting is the number a call in was forwarded from.
	Redirecting string
	Restricted  bool // the caller's number is not to be shown
	Events      chan Event

	state   int
	timer   *time.Timer
	t5      *time.Timer
	done    bool
	relSent bool
}

const (
	csIdle = iota
	csOutgoingIAM
	csOutgoingACM
	csIncomingIAM
	csIncomingACM
	csAnswered
	csReleasing
)

// Config is the link set and its circuits.
type Config struct {
	MTPConfig
	// CICs are the voice circuits on this link set, by circuit identification code. Calls out take the
	// lowest free circuit this end controls on dual seizure first (Q.764 2.10.1.4: the end with the
	// higher point code controls the even ones), the others when those are all busy.
	CICs []int
}

// SS7 is one signalling link with ISUP over it.
type SS7 struct {
	cfg Config
	mtp *mtp3

	// Incoming is told of each call arriving (in its own goroutine).
	Incoming func(*Call)
	// Up is told when the link set comes up (MTP3 and the circuits reset) or goes down.
	Up func(bool)

	mu      sync.Mutex
	calls   map[int]*Call
	blocked map[int]bool // circuits the far end blocked
	isUp    bool         // MTP is up and the circuit reset was acknowledged: calls may go
	mtpOK   bool         // MTP is up
	waitGRA int          // circuit groups whose reset (GRS) is not acknowledged yet
	grs     *time.Timer
}

// New makes an SS7 link set over a signalling link; Run starts it.
func New(link Link, cfg Config) *SS7 {
	s := &SS7{cfg: cfg, calls: map[int]*Call{}, blocked: map[int]bool{}}
	sort.Ints(s.cfg.CICs)
	s.mtp = newMTP3(link, cfg.MTPConfig, s.receive, s.mtpUp)
	return s
}

// Run brings the link up and serves it until the signalling link fails.
func (s *SS7) Run() error {
	s.mtp.l2.start()
	return s.mtp.l2.run()
}

// Close stops it.
func (s *SS7) Close() { s.mtp.l2.close() }

// IsUp says whether calls can be made.
func (s *SS7) IsUp() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isUp
}

// Busy lists the circuits in calls.
func (s *SS7) Busy() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int
	for cic := range s.calls {
		out = append(out, cic)
	}
	sort.Ints(out)
	return out
}

func (s *SS7) mtpUp(up bool) {
	if !up {
		s.mu.Lock()
		s.isUp, s.mtpOK, s.waitGRA = false, false, 0
		if s.grs != nil {
			s.grs.Stop()
			s.grs = nil
		}
		var gone []*Call
		for _, c := range s.calls {
			gone = append(gone, c)
		}
		s.mu.Unlock()
		for _, c := range gone {
			c.free(CauseTemporary)
		}
		if s.Up != nil {
			s.Up(false)
		}
		return
	}
	// MTP up: reset every circuit (GRS in groups of up to 32); calls may go once the far end has
	// acknowledged them all (GRA), so a call is never cleared by a reset still on its way.
	s.mu.Lock()
	s.mtpOK = true
	s.mu.Unlock()
	s.resetCircuits()
}

// resetCircuits sends a GRS for every group of circuits and, until each is acknowledged, again every
// T22 (Q.764 2.10.3.1).
func (s *SS7) resetCircuits() {
	gs := groups(s.cfg.CICs)
	s.mu.Lock()
	if !s.mtpOK {
		s.mu.Unlock()
		return
	}
	s.waitGRA = len(gs)
	if s.grs != nil {
		s.grs.Stop()
	}
	s.grs = time.AfterFunc(T22ISUP, s.resetCircuits)
	s.mu.Unlock()
	if len(gs) == 0 {
		s.circuitsReset()
	}
	for _, g := range gs {
		_ = s.sendISUP(g[0], msgGRS, nil, [][]byte{{byte(g[1] - g[0])}}, nil)
	}
}

// circuitsReset: every group acknowledged, calls may go.
func (s *SS7) circuitsReset() {
	s.mu.Lock()
	was := s.isUp
	s.isUp = s.mtpOK
	if s.grs != nil {
		s.grs.Stop()
		s.grs = nil
	}
	now := s.isUp && !was
	s.mu.Unlock()
	if now && s.Up != nil {
		s.Up(true)
	}
}

// controls says whether this end controls a circuit on dual seizure.
func (s *SS7) controls(cic int) bool { return (s.cfg.OPC > s.cfg.APC) == (cic%2 == 0) }

// groups splits sorted CICs into ranges of consecutive circuits, at most 32 each.
func groups(cics []int) [][2]int {
	var out [][2]int
	for i := 0; i < len(cics); {
		j := i
		for j+1 < len(cics) && cics[j+1] == cics[j]+1 && cics[j+1]-cics[i] < 32 {
			j++
		}
		out = append(out, [2]int{cics[i], cics[j]})
		i = j + 1
	}
	return out
}

// sendISUP builds and sends a message: CIC, type, fixed parameters, pointers and variable ones, and
// optional ones (nil: the message has no optional part).
func (s *SS7) sendISUP(cic, typ int, fixed []byte, variable [][]byte, optional [][2][]byte) error {
	b := []byte{byte(cic), byte(cic >> 8 & 0x0F), byte(typ)}
	if s.cfg.ANSI {
		b[1] = byte(cic >> 8 & 0x3F)
	}
	b = append(b, fixed...)
	nptr := len(variable)
	hasOpt := optional != nil
	if hasOpt {
		nptr++
	}
	ptrs := make([]byte, nptr)
	var body []byte
	for i, v := range variable {
		ptrs[i] = byte(nptr - i + len(body))
		body = append(body, byte(len(v)))
		body = append(body, v...)
	}
	if hasOpt {
		if len(optional) == 0 {
			ptrs[nptr-1] = 0
		} else {
			ptrs[nptr-1] = byte(1 + len(body))
			for _, o := range optional {
				body = append(body, o[0][0], byte(len(o[1])))
				body = append(body, o[1]...)
			}
			body = append(body, parEnd)
		}
	}
	b = append(append(b, ptrs...), body...)
	sls := cic & 0xF // ITU: the CIC's low bits keep a call's messages on one link
	if s.cfg.ANSI {
		sls = cic & 0x1F
	}
	return s.mtp.send(siISUP, s.mtp.label(s.cfg.APC, sls), b)
}

// isupMsg is a received message.
type isupMsg struct {
	cic, typ int
	fixed    []byte
	variable [][]byte
	optional map[byte][]byte
}

// layout gives each message type's fixed part length and number of variable parameters, and
// whether it has an optional part.
func (s *SS7) layout(typ int) (fixed, vars int, opt bool, ok bool) {
	switch typ {
	case msgIAM:
		if s.cfg.ANSI {
			return 4, 2, true, true // NCI, FCI, CPC; user service information, called number
		}
		return 5, 1, true, true // NCI, FCI, CPC, TMR; called number
	case msgSAM:
		return 0, 1, true, true
	case msgACM:
		return 2, 0, true, true
	case msgCON:
		return 2, 0, true, true
	case msgANM, msgRLC:
		return 0, 0, true, true
	case msgREL:
		return 0, 1, true, true
	case msgCPG:
		return 1, 0, true, true
	case msgRSC, msgBLO, msgUBL, msgBLA, msgUBA, msgUCIC:
		return 0, 0, false, true
	case msgGRS:
		return 0, 1, false, true
	case msgGRA:
		return 0, 1, false, true
	case msgCGB, msgCGU, msgCGBA, msgCGUA:
		return 1, 1, false, true
	}
	return 0, 0, false, false
}

func (s *SS7) parse(b []byte) (*isupMsg, bool) {
	if len(b) < 3 {
		return nil, false
	}
	m := &isupMsg{cic: int(b[0]) | int(b[1]&0x0F)<<8, typ: int(b[2]), optional: map[byte][]byte{}}
	if s.cfg.ANSI {
		m.cic = int(b[0]) | int(b[1]&0x3F)<<8
	}
	fixed, vars, opt, ok := s.layout(m.typ)
	if !ok {
		return m, true // a type we do not handle: the caller decides
	}
	p := 3
	if len(b) < p+fixed {
		return nil, false
	}
	m.fixed = b[p : p+fixed]
	p += fixed
	nptr := vars
	if opt && len(b) > p+vars {
		nptr++
	} else {
		opt = false // no optional part sent (ANSI's RLC, for one)
	}
	if len(b) < p+nptr {
		return nil, false
	}
	for i := 0; i < vars; i++ {
		at := p + i + int(b[p+i])
		if at >= len(b) || at+1+int(b[at]) > len(b) {
			return nil, false
		}
		m.variable = append(m.variable, b[at+1:at+1+int(b[at])])
	}
	if opt && b[p+vars] != 0 {
		at := p + vars + int(b[p+vars])
		for at < len(b) && b[at] != parEnd {
			if at+1 >= len(b) || at+2+int(b[at+1]) > len(b) {
				break
			}
			m.optional[b[at]] = b[at+2 : at+2+int(b[at+1])]
			at += 2 + int(b[at+1])
		}
	}
	return m, true
}

// Numbers (Q.763 3.9, 3.10): odd/even and nature of address, the plan, then BCD digits.
func numberParam(digits string, calling, restricted bool) []byte {
	nai := byte(3) // national (significant) number
	d := strings.TrimPrefix(digits, "+")
	if strings.HasPrefix(digits, "+") {
		nai = 4 // international
	}
	if len(d) < 7 {
		nai = 3
	}
	octet2 := byte(0x10) // ISDN (E.164) numbering plan, routing to internal network number allowed
	if calling {
		octet2 = 0x10 | 0x01 // E.164, user provided, verified and passed
		if restricted {
			octet2 |= 0x04 // presentation restricted
		}
	}
	b := []byte{nai, octet2}
	if len(d)%2 == 1 {
		b[0] |= 0x80
	}
	for i := 0; i < len(d); i += 2 {
		lo := bcd(d[i])
		hi := byte(0)
		if i+1 < len(d) {
			hi = bcd(d[i+1])
		}
		b = append(b, lo|hi<<4)
	}
	return b
}

func bcd(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c == '*':
		return 0xB
	case c == '#':
		return 0xC
	}
	return 0
}

func parseNumber(b []byte) (digits string, restricted bool) {
	if len(b) < 2 {
		return "", false
	}
	odd := b[0]&0x80 != 0
	nai := b[0] & 0x7F
	restricted = (b[1]>>2)&3 == 1
	var sb strings.Builder
	for i, v := range b[2:] {
		for k, n := range []byte{v & 0xF, v >> 4} {
			if k == 1 && odd && i == len(b[2:])-1 {
				break
			}
			switch {
			case n <= 9:
				sb.WriteByte('0' + n)
			case n == 0xB:
				sb.WriteByte('*')
			case n == 0xC:
				sb.WriteByte('#')
			case n == 0xF:
				// ST: end of pulsing
			}
		}
	}
	d := sb.String()
	if nai == 4 && d != "" {
		d = "+" + d
	}
	return d, restricted
}

func causeParam(cause int) []byte { return []byte{0x80 | 0x02, 0x80 | byte(cause&0x7F)} } // ITU standard, public network serving the local user

func parseCause(b []byte) int {
	if len(b) < 2 {
		return CauseNormal
	}
	i := 1
	if b[0]&0x80 == 0 && len(b) > 2 {
		i = 2 // a recommendation octet
	}
	return int(b[i] & 0x7F)
}

// ErrNoCircuit is returned when every circuit is in use or blocked.
var ErrNoCircuit = errors.New("ss7: every circuit is busy")

// Setup is what a call out carries.
type Setup struct {
	Called, Calling string
	Name            string
	Restricted      bool
	Redirecting     string
}

// Dial places a call on a free circuit: IAM with the numbers.
func (s *SS7) Dial(su Setup) (*Call, error) {
	s.mu.Lock()
	if !s.isUp {
		s.mu.Unlock()
		return nil, ErrDown
	}
	cic := -1
	for pass := 0; pass < 2 && cic < 0; pass++ {
		for _, c := range s.cfg.CICs {
			if s.calls[c] != nil || s.blocked[c] {
				continue
			}
			if pass == 0 && !s.controls(c) {
				continue // ours first: a dual seizure on them is ours to keep
			}
			cic = c
			break
		}
	}
	if cic < 0 {
		s.mu.Unlock()
		return nil, ErrNoCircuit
	}
	c := &Call{s: s, CIC: cic, Called: su.Called, Calling: su.Calling, Name: su.Name, Outgoing: true,
		Restricted: su.Restricted, Events: make(chan Event, 16), state: csOutgoingIAM}
	s.calls[cic] = c
	c.timer = time.AfterFunc(T7ISUP, func() { c.release(CauseRecovery) })
	s.mu.Unlock()
	fixed := []byte{0x00, 0x60, 0x01, 0x0A} // NCI: no satellite, no continuity check; FCI: national, ISUP all the way, ISUP preferred; CPC: ordinary
	var variable [][]byte
	if s.cfg.ANSI {
		// FCI as T1.113 (national call, ISUP all the way); CPC; then user service information: speech, 64 kbit/s, µ-law.
		fixed = []byte{0x00, 0x60, 0x01, 0x0A}
		variable = [][]byte{{0x80, 0x90, 0xA2}, numberParam(su.Called, false, false)}
	} else {
		fixed = append(fixed, 0x00) // TMR: speech
		variable = [][]byte{numberParam(su.Called, false, false)}
	}
	opt := [][2][]byte{}
	if su.Calling != "" {
		opt = append(opt, [2][]byte{{parCallingNum}, numberParam(su.Calling, true, su.Restricted)})
	}
	if su.Redirecting != "" {
		opt = append(opt, [2][]byte{{parRedirecting}, numberParam(su.Redirecting, true, false)})
	}
	if s.cfg.ANSI && su.Name != "" && !su.Restricted {
		opt = append(opt, [2][]byte{{parGenericName}, append([]byte{0x01}, clip(su.Name, 15)...)}) // calling name, presentation allowed
	}
	if err := s.sendISUP(cic, msgIAM, fixed, variable, opt); err != nil {
		c.free(CauseTemporary)
		return nil, err
	}
	return c, nil
}

func clip(s string, n int) []byte {
	b := []byte(s)
	if len(b) > n {
		b = b[:n]
	}
	return b
}

func (c *Call) event(e Event) {
	select {
	case c.Events <- e:
	default:
	}
}

func (c *Call) stopTimer() {
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
}

// free forgets the call. Says released.
func (c *Call) free(cause int) {
	s := c.s
	s.mu.Lock()
	if c.done {
		s.mu.Unlock()
		return
	}
	c.done = true
	c.stopTimer()
	if c.t5 != nil {
		c.t5.Stop()
	}
	if s.calls[c.CIC] == c {
		delete(s.calls, c.CIC)
	}
	s.mu.Unlock()
	c.event(Event{Kind: "released", Cause: cause})
}

// Alerting tells the caller the called party is being alerted (ACM, subscriber free).
func (c *Call) Alerting() error {
	s := c.s
	s.mu.Lock()
	if c.state != csIncomingIAM {
		s.mu.Unlock()
		return nil
	}
	c.state = csIncomingACM
	s.mu.Unlock()
	// BCI: charge, subscriber free, ordinary subscriber, end to end; ISUP all the way.
	return s.sendISUP(c.CIC, msgACM, []byte{0x16, 0x14}, nil, [][2][]byte{})
}

// Answer answers a call in: ANM (or CON when no ACM was sent).
func (c *Call) Answer() error {
	s := c.s
	s.mu.Lock()
	st := c.state
	if st != csIncomingIAM && st != csIncomingACM {
		s.mu.Unlock()
		return errors.New("ss7: the call cannot be answered now")
	}
	c.state = csAnswered
	s.mu.Unlock()
	if st == csIncomingIAM {
		return s.sendISUP(c.CIC, msgCON, []byte{0x16, 0x14}, nil, [][2][]byte{})
	}
	return s.sendISUP(c.CIC, msgANM, nil, nil, [][2][]byte{})
}

// Hangup releases the call with a cause: REL, then RLC.
func (c *Call) Hangup(cause int) { c.release(cause) }

func (c *Call) release(cause int) {
	s := c.s
	s.mu.Lock()
	if c.done || c.relSent {
		s.mu.Unlock()
		return
	}
	c.relSent = true
	c.state = csReleasing
	c.stopTimer()
	cic := c.CIC
	c.timer = time.AfterFunc(T1ISUP, func() {
		// No RLC: REL again (Q.764 2.10.8.1), and after T5 reset the circuit.
		_ = s.sendISUP(cic, msgREL, nil, [][]byte{causeParam(cause)}, [][2][]byte{})
	})
	c.t5 = time.AfterFunc(T5ISUP, func() {
		_ = s.sendISUP(cic, msgRSC, nil, nil, nil)
		c.free(CauseRecovery)
	})
	s.mu.Unlock()
	_ = s.sendISUP(cic, msgREL, nil, [][]byte{causeParam(cause)}, [][2][]byte{})
}

// receive handles an ISUP message.
func (s *SS7) receive(si int, l Label, data []byte) {
	if si != siISUP || l.OPC != s.cfg.APC {
		return
	}
	m, ok := s.parse(data)
	if !ok {
		return
	}
	s.mu.Lock()
	c := s.calls[m.cic]
	known := false
	for _, x := range s.cfg.CICs {
		if x == m.cic {
			known = true
		}
	}
	s.mu.Unlock()
	if !known && m.typ != msgGRS && m.typ != msgGRA && m.typ != msgUCIC {
		_ = s.sendISUP(m.cic, msgUCIC, nil, nil, nil) // not one of our circuits
		return
	}
	switch m.typ {
	case msgIAM:
		if c != nil {
			if c.Outgoing && c.state == csOutgoingIAM {
				// Dual seizure (Q.764 2.10.1.4): the end with the higher point code controls the even
				// circuits; the other backs off and its call goes again on another circuit.
				if s.controls(m.cic) {
					return // ours wins: their IAM is ignored, they will back off
				}
				c.free(CauseTemporary)
			} else {
				return
			}
		}
		s.incoming(m)
	case msgSAM:
		// overlap sending is not used: numbers go en bloc
	case msgACM, msgCPG, msgCON, msgANM:
		if c == nil || !c.Outgoing {
			return
		}
		s.mu.Lock()
		c.stopTimer()
		switch m.typ {
		case msgACM:
			c.state = csOutgoingACM
			c.timer = time.AfterFunc(T9ISUP, func() { c.release(CauseNoAnswer) })
		case msgCON, msgANM:
			c.state = csAnswered
		}
		s.mu.Unlock()
		switch m.typ {
		case msgACM:
			kind := "proceeding"
			if len(m.fixed) >= 1 && m.fixed[0]>>2&3 == 1 { // called party's status: subscriber free
				kind = "alerting"
			}
			c.event(Event{Kind: kind})
		case msgCPG:
			kind := "progress"
			if len(m.fixed) >= 1 && m.fixed[0]&0x7F == 1 {
				kind = "alerting"
			}
			c.event(Event{Kind: kind})
		default:
			c.event(Event{Kind: "connected"})
		}
	case msgREL:
		cause := CauseNormal
		if len(m.variable) > 0 {
			cause = parseCause(m.variable[0])
		}
		_ = s.sendISUP(m.cic, msgRLC, nil, nil, [][2][]byte{})
		if c != nil {
			c.free(cause)
		}
	case msgRLC:
		if c != nil {
			c.free(CauseNormal)
		}
	case msgRSC:
		if c != nil {
			c.free(CauseTemporary)
		}
		s.mu.Lock()
		delete(s.blocked, m.cic)
		s.mu.Unlock()
		_ = s.sendISUP(m.cic, msgRLC, nil, nil, [][2][]byte{})
	case msgGRS:
		if len(m.variable) < 1 || len(m.variable[0]) < 1 {
			return
		}
		rng := int(m.variable[0][0])
		var gone []*Call
		s.mu.Lock()
		for cic := m.cic; cic <= m.cic+rng; cic++ {
			if x := s.calls[cic]; x != nil {
				gone = append(gone, x)
			}
			delete(s.blocked, cic)
		}
		s.mu.Unlock()
		for _, x := range gone {
			x.free(CauseTemporary)
		}
		status := make([]byte, (rng+8)/8) // none blocked for maintenance
		_ = s.sendISUP(m.cic, msgGRA, nil, [][]byte{append([]byte{byte(rng)}, status...)}, nil)
	case msgGRA:
		s.mu.Lock()
		done := false
		if s.waitGRA > 0 {
			s.waitGRA--
			done = s.waitGRA == 0
		}
		s.mu.Unlock()
		if done {
			s.circuitsReset()
		}
	case msgBLO:
		s.mu.Lock()
		s.blocked[m.cic] = true
		s.mu.Unlock()
		_ = s.sendISUP(m.cic, msgBLA, nil, nil, nil)
	case msgUBL:
		s.mu.Lock()
		delete(s.blocked, m.cic)
		s.mu.Unlock()
		_ = s.sendISUP(m.cic, msgUBA, nil, nil, nil)
	case msgCGB, msgCGU:
		if len(m.variable) < 1 || len(m.variable[0]) < 1 {
			return
		}
		rng := int(m.variable[0][0])
		st := m.variable[0][1:]
		s.mu.Lock()
		for i := 0; i <= rng; i++ {
			if i/8 < len(st) && st[i/8]>>(i%8)&1 == 1 {
				if m.typ == msgCGB {
					s.blocked[m.cic+i] = true
				} else {
					delete(s.blocked, m.cic+i)
				}
			}
		}
		s.mu.Unlock()
		ack := msgCGBA
		if m.typ == msgCGU {
			ack = msgCGUA
		}
		_ = s.sendISUP(m.cic, ack, m.fixed, m.variable, nil)
	}
}

func (s *SS7) incoming(m *isupMsg) {
	c := &Call{s: s, CIC: m.cic, Events: make(chan Event, 16), state: csIncomingIAM}
	called := m.variable
	if s.cfg.ANSI && len(called) >= 2 {
		called = called[1:] // after the user service information
	}
	if len(called) > 0 {
		c.Called, _ = parseNumber(called[0])
	}
	if b, ok := m.optional[parCallingNum]; ok {
		c.Calling, c.Restricted = parseNumber(b)
	}
	if b, ok := m.optional[parRedirecting]; ok {
		c.Redirecting, _ = parseNumber(b)
	}
	if b, ok := m.optional[parGenericName]; ok && len(b) > 1 {
		c.Name = string(b[1:])
	}
	s.mu.Lock()
	s.calls[m.cic] = c
	s.mu.Unlock()
	if s.Incoming != nil {
		go s.Incoming(c)
	} else {
		c.release(CauseRejected)
	}
}
