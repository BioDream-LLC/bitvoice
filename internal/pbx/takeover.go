package pbx

import (
	"encoding/json"
	"errors"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sip"
)

// Calls that outlive their server. Every connected two-party call is saved to the shared database a
// moment after it connects or changes. When a server stops, a running server takes its calls: it
// opens new media ports and sends each party a re-INVITE with its own address and Contact (a target
// refresh, RFC 3261 12.2), so the party's sound and later requests (a BYE, a transfer) come to it.
// The call goes on; only the moments until the stopped server is noticed are silent.

// legSnap is what a party's dialog needs to be carried on by another server.
type legSnap struct {
	Inbound      bool     `json:"inbound"`
	Number       string   `json:"number"`
	Name         string   `json:"name"`
	Ext          string   `json:"ext"`
	TrunkID      string   `json:"trunkId"`
	CallID       string   `json:"callId"`
	LocalTag     string   `json:"localTag"`
	RemoteTag    string   `json:"remoteTag"`
	LocalURI     string   `json:"localUri"`
	RemoteURI    string   `json:"remoteUri"`
	RemoteTarget string   `json:"remoteTarget"`
	RouteSet     []string `json:"routeSet"`
	Transport    string   `json:"transport"`
	Addr         string   `json:"addr"`
	Host         string   `json:"host"`
	Via          string   `json:"via,omitempty"` // the shared address the party used
	LocalSeq     uint32   `json:"localSeq"`
	RemoteSDP    string   `json:"remoteSdp"`
	LocalSDP     string   `json:"localSdp"`
	OnHold       bool     `json:"onHold"`
	WantSRTP     bool     `json:"wantSrtp"`
}

// callSnap is a connected call, as saved for another server to take over.
type callSnap struct {
	ID         string    `json:"id"`
	Direction  string    `json:"direction"`
	From       string    `json:"from"`
	FromName   string    `json:"fromName"`
	To         string    `json:"to"`
	ToName     string    `json:"toName"`
	Trunk      string    `json:"trunk"`
	CallerExt  string    `json:"callerExt"`
	Start      time.Time `json:"start"`
	Answer     time.Time `json:"answer"`
	AnsweredBy string    `json:"answeredBy"`
	Caller     legSnap   `json:"caller"`
	Callee     legSnap   `json:"callee"`
}

// seqMargin is added to a taken-over leg's CSeq: requests sent after the last save used numbers we
// did not see, and a party refuses one that is not higher.
const seqMargin = 1000

func snapLeg(l *Leg) legSnap {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := legSnap{Inbound: l.inbound, Number: l.Number, Name: l.Name, Ext: l.Ext, TrunkID: l.TrunkID, CallID: l.callID,
		LocalTag: l.localTag, RemoteTag: l.remoteTag, LocalURI: l.localURI, RemoteURI: l.remoteURI, RemoteTarget: l.remoteTarget,
		RouteSet: l.routeSet, Transport: l.peer.Transport, Addr: l.peer.Addr.String(), Host: l.peer.Host, Via: viaOf(l.peer), LocalSeq: l.localSeq,
		OnHold: l.onHold, WantSRTP: l.wantSRTP || l.remoteKey != nil}
	if l.remoteSDP != nil {
		s.RemoteSDP = string(l.remoteSDP.Bytes())
	}
	if l.localSDP != nil {
		s.LocalSDP = string(l.localSDP.Bytes())
	}
	return s
}

// snapshot is the call as it can be taken over, or nil when it cannot: not connected to two parties,
// or doing something only this server can (a page, a park, a recording notice, supervision).
func (c *Call) snapshot() *callSnap {
	c.mu.Lock()
	a, b := c.caller, c.callee
	ok := !c.ended && a != nil && b != nil && c.paging == nil && c.xfer == nil && c.parkSlot == "" &&
		c.bridge == nil && c.player == nil && !c.answer.IsZero()
	s := &callSnap{ID: c.ID, Direction: c.direction, From: c.from, FromName: c.fromName, To: c.to, ToName: c.toName,
		Trunk: c.trunk, CallerExt: c.callerExt, Start: c.start, Answer: c.answer, AnsweredBy: c.answeredBy}
	c.mu.Unlock()
	if !ok {
		return nil
	}
	a.mu.Lock()
	ready := a.answered && a.ep != nil && a.remoteSDP != nil
	a.mu.Unlock()
	b.mu.Lock()
	ready = ready && b.answered && b.ep != nil && b.remoteSDP != nil
	b.mu.Unlock()
	if !ready {
		return nil
	}
	s.Caller, s.Callee = snapLeg(a), snapLeg(b)
	return s
}

// CallSnapshots are this server's calls that another could take over, for saving.
func (e *Engine) CallSnapshots() map[string][]byte {
	out := map[string][]byte{}
	for _, c := range e.allCalls() {
		if s := c.snapshot(); s != nil {
			if b, err := json.Marshal(s); err == nil {
				out[c.ID] = b
			}
		}
	}
	return out
}

// LetGo ends, without a word to anyone, calls another server has taken over: this server had been
// cut off from the others, and the parties now talk through the server that took them.
func (e *Engine) LetGo(ids []string) {
	for _, id := range ids {
		e.mu.Lock()
		c := e.calls[id]
		delete(e.calls, id)
		e.mu.Unlock()
		if c == nil {
			continue
		}
		c.mu.Lock()
		c.ended = true
		legs := []*Leg{c.caller, c.callee}
		c.mu.Unlock()
		for _, l := range legs {
			if l != nil {
				l.close()
			}
		}
		e.Log.Warn("another server took over a call while this one was cut off", "call", id)
		e.publish("call.ended", map[string]any{"id": id})
	}
}

// TakeOver carries on calls a stopped server was carrying. It reports how many went on.
func (e *Engine) TakeOver(snaps [][]byte) int {
	// Every call at once: one whose phone does not answer must not hold up the others.
	var n atomic.Int32
	var wg sync.WaitGroup
	for _, b := range snaps {
		var s callSnap
		if json.Unmarshal(b, &s) != nil || s.ID == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := e.takeOver(&s); err != nil {
				e.Log.Warn("could not carry on a call from a stopped server", "call", s.ID, "err", err)
				return
			}
			n.Add(1)
		}()
	}
	wg.Wait()
	return int(n.Load())
}

func (e *Engine) takeOver(s *callSnap) error {
	for _, via := range []string{s.Caller.Via, s.Callee.Via} {
		// A party reached through the shared address: wait until the address has arrived here.
		for i := 0; via != "" && e.HoldsAddress != nil && !e.HoldsAddress() && i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}
	c := &Call{e: e, ID: s.ID, start: s.Start, answer: s.Answer, answered: make(chan struct{}), cancelled: make(chan struct{}),
		results: make(chan forkResult, 4), direction: s.Direction, from: s.From, fromName: s.FromName, to: s.To,
		toName: s.ToName, trunk: s.Trunk, callerExt: s.CallerExt, answeredBy: s.AnsweredBy}
	c.ansOnce.Do(func() { close(c.answered) })
	a, err := e.revive(c, s.Caller)
	if err != nil {
		return err
	}
	b, err := e.revive(c, s.Callee)
	if err != nil {
		a.close()
		return err
	}
	c.caller, c.callee = a, b
	e.mu.Lock()
	e.calls[c.ID] = c
	e.mu.Unlock()
	e.addLeg(a)
	e.addLeg(b)
	// Both parties at once: each hears nothing until it has moved, so neither waits on the other.
	errs := make(chan error, 2)
	for _, l := range []*Leg{a, b} {
		go func() { errs <- l.moveHere() }()
	}
	var failed error
	for range 2 {
		if err := <-errs; err != nil {
			failed = err
		}
	}
	if failed != nil {
		// One party could not be reached from here: end the call cleanly for the other, and record it.
		c.mu.Lock()
		c.disposition, c.cause, c.hangupBy = "answered", "the server carrying it stopped, and a party could not be reached", "system"
		c.mu.Unlock()
		c.hangup("the server carrying it stopped", "system")
		return failed
	}
	connect(a, b)
	e.Log.Info("carried on a call from a stopped server", "call", c.ID, "from", s.From, "to", s.To)
	e.publish("call.updated", c.info())
	return nil
}

// revive rebuilds a party's leg from its saved dialog, with a new media port here.
func (e *Engine) revive(c *Call, s legSnap) (*Leg, error) {
	addr, err := netip.ParseAddrPort(s.Addr)
	if err != nil {
		return nil, errors.New("no address for " + s.Number)
	}
	remote, err := sdp.Parse([]byte(s.RemoteSDP))
	if err != nil || remote.Audio() == nil {
		return nil, errors.New("no media description for " + s.Number)
	}
	peer := sip.Peer{Transport: s.Transport, Addr: addr, Host: s.Host}
	if v, err := netip.ParseAddr(s.Via); err == nil {
		peer.Local = v // through the shared address, now held here
	}
	ep, err := e.pool.AllocateOn(s.Via)
	if err != nil {
		return nil, err
	}
	l := &Leg{e: e, call: c, inbound: s.Inbound, Number: s.Number, Name: s.Name, Ext: s.Ext, TrunkID: s.TrunkID,
		callID: s.CallID, localTag: s.LocalTag, remoteTag: s.RemoteTag, localURI: s.LocalURI, remoteURI: s.RemoteURI,
		remoteTarget: s.RemoteTarget, routeSet: s.RouteSet, peer: peer,
		localSeq: s.LocalSeq + seqMargin, ep: ep, answered: true, onHold: s.OnHold, wantSRTP: s.WantSRTP}
	ep.Start()
	l.setRemoteSDP(remote)
	if s.LocalSDP != "" {
		if ls, err := sdp.Parse([]byte(s.LocalSDP)); err == nil {
			l.localSDP = ls
		}
	}
	return l, nil
}

// moveHere sends the party a re-INVITE: the same session, with this server's media port and Contact.
func (l *Leg) moveHere() error {
	l.mu.Lock()
	base := l.localSDP
	if base == nil {
		base = l.remoteSDP
	}
	hold := l.onHold
	l.mu.Unlock()
	offer := base.Clone()
	stripICE(offer)
	host := l.e.mediaHost(l.peer)
	offer.SetConnection(host)
	for _, m := range offer.Media {
		if m.Type == "audio" && m.Port != 0 {
			m.Port = l.ep.Port()
			dir := "sendrecv"
			if hold {
				dir = "recvonly" // the party had put the call on hold; it stays so
			}
			setDirection(m, dir)
		} else {
			m.Port = 0
		}
	}
	keepOrigin(offer, host) // the same session (o= id), a newer version
	if err := l.secureOffer(offer); err != nil {
		return err
	}
	ans, err := l.reinvite(offer)
	if err != nil {
		return err
	}
	if ans.Audio() == nil {
		return errors.New(l.Number + " answered without sound")
	}
	l.setRemoteSDP(ans)
	l.answeredCrypto(ans)
	l.sendCodec(ans)
	l.mu.Lock()
	l.localSDP = offer
	l.mu.Unlock()
	return nil
}

// secureOffer offers SRTP with a new key when the party used it: the old server's keys went with it.
func (l *Leg) secureOffer(offer *sdp.Session) error {
	l.mu.Lock()
	want := l.wantSRTP
	l.localKey = nil
	l.mu.Unlock()
	a := offer.Audio()
	if !want {
		a.Proto = "RTP/AVP"
		a.SetCrypto(nil)
		return nil
	}
	return l.secure(offer, nil)
}

// keepOrigin keeps the session's o= id and raises its version, with this server as the address.
func keepOrigin(s *sdp.Session, host string) {
	for i := range s.Lines {
		if s.Lines[i].Type != 'o' {
			continue
		}
		f := strings.Fields(s.Lines[i].Value)
		if len(f) != 6 {
			continue
		}
		v, _ := strconv.ParseUint(f[2], 10, 64)
		f[2] = strconv.FormatUint(v+1, 10)
		f[5] = host
		if strings.Contains(host, ":") {
			f[4] = "IP6"
		} else {
			f[4] = "IP4"
		}
		s.Lines[i].Value = strings.Join(f, " ")
	}
}

func viaOf(p sip.Peer) string {
	if p.Local.IsValid() {
		return p.Local.String()
	}
	return ""
}

// reinvite sends this party a new offer inside the call and returns its answer, acknowledged.
func (l *Leg) reinvite(offer *sdp.Session) (*sdp.Session, error) {
	m := l.newRequest(sip.INVITE)
	m.Set("Content-Type", "application/sdp")
	m.Body = offer.Bytes()
	tx := l.e.stack.Request(m, l.peer)
	var final *sip.Message
	for r := range tx.Responses {
		if r.StatusCode >= 200 {
			final = r
		}
	}
	if final == nil {
		return nil, errors.New(l.Number + " did not answer from this server")
	}
	if final.StatusCode >= 300 {
		return nil, errors.New(l.Number + " refused: " + strconv.Itoa(final.StatusCode) + " " + final.Reason)
	}
	l.mu.Lock()
	target, routes := l.remoteTarget, l.routeSet
	l.mu.Unlock()
	ack := &sip.Message{IsRequest: true, Method: sip.ACK, RequestURI: target}
	for _, rt := range routes {
		ack.Add("Route", rt)
	}
	ack.Set("From", l.localURI)
	ack.Set("To", l.remoteURI)
	ack.Set("Call-ID", l.callID)
	n, _ := final.CSeq()
	ack.Set("CSeq", strconv.FormatUint(uint64(n), 10)+" ACK")
	_ = l.e.stack.Stateless(ack, l.peer)
	tx.SetAck(ack)
	if ct, err := sip.ParseAddress(final.Get("Contact")); err == nil && ct.URI != nil {
		l.mu.Lock()
		l.remoteTarget = ct.URI.String()
		l.mu.Unlock()
	}
	ans, err := sdp.Parse(final.Body)
	if err != nil || len(ans.Media) == 0 {
		return nil, errors.New(l.Number + " answered without media")
	}
	return ans, nil
}
