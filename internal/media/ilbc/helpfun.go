package ilbc

// Translations of helpfun.c, filter.c, anaFilter.c, syntFilter.c, hpInput.c and hpOutput.c.
//
// A product is converted to float32 before it is added (float32(a*b)): Go may otherwise fuse a
// multiply and an add into one instruction, which rounds differently from the reference, compiled
// without contraction. C pointers that run backwards (In[-k]) are a slice and an index here.

func mul(a, b float32) float32 { return float32(a * b) }

func autocorr(r []float32, x []float32, n, order int) {
	for lag := 0; lag <= order; lag++ {
		var sum float32
		for i := 0; i < n-lag; i++ {
			sum += mul(x[i], x[i+lag])
		}
		r[lag] = sum
	}
}

func window(z, x, y []float32, n int) {
	for i := 0; i < n; i++ {
		z[i] = mul(x[i], y[i])
	}
}

func levdurb(a, k, r []float32, order int) {
	a[0] = 1.0
	if r[0] < eps {
		for i := 0; i < order; i++ {
			k[i] = 0
			a[i+1] = 0
		}
		return
	}
	k[0] = -r[1] / r[0]
	a[1] = k[0]
	alpha := r[0] + mul(r[1], k[0])
	for m := 1; m < order; m++ {
		sum := r[m+1]
		for i := 0; i < m; i++ {
			sum += mul(a[i+1], r[m-i])
		}
		k[m] = -sum / alpha
		alpha += mul(k[m], sum)
		mh := (m + 1) >> 1
		for i := 0; i < mh; i++ {
			s := a[i+1] + mul(k[m], a[m-i])
			a[m-i] += mul(k[m], a[i+1])
			a[i+1] = s
		}
		a[m+1] = k[m]
	}
}

func interpolate(out, in1, in2 []float32, coef float32, length int) {
	invcoef := float32(1.0) - coef
	for i := 0; i < length; i++ {
		out[i] = mul(coef, in1[i]) + mul(invcoef, in2[i])
	}
}

func bwexpand(out, in []float32, coef float32, length int) {
	chirp := coef
	out[0] = in[0]
	for i := 1; i < length; i++ {
		out[i] = mul(chirp, in[i])
		chirp = mul(chirp, coef)
	}
}

func vq(xq []float32, index *int, cb []float32, x []float32, nCb, dim int) {
	pos := 0
	mindist := floatMax
	minindex := 0
	for j := 0; j < nCb; j++ {
		dist := x[0] - cb[pos]
		dist = mul(dist, dist)
		for i := 1; i < dim; i++ {
			tmp := x[i] - cb[pos+i]
			dist += mul(tmp, tmp)
		}
		if dist < mindist {
			mindist = dist
			minindex = j
		}
		pos += dim
	}
	for i := 0; i < dim; i++ {
		xq[i] = cb[minindex*dim+i]
	}
	*index = minindex
}

func splitVQ(qX []float32, index []int, x []float32, cb []float32, nsplit int, dim, cbsize []int) {
	cbPos, xPos := 0, 0
	for i := 0; i < nsplit; i++ {
		vq(qX[xPos:], &index[i], cb[cbPos:], x[xPos:], cbsize[i], dim[i])
		xPos += dim[i]
		cbPos += dim[i] * cbsize[i]
	}
}

func sortSq(xq *float32, index *int, x float32, cb []float32, cbSize int) {
	if x <= cb[0] {
		*index = 0
		*xq = cb[0]
		return
	}
	i := 0
	for x > cb[i] && i < cbSize-1 {
		i++
	}
	if x > (cb[i]+cb[i-1])/2 {
		*index = i
		*xq = cb[i]
	} else {
		*index = i - 1
		*xq = cb[i-1]
	}
}

func lsfCheck(lsf []float32, dim, noAn int) int {
	const (
		epsL   = float32(0.039)
		eps2   = float32(0.0195)
		maxlsf = float32(3.14)
		minlsf = float32(0.01)
	)
	change := 0
	for n := 0; n < 2; n++ {
		for m := 0; m < noAn; m++ {
			for k := 0; k < dim-1; k++ {
				pos := m*dim + k
				if lsf[pos+1]-lsf[pos] < epsL {
					if lsf[pos+1] < lsf[pos] {
						lsf[pos+1] = lsf[pos] + eps2
						lsf[pos] = lsf[pos+1] - eps2
					} else {
						lsf[pos] -= eps2
						lsf[pos+1] += eps2
					}
					change = 1
				}
				if lsf[pos] < minlsf {
					lsf[pos] = minlsf
					change = 1
				}
				if lsf[pos] > maxlsf {
					lsf[pos] = maxlsf
					change = 1
				}
			}
		}
	}
	return change
}

// allPoleFilter filters buf[at:at+n] in place; it reads the orderCoef samples before at.
func allPoleFilter(buf []float32, at int, coef []float32, n, orderCoef int) {
	for i := 0; i < n; i++ {
		for k := 1; k <= orderCoef; k++ {
			buf[at+i] -= mul(coef[k], buf[at+i-k])
		}
	}
}

// allZeroFilter filters in[at:at+n] into out[oat:]; it reads the orderCoef samples before at.
func allZeroFilter(in []float32, at int, coef []float32, n, orderCoef int, out []float32, oat int) {
	for i := 0; i < n; i++ {
		o := mul(coef[0], in[at+i])
		for k := 1; k <= orderCoef; k++ {
			o += mul(coef[k], in[at+i-k])
		}
		out[oat+i] = o
	}
}

// zeroPoleFilter: out needs orderCoef samples of history before oat.
func zeroPoleFilter(in []float32, at int, zeroCoef, poleCoef []float32, n, orderCoef int, out []float32, oat int) {
	allZeroFilter(in, at, zeroCoef, n, orderCoef, out, oat)
	allPoleFilter(out, oat, poleCoef, n, orderCoef)
}

func downSample(in []float32, coef []float32, lengthIn int, state []float32, out []float32) {
	op := 0
	for i := delayDS; i < lengthIn; i += factorDS {
		ci := 0
		ii := i
		si := filterorderDS - 2
		var o float32
		stop := filterorderDS
		if i < filterorderDS {
			stop = i + 1
		}
		for j := 0; j < stop; j++ {
			o += mul(coef[ci], in[ii])
			ci++
			ii--
		}
		for j := i + 1; j < filterorderDS; j++ {
			o += mul(coef[ci], state[si])
			ci++
			si--
		}
		out[op] = o
		op++
	}
	for i := lengthIn + factorDS; i < lengthIn+delayDS; i += factorDS {
		var o float32
		if i < lengthIn {
			ci := 0
			for j := 0; j < filterorderDS; j++ {
				o += mul(coef[ci], out[op])
				ci++
				op--
			}
		} else {
			ci := i - lengthIn
			ii := lengthIn - 1
			for j := 0; j < filterorderDS-(i-lengthIn); j++ {
				o += mul(coef[ci], in[ii])
				ci++
				ii--
			}
		}
		out[op] = o
		op++
	}
}

func anaFilter(in []float32, a []float32, length int, out []float32, mem []float32) {
	for i := 0; i < lpcFilterorder; i++ {
		pi := i
		pm := lpcFilterorder - 1
		pa := 0
		var o float32
		for j := 0; j <= i; j++ {
			o += mul(a[pa], in[pi])
			pa++
			pi--
		}
		for j := i + 1; j < lpcFilterorder+1; j++ {
			o += mul(a[pa], mem[pm])
			pa++
			pm--
		}
		out[i] = o
	}
	for i := lpcFilterorder; i < length; i++ {
		pi := i
		var o float32
		for j := 0; j < lpcFilterorder+1; j++ {
			o += mul(a[j], in[pi])
			pi--
		}
		out[i] = o
	}
	copy(mem[:lpcFilterorder], in[length-lpcFilterorder:length])
}

func syntFilter(out []float32, a []float32, length int, mem []float32) {
	for i := 0; i < lpcFilterorder; i++ {
		pi := i - 1
		pa := 1
		pm := lpcFilterorder - 1
		for j := 1; j <= i; j++ {
			out[i] -= mul(a[pa], out[pi])
			pa++
			pi--
		}
		for j := i + 1; j < lpcFilterorder+1; j++ {
			out[i] -= mul(a[pa], mem[pm])
			pa++
			pm--
		}
	}
	for i := lpcFilterorder; i < length; i++ {
		pi := i - 1
		for j := 1; j < lpcFilterorder+1; j++ {
			out[i] -= mul(a[j], out[pi])
			pi--
		}
	}
	copy(mem[:lpcFilterorder], out[length-lpcFilterorder:length])
}

func hpFilter(zero, pole []float32, in []float32, length int, out []float32, mem []float32) {
	for i := 0; i < length; i++ {
		o := mul(zero[0], in[i])
		o += mul(zero[1], mem[0])
		o += mul(zero[2], mem[1])
		out[i] = o
		mem[1] = mem[0]
		mem[0] = in[i]
	}
	for i := 0; i < length; i++ {
		out[i] -= mul(pole[1], mem[2])
		out[i] -= mul(pole[2], mem[3])
		mem[3] = mem[2]
		mem[2] = out[i]
	}
}

func hpInput(in []float32, length int, out []float32, mem []float32) {
	hpFilter(hpi_zero_coefsTbl, hpi_pole_coefsTbl, in, length, out, mem)
}

func hpOutput(in []float32, length int, out []float32, mem []float32) {
	hpFilter(hpo_zero_coefsTbl, hpo_pole_coefsTbl, in, length, out, mem)
}
