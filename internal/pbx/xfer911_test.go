package pbx

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/loadgen"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// A call-taker blind-transfers an answered 911 call and hangs up at once (as most desk phones do after
// REFER). When the transfer cannot connect, the call is never dropped and never handed to voicemail:
// every call-taker rings.
func TestEmergencyTransferNeverDropped(t *testing.T) {
	for _, tc := range []struct {
		name  string
		phone bool // 101 has a phone connected (that does not answer)
		why   string
	}{
		{"no phone connected", false, "which has no phone connected"},
		{"nobody answers, voicemail set", true, "Nobody answered the transfer to"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			esinet := newCarrier(t)
			cfg := esinetConfig(esinet.stack.Port(), config.InboundRoute{Name: "911", Trunk: "in", Destination: config.Destination{Type: "extension", Target: "100"}})
			cfg.Extensions[1].Voicemail = config.Voicemail{Enabled: true, PIN: "2468", MaxSeconds: 60}
			cfg.Extensions[1].RingSeconds = 2
			e, rec := startEngine(t, cfg)
			nums := []string{"100", "102"}
			if tc.phone {
				nums = append(nums, "101")
			}
			ps := registered(t, e, nums...)
			taker, other := ps[0], ps[1]
			tx, _, _ := esinet911(t, e, esinet)
			ring := taker.waitRing(5 * time.Second)
			if ring == nil {
				t.Fatal("the call-taker did not ring")
			}
			d := taker.answer(ring)
			if r := finalOf(tx); r == nil || r.StatusCode != 200 {
				t.Fatalf("answer %v", r)
			}
			refer := d.request(sip.REFER)
			refer.Set("Refer-To", "<sip:101@127.0.0.1>")
			refer.Set("Referred-By", "<sip:100@127.0.0.1>")
			if r := taker.send(refer); r == nil || r.StatusCode != 202 {
				t.Fatalf("REFER: %v", r)
			}
			taker.send(d.request(sip.BYE))
			r := other.waitRing(15 * time.Second)
			if r == nil {
				t.Fatalf("the 911 call was dropped or left ringing nobody; calls up: %d", len(e.Calls()))
			}
			od := other.answer(r)
			od.sendRTP(3)
			if len(e.Calls()) != 1 {
				t.Fatalf("calls up: %d", len(e.Calls()))
			}
			hangupAll(e)
			cdr := rec.find(t, "the 911 call", func(r store.CDR) bool { return r.Emergency })
			why := strings.Join(cdr.Why, " ")
			if !strings.Contains(why, tc.why) || !strings.Contains(why, "every call-taker was rung") {
				t.Fatalf("why %q", why)
			}
		})
	}
}

// Any call: a phone that hangs up right after asking for a blind transfer (many do, without waiting
// for the result) releases only itself; the transfer goes on.
func TestBlindTransferTransferorHangsUpAtOnce(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	ps := registered(t, e, "100", "101", "102")
	a, b, c := ps[0], ps[1], ps[2]
	_, bd := answeredCall(t, a, b, "101")
	refer := bd.request(sip.REFER)
	refer.Set("Refer-To", "<sip:102@127.0.0.1>")
	if r := b.send(refer); r == nil || r.StatusCode != 202 {
		t.Fatalf("REFER: %v", r)
	}
	b.send(bd.request(sip.BYE))
	ring := c.waitRing(5 * time.Second)
	if ring == nil {
		t.Fatalf("the transfer was dropped when the transferor hung up; calls up: %d", len(e.Calls()))
	}
	cd := c.answer(ring)
	a.drainRTP()
	cd.sendRTP(3)
	if !a.gotRTP(5 * time.Second) {
		t.Fatal("100 and 102 are not talking")
	}
	hangupAll(e)
}

// A trunk on this computer that allows 127.0.0.1 (an ESInet stand-in, a lab) is that trunk only from
// its own port: BitVoice's own parts speak SIP over loopback too (the call generator, the browser and
// card gateways), and their calls must never arrive as the trunk's, on an ESInet trunk as 911 calls.
func TestLoopbackTrunkDoesNotClaimOwnCalls(t *testing.T) {
	esinet := newCarrier(t)
	cfg := esinetConfig(esinet.stack.Port(), config.InboundRoute{Name: "911", Trunk: "in", Destination: config.Destination{Type: "extension", Target: "100"}})
	e, rec := startEngine(t, cfg)
	e.SetLoadToken("tok")
	registered(t, e, "100")
	pbx := netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))
	run, err := loadgen.Start(loadgen.Plan{From: "101", FromSecret: testSecret, To: "102", AnswerAs: "102", AnswerSecret: testSecret,
		Calls: 2, Rate: 10, Concurrent: 2, Hold: 300 * time.Millisecond, PBX: pbx, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	<-run.Done()
	if r := run.Snapshot(); r.Answered != 2 {
		t.Fatalf("result %+v", r)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, c := range rec.cdrs {
		if c.Emergency || c.To != "102" {
			t.Fatalf("a call between extensions on this computer was taken as the ESInet's: %+v", c)
		}
	}
}
