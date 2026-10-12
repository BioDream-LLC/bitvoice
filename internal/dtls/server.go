package dtls

import (
	"bytes"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"sync"
)

// Errors a handshake can end with.
var (
	ErrFingerprint = errors.New("dtls: the peer's certificate does not match the fingerprint it announced")
	ErrHandshake   = errors.New("dtls: handshake failed")
)

const (
	extUseSRTP       = 14
	extEMS           = 23
	extRenegotiation = 0xff01
	extGroups        = 10
	extPointFormats  = 11
	extSigAlgs       = 13

	groupX25519 = 29
	groupP256   = 23

	sigECDSAP256SHA256 = 0x0403
	sigRSAPKCS1SHA256  = 0x0401
	sigRSAPSSSHA256    = 0x0804

	// SRTPAES128SHA80 is the DTLS-SRTP protection profile BitVoice uses (RFC 5764).
	SRTPAES128SHA80 = 0x0001
)

// Server is one DTLS association, BitVoice being the server (SDP a=setup:passive). Feed every received
// DTLS datagram to Handle; send whatever it returns.
type Server struct {
	cert        []byte // DER
	key         *ecdsa.PrivateKey
	fingerprint []byte // SHA-256 of the client's certificate, from its SDP

	mu           sync.Mutex
	reasm        reassembler
	transcript   bytes.Buffer
	nextRecv     uint16
	clientRandom []byte
	serverRandom []byte
	group        uint16
	ecdhKey      *ecdh.PrivateKey
	ems          bool
	master       []byte
	clientCert   *x509.Certificate
	readEpoch1   *gcmState
	writeEpoch1  *gcmState
	writeSeq     [2]uint64
	flight       [][]byte // the last flight sent, for retransmission
	done         bool
	err          error
	sentSeq      uint16
}

// NewServer prepares an association with BitVoice's certificate and the fingerprint (SHA-256 bytes)
// the browser put in its SDP.
func NewServer(certDER []byte, key *ecdsa.PrivateKey, peerFingerprint []byte) *Server {
	return &Server{cert: certDER, key: key, fingerprint: peerFingerprint}
}

// Done reports whether the handshake completed.
func (s *Server) Done() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.done }

// SRTPKeys returns the client's and server's SRTP master keys and salts (RFC 5764 4.2) once Done.
func (s *Server) SRTPKeys() (clientKey, clientSalt, serverKey, serverSalt []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := prf(s.master, "EXTRACTOR-dtls_srtp", append(append([]byte(nil), s.clientRandom...), s.serverRandom...), 60)
	return m[0:16], m[32:46], m[16:32], m[46:60]
}

// Handle takes one datagram from the browser and returns datagrams to send back.
func (s *Server) Handle(b []byte) ([][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	recs, err := parseRecords(b)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for _, r := range recs {
		if r.epoch == 1 {
			if s.readEpoch1 == nil {
				continue // a record for keys we do not have yet: it will be retransmitted
			}
			plain, err := s.readEpoch1.open(r)
			if err != nil {
				continue // forged or damaged: drop it (RFC 6347 4.1.2.7)
			}
			r.body = plain
		} else if r.epoch != 0 {
			continue
		}
		switch r.typ {
		case ctChangeCipherSpec:
			continue // keys switch with the epoch number
		case ctAlert:
			if len(r.body) >= 2 && r.body[0] == 2 {
				s.err = fmt.Errorf("dtls: the browser sent alert %d", r.body[1])
				return out, s.err
			}
			continue
		case ctHandshake:
		default:
			continue
		}
		msgs, err := s.reasm.add(r.body)
		if err != nil {
			return out, err
		}
		for _, m := range msgs {
			if m.seq < s.nextRecv {
				// A retransmission: our reply was lost. Send the last flight again.
				out = append(out, s.flight...)
				continue
			}
			if m.seq > s.nextRecv {
				continue // out of order; RFC 6347 allows buffering, dropping makes the client resend
			}
			s.nextRecv++
			reply, err := s.step(m)
			if err != nil {
				s.err = err
				return out, err
			}
			if reply != nil {
				s.flight = reply
				out = append(out, reply...)
			}
		}
	}
	return out, nil
}

func (s *Server) step(m handshakeMsg) ([][]byte, error) {
	switch m.typ {
	case hsClientHello:
		if s.clientRandom != nil {
			return nil, ErrHandshake
		}
		s.transcript.Write(m.marshal())
		return s.serverHello(m.body)
	case hsCertificate:
		s.transcript.Write(m.marshal())
		return nil, s.clientCertificate(m.body)
	case hsClientKeyExchange:
		s.transcript.Write(m.marshal())
		return nil, s.keyExchange(m.body)
	case hsCertificateVerify:
		err := s.certificateVerify(m.body)
		s.transcript.Write(m.marshal())
		return nil, err
	case hsFinished:
		if s.readEpoch1 == nil || s.clientCert == nil {
			return nil, ErrHandshake
		}
		want := prf(s.master, "client finished", s.hash(), 12)
		if subtle.ConstantTimeCompare(want, m.body) != 1 {
			return nil, ErrHandshake
		}
		s.transcript.Write(m.marshal())
		fin := handshakeMsg{typ: hsFinished, seq: s.sentSeq, body: prf(s.master, "server finished", s.hash(), 12)}
		s.sentSeq++
		ccs := writeRecordHeader(nil, ctChangeCipherSpec, 0, s.writeSeq[0], 1)
		ccs = append(ccs, 1)
		s.writeSeq[0]++
		rec := s.writeEpoch1.seal(ctHandshake, 1, s.writeSeq[1], fin.marshal())
		s.writeSeq[1]++
		s.done = true
		return [][]byte{append(ccs, rec...)}, nil
	}
	return nil, fmt.Errorf("dtls: unexpected handshake message %d", m.typ)
}

func (s *Server) hash() []byte {
	h := sha256.Sum256(s.transcript.Bytes())
	return h[:]
}

// reader reads TLS vectors.
type reader struct {
	b   []byte
	bad bool
}

func (r *reader) n(k int) []byte {
	if r.bad || len(r.b) < k {
		r.bad = true
		return nil
	}
	v := r.b[:k]
	r.b = r.b[k:]
	return v
}
func (r *reader) u8() int {
	v := r.n(1)
	if v == nil {
		return 0
	}
	return int(v[0])
}
func (r *reader) u16() int {
	v := r.n(2)
	if v == nil {
		return 0
	}
	return int(binary.BigEndian.Uint16(v))
}
func (r *reader) u24() int {
	v := r.n(3)
	if v == nil {
		return 0
	}
	return u24(v)
}

func (s *Server) serverHello(body []byte) ([][]byte, error) {
	r := &reader{b: body}
	if v := r.u16(); v != versionDTLS12 && v != versionDTLS10 {
		return nil, fmt.Errorf("dtls: unsupported version %x", v)
	}
	s.clientRandom = append([]byte(nil), r.n(32)...)
	r.n(r.u8()) // session id
	r.n(r.u8()) // cookie
	suites := r.n(r.u16())
	r.n(r.u8()) // compression
	haveSuite := false
	for i := 0; i+1 < len(suites); i += 2 {
		haveSuite = haveSuite || binary.BigEndian.Uint16(suites[i:]) == suiteECDHE_ECDSA_AES128_GCM_SHA256
	}
	if r.bad || !haveSuite {
		return nil, errors.New("dtls: the browser offered no cipher suite BitVoice supports")
	}
	srtpOK := false
	var srtpMKI []byte
	if len(r.b) >= 2 {
		exts := &reader{b: r.n(r.u16())}
		for len(exts.b) >= 4 && !exts.bad {
			typ := exts.u16()
			data := &reader{b: exts.n(exts.u16())}
			switch typ {
			case extUseSRTP:
				profiles := data.n(data.u16())
				for i := 0; i+1 < len(profiles); i += 2 {
					srtpOK = srtpOK || binary.BigEndian.Uint16(profiles[i:]) == SRTPAES128SHA80
				}
				srtpMKI = data.n(data.u8())
			case extEMS:
				s.ems = true
			case extGroups:
				groups := data.n(data.u16())
				for i := 0; i+1 < len(groups); i += 2 {
					switch binary.BigEndian.Uint16(groups[i:]) {
					case groupX25519:
						s.group = groupX25519 // preferred
					case groupP256:
						if s.group == 0 {
							s.group = groupP256
						}
					}
				}
			}
		}
	}
	if !srtpOK {
		return nil, errors.New("dtls: the browser did not offer SRTP_AES128_CM_HMAC_SHA1_80")
	}
	if s.group == 0 {
		s.group = groupP256
	}
	curve := ecdh.Curve(ecdh.P256())
	if s.group == groupX25519 {
		curve = ecdh.X25519()
	}
	var err error
	if s.ecdhKey, err = curve.GenerateKey(rand.Reader); err != nil {
		return nil, err
	}
	s.serverRandom = make([]byte, 32)
	if _, err := rand.Read(s.serverRandom); err != nil {
		return nil, err
	}

	// ServerHello with use_srtp, extended_master_secret and renegotiation_info.
	var ext []byte
	ext = binary.BigEndian.AppendUint16(ext, extUseSRTP)
	ext = binary.BigEndian.AppendUint16(ext, uint16(5+len(srtpMKI)))
	ext = binary.BigEndian.AppendUint16(ext, 2)
	ext = binary.BigEndian.AppendUint16(ext, SRTPAES128SHA80)
	ext = append(ext, byte(len(srtpMKI)))
	ext = append(ext, srtpMKI...)
	if s.ems {
		ext = binary.BigEndian.AppendUint16(ext, extEMS)
		ext = binary.BigEndian.AppendUint16(ext, 0)
	}
	ext = append(ext, 0xff, 0x01, 0, 1, 0)
	ext = append(ext, 0, extPointFormats, 0, 2, 1, 0)
	var sh []byte
	sh = binary.BigEndian.AppendUint16(sh, versionDTLS12)
	sh = append(sh, s.serverRandom...)
	sh = append(sh, 0) // no session id: no resumption
	sh = binary.BigEndian.AppendUint16(sh, suiteECDHE_ECDSA_AES128_GCM_SHA256)
	sh = append(sh, 0)
	sh = binary.BigEndian.AppendUint16(sh, uint16(len(ext)))
	sh = append(sh, ext...)

	var cert []byte
	cert = append(cert, byte((len(s.cert)+3)>>16), byte((len(s.cert)+3)>>8), byte(len(s.cert)+3))
	cert = append(cert, byte(len(s.cert)>>16), byte(len(s.cert)>>8), byte(len(s.cert)))
	cert = append(cert, s.cert...)

	pub := s.ecdhKey.PublicKey().Bytes()
	params := []byte{3, byte(s.group >> 8), byte(s.group), byte(len(pub))}
	params = append(params, pub...)
	signed := sha256.Sum256(append(append(append([]byte(nil), s.clientRandom...), s.serverRandom...), params...))
	sig, err := ecdsa.SignASN1(rand.Reader, s.key, signed[:])
	if err != nil {
		return nil, err
	}
	ske := append([]byte(nil), params...)
	ske = binary.BigEndian.AppendUint16(ske, sigECDSAP256SHA256)
	ske = binary.BigEndian.AppendUint16(ske, uint16(len(sig)))
	ske = append(ske, sig...)

	// CertificateRequest: the browser's certificate is how its fingerprint is checked.
	cr := []byte{2, 64, 1} // ecdsa_sign, rsa_sign
	cr = binary.BigEndian.AppendUint16(cr, 6)
	cr = binary.BigEndian.AppendUint16(cr, sigECDSAP256SHA256)
	cr = binary.BigEndian.AppendUint16(cr, sigRSAPKCS1SHA256)
	cr = binary.BigEndian.AppendUint16(cr, sigRSAPSSSHA256)
	cr = binary.BigEndian.AppendUint16(cr, 0) // no CA names

	var flight []byte
	for _, m := range []handshakeMsg{{hsServerHello, 0, sh}, {hsCertificate, 1, cert}, {hsServerKeyExchange, 2, ske},
		{hsCertificateRequest, 3, cr}, {hsServerHelloDone, 4, nil}} {
		raw := m.marshal()
		s.transcript.Write(raw)
		flight = writeRecordHeader(flight, ctHandshake, 0, s.writeSeq[0], len(raw))
		flight = append(flight, raw...)
		s.writeSeq[0]++
	}
	s.sentSeq = 5
	return [][]byte{flight}, nil
}

func (s *Server) clientCertificate(body []byte) error {
	r := &reader{b: body}
	all := &reader{b: r.n(r.u24())}
	first := all.n(all.u24())
	if r.bad || all.bad || len(first) == 0 {
		return errors.New("dtls: the browser sent no certificate")
	}
	sum := sha256.Sum256(first)
	if subtle.ConstantTimeCompare(sum[:], s.fingerprint) != 1 {
		return ErrFingerprint
	}
	c, err := x509.ParseCertificate(first)
	if err != nil {
		return fmt.Errorf("dtls: the browser's certificate: %w", err)
	}
	s.clientCert = c
	return nil
}

func (s *Server) keyExchange(body []byte) error {
	r := &reader{b: body}
	pub := r.n(r.u8())
	if r.bad {
		return errShort
	}
	peer, err := s.ecdhKey.Curve().NewPublicKey(pub)
	if err != nil {
		return err
	}
	pms, err := s.ecdhKey.ECDH(peer)
	if err != nil {
		return err
	}
	if s.ems {
		s.master = prf(pms, "extended master secret", s.hash(), 48)
	} else {
		s.master = prf(pms, "master secret", append(append([]byte(nil), s.clientRandom...), s.serverRandom...), 48)
	}
	kb := prf(s.master, "key expansion", append(append([]byte(nil), s.serverRandom...), s.clientRandom...), 40)
	if s.readEpoch1, err = newGCM(kb[0:16], kb[32:36]); err != nil {
		return err
	}
	s.writeEpoch1, err = newGCM(kb[16:32], kb[36:40])
	return err
}

func (s *Server) certificateVerify(body []byte) error {
	if s.clientCert == nil {
		return ErrHandshake
	}
	r := &reader{b: body}
	alg := uint16(r.u16())
	sig := r.n(r.u16())
	if r.bad {
		return errShort
	}
	var h hash.Hash = sha256.New()
	h.Write(s.transcript.Bytes())
	digest := h.Sum(nil)
	switch pk := s.clientCert.PublicKey.(type) {
	case *ecdsa.PublicKey:
		if alg != sigECDSAP256SHA256 || !ecdsa.VerifyASN1(pk, digest, sig) {
			return errors.New("dtls: the browser's signature does not verify")
		}
	case *rsa.PublicKey:
		var err error
		switch alg {
		case sigRSAPKCS1SHA256:
			err = rsa.VerifyPKCS1v15(pk, crypto.SHA256, digest, sig)
		case sigRSAPSSSHA256:
			err = rsa.VerifyPSS(pk, crypto.SHA256, digest, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		default:
			err = errors.New("unsupported signature algorithm")
		}
		if err != nil {
			return fmt.Errorf("dtls: the browser's signature does not verify: %w", err)
		}
	default:
		return errors.New("dtls: unsupported certificate key")
	}
	return nil
}

// Retransmit returns the last flight, for a timer that fires when the browser has not answered.
func (s *Server) Retransmit() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return nil
	}
	return s.flight
}
