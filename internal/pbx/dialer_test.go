package pbx

import (
	"context"
	"encoding/binary"
	"math"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/sdp"
	"github.com/biodream-llc/bitvoice/internal/sip"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// memDialer keeps contacts and the do-not-call list in memory.
type memDialer struct {
	mu  sync.Mutex
	cs  map[string]store.DialContact
	dnc map[string]bool
}

func (m *memDialer) DueDialContacts(campaign string, now time.Time, n int) ([]store.DialContact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.DialContact
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		c, ok := m.cs[id]
		if ok && c.Campaign == campaign && (c.State == "pending" || c.State == "retry" && !c.Next.After(now)) && len(out) < n {
			out = append(out, c)
		}
	}
	return out, nil
}
func (m *memDialer) PutDialContact(c store.DialContact) error {
	m.mu.Lock()
	m.cs[c.ID] = c
	m.mu.Unlock()
	return nil
}
func (m *memDialer) DialContactByID(_, id string) (store.DialContact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cs[id], nil
}
func (m *memDialer) OnDNC(n string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dnc[store.NormalNumber(n)]
}
func (m *memDialer) AddDNC(_ string, ns ...string) error {
	m.mu.Lock()
	for _, n := range ns {
		m.dnc[store.NormalNumber(n)] = true
	}
	m.mu.Unlock()
	return nil
}
func (m *memDialer) ResetDialling() error { return nil }
func (m *memDialer) get(id string) store.DialContact {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cs[id]
}

// talker is a phone company whose called parties answer and speak: a person says a short hello and
// waits; a machine plays a long greeting. Which one is by the number called.
type talker struct {
	stack   *sip.Stack
	invites chan string
}

func newTalker(t *testing.T, machine func(number string) bool) *talker {
	return newTalkerOpt(t, machine, nil)
}

// newTalkerOpt: optOut numbers answer a moment late and press 9 while the message plays.
func newTalkerOpt(t *testing.T, machine func(number string) bool, optOut func(number string) bool) *talker {
	tk := &talker{invites: make(chan string, 16)}
	tk.stack = sip.NewStack(func(tx *sip.ServerTx, in *sip.Incoming) {
		if tx == nil {
			return
		}
		m := in.Msg
		if m.Method != sip.INVITE {
			tx.Respond(sip.NewResponse(m, 200, ""))
			return
		}
		u, _ := sip.ParseURI(m.RequestURI)
		number := u.User
		tk.invites <- number
		offer, err := sdp.Parse(m.Body)
		if err != nil || offer.Audio() == nil {
			tx.Respond(sip.NewResponse(m, 488, ""))
			return
		}
		ip, _ := netip.ParseAddr(offer.Connection(offer.Audio()))
		dst := netip.AddrPortFrom(ip, uint16(offer.Audio().Port))
		conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		t.Cleanup(func() { conn.Close() })
		press := optOut != nil && optOut(number)
		if press {
			time.Sleep(1500 * time.Millisecond)
		}
		r := sip.NewResponse(m, 200, "")
		r.Set("To", m.Get("To")+";tag=far")
		r.Set("Contact", "<sip:far@127.0.0.1:"+strconv.Itoa(tk.stack.Port())+">")
		r.Set("Content-Type", "application/sdp")
		r.Body = sdp.New("127.0.0.1", conn.LocalAddr().(*net.UDPAddr).Port, 1, []sdp.Codec{{PT: 0, Name: "PCMU", ClockRate: 8000}}, 101).Bytes()
		tx.Respond(r)
		speech := 400 * time.Millisecond
		if machine(number) {
			speech = 3 * time.Second
		}
		go func() {
			for i := 0; i < 250; i++ { // five seconds: speech, then quiet
				pkt := make([]byte, 172)
				pkt[0] = 0x80
				binary.BigEndian.PutUint16(pkt[2:], uint16(i))
				binary.BigEndian.PutUint32(pkt[4:], uint32(i*160))
				loud := time.Duration(i)*20*time.Millisecond < speech
				for j := 0; j < 160; j++ {
					v := 0.0
					if loud {
						v = 9000 * math.Sin(2*math.Pi*300*float64(i*160+j)/8000)
					}
					pkt[12+j] = media.LinearToUlaw(int16(v))
				}
				if press && i >= 200 && i < 207 { // a 9, as RFC 4733 events, four seconds in (while the message plays)
					ev := make([]byte, 16)
					ev[0], ev[1] = 0x80, 101
					binary.BigEndian.PutUint16(ev[2:], uint16(i))
					binary.BigEndian.PutUint32(ev[4:], 200*160)
					ev[12], ev[13] = 9, 10
					if i >= 204 {
						ev[13] |= 0x80
					}
					binary.BigEndian.PutUint16(ev[14:], uint16((i-199)*160))
					_, _ = conn.WriteToUDPAddrPort(ev, dst)
				}
				if _, err := conn.WriteToUDPAddrPort(pkt, dst); err != nil {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
		}()
	})
	tk.stack.Host = "127.0.0.1"
	if err := tk.stack.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	tk.stack.Serve(ctx)
	t.Cleanup(func() { cancel(); tk.stack.Close() })
	return tk
}

func waitContact(t *testing.T, m *memDialer, id string, ok func(store.DialContact) bool) store.DialContact {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if c := m.get(id); ok(c) {
			return c
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("contact %s: %+v", id, m.get(id))
	return store.DialContact{}
}

// A progressive campaign calls one contact per free agent: a person who answers is put through to
// the agent, with the campaign's caller ID; an answering machine is told apart and hung up on (to be
// tried again); a number on the do-not-call list is never called.
func TestDialerProgressive(t *testing.T) {
	tk := newTalker(t, func(n string) bool { return strings.HasSuffix(n, "2222") })
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{{ID: "t1", Name: "Carrier", Enabled: true, Host: "127.0.0.1", Port: tk.stack.Port()}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "All", Patterns: []string{"NXXNXXXXXX"}, Trunks: []string{"t1"}}}
	cfg.Queues = []config.Queue{{Number: "700", Name: "Sales", Agents: []string{"101"}, RingSeconds: 10}}
	cfg.Campaigns = []config.Campaign{{ID: "k1", Name: "Renewals", Mode: "progressive", Queue: "700", Active: true, CallerID: "2125550199",
		Hours: []config.OpenHours{{Days: []int{0, 1, 2, 3, 4, 5, 6}, Start: "00:00", End: "00:00"}}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	m := &memDialer{cs: map[string]store.DialContact{
		"a": {ID: "a", Campaign: "k1", Number: "3125551111", Name: "Ann Smith", State: "pending"},
		"b": {ID: "b", Campaign: "k1", Number: "3125552222", Name: "Machine", State: "pending"},
		"c": {ID: "c", Campaign: "k1", Number: "3125553333", Name: "Do not call", State: "pending"},
	}, dnc: map[string]bool{"3125553333": true}}
	e, _ := startEngine(t, cfg, func(e *Engine) { e.Dialer = m })
	agent := registered(t, e, "101")[0]
	ring := agent.waitRing(10 * time.Second)
	if ring == nil {
		t.Fatal("the agent was never rung")
	}
	if !strings.Contains(ring.Req.Get("From"), "3125551111") && !strings.Contains(ring.Req.Get("From"), "Ann Smith") {
		t.Fatalf("the agent does not see the contact: %s", ring.Req.Get("From"))
	}
	ad := agent.answer(ring)
	c := waitContact(t, m, "a", func(c store.DialContact) bool { return c.State == "done" })
	if c.Result != "answered" || c.Attempts != 1 {
		t.Fatalf("contact a %+v", c)
	}
	b := waitContact(t, m, "b", func(c store.DialContact) bool { return c.Result != "" })
	if b.Result != "machine" || b.State != "retry" {
		t.Fatalf("the machine: %+v", b)
	}
	// The agent hangs up; the next contact due is on the list: it is marked, never dialled.
	agent.send(ad.request(sip.BYE))
	waitContact(t, m, "c", func(c store.DialContact) bool { return c.State == "dnc" })
	close(tk.invites)
	for n := range tk.invites {
		if n == "3125553333" {
			t.Fatal("a do-not-call number was dialled")
		}
	}
}

// A power campaign with more calls than agents: one person reaches the agent; another, answered with
// no agent free, hears the message (abandoned, counted); a third presses 9 during it and is put on the
// do-not-call list.
func TestDialerAbandonAndOptOut(t *testing.T) {
	tk := newTalkerOpt(t, func(string) bool { return false }, func(n string) bool { return strings.HasSuffix(n, "12") })
	cfg := testConfig()
	cfg.Trunks = []config.Trunk{{ID: "t1", Name: "Carrier", Enabled: true, Host: "127.0.0.1", Port: tk.stack.Port()}}
	cfg.OutboundRoutes = []config.OutboundRoute{{Name: "All", Patterns: []string{"NXXNXXXXXX"}, Trunks: []string{"t1"}}}
	cfg.Queues = []config.Queue{{Number: "700", Name: "Sales", Agents: []string{"101"}, RingSeconds: 10}}
	cfg.Campaigns = []config.Campaign{{ID: "k2", Name: "Survey", Mode: "power", Ratio: 3, Queue: "700", Active: true,
		Hours: []config.OpenHours{{Days: []int{0, 1, 2, 3, 4, 5, 6}, Start: "00:00", End: "00:00"}}}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	m := &memDialer{cs: map[string]store.DialContact{}, dnc: map[string]bool{}}
	for i, id := range []string{"a", "b", "c"} {
		m.cs[id] = store.DialContact{ID: id, Campaign: "k2", Number: "31255500" + strconv.Itoa(10+i), State: "pending"}
	}
	e, _ := startEngine(t, cfg, func(e *Engine) { e.Dialer = m })
	agent := registered(t, e, "101")[0]
	ring := agent.waitRing(10 * time.Second)
	if ring == nil {
		t.Fatal("the agent was never rung")
	}
	agent.answer(ring)
	c := waitContact(t, m, "c", func(c store.DialContact) bool { return c.State == "dnc" })
	if c.Result != "opted out" || !m.OnDNC("3125550012") {
		t.Fatalf("opt-out: %+v", c)
	}
	results := map[string]int{}
	for _, id := range []string{"a", "b"} {
		results[waitContact(t, m, id, func(c store.DialContact) bool { return c.Result != "" }).Result]++
	}
	if results["answered"] != 1 || results["abandoned"] != 1 {
		t.Fatalf("results %v", results)
	}
	if st := e.DialerStatuses(); len(st) != 1 || st[0].AbandonRate < 30 {
		t.Fatalf("status %+v", st)
	}
}

// AMD on its own: a long greeting is a machine; a hello and quiet is a person.
func TestAnsweringMachineDetection(t *testing.T) {
	for _, tc := range []struct {
		speech  time.Duration
		machine bool
	}{{400 * time.Millisecond, false}, {3 * time.Second, true}, {0, false}} {
		e, _ := startEngine(t, testConfig())
		ep, _ := e.NewMediaEndpoint()
		peer, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		ep.SetRemote("127.0.0.1", peer.LocalAddr().(*net.UDPAddr).Port)
		ep.Start()
		c := &Call{e: e, caller: &Leg{e: e, ep: ep}, cancelled: make(chan struct{})}
		go func() {
			dst := netip.MustParseAddrPort("127.0.0.1:" + strconv.Itoa(ep.Port()))
			for i := 0; i < 200; i++ {
				pkt := make([]byte, 172)
				pkt[0] = 0x80
				binary.BigEndian.PutUint16(pkt[2:], uint16(i))
				for j := 0; j < 160; j++ {
					v := 0.0
					if time.Duration(i)*20*time.Millisecond < tc.speech {
						v = 9000 * math.Sin(2*math.Pi*300*float64(i*160+j)/8000)
					}
					pkt[12+j] = media.LinearToUlaw(int16(v))
				}
				peer.WriteToUDPAddrPort(pkt, dst)
				time.Sleep(20 * time.Millisecond)
			}
		}()
		if got := c.detectMachine(); got != tc.machine {
			t.Errorf("%s of speech: machine %v", tc.speech, got)
		}
		ep.Close()
		peer.Close()
	}
}
