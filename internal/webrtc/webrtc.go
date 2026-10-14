// Package webrtc terminates a browser's WebRTC audio on a BitVoice media endpoint: ICE-lite (answering
// the browser's connectivity checks), DTLS-SRTP (BitVoice is the DTLS server) and an SDP answer with G.711,
// so the call engine sees an ordinary RTP leg.
package webrtc

import (
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
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/dtls"
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
		connected: make(chan struct{}), closed: make(chan struct{})}
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
	ans, err := s.describe(host, mid, "passive", dir, nil)
	if err != nil {
		return nil, nil, err
	}
	ep.SetPayloadType(s.PT)
	if s.OpusPT >= 0 {
		ep.MapIncoming(s.OpusPT, media.PTOpus)
		ep.MapPayloadType(media.PTOpus, s.OpusPT)
	}
	ep.OnOther(s.receive)
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
	w("a=ice-lite")
	w("m=audio %d UDP/TLS/RTP/SAVPF %s", ep.Port(), strings.Join(formats, " "))
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
	w("a=candidate:1 1 udp 2130706431 %s %d typ host", host, ep.Port())
	w("a=end-of-candidates")
	return sdp.Parse([]byte(b.String()))
}

// Offer makes a session that offers WebRTC audio to a browser (a SIP-over-WebSocket softphone):
// Opus, G.711 and key presses, BitVoice as ICE-lite and DTLS server. The browser's answer goes to Accept.
func Offer(ep *media.Endpoint, host string, pts []int) (*Session, *sdp.Session, error) {
	if err := loadIdentity(); err != nil {
		return nil, nil, err
	}
	if len(pts) == 0 {
		return nil, nil, ErrNoAudio
	}
	s := &Session{ep: ep, ufrag: randomToken(8), pwd: randomToken(24), PT: -1, EventPT: -1, OpusPT: -1,
		connected: make(chan struct{}), closed: make(chan struct{})}
	s.mid = "0"
	off, err := s.describe(host, "0", "actpass", "sendrecv", pts)
	if err != nil {
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
	s.mu.Unlock()
	s.ep.SetPayloadType(pt)
	if opus >= 0 {
		s.ep.MapIncoming(opus, media.PTOpus)
		s.ep.MapPayloadType(media.PTOpus, opus)
	}
	go s.retransmit()
	return nil
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
	if stun.IsSTUN(pkt) {
		m, err := stun.Parse(pkt)
		if err != nil || m.Type != stun.BindingRequest || !strings.HasPrefix(m.Username(), s.ufrag+":") || m.Check(s.pwd) != nil {
			return
		}
		s.ep.WriteTo(stun.BindingResponse(m, from, s.pwd), from)
		s.mu.Lock()
		first := !s.remote.IsValid()
		if first || m.UseCandidate() {
			s.remote = from
		}
		s.mu.Unlock()
		if first || m.UseCandidate() {
			s.ep.Latch(from)
		}
		return
	}
	s.mu.Lock()
	ok := s.remote.IsValid() && from == s.remote
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
