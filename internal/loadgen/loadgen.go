// Package loadgen is BitVoice's built-in call generator: it places real SIP calls into BitVoice from this
// computer, with real audio, to measure how many calls it carries and how well (setup time, audio loss),
// and to check after a change that calls still go where they should. It only calls numbers BitVoice
// answers itself or rings locally, never trunks or emergency numbers.
package loadgen

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// Plan is one run.
type Plan struct {
	From, FromSecret string // the extension the calls come from
	To               string // the number dialled
	// AnswerAs, when set, registers this extension too and answers the calls it is rung with, sending the
	// audio back, so calls to a person can be tested without a person.
	AnswerAs, AnswerSecret string
	Calls                  int           // how many in all
	Rate                   float64       // new calls per second
	Concurrent             int           // at most this many at once
	Hold                   time.Duration // how long each answered call lasts
	PBX                    netip.AddrPort
	// Token lets these calls past the per-extension call limit; BitVoice accepts it only from this computer.
	Token string
}

func (p *Plan) host() string { return p.PBX.Addr().String() }

// Limits keep a run from swamping a production system by mistake.
const (
	MaxCalls      = 10000
	MaxConcurrent = 2000 // the media port range (10000 to 20000 by default) holds about 2,500 relayed calls
	MaxRate       = 50
	MaxHold       = time.Hour
)

// Check returns what is wrong with a plan, in words.
func (p *Plan) Check() error {
	switch {
	case p.From == "" || p.To == "":
		return errors.New("choose who calls and the number to call")
	case p.Calls < 1 || p.Calls > MaxCalls:
		return fmt.Errorf("calls must be 1 to %d", MaxCalls)
	case p.Concurrent < 1 || p.Concurrent > MaxConcurrent:
		return fmt.Errorf("calls at once must be 1 to %d", MaxConcurrent)
	case p.Rate <= 0 || p.Rate > MaxRate:
		return fmt.Errorf("calls per second must be above 0 and at most %d", MaxRate)
	case p.Hold < 0 || p.Hold > MaxHold:
		return errors.New("each call may last up to an hour")
	}
	return nil
}

// Result is a run's progress and outcome; Snapshot copies it.
type Result struct {
	Running  bool           `json:"running"`
	Started  time.Time      `json:"started"`
	Finished time.Time      `json:"finished,omitzero"`
	Plan     PlanInfo       `json:"plan"`
	Placed   int            `json:"placed"`
	Answered int            `json:"answered"`
	Failed   int            `json:"failed"`
	Active   int            `json:"active"`
	Failures map[string]int `json:"failures"` // "486 Busy Here": 3
	SetupMS  Spread         `json:"setupMs"`
	Sent     int64          `json:"sent"`
	Received int64          `json:"received"`
	LossPct  float64        `json:"lossPct"`
	MOS      float64        `json:"mos"`
	Stopped  bool           `json:"stopped"`
	answers  []float64
}

// PlanInfo is the plan as shown on screen.
type PlanInfo struct {
	From       string  `json:"from"`
	To         string  `json:"to"`
	AnswerAs   string  `json:"answerAs"`
	Calls      int     `json:"calls"`
	Rate       float64 `json:"rate"`
	Concurrent int     `json:"concurrent"`
	HoldSecs   int     `json:"holdSeconds"`
}

// Spread is a set of timings.
type Spread struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	Max float64 `json:"max"`
}

// Run is a run in progress.
type Run struct {
	plan       Plan
	mu         sync.Mutex
	res        Result
	cancel     context.CancelFunc
	done       chan struct{}
	stack      *sip.Stack
	answer     *sip.Stack
	sent       atomic.Int64
	recv       atomic.Int64
	ended      map[string]chan struct{} // calls ended by BitVoice, by Call-ID
	stopStacks context.CancelFunc
	emu        sync.Mutex
}

// Start begins a run.
func Start(p Plan) (*Run, error) {
	if err := p.Check(); err != nil {
		return nil, err
	}
	// ctx ends the run (Stop); the SIP stacks live on until the last call has hung up, so they can still
	// hear the answers to the hang-ups.
	ctx, cancel := context.WithCancel(context.Background())
	sctx, scancel := context.WithCancel(context.Background())
	r := &Run{plan: p, cancel: cancel, done: make(chan struct{}), ended: map[string]chan struct{}{}, stopStacks: scancel}
	r.res = Result{Running: true, Started: time.Now(), Failures: map[string]int{},
		Plan: PlanInfo{p.From, p.To, p.AnswerAs, p.Calls, p.Rate, p.Concurrent, int(p.Hold.Seconds())}}
	r.stack = sip.NewStack(r.handle)
	r.stack.Host = p.host()
	if err := r.stack.Listen(net.JoinHostPort(p.host(), "0")); err != nil {
		cancel()
		scancel()
		return nil, err
	}
	r.stack.Serve(sctx)
	if p.AnswerAs != "" {
		r.answer = sip.NewStack(r.handleAnswer)
		r.answer.Host = p.host()
		if err := r.answer.Listen(net.JoinHostPort(p.host(), "0")); err != nil {
			r.stack.Close()
			cancel()
			scancel()
			return nil, err
		}
		r.answer.Serve(sctx)
		if code := r.register(p.AnswerAs, p.AnswerSecret, r.answer, 300); code != 200 {
			r.stack.Close()
			r.answer.Close()
			cancel()
			scancel()
			return nil, fmt.Errorf("extension %s could not register to answer (%d)", p.AnswerAs, code)
		}
	}
	go r.loop(ctx)
	return r, nil
}

// Stop ends the run early; calls in progress are hung up.
func (r *Run) Stop() {
	r.mu.Lock()
	r.res.Stopped = true
	r.mu.Unlock()
	r.cancel()
}

// Done is closed when the run has finished.
func (r *Run) Done() <-chan struct{} { return r.done }

// Snapshot returns the progress so far.
func (r *Run) Snapshot() Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.res
	out.Failures = map[string]int{}
	for k, v := range r.res.Failures {
		out.Failures[k] = v
	}
	out.answers = nil
	out.Sent, out.Received = r.sent.Load(), r.recv.Load()
	if out.Sent > 0 {
		out.LossPct = math.Max(0, 100*(1-float64(out.Received)/float64(out.Sent)))
	}
	if len(r.res.answers) > 0 {
		s := slices.Clone(r.res.answers)
		slices.Sort(s)
		out.SetupMS = Spread{P50: s[len(s)/2], P95: s[min(len(s)-1, len(s)*95/100)], Max: s[len(s)-1]}
	}
	if out.Answered > 0 {
		out.MOS = estimateMOS(out.LossPct)
	}
	return out
}

// estimateMOS is the E-model's score for G.711 on a LAN with the measured loss (ITU-T G.107, Ie,eff with
// Bpl 4.3 for random loss), as a guide rather than a measurement.
func estimateMOS(loss float64) float64 {
	ie := 95 * loss / (loss + 4.3)
	rf := 93.2 - ie
	if rf < 0 {
		return 1
	}
	return math.Round((1+0.035*rf+7e-6*rf*(rf-60)*(100-rf))*100) / 100
}

func (r *Run) loop(ctx context.Context) {
	defer close(r.done)
	defer func() {
		r.stopStacks()
		r.stack.Close()
		if r.answer != nil {
			r.answer.Close()
		}
		r.mu.Lock()
		r.res.Running, r.res.Finished = false, time.Now()
		r.mu.Unlock()
	}()
	gap := time.Duration(float64(time.Second) / r.plan.Rate)
	slots := make(chan struct{}, r.plan.Concurrent)
	var wg sync.WaitGroup
	tick := time.NewTicker(gap)
	defer tick.Stop()
	for i := 0; i < r.plan.Calls; i++ {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case slots <- struct{}{}:
		}
		if i > 0 {
			select {
			case <-ctx.Done():
				wg.Wait()
				return
			case <-tick.C:
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			r.call(ctx)
		}()
	}
	wg.Wait()
}

func (r *Run) fail(code int, reason string) {
	r.mu.Lock()
	r.res.Failed++
	key := strconv.Itoa(code) + " " + reason
	if code == 0 {
		key = reason
	}
	r.res.Failures[key]++
	r.mu.Unlock()
}

// send runs a transaction, answering one digest challenge.
func (r *Run) send(st *sip.Stack, m *sip.Message, user, secret string) *sip.Message {
	resp := final(st.Request(m.Clone(), sip.Peer{Transport: "UDP", Addr: r.plan.PBX}))
	if resp != nil && (resp.StatusCode == 401 || resp.StatusCode == 407) {
		h, hdr := resp.Get("WWW-Authenticate"), "Authorization"
		if resp.StatusCode == 407 {
			h, hdr = resp.Get("Proxy-Authenticate"), "Proxy-Authorization"
		}
		ch, err := sip.ParseChallenge(h)
		if err != nil {
			return resp
		}
		cr, _ := sip.Answer(ch, user, secret, m.Method, m.RequestURI)
		next := m.Clone()
		n, method := m.CSeq()
		next.Set("CSeq", strconv.FormatUint(uint64(n+1), 10)+" "+method)
		next.Set(hdr, cr.String())
		resp = final(st.Request(next, sip.Peer{Transport: "UDP", Addr: r.plan.PBX}))
	}
	return resp
}

func final(tx *sip.ClientTx) *sip.Message {
	var f *sip.Message
	for m := range tx.Responses {
		if m.StatusCode >= 200 {
			f = m
		}
	}
	return f
}

func (r *Run) register(user, secret string, st *sip.Stack, expires int) int {
	m := &sip.Message{IsRequest: true, Method: sip.REGISTER, RequestURI: "sip:" + r.plan.PBX.Addr().String()}
	m.Set("From", "<sip:"+user+"@127.0.0.1>;tag="+sip.NewTag())
	m.Set("To", "<sip:"+user+"@127.0.0.1>")
	m.Set("Call-ID", sip.NewCallID())
	m.Set("CSeq", "1 REGISTER")
	m.Set("Contact", fmt.Sprintf("<sip:%s@%s>", user, net.JoinHostPort(r.plan.host(), strconv.Itoa(st.Port()))))
	m.Set("Expires", strconv.Itoa(expires))
	m.Set("User-Agent", "BitVoice call generator")
	resp := r.send(st, m, user, secret)
	if resp == nil {
		return 408
	}
	return resp.StatusCode
}

// call places one call, holds it with audio, and hangs up.
func (r *Run) call(ctx context.Context) {
	rtp, err := net.ListenUDP("udp", &net.UDPAddr{IP: r.plan.PBX.Addr().AsSlice()})
	if err != nil {
		r.fail(0, "no media port")
		return
	}
	defer rtp.Close()
	p := r.plan
	tag, callID := sip.NewTag(), sip.NewCallID()
	from := "<sip:" + p.From + "@127.0.0.1>;tag=" + tag
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:" + p.To + "@127.0.0.1"}
	inv.Set("From", from)
	inv.Set("To", "<sip:"+p.To+"@127.0.0.1>")
	inv.Set("Call-ID", callID)
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", fmt.Sprintf("<sip:%s@%s>", p.From, net.JoinHostPort(p.host(), strconv.Itoa(r.stack.Port()))))
	inv.Set("User-Agent", "BitVoice call generator")
	inv.Set("X-BitVoice-Load", p.Token)
	inv.Set("Content-Type", "application/sdp")
	inv.Body = sdp.New(p.host(), rtp.LocalAddr().(*net.UDPAddr).Port, 1, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 101).Bytes()
	ended := make(chan struct{})
	r.emu.Lock()
	r.ended[callID] = ended
	r.emu.Unlock()
	defer func() { r.emu.Lock(); delete(r.ended, callID); r.emu.Unlock() }()

	r.mu.Lock()
	r.res.Placed++
	r.res.Active++
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.res.Active--; r.mu.Unlock() }()
	t0 := time.Now()
	resp := r.send(r.stack, inv, p.From, p.FromSecret)
	if resp == nil {
		r.fail(408, "No Answer From BitVoice")
		return
	}
	if resp.StatusCode >= 300 {
		r.fail(resp.StatusCode, resp.Reason)
		return
	}
	setup := float64(time.Since(t0).Microseconds()) / 1000
	r.mu.Lock()
	r.res.Answered++
	r.res.answers = append(r.res.answers, setup)
	r.mu.Unlock()

	ct, _ := sip.ParseAddress(resp.Get("Contact"))
	target := inv.RequestURI
	if ct != nil && ct.URI != nil {
		target = ct.URI.String()
	}
	n, _ := resp.CSeq()
	ack := &sip.Message{IsRequest: true, Method: sip.ACK, RequestURI: target}
	ack.Set("From", from)
	ack.Set("To", resp.Get("To"))
	ack.Set("Call-ID", callID)
	ack.Set("CSeq", strconv.FormatUint(uint64(n), 10)+" ACK")
	_ = r.stack.Stateless(ack, sip.Peer{Transport: "UDP", Addr: p.PBX})

	var to netip.AddrPort
	if s, err := sdp.Parse(resp.Body); err == nil && s.Audio() != nil {
		if ip, err := netip.ParseAddr(s.Connection(s.Audio())); err == nil {
			to = netip.AddrPortFrom(ip, uint16(s.Audio().Port))
		}
	}
	r.media(ctx, rtp, to, ended)

	select {
	case <-ended:
		return // BitVoice hung up first
	default:
	}
	bye := &sip.Message{IsRequest: true, Method: sip.BYE, RequestURI: target}
	bye.Set("From", from)
	bye.Set("To", resp.Get("To"))
	bye.Set("Call-ID", callID)
	bye.Set("CSeq", strconv.FormatUint(uint64(n+1), 10)+" BYE")
	r.send(r.stack, bye, p.From, p.FromSecret)
}

// media sends a 1 kHz tone at the real rate for the hold time and counts what comes back.
func (r *Run) media(ctx context.Context, conn *net.UDPConn, to netip.AddrPort, ended chan struct{}) {
	if !to.IsValid() || r.plan.Hold == 0 {
		return
	}
	go func() {
		buf := make([]byte, 2048)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(r.plan.Hold + 2*time.Second))
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n >= 12 && buf[1]&0x7f != 101 {
				r.recv.Add(1)
			}
		}
	}()
	payload := make([]byte, 160)
	for i := range payload {
		payload[i] = media.LinearToUlaw(int16(4000 * math.Sin(2*math.Pi*1000*float64(i)/8000)))
	}
	ssrc := uint32(time.Now().UnixNano())
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	stop := time.After(r.plan.Hold)
	for seq := uint16(0); ; seq++ {
		select {
		case <-ctx.Done():
			return
		case <-ended:
			return
		case <-stop:
			time.Sleep(200 * time.Millisecond) // let the last packets come back
			return
		case <-tick.C:
		}
		pkt := make([]byte, 12, 172)
		pkt[0], pkt[1] = 0x80, 0
		binary.BigEndian.PutUint16(pkt[2:], seq)
		binary.BigEndian.PutUint32(pkt[4:], uint32(seq)*160)
		binary.BigEndian.PutUint32(pkt[8:], ssrc)
		if _, err := conn.WriteToUDPAddrPort(append(pkt, payload...), to); err == nil {
			r.sent.Add(1)
		}
	}
}

// handle answers requests BitVoice sends to the calling side (BYE when the far end hangs up).
func (r *Run) handle(tx *sip.ServerTx, in *sip.Incoming) {
	if tx == nil {
		return
	}
	m := in.Msg
	tx.Respond(sip.NewResponse(m, 200, ""))
	if m.Method == sip.BYE {
		r.emu.Lock()
		if ch, ok := r.ended[m.CallID()]; ok {
			close(ch)
			delete(r.ended, m.CallID())
		}
		r.emu.Unlock()
	}
}

// handleAnswer is the answering extension: it picks up at once and sends back what it hears.
func (r *Run) handleAnswer(tx *sip.ServerTx, in *sip.Incoming) {
	if tx == nil {
		return
	}
	m := in.Msg
	switch m.Method {
	case sip.INVITE:
		to, _ := sip.ParseAddress(m.Get("To"))
		if to != nil && to.Tag() != "" { // re-INVITE: keep going
			tx.Respond(sip.NewResponse(m, 200, ""))
			return
		}
		offer, err := sdp.Parse(m.Body)
		if err != nil || offer.Audio() == nil {
			tx.Respond(sip.NewResponse(m, 488, ""))
			return
		}
		conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: r.plan.PBX.Addr().AsSlice()})
		if err != nil {
			tx.Respond(sip.NewResponse(m, 503, ""))
			return
		}
		pt := 0
		if cs := offer.Audio().Codecs(); len(cs) > 0 && media.Transcodable(media.CodecPT(cs[0].Name)) {
			pt = cs[0].PT
		}
		resp := sip.NewResponse(m, 200, "")
		resp.Set("To", m.Get("To")+";tag="+sip.NewTag())
		resp.Set("Contact", fmt.Sprintf("<sip:%s@%s>", r.plan.AnswerAs, net.JoinHostPort(r.plan.host(), strconv.Itoa(r.answer.Port()))))
		resp.Set("Content-Type", "application/sdp")
		resp.Body = sdp.New(r.plan.host(), conn.LocalAddr().(*net.UDPAddr).Port, 2,
			[]sdp.Codec{{PT: pt, Name: map[int]string{0: "PCMU", 8: "PCMA", 9: "G722"}[pt], ClockRate: 8000}}, 101).Bytes()
		tx.Respond(resp)
		go echo(conn, r.plan.Hold+30*time.Second)
	default:
		tx.Respond(sip.NewResponse(m, 200, ""))
	}
}

// echo sends every packet back until the line is quiet for five seconds or the limit passes.
func echo(conn *net.UDPConn, limit time.Duration) {
	defer conn.Close()
	end := time.Now().Add(limit)
	buf := make([]byte, 2048)
	for time.Now().Before(end) {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, from, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		_, _ = conn.WriteToUDPAddrPort(buf[:n], from)
	}
}
