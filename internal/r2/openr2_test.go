package r2

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/mf"
)

// circuit is one E1 timeslot to OpenR2 over a Unix datagram socket: the ABCD bits, and A-law audio
// every 20 ms carrying the MF tones.
type circuit struct {
	c        *net.UnixConn
	peer     *net.UnixAddr
	mu       sync.Mutex
	ch       *Chan
	fwdGen   *mf.Generator
	backGen  *mf.Generator
	fwdDet   *mf.Detector
	backDet  *mf.Detector
	forward  bool
	stopTick chan struct{}
}

func (k *circuit) SetBits(b int) { _, _ = k.c.WriteToUnix([]byte{'B', byte(b)}, k.peer) }

func (k *circuit) SetTone(n int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.forward {
		k.fwdGen.Set(n)
		k.backGen.Set(0)
	} else {
		k.backGen.Set(n)
		k.fwdGen.Set(0)
	}
}

func (k *circuit) setForward(f bool) {
	k.mu.Lock()
	k.forward = f
	k.mu.Unlock()
}

// tick sends a frame every 20 ms, as an E1's clock does. It polls the clock and never sleeps: macOS
// defers the timers of a busy machine's processes, even a millisecond's sleep, by seconds, which stops
// the line (the far end's MF timers then run out). Yielding only costs one core while the test runs.
func (k *circuit) tick() {
	pcm := make([]int16, 160)
	next := time.Now()
	for {
		next = next.Add(20 * time.Millisecond)
		for time.Now().Before(next) {
			select {
			case <-k.stopTick:
				return
			default:
			}
			runtime.Gosched()
		}
		k.mu.Lock()
		if k.forward {
			k.fwdGen.Fill(pcm)
		} else {
			k.backGen.Fill(pcm)
		}
		k.mu.Unlock()
		b := make([]byte, 161)
		b[0] = 'A'
		for i, v := range pcm {
			b[1+i] = media.LinearToAlaw(v)
		}
		_, _ = k.c.WriteToUnix(b, k.peer)
	}
}

func (k *circuit) read() {
	buf := make([]byte, 512)
	pcm := make([]int16, 512)
	for {
		pcm = pcm[:cap(pcm)]
		n, _, err := k.c.ReadFromUnix(buf)
		if err != nil {
			return
		}
		switch {
		case n >= 2 && buf[0] == 'B':
			k.ch.Bits(int(buf[1]))
		case n > 1 && buf[0] == 'A': // OpenR2 may write less than a frame at a time
			pcm = pcm[:n-1]
			for i := range pcm {
				pcm[i] = media.AlawToLinear(buf[1+i])
			}
			k.mu.Lock()
			fwd := k.forward
			k.mu.Unlock()
			f := k.fwdDet.Feed(pcm) // what a backward end hears: forward signals
			b := k.backDet.Feed(pcm)
			for _, s := range map[bool][]int{true: b, false: f}[fwd] {
				k.ch.Tone(s)
			}
		}
	}
}

// bothIdle waits until each end has seen the other idle: OpenR2 says so, and our circuit has read
// OpenR2's idle bits (it refuses to seize a line whose far end it has not yet heard).
func bothIdle(t *testing.T, ch *Chan, o *r2oracle) {
	t.Helper()
	o.expect("IDLE")
	for end := time.Now().Add(15 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		idle := make(chan bool, 1)
		ch.do(func() { idle <- ch.call == csIdle && ch.line == lsIdle && ch.casRead == casIdle })
		if <-idle {
			return
		}
		if time.Now().After(end) {
			t.Fatal("our circuit never heard OpenR2's line idle")
		}
	}
}

type r2oracle struct {
	t     *testing.T
	lines chan string
}

func (o *r2oracle) expect(prefix string) string {
	o.t.Helper()
	timeout := time.After(15 * time.Second)
	for {
		select {
		case l, ok := <-o.lines:
			if !ok {
				o.t.Fatalf("OpenR2 ended before %q", prefix)
			}
			if l == "RXFULL" {
				o.t.Log("OpenR2 stopped reading the line: its queue filled")
			}
			if strings.HasPrefix(l, "PROTOCOL_ERROR") && !strings.HasPrefix(prefix, "PROTOCOL") {
				if os.Getenv("R2STACKS") != "" {
					_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
				}
				o.t.Fatalf("OpenR2: %s (waiting for %q)", l, prefix)
			}
			if strings.HasPrefix(l, prefix) {
				return l
			}
		case <-timeout:
			if os.Getenv("R2STACKS") != "" {
				_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
			}
			o.t.Fatalf("OpenR2 never said %q", prefix)
		}
	}
}

// startOpenR2 runs OpenR2 (BITVOICE_TEST_OPENR2 names the harness) on one circuit with a BitVoice
// channel at the other end.
func startOpenR2(t *testing.T, variant string, args []string, ev func(**Chan) Events) (*Chan, *circuit, *r2oracle) {
	bin := os.Getenv("BITVOICE_TEST_OPENR2")
	if bin == "" {
		t.Skip("set BITVOICE_TEST_OPENR2 to the OpenR2 harness")
	}
	dir, _ := os.MkdirTemp("/tmp", "r2")
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "s")
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetReadBuffer(1 << 20) // room for jitter: neither end ever blocks the other
	_ = c.SetWriteBuffer(1 << 20)
	names := map[string]string{"itu": "itu", "ar": "ar", "br": "br", "cz": "cz", "co": "co", "ec": "ec", "mx": "mx", "ph": "ph", "ve": "ve"}
	cmd := exec.Command(bin, append([]string{path, names[variant]}, args...)...)
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	o := &r2oracle{t: t, lines: make(chan string, 64)}
	go func() {
		s := bufio.NewScanner(out)
		for s.Scan() {
			o.lines <- s.Text()
		}
		close(o.lines)
	}()
	o.expect("READY")
	k := &circuit{c: c, peer: &net.UnixAddr{Name: path + ".c", Net: "unixgram"}, fwdGen: mf.NewGenerator(mf.Forward, -10),
		backGen: mf.NewGenerator(mf.Backward, -10), fwdDet: mf.NewDetector(mf.Forward), backDet: mf.NewDetector(mf.Backward),
		stopTick: make(chan struct{})}
	var ch *Chan
	ch = New(Variants[variant], k, ev(&ch))
	k.ch = ch
	t.Cleanup(func() { close(k.stopTick); ch.Close() })
	go k.read()
	go k.tick()
	ch.Start()
	return ch, k, o
}

// BitVoice places calls to OpenR2 in each variant: OpenR2 is offered the number and BitVoice's
// caller's number, accepts and answers; BitVoice hears both, and hangs up.
func TestAgainstOpenR2Forward(t *testing.T) {
	for _, v := range Codes() {
		t.Run(v, func(t *testing.T) {
			got := make(chan string, 16)
			ch, k, o := startOpenR2(t, v, []string{"answer"}, func(c **Chan) Events {
				return Events{Accepted: func(bool) { got <- "accepted" }, Answered: func() { got <- "answered" },
					Disconnected: func(cs Cause) { got <- "disconnected " + cs.String(); (*c).Hangup() }, Ended: func() { got <- "ended" }}
			})
			bothIdle(t, ch, o)
			k.setForward(true)
			if err := ch.Dial("3125550199", "5550100", CategoryNational, false); err != nil {
				t.Fatal(err)
			}
			l := o.expect("OFFERED")
			if !strings.Contains(l, "dnis=5550100") || !strings.Contains(l, "ani=3125550199") {
				t.Fatalf("OpenR2: %s", l)
			}
			want(t, got, "accepted")
			want(t, got, "answered")
			ch.Hangup()
			o.expect("DISCONNECT")
			want(t, got, "ended")
		})
	}
}

// OpenR2 calls BitVoice in each variant: BitVoice is offered the number and the caller's, accepts and
// answers; OpenR2 hangs up.
func TestAgainstOpenR2Backward(t *testing.T) {
	for _, v := range Codes() {
		t.Run(v, func(t *testing.T) {
			got := make(chan string, 16)
			ch, _, o := startOpenR2(t, v, []string{"call", "3125550199", "5550100"}, func(c **Chan) Events {
				return Events{
					Offered: func(ani, dnis string, cat int, restricted bool) {
						got <- "offered " + ani + " " + dnis
						(*c).Accept(true)
						time.AfterFunc(300*time.Millisecond, (*c).Answer)
					},
					Disconnected: func(cs Cause) { got <- "disconnected " + cs.String(); (*c).Hangup() },
					Ended:        func() { got <- "ended" }}
			})
			_ = ch
			o.expect("DIALLED")
			l := want(t, got, "offered")
			if l != "offered 3125550199 5550100" {
				t.Fatalf("BitVoice got %q", l)
			}
			o.expect("ACCEPTED")
			o.expect("ANSWERED")
			o.expect("HUNGUP")
			want(t, got, "disconnected")
			want(t, got, "ended")
		})
	}
}

// OpenR2 refuses BitVoice's call as busy.
func TestAgainstOpenR2Busy(t *testing.T) {
	got := make(chan string, 16)
	ch, k, o := startOpenR2(t, "itu", []string{"busy"}, func(c **Chan) Events {
		return Events{Disconnected: func(cs Cause) { got <- "disconnected " + cs.String(); (*c).Hangup() }, Ended: func() { got <- "ended" }}
	})
	bothIdle(t, ch, o)
	k.setForward(true)
	if err := ch.Dial("", "5550100", CategoryNational, true); err != nil {
		t.Fatal(err)
	}
	o.expect("OFFERED")
	want(t, got, "disconnected busy")
	want(t, got, "ended")
}

func want(t *testing.T, ch chan string, prefix string) string {
	t.Helper()
	timeout := time.After(15 * time.Second)
	for {
		select {
		case s := <-ch:
			if strings.HasPrefix(s, prefix) {
				return s
			}
		case <-timeout:
			t.Fatalf("never %q", prefix)
		}
	}
}
