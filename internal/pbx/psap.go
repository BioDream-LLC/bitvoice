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

// locateByNumber looks up a 911 call's location by its callback number, when the call brought none
// and a location server is set (the ALI query of a legacy call). It never holds up the call: what it
// finds is added when it comes.
func (c *Call) locateByNumber() {
	server := c.e.Config().System.PSAP.LocationServer
	c.mu.Lock()
	emergency := c.i3 != nil || c.esinet
	has := c.i3 != nil && (c.i3.Location != nil || c.i3.LocationURI != "")
	number := c.from
	if emergency && !has && server != "" {
		c.aliLookup = "looking"
	}
	c.mu.Unlock()
	if !emergency || has || server == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	loc, raw, err := i3.LocateNumber(ctx, &http.Client{Timeout: 5 * time.Second}, server, number)
	c.mu.Lock()
	if err != nil {
		c.aliLookup = err.Error()
	} else {
		c.aliLookup = "found"
		if c.i3 == nil {
			c.i3 = &i3.Call{}
		}
		c.i3.Location, c.i3.LocationRaw = &loc, raw
	}
	c.mu.Unlock()
	if err != nil {
		c.e.Log.Warn("911 location lookup failed", "call", c.ID, "number", number, "err", err)
	}
	c.e.publish("call.updated", c.info())
}
