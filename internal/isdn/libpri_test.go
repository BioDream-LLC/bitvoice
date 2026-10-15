package isdn

import (
	"bufio"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// gramLink is the D-channel over a Unix datagram socket to the libpri harness, with libpri's two FCS
// bytes on every frame, as DAHDI has them.
type gramLink struct {
	c    *net.UnixConn
	peer *net.UnixAddr
}

func (g *gramLink) ReadFrame() ([]byte, error) {
	buf := make([]byte, 1024)
	for {
		n, from, err := g.c.ReadFromUnix(buf)
		if err != nil {
			return nil, err
		}
		_ = from // the harness's own address, known from the start
		if n >= 3 {
			return append([]byte(nil), buf[:n-2]...), nil
		}
	}
}

func (g *gramLink) WriteFrame(f []byte) error {
	if g.peer == nil {
		return nil
	}
	_, err := g.c.WriteToUnix(append(append([]byte(nil), f...), 0, 0), g.peer)
	return err
}

type oracle struct {
	t     *testing.T
	lines chan string
	cmd   *exec.Cmd
}

// startOracle runs libpri (BITVOICE_TEST_LIBPRI names the harness) as the other side.
func startOracle(t *testing.T, role, sw, script string) (*gramLink, *oracle) {
	bin := os.Getenv("BITVOICE_TEST_LIBPRI")
	if bin == "" {
		t.Skip("set BITVOICE_TEST_LIBPRI to the libpri harness")
	}
	dir, _ := os.MkdirTemp("/tmp", "pri")
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "d")
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	cmd := exec.Command(bin, path, role, sw, script)
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	o := &oracle{t: t, lines: make(chan string, 64), cmd: cmd}
	go func() {
		s := bufio.NewScanner(out)
		for s.Scan() {
			o.lines <- s.Text()
		}
		close(o.lines)
	}()
	return &gramLink{c: c, peer: &net.UnixAddr{Name: path + ".c", Net: "unixgram"}}, o
}

func (o *oracle) expect(prefix string) string {
	o.t.Helper()
	timeout := time.After(8 * time.Second)
	for {
		select {
		case l, ok := <-o.lines:
			if !ok {
				o.t.Fatalf("libpri ended before %q", prefix)
			}
			if strings.HasPrefix(l, prefix) {
				return l
			}
		case <-timeout:
			o.t.Fatalf("libpri never said %q", prefix)
		}
	}
}

// BitVoice as the user side (a PBX) on libpri's network: it calls out (libpri answers and the call is
// cleared from BitVoice), and libpri calls in (BitVoice answers, libpri clears).
func TestAgainstLibpriAsUser(t *testing.T) {
	for _, sw := range []string{"ni2", "euro"} {
		t.Run(sw, func(t *testing.T) {
			link, o := startOracle(t, "net", sw, "answer")
			p := New(link, Config{Channels: []int{1, 2, 3, 4}, ALaw: sw == "euro"})
			go p.Run()
			o.expect("UP")
			waitUp(t, p)
			c, err := p.Dial("2125551234", "3125550100")
			if err != nil {
				t.Fatal(err)
			}
			ring := o.expect("RING")
			if !strings.Contains(ring, "called=2125551234") || !strings.Contains(ring, "calling=3125550100") || !strings.Contains(ring, "channel=1") {
				t.Fatalf("libpri saw %q", ring)
			}
			next(t, c, "connected")
			c.Hangup(CauseNormal)
			if l := o.expect("HANGUP"); !strings.Contains(l, "cause=16") {
				t.Fatalf("libpri: %s", l)
			}
			next(t, c, "released")
			p.Close()
		})
	}
}

// BitVoice as the network side (another PBX on BitVoice): libpri (a CPE) calls in, BitVoice answers;
// libpri hangs up; then BitVoice refuses a call as busy.
func TestAgainstLibpriAsNetwork(t *testing.T) {
	link, o := startOracle(t, "cpe", "ni2", "call")
	p := New(link, Config{Network: true, Channels: []int{1, 2, 3}})
	incoming := make(chan *Call, 2)
	p.Incoming = func(c *Call) { incoming <- c }
	go p.Run()
	o.expect("UP")
	var in *Call
	select {
	case in = <-incoming:
	case <-time.After(5 * time.Second):
		t.Fatal("libpri's SETUP did not arrive")
	}
	if in.Called != "2125551234" || in.Calling != "3125550199" || in.Channel != 2 { // libpri asked for channel 2
		t.Fatalf("got %+v", in)
	}
	o.expect("PROCEEDING")
	in.Alerting()
	o.expect("RINGING")
	in.Answer()
	o.expect("ANSWER")
	next(t, in, "connected")
	o.expect("HUNGUP")
	if e := next(t, in, "disconnected"); e.Cause != CauseNormal {
		t.Fatalf("cause %d", e.Cause)
	}
	next(t, in, "released")
	p.Close()
}

// Names and redirecting numbers, both ways: BitVoice's SETUP as libpri reads it (National ISDN-2's
// facility name and EuroISDN's display), and libpri's as BitVoice reads it, with the connected number
// BitVoice answers with.
func TestAgainstLibpriNames(t *testing.T) {
	for _, sw := range []string{"ni2", "euro"} {
		t.Run(sw, func(t *testing.T) {
			link, o := startOracle(t, "net", sw, "answer")
			p := New(link, Config{Channels: []int{1, 2, 3, 4}, ALaw: sw == "euro", Switch: sw})
			go p.Run()
			o.expect("UP")
			waitUp(t, p)
			c, err := p.DialWith(Setup{Called: "2125551234", Calling: "3125550100", Name: "Front Desk", Redirecting: "3125550111", RedirectReason: RedirectNoReply})
			if err != nil {
				t.Fatal(err)
			}
			ring := o.expect("RING")
			if !strings.Contains(ring, "name=Front Desk") || !strings.Contains(ring, "redir=3125550111 reason=2") {
				t.Fatalf("libpri saw %q", ring)
			}
			next(t, c, "connected")
			c.Hangup(CauseNormal)
			o.expect("HANGUP")
			p.Close()
		})
	}
	link, o := startOracle(t, "cpe", "ni2", "call")
	p := New(link, Config{Network: true, Channels: []int{1, 2, 3}})
	incoming := make(chan *Call, 1)
	p.Incoming = func(c *Call) { incoming <- c }
	go p.Run()
	o.expect("UP")
	in := <-incoming
	if in.Name != "Ann Smith" || in.Redirecting != "3125550111" || in.RedirectReason != RedirectBusy {
		t.Fatalf("got %+v", in)
	}
	in.Answer()
	if l := o.expect("ANSWER"); !strings.Contains(l, "connected=2125551234") {
		t.Fatalf("libpri: %s", l)
	}
	p.Close()
}

// EuroISDN explicit call transfer: BitVoice holds one call and asks the network (libpri) to join it to
// the other; libpri reports the transfer, answers it, and clears both calls.
func TestAgainstLibpriTransferEuro(t *testing.T) {
	link, o := startOracle(t, "net", "euro", "ect")
	p := New(link, Config{Channels: []int{1, 2, 3, 4}, ALaw: true, Switch: "euro"})
	go p.Run()
	o.expect("UP")
	waitUp(t, p)
	a, err := p.Dial("2125550001", "3125550100")
	if err != nil {
		t.Fatal(err)
	}
	next(t, a, "connected")
	b, err := p.Dial("2125550002", "3125550100")
	if err != nil {
		t.Fatal(err)
	}
	next(t, b, "connected")
	if err := b.Hold(); err == nil {
		if err := b.Retrieve(); err != nil {
			t.Fatalf("retrieve: %v", err)
		}
		o.expect("HOLD")
		o.expect("RETRIEVE")
	} else {
		t.Fatalf("hold: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Transfer(b) }()
	if l := o.expect("TRANSFER"); l != "TRANSFER held1=1 held2=0" {
		t.Fatalf("libpri: %s", l)
	}
	if err := <-done; err != nil {
		t.Fatalf("transfer: %v", err)
	}
	for _, c := range []*Call{a, b} {
		for e := range c.Events {
			if e.Kind == "released" {
				break
			}
		}
	}
	if len(p.Busy()) != 0 {
		t.Fatalf("channels still busy: %v", p.Busy())
	}
	p.Close()
}

// National ISDN-2 Two B-Channel Transfer: libpri (a PBX) asks BitVoice (the network) to join two
// calls; BitVoice reads the request (and refuses it, not being a switch), and its own encoding of the
// request is byte for byte libpri's.
func TestAgainstLibpri2BCT(t *testing.T) {
	link, o := startOracle(t, "cpe", "ni2", "bridge")
	var mu sync.Mutex
	var facs [][]byte
	p := New(link, Config{Network: true, Channels: []int{1, 2, 3}, Trace: func(in bool, b []byte) {
		if m, err := Parse(b); in && err == nil && m.Type == MsgFacility {
			if d, ok := m.Get(ieFacility); ok {
				mu.Lock()
				facs = append(facs, d)
				mu.Unlock()
			}
		}
	}})
	asked := make(chan *Call, 2)
	p.Incoming = func(c *Call) {
		c.Answer()
		for e := range c.Events {
			if e.Kind == "transfer-asked" {
				asked <- c
			}
		}
	}
	go p.Run()
	o.expect("UP")
	o.expect("BRIDGED")
	var c *Call
	select {
	case c = <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("the transfer request was not read")
	}
	mu.Lock()
	got := facs[0]
	mu.Unlock()
	var other *Call
	p.mu.Lock()
	for k, x := range p.calls {
		if x != c && !k.ours {
			other = x
		}
	}
	p.mu.Unlock()
	comps := parseFacility(got)
	want := twoBCTFacility(comps[0].InvokeID, other.ref)
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatalf("libpri sent %x, BitVoice would send %x", got, want)
	}
	p.Close()
}
