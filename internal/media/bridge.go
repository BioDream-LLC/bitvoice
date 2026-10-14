package media

import (
	"encoding/binary"
	"math/rand/v2"
	"sync"
	"time"
)

// Bridge mixes audio between several parties: each hears the sum of the others it is allowed to hear,
// every 20 ms, as G.711 µ-law (each party's endpoint converts to its own codec). It serves supervisor
// listen, whisper and barge, and conference rooms. A wideband bridge mixes at 16 kHz: parties on
// wideband codecs (G.722, Opus) hear each other in HD, sent as G.722; the others hear the same mix at
// 8 kHz.
type Bridge struct {
	mu    sync.Mutex
	parts map[string]*part
	hears func(listener, speaker string) bool
	stop  chan struct{}
	once  sync.Once
	// inject is audio everyone hears (join and leave tones); onMix sees the whole room each 20 ms.
	inject []int16
	onMix  func(frame []int16)
	// speaking is who was talking in the last few frames, for the live view.
	speaking map[string]int
	// wide mixes at 16 kHz (n is the samples in a 20 ms frame: 160 or 320).
	wide bool
	up   Doubler // for injected audio, which is 8 kHz
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
	// In a wideband bridge: the G.722 encoder for a wideband party, the downsampler for the others.
	enc  *G722Encoder
	down Halver
}

// NewBridge starts a bridge. hears says whether one party hears another; nil means everyone hears
// everyone.
func NewBridge(hears func(listener, speaker string) bool) *Bridge {
	b := &Bridge{parts: map[string]*part{}, hears: hears, stop: make(chan struct{})}
	go b.run()
	return b
}

// NewWideBridge starts a bridge that mixes at 16 kHz (HD conferences).
func NewWideBridge(hears func(listener, speaker string) bool) *Bridge {
	b := &Bridge{parts: map[string]*part{}, hears: hears, stop: make(chan struct{}), wide: true}
	go b.run()
	return b
}

// Wide says whether the bridge mixes at 16 kHz.
func (b *Bridge) Wide() bool { return b.wide }

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

// Inject plays audio (8 kHz) to every party, mixed with whoever is talking.
func (b *Bridge) Inject(pcm []int16) {
	b.mu.Lock()
	b.inject = append(b.inject, pcm...)
	if len(b.inject) > 8000*10 {
		b.inject = b.inject[len(b.inject)-8000*10:]
	}
	b.mu.Unlock()
}

// OnMix sets a function given everything said in the room, every 20 ms (a recording); nil stops it.
func (b *Bridge) OnMix(f func(frame []int16)) {
	b.mu.Lock()
	b.onMix = f
	b.mu.Unlock()
}

// Speaking says whether a party talked in the last half second.
func (b *Bridge) Speaking(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.speaking[id] > 0
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
	if b.wide {
		p.buf = append(p.buf, p.dec.Wideband(pt, pkt[12:])...)
	} else {
		p.buf = append(p.buf, p.dec.Narrowband(pt, pkt[12:])...)
	}
	if over := len(p.buf) - 4*b.frame(); over > 0 { // keep at most 80 ms: late audio is dropped, not delayed
		p.buf = p.buf[over:]
	}
}

// frame is the samples in 20 ms.
func (b *Bridge) frame() int {
	if b.wide {
		return 320
	}
	return 160
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
	n := b.frame()
	frames := map[string][]int16{}
	if b.speaking == nil {
		b.speaking = map[string]int{}
	}
	for id, p := range b.parts {
		f := make([]int16, n)
		got := copy(f, p.buf)
		p.buf = p.buf[got:]
		frames[id] = f
		lvl := make([]int32, n)
		for i, v := range f {
			lvl[i] = int32(v)
		}
		if voiced(lvl) {
			b.speaking[id] = 25 // half a second of frames
		} else if b.speaking[id] > 0 {
			b.speaking[id]--
		}
	}
	for id := range b.speaking {
		if b.parts[id] == nil {
			delete(b.speaking, id)
		}
	}
	var injected []int16
	if len(b.inject) > 0 {
		injected = make([]int16, 160)
		k := copy(injected, b.inject)
		b.inject = b.inject[k:]
		if b.wide {
			injected = b.up.Double(injected)
		}
	}
	if b.onMix != nil {
		all := make([]int32, n)
		for _, f := range frames {
			for i, v := range f {
				all[i] += int32(v)
			}
		}
		for i, v := range injected {
			all[i] += int32(v)
		}
		frame := make([]int16, n)
		for i, v := range all {
			frame[i] = int16(max(-32768, min(32767, v)))
		}
		if b.wide {
			frame = decimate(frame) // recordings stay 8 kHz
		}
		b.onMix(frame)
	}
	type out struct {
		ep  *Endpoint
		pkt []byte
	}
	var sends []out
	for id, p := range b.parts {
		mixed := make([]int32, n)
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
		if injected != nil {
			any = true
			for i, v := range injected {
				mixed[i] += int32(v)
			}
		}
		if p.quiet && !voiced(mixed) {
			continue
		}
		pcm := make([]int16, n)
		if any {
			for i, v := range mixed {
				pcm[i] = int16(max(-32768, min(32767, v)))
			}
		}
		pkt := make([]byte, 12, 172)
		pkt[0], pkt[1] = 0x80, PTPCMU
		binary.BigEndian.PutUint16(pkt[2:], p.seq)
		binary.BigEndian.PutUint32(pkt[4:], p.ts)
		binary.BigEndian.PutUint32(pkt[8:], p.ssrc)
		p.seq++
		p.ts += 160 // G.711 and G.722 alike: G.722's RTP clock runs at 8000
		switch {
		case b.wide && p.ep.Wideband():
			if p.enc == nil {
				p.enc = NewG722Encoder()
			}
			pkt[1] = PTG722
			pkt = append(pkt, p.enc.Encode(pcm)...)
		case b.wide:
			for _, v := range p.down.Halve(pcm) {
				pkt = append(pkt, LinearToUlaw(v))
			}
		default:
			for _, v := range pcm {
				pkt = append(pkt, LinearToUlaw(v))
			}
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

// decimate halves the rate of a frame without a filter state (for recordings, where a little aliasing
// above 4 kHz does not matter): the average of each pair.
func decimate(f []int16) []int16 {
	out := make([]int16, len(f)/2)
	for i := range out {
		out[i] = int16((int32(f[2*i]) + int32(f[2*i+1])) / 2)
	}
	return out
}
