package tty

import (
	"encoding/binary"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func decode(pcm []int16) string {
	d := NewDecoder()
	var b strings.Builder
	for i := 0; i < len(pcm); i += 160 {
		b.WriteString(d.Feed(pcm[i:min(i+160, len(pcm))]))
	}
	return b.String()
}

func TestRoundTrip(t *testing.T) {
	for _, s := range []string{"HELLO 911 WHAT IS YOUR EMERGENCY?", "MY ADDRESS IS 12A MAIN ST, APT 3. GA", "HELP $5 + (NOW)! SK 1 2 X"} {
		if got := decode(Encode(s)); got != s {
			t.Errorf("sent %q, decoded %q", s, got)
		}
	}
	if got := Sendable("call 911 — now ✓"); got != "CALL 911  NOW " {
		t.Errorf("Sendable = %q", got)
	}
}

// Speech-like noise and other tones never become text.
func TestNoTextFromNoise(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	pcm := make([]int16, 30*rate)
	for i := range pcm {
		f := 300 + 700*math.Sin(float64(i)/4000)
		pcm[i] = int16(4000*math.Sin(2*math.Pi*f*float64(i)/rate) + 1500*rng.NormFloat64())
	}
	for i := 10 * rate; i < 12*rate; i++ { // a DTMF-like pair and a 1400 Hz tone alone
		pcm[i] = int16(3000*math.Sin(2*math.Pi*697*float64(i)/rate) + 3000*math.Sin(2*math.Pi*1209*float64(i)/rate))
	}
	for i := 15 * rate; i < 17*rate; i++ {
		pcm[i] = int16(5000 * math.Sin(2*math.Pi*1400*float64(i)/rate))
	}
	d := NewDecoder()
	if got := d.Feed(pcm); got != "" || d.Heard() {
		t.Fatalf("decoded %q from noise", got)
	}
}

// Text over noise and a level change still decodes.
func TestNoisyLine(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	pcm := Encode("GA HELLO")
	for i := range pcm {
		pcm[i] = int16(float64(pcm[i])*0.3 + 150*rng.NormFloat64())
	}
	if got := decode(pcm); got != "GA HELLO" {
		t.Fatalf("decoded %q", got)
	}
}

func oracle(t *testing.T) string {
	o := os.Getenv("BITVOICE_TEST_SPANDSP_TTY")
	if o == "" {
		t.Skip("set BITVOICE_TEST_SPANDSP_TTY to spandsp's TTY harness")
	}
	return o
}

func pcmBytes(pcm []int16) []byte {
	b := make([]byte, 2*len(pcm))
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
	}
	return b
}

// Against spandsp's V.18 Weitbrecht mode: what it sends we read, what we send it reads.
func TestAgainstSpanDSP(t *testing.T) {
	o := oracle(t)
	all := "THE QUICK BROWN FOX 0123456789 -$'!:(\")=?+./;, EMERGENCY 5 A"
	out, err := exec.Command(o, "tx", all).Output()
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, len(out)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(out[2*i:]))
	}
	if got := decode(pcm); got != all {
		t.Errorf("spandsp sent %q, we read %q", all, got)
	}
	cmd := exec.Command(o, "rx")
	cmd.Stdin = strings.NewReader(string(pcmBytes(Encode(all))))
	heard, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(heard)) != all {
		t.Errorf("we sent %q, spandsp read %q", all, heard)
	}
}
