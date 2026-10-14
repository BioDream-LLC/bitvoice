package ilbc

import "math"

// Translation of doCPLC.c: packet loss concealment.

func sqrtf(x float32) float32 { return float32(math.Sqrt(float64(x))) }

func compCorr(cc, gc, pm *float32, buffer []float32, lag, bLen, sRange int) {
	if bLen-sRange-lag < 0 {
		sRange = bLen - lag
	}
	var ftmp1, ftmp2, ftmp3 float32
	for i := 0; i < sRange; i++ {
		a, b := buffer[bLen-sRange+i], buffer[bLen-sRange+i-lag]
		ftmp1 += mul(a, b)
		ftmp2 += mul(b, b)
		ftmp3 += mul(a, a)
	}
	if ftmp2 > 0.0 {
		*cc = mul(ftmp1, ftmp1) / ftmp2
		*gc = absf(ftmp1 / ftmp2)
		*pm = absf(ftmp1) / mul(sqrtf(ftmp2), sqrtf(ftmp3))
	} else {
		*cc, *gc, *pm = 0.0, 0.0, 0.0
	}
}

func doThePLC(plcResidual, plcLpc []float32, pli int, decresidual, lpc []float32, inlag int, dec *decInst) {
	lag := 20
	var gain, maxcc, maxPer float32
	if pli == 1 {
		dec.consPLICount++
		if dec.prevPLI != 1 {
			lag = inlag - 3
			compCorr(&maxcc, &gain, &maxPer, dec.prevResidual[:], lag, dec.blockl, 60)
			for i := inlag - 2; i <= inlag+3; i++ {
				var maxccComp, gainComp, per float32
				compCorr(&maxccComp, &gainComp, &per, dec.prevResidual[:], i, dec.blockl, 60)
				if maxccComp > maxcc {
					maxcc = maxccComp
					gain = gainComp
					lag = i
					maxPer = per
				}
			}
		} else {
			lag = dec.prevLag
			maxPer = dec.per
		}
		useGain := float32(1.0)
		switch {
		case dec.consPLICount*dec.blockl > 320:
			useGain = 0.9
		case dec.consPLICount*dec.blockl > 2*320:
			useGain = 0.7
		case dec.consPLICount*dec.blockl > 3*320:
			useGain = 0.5
		case dec.consPLICount*dec.blockl > 4*320:
			useGain = 0.0
		}
		ftmp := sqrtf(maxPer)
		var pitchfact float32
		switch {
		case ftmp > 0.7:
			pitchfact = 1.0
		case ftmp > 0.4:
			pitchfact = (ftmp - 0.4) / (float32(0.7) - float32(0.4))
		}
		useLag := lag
		if lag < 80 {
			useLag = 2 * lag
		}
		var randvec [blocklMax]float32
		var energy float32
		for i := 0; i < dec.blockl; i++ {
			dec.seed = (dec.seed*69069 + 1) & (0x80000000 - 1)
			randlag := 50 + int(dec.seed)%70
			pick := i - randlag
			if pick < 0 {
				randvec[i] = dec.prevResidual[dec.blockl+pick]
			} else {
				randvec[i] = randvec[pick]
			}
			pick = i - useLag
			if pick < 0 {
				plcResidual[i] = dec.prevResidual[dec.blockl+pick]
			} else {
				plcResidual[i] = plcResidual[pick]
			}
			mix := mul(pitchfact, plcResidual[i]) + mul(float32(1.0)-pitchfact, randvec[i])
			switch {
			case i < 80:
				plcResidual[i] = mul(useGain, mix)
			case i < 160:
				plcResidual[i] = mul(mul(0.95, useGain), mix)
			default:
				plcResidual[i] = mul(mul(0.9, useGain), mix)
			}
			energy += mul(plcResidual[i], plcResidual[i])
		}
		if math.Sqrt(float64(energy/float32(dec.blockl))) < 30.0 {
			for i := 0; i < dec.blockl; i++ {
				plcResidual[i] = randvec[i]
			}
		}
		copy(plcLpc[:lpcFilterorder+1], dec.prevLpc[:])
	} else {
		copy(plcResidual[:dec.blockl], decresidual[:dec.blockl])
		copy(plcLpc[:lpcFilterorder+1], lpc[:lpcFilterorder+1])
		dec.consPLICount = 0
	}
	if pli != 0 {
		dec.prevLag = lag
		dec.per = maxPer
	}
	dec.prevPLI = pli
	copy(dec.prevLpc[:], plcLpc[:lpcFilterorder+1])
	copy(dec.prevResidual[:dec.blockl], plcResidual[:dec.blockl])
}
