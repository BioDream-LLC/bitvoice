// Package mp3 decodes MPEG-1, 2 and 2.5 Layer III audio (MP3), for hold music uploaded as MP3 and
// streamed from internet radio. It is a Go translation of minimp3 (github.com/lieff/minimp3, by its
// authors dedicated to the public domain under CC0 1.0), its portable floating-point code, with every
// product rounded on its own as C does without fused multiply-add, so it decodes to the same samples.
package mp3

// The header macros of minimp3.
func hdrIsMono(h []byte) bool       { return h[3]&0xC0 == 0xC0 }
func hdrIsMSStereo(h []byte) bool   { return h[3]&0xE0 == 0x60 }
func hdrIsFreeFormat(h []byte) bool { return h[2]&0xF0 == 0 }
func hdrIsCRC(h []byte) bool        { return h[1]&1 == 0 }
func hdrTestPadding(h []byte) bool  { return h[2]&0x2 != 0 }
func hdrTestMPEG1(h []byte) bool    { return h[1]&0x8 != 0 }
func hdrTestNotMPEG25(h []byte) bool {
	return h[1]&0x10 != 0
}
func hdrTestIStereo(h []byte) bool  { return h[3]&0x10 != 0 }
func hdrTestMSStereo(h []byte) bool { return h[3]&0x20 != 0 }
func hdrGetLayer(h []byte) int      { return int(h[1]>>1) & 3 }
func hdrGetBitrate(h []byte) int    { return int(h[2] >> 4) }
func hdrGetSampleRate(h []byte) int { return int(h[2]>>2) & 3 }
func hdrGetMySampleRate(h []byte) int {
	return hdrGetSampleRate(h) + (int(h[1]>>3)&1+int(h[1]>>4)&1)*3
}
func hdrIsFrame576(h []byte) bool { return h[1]&14 == 2 }
func hdrIsLayer1(h []byte) bool   { return h[1]&6 == 6 }

const (
	maxFreeFormatFrameSize = 2304
	maxFrameSyncMatches    = 10
	maxL3FramePayloadBytes = maxFreeFormatFrameSize
	maxBitReservoirBytes   = 511
	shortBlockType         = 2
	stopBlockType          = 3
	hdrSize                = 4
	maxSCFI                = 44 // (MAX_SCF + 3) & ~3, MAX_SCF = 255 - 4 - 210
	// MaxSamplesPerFrame is the most samples (both channels) one frame gives.
	MaxSamplesPerFrame = 1152 * 2
)

type bitstream struct {
	buf        []byte
	pos, limit int
}

func (bs *bitstream) init(data []byte, bytes int) {
	bs.buf, bs.pos, bs.limit = data, 0, bytes*8
}

func (bs *bitstream) byteAt(i int) uint32 {
	if i < len(bs.buf) {
		return uint32(bs.buf[i])
	}
	return 0
}

func (bs *bitstream) get(n int) uint32 {
	s := uint32(bs.pos & 7)
	shl := n + int(s)
	p := bs.pos >> 3
	if bs.pos += n; bs.pos > bs.limit {
		return 0
	}
	var cache uint32
	next := bs.byteAt(p) & (255 >> s)
	p++
	for shl -= 8; shl > 0; shl -= 8 {
		cache |= next << uint(shl)
		next = bs.byteAt(p)
		p++
	}
	return cache | next>>uint(-shl)
}

func hdrValid(h []byte) bool {
	return h[0] == 0xff && (h[1]&0xF0 == 0xf0 || h[1]&0xFE == 0xe2) && hdrGetLayer(h) != 0 &&
		hdrGetBitrate(h) != 15 && hdrGetSampleRate(h) != 3
}

func hdrCompare(h1, h2 []byte) bool {
	return hdrValid(h2) && (h1[1]^h2[1])&0xFE == 0 && (h1[2]^h2[2])&0x0C == 0 && hdrIsFreeFormat(h1) == hdrIsFreeFormat(h2)
}

var halfrate = [2][3][15]uint8{
	{{0, 4, 8, 12, 16, 20, 24, 28, 32, 40, 48, 56, 64, 72, 80}, {0, 4, 8, 12, 16, 20, 24, 28, 32, 40, 48, 56, 64, 72, 80}, {0, 16, 24, 28, 32, 40, 48, 56, 64, 72, 80, 88, 96, 112, 128}},
	{{0, 16, 20, 24, 28, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160}, {0, 16, 24, 28, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192}, {0, 16, 32, 48, 64, 80, 96, 112, 128, 144, 160, 176, 192, 208, 224}},
}

func hdrBitrateKbps(h []byte) int {
	m := 0
	if hdrTestMPEG1(h) {
		m = 1
	}
	return 2 * int(halfrate[m][hdrGetLayer(h)-1][hdrGetBitrate(h)])
}

func hdrSampleRateHz(h []byte) int {
	hz := [3]int{44100, 48000, 32000}[hdrGetSampleRate(h)]
	if !hdrTestMPEG1(h) {
		hz >>= 1
	}
	if !hdrTestNotMPEG25(h) {
		hz >>= 1
	}
	return hz
}

func hdrFrameSamples(h []byte) int {
	if hdrIsLayer1(h) {
		return 384
	}
	if hdrIsFrame576(h) {
		return 576
	}
	return 1152
}

func hdrFrameBytes(h []byte, freeFormatSize int) int {
	fb := hdrFrameSamples(h) * hdrBitrateKbps(h) * 125 / hdrSampleRateHz(h)
	if hdrIsLayer1(h) {
		fb &^= 3
	}
	if fb == 0 {
		return freeFormatSize
	}
	return fb
}

func hdrPadding(h []byte) int {
	if !hdrTestPadding(h) {
		return 0
	}
	if hdrIsLayer1(h) {
		return 4
	}
	return 1
}

type grInfo struct {
	sfbtab                                                     []uint8
	part23Length, bigValues, scalefacCompress                  uint16
	globalGain, blockType, mixedBlockFlag, nLongSfb, nShortSfb uint8
	tableSelect, regionCount, subblockGain                     [3]uint8
	preflag, scalefacScale, count1Table, scfsi                 uint8
}

type scratch struct {
	bs       bitstream
	maindata [maxBitReservoirBytes + maxL3FramePayloadBytes]byte
	gr       [4]grInfo
	grbuf    [2 * 576]float32
	scf      [40]float32
	syn      [(18 + 15) * 2 * 32]float32
	istPos   [2][39]uint8
}

// Decoder keeps the state between frames: the overlap of the transforms, the filter bank, the bit
// reservoir.
type Decoder struct {
	mdctOverlap     [2][9 * 32]float32
	qmfState        [15 * 2 * 32]float32
	reserv          int
	freeFormatBytes int
	header          [4]byte
	reservBuf       [511]byte
	s               scratch
}

func readSideInfo(bs *bitstream, gr []grInfo, hdr []byte) int {
	var tables, scfsi uint32
	var mainDataBegin, part23Sum int
	srIdx := hdrGetMySampleRate(hdr)
	if srIdx != 0 {
		srIdx--
	}
	grCount := 2
	if hdrIsMono(hdr) {
		grCount = 1
	}
	if hdrTestMPEG1(hdr) {
		grCount *= 2
		mainDataBegin = int(bs.get(9))
		scfsi = bs.get(7 + grCount)
	} else {
		mainDataBegin = int(bs.get(8+grCount) >> uint(grCount))
	}
	for g := 0; g < grCount; g++ {
		gi := &gr[g]
		if hdrIsMono(hdr) {
			scfsi <<= 4
		}
		gi.part23Length = uint16(bs.get(12))
		part23Sum += int(gi.part23Length)
		gi.bigValues = uint16(bs.get(9))
		if gi.bigValues > 288 {
			return -1
		}
		gi.globalGain = uint8(bs.get(8))
		if hdrTestMPEG1(hdr) {
			gi.scalefacCompress = uint16(bs.get(4))
		} else {
			gi.scalefacCompress = uint16(bs.get(9))
		}
		gi.sfbtab = scfLong[srIdx][:]
		gi.nLongSfb, gi.nShortSfb = 22, 0
		if bs.get(1) != 0 {
			gi.blockType = uint8(bs.get(2))
			if gi.blockType == 0 {
				return -1
			}
			gi.mixedBlockFlag = uint8(bs.get(1))
			gi.regionCount[0], gi.regionCount[1] = 7, 255
			if gi.blockType == shortBlockType {
				scfsi &= 0x0F0F
				if gi.mixedBlockFlag == 0 {
					gi.regionCount[0] = 8
					gi.sfbtab = scfShort[srIdx][:]
					gi.nLongSfb, gi.nShortSfb = 0, 39
				} else {
					gi.sfbtab = scfMixed[srIdx][:]
					gi.nLongSfb = 6
					if hdrTestMPEG1(hdr) {
						gi.nLongSfb = 8
					}
					gi.nShortSfb = 30
				}
			}
			tables = bs.get(10)
			tables <<= 5
			gi.subblockGain[0] = uint8(bs.get(3))
			gi.subblockGain[1] = uint8(bs.get(3))
			gi.subblockGain[2] = uint8(bs.get(3))
		} else {
			gi.blockType, gi.mixedBlockFlag = 0, 0
			tables = bs.get(15)
			gi.regionCount[0] = uint8(bs.get(4))
			gi.regionCount[1] = uint8(bs.get(3))
			gi.regionCount[2] = 255
		}
		gi.tableSelect[0] = uint8(tables >> 10)
		gi.tableSelect[1] = uint8((tables >> 5) & 31)
		gi.tableSelect[2] = uint8(tables & 31)
		if hdrTestMPEG1(hdr) {
			gi.preflag = uint8(bs.get(1))
		} else if gi.scalefacCompress >= 500 {
			gi.preflag = 1
		} else {
			gi.preflag = 0
		}
		gi.scalefacScale = uint8(bs.get(1))
		gi.count1Table = uint8(bs.get(1))
		gi.scfsi = uint8((scfsi >> 12) & 15)
		scfsi <<= 4
	}
	if part23Sum+bs.pos > bs.limit+mainDataBegin*8 {
		return -1
	}
	return mainDataBegin
}

func readScalefactors(scf, istPos []uint8, scfSize, scfCount []uint8, bs *bitstream, scfsi int) {
	si, ii := 0, 0
	for i := 0; i < 4 && scfCount[i] != 0; i, scfsi = i+1, scfsi*2 {
		cnt := int(scfCount[i])
		if scfsi&8 != 0 {
			copy(scf[si:si+cnt], istPos[ii:ii+cnt])
		} else {
			bits := int(scfSize[i])
			if bits == 0 {
				for k := 0; k < cnt; k++ {
					scf[si+k], istPos[ii+k] = 0, 0
				}
			} else {
				maxScf := -1
				if scfsi < 0 {
					maxScf = 1<<bits - 1
				}
				for k := 0; k < cnt; k++ {
					s := int(bs.get(bits))
					if s == maxScf {
						istPos[ii+k] = 0xFF // -1
					} else {
						istPos[ii+k] = uint8(s)
					}
					scf[si+k] = uint8(s)
				}
			}
		}
		ii += cnt
		si += cnt
	}
	scf[si], scf[si+1], scf[si+2] = 0, 0, 0
}

var expFrac = [4]float32{9.31322575e-10, 7.83145814e-10, 6.58544508e-10, 5.53767716e-10}

func ldexpQ2(y float32, expQ2 int) float32 {
	for {
		e := min(30*4, expQ2)
		f := float32(expFrac[e&3] * float32(int32(1<<30)>>uint(e>>2)))
		y = float32(y * f)
		if expQ2 -= e; expQ2 <= 0 {
			break
		}
	}
	return y
}

var scfcDecode = [16]uint8{0, 1, 2, 3, 12, 5, 6, 7, 9, 10, 11, 13, 14, 15, 18, 19}
var scfMod = [6 * 4]uint8{5, 5, 4, 4, 5, 5, 4, 1, 4, 3, 1, 1, 5, 6, 6, 1, 4, 4, 4, 1, 4, 3, 1, 1}
var preamp = [10]uint8{1, 1, 1, 1, 2, 2, 3, 3, 3, 2}

func decodeScalefactors(hdr []byte, istPos []uint8, bs *bitstream, gr *grInfo, scf []float32, ch int) {
	pi := 0
	if gr.nShortSfb != 0 {
		pi++
	}
	if gr.nLongSfb == 0 {
		pi++
	}
	part := scfPartitions[pi][:]
	var scfSize [4]uint8
	var iscf [42]uint8 // 40 in minimp3, which may write two past the end
	scfShift := int(gr.scalefacScale) + 1
	scfsi := int(gr.scfsi)
	if hdrTestMPEG1(hdr) {
		p := scfcDecode[gr.scalefacCompress]
		scfSize[1], scfSize[0] = p>>2, p>>2
		scfSize[3], scfSize[2] = p&3, p&3
	} else {
		ist := 0
		if hdrTestIStereo(hdr) && ch != 0 {
			ist = 1
		}
		sfc := int(gr.scalefacCompress) >> uint(ist)
		k := ist * 3 * 4
		for sfc >= 0 {
			modprod := 1
			for i := 3; i >= 0; i-- {
				scfSize[i] = uint8(sfc / modprod % int(scfMod[k+i]))
				modprod *= int(scfMod[k+i])
			}
			sfc -= modprod
			k += 4
		}
		part = part[k:]
		scfsi = -16
	}
	readScalefactors(iscf[:], istPos, scfSize[:], part, bs, scfsi)
	if gr.nShortSfb != 0 {
		sh := uint(3 - scfShift)
		for i := 0; i < int(gr.nShortSfb); i += 3 {
			iscf[int(gr.nLongSfb)+i+0] += gr.subblockGain[0] << sh
			iscf[int(gr.nLongSfb)+i+1] += gr.subblockGain[1] << sh
			iscf[int(gr.nLongSfb)+i+2] += gr.subblockGain[2] << sh
		}
	} else if gr.preflag != 0 {
		for i := 0; i < 10; i++ {
			iscf[11+i] += preamp[i]
		}
	}
	gainExp := int(gr.globalGain) - 4 - 210
	if hdrIsMSStereo(hdr) {
		gainExp -= 2
	}
	gain := ldexpQ2(float32(1<<(maxSCFI/4)), maxSCFI-gainExp)
	for i := 0; i < int(gr.nLongSfb)+int(gr.nShortSfb); i++ {
		scf[i] = ldexpQ2(gain, int(iscf[i])<<uint(scfShift))
	}
}

func pow43f(x int) float32 {
	mult := float32(256)
	if x < 129 {
		return pow43[16+x]
	}
	if x < 1024 {
		mult = 16
		x <<= 3
	}
	sign := 2 * x & 64
	frac := float32((x&63)-sign) / float32((x&^63)+sign)
	t := float32(frac * float32(2.0/9))
	u := float32(4.0/3) + t
	v := float32(frac * u)
	w := 1 + v
	return float32(float32(pow43[16+((x+sign)>>6)]*w) * mult)
}

func huffman(dst []float32, bs *bitstream, gr *grInfo, scf []float32, limit int) {
	var one float32
	ireg, bigValCnt := 0, int(gr.bigValues)
	sfb := gr.sfbtab
	sfbi, scfi, di := 0, 0, 0
	next := bs.pos / 8
	cache := (((bs.byteAt(next)*256+bs.byteAt(next+1))*256+bs.byteAt(next+2))*256 + bs.byteAt(next+3)) << uint(bs.pos&7)
	sh := (bs.pos & 7) - 8
	next += 4
	peek := func(n int) uint32 { return cache >> uint(32-n) }
	flush := func(n int) { cache <<= uint(n); sh += n }
	check := func() {
		for sh >= 0 {
			cache |= bs.byteAt(next) << uint(sh)
			next++
			sh -= 8
		}
	}
	for bigValCnt > 0 {
		tabNum := int(gr.tableSelect[ireg])
		sfbCnt := int(gr.regionCount[ireg])
		ireg++
		cb := huffTabs[tabIndex[tabNum]:]
		linbits := int(linBits[tabNum])
		for {
			np := int(sfb[sfbi]) / 2
			sfbi++
			pairs := min(bigValCnt, np)
			one = scf[scfi]
			scfi++
			for {
				w := 5
				leaf := int(cb[peek(w)])
				for leaf < 0 {
					flush(w)
					w = leaf & 7
					leaf = int(cb[int(peek(w))-(leaf>>3)])
				}
				flush(leaf >> 8)
				for j := 0; j < 2; j++ {
					lsb := leaf & 0x0F
					if linbits != 0 && lsb == 15 {
						lsb += int(peek(linbits))
						flush(linbits)
						check()
						sign := float32(1)
						if int32(cache) < 0 {
							sign = -1
						}
						dst[di] = float32(float32(one*pow43f(lsb)) * sign)
					} else {
						dst[di] = float32(pow43[16+lsb-16*int(cache>>31)] * one)
					}
					if lsb != 0 {
						flush(1)
					}
					di++
					leaf >>= 4
				}
				check()
				if pairs--; pairs == 0 {
					break
				}
			}
			if bigValCnt -= np; bigValCnt <= 0 {
				break
			}
			if sfbCnt--; sfbCnt < 0 {
				break
			}
		}
	}
	np := 1 - bigValCnt
	for ; ; di += 4 {
		cb := tab32[:]
		if gr.count1Table != 0 {
			cb = tab33[:]
		}
		leaf := int(cb[peek(4)])
		if leaf&8 == 0 {
			leaf = int(cb[(leaf>>3)+int(cache<<4>>uint(32-(leaf&3)))])
		}
		flush(leaf & 7)
		if next*8-24+sh > limit {
			break
		}
		reload := func() bool {
			if np--; np == 0 {
				np = int(sfb[sfbi]) / 2
				sfbi++
				if np == 0 {
					return false
				}
				one = scf[scfi]
				scfi++
			}
			return true
		}
		deq := func(s int) {
			if leaf&(128>>uint(s)) != 0 {
				if int32(cache) < 0 {
					dst[di+s] = -one
				} else {
					dst[di+s] = one
				}
				flush(1)
			}
		}
		if !reload() {
			break
		}
		deq(0)
		deq(1)
		if !reload() {
			break
		}
		deq(2)
		deq(3)
		check()
	}
	bs.pos = limit
}

func midsideStereo(buf []float32, off, n int) {
	for i := 0; i < n; i++ {
		a, b := buf[off+i], buf[off+576+i]
		buf[off+i] = a + b
		buf[off+576+i] = a - b
	}
}

func intensityStereoBand(buf []float32, off, n int, kl, kr float32) {
	for i := 0; i < n; i++ {
		buf[off+i+576] = float32(buf[off+i] * kr)
		buf[off+i] = float32(buf[off+i] * kl)
	}
}

func stereoTopBand(right []float32, sfb []uint8, nbands int, maxBand *[3]int) {
	maxBand[0], maxBand[1], maxBand[2] = -1, -1, -1
	r := 0
	for i := 0; i < nbands; i++ {
		for k := 0; k < int(sfb[i]); k += 2 {
			if right[r+k] != 0 || right[r+k+1] != 0 {
				maxBand[i%3] = i
				break
			}
		}
		r += int(sfb[i])
	}
}

var pan = [7 * 2]float32{0, 1, 0.21132487, 0.78867513, 0.36602540, 0.63397460, 0.5, 0.5, 0.63397460, 0.36602540, 0.78867513, 0.21132487, 1, 0}

func stereoProcess(buf []float32, istPos []uint8, sfb []uint8, hdr []byte, maxBand [3]int, mpeg2sh int) {
	maxPos := uint(64)
	if hdrTestMPEG1(hdr) {
		maxPos = 7
	}
	off := 0
	for i := 0; sfb[i] != 0; i++ {
		ipos := uint(istPos[i])
		if i > maxBand[i%3] && ipos < maxPos {
			var kl, kr float32
			s := float32(1)
			if hdrTestMSStereo(hdr) {
				s = 1.41421356
			}
			if hdrTestMPEG1(hdr) {
				kl, kr = pan[2*ipos], pan[2*ipos+1]
			} else {
				kl = 1
				kr = ldexpQ2(1, int((ipos+1)>>1)<<uint(mpeg2sh))
				if ipos&1 != 0 {
					kl, kr = kr, 1
				}
			}
			intensityStereoBand(buf, off, int(sfb[i]), float32(kl*s), float32(kr*s))
		} else if hdrTestMSStereo(hdr) {
			midsideStereo(buf, off, int(sfb[i]))
		}
		off += int(sfb[i])
	}
}

func intensityStereo(buf []float32, istPos []uint8, gr []grInfo, hdr []byte) {
	var maxBand [3]int
	nSfb := int(gr[0].nLongSfb) + int(gr[0].nShortSfb)
	maxBlocks := 1
	if gr[0].nShortSfb != 0 {
		maxBlocks = 3
	}
	stereoTopBand(buf[576:], gr[0].sfbtab, nSfb, &maxBand)
	if gr[0].nLongSfb != 0 {
		m := max(max(maxBand[0], maxBand[1]), maxBand[2])
		maxBand = [3]int{m, m, m}
	}
	for i := 0; i < maxBlocks; i++ {
		defaultPos := uint8(0)
		if hdrTestMPEG1(hdr) {
			defaultPos = 3
		}
		itop := nSfb - maxBlocks + i
		prev := itop - maxBlocks
		if maxBand[i] >= prev {
			istPos[itop] = defaultPos
		} else {
			istPos[itop] = istPos[prev]
		}
	}
	stereoProcess(buf, istPos, gr[0].sfbtab, hdr, maxBand, int(gr[1].scalefacCompress&1))
}

func reorder(grbuf []float32, scratchBuf []float32, sfb []uint8) {
	src, dst := 0, 0
	for si := 0; si < len(sfb) && sfb[si] != 0; si += 3 {
		ln := int(sfb[si])
		if src+3*ln > len(grbuf) || dst+3*ln > len(scratchBuf) {
			break // only in a damaged frame: the bands always fill the granule exactly
		}
		for i := 0; i < ln; i, src = i+1, src+1 {
			scratchBuf[dst] = grbuf[src]
			scratchBuf[dst+1] = grbuf[src+ln]
			scratchBuf[dst+2] = grbuf[src+2*ln]
			dst += 3
		}
		src += 2 * ln
	}
	copy(grbuf, scratchBuf[:dst])
}

var aa = [2][8]float32{
	{0.85749293, 0.88174200, 0.94962865, 0.98331459, 0.99551782, 0.99916056, 0.99989920, 0.99999316},
	{0.51449576, 0.47173197, 0.31337745, 0.18191320, 0.09457419, 0.04096558, 0.01419856, 0.00369997},
}

func antialias(grbuf []float32, nbands int) {
	for b := 0; nbands > 0; nbands, b = nbands-1, b+18 {
		for i := 0; i < 8; i++ {
			u, d := grbuf[b+18+i], grbuf[b+17-i]
			grbuf[b+18+i] = float32(u*aa[0][i]) - float32(d*aa[1][i])
			grbuf[b+17-i] = float32(u*aa[1][i]) + float32(d*aa[0][i])
		}
	}
}

func dct3_9(y []float32) {
	s0, s2, s4, s6, s8 := y[0], y[2], y[4], y[6], y[8]
	t0 := s0 + float32(s6*0.5)
	s0 -= s6
	t4 := float32((s4 + s2) * 0.93969262)
	t2 := float32((s8 + s2) * 0.76604444)
	s6 = float32((s4 - s8) * 0.17364818)
	s4 += s8 - s2

	s2 = s0 - float32(s4*0.5)
	y[4] = s4 + s0
	s8 = t0 - t2 + s6
	s0 = t0 - t4 + t2
	s4 = t0 + t4 - s6

	s1, s3, s5, s7 := y[1], y[3], y[5], y[7]
	s3 = float32(s3 * 0.86602540)
	t0 = float32((s5 + s1) * 0.98480775)
	t4 = float32((s5 - s7) * 0.34202014)
	t2 = float32((s1 + s7) * 0.64278761)
	s1 = float32((s1 - s5 - s7) * 0.86602540)

	s5 = t0 - s3 - t2
	s7 = t4 - s3 - t0
	s3 = t4 + s3 - t2

	y[0] = s4 - s7
	y[1] = s2 + s1
	y[2] = s0 - s3
	y[3] = s8 + s5
	y[5] = s8 - s5
	y[6] = s0 + s3
	y[7] = s2 - s1
	y[8] = s4 + s7
}

var twid9 = [18]float32{0.73727734, 0.79335334, 0.84339145, 0.88701083, 0.92387953, 0.95371695, 0.97629601, 0.99144486, 0.99904822,
	0.67559021, 0.60876143, 0.53729961, 0.46174861, 0.38268343, 0.30070580, 0.21643961, 0.13052619, 0.04361938}

func imdct36(grbuf, overlap []float32, window []float32, nbands int) {
	for j, g, o := 0, 0, 0; j < nbands; j, g, o = j+1, g+18, o+9 {
		var co, si [9]float32
		co[0] = -grbuf[g]
		si[0] = grbuf[g+17]
		for i := 0; i < 4; i++ {
			si[8-2*i] = grbuf[g+4*i+1] - grbuf[g+4*i+2]
			co[1+2*i] = grbuf[g+4*i+1] + grbuf[g+4*i+2]
			si[7-2*i] = grbuf[g+4*i+4] - grbuf[g+4*i+3]
			co[2+2*i] = -(grbuf[g+4*i+3] + grbuf[g+4*i+4])
		}
		dct3_9(co[:])
		dct3_9(si[:])
		si[1], si[3], si[5], si[7] = -si[1], -si[3], -si[5], -si[7]
		for i := 0; i < 9; i++ {
			ovl := overlap[o+i]
			sum := float32(co[i]*twid9[9+i]) + float32(si[i]*twid9[i])
			overlap[o+i] = float32(co[i]*twid9[i]) - float32(si[i]*twid9[9+i])
			grbuf[g+i] = float32(ovl*window[i]) - float32(sum*window[9+i])
			grbuf[g+17-i] = float32(ovl*window[9+i]) + float32(sum*window[i])
		}
	}
}

func idct3(x0, x1, x2 float32, dst []float32) {
	m1 := float32(x1 * 0.86602540)
	a1 := x0 - float32(x2*0.5)
	dst[1] = x0 + x2
	dst[0] = a1 + m1
	dst[2] = a1 - m1
}

var twid3 = [6]float32{0.79335334, 0.92387953, 0.99144486, 0.60876143, 0.38268343, 0.13052619}

// imdct12 reads x at stride 3 (x[0], x[3] … x[15]) and writes six samples to dst.
func imdct12(x []float32, dst []float32, overlap []float32) {
	var co, si [3]float32
	idct3(-x[0], x[6]+x[3], x[12]+x[9], co[:])
	idct3(x[15], x[12]-x[9], x[6]-x[3], si[:])
	si[1] = -si[1]
	for i := 0; i < 3; i++ {
		ovl := overlap[i]
		sum := float32(co[i]*twid3[3+i]) + float32(si[i]*twid3[i])
		overlap[i] = float32(co[i]*twid3[i]) - float32(si[i]*twid3[3+i])
		dst[i] = float32(ovl*twid3[2-i]) - float32(sum*twid3[5-i])
		dst[5-i] = float32(ovl*twid3[5-i]) + float32(sum*twid3[2-i])
	}
}

func imdctShort(grbuf, overlap []float32, nbands int) {
	for g, o := 0, 0; nbands > 0; nbands, g, o = nbands-1, g+18, o+9 {
		var tmp [18]float32
		copy(tmp[:], grbuf[g:g+18])
		copy(grbuf[g:g+6], overlap[o:o+6])
		imdct12(tmp[:], grbuf[g+6:], overlap[o+6:])
		imdct12(tmp[1:], grbuf[g+12:], overlap[o+6:])
		imdct12(tmp[2:], overlap[o:], overlap[o+6:])
	}
}

func changeSign(grbuf []float32) {
	g := 18
	for b := 0; b < 32; b, g = b+2, g+36 {
		for i := 1; i < 18; i += 2 {
			grbuf[g+i] = -grbuf[g+i]
		}
	}
}

var mdctWindow = [2][18]float32{
	{0.99904822, 0.99144486, 0.97629601, 0.95371695, 0.92387953, 0.88701083, 0.84339145, 0.79335334, 0.73727734, 0.04361938, 0.13052619, 0.21643961, 0.30070580, 0.38268343, 0.46174861, 0.53729961, 0.60876143, 0.67559021},
	{1, 1, 1, 1, 1, 1, 0.99144486, 0.92387953, 0.79335334, 0, 0, 0, 0, 0, 0, 0.13052619, 0.38268343, 0.60876143},
}

func imdctGr(grbuf, overlap []float32, blockType uint8, nLongBands int) {
	if nLongBands != 0 {
		imdct36(grbuf, overlap, mdctWindow[0][:], nLongBands)
		grbuf = grbuf[18*nLongBands:]
		overlap = overlap[9*nLongBands:]
	}
	if blockType == shortBlockType {
		imdctShort(grbuf, overlap, 32-nLongBands)
	} else {
		w := 0
		if blockType == stopBlockType {
			w = 1
		}
		imdct36(grbuf, overlap, mdctWindow[w][:], 32-nLongBands)
	}
}

func (d *Decoder) saveReservoir() {
	s := &d.s
	pos := (s.bs.pos + 7) / 8
	remains := s.bs.limit/8 - pos
	if remains > maxBitReservoirBytes {
		pos += remains - maxBitReservoirBytes
		remains = maxBitReservoirBytes
	}
	if remains > 0 {
		copy(d.reservBuf[:remains], s.maindata[pos:pos+remains])
	}
	d.reserv = remains
}

func (d *Decoder) restoreReservoir(bs *bitstream, mainDataBegin int) bool {
	s := &d.s
	frameBytes := (bs.limit - bs.pos) / 8
	bytesHave := min(d.reserv, mainDataBegin)
	from := max(0, d.reserv-mainDataBegin)
	copy(s.maindata[:bytesHave], d.reservBuf[from:from+bytesHave])
	copy(s.maindata[bytesHave:bytesHave+frameBytes], bs.buf[bs.pos/8:bs.pos/8+frameBytes])
	s.bs.init(s.maindata[:], bytesHave+frameBytes)
	return d.reserv >= mainDataBegin
}

func (d *Decoder) l3decode(gr []grInfo, nch int) {
	s := &d.s
	h := d.header[:]
	for ch := 0; ch < nch; ch++ {
		limit := s.bs.pos + int(gr[ch].part23Length)
		decodeScalefactors(h, s.istPos[ch][:], &s.bs, &gr[ch], s.scf[:], ch)
		huffman(s.grbuf[576*ch:], &s.bs, &gr[ch], s.scf[:], limit)
	}
	if hdrTestIStereo(h) {
		intensityStereo(s.grbuf[:], s.istPos[1][:], gr, h)
	} else if hdrIsMSStereo(h) {
		midsideStereo(s.grbuf[:], 0, 576)
	}
	for ch := 0; ch < nch; ch++ {
		g := &gr[ch]
		aaBands := 31
		nLongBands := 0
		if g.mixedBlockFlag != 0 {
			nLongBands = 2
		}
		if hdrGetMySampleRate(h) == 2 {
			nLongBands <<= 1
		}
		buf := s.grbuf[576*ch:]
		if g.nShortSfb != 0 {
			aaBands = nLongBands - 1
			reorder(buf[nLongBands*18:], s.syn[:], g.sfbtab[g.nLongSfb:])
		}
		antialias(buf, aaBands)
		imdctGr(buf, d.mdctOverlap[ch][:], g.blockType, nLongBands)
		changeSign(buf)
	}
}

var sec = [24]float32{10.19000816, 0.50060302, 0.50241929, 3.40760851, 0.50547093, 0.52249861, 2.05778098, 0.51544732, 0.56694406,
	1.48416460, 0.53104258, 0.64682180, 1.16943991, 0.55310392, 0.78815460, 0.97256821, 0.58293498, 1.06067765, 0.83934963,
	0.62250412, 1.72244716, 0.74453628, 0.67480832, 5.10114861}

func dctII(grbuf []float32, n int) {
	for k := 0; k < n; k++ {
		var t [4][8]float32
		y := k
		for i := 0; i < 8; i++ {
			x0 := grbuf[y+i*18]
			x1 := grbuf[y+(15-i)*18]
			x2 := grbuf[y+(16+i)*18]
			x3 := grbuf[y+(31-i)*18]
			t0 := x0 + x3
			t1 := x1 + x2
			t2 := float32((x1 - x2) * sec[3*i+0])
			t3 := float32((x0 - x3) * sec[3*i+1])
			t[0][i] = t0 + t1
			t[1][i] = float32((t0 - t1) * sec[3*i+2])
			t[2][i] = t3 + t2
			t[3][i] = float32((t3 - t2) * sec[3*i+2])
		}
		for i := 0; i < 4; i++ {
			x := &t[i]
			x0, x1, x2, x3, x4, x5, x6, x7 := x[0], x[1], x[2], x[3], x[4], x[5], x[6], x[7]
			xt := x0 - x7
			x0 += x7
			x7 = x1 - x6
			x1 += x6
			x6 = x2 - x5
			x2 += x5
			x5 = x3 - x4
			x3 += x4
			x4 = x0 - x3
			x0 += x3
			x3 = x1 - x2
			x1 += x2
			x[0] = x0 + x1
			x[4] = float32((x0 - x1) * 0.70710677)
			x5 = x5 + x6
			x6 = float32((x6 + x7) * 0.70710677)
			x7 = x7 + xt
			x3 = float32((x3 + x4) * 0.70710677)
			x5 -= float32(x7 * 0.198912367)
			x7 += float32(x5 * 0.382683432)
			x5 -= float32(x7 * 0.198912367)
			x0 = xt - x6
			xt += x6
			x[1] = float32((xt + x7) * 0.50979561)
			x[2] = float32((x4 + x3) * 0.54119611)
			x[3] = float32((x0 - x5) * 0.60134488)
			x[5] = float32((x0 + x5) * 0.89997619)
			x[6] = float32((x4 - x3) * 1.30656302)
			x[7] = float32((xt - x7) * 2.56291556)
		}
		for i := 0; i < 7; i, y = i+1, y+4*18 {
			grbuf[y+0*18] = t[0][i]
			grbuf[y+1*18] = t[2][i] + t[3][i] + t[3][i+1]
			grbuf[y+2*18] = t[1][i] + t[1][i+1]
			grbuf[y+3*18] = t[2][i+1] + t[3][i] + t[3][i+1]
		}
		grbuf[y+0*18] = t[0][7]
		grbuf[y+1*18] = t[2][7] + t[3][7]
		grbuf[y+2*18] = t[1][7]
		grbuf[y+3*18] = t[3][7]
	}
}

func scalePCM(sample float32) int16 {
	if sample >= 32766.5 {
		return 32767
	}
	if sample <= -32767.5 {
		return -32768
	}
	s := int16(sample + 0.5)
	if s < 0 {
		s-- // away from zero, as the standard asks
	}
	return s
}

func synthPair(pcm []int16, p, nch int, z []float32, zi int) {
	a := float32((z[zi+14*64] - z[zi]) * 29)
	a += float32((z[zi+1*64] + z[zi+13*64]) * 213)
	a += float32((z[zi+12*64] - z[zi+2*64]) * 459)
	a += float32((z[zi+3*64] + z[zi+11*64]) * 2037)
	a += float32((z[zi+10*64] - z[zi+4*64]) * 5153)
	a += float32((z[zi+5*64] + z[zi+9*64]) * 6574)
	a += float32((z[zi+8*64] - z[zi+6*64]) * 37489)
	a += float32(z[zi+7*64] * 75038)
	pcm[p] = scalePCM(a)
	zi += 2
	a = float32(z[zi+14*64] * 104)
	a += float32(z[zi+12*64] * 1567)
	a += float32(z[zi+10*64] * 9727)
	a += float32(z[zi+8*64] * 64019)
	a += float32(z[zi+6*64] * -9975)
	a += float32(z[zi+4*64] * -45)
	a += float32(z[zi+2*64] * 146)
	a += float32(z[zi+0*64] * -5)
	pcm[p+16*nch] = scalePCM(a)
}

// synth runs the polyphase filter bank for two subband samples: xl at grbuf[x], output at pcm[p],
// working memory lins from li.
func synth(grbuf []float32, x int, pcm []int16, p, nch int, lins []float32, li int) {
	xr := x + 576*(nch-1)
	pr := p + (nch - 1)
	z := li + 15*64
	w := 0
	lins[z+4*15] = grbuf[x+18*16]
	lins[z+4*15+1] = grbuf[xr+18*16]
	lins[z+4*15+2] = grbuf[x]
	lins[z+4*15+3] = grbuf[xr]

	lins[z+4*31] = grbuf[x+1+18*16]
	lins[z+4*31+1] = grbuf[xr+1+18*16]
	lins[z+4*31+2] = grbuf[x+1]
	lins[z+4*31+3] = grbuf[xr+1]

	synthPair(pcm, pr, nch, lins, li+4*15+1)
	synthPair(pcm, pr+32*nch, nch, lins, li+4*15+64+1)
	synthPair(pcm, p, nch, lins, li+4*15)
	synthPair(pcm, p+32*nch, nch, lins, li+4*15+64)

	for i := 14; i >= 0; i-- {
		var a, b [4]float32
		lins[z+4*i] = grbuf[x+18*(31-i)]
		lins[z+4*i+1] = grbuf[xr+18*(31-i)]
		lins[z+4*i+2] = grbuf[x+1+18*(31-i)]
		lins[z+4*i+3] = grbuf[xr+1+18*(31-i)]
		lins[z+4*(i+16)] = grbuf[x+1+18*(1+i)]
		lins[z+4*(i+16)+1] = grbuf[xr+1+18*(1+i)]
		lins[z+4*(i-16)+2] = grbuf[x+18*(1+i)]
		lins[z+4*(i-16)+3] = grbuf[xr+18*(1+i)]
		for k := 0; k < 8; k++ {
			w0, w1 := winTab[w], winTab[w+1]
			w += 2
			vz := z + 4*i - k*64
			vy := z + 4*i - (15-k)*64
			for j := 0; j < 4; j++ {
				zz, yy := lins[vz+j], lins[vy+j]
				switch {
				case k == 0:
					b[j] = float32(zz*w1) + float32(yy*w0)
					a[j] = float32(zz*w0) - float32(yy*w1)
				case k&1 == 1:
					b[j] += float32(zz*w1) + float32(yy*w0)
					a[j] += float32(yy*w1) - float32(zz*w0)
				default:
					b[j] += float32(zz*w1) + float32(yy*w0)
					a[j] += float32(zz*w0) - float32(yy*w1)
				}
			}
		}
		pcm[pr+(15-i)*nch] = scalePCM(a[1])
		pcm[pr+(17+i)*nch] = scalePCM(b[1])
		pcm[p+(15-i)*nch] = scalePCM(a[0])
		pcm[p+(17+i)*nch] = scalePCM(b[0])
		pcm[pr+(47-i)*nch] = scalePCM(a[3])
		pcm[pr+(49+i)*nch] = scalePCM(b[3])
		pcm[p+(47-i)*nch] = scalePCM(a[2])
		pcm[p+(49+i)*nch] = scalePCM(b[2])
	}
}

func (d *Decoder) synthGranule(grbuf []float32, nbands, nch int, pcm []int16, p int) {
	lins := d.s.syn[:]
	for i := 0; i < nch; i++ {
		dctII(grbuf[576*i:], nbands)
	}
	copy(lins[:15*64], d.qmfState[:15*64])
	for i := 0; i < nbands; i += 2 {
		synth(grbuf, i, pcm, p+32*nch*i, nch, lins, i*64)
	}
	if nch == 1 {
		for i := 0; i < 15*64; i += 2 {
			d.qmfState[i] = lins[nbands*64+i]
		}
	} else {
		copy(d.qmfState[:15*64], lins[nbands*64:nbands*64+15*64])
	}
}

func matchFrame(hdr []byte, frameBytes int) bool {
	i := 0
	for nmatch := 0; nmatch < maxFrameSyncMatches; nmatch++ {
		i += hdrFrameBytes(hdr[i:], frameBytes) + hdrPadding(hdr[i:])
		if i+hdrSize > len(hdr) {
			return nmatch > 0
		}
		if !hdrCompare(hdr, hdr[i:]) {
			return false
		}
	}
	return true
}

func findFrame(mp3 []byte, freeFormatBytes *int, ptrFrameBytes *int) int {
	n := len(mp3)
	for i := 0; i < n-hdrSize; i++ {
		h := mp3[i:]
		if !hdrValid(h) {
			continue
		}
		frameBytes := hdrFrameBytes(h, *freeFormatBytes)
		frameAndPadding := frameBytes + hdrPadding(h)
		for k := hdrSize; frameBytes == 0 && k < maxFreeFormatFrameSize && i+2*k < n-hdrSize; k++ {
			if hdrCompare(h, h[k:]) {
				fb := k - hdrPadding(h)
				nextfb := fb + hdrPadding(h[k:])
				if i+k+nextfb+hdrSize > n || !hdrCompare(h, h[k+nextfb:]) {
					continue
				}
				frameAndPadding = k
				frameBytes = fb
				*freeFormatBytes = fb
			}
		}
		if (frameBytes != 0 && i+frameAndPadding <= n && matchFrame(h, frameBytes)) || (i == 0 && frameAndPadding == n) {
			*ptrFrameBytes = frameAndPadding
			return i
		}
		*freeFormatBytes = 0
	}
	*ptrFrameBytes = 0
	return n
}

// FrameInfo describes the frame DecodeFrame read.
type FrameInfo struct {
	FrameBytes, FrameOffset, Channels, Hz, Layer, BitrateKbps int
}

// DecodeFrame decodes the first frame in mp3 into pcm (interleaved, MaxSamplesPerFrame long) and returns
// how many samples per channel it gave; info.FrameBytes is how far to move on. Zero samples with
// FrameBytes > 0 means skipped data (not audio, or a frame that cannot be decoded yet).
func (d *Decoder) DecodeFrame(mp3 []byte, pcm []int16) (samples int, info FrameInfo) {
	defer func() {
		// A damaged frame whose numbers point outside the tables is skipped, never a crash. It cannot
		// happen with a well-formed stream, so it changes nothing that decodes.
		if recover() != nil {
			d.header[0] = 0
			samples = 0
			if info.FrameBytes == 0 {
				info.FrameBytes = 1
			}
		}
	}()
	i, frameSize := 0, 0
	if len(mp3) > 4 && d.header[0] == 0xff && hdrCompare(d.header[:], mp3) {
		frameSize = hdrFrameBytes(mp3, d.freeFormatBytes) + hdrPadding(mp3)
		if frameSize != len(mp3) && (frameSize+hdrSize > len(mp3) || !hdrCompare(mp3, mp3[frameSize:])) {
			frameSize = 0
		}
	}
	if frameSize == 0 {
		*d = Decoder{}
		i = findFrame(mp3, &d.freeFormatBytes, &frameSize)
		if frameSize == 0 || i+frameSize > len(mp3) {
			info.FrameBytes = i
			return 0, info
		}
	}
	hdr := mp3[i:]
	copy(d.header[:], hdr[:hdrSize])
	info.FrameBytes = i + frameSize
	info.FrameOffset = i
	info.Channels = 2
	if hdrIsMono(hdr) {
		info.Channels = 1
	}
	info.Hz = hdrSampleRateHz(hdr)
	info.Layer = 4 - hdrGetLayer(hdr)
	info.BitrateKbps = hdrBitrateKbps(hdr)
	if info.Layer != 3 {
		return 0, info // Layer I and II: not MP3, not decoded
	}
	var bs bitstream
	bs.init(hdr[hdrSize:frameSize], frameSize-hdrSize)
	if hdrIsCRC(hdr) {
		bs.get(16)
	}
	s := &d.s
	mainDataBegin := readSideInfo(&bs, s.gr[:], hdr)
	if mainDataBegin < 0 || bs.pos > bs.limit {
		d.header[0] = 0
		return 0, info
	}
	success := d.restoreReservoir(&bs, mainDataBegin)
	if success {
		grs := 1
		if hdrTestMPEG1(hdr) {
			grs = 2
		}
		for igr, p := 0, 0; igr < grs; igr, p = igr+1, p+576*info.Channels {
			clear(s.grbuf[:])
			d.l3decode(s.gr[igr*info.Channels:], info.Channels)
			d.synthGranule(s.grbuf[:], 18, info.Channels, pcm, p)
		}
	}
	d.saveReservoir()
	if !success {
		return 0, info
	}
	return hdrFrameSamples(d.header[:]), info
}
