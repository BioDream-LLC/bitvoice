package sccp

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sigtran/m3ua"
)

func TestRoundTrip(t *testing.T) {
	for _, m := range []Message{
		{Type: TypeUDT, Class: 0, Called: Address{RouteOnSSN: true, PC: 2, SSN: 202}, Calling: Address{RouteOnSSN: true, PC: 1, SSN: 8}, Data: []byte("hello")},
		{Type: TypeUDT, Class: 1, ReturnOnError: true,
			Called:  Address{PC: -1, SSN: 6, GT: &GT{Indicator: 4, TranslationType: 0, NumberingPlan: 1, NatureOfAddress: 4, Digits: "15551230001"}},
			Calling: Address{PC: -1, SSN: 8, GT: &GT{Indicator: 4, NumberingPlan: 1, NatureOfAddress: 4, Digits: "15559990000"}}, Data: []byte{1, 2, 3}},
		{Type: TypeXUDT, HopCounter: 9, Called: Address{RouteOnSSN: true, PC: 0x3FFF, SSN: 146}, Calling: Address{PC: 5, SSN: 146}, Data: bytes.Repeat([]byte{7}, 200)},
		{Type: TypeUDTS, Cause: CauseUnequippedUser, Called: Address{RouteOnSSN: true, PC: 1, SSN: 8}, Calling: Address{RouteOnSSN: true, PC: 2, SSN: 200}, Data: []byte("x")},
		{Type: TypeUDT, Called: Address{PC: -1, GT: &GT{Indicator: 1, NatureOfAddress: 4, Digits: "123"}}, Calling: Address{PC: -1, GT: &GT{Indicator: 2, TranslationType: 10, Digits: "4567"}}, Data: []byte{0}},
	} {
		b, err := m.Encode()
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(b)
		if err != nil {
			t.Fatalf("% x: %v", b, err)
		}
		again, _ := got.Encode()
		if !bytes.Equal(b, again) || !bytes.Equal(got.Data, m.Data) || got.Called.PC != m.Called.PC || got.Called.SSN != m.Called.SSN {
			t.Fatalf("% x\n% x\n%+v", b, again, got)
		}
		if m.Called.GT != nil && got.Called.GT.Digits != m.Called.GT.Digits {
			t.Fatalf("digits %q, want %q", got.Called.GT.Digits, m.Called.GT.Digits)
		}
	}
}

// Q.713's own example layout: a UDT's three pointers and the parts they point to.
func TestUDTLayout(t *testing.T) {
	b, _ := Message{Type: TypeUDT, Called: Address{RouteOnSSN: true, PC: 2, SSN: 202}, Calling: Address{RouteOnSSN: true, PC: 1, SSN: 8}, Data: []byte("hello")}.Encode()
	want := []byte{0x09, 0x00, 0x03, 0x07, 0x0b, 0x04, 0x43, 0x02, 0x00, 0xca, 0x04, 0x43, 0x01, 0x00, 0x08, 0x05, 'h', 'e', 'l', 'l', 'o'}
	if !bytes.Equal(b, want) {
		t.Fatalf("% x\n% x", b, want)
	}
}

func TestTranslate(t *testing.T) {
	table := []Translation{{Prefix: "1555", TranslationType: -1, PC: 2, SSN: 6}, {Prefix: "15551", TranslationType: 0, PC: 3}, {Prefix: "44", TranslationType: -1, PC: 4, SSN: 8}}
	gt := func(d string) Address {
		return Address{PC: -1, SSN: 6, GT: &GT{Indicator: 4, NumberingPlan: 1, NatureOfAddress: 4, Digits: d}}
	}
	for _, c := range []struct {
		digits  string
		pc, ssn int
		ok      bool
	}{{"15551230001", 3, 6, true}, {"15559990000", 2, 6, true}, {"4420", 4, 8, true}, {"3312", 0, 0, false}} {
		a, ok := Translate(gt(c.digits), table)
		if ok != c.ok || (ok && (!a.RouteOnSSN || a.PC != c.pc || a.SSN != c.ssn || a.GT == nil)) {
			t.Errorf("%s: %+v %v", c.digits, a, ok)
		}
	}
	if _, ok := Translate(Address{RouteOnSSN: true, PC: 9, SSN: 6}, table); ok {
		t.Error("an address routed on its subsystem was translated")
	}
	m := Message{Type: TypeUDT, ReturnOnError: true, Called: gt("3312"), Calling: Address{RouteOnSSN: true, PC: 1, SSN: 8}, Data: []byte("q")}
	r, ok := Return(m, CauseNoTranslationForAddress)
	if !ok || r.Type != TypeUDTS || r.Called.PC != 1 || r.Cause != CauseNoTranslationForAddress {
		t.Fatalf("%+v", r)
	}
	m.ReturnOnError = false
	if _, ok := Return(m, 1); ok {
		t.Fatal("returned without return on error")
	}
}

func TestRefused(t *testing.T) {
	for _, b := range [][]byte{nil, {0x09}, {0x09, 0x00, 0x03, 0x07, 0x40}, {0x09, 0x00, 0x00, 0x00, 0x00}, {0x13, 0, 0, 0, 0}, {0x09, 0x00, 0x03, 0x04, 0x05, 0x01, 0x80, 0x01, 0x01, 0x01, 0x00}} {
		if _, err := Decode(b); err == nil {
			t.Errorf("% x decoded", b)
		}
	}
}

func FuzzDecode(f *testing.F) {
	b, _ := Message{Type: TypeUDT, Called: Address{RouteOnSSN: true, PC: 2, SSN: 202}, Calling: Address{PC: 1, SSN: 8}, Data: []byte("x")}.Encode()
	f.Add(b)
	f.Fuzz(func(t *testing.T, b []byte) {
		if m, err := Decode(b); err == nil {
			_, _ = m.Encode()
		}
	})
}

// Through OsmoSTP to libosmo-sigtran's SCCP (BITVOICE_TEST_OSMOSTP, with Osmocom's echo user at point
// code 2): a UDT to SSN 202 (echo) comes back with the addresses swapped and the data whole, an XUDT
// too, and a UDT asking for return on error to SSN 200 (no user there) comes back as a UDTS.
func TestAgainstOsmocomSCCP(t *testing.T) {
	addr := os.Getenv("BITVOICE_TEST_OSMOSTP")
	if addr == "" {
		t.Skip("set BITVOICE_TEST_OSMOSTP to OsmoSTP's M3UA-over-TCP address")
	}
	const pc = 4 // a point code of its own (other tests register theirs with the same STP)
	a := m3ua.New(m3ua.Config{Addr: addr, OPC: pc})
	got := make(chan Message, 8)
	a.Receive = func(l m3ua.Label, data []byte) {
		if l.SI != SI {
			return
		}
		m, err := Decode(data)
		if err != nil {
			t.Errorf("Osmocom's answer % x: %v", data, err)
			return
		}
		got <- m
	}
	ups := make(chan bool, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx, func(up bool, _ error) { ups <- up })
	if up := <-ups; !up {
		t.Fatal("no M3UA association")
	}
	send := func(m Message) Message {
		t.Helper()
		b, err := m.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Send(m3ua.Label{OPC: pc, DPC: 2, SI: SI}, b); err != nil {
			t.Fatal(err)
		}
		select {
		case r := <-got:
			return r
		case <-time.After(5 * time.Second):
			t.Fatalf("no answer to %+v", m)
		}
		return Message{}
	}
	me := Address{RouteOnSSN: true, PC: pc, SSN: 8}
	r := send(Message{Type: TypeUDT, Class: 0, Called: Address{RouteOnSSN: true, PC: 2, SSN: 202}, Calling: me, Data: []byte("unitdata from BitVoice")})
	if r.Type != TypeUDT || r.Called.PC != pc || r.Called.SSN != 8 || r.Calling.PC != 2 || r.Calling.SSN != 202 || string(r.Data) != "unitdata from BitVoice" {
		t.Fatalf("echo %+v", r)
	}
	long := bytes.Repeat([]byte("x"), 250)
	r = send(Message{Type: TypeXUDT, HopCounter: 15, Called: Address{RouteOnSSN: true, PC: 2, SSN: 202}, Calling: me, Data: long})
	if !bytes.Equal(r.Data, long) {
		t.Fatalf("XUDT echo %+v", r)
	}
	t.Logf("XUDT answered as type %#x", r.Type)
	r = send(Message{Type: TypeUDT, Class: 0, ReturnOnError: true, Called: Address{RouteOnSSN: true, PC: 2, SSN: 200}, Calling: me, Data: []byte("to nobody")})
	if r.Type != TypeUDTS || r.Called.SSN != 8 || string(r.Data) != "to nobody" {
		t.Fatalf("UDTS %+v", r)
	}
	t.Logf("UDTS cause %#x", r.Cause)
}
