package pbx

import (
	"fmt"
	"github.com/biodream-llc/bitvoice/internal/i3"
	"github.com/biodream-llc/bitvoice/internal/isdn"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/screen"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Call is one call: the caller's leg, the legs being rung, and the leg that answered.
type Call struct {
	e  *Engine
	ID string

	mu        sync.Mutex
	account   string       // the PIN an outbound route asked for, when its set records it
	charged   *isdn.Charge // the phone company's advice of charge, the last it gave
	caller    *Leg
	callee    *Leg
	forks     []*Leg
	early     *Leg
	offer     *sdp.Session // the caller's offer, nil for a delayed offer
	ringSent  bool
	ended     bool
	stageNo   int
	busy      bool
	best      int // best failure code seen
	player    *media.Player
	answered  chan struct{}
	ansOnce   sync.Once
	recPaused []store.Span // paused stretches of the finished recording
	// bridge mixes a supervisor in (listen, whisper, barge); onEnd runs when the call ends.
	bridge      *media.Bridge
	supervision string
	supAgent    string
	onEnd       []func()
	// place is where an emergency caller is, sent with the call to the provider.
	place *Place
	// i3 is what an NG911 call brought: the caller's location, Additional Data (nil otherwise).
	i3                 *i3.Call
	esinet             bool      // arrived on a trunk from the ESInet
	srcIP              string    // the address an ESInet call came from (for the BCF)
	suspicious         bool      // the BCF let it through marked suspicious
	inTrunkID          string    // the trunk it came in on (for policy rules)
	rebidAt            time.Time // the location was last fetched (by reference) at
	emergencyTold      bool      // the API was told this 911 call was answered
	takenBy, takenName string    // the call-taker who answered a 911 call
	takenAt            time.Time
	cancelled          chan struct{}
	cnlOnce            sync.Once
	results            chan forkResult

	direction, from, fromName, to, toName, trunk string
	callerExt                                    string
	signed                                       string           // the SHAKEN attestation this call was signed with
	vconv                                        *videoConverters // video converted between the two parties' codecs (under vmu)
	vmu                                          sync.Mutex
	keyframe                                     func() // a video room's request for a full picture for this caller
	cidOverride                                  string // the caller ID calls out present instead (a campaign's)
	deskExt                                      string // the hot desk the call was made from, when it was
	notice                                       string // a recording notice still to give: announce or beep
	noticeLeg                                    *Leg
	vip                                          bool // on the VIP list: to the front of queues
	screen                                       *screen.Verdict
	screened                                     string     // labelled, when screening marked it
	presented                                    string     // the caller ID sent out on a trunk
	endCode                                      int        // the SIP failure an unanswered call ended with
	briefing                                     *recording // both sides kept for a transfer briefing
	linkID                                       string     // web:<link id> for calls from a call link
	start, answer                                time.Time
	answeredBy, disposition, cause, hangupBy     string
	localAnswer                                  bool
	transferring                                 bool
	// xfer is set on the helper call that rings the new party of a blind transfer: whoever answers is
	// handed over on it (nil when nobody does) instead of being connected to a caller.
	xfer     chan *Leg
	helper   *Call
	rec      *recording
	paging   *paging
	intercom bool // a page another server passed on: its phones auto-answer
	// parking
	parkSlot, parkedBy string
	parkedAt           time.Time
}

type forkResult struct {
	leg   *Leg
	stage int
	code  int
}

func (c *Call) info() CallInfo {
	c.mu.Lock()
	ci := CallInfo{ID: c.ID, Direction: c.direction, From: c.from, FromName: c.fromName, To: c.to, ToName: c.toName,
		Trunk: c.trunk, Start: c.start, Answer: c.answer, Screen: c.screen, call: c}
	if c.i3 != nil || c.esinet {
		em := &Emergency{ESInet: c.esinet, Rebid: c.rebidAt, TakenBy: c.takenBy, TakenName: c.takenName, TakenAt: c.takenAt}
		if d := c.i3; d != nil {
			em.Location, em.LocationURI, em.Additional, em.Service, em.Problems = d.Location, d.LocationURI, d.Additional, d.Service, d.Problems
			em.PIDF = d.LocationRaw
		}
		ci.Emergency = em
	}
	legs := []*Leg{c.caller}
	if c.callee != nil {
		legs = append(legs, c.callee)
		ci.State = "answered"
	} else {
		legs = append(legs, c.forks...)
		ci.State = "ringing"
	}
	if c.localAnswer {
		ci.State = "answered"
	}
	if c.answeredBy != "" {
		ci.ToName = c.answeredBy
	}
	if c.parkSlot != "" {
		ci.State = "parked"
		ci.ToName = "Parked on " + c.parkSlot
	}
	c.mu.Unlock()
	worst := 0.0
	for _, l := range legs {
		if l == nil {
			continue
		}
		li := l.info()
		if li.OnHold {
			ci.State = "held"
		}
		if li.Media.MOS > 0 && (worst == 0 || li.Media.MOS < worst) {
			worst = li.Media.MOS
		}
		ci.Legs = append(ci.Legs, li)
	}
	ci.MOS = worst
	if ci.State == "answered" || ci.State == "held" {
		if x, _, _ := c.doorOf(); x != nil {
			ci.Door = x.Number
		}
	}
	ci.Recording = c.recordingState()
	c.mu.Lock()
	ci.Supervision = c.supervision
	c.mu.Unlock()
	return ci
}

// other returns the party on the far side of a leg.
func (c *Call) other(l *Leg) *Leg {
	c.mu.Lock()
	defer c.mu.Unlock()
	if l == c.caller {
		return c.callee
	}
	if l == c.callee {
		return c.caller
	}
	return nil
}

// run offers the call stage by stage until someone answers, the caller gives up, or nothing is left.
func (c *Call) run(p plan) {
	cfg := c.e.Config()
	depth := 0
	for {
		for _, st := range p.stages {
			c.mu.Lock()
			c.stageNo++
			stageNo := c.stageNo
			c.mu.Unlock()
			started := 0
			for _, t := range st.targets {
				if c.startFork(t, stageNo) {
					started++
				}
			}
			for _, w := range st.wake {
				c.wake(w.ext, w.name, stageNo, st.timeout)
			}
			if started == 0 && len(st.wake) == 0 {
				continue
			}
			timer := time.NewTimer(st.timeout)
			failed := 0
			// With apps being woken, the stage rings its whole time: they connect and are rung late.
			expect := started
			if len(st.wake) > 0 {
				expect = int(^uint(0) >> 1)
			}
		wait:
			for failed < expect {
				select {
				case <-c.answered:
					timer.Stop()
					return
				case <-c.cancelled:
					timer.Stop()
					c.cancelForks(nil)
					return
				case r := <-c.results:
					if r.stage != stageNo {
						continue
					}
					failed++
					c.noteFailure(r.code)
				case <-timer.C:
					c.cancelForks(nil)
					c.noteFailure(408)
					break wait
				}
			}
			timer.Stop()
			c.mu.Lock()
			busy := c.busy
			c.mu.Unlock()
			// A busy trunk number is busy on every trunk; a busy extension is still worth trying elsewhere.
			if st.failover && busy {
				break
			}
		}
		select {
		case <-c.answered:
			return
		case <-c.cancelled:
			return
		default:
		}
		if p.noAnswer == nil || depth > 3 {
			break
		}
		depth++
		next := c.e.resolve(cfg, *p.noAnswer, depth)
		if next.reject != 0 {
			c.reject(next.reject, next.reason)
			return
		}
		if next.local != "" {
			c.answerLocal(next)
			return
		}
		p = next
	}
	c.mu.Lock()
	code, busy := c.best, c.busy
	c.mu.Unlock()
	switch {
	case busy:
		c.reject(486, "")
	case code == 0, code == 408, code == 487:
		c.reject(480, "No Answer")
	default:
		c.reject(code, "")
	}
}

func (c *Call) noteFailure(code int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if code == 486 || code == 600 {
		c.busy = true
	}
	// Prefer the most informative failure: a definite refusal over a timeout.
	rank := func(x int) int {
		switch {
		case x == 607 || x == 608: // refused as unwanted, or by a carrier's spam analytics
			return 6
		case x == 486 || x == 600:
			return 5
		case x == 404 || x == 484 || x == 604:
			return 4
		case x == 403 || x == 603:
			return 3
		case x >= 500:
			return 2
		case x == 408 || x == 480 || x == 487:
			return 1
		}
		return 0
	}
	if rank(code) > rank(c.best) {
		c.best = code
	}
}

// startFork sends an INVITE to one target.
func (c *Call) startFork(t target, stageNo int) bool {
	e := c.e
	cfg := e.Config()
	via := ""
	if t.binding != nil && t.binding.peer.Local.IsValid() {
		via = t.binding.peer.Local.String() // the phone uses the shared address: its media does too
	}
	ep, err := e.pool.AllocateOn(via)
	if err != nil {
		return false
	}
	var peer sip.Peer
	var ruri, toVal, fromVal string
	callID := sip.NewCallID()
	tag := sip.NewTag()
	leg := &Leg{e: e, call: c, Number: t.ext, Name: t.name, Ext: t.ext, callID: callID, localTag: tag, ep: ep,
		localSeq: 1, stage: stageNo, confirm: t.confirm}
	c.mu.Lock()
	fromNum, fromName := c.from, c.fromName
	c.mu.Unlock()
	var rcd *config.Brand
	signCID := ""
	if t.trunk != nil {
		c.mu.Lock()
		passedOn := c.direction == "cluster" // the sending server counted the channel already
		c.mu.Unlock()
		if max := t.trunk.MaxChannels; max > 0 && !passedOn && e.trunkChannels(t.trunk.ID) >= max {
			// Every channel the plan includes is in use: fail this target so the next trunk is tried.
			ep.Close()
			c.noteFailure(503)
			return false
		}
		tp, err := e.trunkPeer(t.trunk)
		if err != nil {
			ep.Close()
			c.noteFailure(503)
			return false
		}
		peer = tp
		host := sip.HostPort(t.trunk.Host, t.trunk.Port)
		ruri = "sip:" + t.number + "@" + host
		if t.trunk.Transport == "tcp" || t.trunk.Transport == "tls" {
			ruri += ";transport=" + t.trunk.Transport
		}
		toVal = "<sip:" + t.number + "@" + host + ">"
		domain := t.trunk.FromDomain
		if domain == "" {
			domain = t.trunk.Host
		}
		if t.trunk.Teams && t.trunk.SBCName != "" {
			domain = t.trunk.SBCName // Microsoft matches the From host to the SBC's name
			ruri = strings.Replace(ruri, ";transport=", ";user=phone;transport=", 1)
		}
		cid := t.trunk.CallerID
		if x := cfg.FindExtension(c.callerExt); x != nil && x.CallerID != "" {
			cid = x.CallerID
		}
		if c.cidOverride != "" {
			cid = c.cidOverride
		}
		if cid == "" {
			cid = fromNum
		}
		brand := cfg.System.BrandFor(cid)
		if brand != nil && brand.Name != "" {
			fromName = brand.Name
		}
		fromVal = fmt.Sprintf("%s<sip:%s@%s>;tag=%s", display(fromName), cid, domain, tag)
		signCID = cid
		leg.Number, leg.Name, leg.TrunkID = t.number, t.number, t.trunk.ID
		c.mu.Lock()
		c.presented = cid
		c.mu.Unlock()
		if brand != nil && t.trunk.RichCallData {
			rcd = brand
		}
	} else if t.binding.node != "" {
		// A phone registered with another server: the call goes to that server, which rings it.
		np, addr, err := e.nodePeer(t.binding.node)
		if err != nil {
			ep.Close()
			c.noteFailure(480)
			return false
		}
		peer = np
		ruri = "sip:" + t.ext + "@" + addr + ";transport=" + strings.ToLower(np.Transport)
		leg.TrunkID = t.viaTrunk // priced and counted here, as a call out on that trunk
		toVal = "<sip:" + t.ext + "@" + addr + ">"
		fromVal = fmt.Sprintf("%s<sip:%s@%s>;tag=%s", display(fromName), fromNum, e.stack.LocalHost(peer), tag)
	} else {
		peer = t.binding.peer
		ruri = t.binding.requestURI()
		host := e.stack.LocalHost(peer)
		toVal = "<sip:" + t.ext + "@" + host + ">"
		fromVal = fmt.Sprintf("%s<sip:%s@%s>;tag=%s", display(fromName), fromNum, host, tag)
	}
	if t.trunk != nil {
		leg.wantSRTP = t.trunk.SecureMedia
	} else if t.binding != nil && t.binding.node != "" {
		leg.wantSRTP = peer.Transport == "TLS" // encrypted between servers whenever the keys travel privately
	} else if x := cfg.FindExtension(t.ext); x != nil {
		leg.wantSRTP = x.SecureMedia
	}
	leg.peer = peer
	leg.localURI = fromVal
	leg.remoteURI = toVal
	leg.remoteTarget = ruri

	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: ruri}
	inv.Set("From", fromVal)
	inv.Set("To", toVal)
	inv.Set("Call-ID", callID)
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", e.localContact(peer, fromNum))
	inv.Set("Allow", allowed)
	inv.Set("User-Agent", "BitVoice")
	if rcd != nil {
		richCallData(inv, rcd)
	}
	if t.trunk != nil && t.trunk.Teams {
		teamsHeaders(inv)
	}
	if t.trunk != nil && signCID != "" {
		c.signCall(cfg, t.trunk, inv, signCID, t.number)
	}
	if t.trunk != nil && t.divertFrom != "" {
		inv.Set("Diversion", fmt.Sprintf("<sip:%s@%s>;reason=%s;counter=1", t.divertFrom, e.stack.LocalHost(peer), t.divertReason))
	}
	if t.binding != nil && t.binding.node != "" && t.viaTrunk != "" {
		inv.Set(nodeHeader, e.Cluster.Sign(callID, t.ext+"@"+t.viaTrunk))
		inv.Set(trunkHeader, t.viaTrunk)
		inv.Set(callerHeader, c.callerExt)
	} else if t.binding != nil && t.binding.node != "" {
		inv.Set(nodeHeader, e.Cluster.Sign(callID, t.ext))
		c.mu.Lock()
		if c.paging != nil {
			inv.Set(pageHeader, "1") // the other server rings its phones with auto-answer
		}
		c.mu.Unlock()
	}
	if isIAX(t.trunk) && e.iax() != nil {
		inv.Set(IAXTokenHeader, e.iax().Token()) // the IAX2 gateway takes calls only from the engine
	}
	if t.trunk == nil {
		// Tell the phone who is calling in a way every phone displays.
		inv.Set("Alert-Info", "<http://bitvoice/internal>;info=alert-internal")
		// BitVoice's own call ID, so the mobile app can match the call to the push that woke it.
		inv.Set("X-BitVoice-Call", c.ID)
	}
	c.mu.Lock()
	paging := c.paging != nil || c.intercom
	c.mu.Unlock()
	kind := ""
	if t.ext != "" {
		if x := e.Config().FindExtension(t.ext); x != nil {
			kind = x.Kind
		}
	}
	if paging || kind == "speaker" { // an overhead speaker answers every call at once
		// Auto-answer on the speaker: Call-Info is what Yealink, Grandstream and Snom read; Alert-Info is
		// what Poly reads.
		inv.Set("Call-Info", "<sip:"+e.stack.LocalHost(peer)+">;answer-after=0")
		inv.Set("Alert-Info", "<http://bitvoice/page>;info=alert-autoanswer;delay=0")
		inv.Add("Alert-Info", "Auto Answer")
	}
	var body *sdp.Session
	if c.offer != nil {
		body = leg.describeFor(c.offer)
		shapeOffer(body.Audio(), codecsForKind(e.codecsFor(t.trunk), kind))
	} else {
		// BitVoice's own call (paging, a callback): only codecs it can speak, in this party's order.
		var own []sdp.Codec
		for _, n := range codecsForKind(e.codecsFor(t.trunk), kind) {
			if pt := media.CodecPT(n); media.Transcodable(pt) {
				own = append(own, codecNamed(n, pt))
			}
		}
		body = sdp.New(e.mediaHost(peer), ep.Port(), uint64(time.Now().Unix()), own, 101)
		_ = leg.secure(body, nil)
	}
	inv.Set("Content-Type", "application/sdp")
	inv.Body = body.Bytes()
	if c.direction == "emergency" && t.trunk != nil && c.place != nil && e.Config().System.SendLocation {
		host := e.stack.LocalHost(peer)
		ct, mixed, geo := withLocation(inv.Body, pidf(*c.place, "pres:"+c.callerExt+"@"+host, time.Now()), host)
		inv.Set("Content-Type", ct)
		inv.Body = mixed
		inv.Set("Geolocation", geo)
		inv.Set("Geolocation-Routing", "no")
	} else if t.trunk != nil && c.i3 != nil && len(c.i3.LocationRaw) > 0 {
		// A 911 call going on to a PSAP (routed by the ESRP, or transferred): its location goes with
		// it, as it came, and the service it asked for.
		host := e.stack.LocalHost(peer)
		ct, mixed, geo := withLocation(inv.Body, c.i3.LocationRaw, host)
		inv.Set("Content-Type", ct)
		inv.Body = mixed
		inv.Set("Geolocation", geo)
		inv.Set("Geolocation-Routing", "yes")
		if c.i3.LocationURI != "" {
			inv.Add("Geolocation", "<"+c.i3.LocationURI+">")
		}
	}
	leg.localSDP = body
	leg.inviteReq = inv.Clone()

	c.mu.Lock()
	if c.ended || c.callee != nil {
		c.mu.Unlock()
		ep.Close()
		return false
	}
	c.forks = append(c.forks, leg)
	c.mu.Unlock()
	e.addLeg(leg)
	ep.Start()
	tx := e.stack.Request(inv, peer)
	leg.mu.Lock()
	leg.clientTx = tx
	leg.mu.Unlock()
	go c.watchFork(leg, t.trunk)
	return true
}

// display renders a SIP display name as a quoted string (RFC 3261 25.1): control characters, which
// could end the header early, are dropped and backslashes and quotes are escaped.
func display(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f:
			continue
		case r == '"' || r == '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return ""
	}
	return `"` + b.String() + `" `
}

// watchFork follows the responses to one INVITE.
func (c *Call) watchFork(l *Leg, trunk *config.Trunk) {
	triedAuth := false
	l.mu.Lock()
	tx := l.clientTx
	l.mu.Unlock()
	for {
		var final *sip.Message
		for r := range tx.Responses {
			if r.StatusCode < 200 {
				c.forkProgress(l, r)
				continue
			}
			final = r
		}
		if final == nil {
			final = sip.NewResponse(tx.Req, 408, "")
		}
		if final.StatusCode < 300 {
			c.forkAnswered(l, tx, final)
			return
		}
		if (final.StatusCode == 401 || final.StatusCode == 407) && trunk != nil && !triedAuth && trunk.Password != "" {
			triedAuth = true
			next := l.withCredentials(final, trunk)
			if next != nil {
				c.mu.Lock()
				gone := c.ended || c.callee != nil || !c.hasFork(l)
				c.mu.Unlock()
				if !gone {
					tx = c.e.stack.Request(next, l.peer)
					l.mu.Lock()
					l.clientTx = tx
					l.mu.Unlock()
					continue
				}
			}
		}
		c.forkFailed(l, final.StatusCode)
		return
	}
}

func (c *Call) hasFork(l *Leg) bool {
	for _, f := range c.forks {
		if f == l {
			return true
		}
	}
	return false
}

// withCredentials re-sends an INVITE answering a trunk's challenge.
func (l *Leg) withCredentials(resp *sip.Message, t *config.Trunk) *sip.Message {
	h := resp.Get("WWW-Authenticate")
	header := "Authorization"
	if resp.StatusCode == 407 {
		h, header = resp.Get("Proxy-Authenticate"), "Proxy-Authorization"
	}
	ch, err := sip.ParseChallenge(h)
	if err != nil {
		return nil
	}
	user := t.AuthUser
	if user == "" {
		user = t.Username
	}
	l.mu.Lock()
	inv := l.inviteReq.Clone()
	l.localSeq++
	seq := l.localSeq
	l.mu.Unlock()
	cr, err := sip.Answer(ch, user, t.Password, sip.INVITE, inv.RequestURI)
	if err != nil {
		return nil
	}
	inv.Set("CSeq", strconv.FormatUint(uint64(seq), 10)+" INVITE")
	inv.Set(header, cr.String())
	inv.Del("Via")
	return inv
}

// forkProgress relays ringing and early media to the caller.
func (c *Call) forkProgress(l *Leg, r *sip.Message) {
	if r.StatusCode == 100 {
		return
	}
	if to, err := sip.ParseAddress(r.Get("To")); err == nil && to.Tag() != "" {
		l.mu.Lock()
		l.remoteTag = to.Tag()
		l.remoteURI = r.Get("To")
		l.ringing = true
		l.mu.Unlock()
	}
	c.mu.Lock()
	if c.ended || c.callee != nil || c.xfer != nil || c.localAnswer {
		c.mu.Unlock()
		return
	}
	a := c.caller
	var send *sip.Message
	if len(r.Body) > 0 && c.early == nil && c.offer != nil {
		if s, err := sdp.Parse(r.Body); err == nil && s.Audio() != nil {
			l.setRemoteSDP(s)
			l.answeredCrypto(s)
			c.early = l
			connect(a, l)
			ans := a.describeFor(s)
			fitAnswer(ans.Audio(), c.offer.Audio())
			a.mu.Lock()
			a.localSDP = ans
			a.mu.Unlock()
			a.sendCodec(ans)
			send = c.response(183, ans)
		}
	} else if !c.ringSent && c.early == nil {
		send = c.response(180, nil)
	}
	if send != nil {
		c.ringSent = true
	}
	c.mu.Unlock()
	if send != nil {
		a.invite.Respond(send)
	}
	c.e.publish("call.updated", c.info())
}

// response builds a response to the caller's INVITE. Called with c.mu held.
func (c *Call) response(code int, body *sdp.Session) *sip.Message {
	a := c.caller
	r := sip.NewResponse(a.invite.Req, code, "")
	r.Set("To", a.localURI)
	r.Set("Contact", c.e.localContact(a.peer, ""))
	r.Set("Allow", allowed)
	if body != nil {
		r.Set("Content-Type", "application/sdp")
		r.Body = body.Bytes()
	}
	return r
}

// forkAnswered connects the caller to whoever answered first.
func (c *Call) forkAnswered(l *Leg, tx *sip.ClientTx, r *sip.Message) {
	to, _ := sip.ParseAddress(r.Get("To"))
	l.mu.Lock()
	if to != nil {
		l.remoteTag = to.Tag()
	}
	l.remoteURI = r.Get("To")
	if ct, err := sip.ParseAddress(r.Get("Contact")); err == nil && ct.URI != nil {
		l.remoteTarget = ct.URI.String()
	}
	rr := r.GetAll("Record-Route")
	l.routeSet = nil
	for i := len(rr) - 1; i >= 0; i-- {
		l.routeSet = append(l.routeSet, rr[i])
	}
	l.answered = true
	l.mu.Unlock()

	// ACK the 2xx whatever happens next: an unacknowledged 2xx is retransmitted for half a minute.
	ack := &sip.Message{IsRequest: true, Method: sip.ACK, RequestURI: l.remoteTarget}
	for _, rt := range l.routeSet {
		ack.Add("Route", rt)
	}
	ack.Set("From", l.localURI)
	ack.Set("To", l.remoteURI)
	ack.Set("Call-ID", l.callID)
	n, _ := r.CSeq()
	ack.Set("CSeq", strconv.FormatUint(uint64(n), 10)+" ACK")
	if c.offer == nil && len(r.Body) > 0 {
		// Delayed offer from the caller: BitVoice offered in the INVITE, the callee answered; the caller is
		// offered the callee's description in our 200 and answers in its ACK.
		ack.Set("Content-Length", "0")
	}
	_ = c.e.stack.Stateless(ack, l.peer)
	tx.SetAck(ack)

	var answer *sdp.Session
	if len(r.Body) > 0 {
		if s, err := sdp.Parse(r.Body); err == nil {
			answer = s
		}
	}

	c.mu.Lock()
	if pg := c.paging; pg != nil && !c.ended {
		// A page keeps every phone that answers; none of them is the callee.
		if answer != nil {
			l.setRemoteSDP(answer)
			l.answeredCrypto(answer)
		}
		kept := c.forks[:0]
		for _, f := range c.forks {
			if f != l {
				kept = append(kept, f)
			}
		}
		c.forks = kept
		c.mu.Unlock()
		pg.add(l)
		c.e.publish("call.updated", c.info())
		return
	}
	if c.ended || c.callee != nil {
		c.mu.Unlock()
		l.bye()
		l.close()
		return
	}
	if answer == nil && c.early == l {
		answer = l.remoteSDP
	}
	if answer == nil || answer.Audio() == nil {
		c.mu.Unlock()
		l.bye()
		l.close()
		c.forkFailed(l, 488)
		return
	}
	l.setRemoteSDP(answer)
	l.answeredCrypto(answer)
	if l.confirm {
		// Follow-me to an outside number: the answerer must press 1 first; until then the others ring on.
		l.confirm = false
		c.mu.Unlock()
		l.sendCodec(answer)
		go c.confirmFork(l, func() { c.acceptFork(l, answer) })
		return
	}
	c.mu.Unlock()
	c.acceptFork(l, answer)
}

// acceptFork makes a party who answered the one the caller talks to.
func (c *Call) acceptFork(l *Leg, answer *sdp.Session) {
	c.mu.Lock()
	if c.ended || c.callee != nil {
		c.mu.Unlock()
		l.bye()
		l.close()
		return
	}
	c.callee = l
	c.answer = time.Now()
	c.answeredBy = l.party()
	if c.xfer == nil {
		c.startRecording(l)
	}
	others := c.forks
	c.forks = nil
	if c.xfer != nil {
		c.mu.Unlock()
		c.ansOnce.Do(func() { close(c.answered) })
		for _, f := range others {
			if f != l {
				c.dropFork(f)
			}
		}
		c.xfer <- l
		return
	}
	a := c.caller
	if c.localAnswer {
		// The caller was answered by BitVoice (a queue): connect the agent without a new answer to the
		// caller. Each side keeps its own codec; the relay converts between the G.711 laws.
		if c.player != nil {
			c.player.Stop()
			c.player = nil
		}
		c.mu.Unlock()
		connect(a, l)
		c.bridged()
		for _, f := range others {
			if f != l {
				c.dropFork(f)
			}
		}
		select {
		case c.results <- forkResult{leg: l, stage: l.stage, code: 200}:
		default:
		}
		c.e.publish("call.updated", c.info())
		return
	}
	resp := a.describeFor(answer)
	fitAnswer(resp.Audio(), c.offer.Audio())
	if c.e.videoTranscodeOn() {
		fitVideoAnswer(videoMedia(resp), videoMedia(c.offer))
	}
	a.mu.Lock()
	a.localSDP = resp
	a.answered = true
	a.mu.Unlock()
	a.sendCodec(resp)
	ok := c.response(200, resp)
	c.mu.Unlock()

	c.e.mu.Lock()
	delete(c.e.pending, a.invite.Branch())
	c.e.mu.Unlock()
	connect(a, l)
	a.invite.Respond(ok)
	c.bridged()
	c.ansOnce.Do(func() { close(c.answered) })
	for _, f := range others {
		if f != l {
			c.dropFork(f)
		}
	}
	c.e.publish("call.updated", c.info())
}

// forkFailed records a target that will not answer.
func (c *Call) forkFailed(l *Leg, code int) {
	c.mu.Lock()
	kept := c.forks[:0]
	for _, f := range c.forks {
		if f != l {
			kept = append(kept, f)
		}
	}
	c.forks = kept
	if c.early == l {
		c.early = nil
		connect(c.caller, nil)
	}
	c.mu.Unlock()
	l.close()
	select {
	case c.results <- forkResult{leg: l, stage: l.stage, code: code}:
	default:
	}
	c.e.publish("call.updated", c.info())
}

// dropFork stops ringing a target that is no longer wanted.
func (c *Call) dropFork(l *Leg) {
	l.mu.Lock()
	tx, answered := l.clientTx, l.answered
	l.mu.Unlock()
	if answered {
		l.bye()
		l.close()
		return
	}
	if tx != nil {
		tx.Cancel()
	}
	// The fork's goroutine sees the 487 and closes it.
}

func (c *Call) cancelForks(except *Leg) {
	c.mu.Lock()
	forks := append([]*Leg(nil), c.forks...)
	c.mu.Unlock()
	for _, f := range forks {
		if f != except {
			c.dropFork(f)
		}
	}
}

// callerCancelled handles a CANCEL from the caller.
func (c *Call) callerCancelled() {
	c.mu.Lock()
	if c.callee != nil || c.ended {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.cnlOnce.Do(func() { close(c.cancelled) })
	c.cancelForks(nil)
	c.finish(487, "", "cancelled", "caller", "caller hung up while ringing")
}

// reject refuses the call with a SIP status (or, once BitVoice has answered, ends it).
func (c *Call) reject(code int, reason string) {
	c.mu.Lock()
	answered := c.localAnswer
	c.mu.Unlock()
	if answered {
		c.hangup(sip.ReasonPhrase(code), "system")
		return
	}
	disp := "failed"
	switch code {
	case 486, 600:
		disp = "busy"
	case 480, 408:
		disp = "no-answer"
	}
	c.finish(code, reason, disp, "system", sip.ReasonPhrase(code))
}

// finish ends an unanswered call: answers the caller's INVITE with a failure and records the call.
func (c *Call) finish(code int, reason, disposition, by, cause string) {
	c.mu.Lock()
	if c.ended {
		c.mu.Unlock()
		return
	}
	c.ended = true
	c.disposition, c.hangupBy, c.cause = disposition, by, cause
	c.endCode = code
	if c.xfer != nil {
		c.mu.Unlock()
		c.cnlOnce.Do(func() { close(c.cancelled) })
		c.cancelForks(nil)
		c.xfer <- nil
		return
	}
	a := c.caller
	r := sip.NewResponse(a.invite.Req, code, reason)
	r.Set("To", a.localURI)
	c.mu.Unlock()
	a.invite.Respond(r)
	c.cancelForks(nil)
	c.cleanup()
}

// answerLocal answers a call BitVoice handles itself: the echo test or voicemail.
func (c *Call) answerLocal(p plan) {
	switch p.local {
	case "echo":
		if c.answerHere("Echo test") {
			c.caller.ep.Echo()
		}
	case "voicemail":
		if c.answerHere(p.toName) {
			go c.leaveMessage(p.box)
		}
	case "fax":
		if c.answerHere(p.toName) {
			go c.receiveFax(p.box)
		}
	case "console":
		if c.answerHere(p.toName) {
			go c.joinConsole()
		}
	case "queue":
		if c.answerHere(p.toName) {
			go c.waitInQueue(p.box)
		}
	case "menu":
		if c.answerHere(p.toName) {
			go c.runMenu(p.box)
		}
	case "receptionist":
		if c.answerHere(p.toName) {
			go c.runReceptionist(p.box)
		}
	case "agent":
		if c.answerHere(p.toName) {
			go c.agentCode(p.box)
		}
	case "hotdesk":
		if c.answerHere(p.toName) {
			go c.hotDeskCode(p.box)
		}
	case "emode":
		if c.answerHere(p.toName) {
			go c.toggleEmergencyMode()
		}
	case "pickup":
		c.pickup(p.box)
	case "grouppickup":
		c.groupPickup()
	case "directedpickup":
		c.directedPickup(p.box)
	case "divert":
		if c.answerHere(p.toName) {
			go c.divertCode(p.box)
		}
	case "conference":
		if rc := c.e.Config().FindConference(p.box); rc != nil && rc.Video != "" && c.offer != nil && videoMedia(c.offer) != nil && c.e.videoAllowed() {
			c.caller.videoCodec = rc.VideoCodec
			c.caller.videoEndpoint()
		}
		if c.answerHere(p.toName) {
			go c.joinConference(p.box)
		}
	case "wakeup":
		if c.answerHere(p.toName) {
			go c.wakeUpCode()
		}
	case "audiosocket":
		if c.answerHere(p.toName) {
			go c.runAudioStream(p.box)
		}
	case "pinout":
		if c.answerHere(p.toName) {
			go c.pinOut(p)
		}
	case "toggle":
		if c.answerHere(p.toName) {
			go c.toggleCode(p.box)
		}
	case "roomstatus":
		if c.answerHere(p.toName) {
			go c.roomStatusCode(p.box)
		}
	case "directory":
		if c.answerHere(p.toName) {
			go c.runDirectory(p.box)
		}
	case "dialin":
		if c.answerHere(p.toName) {
			go c.runDialIn(p.box)
		}
	case "page":
		if c.answerHere(p.toName) {
			if g := c.e.Config().FindPageGroup(p.box); g != nil {
				go c.page(g)
			}
		}
	case "messages":
		if c.answerHere("Voicemail") {
			go c.collectMessages(p.box)
		}
	}
}

// answerHere answers the caller at BitVoice, reporting false when the call was refused instead.
func (c *Call) answerHere(name string) bool {
	c.mu.Lock()
	if c.localAnswer {
		// Already answered (a menu passing the caller on): nothing to send, only who has the call now.
		c.answeredBy = name
		c.mu.Unlock()
		c.e.publish("call.updated", c.info())
		return true
	}
	a := c.caller
	if c.offer == nil {
		c.mu.Unlock()
		c.reject(488, "An Offer Is Needed")
		return false
	}
	ans := a.answerFor(c.offer)
	if ans != nil && !onlyG711(ans.Audio()) {
		ans = nil // BitVoice itself speaks G.711, G.722, Opus and G.729 (prompts, voicemail, tones)
	}
	if ans == nil {
		c.mu.Unlock()
		c.reject(488, "Use G.711, G.722, Opus or G.729")
		return false
	}
	c.localAnswer = true
	c.answer = time.Now()
	c.answeredBy = name
	a.mu.Lock()
	a.localSDP = ans
	a.answered = true
	a.mu.Unlock()
	a.sendCodec(ans)
	ok := c.response(200, ans)
	c.mu.Unlock()
	c.e.mu.Lock()
	delete(c.e.pending, a.invite.Branch())
	c.e.mu.Unlock()
	a.invite.Respond(ok)
	c.ansOnce.Do(func() { close(c.answered) })
	c.e.publish("call.updated", c.info())
	return true
}

// legEnded handles a party hanging up.
func (c *Call) legEnded(l *Leg, cause string) {
	c.mu.Lock()
	if pg := c.paging; pg != nil && l != c.caller {
		// One phone leaving a page does not end it for the others.
		c.mu.Unlock()
		pg.mu.Lock()
		for i, x := range pg.legs {
			if x == l {
				pg.legs = append(pg.legs[:i], pg.legs[i+1:]...)
				break
			}
		}
		pg.mu.Unlock()
		l.close()
		c.e.publish("call.updated", c.info())
		return
	}
	transferring := c.transferring
	isCaller := l == c.caller
	c.mu.Unlock()
	if transferring && !isCaller && l != c.callee {
		return
	}
	by := "callee"
	if isCaller {
		by = "caller"
	}
	c.hangup(l.party()+" "+cause, by)
}

// hangup ends an answered (or any) call: BYE to every party still up, then records it.
func (c *Call) hangup(cause, by string) {
	c.mu.Lock()
	if c.ended {
		c.mu.Unlock()
		return
	}
	answered := c.callee != nil || c.localAnswer
	if !answered {
		c.mu.Unlock()
		c.finish(480, "", "cancelled", by, cause)
		return
	}
	c.ended = true
	c.disposition, c.cause, c.hangupBy = "answered", cause, by
	legs := []*Leg{c.caller, c.callee}
	if c.paging != nil {
		legs = append(legs, c.paging.all()...)
	}
	ringing := len(c.forks) > 0
	if c.player != nil {
		c.player.Stop()
	}
	c.mu.Unlock()
	for _, l := range legs {
		if l != nil {
			l.bye()
		}
	}
	if ringing {
		c.cancelForks(nil) // a queued caller hanging up while an agent rings
	}
	c.cleanup()
}

// cleanup releases legs and writes the call record.
func (c *Call) cleanup() {
	c.cnlOnce.Do(func() { close(c.cancelled) })
	c.mu.Lock()
	ends := c.onEnd
	c.onEnd = nil
	c.mu.Unlock()
	for _, f := range ends {
		f()
	}
	c.mu.Lock()
	legs := []*Leg{c.caller, c.callee}
	legs = append(legs, c.forks...)
	if c.paging != nil {
		legs = append(legs, c.paging.all()...)
	}
	c.forks = nil
	if c.player != nil {
		c.player.Stop()
	}
	helper := c.helper
	c.mu.Unlock()
	if helper != nil {
		helper.callerCancelled()
	}
	info := c.info()
	// Give BYEs a moment to leave before the sockets close.
	time.AfterFunc(200*time.Millisecond, func() {
		for _, l := range legs {
			if l != nil {
				l.close()
			}
		}
	})
	c.e.mu.Lock()
	delete(c.e.calls, c.ID)
	if c.caller != nil && c.caller.invite != nil {
		delete(c.e.pending, c.caller.invite.Branch())
	}
	c.e.mu.Unlock()
	c.writeRecord(info, c.saveRecording())
	c.e.publish("call.ended", info)
}

func (c *Call) writeRecord(info CallInfo, recording string) {
	if c.e.Recorder == nil {
		return
	}
	c.mu.Lock()
	end := time.Now()
	r := store.CDR{ID: c.ID, Start: c.start.UTC(), End: end.UTC(), Direction: c.direction, From: c.from,
		FromName: c.fromName, To: c.to, ToName: c.toName, AnsweredBy: c.answeredBy, Trunk: c.trunk,
		Disposition: c.disposition, Cause: c.cause, HangupBy: c.hangupBy, MOS: info.MOS,
		Duration: int(end.Sub(c.start).Seconds()), Recording: recording, Paused: c.recPaused, Signed: c.signed}
	if !c.answer.IsZero() {
		r.Answer = c.answer.UTC()
		r.Billsec = int(end.Sub(c.answer).Seconds())
	}
	r.Ext = c.callerExt
	r.CallerID, r.Code = c.presented, c.endCode
	r.Emergency = c.i3 != nil || c.esinet
	if !c.takenAt.IsZero() {
		r.Taken = c.takenAt.UTC()
	}
	if c.screen != nil {
		r.Attest, r.Verified, r.Screened = c.screen.Attest, c.screen.Verified, c.screened
	}
	if c.callee != nil {
		r.AnsweredExt = c.callee.Ext
	}
	r.Account = c.account
	var trunkID, number string
	if c.callee != nil && c.callee.TrunkID != "" {
		trunkID, number = c.callee.TrunkID, c.callee.Number
	} else if c.caller != nil && c.caller.TrunkID != "" && c.direction == "inbound" {
		trunkID = c.caller.TrunkID
	}
	c.mu.Unlock()
	c.e.price(&r, trunkID, number)
	c.mu.Lock()
	charged := c.charged
	c.mu.Unlock()
	if charged != nil {
		r.Charged = charged.String()
		cur := c.e.Config().System.Costs.Currency
		if cur == "" {
			cur = "USD"
		}
		if charged.Kind == "currency" && strings.EqualFold(charged.Currency, cur) {
			if v, err := strconv.ParseFloat(charged.Money(), 64); err == nil {
				r.Cost, r.Rate = v, "Charged by the phone company"
			}
		} else if charged.Kind == "free" {
			r.Cost, r.Rate = 0, "Free, said the phone company"
		}
	}
	for _, l := range info.Legs {
		if l.Media.Packets == 0 {
			continue // nothing was heard from this side (ringing, or BitVoice answered itself)
		}
		r.Quality = append(r.Quality, store.LegQuality{Ext: l.Ext, Trunk: l.Trunk, Codec: l.Codec, Packets: l.Media.Packets,
			Lost: l.Media.Lost, JitterMS: l.Media.JitterMS, MOS: l.Media.MOS})
	}
	if c.direction == "cluster" {
		return // the server that has the caller writes the call's record
	}
	if err := c.e.Recorder.AddCDR(r); err != nil {
		c.e.Log.Error("could not write call record", "err", err)
		return
	}
	c.e.publish("call.logged", r) // the CRM webhook logs it
	if recording != "" {
		c.e.publish("call.recorded", r) // the AI worker transcribes it when that is switched on
	}
}

// checkMedia hangs up a call whose media has stopped in both directions for a minute.
func (c *Call) checkMedia() {
	const deadAfter = 60 * time.Second
	c.mu.Lock()
	if c.ended || (c.callee == nil && !c.localAnswer) || c.answer.IsZero() || time.Since(c.answer) < deadAfter {
		c.mu.Unlock()
		return
	}
	legs := []*Leg{c.caller}
	if c.callee != nil {
		legs = append(legs, c.callee)
	}
	c.mu.Unlock()
	for _, l := range legs {
		l.mu.Lock()
		hold := l.onHold
		l.mu.Unlock()
		if hold {
			return
		}
		st := l.ep.Stats()
		if img := l.imageEP(); img != nil { // a fax over T.38 sends no sound
			if fs := img.Stats(); fs.LastSeen > st.LastSeen {
				st = fs
			}
		}
		if st.LastSeen != 0 && time.Since(time.UnixMilli(st.LastSeen)) < deadAfter {
			return
		}
	}
	c.hangup("no media for 60 seconds", "system")
}

// holdChanged plays the hold tone to the other party while one side holds.
func (c *Call) holdChanged(l *Leg, hold bool) {
	other := c.other(l)
	if other == nil {
		return
	}
	c.mu.Lock()
	if c.player != nil {
		c.player.Stop()
		c.player = nil
	}
	if hold {
		connect(l, nil)
		connect(other, nil)
		cfg := c.e.Config()
		c.player = c.e.startHold(cfg, other, holdClassOf(cfg, l.Ext), media.ToneHold)
	} else {
		connect(l, other)
	}
	c.mu.Unlock()
	c.e.publish("call.updated", c.info())
}

// refer handles a transfer request from a party in an answered call.
func (c *Call) refer(l *Leg, tx *sip.ServerTx, m *sip.Message) {
	referTo, err := sip.ParseAddress(m.Get("Refer-To"))
	if err != nil || referTo.URI == nil {
		tx.Respond(sip.NewResponse(m, 400, "Bad Refer-To"))
		return
	}
	stay := c.other(l)
	if stay == nil {
		tx.Respond(sip.NewResponse(m, 603, "Nothing To Transfer"))
		return
	}
	r := sip.NewResponse(m, 202, "")
	r.Set("Contact", c.e.localContact(l.peer, ""))
	tx.Respond(r)
	if rep := referTo.URI.Headers; strings.Contains(strings.ToLower(rep), "replaces=") {
		c.attendedTransfer(l, stay, rep)
		return
	}
	go c.blindTransfer(l, stay, cleanNumber(referTo.URI.User))
}

// notifyRefer reports a transfer's progress to the party that asked for it.
// notifyReferIf reports transfer progress to a party that asked for the transfer with REFER; a transfer
// ordered from outside (the API) has nobody to tell.
func notifyReferIf(l *Leg, status string, final bool) {
	l.mu.Lock()
	asked := l.referred
	l.mu.Unlock()
	if asked {
		notifyRefer(l, status, final)
	}
}

func notifyRefer(l *Leg, status string, final bool) {
	n := l.newRequest(sip.NOTIFY)
	n.Set("Event", "refer")
	if final {
		n.Set("Subscription-State", "terminated;reason=noresource")
	} else {
		n.Set("Subscription-State", "active;expires=60")
	}
	n.Set("Content-Type", "message/sipfrag;version=2.0")
	n.Body = []byte("SIP/2.0 " + status + "\r\n")
	l.request(n)
}

// blindTransfer connects the party who stays to a new number and releases the party who transferred.
func (c *Call) blindTransfer(transferor, stay *Leg, number string) {
	cfg := c.e.Config()
	if number != cfg.System.ParkNumber {
		c.briefFor(cleanNumber(number), transferor.party())
	}
	var caller *config.Extension
	if transferor.Ext != "" {
		caller = cfg.FindExtension(transferor.Ext)
	}
	if number == cfg.System.ParkNumber && number != "" {
		if !c.park(transferor, stay) {
			notifyReferIf(transferor, "486 Every Park Slot Is Busy", true)
		}
		return
	}
	p := c.e.route(cfg, caller, number)
	if p.reject != 0 || p.local != "" || len(p.stages) == 0 {
		notifyReferIf(transferor, "404 Not Found", true)
		return
	}
	notifyReferIf(transferor, "100 Trying", false)

	// The party who stays hears ringback while the new party is rung.
	c.mu.Lock()
	c.transferring = true
	if c.player != nil {
		c.player.Stop()
	}
	connect(stay, nil)
	pt := 0
	if strings.EqualFold(stay.codecName(), "PCMA") {
		pt = 8
	}
	c.player = media.NewPlayer(stay.ep, pt)
	c.player.Play(media.Ringback(c.e.Config().System.Country))
	c.mu.Unlock()

	leg := c.dialForTransfer(stay, p)
	c.mu.Lock()
	if c.player != nil {
		c.player.Stop()
		c.player = nil
	}
	c.mu.Unlock()
	if leg == nil {
		notifyReferIf(transferor, "480 Temporarily Unavailable", true)
		c.mu.Lock()
		c.transferring = false
		up := !transferor.ended
		c.mu.Unlock()
		if up {
			connect(stay, transferor)
		} else {
			c.hangup("transfer failed", "system")
		}
		return
	}
	notifyReferIf(transferor, "200 OK", true)
	c.mu.Lock()
	if stay == c.caller {
		c.callee = leg
	} else {
		c.caller = leg
	}
	c.answeredBy = leg.party()
	c.to, c.toName = leg.Number, leg.Name
	c.transferring = false
	c.mu.Unlock()
	connect(stay, leg)
	c.bridged()
	go c.handToNetwork(stay, leg)
	transferor.bye()
	transferor.close()
	c.e.publish("call.updated", c.info())
}

// handToNetwork: after a transfer joined two calls on the telephony cards' trunk, the bridge is asked
// (a REFER naming the other call with Replaces) to have the phone company join them, freeing both
// channels. It is tried only when a span takes transfers; if the network refuses, the call goes on
// through this server as before.
func (c *Call) handToNetwork(a, b *Leg) {
	cfg := c.e.Config()
	if a == nil || b == nil || a.TrunkID == "" || a.TrunkID != b.TrunkID {
		return
	}
	if t := cfg.FindTrunk(a.TrunkID); t == nil || t.Protocol != "dahdi" {
		return
	}
	on := false
	for _, s := range cfg.System.Cards.Spans {
		on = on || s.NetworkTransfer
	}
	if !on {
		return
	}
	b.mu.Lock()
	replaces := b.callID + ";to-tag=" + b.remoteTag + ";from-tag=" + b.localTag
	b.mu.Unlock()
	m := a.newRequest(sip.REFER)
	m.Set("Refer-To", "<sip:cards@127.0.0.1?Replaces="+url.QueryEscape(replaces)+">")
	m.Set("Refer-Sub", "false")
	r := a.request(m)
	switch {
	case r != nil && r.StatusCode == 202:
		c.e.Log.Info("transfer handed to the phone company", "call", c.ID)
	case r != nil:
		c.e.Log.Info("the phone company did not take the transfer; it stays on this server", "call", c.ID, "status", r.StatusCode)
	}
}

// attendedTransfer joins the party who stays with the party on another call the transferor is in.
func (c *Call) attendedTransfer(transferor, stay *Leg, headers string) {
	callID, tags := parseReplaces(headers)
	c.e.mu.Lock()
	var target *Leg
	for _, l := range c.e.legs {
		if l.callID == callID && (tags[l.localTag] || tags[l.remoteTag]) {
			target = l
		}
	}
	c.e.mu.Unlock()
	if target == nil || target.call == c {
		notifyReferIf(transferor, "481 Call Does Not Exist", true)
		return
	}
	c2 := target.call
	join := c2.other(target) // the party on the second call who now talks to `stay`
	if join == nil {
		notifyReferIf(transferor, "481 Call Does Not Exist", true)
		return
	}
	// Move `join` into this call in place of the transferor; the second call loses both legs quietly.
	c2.mu.Lock()
	c2.ended = true
	c2.disposition, c2.cause, c2.hangupBy = "answered", "transferred", "transfer"
	if c2.player != nil {
		c2.player.Stop()
	}
	c2.mu.Unlock()
	join.call = c
	c.mu.Lock()
	if c.player != nil {
		c.player.Stop()
		c.player = nil
	}
	if stay == c.caller {
		c.callee = join
	} else {
		c.caller = join
	}
	c.answeredBy = join.party()
	c.mu.Unlock()
	connect(stay, join)
	go c.handToNetwork(stay, join)
	notifyReferIf(transferor, "200 OK", true)
	transferor.bye()
	target.bye()
	transferor.close()
	target.close()
	c2.e.mu.Lock()
	delete(c2.e.calls, c2.ID)
	c2.e.mu.Unlock()
	c2.writeRecord(c2.info(), c2.saveRecording())
	c2.e.publish("call.ended", c2.info())
	c.e.publish("call.updated", c.info())
}

// parseReplaces reads the Replaces header embedded in a Refer-To URI (RFC 3891), URL-encoded.
func parseReplaces(headers string) (string, map[string]bool) {
	tags := map[string]bool{}
	for _, h := range strings.Split(headers, "&") {
		eq := strings.IndexByte(h, '=')
		if eq < 0 || !strings.EqualFold(h[:eq], "replaces") {
			continue
		}
		v := unescape(h[eq+1:])
		parts := strings.Split(v, ";")
		for _, p := range parts[1:] {
			if kv := strings.SplitN(p, "=", 2); len(kv) == 2 {
				k := strings.ToLower(strings.TrimSpace(kv[0]))
				if k == "to-tag" || k == "from-tag" {
					tags[strings.TrimSpace(kv[1])] = true
				}
			}
		}
		return strings.TrimSpace(parts[0]), tags
	}
	return "", tags
}

func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(n))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// dialForTransfer rings the new party of a blind transfer with a helper call that offers exactly the codec
// the staying party is using, and returns the leg that answered (nil if nobody did).
func (c *Call) dialForTransfer(stay *Leg, p plan) *Leg {
	stay.mu.Lock()
	var offer *sdp.Session
	if stay.remoteSDP != nil && stay.remoteSDP.Audio() != nil {
		offer = stay.remoteSDP.Clone()
	}
	stay.mu.Unlock()
	if offer != nil {
		a := offer.Audio()
		if cs := a.Codecs(); len(cs) > 0 {
			filterFormats(a, []string{strconv.Itoa(cs[0].PT)})
		}
		setDirection(a, "sendrecv")
	}
	h := &Call{e: c.e, ID: config.NewID(), start: time.Now(), offer: offer, from: stay.Number, fromName: stay.Name,
		callerExt: stay.Ext, answered: make(chan struct{}), cancelled: make(chan struct{}),
		results: make(chan forkResult, 64), xfer: make(chan *Leg, 1)}
	c.mu.Lock()
	c.helper = h
	c.mu.Unlock()
	go h.run(p)
	leg := <-h.xfer
	c.mu.Lock()
	c.helper = nil
	c.mu.Unlock()
	if leg != nil {
		leg.call = c
	}
	return leg
}

// onlyG711 narrows an answer to the first G.711 codec offered (G.722 when the party offers no G.711: it is
// converted, which costs a little processor time), then telephone-event and comfort noise at 8 kHz, in
// that order (the first format is the one the phone sends), reporting false when there is none.
func onlyG711(m *sdp.Media) bool {
	if m == nil {
		return false
	}
	codec, event, cn := "", "", ""
	clock := 8000
	events := map[int]string{} // telephone-event by clock rate
	for _, c := range m.Codecs() {
		switch n := strings.ToUpper(c.Name); {
		case codec == "" && (n == "PCMU" || n == "PCMA"):
			codec = strconv.Itoa(c.PT)
		case n == "TELEPHONE-EVENT":
			if _, ok := events[c.ClockRate]; !ok {
				events[c.ClockRate] = strconv.Itoa(c.PT)
			}
		case cn == "" && n == "CN" && c.ClockRate == 8000:
			cn = strconv.Itoa(c.PT)
		}
	}
	// No G.711: the best of the codecs BitVoice converts (each costs a little processor time).
	for _, name := range []string{"G722", "OPUS", "AMR-WB", "G726-40", "G729", "G726-32", "AMR", "ILBC", "SPEEX", "AAL2-G726-32", "G726-24", "G726-16", "GSM"} {
		for _, c := range m.Codecs() {
			if codec == "" && strings.EqualFold(c.Name, name) {
				codec = strconv.Itoa(c.PT)
				if name == "OPUS" {
					clock, cn = 48000, "" // comfort noise is not used with Opus
				}
			}
		}
	}
	if codec == "" {
		return false
	}
	// Key presses at the codec's own clock (RFC 4733), which browsers need with Opus; else 8000.
	if event = events[clock]; event == "" {
		event = events[8000]
	}
	keep := []string{codec}
	for _, f := range []string{event, cn} {
		if f != "" {
			keep = append(keep, f)
		}
	}
	want := map[string]bool{}
	for _, f := range keep {
		want[f] = true
	}
	m.Formats = keep
	var lines []sdp.Line
	for _, l := range m.Lines {
		if l.Type == 'a' && (strings.HasPrefix(l.Value, "rtpmap:") || strings.HasPrefix(l.Value, "fmtp:")) {
			pt, _, _ := strings.Cut(strings.SplitN(l.Value, ":", 2)[1], " ")
			if !want[pt] {
				continue
			}
		}
		lines = append(lines, l)
	}
	m.Lines = lines
	return true
}

// trunkChannels counts the calls using a trunk now, in and out.
func (e *Engine) trunkChannels(id string) int {
	n := e.localTrunkChannels(id)
	if e.Cluster != nil {
		n += e.Cluster.TrunkCalls(id) // the channel limit is the provider's, across every server
	}
	return n
}

// localTrunkChannels counts this server's calls on a trunk (a call another server passed on is that
// server's to count).
func (e *Engine) localTrunkChannels(id string) int {
	n := 0
	for _, c := range e.allCalls() {
		c.mu.Lock()
		legs := append([]*Leg{c.caller, c.callee}, c.forks...)
		ended := c.ended || c.direction == "cluster"
		c.mu.Unlock()
		if ended {
			continue
		}
		seen := map[*Leg]bool{}
		for _, l := range legs {
			if l != nil && !seen[l] && l.TrunkID == id {
				seen[l] = true
				n++
			}
		}
	}
	return n
}

// price fills a call record's department and cost from the trunk's rates.
func (e *Engine) price(r *store.CDR, trunkID, number string) {
	cfg := e.Config()
	if x := cfg.FindExtension(r.Ext); x != nil {
		r.Department = x.Department
	}
	t := cfg.FindTrunk(trunkID)
	if t == nil || r.Billsec <= 0 {
		return
	}
	if number == "" { // a call in: the trunk's inbound price, by the minute
		if t.InboundPerMinute > 0 {
			r.Cost, r.Rate = config.Rate{PerMinute: t.InboundPerMinute}.Charge(r.Billsec), "Incoming"
		}
		return
	}
	if rate, ok := t.RateFor(number); ok {
		r.Cost, r.Rate = rate.Charge(r.Billsec), rate.Name
		if r.Rate == "" {
			r.Rate = "Starting " + rate.Prefix
		}
	}
}
