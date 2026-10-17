package iax

import (
	"bytes"
	"context"
	"net/netip"
	"testing"
	"time"
)

func node(t *testing.T) *Node {
	t.Helper()
	n, err := Listen(context.Background(), "127.0.0.1:0", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(n.Close)
	return n
}

func addr(n *Node) netip.AddrPort { return netip.MustParseAddrPort("127.0.0.1:" + itoa(n.Port())) }

func itoa(v int) string {
	b := []byte{}
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

func next(t *testing.T, c *Call, kinds ...string) Event {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case e, ok := <-c.Events:
			if !ok {
				t.Fatalf("call ended waiting for %v", kinds)
			}
			for _, k := range kinds {
				if e.Kind == k {
					return e
				}
			}
		case <-deadline:
			t.Fatalf("no %v", kinds)
		}
	}
}

func TestFrameRoundTrip(t *testing.T) {
	f := &Frame{Src: 5, Dst: 9, TS: 1234, OSeq: 3, ISeq: 4, Type: TypeIAX, Subclass: IaxNew,
		IEs: IEs{}.SetStr(IECalledNumber, "101").SetU32(IEFormat, FormatULAW).SetU16(IEVersion, 2)}
	p, err := Parse(f.Marshal())
	if err != nil || p.Full == nil {
		t.Fatal(err)
	}
	g := p.Full
	if g.Src != 5 || g.Dst != 9 || g.TS != 1234 || g.OSeq != 3 || g.ISeq != 4 || g.Subclass != IaxNew || g.IEs.Str(IECalledNumber) != "101" {
		t.Fatalf("%+v", g)
	}
	// A format above 127 travels as a power of two.
	v := &Frame{Type: TypeVoice, Subclass: FormatG722, Data: []byte{1, 2}}
	p, _ = Parse(v.Marshal())
	if p.Full.Subclass != FormatG722 || !bytes.Equal(p.Full.Data, []byte{1, 2}) {
		t.Fatalf("voice %+v", p.Full)
	}
	m, _ := Parse(MarshalMini(7, 0x12345, []byte{9}))
	if m.Full != nil || m.MiniSrc != 7 || m.MiniTS != 0x2345 {
		t.Fatalf("mini %+v", m)
	}
}

func TestCallWithPassword(t *testing.T) {
	a, b := node(t), node(t)
	got := make(chan *Call, 1)
	b.Handle(Handlers{OnNew: func(c *Call, ies IEs) {
		if ies.Str(IECalledNumber) != "500" || ies.Str(IEUsername) != "bitvoice" {
			c.Reject("unexpected", 21)
			return
		}
		got <- c
		c.Authenticate("trunk secret")
	}})
	call := a.Dial(addr(b), DialParams{Called: "500", Calling: "100", Username: "bitvoice", Secret: "trunk secret", Formats: FormatULAW | FormatALAW, Preferred: FormatULAW})
	in := <-got
	next(t, in, "authenticated")
	in.Accept(FormatULAW)
	if e := next(t, call, "accepted"); e.Format != FormatULAW {
		t.Fatalf("accepted %v", e.Format)
	}
	in.Ringing()
	next(t, call, "ringing")
	in.Answer()
	next(t, call, "answered")
	for i := range 3 {
		call.SendVoice(bytes.Repeat([]byte{byte(i)}, 160))
	}
	if e := next(t, in, "voice"); len(e.Audio) != 160 || e.Format != FormatULAW {
		t.Fatalf("voice %+v", e)
	}
	in.SendVoice(make([]byte, 160))
	next(t, call, "voice")
	call.SendDTMF('5')
	if e := next(t, in, "dtmf"); e.Digit != '5' {
		t.Fatalf("dtmf %q", e.Digit)
	}
	call.Hangup("done", 16)
	if e := next(t, in, "hangup"); e.Code != 16 {
		t.Fatalf("hangup %+v", e)
	}
}

func TestWrongPasswordRefused(t *testing.T) {
	a, b := node(t), node(t)
	b.Handle(Handlers{OnNew: func(c *Call, ies IEs) { c.Authenticate("right") }})
	call := a.Dial(addr(b), DialParams{Called: "500", Secret: "wrong", Formats: FormatULAW, Preferred: FormatULAW})
	if e := next(t, call, "hangup"); e.Code != 21 {
		t.Fatalf("hangup %+v", e)
	}
}

func TestRegistration(t *testing.T) {
	a, b := node(t), node(t)
	reg := make(chan string, 1)
	b.Handle(Handlers{OnRegister: func(u string) (string, bool) { return "reg secret", u == "acme" },
		Registered: func(u string, from netip.AddrPort, refresh int) { reg <- u }})
	c := a.Register(addr(b), "acme", "reg secret", 120)
	if e := next(t, c, "registered", "hangup"); e.Kind != "registered" || e.Code != 120 {
		t.Fatalf("%+v", e)
	}
	if u := <-reg; u != "acme" {
		t.Fatal(u)
	}
	c = a.Register(addr(b), "acme", "wrong", 120)
	if e := next(t, c, "registered", "hangup"); e.Kind != "hangup" {
		t.Fatalf("wrong secret registered: %+v", e)
	}
}
