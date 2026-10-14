package sccp

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/events"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
	"github.com/biodream-llc/bitvoice/internal/webphone"
)

type nopRecorder struct{}

func (nopRecorder) AddCDR(store.CDR) error { return nil }

// fakePhone is a Skinny phone: it registers, keeps what the server sends, and has an RTP socket.
type fakePhone struct {
	t    *testing.T
	conn net.Conn
	rtp  *net.UDPConn
	got  chan [2]any // id, data
	seen [][2]any
}

func dialPhone(t *testing.T, port int, name string) *fakePhone {
	c, err := net.Dial("tcp", "127.0.0.1:"+itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	f := &fakePhone{t: t, conn: c, rtp: r, got: make(chan [2]any, 256)}
	t.Cleanup(func() { c.Close(); r.Close() })
	go func() {
		for {
			id, d, err := readMessage(c)
			if err != nil {
				close(f.got)
				return
			}
			f.got <- [2]any{id, d}
		}
	}()
	b := (&builder{}).str(name, 16).u32(0, 1, 0x0100007f, 7, 5, 0).u8(11, 0, 0, 0).zero(8)
	f.send(msgRegister, b)
	return f
}

func itoa(n int) string { return strconv.Itoa(n) }

func (f *fakePhone) send(id uint32, b *builder) {
	var d []byte
	if b != nil {
		d = b.b.Bytes()
	}
	_, _ = f.conn.Write(frame(id, d))
}

func (f *fakePhone) wait(what string, id uint32, match func([]byte) bool) []byte {
	f.t.Helper()
	for i, m := range f.seen {
		if m[0].(uint32) == id && (match == nil || match(m[1].([]byte))) {
			f.seen = append(f.seen[:i], f.seen[i+1:]...)
			return m[1].([]byte)
		}
	}
	deadline := time.After(4 * time.Second)
	for {
		select {
		case m, ok := <-f.got:
			if !ok {
				f.t.Fatalf("connection closed waiting for %s", what)
			}
			if m[0].(uint32) == id && (match == nil || match(m[1].([]byte))) {
				return m[1].([]byte)
			}
			f.seen = append(f.seen, m)
		case <-deadline:
			f.t.Fatalf("the phone was never sent %s", what)
			return nil
		}
	}
}

func stateIs(s uint32) func([]byte) bool {
	return func(d []byte) bool { return le32(d, 0) == s }
}

func TestRegisterRefusesUnknownPhones(t *testing.T) {
	s, _ := startServer(t)
	f := dialPhone(t, s.Port(), "SEP000000000099")
	d := f.wait("a refusal", msgRegisterReject, nil)
	if !strings.Contains(cstr(d), "SEP000000000099") {
		t.Fatalf("%q", cstr(d))
	}
}

func startServer(t *testing.T) (*Server, *pbx.Engine) {
	cfg := config.Default()
	cfg.System.PublicAddress = "127.0.0.1"
	cfg.System.RTPMin, cfg.System.RTPMax = 36000, 37000
	for _, n := range []string{"100", "101"} {
		cfg.Extensions = append(cfg.Extensions, config.Extension{Number: n, Name: "Desk " + n, Secret: "a long enough secret 1", Enabled: true, Outbound: true, RingSeconds: 10})
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e := pbx.New(cfg, events.New(), nopRecorder{}, nil)
	if err := e.Start(context.Background(), "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wp, err := webphone.New(ctx, e.SIPPort(), e)
	if err != nil {
		t.Fatal(err)
	}
	s := New(ctx, wp, e, func(string) string { return "a long enough secret 1" }, nil)
	s.Apply(config.SCCP{Enabled: true, Phones: []config.SCCPPhone{{Device: "SEP001122334455", Extension: "100"}, {Device: "SEP001122334466", Extension: "101"}}}, "127.0.0.1:0")
	t.Cleanup(s.Close)
	return s, e
}

// Phone A (extension 100) lifts its handset, gets dial tone and dials 101#; phone B rings with
// A's number, answers, and A's audio reaches B; A hangs up and B is told.
func TestCallBetweenTwoPhones(t *testing.T) {
	DigitTimeout = 500 * time.Millisecond
	s, _ := startServer(t)
	a := dialPhone(t, s.Port(), "SEP001122334455")
	b := dialPhone(t, s.Port(), "SEP001122334466")
	a.wait("a registration", msgRegisterAck, nil)
	b.wait("a registration", msgRegisterAck, nil)
	a.send(msgLineStatReq, (&builder{}).u32(1))
	if d := a.wait("its line", msgLineStat, nil); cstr(d[4:28]) != "100" {
		t.Fatalf("line %q", cstr(d[4:28]))
	}
	a.send(msgSoftKeyTemplateReq, nil)
	a.wait("soft keys", msgSoftKeyTemplateRes, nil)

	a.send(msgOffHook, nil)
	a.wait("dial tone", msgStartTone, stateIs(toneDial))
	for _, k := range []uint32{1, 0, 1, 15} {
		a.send(msgKeypadButton, (&builder{}).u32(k, 1, 0))
	}
	open := a.wait("an audio channel", msgOpenReceiveChannel, nil)
	smt := a.wait("where to send audio", msgStartMediaTransmit, nil)
	ourPort := int(le32(smt, 12))
	ref := le32(open, 0)
	ap := a.rtp.LocalAddr().(*net.UDPAddr)
	a.send(msgOpenReceiveChannelAck, (&builder{}).u32(0).u8(127, 0, 0, 1).u32(uint32(ap.Port), ref))
	a.wait("ring out", msgCallState, stateIs(stateRingOut))

	ci := b.wait("caller ID", msgCallInfo, nil)
	if cstr(ci[40:64]) != "100" {
		t.Errorf("caller %q", cstr(ci[40:64]))
	}
	b.wait("ringing", msgSetRinger, stateIs(ringInside))
	b.send(msgSoftKeyEvent, (&builder{}).u32(keyAnswer, 1, 0))
	bopen := b.wait("an audio channel", msgOpenReceiveChannel, nil)
	bp := b.rtp.LocalAddr().(*net.UDPAddr)
	b.send(msgOpenReceiveChannelAck, (&builder{}).u32(0).u8(127, 0, 0, 1).u32(uint32(bp.Port), le32(bopen, 0)))
	b.wait("connected", msgCallState, stateIs(stateConnected))
	a.wait("connected", msgCallState, stateIs(stateConnected))

	pkt := make([]byte, 172)
	pkt[0] = 0x80
	to := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(ourPort))
	buf := make([]byte, 2048)
	got := false
	for i := 0; i < 60 && !got; i++ {
		binary.BigEndian.PutUint16(pkt[2:], uint16(i))
		binary.BigEndian.PutUint32(pkt[4:], uint32(i*160))
		copy(pkt[12:], bytes.Repeat([]byte{byte(0x20 + i)}, 160))
		_, _ = a.rtp.WriteToUDPAddrPort(pkt, to)
		_ = b.rtp.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
		if n, _, err := b.rtp.ReadFromUDPAddrPort(buf); err == nil && n > 12 {
			got = true
		}
	}
	if !got {
		t.Fatal("no audio from A at B")
	}
	a.send(msgOnHook, nil)
	b.wait("the call closed", msgCloseReceiveChannel, nil)
	if st := s.Status(); !strings.HasPrefix(st["SEP001122334455"], "registered") {
		t.Errorf("status %v", st)
	}
}

func TestFrames(t *testing.T) {
	f := frame(msgKeepAlive, nil)
	id, d, err := readMessage(bytes.NewReader(f))
	if err != nil || id != msgKeepAlive || len(d) != 0 {
		t.Fatal(id, d, err)
	}
	if _, _, err := readMessage(bytes.NewReader([]byte{0xff, 0xff, 0xff, 0x7f, 0, 0, 0, 0, 0, 0, 0, 0})); err == nil {
		t.Fatal("a huge length was accepted")
	}
}

func FuzzHandle(f *testing.F) {
	f.Add(uint32(msgKeypadButton), []byte{1, 0, 0, 0})
	f.Fuzz(func(t *testing.T, id uint32, d []byte) {
		c1, c2 := net.Pipe()
		defer c1.Close()
		defer c2.Close()
		go func() { _, _ = c2.Read(make([]byte, 1<<16)) }()
		p := &phone{s: &Server{phones: map[string]config.SCCPPhone{}, conns: map[string]*phone{}}, conn: c1, name: "SEPX"}
		_ = c1.SetWriteDeadline(time.Now().Add(10 * time.Millisecond))
		if id == msgRegister {
			return
		}
		p.handle(id, d)
	})
}
