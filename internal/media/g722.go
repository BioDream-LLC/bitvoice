package media

// G.722 wideband audio (ITU-T G.722, 64 kbit/s mode): 16 kHz linear PCM in, one octet per pair of
// samples out, split by a quadrature mirror filter into a 6-bit low band and a 2-bit high band, each
// coded with adaptive differential PCM. RTP carries it as payload type 9 with an 8000 Hz clock (RFC 3551).

var (
	g722QMF = [12]int{3, -11, 12, 32, -210, 951, 3876, -805, 362, -156, 53, -11}
	g722Q6  = [32]int{0, 35, 72, 110, 150, 190, 233, 276, 323, 370, 422, 473, 530, 587, 650, 714,
		786, 858, 940, 1023, 1121, 1219, 1339, 1458, 1612, 1765, 1980, 2195, 2557, 2919, 0, 0}
	g722ILN = [32]int{0, 63, 62, 31, 30, 29, 28, 27, 26, 25, 24, 23, 22, 21, 20, 19, 18, 17, 16, 15,
		14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 0}
	g722ILP = [32]int{0, 61, 60, 59, 58, 57, 56, 55, 54, 53, 52, 51, 50, 49, 48, 47, 46, 45, 44, 43,
		42, 41, 40, 39, 38, 37, 36, 35, 34, 33, 32, 0}
	g722WL   = [8]int{-60, -30, 58, 172, 334, 538, 1198, 3042}
	g722RL42 = [16]int{0, 7, 6, 5, 4, 3, 2, 1, 7, 6, 5, 4, 3, 2, 1, 0}
	g722ILB  = [32]int{2048, 2093, 2139, 2186, 2233, 2282, 2332, 2383, 2435, 2489, 2543, 2599, 2656,
		2714, 2774, 2834, 2896, 2960, 3025, 3091, 3158, 3228, 3298, 3371, 3444, 3520, 3597, 3676, 3756,
		3838, 3922, 4008}
	g722QM4 = [16]int{0, -20456, -12896, -8968, -6288, -4240, -2584, -1200, 20456, 12896, 8968, 6288,
		4240, 2584, 1200, 0}
	g722QM6 = [64]int{-136, -136, -136, -136, -24808, -21904, -19008, -16704, -14984, -13512, -12280,
		-11192, -10232, -9360, -8576, -7856, -7192, -6576, -6000, -5456, -4944, -4464, -4008, -3576,
		-3168, -2776, -2400, -2032, -1688, -1360, -1040, -728, 24808, 21904, 19008, 16704, 14984, 13512,
		12280, 11192, 10232, 9360, 8576, 7856, 7192, 6576, 6000, 5456, 4944, 4464, 4008, 3576, 3168, 2776,
		2400, 2032, 1688, 1360, 1040, 728, 432, 136, -432, -136}
	g722QM2 = [4]int{-7408, -1616, 7408, 1616}
	g722IHN = [3]int{0, 1, 0}
	g722IHP = [3]int{0, 3, 2}
	g722WH  = [3]int{0, -214, 798}
	g722RH2 = [4]int{2, 1, 2, 1}
)

func sat16(v int) int {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return v
}

func clampi(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// g722Band is the adaptive predictor and quantiser state of one sub-band.
type g722Band struct {
	s, sp, sz, r int
	a            [3]int
	p            [2]int
	b            [6]int
	d            [7]int
	nb, det      int
}

// scale updates the log scale factor and the step size (blocks 3L/3H).
func (b *g722Band) scale(w, top, shift int) {
	b.nb = clampi((b.nb*127)>>7+w, 0, top)
	wd1 := (b.nb >> 6) & 31
	wd2 := shift - (b.nb >> 11)
	var wd3 int
	if wd2 < 0 {
		wd3 = g722ILB[wd1] << -wd2
	} else {
		wd3 = g722ILB[wd1] >> wd2
	}
	b.det = wd3 << 2
}

func neg(v int) bool { return v < 0 }

// adapt is block 4: reconstruct, update the pole and zero predictors, and predict the next sample.
func (b *g722Band) adapt(dx int) {
	r := sat16(b.s + dx)
	p := sat16(b.sz + dx)

	// UPPOL2
	wd1 := sat16(b.a[1] << 2)
	wd2 := -wd1
	if neg(p) != neg(b.p[0]) {
		wd2 = wd1
	}
	if wd2 > 32767 {
		wd2 = 32767
	}
	wd3 := 128
	if neg(p) != neg(b.p[1]) {
		wd3 = -128
	}
	wd3 += wd2 >> 7
	wd3 += (b.a[2] * 32512) >> 15
	ap2 := clampi(wd3, -12288, 12288)

	// UPPOL1
	wd1 = 192
	if neg(p) != neg(b.p[0]) {
		wd1 = -192
	}
	ap1 := sat16(wd1 + (b.a[1]*32640)>>15)
	lim := sat16(15360 - ap2)
	ap1 = clampi(ap1, -lim, lim)

	// FILTEP
	sp := sat16(((ap1 * sat16(r+r)) >> 15) + ((ap2 * sat16(b.r+b.r)) >> 15))
	b.r = r
	b.a[1], b.a[2] = ap1, ap2
	b.p[1], b.p[0] = b.p[0], p

	// UPZERO, DELAYZ and FILTEZ
	step := 128
	if dx == 0 {
		step = 0
	}
	b.d[0] = dx
	sz := 0
	for i := 5; i >= 0; i-- {
		w := step
		if neg(b.d[i+1]) != neg(dx) {
			w = -step
		}
		b.b[i] = sat16(w + (b.b[i]*32640)>>15)
		sz += (b.b[i] * sat16(b.d[i]+b.d[i])) >> 15
		b.d[i+1] = b.d[i]
	}
	b.sz = sat16(sz)
	b.sp = sp
	b.s = sat16(sp + b.sz)
}

// G722Encoder turns 16 kHz linear PCM into G.722 octets. Keep one per stream.
type G722Encoder struct {
	x    [24]int
	band [2]g722Band
}

func NewG722Encoder() *G722Encoder {
	e := &G722Encoder{}
	e.band[0].det, e.band[1].det = 32, 8
	return e
}

// Encode codes pairs of samples; an odd trailing sample is dropped.
func (e *G722Encoder) Encode(pcm []int16) []byte {
	out := make([]byte, 0, len(pcm)/2)
	for j := 0; j+1 < len(pcm); j += 2 {
		copy(e.x[:22], e.x[2:])
		e.x[22], e.x[23] = int(pcm[j]), int(pcm[j+1])
		var even, odd int
		for i := 0; i < 12; i++ {
			odd += e.x[2*i] * g722QMF[i]
			even += e.x[2*i+1] * g722QMF[11-i]
		}
		xlow, xhigh := (even+odd)>>14, (even-odd)>>14

		// Low band: 6-bit quantiser (block 1L), inverse 4-bit quantiser (2L), scale (3L), predictor (4L).
		lo := &e.band[0]
		el := sat16(xlow - lo.s)
		wd := el
		if el < 0 {
			wd = -(el + 1)
		}
		i := 1
		for ; i < 30; i++ {
			if wd < (g722Q6[i]*lo.det)>>12 {
				break
			}
		}
		ilow := g722ILP[i]
		if el < 0 {
			ilow = g722ILN[i]
		}
		ril := ilow >> 2
		dlow := (lo.det * g722QM4[ril]) >> 15
		lo.scale(g722WL[g722RL42[ril]], 18432, 8)
		lo.adapt(dlow)

		// High band: 2-bit quantiser.
		hi := &e.band[1]
		eh := sat16(xhigh - hi.s)
		wd = eh
		if eh < 0 {
			wd = -(eh + 1)
		}
		mih := 1
		if wd >= (564*hi.det)>>12 {
			mih = 2
		}
		ihigh := g722IHP[mih]
		if eh < 0 {
			ihigh = g722IHN[mih]
		}
		dhigh := (hi.det * g722QM2[ihigh]) >> 15
		hi.scale(g722WH[g722RH2[ihigh]], 22528, 10)
		hi.adapt(dhigh)

		out = append(out, byte(ihigh<<6|ilow))
	}
	return out
}

// G722Decoder turns G.722 octets back into 16 kHz linear PCM, two samples per octet.
type G722Decoder struct {
	x    [24]int
	band [2]g722Band
}

func NewG722Decoder() *G722Decoder {
	d := &G722Decoder{}
	d.band[0].det, d.band[1].det = 32, 8
	return d
}

func (d *G722Decoder) Decode(code []byte) []int16 {
	out := make([]int16, 0, 2*len(code))
	for _, c := range code {
		rl, ihigh := int(c&0x3f), int(c>>6)

		lo := &d.band[0]
		rlow := clampi(lo.s+(lo.det*g722QM6[rl])>>15, -16384, 16383)
		ilow := rl >> 2
		dlow := (lo.det * g722QM4[ilow]) >> 15
		lo.scale(g722WL[g722RL42[ilow]], 18432, 8)
		lo.adapt(dlow)

		hi := &d.band[1]
		dhigh := (hi.det * g722QM2[ihigh]) >> 15
		rhigh := clampi(dhigh+hi.s, -16384, 16383)
		hi.scale(g722WH[g722RH2[ihigh]], 22528, 10)
		hi.adapt(dhigh)

		copy(d.x[:22], d.x[2:])
		d.x[22], d.x[23] = rlow+rhigh, rlow-rhigh
		var x1, x2 int
		for i := 0; i < 12; i++ {
			x2 += d.x[2*i] * g722QMF[i]
			x1 += d.x[2*i+1] * g722QMF[11-i]
		}
		out = append(out, int16(sat16(x1>>11)), int16(sat16(x2>>11)))
	}
	return out
}
