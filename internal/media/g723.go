package media

import (
	"context"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// G.723.1 (5.3 and 6.3 kbit/s) is converted by ffmpeg, run as a separate program, when Settings allow
// it: BitVoice carries no G.723.1 code of its own (the ITU's reference code carries no licence that
// allows including it). Each direction of a converted call has an ffmpeg decoding (raw G.723.1 to
// 8 kHz linear) or encoding (to 6.3 kbit/s, the rate ffmpeg's encoder makes). Audio flows through
// them without waiting: what a packet in gives back is the audio ffmpeg has finished, a few frames
// behind, so the relay never blocks on the converter.

// PTG723 is G.723.1's static payload type (RFC 3551).
const PTG723 = 4

var g723FFmpeg atomic.Pointer[string]

// SetG723Converter switches G.723.1 conversion on with the ffmpeg program named ("" switches it off).
func SetG723Converter(ffmpeg string) {
	if ffmpeg == "" {
		g723FFmpeg.Store(nil)
		return
	}
	g723FFmpeg.Store(&ffmpeg)
}

func g723On() (string, bool) {
	p := g723FFmpeg.Load()
	if p == nil {
		return "", false
	}
	return *p, true
}

// G723Frame is one G.723.1 frame's length from its first octet: 24 (6.3 kbit/s), 20 (5.3), 4 (SID,
// comfort noise), 1 (untransmitted).
func G723Frame(b0 byte) int {
	switch b0 & 3 {
	case 0:
		return 24
	case 1:
		return 20
	case 2:
		return 4
	}
	return 1
}

const g723Samples = 240 // 30 ms at 8 kHz

// G723Idle is how long an ffmpeg may go without audio before it is stopped (a call that ended, or a
// long silence; the next packet starts a new one).
var G723Idle = 5 * time.Second

// pipeProc is one ffmpeg: what is written goes in, what it makes comes out on a queue.
type pipeProc struct {
	cancel context.CancelFunc
	in     io.WriteCloser
	mu     sync.Mutex
	out    []byte
	dead   bool
	last   time.Time
}

func startPipe(ffmpeg string, args ...string) *pipeProc {
	ctx, cancel := context.WithCancel(context.Background())
	base := []string{"-hide_banner", "-loglevel", "error", "-flags", "low_delay", // not -fflags nobuffer: it loses G.723.1 frames
		"-probesize", "32", "-analyzeduration", "0"}
	cmd := exec.CommandContext(ctx, ffmpeg, append(base, args...)...)
	in, err1 := cmd.StdinPipe()
	out, err2 := cmd.StdoutPipe()
	p := &pipeProc{cancel: cancel, in: in, last: time.Now()}
	if err1 != nil || err2 != nil || cmd.Start() != nil {
		cancel()
		p.dead = true
		return p
	}
	go func() {
		t := time.NewTicker(G723Idle / 5)
		defer t.Stop()
		for range t.C {
			p.mu.Lock()
			idle, dead := time.Since(p.last) > G723Idle, p.dead
			p.mu.Unlock()
			if dead {
				return
			}
			if idle {
				p.close()
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := out.Read(buf)
			if n > 0 {
				p.mu.Lock()
				if len(p.out) < 64*1024 { // a converter falling behind loses audio rather than memory
					p.out = append(p.out, buf[:n]...)
				}
				p.mu.Unlock()
			}
			if err != nil {
				p.mu.Lock()
				p.dead = true
				p.mu.Unlock()
				_ = cmd.Wait()
				return
			}
		}
	}()
	return p
}

func (p *pipeProc) write(b []byte) {
	p.mu.Lock()
	dead := p.dead
	p.last = time.Now()
	p.mu.Unlock()
	if !dead {
		_, _ = p.in.Write(b)
	}
}

// take removes up to n bytes made so far.
func (p *pipeProc) take(n int) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.out) < n {
		return nil
	}
	b := append([]byte(nil), p.out[:n]...)
	p.out = p.out[n:]
	return b
}

func (p *pipeProc) takeAll() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := p.out
	p.out = nil
	return b
}

func (p *pipeProc) close() {
	p.mu.Lock()
	p.dead = true
	p.mu.Unlock()
	_ = p.in.Close()
	p.cancel()
}

func (p *pipeProc) isDead() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dead
}

// g723v is one direction's G.723.1 through ffmpeg.
type g723v struct {
	ffmpeg string
	once   sync.Once
	dec    *pipeProc
	enc    *pipeProc
	encBuf []byte // encoded frames not yet sent
	pcm    []int16
	odd    []byte // a byte of a sample split between two reads
}

func newG723() voice {
	ff, ok := g723On()
	if !ok {
		return nil
	}
	return &g723v{ffmpeg: ff}
}

func (*g723v) rate() int      { return 8000 }
func (*g723v) clock() int     { return 8000 }
func (*g723v) take(n int) int { return n - n%g723Samples }

// decode gives as many samples as the payload's frames hold: ffmpeg's audio when it has it, silence
// while it starts (the first frames of a call).
func (g *g723v) decode(p []byte) []int16 {
	if g.dec == nil || g.dec.isDead() {
		g.dec = startPipe(g.ffmpeg, "-f", "g723_1", "-i", "pipe:0", "-flush_packets", "1", "-f", "s16le", "-ar", "8000", "-ac", "1", "pipe:1")
	}
	frames := 0
	for i := 0; i < len(p); {
		n := G723Frame(p[i])
		if i+n > len(p) {
			break
		}
		if n > 1 {
			frames++
		}
		i += n
	}
	g.dec.write(p)
	raw := append(g.odd, g.dec.takeAll()...)
	g.odd = nil
	if len(raw)%2 == 1 {
		g.odd = []byte{raw[len(raw)-1]}
		raw = raw[:len(raw)-1]
	}
	g.pcm = append(g.pcm, splitPCM(raw)...)
	want := frames * g723Samples
	if len(g.pcm) > 10*g723Samples {
		g.pcm = g.pcm[len(g.pcm)-4*g723Samples:] // bound the delay if ffmpeg came out in a burst
	}
	out := make([]int16, want)
	n := copy(out, g.pcm)
	g.pcm = g.pcm[n:]
	return out
}

func splitPCM(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8)
	}
	return out
}

// encode sends the audio to ffmpeg and gives back the frames it has finished (nil while it starts).
func (g *g723v) encode(pcm []int16) []byte {
	if g.enc == nil || g.enc.isDead() {
		g.enc = startPipe(g.ffmpeg, "-f", "s16le", "-ar", "8000", "-ac", "1", "-i", "pipe:0", "-c:a", "g723_1", "-b:a", "6300",
			"-flush_packets", "1", "-f", "g723_1", "pipe:1")
	}
	b := make([]byte, 2*len(pcm))
	for i, v := range pcm {
		b[2*i], b[2*i+1] = byte(v), byte(uint16(v)>>8)
	}
	g.enc.write(b)
	g.encBuf = append(g.encBuf, g.enc.takeAll()...)
	want := len(pcm) / g723Samples
	var out []byte
	for k := 0; k < want && len(g.encBuf) > 0; k++ {
		n := G723Frame(g.encBuf[0])
		if len(g.encBuf) < n {
			break
		}
		out = append(out, g.encBuf[:n]...)
		g.encBuf = g.encBuf[n:]
	}
	for len(g.encBuf) > 10*24 { // bound the delay: whole frames dropped from the front
		g.encBuf = g.encBuf[G723Frame(g.encBuf[0]):]
	}
	return out
}

// Close stops the ffmpeg processes.
func (g *g723v) Close() {
	g.once.Do(func() {
		if g.dec != nil {
			g.dec.close()
		}
		if g.enc != nil {
			g.enc.close()
		}
	})
}
