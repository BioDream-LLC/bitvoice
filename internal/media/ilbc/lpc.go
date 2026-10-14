package ilbc

// Translations of LPCencode.c, LPCdecode.c and FrameClassify.c.

func simpleAnalysis(lsf []float32, data []float32, enc *encInst) {
	var temp [blocklMax]float32
	var lp, lp2, r [lpcFilterorder + 1]float32
	is := lpcLookback + blocklMax - enc.blockl
	copy(enc.lpcBuffer[is:], data[:enc.blockl])
	for k := 0; k < enc.lpcN; k++ {
		is = lpcLookback
		if k < enc.lpcN-1 {
			window(temp[:], lpc_winTbl, enc.lpcBuffer[:], blocklMax)
		} else {
			window(temp[:], lpc_asymwinTbl, enc.lpcBuffer[is:], blocklMax)
		}
		autocorr(r[:], temp[:], blocklMax, lpcFilterorder)
		window(r[:], r[:], lpc_lagwinTbl, lpcFilterorder+1)
		levdurb(lp[:], temp[:], r[:], lpcFilterorder)
		bwexpand(lp2[:], lp[:], lpcChirpSyntdenum, lpcFilterorder+1)
		a2lsf(lsf[k*lpcFilterorder:], lp2[:])
	}
	is = lpcLookback + blocklMax - enc.blockl
	copy(enc.lpcBuffer[:is], enc.lpcBuffer[lpcLookback+blocklMax-is:lpcLookback+blocklMax])
}

func lsfInterpolate2a(a []float32, lsf1, lsf2 []float32, coef float32, length int) {
	var lsftmp [lpcFilterorder]float32
	interpolate(lsftmp[:], lsf1, lsf2, coef, length)
	lsf2a(a, lsftmp[:])
}

func simpleInterpolateLSF(syntdenum, weightdenum, lsf, lsfdeq, lsfold, lsfdeqold []float32, length int, enc *encInst) {
	var lp [lpcFilterorder + 1]float32
	lsf2 := lsf[length:]
	lsfdeq2 := lsfdeq[length:]
	lpLength := length + 1
	if enc.mode == 30 {
		lsfInterpolate2a(lp[:], lsfdeqold, lsfdeq, lsf_weightTbl_30ms[0], length)
		copy(syntdenum[:lpLength], lp[:lpLength])
		lsfInterpolate2a(lp[:], lsfold, lsf, lsf_weightTbl_30ms[0], length)
		bwexpand(weightdenum, lp[:], lpcChirpWeightdenum, lpLength)
		pos := lpLength
		for i := 1; i < enc.nsub; i++ {
			lsfInterpolate2a(lp[:], lsfdeq, lsfdeq2, lsf_weightTbl_30ms[i], length)
			copy(syntdenum[pos:pos+lpLength], lp[:lpLength])
			lsfInterpolate2a(lp[:], lsf, lsf2, lsf_weightTbl_30ms[i], length)
			bwexpand(weightdenum[pos:], lp[:], lpcChirpWeightdenum, lpLength)
			pos += lpLength
		}
		copy(lsfold[:length], lsf2[:length])
		copy(lsfdeqold[:length], lsfdeq2[:length])
		return
	}
	pos := 0
	for i := 0; i < enc.nsub; i++ {
		lsfInterpolate2a(lp[:], lsfdeqold, lsfdeq, lsf_weightTbl_20ms[i], length)
		copy(syntdenum[pos:pos+lpLength], lp[:lpLength])
		lsfInterpolate2a(lp[:], lsfold, lsf, lsf_weightTbl_20ms[i], length)
		bwexpand(weightdenum[pos:], lp[:], lpcChirpWeightdenum, lpLength)
		pos += lpLength
	}
	copy(lsfold[:length], lsf[:length])
	copy(lsfdeqold[:length], lsfdeq[:length])
}

func simplelsfQ(lsfdeq []float32, index []int, lsf []float32, lpcN int) {
	splitVQ(lsfdeq, index, lsf, lsfCbTbl, lsfNsplit, dim_lsfCbTbl, size_lsfCbTbl)
	if lpcN == 2 {
		splitVQ(lsfdeq[lpcFilterorder:], index[lsfNsplit:], lsf[lpcFilterorder:], lsfCbTbl, lsfNsplit, dim_lsfCbTbl, size_lsfCbTbl)
	}
}

func lpcEncode(syntdenum, weightdenum []float32, lsfIndex []int, data []float32, enc *encInst) {
	var lsf, lsfdeq [lpcFilterorder * lpcNMax]float32
	simpleAnalysis(lsf[:], data, enc)
	simplelsfQ(lsfdeq[:], lsfIndex, lsf[:], enc.lpcN)
	lsfCheck(lsfdeq[:], lpcFilterorder, enc.lpcN)
	simpleInterpolateLSF(syntdenum, weightdenum, lsf[:], lsfdeq[:], enc.lsfold[:], enc.lsfdeqold[:], lpcFilterorder, enc)
}

func simplelsfDEQ(lsfdeq []float32, index []int, lpcN int) {
	for half := 0; half < lpcN && half < 2; half++ {
		pos, cbPos := 0, 0
		for i := 0; i < lsfNsplit; i++ {
			for j := 0; j < dim_lsfCbTbl[i]; j++ {
				lsfdeq[half*lpcFilterorder+pos+j] = lsfCbTbl[cbPos+index[half*lsfNsplit+i]*dim_lsfCbTbl[i]+j]
			}
			pos += dim_lsfCbTbl[i]
			cbPos += size_lsfCbTbl[i] * dim_lsfCbTbl[i]
		}
	}
}

func decoderInterpolateLSF(syntdenum, weightdenum, lsfdeq []float32, length int, dec *decInst) {
	var lp [lpcFilterorder + 1]float32
	lsfdeq2 := lsfdeq[length:]
	lpLength := length + 1
	if dec.mode == 30 {
		lsfInterpolate2a(lp[:], dec.lsfdeqold[:], lsfdeq, lsf_weightTbl_30ms[0], length)
		copy(syntdenum[:lpLength], lp[:lpLength])
		bwexpand(weightdenum, lp[:], lpcChirpWeightdenum, lpLength)
		pos := lpLength
		for i := 1; i < 6; i++ {
			lsfInterpolate2a(lp[:], lsfdeq, lsfdeq2, lsf_weightTbl_30ms[i], length)
			copy(syntdenum[pos:pos+lpLength], lp[:lpLength])
			bwexpand(weightdenum[pos:], lp[:], lpcChirpWeightdenum, lpLength)
			pos += lpLength
		}
		copy(dec.lsfdeqold[:length], lsfdeq2[:length])
		return
	}
	pos := 0
	for i := 0; i < dec.nsub; i++ {
		lsfInterpolate2a(lp[:], dec.lsfdeqold[:], lsfdeq, lsf_weightTbl_20ms[i], length)
		copy(syntdenum[pos:pos+lpLength], lp[:lpLength])
		bwexpand(weightdenum[pos:], lp[:], lpcChirpWeightdenum, lpLength)
		pos += lpLength
	}
	copy(dec.lsfdeqold[:length], lsfdeq[:length])
}

func frameClassify(enc *encInst, residual []float32) int {
	var fssqEn, bssqEn [nsubMax]float32
	ssqEnWin := [nsubMax - 1]float32{0.8, 0.9, 1.0, 0.9, 0.8}
	sampEnWin := [5]float32{float32(1.0) / float32(6.0), float32(2.0) / float32(6.0), float32(3.0) / float32(6.0),
		float32(4.0) / float32(6.0), float32(5.0) / float32(6.0)}
	pp := 0
	for l := 0; l < 5; l++ {
		fssqEn[0] += mul(mul(sampEnWin[l], residual[pp]), residual[pp])
		pp++
	}
	for l := 5; l < subl; l++ {
		fssqEn[0] += mul(residual[pp], residual[pp])
		pp++
	}
	for n := 1; n < enc.nsub-1; n++ {
		pp = n * subl
		for l := 0; l < 5; l++ {
			fssqEn[n] += mul(mul(sampEnWin[l], residual[pp]), residual[pp])
			bssqEn[n] += mul(residual[pp], residual[pp])
			pp++
		}
		for l := 5; l < subl-5; l++ {
			fssqEn[n] += mul(residual[pp], residual[pp])
			bssqEn[n] += mul(residual[pp], residual[pp])
			pp++
		}
		for l := subl - 5; l < subl; l++ {
			fssqEn[n] += mul(residual[pp], residual[pp])
			bssqEn[n] += mul(mul(sampEnWin[subl-l-1], residual[pp]), residual[pp])
			pp++
		}
	}
	n := enc.nsub - 1
	pp = n * subl
	for l := 0; l < subl-5; l++ {
		bssqEn[n] += mul(residual[pp], residual[pp])
		pp++
	}
	for l := subl - 5; l < subl; l++ {
		bssqEn[n] += mul(mul(sampEnWin[subl-l-1], residual[pp]), residual[pp])
		pp++
	}
	l := 0
	if enc.mode == 20 {
		l = 1
	}
	maxSsqEn := mul(fssqEn[0]+bssqEn[1], ssqEnWin[l])
	maxSsqEnN := 1
	for n := 2; n < enc.nsub; n++ {
		l++
		if v := mul(fssqEn[n-1]+bssqEn[n], ssqEnWin[l]); v > maxSsqEn {
			maxSsqEn = v
			maxSsqEnN = n
		}
	}
	return maxSsqEnN
}
