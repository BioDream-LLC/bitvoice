package media

import "math/bits"

// GSM 06.10 full rate (13 kbit/s, RTP payload type 3, 33 octets per 20 ms): RPE-LTP. The fixed-point
// arithmetic follows ETSI GSM 06.10 step by step (section numbers in the comments), so streams decode
// the same as every other conforming implementation.

const (
	gsmMinWord = -32768
	gsmMaxWord = 32767
)

var (
	gsmNRFAC = [8]int32{29128, 26215, 23832, 21846, 20165, 18725, 17476, 16384}
	gsmFAC   = [8]int32{18431, 20479, 22527, 24575, 26623, 28671, 30719, 32767}
	gsmQLB   = [4]int32{3277, 11469, 21299, 32767}
	gsmDLB   = [4]int32{6554, 16384, 26214, 32767}
	// Per log area ratio: A, B, MIC, MAC and INVA (tables 4.1 and 4.2).
	gsmLARA    = [8]int32{20480, 20480, 20480, 20480, 13964, 15360, 8534, 9036}
	gsmLARB    = [8]int32{0, 0, 2048, -2560, 94, -1792, -341, -1144}
	gsmLARMIC  = [8]int32{-32, -32, -16, -16, -8, -8, -4, -4}
	gsmLARMAC  = [8]int32{31, 31, 15, 15, 7, 7, 3, 3}
	gsmLARINVA = [8]int32{13107, 13107, 13107, 13107, 19223, 17476, 31454, 29708}
	gsmLARBits = [8]int{6, 6, 5, 5, 4, 4, 3, 3}
)

func gsmSat(v int32) int32    { return min(max(v, gsmMinWord), gsmMaxWord) }
func gsmAdd(a, b int32) int32 { return gsmSat(a + b) }
func gsmSub(a, b int32) int32 { return gsmSat(a - b) }
func gsmAbs(a int32) int32 {
	if a < 0 {
		if a == gsmMinWord {
			return gsmMaxWord
		}
		return -a
	}
	return a
}

// gsmMultR is the rounded product of two words (Q15).
func gsmMultR(a, b int32) int32 {
	if a == gsmMinWord && b == gsmMinWord {
		return gsmMaxWord
	}
	return int32(int16((a*b + 16384) >> 15))
}

// gsmMult is the truncated product.
func gsmMult(a, b int32) int32 {
	if a == gsmMinWord && b == gsmMinWord {
		return gsmMaxWord
	}
	return (a * b) >> 15
}

func gsmLAdd(a, b int64) int64 { return min(max(a+b, -2147483648), 2147483647) }

// gsmNorm is the left shift that normalizes a 32-bit value.
func gsmNorm(a int32) int32 {
	if a < 0 {
		if a <= -1073741824 {
			return 0
		}
		a = ^a
	}
	return int32(bits.LeadingZeros32(uint32(a))) - 1
}

// gsmDiv is num/denum in Q15, 0 <= num <= denum.
func gsmDiv(num, denum int32) int32 {
	if num == 0 {
		return 0
	}
	l, d := num, denum
	var div int32
	for range 15 {
		div <<= 1
		l <<= 1
		if l >= d {
			l -= d
			div++
		}
	}
	return div
}

func gsmAsr(a, n int32) int32 {
	switch {
	case n >= 16:
		if a < 0 {
			return -1
		}
		return 0
	case n <= -16:
		return 0
	case n < 0:
		return int32(int16(a << -n))
	}
	return a >> n
}

func gsmAsl(a, n int32) int32 {
	switch {
	case n >= 16:
		return 0
	case n <= -16:
		if a < 0 {
			return -1
		}
		return 0
	case n < 0:
		return gsmAsr(a, -n)
	}
	return int32(int16(a << n))
}

// gsmState is one direction of one stream.
type gsmState struct {
	dp0   [280]int32
	z1    int32
	lz2   int64
	mp    int32
	u     [8]int32
	larpp [2][8]int32
	j     int
	nrp   int32
	v     [9]int32
	msr   int32
}

func newGSMState() *gsmState { return &gsmState{nrp: 40} }

// gsmFrame is one frame's parameters.
type gsmFrame struct {
	larc  [8]int32
	nc    [4]int32
	bc    [4]int32
	mc    [4]int32
	xmaxc [4]int32
	xmc   [52]int32
}

// --- 4.2.0 to 4.2.3: preprocessing ---

func (s *gsmState) preprocess(in []int16, so []int32) {
	z1, lz2, mp := s.z1, s.lz2, s.mp
	for k := range 160 {
		SO := (int32(in[k]) >> 3) << 2
		s1 := SO - z1
		z1 = SO
		ls2 := int64(s1) << 15
		msp := int32(int16(lz2 >> 15))
		lsp := int32(lz2 - int64(msp)<<15)
		ls2 += int64(gsmMultR(lsp, 32735))
		lz2 = gsmLAdd(int64(msp)*32735, ls2)
		ltemp := gsmLAdd(lz2, 16384)
		msp = gsmMultR(mp, -28180)
		mp = int32(int16(ltemp >> 15))
		so[k] = gsmAdd(mp, msp)
	}
	s.z1, s.lz2, s.mp = z1, lz2, mp
}

// --- 4.2.4 to 4.2.7: LPC analysis ---

func gsmAutocorrelation(s []int32, acf *[9]int64) {
	var smax int32
	for k := range 160 {
		smax = max(smax, gsmAbs(s[k]))
	}
	var scal int32
	if smax != 0 {
		scal = 4 - gsmNorm(smax<<16)
	}
	if scal > 0 {
		f := int32(16384) >> (scal - 1)
		for k := range 160 {
			s[k] = gsmMultR(s[k], f)
		}
	}
	for k := range 9 {
		var sum int64
		for i := k; i < 160; i++ {
			sum += int64(s[i]) * int64(s[i-k])
		}
		acf[k] = sum << 1
	}
	if scal > 0 {
		for k := range 160 {
			s[k] = int32(int16(s[k] << scal))
		}
	}
}

func gsmReflection(lacf *[9]int64, r *[8]int32) {
	if lacf[0] == 0 {
		*r = [8]int32{}
		return
	}
	temp := gsmNorm(int32(lacf[0]))
	var acf, p, kk [9]int32
	for i := range 9 {
		acf[i] = int32((int64(int32(lacf[i]<<temp)) >> 16))
	}
	for i := 1; i <= 7; i++ {
		kk[i] = acf[i]
	}
	p = acf
	for n := 1; n <= 8; n++ {
		t := gsmAbs(p[1])
		if p[0] < t {
			for i := n; i <= 8; i++ {
				r[i-1] = 0
			}
			return
		}
		rn := gsmDiv(t, p[0])
		if p[1] > 0 {
			rn = -rn
		}
		r[n-1] = rn
		if n == 8 {
			return
		}
		p[0] = gsmAdd(p[0], gsmMultR(p[1], rn))
		for m := 1; m <= 8-n; m++ {
			p[m] = gsmAdd(p[m+1], gsmMultR(kk[m], rn))
			kk[m] = gsmAdd(kk[m], gsmMultR(p[m+1], rn))
		}
	}
}

func gsmToLAR(r *[8]int32) {
	for i := range 8 {
		t := gsmAbs(r[i])
		switch {
		case t < 22118:
			t >>= 1
		case t < 31130:
			t -= 11059
		default:
			t = (t - 26112) << 2
		}
		if r[i] < 0 {
			r[i] = -t
		} else {
			r[i] = t
		}
	}
}

func gsmQuantizeLAR(lar *[8]int32) {
	for i := range 8 {
		t := gsmMult(gsmLARA[i], lar[i])
		t = gsmAdd(t, gsmLARB[i])
		t = gsmAdd(t, 256)
		t >>= 9
		switch {
		case t > gsmLARMAC[i]:
			lar[i] = gsmLARMAC[i] - gsmLARMIC[i]
		case t < gsmLARMIC[i]:
			lar[i] = 0
		default:
			lar[i] = t - gsmLARMIC[i]
		}
	}
}

// --- 4.2.8 to 4.2.10 and 4.3.2: short term analysis and synthesis ---

func gsmDecodeLAR(larc *[8]int32, larpp *[8]int32) {
	for i := range 8 {
		t := int32(int16(gsmAdd(larc[i], gsmLARMIC[i]) << 10))
		t = gsmSub(t, gsmLARB[i]<<1)
		t = gsmMultR(gsmLARINVA[i], t)
		larpp[i] = gsmAdd(t, t)
	}
}

// gsmInterpolate fills larp for one of the four parts of a frame (0: samples 0-12, ... 3: 40-159).
func gsmInterpolate(part int, prev, cur *[8]int32, larp *[8]int32) {
	for i := range 8 {
		switch part {
		case 0:
			larp[i] = gsmAdd(gsmAdd(prev[i]>>2, cur[i]>>2), prev[i]>>1)
		case 1:
			larp[i] = gsmAdd(prev[i]>>1, cur[i]>>1)
		case 2:
			larp[i] = gsmAdd(gsmAdd(prev[i]>>2, cur[i]>>2), cur[i]>>1)
		default:
			larp[i] = cur[i]
		}
	}
}

func gsmLARToRP(larp *[8]int32) {
	for i := range 8 {
		neg := larp[i] < 0
		t := gsmAbs(larp[i])
		switch {
		case t < 11059:
			t <<= 1
		case t < 20070:
			t += 11059
		default:
			t = gsmAdd(t>>2, 26112)
		}
		if neg {
			t = -t
		}
		larp[i] = t
	}
}

var gsmParts = [4][2]int{{0, 13}, {13, 14}, {27, 13}, {40, 120}}

func (s *gsmState) shortTermAnalysis(larc *[8]int32, sig []int32) {
	cur := &s.larpp[s.j]
	s.j ^= 1
	prev := &s.larpp[s.j]
	gsmDecodeLAR(larc, cur)
	var larp [8]int32
	for part, se := range gsmParts {
		gsmInterpolate(part, prev, cur, &larp)
		gsmLARToRP(&larp)
		for k := se[0]; k < se[0]+se[1]; k++ {
			di := sig[k]
			sav := di
			for i := range 8 {
				ui := s.u[i]
				rpi := larp[i]
				s.u[i] = sav
				sav = gsmAdd(ui, gsmMultR(rpi, di))
				di = gsmAdd(di, gsmMultR(rpi, ui))
			}
			sig[k] = di
		}
	}
}

func (s *gsmState) shortTermSynthesis(larc *[8]int32, wt []int32, out []int32) {
	cur := &s.larpp[s.j]
	s.j ^= 1
	prev := &s.larpp[s.j]
	gsmDecodeLAR(larc, cur)
	var larp [8]int32
	for part, se := range gsmParts {
		gsmInterpolate(part, prev, cur, &larp)
		gsmLARToRP(&larp)
		for k := se[0]; k < se[0]+se[1]; k++ {
			sri := wt[k]
			for i := 7; i >= 0; i-- {
				sri = gsmSub(sri, gsmMultR(larp[i], s.v[i]))
				s.v[i+1] = gsmAdd(s.v[i], gsmMultR(larp[i], sri))
			}
			s.v[0] = sri
			out[k] = sri
		}
	}
}

// --- 4.2.11 and 4.2.12: long term prediction ---

// gsmLTPParams finds the lag and gain for one subframe: d is d[0..39], dp is dp[-120..-1] at dp[0..119].
func gsmLTPParams(d []int32, dp []int32) (bc, nc int32) {
	var dmax int32
	for k := range 40 {
		dmax = max(dmax, gsmAbs(d[k]))
	}
	var temp int32
	if dmax != 0 {
		temp = gsmNorm(dmax << 16)
	}
	var scal int32
	if temp <= 6 {
		scal = 6 - temp
	}
	var wt [40]int32
	for k := range 40 {
		wt[k] = d[k] >> scal
	}
	var lmax int64
	nc = 40
	for lambda := 40; lambda <= 120; lambda++ {
		var sum int64
		for k := range 40 {
			sum += int64(wt[k]) * int64(dp[120+k-lambda])
		}
		if sum > lmax {
			nc, lmax = int32(lambda), sum
		}
	}
	lmax <<= 1
	lmax >>= 6 - scal
	var lpower int64
	for k := range 40 {
		t := int64(dp[120+k-int(nc)] >> 3)
		lpower += t * t
	}
	lpower <<= 1
	if lmax <= 0 {
		return 0, nc
	}
	if lmax >= lpower {
		return 3, nc
	}
	temp = gsmNorm(int32(lpower))
	R := int32(int64(int32(lmax<<temp)) >> 16)
	S := int32(int64(int32(lpower<<temp)) >> 16)
	for bc = 0; bc <= 2; bc++ {
		if R <= gsmMult(S, gsmDLB[bc]) {
			break
		}
	}
	return bc, nc
}

// --- 4.2.13 to 4.2.17: RPE encoding ---

var gsmH = [11]int64{-134, -374, 0, 2054, 5741, 8192, 5741, 2054, 0, -374, -134}

// gsmWeighting filters e (with five zero samples either side, e[0..49]) into x[0..39].
func gsmWeighting(e *[50]int32, x *[40]int32) {
	for k := range 40 {
		l := int64(4096)
		for i := range 11 {
			l += int64(e[k+i]) * gsmH[i]
		}
		x[k] = int32(min(max(l>>13, gsmMinWord), gsmMaxWord))
	}
}

func gsmGrid(x *[40]int32) (xm [13]int32, mc int32) {
	var em int64 = -1
	for m := range 4 {
		var sum int64
		for i := range 13 {
			t := int64(x[m+3*i] >> 2)
			sum += t * t
		}
		sum <<= 1
		if sum > em {
			mc, em = int32(m), sum
		}
	}
	for i := range 13 {
		xm[i] = x[int(mc)+3*i]
	}
	return xm, mc
}

func gsmExpMant(xmaxc int32) (exp, mant int32) {
	if xmaxc > 15 {
		exp = (xmaxc >> 3) - 1
	}
	mant = xmaxc - exp<<3
	if mant == 0 {
		return -4, 7
	}
	for mant <= 7 {
		mant = mant<<1 | 1
		exp--
	}
	return exp, mant - 8
}

func gsmAPCM(xm *[13]int32, xmc []int32) (xmaxc, exp, mant int32) {
	var xmax int32
	for i := range 13 {
		xmax = max(xmax, gsmAbs(xm[i]))
	}
	temp := xmax >> 9
	itest := false
	for range 6 {
		itest = itest || temp <= 0
		temp >>= 1
		if !itest {
			exp++
		}
	}
	xmaxc = gsmAdd(xmax>>(exp+5), exp<<3)
	exp, mant = gsmExpMant(xmaxc)
	t1 := 6 - exp
	t2 := gsmNRFAC[mant]
	for i := range 13 {
		t := int32(int16(xm[i] << t1))
		t = gsmMult(t, t2)
		xmc[i] = (t >> 12) + 4
	}
	return xmaxc, exp, mant
}

func gsmInverseAPCM(xmc []int32, mant, exp int32, xmp *[13]int32) {
	t1 := gsmFAC[mant]
	t2 := gsmSub(6, exp)
	t3 := gsmAsl(1, gsmSub(t2, 1))
	for i := range 13 {
		t := (xmc[i]<<1 - 7) << 12
		t = gsmMultR(t1, t)
		t = gsmAdd(t, t3)
		xmp[i] = gsmAsr(t, t2)
	}
}

func gsmPosition(mc int32, xmp *[13]int32, ep []int32) {
	for k := range 40 {
		ep[k] = 0
	}
	for i := range 13 {
		ep[int(mc)+3*i] = xmp[i]
	}
}

// --- the coder and the decoder (4.2 and 4.3) ---

func (s *gsmState) encodeFrame(in []int16) gsmFrame {
	var f gsmFrame
	var so [160]int32
	s.preprocess(in, so[:])
	var lacf [9]int64
	// The reference scales the frame in place and back, which loses the low bits it scaled away: so do
	// we, or the short term filter would see other samples than every other implementation's.
	gsmAutocorrelation(so[:], &lacf)
	gsmReflection(&lacf, &f.larc)
	gsmToLAR(&f.larc)
	gsmQuantizeLAR(&f.larc)
	s.shortTermAnalysis(&f.larc, so[:])
	for k := range 4 {
		d := so[k*40 : k*40+40]
		dp := s.dp0[k*40 : k*40+120] // dp[-120..-1]
		bc, nc := gsmLTPParams(d, dp)
		f.bc[k], f.nc[k] = bc, nc
		var e [50]int32
		var dpp [40]int32
		for i := range 40 {
			dpp[i] = gsmMultR(gsmQLB[bc], dp[120+i-int(nc)])
			e[5+i] = gsmSub(d[i], dpp[i])
		}
		var x [40]int32
		gsmWeighting(&e, &x)
		xm, mc := gsmGrid(&x)
		f.mc[k] = mc
		xmaxc, exp, mant := gsmAPCM(&xm, f.xmc[k*13:k*13+13])
		f.xmaxc[k] = xmaxc
		var xmp [13]int32
		gsmInverseAPCM(f.xmc[k*13:k*13+13], mant, exp, &xmp)
		gsmPosition(mc, &xmp, e[5:45])
		for i := range 40 {
			s.dp0[120+k*40+i] = gsmAdd(e[5+i], dpp[i])
		}
	}
	copy(s.dp0[:120], s.dp0[160:280])
	return f
}

func (s *gsmState) decodeFrame(f *gsmFrame, out []int16) {
	var wt [160]int32
	drp := s.dp0[:160] // drp[-120..39] at drp[0..159]
	for j := range 4 {
		exp, mant := gsmExpMant(f.xmaxc[j])
		var xmp [13]int32
		gsmInverseAPCM(f.xmc[j*13:j*13+13], mant, exp, &xmp)
		var erp [40]int32
		gsmPosition(f.mc[j], &xmp, erp[:])
		nr := f.nc[j]
		if nr < 40 || nr > 120 {
			nr = s.nrp
		}
		s.nrp = nr
		brp := gsmQLB[f.bc[j]]
		for k := range 40 {
			drp[120+k] = gsmAdd(erp[k], gsmMultR(brp, drp[120+k-int(nr)]))
		}
		copy(drp[0:120], drp[40:160])
		for k := range 40 {
			wt[j*40+k] = drp[80+k] // the samples just made, now at drp[-40..-1]
		}
	}
	var sr [160]int32
	s.shortTermSynthesis(&f.larc, wt[:], sr[:])
	msr := s.msr
	for k := range 160 {
		msr = gsmAdd(sr[k], gsmMultR(msr, 28180))
		out[k] = int16(gsmAdd(msr, msr) & -8)
	}
	s.msr = msr
}

// --- the 33-octet frame (the signature 0xD, then every parameter, most significant bit first) ---

type gsmBits struct {
	b   []byte
	acc uint64
	n   int
}

func (w *gsmBits) put(v int32, bits int) {
	w.acc = w.acc<<bits | uint64(uint32(v)&(1<<bits-1))
	w.n += bits
	for w.n >= 8 {
		w.b = append(w.b, byte(w.acc>>(w.n-8)))
		w.n -= 8
	}
}

func (f *gsmFrame) pack() []byte {
	w := &gsmBits{b: make([]byte, 0, 33)}
	w.put(0xD, 4)
	for i := range 8 {
		w.put(f.larc[i], gsmLARBits[i])
	}
	for k := range 4 {
		w.put(f.nc[k], 7)
		w.put(f.bc[k], 2)
		w.put(f.mc[k], 2)
		w.put(f.xmaxc[k], 6)
		for i := range 13 {
			w.put(f.xmc[k*13+i], 3)
		}
	}
	return w.b
}

func unpackGSM(p []byte) (gsmFrame, bool) {
	var f gsmFrame
	if len(p) != 33 || p[0]>>4 != 0xD {
		return f, false
	}
	pos := 4
	get := func(bits int) int32 {
		var v int32
		for range bits {
			v = v<<1 | int32(p[pos/8]>>(7-pos%8)&1)
			pos++
		}
		return v
	}
	for i := range 8 {
		f.larc[i] = get(gsmLARBits[i])
	}
	for k := range 4 {
		f.nc[k] = get(7)
		f.bc[k] = get(2)
		f.mc[k] = get(2)
		f.xmaxc[k] = get(6)
		for i := range 13 {
			f.xmc[k*13+i] = get(3)
		}
	}
	return f, true
}

// GSMEncoder encodes 8 kHz audio to GSM 06.10, 160 samples to 33 octets.
type GSMEncoder struct{ s *gsmState }

// GSMDecoder decodes it.
type GSMDecoder struct{ s *gsmState }

// NewGSMEncoder makes an encoder.
func NewGSMEncoder() *GSMEncoder { return &GSMEncoder{newGSMState()} }

// NewGSMDecoder makes a decoder.
func NewGSMDecoder() *GSMDecoder { return &GSMDecoder{newGSMState()} }

// Encode encodes whole 20 ms frames of pcm (a multiple of 160 samples; any rest is ignored).
func (e *GSMEncoder) Encode(pcm []int16) []byte {
	var out []byte
	for i := 0; i+160 <= len(pcm); i += 160 {
		f := e.s.encodeFrame(pcm[i : i+160])
		out = append(out, f.pack()...)
	}
	return out
}

// Decode decodes a payload of whole 33-octet frames; a frame that is not GSM is skipped.
func (d *GSMDecoder) Decode(p []byte) []int16 {
	var out []int16
	for i := 0; i+33 <= len(p); i += 33 {
		f, ok := unpackGSM(p[i : i+33])
		if !ok {
			continue
		}
		pcm := make([]int16, 160)
		d.s.decodeFrame(&f, pcm)
		out = append(out, pcm...)
	}
	return out
}

type gsmv struct {
	enc *GSMEncoder
	dec *GSMDecoder
}

func (*gsmv) rate() int                   { return 8000 }
func (*gsmv) clock() int                  { return 8000 }
func (*gsmv) take(n int) int              { return n - n%160 }
func (g *gsmv) decode(p []byte) []int16   { return g.dec.Decode(p) }
func (g *gsmv) encode(pcm []int16) []byte { return g.enc.Encode(pcm) }
