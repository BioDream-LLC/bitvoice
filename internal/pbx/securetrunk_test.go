package pbx

import (
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// The fake carrier answers every call with plain RTP, as some real ones do even when offered SRTP.

// A trunk that requires encryption hangs up a call out that the provider answered without it.
func TestRequiredEncryptionRefusesPlainAnswer(t *testing.T) {
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.Trunks[0].SecureMedia, cfg.Trunks[0].RequireSecureMedia = true, true
	e, rec := startEngine(t, cfg)
	waitTrunk(t, e, "registered")
	a := registered(t, e, "100")[0]
	if _, code := a.dial("2125551212"); code != 488 {
		t.Fatalf("call answered without encryption: %d, want 488", code)
	}
	select {
	case inv := <-c.invites:
		if off, _ := sdp.Parse(inv.Body); off == nil || !off.Audio().Secure() {
			t.Fatal("the provider was not offered SRTP")
		}
	default:
		t.Fatal("the provider saw no INVITE")
	}
	r := rec.find(t, "the refused call", func(r store.CDR) bool { return r.Direction == "outbound" })
	if r.Disposition != "failed" || !strings.Contains(strings.Join(r.Why, " "), "Carrier answered without encryption") {
		t.Fatalf("record %+v", r)
	}
}

// Asked to encrypt but answered in the clear: the call goes on (Encrypt media alone means "if the
// provider supports it"), and the record says that side was not encrypted.
func TestUnencryptedAnswerMarked(t *testing.T) {
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.Trunks[0].SecureMedia = true
	e, rec := startEngine(t, cfg)
	waitTrunk(t, e, "registered")
	a := registered(t, e, "100")[0]
	d, code := a.dial("2125551212")
	if code != 200 {
		t.Fatalf("call out: %d", code)
	}
	inv := <-c.invites
	off, _ := sdp.Parse(inv.Body)
	media := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(off.Audio().Port))
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40000})
	if err != nil {
		t.Skip("port 40000 (the carrier's answer) is busy:", err)
	}
	defer pc.Close()
	voice := make([]byte, 160)
	for i := range uint16(5) {
		pc.WriteToUDPAddrPort(rtpPacket(i+1, voice), media)
		a.rtp.WriteToUDPAddrPort(rtpPacket(i+1, voice), d.media)
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	a.send(d.request(sip.BYE))
	r := rec.find(t, "the call", func(r store.CDR) bool { return r.Direction == "outbound" })
	var trunk *store.LegQuality
	for i := range r.Quality {
		if r.Quality[i].Trunk == "t1" {
			trunk = &r.Quality[i]
		}
	}
	if trunk == nil || trunk.Secure || !trunk.Unencrypted {
		t.Fatalf("provider side %+v, want unencrypted (quality %+v)", trunk, r.Quality)
	}
}

// A call in offered without SRTP on a trunk that requires it is refused, with a record saying why.
func TestRequiredEncryptionRefusesPlainOffer(t *testing.T) {
	c := newCarrier(t)
	cfg := trunkConfig(c.stack.Port())
	cfg.Trunks[0].SecureMedia, cfg.Trunks[0].RequireSecureMedia = true, true
	e, rec := startEngine(t, cfg)
	waitTrunk(t, e, "registered")
	b := registered(t, e, "101")[0]
	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "sip:acct@127.0.0.1"}
	inv.Set("From", "<sip:3125559999@carrier.test>;tag=in1")
	inv.Set("To", "<sip:+12125550100@carrier.test>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:carrier@127.0.0.1:"+strconv.Itoa(c.stack.Port())+">")
	inv.Set("Content-Type", "application/sdp")
	inv.Body = sdp.New("127.0.0.1", 40002, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 0).Bytes()
	if r := finalOf(c.stack.Request(inv, pbx)); r == nil || r.StatusCode != 488 {
		t.Fatalf("plain call in: %v, want 488", r)
	}
	if ring := b.waitRing(300 * time.Millisecond); ring != nil {
		t.Fatal("the refused call rang 101")
	}
	r := rec.find(t, "the refused call", func(r store.CDR) bool { return r.Direction == "inbound" })
	if r.Disposition != "failed" || !strings.Contains(strings.Join(r.Why, " "), "sent the call without encryption") {
		t.Fatalf("record %+v", r)
	}
}

// An answered call whose provider sent no sound at all says so in its record (the classic firewall
// that lets SIP in and keeps the media out); the side that did send sound is not named.
func TestNoSoundRecorded(t *testing.T) {
	c := newCarrier(t)
	e, rec := startEngine(t, trunkConfig(c.stack.Port()), func(e *Engine) { e.noSoundAfter = 300 * time.Millisecond })
	waitTrunk(t, e, "registered")
	a := registered(t, e, "100")[0]
	d, code := a.dial("2125551212")
	if code != 200 {
		t.Fatalf("call out: %d", code)
	}
	voice := make([]byte, 160)
	for i := range uint16(20) {
		a.rtp.WriteToUDPAddrPort(rtpPacket(i+1, voice), d.media)
		time.Sleep(20 * time.Millisecond)
	}
	a.send(d.request(sip.BYE))
	r := rec.find(t, "the call", func(r store.CDR) bool { return r.Direction == "outbound" })
	if len(r.NoSound) != 1 || r.NoSound[0].Trunk != "t1" || r.NoSound[0].Name != "Carrier" {
		t.Fatalf("no sound %+v, want the provider alone", r.NoSound)
	}
}

// A call too short to judge names nobody.
func TestNoSoundShortCall(t *testing.T) {
	c := newCarrier(t)
	e, rec := startEngine(t, trunkConfig(c.stack.Port()), func(e *Engine) { e.noSoundAfter = time.Minute })
	waitTrunk(t, e, "registered")
	a := registered(t, e, "100")[0]
	d, code := a.dial("2125551212")
	if code != 200 {
		t.Fatalf("call out: %d", code)
	}
	a.send(d.request(sip.BYE))
	if r := rec.find(t, "the call", func(r store.CDR) bool { return r.Direction == "outbound" }); len(r.NoSound) != 0 {
		t.Fatalf("no sound %+v on a short call", r.NoSound)
	}
}
