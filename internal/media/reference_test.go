package media

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The codecs BitVoice converts with its own code or its pinned Go libraries, against the reference
// libraries: spandsp (G.711, G.722), bcg729 (G.729 Annex A), opencore-amr (AMR-NB, AMR-WB) and libopus.
// BITVOICE_TEST_CODEC_ORACLE names a program that runs them (codecoracle: see the comment at its top):
//
//	cc -O2 -o codecoracle codecoracle.c -lspandsp -lopencore-amrnb -lopencore-amrwb -lopus libbcg729.a
//	BITVOICE_TEST_CODEC_ORACLE=$PWD/codecoracle go test ./internal/media -run Reference
//
// G.711 and G.722 must give the same bytes and samples as spandsp, the AMR-NB encoder the same frames
// as opencore; G.729's decoder is held to ITU's own test vectors. Opus is compared by decoding each
// other's packets, and the AMR decoders, which are close to opencore but not exact, are measured.
func oracle(t *testing.T) string {
	o := os.Getenv("BITVOICE_TEST_CODEC_ORACLE")
	if o == "" {
		t.Skip("set BITVOICE_TEST_CODEC_ORACLE to the reference codec program")
	}
	return o
}

func runOracle(t *testing.T, o string, in []byte, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(o, args...)
	cmd.Stdin = bytes.NewReader(in)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v: %v %s", args, err, errb.String())
	}
	return out
}

// voiceLike is a deterministic test signal at rate: voiced vowels (a pitch with harmonics under a
// moving formant), noise-like fricatives, pauses and a loud passage, three seconds long.
func voiceLike(rate int) []int16 {
	n := 3 * rate
	out := make([]int16, n)
	seed := uint32(1)
	phase := 0.0
	for i := range out {
		tm := float64(i) / float64(rate)
		seg := int(tm * 4)
		var v float64
		switch seg % 6 {
		case 0, 1, 3: // voiced: 110-220 Hz pitch, 12 harmonics shaped by a formant
			f0 := 110 + 110*math.Sin(2*math.Pi*0.7*tm)*math.Sin(2*math.Pi*0.7*tm)
			phase += 2 * math.Pi * f0 / float64(rate)
			formant := 500 + 1500*(0.5+0.5*math.Sin(2*math.Pi*1.3*tm))
			for h := 1; h <= 12; h++ {
				fh := f0 * float64(h)
				if fh > float64(rate)/2-200 {
					break
				}
				g := math.Exp(-math.Abs(fh-formant) / 400)
				v += g * math.Sin(float64(h)*phase)
			}
			v *= 5000
		case 2: // fricative
			seed = seed*1664525 + 1013904223
			v = (float64(int32(seed))/float64(1<<31))*3000 + 0
		case 4: // silence with a little noise
			seed = seed*1664525 + 1013904223
			v = float64(int32(seed)) / float64(1<<31) * 30
		case 5: // loud
			v = 26000 * math.Sin(2*math.Pi*440*tm) * math.Sin(2*math.Pi*3*tm)
		}
		out[i] = int16(max(-32768, min(32767, math.Round(v))))
	}
	return out
}

func pcmBytes(p []int16) []byte {
	b := make([]byte, 2*len(p))
	for i, v := range p {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
	}
	return b
}

func bytesPCM(b []byte) []int16 {
	p := make([]int16, len(b)/2)
	for i := range p {
		p[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
	}
	return p
}

// frames splits a length-prefixed stream.
func frames(b []byte) (out [][]byte) {
	for len(b) >= 2 {
		n := int(binary.LittleEndian.Uint16(b))
		out = append(out, b[2:2+n])
		b = b[2+n:]
	}
	return
}

func prefixed(fs [][]byte) []byte {
	var b []byte
	for _, f := range fs {
		b = binary.LittleEndian.AppendUint16(b, uint16(len(f)))
		b = append(b, f...)
	}
	return b
}

// snr is the signal-to-noise ratio of got against want in dB, after the best delay up to maxLag.
func delayedSNR(want, got []int16, maxLag int) (float64, int) {
	best, lag := math.Inf(-1), 0
	for d := 0; d <= maxLag; d++ {
		var s, e float64
		for i := 0; i+d < len(got) && i < len(want); i++ {
			w, g := float64(want[i]), float64(got[i+d])
			s += w * w
			e += (w - g) * (w - g)
		}
		if v := 10 * math.Log10(s/math.Max(e, 1)); v > best {
			best, lag = v, d
		}
	}
	return best, lag
}

func firstDiff(a, b []int16) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return min(len(a), len(b))
	}
	return -1
}

func TestReferenceG711(t *testing.T) {
	o := oracle(t)
	in := voiceLike(8000)
	// Every 16-bit value, and every code back.
	all := make([]int16, 65536)
	for i := range all {
		all[i] = int16(i - 32768)
	}
	codes := make([]byte, 256)
	for i := range codes {
		codes[i] = byte(i)
	}
	for _, c := range []struct {
		name string
		pt   int
	}{{"pcmu", PTPCMU}, {"pcma", PTPCMA}} {
		g := g711{c.pt}
		for _, sig := range [][]int16{all, in} {
			if want, got := runOracle(t, o, pcmBytes(sig), c.name, "enc"), g.encode(sig); !bytes.Equal(want, got) {
				t.Errorf("%s encode differs from spandsp", c.name)
			}
		}
		want := bytesPCM(runOracle(t, o, codes, c.name, "dec"))
		if i := firstDiff(want, g.decode(codes)); i >= 0 {
			t.Errorf("%s decode differs from spandsp at code %d", c.name, i)
		}
	}
}

func TestReferenceG722(t *testing.T) {
	o := oracle(t)
	in := voiceLike(16000)
	want := runOracle(t, o, pcmBytes(in), "g722", "enc")
	got := NewG722Encoder().Encode(in)
	if !bytes.Equal(want, got) {
		i := 0
		for i < min(len(want), len(got)) && want[i] == got[i] {
			i++
		}
		t.Fatalf("G.722 encode differs from spandsp at byte %d of %d (%d vs %d bytes)", i, len(want), len(got), len(want))
	}
	ref := bytesPCM(runOracle(t, o, want, "g722", "dec"))
	if i := firstDiff(ref, NewG722Decoder().Decode(want)); i >= 0 {
		t.Fatalf("G.722 decode differs from spandsp at sample %d", i)
	}
}

// G.729's decoder against ITU's own Annex A test vectors (the .BIT bitstreams and the .PST output of
// the ITU reference decoder, from the Annex A software): sample for sample. BITVOICE_TEST_G729_VECTORS
// names the folder holding them. bcg729 is not used for this: on these vectors it differs from ITU.
func TestReferenceG729Vectors(t *testing.T) {
	dir := os.Getenv("BITVOICE_TEST_G729_VECTORS")
	if dir == "" {
		t.Skip("set BITVOICE_TEST_G729_VECTORS to ITU's G.729 Annex A test vectors")
	}
	for _, n := range []string{"ALGTHM", "FIXED", "LSP", "PITCH", "SPEECH", "TAME", "TEST"} {
		bit, err := os.ReadFile(filepath.Join(dir, n+".BIT"))
		if err != nil {
			t.Fatal(err)
		}
		pst, err := os.ReadFile(filepath.Join(dir, n+".PST"))
		if err != nil {
			pst, err = os.ReadFile(filepath.Join(dir, n+".pst"))
		}
		if err != nil {
			t.Fatal(err)
		}
		// ITU's serial form: per 10 ms frame a sync word, the count (80) and one word per bit
		// (0x0081 a one, 0x007F a zero), in the order RTP carries them (RFC 3551).
		var frames []byte
		for b := bit; len(b) >= 164; b = b[164:] {
			f := make([]byte, 10)
			for k := range 80 {
				if binary.LittleEndian.Uint16(b[4+2*k:]) == 0x0081 {
					f[k/8] |= 0x80 >> (k % 8)
				}
			}
			frames = append(frames, f...)
		}
		want := bytesPCM(pst)
		if i := firstDiff(want, (&g729v{}).decode(frames)); i >= 0 {
			t.Errorf("%s: differs from ITU's reference decoder at sample %d of %d", n, i, len(want))
		}
	}
}

// G.729 with bcg729 both ways: each decodes the other's frames to the signal. BitVoice's encoder is
// not byte-exact with any other (G.729 does not require it); what matters is that a far end hears it.
func TestReferenceG729Interworking(t *testing.T) {
	o := oracle(t)
	in := voiceLike(8000)
	own := (&g729v{}).encode(in)
	ref := runOracle(t, o, pcmBytes(in), "g729", "enc")
	a, _ := delayedSNR(in, bytesPCM(runOracle(t, o, own, "g729", "dec")), 80)
	b, _ := delayedSNR(in, (&g729v{}).decode(ref), 80)
	c, _ := delayedSNR(in, bytesPCM(runOracle(t, o, ref, "g729", "dec")), 80)
	t.Logf("SNR: BitVoice frames by bcg729 %.1f dB, bcg729 frames by BitVoice %.1f dB, bcg729 alone %.1f dB", a, b, c)
	if a < c-1 || b < c-1 {
		t.Errorf("G.729 interworking with bcg729 is worse than bcg729 alone: %.1f, %.1f vs %.1f dB", a, b, c)
	}
}

// AMR's RTP payload (RFC 4867, octet-aligned, one frame) is a CMR byte, a TOC byte with F clear,
// then the frame; MIME storage (what opencore reads and writes) is the TOC byte and the frame.
func amrStorage(p []byte) []byte { return append([]byte{p[1] &^ 0x80}, p[2:]...) }

func TestReferenceAMRNB(t *testing.T) {
	o := oracle(t)
	in := voiceLike(8000)
	enc := newAMR(true, "")
	var own [][]byte
	for i := 0; i+160 <= len(in); i += 160 {
		own = append(own, amrStorage(enc.encode(in[i:i+160])))
	}
	ref := frames(runOracle(t, o, pcmBytes(in), "amrnb", "enc", "7"))
	same := 0
	for i := range min(len(own), len(ref)) {
		if bytes.Equal(own[i], ref[i]) {
			same++
		}
	}
	if same != len(ref) || len(own) != len(ref) {
		t.Errorf("AMR-NB 12.2 encode: %d of %d frames as opencore's", same, len(ref))
	}
	refDec := bytesPCM(runOracle(t, o, prefixed(ref), "amrnb", "dec"))
	dec := newAMR(true, "")
	var ownDec []int16
	for _, f := range ref {
		ownDec = append(ownDec, dec.decode(append([]byte{0xf0}, f...))...)
	}
	// The decoder (goamr-nb) is not bit-exact with opencore on this input although its encoder is:
	// the difference starts at one step of the 13-bit output in the first frame. Keep it measured.
	s, _ := delayedSNR(refDec, ownDec, 0)
	t.Logf("AMR-NB decode against opencore: %.1f dB (first difference at sample %d)", s, firstDiff(refDec, ownDec))
	if s < 25 {
		t.Errorf("AMR-NB decode drifted from opencore: %.1f dB", s)
	}
}

func TestReferenceAMRWB(t *testing.T) {
	o := oracle(t)
	in := voiceLike(16000)
	enc := newAMRWB(true, "")
	var own [][]byte
	for i := 0; i+320 <= len(in); i += 320 {
		own = append(own, amrStorage(enc.encode(in[i:i+320])))
	}
	refDec := bytesPCM(runOracle(t, o, prefixed(own), "amrwb", "dec"))
	dec := newAMRWB(true, "")
	var ownDec []int16
	for _, f := range own {
		ownDec = append(ownDec, dec.decode(append([]byte{0xf0}, f...))...)
	}
	s, _ := delayedSNR(refDec, ownDec, 0)
	t.Logf("AMR-WB decode against opencore: %.1f dB (first difference at sample %d)", s, firstDiff(refDec, ownDec))
	if s < 15 {
		t.Errorf("AMR-WB decode drifted further from opencore: %.1f dB", s)
	}
	if s, _ := delayedSNR(in, refDec, 400); s < 5 {
		t.Errorf("AMR-WB 23.85 through opencore: SNR %.1f dB", s)
	}
}

func TestReferenceOpus(t *testing.T) {
	o := oracle(t)
	in := voiceLike(16000)
	// BitVoice's packets through libopus.
	enc := &opusv{}
	var own [][]byte
	for i := 0; i+320 <= len(in); i += 320 {
		own = append(own, enc.encode(in[i:i+320]))
	}
	refDec := bytesPCM(runOracle(t, o, prefixed(own), "opus", "dec"))
	if s, lag := delayedSNR(in, refDec, 400); s < 8 {
		t.Errorf("BitVoice's Opus through libopus: SNR %.1f dB (delay %d)", s, lag)
	}
	// libopus's packets through BitVoice: the same samples as libopus decodes them.
	ref := frames(runOracle(t, o, pcmBytes(in), "opus", "enc", "24000"))
	want := bytesPCM(runOracle(t, o, prefixed(ref), "opus", "dec"))
	dec := &opusv{}
	var got []int16
	for _, f := range ref {
		got = append(got, dec.decode(f)...)
	}
	if s, _ := delayedSNR(want, got, 0); s < 60 {
		t.Errorf("libopus's packets decoded by BitVoice differ from libopus: SNR %.1f dB (first difference %d)", s, firstDiff(want, got))
	}
}

// AMR-WB's decoder against 3GPP's own test sequences (TS 26.174: T00-T20 at every mode, converted to
// MIME storage frames with opencore's bit order; see wb2mime.py beside them). BITVOICE_TEST_AMRWB_VECTORS
// names the folder holding T_INP and T_660 ... T_2385, each with TNN_MODE.mime and 3GPP's TNN_MODE.out.
//
// The library BitVoice uses (goamr-wb 1.1.1; 1.2.0 decodes the same) is NOT sample-exact with 3GPP's decoder (opencore is on
// 176 of the 207): measured against each sequence's own input it is on average about 3 dB below 3GPP's
// decoder (about 1 dB on average, 7 dB on the worst sequence). This test records that and fails if the
// average gets worse than 2 dB. T23 is the
// decoder homing sequence, which only a circuit-switched decoder acts on: it is left out.
func TestReferenceAMRWBVectors(t *testing.T) {
	dir := os.Getenv("BITVOICE_TEST_AMRWB_VECTORS")
	if dir == "" {
		t.Skip("set BITVOICE_TEST_AMRWB_VECTORS to 3GPP's AMR-WB test sequences")
	}
	files, exact, worst, sum := 0, 0, 0.0, 0.0
	for _, mode := range []string{"660", "885", "1265", "1425", "1585", "1825", "1985", "2305", "2385"} {
		mimes, _ := filepath.Glob(filepath.Join(dir, "T_"+mode, "T*_"+mode+".mime"))
		for _, m := range mimes {
			b, _ := os.ReadFile(m)
			o, err := os.ReadFile(m[:len(m)-5] + ".out")
			if err != nil {
				continue
			}
			name := filepath.Base(m)[:3]
			if name == "T23" {
				continue
			}
			in, err := os.ReadFile(filepath.Join(dir, "T_INP", name+".inp"))
			if err != nil {
				in, err = os.ReadFile(filepath.Join(dir, "T_INP", name+".INP"))
			}
			if err != nil {
				continue
			}
			want, input := bytesPCM(o), bytesPCM(in)
			dec := newAMRWB(true, "")
			var got []int16
			for _, f := range frames(b) {
				got = append(got, dec.decode(append([]byte{0xf0}, f...))...)
			}
			files++
			if n, _ := countDiff(want, got); n == 0 {
				exact++
			}
			ref, _ := delayedSNR(input, want, 400)
			own, _ := delayedSNR(input, got, 400)
			worst = max(worst, ref-own)
			sum += ref - own
			if ref-own > 5 {
				t.Logf("%s: %.1f dB to the input, 3GPP's decoder %.1f dB", filepath.Base(m), own, ref)
			}
		}
	}
	if files == 0 {
		t.Fatal("no sequences found")
	}
	t.Logf("AMR-WB against 3GPP: %d sequences, %d sample for sample; %.1f dB below 3GPP's decoder on average, %.1f at worst",
		files, exact, sum/float64(files), worst)
	if sum/float64(files) > 2 {
		t.Errorf("AMR-WB decode is %.1f dB below 3GPP's decoder on average", sum/float64(files))
	}
}

func countDiff(a, b []int16) (n, maxd int) {
	for i := range min(len(a), len(b)) {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		if d > 0 {
			n++
		}
		maxd = max(maxd, d)
	}
	return n + max(len(a), len(b)) - min(len(a), len(b)), maxd
}
