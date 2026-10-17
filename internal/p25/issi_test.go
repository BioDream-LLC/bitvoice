package p25

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/p25/imbe"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// home is a stand-in home RFSS: it answers the group INVITE, grants or denies push-to-talk, and
// keeps what the console sends it.
type home struct {
	stack   *sip.Stack
	rtp     *net.UDPConn
	invites chan *sip.Message
	got     chan *Payload
	deny    atomic.Bool
	peer    atomic.Value // netip.AddrPort
}

func newHome(t *testing.T, ctx context.Context) *home {
	h := &home{invites: make(chan *sip.Message, 4), got: make(chan *Payload, 1000)}
	h.rtp, _ = net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	h.stack = sip.NewStack(func(tx *sip.ServerTx, in *sip.Incoming) {
		m := in.Msg
		if m.Method == sip.ACK {
			return
		}
		if m.Method != sip.INVITE {
			tx.Respond(sip.NewResponse(m, 200, ""))
			return
		}
		h.invites <- m
		r := sip.NewResponse(m, 200, "")
		r.Set("To", m.Get("To")+";tag=home1")
		r.Set("Content-Type", "application/sdp")
		port := h.rtp.LocalAddr().(*net.UDPAddr).Port
		r.Body = []byte("v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=TIA-P25-Groupcall\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio " + strconv.Itoa(port) + " RTP/AVP 100\r\na=rtpmap:100 X-TIA-P25-IMBE/8000\r\n")
		tx.Respond(r)
	})
	h.stack.Host = "127.0.0.1"
	if err := h.stack.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	h.stack.Serve(ctx)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := h.rtp.ReadFromUDPAddrPort(buf)
			if err != nil {
				return
			}
			h.peer.Store(from)
			p, err := Parse(buf[12:n])
			if err != nil {
				continue
			}
			if p.Packet.Type == PTTRequest {
				answer := PTTGrant
				if h.deny.Load() {
					answer = PTTDeny
				}
				h.sendTo(from, (&Payload{Packet: PacketType{Type: answer, TSN: p.Packet.TSN}}).Marshal())
			}
			h.got <- p
		}
	}()
	t.Cleanup(func() { h.stack.Close(); h.rtp.Close() })
	return h
}

func (h *home) sendTo(to netip.AddrPort, payload []byte) {
	pkt := make([]byte, 12)
	pkt[0], pkt[1] = 0x80, 100
	binary.BigEndian.PutUint32(pkt[8:], 0x4242)
	_, _ = h.rtp.WriteToUDPAddrPort(append(pkt, payload...), to)
}

func TestGroupCall(t *testing.T) {
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

	up := make(chan bool, 4)
	grants := make(chan bool, 4)
	talkers := make(chan *Talker, 8)
	voice := make(chan []int16, 400)
	cfg := Config{WACN: 0xbee07, System: 0xad5, RFSS: 2, HomeRFSS: 3, Unit: 0x1001, Group: 0x333,
		Home: "127.0.0.1:" + strconv.Itoa(h.stack.Port()), Console: true}
	g, err := NewGroup(cfg, stack, rtp, Events{
		State:  func(ok bool, why string) { up <- ok },
		Grant:  func(ok bool, why string) { grants <- ok },
		Talker: func(tk *Talker) { talkers <- tk },
		Voice:  func(pcm []int16) { voice <- pcm },
	})
	if err != nil {
		t.Fatal(err)
	}
	g.Start(ctx)

	// The INVITE is the ISSI's: to the group's identity, routed to the home RFSS, IMBE over RTP.
	inv := <-h.invites
	if inv.RequestURI != "sip:bee07ad50333@p25dr;user=TIA-P25-SG" || !strings.Contains(inv.Get("Route"), "TIA-P25-Groupcall@03.ad5.bee07.p25dr") ||
		!strings.Contains(string(inv.Body), "X-TIA-P25-IMBE/8000") {
		t.Fatalf("INVITE %s\nRoute %s\n%s", inv.RequestURI, inv.Get("Route"), inv.Body)
	}
	if !<-up {
		t.Fatal("the group call did not come up")
	}

	// The dispatcher keys: request, grant, then voice frames; the home RFSS decodes them.
	if err := g.Key(true); err != nil {
		t.Fatal(err)
	}
	select {
	case ok := <-grants:
		if !ok {
			t.Fatal("not granted")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no answer to the push-to-talk request")
	}
	for i := 0; i < 40; i++ {
		pcm := make([]int16, 160)
		for n := range pcm {
			pcm[n] = int16(6000 * sinApprox(float64(i*160+n)*2*3.14159*180/8000))
		}
		g.Speak(pcm)
	}
	g.Key(false)
	var req, hdr, end bool
	var frames []IMBEBlock
	deadline := time.After(2 * time.Second)
collect:
	for {
		select {
		case p := <-h.got:
			switch p.Packet.Type {
			case PTTRequest:
				req = p.PTT != nil && p.PTT.Unit.ID == 0x1001 && p.ConsolePTT != nil
			case PTTProgress:
				hdr = hdr || (p.Header != nil && p.Header.Group == 0x333 && p.Header.AlgID == Clear)
				frames = append(frames, p.Voice...)
			case PTTEnd:
				end = true
				break collect
			}
		case <-deadline:
			break collect
		}
	}
	if !req || !hdr || !end || len(frames) != 40 {
		t.Fatalf("request %v, header word %v, end %v, %d voice frames (want 40)", req, hdr, end, len(frames))
	}
	if frames[0].FT != 0xc2 || frames[8].FT != 0x6a || len(frames[8].Extra) != 3 || frames[18].FT != 0xc2 {
		t.Fatalf("superframe order: %x %x %x", frames[0].FT, frames[8].FT, frames[18].FT)
	}
	d := imbe.New()
	loud := 0
	for _, f := range frames {
		pcm, err := d.Decode(f.Info())
		if err != nil {
			t.Fatal(err)
		}
		var e float64
		for _, s := range pcm {
			e += float64(s) * float64(s)
		}
		if e/160 > 300*300 {
			loud++
		}
	}
	if loud < 20 {
		t.Fatalf("the home RFSS heard only %d loud frames of 40", loud)
	}

	// A radio talks: the capture's talk spurt, replayed from the home RFSS.
	for _, b := range rtpOf(t, "testdata/issi.pcap") {
		h.sendTo(h.peer.Load().(netip.AddrPort), b)
		time.Sleep(time.Millisecond)
	}
	select {
	case tk := <-talkers:
		if tk == nil || tk.Unit.String() != "bee07.ad5.0000c5" {
			t.Fatalf("talker %+v", tk)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the talker was not reported")
	}
	select {
	case tk := <-talkers:
		if tk != nil {
			t.Fatalf("want the end of the transmission, got %+v", tk)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the end of the transmission was not reported")
	}
	if n := len(voice); n < 100 {
		t.Fatalf("only %d frames of the radio's speech came through", n)
	}

	// Another key while the home RFSS refuses the floor.
	h.deny.Store(true)
	g.Key(true)
	if ok := <-grants; ok {
		t.Fatal("granted while the home RFSS denies")
	}
	if g.Transmitting() {
		t.Fatal("transmitting after a deny")
	}
}

// sinApprox avoids importing math in this test file for one sine.
func sinApprox(x float64) float64 {
	for x > 3.14159 {
		x -= 2 * 3.14159
	}
	return x - x*x*x/6 + x*x*x*x*x/120 - x*x*x*x*x*x*x/5040
}

// An encrypted transmission: the home RFSS sees no speech in the clear, a receiver with the key
// hears it, over several superframes (each with its own MI), and one without the key is muted
// and told why.
func TestGroupCallEncrypted(t *testing.T) {
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
	key := Key{Alg: AlgAES256, ID: 0x0101, Bytes: []byte("0123456789abcdef0123456789abcdef")}
	cfg := Config{WACN: 0xbee07, System: 0xad5, RFSS: 2, HomeRFSS: 3, Unit: 0x1001, Group: 0x333,
		Home: "127.0.0.1:" + strconv.Itoa(h.stack.Port()), Key: &key, Keys: []Key{key}}
	up, grants := make(chan bool, 2), make(chan bool, 2)
	g, _ := NewGroup(cfg, stack, rtp, Events{State: func(ok bool, _ string) { up <- ok }, Grant: func(ok bool, _ string) { grants <- ok }})
	g.Start(ctx)
	<-h.invites
	<-up
	g.Key(true)
	<-grants
	const frames = 60
	for i := 0; i < frames; i++ {
		pcm := make([]int16, 160)
		for n := range pcm {
			x := float64(i*160 + n)
			for k := 1; k <= 8; k++ {
				pcm[n] += int16(3000 / float64(k) * sinApprox(float64(k)*x*2*3.14159*140/8000))
			}
		}
		g.Speak(pcm)
	}
	g.Key(false)
	var sent []*Payload
	for len(sent) == 0 || sent[len(sent)-1].Packet.Type != PTTEnd {
		select {
		case p := <-h.got:
			sent = append(sent, p)
		case <-time.After(15 * time.Second):
			t.Fatal("transmission did not end")
		}
	}

	// What the home RFSS can make of it without a key: noise, not the speech.
	listen := func(keys []Key) (loud int, tk *Talker) {
		var got [][]int16
		r, _ := NewGroup(Config{Group: 0x333, Home: "127.0.0.1:1", Keys: keys}, nil, nil, Events{
			Voice: func(pcm []int16) { got = append(got, pcm) },
			Talker: func(t *Talker) {
				if t != nil {
					tk = t
				}
			},
		})
		for _, p := range sent {
			r.receive(p)
		}
		for _, pcm := range got {
			var e float64
			for _, s := range pcm {
				e += float64(s) * float64(s)
			}
			if e/160 > 400*400 {
				loud++
			}
		}
		return loud, tk
	}
	withKey, tk := listen([]Key{key})
	if withKey < frames/2 || tk == nil || tk.Alg != AlgAES256 || tk.KeyID != 0x0101 || tk.NoKey {
		t.Fatalf("with the key: %d loud frames of %d, talker %+v", withKey, frames, tk)
	}
	noKey, tk := listen(nil)
	if noKey != 0 || tk == nil || !tk.NoKey {
		t.Fatalf("without the key: %d loud frames (want silence), talker %+v", noKey, tk)
	}
	other := key
	other.ID = 0x0202
	if n, tk := listen([]Key{other}); n != 0 || !tk.NoKey {
		t.Fatalf("with another key ID: %d loud frames, talker %+v", n, tk)
	}
	// The header word carries the algorithm, key ID and MI; frames 12-17 the next MI.
	var hdr *HeaderWord
	for _, p := range sent {
		if p.Header != nil {
			hdr = p.Header
		}
	}
	if hdr == nil || hdr.AlgID != AlgAES256 || hdr.KeyID != 0x0101 || binary.BigEndian.Uint64(hdr.MI[:8]) == 0 {
		t.Fatalf("header word %+v", hdr)
	}
}

// Voice in blocks the console does not decode (as Phase 2 half-rate voice sent untranscoded would
// come): after a few such packets the talker is marked undecodable, and full-rate voice clears it.
func TestUndecodableVoice(t *testing.T) {
	var last *Talker
	g := NewReceiver(1, nil, Events{Talker: func(t *Talker) { last = t }})
	ptt := &PTTWord{Unit: Unit{ID: 1205}}
	odd := func() *Payload {
		return &Payload{Packet: PacketType{Type: PTTProgress}, PTT: ptt, Other: map[int][]byte{99: make([]byte, 9)}}
	}
	g.Receive(&Payload{Packet: PacketType{Type: PTTStart}, PTT: ptt})
	for i := 0; i < 3; i++ {
		g.Receive(odd())
	}
	if last == nil || !last.Undecodable {
		t.Fatalf("not marked: %+v", last)
	}
	g.Receive(&Payload{Packet: PacketType{Type: PTTProgress}, PTT: ptt, Voice: []IMBEBlock{{FT: 0x62, Mute: true}}})
	if last == nil || last.Undecodable {
		t.Fatalf("not cleared: %+v", last)
	}
}
