package pbx

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/fax"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// faxTerm marks a leg on which BitVoice itself is the fax machine (receiving to a fax number, or
// sending): a switch to T.38 from the far side is answered here instead of passed on.
type faxTerm struct {
	once     sync.Once
	switched chan struct{}
}

func newFaxTerm() *faxTerm { return &faxTerm{switched: make(chan struct{})} }

func (t *faxTerm) done() { t.once.Do(func() { close(t.switched) }) }

// localT38 answers the far side's switch to T.38 on a leg where BitVoice is the fax machine.
func (l *Leg) localT38(tx *sip.ServerTx, m *sip.Message, offer *sdp.Session) {
	ep := l.imageEndpoint()
	oi := imageMedia(offer)
	if ep == nil || oi == nil {
		tx.Respond(sip.NewResponse(m, 488, "Not Acceptable Here"))
		return
	}
	answer := l.faxDescription(offer, ep.Port())
	if im := imageMedia(answer); im != nil {
		fitT38(im)
	}
	ep.SetRemote(offer.Connection(oi), oi.Port)
	l.mu.Lock()
	if !l.onFax {
		l.preFax = l.localSDP
	}
	l.localSDP, l.onFax = answer, true
	term := l.term
	l.mu.Unlock()
	r := sip.NewResponse(m, 200, "")
	r.Set("Contact", l.e.localContact(l.peer, ""))
	r.Set("Content-Type", "application/sdp")
	r.Body = answer.Bytes()
	tx.Respond(r)
	if term != nil {
		term.done()
	}
}

// fitT38 narrows the far side's T.38 settings to what BitVoice's fax machine does: version 0,
// redundancy rather than forward error correction, at most 14400 bit/s.
func fitT38(im *sdp.Media) {
	var lines []sdp.Line
	for _, a := range im.Lines {
		if a.Type != 'a' {
			lines = append(lines, a)
			continue
		}
		name, _, _ := strings.Cut(a.Value, ":")
		switch strings.ToLower(name) {
		case "t38faxversion":
			a.Value = "T38FaxVersion:0"
		case "t38faxudpec":
			a.Value = "T38FaxUdpEC:t38UDPRedundancy"
		case "t38maxbitrate":
			a.Value = "T38MaxBitRate:14400"
		case "t38faxfillbitremoval", "t38faxtranscodingmmr", "t38faxtranscodingjbig":
			continue // not offered back: BitVoice does none of these
		}
		lines = append(lines, a)
	}
	im.Lines = lines
}

// toT38 switches an answered leg to T.38 for BitVoice's own fax machine: it waits a moment for
// the far side to switch (gateways that hear fax tones usually do), then asks itself.
func (l *Leg) toT38(wait time.Duration) (*media.Endpoint, error) {
	l.mu.Lock()
	term := l.term
	l.mu.Unlock()
	select {
	case <-term.switched:
		return l.imageEP(), nil
	case <-time.After(wait):
	}
	ep := l.imageEndpoint()
	if ep == nil {
		return nil, errors.New("no port for the fax")
	}
	offer := l.t38Offer(ep.Port())
	ans, err := l.reinvite(offer)
	if err != nil && strings.Contains(err.Error(), "491") {
		// Both sides asked at once (glare): the far side's request wins.
		select {
		case <-term.switched:
			return l.imageEP(), nil
		case <-time.After(4 * time.Second):
		}
	}
	select {
	case <-term.switched:
		return l.imageEP(), nil
	default:
	}
	im := imageMedia(ans)
	if err != nil || im == nil {
		l.closeImage()
		return nil, errors.New("the other side's line does not take T.38 fax, which BitVoice needs to fax itself")
	}
	ep.SetRemote(ans.Connection(im), im.Port)
	l.mu.Lock()
	if !l.onFax {
		l.preFax = l.localSDP
	}
	l.localSDP, l.onFax = offer, true
	l.mu.Unlock()
	term.done()
	return ep, nil
}

// ErrFaxNoAnswer is a fax that nobody answered.
var ErrFaxNoAnswer = errors.New("the number did not answer")

// SendFax calls number from a fax number and sends the pages as BitVoice's own fax machine, over
// the outbound routes, as an extension's call would go. It returns when the fax is done.
func (e *Engine) SendFax(from, number string, pages []fax.Bitmap) (fax.SendResult, error) {
	cfg := e.Config()
	box := cfg.FindFax(from)
	if box == nil {
		return fax.SendResult{}, errors.New("no fax number " + from)
	}
	number = cleanNumber(number)
	if number == "" {
		return fax.SendResult{}, errors.New("no number to fax")
	}
	if len(pages) == 0 {
		return fax.SendResult{}, errors.New("nothing to send")
	}
	p := e.outbound(cfg, nil, number)
	if p.reject != 0 || len(p.stages) == 0 {
		reason := p.reason
		if reason == "" {
			reason = "No Route"
		}
		return fax.SendResult{}, errors.New(plainRefusal(number, reason))
	}
	cid := box.Station
	if cid == "" {
		cid = box.Number
	}
	h := &Call{e: e, ID: config.NewID(), start: time.Now(), from: cid, fromName: box.Name,
		answered: make(chan struct{}), cancelled: make(chan struct{}), results: make(chan forkResult, 64),
		xfer: make(chan *Leg, 1), direction: "outbound", to: number, trunk: p.trunk}
	p.direction = "outbound"
	go h.run(p)
	leg := <-h.xfer
	if leg == nil {
		return fax.SendResult{}, ErrFaxNoAnswer
	}
	c := &Call{e: e, ID: h.ID, start: h.start, answered: make(chan struct{}), cancelled: make(chan struct{}),
		results: make(chan forkResult, 64), from: cid, fromName: box.Name, to: number,
		direction: "outbound", toName: number, trunk: p.trunk, answer: time.Now(), localAnswer: true}
	leg.call = c
	c.caller = leg
	c.ansOnce.Do(func() { close(c.answered) })
	term := newFaxTerm()
	leg.mu.Lock()
	leg.term = term
	leg.mu.Unlock()
	e.mu.Lock()
	e.calls[c.ID] = c
	e.mu.Unlock()
	e.publish("call.started", c.info())
	e.Log.Info("sending a fax", "call", c.ID, "from", box.Number, "to", number, "pages", len(pages))

	ep, err := leg.toT38(3 * time.Second)
	if err != nil {
		c.hangup("fax needs T.38", "system")
		return fax.SendResult{}, err
	}
	link := fax.NewLink(ep.SendRaw)
	tx := fax.NewSender(cid, pages, link.Send)
	ep.OnRaw(func(d []byte) { link.Receive(d, tx.Handle) })
	tx.Start()
	ended := make(chan struct{})
	go func() {
		for !c.isEnded() {
			time.Sleep(200 * time.Millisecond)
		}
		close(ended)
	}()
	select {
	case <-tx.Done():
	case <-ended:
		tx.Stop()
		<-tx.Done()
	case <-time.After(time.Hour):
		tx.Stop()
		<-tx.Done()
	}
	res := tx.Result()
	e.Log.Info("fax sent", "call", c.ID, "pages", res.Pages, "rate", res.Rate, "err", res.Err)
	time.Sleep(time.Second) // let the goodbye (DCN) reach the other machine before hanging up
	if !c.isEnded() {
		c.hangup("fax sent", "system")
	}
	return res, res.Err
}
