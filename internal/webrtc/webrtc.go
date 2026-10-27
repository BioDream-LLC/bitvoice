// Package webrtc terminates a browser's WebRTC audio on a BitVoice media endpoint: ICE-lite (answering
// the browser's connectivity checks), DTLS-SRTP (BitVoice is the DTLS server) and an SDP answer with G.711,
// so the call engine sees an ordinary RTP leg.
package webrtc

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/dtls"
	"github.com/biodream-llc/bitvoice/internal/ice"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/srtp"
	"github.com/biodream-llc/bitvoice/internal/stun"
)

// identity is BitVoice's DTLS certificate. WebRTC certificates are self-signed and trusted by fingerprint,
// so one is made at start and kept in memory.
var identity struct {
	once        sync.Once
	der         []byte
	key         *ecdsa.PrivateKey
	fingerprint string
	err         error
}

func loadIdentity() error {
	identity.once.Do(func() {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			identity.err = err
			return
		}
		serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
		tmpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "BitVoice WebRTC"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0)}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			identity.err = err
			return
		}
		sum := sha256.Sum256(der)
		identity.der, identity.key, identity.fingerprint = der, key, colonHex(sum[:])
	})
	return identity.err
}

func colonHex(b []byte) string {
	h := strings.ToUpper(hex.EncodeToString(b))
	var out []string
	for i := 0; i < len(h); i += 2 {
		out = append(out, h[i:i+2])
	}
	return strings.Join(out, ":")
}

// Session is one browser audio stream.
type Session struct {
	ep        *media.Endpoint
	ufrag     string
	pwd       string
	dtls      *dtls.Server
	mu        sync.Mutex
	remote    netip.AddrPort
	connected chan struct{}
	once      sync.Once
	closed    chan struct{}
	// PT is the codec chosen as BitVoice numbers it inside (media.PTOpus, 0 PCMU, 8 PCMA); EventPT the
	// browser's telephone-event type. OpusPT is the browser's own number for Opus (-1 without it).
	PT, EventPT, OpusPT int
	eventClock          int
	mid                 string
	// agent is the full ICE agent (nil: ICE-lite); cands its local candidates, pair the chosen pair.
	agent *ice.Agent
	cands []ice.Candidate
	pair  ice.Pair
	// simulcast is what a video answer agreed for a sending browser's layers.
	simulcast Simulcast
	// peerFP is the far end's DTLS certificate fingerprint and remoteUfrag its ICE user name: an offer
	// with another user name is an ICE restart (RFC 8445 9), which must keep the certificate.
	peerFP      []byte
	remoteUfrag string
	// After a restart: the credentials before it, still answered until oldUntil (ICE-lite), and
	// relatch, which moves the media to the first address checked with the new ones.
	oldUfrag, oldPwd string
	oldUntil         time.Time
	relatch          bool
	// restarted: ICE has restarted at least once, so a new path is taken only once media comes on it.
	restarted bool
}

// ICEOptions say how BitVoice takes part in ICE. The zero value is ICE-lite: BitVoice only answers
// the browser's checks at its one address, which suits a server with a public address. Full makes it
// a full agent (RFC 8445) for a server behind NAT: host candidates at Hosts, server-reflexive ones
// from STUN, relayed ones from TURN.
type ICEOptions struct {
	Full      bool
	Hosts     []netip.Addr
	STUN      []netip.AddrPort
	TURN      []ice.TURNServer
	RelayOnly bool
	// Peer is the browser's own address as the web server saw it: the relays let it through from the
	// start, for a browser whose candidates are hidden mDNS names.
	Peer netip.Addr
}

// startICE gathers this side's candidates and gets the agent ready (the endpoint starts reading, so
// STUN and TURN answers arrive).
func (s *Session) startICE(o ICEOptions, host string, controlling bool) {
	hosts := o.Hosts
	if len(hosts) == 0 {
		if ip, err := netip.ParseAddr(host); err == nil {
			hosts = []netip.Addr{ip}
		}
	}
	s.agent = ice.New(ice.Config{Hosts: hosts, Port: s.ep.Port(), STUN: o.STUN, TURN: o.TURN, RelayOnly: o.RelayOnly,
		Controlling: controlling, LocalUfrag: s.ufrag, LocalPwd: s.pwd, Send: s.ep.WriteTo})
	s.ep.OnOther(s.receive)
	s.ep.Start()
	s.cands = s.agent.Gather()
	if o.Peer.IsValid() {
		s.agent.Permit(o.Peer)
		if o.Peer.Unmap().IsLoopback() {
			// The browser runs on this computer: it may send to the relay from any of this
			// computer's own addresses (the one facing the relay), so all of them may come in.
			for _, a := range ownAddrs() {
				s.agent.Permit(a)
			}
		}
	}
	go s.follow()
}

// remoteICE gives the agent the far end's credentials and candidates, and starts the checks.
func (s *Session) remoteICE(sd *sdp.Session, m *sdp.Media) {
	if s.agent == nil {
		return
	}
	var cands []ice.Candidate
	for _, v := range m.Attrs("candidate") {
		c, err := ice.ParseCandidate(v)
		switch {
		case err != nil:
		case c.MDNS != "":
			// A browser's host address behind an mDNS name: resolved on this network, then added.
			go func(c ice.Candidate) {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				if ip, err := ice.ResolveMDNS(ctx, c.MDNS); err == nil {
					c.Addr, c.MDNS = netip.AddrPortFrom(ip, c.Addr.Port()), ""
					s.agent.AddRemote(c)
				}
			}(c)
		default:
			cands = append(cands, c)
		}
	}
	s.agent.SetRemote(attr(sd, m, "ice-ufrag"), attr(sd, m, "ice-pwd"), cands)
	s.agent.Start()
}

// follow puts the chosen pair on the media path: the far end's address, and the TURN channel when
// the pair is relayed.
func (s *Session) follow() {
	for {
		select {
		case <-s.closed:
			s.agent.Close()
			return
		case p := <-s.agent.Selected():
			s.mu.Lock()
			direct := s.restarted && p.Channel == 0 && s.pair.Channel == 0 && s.pair.Remote.IsValid()
			s.pair, s.remote = p, p.Remote
			s.mu.Unlock()
			if direct {
				// After a restart the old path may still carry the call: move once media comes on the new.
				s.ep.Expect(p.Remote)
				continue
			}
			s.ep.SetRelay(p.Relay, p.Channel, p.Remote)
			s.ep.Latch(p.Remote)
		}
	}
}

// ICE reports how the session's media flows: "lite", or the chosen pair's local candidate type
// (host, srflx, prflx, relay) once a full agent has chosen one ("checking" until then).
func (s *Session) ICE() string {
	if s.agent == nil {
		return "lite"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pair.Remote.IsValid() {
		return "checking"
	}
	return string(s.pair.Local.Type)
}

// Errors when a browser offer cannot be answered.
var (
	ErrNoAudio = errors.New("webrtc: the offer has no audio BitVoice can take (G.711 over UDP/TLS/RTP/SAVPF)")
	ErrNoKeys  = errors.New("webrtc: the offer has no ICE credentials or DTLS fingerprint")
)

func randomToken(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// attr finds an attribute in the media section, then at session level.
func attr(s *sdp.Session, m *sdp.Media, name string) string {
	if v, ok := m.Attr(name); ok {
		return v
	}
	for _, l := range s.Lines {
		if l.Type == 'a' && strings.HasPrefix(l.Value, name+":") {
			return l.Value[len(name)+1:]
		}
	}
	return ""
}

// Answer makes a session for a browser's offer on ep and returns the SDP answer. host is the address the
// browser reaches BitVoice on.
func Answer(ep *media.Endpoint, offer *sdp.Session, host string) (*Session, *sdp.Session, error) {
	return AnswerWith(ep, offer, host, ICEOptions{})
}

// AnswerWith is Answer with BitVoice's part in ICE chosen.
func AnswerWith(ep *media.Endpoint, offer *sdp.Session, host string, o ICEOptions) (*Session, *sdp.Session, error) {
	if err := loadIdentity(); err != nil {
		return nil, nil, err
	}
	a := offer.Audio()
	if a == nil || a.Port == 0 || !strings.Contains(strings.ToUpper(a.Proto), "SAVPF") {
		return nil, nil, ErrNoAudio
	}
	fp := attr(offer, a, "fingerprint")
	alg, val, _ := strings.Cut(fp, " ")
	peerFP, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(val), ":", ""))
	if !strings.EqualFold(alg, "sha-256") || err != nil || len(peerFP) != 32 || attr(offer, a, "ice-ufrag") == "" {
		return nil, nil, ErrNoKeys
	}
	s := &Session{ep: ep, ufrag: randomToken(8), pwd: randomToken(24), PT: -1, EventPT: -1, OpusPT: -1,
		connected: make(chan struct{}), closed: make(chan struct{}), peerFP: peerFP, remoteUfrag: attr(offer, a, "ice-ufrag")}
	s.dtls = dtls.NewServer(identity.der, identity.key, peerFP)
	events := map[int]int{}
	for _, c := range a.Codecs() {
		switch {
		case s.PT < 0 && strings.EqualFold(c.Name, "opus") && c.ClockRate == 48000:
			// Opus: HD voice, and it rides out the lost packets of Wi-Fi and mobile networks.
			s.PT, s.OpusPT = media.PTOpus, c.PT
		case s.PT < 0 && strings.EqualFold(c.Name, "PCMU"):
			s.PT = 0
		case s.PT < 0 && strings.EqualFold(c.Name, "PCMA"):
			s.PT = 8
		case strings.EqualFold(c.Name, "telephone-event") && (c.ClockRate == 8000 || c.ClockRate == 48000):
			events[c.ClockRate] = c.PT
		}
	}
	// Key presses at the audio codec's clock: Chrome sends none with Opus unless 48000 is agreed.
	if pt, ok := events[48000]; ok && s.OpusPT >= 0 {
		s.EventPT, s.eventClock = pt, 48000
	} else if pt, ok := events[8000]; ok {
		s.EventPT, s.eventClock = pt, 8000
	}
	if s.PT < 0 {
		return nil, nil, ErrNoAudio
	}
	mid := attr(offer, a, "mid")
	if mid == "" {
		mid = "0"
	}
	dir := "sendrecv"
	switch offer.Direction(a) {
	case "sendonly":
		dir = "recvonly"
	case "recvonly":
		dir = "sendonly"
	}
	s.mid = mid
	if o.Full {
		// The answerer of a full agent is controlled; a full agent facing an ICE-lite one controls.
		_, lite := offer.SessionAttr("ice-lite")
		s.startICE(o, host, lite)
	}
	ans, err := s.describe(host, mid, "passive", dir, nil)
	if err != nil {
		s.Close()
		return nil, nil, err
	}
	ep.SetPayloadType(s.PT)
	if s.OpusPT >= 0 {
		ep.MapIncoming(s.OpusPT, media.PTOpus)
		ep.MapPayloadType(media.PTOpus, s.OpusPT)
	}
	ep.OnOther(s.receive)
	s.remoteICE(offer, a)
	go s.retransmit()
	return s, ans, nil
}

// describe writes BitVoice's side: offering every codec it takes (offer) or the one chosen.
func (s *Session) describe(host, mid, setup, dir string, offer []int) (*sdp.Session, error) {
	ep := s.ep
	fam := "IP4"
	if strings.Contains(host, ":") {
		fam = "IP6"
	}
	var formats []string
	var maps []string
	if offer != nil {
		for _, pt := range offer {
			switch pt {
			case media.PTOpus:
				formats = append(formats, "111")
				maps = append(maps, "a=rtpmap:111 opus/48000/2", "a=fmtp:111 minptime=10;useinbandfec=1")
			case 0:
				formats = append(formats, "0")
				maps = append(maps, "a=rtpmap:0 PCMU/8000")
			case 8:
				formats = append(formats, "8")
				maps = append(maps, "a=rtpmap:8 PCMA/8000")
			}
		}
		formats = append(formats, "101")
		maps = append(maps, "a=rtpmap:101 telephone-event/8000", "a=fmtp:101 0-15")
	} else {
		wirePT := s.PT
		if s.OpusPT >= 0 {
			wirePT = s.OpusPT
		}
		formats = []string{fmt.Sprint(wirePT)}
		switch s.PT {
		case media.PTOpus:
			maps = append(maps, fmt.Sprintf("a=rtpmap:%d opus/48000/2", wirePT), fmt.Sprintf("a=fmtp:%d minptime=10;useinbandfec=1", wirePT))
		case 0:
			maps = append(maps, "a=rtpmap:0 PCMU/8000")
		default:
			maps = append(maps, "a=rtpmap:8 PCMA/8000")
		}
		if s.EventPT >= 0 {
			formats = append(formats, fmt.Sprint(s.EventPT))
			maps = append(maps, fmt.Sprintf("a=rtpmap:%d telephone-event/%d", s.EventPT, s.eventClock), fmt.Sprintf("a=fmtp:%d 0-15", s.EventPT))
		}
	}
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\r\n", args...) }
	w("v=0")
	w("o=BitVoice %d 1 IN %s %s", time.Now().UnixNano()/1000, fam, host)
	w("s=-")
	w("t=0 0")
	w("a=group:BUNDLE %s", mid)
	port := ep.Port()
	if s.agent == nil {
		w("a=ice-lite")
	} else if d, ok := defaultCandidate(s.cands); ok {
		// The m= and c= lines carry the candidate most likely to work (RFC 8839 4.2.1.2).
		host, port = d.Addr.Addr().String(), int(d.Addr.Port())
		if d.Addr.Addr().Is4() {
			fam = "IP4"
		} else {
			fam = "IP6"
		}
	}
	w("m=audio %d UDP/TLS/RTP/SAVPF %s", port, strings.Join(formats, " "))
	w("c=IN %s %s", fam, host)
	w("a=mid:%s", mid)
	w("a=ice-ufrag:%s", s.ufrag)
	w("a=ice-pwd:%s", s.pwd)
	w("a=fingerprint:sha-256 %s", identity.fingerprint)
	w("a=setup:%s", setup)
	w("a=rtcp-mux")
	w("a=%s", dir)
	for _, m := range maps {
		w("%s", m)
	}
	if s.agent == nil {
		w("a=candidate:1 1 udp 2130706431 %s %d typ host", host, ep.Port())
	} else {
		for _, c := range s.cands {
			w("a=candidate:%s", c.SDP())
		}
	}
	w("a=end-of-candidates")
	return sdp.Parse([]byte(b.String()))
}

// defaultCandidate is a relayed candidate if there is one, else server-reflexive, else host.
func defaultCandidate(cands []ice.Candidate) (ice.Candidate, bool) {
	for _, t := range []ice.Type{ice.Relayed, ice.ServerReflexive, ice.Host} {
		for _, c := range cands {
			if c.Type == t {
				return c, true
			}
		}
	}
	return ice.Candidate{}, false
}

// Offer makes a session that offers WebRTC audio to a browser (a SIP-over-WebSocket softphone):
// Opus, G.711 and key presses, BitVoice as ICE-lite and DTLS server. The browser's answer goes to Accept.
func Offer(ep *media.Endpoint, host string, pts []int) (*Session, *sdp.Session, error) {
	return OfferWith(ep, host, pts, ICEOptions{})
}

// OfferWith is Offer with BitVoice's part in ICE chosen (the offering full agent controls).
func OfferWith(ep *media.Endpoint, host string, pts []int, o ICEOptions) (*Session, *sdp.Session, error) {
	if err := loadIdentity(); err != nil {
		return nil, nil, err
	}
	if len(pts) == 0 {
		return nil, nil, ErrNoAudio
	}
	s := &Session{ep: ep, ufrag: randomToken(8), pwd: randomToken(24), PT: -1, EventPT: -1, OpusPT: -1,
		connected: make(chan struct{}), closed: make(chan struct{})}
	s.mid = "0"
	if o.Full {
		s.startICE(o, host, true)
	}
	off, err := s.describe(host, "0", "actpass", "sendrecv", pts)
	if err != nil {
		s.Close()
		return nil, nil, err
	}
	ep.OnOther(s.receive)
	return s, off, nil
}

// Describe writes BitVoice's answer again with another direction (for a re-INVITE): same keys, same codec.
func (s *Session) Describe(host, dir string) (*sdp.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.describe(host, s.mid, "passive", dir, nil)
}

// ErrDTLSRole is returned when the browser's answer wants BitVoice to open the DTLS connection.
var ErrDTLSRole = errors.New("webrtc: the answer asks BitVoice to be the DTLS client, which it is not")

// Accept takes the browser's answer to Offer: its fingerprint, the codec it chose, its key-press type.
func (s *Session) Accept(answer *sdp.Session) error {
	a := answer.Audio()
	if a == nil || a.Port == 0 {
		return ErrNoAudio
	}
	if setup := attr(answer, a, "setup"); setup == "passive" {
		return ErrDTLSRole
	}
	fp := attr(answer, a, "fingerprint")
	alg, val, _ := strings.Cut(fp, " ")
	peerFP, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(val), ":", ""))
	if !strings.EqualFold(alg, "sha-256") || err != nil || len(peerFP) != 32 {
		return ErrNoKeys
	}
	pt, opus, ev := -1, -1, -1
	for _, c := range a.Codecs() {
		switch {
		case pt < 0 && strings.EqualFold(c.Name, "opus") && c.ClockRate == 48000:
			pt, opus = media.PTOpus, c.PT
		case pt < 0 && strings.EqualFold(c.Name, "PCMU"):
			pt = 0
		case pt < 0 && strings.EqualFold(c.Name, "PCMA"):
			pt = 8
		case ev < 0 && strings.EqualFold(c.Name, "telephone-event") && c.ClockRate == 8000:
			ev = c.PT
		}
	}
	if pt < 0 {
		return ErrNoAudio
	}
	s.mu.Lock()
	s.PT, s.OpusPT, s.EventPT, s.eventClock = pt, opus, ev, 8000
	s.dtls = dtls.NewServer(identity.der, identity.key, peerFP)
	s.peerFP, s.remoteUfrag = peerFP, attr(answer, a, "ice-ufrag")
	s.mu.Unlock()
	s.ep.SetPayloadType(pt)
	if opus >= 0 {
		s.ep.MapIncoming(opus, media.PTOpus)
		s.ep.MapPayloadType(media.PTOpus, opus)
	}
	s.remoteICE(answer, a) // the offering full agent stays controlling (RFC 8445 6.1.1)
	go s.retransmit()
	return nil
}

// Errors for an ICE restart BitVoice cannot take.
var (
	ErrRestartKeys = errors.New("webrtc: an ICE restart must keep the DTLS certificate and bring new ICE credentials")
)

// Restarting says whether a new offer from the far end is an ICE restart: new ICE credentials for
// the same session (a phone that moved from Wi-Fi to a mobile network, say).
func (s *Session) Restarting(offer *sdp.Session) bool {
	a := offer.Audio()
	if a == nil {
		return false
	}
	u := attr(offer, a, "ice-ufrag")
	s.mu.Lock()
	defer s.mu.Unlock()
	return u != "" && s.remoteUfrag != "" && u != s.remoteUfrag
}

// Restart answers the far end's ICE restart offer: new credentials on this side, checks done again
// to the far end's new candidates, and the media moved to the new path once it is found. DTLS and
// SRTP carry on (the same keys); the old path carries the call until then.
func (s *Session) Restart(offer *sdp.Session, host string) (*sdp.Session, error) {
	a := offer.Audio()
	if a == nil || a.Port == 0 {
		return nil, ErrNoAudio
	}
	_, val, _ := strings.Cut(attr(offer, a, "fingerprint"), " ")
	fp, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(val), ":", ""))
	u, p := attr(offer, a, "ice-ufrag"), attr(offer, a, "ice-pwd")
	s.mu.Lock()
	if err != nil || !bytes.Equal(fp, s.peerFP) || u == "" || p == "" || u == s.remoteUfrag {
		s.mu.Unlock()
		return nil, ErrRestartKeys
	}
	s.oldUfrag, s.oldPwd, s.oldUntil = s.ufrag, s.pwd, time.Now().Add(ice.OldCredentialsFor)
	s.ufrag, s.pwd, s.remoteUfrag, s.relatch, s.restarted = randomToken(8), randomToken(24), u, true, true
	ufrag, pwd := s.ufrag, s.pwd
	s.mu.Unlock()
	if s.agent != nil {
		s.agent.Restart(ufrag, pwd)
		s.remoteICE(offer, a)
	}
	dir := "sendrecv"
	switch offer.Direction(a) {
	case "sendonly":
		dir = "recvonly"
	case "recvonly":
		dir = "sendonly"
	case "inactive":
		dir = "inactive"
	}
	return s.Describe(host, dir)
}

// Connected is closed once DTLS has finished and SRTP is running.
func (s *Session) Connected() <-chan struct{} { return s.connected }

// Close stops the session's timers (the endpoint is closed by its owner).
func (s *Session) Close() {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
}

func (s *Session) receive(pkt []byte, from netip.AddrPort) {
	if s.agent != nil {
		s.mu.Lock()
		p := s.pair
		s.mu.Unlock()
		if p.Channel != 0 && from == p.Remote && stun.IsSTUN(pkt) {
			// The far end's consent checks, through the relay (the endpoint has unwrapped them).
			s.agent.HandleRelayed(pkt, from, p.Relay)
			return
		}
		consumed, inner, innerFrom := s.agent.Handle(pkt, from)
		if inner != nil {
			s.receive(inner, innerFrom) // relayed by the TURN server before a channel was bound
			return
		}
		if consumed {
			return
		}
		s.dtlsIn(pkt, from)
		return
	}
	if stun.IsSTUN(pkt) {
		m, err := stun.Parse(pkt)
		if err != nil || m.Type != stun.BindingRequest {
			return
		}
		s.mu.Lock()
		u, p, ou, op := s.ufrag, s.pwd, s.oldUfrag, s.oldPwd
		old := ou != "" && time.Now().Before(s.oldUntil)
		s.mu.Unlock()
		if !strings.HasPrefix(m.Username(), u+":") || m.Check(p) != nil {
			if old && strings.HasPrefix(m.Username(), ou+":") && m.Check(op) == nil {
				s.ep.WriteTo(stun.BindingResponse(m, from, op), from) // the old path's consent, until it moves
			}
			return
		}
		s.ep.WriteTo(stun.BindingResponse(m, from, p), from)
		s.mu.Lock()
		move := !s.remote.IsValid() || s.relatch || m.UseCandidate()
		expect := move && s.restarted
		if move {
			s.remote, s.relatch = from, false
		}
		s.mu.Unlock()
		switch {
		case expect:
			s.ep.Expect(from) // after a restart: the old path carries the call until media comes on this one
		case move:
			s.ep.Latch(from)
		}
		return
	}
	s.dtlsIn(pkt, from)
}

// dtlsIn takes DTLS from the far end.
func (s *Session) dtlsIn(pkt []byte, from netip.AddrPort) {
	if len(pkt) == 0 || pkt[0] < 20 || pkt[0] > 63 {
		return
	}
	s.mu.Lock()
	ok := s.remote.IsValid() && from == s.remote
	if !ok && s.agent != nil && !s.remote.IsValid() && s.agent.Valid(from) {
		// A full agent: DTLS may come on a pair that has passed its checks before one is chosen.
		s.remote, ok = from, true
	}
	d := s.dtls
	s.mu.Unlock()

	if !ok || d == nil {
		return // DTLS only from the address ICE checked, and once the answer is known (the browser resends)
	}
	out, err := d.Handle(pkt)
	for _, d := range out {
		s.ep.WriteTo(d, from)
	}
	if err != nil || !d.Done() {
		return
	}
	s.once.Do(func() {
		ck, cs, sk, ss := d.SRTPKeys()
		in, err1 := srtp.New(srtp.AES128SHA80, ck, cs)
		out, err2 := srtp.New(srtp.AES128SHA80, sk, ss)
		if err1 != nil || err2 != nil {
			return
		}
		s.ep.SetSRTP(in, out)
		close(s.connected)
	})
}

// retransmit resends BitVoice's last DTLS flight while the browser has not finished (RFC 6347 4.2.4).
func (s *Session) retransmit() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range 15 {
		select {
		case <-s.closed:
			return
		case <-s.connected:
			return
		case <-t.C:
		}
		s.mu.Lock()
		to, dt := s.remote, s.dtls
		s.mu.Unlock()
		if !to.IsValid() || dt == nil {
			continue
		}
		for _, d := range dt.Retransmit() {
			s.ep.WriteTo(d, to)
		}
	}
}

// ownAddrs lists this computer's own unicast addresses.
func ownAddrs() []netip.Addr {
	var out []netip.Addr
	as, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, a := range as {
		if n, ok := a.(*net.IPNet); ok {
			if ip, ok := netip.AddrFromSlice(n.IP); ok && !ip.IsMulticast() {
				out = append(out, ip.Unmap())
			}
		}
	}
	return out
}
