package ilbc

// Translation of iLBC_encode.c.

func initEncode(enc *encInst, mode int) int {
	*enc = encInst{mode: mode}
	if mode == 30 {
		enc.blockl, enc.nsub, enc.nasub, enc.lpcN = 240, 6, 4, 2
		enc.noOfBytes, enc.noOfWords, enc.stateShortLen = noOfBytes30, noOfWords30, stateShortLen30
		enc.ulpInst = &ulp30msTbl
	} else {
		enc.blockl, enc.nsub, enc.nasub, enc.lpcN = 160, 4, 2, 1
		enc.noOfBytes, enc.noOfWords, enc.stateShortLen = noOfBytes20, noOfWords20, stateShortLen20
		enc.ulpInst = &ulp20msTbl
	}
	copy(enc.lsfold[:], lsfmeanTbl)
	copy(enc.lsfdeqold[:], lsfmeanTbl)
	return enc.noOfBytes
}

// splitPack packs the part of an index that belongs to one protection class.
func splitPack(s *bitstream, v *int, bits [ulpClasses + 2]int, ulp int) {
	var firstpart int
	packsplit(v, &firstpart, v, bits[ulp], bits[ulp]+bits[ulp+1]+bits[ulp+2])
	s.dopack(firstpart, bits[ulp])
}

func iLBCEncode(bytes []byte, block []float32, enc *encInst) {
	var data, residual, reverseResidual, reverseDecresidual, decresidual [blocklMax]float32
	var mem [cbMeml]float32
	var idxVec [stateLen]int
	var gainIndex, cbIndex [cbNStages * nasubMax]int
	var extraGainIndex, extraCbIndex [cbNStages]int
	var lsfI [lsfNsplit * lpcNMax]int
	var weightState [lpcFilterorder]float32
	var syntdenum, weightdenum [nsubMax * (lpcFilterorder + 1)]float32
	const lo = lpcFilterorder + 1

	hpInput(block, enc.blockl, data[:], enc.hpimem[:])
	lpcEncode(syntdenum[:], weightdenum[:], lsfI[:], data[:], enc)
	for n := 0; n < enc.nsub; n++ {
		anaFilter(data[n*subl:], syntdenum[n*lo:], subl, residual[n*subl:], enc.anaMem[:])
	}
	start := frameClassify(enc, residual[:])
	diff := stateLen - enc.stateShortLen
	var en1, en2 float32
	index := (start - 1) * subl
	for i := 0; i < enc.stateShortLen; i++ {
		en1 += mul(residual[index+i], residual[index+i])
	}
	index = (start-1)*subl + diff
	for i := 0; i < enc.stateShortLen; i++ {
		en2 += mul(residual[index+i], residual[index+i])
	}
	stateFirst, startPos := 0, (start-1)*subl+diff
	if en1 > en2 {
		stateFirst, startPos = 1, (start-1)*subl
	}
	var idxForMax int
	stateSearchW(enc, residual[startPos:], syntdenum[:], (start-1)*lo, weightdenum[:], (start-1)*lo,
		&idxForMax, idxVec[:], enc.stateShortLen, stateFirst)
	stateConstructW(idxForMax, idxVec[:], syntdenum[(start-1)*lo:], decresidual[startPos:], enc.stateShortLen)
	if stateFirst != 0 {
		clear(mem[:cbMeml-enc.stateShortLen])
		copy(mem[cbMeml-enc.stateShortLen:], decresidual[startPos:startPos+enc.stateShortLen])
		clear(weightState[:])
		iCBSearch(enc, extraCbIndex[:], extraGainIndex[:], residual[startPos+enc.stateShortLen:],
			mem[cbMeml-stMemLTbl:], stMemLTbl, diff, cbNStages, weightdenum[start*lo:], weightState[:], 0)
		iCBConstruct(decresidual[startPos+enc.stateShortLen:], extraCbIndex[:], extraGainIndex[:],
			mem[cbMeml-stMemLTbl:], stMemLTbl, diff, cbNStages)
	} else {
		for k := 0; k < diff; k++ {
			reverseResidual[k] = residual[(start+1)*subl-1-(k+enc.stateShortLen)]
		}
		memlGotten := enc.stateShortLen
		k := 0
		for ; k < memlGotten; k++ {
			mem[cbMeml-1-k] = decresidual[startPos+k]
		}
		clear(mem[:cbMeml-k])
		clear(weightState[:])
		iCBSearch(enc, extraCbIndex[:], extraGainIndex[:], reverseResidual[:], mem[cbMeml-stMemLTbl:], stMemLTbl,
			diff, cbNStages, weightdenum[(start-1)*lo:], weightState[:], 0)
		iCBConstruct(reverseDecresidual[:], extraCbIndex[:], extraGainIndex[:], mem[cbMeml-stMemLTbl:], stMemLTbl,
			diff, cbNStages)
		for k := 0; k < diff; k++ {
			decresidual[startPos-1-k] = reverseDecresidual[k]
		}
	}
	subcount := 0
	if nFor := enc.nsub - start - 1; nFor > 0 {
		clear(mem[:cbMeml-stateLen])
		copy(mem[cbMeml-stateLen:], decresidual[(start-1)*subl:(start-1)*subl+stateLen])
		clear(weightState[:])
		for subframe := 0; subframe < nFor; subframe++ {
			ml := memLfTbl[subcount]
			iCBSearch(enc, cbIndex[subcount*cbNStages:], gainIndex[subcount*cbNStages:], residual[(start+1+subframe)*subl:],
				mem[cbMeml-ml:], ml, subl, cbNStages, weightdenum[(start+1+subframe)*lo:], weightState[:], subcount+1)
			iCBConstruct(decresidual[(start+1+subframe)*subl:], cbIndex[subcount*cbNStages:], gainIndex[subcount*cbNStages:],
				mem[cbMeml-ml:], ml, subl, cbNStages)
			copy(mem[:cbMeml-subl], mem[subl:])
			copy(mem[cbMeml-subl:], decresidual[(start+1+subframe)*subl:(start+2+subframe)*subl])
			clear(weightState[:])
			subcount++
		}
	}
	if nBack := start - 1; nBack > 0 {
		for n := 0; n < nBack; n++ {
			for k := 0; k < subl; k++ {
				reverseResidual[n*subl+k] = residual[(start-1)*subl-1-n*subl-k]
				reverseDecresidual[n*subl+k] = decresidual[(start-1)*subl-1-n*subl-k]
			}
		}
		memlGotten := subl * (enc.nsub + 1 - start)
		if memlGotten > cbMeml {
			memlGotten = cbMeml
		}
		k := 0
		for ; k < memlGotten; k++ {
			mem[cbMeml-1-k] = decresidual[(start-1)*subl+k]
		}
		clear(mem[:cbMeml-k])
		clear(weightState[:])
		for subframe := 0; subframe < nBack; subframe++ {
			ml := memLfTbl[subcount]
			iCBSearch(enc, cbIndex[subcount*cbNStages:], gainIndex[subcount*cbNStages:], reverseResidual[subframe*subl:],
				mem[cbMeml-ml:], ml, subl, cbNStages, weightdenum[(start-2-subframe)*lo:], weightState[:], subcount+1)
			iCBConstruct(reverseDecresidual[subframe*subl:], cbIndex[subcount*cbNStages:], gainIndex[subcount*cbNStages:],
				mem[cbMeml-ml:], ml, subl, cbNStages)
			copy(mem[:cbMeml-subl], mem[subl:])
			copy(mem[cbMeml-subl:], reverseDecresidual[subframe*subl:(subframe+1)*subl])
			clear(weightState[:])
			subcount++
		}
		for i := 0; i < subl*nBack; i++ {
			decresidual[subl*nBack-i-1] = reverseDecresidual[i]
		}
	}
	indexConvEnc(cbIndex[:])
	s := &bitstream{b: bytes}
	u := enc.ulpInst
	for ulp := 0; ulp < 3; ulp++ {
		for k := 0; k < lsfNsplit*enc.lpcN; k++ {
			splitPack(s, &lsfI[k], u.lsfBits[k], ulp)
		}
		splitPack(s, &start, u.startBits, ulp)
		splitPack(s, &stateFirst, u.startfirstBits, ulp)
		splitPack(s, &idxForMax, u.scaleBits, ulp)
		for k := 0; k < enc.stateShortLen; k++ {
			splitPack(s, &idxVec[k], u.stateBits, ulp)
		}
		for k := 0; k < cbNStages; k++ {
			splitPack(s, &extraCbIndex[k], u.extraCbIndex[k], ulp)
		}
		for k := 0; k < cbNStages; k++ {
			splitPack(s, &extraGainIndex[k], u.extraCbGain[k], ulp)
		}
		for i := 0; i < enc.nasub; i++ {
			for k := 0; k < cbNStages; k++ {
				splitPack(s, &cbIndex[i*cbNStages+k], u.cbIndex[i][k], ulp)
			}
		}
		for i := 0; i < enc.nasub; i++ {
			for k := 0; k < cbNStages; k++ {
				splitPack(s, &gainIndex[i*cbNStages+k], u.cbGain[i][k], ulp)
			}
		}
	}
	s.dopack(0, 1)
}
