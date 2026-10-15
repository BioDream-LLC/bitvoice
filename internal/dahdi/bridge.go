package dahdi

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/biodream-llc/bitvoice/internal/mf"
	"github.com/biodream-llc/bitvoice/internal/r2"
	"log/slog"
	"math"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/webphone"
)

// Config is what bitvoice-dahdi learns from BitVoice (GET /api/cards/bridge).
type Config struct {
	SIPPort  int           `json:"sipPort"`  // the engine's SIP port on this computer
	LinePort int           `json:"linePort"` // where the card trunk reaches this program
	ALaw     bool          `json:"alaw"`
	Channels []ChannelConf `json:"channels"`
	Spans    []SpanConf    `json:"spans,omitempty"`
}

// ChannelConf is one port as the bridge needs it.
type ChannelConf struct {
	Channel   int    `json:"channel"`
	Kind      string `json:"kind"` // phone, line, or em (a channel of an E&M T1)
	Extension string `json:"extension,omitempty"`
	Secret    string `json:"secret,omitempty"`
	Number    string `json:"number,omitempty"`
	// E&M: the start signal (wink, immediate, delay, fgd) and how numbers are sent (dtmf or mf).
	Start   string `json:"start,omitempty"`
	Address string `json:"address,omitempty"`
	// R2: the country variant.
	Variant string `json:"variant,omitempty"`
}

// DigitTimeout is how long a phone may pause between digits before the number is dialled.
var DigitTimeout = 4 * time.Second

// Bridge joins a card's ports to the engine.
type Bridge struct {
	ctx    context.Context
	cancel context.CancelFunc
	cfg    Config
	open   func(n int, alaw bool) (Channel, error)
	log    *slog.Logger
	gw     *webphone.Gateway
	pool   *media.Pool
	lines  *sip.Stack

	mu    sync.Mutex
	ports map[int]*port
	spans []*span
}

type portState int

const (
	stIdle portState = iota
	stDialTone
	stDialling
	stRinging // a call rings the phone, or a line rings us
	stCalling // a call is being set up (an outgoing line call: dialling the number)
	stTalking
	stFinished // the call ended; the phone is still off hook
	stBroken
)

var stateNames = map[portState]string{stIdle: "idle", stDialTone: "dial tone", stDialling: "dialling", stRinging: "ringing",
	stCalling: "calling", stTalking: "in a call", stFinished: "call ended", stBroken: "not working"}

// port is one channel and the call on it.
type port struct {
	b    *Bridge
	conf ChannelConf
	ch   Channel
	law  int // RTP payload type: 0 µ-law, 8 A-law

	mu        sync.Mutex
	state     portState
	err       string
	phone     *webphone.Phone
	call      *webphone.Call
	digits    string
	timer     *time.Timer
	det       Detector
	rtp       *net.UDPConn   // the call's audio, to and from remote
	remote    netip.AddrPort // where audio goes
	dev       *media.Endpoint
	seq       uint16
	ts        uint32
	ssrc      uint32
	out       [][]byte // audio queued for the channel: dialled digits on a line
	line      *lineCall
	lastRing  time.Time
	lastEvent uint32
	eventSeen bool

	waitEv     chan int       // E&M: an outgoing call waiting for the far end's signals
	digitsDone chan struct{}  // E&M: closed when the number coming in is complete
	mfrx       *mf.R1Receiver // E&M: MF digits coming in

	r2      *r2.Chan // MFC-R2: the circuit's signalling
	r2io    *r2io
	r2wait  chan string // MFC-R2: a call out waiting for accepted/answered
	fwdDet  *mf.Detector
	backDet *mf.Detector
}

// New starts the bridge: every port is opened, phones registered, the card trunk's port opened.
func New(ctx context.Context, cfg Config, open func(int, bool) (Channel, error), log *slog.Logger) (*Bridge, error) {
	if log == nil {
		log = slog.Default()
	}
	if open == nil {
		open = Open
	}
	ctx, cancel := context.WithCancel(ctx)
	b := &Bridge{ctx: ctx, cancel: cancel, cfg: cfg, open: open, log: log, ports: map[int]*port{}, pool: &media.Pool{Host: "127.0.0.1", Min: 42000, Max: 43998}}
	gw, err := webphone.New(ctx, cfg.SIPPort, b)
	if err != nil {
		cancel()
		return nil, err
	}
	b.gw = gw
	for _, c := range cfg.Channels {
		p := &port{b: b, conf: c, law: 0, ssrc: uint32(time.Now().UnixNano()) ^ uint32(c.Channel)<<16}
		if cfg.ALaw {
			p.law = 8
		}
		b.ports[c.Channel] = p
	}
	if cfg.LinePort != 0 {
		b.lines = sip.NewStack(b.handleLine)
		b.lines.Host = "127.0.0.1"
		if err := b.lines.Listen("127.0.0.1:" + strconv.Itoa(cfg.LinePort)); err != nil {
			cancel()
			return nil, fmt.Errorf("dahdi: the card trunk's port %d: %w", cfg.LinePort, err)
		}
		b.lines.Serve(ctx)
	}
	for _, p := range b.ports {
		go p.run()
	}
	b.startSpans()
	return b, nil
}

// NewMediaEndpoint is the loopback endpoints the web-phone bridge connects phones' audio through.
func (b *Bridge) NewMediaEndpoint() (*media.Endpoint, error) { return b.pool.Allocate() }

// Status is each port's state, by channel number.
func (b *Bridge) Status() map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]string{}
	for n, p := range b.ports {
		p.mu.Lock()
		s := stateNames[p.state]
		if p.state == stBroken && p.err != "" {
			s += ": " + p.err
		}
		p.mu.Unlock()
		out[strconv.Itoa(n)] = s
	}
	for i, s := range b.spans {
		b.mu.Unlock()
		st := s.status()
		b.mu.Lock()
		out["span"+strconv.Itoa(i+1)] = st
	}
	return out
}

// Close hangs up and closes every port.
func (b *Bridge) Close() {
	b.mu.Lock()
	ports := b.ports
	b.ports = map[int]*port{}
	b.mu.Unlock()
	for _, p := range ports {
		p.hangup()
		p.mu.Lock()
		ch, ph := p.ch, p.phone
		p.state = stBroken
		p.mu.Unlock()
		if ph != nil {
			ph.Close()
		}
		if ch != nil {
			ch.Close()
		}
	}
	if b.lines != nil {
		b.lines.Close()
	}
	b.cancel()
}

func (p *port) set(s portState) {
	p.mu.Lock()
	p.state = s
	p.mu.Unlock()
}

func (p *port) is(s portState) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state == s
}

func (p *port) run() {
	ch, err := p.b.open(p.conf.Channel, p.b.cfg.ALaw)
	if err != nil {
		p.mu.Lock()
		p.state, p.err = stBroken, err.Error()
		p.mu.Unlock()
		p.b.log.Error("dahdi: channel not opened", "channel", p.conf.Channel, "err", err)
		return
	}
	p.mu.Lock()
	p.ch = ch
	p.mu.Unlock()
	if p.isR2() {
		p.r2Start()
	}
	if p.conf.Kind == "phone" {
		id := "ch" + strconv.Itoa(p.conf.Channel)
		ph, err := p.b.gw.OpenAs("card", "BitVoice telephony card", id, p.conf.Extension, p.conf.Secret, "")
		if err != nil {
			p.mu.Lock()
			p.state, p.err = stBroken, "not registered with BitVoice: "+err.Error()
			p.mu.Unlock()
		} else {
			p.mu.Lock()
			p.phone = ph
			p.mu.Unlock()
			go p.phoneEvents(ph)
		}
	}
	buf := make([]byte, FrameBytes)
	for {
		if p.b.ctx.Err() != nil {
			return
		}
		n, err := ch.Read(buf)
		switch {
		case err == ErrEvent:
			if ev, err := ch.Event(); err == nil {
				p.event(ev)
			}
			continue
		case err != nil:
			p.mu.Lock()
			broken := p.state == stBroken
			p.state, p.err = stBroken, err.Error()
			p.mu.Unlock()
			if !broken {
				p.b.log.Warn("dahdi: channel stopped", "channel", p.conf.Channel, "err", err)
			}
			return
		}
		p.audio(buf[:n])
	}
}

// audio is 20 ms read from the port: digits while dialling, the call's sound to the far side.
func (p *port) audio(b []byte) {
	p.mu.Lock()
	st, conn, remote := p.state, p.rtp, p.remote
	var outFrame []byte
	if len(p.out) > 0 {
		outFrame, p.out = p.out[0], p.out[1:]
	}
	p.mu.Unlock()
	if p.isR2() {
		if tone := p.r2Audio(b); tone != nil {
			outFrame = tone // the compelled tones take the channel while they last
		}
	}
	if outFrame != nil {
		_, _ = p.ch.Write(outFrame)
	}
	if p.isEM() {
		if st == stDialling {
			pcm := make([]int16, len(b))
			for i, v := range b {
				pcm[i] = decode(v, p.law)
			}
			p.emAudio(pcm)
		}
	} else if st == stDialTone || st == stDialling || st == stTalking && p.conf.Kind == "phone" {
		pcm := make([]int16, len(b))
		for i, v := range b {
			pcm[i] = decode(v, p.law)
		}
		for _, k := range p.det.Feed(pcm) {
			p.key(k)
		}
	}
	if conn != nil && remote.IsValid() && (st == stTalking || st == stCalling || st == stRinging || st == stDialling) {
		p.sendRTP(conn, remote, uint8(p.law), b, false)
	}
}

func decode(v byte, law int) int16 {
	if law == 8 {
		return media.AlawToLinear(v)
	}
	return media.UlawToLinear(v)
}

func encode(s int16, law int) byte {
	if law == 8 {
		return media.LinearToAlaw(s)
	}
	return media.LinearToUlaw(s)
}

func (p *port) sendRTP(conn *net.UDPConn, to netip.AddrPort, pt uint8, payload []byte, marker bool) {
	p.mu.Lock()
	p.seq++
	seq, ts := p.seq, p.ts
	if pt != 101 {
		p.ts += uint32(len(payload))
	}
	p.mu.Unlock()
	pkt := make([]byte, 12+len(payload))
	pkt[0] = 0x80
	pkt[1] = pt
	if marker {
		pkt[1] |= 0x80
	}
	binary.BigEndian.PutUint16(pkt[2:], seq)
	binary.BigEndian.PutUint32(pkt[4:], ts)
	binary.BigEndian.PutUint32(pkt[8:], p.ssrc)
	copy(pkt[12:], payload)
	_, _ = conn.WriteToUDPAddrPort(pkt, to)
}

// sendKey sends a key pressed during a call as RFC 4733 events, beside the tone itself.
func (p *port) sendKey(k byte) {
	p.mu.Lock()
	conn, to := p.rtp, p.remote
	p.mu.Unlock()
	code := strings.IndexByte("0123456789*#ABCD", k)
	if conn == nil || code < 0 {
		return
	}
	for i, end := range []bool{false, false, true, true, true} {
		e := []byte{byte(code), 10, 0, 0}
		if end {
			e[1] |= 0x80
		}
		binary.BigEndian.PutUint16(e[2:], uint16(160*(i+1)))
		p.sendRTP(conn, to, 101, e, i == 0)
	}
}

// receive writes the far side's audio to the port.
func (p *port) receive(conn *net.UDPConn) {
	buf := make([]byte, 2048)
	for {
		n, _, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		if n < 12 || buf[0]>>6 != 2 {
			continue
		}
		hl := 12 + 4*int(buf[0]&0x0f)
		if buf[0]&0x10 != 0 && n >= hl+4 {
			hl += 4 + 4*int(binary.BigEndian.Uint16(buf[hl+2:]))
		}
		pt := int(buf[1] & 0x7f)
		if pt == 101 && hl+4 <= n {
			// A key from the far side: played to the port as its tone, once per event.
			ts := binary.BigEndian.Uint32(buf[4:])
			p.mu.Lock()
			fresh := ts != p.lastEvent || !p.eventSeen
			p.lastEvent, p.eventSeen = ts, true
			if fresh && int(buf[hl]) < 16 {
				p.out = append(p.out, dtmfFrames(string("0123456789*#ABCD"[buf[hl]]), p.law, false)...)
			}
			p.mu.Unlock()
			continue
		}
		if hl >= n || pt != p.law && !(pt == 0 || pt == 8) {
			continue
		}
		payload := buf[hl:n]
		if pt != p.law { // the other law: convert
			out := make([]byte, len(payload))
			for i, v := range payload {
				out[i] = encode(decode(v, pt), p.law)
			}
			payload = out
		}
		p.mu.Lock()
		busy := len(p.out) > 0
		p.mu.Unlock()
		if !busy {
			_, _ = p.ch.Write(payload)
		}
	}
}

// openRTP makes the call's audio socket on this computer.
func (p *port) openRTP(remote netip.AddrPort) (*net.UDPConn, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.rtp != nil {
		p.rtp.Close()
	}
	p.rtp, p.remote = conn, remote
	p.mu.Unlock()
	go p.receive(conn)
	return conn, nil
}

func (p *port) closeRTP() {
	p.mu.Lock()
	conn, dev := p.rtp, p.dev
	p.rtp, p.remote, p.dev = nil, netip.AddrPort{}, nil
	p.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
	if dev != nil {
		dev.Close()
	}
}

// devEndpoint is the loopback endpoint the web-phone bridge joins to the engine, with this port's
// audio socket as its far end.
func (p *port) devEndpoint() (*media.Endpoint, error) {
	ep, err := p.b.pool.Allocate()
	if err != nil {
		return nil, err
	}
	conn, err := p.openRTP(netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(ep.Port())))
	if err != nil {
		ep.Close()
		return nil, err
	}
	ep.SetRemote("127.0.0.1", conn.LocalAddr().(*net.UDPAddr).Port)
	ep.Start()
	p.mu.Lock()
	p.dev = ep
	p.mu.Unlock()
	return ep, nil
}

// hangup ends whatever call the port has.
func (p *port) hangup() {
	p.mu.Lock()
	c, lc := p.call, p.line
	p.call, p.line, p.digits = nil, nil, ""
	if p.timer != nil {
		p.timer.Stop()
	}
	p.mu.Unlock()
	if c != nil {
		c.Hangup()
	}
	if lc != nil {
		lc.end(true)
	}
	p.closeRTP()
}

func (p *port) event(ev int) {
	if p.conf.Kind == "bchan" {
		return // a PRI B-channel: its signalling is on the D-channel
	}
	if p.conf.Kind == "phone" {
		p.phoneEvent(ev)
	} else if p.isEM() {
		p.emEvent(ev)
	} else if p.isR2() {
		if ev == EventBitsChanged {
			p.r2Bits()
		}
	} else {
		p.lineEvent(ev)
	}
}

// Phones (FXS ports).

func (p *port) phoneEvent(ev int) {
	p.mu.Lock()
	st, c := p.state, p.call
	p.mu.Unlock()
	switch ev {
	case EventRingOffHook: // lifted
		switch st {
		case stIdle:
			p.set(stDialTone)
			p.det = Detector{}
			_ = p.ch.Tone(ToneDial)
		case stRinging:
			ep, err := p.devEndpoint()
			if err == nil && c != nil {
				err = c.Answer(ep, p.law, 101)
			}
			if err != nil {
				p.hangup()
				p.set(stFinished)
				_ = p.ch.Tone(ToneCongestion)
				return
			}
			p.set(stTalking)
		}
	case EventOnHook:
		p.hangup()
		_ = p.ch.Tone(ToneStop)
		p.set(stIdle)
	case EventAlarm:
		p.mu.Lock()
		p.err = "the card reports an alarm"
		p.mu.Unlock()
	}
}

func (p *port) key(k byte) {
	p.mu.Lock()
	st := p.state
	if st == stTalking {
		p.mu.Unlock()
		p.sendKey(k)
		return
	}
	if st != stDialTone && st != stDialling {
		p.mu.Unlock()
		return
	}
	first := st == stDialTone
	p.state = stDialling
	if p.timer != nil {
		p.timer.Stop()
	}
	if k != '#' {
		p.digits += string(k)
		p.timer = time.AfterFunc(DigitTimeout, p.dial)
	}
	p.mu.Unlock()
	if first {
		_ = p.ch.Tone(ToneStop)
	}
	if k == '#' {
		go p.dial()
	}
}

func (p *port) dial() {
	p.mu.Lock()
	if p.state != stDialling || p.call != nil || p.phone == nil {
		p.mu.Unlock()
		return
	}
	number, ph := p.digits, p.phone
	p.state = stCalling
	p.mu.Unlock()
	if number == "" {
		p.set(stFinished)
		_ = p.ch.Tone(ToneCongestion)
		return
	}
	ep, err := p.devEndpoint()
	var c *webphone.Call
	if err == nil {
		c, err = ph.Dial(number, ep, p.law, 101)
	}
	if err != nil {
		p.closeRTP()
		p.set(stFinished)
		_ = p.ch.Tone(ToneCongestion)
		return
	}
	p.mu.Lock()
	p.call = c
	p.mu.Unlock()
}

func (p *port) phoneEvents(ph *webphone.Phone) {
	for {
		select {
		case <-p.b.ctx.Done():
			return
		case ev := <-ph.Events:
			p.phoneCallEvent(ph, ev)
		}
	}
}

func (p *port) phoneCallEvent(ph *webphone.Phone, ev webphone.Event) {
	p.mu.Lock()
	st := p.state
	mine := p.call != nil && p.call.ID == ev.CallID
	p.mu.Unlock()
	switch ev.Type {
	case "incoming":
		c := ph.Find(ev.CallID)
		if st != stIdle || c == nil {
			if c != nil {
				c.Hangup() // busy here: the extension's other phones still ring
			}
			return
		}
		p.mu.Lock()
		p.state, p.call = stRinging, c
		p.mu.Unlock()
		_ = p.ch.Hook(HookRing)
	case "ringing":
		if mine {
			_ = p.ch.Tone(ToneRingback)
		}
	case "answered":
		if mine && st == stCalling {
			_ = p.ch.Tone(ToneStop)
			p.set(stTalking)
		}
	case "ended", "failed":
		if !mine {
			return
		}
		p.mu.Lock()
		p.call = nil
		p.mu.Unlock()
		p.closeRTP()
		if st == stRinging {
			_ = p.ch.Hook(RingOff)
			p.set(stIdle)
			return
		}
		p.set(stFinished)
		t := ToneCongestion
		if ev.Type == "failed" && strings.HasPrefix(ev.Reason, "486") {
			t = ToneBusy
		}
		_ = p.ch.Tone(t)
	}
}

// DTMF generation, for dialling on lines.
func dtmfFrames(number string, law int, lead bool) [][]byte {
	var pcm []int16
	silence := make([]int16, 8*60)
	if lead {
		pcm = append(pcm, make([]int16, 8*500)...) // wait for the exchange's dial tone
	}
	for i := 0; i < len(number); i++ {
		var r, c float64
		for ri, row := range keys {
			for ci, k := range row {
				if k == number[i] {
					r, c = rowFreqs[ri], colFreqs[ci]
				}
			}
		}
		if r == 0 {
			if number[i] == ',' {
				pcm = append(pcm, make([]int16, 8*1000)...)
			}
			continue
		}
		for n := 0; n < 8*90; n++ {
			t := float64(n) / 8000
			pcm = append(pcm, int16(7000*(math.Sin(2*math.Pi*r*t)+math.Sin(2*math.Pi*c*t))))
		}
		pcm = append(pcm, silence...)
	}
	var out [][]byte
	for len(pcm) > 0 {
		n := min(FrameBytes, len(pcm))
		f := make([]byte, FrameBytes)
		for i := range f {
			v := int16(0)
			if i < n {
				v = pcm[i]
			}
			f[i] = encode(v, law)
		}
		out = append(out, f)
		pcm = pcm[n:]
	}
	return out
}
