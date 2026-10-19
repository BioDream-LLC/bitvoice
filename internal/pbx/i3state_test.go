package pbx

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/i3"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// fakePSAPs is a SIP server standing for two PSAPs behind one trunk: it takes SUBSCRIBEs to their
// state events, sends the NOTIFYs a test asks for, and answers calls, saying which PSAP took each.
type fakePSAPs struct {
	t      *testing.T
	stack  *sip.Stack
	mu     sync.Mutex
	subs   map[string]*sip.Message // "east emergency-QueueState" -> the SUBSCRIBE
	tags   map[string]string       // the same key -> our tag
	calls  chan string
	refuse map[string]int // event -> status to answer SUBSCRIBE with
	seq    uint32
}

func newFakePSAPs(t *testing.T) *fakePSAPs {
	f := &fakePSAPs{t: t, subs: map[string]*sip.Message{}, tags: map[string]string{}, calls: make(chan string, 8), refuse: map[string]int{}}
	f.stack = sip.NewStack(f.handle)
	f.stack.Host = "127.0.0.1"
	f.stack.T1, f.stack.T2 = 50*time.Millisecond, 200*time.Millisecond
	if err := f.stack.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.stack.Serve(ctx)
	t.Cleanup(func() { cancel(); f.stack.Close() })
	return f
}

func (f *fakePSAPs) handle(tx *sip.ServerTx, in *sip.Incoming) {
	if tx == nil {
		return
	}
	m := in.Msg
	switch m.Method {
	case sip.SUBSCRIBE:
		ev := m.Get("Event")
		f.mu.Lock()
		code := f.refuse[ev]
		f.mu.Unlock()
		if code != 0 {
			tx.Respond(sip.NewResponse(m, code, ""))
			return
		}
		key := userOf(m.RequestURI) + " " + ev
		tag := sip.NewTag()
		f.mu.Lock()
		if old, ok := f.tags[key]; ok && strings.Contains(m.Get("To"), "tag=") {
			tag = old
		}
		f.subs[key], f.tags[key] = m, tag
		f.mu.Unlock()
		r := sip.NewResponse(m, 200, "")
		if !strings.Contains(m.Get("To"), "tag=") {
			r.Set("To", m.Get("To")+";tag="+tag)
		}
		r.Set("Contact", "<sip:psap@127.0.0.1:"+strconv.Itoa(f.stack.Port())+">")
		r.Set("Expires", m.Get("Expires"))
		tx.Respond(r)
	case sip.INVITE:
		f.calls <- userOf(m.RequestURI)
		tx.Respond(sip.NewResponse(m, 486, "Busy Here")) // which PSAP was tried is all a test needs
	default:
		tx.Respond(sip.NewResponse(m, 200, ""))
	}
}

// subscribed waits for BitVoice's SUBSCRIBE for a PSAP user and event.
func (f *fakePSAPs) subscribed(user, ev string) *sip.Message {
	f.t.Helper()
	var m *sip.Message
	waitFor(f.t, "no SUBSCRIBE for "+user+" "+ev, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		m = f.subs[user+" "+ev]
		return m != nil
	})
	return m
}

// notify sends a NOTIFY in the subscription for a PSAP user and event; it returns the answer's code.
func (f *fakePSAPs) notify(e *Engine, user, ev, body, state string) int {
	f.t.Helper()
	sub := f.subscribed(user, ev)
	f.mu.Lock()
	tag := f.tags[user+" "+ev]
	f.seq++
	seq := f.seq
	f.mu.Unlock()
	ct, _ := sip.ParseAddress(sub.Get("Contact"))
	n := &sip.Message{IsRequest: true, Method: sip.NOTIFY, RequestURI: ct.URI.String()}
	n.Set("From", sub.Get("To")+";tag="+tag)
	n.Set("To", sub.Get("From"))
	n.Set("Call-ID", sub.CallID())
	n.Set("CSeq", strconv.FormatUint(uint64(seq), 10)+" NOTIFY")
	n.Set("Event", ev)
	n.Set("Subscription-State", state)
	n.Set("Content-Type", "application/EmergencyCallData.queuestate+json")
	n.Body = []byte(body)
	r := finalOf(f.stack.Request(n, sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}))
	if r == nil {
		return 0
	}
	return r.StatusCode
}

// The ESRP subscribes to each PSAP's queue and service state over its trunk; what they say decides:
// a full queue or a downed service passes the PSAP over for its overflow, back again when it is
// Active; a policy rule's queue state condition sees the same; every PSAP saying no still gets the
// call; a PSAP refusing the subscription is retried later, one not answering is Unreachable.
func TestESRPWatchesPSAPStates(t *testing.T) {
	esinet, psaps := newCarrier(t), newFakePSAPs(t)
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{
		{ID: "in", Name: "ESInet", Enabled: true, Host: "127.0.0.1", Port: esinet.stack.Port(), ESInet: true, AllowFrom: []string{"127.0.0.1"}},
		{ID: "out", Name: "PSAPs", Enabled: true, Host: "127.0.0.1", Port: psaps.stack.Port()},
	}
	cfg.System.Core = config.Core{WatchStates: true, PSAPs: []config.CorePSAP{
		{URI: "sip:east@east.example.gov", Name: "East", Trunk: "out", Overflow: "sip:west@west.example.gov"},
		{URI: "sip:west@west.example.gov", Name: "West", Trunk: "out"},
	}, Default: "sip:east@east.example.gov"}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "911", Trunk: "in", Destination: config.Destination{Type: "esrp"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg, func(e *Engine) {
		e.FindService = func(string, *i3.Location) (string, string) { return "sip:east@east.example.gov", "" }
	})
	for _, u := range []string{"east", "west"} {
		for _, ev := range []string{"emergency-QueueState", "emergency-ServiceState"} {
			m := psaps.subscribed(u, ev)
			if m.Get("Expires") != "3600" || !strings.Contains(m.Get("Accept"), "EmergencyCallData") {
				t.Fatalf("SUBSCRIBE %s %s: %s", u, ev, m.String())
			}
		}
	}
	tried := func() []string {
		pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
		inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "urn:service:sos"}
		inv.Set("From", "<sip:+17175550100@esinet.test>;tag="+sip.NewTag())
		inv.Set("To", "<urn:service:sos>")
		inv.Set("Call-ID", sip.NewCallID())
		inv.Set("CSeq", "1 INVITE")
		inv.Set("Contact", "<sip:esrp@127.0.0.1:"+strconv.Itoa(esinet.stack.Port())+">")
		inv.Set("Geolocation", "<cid:loc@esinet.test>")
		inv.Set("Content-Type", "multipart/mixed;boundary=B")
		inv.Body = []byte("--B\r\nContent-Type: application/sdp\r\n\r\nv=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 40030 RTP/AVP 0\r\n\r\n--B\r\nContent-Type: application/pidf+xml\r\nContent-ID: <loc@esinet.test>\r\n\r\n" + testPIDF + "\r\n--B--\r\n")
		tx := esinet.stack.Request(inv, pbx)
		finalOf(tx)
		var got []string
		for {
			select {
			case u := <-psaps.calls:
				got = append(got, u)
			case <-time.After(300 * time.Millisecond):
				return got
			}
		}
	}
	if got := tried(); len(got) < 1 || got[0] != "east" {
		t.Fatalf("before any state: %v", got)
	}
	queue := func(state string, n, mx int) string {
		return `{"queueState":{"queueUri":"sip:east@east.example.gov","queueLength":` + strconv.Itoa(n) + `,"queueMaxLength":` + strconv.Itoa(mx) + `,"state":"` + state + `"}}`
	}
	if code := psaps.notify(e, "east", "emergency-QueueState", queue("Full", 5, 5), "active;expires=3500"); code != 200 {
		t.Fatalf("NOTIFY answered %d", code)
	}
	if got := tried(); len(got) < 1 || got[0] != "west" || contains(got, "east") {
		t.Fatalf("East full: %v, want West only", got)
	}
	psaps.notify(e, "east", "emergency-QueueState", queue("Active", 1, 5), "active;expires=3500")
	if got := tried(); len(got) < 1 || got[0] != "east" {
		t.Fatalf("East active again: %v", got)
	}
	psaps.notify(e, "east", "emergency-ServiceState", `{"service":{"name":"PSAP","domain":"east.example.gov"},"serviceState":{"state":"Down","reason":"power"},"securityPosture":{"posture":"Yellow","reason":""}}`, "active;expires=3500")
	if got := tried(); len(got) < 1 || got[0] != "west" {
		t.Fatalf("East down: %v", got)
	}
	if s := e.watchedService("east.example.gov", true); s != "Yellow" {
		t.Fatalf("posture by service id: %q", s)
	}
	psaps.notify(e, "west", "emergency-QueueState", `{"queueState":{"queueUri":"sip:west@west.example.gov","queueLength":0,"queueMaxLength":3,"state":"Disabled"}}`, "active;expires=3500")
	if got := tried(); len(got) < 1 || got[0] != "east" {
		t.Fatalf("every PSAP said no: still sent as listed, got %v", got)
	}
	st := e.PSAPStates()
	if len(st) != 2 || st[0].Service != "Down" || st[0].Reason != "power" || st[1].Queue != "Disabled" || st[1].Max != 3 || st[0].Subscriptions[evQueueState] != "active" {
		t.Fatalf("states %+v", st)
	}
	if code := psaps.notify(e, "east", "emergency-QueueState", "{not json", "active"); code != 400 {
		t.Fatalf("a bad body: %d", code)
	}
	// The policy rules' queue state condition reads the same.
	if s := e.queueState(e.Config(), "sip:west@west.example.gov"); s != "Disabled" {
		t.Fatalf("queueState %q", s)
	}
	// Switched off: the subscriptions end (Expires: 0) and nothing is kept.
	cfg2 := *e.Config()
	cfg2.System.Core.WatchStates = false
	e.Apply(cfg2)
	waitFor(t, "states kept after switching off", func() bool { e.watchTick(); return len(e.PSAPStates()[0].Subscriptions) == 0 })
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// This server as a notifier: an allowed element subscribes to its queue, service and element
// state and gets NOTIFYs in NENA's shapes (checked against NENA's notify-body schemas when
// BITVOICE_TEST_NENA is set), with the queue's length as calls wait; others get 603 Decline, a
// queue that is not this server's 488, and with States off 489.
func TestStatesNotifier(t *testing.T) {
	cfg := testConfig()
	cfg.System.Core = config.Core{States: true, AllowFrom: []string{"127.0.0.1"}, QueueURI: "sip:sos@esrp.example.gov", Source: "esrp.example.gov", QueueMax: 2}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	got := make(chan *sip.Message, 16)
	el := sip.NewStack(func(tx *sip.ServerTx, in *sip.Incoming) {
		if tx != nil {
			got <- in.Msg
			tx.Respond(sip.NewResponse(in.Msg, 200, ""))
		}
	})
	el.Host = "127.0.0.1"
	if err := el.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	el.Serve(ctx)
	defer func() { cancel(); el.Close() }()
	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	sub := func(user, ev string) int {
		m := &sip.Message{IsRequest: true, Method: sip.SUBSCRIBE, RequestURI: "sip:" + user + "@127.0.0.1:" + strconv.Itoa(e.SIPPort())}
		m.Set("From", "<sip:esrp@upstream.example.gov>;tag="+sip.NewTag())
		m.Set("To", "<sip:"+user+"@esrp.example.gov>")
		m.Set("Call-ID", sip.NewCallID())
		m.Set("CSeq", "1 SUBSCRIBE")
		m.Set("Contact", "<sip:esrp@127.0.0.1:"+strconv.Itoa(el.Port())+">")
		m.Set("Event", ev)
		m.Set("Expires", "600")
		r := finalOf(el.Request(m, pbx))
		if r == nil {
			return 0
		}
		return r.StatusCode
	}
	next := func(ev string) map[string]any {
		t.Helper()
		for {
			select {
			case m := <-got:
				if !strings.EqualFold(m.Get("Event"), ev) {
					continue
				}
				if !strings.HasPrefix(m.Get("Subscription-State"), "active") || !strings.HasPrefix(m.Get("Content-Type"), "application/EmergencyCallData.") {
					t.Fatalf("NOTIFY %s", m.String())
				}
				checkNENABody(t, ev, m.Body)
				var v map[string]any
				if err := json.Unmarshal(m.Body, &v); err != nil {
					t.Fatal(err)
				}
				return v
			case <-time.After(patient(2 * time.Second)):
				t.Fatalf("no NOTIFY for %s", ev)
			}
		}
	}
	if code := sub("sos", "emergency-QueueState"); code != 200 {
		t.Fatalf("subscribe queue %d", code)
	}
	q := next("emergency-QueueState")["queueState"].(map[string]any)
	if q["queueUri"] != "sip:sos@esrp.example.gov" || q["queueLength"] != float64(0) || q["queueMaxLength"] != float64(2) || q["state"] != "Active" {
		t.Fatalf("queue %v", q)
	}
	if code := sub("sos", "emergency-ServiceState"); code != 200 {
		t.Fatal(code)
	}
	if s := next("emergency-ServiceState"); s["serviceState"].(map[string]any)["state"] != "Normal" || s["service"].(map[string]any)["domain"] != "esrp.example.gov" {
		t.Fatalf("service %v", s)
	}
	if code := sub("anything", "emergency-ElementState"); code != 200 {
		t.Fatal(code)
	}
	if s := next("emergency-ElementState"); s["elementState"].(map[string]any)["state"] != "Normal" {
		t.Fatalf("element %v", s)
	}
	if code := sub("other-queue", "emergency-QueueState"); code != 488 {
		t.Fatalf("another queue: %d", code)
	}
	// Two 911 calls waiting fill the queue (most 2): its subscribers hear Full.
	cfg2 := *e.Config()
	cfg2.System.Core.States = true
	for i := 0; i < 2; i++ {
		c := &Call{ID: "w" + strconv.Itoa(i), esinet: true, answered: make(chan struct{})}
		e.mu.Lock()
		e.calls[c.ID] = c
		e.mu.Unlock()
		defer func() { // stand-ins, not real calls: gone before the engine stops
			e.mu.Lock()
			delete(e.calls, c.ID)
			e.mu.Unlock()
		}()
	}
	e.statesChanged(false)
	for {
		q := next("emergency-QueueState")["queueState"].(map[string]any)
		if q["state"] == "Full" && q["queueLength"] == float64(2) {
			break
		}
	}
	cfg2.System.Core.AllowFrom = []string{"10.0.0.1"}
	e.Apply(cfg2)
	if code := sub("sos", "emergency-QueueState"); code != 603 {
		t.Fatalf("not allowed: %d", code)
	}
	cfg2.System.Core.States = false
	e.Apply(cfg2)
	if code := sub("sos", "emergency-QueueState"); code != 489 {
		t.Fatalf("states off: %d", code)
	}
}

// checkNENABody validates a NOTIFY body against NENA's schema (notify-bodies in the NENA911
// repositories), when BITVOICE_TEST_NENA names the directory with jschema.js.
func checkNENABody(t *testing.T, ev string, body []byte) {
	t.Helper()
	dir := os.Getenv("BITVOICE_TEST_NENA")
	if dir == "" {
		return
	}
	file := map[string]string{"emergency-queuestate": "i3-queue-state-notify-body.json", "emergency-servicestate": "i3-service-state-notify-body.json",
		"emergency-elementstate": "i3-element-state-notify-body.json"}[strings.ToLower(ev)]
	cmd := exec.Command("node", filepath.Join(dir, "jschema.js"), filepath.Join(dir, "..", "nena", "notify-bodies", file))
	cmd.Stdin = strings.NewReader(string(body))
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("%s body against NENA's schema: %s %v\n%s", ev, out, err, body)
	}
}
