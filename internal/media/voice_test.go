package media

import (
	"encoding/binary"
	"math"
	"slices"
	"testing"
)

// speechLike is a few seconds of a voiced, wobbling signal (codecs built for speech handle a
// steady sine poorly at low rates, so the test signal moves like a voice).
func speechLike(n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		t := float64(i) / 8000
		f0 := 180 + 40*math.Sin(2*math.Pi*3*t)
		env := 0.6 + 0.4*math.Sin(2*math.Pi*2*t)
		v := 0.0
		for h := 1; h <= 8; h++ {
			v += math.Sin(2*math.Pi*f0*float64(h)*t) / float64(h)
		}
		out[i] = int16(5000 * env * v)
	}
	return out
}

func pcmuPacket(seq int, ts uint32, pcm []int16) []byte {
	pkt := make([]byte, 12, 12+len(pcm))
	pkt[0], pkt[1] = 0x80, PTPCMU
	binary.BigEndian.PutUint16(pkt[2:], uint16(seq))
	binary.BigEndian.PutUint32(pkt[4:], ts)
	binary.BigEndian.PutUint32(pkt[8:], 0xabc)
	for _, v := range pcm {
		pkt = append(pkt, LinearToUlaw(v))
	}
	return pkt
}

// roundTrip sends PCMU through codec pt and back, checking the middle packets' numbering.
func roundTrip(t *testing.T, pt, ptime, wantBytes int, tsStep uint32) (in, out []int16) {
	in = speechLike(8000 * 3)
	there, back := NewTranscoder(PTPCMU, pt), NewTranscoder(pt, PTPCMU)
	step := 8 * ptime
	var lastTS uint32
	var lastSeq uint16
	got := 0
	for i := 0; i+step <= len(in); i += step {
		w := there.Convert(pcmuPacket(i/step, uint32(i), in[i:i+step]))
		if w == nil {
			continue
		}
		if w[1]&0x7f != byte(pt) {
			t.Fatalf("payload type %d", w[1]&0x7f)
		}
		ts, seq := binary.BigEndian.Uint32(w[4:]), binary.BigEndian.Uint16(w[2:])
		if got > 0 && (seq != lastSeq+1 || (tsStep != 0 && ts-lastTS != tsStep)) {
			t.Fatalf("packet %d: seq %d after %d, ts step %d", got, seq, lastSeq, ts-lastTS)
		}
		if wantBytes > 0 && len(w)-12 != wantBytes {
			t.Fatalf("payload of %d bytes, want %d", len(w)-12, wantBytes)
		}
		lastTS, lastSeq = ts, seq
		got++
		if n := back.Convert(w); n != nil {
			for _, b := range n[12:] {
				out = append(out, UlawToLinear(b))
			}
		}
	}
	return in, out
}

func TestTranscodeG729(t *testing.T) {
	in, out := roundTrip(t, PTG729, 20, 20, 160)
	snr, lag := snrWide(in, out)
	t.Logf("G.729: %.1f dB at delay %d", snr, lag)
	// CELP codecs keep how speech sounds, not its waveform: G.729's own regression floor is 2 dB
	// waveform SNR, so the loudness contour carries the test.
	if c := envelopeCorr(in, out, lag); snr < 1 || c < 0.8 {
		t.Fatalf("G.729 round trip: %.1f dB, envelope correlation %.2f", snr, c)
	}
}

func TestTranscodeOpus(t *testing.T) {
	in, out := roundTrip(t, PTOpus, 20, 0, 960)
	snr, lag := snrWide(in, out)
	t.Logf("Opus: %.1f dB at delay %d", snr, lag)
	if c := envelopeCorr(in, out, lag); snr < 3 || c < 0.9 {
		t.Fatalf("Opus round trip: %.1f dB, envelope correlation %.2f", snr, c)
	}
}

// A 30 ms G.711 stream into Opus: packets come out as whole Opus frames (20 or 40 ms) with
// timestamps that follow the audio.
func TestTranscodeOpusOddPtime(t *testing.T) {
	x := NewTranscoder(PTPCMU, PTOpus)
	in := speechLike(8000)
	var total uint32
	var prev uint32
	n := 0
	for i := 0; i+240 <= len(in); i += 240 {
		w := x.Convert(pcmuPacket(i/240, uint32(i), in[i:i+240]))
		if w == nil {
			continue
		}
		ts := binary.BigEndian.Uint32(w[4:])
		if n > 0 {
			if d := ts - prev; d != 960 && d != 1920 {
				t.Fatalf("timestamp step %d", d)
			}
			total += ts - prev
		}
		prev = ts
		n++
	}
	if total < 40000 {
		t.Fatalf("only %d of 48000 ticks came out", total)
	}
}

// A lost Opus packet is concealed: the PCMU side keeps a steady stream.
func TestOpusLossConcealed(t *testing.T) {
	enc, dec := NewTranscoder(PTPCMU, PTOpus), NewTranscoder(PTOpus, PTPCMU)
	in := speechLike(8000)
	var outTS []uint32
	for i := 0; i+160 <= len(in); i += 160 {
		w := enc.Convert(pcmuPacket(i/160, uint32(i), in[i:i+160]))
		if w == nil || i/160 == 20 {
			continue // packet 20 is lost on the way
		}
		if n := dec.Convert(w); n != nil {
			outTS = append(outTS, binary.BigEndian.Uint32(n[4:]))
			if len(n)-12 < 160 {
				t.Fatalf("short packet %d", len(n)-12)
			}
		}
	}
	for i := 1; i < len(outTS); i++ {
		if d := outTS[i] - outTS[i-1]; d != 160 && d != 320 {
			t.Fatalf("timestamp step %d at %d", d, i)
		}
	}
}

// snrWide is snrAt over delays up to 50 ms (codecs with lookahead and resampling delay more).
func snrWide(in, out []int16) (float64, int) {
	best, lag := -100.0, 0
	for d := 0; d < 400; d++ {
		var sig, noise float64
		for i := 800; i+d < len(out) && i < len(in)-800; i++ {
			s, o := float64(in[i]), float64(out[i+d])
			sig += s * s
			noise += (s - o) * (s - o)
		}
		if noise > 0 {
			if v := 10 * math.Log10(sig/noise); v > best {
				best, lag = v, d
			}
		}
	}
	return best, lag
}

// envelopeCorr correlates the 10 ms loudness of in and out (out delayed by lag).
func envelopeCorr(in, out []int16, lag int) float64 {
	var a, b []float64
	for i := 800; i+80+lag <= len(out) && i+80 <= len(in); i += 80 {
		var x, y float64
		for k := 0; k < 80; k++ {
			x += float64(in[i+k]) * float64(in[i+k])
			y += float64(out[i+k+lag]) * float64(out[i+k+lag])
		}
		a, b = append(a, math.Log10(x+1)), append(b, math.Log10(y+1))
	}
	mean := func(v []float64) (m float64) {
		for _, x := range v {
			m += x
		}
		return m / float64(len(v))
	}
	ma, mb := mean(a), mean(b)
	var sab, saa, sbb float64
	for i := range a {
		sab += (a[i] - ma) * (b[i] - mb)
		saa += (a[i] - ma) * (a[i] - ma)
		sbb += (b[i] - mb) * (b[i] - mb)
	}
	return sab / math.Sqrt(saa*sbb)
}

func benchConvert(b *testing.B, pt int) {
	there, back := NewTranscoder(PTPCMU, pt), NewTranscoder(pt, PTPCMU)
	in := speechLike(160 * 500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := i % 500
		if w := there.Convert(pcmuPacket(i, uint32(i*160), in[k*160:k*160+160])); w != nil {
			back.Convert(w)
		}
	}
}

// One iteration is 20 ms of one call both ways: 50 a second per call.
func BenchmarkOpusBothWays(b *testing.B) { benchConvert(b, PTOpus) }
func BenchmarkG729BothWays(b *testing.B) { benchConvert(b, PTG729) }

// Many G.729 streams at once (each call has its own coder; the library shares a flag between them):
// each comes out exactly as it does alone.
func TestG729Concurrent(t *testing.T) {
	_, alone := roundTripQuiet(PTG729)
	done := make(chan []int16, 8)
	for g := 0; g < 8; g++ {
		go func() {
			_, out := roundTripQuiet(PTG729)
			done <- out
		}()
	}
	for g := 0; g < 8; g++ {
		if out := <-done; !slices.Equal(out, alone) {
			t.Fatal("a stream coded alongside others came out different")
		}
	}
}

func roundTripQuiet(pt int) (in, out []int16) {
	in = speechLike(8000)
	there, back := NewTranscoder(PTPCMU, pt), NewTranscoder(pt, PTPCMU)
	for i := 0; i+160 <= len(in); i += 160 {
		if w := there.Convert(pcmuPacket(i/160, uint32(i), in[i:i+160])); w != nil {
			if n := back.Convert(w); n != nil {
				for _, b := range n[12:] {
					out = append(out, UlawToLinear(b))
				}
			}
		}
	}
	return in, out
}
