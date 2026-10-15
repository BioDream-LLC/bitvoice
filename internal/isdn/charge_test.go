package isdn

import (
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestChargeEncoding(t *testing.T) {
	for _, c := range []Charge{
		{Kind: "currency", Currency: "EUR", Amount: 150, Multiplier: 1, Total: true},
		{Kind: "units", Units: 3},
		{Kind: "free"},
		{Kind: "unavailable"},
		{Final: true, Kind: "currency", Currency: "GBP", Amount: 1234, Multiplier: 0},
		{Final: true, Kind: "units", Units: 70000},
		{Final: true, Kind: "free"},
	} {
		comps := parseFacility(chargeFacility(5, c))
		if len(comps) != 1 {
			t.Fatalf("%+v: %d components", c, len(comps))
		}
		got, ok := parseCharge(comps[0].Op, comps[0].Arg)
		if !ok || got != c {
			t.Errorf("sent %+v, read %+v", c, got)
		}
	}
	for in, want := range map[Charge]string{
		{Kind: "currency", Currency: "EUR", Amount: 150, Multiplier: 1}: "EUR 1.50",
		{Kind: "currency", Currency: "USD", Amount: 5, Multiplier: 0}:   "USD 0.005",
		{Kind: "currency", Currency: "JPY", Amount: 12, Multiplier: 4}:  "JPY 120",
		{Kind: "units", Units: 1}:                                       "1 unit",
	} {
		if in.String() != want {
			t.Errorf("%+v says %q, want %q", in, in.String(), want)
		}
	}
	for _, c := range []Charge{
		{Kind: "currency", Currency: "EUR", Amount: 150, Multiplier: 1, Total: true},
		{Final: true, Kind: "units", Units: 5},
		{Final: true, Kind: "free"},
		{Kind: "unavailable"},
	} {
		got, ok := ParseChargeXML(ChargeXML(c))
		if !ok || got.String() != c.String() || got.Final != c.Final || got.Total != c.Total {
			t.Errorf("XML: sent %+v, read %+v from %s", c, got, ChargeXML(c))
		}
	}
	if a, m, err := ParseMoney("1.50"); err != nil || a != 150 || m != 1 {
		t.Errorf("ParseMoney: %d %d %v", a, m, err)
	}
}

// BitVoice on both sides of a EuroISDN span: the user side asks for advice of charge, the network
// side agrees, gives a figure during the call and the whole charge with the DISCONNECT.
func TestChargeBothSides(t *testing.T) {
	T200 = 100 * time.Millisecond
	a, b := pair()
	defer close(a.done)
	user := New(a, Config{Channels: []int{1, 2}, Switch: "euro", ALaw: true, RequestCharge: true})
	net := New(b, Config{Network: true, Channels: []int{1, 2}, Switch: "euro", ALaw: true, OfferCharge: true})
	incoming := make(chan *Call, 1)
	net.Incoming = func(c *Call) { incoming <- c }
	go user.Run()
	go net.Run()
	waitUp(t, user, net)
	c, err := user.Dial("1234", "5678")
	if err != nil {
		t.Fatal(err)
	}
	in := <-incoming
	if in.ChargeAsked != AskDuring|AskAtTheEnd {
		t.Fatalf("asked %d", in.ChargeAsked)
	}
	in.Answer()
	next(t, c, "connected")
	if err := in.SendCharge(Charge{Kind: "currency", Currency: "EUR", Amount: 25, Multiplier: 1, Total: true}); err != nil {
		t.Fatal(err)
	}
	if e := next(t, c, "charge"); e.Charge.String() != "EUR 0.25" || e.Charge.Final {
		t.Fatalf("got %+v", e.Charge)
	}
	in.SetFinalCharge(Charge{Kind: "units", Units: 4})
	in.Hangup(CauseNormal)
	if e := next(t, c, "charge"); e.Charge.String() != "4 units" || !e.Charge.Final {
		t.Fatalf("got %+v", e.Charge)
	}
	next(t, c, "released")
	if c.Charge == nil || c.Charge.Units != 4 {
		t.Fatalf("call keeps %+v", c.Charge)
	}
}

// BitVoice on both sides of a DMS-100 span: the user side places two calls, each given an identifier
// by the network, then asks the network to join them; the network finds the other call by it.
func TestRLTBothSides(t *testing.T) {
	T200 = 100 * time.Millisecond
	a, b := pair()
	defer close(a.done)
	user := New(a, Config{Channels: []int{1, 2, 3}, Switch: "dms100"})
	net := New(b, Config{Network: true, Channels: []int{1, 2, 3}, Switch: "dms100"})
	incoming := make(chan *Call, 2)
	net.Incoming = func(c *Call) { c.Answer(); incoming <- c }
	joined := make(chan [2]*Call, 1)
	net.OnTransfer = func(x, y *Call) bool {
		joined <- [2]*Call{x, y}
		go func() { time.Sleep(50 * time.Millisecond); x.Hangup(CauseNormal); y.Hangup(CauseNormal) }()
		return true
	}
	go user.Run()
	go net.Run()
	waitUp(t, user, net)
	var calls []*Call
	for _, n := range []string{"5550001", "5550002"} {
		c, err := user.Dial(n, "5550100")
		if err != nil {
			t.Fatal(err)
		}
		next(t, c, "connected")
		calls = append(calls, c)
	}
	in1, in2 := <-incoming, <-incoming
	deadline := time.Now().Add(2 * time.Second)
	for calls[1].rltID == 0 || calls[0].rltID == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no transfer identifiers from the network")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if calls[0].rltID == calls[1].rltID {
		t.Fatal("the same identifier twice")
	}
	if err := calls[0].Transfer(calls[1]); err != nil {
		t.Fatal(err)
	}
	j := <-joined
	if !(j[0] == in1 && j[1] == in2) && !(j[0] == in2 && j[1] == in1) {
		t.Fatal("the network joined the wrong calls")
	}
	next(t, calls[0], "released")
	next(t, calls[1], "released")
}

// libpri as the network of a EuroISDN span gives advice of charge on BitVoice's outgoing call: two
// figures during it (money, then units) and the whole charge as it clears the call.
func TestAgainstLibpriChargeAsUser(t *testing.T) {
	link, o := startOracle(t, "net", "euro", "aoc")
	p := New(link, Config{Channels: []int{1, 2, 3}, ALaw: true, Switch: "euro", RequestCharge: true})
	go p.Run()
	o.expect("UP")
	waitUp(t, p)
	c, err := p.Dial("2125551234", "3125550100")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	timeout := time.After(6 * time.Second)
	for done := false; !done; {
		select {
		case e := <-c.Events:
			switch e.Kind {
			case "charge":
				s := e.Charge.String()
				if e.Charge.Final {
					s += " final"
				} else if e.Charge.Total {
					s += " total"
				}
				got = append(got, s)
			case "released":
				done = true
			}
		case <-timeout:
			t.Fatalf("got %v and no release", got)
		}
	}
	// (libpri's encoder always sends a subtotal, whatever billing_accumulation says.)
	if strings.Join(got, ", ") != "EUR 1.50, 3 units, 5 units final" {
		t.Fatalf("got %v", got)
	}
}

// BitVoice as the network of a EuroISDN span: libpri (a PBX) asks for advice of charge and reads
// the figures BitVoice gives.
func TestAgainstLibpriChargeAsNetwork(t *testing.T) {
	link, o := startOracle(t, "cpe", "euro", "aoccall")
	p := New(link, Config{Network: true, Channels: []int{1, 2, 3}, ALaw: true, Switch: "euro", OfferCharge: true})
	incoming := make(chan *Call, 1)
	p.Incoming = func(c *Call) { incoming <- c }
	go p.Run()
	o.expect("UP")
	var in *Call
	select {
	case in = <-incoming:
	case <-time.After(5 * time.Second):
		t.Fatal("libpri's SETUP did not arrive")
	}
	if in.ChargeAsked != AskDuring|AskAtTheEnd {
		t.Fatalf("libpri asked %d", in.ChargeAsked)
	}
	in.Answer()
	o.expect("ANSWER")
	in.SendCharge(Charge{Kind: "currency", Currency: "EUR", Amount: 230, Multiplier: 1, Total: true})
	if l := o.expect("AOCD"); l != "AOCD currency=EUR cost=230 mult=1 total=1" {
		t.Fatalf("libpri: %s", l)
	}
	in.SendCharge(Charge{Kind: "units", Units: 7})
	if l := o.expect("AOCD"); l != "AOCD units=7 total=0" {
		t.Fatalf("libpri: %s", l)
	}
	in.SetFinalCharge(Charge{Kind: "currency", Currency: "EUR", Amount: 4125, Multiplier: 0})
	in.Hangup(CauseNormal)
	if l := o.expect("AOCE"); l != "AOCE currency=EUR cost=4125 mult=0" {
		t.Fatalf("libpri: %s", l)
	}
	p.Close()
}

// BitVoice as a DMS-100 for libpri (a PBX): libpri's two calls get transfer identifiers from
// BitVoice, libpri asks to join them by RLT, BitVoice finds both and clears them; its own encoding of
// the request is byte for byte libpri's.
func TestAgainstLibpriRLT(t *testing.T) {
	link, o := startOracle(t, "cpe", "dms100", "bridge")
	var mu sync.Mutex
	var facs [][]byte
	p := New(link, Config{Network: true, Channels: []int{1, 2, 3}, Switch: "dms100", Trace: func(in bool, b []byte) {
		if m, err := Parse(b); in && err == nil && m.Type == MsgFacility {
			mu.Lock()
			facs = append(facs, m.GetAll(ieFacility)...)
			mu.Unlock()
		}
	}})
	p.Incoming = func(c *Call) { c.Answer() }
	joined := make(chan [2]*Call, 1)
	p.OnTransfer = func(a, b *Call) bool {
		joined <- [2]*Call{a, b}
		go func() { time.Sleep(50 * time.Millisecond); a.Hangup(CauseNormal); b.Hangup(CauseNormal) }()
		return true
	}
	go p.Run()
	o.expect("UP")
	o.expect("BRIDGED")
	var j [2]*Call
	select {
	case j = <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("libpri's RLT request was not read")
	}
	if j[0].Called == j[1].Called {
		t.Fatalf("joined %s to itself", j[0].Called)
	}
	o.expect("HANGUP")
	o.expect("HANGUP")
	mu.Lock()
	got := facs[0]
	mu.Unlock()
	if want := rltTransfer(j[1].rltID); hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatalf("libpri sent %x, BitVoice would send %x", got, want)
	}
	p.Close()
}
