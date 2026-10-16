package pbx

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/p25"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// P25 talkgroups on the radio console: each is an ISSI group call (package p25) with the
// talkgroup's home RFSS. Inside, the channel is a bridge party like a RoIP gateway's, fed by an
// in-process gateway: the talkgroup's voice, decoded, comes into the bridge as G.711; the bridge's
// audio for the channel goes out as IMBE while a dispatcher's push-to-talk is granted.

type p25Channel struct {
	group      *p25.Group
	rtp        *net.UDPConn // the ISSI's RTP
	gw         *net.UDPConn // the in-process gateway's side of the bridge party
	cancel     context.CancelFunc
	up         bool
	why        string
	talker     string
	talkerName string // its alias
	enc        string // the talker's encryption ("AES-256"); empty when clear
	noKey      bool   // encrypted with a key this console does not have
	odd        bool   // voice in blocks this console does not decode (Phase 2 half-rate, most likely)
	txEnc      string // what dispatchers transmit with ("AES-256"; empty: clear)
}

// p25Keys turns the console's traffic keys into the p25 package's, with the channel's transmit key.
func p25Keys(pc config.P25Console, txKey string) (keys []p25.Key, tx *p25.Key) {
	for _, k := range pc.Keys {
		alg := map[string]int{"aes256": p25.AlgAES256, "des": p25.AlgDES, "tdes": p25.AlgTDES}[k.Alg]
		b, err := hex.DecodeString(k.Key)
		if err != nil || alg == 0 {
			continue
		}
		keys = append(keys, p25.Key{Alg: alg, ID: k.KeyID, Bytes: b})
		if k.ID == txKey {
			c := keys[len(keys)-1]
			tx = &c
		}
	}
	return keys, tx
}

// issiStack is the console's SIP stack for the ISSI, on its own port; requests go to the group
// they name.
func (e *Engine) issiStack(r *radio, cfg config.P25Console) (*sip.Stack, error) {
	if r.issi != nil && r.issiPort == cfg.SIPPort {
		return r.issi, nil
	}
	if r.issi != nil {
		r.issi.Close()
		r.issi = nil
	}
	s := sip.NewStack(func(tx *sip.ServerTx, in *sip.Incoming) {
		r.mu.Lock()
		var groups []*p25.Group
		for _, ch := range r.channels {
			if ch.p25 != nil {
				groups = append(groups, ch.p25.group)
			}
		}
		r.mu.Unlock()
		for _, g := range groups {
			if g.Handle(tx, in) {
				return
			}
		}
		if in.Msg.Method == sip.INVITE && e.incomingUnitCall(r, tx, in) {
			return
		}
		if in.Msg.Method != sip.ACK {
			tx.Respond(sip.NewResponse(in.Msg, 404, "No Such Talkgroup"))
		}
	})
	s.Host = e.stack.Host
	host := e.pool.Host
	if err := s.Listen(net.JoinHostPort(host, strconv.Itoa(cfg.SIPPort))); err != nil {
		return nil, err
	}
	s.Serve(e.ctx)
	r.issi, r.issiPort = s, cfg.SIPPort
	return s, nil
}

func parseHex(s string) uint32 {
	v, _ := strconv.ParseUint(s, 16, 32)
	return uint32(v)
}

// openP25 sets up a talkgroup channel: its bridge party, its ISSI group call.
func (e *Engine) openP25(r *radio, ch *radioChannel, cfg *config.Config) error {
	pc := cfg.System.Radio.P25
	stack, err := e.issiStack(r, pc)
	if err != nil {
		return errors.New("the ISSI's SIP port " + strconv.Itoa(pc.SIPPort) + " cannot be opened: " + err.Error())
	}
	rtp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(e.pool.Host), Port: ch.cfg.Port})
	if err != nil {
		return errors.New("port " + strconv.Itoa(ch.cfg.Port) + " cannot be opened: " + err.Error())
	}
	ep, err := e.pool.Allocate()
	if err != nil {
		rtp.Close()
		return err
	}
	gw, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		rtp.Close()
		ep.Close()
		return err
	}
	ep.SetRemote("127.0.0.1", gw.LocalAddr().(*net.UDPAddr).Port)
	ep.SetPayloadType(media.PTPCMU)
	e.recordChannel(r, ch.cfg.ID, ep)
	ep.Start()
	epAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: ep.Port()}
	pc25 := &p25Channel{rtp: rtp, gw: gw}
	id := ch.cfg.ID
	var seq uint16
	var ts uint32
	keys, txKey := p25Keys(pc, ch.cfg.TxKey)
	home, homeRFSS := ch.cfg.Home, ch.cfg.HomeRFSS
	if ch.cfg.TxKey != "" && txKey == nil {
		rtp.Close()
		gw.Close()
		ep.Close()
		return errors.New("the transmit key is missing or not valid")
	}
	if txKey != nil {
		pc25.txEnc = p25.AlgName(txKey.Alg)
	}
	gcfg := p25.Config{WACN: parseHex(pc.WACN), System: parseHex(pc.System), RFSS: uint32(pc.RFSS), Unit: uint32(pc.Unit),
		Group: uint32(ch.cfg.Group), Home: home, HomeRFSS: uint32(homeRFSS), Priority: pc.Priority, Console: pc.Console,
		Key: txKey, Keys: keys}
	if ch.private {
		gcfg.Group, gcfg.CallUnit, gcfg.Once, gcfg.Emergency = 0, ch.unit, true, ch.emergency
	}
	g, err := p25.NewGroup(gcfg, stack, rtp, p25.Events{
		Voice: func(pcm []int16) {
			pkt := make([]byte, 12, 172)
			pkt[0], pkt[1] = 0x80, media.PTPCMU
			seq++
			ts += 160
			binary.BigEndian.PutUint16(pkt[2:], seq)
			binary.BigEndian.PutUint32(pkt[4:], ts)
			binary.BigEndian.PutUint32(pkt[8:], 0x25)
			for _, s := range pcm {
				pkt = append(pkt, media.LinearToUlaw(s))
			}
			_, _ = gw.WriteToUDP(pkt, epAddr)
		},
		Talker: func(t *p25.Talker) {
			r.mu.Lock()
			var ev *RadioUnitEvent
			if c := r.channels[id]; c != nil && c.p25 != nil && t != nil && (c.p25.talker == "" || !strings.HasPrefix(c.p25.talker, t.Unit.String()) ||
				t.Emergency && !strings.HasSuffix(c.p25.talker, " EMERGENCY")) {
				ev = &RadioUnitEvent{Channel: id, ChannelName: c.cfg.Name, Unit: t.Unit.String(), UnitID: int(t.Unit.ID), Alias: e.Config().System.Radio.AliasFor(int(t.Unit.ID)),
					WACN: fmt.Sprintf("%05X", t.Unit.WACN), System: fmt.Sprintf("%03X", t.Unit.System), Emergency: t.Emergency,
					Private: c.private, At: time.Now().UTC()}
				if t.Alg != p25.AlgClear {
					ev.Encryption = p25.AlgName(t.Alg)
				}
			}
			if c := r.channels[id]; c != nil && c.p25 != nil {
				c.p25.talker, c.p25.enc, c.p25.noKey, c.p25.odd = "", "", false, false
				if t != nil {
					c.p25.odd = t.Undecodable
					if t.Alg != p25.AlgClear {
						c.p25.enc, c.p25.noKey = p25.AlgName(t.Alg), t.NoKey
					}
					c.p25.talker = t.Unit.String()
					c.p25.talkerName = e.Config().System.Radio.AliasFor(int(t.Unit.ID))
					if t.Emergency {
						c.p25.talker += " EMERGENCY"
						if c.emUnit != t.Unit.String() || c.emAt.IsZero() {
							c.emUnit, c.emAlias, c.emAt = t.Unit.String(), c.p25.talkerName, time.Now()
						}
					}
				}
			}
			r.mu.Unlock()
			if ev != nil {
				e.publish("radio.unit", *ev) // for CAD: who is talking where
				if ev.Emergency {
					e.Log.Warn("radio emergency", "unit", ev.UnitID, "channel", ev.ChannelName)
					e.publish("radio.emergency", *ev)
				}
			}
			e.publish("radio.updated", nil)
		},
		Grant: func(ok bool, why string) {
			if !ok {
				// The floor went to someone else: the dispatcher's key is let go and told why.
				r.mu.Lock()
				if c := r.channels[id]; c != nil {
					if p := r.positions[c.tx]; p != nil {
						delete(p.ptt, id)
					}
					c.tx, c.notice = "", "not granted: "+why
				}
				r.mu.Unlock()
			}
			e.publish("radio.updated", nil)
		},
		State: func(up bool, why string) {
			r.mu.Lock()
			if c := r.channels[id]; c != nil && c.p25 != nil {
				c.p25.up, c.p25.why = up, why
			}
			r.mu.Unlock()
			if !up && ch.private {
				go e.endPrivate(id) // a private call is gone once it ends
			} else if !up {
				e.Log.Warn("talkgroup down", "channel", ch.cfg.Name, "why", why)
			}
			e.publish("radio.updated", nil)
		},
	})
	if err != nil {
		rtp.Close()
		gw.Close()
		ep.Close()
		return err
	}
	pc25.group = g
	ch.ep, ch.p25 = ep, pc25
	// The bridge's audio for the channel, 20 ms at a time, goes out while the floor is granted.
	go func() {
		buf := make([]byte, 2048)
		var pcm []int16
		for {
			n, _, err := gw.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n <= 12 || buf[1]&0x7f != media.PTPCMU {
				continue
			}
			if !g.Transmitting() {
				pcm = pcm[:0]
				continue
			}
			for _, b := range buf[12:n] {
				pcm = append(pcm, media.UlawToLinear(b))
			}
			for len(pcm) >= 160 {
				g.Speak(pcm[:160])
				pcm = append(pcm[:0], pcm[160:]...)
			}
		}
	}()
	ctx, cancel := context.WithCancel(e.ctx)
	pc25.cancel = cancel
	if !ch.incoming {
		g.Start(ctx)
	}
	r.bridge.Add("ch:"+id, ep) // P25 sends frames for the whole transmission, silence included
	return nil
}

// unitHome is where private calls go: the configured home of the radios, else the first talkgroup's.
func unitHome(cfg *config.Config) (string, int) {
	pc := cfg.System.Radio.P25
	if pc.UnitHome != "" {
		return pc.UnitHome, pc.UnitHomeRFSS
	}
	for _, ch := range cfg.System.Radio.Channels {
		if ch.Kind == "p25" {
			return ch.Home, ch.HomeRFSS
		}
	}
	return "", 0
}

// UnitCall starts a private call from a dispatcher's position to a radio. The call is a channel
// of its own on the console until either side hangs up; the dispatcher talks on it with push-to-talk.
func (e *Engine) UnitCall(call string, unit int, emergency bool) (string, error) {
	if unit < 1 || unit > 0xffffff {
		return "", errors.New("a unit ID is 1 to 16777215")
	}
	cfg := e.Config()
	home, homeRFSS := unitHome(cfg)
	if home == "" {
		return "", errors.New("set up the console on a P25 system first (a talkgroup channel, or the radios' home RFSS)")
	}
	r := e.radioSys()
	r.mu.Lock()
	p := r.positions[call]
	id := "unit-" + strconv.Itoa(unit)
	_, busy := r.channels[id]
	r.mu.Unlock()
	if p == nil {
		return "", errors.New("join the console first")
	}
	if busy {
		return id, nil // already in a call with this unit
	}
	ch := &radioChannel{cfg: config.RadioChannel{ID: id, Name: "Unit " + strconv.Itoa(unit), Kind: "p25", Home: home, HomeRFSS: homeRFSS,
		TxKey: cfg.System.Radio.P25.UnitTxKey, Color: "violet"}, private: true, unit: uint32(unit), emergency: emergency}
	r.mu.Lock()
	r.channels[id] = ch
	if p.monitor != nil {
		p.monitor[id] = true
	}
	r.mu.Unlock()
	if err := e.openP25(r, ch, cfg); err != nil {
		e.endPrivate(id)
		return "", err
	}
	e.publish("radio.updated", nil)
	return id, nil
}

// RadioUnitEvent says a radio began talking on a channel (radio.unit), or raised an emergency
// (radio.emergency): what CAD wants to know about units on the air.
type RadioUnitEvent struct {
	Channel     string    `json:"channel"`
	ChannelName string    `json:"channelName"`
	Unit        string    `json:"unit"`   // WACN.system.unit in hex, as P25 writes it
	UnitID      int       `json:"unitId"` // the unit ID in decimal, as radios show it
	WACN        string    `json:"wacn"`
	System      string    `json:"system"`
	Emergency   bool      `json:"emergency"`
	Alias       string    `json:"alias,omitempty"` // the radio's name on the console
	Encryption  string    `json:"encryption,omitempty"`
	Private     bool      `json:"private,omitempty"` // a unit-to-unit call to the console
	At          time.Time `json:"at"`
}

// incomingUnitCall answers a radio's private call to the console's unit ID.
func (e *Engine) incomingUnitCall(r *radio, tx *sip.ServerTx, in *sip.Incoming) bool {
	cfg := e.Config()
	pc := cfg.System.Radio.P25
	u, err := sip.ParseURI(in.Msg.RequestURI)
	want := fmt.Sprintf("%05x%03x%06x", parseHex(pc.WACN), parseHex(pc.System), pc.Unit)
	if err != nil || !strings.EqualFold(u.User, want) {
		return false
	}
	from, err := sip.ParseURI(strings.Trim(strings.SplitN(in.Msg.Get("From"), ";tag=", 2)[0], " <>"))
	if err != nil || len(from.User) != 14 {
		return false
	}
	unit, err := strconv.ParseUint(from.User[8:], 16, 32)
	if err != nil || unit == 0 {
		return false
	}
	id := "unit-" + strconv.FormatUint(unit, 10)
	r.mu.Lock()
	_, busy := r.channels[id]
	r.mu.Unlock()
	if busy {
		tx.Respond(sip.NewResponse(in.Msg, 486, "Busy Here"))
		return true
	}
	home, homeRFSS := unitHome(cfg)
	if home == "" {
		home = in.From.Addr.String()
	}
	ch := &radioChannel{cfg: config.RadioChannel{ID: id, Name: "Unit " + strconv.FormatUint(unit, 10), Kind: "p25", Home: home, HomeRFSS: homeRFSS,
		TxKey: pc.UnitTxKey, Color: "violet"}, private: true, unit: uint32(unit), incoming: true,
		emergency: strings.HasSuffix(strings.TrimSpace(in.Msg.Get("Priority")), ";e")}
	if ch.emergency {
		ch.emUnit, ch.emAlias, ch.emAt = fmt.Sprintf("%s.%s.%06x", strings.ToLower(pc.WACN), strings.ToLower(pc.System), unit), cfg.System.Radio.AliasFor(int(unit)), time.Now()
	}
	r.mu.Lock()
	r.channels[id] = ch
	for _, p := range r.positions {
		if p.monitor != nil {
			p.monitor[id] = true // every dispatcher hears a radio calling in
		}
	}
	r.mu.Unlock()
	if err := e.openP25(r, ch, cfg); err != nil {
		e.endPrivate(id)
		tx.Respond(sip.NewResponse(in.Msg, 503, "Service Unavailable"))
		return true
	}
	if !ch.p25.group.Handle(tx, in) {
		e.endPrivate(id)
		tx.Respond(sip.NewResponse(in.Msg, 500, ""))
		return true
	}
	ch.p25.group.Start(e.ctx)
	e.Log.Info("private call from a radio", "unit", unit, "emergency", ch.emergency)
	ev := RadioUnitEvent{Channel: id, ChannelName: ch.cfg.Name, Unit: fmt.Sprintf("%s.%s.%06x", strings.ToLower(pc.WACN), strings.ToLower(pc.System), unit),
		UnitID: int(unit), WACN: strings.ToUpper(pc.WACN), System: strings.ToUpper(pc.System), Emergency: ch.emergency, Private: true, At: time.Now().UTC(),
		Alias: e.Config().System.Radio.AliasFor(int(unit))}
	e.publish("radio.unit", ev)
	if ev.Emergency {
		e.publish("radio.emergency", ev)
	}
	e.publish("radio.updated", nil)
	return true
}

// EndUnitCall hangs up a private call.
func (e *Engine) EndUnitCall(channel string) error {
	r := e.radioSys()
	r.mu.Lock()
	ch := r.channels[channel]
	r.mu.Unlock()
	if ch == nil || !ch.private {
		return errors.New("no such private call")
	}
	e.endPrivate(channel)
	return nil
}

// endPrivate closes a private call's channel.
func (e *Engine) endPrivate(id string) {
	r := e.radioSys()
	r.mu.Lock()
	ch := r.channels[id]
	if ch == nil || !ch.private {
		r.mu.Unlock()
		return
	}
	delete(r.channels, id)
	for _, p := range r.positions {
		delete(p.ptt, id)
		if p.monitor != nil {
			delete(p.monitor, id)
		}
	}
	for pr := range r.patches {
		if pr[0] == id || pr[1] == id {
			delete(r.patches, pr)
		}
	}
	r.mu.Unlock()
	r.bridge.Remove("ch:" + id)
	if ch.p25 != nil {
		ch.p25.close()
	}
	if ch.ep != nil {
		ch.ep.Close()
	}
	e.publish("radio.updated", nil)
}

func (c *p25Channel) close() {
	c.cancel()
	time.Sleep(10 * time.Millisecond) // the BYE goes before the sockets close
	c.gw.Close()
	c.rtp.Close()
}
