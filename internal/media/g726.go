package media

// G.726: ADPCM at 32 and 16 kbit/s (ITU-T G.726, which absorbed G.721), the two rates RTP equipment
// uses (G726-32 and G726-16, RFC 3551). Both decode bit for bit as ffmpeg's implementation does. The
// arithmetic follows the recommendation bit for bit, in the integer form of the widely used public
// domain reference: a 14-bit linear signal, an adaptive quantizer and a pole-zero predictor whose
// coefficients adapt sample by sample. Packing on the wire is RFC 3551's: the first codeword sits in
// the least significant bits of the first octet.

// g726Rate describes one of the four bit rates: its codeword size and tables.
type g726Rate struct {
	bits   int
	qtab   []int32 // quantizer decision levels
	dqln   []int32 // log of the reconstructed difference, by codeword
	wi     []int32 // scale factor multipliers, by codeword
	fi     []int32 // transition detector inputs, by codeword
	noZero bool    // 16 kbit/s has no "zero" codeword
}

var (
	g726r16 = &g726Rate{bits: 2, qtab: []int32{261},
		dqln: []int32{116, 365, 365, 116}, wi: []int32{-704, 14048, 14048, -704}, fi: []int32{0, 0xE00, 0xE00, 0}, noZero: true}
	g726r32 = &g726Rate{bits: 4, qtab: []int32{-124, 80, 178, 246, 300, 349, 400},
		dqln: []int32{-2048, 4, 135, 213, 273, 323, 373, 425, 425, 373, 323, 273, 213, 135, 4, -2048},
		wi:   []int32{-12 << 5, 18 << 5, 41 << 5, 64 << 5, 112 << 5, 198 << 5, 355 << 5, 1122 << 5, 1122 << 5, 355 << 5, 198 << 5, 112 << 5, 64 << 5, 41 << 5, 18 << 5, -12 << 5},
		fi:   []int32{0, 0, 0, 0x200, 0x200, 0x200, 0x600, 0xE00, 0xE00, 0x600, 0x200, 0x200, 0x200, 0, 0, 0}}
)

// g726State is the adaptive state of one direction of one stream.
type g726State struct {
	yl       int32    // locked (slow) quantizer scale factor
	yu       int32    // unlocked (fast) quantizer scale factor
	dms, dml int32    // short and long term averages of the transition input
	ap       int32    // speed control
	a        [2]int32 // pole predictor coefficients
	b        [6]int32 // zero predictor coefficients
	pk       [2]int32 // signs of the partial reconstruction
	dq       [6]int32 // quantized differences, in the predictor's floating format
	sr       [2]int32 // reconstructed signal, in the same format
	td       int32    // tone detected
}

func newG726State() *g726State {
	s := &g726State{yl: 34816, yu: 544}
	for i := range s.sr {
		s.sr[i] = 32
	}
	for i := range s.dq {
		s.dq[i] = 32
	}
	return s
}

var g726power2 = [15]int32{1, 2, 4, 8, 0x10, 0x20, 0x40, 0x80, 0x100, 0x200, 0x400, 0x800, 0x1000, 0x2000, 0x4000}

// quan is the index of the first table entry above val.
func quan(val int32, table []int32) int32 {
	for i, t := range table {
		if val < t {
			return int32(i)
		}
	}
	return int32(len(table))
}

// s16 keeps a value as the 16-bit signed number the recommendation stores it in.
func s16(v int32) int32 { return int32(int16(v)) }

// fmult multiplies a predictor coefficient by a value in the floating format.
func fmult(an, srn int32) int32 {
	var anmag int32
	if an > 0 {
		anmag = an
	} else {
		anmag = (-an) & 0x1FFF
	}
	anexp := quan(anmag, g726power2[:]) - 6
	var anmant int32
	switch {
	case anmag == 0:
		anmant = 32
	case anexp >= 0:
		anmant = anmag >> anexp
	default:
		anmant = anmag << -anexp
	}
	wanexp := anexp + ((srn >> 6) & 0xF) - 13
	wanmant := (anmant*(srn&0o77) + 0x30) >> 4
	var ret int32
	if wanexp >= 0 {
		ret = (wanmant << wanexp) & 0x7FFF
	} else {
		ret = wanmant >> -wanexp
	}
	if (an ^ srn) < 0 {
		return -ret
	}
	return ret
}

func (s *g726State) predictorZero() int32 {
	sezi := fmult(s.b[0]>>2, s.dq[0])
	for i := 1; i < 6; i++ {
		sezi += fmult(s.b[i]>>2, s.dq[i])
	}
	return sezi
}

func (s *g726State) predictorPole() int32 {
	return fmult(s.a[1]>>2, s.sr[1]) + fmult(s.a[0]>>2, s.sr[0])
}

func (s *g726State) stepSize() int32 {
	if s.ap >= 256 {
		return s.yu
	}
	y := s.yl >> 6
	dif := s.yu - y
	al := s.ap >> 2
	if dif > 0 {
		y += (dif * al) >> 6
	} else if dif < 0 {
		y += (dif*al + 0x3F) >> 6
	}
	return y
}

func (r *g726Rate) quantize(d, y int32) int32 {
	dqm := d
	if dqm < 0 {
		dqm = -dqm
	}
	exp := quan(dqm>>1, g726power2[:])
	mant := ((dqm << 7) >> exp) & 0x7F
	dl := (exp << 7) + mant
	dln := dl - (y >> 2)
	i := quan(dln, r.qtab)
	size := int32(len(r.qtab))
	switch {
	case d < 0:
		return (size << 1) + 1 - i
	case i == 0 && !r.noZero:
		return (size << 1) + 1 // the "zero" codeword
	}
	return i
}

func reconstruct(sign bool, dqln, y int32) int32 {
	dql := dqln + (y >> 2)
	if dql < 0 {
		if sign {
			return -0x8000
		}
		return 0
	}
	dex := (dql >> 7) & 15
	dqt := 128 + (dql & 127)
	dq := (dqt << 7) >> (14 - dex)
	if sign {
		return dq - 0x8000
	}
	return dq
}

// float converts a magnitude with its sign to the predictor's floating format.
func g726float(v int32) int32 {
	switch {
	case v == 0:
		return 0x20
	case v > 0:
		exp := quan(v, g726power2[:])
		return (exp << 6) + ((v << 6) >> exp)
	case v > -32768:
		mag := -v
		exp := quan(mag, g726power2[:])
		return s16((exp << 6) + ((mag << 6) >> exp) - 0x400)
	}
	return s16(0xFC20)
}

func (s *g726State) update(bits int, y, wi, fi, dq, sr, dqsez int32) {
	var pk0 int32
	if dqsez < 0 {
		pk0 = 1
	}
	mag := dq & 0x7FFF
	ylint := s.yl >> 15
	ylfrac := (s.yl >> 10) & 0x1F
	thr1 := (32 + ylfrac) << ylint
	thr2 := thr1
	if ylint > 9 {
		thr2 = 31 << 10
	}
	dqthr := (thr2 + (thr2 >> 1)) >> 1
	tr := s.td != 0 && mag > dqthr

	s.yu = y + ((wi - y) >> 5)
	s.yu = min(max(s.yu, 544), 5120)
	s.yl += s.yu + ((-s.yl) >> 6)

	var a2p int32
	if tr {
		s.a = [2]int32{}
		s.b = [6]int32{}
	} else {
		pks1 := pk0 ^ s.pk[0]
		a2p = s.a[1] - (s.a[1] >> 7)
		if dqsez != 0 {
			fa1 := -s.a[0]
			if pks1 != 0 {
				fa1 = s.a[0]
			}
			switch {
			case fa1 < -8191:
				a2p -= 0x100
			case fa1 > 8191:
				a2p += 0xFF
			default:
				a2p += fa1 >> 5
			}
			if pk0^s.pk[1] != 0 {
				switch {
				case a2p <= -12160:
					a2p = -12288
				case a2p >= 12416:
					a2p = 12288
				default:
					a2p -= 0x80
				}
			} else {
				switch {
				case a2p <= -12416:
					a2p = -12288
				case a2p >= 12160:
					a2p = 12288
				default:
					a2p += 0x80
				}
			}
		}
		s.a[1] = s16(a2p)
		s.a[0] -= s.a[0] >> 8
		if dqsez != 0 {
			if pks1 == 0 {
				s.a[0] += 192
			} else {
				s.a[0] -= 192
			}
		}
		a1ul := 15360 - a2p
		s.a[0] = s16(min(max(s.a[0], -a1ul), a1ul))
		for i := range s.b {
			if bits == 5 {
				s.b[i] -= s.b[i] >> 9
			} else {
				s.b[i] -= s.b[i] >> 8
			}
			if dq&0x7FFF != 0 {
				if (dq ^ s.dq[i]) >= 0 {
					s.b[i] += 128
				} else {
					s.b[i] -= 128
				}
			}
			s.b[i] = s16(s.b[i])
		}
	}
	for i := 5; i > 0; i-- {
		s.dq[i] = s.dq[i-1]
	}
	if mag == 0 {
		if dq >= 0 {
			s.dq[0] = 0x20
		} else {
			s.dq[0] = s16(0xFC20)
		}
	} else {
		exp := quan(mag, g726power2[:])
		v := (exp << 6) + ((mag << 6) >> exp)
		if dq < 0 {
			v -= 0x400
		}
		s.dq[0] = s16(v)
	}
	s.sr[1] = s.sr[0]
	s.sr[0] = g726float(sr)
	s.pk[1] = s.pk[0]
	s.pk[0] = pk0
	switch {
	case tr:
		s.td = 0
	case a2p < -11776:
		s.td = 1
	default:
		s.td = 0
	}
	s.dms += (fi - s.dms) >> 5
	s.dml += ((fi << 2) - s.dml) >> 7
	switch {
	case tr:
		s.ap = 256
	case y < 1536, s.td == 1:
		s.ap += (0x200 - s.ap) >> 4
	default:
		d := (s.dms << 2) - s.dml
		if d < 0 {
			d = -d
		}
		if d >= (s.dml >> 3) {
			s.ap += (0x200 - s.ap) >> 4
		} else {
			s.ap += (-s.ap) >> 4
		}
	}
}

// encode turns one 16-bit sample into a codeword.
func (s *g726State) encode(r *g726Rate, sample int16) int32 {
	sl := int32(sample) >> 2 // 14 bits
	sezi := s16(s.predictorZero())
	sez := sezi >> 1
	se := s16((sezi + s.predictorPole()) >> 1)
	d := s16(sl - se)
	y := s.stepSize()
	i := r.quantize(d, y)
	sign := int32(1) << (r.bits - 1)
	dq := reconstruct(i&sign != 0, r.dqln[i], y)
	var sr int32
	if dq < 0 {
		sr = s16(se - (dq & 0x3FFF))
	} else {
		sr = s16(se + dq)
	}
	dqsez := s16(sr + sez - se)
	s.update(r.bits, y, r.wi[i], r.fi[i], dq, sr, dqsez)
	return i
}

// decode turns one codeword into a 16-bit sample.
func (s *g726State) decode(r *g726Rate, i int32) int16 {
	i &= int32(1)<<r.bits - 1
	sezi := s16(s.predictorZero())
	sez := sezi >> 1
	se := s16((sezi + s.predictorPole()) >> 1)
	y := s.stepSize()
	sign := int32(1) << (r.bits - 1)
	dq := reconstruct(i&sign != 0, r.dqln[i], y)
	var sr int32
	if dq < 0 {
		sr = s16(se - (dq & 0x3FFF))
	} else {
		sr = s16(se + dq)
	}
	dqsez := s16(sr - se + sez)
	s.update(r.bits, y, r.wi[i], r.fi[i], dq, sr, dqsez)
	out := sr << 2
	return int16(min(max(out, -32768), 32767))
}

// G726Encoder encodes 8 kHz linear audio at one of G.726's rates.
type G726Encoder struct {
	r   *g726Rate
	s   *g726State
	msb bool
}

// G726Decoder decodes it.
type G726Decoder struct {
	r   *g726Rate
	s   *g726State
	msb bool
}

func g726RateFor(kbps int) *g726Rate {
	switch kbps {
	case 16:
		return g726r16
	}
	return g726r32
}

// NewG726Encoder makes an encoder for 32 or 16 kbit/s.
func NewG726Encoder(kbps int) *G726Encoder {
	return &G726Encoder{r: g726RateFor(kbps), s: newG726State()}
}

// NewG726Decoder makes a decoder for 32 or 16 kbit/s.
func NewG726Decoder(kbps int) *G726Decoder {
	return &G726Decoder{r: g726RateFor(kbps), s: newG726State()}
}

// MSBFirst packs the first codeword in the high bits instead (AAL2-G726-32, ITU-T I.366.2), the
// order some older phones and gateways use.
func (e *G726Encoder) MSBFirst() *G726Encoder { e.msb = true; return e }

// MSBFirst reads the AAL2 order.
func (d *G726Decoder) MSBFirst() *G726Decoder { d.msb = true; return d }

// Encode packs the codewords of pcm, first codeword in the low bits (RFC 3551). len(pcm)*bits must be
// a multiple of 8; 20 ms (160 samples) always is.
func (e *G726Encoder) Encode(pcm []int16) []byte {
	if e.msb {
		return e.encodeMSB(pcm)
	}
	out := make([]byte, 0, len(pcm)*e.r.bits/8+1)
	var acc uint32
	n := 0
	for _, v := range pcm {
		acc |= uint32(e.s.encode(e.r, v)) << n
		n += e.r.bits
		for n >= 8 {
			out = append(out, byte(acc))
			acc >>= 8
			n -= 8
		}
	}
	if n > 0 {
		out = append(out, byte(acc))
	}
	return out
}

// Decode unpacks and decodes a payload.
func (d *G726Decoder) Decode(p []byte) []int16 {
	if d.msb {
		return d.decodeMSB(p)
	}
	out := make([]int16, 0, len(p)*8/d.r.bits)
	var acc uint32
	n := 0
	mask := uint32(1)<<d.r.bits - 1
	for _, b := range p {
		acc |= uint32(b) << n
		n += 8
		for n >= d.r.bits {
			out = append(out, d.s.decode(d.r, int32(acc&mask)))
			acc >>= d.r.bits
			n -= d.r.bits
		}
	}
	return out
}

func (e *G726Encoder) encodeMSB(pcm []int16) []byte {
	out := make([]byte, 0, len(pcm)*e.r.bits/8+1)
	var acc uint32
	n := 0
	for _, v := range pcm {
		acc = acc<<e.r.bits | uint32(e.s.encode(e.r, v))
		n += e.r.bits
		for n >= 8 {
			out = append(out, byte(acc>>(n-8)))
			n -= 8
		}
	}
	if n > 0 {
		out = append(out, byte(acc<<(8-n)))
	}
	return out
}

func (d *G726Decoder) decodeMSB(p []byte) []int16 {
	out := make([]int16, 0, len(p)*8/d.r.bits)
	var acc uint32
	n := 0
	mask := uint32(1)<<d.r.bits - 1
	for _, b := range p {
		acc = acc<<8 | uint32(b)
		n += 8
		for n >= d.r.bits {
			out = append(out, d.s.decode(d.r, int32(acc>>(n-d.r.bits)&mask)))
			n -= d.r.bits
		}
	}
	return out
}
