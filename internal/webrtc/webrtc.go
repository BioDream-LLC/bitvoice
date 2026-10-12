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
	wirePT := s.PT
	if s.OpusPT >= 0 {
		wirePT = s.OpusPT
	}
	formats := []string{fmt.Sprint(wirePT)}
	if s.EventPT >= 0 {
		formats = append(formats, fmt.Sprint(s.EventPT))
	}
	mid := attr(offer, a, "mid")
	if mid == "" {
		mid = "0"
	}
	fam := "IP4"
	if strings.Contains(host, ":") {
		fam = "IP6"
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
	w("a=setup:passive")
	w("a=rtcp-mux")
	switch offer.Direction(a) {
	case "sendonly":
		w("a=recvonly")
	case "recvonly":
		w("a=sendonly")
	default:
		w("a=sendrecv")
	}
	switch s.PT {
	case media.PTOpus:
		w("a=rtpmap:%d opus/48000/2", wirePT)
		w("a=fmtp:%d minptime=10;useinbandfec=1", wirePT)
	case 0:
		w("a=rtpmap:0 PCMU/8000")
	default:
		w("a=rtpmap:8 PCMA/8000")
	}
	if s.EventPT >= 0 {
		w("a=rtpmap:%d telephone-event/%d", s.EventPT, s.eventClock)
		w("a=fmtp:%d 0-15", s.EventPT)
	}
	w("a=candidate:1 1 udp 2130706431 %s %d typ host", host, ep.Port())
	w("a=end-of-candidates")
	ans, err := sdp.Parse([]byte(b.String()))
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
	s.mu.Unlock()
	if !ok {
		return // DTLS only from the address ICE checked
	}
	out, err := s.dtls.Handle(pkt)
	for _, d := range out {
		s.ep.WriteTo(d, from)
	}
	if err != nil || !s.dtls.Done() {
		return
	}
	s.once.Do(func() {
		ck, cs, sk, ss := s.dtls.SRTPKeys()
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
		to := s.remote
		s.mu.Unlock()
		if !to.IsValid() {
			continue
		}
		for _, d := range s.dtls.Retransmit() {
			s.ep.WriteTo(d, to)
		}
	}
}
