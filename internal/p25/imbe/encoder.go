package imbe

// The IMBE encoder, written for BitVoice from TIA-102.BABA: speech analysis (section 5: pitch,
// voiced/unvoiced bands, spectral amplitudes), parameter encoding (section 6: the fundamental,
// the V/UV decisions, the spectral amplitudes as prediction residuals in six DCT blocks, a gain
// vector and higher order coefficients) and bit prioritisation (section 7.1, the inverse of the
// decoder's unpacking). It makes the 88 information bits of one 20 ms frame from 160 samples of
// 8 kHz speech; the frames go on the ISSI as they are (no FEC on the wire).

import (
	"math"
	"math/cmplx"

	"github.com/biodream-llc/bitvoice/internal/p25/dsp"
	"github.com/biodream-llc/bitvoice/internal/p25/mbe"
)

const (
	minPeriod = 24.0 // the shortest pitch period coded
	encWindow = 256  // analysis window and FFT length
	encFine   = 16   // oversampling of the window's spectrum, for harmonics between FFT bins
)

// Encoder turns speech into IMBE frames. It keeps the history the encoding needs: the samples
// ahead of the frame being coded, the last pitch, and the quantised amplitudes the decoder will
// have (its prediction runs on those, so the encoder runs the same prediction).
type Encoder struct {
	hist     []float64 // the latest samples, oldest first
	dc       float64   // DC-blocker state
	dcPrev   float64
	prevP    float64 // last pitch period, samples
	prevL    int
	prevLog2 [57]float64
	window   [encWindow]float64
	wSpec    []complex128 // the window's spectrum at encFine x resolution
	wEnergy  float64
	plan     dsp.Plan
}

// NewEncoder makes an encoder.
func NewEncoder() *Encoder {
	e := &Encoder{hist: make([]float64, encWindow), plan: dsp.New(encWindow)}
	for n := range e.window {
		e.window[n] = 0.54 - 0.46*math.Cos(2*math.Pi*float64(n)/float64(encWindow-1))
	}
	fine := dsp.New(encWindow * encFine)
	in := make([]complex128, encWindow*encFine)
	for n, w := range e.window {
		in[n] = complex(w, 0)
	}
	e.wSpec = fine.Forward(nil, in)
	for _, w := range e.window {
		e.wEnergy += w * w
	}
	return e
}

// w is the window's spectrum at a frequency offset of x FFT bins (x may be fractional).
func (e *Encoder) w(x float64) complex128 {
	n := len(e.wSpec)
	i := int(math.Round(x * encFine))
	i %= n
	if i < 0 {
		i += n
	}
	return e.wSpec[i]
}

// Encode codes 160 samples (20 ms at 8 kHz) as one frame's 88 information bits, packed in 11
// octets, u0 first. The frame describes the speech a little behind the input (the analysis
// window is centred on the middle of the history).
func (e *Encoder) Encode(pcm []int16) []byte {
	// High-pass (a DC blocker), as the analysis expects speech without a DC offset.
	for _, s := range pcm {
		x := float64(s)
		y := x - e.dcPrev + 0.99*e.dc
		e.dcPrev, e.dc = x, y
		e.hist = append(e.hist, y)
	}
	e.hist = e.hist[len(e.hist)-encWindow:]

	var seg [encWindow]float64
	energy := 0.0
	for n := range seg {
		seg[n] = e.hist[n] * e.window[n]
		energy += seg[n] * seg[n]
	}
	if energy < 1e3 {
		return e.silence()
	}
	in := make([]complex128, encWindow)
	for n, v := range seg {
		in[n] = complex(v, 0)
	}
	S := e.plan.Forward(nil, in)

	P := e.pitch(seg[:])
	w0, b0 := e.refine(S, P)
	L := int(0.9254 * math.Floor(math.Pi/w0+0.25))
	K := 12
	if L < 37 {
		K = (L + 2) / 3
	}

	// Harmonic amplitudes and band voicing (BABA 5.2, 5.3).
	var M [57]float64
	voiced := make([]bool, K+1)
	binsPer := float64(encWindow) / (2 * math.Pi)
	for k := 1; k <= K; k++ {
		first, last := 3*k-2, 3*k
		if k == K {
			last = L
		}
		var sig, errE float64
		amps := map[int]float64{}
		uv := map[int]float64{}
		for l := first; l <= last; l++ {
			c := float64(l) * w0 * binsPer
			lo, hi := int(math.Ceil(c-w0*binsPer/2)), int(math.Ceil(c+w0*binsPer/2))-1
			var num complex128
			var den, se float64
			for b := lo; b <= hi && b < encWindow/2; b++ {
				if b < 0 {
					continue
				}
				wv := e.w(float64(b) - c)
				num += S[b] * cmplx.Conj(wv)
				den += real(wv)*real(wv) + imag(wv)*imag(wv)
				se += real(S[b])*real(S[b]) + imag(S[b])*imag(S[b])
			}
			if den == 0 {
				continue
			}
			A := num / complex(den, 0)
			for b := lo; b <= hi && b < encWindow/2; b++ {
				if b < 0 {
					continue
				}
				d := S[b] - A*e.w(float64(b)-c)
				errE += real(d)*real(d) + imag(d)*imag(d)
			}
			sig += se
			amps[l] = cmplx.Abs(A)
			uv[l] = math.Sqrt(se / den)
		}
		// Voiced when the harmonic model explains the band: the threshold falls with frequency.
		frac := float64(k) / float64(K)
		voiced[k] = sig > 0 && errE/sig < 0.38-0.2*frac
		for l := first; l <= last; l++ {
			if voiced[k] {
				M[l] = amps[l]
			} else {
				M[l] = uv[l]
			}
		}
	}
	return e.quantise(b0, w0, L, K, voiced, &M)
}

// silence is a frame of very low amplitudes at the last pitch (the decoder plays it as quiet).
func (e *Encoder) silence() []byte {
	P := e.prevP
	if P == 0 {
		P = 60
	}
	w0 := 2 * math.Pi / P
	b0 := int(math.Floor(4*math.Pi/w0 - 39))
	b0 = max(IdleToneMaxB0+2, min(207, b0))
	w0 = 4 * math.Pi / (float64(b0) + 39.5)
	L := int(0.9254 * math.Floor(math.Pi/w0+0.25))
	K := 12
	if L < 37 {
		K = (L + 2) / 3
	}
	var M [57]float64
	for l := 1; l <= L; l++ {
		M[l] = 1
	}
	return e.quantise(b0, w0, L, K, make([]bool, K+1), &M)
}

// pitch estimates the pitch period (samples, 21 to 122 in half samples, BABA 5.1) by normalised
// autocorrelation, preferring the shortest period that explains the signal (no octave errors) and
// a period near the last one when two are close.
func (e *Encoder) pitch(x []float64) float64 {
	r := func(p float64) float64 {
		ip := int(p)
		f := p - float64(ip)
		var num, e0, e1 float64
		for n := 0; n+ip+1 < len(x); n++ {
			y := (1-f)*x[n+ip] + f*x[n+ip+1]
			num += x[n] * y
			e0 += x[n] * x[n]
			e1 += y * y
		}
		if e0 == 0 || e1 == 0 {
			return 0
		}
		return num / math.Sqrt(e0*e1)
	}
	best, bestR := 0.0, -1.0
	score := map[float64]float64{}
	// From 24 samples (333 Hz): above it is the codebook's idle-tone corner, which decoders mute.
	for p := minPeriod; p <= 122; p += 0.5 {
		v := r(p)
		score[p] = v
		if e.prevP > 0 && math.Abs(p-e.prevP) <= 0.15*e.prevP {
			v += 0.05 // look-back tracking: a small preference for continuing the last pitch
		}
		if v > bestR {
			best, bestR = p, v
		}
	}
	for _, div := range []float64{3, 2} {
		q := math.Round(best/div*2) / 2
		if q >= minPeriod && score[q] > 0.85*score[best] {
			best = q
			break
		}
	}
	e.prevP = best
	return best
}

// refine finds the fundamental (BABA 5.1.5) near 2 pi / P that best fits the spectrum with
// harmonics, and returns it quantised: the decoder's ω0 and b0.
func (e *Encoder) refine(S []complex128, P float64) (float64, int) {
	binsPer := float64(encWindow) / (2 * math.Pi)
	bestB, bestErr := 0, math.Inf(1)
	lo := int(math.Floor(4*math.Pi/(2*math.Pi/(P-0.75)) - 39))
	hi := int(math.Floor(4*math.Pi/(2*math.Pi/(P+0.75)) - 39))
	for b0 := max(IdleToneMaxB0+2, lo); b0 <= min(207, hi); b0++ {
		w0 := 4 * math.Pi / (float64(b0) + 39.5)
		var errE float64
		for l := 1; float64(l)*w0 < math.Pi*0.75; l++ {
			c := float64(l) * w0 * binsPer
			a, b := int(math.Ceil(c-w0*binsPer/2)), int(math.Ceil(c+w0*binsPer/2))-1
			var num complex128
			var den float64
			for k := max(a, 0); k <= b && k < encWindow/2; k++ {
				wv := e.w(float64(k) - c)
				num += S[k] * cmplx.Conj(wv)
				den += real(wv)*real(wv) + imag(wv)*imag(wv)
			}
			if den == 0 {
				continue
			}
			A := num / complex(den, 0)
			for k := max(a, 0); k <= b && k < encWindow/2; k++ {
				d := S[k] - A*e.w(float64(k)-c)
				errE += real(d)*real(d) + imag(d)*imag(d)
			}
		}
		if errE < bestErr {
			bestB, bestErr = b0, errE
		}
	}
	if bestErr == math.Inf(1) {
		bestB = max(IdleToneMaxB0+2, min(207, int(math.Floor(2*P-39))))
	}
	return 4 * math.Pi / (float64(bestB) + 39.5), bestB
}

// quantise encodes the parameters (BABA 6) and places the bits (7.1).
func (e *Encoder) quantise(b0 int, w0 float64, L, K int, voiced []bool, M *[57]float64) []byte {
	L9 := L - 9
	var bb [58][12]byte
	put := func(v, value, bits int) {
		for i := 0; i < bits; i++ {
			bb[v][i] = byte(value >> i & 1)
		}
	}
	// b1: the V/UV decisions, band 1 most significant.
	b1 := 0
	for k := 1; k <= K; k++ {
		if voiced[k] {
			b1 |= 1 << (K - k)
		}
	}
	put(1, b1, K)

	// Prediction residuals against the decoder's view of the previous frame (equations 52-57).
	// Empty harmonics are floored 48 dB under the strongest: a pure tone (an alert tone) otherwise
	// spans more octaves than the quantisers follow, and its level collapses after the first frame.
	peak := 1e-3
	for l := 1; l <= L; l++ {
		peak = math.Max(peak, M[l])
	}
	var log2M [57]float64
	for l := 1; l <= L; l++ {
		log2M[l] = math.Log2(math.Max(M[l], peak/256))
	}
	pred := mbe.PredictionTerms(e.prevL, &e.prevLog2, L)
	ave := 0.0
	for l := 1; l <= L; l++ {
		ave += pred[l]
	}
	ave /= float64(L)
	var T [57]float64
	for l := 1; l <= L; l++ {
		T[l] = log2M[l] - pred[l] + ave
	}
	// Six blocks, each transformed with a DCT (equation 60); the first coefficients form the gain
	// vector, transformed again.
	var C [7][12]float64
	l := 1
	for i := 1; i <= 6; i++ {
		J := imbeJi[L9][i-1]
		for k := 1; k <= J; k++ {
			s := 0.0
			for j := 1; j <= J; j++ {
				s += T[l+j-1] * math.Cos(math.Pi*float64(k-1)*(float64(j)-0.5)/float64(J))
			}
			C[i][k] = s / float64(J)
		}
		l += J
	}
	var G [7]float64
	for m := 1; m <= 6; m++ {
		s := 0.0
		for i := 1; i <= 6; i++ {
			s += C[i][1] * math.Cos(math.Pi*float64(m-1)*(float64(i)-0.5)/6)
		}
		G[m] = s / 6
	}
	// b2: G1 against its table; b3-b7: G2-G6 with uniform quantisers.
	b2 := 0
	for i := range b2Table {
		if math.Abs(b2Table[i]-G[1]) < math.Abs(b2Table[b2]-G[1]) {
			b2 = i
		}
	}
	put(2, b2, 6)
	uniform := func(x, step float64, bits int) int {
		q := int(math.Floor(x/step + float64(int(1)<<(bits-1))))
		return max(0, min(1<<bits-1, q))
	}
	for i := 2; i <= 6; i++ {
		bits, step := int(ba[L9][i-2][0]), ba[L9][i-2][1]
		put(i+1, uniform(G[i], step, bits), bits)
	}
	// b8 on: the higher order DCT coefficients.
	m := 8
	for i := 1; i <= 6; i++ {
		J := imbeJi[L9][i-1]
		for k := 2; k <= J; k++ {
			if bits := hoba[L9][m-8]; bits > 0 {
				put(m, uniform(C[i][k], quantstep[bits-1]*standdev[k-2], bits), bits)
			}
			m++
		}
	}

	// Bit prioritisation: b0's six high bits first, its two low bits near the end, the rest by
	// the table for this L.
	info := make([]byte, InfoBits)
	for i := 0; i < 6; i++ {
		info[i] = byte(b0 >> (7 - i) & 1)
	}
	info[85], info[86] = byte(b0>>1&1), byte(b0&1)
	for i := 0; i < 79; i++ {
		info[6+i] = bb[bo[L9][i][0]][bo[L9][i][1]]
	}
	frame := make([]byte, FrameBytes)
	for i, b := range info {
		frame[i/8] |= b << (7 - i%8)
	}

	// What the decoder will reconstruct becomes the next frame's prediction base.
	if p, err := UnpackParams(info); err == nil {
		var s mbe.SynthState
		s.PrevL, s.PrevLog2Ml = e.prevL, e.prevLog2
		s.PrevW0 = w0
		var got [57]float64
		mbe.PredictLog2Ml(&s, p.MBE(), &got)
		e.prevL, e.prevLog2 = L, got
	}
	return frame
}
