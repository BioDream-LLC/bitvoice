package speex

import (
	"errors"
	"math"
)

// The narrowband modes and the encoder and decoder of libspeex (modes.c, nb_celp.c, speex.c).

const (
	lpcOrder   = 10
	frameSize  = 160
	subframe   = 40
	nbSub      = 4
	pitchStart = 17
	pitchEnd   = 144
	windowSize = frameSize + subframe
	// FrameSamples is the samples in one Speex frame: 20 ms at 8 kHz.
	FrameSamples = frameSize
)

type submode struct {
	lbrPitch, forcedPitchGain, haveSubframeGain int
	doubleCodebook                              bool
	lspQuant                                    func(lsp, q []float32, order int, w *bitWriter)
	lspUnquant                                  func(lsp []float32, order int, r *bitReader)
	ltpQuant                                    ltpQuantFunc
	ltpUnquant                                  ltpUnquantFunc
	ltp                                         *ltpParams
	innovQuant                                  innovQuantFunc
	innovUnquant                                innovUnquantFunc
	innov                                       *cbParams
	combGain                                    float32
	bitsPerFrame                                int
	noise                                       bool
}

var (
	ltpNB   = &ltpParams{gainCdbkNB[:], 7, 7}
	ltpVLBR = &ltpParams{gainCdbkLBR[:], 5, 0}
	ltpLBR  = &ltpParams{gainCdbkLBR[:], 5, 7}
	ltpMed  = &ltpParams{gainCdbkLBR[:], 5, 7}

	cbVLBR = &cbParams{10, 4, exc10x16[:], 4, 0}
	cbULBR = &cbParams{20, 2, exc20x32[:], 5, 0}
	cbLBR  = &cbParams{10, 4, exc10x32[:], 5, 0}
	cbNB   = &cbParams{5, 8, exc5x64[:], 6, 0}
	cbMed  = &cbParams{8, 5, exc8x128[:], 7, 0}
	cbSB   = &cbParams{5, 8, exc5x256[:], 8, 0}
)

var submodes = [16]*submode{
	1: {0, 1, 0, false, lspQuantLBR, lspUnquantLBR, forcedPitchQuant, forcedPitchUnquant, nil, noiseCbQuant, noiseCbUnquant, nil, -1, 43, true},
	2: {0, 0, 0, false, lspQuantLBR, lspUnquantLBR, pitchSearch3tap, pitchUnquant3tap, ltpVLBR, splitCbSearch, splitCbUnquant, cbVLBR, .6, 119, false},
	3: {-1, 0, 1, false, lspQuantLBR, lspUnquantLBR, pitchSearch3tap, pitchUnquant3tap, ltpLBR, splitCbSearch, splitCbUnquant, cbLBR, .55, 160, false},
	4: {-1, 0, 1, false, lspQuantLBR, lspUnquantLBR, pitchSearch3tap, pitchUnquant3tap, ltpMed, splitCbSearch, splitCbUnquant, cbMed, .45, 220, false},
	5: {-1, 0, 3, false, lspQuantNB, lspUnquantNB, pitchSearch3tap, pitchUnquant3tap, ltpNB, splitCbSearch, splitCbUnquant, cbNB, .25, 300, false},
	6: {-1, 0, 3, false, lspQuantNB, lspUnquantNB, pitchSearch3tap, pitchUnquant3tap, ltpNB, splitCbSearch, splitCbUnquant, cbSB, .15, 364, false},
	7: {-1, 0, 3, true, lspQuantNB, lspUnquantNB, pitchSearch3tap, pitchUnquant3tap, ltpNB, splitCbSearch, splitCbUnquant, cbNB, .05, 492, false},
	8: {0, 1, 0, false, lspQuantLBR, lspUnquantLBR, forcedPitchQuant, forcedPitchUnquant, nil, splitCbSearch, splitCbUnquant, cbULBR, .5, 79, false},
}

// qualityMap gives the submode of each quality, 0 to 10.
var qualityMap = [11]int{1, 8, 2, 3, 3, 4, 4, 5, 5, 6, 7}

// Bitrate is the bit rate of a quality, in bits per second.
func Bitrate(quality int) int {
	return submodes[qualityMap[min(max(quality, 0), 10)]].bitsPerFrame * 50
}

var (
	scal3Bound = [7]float32{0.112338, 0.236980, 0.369316, 0.492054, 0.637471, 0.828874, 1.132784}
	scal3      = [8]float32{0.061130, 0.163546, 0.310413, 0.428220, 0.555887, 0.719055, 0.938694, 1.326874}
	scal1Bound = [1]float32{0.87798}
	scal1      = [2]float32{0.70469, 1.05127}
)

// Encoder is a narrowband Speex encoder. Its zero value is not ready: use NewEncoder.
type Encoder struct {
	first        bool
	cumulGain    float32
	boundedPitch bool
	gamma1       float32
	gamma2       float32
	lpcFloor     float32
	winBuf       [windowSize - frameSize]float32
	excBuf       [frameSize + pitchEnd + 2]float32
	swBuf        [frameSize + pitchEnd + 2]float32
	oldLSP       [lpcOrder]float32
	oldQLSP      [lpcOrder]float32
	memSp        [lpcOrder]float32
	memSw        [lpcOrder]float32
	memSwWhole   [lpcOrder]float32
	memExc       [lpcOrder]float32
	memExc2      [lpcOrder]float32
	memHp        [2]float32
	complexity   int
	plcTuning    int
	submodeID    int
}

// NewEncoder makes an encoder at a quality from 0 to 10 (8, 15 kbit/s, is the usual) and a
// complexity from 1 to 10 (2 is libspeex's default).
func NewEncoder(quality, complexity int) *Encoder {
	return NewEncoderMode(qualityMap[min(max(quality, 0), 10)], complexity)
}

// NewEncoderMode makes an encoder for one of Speex's narrowband modes, 1 to 8 (RFC 5574's mode=).
func NewEncoderMode(mode, complexity int) *Encoder {
	if mode < 1 || mode > 8 {
		mode = 5
	}
	e := &Encoder{first: true, cumulGain: 1024, boundedPitch: true, gamma1: 0.92, gamma2: 0.6, lpcFloor: .0002,
		complexity: min(max(complexity, 0), 10), plcTuning: 2, submodeID: mode}
	for i := range e.oldLSP {
		e.oldLSP[i] = mul(3.1415927, float32(i+1)) / float32(lpcOrder+1)
	}
	return e
}

const excAt = pitchEnd + 2 // where the reference's exc and sw pointers sit in their buffers

// Encode codes one frame of 160 samples.
func (st *Encoder) Encode(pcm []int16) []byte {
	in := make([]float32, frameSize)
	for i := range in {
		in[i] = float32(pcm[i])
	}
	w := &bitWriter{}
	sm := submodes[st.submodeID]
	copy(st.excBuf[:excAt], st.excBuf[frameSize:])
	copy(st.swBuf[:excAt], st.swBuf[frameSize:])
	exc := st.excBuf[excAt:]
	sw := st.swBuf[excAt:]
	highpass(in, in, frameSize, 0, &st.memHp)

	var lpc, bw1, bw2, lsp, qlsp, iLSP, iQLSP, iLPC, iQLPC [lpcOrder]float32
	{
		var wsig [windowSize]float32
		var ac [lpcOrder + 1]float32
		for i := 0; i < windowSize-frameSize; i++ {
			wsig[i] = mul(st.winBuf[i], lpcWindow[i])
		}
		for i := windowSize - frameSize; i < windowSize; i++ {
			wsig[i] = mul(in[i-windowSize+frameSize], lpcWindow[i])
		}
		autocorrelate(wsig[:], ac[:], lpcOrder+1, windowSize)
		ac[0] += mul(ac[0], st.lpcFloor)
		for i := range ac {
			ac[i] = mul(ac[i], lagWindow[i])
		}
		ac[0]++
		levinson(lpc[:], ac[:], lpcOrder)
		if lpcToLSP(lpc[:], lpcOrder, lsp[:], 10, .2) != lpcOrder {
			lsp = st.oldLSP
		}
	}
	var olPitch int
	var olPitchCoef, olGain float32
	{
		diff := windowSize - frameSize
		if st.first {
			iLSP = lsp
		} else {
			lspInterpolate(st.oldLSP[:], lsp[:], iLSP[:], lpcOrder, nbSub, nbSub<<1, .002)
		}
		lspToLPC(iLSP[:], iLPC[:], lpcOrder)
		if (st.complexity > 2 && sm.haveSubframeGain < 3) || sm.forcedPitchGain != 0 || sm.lbrPitch != -1 {
			var nolPitch [6]int
			var nolCoef [6]float32
			bwLPC(0.9, iLPC[:], bw1[:], lpcOrder)
			bwLPC(0.55, iLPC[:], bw2[:], lpcOrder)
			copy(sw[:diff], st.winBuf[:])
			copy(sw[diff:frameSize], in[:frameSize-diff])
			filterMem16(sw, bw1[:], bw2[:], sw, frameSize, lpcOrder, st.memSwWhole[:])
			openLoopNbestPitch(st.swBuf[:], excAt, pitchStart, pitchEnd, frameSize, nolPitch[:], nolCoef[:], 6)
			olPitch, olPitchCoef = nolPitch[0], nolCoef[0]
			near := func(a, b, lim int) bool { d := a - b; return d <= lim && -d <= lim }
			for i := 1; i < 6; i++ {
				if float64(nolCoef[i]) > mul64(.85, float64(nolCoef[0])) &&
					(near(2*nolPitch[i], olPitch, 2) || near(3*nolPitch[i], olPitch, 3) || near(4*nolPitch[i], olPitch, 4) || near(5*nolPitch[i], olPitch, 5)) {
					olPitch = nolPitch[i]
				}
			}
		}
		firMem16(st.winBuf[:], iLPC[:], exc, diff, lpcOrder, st.memExc[:])
		firMem16(in, iLPC[:], exc[diff:], frameSize-diff, lpcOrder, st.memExc[:])
		g := computeRMS(exc, frameSize)
		if st.submodeID != 1 && olPitch > 0 {
			olGain = mul(g, float32(mul64(1.1, math.Sqrt(1.-mul64(.8, float64(mul(olPitchCoef, olPitchCoef)))))))
		} else {
			olGain = g
		}
	}
	w.pack(0, 1)
	w.pack(uint32(st.submodeID), 4)
	if st.first {
		st.oldLSP = lsp
	}
	sm.lspQuant(lsp[:], qlsp[:], lpcOrder, w)
	if sm.lbrPitch != -1 {
		w.pack(uint32(olPitch-pitchStart), 7)
	}
	if sm.forcedPitchGain != 0 {
		olPitchCoef = float32(mul64(.9, float64(olPitchCoef)))
		quant := int(math.Floor(.5 + float64(mul(15, olPitchCoef))))
		quant = min(max(quant, 0), 15)
		w.pack(uint32(quant), 4)
		olPitchCoef = float32(mul64(0.066667, float64(quant)))
	}
	{
		qe := int(math.Floor(.5 + mul64(3.5, math.Log(float64(olGain)))))
		qe = min(max(qe, 0), 31)
		olGain = float32(math.Exp(float64(qe) / 3.5))
		w.pack(uint32(qe), 5)
	}
	if st.first {
		st.oldQLSP = qlsp
	}
	var target, innov, exc32, synResp, mem [subframe]float32
	for sub := 0; sub < nbSub; sub++ {
		offset := subframe * sub
		ex := st.excBuf[excAt+offset : excAt+offset+subframe]
		s := st.swBuf[excAt+offset : excAt+offset+subframe]
		lspInterpolate(st.oldLSP[:], lsp[:], iLSP[:], lpcOrder, sub, nbSub, .002)
		lspInterpolate(st.oldQLSP[:], qlsp[:], iQLSP[:], lpcOrder, sub, nbSub, .002)
		lspToLPC(iLSP[:], iLPC[:], lpcOrder)
		lspToLPC(iQLSP[:], iQLPC[:], lpcOrder)
		bwLPC(st.gamma1, iLPC[:], bw1[:], lpcOrder)
		bwLPC(st.gamma2, iLPC[:], bw2[:], lpcOrder)
		var inBuf []float32
		if sub == 0 {
			inBuf = st.winBuf[:]
		} else {
			inBuf = in[(sub-1)*subframe:]
		}
		copy(s, inBuf[:subframe])
		bound := subframe
		if st.complexity == 0 {
			bound >>= 1
		}
		impulseResponse(iQLPC[:], bw1[:], bw2[:], synResp[:], bound, lpcOrder)
		for i := bound; i < subframe; i++ {
			synResp[i] = 1e-15
		}
		copy(mem[:lpcOrder], st.memSp[:])
		for i := range ex {
			ex[i] = 1e-15
		}
		iirMem16(ex, iQLPC[:], ex, subframe, lpcOrder, mem[:lpcOrder])
		copy(mem[:lpcOrder], st.memSw[:])
		filterMem16(ex, bw1[:], bw2[:], ex, subframe, lpcOrder, mem[:lpcOrder])
		copy(mem[:lpcOrder], st.memSw[:])
		filterMem16(s, bw1[:], bw2[:], s, subframe, lpcOrder, mem[:lpcOrder])
		if st.complexity == 0 {
			copy(st.memSw[:], mem[:lpcOrder])
		}
		for i := range target {
			target[i] = s[i] - ex[i]
		}
		copy(ex, inBuf[:subframe])
		firMem16(ex, iQLPC[:], ex, subframe, lpcOrder, st.memExc2[:])
		pitMin, pitMax := pitchStart, pitchEnd
		if sm.lbrPitch != -1 {
			if margin := sm.lbrPitch; margin != 0 {
				olPitch = min(max(olPitch, pitchStart+margin-1), pitchEnd-margin)
				pitMin, pitMax = olPitch-margin+1, olPitch+margin
			} else {
				pitMin, pitMax = olPitch, olPitch
			}
		}
		if st.boundedPitch && pitMax > offset {
			pitMax = offset
		}
		sm.ltpQuant(target[:], st.swBuf[:], excAt+offset, iQLPC[:], bw1[:], bw2[:], exc32[:], sm.ltp, pitMin, pitMax, olPitchCoef,
			lpcOrder, subframe, w, st.excBuf[:], excAt+offset, synResp[:], st.complexity, st.plcTuning, &st.cumulGain)
		innov = [subframe]float32{}
		for i := range ex {
			ex[i] -= exc32[i]
		}
		ener := computeRMS(ex, subframe)
		fine := ener / olGain
		switch sm.haveSubframeGain {
		case 3:
			qe := scalQuant(fine, scal3Bound[:], 8)
			w.pack(uint32(qe), 3)
			ener = mul(scal3[qe], olGain)
		case 1:
			qe := scalQuant(fine, scal1Bound[:], 2)
			w.pack(uint32(qe), 1)
			ener = mul(scal1[qe], olGain)
		default:
			ener = olGain
		}
		signalDiv(target[:], target[:], ener, subframe)
		sm.innovQuant(target[:], iQLPC[:], bw1[:], bw2[:], sm.innov, lpcOrder, subframe, innov[:], synResp[:], w, st.complexity, sm.doubleCodebook)
		signalMul(innov[:], innov[:], ener, subframe)
		if sm.doubleCodebook {
			var innov2 [subframe]float32
			for i := range target {
				target[i] = mul(2.2, target[i])
			}
			sm.innovQuant(target[:], iQLPC[:], bw1[:], bw2[:], sm.innov, lpcOrder, subframe, innov2[:], synResp[:], w, st.complexity, false)
			signalMul(innov2[:], innov2[:], mul(0.454545, ener), subframe)
			for i := range innov {
				innov[i] += innov2[i]
			}
		}
		for i := range ex {
			ex[i] = exc32[i] + innov[i]
		}
		iirMem16(ex, iQLPC[:], s, subframe, lpcOrder, st.memSp[:])
		if st.complexity != 0 {
			filterMem16(s, bw1[:], bw2[:], s, subframe, lpcOrder, st.memSw[:])
		}
	}
	st.oldLSP = lsp
	st.oldQLSP = qlsp
	if st.submodeID == 1 {
		w.pack(0, 4)
	}
	st.first = false
	copy(st.winBuf[:], in[2*frameSize-windowSize:])
	st.boundedPitch = sm.noise
	return w.bytes()
}

// Decoder is a narrowband Speex decoder, with the reference's pitch enhancer on.
type Decoder struct {
	first         bool
	countLost     int
	excBuf        [frameSize + 2*pitchEnd + subframe + 12]float32
	oldQLSP       [lpcOrder]float32
	interpQLPC    [lpcOrder]float32
	memSp         [lpcOrder]float32
	memHp         [2]float32
	lastPitch     int
	lastPitchGain float32
	pitchGainBuf  [3]float32
	pitchGainIdx  int
	seed          uint32
	submodeID     int
	vocM1, vocM2  float32
	vocMean       float32
	vocOffset     int
	dtx           bool
}

// NewDecoder makes a decoder.
func NewDecoder() *Decoder {
	return &Decoder{first: true, lastPitch: 40, seed: 1000, submodeID: 5}
}

const decExcAt = 2*pitchEnd + subframe + 6

// ErrCorrupt is a packet the decoder cannot read.
var ErrCorrupt = errors.New("speex: corrupt packet")

var attenuation = [10]float32{1., 0.961, 0.852, 0.698, 0.527, 0.368, 0.237, 0.141, 0.077, 0.039}

// Decode decodes every frame in a packet (an RTP packet may hold several), 160 samples each.
func (st *Decoder) Decode(packet []byte) ([]int16, error) {
	r := &bitReader{buf: packet}
	var out []int16
	for r.remaining() >= 5 {
		frame := make([]float32, frameSize)
		ok, err := st.decode(r, frame)
		if err != nil {
			if len(out) > 0 {
				break
			}
			return nil, err
		}
		if !ok {
			break
		}
		out = append(out, toPCM(frame)...)
	}
	if len(out) == 0 {
		return nil, ErrCorrupt
	}
	return out, nil
}

// Conceal makes up 160 samples for a lost frame (or comfort noise in a silence).
func (st *Decoder) Conceal() []int16 {
	frame := make([]float32, frameSize)
	if st.dtx {
		st.submodeID = 0
		st.decodeFrame(nil, frame)
	} else {
		st.decodeLost(frame)
	}
	return toPCM(frame)
}

func toPCM(f []float32) []int16 {
	out := make([]int16, len(f))
	for i, v := range f {
		switch {
		case v > 32767:
			out[i] = 32767
		case v < -32768:
			out[i] = -32768
		default:
			out[i] = int16(math.Floor(.5 + float64(v)))
		}
	}
	return out
}

var wbSkip = [8]int{0, 36, 112, 192, 352, 0, 0, 0}

// decode reads the mode of the next frame and decodes it; false at the end of the packet.
func (st *Decoder) decode(r *bitReader, out []float32) (bool, error) {
	m := 0
	for {
		if r.remaining() < 5 {
			return false, nil
		}
		if r.unpack(1) != 0 {
			r.advance(wbSkip[r.unpack(3)] - 4)
			if r.remaining() < 5 {
				return false, nil
			}
			if r.unpack(1) != 0 {
				r.advance(wbSkip[r.unpack(3)] - 4)
				if r.unpack(1) != 0 {
					return false, ErrCorrupt
				}
			}
		}
		if r.remaining() < 4 {
			return false, nil
		}
		m = int(r.unpack(4))
		switch {
		case m == 15:
			return false, nil
		case m == 14:
			id := r.unpack(4)
			adv := 64
			switch {
			case id < 2:
				adv = 1
			case id < 8:
				adv = 4
			case id < 10:
				adv = 8
			case id < 12:
				adv = 16
			case id < 14:
				adv = 32
			}
			r.advance(adv)
		case m == 13:
			r.advance(5 + 8*int(r.unpack(4)))
		case m > 8:
			return false, ErrCorrupt
		}
		if m <= 8 {
			break
		}
	}
	st.submodeID = m
	st.decodeFrame(r, out)
	return true, nil
}

func (st *Decoder) decodeFrame(r *bitReader, out []float32) {
	const at = decExcAt
	b := st.excBuf[:]
	copy(b, b[frameSize:])
	sm := submodes[st.submodeID]
	if sm == nil {
		var lpc [lpcOrder]float32
		bwLPC(0.93, st.interpQLPC[:], lpc[:], lpcOrder)
		g := computeRMS(b[at:], frameSize)
		for i := 0; i < frameSize; i++ {
			b[at+i] = speexRand(g, &st.seed)
		}
		st.first = true
		iirMem16(b[at:], lpc[:], out, frameSize, lpcOrder, st.memSp[:])
		st.countLost = 0
		return
	}
	var qlsp [lpcOrder]float32
	sm.lspUnquant(qlsp[:], lpcOrder, r)
	if st.countLost != 0 {
		var dist float32
		for i := range qlsp {
			dist += abs32(st.oldQLSP[i] - qlsp[i])
		}
		fact := float32(mul64(.6, math.Exp(mul64(-.2, float64(dist)))))
		for i := range st.memSp {
			st.memSp[i] = mul(fact, st.memSp[i])
		}
	}
	if st.first || st.countLost != 0 {
		st.oldQLSP = qlsp
	}
	olPitch := 0
	var olPitchCoef float32
	if sm.lbrPitch != -1 {
		olPitch = pitchStart + int(r.unpack(7))
	}
	if sm.forcedPitchGain != 0 {
		olPitchCoef = float32(mul64(0.066667, float64(r.unpack(4))))
	}
	olGain := float32(math.Exp(float64(r.unpack(5)) / 3.5))
	if st.submodeID == 1 {
		st.dtx = r.unpack(4) == 15
	}
	if st.submodeID > 1 {
		st.dtx = false
	}
	bestPitch := 40
	var bestPitchGain, pitchAverage float32
	var innov, exc32 [subframe]float32
	var pitchGain [3]float32
	iabs := func(x int) int {
		if x < 0 {
			return -x
		}
		return x
	}
	for sub := 0; sub < nbSub; sub++ {
		offset := subframe * sub
		ex := b[at+offset : at+offset+subframe]
		clear(ex)
		pitMin, pitMax := pitchStart, pitchEnd
		if sm.lbrPitch != -1 {
			if margin := sm.lbrPitch; margin != 0 {
				pitMin, pitMax = max(olPitch-margin+1, pitchStart), min(olPitch+margin, pitchEnd)
			} else {
				pitMin, pitMax = olPitch, olPitch
			}
		}
		var pitch int
		sm.ltpUnquant(b, at+offset, exc32[:], pitMin, pitMax, olPitchCoef, sm.ltp, subframe, &pitch, pitchGain[:], r, st.countLost, offset, st.lastPitchGain)
		sanitize(exc32[:], -32000, 32000)
		side := func(g float32) float64 {
			if g > 0 {
				return float64(g)
			}
			return mul64(-.5, float64(g))
		}
		tmp := float32(float64(abs32(pitchGain[1])) + side(pitchGain[0]) + side(pitchGain[2]))
		pitchAverage += tmp
		bp := bestPitch
		if (tmp > bestPitchGain && iabs(2*bp-pitch) >= 3 && iabs(3*bp-pitch) >= 4 && iabs(4*bp-pitch) >= 5) ||
			(float64(tmp) > mul64(.6, float64(bestPitchGain)) && (iabs(bp-2*pitch) < 3 || iabs(bp-3*pitch) < 4 || iabs(bp-4*pitch) < 5)) ||
			(mul64(.67, float64(tmp)) > float64(bestPitchGain) && (iabs(2*bp-pitch) < 3 || iabs(3*bp-pitch) < 4 || iabs(4*bp-pitch) < 5)) {
			bestPitch = pitch
			if tmp > bestPitchGain {
				bestPitchGain = tmp
			}
		}
		innov = [subframe]float32{}
		var ener float32
		switch sm.haveSubframeGain {
		case 3:
			ener = mul(scal3[r.unpack(3)], olGain)
		case 1:
			ener = mul(scal1[r.unpack(1)], olGain)
		default:
			ener = olGain
		}
		sm.innovUnquant(innov[:], sm.innov, subframe, r, &st.seed)
		signalMul(innov[:], innov[:], ener, subframe)
		if sm.doubleCodebook {
			var innov2 [subframe]float32
			sm.innovUnquant(innov2[:], sm.innov, subframe, r, &st.seed)
			signalMul(innov2[:], innov2[:], mul(0.454545, ener), subframe)
			for i := range innov {
				innov[i] += innov2[i]
			}
		}
		for i := range ex {
			ex[i] = exc32[i] + innov[i]
		}
		if st.submodeID == 1 {
			g := mul(1.5, olPitchCoef-.2)
			if g < 0 {
				g = 0
			}
			if g > 1 {
				g = 1
			}
			clear(ex)
			for st.vocOffset < subframe {
				if st.vocOffset >= 0 {
					ex[st.vocOffset] = mul(float32(math.Sqrt(float64(2*olPitch))), mul(g, olGain))
				}
				st.vocOffset += olPitch
			}
			st.vocOffset -= subframe
			for i := range ex {
				exci := ex[i]
				ex[i] = (mul(.7, ex[i]) + mul(.3, st.vocM1)) + (mul(1-mul(.85, g), innov[i]) - mul(mul(.15, g), st.vocM2))
				st.vocM1 = exci
				st.vocM2 = innov[i]
				st.vocMean = mul(.8, st.vocMean) + mul(.2, ex[i])
				ex[i] -= st.vocMean
			}
		}
	}
	if sm.combGain > 0 && st.countLost == 0 {
		multicomb(b, at-subframe, out, 2*subframe, bestPitch, subframe, sm.combGain)
		multicomb(b, at+subframe, out[2*subframe:], 2*subframe, bestPitch, subframe, sm.combGain)
	} else {
		copy(out, b[at-subframe:at-subframe+frameSize])
	}
	if st.countLost != 0 {
		excEner := computeRMS(b[at:], frameSize)
		gain := olGain / (excEner + 1)
		if gain > 2 {
			gain = 2
		}
		for i := 0; i < frameSize; i++ {
			b[at+i] = mul(gain, b[at+i])
			out[i] = b[at+i-subframe]
		}
	}
	var iq, ak [lpcOrder]float32
	for sub := 0; sub < nbSub; sub++ {
		sp := out[subframe*sub:]
		lspInterpolate(st.oldQLSP[:], qlsp[:], iq[:], lpcOrder, sub, nbSub, .002)
		lspToLPC(iq[:], ak[:], lpcOrder)
		iirMem16(sp, st.interpQLPC[:], sp, subframe, lpcOrder, st.memSp[:])
		st.interpQLPC = ak
	}
	highpass(out, out, frameSize, 1, &st.memHp)
	st.oldQLSP = qlsp
	st.first = false
	st.countLost = 0
	st.lastPitch = bestPitch
	st.lastPitchGain = float32(mul64(.25, float64(pitchAverage)))
	st.pitchGainBuf[st.pitchGainIdx] = st.lastPitchGain
	st.pitchGainIdx = (st.pitchGainIdx + 1) % 3
}

func (st *Decoder) decodeLost(out []float32) {
	const at = decExcAt
	b := st.excBuf[:]
	fact := float32(0)
	if st.countLost < 10 {
		fact = attenuation[st.countLost]
	}
	gainMed := median3(st.pitchGainBuf[0], st.pitchGainBuf[1], st.pitchGainBuf[2])
	if gainMed < st.lastPitchGain {
		st.lastPitchGain = gainMed
	}
	pitchGain := st.lastPitchGain
	if float64(pitchGain) > .85 {
		pitchGain = .85
	}
	pitchGain = mul(fact, pitchGain) + 1e-15
	innovGain := computeRMS(b[at:], frameSize)
	noiseGain := mul(innovGain, mul(fact, 1-mul(pitchGain, pitchGain)))
	copy(b, b[frameSize:])
	pitchVal := st.lastPitch + int(speexRand(float32(1+st.countLost), &st.seed))
	pitchVal = min(max(pitchVal, pitchStart), pitchEnd)
	for i := 0; i < frameSize; i++ {
		b[at+i] = mul(pitchGain, b[at+i-pitchVal]+1e-15) + speexRand(noiseGain, &st.seed)
	}
	bwLPC(.98, st.interpQLPC[:], st.interpQLPC[:], lpcOrder)
	iirMem16(b[at-subframe:], st.interpQLPC[:], out, frameSize, lpcOrder, st.memSp[:])
	highpass(out, out, frameSize, 1, &st.memHp)
	st.first = false
	st.countLost++
	st.pitchGainBuf[st.pitchGainIdx] = pitchGain
	st.pitchGainIdx = (st.pitchGainIdx + 1) % 3
}

func median3(a, b, c float32) float32 {
	if a < b {
		if b < c {
			return b
		}
		if a < c {
			return c
		}
		return a
	}
	if c < b {
		return b
	}
	if c < a {
		return c
	}
	return a
}
