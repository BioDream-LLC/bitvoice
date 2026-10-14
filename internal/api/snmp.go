package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/snmp"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// snmpState is the running agent, started again when its address changes.
type snmpState struct {
	mu     sync.Mutex
	listen string
	agent  *snmp.Agent
	cancel context.CancelFunc
	err    string
	trunks map[string]bool // last known up/down, so a trap goes only on a change
}

// SNMP OIDs under the base: .1 scalars, .2 the trunk table, .3 the queue table, .0 traps.
func (s *Server) snmpTable() snmp.Table {
	cfg := s.config()
	base, err := snmp.ParseOID(cfg.System.SNMP.BaseOID)
	if err != nil {
		base = snmp.MustOID("1.3.6.1.4.1.8072.9999.9999.7")
	}
	up := time.Since(s.started)
	t := snmp.Table{
		{Name: snmp.SysDescr, Value: snmp.String("BitVoice " + s.Version + " phone system")},
		{Name: snmp.SysObjectID, Value: snmp.ObjectID(base)},
		{Name: snmp.SysUpTime, Value: snmp.Ticks(uint32(up / (10 * time.Millisecond)))},
		{Name: snmp.SysContact, Value: snmp.String(cfg.System.SNMP.Contact)},
		{Name: snmp.SysName, Value: snmp.String(cfg.System.Name)},
		{Name: snmp.SysLocation, Value: snmp.String(cfg.System.SNMP.Location)},
	}
	scalar := func(n uint32, v snmp.Value) { t = append(t, snmp.VarBind{Name: base.Append(1, n, 0), Value: v}) }
	scalar(1, snmp.String(s.Version))
	var active, ringing int
	trunksUp, waiting := 0, 0
	if s.Engine != nil {
		for _, c := range s.Engine.Calls() {
			active++
			if c.State == "ringing" {
				ringing++
			}
		}
		ts := s.Engine.TrunkStatus()
		for i, tr := range ts {
			isUp := tr.State == "registered" || tr.State == "reachable"
			if isUp {
				trunksUp++
			}
			row := uint32(i + 1)
			col := func(c uint32, v snmp.Value) { t = append(t, snmp.VarBind{Name: base.Append(2, 1, c, row), Value: v}) }
			col(1, snmp.Integer(int64(row)))
			col(2, snmp.String(tr.Name))
			col(3, snmp.Integer(map[bool]int64{true: 1, false: 2}[isUp])) // 1 up, 2 down, as ifOperStatus
			col(4, snmp.Gauge(uint32(max(tr.LatencyMS, 0))))
			col(5, snmp.String(tr.State))
		}
		scalar(5, snmp.Gauge(uint32(trunksUp)))
		scalar(6, snmp.Gauge(uint32(len(ts))))
		for i, q := range s.Engine.Queues() {
			waiting += len(q.Waiting)
			row := uint32(i + 1)
			col := func(c uint32, v snmp.Value) { t = append(t, snmp.VarBind{Name: base.Append(3, 1, c, row), Value: v}) }
			col(1, snmp.Integer(int64(row)))
			col(2, snmp.String(q.Name))
			col(3, snmp.Gauge(uint32(len(q.Waiting))))
			col(4, snmp.Gauge(uint32(len(q.Agents))))
			col(5, snmp.Counter(uint32(q.Totals.Answered)))
			col(6, snmp.Counter(uint32(q.Totals.Abandoned)))
		}
		scalar(4, snmp.Gauge(uint32(len(s.Engine.Registrations()))))
	}
	scalar(2, snmp.Gauge(uint32(active)))
	scalar(3, snmp.Gauge(uint32(ringing)))
	scalar(7, snmp.Gauge(uint32(waiting)))
	m := &s.metrics
	m.mu.Lock()
	var total uint64
	for _, n := range m.calls {
		total += uint64(n)
	}
	mos := 0.0
	if m.mosCount > 0 {
		mos = m.mosSum / float64(m.mosCount)
	}
	lost, talk := m.lost, 0.0
	for _, v := range m.talk {
		talk += v
	}
	m.mu.Unlock()
	scalar(8, snmp.Counter64(total))
	scalar(9, snmp.Counter64(uint64(talk)))
	scalar(10, snmp.Counter64(uint64(lost)))
	scalar(11, snmp.Gauge(uint32(mos*100)))
	return t
}

// RunSNMP keeps the agent in line with the settings, and sends traps.
func (s *Server) RunSNMP(ctx context.Context) {
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
	s.syncSNMP(ctx)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	alive := map[string]bool{}
	for {
		select {
		case <-tick.C:
			// A server of the cluster that stopped answering: one trap, from the server that runs the
			// jobs only one server runs.
			if s.Cluster == nil || !s.Cluster.Leader() {
				continue
			}
			for _, n := range s.Cluster.Nodes() {
				if was, ok := alive[n.ID]; ok && was && !n.Alive && !n.Self {
					s.snmpTrap("server.down", map[string]any{"down": n.Name})
				}
				alive[n.ID] = n.Alive
			}
		case <-ctx.Done():
			s.snmp.mu.Lock()
			if s.snmp.cancel != nil {
				s.snmp.cancel()
			}
			s.snmp.mu.Unlock()
			return
		case ev := <-sub.C:
			switch ev.Type {
			case "config.applied":
				s.syncSNMP(ctx)
			case "trunks.changed", "emergency.call":
				s.snmpTrap(ev.Type, ev.Data)
			}
		}
	}
}

func (s *Server) syncSNMP(ctx context.Context) {
	cfg := s.config()
	sn := cfg.System.SNMP
	var allow []netip.Prefix
	for _, a := range sn.AllowFrom {
		if p, err := netip.ParsePrefix(a); err == nil {
			allow = append(allow, p)
		} else if ip, err := netip.ParseAddr(a); err == nil {
			allow = append(allow, netip.PrefixFrom(ip, ip.BitLen()))
		}
	}
	listen := ""
	if sn.Enabled {
		listen = sn.Listen
	}
	s.snmp.mu.Lock()
	defer s.snmp.mu.Unlock()
	if listen == s.snmp.listen {
		if s.snmp.agent != nil {
			s.snmp.agent.Configure(sn.Community, allow)
		}
		return
	}
	if s.snmp.cancel != nil {
		s.snmp.cancel()
	}
	s.snmp.listen, s.snmp.agent, s.snmp.cancel, s.snmp.err = listen, nil, nil, ""
	if listen == "" {
		return
	}
	a := &snmp.Agent{Source: s.snmpTable, Log: s.Log}
	a.Configure(sn.Community, allow)
	if err := a.Listen(listen); err != nil {
		s.snmp.err = err.Error()
		if s.Log != nil {
			s.Log.Warn("SNMP agent could not listen", "listen", listen, "err", err)
		}
		return
	}
	actx, cancel := context.WithCancel(ctx)
	s.snmp.agent, s.snmp.cancel = a, cancel
	go a.Serve(actx)
}

// snmpTrap sends a trap to each manager for a trunk going down or coming back, an emergency call, or a
// server of the cluster stopping.
func (s *Server) snmpTrap(typ string, data any) {
	cfg := s.config()
	sn := cfg.System.SNMP
	if len(sn.Traps) == 0 {
		return
	}
	base, err := snmp.ParseOID(sn.BaseOID)
	if err != nil {
		return
	}
	var trap snmp.OID
	var vbs []snmp.VarBind
	m, _ := data.(map[string]any)
	switch typ {
	case "trunks.changed":
		id, _ := m["id"].(string)
		state, _ := m["state"].(string)
		up := state == "registered" || state == "reachable"
		s.snmp.mu.Lock()
		if s.snmp.trunks == nil {
			s.snmp.trunks = map[string]bool{}
		}
		was, known := s.snmp.trunks[id]
		s.snmp.trunks[id] = up
		s.snmp.mu.Unlock()
		if known && was == up || !known && up {
			return // no change, or the first sight of a trunk that is fine
		}
		name := id
		for _, t := range cfg.Trunks {
			if t.ID == id {
				name = t.Name
			}
		}
		trap = base.Append(0, map[bool]uint32{false: 1, true: 2}[up]) // .0.1 down, .0.2 up
		why, _ := m["error"].(string)
		vbs = []snmp.VarBind{{Name: base.Append(4, 1, 0), Value: snmp.String(name)}, {Name: base.Append(4, 2, 0), Value: snmp.String(firstNonEmpty(why, state))}}
	case "emergency.call":
		trap = base.Append(0, 3)
		num, _ := m["number"].(string)
		ext, _ := m["extension"].(string)
		vbs = []snmp.VarBind{{Name: base.Append(4, 3, 0), Value: snmp.String(num)}, {Name: base.Append(4, 4, 0), Value: snmp.String(ext)}}
	case "server.down":
		down, _ := m["down"].(string)
		if down == "" {
			return
		}
		trap = base.Append(0, 4)
		vbs = []snmp.VarBind{{Name: base.Append(4, 5, 0), Value: snmp.String(down)}}
	default:
		return
	}
	for _, t := range sn.Traps {
		addr := t.Host
		if _, _, err := net.SplitHostPort(addr); err != nil {
			addr = net.JoinHostPort(addr, "162")
		}
		go func(addr, community string) {
			if err := snmp.Trap(addr, community, time.Since(s.started), trap, vbs...); err != nil && s.Log != nil {
				s.Log.Warn("SNMP trap not sent", "to", addr, "err", err)
			}
		}(addr, t.Community)
	}
}

// snmpMIB is the MIB describing BitVoice's values, for monitoring tools to load.
func snmpMIB(base string) string {
	r := strings.NewReplacer("BASE", strings.ReplaceAll(base, ".", " "))
	return r.Replace(`BITVOICE-MIB DEFINITIONS ::= BEGIN
-- BitVoice's monitoring values. The base OID is the one set in Settings, SNMP: { BASE }.
IMPORTS OBJECT-TYPE, NOTIFICATION-TYPE, Gauge32, Counter32, Counter64, Integer32 FROM SNMPv2-SMI DisplayString FROM SNMPv2-TC;

bitvoice OBJECT IDENTIFIER ::= { BASE }
bvScalars OBJECT IDENTIFIER ::= { bitvoice 1 }
bvVersion        OBJECT-TYPE SYNTAX DisplayString MAX-ACCESS read-only STATUS current DESCRIPTION "BitVoice's version." ::= { bvScalars 1 }
bvCallsActive    OBJECT-TYPE SYNTAX Gauge32 MAX-ACCESS read-only STATUS current DESCRIPTION "Calls in progress." ::= { bvScalars 2 }
bvCallsRinging   OBJECT-TYPE SYNTAX Gauge32 MAX-ACCESS read-only STATUS current DESCRIPTION "Calls ringing." ::= { bvScalars 3 }
bvPhones         OBJECT-TYPE SYNTAX Gauge32 MAX-ACCESS read-only STATUS current DESCRIPTION "Phone registrations." ::= { bvScalars 4 }
bvTrunksUp       OBJECT-TYPE SYNTAX Gauge32 MAX-ACCESS read-only STATUS current DESCRIPTION "Trunks registered or reachable." ::= { bvScalars 5 }
bvTrunks         OBJECT-TYPE SYNTAX Gauge32 MAX-ACCESS read-only STATUS current DESCRIPTION "Trunks set up." ::= { bvScalars 6 }
bvQueueWaiting   OBJECT-TYPE SYNTAX Gauge32 MAX-ACCESS read-only STATUS current DESCRIPTION "Callers waiting in all queues." ::= { bvScalars 7 }
bvCallsTotal     OBJECT-TYPE SYNTAX Counter64 MAX-ACCESS read-only STATUS current DESCRIPTION "Calls ended since start." ::= { bvScalars 8 }
bvTalkSeconds    OBJECT-TYPE SYNTAX Counter64 MAX-ACCESS read-only STATUS current DESCRIPTION "Seconds of answered calls since start." ::= { bvScalars 9 }
bvRtpLost        OBJECT-TYPE SYNTAX Counter64 MAX-ACCESS read-only STATUS current DESCRIPTION "Sound packets lost on calls that ended." ::= { bvScalars 10 }
bvMosTimes100    OBJECT-TYPE SYNTAX Gauge32 MAX-ACCESS read-only STATUS current DESCRIPTION "Average call quality (MOS) times 100." ::= { bvScalars 11 }

bvTrunkTable OBJECT IDENTIFIER ::= { bitvoice 2 }
-- bvTrunkEntry { bvTrunkTable 1 }: 1 index, 2 name, 3 status (1 up, 2 down), 4 latency in ms, 5 state in words.
bvQueueTable OBJECT IDENTIFIER ::= { bitvoice 3 }
-- bvQueueEntry { bvQueueTable 1 }: 1 index, 2 name, 3 waiting, 4 agents, 5 answered today, 6 abandoned today.

bvTraps OBJECT IDENTIFIER ::= { bitvoice 0 }
bvTrunkDown      NOTIFICATION-TYPE STATUS current DESCRIPTION "A trunk stopped working (name, reason)." ::= { bvTraps 1 }
bvTrunkUp        NOTIFICATION-TYPE STATUS current DESCRIPTION "A trunk works again." ::= { bvTraps 2 }
bvEmergencyCall  NOTIFICATION-TYPE STATUS current DESCRIPTION "An emergency call was made (number, extension)." ::= { bvTraps 3 }
bvServerDown     NOTIFICATION-TYPE STATUS current DESCRIPTION "A server of the cluster stopped answering." ::= { bvTraps 4 }
END
`)
}

func (s *Server) snmpRoutes() {
	s.handle("GET /api/snmp", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		s.snmp.mu.Lock()
		running, listen, errText := s.snmp.agent != nil, s.snmp.listen, s.snmp.err
		addr := ""
		if s.snmp.agent != nil {
			addr = s.snmp.agent.Addr().String()
		}
		s.snmp.mu.Unlock()
		writeJSON(w, 200, map[string]any{"running": running, "listen": listen, "address": addr, "error": errText})
	})
	s.handle("GET /api/snmp/mib", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="BITVOICE-MIB.txt"`)
		_, _ = w.Write([]byte(snmpMIB(s.config().System.SNMP.BaseOID)))
	})
}
