package ilbc

import "math"

// Translation of enhancer.c: the decoder's pitch enhancer.

func nearestNeighbor(index *int, array []float32, value float32, arlength int) {
	crit := array[0] - value
	bestcrit := mul(crit, crit)
	*index = 0
	for i := 1; i < arlength; i++ {
		crit = array[i] - value
		crit = mul(crit, crit)
		if crit < bestcrit {
			bestcrit = crit
			*index = i
		}
	}
}

func mycorr1(corr []float32, seq1 []float32, dim1 int, seq2 []float32, dim2 int) {
	for i := 0; i <= dim1-dim2; i++ {
		var c float32
		for j := 0; j < dim2; j++ {
			c += mul(seq1[i+j], seq2[j])
		}
		corr[i] = c
	}
}

func enhUpsample(useq1 []float32, seq1 []float32, dim1, hfl int) {
	var polyp [enhUps0]int // offsets into polyphaserTbl
	filterlength := 2*hfl + 1
	if filterlength > dim1 {
		hfl2 := dim1 / 2
		for j := 0; j < enhUps0; j++ {
			polyp[j] = j*filterlength + hfl - hfl2
		}
		hfl = hfl2
		filterlength = 2*hfl + 1
	} else {
		for j := 0; j < enhUps0; j++ {
			polyp[j] = j * filterlength
		}
	}
	pu := 0
	for i := hfl; i < filterlength; i++ {
		for j := 0; j < enhUps0; j++ {
			var o float32
			pp, ps := polyp[j], i
			for k := 0; k <= i; k++ {
				o += mul(seq1[ps], polyphaserTbl[pp])
				ps--
				pp++
			}
			useq1[pu] = o
			pu++
		}
	}
	for i := filterlength; i < dim1; i++ {
		for j := 0; j < enhUps0; j++ {
			var o float32
			pp, ps := polyp[j], i
			for k := 0; k < filterlength; k++ {
				o += mul(seq1[ps], polyphaserTbl[pp])
				ps--
				pp++
			}
			useq1[pu] = o
			pu++
		}
	}
	for q := 1; q <= hfl; q++ {
		for j := 0; j < enhUps0; j++ {
			var o float32
			pp, ps := polyp[j]+q, dim1-1
			for k := 0; k < filterlength-q; k++ {
				o += mul(seq1[ps], polyphaserTbl[pp])
				ps--
				pp++
			}
			useq1[pu] = o
			pu++
		}
	}
}

func refiner(seg []float32, updStartPos *float32, idata []float32, idatal, centerStartPos int, estSegPos, period float32) {
	var vect [enhVectl]float32
	var corrVec [enhCorrdim]float32
	var corrVecUps [enhCorrdim * enhUps0]float32
	estSegPosRounded := int(float64(estSegPos) - 0.5)
	searchSegStartPos := estSegPosRounded - enhSlop
	if searchSegStartPos < 0 {
		searchSegStartPos = 0
	}
	searchSegEndPos := estSegPosRounded + enhSlop
	if searchSegEndPos+enhBlockl >= idatal {
		searchSegEndPos = idatal - enhBlockl - 1
	}
	corrdim := searchSegEndPos - searchSegStartPos + 1
	mycorr1(corrVec[:], idata[searchSegStartPos:], corrdim+enhBlockl-1, idata[centerStartPos:], enhBlockl)
	enhUpsample(corrVecUps[:], corrVec[:], corrdim, enhFL0)
	tloc := 0
	maxv := corrVecUps[0]
	for i := 1; i < enhUps0*corrdim; i++ {
		if corrVecUps[i] > maxv {
			tloc = i
			maxv = corrVecUps[i]
		}
	}
	*updStartPos = float32(searchSegStartPos) + float32(tloc)/float32(enhUps0) + float32(1.0)
	tloc2 := tloc / enhUps0
	if tloc > tloc2*enhUps0 {
		tloc2++
	}
	st := searchSegStartPos + tloc2 - enhFL0
	if st < 0 {
		copy(vect[-st:], idata[:enhVectl+st])
	} else {
		en := st + enhVectl
		if en > idatal {
			copy(vect[:enhVectl-(en-idatal)], idata[st:])
		} else {
			copy(vect[:], idata[st:st+enhVectl])
		}
	}
	fraction := tloc2*enhUps0 - tloc
	mycorr1(seg, vect[:], enhVectl, polyphaserTbl[(2*enhFL0+1)*fraction:], 2*enhFL0+1)
}

func smath(odata []float32, sseq []float32, hl int, alpha0 float32) {
	var surround [blocklMax]float32
	var wt [2*enhHL + 1]float32
	for i := 1; i <= 2*hl+1; i++ {
		x := float32(2) * pi * float32(i) // (2*PI*i) in float, then divided
		x = x / float32(2*hl+2)
		wt[i-1] = mul(0.5, 1-cosf(x))
	}
	wt[hl] = 0.0
	for i := 0; i < enhBlockl; i++ {
		surround[i] = mul(sseq[i], wt[0])
	}
	for k := 1; k < hl; k++ {
		ps := sseq[k*enhBlockl:]
		for i := 0; i < enhBlockl; i++ {
			surround[i] += mul(ps[i], wt[k])
		}
	}
	for k := hl + 1; k <= 2*hl; k++ {
		ps := sseq[k*enhBlockl:]
		for i := 0; i < enhBlockl; i++ {
			surround[i] += mul(ps[i], wt[k])
		}
	}
	var w00, w10, w11 float32
	ps := sseq[hl*enhBlockl:]
	for i := 0; i < enhBlockl; i++ {
		w00 += mul(ps[i], ps[i])
		w11 += mul(surround[i], surround[i])
		w10 += mul(surround[i], ps[i])
	}
	if math.Abs(float64(w11)) < 1.0 {
		w11 = 1.0
	}
	c := sqrtf(w00 / w11)
	var errs float32
	for i := 0; i < enhBlockl; i++ {
		odata[i] = mul(c, surround[i])
		e := ps[i] - odata[i]
		errs += mul(e, e)
	}
	if errs > mul(alpha0, w00) {
		if w00 < 1 {
			w00 = 1
		}
		denom := (mul(w11, w00) - mul(w10, w10)) / mul(w00, w00)
		var a, b float32
		if float64(denom) > 0.0001 {
			a = sqrtf((alpha0 - mul(alpha0, alpha0)/4) / denom)
			b = -alpha0/2 - mul(a, w10)/w00
			b = b + 1
		} else {
			a, b = 0.0, 1.0
		}
		for i := 0; i < enhBlockl; i++ {
			odata[i] = mul(a, surround[i]) + mul(b, ps[i])
		}
	}
}

func getsseq(sseq []float32, idata []float32, idatal, centerStartPos int, period, plocs []float32, periodl, hl int) {
	var blockStartPos [2*enhHL + 1]float32
	var lagBlock [2*enhHL + 1]int
	var plocs2 [enhPlocsl]float32
	centerEndPos := centerStartPos + enhBlockl - 1
	nearestNeighbor(&lagBlock[hl], plocs, mul(0.5, float32(centerStartPos+centerEndPos)), periodl)
	blockStartPos[hl] = float32(centerStartPos)
	copy(sseq[enhBlockl*hl:enhBlockl*hl+enhBlockl], idata[centerStartPos:centerStartPos+enhBlockl])
	for q := hl - 1; q >= 0; q-- {
		blockStartPos[q] = blockStartPos[q+1] - period[lagBlock[q+1]]
		nearestNeighbor(&lagBlock[q], plocs, blockStartPos[q]+enhBlocklHalf-period[lagBlock[q+1]], periodl)
		if blockStartPos[q]-enhOverhang >= 0 {
			refiner(sseq[q*enhBlockl:], &blockStartPos[q], idata, idatal, centerStartPos, blockStartPos[q], period[lagBlock[q+1]])
		} else {
			clear(sseq[q*enhBlockl : q*enhBlockl+enhBlockl])
		}
	}
	for i := 0; i < periodl; i++ {
		plocs2[i] = plocs[i] - period[i]
	}
	for q := hl + 1; q <= 2*hl; q++ {
		nearestNeighbor(&lagBlock[q], plocs2[:], blockStartPos[q-1]+enhBlocklHalf, periodl)
		blockStartPos[q] = blockStartPos[q-1] + period[lagBlock[q]]
		if blockStartPos[q]+enhBlockl+enhOverhang < float32(idatal) {
			refiner(sseq[enhBlockl*q:], &blockStartPos[q], idata, idatal, centerStartPos, blockStartPos[q], period[lagBlock[q]])
		} else {
			clear(sseq[q*enhBlockl : q*enhBlockl+enhBlockl])
		}
	}
}

func enhancer(odata []float32, idata []float32, idatal, centerStartPos int, alpha0 float32, period, plocs []float32, periodl int) {
	var sseq [(2*enhHL + 1) * enhBlockl]float32
	getsseq(sseq[:], idata, idatal, centerStartPos, period, plocs, periodl, enhHL)
	smath(odata, sseq[:], enhHL, alpha0)
}

func xCorrCoef(target, regressor []float32, n int) float32 {
	var ftmp1, ftmp2 float32
	for i := 0; i < n; i++ {
		ftmp1 += mul(target[i], regressor[i])
		ftmp2 += mul(regressor[i], regressor[i])
	}
	if ftmp1 > 0.0 {
		return mul(ftmp1, ftmp1) / ftmp2
	}
	return 0.0
}

func enhancerInterface(out []float32, in []float32, dec *decInst) int {
	enhBuf := dec.enhBuf[:]
	enhPeriod := dec.enhPeriod[:]
	var plcPred [enhBlockl]float32
	var lpState [6]float32
	var downsampled [(enhNblocks*enhBlockl + 120) / 2]float32
	inLen := enhNblocks*enhBlockl + 120
	lag := 0
	copy(enhBuf, enhBuf[dec.blockl:enhBufl])
	copy(enhBuf[enhBufl-dec.blockl:], in[:dec.blockl])
	plcBlockl := 40
	if dec.mode == 30 {
		plcBlockl = enhBlockl
	}
	ioffset := 0
	if dec.mode == 20 {
		ioffset = 1
	}
	i := 3 - ioffset
	copy(enhPeriod, enhPeriod[i:enhNblocksTot])
	copy(lpState[:], enhBuf[(enhNblocksExtra+ioffset)*enhBlockl-126:])
	downSample(enhBuf[(enhNblocksExtra+ioffset)*enhBlockl-120:], lpFilt_coefsTbl, inLen-ioffset*enhBlockl, lpState[:], downsampled[:])
	for iblock := 0; iblock < enhNblocks-ioffset; iblock++ {
		lag = 10
		base := 60 + iblock*enhBlocklHalf
		maxcc := xCorrCoef(downsampled[base:], downsampled[base-lag:], enhBlocklHalf)
		for ilag := 11; ilag < 60; ilag++ {
			if cc := xCorrCoef(downsampled[base:], downsampled[base-ilag:], enhBlocklHalf); cc > maxcc {
				maxcc = cc
				lag = ilag
			}
		}
		enhPeriod[iblock+enhNblocksExtra+ioffset] = float32(lag) * 2
	}
	if dec.prevEnhPl == 1 {
		inlag := int(enhPeriod[enhNblocksExtra+ioffset])
		lag = inlag - 1
		maxcc := xCorrCoef(in, in[lag:], plcBlockl)
		for ilag := inlag; ilag <= inlag+1; ilag++ {
			if cc := xCorrCoef(in, in[ilag:], plcBlockl); cc > maxcc {
				maxcc = cc
				lag = ilag
			}
		}
		enhPeriod[enhNblocksExtra+ioffset-1] = float32(lag)
		inPtr := lag - 1
		p1 := plcBlockl - 1
		start := lag
		if lag > plcBlockl {
			start = plcBlockl
		}
		for isample := start; isample > 0; isample-- {
			plcPred[p1] = in[inPtr]
			p1--
			inPtr--
		}
		p2 := enhBufl - 1 - dec.blockl
		for isample := plcBlockl - 1 - lag; isample >= 0; isample-- {
			plcPred[p1] = enhBuf[p2]
			p1--
			p2--
		}
		var ftmp1, ftmp2 float32
		for i := 0; i < plcBlockl; i++ {
			v := enhBuf[enhBufl-1-dec.blockl-i]
			ftmp2 += mul(v, v)
			ftmp1 += mul(plcPred[i], plcPred[i])
		}
		ftmp1 = sqrtf(ftmp1 / float32(plcBlockl))
		ftmp2 = sqrtf(ftmp2 / float32(plcBlockl))
		if ftmp1 > mul(2.0, ftmp2) && ftmp1 > 0.0 {
			r := mul(2.0, ftmp2) / ftmp1
			for i := 0; i < plcBlockl-10; i++ {
				plcPred[i] = mul(plcPred[i], r)
			}
			for i := plcBlockl - 10; i < plcBlockl; i++ {
				plcPred[i] = mul(plcPred[i], mul(float32(i-plcBlockl+10), float32(1.0)-r)/float32(10)+r)
			}
		}
		p1 = enhBufl - 1 - dec.blockl
		for i := 0; i < plcBlockl; i++ {
			f := float32(i+1) / float32(plcBlockl+1)
			enhBuf[p1] = mul(enhBuf[p1], f)
			enhBuf[p1] += mul(float32(1.0)-f, plcPred[plcBlockl-1-i])
			p1--
		}
	}
	if dec.mode == 20 {
		for iblock := 0; iblock < 2; iblock++ {
			enhancer(out[iblock*enhBlockl:], enhBuf, enhBufl, (5+iblock)*enhBlockl+40, enhAlpha0, enhPeriod, enh_plocsTbl, enhNblocksTot)
		}
	} else {
		for iblock := 0; iblock < 3; iblock++ {
			enhancer(out[iblock*enhBlockl:], enhBuf, enhBufl, (4+iblock)*enhBlockl, enhAlpha0, enhPeriod, enh_plocsTbl, enhNblocksTot)
		}
	}
	return lag * 2
}
