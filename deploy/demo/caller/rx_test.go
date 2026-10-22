package main

import (
	"math"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/p25"
	"github.com/biodream-llc/bitvoice/internal/p25/imbe"
)

// A unit's transmission, as the demo's radio system sends it, decodes at a P25 receiver as speech the
// whole way through (frames 12-17 carry a clear sync word: zeros there read as encrypted, and the
// rest went silent), with the unit as the talker and its emergency flag.
func TestTransmissionHeard(t *testing.T) {
	var talker *p25.Talker
	loud, frames := 0, 0
	rx := p25.NewReceiver(demoGroup, nil, p25.Events{
		Talker: func(tk *p25.Talker) {
			if tk != nil {
				talker = tk
			}
		},
		Voice: func(pcm []int16) {
			var e float64
			for _, s := range pcm {
				e += float64(s) * float64(s)
			}
			frames++
			if math.Sqrt(e/float64(len(pcm))) >= 300 {
				loud++
			}
		}})
	word := &p25.PTTWord{Unit: p25.Unit{WACN: demoWACN, System: demoSystem, ID: 1207}, Priority: 200}
	pkt := func(ty int) p25.PacketType {
		return p25.PacketType{Type: ty, TSN: 2, Options: p25.ServiceOptions{Emergency: true, Priority: 4}}
	}
	parse := func(p *p25.Payload) *p25.Payload {
		q, err := p25.Parse(p.Marshal())
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	rx.Receive(parse(&p25.Payload{Packet: pkt(p25.PTTStart), PTT: word}))
	h := p25.HeaderWord{AlgID: p25.AlgClear, Group: demoGroup, VBB: 2}
	rx.Receive(parse(&p25.Payload{S: true, Packet: pkt(p25.PTTProgress), PTT: word, Header: &h}))
	enc := imbe.NewEncoder()
	v := newTalk(1207)
	const n = 150
	for i := range n {
		f := p25.ClearVoiceFrame(i, enc.Encode(v.next()))
		rx.Receive(parse(&p25.Payload{S: true, Packet: pkt(p25.PTTProgress), PTT: word, Voice: []p25.IMBEBlock{f}}))
	}
	if frames != n || loud < n*4/10 {
		t.Fatalf("%d frames decoded, %d loud", frames, loud)
	}
	if talker == nil || talker.Unit.ID != 1207 || !talker.Emergency || talker.Alg != p25.AlgClear {
		t.Fatalf("talker %+v", talker)
	}
}
