package ss7

import (
	"bufio"
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

func waitLinks(t *testing.T, s *SS7, want map[int]string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		got := s.Links()
		ok := len(got) == len(want)
		for k, v := range want {
			ok = ok && got[k] == v
		}
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("links %v, want %v", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A link set of two links between two BitVoice signalling points: calls share the links by SLS;
// one link fails in the middle of a call (its frames lost both ways): its traffic changes over to
// the other link (COO/COA), messages it had not got acknowledged are sent again there, and the call
// goes on and is released normally; the link comes back and changes back (CBD/CBA). Losing both
// links takes the link set down; a link code used twice is refused.
func TestLinkSetChangeover(t *testing.T) {
	fast()
	T2MTP3, T1MTP3, T4MTP3 = 300*time.Millisecond, 100*time.Millisecond, 300*time.Millisecond
	oldWait, oldPeriod := SLTWait, SLTPeriod
	SLTWait, SLTPeriod = 300*time.Millisecond, 500*time.Millisecond // a cut line is noticed quickly
	defer func() { SLTWait, SLTPeriod = oldWait, oldPeriod }()
	for _, ansi := range []bool{false, true} {
		t.Run(map[bool]string{false: "itu", true: "ansi"}[ansi], func(t *testing.T) {
			a0, b0 := pair()
			a1, b1 := pair()
			defer close(a0.done)
			defer close(a1.done)
			cics := []int{1, 2, 3, 4, 5, 6, 7, 8}
			a := NewLinkSet(Config{MTPConfig: MTPConfig{ANSI: ansi, OPC: 100, APC: 200, NI: NINational}, CICs: cics})
			b := NewLinkSet(Config{MTPConfig: MTPConfig{ANSI: ansi, OPC: 200, APC: 100, NI: NINational}, CICs: cics})
			in := make(chan *Call, 8)
			b.Incoming = func(c *Call) { in <- c }
			go a.AddLink(a0, 0)
			go a.AddLink(a1, 1)
			go b.AddLink(b0, 0)
			go b.AddLink(b1, 1)
			waitUp(t, a, b)
			waitLinks(t, a, map[int]string{0: "in service", 1: "in service"})
			waitLinks(t, b, map[int]string{0: "in service", 1: "in service"})
			if err := a.AddLink(a0, 1); err != ErrLinkSetFull {
				t.Fatalf("a second link 1: %v", err)
			}
			// Two calls: on different links (CICs 1 and 3: SLS 1 and 3, shared out in turn).
			used := map[*pipeLink]int{}
			count := func(l *pipeLink) func([]byte) bool {
				return func(f []byte) bool {
					if len(f) > 3 && f[2]&0x3F > 2 {
						used[l]++
					}
					return false
				}
			}
			a0.mu.Lock()
			a0.drop = count(a0)
			a0.mu.Unlock()
			a1.mu.Lock()
			a1.drop = count(a1)
			a1.mu.Unlock()
			c1, err := a.Dial(Setup{Called: "2125551234", Calling: "3125550199"})
			if err != nil {
				t.Fatal(err)
			}
			c2, _ := a.Dial(Setup{Called: "2125551235", Calling: "3125550199"})
			g1, g2 := <-in, <-in
			if g1.CIC != c1.CIC { // the IAMs may arrive either way round
				g1, g2 = g2, g1
			}
			g1.Alerting()
			g2.Alerting()
			next(t, c1, "alerting")
			next(t, c2, "alerting")
			a0.mu.Lock()
			n0 := used[a0]
			a0.mu.Unlock()
			a1.mu.Lock()
			n1 := used[a1]
			a1.mu.Unlock()
			if n0 == 0 || n1 == 0 {
				t.Fatalf("traffic not shared: link 0 %d, link 1 %d", n0, n1)
			}
			// Link 0 fails both ways.
			lose := func(f []byte) bool { return true }
			a0.mu.Lock()
			a0.drop = lose
			a0.mu.Unlock()
			b0.mu.Lock()
			b0.drop = lose
			b0.mu.Unlock()
			waitLinks(t, a, map[int]string{0: "changed over", 1: "in service"})
			waitLinks(t, b, map[int]string{0: "changed over", 1: "in service"})
			if !a.IsUp() || !b.IsUp() {
				t.Fatal("the link set went down with one link left")
			}
			// Both calls go on over link 1.
			g1.Answer()
			g2.Answer()
			next(t, c1, "connected")
			next(t, c2, "connected")
			c1.Hangup(CauseNormal)
			next(t, g1, "released")
			next(t, c1, "released")
			// Link 0 is mended: it aligns, tests and changes back.
			a0.mu.Lock()
			a0.drop = nil
			a0.mu.Unlock()
			b0.mu.Lock()
			b0.drop = nil
			b0.mu.Unlock()
			waitLinks(t, a, map[int]string{0: "in service", 1: "in service"})
			waitLinks(t, b, map[int]string{0: "in service", 1: "in service"})
			g2.Hangup(CauseNormal)
			next(t, c2, "released")
			// Both links lost: the link set goes down.
			a0.mu.Lock()
			a0.drop = lose
			a0.mu.Unlock()
			a1.mu.Lock()
			a1.drop = lose
			a1.mu.Unlock()
			deadline := time.Now().Add(15 * time.Second)
			for a.IsUp() {
				if time.Now().After(deadline) {
					t.Fatal("the link set stayed up with no link")
				}
				time.Sleep(10 * time.Millisecond)
			}
			a.Close()
			b.Close()
		})
	}
}

// cutLink is a gramLink that can be cut: while cut, nothing goes out and what comes in is dropped.
type cutLink struct {
	*gramLink
	mu  sync.Mutex
	cut bool
}

func (c *cutLink) set(cut bool) { c.mu.Lock(); c.cut = cut; c.mu.Unlock() }
func (c *cutLink) isCut() bool  { c.mu.Lock(); defer c.mu.Unlock(); return c.cut }

func (c *cutLink) ReadFrame() ([]byte, error) {
	for {
		f, err := c.gramLink.ReadFrame()
		if err != nil || !c.isCut() {
			return f, err
		}
	}
}

func (c *cutLink) WriteFrame(f []byte) error {
	if c.isCut() {
		return nil
	}
	return c.gramLink.WriteFrame(f)
}

// startOracleLinks starts libss7 with a link set of n links, one socket each.
func startOracleLinks(t *testing.T, variant, script string, n int) ([]*cutLink, *oracle) {
	bin := os.Getenv("BITVOICE_TEST_LIBSS7")
	if bin == "" {
		t.Skip("set BITVOICE_TEST_LIBSS7 to the libss7 harness")
	}
	dir, _ := os.MkdirTemp("/tmp", "ss7")
	t.Cleanup(func() { os.RemoveAll(dir) })
	var paths []string
	var links []*cutLink
	for i := 0; i < n; i++ {
		path := filepath.Join(dir, "l"+strconv.Itoa(i))
		c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		paths = append(paths, path)
		links = append(links, &cutLink{gramLink: &gramLink{c: c, peer: &net.UnixAddr{Name: path + ".c", Net: "unixgram"}}})
	}
	cmd := exec.Command(bin, strings.Join(paths, ","), variant, "200", "100", script)
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	o := &oracle{t: t, lines: make(chan string, 256)}
	go func() {
		s := bufio.NewScanner(out)
		for s.Scan() {
			o.lines <- s.Text()
		}
		close(o.lines)
	}()
	for _, p := range paths {
		for end := time.Now().Add(15 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(p + ".c"); err == nil {
				break
			}
			if time.Now().After(end) {
				t.Fatal("the libss7 oracle never opened its sockets")
			}
		}
	}
	return links, o
}

// A link set of two links with libss7: both come up; BitVoice calls, libss7 answers and holds; one
// link is cut (both ways): each end sees it fail and moves its traffic to the other link (libss7 says
// so), and the call is released over it; the link is mended, comes back into service, and a second
// call goes. libss7 takes network management messages only on the link their SLS names, so a
// changeover message about the cut link, sent on the other as Q.704 has it, is dropped there: both
// ends then change over by time (Q.704 5.6.2), which this checks.
func TestLinkSetAgainstLibss7(t *testing.T) {
	Fill = 10 * time.Millisecond
	// A cut line is noticed by the link test (on a real span, also by the card's alarm at once).
	oldWait, oldPeriod := SLTWait, SLTPeriod
	SLTWait, SLTPeriod = 300*time.Millisecond, 500*time.Millisecond
	defer func() { SLTWait, SLTPeriod = oldWait, oldPeriod }()
	t.Setenv("SS7FASTTEST", "1")
	links, o := startOracleLinks(t, "itu", "hold", 2)
	s := NewLinkSet(cfgFor("itu"))
	for i, l := range links {
		go s.AddLink(l, i)
	}
	o.expect("UP")
	waitUp(t, s)
	waitReset(t, s)
	waitLinks(t, s, map[int]string{0: "in service", 1: "in service"})
	c, err := s.Dial(Setup{Called: "2125551234", Calling: "3125550100"})
	if err != nil {
		t.Fatal(err)
	}
	o.expect("IAM")
	next(t, c, "connected")
	links[0].set(true)
	waitLinks(t, s, map[int]string{0: "changed over", 1: "in service"})
	for l := o.expect("MSG"); !strings.Contains(strings.ToLower(l), "changeover"); l = o.expect("MSG") {
		// libss7's changeback at the start may be said late: wait for its changeover
	}
	c.Hangup(CauseNormal)
	if l := o.expect("REL"); !strings.Contains(l, "cause=16") {
		t.Fatalf("libss7: %s", l)
	}
	next(t, c, "released")
	links[0].set(false)
	waitLinks(t, s, map[int]string{0: "in service", 1: "in service"})
	c2, err := s.Dial(Setup{Called: "2125551235", Calling: "3125550100"})
	if err != nil {
		t.Fatal(err)
	}
	o.expect("IAM")
	next(t, c2, "connected")
	c2.Hangup(CauseNormal)
	o.expect("REL")
	s.Close()
}
