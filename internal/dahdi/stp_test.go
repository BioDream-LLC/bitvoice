package dahdi

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/isdn"
	"github.com/biodream-llc/bitvoice/internal/ss7"
	"github.com/biodream-llc/bitvoice/internal/ss7/sccp"
)

// stpRig is the bridge as STP 1 between two switches, 3 and 5, each on a span of its own; with an
// IP STP when addr is set.
type stpRig struct {
	b        *Bridge
	switches map[int]*ss7.SS7
	got      map[int]chan sccp.Message
}

func newSTPRig(t *testing.T, addr string) *stpRig {
	t.Helper()
	ss7.T4E, ss7.Fill = 200*time.Millisecond, 10*time.Millisecond
	ss7.SLTWait, ss7.SLTPeriod = 300*time.Millisecond, 500*time.Millisecond
	t.Cleanup(func() { ss7.SLTWait, ss7.SLTPeriod = 4*time.Second, 30*time.Second })
	r := &stpRig{switches: map[int]*ss7.SS7{}, got: map[int]chan sccp.Message{}}
	links := map[int]isdn.Link{}
	far := map[int]isdn.Link{}
	var spans []SpanConf
	for i, apc := range []int{3, 5} {
		d := 24 + 30*i
		bridgeEnd, farEnd := NewFakeD()
		links[d], far[apc] = bridgeEnd, farEnd
		spans = append(spans, SpanConf{D: d, B: []int{d - 2, d - 1}, SS7: &SS7Conf{OPC: 1, APC: apc, NI: 0, FirstCIC: 1}})
	}
	old := OpenD
	OpenD = func(n int) (isdn.Link, error) {
		if l := links[n]; l != nil {
			return l, nil
		}
		return nil, errors.New("not a signalling link")
	}
	t.Cleanup(func() { OpenD = old })
	fakes := map[int]*Fake{}
	var fmu sync.Mutex // the ports open their channels at once
	cfg := Config{Spans: spans, STP: &STPConf{OPC: 1, NI: 0, Relay: true, M3UA: addr,
		Translations: []sccp.Translation{{Prefix: "1555", TranslationType: -1, PC: 5, SSN: 6}, {Prefix: "4420", TranslationType: -1, PC: 2, SSN: 202}}}}
	b, err := New(context.Background(), cfg, func(n int, _ bool) (Channel, error) {
		fmu.Lock()
		defer fmu.Unlock()
		if fakes[n] == nil {
			fakes[n] = NewFake()
		}
		return fakes[n], nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	r.b = b
	for _, pc := range []int{3, 5} {
		sw := ss7.NewLinkSet(ss7.Config{MTPConfig: ss7.MTPConfig{OPC: pc, APC: 1, NI: 0, Emergency: true}, CICs: []int{1, 2}})
		ch := make(chan sccp.Message, 8)
		sw.User = func(si int, l ss7.Label, data []byte) {
			if m, err := sccp.Decode(data); si == sccp.SI && err == nil {
				ch <- m
			}
		}
		r.switches[pc], r.got[pc] = sw, ch
		go sw.AddLink(far[pc], 0)
		t.Cleanup(sw.Close)
	}
	deadline := time.Now().Add(15 * time.Second)
	for !(r.switches[3].IsUp() && r.switches[5].IsUp()) {
		if time.Now().After(deadline) {
			t.Fatalf("link sets not up: %v", b.Status())
		}
		time.Sleep(20 * time.Millisecond)
	}
	return r
}

func (r *stpRig) send(t *testing.T, from, dpc int, m sccp.Message) {
	t.Helper()
	b, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.switches[from].SendMTP(sccp.SI, ss7.Label{OPC: from, DPC: dpc, SLS: 1}, b); err != nil {
		t.Fatal(err)
	}
}

func (r *stpRig) want(t *testing.T, at int) sccp.Message {
	t.Helper()
	select {
	case m := <-r.got[at]:
		return m
	case <-time.After(5 * time.Second):
		t.Fatalf("nothing reached point code %d; %v", at, r.b.Status())
	}
	return sccp.Message{}
}

// The bridge as STP between two switches on its link sets: a message for the other switch is passed
// on unchanged; a global title addressed to the STP is translated to the other switch's subsystem;
// one it cannot translate comes back as a UDTS when asked; one for a point code nobody serves too.
// (Passed on counts every message the STP sends on: the translated one and the returns too.)
func TestSTPBetweenLinkSets(t *testing.T) {
	r := newSTPRig(t, "")
	at3 := sccp.Address{RouteOnSSN: true, PC: 3, SSN: 8}
	r.send(t, 3, 5, sccp.Message{Type: sccp.TypeUDT, Called: sccp.Address{RouteOnSSN: true, PC: 5, SSN: 6}, Calling: at3, Data: []byte("hi 5")})
	if m := r.want(t, 5); string(m.Data) != "hi 5" || m.Called.SSN != 6 {
		t.Fatalf("passed on: %+v", m)
	}
	gt := sccp.Address{PC: -1, SSN: 6, GT: &sccp.GT{Indicator: 4, NumberingPlan: 1, NatureOfAddress: 4, Digits: "15551230000"}}
	r.send(t, 3, 1, sccp.Message{Type: sccp.TypeUDT, Called: gt, Calling: at3, Data: []byte("by title")})
	m := r.want(t, 5)
	if string(m.Data) != "by title" || !m.Called.RouteOnSSN || m.Called.PC != 5 || m.Called.SSN != 6 || m.Called.GT == nil || m.Called.GT.Digits != "15551230000" {
		t.Fatalf("translated: %+v", m)
	}
	unknown := gt
	unknown.GT = &sccp.GT{Indicator: 4, NumberingPlan: 1, NatureOfAddress: 4, Digits: "3312"}
	r.send(t, 3, 1, sccp.Message{Type: sccp.TypeUDT, ReturnOnError: true, Called: unknown, Calling: at3, Data: []byte("nowhere")})
	if m := r.want(t, 3); m.Type != sccp.TypeUDTS || m.Cause != sccp.CauseNoTranslationForAddress || string(m.Data) != "nowhere" {
		t.Fatalf("no translation: %+v", m)
	}
	r.send(t, 3, 9, sccp.Message{Type: sccp.TypeXUDT, HopCounter: 10, ReturnOnError: true, Called: sccp.Address{RouteOnSSN: true, PC: 9, SSN: 6}, Calling: at3, Data: []byte("to 9")})
	if m := r.want(t, 3); m.Type != sccp.TypeXUDTS || m.Cause != sccp.CauseMTPFailure {
		t.Fatalf("no route: %+v", m)
	}
	if st := r.b.Status()["stp"]; !strings.Contains(st, "4 passed on, 1 translated, 2 returned") {
		t.Fatalf("status %q", st)
	}
}

// With an IP STP (BITVOICE_TEST_OSMOSTP: OsmoSTP, Osmocom's SCCP echo user at point code 2 behind it):
// a switch on the bridge's link set reaches the echo service over M3UA, by point code and by global
// title, and its echo comes back to the switch through the bridge (the STP registered the switch's
// point code with the IP STP).
func TestSTPToOsmoSTP(t *testing.T) {
	addr := os.Getenv("BITVOICE_TEST_OSMOSTP")
	if addr == "" {
		t.Skip("set BITVOICE_TEST_OSMOSTP to OsmoSTP's M3UA-over-TCP address")
	}
	r := newSTPRig(t, addr)
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(r.b.Status()["stp"], "active") {
		if time.Now().After(deadline) {
			t.Fatalf("M3UA not active: %v", r.b.Status())
		}
		time.Sleep(50 * time.Millisecond)
	}
	at3 := sccp.Address{RouteOnSSN: true, PC: 3, SSN: 8}
	r.send(t, 3, 2, sccp.Message{Type: sccp.TypeUDT, Called: sccp.Address{RouteOnSSN: true, PC: 2, SSN: 202}, Calling: at3, Data: []byte("echo over IP")})
	if m := r.want(t, 3); string(m.Data) != "echo over IP" || m.Calling.PC != 2 || m.Calling.SSN != 202 {
		t.Fatalf("echo: %+v", m)
	}
	gt := sccp.Address{PC: -1, SSN: 202, GT: &sccp.GT{Indicator: 4, NumberingPlan: 1, NatureOfAddress: 4, Digits: "442071234567"}}
	r.send(t, 3, 1, sccp.Message{Type: sccp.TypeUDT, Called: gt, Calling: at3, Data: []byte("echo by title")})
	if m := r.want(t, 3); string(m.Data) != "echo by title" {
		t.Fatalf("echo by title: %+v", m)
	}
	t.Log(r.b.Status()["stp"])
}
