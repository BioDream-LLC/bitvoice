package ss7

import (
	"bufio"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type pipeLink struct {
	in, out chan []byte
	done    chan struct{}
	mu      sync.Mutex
	drop    func([]byte) bool
}

func pair() (*pipeLink, *pipeLink) {
	a, b, done := make(chan []byte, 1024), make(chan []byte, 1024), make(chan struct{})
	return &pipeLink{in: a, out: b, done: done}, &pipeLink{in: b, out: a, done: done}
}

func (l *pipeLink) ReadFrame() ([]byte, error) {
	select {
	case f := <-l.in:
		return f, nil
	case <-l.done:
		return nil, errors.New("closed")
	}
}

func (l *pipeLink) WriteFrame(f []byte) error {
	l.mu.Lock()
	d := l.drop
	l.mu.Unlock()
	if d != nil && d(f) {
		return nil
	}
	select {
	case l.out <- append([]byte(nil), f...):
	case <-l.done:
	default: // a full line drops, as a real one would lose frames
	}
	return nil
}

func waitUp(t *testing.T, ss ...*SS7) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for _, s := range ss {
		for !s.IsUp() {
			if time.Now().After(deadline) {
				t.Fatal("the link set did not come up")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func next(t *testing.T, c *Call, kind string) Event {
	t.Helper()
	timeout := time.After(8 * time.Second)
	for {
		select {
		case e := <-c.Events:
			if e.Kind == kind {
				return e
			}
		case <-timeout:
			t.Fatalf("no %s event on CIC %d", kind, c.CIC)
		}
	}
}

func TestLabelsAndNumbers(t *testing.T) {
	for _, ansi := range []bool{false, true} {
		l := Label{DPC: 1234, OPC: 4321, SLS: 9}
		if ansi {
			l = Label{DPC: 0x010203, OPC: 0x040506, SLS: 17}
		}
		got, ok := parseLabel(l.bytes(ansi), ansi)
		if !ok || got != l {
			t.Errorf("ansi=%v: %+v became %+v", ansi, l, got)
		}
	}
	for _, n := range []string{"2125551234", "212555123", "+442071234567", "100"} {
		d, _ := parseNumber(numberParam(n, false, false))
		if d != n {
			t.Errorf("%s became %s", n, d)
		}
	}
	if _, r := parseNumber(numberParam("3125550199", true, true)); !r {
		t.Error("presentation restricted lost")
	}
	if g := groups([]int{1, 2, 3, 5, 6, 40}); len(g) != 3 || g[0] != [2]int{1, 3} || g[1] != [2]int{5, 6} {
		t.Errorf("groups %v", g)
	}
}

func fast() {
	T4E, Fill = 200*time.Millisecond, 10*time.Millisecond
}

// Two BitVoice signalling points on one link: alignment, link test and traffic restart; a call each
// way with the numbers and the caller's name (ANSI) carried, answered and released; a busy refusal;
// and MSUs lost on the line are sent again.
func TestBothEnds(t *testing.T) {
	fast()
	for _, ansi := range []bool{false, true} {
		t.Run(map[bool]string{false: "itu", true: "ansi"}[ansi], func(t *testing.T) {
			la, lb := pair()
			defer close(la.done)
			cics := []int{1, 2, 3, 4}
			a := New(la, Config{MTPConfig: MTPConfig{ANSI: ansi, OPC: 100, APC: 200, NI: NINational, Emergency: true}, CICs: cics})
			b := New(lb, Config{MTPConfig: MTPConfig{ANSI: ansi, OPC: 200, APC: 100, NI: NINational, Emergency: true}, CICs: cics})
			in := make(chan *Call, 4)
			b.Incoming = func(c *Call) { in <- c }
			a.Incoming = func(c *Call) { in <- c }
			go a.Run()
			go b.Run()
			waitUp(t, a, b)
			// Lose every third MSU for a while: basic error correction sends them again.
			n := 0
			la.mu.Lock()
			la.drop = func(f []byte) bool {
				if len(f) > 3 && f[2]&0x3F > 2 {
					n++
					return n%3 == 0
				}
				return false
			}
			la.mu.Unlock()
			c, err := a.Dial(Setup{Called: "2125551234", Calling: "3125550199", Name: "Front Desk"})
			if err != nil {
				t.Fatal(err)
			}
			if c.CIC%2 != 1 { // 100 < 200: a controls the odd circuits
				t.Fatalf("took CIC %d", c.CIC)
			}
			got := <-in
			if got.Called != "2125551234" || got.Calling != "3125550199" || got.CIC != c.CIC {
				t.Fatalf("b saw %+v", got)
			}
			if ansi && got.Name != "Front Desk" {
				t.Fatalf("name %q", got.Name)
			}
			got.Alerting()
			next(t, c, "alerting")
			got.Answer()
			next(t, c, "connected")
			c.Hangup(CauseNormal)
			if e := next(t, got, "released"); e.Cause != CauseNormal {
				t.Fatalf("cause %d", e.Cause)
			}
			next(t, c, "released")
			la.mu.Lock()
			la.drop = nil
			la.mu.Unlock()

			// b calls a; a is busy.
			c2, err := b.Dial(Setup{Called: "100", Calling: "2125550100", Restricted: true})
			if err != nil {
				t.Fatal(err)
			}
			got = <-in
			if !got.Restricted || got.Calling != "2125550100" {
				t.Fatalf("a saw %+v", got)
			}
			got.Hangup(CauseBusy)
			if e := next(t, c2, "released"); e.Cause != CauseBusy {
				t.Fatalf("cause %d", e.Cause)
			}
			deadline := time.Now().Add(15 * time.Second)
			for len(a.Busy())+len(b.Busy()) != 0 { // a frees its circuit when the RLC arrives
				if time.Now().After(deadline) {
					t.Fatalf("circuits left busy %v %v", a.Busy(), b.Busy())
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

// Dual seizure: both ends take the same circuit at once; the end controlling it keeps its call, the
// other's call is freed for it to go again.
func TestDualSeizure(t *testing.T) {
	fast()
	la, lb := pair()
	defer close(la.done)
	a := New(la, Config{MTPConfig: MTPConfig{OPC: 100, APC: 200, Emergency: true}, CICs: []int{2}})
	b := New(lb, Config{MTPConfig: MTPConfig{OPC: 200, APC: 100, Emergency: true}, CICs: []int{2}})
	in := make(chan *Call, 2)
	a.Incoming = func(c *Call) { in <- c }
	b.Incoming = func(c *Call) { in <- c }
	go a.Run()
	go b.Run()
	waitUp(t, a, b)
	ca, _ := a.Dial(Setup{Called: "1"})
	cb, _ := b.Dial(Setup{Called: "2"})
	next(t, ca, "released") // b (200) controls the even CIC 2: a backs off
	got := <-in
	if got.Called != "2" {
		t.Fatalf("a was offered %+v", got)
	}
	got.Answer()
	next(t, cb, "connected")
}

// gramLink is the signalling link over a Unix datagram socket to the libss7 harness, two FCS octets on
// every frame as DAHDI gives them.
type gramLink struct {
	c    *net.UnixConn
	peer *net.UnixAddr
}

func (g *gramLink) ReadFrame() ([]byte, error) {
	buf := make([]byte, 512)
	for {
		n, _, err := g.c.ReadFromUnix(buf)
		if err != nil {
			return nil, err
		}
		if n >= 5 {
			return append([]byte(nil), buf[:n-2]...), nil
		}
	}
}

func (g *gramLink) WriteFrame(f []byte) error {
	_, err := g.c.WriteToUnix(append(append([]byte(nil), f...), 0, 0), g.peer)
	return err
}

type oracle struct {
	t     *testing.T
	lines chan string
}

func (o *oracle) expect(prefix string) string {
	o.t.Helper()
	timeout := time.After(20 * time.Second)
	for {
		select {
		case l, ok := <-o.lines:
			if !ok {
				o.t.Fatalf("libss7 ended before %q", prefix)
			}
			if strings.HasPrefix(l, prefix) {
				return l
			}
		case <-timeout:
			o.t.Fatalf("libss7 never said %q", prefix)
		}
	}
}

func startOracle(t *testing.T, variant string, script string) (*gramLink, *oracle) {
	bin := os.Getenv("BITVOICE_TEST_LIBSS7")
	if bin == "" {
		t.Skip("set BITVOICE_TEST_LIBSS7 to the libss7 harness")
	}
	dir, _ := os.MkdirTemp("/tmp", "ss7")
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "l")
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	cmd := exec.Command(bin, path, variant, "200", "100", script)
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	o := &oracle{t: t, lines: make(chan string, 64)}
	go func() {
		s := bufio.NewScanner(out)
		for s.Scan() {
			o.lines <- s.Text()
		}
		close(o.lines)
	}()
	// The oracle binds path.c when it is ready to hear us.
	for end := time.Now().Add(15 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(path + ".c"); err == nil {
			break
		}
		if time.Now().After(end) {
			t.Fatal("the libss7 oracle never opened its socket")
		}
	}
	return &gramLink{c: c, peer: &net.UnixAddr{Name: path + ".c", Net: "unixgram"}}, o
}

func waitReset(t *testing.T, s *SS7) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		s.mu.Lock()
		ok := s.isUp
		s.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no GRA for the circuits' reset")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func cfgFor(variant string) Config {
	return Config{MTPConfig: MTPConfig{ANSI: variant == "ansi", OPC: 100, APC: 200, NI: NINational, Emergency: true}, CICs: []int{1, 2, 3, 4}}
}

// BitVoice calls libss7, which answers; BitVoice hangs up. libss7 sees the numbers (and on ANSI the
// caller's name), and the circuits reset at start.
func TestAgainstLibss7Out(t *testing.T) {
	for _, v := range []string{"itu", "ansi"} {
		t.Run(v, func(t *testing.T) {
			Fill = 10 * time.Millisecond
			link, o := startOracle(t, v, "answer")
			s := New(link, cfgFor(v))
			go s.Run()
			o.expect("UP")
			waitUp(t, s)
			waitReset(t, s) // libss7 answered the circuits' reset (GRS) with GRA
			c, err := s.Dial(Setup{Called: "2125551234", Calling: "3125550100", Name: "Front Desk"})
			if err != nil {
				t.Fatal(err)
			}
			l := o.expect("IAM")
			if !strings.Contains(l, "cic="+strconv.Itoa(c.CIC)) || !strings.Contains(l, "called=2125551234") || !strings.Contains(l, "calling=3125550100") {
				t.Fatalf("libss7: %s", l)
			}
			if v == "ansi" && !strings.Contains(l, "name=Front Desk") {
				t.Fatalf("libss7: %s", l)
			}
			o.expect("ANSWERED")
			if e := next(t, c, "connected"); e.Kind != "connected" {
				t.Fatal(e)
			}
			c.Hangup(CauseNormal)
			if l := o.expect("REL"); !strings.Contains(l, "cause=16") {
				t.Fatalf("libss7: %s", l)
			}
			next(t, c, "released")
			s.Close()
		})
	}
}

// libss7 calls BitVoice, BitVoice answers; libss7 hangs up. Then libss7 refuses a call as busy.
func TestAgainstLibss7In(t *testing.T) {
	for _, v := range []string{"itu", "ansi"} {
		t.Run(v, func(t *testing.T) {
			Fill = 10 * time.Millisecond
			link, o := startOracle(t, v, "call")
			s := New(link, cfgFor(v))
			in := make(chan *Call, 1)
			s.Incoming = func(c *Call) { in <- c }
			go s.Run()
			o.expect("UP")
			o.expect("DIALLED")
			var c *Call
			select {
			case c = <-in:
			case <-time.After(10 * time.Second):
				t.Fatal("libss7's IAM did not arrive")
			}
			if c.CIC != 2 || c.Called != "2125551234" || c.Calling != "3125550199" {
				t.Fatalf("got %+v", c)
			}
			if v == "ansi" && c.Name != "Ann Smith" {
				t.Fatalf("name %q", c.Name)
			}
			c.Alerting()
			o.expect("ACM")
			c.Answer()
			o.expect("ANM")
			o.expect("HUNGUP")
			next(t, c, "released")
			o.expect("RLC")
			s.Close()
		})
	}
	t.Run("busy", func(t *testing.T) {
		link, o := startOracle(t, "itu", "busy")
		s := New(link, cfgFor("itu"))
		go s.Run()
		o.expect("UP")
		waitUp(t, s)
		c, err := s.Dial(Setup{Called: "5551234"})
		if err != nil {
			t.Fatal(err)
		}
		if e := next(t, c, "released"); e.Cause != CauseBusy {
			t.Fatalf("cause %d", e.Cause)
		}
		s.Close()
	})
	t.Run("block", func(t *testing.T) {
		link, o := startOracle(t, "itu", "block")
		s := New(link, cfgFor("itu"))
		go s.Run()
		o.expect("UP")
		o.expect("BLO")
		o.expect("BLA")
		s.mu.Lock()
		blocked := s.blocked[3]
		s.mu.Unlock()
		if !blocked {
			t.Fatal("CIC 3 not blocked")
		}
		s.Close()
	})
}
