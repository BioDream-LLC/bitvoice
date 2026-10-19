package pbx

import (
	"net/netip"
	"strconv"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
)

// bcfMedia sets the BCF's media inspection on an ESInet call's caller leg (System.Core.BCF.
// InspectMedia): media only from the address its SDP gave, only the payload types it offered, well
// formed, not too large and not a flood. What breaks a rule is dropped; the first breach of each
// kind marks the call suspicious and goes in the BCF log, with the counts kept on the call.
func (e *Engine) bcfMedia(cfg *config.Config, c *Call, a *Leg, offer *sdp.Session) {
	b := cfg.System.Core.BCF
	if !b.Enabled || !b.InspectMedia || offer == nil || a.ep == nil {
		return
	}
	m := offer.Audio()
	if m == nil {
		return
	}
	r := media.InspectRules{PerSecond: b.MediaPerSecond, MaxBytes: b.MediaMaxBytes}
	if r.PerSecond == 0 {
		r.PerSecond = 150
	}
	if r.MaxBytes == 0 {
		r.MaxBytes = 1500
	}
	if addr, err := netip.ParseAddr(offer.Connection(m)); err == nil && !addr.IsUnspecified() {
		r.Source = addr
	}
	for _, f := range m.Formats {
		if pt, err := strconv.Atoi(f); err == nil && pt >= 0 && pt < 128 {
			r.PayloadTypes = append(r.PayloadTypes, pt)
		}
	}
	src := c.srcIP
	r.Report = func(f media.Finding) {
		c.mu.Lock()
		if c.mediaFindings == nil {
			c.mediaFindings = map[string]int{}
		}
		c.mediaFindings[f.Kind] = f.Count
		from := c.from
		c.mu.Unlock()
		if f.Count == 1 {
			c.note("The BCF dropped " + f.What + ".")
		}
		ev := BCFEvent{At: time.Now(), Source: src, From: from, Action: "media",
			Why: f.What + " (" + strconv.Itoa(f.Count) + " dropped)"}
		e.bcf.mu.Lock()
		e.bcf.log = append(e.bcf.log, ev)
		if len(e.bcf.log) > 200 {
			e.bcf.log = e.bcf.log[len(e.bcf.log)-200:]
		}
		e.bcf.mu.Unlock()
		e.publish("bcf.event", ev)
	}
	a.ep.SetInspector(media.NewInspector(r))
}
