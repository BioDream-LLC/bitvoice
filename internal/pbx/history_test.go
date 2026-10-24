package pbx

import (
	"context"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// A call from the carrier that was forwarded on its way (History-Info, as Teams and many carriers
// send it): the call record and the why trail say who forwarded it and why. 101 forwards every call
// on to an outside number: the INVITE to the carrier carries Diversion and History-Info, with the
// history the call came in with kept and the forward added to it.
func TestHistoryInfoInAndOut(t *testing.T) {
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	e, rec := startEngine(t, cfg)
	waitTrunk(t, e, "registered")
	b := registered(t, e, "101")[0]
	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	call := func(from string, extra map[string]string) *sip.ClientTx {
		inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:acct@127.0.0.1"}
		inv.Set("From", "<sip:"+from+"@carrier.test>;tag=in"+from)
		inv.Set("To", "<sip:+12125550100@carrier.test>")
		inv.Set("Call-ID", sip.NewCallID())
		inv.Set("CSeq", "1 INVITE")
		inv.Set("Contact", "<sip:carrier@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
		for k, v := range extra {
			inv.Set(k, v)
		}
		inv.Set("Content-Type", "application/sdp")
		inv.Body = sdp.New("127.0.0.1", 40002, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
		return c.stack.Request(inv, pbx)
	}
	hi := map[string]string{"History-Info": "<sip:+12125550177@carrier.test>;index=1, <sip:+12125550100@carrier.test;cause=486>;index=1.1;rc=1"}
	tx := call("3125559999", hi)
	ring := b.waitRing(patient(3 * time.Second))
	if ring == nil {
		t.Fatal("the forwarded call did not ring 101")
	}
	b.answer(ring)
	if res := finalOf(tx); res == nil || res.StatusCode != 200 {
		t.Fatalf("carrier got %v", res)
	}
	for _, ci := range e.Calls() {
		if ci.ForwardedFrom != "+12125550177" || ci.ForwardReason != "user-busy" {
			t.Fatalf("the live call %+v", ci)
		}
		e.Hangup(ci.ID, "test")
	}
	r := rec.last(t)
	if r.ForwardedFrom != "+12125550177" || r.ForwardReason != "user-busy" || r.Original != "+12125550177" {
		t.Fatalf("call record %+v", r)
	}
	if !strings.Contains(strings.Join(r.Why, " "), "forwarded to here by +12125550177 (it was busy)") {
		t.Fatalf("why %q", r.Why)
	}

	// 101 forwards everything to an outside number: the carrier gets the forward in both forms.
	if err := e.SetDivert(Divert{Ext: "101", All: "2125550144"}); err != nil {
		t.Fatal(err)
	}
	call("3125559998", hi)
	var out *sip.Message
	select {
	case out = <-c.invites:
	case <-time.After(patient(10 * time.Second)):
		t.Fatal("the forward never reached the carrier")
	}
	if d := out.Get("Diversion"); !strings.Contains(d, "<sip:101@") || !strings.Contains(d, "reason=unconditional") || !strings.Contains(d, "counter=2") {
		t.Fatalf("Diversion %q", d)
	}
	all := out.GetAll("History-Info")
	if len(all) != 4 || !strings.HasPrefix(all[0], "<sip:+12125550177@carrier.test>;index=1") ||
		!strings.Contains(all[2], "sip:101@") || !strings.Contains(all[2], ";index=1.2") ||
		!strings.Contains(all[3], "cause=302") || !strings.Contains(all[3], ";index=1.2.1;rc=1.2") {
		t.Fatalf("History-Info %q", all)
	}
	f, ok := sip.ForwardOf(out)
	if !ok || f.From != "101" || f.Original != "+12125550177" || f.Reason != "unconditional" {
		t.Fatalf("the carrier reads %+v", f)
	}
	<-c.answered

	// A trunk set to Diversion only sends no History-Info.
	cfg2 := trunkConfig(c.stack.Port())
	cfg2.Trunks[0].Redirecting = "diversion"
	e.Apply(cfg2)
	if err := e.SetDivert(Divert{Ext: "101", All: "2125550145"}); err != nil {
		t.Fatal(err)
	}
	call("3125559997", nil)
	select {
	case out = <-c.invites:
	case <-time.After(patient(10 * time.Second)):
		t.Fatal("the second forward never reached the carrier")
	}
	if out.Get("Diversion") == "" || len(out.GetAll("History-Info")) != 0 {
		t.Fatalf("Diversion only: %q %q", out.Get("Diversion"), out.GetAll("History-Info"))
	}
}

// Against Asterisk 20's PJSIP as the carrier (~/.bvtest/history): BITVOICE_TEST_ASTERISK_HISTORY is
// its SIP address (127.0.0.1:5070), BITVOICE_TEST_ASTERISK_CONTAINER the Docker container whose log
// shows what Asterisk read. Asterisk forwards a call to BitVoice with redirecting information (it
// sends Diversion and History-Info); BitVoice reads it. 101 forwards on to Asterisk; Asterisk's
// REDIRECTING() reads what BitVoice sent, once from History-Info alone and once from Diversion alone.
func TestHistoryInfoWithAsterisk(t *testing.T) {
	addr, ctr := os.Getenv("BITVOICE_TEST_ASTERISK_HISTORY"), os.Getenv("BITVOICE_TEST_ASTERISK_CONTAINER")
	if addr == "" || ctr == "" {
		t.Skip("set BITVOICE_TEST_ASTERISK_HISTORY and BITVOICE_TEST_ASTERISK_CONTAINER")
	}
	host, port, _ := strings.Cut(addr, ":")
	p, _ := strconv.Atoi(port)
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{{ID: "ast", Name: "Asterisk", Enabled: true, Host: host, Port: p, Transport: "tcp", AllowFrom: []string{"127.0.0.1", "172.17.0.0/16", "192.168.65.0/24"}}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "out", Patterns: []string{"NXXNXXXXXX"}, Trunks: []string{"ast"}}}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "Main", DID: "+12125550100", Destination: config.Destination{Type: "extension", Target: "101"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	rec := &memRecorder{}
	e := New(cfg, events.New(), rec, nil)
	e.T1, e.T2 = 50*time.Millisecond, 200*time.Millisecond
	if err := e.Start(context.Background(), "0.0.0.0:25070"); err != nil { // Asterisk in Docker reaches the Mac here
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	b := registered(t, e, "101")[0]
	asterisk := func(cmd string) string {
		out, _ := exec.Command("docker", "exec", ctr, "asterisk", "-rx", cmd).CombinedOutput()
		return string(out)
	}
	// In: Asterisk forwards a call to BitVoice.
	asterisk("channel originate Local/s@forwarder application Wait 5")
	ring := b.waitRing(patient(10 * time.Second))
	if ring == nil {
		t.Fatal("Asterisk's forwarded call did not ring 101")
	}
	b.answer(ring)
	time.Sleep(300 * time.Millisecond)
	for _, ci := range e.Calls() {
		if ci.ForwardedFrom != "2125550177" || ci.ForwardReason != "user-busy" {
			t.Fatalf("read from Asterisk: %+v (History-Info %q)", ci, ring.Req.GetAll("History-Info"))
		}
		e.Hangup(ci.ID, "test")
	}
	rec.last(t)
	// Out: 101 forwards every call to Asterisk; Asterisk reads who and why.
	for _, mode := range []string{"history-info", "diversion"} {
		cfg.Trunks[0].Redirecting = mode
		e.Apply(cfg)
		if err := e.SetDivert(Divert{Ext: "101", All: "2125550144"}); err != nil {
			t.Fatal(err)
		}
		before := len(strings.Split(dockerLogs(ctr), "\n"))
		a := registered(t, e, "100")[0]
		d, code := a.dial("101")
		if code != 200 {
			t.Fatalf("%s: the forward was not answered: %d", mode, code)
		}
		var line string
		for end := time.Now().Add(patient(10 * time.Second)); time.Now().Before(end) && line == ""; time.Sleep(100 * time.Millisecond) {
			lines := strings.Split(dockerLogs(ctr), "\n")
			for _, l := range lines[min(before, len(lines)):] {
				if strings.Contains(l, "BVFWD") {
					line = l
				}
			}
		}
		a.send(d.request(sip.BYE))
		if line == "" {
			r := rec.last(t)
			t.Fatalf("%s: Asterisk never got the forward; the call record says %+v", mode, r.Why)
		}
		// Asterisk names an unconditional forward cfu (call forward unconditional) in REDIRECTING(reason).
		if !strings.Contains(line, "redir=101") || !(strings.Contains(line, "reason=cfu") || strings.Contains(line, "reason=unconditional")) {
			t.Fatalf("%s: Asterisk read %q", mode, line)
		}
		if mode == "history-info" && (!strings.Contains(line, "hi=<sip:101@") || strings.Contains(line, "div=<")) {
			t.Fatalf("history-info only: %q", line)
		}
		if mode == "diversion" && (!strings.Contains(line, "div=<sip:101@") || strings.Contains(line, "hi=<")) {
			t.Fatalf("diversion only: %q", line)
		}
	}
}

func dockerLogs(ctr string) string {
	out, _ := exec.Command("docker", "logs", ctr).CombinedOutput()
	return string(out)
}
