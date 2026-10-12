package pbx

import (
	"encoding/binary"
	"errors"
	"math"
	"net"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
)

// Alert tones: the tones a dispatcher sends on a channel to get attention before a message (a
// steady alert, a warble, pulses) and a short beep. The tone is a bridge party of its own,
// "tone:<channel>", that the channel hears (and dispatchers monitoring it, as sidetone) while it
// plays; the channel is held for it like a dispatcher's push-to-talk.

// AlertTones are the tones there are, with a description for the console.
var AlertTones = map[string]string{
	"steady": "Alert 1: a steady 1000 Hz tone, 3 seconds",
	"warble": "Alert 2: 1000 and 1500 Hz alternating, 3 seconds",
	"pulsed": "Alert 3: 1000 Hz pulses, a quarter second on and off, 3 seconds",
	"beep":   "A short 1000 Hz beep",
}

// toneSample is the tone's level at sample n (8 kHz).
func toneSample(kind string, n int) float64 {
	t := float64(n) / 8000
	const amp = 0.45 // about -7 dBFS: loud and clean through any vocoder
	switch kind {
	case "warble":
		f := 1000.0
		if int(t*8)%2 == 1 { // eight changes a second
			f = 1500
		}
		return amp * math.Sin(2*math.Pi*f*t)
	case "pulsed":
		if int(t*4)%2 == 1 {
			return 0
		}
	}
	return amp * math.Sin(2*math.Pi*1000*t)
}

func toneLength(kind string) int {
	if kind == "beep" {
		return 8000 / 4 // a quarter second
	}
	return 3 * 8000
}

// AlertTone sends an alert tone on a channel for a dispatcher's position. It returns once the
// tone has started; the channel is free again when it ends.
func (e *Engine) AlertTone(call, channel, kind string) error {
	if _, ok := AlertTones[kind]; !ok {
		return errors.New("no such alert tone")
	}
	r := e.radioSys()
	r.mu.Lock()
	p := r.positions[call]
	ch := r.channels[channel]
	switch {
	case p == nil:
		r.mu.Unlock()
		return ErrNoPosition
	case ch == nil:
		r.mu.Unlock()
		return errors.New("no such channel")
	case ch.tx != "" && ch.tx != call:
		r.mu.Unlock()
		return errors.New(ch.cfg.Name + " is in use by another dispatcher")
	case ch.tone:
		r.mu.Unlock()
		return errors.New("a tone is already playing on " + ch.cfg.Name)
	}
	ch.tx, ch.txAt, ch.tone, ch.toneKind, ch.notice = call, time.Now(), true, kind, ""
	group := ch.p25
	r.mu.Unlock()
	e.publish("radio.updated", nil)

	release := func() {
		r.mu.Lock()
		if ch.tx == call && !p.ptt[channel] {
			ch.tx = ""
		}
		ch.tone = false
		r.mu.Unlock()
		e.publish("radio.updated", nil)
	}
	ep, err := e.pool.Allocate()
	if err != nil {
		release()
		return err
	}
	src, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		ep.Close()
		release()
		return err
	}
	ep.SetRemote("127.0.0.1", src.LocalAddr().(*net.UDPAddr).Port)
	ep.SetPayloadType(media.PTPCMU)
	ep.Start()
	party := "tone:" + channel
	if group != nil && !group.group.Transmitting() {
		if err := group.group.Key(true); err != nil {
			src.Close()
			ep.Close()
			release()
			return err
		}
	}
	go func() {
		defer func() {
			r.bridge.Remove(party)
			src.Close()
			ep.Close()
			r.mu.Lock()
			keep := p.ptt[channel] // the dispatcher still holds push-to-talk
			r.mu.Unlock()
			if group != nil && !keep {
				_ = group.group.Key(false)
			}
			release()
		}()
		if group != nil { // a P25 talkgroup: the tone goes once the home RFSS grants the channel
			for i := 0; i < 100 && !group.group.Transmitting(); i++ {
				time.Sleep(20 * time.Millisecond)
			}
			if !group.group.Transmitting() {
				return
			}
		}
		r.bridge.Add(party, ep)
		to := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: ep.Port()}
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		var seq uint16
		n, total := 0, toneLength(kind)
		for n < total {
			<-tick.C
			pkt := make([]byte, 12, 172)
			pkt[0], pkt[1] = 0x80, media.PTPCMU
			binary.BigEndian.PutUint16(pkt[2:], seq)
			binary.BigEndian.PutUint32(pkt[4:], uint32(n))
			binary.BigEndian.PutUint32(pkt[8:], 0x70e)
			seq++
			for i := 0; i < 160; i++ {
				v := 0.0
				if n < total {
					v = toneSample(kind, n)
				}
				pkt = append(pkt, media.LinearToUlaw(int16(v*32767)))
				n++
			}
			_, _ = src.WriteToUDP(pkt, to)
		}
		time.Sleep(100 * time.Millisecond) // the bridge's buffer drains
	}()
	e.Log.Info("alert tone", "channel", ch.cfg.Name, "tone", kind)
	return nil
}
