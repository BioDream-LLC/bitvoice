package p25

import (
	"bufio"
	"bytes"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/p25/imbe"
	"github.com/biodream-llc/bitvoice/internal/p25/mbe"
)

// The IMBE frames of the NIST ISSI capture, unpacked by BitVoice and by mbelib (szechyjs/mbelib, the
// open reference most P25 software decodes with): the same fundamental, harmonic count, voicing
// decisions and predicted log amplitudes, frame for frame. BITVOICE_TEST_MBELIB names a program that
// prints mbelib's parameters for 88-bit frames (imbeparms: see the comment at its top).
func TestIMBEParamsMatchMbelib(t *testing.T) {
	prog := os.Getenv("BITVOICE_TEST_MBELIB")
	if prog == "" {
		t.Skip("set BITVOICE_TEST_MBELIB to the mbelib parameter program")
	}
	var infos [][]byte
	var in bytes.Buffer
	for _, b := range rtpOf(t, "testdata/issi.pcap") {
		p, err := Parse(b)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range p.Voice {
			packed := v.Info() // 11 bytes, MSB first
			info := make([]byte, 88)
			for i := range info {
				info[i] = packed[i/8] >> (7 - i%8) & 1
			}
			infos = append(infos, info)
			in.Write(info)
		}
	}
	cmd := exec.Command(prog)
	cmd.Stdin = &in
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var st mbe.SynthState
	lines := bufio.NewScanner(bytes.NewReader(out))
	lines.Buffer(make([]byte, 1<<16), 1<<16)
	n, worst := 0, 0.0
	for i := 0; lines.Scan(); i++ {
		ref := lines.Text()
		p, err := imbe.UnpackParams(infos[i])
		switch {
		case ref == "bad":
			if err == nil {
				t.Errorf("frame %d: mbelib refuses it, BitVoice does not", i)
			}
			continue
		case ref == "silent":
			if err != nil || !p.Silent {
				t.Errorf("frame %d: mbelib says silence, BitVoice %v %v", i, p.Silent, err)
			}
			st.Reset()
			continue
		case err != nil:
			t.Errorf("frame %d: %v, mbelib decodes it", i, err)
			continue
		}
		head, amps, _ := strings.Cut(ref, " |")
		f := strings.Fields(head)
		w0, _ := strconv.ParseFloat(f[0], 64)
		L, _ := strconv.Atoi(f[1])
		K, _ := strconv.Atoi(f[2])
		if math.Abs(w0-p.W0) > 1e-6 || L != p.L || K != p.K {
			t.Errorf("frame %d: w0 %.6f L %d K %d, mbelib %.6f %d %d", i, p.W0, p.L, p.K, w0, L, K)
			continue
		}
		for l := 1; l <= L; l++ {
			if v, _ := strconv.Atoi(f[2+l]); v != p.Vl[l] {
				t.Errorf("frame %d: voicing of harmonic %d differs", i, l)
				break
			}
		}
		m := p.MBE()
		var log2M [57]float64
		mbe.PredictLog2Ml(&st, m, &log2M)
		for l, a := range strings.Fields(amps) {
			v, _ := strconv.ParseFloat(a, 64)
			worst = math.Max(worst, math.Abs(v-log2M[l+1]))
		}
		st.UpdateLog2Ml(m, &log2M)
		st.PrevW0, st.PrevL = m.W0, m.L
		n++
	}
	t.Logf("%d frames compared with mbelib; log2 amplitudes within %.2g", n, worst)
	if n < 100 || worst > 1e-4 {
		t.Fatalf("IMBE parameters differ from mbelib (%d frames, worst %.3g)", n, worst)
	}
}
