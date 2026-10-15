package isdn

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// pipeLink is one end of an in-memory D-channel; drop loses frames on the way, to test recovery.
type pipeLink struct {
	in   chan []byte
	out  chan []byte
	mu   sync.Mutex
	drop func([]byte) bool
	done chan struct{}
}

func pair() (*pipeLink, *pipeLink) {
	a, b := make(chan []byte, 256), make(chan []byte, 256)
	done := make(chan struct{})
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
	}
	return nil
}

func waitUp(t *testing.T, ps ...*PRI) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for _, p := range ps {
		for !p.IsUp() {
			if time.Now().After(deadline) {
				t.Fatal("the D-channel did not come up")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func next(t *testing.T, c *Call, kind string) Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-c.Events:
			if e.Kind == kind {
				return e
			}
		case <-timeout:
			t.Fatalf("no %s event", kind)
		}
	}
}

// A user side and a network side bring the link up, and calls go both ways: SETUP with the numbers,
// CALL PROCEEDING with the channel, ALERTING, CONNECT and CONNECT ACK, then DISCONNECT, RELEASE and
// RELEASE COMPLETE, with the cause carried.
func TestCallsBothWays(t *testing.T) {
	T200 = 100 * time.Millisecond
	a, b := pair()
	defer close(a.done)
	user := New(a, Config{Channels: []int{1, 2, 3}})
	net := New(b, Config{Network: true, Channels: []int{1, 2, 3}})
	incoming := make(chan *Call, 4)
	net.Incoming = func(c *Call) { incoming <- c }
	user.Incoming = func(c *Call) { incoming <- c }
	go user.Run()
	go net.Run()
	waitUp(t, user, net)

	c, err := user.Dial("2125551234", "2125550100")
	if err != nil {
		t.Fatal(err)
	}
	in := <-incoming
	if in.Called != "2125551234" || in.Calling != "2125550100" || in.Channel != c.Channel {
		t.Fatalf("network saw %+v", in)
	}
	next(t, c, "proceeding")
	in.Alerting()
	next(t, c, "alerting")
	in.Answer()
	next(t, c, "connected")
	next(t, in, "connected")
	if got := user.Busy(); len(got) != 1 || got[0] != c.Channel {
		t.Fatalf("busy %v", got)
	}
	in.Hangup(CauseNormal)
	if e := next(t, c, "disconnected"); e.Cause != CauseNormal {
		t.Fatalf("cause %d", e.Cause)
	}
	next(t, c, "released")
	next(t, in, "released")
	if len(user.Busy()) != 0 || len(net.Busy()) != 0 {
		t.Fatal("channels left busy")
	}

	// The network calls the user, which refuses: busy.
	c2, err := net.Dial("100", "+442071234567")
	if err != nil {
		t.Fatal(err)
	}
	in2 := <-incoming
	if in2.Calling != "+442071234567" {
		t.Fatalf("calling %q", in2.Calling)
	}
	in2.Hangup(CauseBusy)
	if e := next(t, c2, "disconnected"); e.Cause != CauseBusy {
		t.Fatalf("cause %d", e.Cause)
	}
	next(t, c2, "released")

	// Every channel busy: the next call is refused at once.
	var calls []*Call
	for range 3 {
		x, err := user.Dial("5", "6")
		if err != nil {
			t.Fatal(err)
		}
		calls = append(calls, x)
		<-incoming
	}
	if _, err := user.Dial("5", "6"); err != ErrNoChannel {
		t.Fatalf("a fourth call: %v", err)
	}
}

// Lost frames are sent again (T200), and the link comes back after the other side restarts.
func TestRecovery(t *testing.T) {
	T200 = 50 * time.Millisecond
	a, b := pair()
	defer close(a.done)
	user := New(a, Config{Channels: []int{1, 2}})
	net := New(b, Config{Network: true, Channels: []int{1, 2}})
	incoming := make(chan *Call, 4)
	net.Incoming = func(c *Call) { incoming <- c }
	go user.Run()
	go net.Run()
	waitUp(t, user, net)
	// Drop the first I frame the user sends.
	dropped := false
	a.mu.Lock()
	a.drop = func(f []byte) bool {
		if !dropped && len(f) > 4 && f[2]&1 == 0 {
			dropped = true
			return true
		}
		return false
	}
	a.mu.Unlock()
	c, err := user.Dial("123", "456")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case in := <-incoming:
		in.Answer()
	case <-time.After(3 * time.Second):
		t.Fatal("the lost SETUP was not sent again")
	}
	next(t, c, "connected")
	if !dropped {
		t.Fatal("nothing was dropped")
	}
}

func TestMessages(t *testing.T) {
	m := &Message{CallRef: 0x1234, FromSide: true, Type: MsgSetup}
	m.add(ieChannelID, channelID(17, true))
	m.add(ieCalledNumber, numberIE("+33123456789", false))
	m.IEs = append(m.IEs, IE{ID: ieSendingComplete})
	b := m.Bytes()
	g, err := Parse(b)
	if err != nil || g.CallRef != 0x1234 || !g.FromSide || g.Type != MsgSetup {
		t.Fatalf("%+v %v", g, err)
	}
	d, _ := g.Get(ieChannelID)
	if ch, ex, ok := parseChannel(d); !ok || ch != 17 || !ex {
		t.Fatalf("channel %d %v %v", ch, ex, ok)
	}
	d, _ = g.Get(ieCalledNumber)
	if n := parseNumber(d); n != "+33123456789" {
		t.Fatalf("number %q", n)
	}
	if _, ok := g.Get(ieSendingComplete); !ok {
		t.Fatal("sending complete lost")
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte{0x08, 0x02, 0x00, 0x01, 0x05, 0x04, 0x03, 0x80, 0x90, 0xA2, 0x18, 0x03, 0xA9, 0x83, 0x81})
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := Parse(b)
		if err != nil {
			return
		}
		for _, ie := range m.IEs {
			parseChannel(ie.Data)
			parseNumber(ie.Data)
			parseCause(ie.Data)
		}
	})
}

func FuzzFacility(f *testing.F) {
	f.Add(nameFacility(1, "Ann Smith"))
	f.Add(twoBCTFacility(3, 2))
	f.Add(ectFacility(7))
	f.Fuzz(func(t *testing.T, b []byte) {
		parseFacility(b)
		facilityName(b)
		parseRedirecting(b)
	})
}

// Transfers between BitVoice's own two sides: the user side asks, the network side's OnTransfer
// sees the two calls, accepts, and clears them; on both switch kinds. Without OnTransfer it is refused.
func TestTransferBothWays(t *testing.T) {
	T200 = 100 * time.Millisecond
	for _, sw := range []string{"ni2", "euroisdn"} {
		t.Run(sw, func(t *testing.T) {
			ua, na := pair()
			user := New(ua, Config{Channels: []int{1, 2, 3}, Switch: sw})
			net := New(na, Config{Network: true, Channels: []int{1, 2, 3}, Switch: sw})
			joined := make(chan [2]string, 1)
			net.OnTransfer = func(a, b *Call) bool {
				joined <- [2]string{a.Called, b.Called}
				go func() { a.Hangup(CauseNormal); b.Hangup(CauseNormal) }()
				return true
			}
			net.Incoming = func(c *Call) { c.Answer() }
			go user.Run()
			go net.Run()
			waitUp(t, user)
			a, _ := user.Dial("2125550001", "")
			next(t, a, "connected")
			b, _ := user.Dial("2125550002", "")
			next(t, b, "connected")
			if err := a.Transfer(b); err != nil {
				t.Fatal(err)
			}
			if j := <-joined; j != [2]string{"2125550001", "2125550002"} {
				t.Fatalf("joined %v", j)
			}
			next(t, a, "released")
			next(t, b, "released")
			net.OnTransfer = nil
			c, _ := user.Dial("2125550003", "")
			next(t, c, "connected")
			d, _ := user.Dial("2125550004", "")
			next(t, d, "connected")
			if err := c.Transfer(d); err == nil {
				t.Fatal("a refused transfer succeeded")
			}
			user.Close()
			net.Close()
		})
	}
}
