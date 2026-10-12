package media

import (
	"encoding/binary"
	"math"
	"slices"
)

// SplitStereoWAV returns the two channels of a 16-bit PCM stereo WAV, as BitVoice records calls (the
// caller on the left, the other side on the right). It returns ErrWAV for anything else.
func SplitStereoWAV(b []byte) (left, right []int16, rate int, err error) {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, nil, 0, ErrWAV
	}
	var format, channels, bits uint16
	var data []byte
	for p := 12; p+8 <= len(b); {
		id := string(b[p : p+4])
		size := int(binary.LittleEndian.Uint32(b[p+4:]))
		body := b[p+8:]
		if size > len(body) {
			size = len(body)
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, nil, 0, ErrWAV
			}
			format, channels = binary.LittleEndian.Uint16(body), binary.LittleEndian.Uint16(body[2:])
			rate = int(binary.LittleEndian.Uint32(body[4:]))
			bits = binary.LittleEndian.Uint16(body[14:])
		case "data":
			data = body[:size]
		}
		p += 8 + size + size%2
	}
	if format != 1 || bits != 16 || channels != 2 || data == nil || rate < 4000 {
		return nil, nil, 0, ErrWAV
	}
	n := len(data) / 4
	left, right = make([]int16, n), make([]int16, n)
	for i := range n {
		left[i] = int16(binary.LittleEndian.Uint16(data[i*4:]))
		right[i] = int16(binary.LittleEndian.Uint16(data[i*4+2:]))
	}
	return left, right, rate, nil
}

// Segment is a stretch of speech, in samples.
type Segment struct{ Start, End int }

// SpeechSegments finds where someone is talking in pcm: 20 ms frames louder than the channel's own
// noise floor (and an absolute minimum), joined across pauses shorter than 1.2 s, padded by 0.2 s, at
// least 0.4 s long and split at 30 s so each piece can be transcribed on its own.
func SpeechSegments(pcm []int16, rate int) []Segment {
	frame := rate / 50
	if frame == 0 || len(pcm) < frame {
		return nil
	}
	nf := len(pcm) / frame
	rms := make([]float64, nf)
	for f := range nf {
		var s float64
		for _, v := range pcm[f*frame : (f+1)*frame] {
			s += float64(v) * float64(v)
		}
		rms[f] = math.Sqrt(s / float64(frame))
	}
	// The noise floor: the level that a tenth of the frames are quieter than.
	sorted := append([]float64(nil), rms...)
	slices.Sort(sorted)
	threshold := math.Max(400, sorted[len(sorted)/10]*4)
	gap, pad, minLen, maxLen := 60, 10, 20, 1500 // in frames
	var segs []Segment
	start, last := -1, -1
	flush := func() {
		if start < 0 {
			return
		}
		a, z := max(0, start-pad), min(nf, last+1+pad)
		for a < z {
			b := min(z, a+maxLen)
			if b-a >= minLen {
				segs = append(segs, Segment{a * frame, b * frame})
			}
			a = b
		}
		start = -1
	}
	for f, v := range rms {
		if v < threshold {
			continue
		}
		if start >= 0 && f-last > gap {
			flush()
		}
		if start < 0 {
			start = f
		}
		last = f
	}
	flush()
	return segs
}
