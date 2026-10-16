package pbx

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"net"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/p25"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// captureRTP reads the RTP payloads to port 25000 in the ISSI capture.
func captureRTP(t *testing.T) [][]byte {
	data, err := os.ReadFile("../p25/testdata/issi.pcap")
	if err != nil {
		t.Fatal(err)
	}
	link := binary.LittleEndian.Uint32(data[20:])
	var out [][]byte
	for off := 24; off+16 <= len(data); {
		n := int(binary.LittleEndian.Uint32(data[off+8:]))
		frame := data[off+16 : off+16+n]
		off += 16 + n
		ip := frame
		if link == 1 {
			ip = frame[14:]
		}
		if len(ip) < 28 || ip[0]>>4 != 4 || ip[9] != 17 {
			continue
		}
		udp := ip[int(ip[0]&0xf)*4:]
		if binary.BigEndian.Uint16(udp[2:]) == 25000 {
			out = append(out, udp[8+12:binary.BigEndian.Uint16(udp[4:])])
		}
	}
	return out
}

// A P25 talkgroup on the console: a radio's talk spurt reaches the dispatcher, and the
// dispatcher's push-to-talk is granted by the home RFSS and carries their voice to it as IMBE.
func TestRadioConsoleP25(t *testing.T) { radioConsoleP25(t, false) }

// The same with the channel transmitting AES-256: the home RFSS gets the algorithm and key ID in
// the header word, and can hear the dispatcher only with the key.
func TestRadioConsoleP25Encrypted(t *testing.T) { radioConsoleP25(t, true) }

func radioConsoleP25(t *testing.T, encrypted bool) {
	h := newHomeRFSS(t)
	home, got, invited := h.stack, h.got, h.invited
	sendHome := h.send
	var peer = &h.peer
	sipPort := freeUDP(t)
	rtpPort := freeUDP(t) &^ 1
	cfg := testConfig()
	cfg.System.Radio = config.Radio{Console: "800",
		P25: config.P25Console{WACN: "BEE07", System: "AD5", RFSS: 2, Unit: 0x1001, SIPPort: sipPort},
		Channels: []config.RadioChannel{{ID: "tg", Name: "County fire", Kind: "p25", Port: rtpPort, Group: 0x333,
			Home: "127.0.0.1:" + strconv.Itoa(home.Port()), HomeRFSS: 3}}}
	key := p25.Key{Alg: p25.AlgAES256, ID: 0x0101, Bytes: []byte("0123456789abcdef0123456789abcdef")}
	if encrypted {
		cfg.System.Radio.P25.Keys = []config.P25Key{{ID: "k1", Name: "Fire tac", Alg: "aes256", KeyID: 0x0101, Key: hex.EncodeToString(key.Bytes)}}
		cfg.System.Radio.Channels[0].TxKey = "k1"
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	select {
	case <-invited:
	case <-time.After(3 * time.Second):
		t.Fatal("the console never set up the group call")
	}
	disp := registered(t, e, "100")[0]
	d, code := disp.dial("800")
	if code != 200 {
		t.Fatalf("console answered %d", code)
	}
	var call string
	for i := 0; i < 40 && call == ""; i++ {
		if ps := e.Console().Positions; len(ps) == 1 {
			call = ps[0].Call
		}
		time.Sleep(50 * time.Millisecond)
	}
	for i := 0; i < 40 && peer.Load() == nil; i++ {
		time.Sleep(50 * time.Millisecond) // the console's first heartbeat tells the home where it is
	}
	if st := e.Console().Channels; len(st) != 1 || !st[0].Linked || st[0].Kind != "p25" {
		t.Fatalf("talkgroup state %+v", st)
	}

	// A radio talks.
	sub := e.Bus.Subscribe()
	defer e.Bus.Unsubscribe(sub)
	disp.drainRTP()
	go func() {
		for _, b := range captureRTP(t) {
			sendHome(peer.Load().(netip.AddrPort), b)
			time.Sleep(20 * time.Millisecond)
		}
	}()
	if !receivedLoud(disp.rtp, 3*time.Second) {
		t.Fatal("the dispatcher does not hear the talkgroup")
	}
	if st := e.Console().Channels[0]; st.Talker != "bee07.ad5.0000c5" {
		t.Fatalf("talker not shown: %+v", st)
	}
	for found := false; !found; { // and CAD hears who is on the air
		select {
		case ev := <-sub.C:
			if u, ok := ev.Data.(RadioUnitEvent); ev.Type == "radio.unit" && ok {
				if u.UnitID != 197 || u.ChannelName != "County fire" || u.WACN != "BEE07" {
					t.Fatalf("radio.unit %+v", u)
				}
				found = true
			}
		case <-time.After(time.Second):
			t.Fatal("no radio.unit event")
		}
	}
	time.Sleep(2600 * time.Millisecond) // the rest of the radio's transmission

	// The dispatcher talks.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for seq := uint16(0); ; seq++ {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
			disp.rtp.WriteToUDP(voicePacket(seq), net.UDPAddrFromAddrPort(d.media))
		}
	}()
	if err := e.PTT(call, "tg", true); err != nil {
		t.Fatal(err)
	}
	var heard [][]int16
	var keys []p25.Key
	if encrypted {
		keys = []p25.Key{key}
	}
	rx := p25.NewReceiver(0x333, keys, p25.Events{Voice: func(pcm []int16) { heard = append(heard, pcm) }})
	loud, req, alg := 0, false, p25.AlgClear
	deadline := time.After(12 * time.Second) // 1.2 s of voice; slow CI machines need the room
	for loud < 60 {
		select {
		case p := <-got:
			if p.Packet.Type == p25.PTTRequest && p.PTT != nil && p.PTT.Unit.ID == 0x1001 {
				req = true
			}
			if p.Header != nil {
				alg = p.Header.AlgID
			}
			heard = heard[:0]
			rx.Receive(p)
			for _, pcm := range heard {
				var e2 float64
				for _, s := range pcm {
					e2 += float64(s) * float64(s)
				}
				if e2/160 > 500*500 {
					loud++
				}
			}
		case <-deadline:
			t.Fatalf("request %v, %d loud voice frames at the home RFSS", req, loud)
		}
	}
	if want := map[bool]int{false: p25.AlgClear, true: p25.AlgAES256}[encrypted]; alg != want {
		t.Fatalf("header word algorithm %#x, want %#x", alg, want)
	}
	if encrypted && e.Console().Channels[0].TxEncrypt != "AES-256" {
		t.Fatalf("tile does not say the channel transmits encrypted: %+v", e.Console().Channels[0])
	}
	e.PTT(call, "tg", false)
	for end := false; !end; {
		select {
		case p := <-got:
			end = p.Packet.Type == p25.PTTEnd
		case <-time.After(2 * time.Second):
			t.Fatal("no end of transmission")
		}
	}
}

// handedOut is every port pair freeUDP has given this test binary, so two calls never return the same
// pair. Windows hands out free ports in order rather than at random, so without it the next free port is
// often the one just returned.
var handedOut = struct {
	sync.Mutex
	ports map[int]bool
}{ports: map[int]bool{}}

// freeUDP returns an even port, free together with the odd one above it (for UDP and TCP), that no earlier
// call returned.
func freeUDP(t *testing.T) int {
	handedOut.Lock()
	defer handedOut.Unlock()
	for range 1000 {
		c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		p := c.LocalAddr().(*net.UDPAddr).Port &^ 1
		c.Close()
		if p <= 40000 || p >= 64000 || handedOut.ports[p] {
			continue
		}
		// Both on loopback and on every address: Windows lets a socket on every address share a port
		// with one on 127.0.0.1, and then the loopback one gets the packets.
		ok := true
		for _, q := range []int{p, p + 1} {
			for _, ip := range []net.IP{net.IPv4(127, 0, 0, 1), nil} {
				c2, err := net.ListenUDP("udp", &net.UDPAddr{IP: ip, Port: q})
				if err != nil {
					ok = false
					break
				}
				c2.Close()
				// TCP too: a port used for SIP (the ISSI's) is opened for both.
				l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: ip, Port: q})
				if err != nil {
					ok = false
					break
				}
				l.Close()
			}
		}
		if ok {
			handedOut.ports[p] = true
			return p
		}
	}
	t.Fatal("no free UDP port pair")
	return 0
}

// voicePacket is 20 ms of a voice-like sound (a 150 Hz voice with a moving pitch and twelve
// harmonics), G.711 µ-law.
func voicePacket(seq uint16) []byte {
	pay := make([]byte, 160)
	for i := range pay {
		n := float64(int(seq)*160 + i)
		f0 := 150 * (1 + 0.1*math.Sin(2*math.Pi*n/8000))
		ph := 2 * math.Pi * f0 * n / 8000
		v := 0.0
		for h := 1; h <= 12; h++ {
			v += math.Sin(float64(h)*ph) / float64(h)
		}
		pay[i] = media.LinearToUlaw(int16(5000 * v))
	}
	return rtpPacket(seq, pay)
}

// homeRFSS is a stand-in home RFSS: it answers INVITEs (keeping them), grants every push-to-talk
// request, and keeps the payloads it receives.
type homeRFSS struct {
	stack   *sip.Stack
	rtp     *net.UDPConn
	peer    atomic.Value // netip.AddrPort: where the console's RTP comes from
	got     chan *p25.Payload
	invited chan *sip.Message
	byes    chan *sip.Message
}

func newHomeRFSS(t *testing.T) *homeRFSS {
	h := &homeRFSS{got: make(chan *p25.Payload, 2000), invited: make(chan *sip.Message, 4), byes: make(chan *sip.Message, 4)}
	h.rtp, _ = net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	h.stack = sip.NewStack(func(tx *sip.ServerTx, in *sip.Incoming) {
		m := in.Msg
		switch m.Method {
		case sip.ACK:
			return
		case sip.INVITE:
			h.invited <- m
			r := sip.NewResponse(m, 200, "")
			r.Set("To", m.Get("To")+";tag=h1")
			r.Set("Content-Type", "application/sdp")
			r.Body = []byte(h.sdp())
			tx.Respond(r)
			return
		case sip.BYE:
			h.byes <- m
		}
		tx.Respond(sip.NewResponse(m, 200, ""))
	})
	h.stack.Host = "127.0.0.1"
	if err := h.stack.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	h.stack.Serve(t.Context())
	t.Cleanup(func() { h.stack.Close(); h.rtp.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := h.rtp.ReadFromUDPAddrPort(buf)
			if err != nil {
				return
			}
			h.peer.Store(from)
			p, err := p25.Parse(buf[12:n])
			if err != nil {
				continue
			}
			if p.Packet.Type == p25.PTTRequest {
				h.send(from, (&p25.Payload{Packet: p25.PacketType{Type: p25.PTTGrant, TSN: p.Packet.TSN}}).Marshal())
			}
			h.got <- p
		}
	}()
	return h
}

func (h *homeRFSS) sdp() string {
	return "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=TIA-P25-Groupcall\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio " +
		strconv.Itoa(h.rtp.LocalAddr().(*net.UDPAddr).Port) + " RTP/AVP 100\r\na=rtpmap:100 X-TIA-P25-IMBE/8000\r\n"
}

func (h *homeRFSS) send(to netip.AddrPort, payload []byte) {
	pkt := make([]byte, 12)
	pkt[0], pkt[1] = 0x80, 100
	_, _ = h.rtp.WriteToUDPAddrPort(append(pkt, payload...), to)
}

// waitPeer waits for the console's first RTP packet (its heartbeat query).
func (h *homeRFSS) waitPeer(t *testing.T) netip.AddrPort {
	for i := 0; i < 60; i++ {
		if p, ok := h.peer.Load().(netip.AddrPort); ok {
			return p
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no RTP from the console")
	return netip.AddrPort{}
}
