package pbx

import (
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// The BCF's media inspection on an ESInet call answered by a phone: packets with a payload type the
// offer did not name, header lengths that do not fit, too many bytes, or past the packets-a-second
// limit never reach the phone and are logged; good packets do, and the call goes on.
func TestBCFInspectsMedia(t *testing.T) {
	esinet := newCarrier(t)
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{{ID: "in", Name: "ESInet", Enabled: true, Host: "127.0.0.1", Port: esinet.stack.Port(), ESInet: true, AllowFrom: []string{"127.0.0.1"}}}
	cfg.System.Core.BCF = config.BCF{Enabled: true, InspectMedia: true, MediaPerSecond: 100, MediaMaxBytes: 400}
	cfg.InboundRoutes = []config.InboundRoute{{Name: "911", Trunk: "in", Destination: config.Destination{Type: "extension", Target: "100"}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ph := registered(t, e, "100")[0]

	media, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer media.Close()
	port := media.LocalAddr().(*net.UDPAddr).Port
	pbx := sip.Peer{Transport: "UDP", Addr: netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(e.SIPPort()))}
	inv := &sip.Message{IsRequest: true, Method: sip.INVITE, RequestURI: "urn:service:sos"}
	inv.Set("From", "<sip:+17175550100@esinet.test>;tag="+sip.NewTag())
	inv.Set("To", "<urn:service:sos>")
	inv.Set("Call-ID", sip.NewCallID())
	inv.Set("CSeq", "1 INVITE")
	inv.Set("Contact", "<sip:esrp@127.0.0.1:"+strconv.Itoa(esinet.stack.Port())+">")
	inv.Set("Content-Type", "application/sdp")
	inv.Body = sdp.New("127.0.0.1", port, 3, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}, {PT: 101, Name: "telephone-event", ClockRate: 8000}}, 0).Bytes()
	tx := esinet.stack.Request(inv, pbx)
	ring := ph.waitRing(5 * time.Second)
	if ring == nil {
		t.Fatal("the phone did not ring")
	}
	ph.answer(ring)
	r := finalOf(tx)
	if r == nil || r.StatusCode != 200 {
		t.Fatalf("answer %v", r)
	}
	to := mediaOf(r.Body)
	ack := &sip.Message{IsRequest: true, Method: sip.ACK, RequestURI: "sip:x@127.0.0.1:" + strconv.Itoa(e.SIPPort())}
	ack.Set("From", inv.Get("From"))
	ack.Set("To", r.Get("To"))
	ack.Set("Call-ID", inv.CallID())
	ack.Set("CSeq", "1 ACK")
	_ = esinet.stack.Stateless(ack, pbx)

	send := func(pt byte, size int, mangle func([]byte), n int, seq *uint16) {
		for i := 0; i < n; i++ {
			pkt := make([]byte, size)
			pkt[0], pkt[1] = 0x80, pt
			*seq++
			pkt[2], pkt[3] = byte(*seq>>8), byte(*seq)
			pkt[8] = 7
			if mangle != nil {
				mangle(pkt)
			}
			_, _ = media.WriteToUDPAddrPort(pkt, to)
			time.Sleep(2 * time.Millisecond)
		}
	}
	got := func() map[byte]int {
		out := map[byte]int{}
		_ = ph.rtp.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
		buf := make([]byte, 2048)
		for {
			n, _, err := ph.rtp.ReadFromUDP(buf)
			if err != nil {
				return out
			}
			if n >= 12 && buf[0]>>6 == 2 {
				out[buf[1]&0x7f]++
			}
		}
	}
	var seq uint16
	send(0, 172, nil, 10, &seq) // good: reaches the phone
	if g := got(); g[0] < 8 {
		t.Fatalf("good media did not reach the phone: %v", g)
	}
	send(99, 172, nil, 10, &seq)                                                // a payload type not offered
	send(0, 40, func(p []byte) { p[0] |= 0x10; p[14], p[15] = 0, 50 }, 5, &seq) // an extension longer than the packet
	send(0, 600, nil, 5, &seq)                                                  // over 400 bytes
	g := got()
	if g[99] != 0 {
		t.Fatalf("a payload type the offer did not name reached the phone: %v", g)
	}
	waitFor(t, "the BCF did not log the media", func() bool {
		kinds := map[string]bool{}
		for _, ev := range e.BCFLog() {
			if ev.Action == "media" {
				for _, k := range []string{"payload type 99", "do not fit", "over 400 bytes"} {
					if strings.Contains(ev.Why, k) {
						kinds[k] = true
					}
				}
			}
		}
		return len(kinds) == 3
	})
	// A flood: 400 packets as fast as they go; at most the limit's worth arrive.
	for i := 0; i < 400; i++ {
		pkt := make([]byte, 172)
		pkt[0] = 0x80
		seq++
		pkt[2], pkt[3] = byte(seq>>8), byte(seq)
		_, _ = media.WriteToUDPAddrPort(pkt, to)
	}
	if g := got(); g[0] > 150 {
		t.Fatalf("the flood went through: %d packets", g[0])
	}
	waitFor(t, "the flood was not logged", func() bool {
		for _, ev := range e.BCFLog() {
			if ev.Action == "media" && strings.Contains(ev.Why, "more than 100 RTP packets in a second") {
				return true
			}
		}
		return false
	})
	time.Sleep(1100 * time.Millisecond) // the next second: good media flows again
	send(0, 172, nil, 10, &seq)
	if g := got(); g[0] < 8 {
		t.Fatalf("good media stopped after the flood: %v", g)
	}
	if calls := e.Calls(); len(calls) != 1 {
		t.Fatalf("the call did not go on: %+v", calls)
	}
}
