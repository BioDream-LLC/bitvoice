package isdn

import (
	"strings"
	"testing"
	"time"
)

func TestBRIMessages(t *testing.T) {
	m := &Message{CallRef: 5, FromSide: true, Type: MsgSetup, Short: true}
	m.add(ieChannelID, briChannelID(2, true))
	b := m.Bytes()
	if b[1] != 1 || b[2] != 0x85 {
		t.Fatalf("call reference: % x", b)
	}
	got, err := Parse(b)
	if err != nil || !got.Short || got.CallRef != 5 || !got.FromSide {
		t.Fatalf("parsed %+v %v", got, err)
	}
	d, _ := got.Get(ieChannelID)
	if ch, excl, ok := parseChannel(d); !ok || ch != 2 || !excl {
		t.Fatalf("channel %d %v %v", ch, excl, ok)
	}
}

// BitVoice on both sides of a multipoint BRI: two terminals ask for TEIs, a call to the line is
// offered to both, the one that answers gets it and the other is released; a terminal dials digit by
// digit and the network side collects the number.
func TestBRIMultipoint(t *testing.T) {
	T200, T302 = 100*time.Millisecond, 300*time.Millisecond
	defer func() { T302 = 4 * time.Second }()
	hub := newBus()
	net := New(hub.port(), Config{Network: true, BRI: true, PTMP: true, ALaw: true, Switch: "euro"})
	inNet := make(chan *Call, 2)
	net.Incoming = func(c *Call) { inNet <- c }
	var terms []*PRI
	ringing := make(chan *Call, 4)
	for i := 0; i < 2; i++ {
		te := New(hub.port(), Config{BRI: true, PTMP: true, ALaw: true, Switch: "euro"})
		i := i
		te.Incoming = func(c *Call) {
			c.Alerting()
			if i == 1 {
				c.Answer()
			}
			ringing <- c
		}
		terms = append(terms, te)
	}
	go net.Run()
	for _, te := range terms {
		go te.Run()
	}
	waitUp(t, net, terms[0], terms[1])
	if terms[0].dl.tei == terms[1].dl.tei || terms[0].dl.tei < 64 {
		t.Fatalf("TEIs %d and %d", terms[0].dl.tei, terms[1].dl.tei)
	}
	c, err := net.Dial("5551234", "0201234567")
	if err != nil {
		t.Fatal(err)
	}
	r1, r2 := <-ringing, <-ringing
	next(t, c, "connected")
	if c.tei != terms[1].dl.tei {
		t.Fatalf("the call went to TEI %d, the answering terminal is %d", c.tei, terms[1].dl.tei)
	}
	loser := r1
	if r1.p == terms[1] {
		loser = r2
	}
	if e := next(t, loser, "released"); e.Cause != CauseNonSelected {
		t.Fatalf("the other terminal was released with cause %d", e.Cause)
	}
	c.Hangup(CauseNormal)
	next(t, c, "released")
}

// bus is an S-bus: frames from any port reach every other port.
type bus struct {
	ports []*pipeLink
}

func newBus() *bus { return &bus{} }

func (b *bus) port() *pipeLink {
	l := &pipeLink{in: make(chan []byte, 256), out: make(chan []byte, 256), done: make(chan struct{})}
	b.ports = append(b.ports, l)
	go func() {
		for f := range l.out {
			for _, o := range b.ports {
				if o != l {
					select {
					case o.in <- append([]byte(nil), f...):
					default:
					}
				}
			}
		}
	}()
	return l
}

// BitVoice as the terminal on libpri's BRI network, point to point and point to multipoint: a call
// out, answered by libpri, and a call in (libpri's SETUP, broadcast on a multipoint line), answered.
func TestAgainstLibpriBRIUser(t *testing.T) {
	for _, mode := range []string{"ptp", "ptmp"} {
		t.Run(mode, func(t *testing.T) {
			link, o := startOracle(t, "net", "euro", "answer", mode)
			p := New(link, Config{BRI: true, PTMP: mode == "ptmp", ALaw: true, Switch: "euro"})
			go p.Run()
			waitUp(t, p)
			c, err := p.Dial("5551234", "0201234567")
			if err != nil {
				t.Fatal(err)
			}
			ring := o.expect("RING")
			if !strings.Contains(ring, "called=5551234") || !strings.Contains(ring, "channel=") {
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
		t.Run(mode+"-in", func(t *testing.T) {
			link, o := startOracle(t, "net", "euro", "call", mode)
			p := New(link, Config{BRI: true, PTMP: mode == "ptmp", ALaw: true, Switch: "euro"})
			incoming := make(chan *Call, 1)
			p.Incoming = func(c *Call) { incoming <- c }
			go p.Run()
			var in *Call
			select {
			case in = <-incoming:
			case <-time.After(8 * time.Second):
				t.Fatal("libpri's SETUP did not arrive")
			}
			if in.Called != "2125551234" || in.Channel != 2 {
				t.Fatalf("got %+v", in)
			}
			in.Alerting()
			in.Answer()
			o.expect("ANSWER")
			o.expect("HUNGUP")
			next(t, in, "released")
			p.Close()
		})
	}
}

// BitVoice as the network of a multipoint BRI with libpri as the terminal: libpri gets its TEI from
// BitVoice and calls in; BitVoice offers a call to the line and libpri answers it; libpri dials digit
// by digit and BitVoice collects the number.
func TestAgainstLibpriBRINetwork(t *testing.T) {
	T302 = 500 * time.Millisecond
	defer func() { T302 = 4 * time.Second }()
	t.Run("in", func(t *testing.T) {
		link, o := startOracle(t, "cpe", "euro", "call", "ptmp")
		p := New(link, Config{Network: true, BRI: true, PTMP: true, ALaw: true, Switch: "euro"})
		incoming := make(chan *Call, 1)
		p.Incoming = func(c *Call) { incoming <- c }
		go p.Run()
		var in *Call
		select {
		case in = <-incoming:
		case <-time.After(8 * time.Second):
			t.Fatal("libpri's SETUP did not arrive")
		}
		if in.Called != "2125551234" || in.tei < 64 {
			t.Fatalf("got %+v (TEI %d)", in, in.tei)
		}
		in.Answer()
		o.expect("ANSWER")
		o.expect("HUNGUP")
		next(t, in, "released")
		p.Close()
	})
	t.Run("out", func(t *testing.T) {
		link, o := startOracle(t, "cpe", "euro", "answer", "ptmp")
		p := New(link, Config{Network: true, BRI: true, PTMP: true, ALaw: true, Switch: "euro"})
		go p.Run()
		waitUp(t, p)
		var c *Call
		deadline := time.Now().Add(8 * time.Second)
		for {
			// libpri asks for its TEI when it starts; until then a broadcast SETUP reaches nobody.
			p.dl.mu.Lock()
			n := len(p.dl.links)
			p.dl.mu.Unlock()
			if n > 0 || time.Now().After(deadline) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		c, err := p.Dial("5551234", "0201234567")
		if err != nil {
			t.Fatal(err)
		}
		if l := o.expect("RING"); !strings.Contains(l, "called=5551234") {
			t.Fatalf("libpri: %s", l)
		}
		next(t, c, "connected")
		c.Hangup(CauseNormal)
		o.expect("HANGUP")
		next(t, c, "released")
		p.Close()
	})
	t.Run("overlap", func(t *testing.T) {
		link, o := startOracle(t, "cpe", "euro", "overlap", "ptmp")
		p := New(link, Config{Network: true, BRI: true, PTMP: true, ALaw: true, Switch: "euro"})
		incoming := make(chan *Call, 1)
		p.Incoming = func(c *Call) { incoming <- c }
		go p.Run()
		o.expect("SETUP_ACK")
		var in *Call
		select {
		case in = <-incoming:
		case <-time.After(8 * time.Second):
			t.Fatal("the number was not collected")
		}
		if in.Called != "2125551" {
			t.Fatalf("collected %q", in.Called)
		}
		o.expect("PROCEEDING")
		p.Close()
	})
}
