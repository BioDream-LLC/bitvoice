package mp3

import (
	"errors"
	"io"
)

// ErrNotMP3 is returned for data with no MP3 audio in it.
var ErrNotMP3 = errors.New("mp3: no MP3 (MPEG Layer III) audio found")

// skipID3 returns where the audio starts after an ID3v2 tag (which may hold pictures that look like
// frame headers).
func skipID3(b []byte) int {
	if len(b) < 10 || string(b[:3]) != "ID3" || b[6]|b[7]|b[8]|b[9] >= 0x80 {
		return 0
	}
	n := int(b[6])<<21 | int(b[7])<<14 | int(b[8])<<7 | int(b[9])
	n += 10
	if b[5]&0x10 != 0 {
		n += 10 // a footer
	}
	return min(n, len(b))
}

// Looks says whether b starts like an MP3 file: an ID3v2 tag or a Layer III frame header.
func Looks(b []byte) bool {
	if len(b) >= 3 && string(b[:3]) == "ID3" {
		return true
	}
	return len(b) >= 4 && hdrValid(b) && hdrGetLayer(b) == 1
}

// Decode decodes a whole file: interleaved samples, the sample rate and the number of channels.
func Decode(b []byte) (pcm []int16, rate, channels int, err error) {
	var d Decoder
	frame := make([]int16, MaxSamplesPerFrame)
	for p := skipID3(b); p < len(b); {
		n, info := d.DecodeFrame(b[p:], frame)
		if info.FrameBytes == 0 {
			break
		}
		p += info.FrameBytes
		if n == 0 {
			continue
		}
		if rate == 0 {
			rate, channels = info.Hz, info.Channels
		}
		if info.Channels != channels {
			continue // a channel change mid-file: keep to the first layout
		}
		pcm = append(pcm, frame[:n*info.Channels]...)
	}
	if rate == 0 {
		return nil, 0, 0, ErrNotMP3
	}
	return pcm, rate, channels, nil
}

// Stream decodes MP3 as it arrives (internet radio), frame by frame.
type Stream struct {
	r     io.Reader
	buf   []byte
	d     Decoder
	frame []int16
	eof   bool
	first bool
}

// NewStream reads MP3 from r.
func NewStream(r io.Reader) *Stream {
	return &Stream{r: r, frame: make([]int16, MaxSamplesPerFrame), first: true}
}

const streamKeep = 8 << 10 // frames are checked against the next one: keep several in hand

// Next returns the next frame's samples (interleaved, valid until the next call), its rate and channels.
func (s *Stream) Next() ([]int16, int, int, error) {
	junk := 0
	for {
		for !s.eof && len(s.buf) < streamKeep {
			chunk := make([]byte, 4096)
			n, err := s.r.Read(chunk)
			s.buf = append(s.buf, chunk[:n]...)
			if err == io.EOF {
				s.eof = true
			} else if err != nil {
				return nil, 0, 0, err
			}
		}
		if s.first {
			if skip := skipID3(s.buf); skip > 0 && skip < len(s.buf) {
				s.buf = s.buf[skip:]
			}
			s.first = false
		}
		if len(s.buf) == 0 && s.eof {
			return nil, 0, 0, io.EOF
		}
		n, info := s.d.DecodeFrame(s.buf, s.frame)
		if info.FrameBytes == 0 {
			if s.eof {
				return nil, 0, 0, io.EOF
			}
			s.buf = s.buf[len(s.buf)/2:] // no frame in what is here: drop the older half and read on
			continue
		}
		s.buf = append(s.buf[:0], s.buf[info.FrameBytes:]...)
		if n > 0 {
			return s.frame[:n*info.Channels], info.Hz, info.Channels, nil
		}
		if junk += info.FrameBytes; junk > 1<<20 {
			return nil, 0, 0, ErrNotMP3
		}
	}
}
