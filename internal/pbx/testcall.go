package pbx

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/i3"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// Test calls (NENA-STA-010.3 section 9, RFC 6881): a call to urn:service:test.sos from the ESInet is
// answered at once, with no call-taker, saying which PSAP took it and the location it brought; the
// first three packets of sound are sent back, and the PSAP hangs up. The test call generator (the
// i3 Test Call service) places such calls to a PSAP that asks for them.

// IsTestCall says whether a request URI asks for a test call.
func IsTestCall(ruri string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(ruri)), "urn:service:test.sos")
}

type testCallState struct {
	mu   sync.Mutex
	seen map[string]time.Time // source -> last test call
	now  int                  // test calls up
}

const maxTestCalls = 4

// testCallPlan decides on a test call that reached this PSAP.
func (e *Engine) testCallPlan(cfg *config.Config, c *Call, source string) plan {
	if cfg.System.PSAP.TestCallsOff {
		return plan{reject: 404, reason: "Test Calls Not Taken Here", direction: "test"}
	}
	t := &e.tests
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.seen == nil {
		t.seen = map[string]time.Time{}
	}
	if last, ok := t.seen[source]; ok && time.Since(last) < 2*time.Minute {
		return plan{reject: 486, reason: "Busy Here", direction: "test"} // a repeat within two minutes
	}
	if t.now >= maxTestCalls {
		return plan{reject: 486, reason: "Busy Here", direction: "test"}
	}
	t.seen[source] = time.Now()
	for k, v := range t.seen {
		if time.Since(v) > 10*time.Minute {
			delete(t.seen, k)
		}
	}
	return plan{local: "test911", toName: "Test call", direction: "test"}
}

// answerTestCall answers a test call: 200 OK with the PSAP's name, the service and the location as
// text beside the SDP; three packets looped back; then BYE.
func (c *Call) answerTestCall() {
	cfg := c.e.Config()
	name := cfg.System.Name
	if a := cfg.System.PSAP.AgencyID; a != "" {
		name += " (" + a + ")"
	}
	where := "no location"
	if c.i3 != nil && c.i3.Location != nil {
		where = describeLocation(*c.i3.Location)
	} else if c.i3 != nil && c.i3.LocationURI != "" {
		where = "a location by reference: " + c.i3.LocationURI
	}
	c.mu.Lock()
	c.testText = name + "\r\nurn:service:test.sos\r\n" + where + "\r\n"
	c.mu.Unlock()
	if !c.answerHere("Test call") {
		return
	}
	c.e.tests.mu.Lock()
	c.e.tests.now++
	c.e.tests.mu.Unlock()
	ep := c.caller.ep
	var once sync.Once
	done := func(why string) {
		once.Do(func() {
			c.e.tests.mu.Lock()
			c.e.tests.now--
			c.e.tests.mu.Unlock()
			c.hangup(why, "system")
		})
	}
	n := 0
	var mu sync.Mutex
	stop := ep.Listen(func(pkt []byte) {
		mu.Lock()
		defer mu.Unlock()
		if n >= 3 || len(pkt) < 12 {
			return
		}
		n++
		ep.SendRTP(append([]byte(nil), pkt...))
		if n == 3 {
			time.AfterFunc(300*time.Millisecond, func() { done("test call: three packets looped back") })
		}
	})
	time.AfterFunc(10*time.Second, func() { stop(); done("test call: ended") })
	c.e.publish("testcall.answered", map[string]any{"call": c.ID, "from": c.from, "where": where})
}

func describeLocation(l i3.Location) string {
	if l.Civic != nil && !l.Civic.Empty() {
		return l.Civic.String()
	}
	if l.HasPoint() {
		s := fmt.Sprintf("%.6f, %.6f", *l.Lat, *l.Lon)
		if l.Radius > 0 {
			s += fmt.Sprintf(" (within %.0f m)", l.Radius)
		}
		return s
	}
	return "a location with no address or position"
}

// TestCallResult is how a test call placed by the generator went.
type TestCallResult struct {
	At       time.Time `json:"at"`
	PSAP     string    `json:"psap"`
	Status   int       `json:"status"`
	Reason   string    `json:"reason,omitempty"`
	Answer   string    `json:"answer,omitempty"` // the PSAP's text (its name, the service, the location)
	Sent     int       `json:"sent"`
	Echoed   int       `json:"echoed"`
	OK       bool      `json:"ok"`
	Problem  string    `json:"problem,omitempty"`
	Duration float64   `json:"seconds"`
}

// PlaceTestCall sends one test call to a PSAP of the ESRP's list, over its trunk, with a location
// (PIDF-LO), and reports what came back.
func (e *Engine) PlaceTestCall(ctx context.Context, psapURI string, pidfLO []byte) (TestCallResult, error) {
	cfg := e.Config()
	res := TestCallResult{At: time.Now().UTC(), PSAP: psapURI}
	var ps *config.CorePSAP
	for i := range cfg.System.Core.PSAPs {
		if strings.EqualFold(cfg.System.Core.PSAPs[i].URI, psapURI) {
			ps = &cfg.System.Core.PSAPs[i]
		}
	}
	if ps == nil {
		return res, errors.New("not a PSAP of the ESRP's list")
	}
	tr := cfg.FindTrunk(ps.Trunk)
	if tr == nil || !tr.Enabled {
		return res, errors.New("the PSAP's trunk is off")
	}
	peer, err := e.trunkPeer(tr)
	if err != nil {
		return res, err
	}
	ep, err := e.pool.Allocate()
	if err != nil {
		return res, errors.New("no media ports free")
	}
	defer ep.Close()
	host := e.stack.LocalHost(peer)
	offer := sdp.New(e.mediaHost(peer), ep.Port(), uint64(time.Now().Unix()), []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 101)
	ct, body, geo := withLocation(offer.Bytes(), pidfLO, host)
	tag := randomToken(6)
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "urn:service:test.sos"}
	inv.Set("Route", "<sip:"+sip.HostPort(tr.Host, tr.Port)+";lr>")
	src := cfg.System.Core.Source
	if src == "" {
		src = host
	}
	inv.Set("From", "<sip:testcall@"+src+">;tag="+tag)
	inv.Set("To", "<urn:service:test.sos>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", e.localContact(peer, "testcall"))
	inv.Set("Geolocation", geo)
	inv.Set("Geolocation-Routing", "yes")
	inv.Set("Content-Type", ct)
	inv.Body = body
	start := time.Now()
	tx := e.stack.Request(inv, peer)
	var final *sip.Message
	timeout := time.After(30 * time.Second)
wait:
	for {
		select {
		case r, ok := <-tx.Responses:
			if !ok {
				break wait
			}
			if r.StatusCode >= 200 {
				final = r
				break wait
			}
		case <-timeout:
			tx.Cancel()
			break wait
		case <-ctx.Done():
			tx.Cancel()
			return res, ctx.Err()
		}
	}
	if final == nil {
		res.Problem = "no answer in 30 seconds"
		return res, nil
	}
	res.Status, res.Reason = final.StatusCode, final.Reason
	if final.StatusCode == 401 || final.StatusCode == 407 {
		res.Problem = "the PSAP's trunk asks for a password: test calls go over trunks trusted by address"
		return res, nil
	}
	if final.StatusCode != 200 {
		res.Problem = "the PSAP refused the test call"
		return res, nil
	}
	ack := &sip.Message{IsRequest: true, Method: sip.ACK, RequestURI: inv.RequestURI}
	target := inv.RequestURI
	if c, err := sip.ParseAddress(final.Get("Contact")); err == nil && c.URI != nil {
		target = c.URI.String()
		ack.RequestURI = target
	}
	for _, k := range []string{"From", "Call-ID"} {
		ack.Set(k, inv.Get(k))
	}
	ack.Set("To", final.Get("To"))
	ack.Set("CSeq", "1 ACK")
	_ = e.stack.Stateless(ack, peer)
	ans, text := splitAnswer(final)
	res.Answer = text
	if ans != nil {
		if a := ans.Audio(); a != nil {
			ep.SetRemote(ans.Connection(a), a.Port)
		}
	}
	got := make(chan struct{}, 8)
	stop := ep.Listen(func(pkt []byte) {
		if len(pkt) >= 12 {
			select {
			case got <- struct{}{}:
			default:
			}
		}
	})
	defer stop()
	ep.Start()
	seq := uint16(time.Now().UnixNano())
	for i := 0; i < 3; i++ {
		pkt := make([]byte, 12+160)
		pkt[0], pkt[1] = 0x80, 0
		binary.BigEndian.PutUint16(pkt[2:], seq+uint16(i))
		binary.BigEndian.PutUint32(pkt[4:], uint32(i*160))
		binary.BigEndian.PutUint32(pkt[8:], 0x7e57ca11)
		for j := 12; j < len(pkt); j++ {
			pkt[j] = 0xff // silence in mu-law
		}
		ep.SendRTP(pkt)
		res.Sent++
		time.Sleep(20 * time.Millisecond)
	}
	deadline := time.After(3 * time.Second)
count:
	for res.Echoed < 3 {
		select {
		case <-got:
			res.Echoed++
		case <-deadline:
			break count
		}
	}
	bye := &sip.Message{IsRequest: true, Method: sip.BYE, RequestURI: target}
	bye.Set("From", inv.Get("From"))
	bye.Set("To", final.Get("To"))
	bye.Set("Call-ID", inv.Get("Call-ID"))
	bye.Set("CSeq", "2 BYE")
	btx := e.stack.Request(bye, peer)
	select {
	case <-btx.Responses:
	case <-time.After(2 * time.Second):
	}
	res.Duration = time.Since(start).Seconds()
	res.OK = res.Echoed > 0 && strings.Contains(res.Answer, "urn:service:test.sos")
	if !res.OK {
		switch {
		case res.Echoed == 0:
			res.Problem = "no sound came back"
		default:
			res.Problem = "the answer did not say urn:service:test.sos"
		}
	}
	return res, nil
}

// splitAnswer reads a test call's 200 OK: its SDP and its text/plain part.
func splitAnswer(r *sip.Message) (*sdp.Session, string) {
	d := i3.FromInvite(r, "", r.Get("Content-Type"), r.Body)
	var s *sdp.Session
	if len(d.SDP) > 0 {
		s, _ = sdp.Parse(d.SDP)
	}
	return s, i3.TextPart(r.Get("Content-Type"), r.Body)
}

// testCallBody wraps a test call's answer: the SDP and the text, as section 9 asks.
func testCallBody(sd []byte, text string) (string, []byte) {
	boundary := "bitvoice-test-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	var b strings.Builder
	fmt.Fprintf(&b, "--%s\r\nContent-Type: application/sdp\r\n\r\n%s\r\n", boundary, sd)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain;charset=utf-8\r\n\r\n%s\r\n--%s--\r\n", boundary, text, boundary)
	return "multipart/mixed;boundary=" + boundary, []byte(b.String())
}
