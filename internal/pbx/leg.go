package pbx

import (
	"fmt"
	"github.com/biodream-llc/bitvoice/internal/isdn"
	"strconv"
	"strings"
	"sync"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// Leg is one SIP dialog between BitVoice and a party, with the media endpoint that faces that party.
type Leg struct {
	e    *Engine
	call *Call

	inbound bool // created from an INVITE BitVoice received
	// videoCodec, when set, is the only video codec this leg's answers take (a video conference room).
	videoCodec string
	Number     string
	Name       string
	Ext        string // extension number when the party is an extension
	TrunkID    string

	callID       string
	localTag     string
	remoteTag    string
	localURI     string // our From (outbound) or To (inbound) value, with our tag
	remoteURI    string // the other side's, with its tag once known
	remoteTarget string
	routeSet     []string
	peer         sip.Peer

	mu        sync.Mutex
	localSeq  uint32
	ep        *media.Endpoint
	remoteSDP *sdp.Session // the last description this party sent
	localSDP  *sdp.Session // the last description BitVoice sent this party
	onHold    bool
	ended     bool
	// referred: this party asked for a transfer with REFER and expects NOTIFYs about it.
	referred bool
	answered bool
	ringing  bool

	invite    *sip.ServerTx // inbound: the initial INVITE
	clientTx  *sip.ClientTx // outbound: the initial INVITE
	inviteReq *sip.Message  // outbound: the INVITE as sent, for re-sending with credentials
	stage     int
	confirm   bool // follow-me to an outside number: press 1 before the call is put through

	// SRTP: whether to offer it to this party, our master key and suite, and the party's key.
	wantSRTP  bool
	localKey  []byte
	suite     string
	remoteKey []byte
	// video carries the call's video, when both parties send it: relayed as it is, keyed apart.
	video      *media.Endpoint
	vLocalKey  []byte
	vRemoteKey []byte
	vSuite     string
	// image carries a T.38 fax (UDPTL) while onFax; preFax is the description from before it.
	image  *media.Endpoint
	onFax  bool
	preFax *sdp.Session
	term   *faxTerm // BitVoice is the fax machine on this leg
}

// party describes the leg for records.
func (l *Leg) party() string {
	if l.Name != "" && l.Name != l.Number {
		return l.Number + " " + l.Name
	}
	return l.Number
}

func (l *Leg) newRequest(method string) *sip.Message {
	l.mu.Lock()
	l.localSeq++
	seq := l.localSeq
	l.mu.Unlock()
	m := &sip.Message{IsRequest: true, Method: method, RequestURI: l.remoteTarget}
	for _, r := range l.routeSet {
		m.Add("Route", r)
	}
	m.Set("From", l.localURI)
	m.Set("To", l.remoteURI)
	m.Set("Call-ID", l.callID)
	m.Set("CSeq", fmt.Sprintf("%d %s", seq, method))
	m.Set("Contact", l.e.localContact(l.peer, ""))
	m.Set("User-Agent", "BitVoice")
	return m
}

// request sends an in-dialog request and returns the final response (or nil on timeout).
func (l *Leg) request(m *sip.Message) *sip.Message {
	tx := l.e.stack.Request(m, l.peer)
	var final *sip.Message
	for r := range tx.Responses {
		if r.StatusCode >= 200 {
			final = r
		}
	}
	return final
}

// bye ends the dialog from BitVoice's side.
func (l *Leg) bye() {
	l.mu.Lock()
	if l.ended {
		l.mu.Unlock()
		return
	}
	l.ended = true
	answered := l.answered
	l.mu.Unlock()
	if answered {
		go l.request(l.newRequest(sip.BYE))
	}
}

func (l *Leg) close() {
	l.mu.Lock()
	l.ended = true
	ep := l.ep
	l.mu.Unlock()
	l.e.removeLeg(l)
	if ep != nil {
		ep.Close()
	}
	stopVideoConversion(l)
	l.closeVideo()
	l.closeImage()
}

// receive handles a request inside this leg's dialog.
func (l *Leg) receive(tx *sip.ServerTx, in *sip.Incoming) {
	m := in.Msg
	ok := func() {
		if tx != nil {
			tx.Respond(sip.NewResponse(m, 200, ""))
		}
	}
	switch m.Method {
	case sip.ACK:
		// The answer to a delayed offer arrives in the ACK.
		if len(m.Body) > 0 {
			if s, err := sdp.Parse(m.Body); err == nil {
				l.setRemoteSDP(s)
			}
		}
	case sip.BYE:
		l.charge(m)
		ok()
		l.mu.Lock()
		l.ended = true
		l.mu.Unlock()
		l.call.legEnded(l, "hung up")
	case sip.INVITE, sip.UPDATE:
		l.reoffer(tx, m)
	case sip.REFER:
		l.mu.Lock()
		l.referred = true
		l.mu.Unlock()
		l.call.refer(l, tx, m)
	case sip.INFO:
		if l.charge(m) {
			ok()
			return
		}
		ok()
		if strings.Contains(string(m.Body), "picture_fast_update") {
			l.call.mu.Lock()
			kf := l.call.keyframe
			l.call.mu.Unlock()
			if kf != nil {
				go kf() // a video room member asks for a full picture
				return
			}
		}
		// DTMF by SIP INFO goes to the other party unchanged.
		if other := l.call.other(l); other != nil && len(m.Body) > 0 {
			fwd := other.newRequest(sip.INFO)
			fwd.Set("Content-Type", m.Get("Content-Type"))
			fwd.Body = m.Body
			go other.request(fwd)
		}
	case sip.OPTIONS, sip.NOTIFY, sip.MESSAGE:
		ok()
	case sip.CANCEL:
		ok()
	default:
		if tx != nil {
			tx.Respond(sip.NewResponse(m, 501, ""))
		}
	}
}

// setRemoteSDP takes a description from this party: where to send its media and which law it speaks.
func (l *Leg) setRemoteSDP(s *sdp.Session) {
	a := s.Audio()
	if a == nil {
		return
	}
	l.mu.Lock()
	l.remoteSDP = s
	ep := l.ep
	l.mu.Unlock()
	if ep == nil {
		return
	}
	ep.SetRemote(s.Connection(a), a.Port)
	applyCodec(ep, a.Codecs())
	l.videoRemote(s, false)
}

// reoffer answers a re-INVITE or UPDATE locally. Media is anchored, so a party changing address, putting
// the call on hold or refreshing the session never needs the other party involved: BitVoice updates where
// it sends media, plays hold music to the other side if this one went on hold, and answers.
func (l *Leg) reoffer(tx *sip.ServerTx, m *sip.Message) {
	if len(m.Body) == 0 {
		// A session refresh, or a re-INVITE asking BitVoice to offer: offer what this party already has.
		l.mu.Lock()
		local := l.localSDP
		l.mu.Unlock()
		r := sip.NewResponse(m, 200, "")
		r.Set("Contact", l.e.localContact(l.peer, ""))
		if local != nil && m.Method == sip.INVITE {
			c := local.Clone()
			c.BumpVersion()
			r.Set("Content-Type", "application/sdp")
			r.Body = c.Bytes()
		}
		tx.Respond(r)
		return
	}
	offer, err := sdp.Parse(m.Body)
	if err == nil && imageMedia(offer) != nil {
		l.mu.Lock()
		term := l.term
		l.mu.Unlock()
		if term != nil {
			l.localT38(tx, m, offer) // BitVoice's own fax machine is on this leg
			return
		}
		l.faxSwitch(tx, m, offer) // a fax switching to T.38
		return
	}
	if err != nil || offer.Audio() == nil {
		tx.Respond(sip.NewResponse(m, 488, ""))
		return
	}
	l.faxBack()           // back to sound after a fax
	l.videoUpgrade(offer) // a camera switched on: ask the other party first
	answer := l.answerFor(offer)
	if answer == nil {
		tx.Respond(sip.NewResponse(m, 488, "No Common Codec"))
		return
	}
	l.setRemoteSDP(offer)
	dir := offer.Direction(offer.Audio())
	hold := dir == "sendonly" || dir == "inactive" || offer.Connection(offer.Audio()) == "0.0.0.0"
	r := sip.NewResponse(m, 200, "")
	r.Set("Contact", l.e.localContact(l.peer, ""))
	r.Set("Content-Type", "application/sdp")
	r.Body = answer.Bytes()
	tx.Respond(r)
	l.sendCodec(answer)
	l.mu.Lock()
	l.localSDP = answer
	was := l.onHold
	l.onHold = hold
	l.mu.Unlock()
	if hold != was {
		l.call.holdChanged(l, hold)
	}
}

// answerFor builds BitVoice's answer to an offer from this party: the codecs offered that BitVoice already
// agreed with the party, at this leg's port.
func (l *Leg) answerFor(offer *sdp.Session) *sdp.Session {
	l.mu.Lock()
	prev := l.localSDP
	port := l.ep.Port()
	l.mu.Unlock()
	ans := offer.Clone()
	stripICE(ans)
	ans.SetConnection(l.e.mediaHost(l.peer))
	a := ans.Audio()
	a.Port = port
	if prev != nil && prev.Audio() != nil {
		known := map[string]bool{}
		for _, c := range prev.Audio().Codecs() {
			known[strings.ToUpper(c.Name)] = true
		}
		var keep []string
		for _, c := range a.Codecs() {
			if known[strings.ToUpper(c.Name)] {
				keep = append(keep, strconv.Itoa(c.PT))
			}
		}
		if len(keep) == 0 {
			return nil
		}
		filterFormats(a, keep)
	}
	// Video goes on when this leg already carries it; other media is refused: port 0 (RFC 3264 6).
	carried := false
	for i, m := range ans.Media {
		if m == a {
			continue
		}
		if m.Type == "video" && m.Port != 0 && !carried && l.videoEP() != nil {
			if l.videoCodec != "" {
				var keep []string
				for _, c := range m.Codecs() {
					if strings.EqualFold(c.Name, l.videoCodec) {
						keep = append(keep, strconv.Itoa(c.PT))
						break
					}
				}
				if len(keep) == 0 {
					m.Port = 0
					continue
				}
				filterFormats(m, keep)
			}
			carried = l.describeVideo(m, offer.Media[i], a.Secure() || offer.Audio().Secure())
			continue
		}
		m.Port = 0
	}
	setDirection(a, answerDirection(offer.Direction(offer.Audio())))
	// The origin must be BitVoice's own and must increase on every change (RFC 3264 8).
	ans.Lines = replaceOrigin(ans.Lines, prev)
	if l.secure(ans, offer) != nil {
		return nil
	}
	return ans
}

func answerDirection(offered string) string {
	switch offered {
	case "sendonly":
		return "recvonly"
	case "recvonly":
		return "sendonly"
	case "inactive":
		return "inactive"
	}
	return "sendrecv"
}

func replaceOrigin(lines []sdp.Line, prev *sdp.Session) []sdp.Line {
	if prev == nil {
		return lines
	}
	var origin string
	for _, l := range prev.Lines {
		if l.Type == 'o' {
			origin = l.Value
		}
	}
	f := strings.Fields(origin)
	if len(f) != 6 {
		return lines
	}
	if n, err := strconv.ParseUint(f[2], 10, 64); err == nil {
		f[2] = strconv.FormatUint(n+1, 10)
	}
	for i := range lines {
		if lines[i].Type == 'o' {
			lines[i].Value = strings.Join(f, " ")
		}
	}
	return lines
}

// stripICE removes attributes that describe someone else's addresses or that BitVoice does not relay.
func stripICE(s *sdp.Session) {
	drop := func(lines []sdp.Line) []sdp.Line {
		out := lines[:0]
		for _, l := range lines {
			if l.Type == 'a' {
				name := l.Value
				if i := strings.IndexByte(name, ':'); i >= 0 {
					name = name[:i]
				}
				switch name {
				case "ice-ufrag", "ice-pwd", "ice-options", "ice-lite", "candidate", "end-of-candidates",
					"rtcp-mux", "rtcp", "remote-candidates", "fingerprint", "setup":
					continue
				}
			}
			out = append(out, l)
		}
		return out
	}
	s.Lines = drop(s.Lines)
	for _, m := range s.Media {
		m.Lines = drop(m.Lines)
	}
}

func filterFormats(m *sdp.Media, keep []string) {
	want := map[string]bool{}
	for _, k := range keep {
		want[k] = true
	}
	// telephone-event and comfort noise ride along with whatever audio codec remains.
	for _, c := range m.Codecs() {
		n := strings.ToLower(c.Name)
		if n == "telephone-event" || n == "cn" {
			want[strconv.Itoa(c.PT)] = true
		}
	}
	var formats []string
	for _, f := range m.Formats {
		if want[f] {
			formats = append(formats, f)
		}
	}
	m.Formats = formats
	var lines []sdp.Line
	for _, l := range m.Lines {
		if l.Type == 'a' && (strings.HasPrefix(l.Value, "rtpmap:") || strings.HasPrefix(l.Value, "fmtp:")) {
			v := l.Value[strings.IndexByte(l.Value, ':')+1:]
			pt := v
			if i := strings.IndexByte(v, ' '); i >= 0 {
				pt = v[:i]
			}
			if !want[pt] {
				continue
			}
		}
		lines = append(lines, l)
	}
	m.Lines = lines
}

func setDirection(m *sdp.Media, dir string) {
	var lines []sdp.Line
	for _, l := range m.Lines {
		switch l.Value {
		case "sendrecv", "sendonly", "recvonly", "inactive":
			if l.Type == 'a' {
				continue
			}
		}
		lines = append(lines, l)
	}
	m.Lines = append(lines, sdp.Line{Type: 'a', Value: dir})
}

// describeFor rewrites a description received from one party so it can be sent to this party: BitVoice's
// address and this leg's port, nothing of the other party's network.
func (l *Leg) describeFor(from *sdp.Session) *sdp.Session {
	s := from.Clone()
	stripICE(s)
	s.SetConnection(l.e.mediaHost(l.peer))
	var video *sdp.Media
	for _, m := range s.Media {
		switch {
		case m.Type == "audio" && m.Port != 0:
			m.Port = l.ep.Port()
		case m.Type == "video" && m.Port != 0 && video == nil:
			video = m // filled in below, once the sound's encryption is known
		default:
			m.Port = 0
		}
	}
	// BitVoice's own origin line, so each leg sees a consistent session owner.
	for i := range s.Lines {
		if s.Lines[i].Type == 'o' {
			id := strconv.FormatUint(uint64(l.ep.Port())*1000+uint64(len(l.callID)), 10)
			fam := "IP4"
			host := l.e.mediaHost(l.peer)
			if strings.Contains(host, ":") {
				fam = "IP6"
			}
			s.Lines[i].Value = "BitVoice " + id + " " + id + " IN " + fam + " " + host
		}
	}
	// The party that called BitVoice gets an answer to its own offer; anyone else gets an offer.
	var offer *sdp.Session
	if l.inbound {
		l.mu.Lock()
		offer = l.remoteSDP
		l.mu.Unlock()
	}
	_ = l.secure(s, offer)
	if video != nil {
		var answering *sdp.Media
		if offer != nil {
			if answering = videoMedia(offer); answering == nil {
				answering = &sdp.Media{} // the party offered no video: refuse it
			}
		}
		if l.describeVideo(video, answering, s.Audio() != nil && s.Audio().Secure()) && offer == nil && l.e.videoTranscodeOn() {
			offerVideoAlternatives(video) // a party with the other codec can still be shown
		}
	}
	return s
}

// codecName returns the first negotiated audio codec.
func (l *Leg) codecName() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range []*sdp.Session{l.remoteSDP, l.localSDP} {
		if s != nil && s.Audio() != nil {
			for _, c := range s.Audio().Codecs() {
				if n := strings.ToLower(c.Name); n != "telephone-event" && n != "cn" {
					return c.Name
				}
			}
		}
	}
	return ""
}

func (l *Leg) info() LegInfo {
	li := LegInfo{Party: l.party(), Peer: l.peer.Addr.String(), Codec: l.codecName(), Transport: l.peer.Transport, Ext: l.Ext, Trunk: l.TrunkID}
	l.mu.Lock()
	li.OnHold, li.Ringing = l.onHold, l.ringing && !l.answered
	li.Video, li.Fax = l.video != nil && videoMedia(l.remoteSDP) != nil, l.onFax
	ep := l.ep
	l.mu.Unlock()
	if ep != nil {
		li.Media = ep.Stats()
		li.Secure = ep.Secure()
	}
	return li
}

// charge takes the phone company's advice of charge from a trunk (RFC-less but standard: 3GPP TS
// 24.647's XML in an INFO or the BYE), keeping the latest on the call for its record. It reports
// whether the message was one.
func (l *Leg) charge(m *sip.Message) bool {
	if !strings.HasPrefix(strings.ToLower(m.Get("Content-Type")), isdn.ChargeContentType) || l.TrunkID == "" {
		return false
	}
	c, ok := isdn.ParseChargeXML(m.Body)
	if !ok {
		return true
	}
	l.call.mu.Lock()
	if old := l.call.charged; old == nil || !old.Final || c.Final {
		l.call.charged = &c // a figure during the call never replaces the final one
	}
	l.call.mu.Unlock()
	l.e.publish("call.charged", map[string]any{"call": l.call.ID, "trunk": l.TrunkID, "charge": c.String(), "final": c.Final})
	return true
}
