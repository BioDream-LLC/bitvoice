package pbx

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/prompts"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// screenRig is BitVoice with a provider (a test phone at the trunk's address) calling in to
// +12125550100, which rings extension 101; the robot test's words are recorded.
type screenRig struct {
	e       *Engine
	rec     *memRecorder
	carrier *phone
	ps      []*phone   // 100, 101, 102
	dialing sync.Mutex // one call in at a time: the provider's number is the test phone's user
}

func newScreenRig(t *testing.T, sc config.Screening, setup ...func(*Engine)) *screenRig {
	t.Helper()
	carrier := newPhone(t, 0, "carrier", "")
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{{ID: "t1", Name: "Carrier", Enabled: true, Host: "127.0.0.1", Port: carrier.stack.Port()}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "Main", DID: "+12125550100", Destination: config.Destination{Type: "extension", Target: "101"}}}
	cfg.System.Screening = sc
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	words := fakePrompts{prompts.Box("en", "screen-press"): make([]int16, 1600), prompts.Box("en", "screen-connecting"): make([]int16, 800)}
	for d := '0'; d <= '9'; d++ {
		words[prompts.Box("en", "digit-"+string(d))] = make([]int16, 800)
	}
	e, rec := startEngine(t, cfg, append([]func(*Engine){func(e *Engine) { e.Mail = promptMail{words} }}, setup...)...)
	carrier.pbx = sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	return &screenRig{e: e, rec: rec, carrier: carrier, ps: registered(t, e, "100", "101", "102")}
}

// call is a call in from the provider, from a number.
func (r *screenRig) call(from string) (*dialog, int) {
	r.dialing.Lock()
	defer r.dialing.Unlock()
	r.carrier.user = from
	return r.carrier.dial("+12125550100")
}

// digitOf is the key the robot test asks the caller from a number to press.
func (r *screenRig) digitOf(t *testing.T, from string) byte {
	t.Helper()
	deadline := time.Now().Add(patient(2 * time.Second))
	for time.Now().Before(deadline) {
		for _, ci := range r.e.Calls() {
			if ci.From == from && ci.call != nil {
				ci.call.mu.Lock()
				d := ci.call.screenDigit
				ci.call.mu.Unlock()
				if d != 0 {
					return d
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no robot test for %s", from)
	return 0
}

func noRing(t *testing.T, p *phone, what string) {
	t.Helper()
	if tx := p.waitRing(300 * time.Millisecond); tx != nil {
		t.Fatalf("%s rang %s", what, p.user)
	}
}

// Unknown callers take the robot test: the key it names puts them through, anything else ends the
// call. Known, allowed and emergency call-back callers ring at once.
func TestScreeningRobotTest(t *testing.T) {
	var known sync.Map
	known.Store("3125550199", "in the contacts as Dana Reyes")
	r := newScreenRig(t, config.Screening{Unknown: "test", Allowed: []config.ScreenNumber{{Number: "3125550177"}}}, func(e *Engine) {
		e.KnownCaller = func(n string) string {
			if v, ok := known.Load(n); ok {
				return v.(string)
			}
			return ""
		}
	})
	desk := r.ps[1]

	// An unknown caller is answered by screening, nothing rings until the right key.
	d, code := r.call("3125550142")
	if code != 200 {
		t.Fatalf("robot test answered %d", code)
	}
	digit := r.digitOf(t, "3125550142")
	noRing(t, desk, "an unknown caller before the test")
	var seq uint16 = 1
	d.press(&seq, string(digit))
	ring := desk.waitRing(3 * time.Second)
	if ring == nil {
		t.Fatal("the right key did not put the caller through")
	}
	desk.send(desk.answer(ring).request("BYE"))
	if c := r.rec.find(t, "the passed call", func(c store.CDR) bool { return c.From == "3125550142" }); c.Screened != "passed" ||
		!strings.Contains(strings.Join(c.Why, " "), "robot test") {
		t.Fatalf("record %+v", c)
	}

	// The wrong key twice: the call ends and nothing rings.
	for len(r.carrier.byes) > 0 {
		<-r.carrier.byes
	}
	d, code = r.call("3125550143")
	if code != 200 {
		t.Fatalf("robot test answered %d", code)
	}
	wrong := byte('1')
	if r.digitOf(t, "3125550143") == '1' {
		wrong = '2'
	}
	d.press(&seq, string(wrong))
	time.Sleep(100 * time.Millisecond)
	d.press(&seq, string(wrong))
	select {
	case <-r.carrier.byes:
	case <-time.After(patient(15 * time.Second)):
		t.Fatal("a caller who failed the test was not hung up")
	}
	noRing(t, desk, "a failed caller")
	if c := r.rec.find(t, "the failed call", func(c store.CDR) bool { return c.From == "3125550143" }); c.Screened != "failed" {
		t.Fatalf("record %+v", c)
	}

	// Known and always-allowed callers ring at once.
	for _, n := range []string{"3125550199", "3125550177"} {
		go r.call(n)
		ring := desk.waitRing(2 * time.Second)
		if ring == nil {
			t.Fatalf("%s did not ring at once", n)
		}
		ring.Respond(sip.NewResponse(ring.Req, 486, "Busy Here"))
	}
	// Within an hour of an emergency call nothing is held up.
	r.e.lastEmergency.Store(time.Now().Unix())
	go r.call("3125550144")
	if ring := desk.waitRing(2 * time.Second); ring == nil {
		t.Fatal("a possible emergency call-back was given the robot test")
	} else {
		ring.Respond(sip.NewResponse(ring.Req, 486, "Busy Here"))
	}
}

// With "send", unknown callers go straight to the place chosen; with the test's words not recorded,
// the test is not given (a caller could not pass it) and Health is told.
func TestScreeningSendUnknownAndMissingWords(t *testing.T) {
	r := newScreenRig(t, config.Screening{Unknown: "send", UnknownTo: &config.Destination{Type: "extension", Target: "102"}})
	go r.call("3125550142")
	ring := r.ps[2].waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("the unknown caller was not sent to 102")
	}
	ring.Respond(sip.NewResponse(ring.Req, 486, "Busy Here"))
	noRing(t, r.ps[1], "a caller sent elsewhere")
	if c := r.rec.find(t, "the sent call", func(c store.CDR) bool { return c.From == "3125550142" }); c.Screened != "sent" {
		t.Fatalf("record %+v", c)
	}

}

func TestScreeningTestNeedsItsWords(t *testing.T) {
	r := newScreenRig(t, config.Screening{Unknown: "test"}, func(e *Engine) { e.Mail = promptMail{fakePrompts{}} })
	cfg := r.e.Config()
	if miss := r.e.ScreeningPromptsMissing(cfg); len(miss) != 10 || miss[0] != "screen-press" {
		t.Fatalf("missing %v", miss)
	}
	go r.call("3125550145")
	ring := r.ps[1].waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("with no words recorded the call should ring as usual")
	}
	ring.Respond(sip.NewResponse(ring.Req, 486, "Busy Here"))
}

// The spam database is asked about unknown callers only: what it calls spam is labelled or refused,
// and a broken database never holds a call up.
func TestScreeningSpamDatabase(t *testing.T) {
	var asked atomic.Int32
	db := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		asked.Add(1)
		if u, p, _ := req.BasicAuth(); u != "AC1" || p != "tok" {
			w.WriteHeader(401)
			return
		}
		// Twilio Lookup with the Nomorobo add-on: add_ons.results.nomorobo_spamscore.result.score.
		score := "0"
		if strings.Contains(req.URL.Path, "%2B13125550666") || strings.Contains(req.URL.Path, "+13125550666") {
			score = "1"
		}
		if strings.Contains(req.URL.Path, "13125550500") {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"phone_number":"x","add_ons":{"status":"successful","results":{"nomorobo_spamscore":{"status":"successful","result":{"status":"success","message":"success","score":` + score + `}}}}}`))
	}))
	t.Cleanup(db.Close)
	rep := config.Reputation{URL: db.URL + "/v1/PhoneNumbers/{e164}?AddOns=nomorobo_spamscore", Username: "AC1", Password: "tok",
		Field: "add_ons.results.nomorobo_spamscore.result.score", Action: "label"}
	r := newScreenRig(t, config.Screening{Reputation: rep}, func(e *Engine) {
		e.KnownCaller = func(n string) string {
			if n == "3125550199" {
				return "called from here"
			}
			return ""
		}
	})
	desk := r.ps[1]
	ringFrom := func(n string) string {
		t.Helper()
		go r.call(n)
		ring := desk.waitRing(2 * time.Second)
		if ring == nil {
			t.Fatalf("%s did not ring", n)
		}
		ring.Respond(sip.NewResponse(ring.Req, 486, "Busy Here"))
		return ring.Req.Get("From")
	}
	if from := ringFrom("3125550666"); !strings.Contains(from, "Spam?") {
		t.Fatalf("spam not labelled: %s", from)
	}
	if from := ringFrom("3125550142"); strings.Contains(from, "Spam?") {
		t.Fatalf("a clean number was labelled: %s", from)
	}
	n := asked.Load()
	ringFrom("3125550199") // known: not looked up (each lookup costs money)
	ringFrom("3125550666") // asked before: from the cache
	if asked.Load() != n {
		t.Fatalf("looked up %d more times", asked.Load()-n)
	}
	ringFrom("3125550500") // the database fails: the call rings as usual

	cfg := r.e.Config().Clone()
	cfg.System.Screening.Reputation.Action = "block"
	cfg.System.Screening.Reputation.CacheHours = -1
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	r.e.Apply(cfg)
	if _, code := r.call("3125550666"); code != 603 {
		t.Fatalf("spam with block: %d, want 603", code)
	}
	if spam, said, err := r.e.TestReputation(t.Context(), &cfg, "+1 312 555 0666"); err != nil || !spam || !strings.Contains(said, "1") {
		t.Fatalf("test button: %v %q %v", spam, said, err)
	}
}

// The block-last-caller code puts the last outside caller who rang the extension on the block list.
func TestBlockLastCallerCode(t *testing.T) {
	var got []string
	var mu sync.Mutex
	r := newScreenRig(t, config.Screening{}, func(e *Engine) {
		e.BlockNumber = func(n, by, note string) error {
			mu.Lock()
			got = append(got, n+"|"+by)
			mu.Unlock()
			if n == "3125550177" {
				return ErrAlwaysAllowed
			}
			return nil
		}
	})
	desk := r.ps[1]
	// Nobody rang yet: nothing to block.
	if d, code := desk.dial("*32"); code != 200 {
		t.Fatalf("*32 answered %d", code)
	} else {
		_ = d
		select {
		case <-desk.byes:
		case <-time.After(patient(5 * time.Second)):
			t.Fatal("*32 did not hang up")
		}
	}
	go r.call("3125550666")
	ring := desk.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("no ring")
	}
	ring.Respond(sip.NewResponse(ring.Req, 486, "Busy Here"))
	if _, code := desk.dial("*32"); code != 200 {
		t.Fatalf("*32 answered %d", code)
	}
	deadline := time.Now().Add(patient(2 * time.Second))
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "3125550666|extension 101" {
		t.Fatalf("blocked %v", got)
	}
	// Another extension's last caller is its own.
	if _, _, ok := r.e.LastCallerOf("102"); ok {
		t.Fatal("102 was never rung from outside")
	}
}

// A 911 call is never refused, held up or marked as spam by screening, even from a blocked or withheld number: the
// block list and the robot test are for office lines, not for people calling for help.
func TestScreeningNeverStops911(t *testing.T) {
	cfg := testConfig()
	cfg.System.Screening = config.Screening{Anonymous: "block", Unknown: "test", Blocked: []config.ScreenNumber{{Number: "3125559999"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Log: slog.New(slog.DiscardHandler)}
	trunk := &config.Trunk{Name: "ESInet"}
	for _, from := range []string{"3125559999", "anonymous"} {
		m := &sip.Message{IsRequest: true, Method: sip.INVITE}
		m.Set("From", "<sip:"+from+"@esinet.test>")
		c := &Call{from: from}
		if why := e.screenCall(&cfg, c, m, trunk, "911", true); why != "" {
			t.Fatalf("%s: a 911 call was refused (%s)", from, why)
		}
		if why := e.screenUnknown(&cfg, c, trunk, "911", true); why != "" || c.unknownAct != "" {
			t.Fatalf("%s: a 911 call was screened (%q %q)", from, why, c.unknownAct)
		}
		if c.screened != "" {
			t.Fatalf("%s: a 911 call marked %q for the call-taker", from, c.screened)
		}
	}
	// The same caller on an office line is refused.
	m := &sip.Message{IsRequest: true, Method: sip.INVITE}
	m.Set("From", "<sip:3125559999@carrier.test>")
	if why := e.screenCall(&cfg, &Call{from: "3125559999"}, m, &config.Trunk{Name: "Carrier"}, "+12125550100", false); why == "" {
		t.Fatal("a blocked number on an office line rang")
	}
}
