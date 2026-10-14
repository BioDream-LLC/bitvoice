package media

import (
	"errors"

	"github.com/biodream-llc/bitvoice/internal/media/mp3"
)

// ErrAudio is returned for a file that is neither WAV nor MP3.
var ErrAudio = errors.New("media: upload a WAV or MP3 file")

// DecodeAudio reads a WAV or MP3 file as 8 kHz mono audio.
func DecodeAudio(b []byte) ([]int16, error) {
	if len(b) >= 12 && string(b[:4]) == "RIFF" {
		return DecodeWAV(b)
	}
	if mp3.Looks(b) {
		pcm, rate, ch, err := mp3.Decode(b)
		if err != nil {
			return nil, err
		}
		return resample(downmix(pcm, ch), rate), nil
	}
	if pcm, rate, ch, err := mp3.Decode(b); err == nil { // an MP3 with junk before the first frame
		return resample(downmix(pcm, ch), rate), nil
	}
	return nil, ErrAudio
}

func downmix(pcm []int16, ch int) []float64 {
	n := len(pcm) / ch
	out := make([]float64, n)
	for i := range n {
		var s float64
		for c := range ch {
			s += float64(pcm[i*ch+c])
		}
		out[i] = s / float64(ch)
	}
	return out
}

// Resampler turns a stream of audio at any rate into 8 kHz, piece by piece, as resample does for a
// whole file: averaging each output sample's span going down, interpolating going up.
type Resampler struct {
	n    int       // output samples made since the rate was set
	base int       // input samples dropped since then
	tail []float64 // input kept from the last piece
	rate int
}

// Push takes interleaved samples with ch channels at rate and returns the 8 kHz samples they complete.
func (r *Resampler) Push(pcm []int16, rate, ch int) []int16 {
	if rate != r.rate {
		r.rate, r.n, r.base, r.tail = rate, 0, 0, nil
	}
	in := append(r.tail, downmix(pcm, ch)...)
	step := float64(rate) / Rate
	var out []int16
	for {
		// The same arithmetic as resample on the whole stream: n*step, from the stream's start.
		start := float64(r.n) * step
		if step > 1 {
			a, z := int(start)-r.base, int(start+step)-r.base
			if z > len(in) {
				break
			}
			var s float64
			for _, v := range in[a:z] {
				s += v
			}
			out = append(out, clamp(s/float64(max(z-a, 1))))
		} else {
			ga := int(start)
			a := ga - r.base
			if a+1 >= len(in) {
				break
			}
			f := start - float64(ga)
			out = append(out, clamp(in[a]+(in[a+1]-in[a])*f))
		}
		r.n++
	}
	drop := min(max(int(float64(r.n)*step)-r.base, 0), len(in))
	r.tail = append([]float64(nil), in[drop:]...)
	r.base += drop
	return out
}
