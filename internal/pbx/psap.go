package pbx

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/biodream-llc/bitvoice/internal/i3"
)

// EmergencyAnswer is a 911 call a call-taker has just answered: what CAD and the i3 log are told.
type EmergencyAnswer struct {
	Call      CallInfo
	Agent     string // the call-taker's extension
	AgentName string
	PIDF      []byte // the location as it came
}

// emergencyAnswered tells the API a 911 call was answered (once per call).
func (c *Call) emergencyAnswered(taker *Leg) {
	c.mu.Lock()
	is := c.i3 != nil || c.esinet
	done := c.emergencyTold
	c.emergencyTold = true
	if is && !done {
		c.takenBy, c.takenName, c.takenAt = taker.Ext, taker.Name, time.Now()
	}
	var raw []byte
	if c.i3 != nil {
		raw = c.i3.LocationRaw
	}
	c.mu.Unlock()
	if !is || done || c.e.OnEmergencyAnswer == nil {
		return
	}
	info := c.info()
	go c.e.OnEmergencyAnswer(EmergencyAnswer{Call: info, Agent: taker.Ext, AgentName: taker.Name, PIDF: raw})
}

// Rebid fetches a 911 caller's location again by reference (the caller may have moved, or the
// first fix was rough), and keeps it with the call.
func (e *Engine) Rebid(ctx context.Context, id string) (*i3.Location, error) {
	e.mu.Lock()
	c := e.calls[id]
	e.mu.Unlock()
	if c == nil {
		return nil, errors.New("no such call")
	}
	c.mu.Lock()
	d := c.i3
	c.mu.Unlock()
	if d == nil || d.LocationURI == "" {
		return nil, errors.New("this call's location came with it; there is no reference to fetch again")
	}
	loc, raw, err := i3.Dereference(ctx, &http.Client{Timeout: 5 * time.Second}, d.LocationURI)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	d.Location, d.LocationRaw = &loc, raw
	c.rebidAt = time.Now()
	c.mu.Unlock()
	e.publish("call.updated", c.info())
	return &loc, nil
}
