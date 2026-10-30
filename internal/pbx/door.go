package pbx

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
)

// Door stations: a call with a door station watches the other party's key presses for the door's code,
// and opens the door by calling the controller's URL. Without a URL the key presses simply reach the
// door station, which opens the door itself; the Open door button sends it the code.

// doorOf returns the door station extension in a call and the leg it is on, and the other party's leg.
func (c *Call) doorOf() (*config.Extension, *Leg, *Leg) {
	cfg := c.e.Config()
	c.mu.Lock()
	a, b := c.caller, c.callee
	c.mu.Unlock()
	if a == nil || b == nil {
		return nil, nil, nil
	}
	for _, pair := range [][2]*Leg{{a, b}, {b, a}} {
		if pair[0].Ext == "" {
			continue
		}
		if x := cfg.FindExtension(pair[0].Ext); x != nil && x.Kind == "door" {
			return x, pair[0], pair[1]
		}
	}
	return nil, nil, nil
}

// bridged runs whenever two parties have just been connected: it switches audio clean-up on when set,
// and listens to both for key codes (a door code from whoever is talking to a door station, and the
// recording pause codes from extensions).
func (c *Call) bridged() {
	c.mu.Lock()
	a, b := c.caller, c.callee
	c.mu.Unlock()
	if a == nil || b == nil {
		return
	}
	c.consentNotice()
	c.keepForBrief()
	c.emergencyAnswered(b)
	// Live transcript for an ordinary call (911 starts its own in emergencyAnswered), once per call.
	c.mu.Lock()
	is911, textOnly := c.i3 != nil || c.esinet, c.textOnly
	c.mu.Unlock()
	if !is911 && !textOnly {
		c.liveOnce.Do(func() { go c.liveTranscript() })
	}
	clean := c.e.Config().System.Audio.Calls
	for _, l := range []*Leg{a, b} {
		if l.ep == nil {
			continue
		}
		l.ep.SetCleanup(clean)
		var mu sync.Mutex
		typed, last := "", time.Time{}
		l.ep.OnDTMF(func(k byte) {
			mu.Lock()
			if time.Since(last) > 3*time.Second {
				typed = ""
			}
			typed, last = typed+string(k), time.Now()
			got := typed
			mu.Unlock()
			if c.keys(l, got) {
				mu.Lock()
				typed = ""
				mu.Unlock()
			}
		})
	}
}

// keys acts on what a party has typed so far; true when a code was used.
func (c *Call) keys(l *Leg, typed string) bool {
	cfg := c.e.Config()
	if x, door, _ := c.doorOf(); x != nil && door != l && strings.HasSuffix(typed, x.Door.Code) {
		if x.Door.URL != "" {
			go func() { _ = c.openDoor(l.party()) }()
		}
		return true
	}
	if l.Ext == "" { // only people on extensions control the recording, never an outside caller
		return false
	}
	rc := cfg.System.Recording
	switch {
	case rc.PauseCode != "" && strings.HasSuffix(typed, rc.PauseCode):
		c.pauseRecording(true, "extension "+l.Ext)
		return true
	case rc.ResumeCode != "" && strings.HasSuffix(typed, rc.ResumeCode):
		c.pauseRecording(false, "extension "+l.Ext)
		return true
	}
	return false
}

// OpenDoor opens the door for a call with a door station (the button on the screen).
func (e *Engine) OpenDoor(callID, by string) error {
	e.mu.Lock()
	c := e.calls[callID]
	e.mu.Unlock()
	if c == nil {
		return errors.New("no such call")
	}
	return c.openDoor(by)
}

var doorClient = &http.Client{Timeout: 5 * time.Second}

func (c *Call) openDoor(by string) error {
	x, door, _ := c.doorOf()
	if x == nil {
		return errors.New("this call is not with a door station")
	}
	var err error
	if x.Door.URL == "" {
		err = sendKeys(door, x.Door.Code)
	} else {
		err = triggerDoor(x.Door)
	}
	ev := map[string]any{"call": c.ID, "door": x.Number, "name": x.Name, "by": by, "ok": err == nil}
	if err != nil {
		ev["error"] = err.Error()
		c.e.Log.Warn("door did not open", "door", x.Number, "err", err)
	} else {
		c.e.Log.Info("door opened", "door", x.Number, "by", by)
	}
	c.e.publish("door.opened", ev)
	return err
}

func triggerDoor(d config.Door) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, d.Method, d.URL, nil)
	if err != nil {
		return err
	}
	if d.Username != "" {
		req.SetBasicAuth(d.Username, d.Password)
	}
	resp, err := doorClient.Do(req)
	if err != nil {
		return fmt.Errorf("the door controller did not answer: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("the door controller answered %s", resp.Status)
	}
	return nil
}

// sendKeys sends key presses to a party as RFC 4733 events, at the payload type it agreed for them.
func sendKeys(l *Leg, keys string) error {
	pt := l.eventPT()
	if pt < 0 {
		return errors.New("the door station did not agree to key presses as events; set a door controller address instead")
	}
	ssrc := uint32(time.Now().UnixNano())
	ts := uint32(time.Now().UnixMilli() * 8)
	seq := uint16(time.Now().UnixNano())
	for _, k := range keys {
		ev := strings.IndexRune("0123456789*#", k)
		if ev < 0 {
			continue
		}
		for i := 0; i < 8; i++ { // 160 ms of key, then the end three times
			end := i >= 5
			dur := uint16(min(i+1, 5) * 160)
			pkt := make([]byte, 16)
			pkt[0], pkt[1] = 0x80, byte(pt)
			if i == 0 {
				pkt[1] |= 0x80 // marker on the first packet of an event
			}
			binary.BigEndian.PutUint16(pkt[2:], seq)
			binary.BigEndian.PutUint32(pkt[4:], ts)
			binary.BigEndian.PutUint32(pkt[8:], ssrc)
			pkt[12] = byte(ev)
			pkt[13] = 10 // volume -10 dBm0
			if end {
				pkt[13] |= 0x80
			}
			binary.BigEndian.PutUint16(pkt[14:], dur)
			l.ep.SendRTP(pkt)
			seq++
			time.Sleep(20 * time.Millisecond)
		}
		ts += 8 * 250
		time.Sleep(80 * time.Millisecond)
	}
	return nil
}

// eventPT is the telephone-event payload type this party agreed, or -1.
func (l *Leg) eventPT() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.remoteSDP
	if s == nil || s.Audio() == nil {
		return -1
	}
	for _, cd := range s.Audio().Codecs() {
		if strings.EqualFold(cd.Name, "telephone-event") && cd.ClockRate == 8000 {
			return cd.PT
		}
	}
	return -1
}

// multicastPage copies a page to a multicast group as G.711 µ-law, until the page ends.
func (c *Call) multicastPage(addr string, caller *Leg) func(pkt []byte) {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return nil
	}
	conn, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(ap))
	if err != nil {
		c.e.Log.Warn("multicast paging unavailable", "address", addr, "err", err)
		return nil
	}
	go func() { <-c.cancelled; conn.Close() }()
	var mu sync.Mutex
	var x *media.Transcoder
	from := -1
	return func(pkt []byte) {
		if len(pkt) < 12 {
			return
		}
		pt := int(pkt[1] & 0x7f)
		if !media.Transcodable(pt) {
			return
		}
		mu.Lock()
		if pt != from {
			from, x = pt, nil
			if pt != media.PTPCMU {
				x = media.NewTranscoder(pt, media.PTPCMU)
			}
		}
		out := pkt
		if x != nil {
			out = x.Convert(pkt)
		}
		mu.Unlock()
		if out != nil { // nil while a frame is being buffered
			_, _ = conn.Write(out)
		}
	}
}
