package main

import (
	"encoding/binary"
	"errors"
	"math"
	"time"
)

// Each line of a scenario is sent as a steady tone of its own pitch, as long as the line would take to
// say. BitVoice hears it, cuts it at the pause after it, and sends that piece to its speech service as
// for any 911 call; the demo's stand-in speech service (stt.go) tells the pitch and answers with the
// line. So the demo's live transcripts are made up, and everything around them is real.

const sampleRate = 8000

// pitch is the tone line k is played as.
func pitch(k int) float64 { return 500 + 35*float64(k) }

// speaking is how long a line takes to say.
func speaking(text string) time.Duration {
	d := time.Duration(len(text)) * 65 * time.Millisecond
	return min(max(d, 1600*time.Millisecond), 9*time.Second)
}

// pause is the silence between two lines: longer than BitVoice's cut (1.5 s).
const pause = 2200 * time.Millisecond

// segment is a stretch of one side's sound: a tone from at for dur.
type segment struct {
	at, dur time.Duration
	freq    float64
}

// timeline lays a scenario's lines one after another, from the answer.
func timeline(s int) (caller, taker []segment, end time.Duration) {
	at := 1500 * time.Millisecond
	for i, l := range scenarios[s].lines {
		seg := segment{at: at, dur: speaking(l.text), freq: pitch(lineIndex(s, i))}
		if l.taker {
			taker = append(taker, seg)
		} else {
			caller = append(caller, seg)
		}
		at += seg.dur + pause
	}
	return caller, taker, at + time.Second
}

// sample is the sound of a side at sample n (from the answer).
func sample(segs []segment, n int) int16 {
	t := time.Duration(n) * time.Second / sampleRate
	for _, s := range segs {
		if t >= s.at && t < s.at+s.dur {
			// Soft edges, so the start and end do not click.
			edge := min(t-s.at, s.at+s.dur-t)
			gain := min(1, float64(edge)/float64(20*time.Millisecond))
			return int16(9000 * gain * math.Sin(2*math.Pi*s.freq*float64(n)/sampleRate))
		}
	}
	return 0
}

// whichLine names the line a piece of sound is, or -1 when it is not one of ours (someone really
// speaking, on a visitor's phone): the strongest of the lines' pitches, and only when it carries most
// of the sound.
func whichLine(pcm []int16, rate int) int {
	if len(pcm) < rate/4 {
		return -1
	}
	var total float64
	for _, s := range pcm {
		total += float64(s) * float64(s)
	}
	total /= float64(len(pcm))
	if total < 1e5 { // quieter than our tones by far
		return -1
	}
	best, bestP := -1, 0.0
	for k := range totalLines() {
		if p := goertzel(pcm, rate, pitch(k)); p > bestP {
			best, bestP = k, p
		}
	}
	// A pure tone of amplitude A has mean power A²/2; goertzel returns the power at its frequency on
	// the same scale. Most of the sound must be that one tone.
	if bestP < 0.5*total {
		return -1
	}
	return best
}

// goertzel is the mean power of pcm at freq.
func goertzel(pcm []int16, rate int, freq float64) float64 {
	w := 2 * math.Pi * freq / float64(rate)
	c := 2 * math.Cos(w)
	var s1, s2 float64
	for _, x := range pcm {
		s0 := float64(x) + c*s1 - s2
		s2, s1 = s1, s0
	}
	p := s1*s1 + s2*s2 - c*s1*s2
	n := float64(len(pcm))
	return 2 * p / (n * n)
}

// readWAV reads 16-bit PCM WAV (mono, or the first channel).
func readWAV(b []byte) ([]int16, int, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, 0, errors.New("not a WAV file")
	}
	rate, channels, bits := 0, 1, 16
	for i := 12; i+8 <= len(b); {
		id, size := string(b[i:i+4]), int(binary.LittleEndian.Uint32(b[i+4:i+8]))
		body := b[i+8:]
		if size > len(body) {
			size = len(body)
		}
		switch id {
		case "fmt ":
			if size < 16 || binary.LittleEndian.Uint16(body[0:2]) != 1 {
				return nil, 0, errors.New("not PCM")
			}
			channels = int(binary.LittleEndian.Uint16(body[2:4]))
			rate = int(binary.LittleEndian.Uint32(body[4:8]))
			bits = int(binary.LittleEndian.Uint16(body[14:16]))
		case "data":
			if rate == 0 || bits != 16 || channels < 1 {
				return nil, 0, errors.New("unsupported WAV")
			}
			step := 2 * channels
			pcm := make([]int16, 0, size/step)
			for j := 0; j+step <= size; j += step {
				pcm = append(pcm, int16(binary.LittleEndian.Uint16(body[j:j+2])))
			}
			return pcm, rate, nil
		}
		i += 8 + size + size%2
	}
	return nil, 0, errors.New("no sound in the WAV file")
}
