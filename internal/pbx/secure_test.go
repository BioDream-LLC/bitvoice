package pbx

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/srtp"
)

func rtpPacket(seq uint16, payload []byte) []byte {
	p := make([]byte, 12, 12+len(payload))
	p[0], p[1] = 0x80, 0
	binary.BigEndian.PutUint16(p[2:], seq)
	binary.BigEndian.PutUint32(p[4:], uint32(seq)*160)
	binary.BigEndian.PutUint32(p[8:], 0x1234)
	return append(p, payload...)
}

func (p *phone) readRTP(t *testing.T) []byte {
	t.Helper()
	_ = p.rtp.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := p.rtp.ReadFromUDP(buf)
	if err != nil {
		t.Fatal("no media:", err)
	}
	return buf[:n]
}

// An encrypted phone calls a plain one: BitVoice answers SRTP with its own key, decrypts, and relays
// plain RTP to the other phone, and the reverse.
func TestSRTPToPlainPhone(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	ps := registered(t, e, "100", "101")
	a, b := ps[0], ps[1]
	a.srtpKey = make([]byte, 30)
	rand.Read(a.srtpKey)

	type res struct {
		d    *dialog
		code int
		ans  *sdp.Session
	}
	ch := make(chan res, 1)
	go func() {
		d, code := a.dial("101")
		ch <- res{d: d, code: code}
	}()
	ring := b.waitRing(2 * time.Second)
	if ring == nil {
		t.Fatal("101 not rung")
	}
	if off, _ := sdp.Parse(ring.Req.Body); off.Audio().Secure() || len(off.Audio().Cryptos()) != 0 {
		t.Fatal("the plain phone was offered SRTP or someone else's key")
	}
	bd := b.answer(ring)
	r := <-ch
	if r.code != 200 {
		t.Fatal(r.code)
	}
	// The caller's answer carries BitVoice's key (not the caller's own).
	key := e.legKeyForTest(t, a.user)
	if bytes.Equal(key, a.srtpKey) {
		t.Fatal("BitVoice answered with the caller's own key")
	}
	tx, _ := srtp.New(srtp.AES128SHA80, a.srtpKey[:16], a.srtpKey[16:])
	rx, _ := srtp.New(srtp.AES128SHA80, key[:16], key[16:])

	voice := bytes.Repeat([]byte{0x55}, 160)
	for i := range uint16(3) {
		sp, _ := tx.Protect(rtpPacket(i+1, voice))
		a.rtp.WriteToUDPAddrPort(sp, r.d.media)
		time.Sleep(5 * time.Millisecond)
	}
	got := b.readRTP(t)
	if !bytes.Equal(got[12:], voice) {
		t.Fatal("the plain phone did not get the decrypted audio")
	}
	// A forged packet (wrong key) is dropped.
	bad, _ := srtp.New(srtp.AES128SHA80, make([]byte, 16), make([]byte, 14))
	fp, _ := bad.Protect(rtpPacket(9, voice))
	a.rtp.WriteToUDPAddrPort(fp, r.d.media)

	b.rtp.WriteToUDPAddrPort(rtpPacket(1, voice), bd.media)
	enc := a.readRTP(t)
	plain, err := rx.Unprotect(enc)
	if err != nil || !bytes.Equal(plain[12:], voice) {
		t.Fatalf("caller cannot decrypt what BitVoice sent: %v", err)
	}
	a.send(r.d.request("BYE"))
}

// An offer with only suites BitVoice does not know is refused rather than answered in the clear.
func TestSRTPUnknownSuiteRefused(t *testing.T) {
	e, _ := startEngine(t, testConfig())
	a := registered(t, e, "100")[0]
	s := sdp.New("127.0.0.1", a.rtpPort(), 1, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 101)
	s.Audio().Proto = "RTP/SAVP"
	s.Audio().SetCrypto([]sdp.Crypto{{Tag: 1, Suite: "AEAD_AES_256_GCM", Key: make([]byte, 44)}})
	m := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:*43@127.0.0.1"}
	m.Set("From", "<sip:100@127.0.0.1>;tag="+sip.NewTag())
	m.Set("To", "<sip:*43@127.0.0.1>")
	m.Set("Call-ID", sip.NewCallID())
	m.Set("CSeq", "1 INVITE")
	m.Set("Contact", a.contact())
	m.Set("Content-Type", "application/sdp")
	m.Body = s.Bytes()
	if r := a.send(m); r == nil || r.StatusCode != 488 {
		t.Fatalf("unknown suite: %v", r)
	}
}

// legKeyForTest returns BitVoice's master key on the caller leg of an extension's current call.
func (e *Engine) legKeyForTest(t interface{ Fatal(...any) }, ext string) []byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range e.calls {
		if c.caller != nil && c.caller.Ext == ext {
			c.caller.mu.Lock()
			defer c.caller.mu.Unlock()
			return c.caller.localKey
		}
	}
	t.Fatal("no call from " + ext)
	return nil
}
