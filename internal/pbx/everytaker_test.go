package pbx

import (
	"math"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// esinetConfig is a PSAP taking 911 calls from an ESInet trunk, with the routes given.
func esinetConfig(port int, routes ...config.InboundRoute) config.Config {
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{{ID: "in", Name: "ESInet", Enabled: true, Host: "127.0.0.1", Port: port, ESInet: true, AllowFrom: []string{"127.0.0.1"}}}
	cfg.InboundRoutes = routes
	return cfg
}

// esinet911 sends a voice 911 call from the ESInet stand-in; it returns the transaction and the caller's
// media socket.
func esinet911(t *testing.T, e *Engine, esinet *carrier) (*sip.ClientTx, *sip.Message, *net.UDPConn) {
	t.Helper()
	media, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { media.Close() })
	port := media.LocalAddr().(*net.UDPAddr).Port
	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "urn:service:sos"}
	inv.Set("From", "<sip:+17175550100@esinet.test>;tag="+sip.NewTag())
	inv.Set("To", "<urn:service:sos>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:esrp@127.0.0.1:"+strconv.Itoa(esinet.stack.Port())+">")
	inv.Set("Content-Type", "application/sdp")
	inv.Body = sdp.New("127.0.0.1", port, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
	return esinet.stack.Request(inv, pbx), inv, media
}

// A route that hangs up 911 calls (a mistake in the routing set-up) never refuses one: the call rings
// every call-taker, and its routing trail says why.
func TestEmergencyHangupRouteRingsEveryone(t *testing.T) {
	esinet := newCarrier(t)
	cfg := esinetConfig(esinet.stack.Port(), config.InboundRoute{Name: "Closed", Trunk: "in", Destination: config.Destination{Type: "hangup"}})
	e, rec := startEngine(t, cfg)
	ps := registered(t, e, "100", "101")
	tx, _, _ := esinet911(t, e, esinet)
	r0, r1 := ps[0].waitRing(5*time.Second), ps[1].waitRing(5*time.Second)
	if r0 == nil || r1 == nil {
		t.Fatalf("every call-taker should ring: 100 %v, 101 %v", r0 != nil, r1 != nil)
	}
	ps[1].answer(r1)
	if r := finalOf(tx); r == nil || r.StatusCode != 200 {
		t.Fatalf("the 911 call was not answered: %v", r)
	}
	hangupAll(e)
	cdr := rec.find(t, "the 911 call", func(r store.CDR) bool { return r.Emergency })
	why := strings.Join(cdr.Why, " ")
	if !strings.Contains(why, "Closed would have refused it (480 Temporarily Unavailable).") || !strings.Contains(why, "never refused") {
		t.Fatalf("why %q", why)
	}
}

// With no incoming route at all, a 911 call still rings every call-taker.
func TestEmergencyNoRouteRingsEveryone(t *testing.T) {
	esinet := newCarrier(t)
	e, _ := startEngine(t, esinetConfig(esinet.stack.Port()))
	p := registered(t, e, "100")[0]
	tx, _, _ := esinet911(t, e, esinet)
	ring := p.waitRing(5 * time.Second)
	if ring == nil {
		t.Fatal("the call-taker did not ring")
	}
	p.answer(ring)
	if r := finalOf(tx); r == nil || r.StatusCode != 200 {
		t.Fatalf("answer %v", r)
	}
	hangupAll(e)
}

// A 911 call its route rang without an answer is not refused either: every call-taker is rung next,
// and a phone that connects meanwhile is rung too.
func TestEmergencyUnansweredRingsEveryone(t *testing.T) {
	esinet := newCarrier(t)
	cfg := esinetConfig(esinet.stack.Port(), config.InboundRoute{Name: "911", Trunk: "in", Destination: config.Destination{Type: "extension", Target: "100"}})
	cfg.FindExtension("100").RingSeconds = 1
	e, rec := startEngine(t, cfg)
	p100 := registered(t, e, "100")[0]
	tx, _, _ := esinet911(t, e, esinet)
	if p100.waitRing(5*time.Second) == nil {
		t.Fatal("100 did not ring")
	}
	// 100 does not answer; every call-taker is rung next, 100 again among them.
	if p100.waitRing(5*time.Second) == nil {
		t.Fatal("the call was not offered to every call-taker")
	}
	p101 := registered(t, e, "101")[0] // connects while the call rings
	ring := p101.waitRing(5 * time.Second)
	if ring == nil {
		t.Fatal("a phone that connected while the 911 call rang was not rung")
	}
	p101.answer(ring)
	if r := finalOf(tx); r == nil || r.StatusCode != 200 {
		t.Fatalf("answer %v", r)
	}
	hangupAll(e)
	cdr := rec.find(t, "the 911 call", func(r store.CDR) bool { return r.Emergency })
	if !strings.Contains(strings.Join(cdr.Why, " "), "An emergency call is never left unanswered") {
		t.Fatalf("why %q", cdr.Why)
	}
}

// A test call (urn:service:test.sos) and calls that are not 911 calls keep their route's refusal.
func TestHangupRouteStillRefusesOrdinaryCalls(t *testing.T) {
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.InboundRoutes[0].Destination = config.Destination{Type: "hangup"}
	e, _ := startEngine(t, cfg)
	waitTrunk(t, e, "registered")
	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:acct@127.0.0.1"}
	inv.Set("From", "<sip:3125559999@carrier.test>;tag=o1")
	inv.Set("To", "<sip:+12125550100@carrier.test>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:carrier@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
	inv.Set("Content-Type", "application/sdp")
	inv.Body = sdp.New("127.0.0.1", 40002, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
	if r := finalOf(c.stack.Request(inv, pbx)); r == nil || r.StatusCode != 480 {
		t.Fatalf("an ordinary call on a hang-up route: %v, want 480", r)
	}
}

// A call whose one side stopped sending sound (a caller who vanished without a hang-up) while the other
// still sends is ended, saying whose audio stopped, on the record and in call.ended.
func TestOneSidedSilenceEndsCall(t *testing.T) {
	old := mediaDeadAfter
	mediaDeadAfter = 1500 * time.Millisecond
	t.Cleanup(func() { mediaDeadAfter = old })
	e, rec := startEngine(t, testConfig())
	ps := registered(t, e, "100", "101")
	a, b := ps[0], ps[1]
	ended := e.Bus.Subscribe()
	defer e.Bus.Unsubscribe(ended)
	var d *dialog
	done := make(chan int, 1)
	go func() {
		dd, code := a.dial("101")
		d = dd
		done <- code
	}()
	ring := b.waitRing(5 * time.Second)
	if ring == nil {
		t.Fatal("101 did not ring")
	}
	db := b.answer(ring)
	if code := <-done; code != 200 {
		t.Fatalf("dial %d", code)
	}
	voice := make([]byte, 160)
	stopA := time.Now().Add(500 * time.Millisecond) // the caller sends, then vanishes
	deadline := time.Now().Add(patient(10 * time.Second))
	for i := uint16(1); time.Now().Before(deadline); i++ {
		if time.Now().Before(stopA) {
			a.rtp.WriteToUDPAddrPort(rtpPacket(i, voice), d.media)
		}
		b.rtp.WriteToUDPAddrPort(rtpPacket(i, voice), db.media)
		if len(e.Calls()) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(e.Calls()) != 0 {
		t.Fatal("the call stayed up after the caller's audio stopped")
	}
	r := rec.find(t, "the call", func(r store.CDR) bool { return r.To == "101" })
	if r.Cause != "the caller's audio stopped" || r.HangupBy != "system" {
		t.Fatalf("cause %q by %q", r.Cause, r.HangupBy)
	}
	if !strings.Contains(strings.Join(r.Why, " "), "The caller's audio stopped: no sound for 1 seconds") {
		t.Fatalf("why %q", r.Why)
	}
	for {
		select {
		case ev := <-ended.C:
			if ev.Type == "call.ended" {
				if ci, ok := ev.Data.(CallInfo); ok && ci.Cause == "the caller's audio stopped" && ci.EndedBy == "system" {
					return
				}
			}
		case <-time.After(patient(2 * time.Second)):
			t.Fatal("call.ended did not say why")
		}
	}
}

func hangupAll(e *Engine) {
	for _, c := range e.Calls() {
		e.Hangup(c.ID, "test")
	}
}

// With live transcripts on, an answered 911 call's caller is written out as they speak: a piece ends at
// their pause and comes as a call.transcript event naming the side.
func TestLiveTranscript911(t *testing.T) {
	ai, _ := fakeAIWith(t, "", "My house is on fire.")
	esinet := newCarrier(t)
	cfg := esinetConfig(esinet.stack.Port(), config.InboundRoute{Name: "911", Trunk: "in", Destination: config.Destination{Type: "extension", Target: "100"}})
	cfg.System.AI = config.AI{Enabled: true, BaseURL: ai.URL, Model: "m", LiveTranscripts: true}
	e, _ := startEngine(t, cfg)
	p := registered(t, e, "100")[0]
	events := e.Bus.Subscribe()
	defer e.Bus.Unsubscribe(events)
	tx, inv, caller := esinet911(t, e, esinet)
	ring := p.waitRing(5 * time.Second)
	if ring == nil {
		t.Fatal("the call-taker did not ring")
	}
	p.answer(ring)
	r := finalOf(tx)
	if r == nil || r.StatusCode != 200 {
		t.Fatalf("answer %v", r)
	}
	to := mediaOf(r.Body)
	ack := &sip.Message{IsRequest: true, Method: sip.ACK, RequestURI: "sip:x@127.0.0.1:" + strconv.Itoa(e.SIPPort())}
	ack.Set("From", inv.Get("From"))
	ack.Set("To", r.Get("To"))
	ack.Set("Call-ID", inv.CallID())
	ack.Set("CSeq", "1 ACK")
	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	_ = esinet.stack.Stateless(ack, pbx)
	// A second of speech-like sound, then quiet: the pause ends the piece.
	n := 0
	frame := func(loud bool) []byte {
		b := make([]byte, 160)
		for i := range b {
			v := 0.0
			if loud {
				v = 9000 * math.Sin(float64(n)*2*math.Pi*300/8000) * (0.6 + 0.4*math.Sin(float64(n)*2*math.Pi*3/8000))
			}
			b[i] = media.LinearToUlaw(int16(v))
			n++
		}
		return b
	}
	go func() {
		for i := uint16(1); i <= 200; i++ {
			caller.WriteToUDPAddrPort(rtpPacket(i, frame(i <= 50)), to)
			time.Sleep(20 * time.Millisecond)
		}
	}()
	deadline := time.After(patient(15 * time.Second))
	for {
		select {
		case ev := <-events.C:
			if ev.Type != "call.transcript" {
				continue
			}
			m := ev.Data.(map[string]any)
			lines := m["lines"].([]TranscriptLine)
			if len(lines) != 1 || lines[0].Side != "caller" || lines[0].Speaker != "Caller" || lines[0].Text != "My house is on fire." {
				t.Fatalf("lines %+v", lines)
			}
			hangupAll(e)
			return
		case <-deadline:
			t.Fatal("no live transcript came")
		}
	}
}

// waitNoCalls waits until every call has ended.
func waitNoCalls(t *testing.T, e *Engine) {
	t.Helper()
	deadline := time.Now().Add(patient(5 * time.Second))
	for len(e.Calls()) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("calls did not end")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
