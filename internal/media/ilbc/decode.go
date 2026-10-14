package ilbc

import "errors"

// Translation of iLBC_decode.c, and the public interface.

func initDecode(dec *decInst, mode, useEnhancer int) int {
	*dec = decInst{mode: mode}
	if mode == 30 {
		dec.blockl, dec.nsub, dec.nasub, dec.lpcN = 240, 6, 4, 2
		dec.noOfBytes, dec.noOfWords, dec.stateShortLen = noOfBytes30, noOfWords30, stateShortLen30
		dec.ulpInst = &ulp30msTbl
	} else {
		dec.blockl, dec.nsub, dec.nasub, dec.lpcN = 160, 4, 2, 1
		dec.noOfBytes, dec.noOfWords, dec.stateShortLen = noOfBytes20, noOfWords20, stateShortLen20
		dec.ulpInst = &ulp20msTbl
	}
	copy(dec.lsfdeqold[:], lsfmeanTbl)
	for i := 0; i < nsubMax; i++ {
		dec.oldSyntdenum[i*(lpcFilterorder+1)] = 1.0
	}
	dec.lastLag = 20
	dec.prevLag = 120
	dec.prevLpc[0] = 1.0
	dec.seed = 777
	dec.useEnhancer = useEnhancer
	for i := range dec.enhPeriod {
		dec.enhPeriod[i] = 40.0
	}
	return dec.blockl
}

func decodeResidual(dec *decInst, decresidual []float32, start, idxForMax int, idxVec []int, syntdenum []float32,
	cbIndex, gainIndex, extraCbIndex, extraGainIndex []int, stateFirst int) {
	var reverseDecresidual [blocklMax]float32
	var mem [cbMeml]float32
	const lo = lpcFilterorder + 1
	diff := stateLen - dec.stateShortLen
	startPos := (start-1)*subl + diff
	if stateFirst == 1 {
		startPos = (start - 1) * subl
	}
	stateConstructW(idxForMax, idxVec, syntdenum[(start-1)*lo:], decresidual[startPos:], dec.stateShortLen)
	if stateFirst != 0 {
		copy(mem[cbMeml-dec.stateShortLen:], decresidual[startPos:startPos+dec.stateShortLen])
		iCBConstruct(decresidual[startPos+dec.stateShortLen:], extraCbIndex, extraGainIndex, mem[cbMeml-stMemLTbl:], stMemLTbl, diff, cbNStages)
	} else {
		for k := 0; k < diff; k++ {
			reverseDecresidual[k] = decresidual[(start+1)*subl-1-(k+dec.stateShortLen)]
		}
		k := 0
		for ; k < dec.stateShortLen; k++ {
			mem[cbMeml-1-k] = decresidual[startPos+k]
		}
		clear(mem[:cbMeml-k])
		iCBConstruct(reverseDecresidual[:], extraCbIndex, extraGainIndex, mem[cbMeml-stMemLTbl:], stMemLTbl, diff, cbNStages)
		for k := 0; k < diff; k++ {
			decresidual[startPos-1-k] = reverseDecresidual[k]
		}
	}
	subcount := 0
	if nFor := dec.nsub - start - 1; nFor > 0 {
		clear(mem[:cbMeml-stateLen])
		copy(mem[cbMeml-stateLen:], decresidual[(start-1)*subl:(start-1)*subl+stateLen])
		for subframe := 0; subframe < nFor; subframe++ {
			ml := memLfTbl[subcount]
			iCBConstruct(decresidual[(start+1+subframe)*subl:], cbIndex[subcount*cbNStages:], gainIndex[subcount*cbNStages:],
				mem[cbMeml-ml:], ml, subl, cbNStages)
			copy(mem[:cbMeml-subl], mem[subl:])
			copy(mem[cbMeml-subl:], decresidual[(start+1+subframe)*subl:(start+2+subframe)*subl])
			subcount++
		}
	}
	if nBack := start - 1; nBack > 0 {
		memlGotten := subl * (dec.nsub + 1 - start)
		if memlGotten > cbMeml {
			memlGotten = cbMeml
		}
		k := 0
		for ; k < memlGotten; k++ {
			mem[cbMeml-1-k] = decresidual[(start-1)*subl+k]
		}
		clear(mem[:cbMeml-k])
		for subframe := 0; subframe < nBack; subframe++ {
			ml := memLfTbl[subcount]
			iCBConstruct(reverseDecresidual[subframe*subl:], cbIndex[subcount*cbNStages:], gainIndex[subcount*cbNStages:],
				mem[cbMeml-ml:], ml, subl, cbNStages)
			copy(mem[:cbMeml-subl], mem[subl:])
			copy(mem[cbMeml-subl:], reverseDecresidual[subframe*subl:(subframe+1)*subl])
			subcount++
		}
		for i := 0; i < subl*nBack; i++ {
			decresidual[subl*nBack-i-1] = reverseDecresidual[i]
		}
	}
}

func unpackCombine(s *bitstream, v *int, bits [ulpClasses + 2]int, ulp int) {
	var lastpart int
	s.unpack(&lastpart, bits[ulp])
	packcombine(v, lastpart, bits[ulp])
}

// iLBCDecode decodes one frame; mode 0 conceals a lost one.
func iLBCDecode(decblock []float32, bytes []byte, dec *decInst, mode int) {
	var data, plcResidual, zeros, decresidual [blocklMax]float32
	var lsfdeq [lpcFilterorder * lpcNMax]float32
	var plcLpc, one [lpcFilterorder + 1]float32
	var idxVec [stateLen]int
	var gainIndex, cbIndex [nasubMax * cbNStages]int
	var extraGainIndex, extraCbIndex [cbNStages]int
	var lsfI [lsfNsplit * lpcNMax]int
	var weightdenum, syntdenum [(lpcFilterorder + 1) * nsubMax]float32
	const lo = lpcFilterorder + 1
	if mode > 0 {
		s := &bitstream{b: bytes}
		var start, stateFirst, idxForMax, lastBit int
		u := dec.ulpInst
		for ulp := 0; ulp < 3; ulp++ {
			for k := 0; k < lsfNsplit*dec.lpcN; k++ {
				unpackCombine(s, &lsfI[k], u.lsfBits[k], ulp)
			}
			unpackCombine(s, &start, u.startBits, ulp)
			unpackCombine(s, &stateFirst, u.startfirstBits, ulp)
			unpackCombine(s, &idxForMax, u.scaleBits, ulp)
			for k := 0; k < dec.stateShortLen; k++ {
				unpackCombine(s, &idxVec[k], u.stateBits, ulp)
			}
			for k := 0; k < cbNStages; k++ {
				unpackCombine(s, &extraCbIndex[k], u.extraCbIndex[k], ulp)
			}
			for k := 0; k < cbNStages; k++ {
				unpackCombine(s, &extraGainIndex[k], u.extraCbGain[k], ulp)
			}
			for i := 0; i < dec.nasub; i++ {
				for k := 0; k < cbNStages; k++ {
					unpackCombine(s, &cbIndex[i*cbNStages+k], u.cbIndex[i][k], ulp)
				}
			}
			for i := 0; i < dec.nasub; i++ {
				for k := 0; k < cbNStages; k++ {
					unpackCombine(s, &gainIndex[i*cbNStages+k], u.cbGain[i][k], ulp)
				}
			}
		}
		s.unpack(&lastBit, 1)
		if start < 1 || (dec.mode == 20 && start > 3) || (dec.mode == 30 && start > 5) || lastBit == 1 {
			mode = 0
		}
		if mode == 1 {
			indexConvDec(cbIndex[:])
			simplelsfDEQ(lsfdeq[:], lsfI[:], dec.lpcN)
			lsfCheck(lsfdeq[:], lpcFilterorder, dec.lpcN)
			decoderInterpolateLSF(syntdenum[:], weightdenum[:], lsfdeq[:], lpcFilterorder, dec)
			decodeResidual(dec, decresidual[:], start, idxForMax, idxVec[:], syntdenum[:], cbIndex[:], gainIndex[:],
				extraCbIndex[:], extraGainIndex[:], stateFirst)
			doThePLC(plcResidual[:], plcLpc[:], 0, decresidual[:], syntdenum[lo*(dec.nsub-1):], dec.lastLag, dec)
			copy(decresidual[:dec.blockl], plcResidual[:dec.blockl])
		}
	}
	if mode == 0 {
		one[0] = 1
		doThePLC(plcResidual[:], plcLpc[:], 1, zeros[:], one[:], dec.lastLag, dec)
		copy(decresidual[:dec.blockl], plcResidual[:dec.blockl])
		for i := 0; i < dec.nsub; i++ {
			copy(syntdenum[i*lo:(i+1)*lo], plcLpc[:])
		}
	}
	if dec.useEnhancer == 1 {
		dec.lastLag = enhancerInterface(data[:], decresidual[:], dec)
		if dec.mode == 20 {
			syntFilter(data[0:], dec.oldSyntdenum[(dec.nsub-1)*lo:], subl, dec.syntMem[:])
			for i := 1; i < dec.nsub; i++ {
				syntFilter(data[i*subl:], syntdenum[(i-1)*lo:], subl, dec.syntMem[:])
			}
		} else {
			for i := 0; i < 2; i++ {
				syntFilter(data[i*subl:], dec.oldSyntdenum[(i+dec.nsub-2)*lo:], subl, dec.syntMem[:])
			}
			for i := 2; i < dec.nsub; i++ {
				syntFilter(data[i*subl:], syntdenum[(i-2)*lo:], subl, dec.syntMem[:])
			}
		}
	} else {
		lag := 20
		maxcc := xCorrCoef(decresidual[blocklMax-enhBlockl:], decresidual[blocklMax-enhBlockl-lag:], enhBlockl)
		for ilag := 21; ilag < 120; ilag++ {
			if cc := xCorrCoef(decresidual[blocklMax-enhBlockl:], decresidual[blocklMax-enhBlockl-ilag:], enhBlockl); cc > maxcc {
				maxcc = cc
				lag = ilag
			}
		}
		dec.lastLag = lag
		copy(data[:dec.blockl], decresidual[:dec.blockl])
		for i := 0; i < dec.nsub; i++ {
			syntFilter(data[i*subl:], syntdenum[i*lo:], subl, dec.syntMem[:])
		}
	}
	hpOutput(data[:], dec.blockl, decblock, dec.hpomem[:])
	copy(dec.oldSyntdenum[:dec.nsub*lo], syntdenum[:dec.nsub*lo])
	dec.prevEnhPl = 0
	if mode == 0 {
		dec.prevEnhPl = 1
	}
}

// Encoder encodes 8 kHz audio into iLBC frames: 160 samples into 38 bytes (20 ms mode) or 240 into
// 50 (30 ms mode).
type Encoder struct{ st encInst }

// Decoder decodes them.
type Decoder struct{ st decInst }

// ErrMode is a frame mode other than 20 and 30 ms.
var ErrMode = errors.New("ilbc: the mode is 20 or 30 (ms)")

// NewEncoder makes an encoder for 20 or 30 ms frames.
func NewEncoder(mode int) (*Encoder, error) {
	if mode != 20 && mode != 30 {
		return nil, ErrMode
	}
	e := &Encoder{}
	initEncode(&e.st, mode)
	return e, nil
}

// NewDecoder makes a decoder; the enhancer improves the sound at 40 or 80 samples' delay.
func NewDecoder(mode int, enhancer bool) (*Decoder, error) {
	if mode != 20 && mode != 30 {
		return nil, ErrMode
	}
	d := &Decoder{}
	use := 0
	if enhancer {
		use = 1
	}
	initDecode(&d.st, mode, use)
	return d, nil
}

// FrameSamples and FrameBytes are one frame's sizes.
func (e *Encoder) FrameSamples() int { return e.st.blockl }
func (e *Encoder) FrameBytes() int   { return e.st.noOfBytes }
func (d *Decoder) FrameSamples() int { return d.st.blockl }
func (d *Decoder) FrameBytes() int   { return d.st.noOfBytes }

// Encode encodes one frame of pcm (FrameSamples long) into out (FrameBytes long).
func (e *Encoder) Encode(out []byte, pcm []int16) {
	var block [blocklMax]float32
	for k := 0; k < e.st.blockl; k++ {
		block[k] = float32(pcm[k])
	}
	iLBCEncode(out[:e.st.noOfBytes], block[:], &e.st)
}

// Decode decodes one frame into out (FrameSamples long); a nil frame conceals a lost one.
func (d *Decoder) Decode(out []int16, frame []byte) {
	var decblock [blocklMax]float32
	mode := 1
	if len(frame) < d.st.noOfBytes {
		mode, frame = 0, make([]byte, d.st.noOfBytes)
	}
	iLBCDecode(decblock[:], frame, &d.st, mode)
	for k := 0; k < d.st.blockl; k++ {
		v := decblock[k]
		if v < minSample {
			v = minSample
		} else if v > maxSample {
			v = maxSample
		}
		out[k] = int16(v)
	}
}
