package ilbc

import "math"

// Translations of StateSearchW.c and iCBSearch.c.

// absQuantW: in is buf[inAt:], with the filter order's history before it; the weighting and
// synthesis filters are wd[wAt:] and sd[sAt:].
func absQuantW(enc *encInst, buf []float32, inAt int, sd []float32, sAt int, wd []float32, wAt int, out []int, length, stateFirst int) {
	var syntOutBuf [lpcFilterorder + stateShortLen30]float32
	const so = lpcFilterorder // syntOut is syntOutBuf[so:]
	if stateFirst != 0 {
		allPoleFilter(buf, inAt, wd[wAt:], subl, lpcFilterorder)
	} else {
		allPoleFilter(buf, inAt, wd[wAt:], enc.stateShortLen-subl, lpcFilterorder)
	}
	for n := 0; n < length; n++ {
		if (stateFirst != 0 && n == subl) || (stateFirst == 0 && n == enc.stateShortLen-subl) {
			sAt += lpcFilterorder + 1
			wAt += lpcFilterorder + 1
			allPoleFilter(buf, inAt+n, wd[wAt:], length-n, lpcFilterorder)
		}
		syntOutBuf[so+n] = 0.0
		allPoleFilter(syntOutBuf[:], so+n, wd[wAt:], 1, lpcFilterorder)
		toQ := buf[inAt+n] - syntOutBuf[so+n]
		var xq float32
		var index int
		sortSq(&xq, &index, toQ, state_sq3Tbl, 8)
		out[n] = index
		syntOutBuf[so+n] = state_sq3Tbl[out[n]]
		allPoleFilter(syntOutBuf[:], so+n, wd[wAt:], 1, lpcFilterorder)
	}
	_ = sAt
}

func stateSearchW(enc *encInst, residual []float32, sd []float32, sAt int, wd []float32, wAt int, idxForMax *int, idxVec []int, length, stateFirst int) {
	var tmpbuf, foutbuf [lpcFilterorder + 2*stateShortLen30]float32
	var numerator [1 + lpcFilterorder]float32
	syntDenum := sd[sAt:]
	for k := 0; k < lpcFilterorder; k++ {
		numerator[k] = syntDenum[lpcFilterorder-k]
	}
	numerator[lpcFilterorder] = syntDenum[0]
	copy(tmpbuf[lpcFilterorder:], residual[:length])
	zeroPoleFilter(tmpbuf[:], lpcFilterorder, numerator[:], syntDenum, 2*length, lpcFilterorder, foutbuf[:], lpcFilterorder)
	fout := foutbuf[lpcFilterorder:]
	for k := 0; k < length; k++ {
		fout[k] += fout[k+length]
	}
	maxVal := fout[0]
	for k := 1; k < length; k++ {
		if mul(fout[k], fout[k]) > mul(maxVal, maxVal) {
			maxVal = fout[k]
		}
	}
	maxVal = absf(maxVal)
	if maxVal < 10.0 {
		maxVal = 10.0
	}
	maxVal = float32(math.Log10(float64(maxVal)))
	var dtmp float32
	sortSq(&dtmp, idxForMax, maxVal, state_frgqTbl, 64)
	maxVal = state_frgqTbl[*idxForMax]
	qmax := float32(math.Pow(10, float64(maxVal)))
	scal := float32(4.5) / qmax
	for k := 0; k < length; k++ {
		fout[k] = mul(fout[k], scal)
	}
	absQuantW(enc, foutbuf[:], lpcFilterorder, sd, sAt, wd, wAt, idxVec, length, stateFirst)
}

func iCBSearch(enc *encInst, index, gainIndex []int, intarget []float32, mem []float32, lMem, lTarget, nStages int,
	weightDenum []float32, weightState []float32, block int) {
	var gains [cbNStages]float32
	var target [subl]float32
	var buf [cbMeml + subl + 2*lpcFilterorder]float32
	var invenergy, energy [cbExpand * 128]float32
	var cbvectors [cbMeml]float32
	var cvec, augVec [subl]float32
	baseSize := lMem - lTarget + 1
	if lTarget == subl {
		baseSize = lMem - lTarget + 1 + lTarget/2
	}
	copy(buf[:lpcFilterorder], weightState[:lpcFilterorder])
	copy(buf[lpcFilterorder:], mem[:lMem])
	copy(buf[lpcFilterorder+lMem:], intarget[:lTarget])
	allPoleFilter(buf[:], lpcFilterorder, weightDenum, lMem+lTarget, lpcFilterorder)
	copy(target[:lTarget], buf[lpcFilterorder+lMem:lpcFilterorder+lMem+lTarget])
	var tene float32
	for i := 0; i < lTarget; i++ {
		tene += mul(target[i], target[i])
	}
	filteredCBvecs(cbvectors[:], buf[lpcFilterorder:], lMem)
	const mb = lpcFilterorder // buf+LPC_FILTERORDER
	for stage := 0; stage < nStages; stage++ {
		rng := search_rangeTbl[block][stage]
		maxMeasure := float32(-10000000.0)
		var gain float32
		bestIndex := 0
		var crossDot float32
		pp := mb + lMem - lTarget
		for j := 0; j < lTarget; j++ {
			crossDot += mul(target[j], buf[pp])
			pp++
		}
		var measure float32
		ppe, ppi, ppo := 0, 0, 0
		if stage == 0 {
			ppi = mb + lMem - lTarget - 1
			ppo = mb + lMem - 1
			energy[0] = 0.0
			pp = mb + lMem - lTarget
			for j := 0; j < lTarget; j++ {
				energy[0] += mul(buf[pp], buf[pp])
				pp++
			}
			if energy[0] > 0.0 {
				invenergy[0] = float32(1.0) / (energy[0] + eps)
			} else {
				invenergy[0] = 0.0
			}
			ppe = 1
			measure = -10000000.0
			if crossDot > 0.0 {
				measure = mul(mul(crossDot, crossDot), invenergy[0])
			}
		} else {
			measure = mul(mul(crossDot, crossDot), invenergy[0])
		}
		ftmp := mul(crossDot, invenergy[0])
		if measure > maxMeasure && absf(ftmp) < cbMaxgain {
			bestIndex = 0
			maxMeasure = measure
			gain = ftmp
		}
		for icount := 1; icount < rng; icount++ {
			crossDot = 0.0
			pp = mb + lMem - lTarget - icount
			for j := 0; j < lTarget; j++ {
				crossDot += mul(target[j], buf[pp])
				pp++
			}
			if stage == 0 {
				energy[ppe] = energy[icount-1] + mul(buf[ppi], buf[ppi]) - mul(buf[ppo], buf[ppo])
				ppe++
				ppo--
				ppi--
				if energy[icount] > 0.0 {
					invenergy[icount] = float32(1.0) / (energy[icount] + eps)
				} else {
					invenergy[icount] = 0.0
				}
				measure = -10000000.0
				if crossDot > 0.0 {
					measure = mul(mul(crossDot, crossDot), invenergy[icount])
				}
			} else {
				measure = mul(mul(crossDot, crossDot), invenergy[icount])
			}
			ftmp = mul(crossDot, invenergy[icount])
			if measure > maxMeasure && absf(ftmp) < cbMaxgain {
				bestIndex = icount
				maxMeasure = measure
				gain = ftmp
			}
		}
		if lTarget == subl {
			searchAugmentedCB(20, 39, stage, baseSize-lTarget/2, target[:], buf[:], mb+lMem,
				&maxMeasure, &bestIndex, &gain, energy[:], invenergy[:])
		}
		baseIndex := bestIndex
		sIndAug, eIndAug := 0, 0
		sInd := baseIndex - cbResrange/2
		eInd := sInd + cbResrange
		if lTarget == subl {
			if sInd < 0 {
				sIndAug = 40 + sInd
				eIndAug = 39
				sInd = 0
			} else if baseIndex < baseSize-20 {
				if eInd > rng {
					sInd -= eInd - rng
					eInd = rng
				}
			} else {
				if sInd < baseSize-20 {
					sIndAug = 20
					sInd = 0
					eInd = 0
					eIndAug = 19 + cbResrange
					if eIndAug > 39 {
						eInd = eIndAug - 39
						eIndAug = 39
					}
				} else {
					sIndAug = 20 + sInd - (baseSize - 20)
					eIndAug = 39
					sInd = 0
					eInd = cbResrange - (eIndAug - sIndAug + 1)
				}
			}
		} else {
			if sInd < 0 {
				eInd -= sInd
				sInd = 0
			}
			if eInd > rng {
				sInd -= eInd - rng
				eInd = rng
			}
		}
		counter := sInd
		sInd += baseSize
		eInd += baseSize
		if stage == 0 {
			ppe = baseSize
			energy[ppe] = 0.0
			pp = lMem - lTarget
			for j := 0; j < lTarget; j++ {
				energy[ppe] += mul(cbvectors[pp], cbvectors[pp])
				pp++
			}
			ppi = lMem - 1 - lTarget
			ppo = lMem - 1
			for j := 0; j < rng-1; j++ {
				energy[ppe+1] = energy[ppe] + mul(cbvectors[ppi], cbvectors[ppi]) - mul(cbvectors[ppo], cbvectors[ppo])
				ppo--
				ppi--
				ppe++
			}
		}
		for icount := sInd; icount < eInd; icount++ {
			crossDot = 0.0
			pp = lMem - counter - lTarget
			counter++
			for j := 0; j < lTarget; j++ {
				crossDot += mul(target[j], cbvectors[pp])
				pp++
			}
			if energy[icount] > 0.0 {
				invenergy[icount] = float32(1.0) / (energy[icount] + eps)
			} else {
				invenergy[icount] = 0.0
			}
			if stage == 0 {
				measure = -10000000.0
				if crossDot > 0.0 {
					measure = mul(mul(crossDot, crossDot), invenergy[icount])
				}
			} else {
				measure = mul(mul(crossDot, crossDot), invenergy[icount])
			}
			ftmp = mul(crossDot, invenergy[icount])
			if measure > maxMeasure && absf(ftmp) < cbMaxgain {
				bestIndex = icount
				maxMeasure = measure
				gain = ftmp
			}
		}
		if lTarget == subl && sIndAug != 0 {
			searchAugmentedCB(sIndAug, eIndAug, stage, 2*baseSize-20, target[:], cbvectors[:], lMem,
				&maxMeasure, &bestIndex, &gain, energy[:], invenergy[:])
		}
		index[stage] = bestIndex
		if stage == 0 {
			if gain < 0.0 {
				gain = 0.0
			}
			if gain > cbMaxgain {
				gain = cbMaxgain
			}
			gain = gainquant(gain, 1.0, 32, &gainIndex[stage])
		} else if stage == 1 {
			gain = gainquant(gain, absf(gains[stage-1]), 16, &gainIndex[stage])
		} else {
			gain = gainquant(gain, absf(gains[stage-1]), 8, &gainIndex[stage])
		}
		// The chosen vector: in buf, in the filtered vectors, or an augmented one.
		var src []float32
		var at int
		if lTarget == stateLen-enc.stateShortLen {
			if index[stage] < baseSize {
				src, at = buf[:], mb+lMem-lTarget-index[stage]
			} else {
				src, at = cbvectors[:], lMem-lTarget-index[stage]+baseSize
			}
		} else {
			if index[stage] < baseSize {
				if index[stage] < baseSize-20 {
					src, at = buf[:], mb+lMem-lTarget-index[stage]
				} else {
					createAugmentedVec(index[stage]-baseSize+40, buf[:], mb+lMem, augVec[:])
					src, at = augVec[:], 0
				}
			} else {
				filterno := index[stage] / baseSize
				position := index[stage] - filterno*baseSize
				if position < baseSize-20 {
					src, at = cbvectors[:], filterno*lMem-lTarget-index[stage]+filterno*baseSize
				} else {
					createAugmentedVec(index[stage]-(filterno+1)*baseSize+40, cbvectors[:], filterno*lMem, augVec[:])
					src, at = augVec[:], 0
				}
			}
		}
		for j := 0; j < lTarget; j++ {
			cvec[j] += mul(gain, src[at+j])
			target[j] -= mul(gain, src[at+j])
		}
		gains[stage] = gain
	}
	var cene float32
	for i := 0; i < lTarget; i++ {
		cene += mul(cvec[i], cvec[i])
	}
	j := gainIndex[0]
	for i := gainIndex[0]; i < 32; i++ {
		ftmp := mul(mul(cene, gain_sq5Tbl[i]), gain_sq5Tbl[i])
		if ftmp < mul(mul(tene, gains[0]), gains[0]) && float64(gain_sq5Tbl[j]) < 2.0*float64(gains[0]) {
			j = i
		}
	}
	gainIndex[0] = j
}
