package m3ua

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

// Against OsmoSTP (BITVOICE_TEST_OSMOSTP=host:port of its M3UA-over-TCP listener), with the Osmocom
// SCCP echo user at point code 2 behind it: the ASP comes up with a routing key registered for its
// point code, and an SCCP unitdata to SSN 202 (libosmo-sigtran's echo service) comes back.
func TestAgainstOsmoSTP(t *testing.T) {
	addr := os.Getenv("BITVOICE_TEST_OSMOSTP")
	if addr == "" {
		t.Skip("set BITVOICE_TEST_OSMOSTP to OsmoSTP's M3UA-over-TCP address")
	}
	a := New(Config{Addr: addr, OPC: 1, NI: 0, Heartbeat: time.Second}) // OsmoSTP and its users default to the international network
	got := make(chan []byte, 4)
	a.Receive = func(l Label, data []byte) {
		if l.SI == 3 && l.DPC == 1 && l.OPC == 2 {
			got <- data
		}
	}
	ups := make(chan bool, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx, func(up bool, err error) {
		if err != nil {
			t.Log(err)
		}
		ups <- up
	})
	select {
	case up := <-ups:
		if !up {
			t.Fatal("the ASP did not come up")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no ASP active in 10 s")
	}
	if a.RC() == 0 {
		t.Fatal("no routing context registered")
	}
	// UDT, class 0; called: route on SSN, PC 2, SSN 202; calling: PC 1, SSN 8; data.
	udt := []byte{0x09, 0x00, 0x03, 0x07, 0x0b, 0x04, 0x43, 0x02, 0x00, 0xca, 0x04, 0x43, 0x01, 0x00, 0x08, 0x05, 'h', 'e', 'l', 'l', 'o'}
	if err := a.Send(Label{OPC: 1, DPC: 2, SI: 3, NI: 0, SLS: 0}, udt); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-got:
		if b[0] != 0x09 || !bytes.HasSuffix(b, []byte("hello")) {
			t.Fatalf("answer % x", b)
		}
		t.Logf("echoed: % x", b)
	case <-time.After(5 * time.Second):
		t.Fatal("no echo from SSN 202")
	}
	time.Sleep(2500 * time.Millisecond) // heartbeats answered meanwhile
	if !a.Active() {
		t.Fatal("the ASP went down")
	}
}
