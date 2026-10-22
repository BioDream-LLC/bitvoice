package p25

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime"
	"mime/multipart"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/p25/imbe"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// An ISSI group call, as a console subsystem (CSSI) or a serving RF subsystem holds one with a
// talkgroup's home RFSS: a SIP INVITE to the group sets up an RTP session; push-to-talk is asked
// for and granted on it; voice goes both ways as IMBE frames; heartbeats keep it up.

// Config names this subsystem and the talkgroup.
type Config struct {
	WACN     uint32 // the P25 system's wide area communications network ID (20 bits)
	System   uint32 // system ID (12 bits)
	RFSS     uint32 // this subsystem's RFSS ID (8 bits)
	Unit     uint32 // the unit ID dispatchers transmit as (24 bits)
	Group    uint32 // the talkgroup (16 bits)
	Home     string // the home RFSS's SIP address, host:port
	HomeRFSS uint32 // the home RFSS's ID, for the call's route
	Priority int    // transmit priority 1-4 (4 normal)
	Console  bool   // add the CSSI console PTT word to transmit requests
	// Heartbeat is the interval asked for (default 30 s).
	Heartbeat time.Duration
	// Key encrypts what this console transmits (nil: clear). Keys decrypt what it receives, found
	// by algorithm and key ID; the transmit key is one of them.
	Key  *Key
	Keys []Key
	// CallUnit makes this a unit-to-unit (private) call with that unit instead of a group call;
	// Home is then the called unit's home RFSS. Emergency marks the call an emergency.
	CallUnit  uint32
	Emergency bool
	// Once: try to set the call up once and do not set it up again when it ends (private calls).
	Once bool
}

// GroupURI is the talkgroup's SIP identity: WACN, system and group in hex at p25dr.
func (c Config) GroupURI() string {
	return fmt.Sprintf("sip:%05x%03x%04x@p25dr;user=TIA-P25-SG", c.WACN, c.System, c.Group)
}

// UnitURI is a subscriber unit's SIP identity (its "radical name"): WACN, system and unit ID.
func (c Config) UnitURI(unit uint32) string {
	return "sip:" + c.unitUser(unit) + "@p25dr;user=TIA-P25-SU"
}

func (c Config) unitUser(unit uint32) string {
	return fmt.Sprintf("%05x%03x%06x", c.WACN, c.System, unit)
}

// target is what this call is set up to: the talkgroup, or the called unit.
func (c Config) target() string {
	if c.CallUnit != 0 {
		return c.UnitURI(c.CallUnit)
	}
	return c.GroupURI()
}

// localUser is the user part requests to this end carry: the group's, or this console's unit's.
func (c Config) localUser() string {
	if c.CallUnit != 0 {
		return c.unitUser(c.Unit)
	}
	return fmt.Sprintf("%05x%03x%04x", c.WACN, c.System, c.Group)
}

func (c Config) userParam() string {
	if c.CallUnit != 0 {
		return "TIA-P25-SU"
	}
	return "TIA-P25-SG"
}

// bodyBoundary separates the parts of an ISSI multipart body.
const bodyBoundary = "P25 ISSI body boundary"

// sdpOf finds the SDP in a body that may be multipart (an SDP part and ISSI call parameters).
func sdpOf(contentType string, body []byte) []byte {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "multipart/") {
		return body
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		return nil
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := mr.NextPart()
		if err != nil {
			return nil
		}
		if strings.HasPrefix(strings.ToLower(part.Header.Get("Content-Type")), "application/sdp") {
			b, _ := io.ReadAll(part)
			return b
		}
	}
}

func (c Config) domain(rfss uint32) string {
	return fmt.Sprintf("%02x.%03x.%05x.p25dr", rfss, c.System, c.WACN)
}

// Talker is who is transmitting on the talkgroup.
type Talker struct {
	Unit      Unit
	Emergency bool
	Since     time.Time
	// Alg is the transmission's encryption (AlgClear when clear); NoKey says this console has no
	// key for it, so its voice is muted rather than played as noise.
	Alg   int
	KeyID int
	NoKey bool
	// Undecodable: the transmission's voice comes in blocks this console does not decode (most
	// likely P25 Phase 2 half-rate voice, sent untranscoded): it is silent here.
	Undecodable bool
}

// Events are what the group call tells its owner. Each may be nil.
type Events struct {
	Voice  func(pcm []int16)              // 20 ms of received speech, 8 kHz
	Talker func(t *Talker)                // someone started (t) or stopped (nil) talking
	Grant  func(granted bool, why string) // the answer to a push-to-talk request
	State  func(up bool, why string)      // the call came up or went down
}

// Group is one ISSI group call.
type Group struct {
	oddVoice int // talk-spurt packets in a row whose voice is in blocks not decoded here
	cfg      Config
	stack    *sip.Stack
	rtp      *net.UDPConn
	ev       Events
	home     sip.Peer

	mu       sync.Mutex
	up       bool
	media    netip.AddrPort // where the home RFSS wants RTP
	callID   string
	tag      string
	remote   string // the To header with the home's tag
	cseq     int
	lastHB   time.Time
	talker   *Talker
	keying   bool // asked to transmit, no answer yet
	tx       bool // granted
	tsn      int
	seq      uint16
	ts       uint32
	ssrc     uint32
	frame    int // place in the 18-frame superframe
	sentHdr  bool
	dec      *imbe.Decoder
	enc      *imbe.Encoder
	lastRx   time.Time
	cancel   context.CancelFunc
	ever     bool   // the call was set up once
	incoming bool   // set up by the other side
	local    string // the To of the INVITE we answered

	// Encryption while transmitting: the MI of the present superframe and its keystream.
	txMI uint64
	txKS []byte
	// While receiving: the key in use, the present MI and keystream, the encryption sync symbols
	// gathered from frames 12-17, and the MI they gave for the next superframe.
	rxKey  *Key
	rxAlg  int
	rxKID  int
	rxKS   []byte
	rxES   []byte
	rxNext uint64
	rxHave bool
}

// superframe is the frame types of the 18 IMBE frames of a P25 voice superframe (LDU1 then LDU2)
// and how many octets of frame-specific data each carries on the ISSI.
var superframe = [18]struct{ ft, extra int }{
	{0xc2, 0}, {0xc3, 0}, {0xc4, 0}, {0xc5, 0}, {0xc6, 0}, {0xc7, 0}, {0xc8, 0}, {0xc9, 0}, {0x6a, 3},
	{0xcb, 0}, {0xcc, 0}, {0x6d, 4}, {0x6e, 4}, {0x6f, 4}, {0x70, 4}, {0x71, 4}, {0x72, 4}, {0x73, 3},
}

// NewGroup makes a group call over a SIP stack and an RTP socket of its own. Start sets it up.
func NewGroup(cfg Config, stack *sip.Stack, rtp *net.UDPConn, ev Events) (*Group, error) {
	ap, err := netip.ParseAddrPort(cfg.Home)
	if err != nil {
		addr, err2 := net.ResolveUDPAddr("udp", cfg.Home)
		if err2 != nil {
			return nil, fmt.Errorf("p25: home RFSS address: %v", err)
		}
		ap = addr.AddrPort()
	}
	if cfg.Priority == 0 {
		cfg.Priority = 4
	}
	if cfg.Heartbeat == 0 {
		cfg.Heartbeat = 30 * time.Second
	}
	return &Group{cfg: cfg, stack: stack, rtp: rtp, ev: ev, home: sip.Peer{Transport: "UDP", Addr: ap},
		dec: imbe.New(), enc: imbe.NewEncoder(), ssrc: rand.Uint32(), tsn: 1, rxAlg: AlgClear}, nil
}

// Start sets up the call and keeps it up (again after a failure) until ctx ends.
func (g *Group) Start(ctx context.Context) {
	ctx, g.cancel = context.WithCancel(ctx)
	go g.readRTP(ctx)
	go func() {
		backoff := time.Second
		for ctx.Err() == nil {
			g.mu.Lock()
			up, last := g.up, g.lastHB
			g.mu.Unlock()
			g.mu.Lock()
			ever := g.ever
			g.mu.Unlock()
			switch {
			case !up && g.cfg.Once && ever:
				return // a private call that ended is not set up again
			case !up:
				if err := g.invite(); err != nil {
					if g.cfg.Once {
						g.state(false, err.Error())
						return
					}
					g.state(false, err.Error())
					select {
					case <-ctx.Done():
					case <-time.After(backoff):
					}
					backoff = min(backoff*2, 30*time.Second)
					continue
				}
				backoff = time.Second
				g.state(true, "")
				g.mu.Lock()
				g.ever = true
				g.mu.Unlock()
			case time.Since(last) > 3*g.cfg.Heartbeat:
				g.mu.Lock()
				g.up = false
				g.mu.Unlock()
				g.state(false, "no heartbeat from the home RFSS")
				continue
			}
			g.send(&Payload{Packet: PacketType{Type: Heartbeat, Interval: int(g.cfg.Heartbeat / time.Second)}})
			select {
			case <-ctx.Done():
			case <-time.After(g.cfg.Heartbeat):
			}
		}
		g.bye()
	}()
}

// Stop ends the call.
func (g *Group) Stop() {
	if g.cancel != nil {
		g.cancel()
	}
}

func (g *Group) state(up bool, why string) {
	if g.ev.State != nil {
		g.ev.State(up, why)
	}
}

func (g *Group) offer() []byte {
	port := g.rtp.LocalAddr().(*net.UDPAddr).Port
	host := g.stack.LocalHost(g.home)
	name := "TIA-P25-Groupcall"
	if g.cfg.CallUnit != 0 {
		name = "TIA-P25-U2UCall"
	}
	return []byte("v=0\r\no=- " + strconv.FormatInt(time.Now().Unix(), 10) + " 1 IN IP4 " + host + "\r\ns=" + name + "\r\nc=IN IP4 " + host +
		"\r\nt=0 0\r\nm=audio " + strconv.Itoa(port) + " RTP/AVP 100\r\na=rtpmap:100 X-TIA-P25-IMBE/8000\r\n")
}

// body is what an INVITE carries: the SDP, and for a unit-to-unit call the ISSI call parameters
// too (half duplex, no availability check: a dispatcher's private call rings straight through).
func (g *Group) body() (contentType string, body []byte) {
	if g.cfg.CallUnit == 0 {
		return "application/sdp", g.offer()
	}
	var b strings.Builder
	b.WriteString("--" + bodyBoundary + "\r\nContent-Type: application/sdp\r\n\r\n")
	b.Write(g.offer())
	b.WriteString("\r\n--" + bodyBoundary + "\r\nContent-Type: application/x-tia-p25-issi\r\n\r\nc-duplex:0\r\nc-pref:0\r\n")
	b.WriteString("\r\n--" + bodyBoundary + "--\r\n")
	return `multipart/mixed;boundary="` + bodyBoundary + `"`, []byte(b.String())
}

func (g *Group) priority() string {
	if g.cfg.Emergency {
		return strconv.Itoa(g.cfg.Priority) + ";e"
	}
	return strconv.Itoa(g.cfg.Priority) + ";a"
}

// invite sets up the call with the home RFSS.
func (g *Group) invite() error {
	g.mu.Lock()
	g.callID = sip.NewCallID() + "@" + g.cfg.domain(g.cfg.RFSS)
	g.tag = sip.NewTag()
	g.cseq++
	cseq := g.cseq
	g.mu.Unlock()
	uri := g.cfg.target()
	m := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: uri}
	from := uri
	if g.cfg.CallUnit != 0 {
		from = g.cfg.UnitURI(g.cfg.Unit) // the dispatcher calls as the console's unit
		m.Set("Route", "<sip:TIA-P25-U2UDest@"+g.cfg.domain(g.cfg.HomeRFSS)+";lr>")
	} else {
		m.Set("Route", "<sip:TIA-P25-Groupcall@"+g.cfg.domain(g.cfg.HomeRFSS)+";lr>")
	}
	m.Set("From", "<"+from+">;tag="+g.tag)
	m.Set("To", "<"+uri+">")
	m.Set("Call-ID", g.callID)
	m.Set("CSeq", strconv.Itoa(cseq)+" INVITE")
	m.Set("Contact", fmt.Sprintf("<sip:%s@%s:%d;user=%s>", g.cfg.localUser(), g.stack.LocalHost(g.home), g.stack.Port(), g.cfg.userParam()))
	m.Set("Priority", g.priority())
	m.Set("Allow", "INVITE, ACK, CANCEL, BYE, REGISTER")
	m.Set("Accept", "application/sdp;level=1,application/x-tia-p25-issi")
	ct, body := g.body()
	m.Set("Content-Type", ct)
	m.Body = body
	tx := g.stack.Request(m, g.home)
	var final *sip.Message
	for r := range tx.Responses {
		if r.StatusCode >= 200 {
			final = r
		}
	}
	if final == nil {
		return errors.New("the home RFSS did not answer")
	}
	if final.StatusCode != 200 {
		what := "the group call"
		if g.cfg.CallUnit != 0 {
			what = "the call to the unit"
		}
		return fmt.Errorf("the home RFSS refused %s: %d %s", what, final.StatusCode, final.Reason)
	}
	media, err := mediaOf(sdpOf(final.Get("Content-Type"), final.Body))
	if err != nil {
		return err
	}
	ack := &sip.Message{IsRequest: true, Method: sip.ACK, RequestURI: uri}
	ack.Set("Route", m.Get("Route"))
	ack.Set("From", m.Get("From"))
	ack.Set("To", final.Get("To"))
	ack.Set("Call-ID", g.callID)
	ack.Set("CSeq", strconv.Itoa(cseq)+" ACK")
	_ = g.stack.Stateless(ack, g.home)
	g.mu.Lock()
	g.up, g.media, g.remote, g.lastHB, g.ever = true, media, final.Get("To"), time.Now(), true
	g.mu.Unlock()
	g.send(&Payload{Packet: PacketType{Type: HeartbeatQ, Interval: int(g.cfg.Heartbeat / time.Second)}})
	return nil
}

func (g *Group) bye() {
	g.mu.Lock()
	up, uri := g.up, g.cfg.target()
	g.up = false
	g.cseq++
	cseq := g.cseq
	g.mu.Unlock()
	if !up {
		return
	}
	m := &sip.Message{IsRequest: true, Method: sip.BYE, RequestURI: uri}
	from := uri
	if g.cfg.CallUnit != 0 {
		from = g.cfg.UnitURI(g.cfg.Unit)
	}
	if g.incoming {
		from = g.local // the To of the INVITE we answered
	}
	m.Set("From", "<"+strings.Trim(stripTag(from), "<>")+">;tag="+g.tag)
	m.Set("To", g.remote)
	m.Set("Call-ID", g.callID)
	m.Set("CSeq", strconv.Itoa(cseq)+" BYE")
	tx := g.stack.Request(m, g.home)
	for range tx.Responses {
	}
}

// mediaOf is where an SDP body wants RTP sent.
func mediaOf(body []byte) (netip.AddrPort, error) {
	s, err := sdp.Parse(body)
	if err != nil || s.Audio() == nil {
		return netip.AddrPort{}, errors.New("the home RFSS's answer has no audio description")
	}
	a, err := netip.ParseAddr(s.Connection(s.Audio()))
	if err != nil {
		return netip.AddrPort{}, errors.New("the home RFSS's answer has no address")
	}
	return netip.AddrPortFrom(a, uint16(s.Audio().Port)), nil
}

// Handle answers SIP requests for this call from the home RFSS (a call set up from its side, a
// BYE). It reports false for requests that are not for this call.
func (g *Group) Handle(tx *sip.ServerTx, in *sip.Incoming) bool {
	m := in.Msg
	if m.Method == sip.ACK {
		return true // no transaction to answer
	}
	u, err := sip.ParseURI(m.RequestURI)
	if err != nil || !strings.EqualFold(u.User, g.cfg.localUser()) {
		return false
	}
	g.mu.Lock()
	callID := g.callID
	g.mu.Unlock()
	if m.Method != sip.INVITE && callID != "" && m.CallID() != callID {
		return false // another call to the same identity
	}
	switch m.Method {
	case sip.INVITE:
		if g.cfg.CallUnit != 0 && callID != "" && m.CallID() != callID {
			return false // a unit call takes only the INVITE it was made for
		}
		media, err := mediaOf(sdpOf(m.Get("Content-Type"), m.Body))
		if err != nil {
			tx.Respond(sip.NewResponse(m, 488, "Not Acceptable Here"))
			return true
		}
		r := sip.NewResponse(m, 200, "")
		tag := sip.NewTag()
		r.Set("To", m.Get("To")+";tag="+tag)
		r.Set("Contact", fmt.Sprintf("<sip:%s@%s:%d;user=%s>", g.cfg.localUser(), g.stack.LocalHost(in.From), g.stack.Port(), g.cfg.userParam()))
		r.Set("Content-Type", "application/sdp")
		r.Body = g.offer()
		tx.Respond(r)
		g.mu.Lock()
		g.up, g.media, g.lastHB, g.ever, g.incoming = true, media, time.Now(), true, true
		g.callID, g.tag, g.remote, g.local = m.CallID(), tag, m.Get("From"), m.Get("To")
		if g.home.Addr != in.From.Addr && g.cfg.CallUnit != 0 {
			g.home = in.From // a unit call is answered where it came from
		}
		g.mu.Unlock()
		g.state(true, "")
	case sip.BYE:
		tx.Respond(sip.NewResponse(m, 200, ""))
		g.mu.Lock()
		g.up = false
		g.mu.Unlock()
		why := "the home RFSS ended the group call"
		if g.cfg.CallUnit != 0 {
			why = "the unit hung up"
		}
		g.state(false, why)
	default:
		tx.Respond(sip.NewResponse(m, 200, ""))
	}
	return true
}

// stripTag drops the parameters after a name-addr (its tag).
func stripTag(s string) string {
	if i := strings.Index(s, ">"); i >= 0 {
		return s[:i+1]
	}
	return s
}

// send puts a P25 payload in an RTP packet to the home RFSS.
func (g *Group) send(p *Payload) {
	g.mu.Lock()
	to := g.media
	g.seq++
	pkt := make([]byte, 12, 64)
	pkt[0], pkt[1] = 0x80, 100
	binary.BigEndian.PutUint16(pkt[2:], g.seq)
	binary.BigEndian.PutUint32(pkt[4:], g.ts)
	binary.BigEndian.PutUint32(pkt[8:], g.ssrc)
	g.mu.Unlock()
	if !to.IsValid() {
		return
	}
	_, _ = g.rtp.WriteToUDPAddrPort(append(pkt, p.Marshal()...), to)
}

func (g *Group) pttWord() *PTTWord {
	return &PTTWord{Unit: Unit{WACN: g.cfg.WACN, System: g.cfg.System, ID: g.cfg.Unit}, Priority: g.cfg.Priority}
}

func (g *Group) packet(t int) PacketType {
	return PacketType{Type: t, TSN: g.tsn, Options: ServiceOptions{Priority: g.cfg.Priority}}
}

// Key asks to transmit (on) or ends the transmission (off). The answer to a request comes to
// Events.Grant.
func (g *Group) Key(on bool) error {
	g.mu.Lock()
	if !g.up {
		g.mu.Unlock()
		return errors.New("the talkgroup is not connected")
	}
	if on {
		if g.tx || g.keying {
			g.mu.Unlock()
			return nil
		}
		g.tsn = g.tsn%127 + 1
		g.keying, g.sentHdr, g.frame = true, false, 0
		g.txMI, g.txKS = 0, nil
		if k := g.cfg.Key; k != nil {
			var b [8]byte
			for g.txMI == 0 { // an all-zero MI would make the LFSR stay at zero
				_, _ = crand.Read(b[:])
				g.txMI = binary.BigEndian.Uint64(b[:])
			}
			ks, err := k.Keystream(g.txMI)
			if err != nil {
				g.keying = false
				g.mu.Unlock()
				return err
			}
			g.txKS = ks
		}
		p := &Payload{Packet: g.packet(PTTRequest), PTT: g.pttWord()}
		if g.cfg.Console {
			p.ConsolePTT = &ConsolePTTWord{PTTWord: *g.pttWord(), Console: uint64(g.cfg.RFSS), VoiceSource: uint64(g.cfg.Unit), ConsolePrio: g.cfg.Priority}
		}
		g.mu.Unlock()
		g.send(p)
		return nil
	}
	was := g.tx || g.keying
	g.tx, g.keying = false, false
	p := &Payload{Packet: g.packet(PTTEnd)}
	g.mu.Unlock()
	if was {
		g.send(p)
		g.send(p) // sent twice, as equipment does, against a loss
	}
	return nil
}

// Transmitting says whether a transmission was granted and is going on.
func (g *Group) Transmitting() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tx
}

// Speak sends 20 ms of the dispatcher's speech while a transmission is granted.
func (g *Group) Speak(pcm []int16) {
	g.mu.Lock()
	if !g.tx {
		g.mu.Unlock()
		return
	}
	hdr := !g.sentHdr
	g.sentHdr = true
	idx := g.frame % 18
	sf := superframe[idx]
	g.frame++
	g.ts += 160
	alg, kid, mi, ks := AlgClear, 0, g.txMI, g.txKS
	if k := g.cfg.Key; k != nil {
		alg, kid = k.Alg, k.ID
	}
	next := uint64(0)
	if ks != nil {
		next = NextMI(mi)
		if idx == 17 { // the next superframe uses the MI sent in this one's LDU2
			g.txMI = next
			g.txKS, _ = g.cfg.Key.Keystream(next)
		}
	}
	g.mu.Unlock()
	if hdr {
		h := HeaderWord{AlgID: alg, KeyID: kid, Group: int(g.cfg.Group), VBB: 2}
		binary.BigEndian.PutUint64(h.MI[:8], mi)
		g.send(&Payload{S: true, Packet: g.packet(PTTProgress), PTT: g.pttWord(), Header: &h})
	}
	f := IMBEBlock{FT: sf.ft, Extra: make([]byte, sf.extra)}
	info := g.enc.Encode(pcm)
	if ks != nil {
		off := imbeOffset(idx + 1)
		for i := range info {
			info[i] ^= ks[off+i]
		}
	}
	f.SetInfo(info)
	if idx >= 11 && idx <= 16 { // frames 12-17: four symbols each of the encryption sync word
		cw := esEncode(next, alg, kid)
		var s [4]byte
		copy(s[:], cw[4*(idx-11):])
		putES(f.Extra, s)
	}
	g.send(&Payload{S: true, Packet: g.packet(PTTProgress), PTT: g.pttWord(), Voice: []IMBEBlock{f}})
}

// ClearVoiceFrame is the IMBE frame at position i of a transmission sent in the clear: its frame type
// in superframe order, and in frames 12-17 the encryption sync word saying "clear" (a receiver that
// read zeros there would take the voice for encrypted). For programs that play a radio's side, like
// the demo's radio system.
func ClearVoiceFrame(i int, info []byte) IMBEBlock {
	idx := i % 18
	sf := superframe[idx]
	f := IMBEBlock{FT: sf.ft, Extra: make([]byte, sf.extra)}
	f.SetInfo(info)
	if idx >= 11 && idx <= 16 {
		cw := esEncode(0, AlgClear, 0)
		var s [4]byte
		copy(s[:], cw[4*(idx-11):])
		putES(f.Extra, s)
	}
	return f
}

// frameIndex is where a frame type falls in the superframe (0-17), or -1.
func frameIndex(ft int) int {
	for i, s := range superframe {
		if s.ft == ft {
			return i
		}
	}
	return -1
}

// keyFor finds a receive key.
func (g *Group) keyFor(alg, kid int) *Key {
	for i := range g.cfg.Keys {
		if k := &g.cfg.Keys[i]; k.Alg == alg && k.ID == kid {
			return k
		}
	}
	return nil
}

// startRx sets up decryption from a header word or an encryption sync (mi for the superframe that
// starts now). It reports whether the talker's encryption changed.
func (g *Group) startRx(alg, kid int, mi uint64) {
	g.rxAlg, g.rxKID, g.rxKS, g.rxKey = alg, kid, nil, nil
	if alg == AlgClear {
		return
	}
	if k := g.keyFor(alg, kid); k != nil {
		g.rxKey = k
		g.rxKS, _ = k.Keystream(mi)
	}
}

// voiceIn decrypts (if it must) and decodes one received frame; nil when it is to stay silent.
func (g *Group) voiceIn(v IMBEBlock) []byte {
	idx := frameIndex(v.FT)
	info := v.Info()
	if idx == 0 && g.rxHave { // a new superframe: the MI its predecessor announced
		g.rxHave = false
		g.startRx(g.rxAlg, g.rxKID, g.rxNext)
	}
	if idx >= 11 && idx <= 16 && len(v.Extra) >= 4 {
		if idx == 11 {
			g.rxES = g.rxES[:0]
		}
		s := getES(v.Extra)
		g.rxES = append(g.rxES, s[:]...)
		if idx == 16 && len(g.rxES) == 24 {
			var cw [24]byte
			copy(cw[:], g.rxES)
			if mi, alg, kid, ok := esDecode(cw); ok {
				if alg != g.rxAlg || kid != g.rxKID {
					g.rxAlg, g.rxKID, g.rxKS, g.rxKey = alg, kid, nil, g.keyFor(alg, kid) // joined late
				}
				g.rxNext, g.rxHave = mi, true
			}
		}
	}
	if g.rxAlg == AlgClear {
		return info
	}
	if g.rxKS == nil || idx < 0 {
		return nil // encrypted and no key (or not yet synchronised): silence, not noise
	}
	off := imbeOffset(idx + 1)
	for i := range info {
		info[i] ^= g.rxKS[off+i]
	}
	return info
}

func (g *Group) readRTP(ctx context.Context) {
	go func() { <-ctx.Done(); _ = g.rtp.SetReadDeadline(time.Now()) }()
	buf := make([]byte, 2048)
	for {
		n, from, err := g.rtp.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if n < 13 || buf[0]>>6 != 2 {
			continue
		}
		g.mu.Lock()
		want := g.media
		g.mu.Unlock()
		if want.IsValid() && from.Addr().Unmap() != want.Addr().Unmap() {
			continue // not the home RFSS
		}
		hdr := 12 + 4*int(buf[0]&0x0f)
		if hdr >= n {
			continue
		}
		p, err := Parse(buf[hdr:n])
		if err != nil {
			continue
		}
		g.receive(p)
	}
}

func (g *Group) receive(p *Payload) {
	g.mu.Lock()
	g.lastHB = time.Now()
	t := p.Packet.Type
	var talker *Talker
	changed := false
	var grant *bool
	why := ""
	switch t {
	case PTTGrant:
		if g.keying {
			g.keying, g.tx = false, true
			ok := true
			grant = &ok
		}
	case PTTDeny, PTTWait:
		if g.keying {
			g.keying = false
			no := false
			grant, why = &no, PacketTypeName(t)
			if p.PTT != nil {
				why += " (unit " + p.PTT.Unit.String() + " has the floor)"
			}
		}
	case PTTStart, PTTProgress:
		if p.Header != nil {
			mi := binary.BigEndian.Uint64(p.Header.MI[:8])
			g.rxHave = false
			g.startRx(p.Header.AlgID, p.Header.KeyID, mi)
		}
		if p.PTT != nil && (g.talker == nil || g.talker.Unit != p.PTT.Unit) {
			g.talker = &Talker{Unit: p.PTT.Unit, Emergency: p.Packet.Options.Emergency, Since: time.Now(), Alg: AlgClear}
			talker, changed = g.talker, true
		}
		if g.talker != nil && (g.talker.Alg != g.rxAlg || g.talker.NoKey != (g.rxAlg != AlgClear && g.keyFor(g.rxAlg, g.rxKID) == nil)) {
			c := *g.talker
			c.Alg, c.KeyID = g.rxAlg, g.rxKID
			c.NoKey = g.rxAlg != AlgClear && g.keyFor(g.rxAlg, g.rxKID) == nil
			g.talker = &c
			talker, changed = g.talker, true
		}
		if g.tx && p.PTT != nil && p.PTT.Unit.ID != g.cfg.Unit {
			g.tx = false // someone else won the floor (pre-empted)
		}
		g.lastRx = time.Now()
	case PTTEnd:
		if g.talker != nil {
			g.talker, changed = nil, true
		}
		g.rxAlg, g.rxKS, g.rxKey, g.rxHave = AlgClear, nil, nil, false
	case HeartbeatQ:
		g.mu.Unlock()
		g.send(&Payload{Packet: PacketType{Type: Heartbeat, Interval: int(g.cfg.Heartbeat / time.Second)}})
		return
	}
	dec := g.dec
	if (t == PTTStart || t == PTTProgress) && len(p.Voice) == 0 && len(p.Other) > 0 {
		g.oddVoice++
	} else if len(p.Voice) > 0 {
		g.oddVoice = 0
	}
	if g.talker != nil && g.talker.Undecodable != (g.oddVoice >= 3) {
		c := *g.talker
		c.Undecodable = g.oddVoice >= 3
		g.talker, talker, changed = &c, &c, true
	}
	var frames [][]byte
	for _, v := range p.Voice {
		if v.Mute {
			continue
		}
		frames = append(frames, g.voiceIn(v))
	}
	if changed && talker != nil {
		c := *talker // the encryption seen in this packet's frames too
		c.Alg, c.KeyID = g.rxAlg, g.rxKID
		c.NoKey = g.rxAlg != AlgClear && g.keyFor(g.rxAlg, g.rxKID) == nil
		g.talker, talker = &c, &c
	}
	g.mu.Unlock()
	if grant != nil && g.ev.Grant != nil {
		g.ev.Grant(*grant, why)
	}
	if changed && g.ev.Talker != nil {
		g.ev.Talker(talker)
	}
	if g.ev.Voice != nil {
		for _, info := range frames {
			if info == nil {
				g.ev.Voice(make([]int16, 160))
				continue
			}
			if pcm, err := dec.Decode(info); err == nil {
				g.ev.Voice(pcm)
			}
		}
	}
}

// CurrentTalker is who is talking now (nil: nobody).
func (g *Group) CurrentTalker() *Talker {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.talker != nil && time.Since(g.lastRx) > 2*time.Second {
		g.talker = nil // the end was lost
	}
	return g.talker
}

// Up says whether the group call is set up.
func (g *Group) Up() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.up
}

// NewReceiver is a group call's receiving side on its own, not connected to anything: payloads
// given to Receive are decrypted with keys and decoded, and reported through ev. For a stand-in
// home RFSS in tests, and for replaying captures.
func NewReceiver(group uint32, keys []Key, ev Events) *Group {
	g, _ := NewGroup(Config{Group: group, Home: "127.0.0.1:1", Keys: keys}, nil, nil, ev)
	return g
}

// Receive handles one payload as if it had come from the home RFSS.
func (g *Group) Receive(p *Payload) { g.receive(p) }
