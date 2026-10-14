package pbx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/cluster"
	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/sip"
)

// ClusterLink is what the engine needs from the servers it shares a database with.
type ClusterLink interface {
	Self() cluster.Node
	// Leader says whether this server runs the jobs only one server may run, such as wake-up calls.
	Leader() bool
	Remote() []cluster.Reg
	Peer(id string) (cluster.Node, bool)
	RemoteBusy(ext string) bool
	RemoteRinging(ext string) bool
	ParkedAt(slot string) string
	TrunkCalls(trunk string) int
	TrunkOwner(trunk string) (node string, mine bool)
	NodeName(id string) string
	Poke()
	Sign(callID, ext string) string
	Verify(token, callID, ext string) (cluster.Node, error)
}

// nodeHeader carries a call from one server to another.
const nodeHeader = "X-BitVoice-Node"

// remoteContacts lists the phones other servers hold for these extensions, as bindings that a call
// reaches through that server.
func (e *Engine) remoteContacts(numbers []string) []*Binding {
	if e.Cluster == nil {
		return nil
	}
	want := map[string]bool{}
	for _, n := range numbers {
		want[n] = true
	}
	var out []*Binding
	now := time.Now()
	newest := map[string]time.Time{} // a contact on several servers: only its newest registration counts
	for _, r := range e.Cluster.Remote() {
		if want[r.Ext] && r.Since.After(newest[r.Contact]) {
			newest[r.Contact] = r.Since
		}
	}
	for _, r := range e.Cluster.Remote() {
		if !want[r.Ext] || !now.Before(r.Expires) || r.Since.Before(newest[r.Contact]) {
			continue
		}
		out = append(out, &Binding{Extension: r.Ext, Contact: r.Contact, Source: r.Source, Transport: r.Transport,
			UserAgent: r.UserAgent, Expires: r.Expires, Since: r.Since, node: r.Node})
	}
	return out
}

// ClusterState is what this server tells the others with each beat.
func (e *Engine) ClusterState() cluster.State {
	var st cluster.State
	for _, b := range e.reg.list() {
		st.Regs = append(st.Regs, cluster.Reg{Ext: b.Extension, Contact: b.Contact, UserAgent: b.UserAgent, Source: b.Source,
			Transport: b.Transport, Since: b.Since, Expires: b.Expires, Via: viaOf(b.peer)})
	}
	st.Info.Phones = len(st.Regs)
	if e.CertPin != nil && e.TLSPort() > 0 {
		st.Info.Cert, st.Info.TLSPort = e.CertPin(), e.TLSPort()
	}
	busy, ringing := e.localBusy()
	for ext := range busy {
		st.Info.Busy = append(st.Info.Busy, ext)
	}
	for ext := range ringing {
		if !busy[ext] {
			st.Info.Ringing = append(st.Info.Ringing, ext)
		}
	}
	slices.Sort(st.Info.Busy)
	slices.Sort(st.Info.Ringing)
	e.mu.Lock()
	for slot := range e.parked {
		st.Info.Parked = append(st.Info.Parked, slot)
	}
	e.mu.Unlock()
	slices.Sort(st.Info.Parked)
	st.Calls = e.CallSnapshots()
	if e.Config().System.Cluster.SharedAddress != "" {
		st.OneTrunks = append(st.OneTrunks, cluster.AddressLease)
	}
	for _, t := range e.Config().Trunks {
		if t.Enabled && t.Shared == "one" {
			st.OneTrunks = append(st.OneTrunks, t.ID)
		}
	}
	for _, t := range e.Config().Trunks {
		if n := e.localTrunkChannels(t.ID); n > 0 {
			if st.Info.Trunks == nil {
				st.Info.Trunks = map[string]int{}
			}
			st.Info.Trunks[t.ID] = n
		}
	}
	for _, c := range e.Calls() {
		if c.Direction == "cluster" {
			continue // the server that has the caller lists it
		}
		st.Info.Calls = append(st.Info.Calls, cluster.Call{ID: c.ID, From: c.From, FromName: c.FromName, To: c.To,
			ToName: c.ToName, State: c.State, Start: c.Start})
	}
	return st
}

// nodePeer is where to send a call for another server's phone (over TCP: SDP with encryption keys
// can be too big for one UDP packet).
func (e *Engine) nodePeer(nodeID string) (sip.Peer, string, error) {
	n, ok := e.Cluster.Peer(nodeID)
	if !ok {
		return sip.Peer{}, "", errors.New("that server is not running")
	}
	pin, _ := hex.DecodeString(n.Info.Cert)
	ap, err := netip.ParseAddrPort(n.SIPAddr)
	if err != nil {
		host, port, err := net.SplitHostPort(n.SIPAddr)
		if err != nil {
			return sip.Peer{}, "", err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
		if err != nil || len(ips) == 0 {
			return sip.Peer{}, "", errors.New("cannot find " + host)
		}
		ap, err = netip.ParseAddrPort(net.JoinHostPort(ips[0].String(), port))
		if err != nil {
			return sip.Peer{}, "", err
		}
	}
	if len(pin) == sha256.Size && n.Info.TLSPort > 0 {
		// Over TLS, to the certificate the server published: signalling and the media keys in it are
		// private between the servers.
		return sip.Peer{Transport: "TLS", Addr: netip.AddrPortFrom(ap.Addr(), uint16(n.Info.TLSPort)), Pin: pin}, n.SIPAddr, nil
	}
	return sip.Peer{Transport: "TCP", Addr: ap}, n.SIPAddr, nil
}

// ClusterChanged is called when another server's phones, lamps or parked calls changed: watchers'
// busy lamps and the screens catch up.
func (e *Engine) ClusterChanged() {
	go e.presenceChanged()
	e.publish("registrations.changed", map[string]any{})
}

// remoteTarget is a target reached through another server: one of its phones, or one of its park slots.
func remoteTarget(node, user, name string) target {
	return target{ext: user, name: name, binding: &Binding{Extension: user, node: node}}
}

// clusterCall checks a call another server sent, and plans it: ring this server's own phones for the
// extension (with auto-answer for a page), or pick up a call parked here; nothing else (the sending
// server does voicemail, forwarding and the call record).
func (e *Engine) clusterCall(cfg *config.Config, m *sip.Message, src netip.Addr) (plan, error) {
	ext := cleanNumber(userOf(m.RequestURI))
	trunkID := m.Get(trunkHeader)
	signed := ext
	if trunkID != "" {
		signed = ext + "@" + trunkID
	}
	n, err := e.Cluster.Verify(m.Get(nodeHeader), m.CallID(), signed)
	if err != nil {
		return plan{}, err
	}
	if ap, err := netip.ParseAddrPort(n.SIPAddr); err == nil && ap.Addr().Unmap() != src.Unmap() {
		return plan{}, errors.New("cluster: the call came from another address than its server's")
	}
	if trunkID != "" {
		// Another server's call out on a trunk this server holds.
		t := cfg.FindTrunk(trunkID)
		if t == nil || !t.Enabled {
			return plan{reject: 503, reason: "No Such Trunk"}, nil
		}
		tc := *t
		return plan{toName: ext, direction: "cluster", trunk: t.Name, callerExt: m.Get(callerHeader),
			stages: []stage{{targets: []target{{trunk: &tc, number: ext, name: ext}}, timeout: 3 * time.Minute}}}, nil
	}
	if slices.Contains(parkSlots(cfg), ext) {
		return plan{local: "pickup", box: ext, toName: "Parked call", direction: "cluster"}, nil
	}
	x := cfg.FindExtension(ext)
	if x == nil || !x.Enabled {
		return plan{reject: 404}, nil
	}
	var ts []target
	for _, b := range e.reg.localContacts(ext) {
		ts = append(ts, target{ext: ext, name: x.Name, binding: b})
	}
	if len(ts) == 0 {
		return plan{reject: 480, reason: "Not Here"}, nil
	}
	return plan{toName: x.Name, direction: "cluster", intercom: m.Get(pageHeader) == "1", stages: []stage{{targets: ts, timeout: 3 * time.Minute}}}, nil
}

// pageHeader marks a call between servers as a page: the receiving server rings with auto-answer.
const pageHeader = "X-BitVoice-Page"

// trunkHeader asks the server holding a trunk shared one at a time to place a call out on it;
// callerHeader names the calling extension, whose caller ID the call carries.
const (
	trunkHeader  = "X-BitVoice-Trunk"
	callerHeader = "X-BitVoice-Caller"
)

// trunkTarget is a call out on a trunk that another server holds.
func trunkTarget(node string, t *config.Trunk, number string) target {
	return target{ext: number, name: number, viaTrunk: t.ID, binding: &Binding{Extension: number, node: node}}
}

// heldElsewhere says which server to send a call out through, for a trunk held one server at a
// time by another server ("" to place it here).
func (e *Engine) heldElsewhere(t *config.Trunk) string {
	if e.Cluster == nil || t.Shared != "one" {
		return ""
	}
	node, mine := e.Cluster.TrunkOwner(t.ID)
	if mine || node == "" {
		return "" // this server holds it, or nobody does yet: try it from here
	}
	if _, ok := e.Cluster.Peer(node); !ok {
		return ""
	}
	return node
}

// clusterMessage delivers a text another server sent to this server's phones for an extension.
func (e *Engine) clusterMessage(m *sip.Message) bool {
	ext := cleanNumber(userOf(m.RequestURI))
	if _, err := e.Cluster.Verify(m.Get(nodeHeader), m.CallID(), ext); err != nil {
		return false
	}
	from, _ := sip.ParseAddress(m.Get("From"))
	name := "BitVoice"
	if from != nil && from.Display != "" {
		name = from.Display
	}
	e.sendLocalText(ext, name, string(m.Body))
	return true
}

// textElsewhere sends a text to an extension's phones on the other servers.
func (e *Engine) textElsewhere(ext, fromName, text string) int {
	if e.Cluster == nil {
		return 0
	}
	nodes := map[string]bool{}
	for _, b := range e.remoteContacts([]string{ext}) {
		nodes[b.node] = true
	}
	sent := 0
	for node := range nodes {
		peer, addr, err := e.nodePeer(node)
		if err != nil {
			continue
		}
		callID := sip.NewCallID()
		msg := &sip.Message{IsRequest: true, Method: sip.MESSAGE, RequestURI: "sip:" + ext + "@" + addr + ";transport=" + strings.ToLower(peer.Transport)}
		msg.Set("From", display(fromName)+"<sip:bitvoice@"+e.stack.LocalHost(peer)+">;tag="+sip.NewTag())
		msg.Set("To", "<sip:"+ext+"@"+addr+">")
		msg.Set("Call-ID", callID)
		msg.Set("CSeq", "1 MESSAGE")
		msg.Set("Content-Type", "text/plain;charset=UTF-8")
		msg.Set(nodeHeader, e.Cluster.Sign(callID, ext))
		msg.Body = []byte(text)
		go func() {
			for range e.stack.Request(msg, peer).Responses {
			}
		}()
		sent++
	}
	return sent
}

func emptyMap(v any) bool {
	m, ok := v.(map[string]any)
	return ok && len(m) == 0
}

// Adopt takes in phones a stopped server had registered through the shared address, now held here:
// they are reached at the same addresses, from the same address, until they register again.
func (e *Engine) Adopt(regs []cluster.Reg) int {
	n := 0
	cfg := e.Config()
	for _, r := range regs {
		x := cfg.FindExtension(r.Ext)
		src, err := netip.ParseAddrPort(r.Source)
		via, err2 := netip.ParseAddr(r.Via)
		uri, err3 := sip.ParseURI(r.Contact)
		if x == nil || err != nil || err2 != nil || err3 != nil || r.Transport != "UDP" {
			continue // a TCP or TLS phone's connection went with its server: it registers again by itself
		}
		e.reg.mu.Lock()
		dup := false
		for _, b := range e.reg.bindings[r.Ext] {
			dup = dup || b.Contact == r.Contact
		}
		if !dup {
			e.reg.bindings[r.Ext] = append(e.reg.bindings[r.Ext], &Binding{Extension: r.Ext, Name: x.Name, Contact: r.Contact,
				Source: r.Source, Transport: "UDP", UserAgent: r.UserAgent, Expires: r.Expires, Since: r.Since,
				peer: sip.Peer{Transport: "UDP", Addr: src, Local: via}, uri: uri})
			n++
		}
		e.reg.mu.Unlock()
	}
	if n > 0 {
		e.publish("registrations.changed", map[string]any{"adopted": n})
	}
	return n
}
