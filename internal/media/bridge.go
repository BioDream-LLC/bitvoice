package media

import (
	"encoding/binary"
	"math/rand/v2"
	"sync"
	"time"
)

// Bridge mixes audio between several parties: each hears the sum of the others it is allowed to hear,
// every 20 ms, as G.711 µ-law (each party's endpoint converts to its own codec). It serves supervisor
// listen, whisper and barge, and conference rooms.
type Bridge struct {
	mu    sync.Mutex
	parts map[string]*part
	hears func(listener, speaker string) bool
	stop  chan struct{}
	once  sync.Once
}

type part struct {
	ep   *Endpoint
	buf  []int16 // audio received and not yet mixed
	dec  Decoders
	seq  uint16
	ts   uint32
	ssrc uint32
	// quiet parties get nothing while nobody they hear is talking (a radio that keys on voice
	// must not be held on the air by silence).
	quiet bool
}

// NewBridge starts a bridge. hears says whether one party hears another; nil means everyone hears
// everyone.
func NewBridge(hears func(listener, speaker string) bool) *Bridge {
	b := &Bridge{parts: map[string]*part{}, hears: hears, stop: make(chan struct{})}
	go b.run()
	return b
}

// SetHearing changes who hears whom (listen becoming barge, say).
func (b *Bridge) SetHearing(hears func(listener, speaker string) bool) {
	b.mu.Lock()
	b.hears = hears
	b.mu.Unlock()
}

// Add puts a party in the bridge; its audio is taken from its endpoint from now on, and no longer
// forwarded anywhere else.
func (b *Bridge) Add(id string, ep *Endpoint) {
	p := &part{ep: ep, ssrc: rand.Uint32(), seq: uint16(rand.Uint32()), ts: rand.Uint32()}
	b.mu.Lock()
	b.parts[id] = p
	b.mu.Unlock()
	ep.mu.Lock()
	ep.peer, ep.echo = nil, false
	ep.mu.Unlock()
	ep.SetTap(func(pkt []byte) { b.in(id, pkt) })
}

// AddQuiet adds a party that is sent audio only while someone it hears is talking.
func (b *Bridge) AddQuiet(id string, ep *Endpoint) {
	b.Add(id, ep)
	b.mu.Lock()
	if p := b.parts[id]; p != nil {
		p.quiet = true
	}
	b.mu.Unlock()
}

// Remove takes a party out; its endpoint keeps whatever forwarding it is given next.
func (b *Bridge) Remove(id string) {
	b.mu.Lock()
	p := b.parts[id]
	delete(b.parts, id)
	b.mu.Unlock()
	if p != nil {
		p.ep.SetTap(nil)
	}
}

// Len is how many parties are in.
func (b *Bridge) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.parts)
}

// Close stops mixing and releases every party.
func (b *Bridge) Close() {
	b.once.Do(func() { close(b.stop) })
	b.mu.Lock()
	ps := b.parts
	b.parts = map[string]*part{}
	b.mu.Unlock()
	for _, p := range ps {
		p.ep.SetTap(nil)
	}
}

func (b *Bridge) in(id string, pkt []byte) {
	if len(pkt) < 12 || pkt[0]&0x3f != 0 {
		return
	}
	pt := int(pkt[1] & 0x7f)
	if !Transcodable(pt) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.parts[id]
	if p == nil {
		return
	}
	p.buf = append(p.buf, p.dec.Narrowband(pt, pkt[12:])...)
	if over := len(p.buf) - 4*160; over > 0 { // keep at most 80 ms: late audio is dropped, not delayed
		p.buf = p.buf[over:]
	}
}

func (b *Bridge) run() {
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-b.stop:
			return
		case <-tick.C:
		}
		b.mix()
	}
}

func (b *Bridge) mix() {
	b.mu.Lock()
	frames := map[string][]int16{}
	for id, p := range b.parts {
		f := make([]int16, 160)
		n := copy(f, p.buf)
		p.buf = p.buf[n:]
		frames[id] = f
	}
	type out struct {
		ep  *Endpoint
		pkt []byte
	}
	var sends []out
	for id, p := range b.parts {
		mixed := make([]int32, 160)
		any := false
		for sid, f := range frames {
			if sid == id || (b.hears != nil && !b.hears(id, sid)) {
				continue
			}
			any = true
			for i, v := range f {
				mixed[i] += int32(v)
			}
		}
		if p.quiet && !voiced(mixed) {
			continue
		}
		pkt := make([]byte, 12, 172)
		pkt[0], pkt[1] = 0x80, PTPCMU
		binary.BigEndian.PutUint16(pkt[2:], p.seq)
		binary.BigEndian.PutUint32(pkt[4:], p.ts)
		binary.BigEndian.PutUint32(pkt[8:], p.ssrc)
		p.seq++
		p.ts += 160
		for _, v := range mixed {
			if !any {
				v = 0
			}
			pkt = append(pkt, LinearToUlaw(int16(max(-32768, min(32767, v)))))
		}
		sends = append(sends, out{p.ep, pkt})
	}
	b.mu.Unlock()
	for _, s := range sends {
		s.ep.SendRTP(s.pkt)
	}
}

// voiced says whether a frame holds more than silence (a level above about -50 dBFS).
func voiced(f []int32) bool {
	var sum int64
	for _, v := range f {
		if v < 0 {
			v = -v
		}
		sum += int64(v)
	}
	return sum/int64(len(f)) > 100
}
