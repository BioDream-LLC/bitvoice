package pbx

import (
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
)

// Paging: the caller is answered by BitVoice, every member's phone is called with the auto-answer
// headers phones understand, and what the caller says is copied to every phone that answered. The phones
// are heard by nobody.

type paging struct {
	mu   sync.Mutex
	legs []*Leg
}

func (pg *paging) add(l *Leg) {
	pg.mu.Lock()
	pg.legs = append(pg.legs, l)
	pg.mu.Unlock()
}

func (pg *paging) all() []*Leg {
	pg.mu.Lock()
	defer pg.mu.Unlock()
	return append([]*Leg(nil), pg.legs...)
}

// page starts a page to a group. Called on the call's own goroutine after answering.
func (c *Call) page(g *config.PageGroup) {
	cfg := c.e.Config()
	pg := &paging{}
	c.mu.Lock()
	c.paging = pg
	caller := c.caller
	c.mu.Unlock()
	var mcast func(pkt []byte)
	if g.Multicast != "" {
		mcast = c.multicastPage(g.Multicast, caller)
	}
	caller.ep.Record(func(pkt []byte) {
		for _, l := range pg.all() {
			l.ep.SendRTP(append([]byte(nil), pkt...))
		}
		if mcast != nil {
			mcast(pkt)
		}
	})
	// A short tone tells the person paging that they are live.
	c.play(media.ToneSamples(media.ToneConfirm), nil)
	c.mu.Lock()
	c.stageNo++
	stage := c.stageNo
	c.mu.Unlock()
	started := 0
	for _, n := range g.Members {
		if n == caller.Ext {
			continue
		}
		x := cfg.FindExtension(n)
		if x == nil || !x.Enabled {
			continue
		}
		for _, b := range c.e.reg.contacts(n) {
			if c.startFork(target{ext: n, name: x.Name, binding: b}, stage) {
				started++
			}
		}
	}
	if started == 0 && g.Multicast == "" {
		c.hangup("nobody to page", "system")
		return
	}
	// Phones that do not auto-answer within a few seconds are left out.
	select {
	case <-c.cancelled:
	case <-time.After(8 * time.Second):
		c.cancelForks(nil)
	}
}
