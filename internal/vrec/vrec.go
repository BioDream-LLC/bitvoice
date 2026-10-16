// Package vrec records a live video RTP stream to a file as it was sent, H.264 (RFC 6184) or VP8
// (RFC 7741), each frame with its own RTP time in an IVF file (H.264 frames in Annex B form). The
// pictures are not decoded or re-encoded, so the file holds the caller's own bitstream and timing;
// ffmpeg, VLC and forensic video tools read it.
package vrec

import (
	"encoding/binary"
	"errors"
	"io"
	"strings"
)

// Recorder turns RTP packets into frames and writes them. It is not safe for use by several
// goroutines at once.
type Recorder struct {
	w      io.Writer
	h264   bool
	err    error
	frames int
	bytes  int64

	// reordering: packets are held up to `window` deep and written in sequence order
	held    map[uint16][]byte
	next    uint16
	started bool
	written bool // a packet has been passed on: earlier ones are late from now on
	closing bool

	// the frame being assembled
	ts      uint32
	frame   []byte
	broken  bool // a packet of this frame was lost: drop it rather than write a damaged picture
	firstTS uint32
	fu      bool // inside an H.264 FU-A
	ivfHdr  bool
	width   uint16
	height  uint16
	seenKey bool // nothing is written before the first key frame: a decoder cannot start earlier
}

const window = 32

// New records codec ("H264" or "VP8") to w.
func New(w io.Writer, codec string) (*Recorder, error) {
	switch strings.ToUpper(codec) {
	case "H264":
		return &Recorder{w: w, h264: true, held: map[uint16][]byte{}}, nil
	case "VP8":
		return &Recorder{w: w, held: map[uint16][]byte{}}, nil
	}
	return nil, errors.New("vrec: only H.264 and VP8 are recorded")
}

// Frames is how many pictures were written; Bytes how many bytes.
func (r *Recorder) Frames() int       { return r.frames }
func (r *Recorder) Bytes() int64      { return r.bytes }
func (r *Recorder) Err() error        { return r.err }
func (r *Recorder) Extension() string { return ".ivf" }

// Write takes one RTP packet (decrypted).
func (r *Recorder) Write(pkt []byte) {
	if r.err != nil || len(pkt) < 12 || pkt[0]>>6 != 2 {
		return
	}
	seq := binary.BigEndian.Uint16(pkt[2:])
	if !r.started {
		r.started, r.next = true, seq
	}
	if d := seq - r.next; d >= 0x8000 {
		if r.written || r.next-seq > window {
			return // older than what was already written: too late
		}
		r.next = seq // the stream's first packets came out of order
	}
	r.held[seq] = append([]byte(nil), pkt...)
	for len(r.held) > 0 {
		p, ok := r.held[r.next]
		if !ok {
			if len(r.held) < window {
				return
			}
			r.broken = true // the gap is not coming: skip it
			for {
				r.next++
				if _, ok := r.held[r.next]; ok {
					break
				}
			}
			continue
		}
		if !r.written && len(r.held) < window/4 && !r.closing {
			return // hold a few at the start, in case an earlier one is still coming
		}
		delete(r.held, r.next)
		r.next++
		r.written = true
		r.packet(p)
	}
}

// Close writes what is held and the last frame.
func (r *Recorder) Close() error {
	r.closing = true
	for len(r.held) > 0 && r.err == nil {
		if p, ok := r.held[r.next]; ok {
			delete(r.held, r.next)
			r.packet(p)
		} else {
			r.broken = true
		}
		r.next++
	}
	r.flush()
	return r.err
}

func (r *Recorder) packet(p []byte) {
	cc := int(p[0] & 0x0f)
	off := 12 + 4*cc
	if p[0]&0x10 != 0 { // header extension
		if len(p) < off+4 {
			return
		}
		off += 4 + 4*int(binary.BigEndian.Uint16(p[off+2:]))
	}
	end := len(p)
	if p[0]&0x20 != 0 { // padding
		if n := int(p[end-1]); n <= end-off {
			end -= n
		}
	}
	if off >= end {
		return
	}
	ts := binary.BigEndian.Uint32(p[4:])
	if len(r.frame) > 0 || r.fu {
		if ts != r.ts {
			r.flush()
		}
	}
	r.ts = ts
	if r.h264 {
		r.h264Payload(p[off:end])
	} else {
		r.vp8Payload(p[off:end])
	}
	if p[1]&0x80 != 0 { // marker: the frame is complete
		r.flush()
	}
}

var startCode = []byte{0, 0, 0, 1}

func (r *Recorder) nal(n []byte) {
	if len(n) == 0 {
		return
	}
	r.frame = append(r.frame, startCode...)
	r.frame = append(r.frame, n...)
}

func (r *Recorder) h264Payload(b []byte) {
	switch t := b[0] & 0x1f; {
	case t >= 1 && t <= 23:
		r.nal(b)
	case t == 24: // STAP-A
		for b = b[1:]; len(b) >= 2; {
			n := int(binary.BigEndian.Uint16(b))
			if n > len(b)-2 {
				r.broken = true
				return
			}
			r.nal(b[2 : 2+n])
			b = b[2+n:]
		}
	case t == 28: // FU-A
		if len(b) < 2 {
			return
		}
		start, stop := b[1]&0x80 != 0, b[1]&0x40 != 0
		if start {
			r.fu = true
			r.frame = append(r.frame, startCode...)
			r.frame = append(r.frame, b[0]&0xe0|b[1]&0x1f)
		} else if !r.fu {
			r.broken = true // the start of this NAL was lost
			return
		}
		r.frame = append(r.frame, b[2:]...)
		if stop {
			r.fu = false
		}
	default: // STAP-B, MTAP, FU-B: not used by packetization-mode 1 senders
		r.broken = true
	}
}

func (r *Recorder) vp8Payload(b []byte) {
	// RFC 7741 payload descriptor
	x, s := b[0]&0x80 != 0, b[0]&0x10 != 0
	pid := b[0] & 0x07
	i := 1
	if x {
		if len(b) < 2 {
			return
		}
		ext := b[1]
		i = 2
		if ext&0x80 != 0 { // PictureID
			if i < len(b) && b[i]&0x80 != 0 {
				i += 2
			} else {
				i++
			}
		}
		if ext&0x40 != 0 { // TL0PICIDX
			i++
		}
		if ext&0x30 != 0 { // TID/KEYIDX
			i++
		}
	}
	if i >= len(b) {
		return
	}
	if s && pid == 0 {
		if len(r.frame) > 0 {
			r.flush()
		}
		r.frame = append(r.frame[:0], b[i:]...)
		return
	}
	if len(r.frame) == 0 {
		r.broken = true // the frame's first part was lost
		return
	}
	r.frame = append(r.frame, b[i:]...)
}

func (r *Recorder) keyFrame() bool {
	if r.h264 {
		for f := r.frame; len(f) > 4; {
			i := indexStart(f[4:])
			n := f[4:]
			if i >= 0 {
				n = f[4 : 4+i]
			}
			if len(n) > 0 && (n[0]&0x1f == 5 || n[0]&0x1f == 7) {
				return true
			}
			if i < 0 {
				break
			}
			f = f[4+i:]
		}
		return false
	}
	// H.264 in IVF: the size is read from the SPS by the decoder; IVF's own fields stay 0.
	// VP8 frame tag (RFC 6386 9.1): bit 0 clear is a key frame, with its size after the start code
	if len(r.frame) < 10 || r.frame[0]&1 != 0 {
		return false
	}
	if r.width == 0 {
		r.width = binary.LittleEndian.Uint16(r.frame[6:]) & 0x3fff
		r.height = binary.LittleEndian.Uint16(r.frame[8:]) & 0x3fff
	}
	return true
}

func indexStart(b []byte) int {
	for i := 0; i+4 <= len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 0 && b[i+3] == 1 {
			return i
		}
	}
	return -1
}

func (r *Recorder) flush() {
	defer func() { r.frame, r.broken, r.fu = r.frame[:0], false, false }()
	if len(r.frame) == 0 || r.err != nil {
		return
	}
	if r.broken {
		if r.keyFrameNeededAfterLoss() {
			r.seenKey = false
		}
		return
	}
	key := r.keyFrame()
	if !r.seenKey {
		if !key {
			return
		}
		if r.frames == 0 {
			r.firstTS = r.ts // the file's clock starts at its first picture, and never restarts
		}
		r.seenKey = true
	}
	{
		if !r.ivfHdr {
			r.ivfHdr = true
			h := make([]byte, 32)
			copy(h, "DKIF")
			binary.LittleEndian.PutUint16(h[6:], 32)
			copy(h[8:], "VP80")
			if r.h264 {
				copy(h[8:], "H264")
			}
			binary.LittleEndian.PutUint16(h[12:], r.width)
			binary.LittleEndian.PutUint16(h[14:], r.height)
			binary.LittleEndian.PutUint32(h[16:], 90000) // time base: the RTP clock
			binary.LittleEndian.PutUint32(h[20:], 1)
			r.write(h)
		}
		fh := make([]byte, 12)
		binary.LittleEndian.PutUint32(fh, uint32(len(r.frame)))
		binary.LittleEndian.PutUint64(fh[4:], uint64(r.ts-r.firstTS))
		r.write(fh)
		r.write(r.frame)
	}
	r.frames++
}

// keyFrameNeededAfterLoss: after a damaged frame later pictures refer to a picture the file lacks,
// so writing resumes at the next key frame (the caller's phone sends one when asked, by PLI).
func (r *Recorder) keyFrameNeededAfterLoss() bool { return true }

// NeedsKeyFrame says the recording is waiting for a full picture (to ask the sender for one).
func (r *Recorder) NeedsKeyFrame() bool { return !r.seenKey }

func (r *Recorder) write(b []byte) {
	if r.err != nil {
		return
	}
	n, err := r.w.Write(b)
	r.bytes += int64(n)
	r.err = err
}
