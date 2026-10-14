package speex

import "math"

// The filters, linear prediction and line spectral pairs of libspeex (filters.c, lpc.c, lsp.c).

func bwLPC(gamma float32, in, out []float32, order int) {
	tmp := gamma
	for i := 0; i < order; i++ {
		out[i] = mul(tmp, in[i])
		tmp = mul(tmp, gamma)
	}
}

func sanitize(v []float32, lo, hi float32) {
	for i := range v {
		if !(v[i] >= lo && v[i] <= hi) {
			switch {
			case v[i] < lo:
				v[i] = lo
			case v[i] > hi:
				v[i] = hi
			default:
				v[i] = 0
			}
		}
	}
}

var hpPole = [5][3]float32{{1, -1.91120, 0.91498}, {1, -1.92683, 0.93071}, {1, -1.93338, 0.93553}, {1, -1.97226, 0.97332}, {1, -1.37000, 0.39900}}
var hpZero = [5][3]float32{{0.95654, -1.91309, 0.95654}, {0.96446, -1.92879, 0.96446}, {0.96723, -1.93445, 0.96723}, {0.98645, -1.97277, 0.98645}, {0.88000, -1.76000, 0.88000}}

func highpass(x, y []float32, n, id int, mem *[2]float32) {
	den, num := hpPole[id], hpZero[id]
	for i := 0; i < n; i++ {
		vout := mul(num[0], x[i]) + mem[0]
		mem[0] = mem[1] + mul(num[1], x[i]) + mul(-den[1], vout)
		mem[1] = mul(num[2], x[i]) + mul(-den[2], vout)
		y[i] = vout
	}
}

func signalMul(x, y []float32, scale float32, n int) {
	for i := 0; i < n; i++ {
		y[i] = mul(scale, x[i])
	}
}

func signalDiv(x, y []float32, scale float32, n int) {
	s := 1 / scale
	for i := 0; i < n; i++ {
		y[i] = mul(s, x[i])
	}
}

func computeRMS(x []float32, n int) float32 {
	var sum float32
	for i := 0; i < n; i++ {
		sum += mul(x[i], x[i])
	}
	return float32(math.Sqrt(.1 + float64(sum/float32(n))))
}

func filterMem16(x, num, den, y []float32, n, ord int, mem []float32) {
	for i := 0; i < n; i++ {
		xi := x[i]
		yi := x[i] + mem[0]
		nyi := -yi
		for j := 0; j < ord-1; j++ {
			mem[j] = mem[j+1] + mul(num[j], xi) + mul(den[j], nyi)
		}
		mem[ord-1] = mul(num[ord-1], xi) + mul(den[ord-1], nyi)
		y[i] = yi
	}
}

func iirMem16(x, den, y []float32, n, ord int, mem []float32) {
	for i := 0; i < n; i++ {
		yi := x[i] + mem[0]
		nyi := -yi
		for j := 0; j < ord-1; j++ {
			mem[j] = mem[j+1] + mul(den[j], nyi)
		}
		mem[ord-1] = mul(den[ord-1], nyi)
		y[i] = yi
	}
}

func firMem16(x, num, y []float32, n, ord int, mem []float32) {
	for i := 0; i < n; i++ {
		xi := x[i]
		yi := x[i] + mem[0]
		for j := 0; j < ord-1; j++ {
			mem[j] = mem[j+1] + mul(num[j], xi)
		}
		mem[ord-1] = mul(num[ord-1], xi)
		y[i] = yi
	}
}

func synPercepZero16(xx, ak, awk1, awk2, y []float32, n, ord int) {
	var mem [lpcOrder]float32
	iirMem16(xx, ak, y, n, ord, mem[:])
	mem = [lpcOrder]float32{}
	filterMem16(y, awk1, awk2, y, n, ord, mem[:])
}

func residuePercepZero16(xx, ak, awk1, awk2, y []float32, n, ord int) {
	var mem [lpcOrder]float32
	filterMem16(xx, ak, awk1, y, n, ord, mem[:])
	mem = [lpcOrder]float32{}
	firMem16(y, awk2, y, n, ord, mem[:])
}

func impulseResponse(ak, awk1, awk2, y []float32, n, ord int) {
	y[0] = 1
	i := 0
	for ; i < ord; i++ {
		y[i+1] = awk1[i]
	}
	i++
	for ; i < n; i++ {
		y[i] = 1e-15
	}
	var mem1, mem2 [lpcOrder]float32
	for i := 0; i < n; i++ {
		y1 := y[i] + mem1[0]
		ny1i := -y1
		y[i] = y1 + mem2[0]
		ny2i := -y[i]
		for j := 0; j < ord-1; j++ {
			mem1[j] = mem1[j+1] + mul(awk2[j], ny1i)
			mem2[j] = mem2[j+1] + mul(ak[j], ny2i)
		}
		mem1[ord-1] = mul(awk2[ord-1], ny1i)
		mem2[ord-1] = mul(ak[ord-1], ny2i)
	}
}

func innerProd(x, y []float32, n int) float32 {
	var sum float32
	for i := 0; i+3 < n; i += 4 {
		var part float32
		part += mul(x[i], y[i])
		part += mul(x[i+1], y[i+1])
		part += mul(x[i+2], y[i+2])
		part += mul(x[i+3], y[i+3])
		sum += part
	}
	return sum
}

var shiftFilt = [3][7]float32{
	{-0.011915, 0.046995, -0.152373, 0.614108, 0.614108, -0.152373, 0.046995},
	{-0.0324855, 0.0859768, -0.2042986, 0.9640297, 0.2086420, -0.0302054, -0.0063646},
	{-0.0063646, -0.0302054, 0.2086420, 0.9640297, -0.2042986, 0.0859768, -0.0324855},
}

// interpPitch reads buf around position at (the reference's exc pointer).
func interpPitch(buf []float32, at int, interp []float32, pitch, n int) int {
	var corr [4][7]float32
	for i := 0; i < 7; i++ {
		corr[0][i] = innerProd(buf[at:], buf[at-pitch-3+i:], n)
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 7; j++ {
			i1, i2 := max(3-j, 0), min(10-j, 7)
			var tmp float32
			for k := i1; k < i2; k++ {
				tmp += mul(shiftFilt[i][k], corr[0][j+k-3])
			}
			corr[i+1][j] = tmp
		}
	}
	maxi, maxj := 0, 0
	maxcorr := corr[0][0]
	for i := 0; i < 4; i++ {
		for j := 0; j < 7; j++ {
			if corr[i][j] > maxcorr {
				maxcorr, maxi, maxj = corr[i][j], i, j
			}
		}
	}
	for i := 0; i < n; i++ {
		var tmp float32
		if maxi > 0 {
			for k := 0; k < 7; k++ {
				tmp += mul(buf[at+i-(pitch-maxj+3)+k-3], shiftFilt[maxi-1][k])
			}
		} else {
			tmp = buf[at+i-(pitch-maxj+3)]
		}
		interp[i] = tmp
	}
	return pitch - maxj + 3
}

// multicomb is the decoder's pitch enhancer over 80 samples at buf[at:].
func multicomb(buf []float32, at int, out []float32, nsf, pitch, maxPitch int, combGain float32) {
	exc := buf[at:]
	iexc := make([]float32, 2*nsf)
	interpPitch(buf, at, iexc, pitch, 80)
	if pitch > maxPitch {
		interpPitch(buf, at, iexc[nsf:], 2*pitch, 80)
	} else {
		interpPitch(buf, at, iexc[nsf:], -pitch, 80)
	}
	iexc0 := float32(math.Sqrt(float64(1000 + innerProd(iexc, iexc, nsf))))
	iexc1 := float32(math.Sqrt(float64(1000 + innerProd(iexc[nsf:], iexc[nsf:], nsf))))
	excMag := float32(math.Sqrt(float64(1 + innerProd(exc, exc, nsf))))
	corr0 := innerProd(iexc, exc, nsf)
	if corr0 < 0 {
		corr0 = 0
	}
	corr1 := innerProd(iexc[nsf:], exc, nsf)
	if corr1 < 0 {
		corr1 = 0
	}
	var pgain1, pgain2 float32
	if corr0 > mul(iexc0, excMag) {
		pgain1 = 1
	} else {
		pgain1 = corr0 / excMag / iexc0
	}
	if corr1 > mul(iexc1, excMag) {
		pgain2 = 1
	} else {
		pgain2 = corr1 / excMag / iexc1
	}
	gg1, gg2 := excMag/iexc0, excMag/iexc1
	var c1, c2 float32
	if combGain > 0 {
		c1 = float32(mul64(.4, float64(combGain)) + .07)
		c2 = float32(.5 + mul64(1.72, float64(c1)-.07))
	}
	g1 := 1 - mul(mul(c2, pgain1), pgain1)
	g2 := 1 - mul(mul(c2, pgain2), pgain2)
	if g1 < c1 {
		g1 = c1
	}
	if g2 < c1 {
		g2 = c1
	}
	g1, g2 = c1/g1, c1/g2
	var gain0, gain1 float32
	if pitch > maxPitch {
		gain0 = float32(mul64(.7, float64(mul(g1, gg1))))
		gain1 = float32(mul64(.3, float64(mul(g2, gg2))))
	} else {
		gain0 = float32(mul64(.6, float64(mul(g1, gg1))))
		gain1 = float32(mul64(.6, float64(mul(g2, gg2))))
	}
	for i := 0; i < nsf; i++ {
		out[i] = exc[i] + (mul(gain0, iexc[i]) + mul(gain1, iexc[i+nsf]))
	}
	newEner := max(computeRMS(out, nsf), 1)
	oldEner := max(computeRMS(exc, nsf), 1)
	oldEner = min(oldEner, newEner)
	ngain := oldEner / newEner
	for i := 0; i < nsf; i++ {
		out[i] = mul(ngain, out[i])
	}
}

func autocorrelate(x, ac []float32, lag, n int) {
	for lag > 0 {
		lag--
		var d float32
		for i := lag; i < n; i++ {
			d += mul(x[i], x[i-lag])
		}
		ac[lag] = d
	}
	ac[0] += 10
}

func levinson(lpc, ac []float32, p int) float32 {
	errv := ac[0]
	for i := 0; i < p; i++ {
		rr := -ac[i+1]
		for j := 0; j < i; j++ {
			rr -= mul(lpc[j], ac[i-j])
		}
		r := float32(float64(rr) / (float64(errv) + mul64(.003, float64(ac[0]))))
		lpc[i] = r
		for j := 0; j < (i+1)>>1; j++ {
			t1, t2 := lpc[j], lpc[i-1-j]
			lpc[j] = t1 + mul(r, t2)
			lpc[i-1-j] = t2 + mul(r, t1)
		}
		errv -= mul(r, mul(errv, r))
	}
	return errv
}

func chebPolyEva(coef []float32, x float32, m int) float32 {
	var b0, b1 float32
	x *= 2
	for k := m; k > 0; k-- {
		tmp := b0
		b0 = mul(x, b0) - b1 + coef[m-k]
		b1 = tmp
	}
	return float32(-float64(b1) + mul64(mul64(.5, float64(x)), float64(b0)) + float64(coef[m]))
}

func lpcToLSP(a []float32, order int, freq []float32, nb int, delta float32) int {
	m := order / 2
	p := make([]float32, m+1)
	q := make([]float32, m+1)
	p[0], q[0] = 1, 1
	for i := 0; i < m; i++ {
		p[i+1] = (a[i] + a[order-1-i]) - p[i]
		q[i+1] = (a[i] - a[order-1-i]) + q[i]
	}
	for i := 0; i < m; i++ {
		p[i] *= 2
		q[i] *= 2
	}
	var xr, xm float32
	xl := float32(1)
	roots := 0
	for j := 0; j < order; j++ {
		pt := p
		if j&1 != 0 {
			pt = q
		}
		psuml := chebPolyEva(pt, xl, m)
		for float64(xr) >= -1 {
			dd := float32(float64(delta) * (1 - mul64(mul64(.9, float64(xl)), float64(xl))))
			if math.Abs(float64(psuml)) < .2 {
				dd = float32(float64(dd) * .5)
			}
			xr = xl - dd
			psumr := chebPolyEva(pt, xr, m)
			tpsumr, txr := psumr, xr
			if mul(psumr, psuml) < 0 {
				roots++
				for k := 0; k <= nb; k++ {
					xm = float32(mul64(.5, float64(xl+xr)))
					psumm := chebPolyEva(pt, xm, m)
					if !(mul(psumm, psuml) < 0) {
						psuml, xl = psumm, xm
					} else {
						xr = xm
					}
				}
				freq[j] = float32(math.Acos(float64(xm)))
				xl = xm
				break
			}
			psuml, xl = tpsumr, txr
		}
	}
	return roots
}

func lspToLPC(freq, ak []float32, order int) {
	m := order >> 1
	wp := make([]float32, 4*m+2)
	var xf [lpcOrder]float32
	for i := 0; i < order; i++ {
		xf[i] = spxCos(freq[i])
	}
	xin1, xin2 := float32(1), float32(1)
	n4 := 0
	for j := 0; j <= order; j++ {
		for i, i2 := 0, 0; i < m; i, i2 = i+1, i2+2 {
			n1 := i * 4
			n2, n3 := n1+1, n1+2
			n4 = n1 + 3
			xout1 := xin1 - mul(2*xf[i2], wp[n1]) + wp[n2]
			xout2 := xin2 - mul(2*xf[i2+1], wp[n3]) + wp[n4]
			wp[n2] = wp[n1]
			wp[n4] = wp[n3]
			wp[n1] = xin1
			wp[n3] = xin2
			xin1, xin2 = xout1, xout2
		}
		xout1 := xin1 + wp[n4+1]
		xout2 := xin2 - wp[n4+2]
		if j > 0 {
			ak[j-1] = mul(xout1+xout2, 0.5)
		}
		wp[n4+1] = xin1
		wp[n4+2] = xin2
		xin1, xin2 = 0, 0
	}
}

func lspInterpolate(old, cur, lsp []float32, n, sub, nbSub int, margin float32) {
	tmp := (1 + float32(sub)) / float32(nbSub)
	for i := 0; i < n; i++ {
		lsp[i] = mul(1-tmp, old[i]) + mul(tmp, cur[i])
	}
	if lsp[0] < margin {
		lsp[0] = margin
	}
	if float64(lsp[n-1]) > math.Pi-float64(margin) {
		lsp[n-1] = float32(math.Pi - float64(margin))
	}
	for i := 1; i < n-1; i++ {
		if lsp[i] < lsp[i-1]+margin {
			lsp[i] = lsp[i-1] + margin
		}
		if lsp[i] > lsp[i+1]-margin {
			lsp[i] = mul(.5, lsp[i]+lsp[i+1]-margin)
		}
	}
}
