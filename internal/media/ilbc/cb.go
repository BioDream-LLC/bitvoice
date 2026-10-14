package ilbc

import "math"

// Translations of createCB.c, StateConstructW.c and iCBConstruct.c.

func filteredCBvecs(cbvectors []float32, mem []float32, lMem int) {
	var tempbuff2 [cbMeml + cbFilterlen]float32
	copy(tempbuff2[cbHalffilterlen-1:], mem[:lMem])
	for k := 0; k < lMem; k++ {
		pp := k
		pp1 := cbFilterlen - 1
		var o float32
		for j := 0; j < cbFilterlen; j++ {
			o += mul(tempbuff2[pp], cbfiltersTbl[pp1])
			pp++
			pp1--
		}
		cbvectors[k] = o
	}
}

// searchAugmentedCB: buffer[at] is the position the reference's buffer pointer points at.
func searchAugmentedCB(low, high, stage, startIndex int, target []float32, buffer []float32, at int,
	maxMeasure *float32, bestIndex *int, gain *float32, energy, invenergy []float32) {
	var nrjRecursive float32
	pp := at - low + 1
	for j := 0; j < low-5; j++ {
		nrjRecursive += mul(buffer[pp], buffer[pp])
		pp++
	}
	ppe := at - low
	for icount := low; icount <= high; icount++ {
		tmpIndex := startIndex + icount - 20
		ilow := icount - 4
		nrjRecursive = nrjRecursive + mul(buffer[ppe], buffer[ppe])
		ppe--
		energy[tmpIndex] = nrjRecursive
		var crossDot float32
		pp = at - icount
		for j := 0; j < ilow; j++ {
			crossDot += mul(target[j], buffer[pp])
			pp++
		}
		alfa := float32(0.2)
		ppo := at - 4
		ppi := at - icount - 4
		for j := ilow; j < icount; j++ {
			weighted := mul(float32(1.0)-alfa, buffer[ppo]) + mul(alfa, buffer[ppi])
			ppo++
			ppi++
			energy[tmpIndex] += mul(weighted, weighted)
			crossDot += mul(target[j], weighted)
			alfa += 0.2
		}
		pp = at - icount
		for j := icount; j < subl; j++ {
			energy[tmpIndex] += mul(buffer[pp], buffer[pp])
			crossDot += mul(target[j], buffer[pp])
			pp++
		}
		if energy[tmpIndex] > 0.0 {
			invenergy[tmpIndex] = float32(1.0) / (energy[tmpIndex] + eps)
		} else {
			invenergy[tmpIndex] = 0.0
		}
		var measure float32
		if stage == 0 {
			measure = -10000000.0
			if crossDot > 0.0 {
				measure = mul(mul(crossDot, crossDot), invenergy[tmpIndex])
			}
		} else {
			measure = mul(mul(crossDot, crossDot), invenergy[tmpIndex])
		}
		ftmp := mul(crossDot, invenergy[tmpIndex])
		if measure > *maxMeasure && absf(ftmp) < cbMaxgain {
			*bestIndex = tmpIndex
			*maxMeasure = measure
			*gain = ftmp
		}
	}
}

// createAugmentedVec: buffer[at] is the reference's buffer pointer.
func createAugmentedVec(index int, buffer []float32, at int, cbVec []float32) {
	ilow := index - 5
	copy(cbVec[:index], buffer[at-index:at])
	alfa1 := float32(0.2)
	var alfa float32
	ppo := at - 5
	ppi := at - index - 5
	for j := ilow; j < index; j++ {
		cbVec[j] = mul(float32(1.0)-alfa, buffer[ppo]) + mul(alfa, buffer[ppi])
		ppo++
		ppi++
		alfa += alfa1
	}
	copy(cbVec[index:subl], buffer[at-index:at-index+subl-index])
}

func stateConstructW(idxForMax int, idxVec []int, syntDenum []float32, out []float32, length int) {
	var tmpbuf, foutbuf [lpcFilterorder + 2*stateLen]float32
	var numerator [lpcFilterorder + 1]float32
	maxVal := state_frgqTbl[idxForMax]
	maxVal = float32(math.Pow(10, float64(maxVal))) / float32(4.5)
	for k := 0; k < lpcFilterorder; k++ {
		numerator[k] = syntDenum[lpcFilterorder-k]
	}
	numerator[lpcFilterorder] = syntDenum[0]
	tmp := tmpbuf[:]
	for k := 0; k < length; k++ {
		tmp[lpcFilterorder+k] = mul(maxVal, state_sq3Tbl[idxVec[length-1-k]])
	}
	zeroPoleFilter(tmp, lpcFilterorder, numerator[:], syntDenum, 2*length, lpcFilterorder, foutbuf[:], lpcFilterorder)
	fout := foutbuf[lpcFilterorder:]
	for k := 0; k < length; k++ {
		out[k] = fout[length-1-k] + fout[2*length-1-k]
	}
}

func indexConvEnc(index []int) {
	for k := 1; k < cbNStages; k++ {
		if index[k] >= 108 && index[k] < 172 {
			index[k] -= 64
		} else if index[k] >= 236 {
			index[k] -= 128
		}
	}
}

func indexConvDec(index []int) {
	for k := 1; k < cbNStages; k++ {
		if index[k] >= 44 && index[k] < 108 {
			index[k] += 64
		} else if index[k] >= 108 && index[k] < 128 {
			index[k] += 128
		}
	}
}

func iCBConstruct(decvector []float32, index, gainIndex []int, mem []float32, lMem, veclen, nStages int) {
	var gain [cbNStages]float32
	var cbvec [subl]float32
	gain[0] = gaindequant(gainIndex[0], 1.0, 32)
	if nStages > 1 {
		gain[1] = gaindequant(gainIndex[1], absf(gain[0]), 16)
	}
	if nStages > 2 {
		gain[2] = gaindequant(gainIndex[2], absf(gain[1]), 8)
	}
	getCBvec(cbvec[:], mem, index[0], lMem, veclen)
	for j := 0; j < veclen; j++ {
		decvector[j] = mul(gain[0], cbvec[j])
	}
	for k := 1; k < nStages; k++ {
		getCBvec(cbvec[:], mem, index[k], lMem, veclen)
		for j := 0; j < veclen; j++ {
			decvector[j] += mul(gain[k], cbvec[j])
		}
	}
}
