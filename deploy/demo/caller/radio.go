package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/p25"
	"github.com/biodream-llc/bitvoice/internal/p25/imbe"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// The demo's radio system: a stand-in P25 home RFSS on this computer, for the console's talkgroup
// (deploy/demo/seed sets the channel up). Now and then one of the invented units talks on it, as a
// real radio's transmission arrives over the ISSI (IMBE voice, the unit in each PTT word); once in a
// while one presses its emergency button. The units also report positions, which BitVoice puts on
// the map. BitVoice turns all of it into radio events for the console and the CAD.

// The P25 identity the seed gives the demo's radio system.
const (
	demoWACN   = 0xBEE07
	demoSystem = 0x1D5
	demoGroup  = 0x0911
)

// unit is an invented radio, by ID (the seed names them with the same aliases).
type unit struct {
	id       uint32
	lat, lon float64 // where it starts
}

var units = []unit{
	{1207, 40.2790, -76.8890}, // Engine 7
	{1212, 40.2652, -76.8706}, // Ladder 12
	{1303, 40.2601, -76.8835}, // Medic 3
	{1310, 40.2735, -76.9005}, // Medic 10
	{1401, 40.2560, -76.8441}, // Battalion 1
}

type rfss struct {
	log   *slog.Logger
	stack *sip.Stack
	rtp   *net.UDPConn

	mu      sync.Mutex
	console netip.AddrPort // where the console's RTP comes from (its heartbeats)
	seq     uint16
	ts      uint32
	tsn     int
	ssrc    uint32
	busy    bool // a unit (or the dispatcher) is on the air
	granted bool // the dispatcher has the floor
}

func newRFSS(log *slog.Logger, sipAddr string) (*rfss, error) {
	r := &rfss{log: log, ssrc: rand.Uint32(), tsn: 1}
	var err error
	if r.rtp, err = net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}); err != nil {
		return nil, err
	}
	r.stack = sip.NewStack(r.handle)
	r.stack.Host = "127.0.0.1"
	if err := r.stack.Listen(sipAddr); err != nil {
		r.rtp.Close()
		return nil, err
	}
	return r, nil
}

func (r *rfss) serve(ctx context.Context) {
	r.stack.Serve(ctx)
	go func() { <-ctx.Done(); r.stack.Close(); r.rtp.Close() }()
	go r.readRTP()
}

// handle answers the console's group call (and its end).
func (r *rfss) handle(tx *sip.ServerTx, in *sip.Incoming) {
	if tx == nil {
		return
	}
	m := in.Msg
	switch m.Method {
	case sip.INVITE:
		ok := sip.NewResponse(m, 200, "")
		if to, _ := sip.ParseAddress(m.Get("To")); to != nil && to.Tag() == "" {
			ok.Set("To", m.Get("To")+";tag="+sip.NewTag())
		}
		ok.Set("Contact", "<sip:rfss@127.0.0.1:"+strconv.Itoa(r.stack.Port())+">")
		ok.Set("Content-Type", "application/sdp")
		ok.Body = []byte("v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=TIA-P25-Groupcall\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio " +
			strconv.Itoa(r.rtp.LocalAddr().(*net.UDPAddr).Port) + " RTP/AVP 100\r\na=rtpmap:100 X-TIA-P25-IMBE/8000\r\n")
		tx.Respond(ok)
		r.log.Info("radio: the console joined the talkgroup")
	default:
		tx.Respond(sip.NewResponse(m, 200, ""))
	}
}

// readRTP answers the console: heartbeats back (so it knows the home is there), and its dispatcher's
// requests to talk granted while no unit is on the air.
func (r *rfss) readRTP() {
	buf := make([]byte, 2048)
	for {
		n, from, err := r.rtp.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		if n < 12 {
			continue
		}
		p, err := p25.Parse(buf[12:n])
		if err != nil {
			continue
		}
		r.mu.Lock()
		r.console = from
		busy := r.busy
		switch p.Packet.Type {
		case p25.PTTRequest:
			if !busy {
				r.busy, r.granted = true, true
			}
		case p25.PTTEnd:
			if r.granted {
				r.busy, r.granted = false, false
			}
		}
		r.mu.Unlock()
		switch p.Packet.Type {
		case p25.Heartbeat:
			r.send(&p25.Payload{Packet: p25.PacketType{Type: p25.Heartbeat, Interval: p.Packet.Interval}})
		case p25.PTTRequest:
			t := p25.PTTGrant
			if busy {
				t = p25.PTTWait
			}
			r.send(&p25.Payload{Packet: p25.PacketType{Type: t, TSN: p.Packet.TSN}})
		}
	}
}

func (r *rfss) send(p *p25.Payload) {
	r.mu.Lock()
	to := r.console
	r.seq++
	pkt := make([]byte, 12, 64)
	pkt[0], pkt[1] = 0x80, 100
	binary.BigEndian.PutUint16(pkt[2:], r.seq)
	binary.BigEndian.PutUint32(pkt[4:], r.ts)
	binary.BigEndian.PutUint32(pkt[8:], r.ssrc)
	r.mu.Unlock()
	if to.IsValid() {
		_, _ = r.rtp.WriteToUDPAddrPort(append(pkt, p.Marshal()...), to)
	}
}

// transmit puts unit u on the air for d, as a radio's transmission arrives over the ISSI: the
// header, then IMBE voice frames in superframe order, each with the unit's PTT word, then the end.
// It reports false when the talkgroup is busy or the console is not there.
func (r *rfss) transmit(ctx context.Context, u uint32, d time.Duration, emergency bool) bool {
	r.mu.Lock()
	if r.busy || !r.console.IsValid() {
		r.mu.Unlock()
		return false
	}
	r.busy = true
	r.tsn = r.tsn%127 + 1
	tsn := r.tsn
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.busy = false
		r.mu.Unlock()
	}()
	opt := p25.ServiceOptions{Emergency: emergency, Priority: 4}
	word := &p25.PTTWord{Unit: p25.Unit{WACN: demoWACN, System: demoSystem, ID: u}, Priority: 4}
	if emergency {
		word.Priority = 200 // a larger number wins the floor
	}
	pkt := func(t int) p25.PacketType { return p25.PacketType{Type: t, TSN: tsn, Options: opt} }
	r.send(&p25.Payload{Packet: pkt(p25.PTTStart), PTT: word})
	enc := imbe.NewEncoder()
	voice := newTalk(u)
	frames := int(d / (20 * time.Millisecond))
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for i := range frames {
		select {
		case <-ctx.Done():
			return true
		case <-t.C:
		}
		r.mu.Lock()
		r.ts += 160
		r.mu.Unlock()
		if i == 0 {
			h := p25.HeaderWord{AlgID: p25.AlgClear, Group: demoGroup, VBB: 2}
			r.send(&p25.Payload{S: true, Packet: pkt(p25.PTTProgress), PTT: word, Header: &h})
		}
		f := p25.ClearVoiceFrame(i, enc.Encode(voice.next()))
		r.send(&p25.Payload{S: true, Packet: pkt(p25.PTTProgress), PTT: word, Voice: []p25.IMBEBlock{f}})
	}
	end := &p25.Payload{Packet: pkt(p25.PTTEnd)}
	r.send(end)
	r.send(end) // twice, as equipment does, against a loss
	return true
}

// talk is a voice-like sound for a transmission (no words: the demo makes no speech): a pitch that
// moves like speech, with harmonics, in syllables. Each unit has its own voice.
type talk struct {
	n     int
	pitch float64
	phase float64
	rng   *rand.Rand
}

func newTalk(u uint32) *talk {
	return &talk{pitch: 105 + float64(u%7)*12, rng: rand.New(rand.NewPCG(uint64(u), uint64(time.Now().UnixNano())))}
}

func (t *talk) next() []int16 {
	pcm := make([]int16, 160)
	for i := range pcm {
		s := float64(t.n) / sampleRate
		f := t.pitch * (1 + 0.12*math.Sin(2*math.Pi*1.7*s) + 0.05*math.Sin(2*math.Pi*0.6*s))
		t.phase += 2 * math.Pi * f / sampleRate
		syll := math.Max(0, math.Sin(2*math.Pi*3.2*s)) // syllables, about three a second
		var v float64
		for h := 1; h <= 10; h++ {
			v += math.Sin(float64(h)*t.phase) / float64(h)
		}
		pcm[i] = int16(5500 * syll * v)
		t.n++
	}
	return pcm
}

// radioTraffic makes the units talk now and then, and report where they are. Every eighth
// transmission is an emergency.
func radioTraffic(ctx context.Context, log *slog.Logger, r *rfss, every time.Duration, api *apiClient) {
	pos := make([]unit, len(units))
	copy(pos, units)
	k := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(every/2 + time.Duration(rand.Int64N(int64(every)))):
		}
		u := units[k%len(units)]
		emergency := k%8 == 7
		k++
		if r.transmit(ctx, u.id, time.Duration(2500+rand.IntN(3500))*time.Millisecond, emergency) {
			log.Info("radio: a unit talked", "unit", u.id, "emergency", emergency)
		}
		if api != nil {
			// Positions: each unit drifts a little, as a crew moving about the city.
			var fixes []map[string]any
			for i := range pos {
				pos[i].lat += (rand.Float64() - 0.5) * 0.002
				pos[i].lon += (rand.Float64() - 0.5) * 0.002
				fixes = append(fixes, map[string]any{"unit": pos[i].id, "lat": pos[i].lat, "lon": pos[i].lon, "accuracy": 10,
					"speed": rand.IntN(50), "heading": rand.IntN(360), "source": "demo radio system"})
			}
			if err := api.post("/api/radio/locations", fixes); err != nil {
				log.Warn("radio positions", "err", err)
			}
		}
	}
}

// apiClient posts to BitVoice's API on this computer with a token.
type apiClient struct {
	base, token string
	http        *http.Client
}

func newAPI(base, token string) *apiClient {
	return &apiClient{base: base, token: token, http: &http.Client{Timeout: 10 * time.Second,
		// BitVoice's own address, on this computer: its certificate names the public name.
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}}
}

func (a *apiClient) post(path string, v any) error {
	b, _ := json.Marshal(v)
	req, _ := http.NewRequest(http.MethodPost, a.base+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return &httpError{resp.Status}
	}
	return nil
}

type httpError struct{ status string }

func (e *httpError) Error() string { return "BitVoice said " + e.status }
