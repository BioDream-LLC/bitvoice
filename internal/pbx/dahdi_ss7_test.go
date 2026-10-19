package pbx

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/dahdi"
	"github.com/biodream-llc/bitvoice/internal/isdn"
	"github.com/biodream-llc/bitvoice/internal/ss7"
)

// An E1 run as SS7 on the card: the carrier's switch (a BitVoice ss7.SS7 with the point codes the
// other way round) calls the DID on a circuit, which reaches 101 with the caller ID; 101 answers and
// the switch hears ANM; 101 hangs up; then 101 calls out, and the switch gets an IAM with the
// number and 101's caller ID, answers and releases.
func TestTelephonyCardSS7(t *testing.T) {
	ss7.T4E, ss7.Fill = 200*time.Millisecond, 10*time.Millisecond
	cfg := testConfig()
	cfg.System.Cards = config.Cards{Enabled: true, Token: "abcdefghijklmnopqrstuvwxyz012345", LinePort: 25396, ALaw: true,
		Spans: []config.CardSpan{{Type: "e1", First: 1, Signalling: "ss7", OPC: "1-10-2", APC: "1-10-1", FirstCIC: 33}}}
	cfg.Trunks = []config.Trunk{{ID: "cards", Name: "SS7", Enabled: true, Protocol: "dahdi"}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "Main", DID: "2125550100", Destination: config.Destination{Type: "extension", Target: "101"}}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "Out", Patterns: []string{"9."}, Strip: 1, Trunks: []string{"cards"}}}
	cfg.Extensions[1].CallerID = "2125550101"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	sp := cfg.System.Cards.Spans[0]
	if sp.Variant != "itu" || sp.NI != "national" {
		t.Fatalf("defaults %+v", sp)
	}
	opc, _ := config.PointCode(sp.OPC, false)
	apc, _ := config.PointCode(sp.APC, false)
	if opc != 1<<11|10<<3|2 {
		t.Fatalf("point code %d", opc)
	}
	bs, d := sp.Channels()
	e, _ := startEngine(t, cfg)
	bridgeEnd, farEnd := dahdi.NewFakeD()
	dahdi.OpenD = func(n int) (isdn.Link, error) {
		if n != d {
			return nil, errors.New("not the signalling link")
		}
		return bridgeEnd, nil
	}
	fakes := map[int]*dahdi.Fake{}
	var cics []int
	for i, ch := range bs {
		fakes[ch] = dahdi.NewFake()
		cics = append(cics, 33+i)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b, err := dahdi.New(ctx, dahdi.Config{SIPPort: e.SIPPort(), LinePort: 25396, ALaw: true,
		Spans: []dahdi.SpanConf{{D: d, B: bs, SS7: &dahdi.SS7Conf{OPC: opc, APC: apc, NI: 2, FirstCIC: 33}}}},
		func(n int, _ bool) (dahdi.Channel, error) { return fakes[n], nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	sw := ss7.New(farEnd, ss7.Config{MTPConfig: ss7.MTPConfig{OPC: apc, APC: opc, NI: 2, Emergency: true}, CICs: cics})
	incoming := make(chan *ss7.Call, 2)
	sw.Incoming = func(c *ss7.Call) { incoming <- c }
	go sw.Run()
	t.Cleanup(sw.Close)
	waitFor(t, "the SS7 link set did not come up", func() bool { return strings.HasPrefix(b.Status()["span1"], "SS7 link set up") })

	callee := registered(t, e, "101")[0]
	in, err := sw.Dial(ss7.Setup{Called: "2125550100", Calling: "3125550199"})
	if err != nil {
		t.Fatal(err)
	}
	ring := callee.waitRing(4 * time.Second)
	if ring == nil {
		t.Fatalf("the DID did not reach 101 (%v)", b.Status())
	}
	if !strings.Contains(ring.Req.Get("From"), "3125550199") {
		t.Fatalf("caller ID lost: %s", ring.Req.Get("From"))
	}
	waitSS7(t, in, "alerting")
	cd := callee.answer(ring)
	waitSS7(t, in, "connected")
	ch := bs[in.CIC-33]
	before := fakes[ch].Wrote()
	cd.sendRTP(20)
	waitFor(t, "101's audio did not reach the circuit", func() bool { return fakes[ch].Wrote() > before })
	callee.send(cd.request("BYE"))
	waitSS7(t, in, "released")

	codes := make(chan int, 1)
	go func() { _, code := callee.dial("93125550199"); codes <- code }()
	var out *ss7.Call
	select {
	case out = <-incoming:
	case <-time.After(15 * time.Second):
		t.Fatalf("no IAM (%v)", b.Status())
	}
	if out.Called != "3125550199" || out.Calling != "2125550101" {
		t.Fatalf("IAM called %q calling %q", out.Called, out.Calling)
	}
	out.Alerting()
	time.Sleep(100 * time.Millisecond)
	out.Answer()
	if code := <-codes; code != 200 {
		t.Fatalf("the call out was answered with %d", code)
	}
	out.Hangup(ss7.CauseNormal)
	callee.waitBye(t, 3*time.Second)
	waitFor(t, "the circuits are not free", func() bool { return len(sw.Busy()) == 0 })
}

func waitSS7(t *testing.T, c *ss7.Call, kind string) {
	t.Helper()
	timeout := time.After(15 * time.Second)
	for {
		select {
		case e := <-c.Events:
			if e.Kind == kind {
				return
			}
		case <-timeout:
			t.Fatalf("no %s on CIC %d", kind, c.CIC)
		}
	}
}

// cutD is a signalling link that can be cut (as a span losing its line): nothing goes out, and what
// comes in is lost.
type cutD struct {
	isdn.Link
	cut atomic.Bool
}

func (c *cutD) ReadFrame() ([]byte, error) {
	for {
		f, err := c.Link.ReadFrame()
		if err != nil || !c.cut.Load() {
			return f, err
		}
	}
}

func (c *cutD) WriteFrame(f []byte) error {
	if c.cut.Load() {
		return nil
	}
	return c.Link.WriteFrame(f)
}

// Two E1 spans with the same point codes are one SS7 link set (each span's signalling link with its
// own code): the switch calls on a circuit of the second span; the first span's line is cut in the
// middle of the call: its link changes over, the call goes on and is released over the other link,
// and calls still come in on both spans' circuits.
func TestTelephonyCardSS7LinkSet(t *testing.T) {
	ss7.T4E, ss7.Fill = 200*time.Millisecond, 10*time.Millisecond
	ss7.SLTWait, ss7.SLTPeriod = 300*time.Millisecond, 500*time.Millisecond
	ss7.T2MTP3, ss7.T1MTP3, ss7.T4MTP3 = 300*time.Millisecond, 100*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { ss7.SLTWait, ss7.SLTPeriod = 4*time.Second, 30*time.Second })
	cfg := testConfig()
	cfg.System.Cards = config.Cards{Enabled: true, Token: "abcdefghijklmnopqrstuvwxyz012345", LinePort: 25397, ALaw: true,
		Spans: []config.CardSpan{
			{Type: "e1", First: 1, Signalling: "ss7", OPC: "1-10-2", APC: "1-10-1", SLC: 0, FirstCIC: 1},
			{Type: "e1", First: 32, Signalling: "ss7", OPC: "1-10-2", APC: "1-10-1", SLC: 1, FirstCIC: 33}}}
	cfg.Trunks = []config.Trunk{{ID: "cards", Name: "SS7", Enabled: true, Protocol: "dahdi"}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "Main", DID: "2125550100", Destination: config.Destination{Type: "extension", Target: "101"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := cfg
	bad.System.Cards.Spans = append([]config.CardSpan(nil), cfg.System.Cards.Spans...)
	bad.System.Cards.Spans[1].SLC, bad.System.Cards.Spans[1].FirstCIC = 0, 20
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "signalling link code 0") || !strings.Contains(err.Error(), "circuit 20 is also on span 1") {
		t.Fatalf("one link set with a code and circuits twice: %v", err)
	}
	opc, _ := config.PointCode("1-10-2", false)
	apc, _ := config.PointCode("1-10-1", false)
	e, _ := startEngine(t, cfg)
	var spans []dahdi.SpanConf
	fakes := map[int]*dahdi.Fake{}
	links := map[int]*cutD{}
	var far []*cutD
	var cics []int
	for i, sp := range cfg.System.Cards.Spans {
		bs, d := sp.Channels()
		bridgeEnd, farEnd := dahdi.NewFakeD()
		links[d] = &cutD{Link: bridgeEnd}
		far = append(far, &cutD{Link: farEnd})
		for j, ch := range bs {
			fakes[ch] = dahdi.NewFake()
			cics = append(cics, sp.FirstCIC+j)
		}
		spans = append(spans, dahdi.SpanConf{D: d, B: bs, SS7: &dahdi.SS7Conf{OPC: opc, APC: apc, NI: 2, SLC: i, FirstCIC: sp.FirstCIC}})
	}
	dahdi.OpenD = func(n int) (isdn.Link, error) {
		if l := links[n]; l != nil {
			return l, nil
		}
		return nil, errors.New("not a signalling link")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b, err := dahdi.New(ctx, dahdi.Config{SIPPort: e.SIPPort(), LinePort: 25397, ALaw: true, Spans: spans},
		func(n int, _ bool) (dahdi.Channel, error) { return fakes[n], nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	sw := ss7.NewLinkSet(ss7.Config{MTPConfig: ss7.MTPConfig{OPC: apc, APC: opc, NI: 2}, CICs: cics})
	for i, l := range far {
		go sw.AddLink(l, i)
	}
	t.Cleanup(sw.Close)
	waitFor(t, "the link set did not come up with both links", func() bool {
		return strings.Contains(b.Status()["span1"], "2 of 2 links in service") && strings.Contains(b.Status()["span2"], "2 of 2 links in service")
	})
	callee := registered(t, e, "101")[0]
	// A call in: the switch takes its own circuits (odd ones, its point code being the lower) and the
	// first free is CIC 1, on span 1; then 3 ... so take several to reach span 2's.
	var calls []*ss7.Call
	var span2 *ss7.Call
	for span2 == nil {
		c, err := sw.Dial(ss7.Setup{Called: "2125550100", Calling: "3125550199"})
		if err != nil {
			t.Fatal(err)
		}
		calls = append(calls, c)
		if c.CIC > 32 {
			span2 = c
		}
		if len(calls) > 20 {
			t.Fatal("no circuit of span 2 taken")
		}
	}
	var dialogs []*dialog
	for range calls {
		ring := callee.waitRing(4 * time.Second)
		if ring == nil {
			t.Fatalf("a call did not reach 101 (%v)", b.Status())
		}
		dialogs = append(dialogs, callee.answer(ring))
	}
	waitSS7(t, span2, "connected")
	// Span 1's line is cut: its link changes over; the call on span 2 goes on and is released.
	links[spans[0].D].cut.Store(true)
	far[0].cut.Store(true)
	waitFor(t, "span 1's link did not change over", func() bool { return strings.Contains(b.Status()["span1"], "this span's link changed over") })
	if !strings.HasPrefix(b.Status()["span2"], "SS7 link set up (1 of 2 links in service") {
		t.Fatalf("span 2: %s", b.Status()["span2"])
	}
	span2.Hangup(ss7.CauseNormal)
	callee.waitBye(t, 3*time.Second)
	waitSS7(t, span2, "released")
	for _, c := range calls[:len(calls)-1] {
		c.Hangup(ss7.CauseNormal)
	}
	waitFor(t, "the circuits are not free", func() bool { return len(sw.Busy()) == 0 })
	// The line is back: the link changes back into service.
	links[spans[0].D].cut.Store(false)
	far[0].cut.Store(false)
	waitFor(t, "span 1's link did not come back", func() bool { return strings.Contains(b.Status()["span1"], "2 of 2 links in service") })
	_ = dialogs
}
