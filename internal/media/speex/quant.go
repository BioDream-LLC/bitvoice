package speex

import "math"

// Vector quantisation, the LSP quantisers, the pitch predictor and the innovation codebooks of
// libspeex (vq.c, quant_lsp.c, ltp.c, cb_search.c).

func scalQuant(in float32, boundary []float32, entries int) int {
	i := 0
	for i < entries-1 && in > boundary[i] {
		i++
	}
	return i
}

func vqNbest(in, codebook []float32, n, entries int, e []float32, nBest int, nbest []int, bestDist []float32) {
	used, c := 0, 0
	for i := 0; i < entries; i++ {
		var dist float32
		for j := 0; j < n; j++ {
			dist += mul(in[j], codebook[c])
			c++
		}
		dist = mul(.5, e[i]) - dist
		if i < nBest || dist < bestDist[nBest-1] {
			k := nBest - 1
			for ; k >= 1 && (k > used || dist < bestDist[k-1]); k-- {
				bestDist[k] = bestDist[k-1]
				nbest[k] = nbest[k-1]
			}
			bestDist[k] = dist
			nbest[k] = i
			used++
		}
	}
}

func vqNbestSign(in, codebook []float32, n, entries int, e []float32, nBest int, nbest []int, bestDist []float32) {
	used, c := 0, 0
	for i := 0; i < entries; i++ {
		var dist float32
		for j := 0; j < n; j++ {
			dist += mul(in[j], codebook[c])
			c++
		}
		sign := false
		if dist > 0 {
			dist = -dist
		} else {
			sign = true
		}
		dist += mul(.5, e[i])
		if i < nBest || dist < bestDist[nBest-1] {
			k := nBest - 1
			for ; k >= 1 && (k > used || dist < bestDist[k-1]); k-- {
				bestDist[k] = bestDist[k-1]
				nbest[k] = nbest[k-1]
			}
			bestDist[k] = dist
			nbest[k] = i
			used++
			if sign {
				nbest[k] += entries
			}
		}
	}
}

// LSP quantisation.

func quantWeights(q, w []float32, order int) {
	for i := 0; i < order; i++ {
		var t1, t2 float32
		if i == 0 {
			t1 = q[i]
		} else {
			t1 = q[i] - q[i-1]
		}
		if i == order-1 {
			t2 = float32(math.Pi - float64(q[i]))
		} else {
			t2 = q[i+1] - q[i]
		}
		if t2 < t1 {
			t1 = t2
		}
		w[i] = float32(10 / (.04 + float64(t1)))
	}
}

func lspQuant(x []float32, cdbk []int8, nbVec, nbDim int) int {
	best := float32(1e15)
	id, c := 0, 0
	for i := 0; i < nbVec; i++ {
		var dist float32
		for j := 0; j < nbDim; j++ {
			t := x[j] - float32(cdbk[c])
			c++
			dist += mul(t, t)
		}
		if dist < best {
			best, id = dist, i
		}
	}
	for j := 0; j < nbDim; j++ {
		x[j] -= float32(cdbk[id*nbDim+j])
	}
	return id
}

func lspWeightQuant(x, w []float32, cdbk []int8, nbVec, nbDim int) int {
	best := float32(1e15)
	id, c := 0, 0
	for i := 0; i < nbVec; i++ {
		var dist float32
		for j := 0; j < nbDim; j++ {
			t := x[j] - float32(cdbk[c])
			c++
			dist += mul(w[j], mul(t, t))
		}
		if dist < best {
			best, id = dist, i
		}
	}
	for j := 0; j < nbDim; j++ {
		x[j] -= float32(cdbk[id*nbDim+j])
	}
	return id
}

func lspQuantStart(lsp, q, w []float32, order int) {
	copy(q, lsp[:order])
	quantWeights(q, w, order)
	for i := 0; i < order; i++ {
		q[i] = float32(float64(q[i]) - (mul64(.25, float64(i)) + .25))
	}
	for i := 0; i < order; i++ {
		q[i] = float32(256 * float64(q[i]))
	}
}

func lspQuantNB(lsp, q []float32, order int, w *bitWriter) {
	var wt [lpcOrder]float32
	lspQuantStart(lsp, q, wt[:], order)
	w.pack(uint32(lspQuant(q, cdbkNB[:], 64, order)), 6)
	for i := 0; i < order; i++ {
		q[i] *= 2
	}
	w.pack(uint32(lspWeightQuant(q, wt[:], cdbkNBLow1[:], 64, 5)), 6)
	for i := 0; i < 5; i++ {
		q[i] *= 2
	}
	w.pack(uint32(lspWeightQuant(q, wt[:], cdbkNBLow2[:], 64, 5)), 6)
	w.pack(uint32(lspWeightQuant(q[5:], wt[5:], cdbkNBHigh1[:], 64, 5)), 6)
	for i := 5; i < 10; i++ {
		q[i] *= 2
	}
	w.pack(uint32(lspWeightQuant(q[5:], wt[5:], cdbkNBHigh2[:], 64, 5)), 6)
	for i := 0; i < order; i++ {
		q[i] = float32(float64(q[i]) * .00097656)
	}
	for i := 0; i < order; i++ {
		q[i] = lsp[i] - q[i]
	}
}

func lspUnquantNB(lsp []float32, order int, r *bitReader) {
	for i := 0; i < order; i++ {
		lsp[i] = float32(mul64(.25, float64(i)) + .25)
	}
	add := func(at int, scale float64, cb []int8, dim int) {
		id := int(r.unpack(6))
		for i := 0; i < dim; i++ {
			lsp[at+i] = float32(float64(lsp[at+i]) + mul64(scale, float64(cb[id*dim+i])))
		}
	}
	add(0, 0.0039062, cdbkNB[:], 10)
	add(0, 0.0019531, cdbkNBLow1[:], 5)
	add(0, 0.00097656, cdbkNBLow2[:], 5)
	add(5, 0.0019531, cdbkNBHigh1[:], 5)
	add(5, 0.00097656, cdbkNBHigh2[:], 5)
}

func lspQuantLBR(lsp, q []float32, order int, w *bitWriter) {
	var wt [lpcOrder]float32
	lspQuantStart(lsp, q, wt[:], order)
	w.pack(uint32(lspQuant(q, cdbkNB[:], 64, order)), 6)
	for i := 0; i < order; i++ {
		q[i] *= 2
	}
	w.pack(uint32(lspWeightQuant(q, wt[:], cdbkNBLow1[:], 64, 5)), 6)
	w.pack(uint32(lspWeightQuant(q[5:], wt[5:], cdbkNBHigh1[:], 64, 5)), 6)
	for i := 0; i < order; i++ {
		q[i] = float32(float64(q[i]) * 0.0019531)
	}
	for i := 0; i < order; i++ {
		q[i] = lsp[i] - q[i]
	}
}

func lspUnquantLBR(lsp []float32, order int, r *bitReader) {
	for i := 0; i < order; i++ {
		lsp[i] = float32(mul64(.25, float64(i)) + .25)
	}
	add := func(at int, scale float64, cb []int8, dim int) {
		id := int(r.unpack(6))
		for i := 0; i < dim; i++ {
			lsp[at+i] = float32(float64(lsp[at+i]) + mul64(scale, float64(cb[id*dim+i])))
		}
	}
	add(0, 0.0039062, cdbkNB[:], 10)
	add(0, 0.0019531, cdbkNBLow1[:], 5)
	add(5, 0.0019531, cdbkNBHigh1[:], 5)
}

// The pitch predictor.

type ltpParams struct {
	gainCdbk  []int8
	gainBits  int
	pitchBits int
}

func pitchXcorr(x, y, corr []float32, n, nbPitch int) {
	for i := 0; i < nbPitch; i++ {
		corr[nbPitch-1-i] = innerProd(x, y[i:], n)
	}
}

// openLoopNbestPitch searches sw (the reference's pointer is sw[at]) for the nb best pitches.
func openLoopNbestPitch(sw []float32, at, start, end, n int, pitch []int, gain []float32, nBest int) {
	energy := make([]float32, end-start+2)
	corr := make([]float32, end-start+1)
	bestScore := make([]float32, nBest)
	bestEner := make([]float32, nBest)
	for i := 0; i < nBest; i++ {
		bestScore[i], bestEner[i], pitch[i] = -1, 0, start
	}
	energy[0] = innerProd(sw[at-start:], sw[at-start:], n)
	e0 := innerProd(sw[at:], sw[at:], n)
	for i := start; i < end; i++ {
		a, b := sw[at-i-1], sw[at-i+n-1]
		energy[i-start+1] = energy[i-start] + mul(a, a) - mul(b, b)
		if energy[i-start+1] < 0 {
			energy[i-start+1] = 0
		}
	}
	pitchXcorr(sw[at:], sw[at-end:], corr, n, end-start+1)
	for i := start; i <= end; i++ {
		tmp := mul(corr[i-start], corr[i-start])
		if mul(tmp, bestEner[nBest-1]) > mul(bestScore[nBest-1], 1+energy[i-start]) {
			bestScore[nBest-1] = tmp
			bestEner[nBest-1] = energy[i-start] + 1
			pitch[nBest-1] = i
			for j := 0; j < nBest-1; j++ {
				if mul(tmp, bestEner[j]) > mul(bestScore[j], 1+energy[i-start]) {
					for k := nBest - 1; k > j; k-- {
						bestScore[k] = bestScore[k-1]
						bestEner[k] = bestEner[k-1]
						pitch[k] = pitch[k-1]
					}
					bestScore[j] = tmp
					bestEner[j] = energy[i-start] + 1
					pitch[j] = i
					break
				}
			}
		}
	}
	if gain != nil {
		for j := 0; j < nBest; j++ {
			i := pitch[j]
			g := corr[i-start] / (10 + mul(float32(math.Sqrt(float64(e0))), float32(math.Sqrt(float64(energy[i-start])))))
			if g < 0 {
				g = 0
			}
			gain[j] = g
		}
	}
}

func pitchError(c []float32, g *[3]float32, pc float32) float32 {
	var sum float32
	sum += mul(mul(g[0], pc), c[0])
	sum += mul(mul(g[1], pc), c[1])
	sum += mul(mul(g[2], pc), c[2])
	sum -= mul(mul(g[0], g[1]), c[3])
	sum -= mul(mul(g[2], g[1]), c[4])
	sum -= mul(mul(g[2], g[0]), c[5])
	sum -= mul(mul(g[0], g[0]), c[6])
	sum -= mul(mul(g[1], g[1]), c[7])
	sum -= mul(mul(g[2], g[2]), c[8])
	return sum
}

func pitchGainSearchVQ(cdbk []int8, size int, c []float32, maxGain float32) int {
	best := 0
	bestSum := float32(-1e15)
	for i := 0; i < size; i++ {
		p := cdbk[4*i:]
		g := [3]float32{float32(p[0]) + 32, float32(p[1]) + 32, float32(p[2]) + 32}
		sum := pitchError(c, &g, 64)
		if sum > bestSum && float32(p[3]) <= maxGain {
			bestSum, best = sum, i
		}
	}
	return best
}

// pitchGainSearch3tap: exc2 is the past excitation, the reference's pointer at exc2[at].
func pitchGainSearch3tap(target, ak, awk1, awk2, exc []float32, cdbk []int8, size, pitch, p, nsf int,
	exc2 []float32, at int, r, newTarget []float32, index *int, plc int, cumulGain float32) float32 {
	maxGain := float32(128)
	if cumulGain > 262144 {
		maxGain = 31
	}
	tmp1 := make([]float32, 3*nsf)
	x := [3][]float32{tmp1[:nsf], tmp1[nsf : 2*nsf], tmp1[2*nsf:]}
	e := make([]float32, nsf)
	copy(newTarget, target[:nsf])
	{
		pp := pitch - 1
		bound := nsf
		if nsf-pp > 0 {
			bound = pp
		}
		j := 0
		for ; j < bound; j++ {
			e[j] = exc2[at+j-pp]
		}
		bound = nsf
		if nsf-pp-pitch > 0 {
			bound = pp + pitch
		}
		for ; j < bound; j++ {
			e[j] = exc2[at+j-pp-pitch]
		}
		for ; j < nsf; j++ {
			e[j] = 0
		}
		var mm [lpcOrder]float32
		iirMem16(e, ak, e, nsf, p, mm[:])
		mm = [lpcOrder]float32{}
		filterMem16(e, awk1, awk2, e, nsf, 10, mm[:])
		copy(x[2], e)
	}
	for i := 1; i >= 0; i-- {
		e0 := exc2[at-pitch-1+i]
		x[i][0] = mul(r[0], e0)
		for j := 0; j < nsf-1; j++ {
			x[i][j+1] = x[i+1][j] + mul(r[j+1], e0)
		}
	}
	var corr [3]float32
	var a [3][3]float32
	for i := 0; i < 3; i++ {
		corr[i] = innerProd(x[i], newTarget, nsf)
	}
	for i := 0; i < 3; i++ {
		for j := 0; j <= i; j++ {
			a[i][j] = innerProd(x[i], x[j], nsf)
			a[j][i] = a[i][j]
		}
	}
	c := []float32{corr[2], corr[1], corr[0], a[1][2], a[0][1], a[0][2], a[2][2], a[1][1], a[0][0]}
	plc = min(max(plc, 2), 30)
	f := .5 * (1 + mul64(.02, float64(plc)))
	for i := 6; i < 9; i++ {
		c[i] = float32(float64(c[i]) * f)
	}
	best := pitchGainSearchVQ(cdbk, size, c, maxGain)
	var gain [3]float32
	for k := 0; k < 3; k++ {
		gain[k] = float32(mul64(0.015625, float64(cdbk[best*4+k])) + .5)
	}
	*index = best
	clear(exc[:nsf])
	for i := 0; i < 3; i++ {
		pp := pitch + 1 - i
		t1 := min(nsf, pp)
		for j := 0; j < t1; j++ {
			exc[j] += mul(gain[2-i], exc2[at+j-pp])
		}
		t3 := min(nsf, pp+pitch)
		for j := t1; j < t3; j++ {
			exc[j] += mul(gain[2-i], exc2[at+j-pp-pitch])
		}
	}
	for i := 0; i < nsf; i++ {
		t := mul(gain[0], x[2][i]) + mul(gain[1], x[1][i]) + mul(gain[2], x[0][i])
		newTarget[i] -= t
	}
	return innerProd(newTarget, newTarget, nsf)
}

// ltpQuant and ltpUnquant are the two pitch predictors of the submodes: the 3-tap search and the
// forced gain of the lowest rates. sw and exc2 are buffers with the reference's pointers at swAt
// and excAt.
type ltpQuantFunc func(target, sw []float32, swAt int, ak, awk1, awk2, exc []float32, par *ltpParams, start, end int,
	pitchCoef float32, p, nsf int, w *bitWriter, exc2 []float32, excAt int, r []float32, complexity, plc int, cumulGain *float32) int

type ltpUnquantFunc func(exc []float32, at int, out []float32, start, end int, pitchCoef float32, par *ltpParams, nsf int,
	pitchVal *int, gainVal []float32, rd *bitReader, countLost, subOffset int, lastPitchGain float32)

func pitchSearch3tap(target, sw []float32, swAt int, ak, awk1, awk2, exc []float32, par *ltpParams, start, end int,
	pitchCoef float32, p, nsf int, w *bitWriter, exc2 []float32, excAt int, r []float32, complexity, plc int, cumulGain *float32) int {
	size := 1 << par.gainBits
	cdbk := par.gainCdbk
	n := min(max(complexity, 1), 10)
	if end < start {
		w.pack(0, par.pitchBits)
		w.pack(0, par.gainBits)
		clear(exc[:nsf])
		return start
	}
	n = min(n, end-start+1)
	nbest := make([]int, n)
	if end != start {
		openLoopNbestPitch(sw, swAt, start, end, nsf, nbest, nil, n)
	} else {
		nbest[0] = start
	}
	bestExc := make([]float32, nsf)
	newTarget := make([]float32, nsf)
	bestTarget := make([]float32, nsf)
	bestErr := float32(-1)
	pitch, bestPitch, bestGain := 0, 0, 0
	for i := 0; i < n; i++ {
		pitch = nbest[i]
		clear(exc[:nsf])
		var idx int
		err := pitchGainSearch3tap(target, ak, awk1, awk2, exc, cdbk, size, pitch, p, nsf, exc2, excAt, r, newTarget, &idx, plc, *cumulGain)
		if err < bestErr || bestErr < 0 {
			copy(bestExc, exc[:nsf])
			copy(bestTarget, newTarget)
			bestErr, bestPitch, bestGain = err, pitch, idx
		}
	}
	w.pack(uint32(bestPitch-start), par.pitchBits)
	w.pack(uint32(bestGain), par.gainBits)
	g := float64(*cumulGain)
	if 1024 > *cumulGain {
		g = 1024
	}
	*cumulGain = float32(mul64(mul64(0.03125, g), float64(par.gainCdbk[4*bestGain+3])))
	copy(exc, bestExc)
	copy(target, bestTarget)
	return pitch // as the reference: the last pitch tried
}

func pitchUnquant3tap(exc []float32, at int, out []float32, start, end int, pitchCoef float32, par *ltpParams, nsf int,
	pitchVal *int, gainVal []float32, rd *bitReader, countLost, subOffset int, lastPitchGain float32) {
	cdbk := par.gainCdbk
	pitch := int(rd.unpack(par.pitchBits)) + start
	gi := int(rd.unpack(par.gainBits))
	var gain [3]float32
	for k := 0; k < 3; k++ {
		gain[k] = float32(mul64(0.015625, float64(cdbk[gi*4+k])) + .5)
	}
	if countLost != 0 && pitch > subOffset {
		tmp := lastPitchGain
		if countLost >= 4 {
			tmp = float32(mul64(.5, float64(lastPitchGain)))
		}
		if float64(tmp) > .95 {
			tmp = .95
		}
		side := func(g float32) float64 {
			if g > 0 {
				return float64(g)
			}
			return mul64(-.5, float64(g))
		}
		sum := float32(float64(abs32(gain[1])) + side(gain[0]) + side(gain[2]))
		if sum > tmp {
			fact := tmp / sum
			for i := range gain {
				gain[i] = mul(fact, gain[i])
			}
		}
	}
	*pitchVal = pitch
	copy(gainVal, gain[:])
	clear(out[:nsf])
	for i := 0; i < 3; i++ {
		pp := pitch + 1 - i
		t1 := min(nsf, pp)
		for j := 0; j < t1; j++ {
			out[j] += mul(gain[2-i], exc[at+j-pp])
		}
		t3 := min(nsf, pp+pitch)
		for j := t1; j < t3; j++ {
			out[j] += mul(gain[2-i], exc[at+j-pp-pitch])
		}
	}
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

func forcedPitchQuant(target, sw []float32, swAt int, ak, awk1, awk2, exc []float32, par *ltpParams, start, end int,
	pitchCoef float32, p, nsf int, w *bitWriter, exc2 []float32, excAt int, r []float32, complexity, plc int, cumulGain *float32) int {
	if float64(pitchCoef) > .99 {
		pitchCoef = .99
	}
	i := 0
	for ; i < nsf && i < start; i++ {
		exc[i] = mul(pitchCoef, exc2[excAt+i-start])
	}
	for ; i < nsf; i++ {
		exc[i] = mul(pitchCoef, exc[i-start])
	}
	res := make([]float32, nsf)
	copy(res, exc[:nsf])
	synPercepZero16(res, ak, awk1, awk2, res, nsf, p)
	for i := 0; i < nsf; i++ {
		target[i] -= res[i]
	}
	return start
}

func forcedPitchUnquant(exc []float32, at int, out []float32, start, end int, pitchCoef float32, par *ltpParams, nsf int,
	pitchVal *int, gainVal []float32, rd *bitReader, countLost, subOffset int, lastPitchGain float32) {
	if float64(pitchCoef) > .99 {
		pitchCoef = .99
	}
	for i := 0; i < nsf; i++ {
		out[i] = mul(exc[at+i-start], pitchCoef)
		exc[at+i] = out[i]
	}
	*pitchVal = start
	gainVal[0], gainVal[2] = 0, 0
	gainVal[1] = pitchCoef
}

// The innovation codebooks.

type cbParams struct {
	subvectSize, nbSubvect int
	shapeCb                []int8
	shapeBits              int
	haveSign               int
}

type innovQuantFunc func(target, ak, awk1, awk2 []float32, par *cbParams, p, nsf int, exc, r []float32, w *bitWriter, complexity int, update bool)
type innovUnquantFunc func(exc []float32, par *cbParams, nsf int, rd *bitReader, seed *uint32)

func weightedCodebook(cb []int8, r, resp, e []float32, size, ss int) {
	shape := make([]float32, ss)
	for i := 0; i < size; i++ {
		res := resp[i*ss:]
		for k := 0; k < ss; k++ {
			shape[k] = float32(cb[i*ss+k])
		}
		e[i] = 0
		for j := 0; j < ss; j++ {
			var resj float32
			for k := 0; k <= j; k++ {
				resj += mul(shape[k], r[j-k])
			}
			r16 := mul(0.03125, resj)
			e[i] += mul(r16, r16)
			res[j] = r16
		}
	}
}

func targetUpdate(t []float32, g float32, r []float32, n int) {
	for i := 0; i < n; i++ {
		t[i] -= mul(g, r[i])
	}
}

func cbEntry(cb []int8, ind, size, ss, j int) float32 {
	sign := float32(1)
	if ind >= size {
		sign = -1
		ind -= size
	}
	return float32(mul64(mul64(float64(sign), 0.03125), float64(cb[ind*ss+j])))
}

func splitCbSearchN1(target, ak, awk1, awk2 []float32, par *cbParams, p, nsf int, exc, r []float32, w *bitWriter, update bool) {
	ss, nb := par.subvectSize, par.nbSubvect
	size := 1 << par.shapeBits
	cb := par.shapeCb
	resp := make([]float32, size*ss)
	e := make([]float32, size)
	t := make([]float32, nsf)
	ex := make([]float32, nsf)
	copy(t, target[:nsf])
	weightedCodebook(cb, r, resp, e, size, ss)
	var best [1]int
	var bestDist [1]float32
	for i := 0; i < nb; i++ {
		x := t[ss*i:]
		if par.haveSign != 0 {
			vqNbestSign(x, resp, ss, size, e, 1, best[:], bestDist[:])
		} else {
			vqNbest(x, resp, ss, size, e, 1, best[:], bestDist[:])
		}
		w.pack(uint32(best[0]), par.shapeBits+par.haveSign)
		rind, neg := best[0], false
		if rind >= size {
			neg = true
			rind -= size
		}
		res := resp[rind*ss:]
		for m := 0; m < ss; m++ {
			if neg {
				t[ss*i+m] += res[m]
			} else {
				t[ss*i+m] -= res[m]
			}
		}
		for j := 0; j < ss; j++ {
			ex[ss*i+j] = cbEntry(cb, best[0], size, ss, j)
		}
		for m := 0; m < ss; m++ {
			q := ss - m
			g := cbEntry(cb, best[0], size, ss, m)
			targetUpdate(t[ss*(i+1):], g, r[q:], nsf-ss*(i+1))
		}
	}
	for j := 0; j < nsf; j++ {
		exc[j] += ex[j]
	}
	if update {
		r2 := make([]float32, nsf)
		copy(r2, ex)
		synPercepZero16(r2, ak, awk1, awk2, r2, nsf, p)
		for j := 0; j < nsf; j++ {
			target[j] -= r2[j]
		}
	}
}

func splitCbSearch(target, ak, awk1, awk2 []float32, par *cbParams, p, nsf int, exc, r []float32, w *bitWriter, complexity int, update bool) {
	n := min(complexity, 10)
	n = (2 * n) / 3
	if n < 1 {
		n = 1
	}
	if n == 1 {
		splitCbSearchN1(target, ak, awk1, awk2, par, p, nsf, exc, r, w, update)
		return
	}
	ss, nb := par.subvectSize, par.nbSubvect
	size := 1 << par.shapeBits
	cb := par.shapeCb
	resp := make([]float32, size*ss)
	e := make([]float32, size)
	ex := make([]float32, nsf)
	ot, nt := make([][]float32, n), make([][]float32, n)
	oind, nind := make([][]int, n), make([][]int, n)
	for i := 0; i < n; i++ {
		ot[i], nt[i] = make([]float32, nsf), make([]float32, nsf)
		oind[i], nind[i] = make([]int, nb), make([]int, nb)
	}
	bestIndex := make([]int, n)
	bestDist := make([]float32, n)
	bestNind := make([]int, n)
	bestNtarget := make([]int, n)
	ndist := make([]float32, n)
	odist := make([]float32, n)
	for j := 0; j < n; j++ {
		copy(ot[j], target[:nsf])
	}
	weightedCodebook(cb, r, resp, e, size, ss)
	for i := 0; i < nb; i++ {
		for j := 0; j < n; j++ {
			ndist[j] = 1e15
			bestNind[j], bestNtarget[j] = 0, 0
		}
		for j := 0; j < n; j++ {
			x := ot[j][ss*i:]
			var tener float32
			for m := 0; m < ss; m++ {
				tener += mul(x[m], x[m])
			}
			tener = mul(tener, .5)
			if par.haveSign != 0 {
				vqNbestSign(x, resp, ss, size, e, n, bestIndex, bestDist)
			} else {
				vqNbest(x, resp, ss, size, e, n, bestIndex, bestDist)
			}
			for k := 0; k < n; k++ {
				errv := odist[j] + bestDist[k] + tener
				if errv < ndist[n-1] {
					for m := 0; m < n; m++ {
						if errv < ndist[m] {
							q := n - 1
							for ; q > m; q-- {
								ndist[q] = ndist[q-1]
								bestNind[q] = bestNind[q-1]
								bestNtarget[q] = bestNtarget[q-1]
							}
							ndist[m] = errv
							bestNind[q] = bestIndex[k]
							bestNtarget[q] = j
							break
						}
					}
				}
			}
			if i == 0 {
				break
			}
		}
		for j := 0; j < n; j++ {
			for m := (i + 1) * ss; m < nsf; m++ {
				nt[j][m] = ot[bestNtarget[j]][m]
			}
			for m := 0; m < ss; m++ {
				q := ss - m
				g := cbEntry(cb, bestNind[j], size, ss, m)
				targetUpdate(nt[j][ss*(i+1):], g, r[q:], nsf-ss*(i+1))
			}
			copy(nind[j], oind[bestNtarget[j]])
			nind[j][i] = bestNind[j]
		}
		ot, nt = nt, ot
		for j := 0; j < n; j++ {
			copy(oind[j], nind[j])
			odist[j] = ndist[j]
		}
	}
	for i := 0; i < nb; i++ {
		w.pack(uint32(nind[0][i]), par.shapeBits+par.haveSign)
	}
	for i := 0; i < nb; i++ {
		for j := 0; j < ss; j++ {
			ex[ss*i+j] = cbEntry(cb, nind[0][i], size, ss, j)
		}
	}
	for j := 0; j < nsf; j++ {
		exc[j] += ex[j]
	}
	if update {
		r2 := make([]float32, nsf)
		copy(r2, ex)
		synPercepZero16(r2, ak, awk1, awk2, r2, nsf, p)
		for j := 0; j < nsf; j++ {
			target[j] -= r2[j]
		}
	}
}

func splitCbUnquant(exc []float32, par *cbParams, nsf int, rd *bitReader, seed *uint32) {
	ss, nb := par.subvectSize, par.nbSubvect
	ind := make([]int, nb)
	signs := make([]bool, nb)
	for i := 0; i < nb; i++ {
		if par.haveSign != 0 {
			signs[i] = rd.unpack(1) != 0
		}
		ind[i] = int(rd.unpack(par.shapeBits))
	}
	for i := 0; i < nb; i++ {
		s := 1.0
		if signs[i] {
			s = -1
		}
		for j := 0; j < ss; j++ {
			exc[ss*i+j] = float32(float64(exc[ss*i+j]) + mul64(mul64(s, 0.03125), float64(par.shapeCb[ind[i]*ss+j])))
		}
	}
}

func noiseCbQuant(target, ak, awk1, awk2 []float32, par *cbParams, p, nsf int, exc, r []float32, w *bitWriter, complexity int, update bool) {
	tmp := make([]float32, nsf)
	residuePercepZero16(target, ak, awk1, awk2, tmp, nsf, p)
	for i := 0; i < nsf; i++ {
		exc[i] += tmp[i]
	}
	clear(target[:nsf])
}

func noiseCbUnquant(exc []float32, par *cbParams, nsf int, rd *bitReader, seed *uint32) {
	for i := 0; i < nsf; i++ {
		exc[i] = speexRand(1, seed)
	}
}
