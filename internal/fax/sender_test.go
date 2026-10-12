package fax

import (
	"bytes"
	"testing"
	"time"
)

// TestSendToReceiver sends two pages from BitVoice's sender to BitVoice's receiver over a pretend
// T.38 link that carries each packet through the UDPTL encoding both ways.
func TestSendToReceiver(t *testing.T) {
	var rx *Receiver
	var tx *Sender
	// Each direction is a queue with one reader, so packets arrive in order, as on a quiet network.
	wire := func(deliver func(IFP)) *Link {
		q := make(chan []byte, 4096)
		in := NewLink(nil)
		go func() {
			for b := range q {
				in.Receive(b, deliver)
			}
		}()
		return NewLink(func(b []byte) { q <- append([]byte(nil), b...) })
	}
	fromRx := wire(func(p IFP) { tx.Handle(p) })
	fromTx := wire(func(p IFP) { rx.Handle(p) })
	rx = NewReceiver("+15550001111", fromRx.Send)
	pages := []Bitmap{testPage(), testPage()}
	for i := range pages {
		pages[i].Fine = true
	}
	tx = NewSender("+15550002222", pages, fromTx.Send)
	rx.Start()
	tx.Start()
	select {
	case <-tx.Done():
	case <-time.After(60 * time.Second):
		t.Fatal("the sender never finished")
	}
	select {
	case <-rx.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the receiver never finished")
	}
	sr, rr := tx.Result(), rx.Result()
	if sr.Err != nil || rr.Err != nil {
		t.Fatalf("send %v, receive %v", sr.Err, rr.Err)
	}
	if sr.Pages != 2 || len(rr.Pages) != 2 || sr.Rate != 14400 {
		t.Fatalf("sent %d pages at %d, received %d", sr.Pages, sr.Rate, len(rr.Pages))
	}
	if rr.RemoteID != "+15550002222" || sr.RemoteID != "+15550001111" {
		t.Fatalf("ids %q %q", rr.RemoteID, sr.RemoteID)
	}
	want := EncodeMH(pages[0], 0)
	for i, p := range rr.Pages {
		if !p.Params.Fine || !bytes.Equal(p.Data, want) {
			t.Fatalf("page %d: fine %v, %d bytes, want %d", i, p.Params.Fine, len(p.Data), len(want))
		}
	}
}
