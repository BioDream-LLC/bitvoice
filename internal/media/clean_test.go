package media

import (
	"math"
	"math/cmplx"
	"math/rand/v2"
	"testing"
)

func rmsOf(s []int16) float64 {
	var sum float64
	for _, v := range s {
		f := float64(v) / 32768
		sum += f * f
	}
	return math.Sqrt(sum / float64(max(1, len(s))))
}

func db(r float64) float64 { return 20 * math.Log10(r+1e-12) }

// bursts is half-second 1 kHz tone bursts, half a second apart, over steady noise.
func bursts(rate int, seconds int, tone, noise float64) []int16 {
	rng := rand.New(rand.NewPCG(1, 2))
	out := make([]int16, rate*seconds)
	for i := range out {
		v := noise * rng.NormFloat64()
		if (i/(rate/2))%2 == 1 {
			v += tone * math.Sin(2*math.Pi*1000*float64(i)/float64(rate))
		}
		out[i] = int16(clampf(v, -1, 1) * 32767)
	}
	return out
}

func TestFFTRoundTrip(t *testing.T) {
	x := make([]complex128, 256)
	for i := range x {
		x[i] = complex(math.Sin(float64(i)), 0)
	}
	y := append([]complex128(nil), x...)
	fft(y, false)
	fft(y, true)
	for i := range x {
		if cmplx.Abs(x[i]-y[i]) > 1e-9 {
			t.Fatalf("sample %d: %v vs %v", i, x[i], y[i])
		}
	}
}

func TestNoiseSuppression(t *testing.T) {
	for _, rate := range []int{8000, 16000} {
		in := bursts(rate, 6, 0.1, 0.01)
		c := NewCleaner(rate)
		c.Level = false
		out := c.Process(in)
		half := rate / 2
		// Compare the last gap and the last burst (after the noise estimate settled), allowing for latency.
		gapIn, gapOut := rmsOf(in[10*half+half/4:11*half-half/4]), rmsOf(out[10*half+half/4:11*half-half/4])
		toneIn, toneOut := rmsOf(in[11*half+half/4:12*half-half/4]), rmsOf(out[11*half+half/4:12*half-half/4])
		t.Logf("%d Hz: noise %.1f -> %.1f dB, speech %.1f -> %.1f dB", rate, db(gapIn), db(gapOut), db(toneIn), db(toneOut))
		if db(gapIn)-db(gapOut) < 10 {
			t.Errorf("%d Hz: noise reduced only %.1f dB", rate, db(gapIn)-db(gapOut))
		}
		if math.Abs(db(toneIn)-db(toneOut)) > 2 {
			t.Errorf("%d Hz: speech level changed %.1f dB", rate, db(toneOut)-db(toneIn))
		}
	}
}

func TestLevelControl(t *testing.T) {
	quiet := bursts(8000, 8, 0.02, 0.0005) // a quiet talker, -37 dBFS
	out := Clean(quiet, 8000)
	if len(out) != len(quiet) {
		t.Fatalf("length %d, want %d", len(out), len(quiet))
	}
	last := out[15*4000+1000 : 16*4000-1000]
	if l := db(rmsOf(last)); l < -26 || l > -14 {
		t.Fatalf("quiet talker brought to %.1f dBFS, want about -20", l)
	}
	loud := bursts(8000, 4, 0.9, 0.001)
	for i, v := range Clean(loud, 8000) {
		if v == math.MaxInt16 || v == math.MinInt16 {
			t.Fatalf("clipped at %d", i)
		}
	}
}

// Clean keeps the timing: a click stays where it was.
func TestCleanAlignment(t *testing.T) {
	in := make([]int16, 8000)
	for i := 4000; i < 4080; i++ {
		in[i] = int16(16000 * math.Sin(2*math.Pi*1000*float64(i)/8000))
	}
	out := Clean(in, 8000)
	peak, at := 0.0, 0
	for i, v := range out {
		if a := math.Abs(float64(v)); a > peak {
			peak, at = a, i
		}
	}
	if at < 3990 || at > 4090 {
		t.Fatalf("peak moved to %d", at)
	}
}

// Steady hiss with nobody talking is turned down, never up by the level control.
func TestHissIsNotBoosted(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	in := make([]int16, 3*8000)
	for i := range in {
		in[i] = int16(300 * rng.NormFloat64())
	}
	out := NewCleaner(8000).Process(in)
	if a, b := db(rmsOf(in[16000:])), db(rmsOf(out[16000:])); b > a-6 {
		t.Fatalf("hiss %.1f dB in, %.1f dB out", a, b)
	}
}
