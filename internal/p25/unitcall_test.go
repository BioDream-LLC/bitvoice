package p25

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/sip"
)

// A private call from the console to a radio: the INVITE goes to the unit's identity, routed to
// its home RFSS as the unit-to-unit destination, with the SDP and the ISSI call parameters in one
// multipart body; it is not set up again once it ends.
func TestUnitCallOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := newHome(t, ctx)
	stack := sip.NewStack(func(tx *sip.ServerTx, in *sip.Incoming) {})
	stack.Host = "127.0.0.1"
	if err := stack.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	stack.Serve(ctx)
	defer stack.Close()
	rtp, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer rtp.Close()
	states := make(chan string, 4)
	grants := make(chan bool, 2)
	g, _ := NewGroup(Config{WACN: 0xbee07, System: 0xad5, RFSS: 2, HomeRFSS: 3, Unit: 0x1001, CallUnit: 0xc5, Once: true, Emergency: true,
		Home: "127.0.0.1:" + strconv.Itoa(h.stack.Port())}, stack, rtp, Events{
		State: func(up bool, why string) { states <- strconv.FormatBool(up) + " " + why },
		Grant: func(ok bool, _ string) { grants <- ok },
	})
	g.Start(ctx)
	inv := <-h.invites
	if inv.RequestURI != "sip:bee07ad50000c5@p25dr;user=TIA-P25-SU" || !strings.Contains(inv.Get("From"), "sip:bee07ad5001001@p25dr;user=TIA-P25-SU") ||
		!strings.Contains(inv.Get("Route"), "TIA-P25-U2UDest@03.ad5.bee07.p25dr") || inv.Get("Priority") != "4;e" ||
		!strings.HasPrefix(inv.Get("Content-Type"), "multipart/mixed") || !strings.Contains(string(inv.Body), "s=TIA-P25-U2UCall") ||
		!strings.Contains(string(inv.Body), "application/x-tia-p25-issi") {
		t.Fatalf("INVITE %s\nFrom %s\nRoute %s\nPriority %s\n%s", inv.RequestURI, inv.Get("From"), inv.Get("Route"), inv.Get("Priority"), inv.Body)
	}
	if s := <-states; s != "true " {
		t.Fatalf("state %q", s)
	}
	g.Key(true)
	if !<-grants {
		t.Fatal("not granted")
	}
	g.Key(false)
	g.Stop()
	select {
	case <-h.invites:
		t.Fatal("a private call was set up again")
	case <-time.After(300 * time.Millisecond):
	}
}

// A radio calls the console's unit: the call is answered, and the unit's BYE ends it.
func TestUnitCallIn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rtp, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer rtp.Close()
	var gp atomic.Pointer[Group]
	states := make(chan string, 4)
	stack := sip.NewStack(func(tx *sip.ServerTx, in *sip.Incoming) {
		if g := gp.Load(); (g == nil || !g.Handle(tx, in)) && in.Msg.Method != sip.ACK {
			tx.Respond(sip.NewResponse(in.Msg, 404, ""))
		}
	})
	stack.Host = "127.0.0.1"
	if err := stack.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	stack.Serve(ctx)
	defer stack.Close()
	home := sip.NewStack(func(tx *sip.ServerTx, in *sip.Incoming) {})
	home.Host = "127.0.0.1"
	if err := home.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	home.Serve(ctx)
	defer home.Close()
	g, _ := NewGroup(Config{WACN: 0xbee07, System: 0xad5, RFSS: 2, HomeRFSS: 3, Unit: 0x1001, CallUnit: 0xc5, Once: true,
		Home: "127.0.0.1:" + strconv.Itoa(home.Port())}, stack, rtp, Events{State: func(up bool, why string) { states <- strconv.FormatBool(up) + " " + why }})
	gp.Store(g)

	peer := sip.Peer{Transport: "UDP", Addr: netipAddr(t, stack.Port())}
	call := func(method string, body string, cseq int) *sip.Message {
		m := &sip.Message{IsRequest: true, Method: method, RequestURI: "sip:bee07ad5001001@p25dr;user=TIA-P25-SU"}
		m.Set("From", "<sip:bee07ad50000c5@p25dr;user=TIA-P25-SU>;tag=radio1")
		m.Set("To", "<sip:bee07ad5001001@p25dr;user=TIA-P25-SU>")
		m.Set("Call-ID", "u2u-1@03.ad5.bee07.p25dr")
		m.Set("CSeq", strconv.Itoa(cseq)+" "+method)
		if body != "" {
			m.Set("Content-Type", `multipart/mixed;boundary="P25 ISSI body boundary"`)
			m.Body = []byte(body)
		}
		var final *sip.Message
		for r := range home.Request(m, peer).Responses {
			final = r
		}
		return final
	}
	body := "--P25 ISSI body boundary\r\nContent-Type: application/sdp\r\n\r\nv=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=TIA-P25-U2UCall\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 25000 RTP/AVP 100\r\na=rtpmap:100 X-TIA-P25-IMBE/8000\r\n" +
		"\r\n--P25 ISSI body boundary\r\nContent-Type: application/x-tia-p25-issi\r\n\r\nc-duplex:0\r\n\r\n--P25 ISSI body boundary--\r\n"
	if r := call(sip.INVITE, body, 1); r == nil || r.StatusCode != 200 || !strings.Contains(string(r.Body), "s=TIA-P25-U2UCall") {
		t.Fatalf("answer %+v", r)
	}
	if s := <-states; s != "true " {
		t.Fatalf("state %q", s)
	}
	g.Start(ctx)
	if !g.Up() {
		t.Fatal("not up")
	}
	if r := call(sip.BYE, "", 2); r == nil || r.StatusCode != 200 {
		t.Fatalf("BYE %+v", r)
	}
	if s := <-states; s != "false the unit hung up" {
		t.Fatalf("state %q", s)
	}
}

func netipAddr(t *testing.T, port int) netip.AddrPort {
	return netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(port))
}
