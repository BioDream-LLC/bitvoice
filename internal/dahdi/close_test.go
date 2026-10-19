package dahdi

import (
	"context"
	"errors"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/isdn"
)

// exclusive opens channels the way the driver does: a channel open elsewhere is busy until closed.
type exclusive struct {
	mu   sync.Mutex
	held map[int]bool
}

type heldChannel struct {
	*Fake
	x    *exclusive
	n    int
	once sync.Once
}

func (h *heldChannel) Close() error {
	h.once.Do(func() {
		_ = h.Fake.Close()
		h.x.mu.Lock()
		delete(h.x.held, h.n)
		h.x.mu.Unlock()
	})
	return nil
}

func (x *exclusive) take(n int) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.held[n] {
		return syscall.EBUSY
	}
	x.held[n] = true
	return nil
}

func (x *exclusive) open(n int, _ bool) (Channel, error) {
	if err := x.take(n); err != nil {
		return nil, err
	}
	return &heldChannel{Fake: NewFake(), x: x, n: n}, nil
}

type heldD struct {
	*FakeD
	x    *exclusive
	n    int
	once sync.Once
}

func (h *heldD) Close() error {
	h.once.Do(func() {
		h.FakeD.Close()
		h.x.mu.Lock()
		delete(h.x.held, h.n)
		h.x.mu.Unlock()
	})
	return nil
}

// TestCloseFreesTheCard: when Close returns, every channel of the card is closed, so a bridge started
// straight after (bitvoice-dahdi restarts its bridge whenever BitVoice's ports change) opens them all.
// Before, the D-channel closed later on its own and the B-channels' ports, finding them busy, never
// tried again: the span came back with no channel to call on.
func TestCloseFreesTheCard(t *testing.T) {
	x := &exclusive{held: map[int]bool{}}
	old := OpenD
	OpenD = func(n int) (isdn.Link, error) {
		if err := x.take(n); err != nil {
			return nil, err
		}
		d, _ := NewFakeD()
		return &heldD{FakeD: d, x: x, n: n}, nil
	}
	defer func() { OpenD = old }()
	cfg := Config{Spans: []SpanConf{{D: 24, B: []int{1, 2, 3}, Q931: []int{1, 2, 3}, Switch: "ni2"}}}
	for round := 1; round <= 3; round++ {
		b, err := New(context.Background(), cfg, x.open, nil)
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			st := b.Status()
			ok := !strings.HasPrefix(st["1"], "not working") && !strings.HasPrefix(st["3"], "not working")
			x.mu.Lock()
			all := x.held[1] && x.held[2] && x.held[3] && x.held[24]
			x.mu.Unlock()
			if ok && all {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("round %d: the span's channels are not all open: %v", round, st)
			}
			time.Sleep(10 * time.Millisecond)
		}
		b.Close()
		x.mu.Lock()
		left := len(x.held)
		x.mu.Unlock()
		if left != 0 {
			t.Fatalf("round %d: %d channels still open after Close", round, left)
		}
	}
}

// TestPortTriesTheChannelAgain: a channel busy when the bridge starts (another program still closing
// it, or dahdi_cfg not run yet) is opened once it is free.
func TestPortTriesTheChannelAgain(t *testing.T) {
	x := &exclusive{held: map[int]bool{5: true}}
	b, err := New(context.Background(), Config{Channels: []ChannelConf{{Channel: 5, Kind: "line", Number: "2125550100"}}}, x.open, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	waitState := func(want string) {
		t.Helper()
		deadline := time.Now().Add(6 * time.Second)
		for !strings.HasPrefix(b.Status()["5"], want) {
			if time.Now().After(deadline) {
				t.Fatalf("channel 5 is %q, want %q", b.Status()["5"], want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitState("not working")
	x.mu.Lock()
	delete(x.held, 5) // the other program lets go
	x.mu.Unlock()
	waitState("idle")
	if !errors.Is(x.take(5), syscall.EBUSY) {
		t.Fatal("the bridge does not hold channel 5")
	}
}
