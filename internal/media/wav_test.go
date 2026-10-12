package media

import (
	"encoding/binary"
	"testing"
	"time"
)

func TestWAVRoundTrip(t *testing.T) {
	pcm := ToneSamples(ToneBeep)
	got, err := DecodeWAV(EncodeWAV(pcm))
	if err != nil || len(got) != len(pcm) {
		t.Fatalf("decode: %v, %d samples, want %d", err, len(got), len(pcm))
	}
	for i := range pcm {
		if got[i] != pcm[i] {
			t.Fatalf("sample %d: %d, want %d", i, got[i], pcm[i])
		}
	}
}

// A 48 kHz stereo file, as a browser or a laptop records, comes out as 8 kHz mono at the same length.
func TestWAVResample(t *testing.T) {
	const rate, secs = 48000, 2
	data := make([]byte, rate*secs*4)
	for i := range rate * secs {
		binary.LittleEndian.PutUint16(data[i*4:], uint16(int16(1000)))
		binary.LittleEndian.PutUint16(data[i*4+2:], uint16(int16(3000)))
	}
	b := []byte("RIFF\x00\x00\x00\x00WAVEfmt ")
	b = binary.LittleEndian.AppendUint32(b, 16)
	b = binary.LittleEndian.AppendUint16(b, 1)
	b = binary.LittleEndian.AppendUint16(b, 2)
	b = binary.LittleEndian.AppendUint32(b, rate)
	b = binary.LittleEndian.AppendUint32(b, rate*4)
	b = binary.LittleEndian.AppendUint16(b, 4)
	b = binary.LittleEndian.AppendUint16(b, 16)
	b = append(b, "LIST\x04\x00\x00\x00abcd"...) // an unrelated chunk is skipped
	b = append(b, "data"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(data)))
	b = append(b, data...)
	got, err := DecodeWAV(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != Rate*secs || got[100] != 2000 {
		t.Fatalf("%d samples, sample %d", len(got), got[100])
	}
	if _, err := DecodeWAV([]byte("RIFFxxxxWAVEjunk")); err == nil {
		t.Fatal("garbage accepted")
	}
}

func rtp(pt byte, seq uint16, ts uint32, payload []byte) []byte {
	p := make([]byte, 12, 12+len(payload))
	p[0], p[1] = 0x80, pt
	binary.BigEndian.PutUint16(p[2:], seq)
	binary.BigEndian.PutUint32(p[4:], ts)
	return append(p, payload...)
}

func TestCapture(t *testing.T) {
	c := NewCapture(1)
	frame := make([]byte, 160)
	for i := range frame {
		frame[i] = LinearToUlaw(4000)
	}
	c.Packet(rtp(0, 1, 0, frame))
	c.Packet(rtp(0, 1, 0, frame))   // duplicate: ignored
	c.Packet(rtp(0, 3, 320, frame)) // one lost packet: becomes silence
	// Key 5 then #: the end packet is sent three times and counts once.
	for range 3 {
		c.Packet(rtp(101, 4, 480, []byte{5, 0x80, 0, 160}))
	}
	c.Packet(rtp(101, 7, 960, []byte{11, 0x80, 0, 160}))
	if k := <-c.Keys; k != '5' {
		t.Fatalf("key %c", k)
	}
	if k := <-c.Keys; k != '#' {
		t.Fatalf("key %c", k)
	}
	if len(c.Keys) != 0 {
		t.Fatal("repeated end packets counted twice")
	}
	pcm := c.Stop()
	if len(pcm) != 480 || pcm[200] != 0 || pcm[0] < 3800 {
		t.Fatalf("%d samples, %d %d", len(pcm), pcm[0], pcm[200])
	}
	full := NewCapture(1)
	for i := range uint16(60) {
		full.Packet(rtp(8, i, uint32(i)*160, frame))
	}
	if !full.Full() || len(full.Stop()) != Rate {
		t.Fatal("maximum length not enforced")
	}
}

func TestTrimSilence(t *testing.T) {
	pcm := append(ToneSamples(ToneBeep), make([]int16, Rate*3)...)
	got := TrimSilence(pcm)
	if len(got) > len(ToneSamples(ToneBeep))+Rate/2 || len(got) < Rate/4 {
		t.Fatalf("trimmed to %d samples", len(got))
	}
}

func TestTrackKeepsTime(t *testing.T) {
	start := time.Unix(1000, 0)
	clock := start
	tr := NewTrack(start, 10)
	tr.now = func() time.Time { return clock }
	frame := make([]byte, 160)
	for i := range frame {
		frame[i] = LinearToUlaw(4000)
	}
	clock = start.Add(20 * time.Millisecond)
	tr.Packet(rtp(0, 1, 0, frame))
	clock = start.Add(1020 * time.Millisecond) // a second of silence (say on hold)
	tr.Packet(rtp(0, 2, 160, frame))
	pcm := tr.Samples()
	if len(pcm) != Rate+160 || pcm[100] < 3800 || pcm[4000] != 0 || pcm[Rate+50] < 3800 {
		t.Fatalf("%d samples", len(pcm))
	}
	st := EncodeStereoWAV(pcm, pcm[:10])
	if len(st) != 44+len(pcm)*4 {
		t.Fatal(len(st))
	}
}
