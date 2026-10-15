package media

import (
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func needFFmpeg(t *testing.T) string {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if out, _ := exec.Command(ff, "-hide_banner", "-codecs").Output(); !contains(string(out), "g723_1") {
		t.Skip("this ffmpeg has no G.723.1")
	}
	return ff
}

func contains(s, w string) bool {
	for i := 0; i+len(w) <= len(s); i++ {
		if s[i:i+len(w)] == w {
			return true
		}
	}
	return false
}

func g723rtp(pt int, seq uint16, ts uint32, payload []byte) []byte {
	b := make([]byte, 12, 12+len(payload))
	b[0], b[1] = 0x80, byte(pt)
	binary.BigEndian.PutUint16(b[2:], seq)
	binary.BigEndian.PutUint32(b[4:], ts)
	return append(b, payload...)
}

func g723tone(n int, f float64, at int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(8000 * math.Sin(2*math.Pi*f*float64(at+i)/8000))
	}
	return out
}

// toneLevel is the strength of frequency f in the audio (Goertzel, normalized).
func toneLevel(pcm []int16, f float64) float64 {
	c := 2 * math.Cos(2*math.Pi*f/8000)
	var s1, s2, e float64
	for _, v := range pcm {
		x := float64(v)
		s := x + c*s1 - s2
		s2, s1 = s1, s
		e += x * x
	}
	if e == 0 {
		return 0
	}
	return (s1*s1 + s2*s2 - c*s1*s2) / (e * float64(len(pcm)) / 2)
}

// A 1 kHz tone through G.723.1 both ways with ffmpeg: PCMU in, G.723.1 frames out that ffmpeg itself
// decodes back to the tone; and G.723.1 frames ffmpeg made, converted to PCMU, carry the tone. The
// converter starts and keeps up without ever blocking the stream.
func TestG723ThroughFFmpeg(t *testing.T) {
	ff := needFFmpeg(t)
	SetG723Converter(ff)
	defer SetG723Converter("")
	if !Transcodable(PTG723) || CodecPT("G723") != PTG723 {
		t.Fatal("G.723.1 not convertible with the converter on")
	}

	// PCMU to G.723.1.
	enc := NewTranscoder(PTPCMU, PTG723)
	var frames []byte
	for i := 0; i < 100; i++ { // 2 s of 20 ms packets
		pcm := g723tone(160, 1000, i*160)
		payload := make([]byte, 160)
		for k, v := range pcm {
			payload[k] = LinearToUlaw(v)
		}
		start := time.Now()
		out := enc.Convert(g723rtp(PTPCMU, uint16(i), uint32(i*160), payload))
		if d := time.Since(start); d > 20*time.Millisecond {
			t.Fatalf("Convert took %v: it must not wait for ffmpeg", d)
		}
		if out != nil {
			if out[1]&0x7F != PTG723 {
				t.Fatalf("payload type %d", out[1]&0x7F)
			}
			frames = append(frames, out[12:]...)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(frames) < 24*40 {
		t.Fatalf("only %d bytes of G.723.1 in 2 s", len(frames))
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "a.723")
	os.WriteFile(in, frames, 0o600)
	raw, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-f", "g723_1", "-i", in, "-f", "s16le", "-ar", "8000", "-ac", "1", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	pcm := splitPCM(raw)
	if len(pcm) < 8000 {
		t.Fatalf("ffmpeg decoded %d samples", len(pcm))
	}
	mid := pcm[len(pcm)/4 : 3*len(pcm)/4]
	if l := toneLevel(mid, 1000); l < 0.5 {
		t.Fatalf("the tone did not survive: level %.2f", l)
	}

	// G.723.1 (made by ffmpeg) to PCMU.
	src := filepath.Join(dir, "b.723")
	if err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=700:sample_rate=8000:duration=2",
		"-c:a", "g723_1", "-b:a", "6300", "-f", "g723_1", "-y", src).Run(); err != nil {
		t.Fatal(err)
	}
	g, _ := os.ReadFile(src)
	dec := NewTranscoder(PTG723, PTPCMU)
	var heard []int16
	for i, k := 0, 0; i+24 <= len(g); i, k = i+24, k+1 {
		out := dec.Convert(g723rtp(PTG723, uint16(k), uint32(k*240), g[i:i+24]))
		for _, b := range out[min(12, len(out)):] {
			heard = append(heard, UlawToLinear(b))
		}
		time.Sleep(30 * time.Millisecond)
	}
	if len(heard) < 12000 {
		t.Fatalf("heard %d samples", len(heard))
	}
	nz := 0
	for _, s := range heard {
		if s > 100 || s < -100 {
			nz++
		}
	}
	t.Logf("heard %d samples, %d loud, level at 700 Hz %.2f", len(heard), nz, toneLevel(heard[len(heard)/3:], 700))
	if l := toneLevel(heard[len(heard)/3:], 700); l < 0.5 {
		t.Fatalf("the tone did not come through: level %.2f", l)
	}
}

func TestG723Off(t *testing.T) {
	SetG723Converter("")
	if Transcodable(PTG723) {
		t.Fatal("G.723.1 convertible with the converter off")
	}
	if G723Frame(0) != 24 || G723Frame(1) != 20 || G723Frame(2) != 4 || G723Frame(3) != 1 {
		t.Fatal("frame sizes")
	}
}
