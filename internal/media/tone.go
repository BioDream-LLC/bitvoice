package media

import (
	"encoding/binary"
	"math"
	"math/rand/v2"
	"sync"
	"time"
)

// G.711 (ITU-T G.711) companding, written from the standard's segment tables.

// LinearToUlaw encodes one 16-bit sample as mu-law.
func LinearToUlaw(s int16) byte {
	const bias, clip = 0x84, 32635
	x := int(s)
	sign := 0
	if x < 0 {
		x, sign = -x, 0x80
	}
	if x > clip {
		x = clip
	}
	x += bias
	exp := 7
	for mask := 0x4000; x&mask == 0 && exp > 0; mask >>= 1 {
		exp--
	}
	mant := (x >> (exp + 3)) & 0x0f
	return ^byte(sign | exp<<4 | mant)
}

// UlawToLinear decodes one mu-law byte.
func UlawToLinear(u byte) int16 {
	u = ^u
	sign := u & 0x80
	exp := int(u>>4) & 0x07
	mant := int(u & 0x0f)
	x := ((mant << 3) + 0x84) << exp
	x -= 0x84
	if sign != 0 {
		return int16(-x)
	}
	return int16(x)
}

// LinearToAlaw encodes one 16-bit sample as A-law.
func LinearToAlaw(s int16) byte {
	x := int(s) >> 3 // 13-bit
	sign := 0x80
	if x < 0 {
		x, sign = -x-1, 0
	}
	if x > 0xfff {
		x = 0xfff
	}
	var out int
	if x < 32 {
		out = x >> 1
	} else {
		exp := 1
		for v := x >> 5; v > 1; v >>= 1 {
			exp++
		}
		out = exp<<4 | (x>>exp)&0x0f
	}
	return byte(out|sign) ^ 0x55
}

// AlawToLinear decodes one A-law byte.
func AlawToLinear(a byte) int16 {
	a ^= 0x55
	sign := a & 0x80
	exp := int(a>>4) & 0x07
	mant := int(a & 0x0f)
	var x int
	if exp == 0 {
		x = mant<<4 + 8
	} else {
		x = (mant<<4 + 0x108) << (exp - 1)
	}
	if sign == 0 {
		return int16(-x)
	}
	return int16(x)
}

// Tone is a sum of sine waves played with a cadence, e.g. a ringback or confirmation tone.
type Tone struct {
	Freqs  []float64
	Level  float64       // 0..1 of full scale
	On     time.Duration // 0 for continuous
	Off    time.Duration
	Repeat int // number of on/off cycles, 0 for until stopped
	// Cadence, when set, replaces On and Off: alternating on and off times (British double ring).
	Cadence []time.Duration
}

// onAt reports whether sample n of a tone is sounding, and the length of one cycle in samples.
func (t Tone) onAt(n int) (bool, int) {
	if len(t.Cadence) > 0 {
		total := 0
		for _, d := range t.Cadence {
			total += int(d.Seconds() * 8000)
		}
		pos := n % total
		for i, d := range t.Cadence {
			s := int(d.Seconds() * 8000)
			if pos < s {
				return i%2 == 0, total
			}
			pos -= s
		}
		return false, total
	}
	cycle := int((t.On + t.Off).Seconds() * 8000)
	if cycle == 0 {
		return true, 0
	}
	return n%cycle < int(t.On.Seconds()*8000), cycle
}

// Some tones used by the system.
var (
	ToneConfirm  = Tone{Freqs: []float64{1400}, Level: 0.25, On: 150 * time.Millisecond, Off: 100 * time.Millisecond, Repeat: 3}
	ToneRingback = Tone{Freqs: []float64{440, 480}, Level: 0.2, On: 2 * time.Second, Off: 4 * time.Second}
	ToneBeep     = Tone{Freqs: []float64{1000}, Level: 0.3, On: 400 * time.Millisecond, Off: 100 * time.Millisecond, Repeat: 1}
	ToneHold     = Tone{Freqs: []float64{440, 660}, Level: 0.12, On: 300 * time.Millisecond, Off: 4700 * time.Millisecond}
	// ToneOff is a single low confirmation (something switched off); ToneError a long low buzz.
	ToneOff   = Tone{Freqs: []float64{700}, Level: 0.25, On: 400 * time.Millisecond, Off: 100 * time.Millisecond, Repeat: 1}
	ToneError = Tone{Freqs: []float64{300}, Level: 0.25, On: 900 * time.Millisecond, Off: 100 * time.Millisecond, Repeat: 1}
)

// Player sends generated G.711 audio to an endpoint in real time, 20 ms per packet.
type Player struct {
	ep    *Endpoint
	pt    byte
	alaw  bool
	ssrc  uint32
	seq   uint16
	ts    uint32
	stop  chan struct{}
	once  sync.Once
	doneC chan struct{}
}

// NewPlayer prepares a player for an endpoint using payload type 0 (PCMU) or 8 (PCMA).
func NewPlayer(ep *Endpoint, pt int) *Player {
	return &Player{ep: ep, pt: byte(pt), alaw: pt == 8, ssrc: rand.Uint32(), seq: uint16(rand.Uint32()),
		ts: rand.Uint32(), stop: make(chan struct{}), doneC: make(chan struct{})}
}

// Play plays a tone, then returns (immediately for an endless tone; use Stop).
func (p *Player) Play(t Tone) {
	go func() {
		defer close(p.doneC)
		const frame = 160
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		var n int // sample index
		_, cycle := t.onAt(0)
		total := 0
		if t.Repeat > 0 {
			total = cycle * t.Repeat
		}
		pkt := make([]byte, 12+frame)
		for {
			select {
			case <-p.stop:
				return
			case <-tick.C:
			}
			for i := 0; i < frame; i++ {
				var v float64
				if on, _ := t.onAt(n); on {
					for _, f := range t.Freqs {
						v += math.Sin(2 * math.Pi * f * float64(n) / 8000)
					}
					v = v / float64(len(t.Freqs)) * t.Level * 32767
				}
				s := int16(v)
				if p.alaw {
					pkt[12+i] = LinearToAlaw(s)
				} else {
					pkt[12+i] = LinearToUlaw(s)
				}
				n++
			}
			p.packet(pkt)
			if total > 0 && n >= total {
				return
			}
		}
	}()
}

// PlayPCM plays 8 kHz audio once, then returns. Done is closed when it has finished or was stopped.
func (p *Player) PlayPCM(pcm []int16) {
	go func() {
		defer close(p.doneC)
		const frame = 160
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		pkt := make([]byte, 12+frame)
		for i := 0; i < len(pcm); i += frame {
			select {
			case <-p.stop:
				return
			case <-tick.C:
			}
			for j := range frame {
				var s int16
				if i+j < len(pcm) {
					s = pcm[i+j]
				}
				if p.alaw {
					pkt[12+j] = LinearToAlaw(s)
				} else {
					pkt[12+j] = LinearToUlaw(s)
				}
			}
			p.packet(pkt)
		}
	}()
}

// PlayStream plays 8 kHz audio from next, one piece after another without a gap, until stopped:
// hold music, track after track. When next has nothing (nil), the fallback tone plays instead.
func (p *Player) PlayStream(next func() []int16, fallback Tone) {
	go func() {
		defer close(p.doneC)
		const frame = 160
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		var cur []int16
		tone := ToneSamples(Tone{Freqs: fallback.Freqs, Level: fallback.Level, On: fallback.On, Off: fallback.Off, Repeat: 1})
		pkt := make([]byte, 12+frame)
		for {
			select {
			case <-p.stop:
				return
			case <-tick.C:
			}
			for j := range frame {
				for len(cur) == 0 {
					if cur = next(); len(cur) == 0 {
						cur = tone
					}
				}
				s := cur[0]
				cur = cur[1:]
				if p.alaw {
					pkt[12+j] = LinearToAlaw(s)
				} else {
					pkt[12+j] = LinearToUlaw(s)
				}
			}
			p.packet(pkt)
		}
	}()
}

// ToneSamples renders a tone with a finite Repeat as 8 kHz audio, for joining to other audio.
func ToneSamples(t Tone) []int16 {
	_, cycle := t.onAt(0)
	out := make([]int16, cycle*max(t.Repeat, 1))
	for n := range out {
		if on, _ := t.onAt(n); on {
			var v float64
			for _, f := range t.Freqs {
				v += math.Sin(2 * math.Pi * f * float64(n) / Rate)
			}
			out[n] = int16(v / float64(len(t.Freqs)) * t.Level * 32767)
		}
	}
	return out
}

func (p *Player) packet(pkt []byte) {
	pkt[0] = 0x80
	pkt[1] = p.pt
	binary.BigEndian.PutUint16(pkt[2:], p.seq)
	binary.BigEndian.PutUint32(pkt[4:], p.ts)
	binary.BigEndian.PutUint32(pkt[8:], p.ssrc)
	p.seq++
	p.ts += uint32(len(pkt) - 12)
	p.ep.SendRTP(pkt)
}

// Stop ends playback.
func (p *Player) Stop() {
	p.once.Do(func() { close(p.stop) })
}

// Done is closed when playback has finished.
func (p *Player) Done() <-chan struct{} { return p.doneC }
