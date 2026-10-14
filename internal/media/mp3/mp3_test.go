package mp3

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
	"testing"
)

// The samples minimp3's own C code decodes from these files (lame-encoded: MPEG-1 joint stereo at
// 128 kbit/s, MPEG-2 at 22.05 kHz, MPEG-2.5 mono at 8 kHz), compiled without fused multiply-add.
var refs = map[string]string{
	"js128":  "85d1d6ed4d5c81aadd76694dca82b3a379ec130a44623b3e2e11a25ee51e5c42",
	"m25_8k": "e625878acd445ecd9a41bf40209a3c433a76f28594454fe62518eb47867d7e1e",
	"m2_22k": "1d34b517cd11f39939f7de1d6bdbbf29d86a459fbb3ba7eb8dd65217df44ba1e",
}

func hashPCM(pcm []int16) string {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, pcm)
	s := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(s[:])
}

func TestSameAsMinimp3(t *testing.T) {
	for name, want := range refs {
		b, err := os.ReadFile("testdata/" + name + ".mp3")
		if err != nil {
			t.Fatal(err)
		}
		if got := hashPCM(decodeAll(b)); got != want {
			t.Errorf("%s decodes to %s, minimp3 to %s", name, got, want)
		}
	}
}

// Decode skips an ID3v2 tag; Stream gives the same samples a few bytes at a time.
func TestDecodeAndStream(t *testing.T) {
	b, _ := os.ReadFile("testdata/m2_22k.mp3")
	tag := append([]byte("ID3\x04\x00\x00\x00\x00\x00\x10"), make([]byte, 16)...)
	tag[20] = 0xFF // a false frame sync inside the tag
	tag[21] = 0xFB
	pcm, rate, ch, err := Decode(append(tag, b...))
	if err != nil || rate != 22050 || ch != 2 || hashPCM(pcm) != refs["m2_22k"] {
		t.Fatalf("decode: %v %d %d", err, rate, ch)
	}
	if !Looks(b) || !Looks(tag) || Looks([]byte("RIFF....WAVE")) {
		t.Fatal("Looks")
	}
	s := NewStream(&trickle{b: b})
	var all []int16
	for {
		f, r, c, err := s.Next()
		if err == io.EOF {
			break
		}
		if err != nil || r != 22050 || c != 2 {
			t.Fatalf("stream: %v %d %d", err, r, c)
		}
		all = append(all, f...)
	}
	if hashPCM(all) != refs["m2_22k"] {
		t.Fatalf("stream decoded %d samples differently", len(all))
	}
	if _, _, _, err := Decode([]byte("not audio at all")); err != ErrNotMP3 {
		t.Fatal(err)
	}
}

type trickle struct{ b []byte }

func (r *trickle) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := min(len(p), 333, len(r.b))
	copy(p, r.b[:n])
	r.b = r.b[n:]
	return n, nil
}

func FuzzDecode(f *testing.F) {
	b, _ := os.ReadFile("testdata/m25_8k.mp3")
	f.Add(b[:2000])
	f.Add([]byte{0xFF, 0xFB, 0x90, 0x64, 0, 0, 0})
	f.Fuzz(func(t *testing.T, b []byte) { _, _, _, _ = Decode(b) })
}
