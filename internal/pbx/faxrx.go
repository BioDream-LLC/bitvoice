package pbx

import (
	"errors"
	"time"

	"github.com/biodream-llc/bitvoice/internal/fax"
	"github.com/biodream-llc/bitvoice/internal/sdp"
)

// FaxIn is a fax BitVoice received, or tried to.
type FaxIn struct {
	Box     string // the fax box's number
	From    string // the caller's number
	Station string // the sender's own identity (TSI), usually its fax number
	Call    string
	At      time.Time
	Result  fax.Result
}

// t38Offer switches this leg to T.38 on its fax port: sound refused, the T.38 settings BitVoice
// takes (version 0, up to 14400 bit/s, training checked here, redundancy for lost packets).
func (l *Leg) t38Offer(port int) *sdp.Session {
	l.mu.Lock()
	base := l.localSDP
	l.mu.Unlock()
	s := base.Clone()
	for _, m := range s.Media {
		m.Port = 0
	}
	s.Media = append(s.Media, &sdp.Media{Type: "image", Port: port, Proto: "udptl", Formats: []string{"t38"},
		Lines: []sdp.Line{{Type: 'a', Value: "T38FaxVersion:0"}, {Type: 'a', Value: "T38MaxBitRate:14400"},
			{Type: 'a', Value: "T38FaxRateManagement:transferredTCF"}, {Type: 'a', Value: "T38FaxMaxBuffer:2000"},
			{Type: 'a', Value: "T38FaxMaxDatagram:400"}, {Type: 'a', Value: "T38FaxUdpEC:t38UDPRedundancy"}}})
	s.Lines = replaceOrigin(s.Lines, base)
	return s
}

// receiveFax is BitVoice's fax machine on an answered call: the call is switched to T.38, the fax
// is received, and the call is ended.
func (c *Call) receiveFax(number string) {
	e := c.e
	box := e.Config().FindFax(number)
	in := FaxIn{Box: number, From: c.callerExt, Call: c.ID, At: time.Now()}
	if in.From == "" {
		in.From = c.caller.Number
	}
	done := func(err error) {
		if err != nil && in.Result.Err == nil {
			in.Result.Err = err
		}
		if e.OnFax != nil {
			e.OnFax(in)
		}
	}
	if box == nil {
		done(errors.New("the fax box was removed"))
		c.hangup("no fax box", "system")
		return
	}
	l := c.caller
	l.mu.Lock()
	l.term = newFaxTerm()
	l.mu.Unlock()
	// The sender's gateway may switch to T.38 itself on hearing the answer; if not, BitVoice asks.
	ep, err := l.toT38(time.Second)
	if err != nil {
		done(errors.New("the sender's line does not take T.38 fax, which BitVoice needs to receive faxes itself"))
		c.hangup("fax needs T.38", "system")
		return
	}
	link := fax.NewLink(ep.SendRaw)
	rx := fax.NewReceiver(box.Station, link.Send)
	ep.OnRaw(func(d []byte) { link.Receive(d, rx.Handle) })
	rx.Start()
	e.Log.Info("receiving a fax", "call", c.ID, "box", number, "from", in.From)
	ended := make(chan struct{})
	go func() {
		for !c.isEnded() {
			time.Sleep(200 * time.Millisecond)
		}
		close(ended)
	}()
	select {
	case <-rx.Done():
	case <-ended:
		rx.Stop()
	case <-time.After(time.Hour):
		rx.Stop()
	}
	in.Result = rx.Result()
	in.Station = in.Result.RemoteID
	done(nil)
	e.Log.Info("fax done", "call", c.ID, "pages", len(in.Result.Pages), "err", in.Result.Err)
	// The sender hangs up after its goodbye (DCN); if it does not, BitVoice does.
	for i := 0; i < 50 && !c.isEnded(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if !c.isEnded() {
		c.hangup("fax received", "system")
	}
}

func (c *Call) isEnded() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ended
}
