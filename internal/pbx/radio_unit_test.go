package pbx

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/p25"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// Private calls on the console: a dispatcher calls a radio (its own channel while it lasts, push-to-
// talk granted, hung up with a BYE); then a radio calls the console's unit ID and hangs up.
func TestRadioConsolePrivateCalls(t *testing.T) {
	h := newHomeRFSS(t)
	sipPort := freeUDP(t)
	cfg := testConfig()
	cfg.System.Radio = config.Radio{Console: "800",
		P25: config.P25Console{WACN: "BEE07", System: "AD5", RFSS: 2, Unit: 0x1001, SIPPort: sipPort,
			UnitHome: "127.0.0.1:" + strconv.Itoa(h.stack.Port()), UnitHomeRFSS: 3},
		Aliases: []config.UnitAlias{{Unit: 300, Name: "Engine 5"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	disp := registered(t, e, "100")[0]
	if _, code := disp.dial("800"); code != 200 {
		t.Fatalf("console answered %d", code)
	}
	var call string
	for i := 0; i < 40 && call == ""; i++ {
		if ps := e.Console().Positions; len(ps) == 1 {
			call = ps[0].Call
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Out: the dispatcher calls unit 197.
	id, err := e.UnitCall(call, 197, false)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case inv := <-h.invited:
		if inv.RequestURI != "sip:bee07ad50000c5@p25dr;user=TIA-P25-SU" {
			t.Fatalf("INVITE to %s", inv.RequestURI)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no INVITE for the private call")
	}
	tile := func() *ChannelState {
		for _, c := range e.Console().Channels {
			if c.ID == id {
				return &c
			}
		}
		return nil
	}
	for i := 0; i < 40 && (tile() == nil || !tile().Linked); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if c := tile(); c == nil || !c.Private || c.Unit != 197 || c.Name != "Unit 197" {
		t.Fatalf("private call tile %+v", c)
	}
	if err := e.PTT(call, id, true); err != nil {
		t.Fatal(err)
	}
	for granted := false; !granted; {
		select {
		case p := <-h.got:
			granted = p.Packet.Type == p25.PTTRequest
		case <-time.After(15 * time.Second):
			t.Fatal("no push-to-talk request on the private call")
		}
	}
	e.PTT(call, id, false)
	if err := e.EndUnitCall(id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.byes:
	case <-time.After(15 * time.Second):
		t.Fatal("hanging up sent no BYE")
	}
	if tile() != nil {
		t.Fatal("the private call's tile stayed")
	}

	// In: unit 300 calls the console.
	outPeer, _ := h.peer.Load().(netip.AddrPort)
	console := sip.Peer{Transport: "UDP", Addr: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(sipPort))}
	req := func(method string, cseq int, body string) *sip.Message {
		m := &sip.Message{IsRequest: true, Method: method, RequestURI: "sip:bee07ad5001001@p25dr;user=TIA-P25-SU"}
		m.Set("From", "<sip:bee07ad500012c@p25dr;user=TIA-P25-SU>;tag=r300")
		m.Set("To", "<sip:bee07ad5001001@p25dr;user=TIA-P25-SU>")
		m.Set("Call-ID", "in-300@03.ad5.bee07.p25dr")
		m.Set("CSeq", strconv.Itoa(cseq)+" "+method)
		m.Set("Priority", "4;e")
		if body != "" {
			m.Set("Content-Type", "application/sdp")
			m.Body = []byte(body)
		}
		var final *sip.Message
		for r := range h.stack.Request(m, console).Responses {
			final = r
		}
		return final
	}
	if r := req(sip.INVITE, 1, strings.Replace(h.sdp(), "Groupcall", "U2UCall", 1)); r == nil || r.StatusCode != 200 {
		t.Fatalf("the console did not answer the radio's call: %+v", r)
	}
	id = "unit-300"
	if c := tile(); c == nil || !c.Incoming || !c.Emergency || c.Unit != 300 {
		t.Fatalf("incoming private call tile %+v", c)
	}
	// The emergency stays raised, with the radio's alias, until a dispatcher acknowledges it.
	if c := tile(); c.EmergencyUnit == "" || c.EmergencyAlias != "Engine 5" || c.EmergencyAt.IsZero() {
		t.Fatalf("emergency not raised: %+v", c)
	}
	if a, err := e.AckEmergency(id, "Pat"); err != nil || a.Alias != "Engine 5" || a.By != "Pat" {
		t.Fatalf("ack %+v %v", a, err)
	}
	if c := tile(); c.EmergencyUnit != "" || c.AckBy != "Pat" || c.AckAt.IsZero() {
		t.Fatalf("after the ack %+v", c)
	}
	if _, err := e.AckEmergency(id, "Pat"); err == nil {
		t.Fatal("acknowledged twice")
	}
	// The radio talks; the dispatcher hears it. The call's first RTP (a heartbeat) says where to send.
	disp.drainRTP()
	var peer netip.AddrPort
	for i := 0; i < 60 && (!peer.IsValid() || peer == outPeer); i++ {
		peer, _ = h.peer.Load().(netip.AddrPort)
		time.Sleep(50 * time.Millisecond)
	}
	if peer == outPeer {
		t.Fatal("no RTP from the console for the radio's call")
	}
	go func() {
		for _, b := range captureRTP(t) {
			h.send(peer, b)
			time.Sleep(20 * time.Millisecond)
		}
	}()
	if !receivedLoud(disp.rtp, 3*time.Second) {
		t.Fatal("the dispatcher does not hear the radio's private call")
	}
	if r := req(sip.BYE, 2, ""); r == nil || r.StatusCode != 200 {
		t.Fatalf("BYE answered %+v", r)
	}
	for i := 0; i < 40 && tile() != nil; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if tile() != nil {
		t.Fatal("the radio hung up but the tile stayed")
	}
}
