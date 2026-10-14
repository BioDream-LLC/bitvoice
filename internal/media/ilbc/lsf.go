package ilbc

import "math"

// Translations of lsf.c, gainquant.c, getCBvec.c and packing.c.

func cosf(x float32) float32 { return float32(math.Cos(float64(x))) }

func absf(x float32) float32 { return float32(math.Abs(float64(x))) }

func a2lsf(freq []float32, a []float32) {
	steps := [lsfNumberOfSteps]float32{0.00635, 0.003175, 0.0015875, 0.00079375}
	var p, q, pPre, qPre [lpcHalforder]float32
	for i := 0; i < lpcHalforder; i++ {
		p[i] = float32(-1.0) * (a[i+1] + a[lpcFilterorder-i])
		q[i] = a[lpcFilterorder-i] - a[i+1]
	}
	pPre[0] = float32(-1.0) - p[0]
	pPre[1] = -pPre[0] - p[1]
	pPre[2] = -pPre[1] - p[2]
	pPre[3] = -pPre[2] - p[3]
	pPre[4] = -pPre[3] - p[4]
	pPre[4] = pPre[4] / 2
	qPre[0] = float32(1.0) - q[0]
	qPre[1] = qPre[0] - q[1]
	qPre[2] = qPre[1] - q[2]
	qPre[3] = qPre[2] - q[3]
	qPre[4] = qPre[3] - q[4]
	qPre[4] = qPre[4] / 2
	var omega, oldOmega float32
	oldP, oldQ := floatMax, floatMax
	for lspIndex := 0; lspIndex < lpcFilterorder; lspIndex++ {
		pq := &pPre
		old := &oldP
		if lspIndex&1 != 0 {
			pq, old = &qPre, &oldQ
		}
		stepIdx := 0
		step := steps[0]
		for stepIdx < lsfNumberOfSteps {
			hlp := cosf(omega * twoPi)
			hlp1 := mul(2.0, hlp) + pq[0]
			hlp2 := mul(mul(2.0, hlp), hlp1) - 1.0 + pq[1]
			hlp3 := mul(mul(2.0, hlp), hlp2) - hlp1 + pq[2]
			hlp4 := mul(mul(2.0, hlp), hlp3) - hlp2 + pq[3]
			hlp5 := mul(hlp, hlp4) - hlp3 + pq[4]
			if mul(hlp5, *old) <= 0.0 || omega >= 0.5 {
				if stepIdx == lsfNumberOfSteps-1 {
					if absf(hlp5) >= absf(*old) {
						freq[lspIndex] = omega - step
					} else {
						freq[lspIndex] = omega
					}
					if *old >= 0.0 {
						*old = float32(-1.0) * floatMax
					} else {
						*old = floatMax
					}
					omega = oldOmega
					stepIdx = lsfNumberOfSteps
				} else {
					if stepIdx == 0 {
						oldOmega = omega
					}
					stepIdx++
					omega -= steps[stepIdx]
					step = steps[stepIdx]
				}
			} else {
				*old = hlp5
				omega += step
			}
		}
	}
	for i := 0; i < lpcFilterorder; i++ {
		freq[i] = freq[i] * twoPi
	}
}

func lsf2a(aCoef []float32, freq []float32) {
	var p, q, a1, a2, b1, b2 [lpcHalforder]float32
	var a, b [lpcHalforder + 1]float32
	for i := 0; i < lpcFilterorder; i++ {
		freq[i] = freq[i] * pi2
	}
	if freq[0] <= 0.0 || freq[lpcFilterorder-1] >= 0.5 {
		if freq[0] <= 0.0 {
			freq[0] = 0.022
		}
		if freq[lpcFilterorder-1] >= 0.5 {
			freq[lpcFilterorder-1] = 0.499
		}
		hlp := (freq[lpcFilterorder-1] - freq[0]) / float32(lpcFilterorder-1)
		for i := 1; i < lpcFilterorder; i++ {
			freq[i] = freq[i-1] + hlp
		}
	}
	for i := 0; i < lpcHalforder; i++ {
		p[i] = cosf(twoPi * freq[2*i])
		q[i] = cosf(twoPi * freq[2*i+1])
	}
	a[0], b[0] = 0.25, 0.25
	step := func() {
		for i := 0; i < lpcHalforder; i++ {
			a[i+1] = a[i] - mul(mul(2, p[i]), a1[i]) + a2[i]
			b[i+1] = b[i] - mul(mul(2, q[i]), b1[i]) + b2[i]
			a2[i] = a1[i]
			a1[i] = a[i]
			b2[i] = b1[i]
			b1[i] = b[i]
		}
	}
	step()
	for j := 0; j < lpcFilterorder; j++ {
		if j == 0 {
			a[0], b[0] = 0.25, -0.25
		} else {
			a[0], b[0] = 0.0, 0.0
		}
		step()
		aCoef[j+1] = 2 * (a[lpcHalforder] + b[lpcHalforder])
	}
	aCoef[0] = 1.0
}

func gainquant(in, maxIn float32, cblen int, index *int) float32 {
	scale := maxIn
	if scale < 0.1 {
		scale = 0.1
	}
	cb := gain_sq5Tbl
	switch cblen {
	case 8:
		cb = gain_sq3Tbl
	case 16:
		cb = gain_sq4Tbl
	}
	minmeasure := float32(10000000.0)
	tindex := 0
	for i := 0; i < cblen; i++ {
		d := in - mul(scale, cb[i])
		measure := mul(d, d)
		if measure < minmeasure {
			tindex = i
			minmeasure = measure
		}
	}
	*index = tindex
	return mul(scale, cb[tindex])
}

func gaindequant(index int, maxIn float32, cblen int) float32 {
	scale := absf(maxIn)
	if scale < 0.1 {
		scale = 0.1
	}
	switch cblen {
	case 8:
		return mul(scale, gain_sq3Tbl[index])
	case 16:
		return mul(scale, gain_sq4Tbl[index])
	case 32:
		return mul(scale, gain_sq5Tbl[index])
	}
	return 0.0
}

func getCBvec(cbvec []float32, mem []float32, index, lMem, cbveclen int) {
	var tmpbuf [cbMeml]float32
	baseSize := lMem - cbveclen + 1
	if cbveclen == subl {
		baseSize += cbveclen / 2
	}
	switch {
	case index < lMem-cbveclen+1:
		k := index + cbveclen
		copy(cbvec[:cbveclen], mem[lMem-k:lMem-k+cbveclen])
	case index < baseSize:
		k := 2*(index-(lMem-cbveclen+1)) + cbveclen
		ihigh := k / 2
		ilow := ihigh - 5
		copy(cbvec[:ilow], mem[lMem-k/2:lMem-k/2+ilow])
		alfa1 := float32(0.2)
		var alfa float32
		for j := ilow; j < ihigh; j++ {
			cbvec[j] = mul(float32(1.0)-alfa, mem[lMem-k/2+j]) + mul(alfa, mem[lMem-k+j])
			alfa += alfa1
		}
		copy(cbvec[ihigh:cbveclen], mem[lMem-k+ihigh:lMem-k+cbveclen])
	default:
		var tempbuff2 [cbMeml + cbFilterlen + 1]float32
		copy(tempbuff2[cbHalffilterlen:], mem[:lMem])
		if index-baseSize < lMem-cbveclen+1 {
			k := index - baseSize + cbveclen
			sFilt := lMem - k
			memInd := sFilt + 1 - cbHalffilterlen
			for n := 0; n < cbveclen; n++ {
				pp := memInd + n + cbHalffilterlen
				pp1 := cbFilterlen - 1
				var o float32
				for j := 0; j < cbFilterlen; j++ {
					o += mul(tempbuff2[pp], cbfiltersTbl[pp1])
					pp++
					pp1--
				}
				cbvec[n] = o
			}
		} else {
			k := 2*(index-baseSize-(lMem-cbveclen+1)) + cbveclen
			sFilt := lMem - k
			memInd := sFilt + 1 - cbHalffilterlen
			for i := 0; i < k; i++ {
				pp := memInd + i + cbHalffilterlen
				pp1 := cbFilterlen - 1
				var o float32
				for j := 0; j < cbFilterlen; j++ {
					o += mul(tempbuff2[pp], cbfiltersTbl[pp1])
					pp++
					pp1--
				}
				tmpbuf[sFilt+i] = o
			}
			ihigh := k / 2
			ilow := ihigh - 5
			copy(cbvec[:ilow], tmpbuf[lMem-k/2:lMem-k/2+ilow])
			alfa1 := float32(0.2)
			var alfa float32
			for j := ilow; j < ihigh; j++ {
				cbvec[j] = mul(float32(1.0)-alfa, tmpbuf[lMem-k/2+j]) + mul(alfa, tmpbuf[lMem-k+j])
				alfa += alfa1
			}
			copy(cbvec[ihigh:cbveclen], tmpbuf[lMem-k+ihigh:lMem-k+cbveclen])
		}
	}
}

func packsplit(index *int, firstpart, rest *int, bitnoFirstpart, bitnoTotal int) {
	bitnoRest := bitnoTotal - bitnoFirstpart
	*firstpart = *index >> bitnoRest
	*rest = *index - (*firstpart << bitnoRest)
}

func packcombine(index *int, rest, bitnoRest int) {
	*index = *index << bitnoRest
	*index += rest
}

// bitstream is the reference's unsigned char ** with its bit position.
type bitstream struct {
	b   []byte
	at  int
	pos int
}

func (s *bitstream) dopack(index, bitno int) {
	if s.pos == 0 {
		s.b[s.at] = 0
	}
	for bitno > 0 {
		if s.pos == 8 {
			s.pos = 0
			s.at++
			s.b[s.at] = 0
		}
		posLeft := 8 - s.pos
		if bitno <= posLeft {
			s.b[s.at] |= byte(index << (posLeft - bitno))
			s.pos += bitno
			bitno = 0
		} else {
			s.b[s.at] |= byte(index >> (bitno - posLeft))
			s.pos = 8
			index -= (index >> (bitno - posLeft)) << (bitno - posLeft)
			bitno -= posLeft
		}
	}
}

func (s *bitstream) unpack(index *int, bitno int) {
	*index = 0
	for bitno > 0 {
		if s.pos == 8 {
			s.pos = 0
			s.at++
		}
		bitsLeft := 8 - s.pos
		cur := 0
		if s.at < len(s.b) {
			cur = int(s.b[s.at])
		}
		if bitsLeft >= bitno {
			*index += ((cur << s.pos) & 0xFF) >> (8 - bitno)
			s.pos += bitno
			bitno = 0
		} else {
			if 8-bitno > 0 {
				*index += ((cur << s.pos) & 0xFF) >> (8 - bitno)
			} else {
				*index += ((cur << s.pos) & 0xFF) << (bitno - 8)
			}
			s.pos = 8
			bitno -= bitsLeft
		}
	}
}
