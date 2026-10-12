package pbx

import (
	"fmt"
	"strconv"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// Call parking: a call transferred to the park number waits in the first free slot with hold tone. Anyone
// dials the slot to pick it up; after the park time it rings whoever parked it.

// parkSlots returns the slot numbers: the park number plus 1 to 9.
func parkSlots(cfg *config.Config) []string {
	n, err := strconv.Atoi(cfg.System.ParkNumber)
	if cfg.System.ParkNumber == "" || err != nil {
		return nil
	}
	out := make([]string, 0, 9)
	for i := 1; i <= 9; i++ {
		out = append(out, strconv.Itoa(n+i))
	}
	return out
}

// ParkedCall is a call waiting in a slot, for the dashboard.
type ParkedCall struct {
	Slot     string    `json:"slot"`
	CallID   string    `json:"callId"`
	Party    string    `json:"party"`
	ParkedBy string    `json:"parkedBy"`
	Since    time.Time `json:"since"`
}

// Parked lists the parked calls.
func (e *Engine) Parked() []ParkedCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []ParkedCall{}
	for slot, c := range e.parked {
		c.mu.Lock()
		out = append(out, ParkedCall{Slot: slot, CallID: c.ID, Party: c.caller.party(), ParkedBy: c.parkedBy, Since: c.parkedAt})
		c.mu.Unlock()
	}
	return out
}

// parkedHere is the call waiting in a slot on this server, if any.
func (e *Engine) parkedHere(slot string) *Call {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.parked[slot]
}

// park puts stay in a free slot, releases the transferor, and tells the transferor's phones the slot.
func (c *Call) park(transferor, stay *Leg) bool {
	cfg := c.e.Config()
	c.e.mu.Lock()
	slot := ""
	for _, s := range parkSlots(cfg) {
		if c.e.parked[s] == nil && (c.e.Cluster == nil || c.e.Cluster.ParkedAt(s) == "") {
			slot = s
			break
		}
	}
	if slot != "" {
		c.e.parked[slot] = c
	}
	c.e.mu.Unlock()
	if slot == "" {
		return false
	}
	c.mu.Lock()
	if c.player != nil {
		c.player.Stop()
	}
	// The parked party always sits in the caller position; nobody is on the other side until pickup.
	c.caller, c.callee = stay, nil
	c.localAnswer = true
	c.parkSlot, c.parkedBy, c.parkedAt = slot, transferor.Ext, time.Now()
	connect(stay, nil)
	pt := 0
	if stay.codecName() == "PCMA" {
		pt = 8
	}
	c.player = media.NewPlayer(stay.ep, pt)
	c.player.Play(media.ToneHold)
	c.mu.Unlock()
	notifyRefer(transferor, "200 OK", true)
	transferor.bye()
	transferor.close()
	c.e.notifyExtension(transferor.Ext, fmt.Sprintf("Call from %s parked on %s", stay.party(), slot))
	c.e.publish("call.updated", c.info())
	go c.parkTimer(slot, transferor.Ext)
	return true
}

func (c *Call) parkTimer(slot, parker string) {
	cfg := c.e.Config()
	secs := cfg.System.ParkSeconds
	if secs <= 0 {
		secs = 120
	}
	select {
	case <-c.cancelled:
		c.unpark(slot)
		return
	case <-time.After(time.Duration(secs) * time.Second):
	}
	c.mu.Lock()
	still := c.parkSlot == slot
	c.mu.Unlock()
	if !still {
		return
	}
	c.unpark(slot)
	if parker == "" {
		c.hangup("parked too long", "system")
		return
	}
	c.goTo(cfg, config.Destination{Type: "extension", Target: parker})
}

func (c *Call) unpark(slot string) {
	c.e.mu.Lock()
	if c.e.parked[slot] == c {
		delete(c.e.parked, slot)
	}
	c.e.mu.Unlock()
	c.mu.Lock()
	if c.parkSlot == slot {
		c.parkSlot = ""
	}
	c.mu.Unlock()
}

// pickup joins the caller of this call (someone who dialled a slot) to the parked call, and retires this call.
func (c *Call) pickup(slot string) {
	c.e.mu.Lock()
	parked := c.e.parked[slot]
	if parked != nil {
		delete(c.e.parked, slot)
	}
	c.e.mu.Unlock()
	if parked == nil {
		c.reject(404, "Nothing Parked There")
		return
	}
	if !c.answerHere("Parked call") {
		c.e.mu.Lock()
		c.e.parked[slot] = parked
		c.e.mu.Unlock()
		return
	}
	picker := c.caller
	// The picker's leg moves into the parked call; this call ends without hanging anyone up.
	c.mu.Lock()
	c.ended = true
	c.disposition, c.cause, c.hangupBy = "answered", "picked up a parked call", "pickup"
	c.mu.Unlock()
	picker.call = parked
	parked.mu.Lock()
	if parked.player != nil {
		parked.player.Stop()
		parked.player = nil
	}
	parked.parkSlot = ""
	parked.callee = picker
	parked.answeredBy = picker.party()
	stay := parked.caller
	parked.mu.Unlock()
	connect(stay, picker)
	c.e.mu.Lock()
	delete(c.e.calls, c.ID)
	c.e.mu.Unlock()
	c.writeRecord(c.info(), "")
	c.e.publish("call.ended", c.info())
	c.e.publish("call.updated", parked.info())
}

// notifyExtension shows a short text on an extension's phones (SIP MESSAGE).
func (e *Engine) notifyExtension(ext, text string) {
	if ext == "" {
		return
	}
	e.textElsewhere(ext, "BitVoice", text)
	for _, b := range e.reg.localContacts(ext) {
		msg := &sip.Message{IsRequest: true, Method: sip.MESSAGE, RequestURI: b.requestURI()}
		msg.Set("From", "\"BitVoice\" <sip:bitvoice@"+e.stack.LocalHost(b.peer)+">;tag="+sip.NewTag())
		msg.Set("To", "<sip:"+ext+"@"+e.stack.LocalHost(b.peer)+">")
		msg.Set("Call-ID", sip.NewCallID())
		msg.Set("CSeq", "1 MESSAGE")
		msg.Set("Content-Type", "text/plain;charset=UTF-8")
		msg.Body = []byte(text)
		tx := e.stack.Request(msg, b.peer)
		go func() {
			for range tx.Responses {
			}
		}()
	}
}
