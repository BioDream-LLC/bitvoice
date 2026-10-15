package dahdi

import (
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/mf"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// E&M trunks: the channels of a T1 span set up as E&M (robbed-bit signalling, e&m in
// /etc/dahdi/system.conf), each a line of the card trunk. The driver turns the signalling bits into
// hook states and events; the start signal and the number are handled here: wink start (the far end
// winks when it is ready for the number), immediate start, or delay dial; the number dialled as DTMF
// or MF R1 (KP digits ST); Feature Group D sends the caller's number (ANI) too, as KP ANI ST KP DNIS ST.

// E&M start signals.
const (
	StartWink      = "wink"
	StartImmediate = "immediate"
	StartDelay     = "delay"
	StartFGD       = "fgd"
)

// E&M timers.
var (
	WinkTime    = 5 * time.Second        // waiting for the far end's wink
	WinkDelay   = 100 * time.Millisecond // between seeing a seizure and winking back
	PreDial     = 300 * time.Millisecond // immediate start: off hook to the first digit
	EMDigitWait = 4 * time.Second        // waiting for the next digit of a number coming in
)

func (p *port) isEM() bool { return p.conf.Kind == "em" }

func (p *port) emMF() bool { return p.conf.Address == "mf" || p.conf.Start == StartFGD }

// emEvent handles the far end's signalling on an E&M channel.
func (p *port) emEvent(ev int) {
	p.mu.Lock()
	st, lc := p.state, p.line
	waiting := p.waitEv
	p.mu.Unlock()
	if waiting != nil {
		select {
		case waiting <- ev:
		default:
		}
	}
	switch ev {
	case EventRingOffHook:
		if st == stIdle {
			p.set(stRinging)
			go p.emIncoming()
		}
	case EventOnHook:
		if lc != nil && (st == stTalking || st == stCalling || st == stRinging || st == stDialling) {
			p.mu.Lock()
			p.line = nil
			p.mu.Unlock()
			lc.end(true)
			p.closeRTP()
			p.set(stIdle)
		} else if st == stDialling || st == stRinging {
			p.emReset()
		}
	case EventAlarm:
		p.mu.Lock()
		p.err = "the card reports an alarm on the span"
		p.mu.Unlock()
	}
}

func (p *port) emReset() {
	p.mu.Lock()
	p.digits = ""
	if p.timer != nil {
		p.timer.Stop()
	}
	if p.digitsDone != nil {
		close(p.digitsDone)
		p.digitsDone = nil
	}
	p.state = stIdle
	p.mu.Unlock()
	_ = p.ch.Hook(HookOn)
	p.set(stIdle)
}

// emIncoming answers a seizure: the start signal, then the number, then a call to the engine.
func (p *port) emIncoming() {
	switch p.conf.Start {
	case StartImmediate:
	case StartDelay:
		// Delay dial: off hook until ready, then on hook: "send the number now".
		_ = p.ch.Hook(HookOff)
		time.Sleep(200 * time.Millisecond)
		_ = p.ch.Hook(HookOn)
	default: // wink, Feature Group D
		time.Sleep(WinkDelay)
		_ = p.ch.Hook(HookWink)
	}
	done := make(chan struct{})
	p.mu.Lock()
	p.digits, p.digitsDone = "", done
	p.mfrx = mf.NewR1Receiver()
	p.det = Detector{}
	p.state = stDialling
	p.timer = time.AfterFunc(EMDigitWait*2, func() { p.emDigitsDone(done) })
	p.mu.Unlock()
	<-done
	p.mu.Lock()
	got := p.digits
	stillOn := p.state == stDialling
	p.mu.Unlock()
	if !stillOn {
		return
	}
	ani, dnis := "", got
	if p.emMF() {
		ani, dnis = splitMF(got)
	}
	if dnis == "" {
		dnis = p.conf.Number // nothing dialled: the line's own number, as an analogue line
	}
	p.set(stRinging)
	p.callEngine(ani, dnis, "", func() {
		_ = p.ch.Hook(HookOff) // answer supervision: the far end sees the call answered
	})
}

// splitMF reads what MF digits carried: "K ani S K dnis S" (Feature Group D) or "K dnis S".
func splitMF(s string) (ani, dnis string) {
	var fields []string
	for _, part := range strings.Split(s, "K") {
		part = strings.TrimRight(part, "SABC")
		if part != "" {
			fields = append(fields, part)
		}
	}
	switch len(fields) {
	case 0:
		return "", ""
	case 1:
		return "", fields[0]
	}
	ani = fields[len(fields)-2]
	if len(ani) > 10 && strings.HasPrefix(ani, "00") {
		ani = ani[2:] // the information digits before the ANI
	} else if len(ani) == 12 {
		ani = ani[2:]
	}
	return ani, fields[len(fields)-1]
}

// emAudio takes the number coming in, digit by digit.
func (p *port) emAudio(pcm []int16) {
	p.mu.Lock()
	if p.state != stDialling || p.digitsDone == nil {
		p.mu.Unlock()
		return
	}
	var got string
	if p.emMF() {
		got = p.mfrx.Feed(pcm)
	} else {
		got = string(p.det.Feed(pcm))
	}
	done := p.digitsDone
	end := false
	for _, c := range []byte(got) {
		if c == '#' && !p.emMF() {
			end = true
			break
		}
		p.digits += string(c)
		// MF: the number ends with ST; with Feature Group D, the second ST.
		if c == 'S' && (p.conf.Start != StartFGD || strings.Count(p.digits, "S") >= 2) {
			end = true
		}
	}
	if got != "" && !end {
		if p.timer != nil {
			p.timer.Stop()
		}
		p.timer = time.AfterFunc(EMDigitWait, func() { p.emDigitsDone(done) })
	}
	p.mu.Unlock()
	if end {
		p.emDigitsDone(done)
	}
}

func (p *port) emDigitsDone(done chan struct{}) {
	p.mu.Lock()
	if p.digitsDone != done {
		p.mu.Unlock()
		return
	}
	p.digitsDone = nil
	if p.timer != nil {
		p.timer.Stop()
	}
	p.mu.Unlock()
	close(done)
}

// emOut places the engine's call on an E&M channel: seize, wait for the start signal, send the
// number, and answer when the far end does.
func (b *Bridge) emOut(p *port, tx *sip.ServerTx, m *sip.Message, number, ani string) {
	conn, err := p.openRTP(remoteOf(m.Body))
	if err != nil {
		p.set(stIdle)
		tx.Respond(sip.NewResponse(m, 503, ""))
		return
	}
	lc := &lineCall{p: p, callID: m.CallID(), invite: tx, cseq: 1000}
	lc.localURI = m.Get("To") + ";tag=" + sip.NewTag()
	lc.remoteURI = m.Get("From")
	if ct, err := sip.ParseAddress(m.Get("Contact")); err == nil && ct.URI != nil {
		lc.target = ct.URI.String()
	}
	evs := make(chan int, 8)
	p.mu.Lock()
	p.line, p.waitEv = lc, evs
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		if p.waitEv == evs {
			p.waitEv = nil
		}
		p.mu.Unlock()
	}()
	fail := func(code int, reason string) {
		p.lineDone()
		r := sip.NewResponse(m, code, reason)
		r.Set("To", lc.localURI)
		tx.Respond(r)
	}
	tx.NoAck = func() { p.lineDone() }
	if err := p.ch.Hook(HookOff); err != nil {
		fail(503, "Line Not Available")
		return
	}
	wait := func(want int, d time.Duration) bool {
		t := time.After(d)
		for {
			select {
			case ev := <-evs:
				if ev == want {
					return true
				}
				if ev == EventOnHook && want != EventOnHook {
					return false
				}
			case <-t:
				return false
			}
		}
	}
	switch p.conf.Start {
	case StartImmediate:
		time.Sleep(PreDial)
	case StartDelay:
		// The far end goes off hook while it gets ready, and back on hook when it is.
		if wait(EventRingOffHook, 2*time.Second) && !wait(EventOnHook, WinkTime) {
			fail(503, "No Start Dial Signal")
			return
		}
	default:
		if !wait(EventWinkFlash, WinkTime) {
			fail(503, "No Wink")
			return
		}
		time.Sleep(70 * time.Millisecond)
	}
	var sound [][]byte
	switch {
	case p.conf.Start == StartFGD:
		sound = pcmFrames(mf.R1Digits("K00"+ani+"SK"+number+"S"), p.law)
	case p.conf.Address == "mf":
		sound = pcmFrames(mf.R1Digits("K"+number+"S"), p.law)
	default:
		sound = dtmfFrames(number, p.law, false)
	}
	r := sip.NewResponse(m, 183, "")
	r.Set("To", lc.localURI)
	r.Set("Contact", b.contact())
	r.Set("Content-Type", "application/sdp")
	r.Body = p.description(conn).Bytes()
	tx.Respond(r)
	p.mu.Lock()
	p.out = append(p.out, sound...)
	p.mu.Unlock()
	// Answer supervision: the far end goes off hook when the called party answers.
	t := time.After(3 * time.Minute)
	for {
		select {
		case ev := <-evs:
			switch ev {
			case EventRingOffHook:
				lc.mu.Lock()
				if lc.ended {
					lc.mu.Unlock()
					return
				}
				lc.answered = true
				lc.mu.Unlock()
				ok := sip.NewResponse(m, 200, "")
				ok.Set("To", lc.localURI)
				ok.Set("Contact", b.contact())
				ok.Set("Content-Type", "application/sdp")
				ok.Body = p.description(conn).Bytes()
				tx.Respond(ok)
				p.set(stTalking)
				return
			case EventOnHook:
				return // the far end gave up: emEvent ended the call
			}
		case <-t:
			fail(480, "No Answer")
			return
		}
		p.mu.Lock()
		gone := p.line != lc
		p.mu.Unlock()
		if gone {
			return
		}
	}
}

// pcmFrames turns linear audio into 20 ms G.711 frames.
func pcmFrames(pcm []int16, law int) [][]byte {
	var out [][]byte
	for i := 0; i < len(pcm); i += FrameBytes {
		f := make([]byte, FrameBytes)
		for j := range f {
			v := int16(0)
			if i+j < len(pcm) {
				v = pcm[i+j]
			}
			f[j] = encode(v, law)
		}
		out = append(out, f)
	}
	return out
}

// callEngine calls the engine for a call arriving on a line (E&M, R2): from the caller's number to
// the one dialled; onAnswer runs when the engine answers.
func (p *port) callEngine(from, to, name string, onAnswer func()) {
	b := p.b
	conn, err := p.openRTP(netip.AddrPort{})
	if err != nil {
		p.emReset()
		return
	}
	if to == "" {
		to = "s"
	}
	if from == "" {
		from = "anonymous"
	}
	lc := &lineCall{p: p, callID: sip.NewCallID(), inbound: true, cseq: 1}
	lc.localURI = sipDisplay(name) + "<sip:" + from + "@127.0.0.1>;tag=" + sip.NewTag()
	lc.remoteURI = "<sip:" + to + "@127.0.0.1>"
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:" + to + "@" + b.engine().Addr.String()}
	inv.Set("From", lc.localURI)
	inv.Set("To", lc.remoteURI)
	inv.Set("Call-ID", lc.callID)
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", b.contact())
	inv.Set("Content-Type", "application/sdp")
	inv.Body = p.description(conn).Bytes()
	p.mu.Lock()
	p.line = lc
	p.mu.Unlock()
	tx := b.lines.Request(inv, b.engine())
	lc.mu.Lock()
	lc.client = tx
	lc.mu.Unlock()
	var fin *sip.Message
	for r := range tx.Responses {
		if r.StatusCode >= 200 {
			fin = r
		}
	}
	if fin == nil || fin.StatusCode >= 300 {
		p.mu.Lock()
		mine := p.line == lc
		if mine {
			p.line = nil
		}
		p.mu.Unlock()
		if mine {
			p.closeRTP()
			p.emReset()
		}
		return
	}
	lc.mu.Lock()
	lc.remoteURI = fin.Get("To")
	if ct, err := sip.ParseAddress(fin.Get("Contact")); err == nil && ct.URI != nil {
		lc.target = ct.URI.String()
	}
	lc.answered = true
	ended := lc.ended
	lc.mu.Unlock()
	p.mu.Lock()
	p.remote = remoteOf(fin.Body)
	p.mu.Unlock()
	n, _ := fin.CSeq()
	ack := lc.request(sip.ACK)
	ack.Set("CSeq", strconv.FormatUint(uint64(n), 10)+" ACK")
	_ = b.lines.Stateless(ack, b.engine())
	tx.SetAck(ack)
	if ended {
		return
	}
	onAnswer()
	p.set(stTalking)
}
