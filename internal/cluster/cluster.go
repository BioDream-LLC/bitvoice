// Package cluster lets BitVoice servers that share one PostgreSQL database work as one phone system:
// each says every two seconds that it is alive, which phones are registered with it and which
// extensions are on a call, and reads the same of the others. A call for a phone registered with
// another server is then sent to that server, signed with the cluster's shared secret.
package cluster

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/biodream-llc/bitvoice/internal/db"
)

const (
	// Beat is how often a server says it is alive and what it holds.
	Beat = time.Second
	// Gone is how long a server may be silent before the others stop sending it calls and carry on
	// the calls it had: three missed beats.
	Gone = 4 * time.Second
	// leaseFor is how long a trunk's lease lasts without renewal; its holder stops registering the
	// trunk well before (holdFresh), so two servers never both do.
	leaseFor  = 5 * time.Second
	holdFresh = 3 * time.Second
	// tokenLife is how long a signed call between servers stays valid.
	tokenLife = time.Minute
)

// Node is one server.
type Node struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	SIPAddr string    `json:"sipAddr"` // host:port the other servers send calls to
	Version string    `json:"version"`
	Started time.Time `json:"started"`
	Seen    time.Time `json:"seen"`
	Alive   bool      `json:"alive"`
	Self    bool      `json:"self"`
	Info    Info      `json:"info"`
}

// Info is what a server says about itself with each beat.
type Info struct {
	Phones  int      `json:"phones"`
	Calls   []Call   `json:"calls"`
	Busy    []string `json:"busy"`    // extensions on a call
	Ringing []string `json:"ringing"` // extensions ringing
	Parked  []string `json:"parked"`  // park slots holding a call on this server
	// Trunks counts this server's calls on each trunk, for channel limits across servers.
	Trunks map[string]int `json:"trunks,omitempty"`
	// Cert is the SHA-256 (hex) of the certificate this server presents on SIP over TLS: the others
	// connect to it over TLS and accept that certificate only.
	Cert string `json:"cert,omitempty"`
	// TLSPort is where it takes SIP over TLS (0: none, the others use TCP).
	TLSPort int `json:"tlsPort,omitempty"`
	// Unreachable lists the other servers this one could not connect to, by id, with the reason.
	Unreachable map[string]string `json:"unreachable,omitempty"`
}

// Call is a call in progress, for the live list on every server.
type Call struct {
	ID       string    `json:"id"`
	From     string    `json:"from"`
	FromName string    `json:"fromName"`
	To       string    `json:"to"`
	ToName   string    `json:"toName"`
	State    string    `json:"state"`
	Start    time.Time `json:"start"`
}

// Reg is a phone registered with a server.
type Reg struct {
	Node      string    `json:"node"`
	Ext       string    `json:"ext"`
	Contact   string    `json:"contact"`
	UserAgent string    `json:"userAgent"`
	Source    string    `json:"source"`
	Transport string    `json:"transport"`
	Since     time.Time `json:"since"`
	Expires   time.Time `json:"expires"`
	Via       string    `json:"via,omitempty"` // the shared address the phone registered through
}

// AddressLease is the lease name of the shared address, held like a trunk shared one at a time.
const AddressLease = "@address"

// Orphans lists the phones stopped servers had registered through the shared address: the server now
// holding it can reach them at the same addresses.
func (c *Cluster) Orphans(ctx context.Context) []Reg {
	alive := map[string]bool{}
	for _, n := range c.Nodes() {
		alive[n.ID] = n.Alive
	}
	rows, err := c.d.Query(ctx, `SELECT body FROM registrations WHERE expires_ms > ?`, time.Now().UnixMilli())
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Reg
	for rows.Next() {
		var body string
		var r Reg
		if rows.Scan(&body) == nil && json.Unmarshal([]byte(body), &r) == nil && r.Via != "" && !alive[r.Node] {
			out = append(out, r)
		}
	}
	return out
}

// State is what this server holds, asked for with each beat.
type State struct {
	Regs []Reg
	Info Info
	// OneTrunks are the trunks registered by one server at a time: this server takes or keeps each
	// trunk's lease with each beat.
	OneTrunks []string
	// Calls are this server's connected calls, saved for another server to carry on.
	Calls map[string][]byte
}

// lease is which server holds a trunk registered by one server at a time.
type lease struct {
	Node  string `json:"node"`
	Until int64  `json:"until"` // unix ms
}

// Cluster is this server's place among the servers sharing the database.
type Cluster struct {
	d       *db.DB
	dir     string
	version string

	mu   sync.Mutex
	self Node
	// unreachable: other servers this one could not connect to at the last probe.
	unreachable map[string]string
	secret      []byte
	nodes       []Node
	remote      []Reg
	lastReg     string // what was last written, to skip writing the same again
	custom      bool   // a person set this server's address
	seen        string // the others' state last read, to tell when it changed

	// OnChange is called when another server's phones, busy lamps or parked calls changed.
	OnChange func()
	poke     chan struct{}

	owners map[string]string    // trunk -> the server holding its lease
	mine   map[string]time.Time // trunk -> when this server last renewed its lease

	saved      map[string]string // call -> the body last saved
	handedOver bool              // stopping: the calls are the others' now, nothing more is saved

	// TakeOver carries on calls a stopped server had; LetGo drops calls another server took over.
	// Slow, when set, hears of a check-in that took long (the database answering slowly).
	Slow     func(time.Duration)
	TakeOver func(calls [][]byte) int
	LetGo    func(ids []string)
}

// syncCalls saves this server's calls, drops those that ended, lets go of any another server took,
// and takes over the calls of servers that stopped.
func (c *Cluster) syncCalls(ctx context.Context, self string, calls map[string][]byte, dead []string) {
	c.mu.Lock()
	done := c.handedOver
	c.mu.Unlock()
	if done {
		return
	}
	if c.saved == nil {
		c.saved = map[string]string{}
	}
	// Calls this server has that another now holds: it was cut off and they were taken over.
	if len(calls) > 0 {
		ids := make([]any, 0, len(calls))
		for id := range calls {
			ids = append(ids, id)
		}
		q := `SELECT id FROM live_calls WHERE node <> ? AND id IN (?` + strings.Repeat(", ?", len(ids)-1) + `)`
		if rows, err := c.d.Query(ctx, q, append([]any{self}, ids...)...); err == nil {
			var gone []string
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					gone = append(gone, id)
					delete(calls, id)
				}
			}
			rows.Close()
			if len(gone) > 0 && c.LetGo != nil {
				c.LetGo(gone)
			}
		}
	}
	now := time.Now().UnixMilli()
	for id, body := range calls {
		if c.saved[id] == string(body) {
			continue
		}
		// Never over another server's: the WHERE keeps a call taken over with its new server.
		if _, err := c.d.Exec(ctx, `INSERT INTO live_calls (id, node, body, updated_ms) VALUES (?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET body = excluded.body, updated_ms = excluded.updated_ms WHERE live_calls.node = excluded.node`,
			id, self, string(body), now); err == nil {
			c.saved[id] = string(body)
		}
	}
	for id := range c.saved {
		if _, ok := calls[id]; !ok {
			if _, err := c.d.Exec(ctx, `DELETE FROM live_calls WHERE id = ? AND node = ?`, id, self); err == nil {
				delete(c.saved, id)
			}
		}
	}
	// A stopped server's calls: whichever running server claims them first carries them on. Calls
	// made through the shared address go to the server that holds it, which alone can reach them.
	_, holdsAddress := c.TrunkOwner(AddressLease)
	for _, node := range dead {
		q := `UPDATE live_calls SET node = ?, updated_ms = ? WHERE node = ? AND body NOT LIKE '%"via":"%' RETURNING id, body`
		if holdsAddress {
			q = `UPDATE live_calls SET node = ?, updated_ms = ? WHERE node = ? RETURNING id, body`
		}
		rows, err := c.d.Query(ctx, q, self, now, node)
		if err != nil {
			continue
		}
		var took [][]byte
		for rows.Next() {
			var id, body string
			if rows.Scan(&id, &body) == nil {
				took = append(took, []byte(body))
				c.saved[id] = "" // ours now: deleted once it is no longer among this server's calls
			}
		}
		rows.Close()
		if len(took) > 0 && c.TakeOver != nil {
			// Off the beat: reaching a phone can take seconds, and the beat renews this server's leases.
			go c.TakeOver(took)
		}
	}
}

// TrunkOwner says which server registers a trunk shared one at a time, and whether it is this one.
// This server counts as the holder only while its lease is fresh, so it stops registering before
// another server can take over.
func (c *Cluster) TrunkOwner(trunk string) (node string, mine bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	node = c.owners[trunk]
	at, ok := c.mine[trunk]
	return node, ok && node == c.self.ID && time.Since(at) < holdFresh
}

// NodeName names a server for people.
func (c *Cluster) NodeName(id string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range c.nodes {
		if n.ID == id {
			return n.Name
		}
	}
	return id
}

// claimTrunks takes or renews each trunk's lease, and reads who holds the others.
func (c *Cluster) claimTrunks(ctx context.Context, trunks []string, alive map[string]bool) {
	self := c.Self().ID
	owners, mine := map[string]string{}, map[string]time.Time{}
	c.mu.Lock()
	for k, v := range c.mine {
		mine[k] = v
	}
	c.mu.Unlock()
	for _, id := range trunks {
		now := time.Now()
		var holder string
		err := c.d.MutateDoc(ctx, "trunk-lease/"+id, func(old []byte) ([]byte, error) {
			var l lease
			_ = json.Unmarshal(old, &l)
			// Only a lease that ran out (or was given up) passes on: a holder that merely looks
			// stopped for a moment keeps it, so two servers never hold a line or the address at once.
			free := l.Node == "" || l.Node == self || now.UnixMilli() > l.Until
			if !free {
				holder = l.Node
				return nil, nil
			}
			holder = self
			return json.Marshal(lease{Node: self, Until: now.Add(leaseFor).UnixMilli()})
		})
		if err != nil {
			continue // keep what was known; an unrenewed lease runs out by itself
		}
		owners[id] = holder
		if holder == self {
			mine[id] = now
		} else {
			delete(mine, id)
		}
	}
	c.mu.Lock()
	c.owners = owners
	c.mine = mine
	c.mu.Unlock()
}

// channel is the PostgreSQL notification channel servers wake each other on.
const channel = "bitvoice_cluster"

// Poke asks for an early beat: something changed here that the others should hear of now.
func (c *Cluster) Poke() {
	select {
	case c.poke <- struct{}{}:
	default:
	}
}

// SetDefaultAddr is where this server listens, used unless a person set an address.
func (c *Cluster) SetDefaultAddr(addr string) {
	c.mu.Lock()
	if !c.custom {
		c.self.SIPAddr = addr
	}
	c.mu.Unlock()
}

// identity is this server's own, kept in its data directory (not in the shared database).
type identity struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	SIPAddr string `json:"sipAddr"`
}

// New joins the cluster in d. sipAddr is where the others reach this server unless an address was
// set before (in Servers).
func New(d *db.DB, dir, version, sipAddr string) (*Cluster, error) {
	c := &Cluster{d: d, dir: dir, version: version, poke: make(chan struct{}, 1)}
	var id identity
	if b, err := os.ReadFile(filepath.Join(dir, "node.json")); err == nil {
		_ = json.Unmarshal(b, &id)
	}
	if id.ID == "" {
		b := make([]byte, 6)
		rand.Read(b)
		id.ID = hex.EncodeToString(b)
		id.Name = "Server " + strings.ToUpper(id.ID[:4])
	}
	if err := c.saveIdentity(id); err != nil {
		return nil, err
	}
	addr := id.SIPAddr // set by a person in Servers; else where this server listens
	if addr == "" {
		addr = sipAddr
	}
	c.custom = id.SIPAddr != ""
	c.self = Node{ID: id.ID, Name: id.Name, SIPAddr: addr, Version: version, Started: time.Now().UTC(), Self: true, Alive: true}
	if err := c.loadSecret(context.Background()); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Cluster) saveIdentity(id identity) error {
	b, _ := json.MarshalIndent(id, "", "  ")
	tmp := filepath.Join(c.dir, ".node.json.new")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(c.dir, "node.json"))
}

// loadSecret reads the cluster's shared secret, making it on the first server.
func (c *Cluster) loadSecret(ctx context.Context) error {
	var secret string
	err := c.d.MutateDoc(ctx, "cluster.json", func(old []byte) ([]byte, error) {
		var v struct {
			Secret string `json:"secret"`
		}
		_ = json.Unmarshal(old, &v)
		if v.Secret != "" {
			secret = v.Secret
			return nil, nil
		}
		b := make([]byte, 32)
		rand.Read(b)
		v.Secret = hex.EncodeToString(b)
		secret = v.Secret
		return json.Marshal(v)
	})
	c.secret = []byte(secret)
	return err
}

// Self is this server.
func (c *Cluster) Self() Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.self
}

// SetSelf renames this server or changes where the others reach it.
func (c *Cluster) SetSelf(name, sipAddr string) (Node, error) {
	name, sipAddr = strings.TrimSpace(name), strings.TrimSpace(sipAddr)
	if name == "" || len(name) > 60 {
		return Node{}, errors.New("give the server a name of up to 60 characters")
	}
	if sipAddr == "" || !strings.Contains(sipAddr, ":") {
		return Node{}, errors.New("enter where the other servers reach this one, as address:port (192.0.2.10:5060)")
	}
	c.mu.Lock()
	c.self.Name, c.self.SIPAddr, c.custom = name, sipAddr, true
	self := c.self
	c.mu.Unlock()
	return self, c.saveIdentity(identity{ID: self.ID, Name: name, SIPAddr: sipAddr})
}

// Nodes lists every server, this one first, then by name.
func (c *Cluster) Nodes() []Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.nodes)
}

// Peer finds another server that is alive.
func (c *Cluster) Peer(id string) (Node, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range c.nodes {
		if n.ID == id && n.Alive && !n.Self {
			return n, true
		}
	}
	return Node{}, false
}

// Remote lists the phones registered with the other servers that are alive.
func (c *Cluster) Remote() []Reg {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.remote
}

// RemoteBusy says whether an extension is on a call on another server.
func (c *Cluster) RemoteBusy(ext string) bool {
	return c.remoteHas(ext, func(i Info) []string { return i.Busy })
}

// RemoteRinging says whether an extension is ringing on another server.
func (c *Cluster) RemoteRinging(ext string) bool {
	return c.remoteHas(ext, func(i Info) []string { return i.Ringing })
}

// ParkedAt says which other server holds a call in a park slot ("" when none does).
func (c *Cluster) ParkedAt(slot string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range c.nodes {
		if n.Alive && !n.Self && slices.Contains(n.Info.Parked, slot) {
			return n.ID
		}
	}
	return ""
}

// TrunkCalls counts the other servers' calls on a trunk.
func (c *Cluster) TrunkCalls(trunk string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, x := range c.nodes {
		if x.Alive && !x.Self {
			n += x.Info.Trunks[trunk]
		}
	}
	return n
}

func (c *Cluster) remoteHas(ext string, list func(Info) []string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range c.nodes {
		if n.Alive && !n.Self && slices.Contains(list(n.Info), ext) {
			return true
		}
	}
	return false
}

// Sign makes the token a call to another server carries: who sends it, when, and a MAC over both
// and the call, so only a server holding the cluster's secret can send calls in.
func (c *Cluster) Sign(callID, ext string) string {
	self := c.Self()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	return self.ID + ";" + ts + ";" + c.mac(self.ID, ts, callID, ext)
}

// Verify checks a token and returns the server that sent it.
func (c *Cluster) Verify(token, callID, ext string) (Node, error) {
	parts := strings.Split(token, ";")
	if len(parts) != 3 {
		return Node{}, errors.New("cluster: malformed token")
	}
	ts, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Since(time.Unix(ts, 0)).Abs() > tokenLife {
		return Node{}, errors.New("cluster: token too old (are the servers' clocks right?)")
	}
	if !hmac.Equal([]byte(parts[2]), []byte(c.mac(parts[0], parts[1], callID, ext))) {
		return Node{}, errors.New("cluster: bad signature")
	}
	n, ok := c.Peer(parts[0])
	if !ok {
		return Node{}, errors.New("cluster: the sending server is not a live member")
	}
	return n, nil
}

func (c *Cluster) mac(node, ts, callID, ext string) string {
	m := hmac.New(sha256.New, c.secret)
	m.Write([]byte(node + "|" + ts + "|" + callID + "|" + ext))
	return hex.EncodeToString(m.Sum(nil))
}

// Run beats until ctx ends, then tells the others this server is gone. Between beats, a change here
// (Poke) is written and announced at once, and an announcement from another server is read at once.
func (c *Cluster) Run(ctx context.Context, state func() State) {
	t := time.NewTicker(Beat)
	defer t.Stop()
	heard := make(chan struct{}, 1)
	self := c.Self().ID
	go c.d.Listen(ctx, channel, func(from string) {
		if from != self {
			select {
			case heard <- struct{}{}:
			default:
			}
		}
	})
	c.beat(ctx, state())
	_ = c.d.Notify(ctx, channel, self) // the others see this server at once
	go func() {
		pt := time.NewTicker(10 * time.Second)
		defer pt.Stop()
		for {
			c.probe(ctx)
			select {
			case <-ctx.Done():
				return
			case <-pt.C:
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			c.mu.Lock()
			handed := c.handedOver
			c.mu.Unlock()
			if !handed {
				c.leave()
			}
			return
		case <-t.C:
		case <-c.poke:
			c.beat(ctx, state())
			_ = c.d.Notify(ctx, channel, self)
			continue
		case <-heard:
		}
		c.beat(ctx, state())
	}
}

// Beat once: say what this server holds, and read what the others do.
func (c *Cluster) beat(ctx context.Context, st State) {
	self := c.Self()
	now := time.Now()
	defer func() {
		if d := time.Since(now); d > holdFresh/2 && c.Slow != nil {
			c.Slow(d) // the database is slow to answer: leases may lapse
		}
	}()
	st.Info.Unreachable = c.Unreachable()
	info, _ := json.Marshal(st.Info)
	_, _ = c.d.Exec(ctx, `INSERT INTO nodes (id, name, sip_addr, version, started_ms, seen_ms, info) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, sip_addr = excluded.sip_addr, version = excluded.version,
		started_ms = excluded.started_ms, seen_ms = excluded.seen_ms, info = excluded.info`,
		self.ID, self.Name, self.SIPAddr, self.Version, self.Started.UnixMilli(), now.UnixMilli(), string(info))
	c.writeRegs(ctx, self.ID, st.Regs)

	var nodes []Node
	alive := map[string]bool{}
	if rows, err := c.d.Query(ctx, `SELECT id, name, sip_addr, version, started_ms, seen_ms, info FROM nodes`); err == nil {
		for rows.Next() {
			var n Node
			var started, seen int64
			var info string
			if rows.Scan(&n.ID, &n.Name, &n.SIPAddr, &n.Version, &started, &seen, &info) != nil {
				continue
			}
			n.Started, n.Seen = time.UnixMilli(started).UTC(), time.UnixMilli(seen).UTC()
			_ = json.Unmarshal([]byte(info), &n.Info)
			n.Self = n.ID == self.ID
			n.Alive = n.Self || now.Sub(n.Seen) < Gone
			if n.Self {
				n.Info = st.Info
			}
			alive[n.ID] = n.Alive
			// A server gone for a week is forgotten.
			if !n.Alive && now.Sub(n.Seen) > 7*24*time.Hour {
				_, _ = c.d.Exec(ctx, `DELETE FROM nodes WHERE id = ?`, n.ID)
				continue
			}
			nodes = append(nodes, n)
		}
		rows.Close()
	}
	slices.SortFunc(nodes, func(a, b Node) int {
		if a.Self != b.Self {
			if a.Self {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	var remote []Reg
	if rows, err := c.d.Query(ctx, `SELECT body FROM registrations WHERE node <> ? AND expires_ms > ?`, self.ID, now.UnixMilli()); err == nil {
		for rows.Next() {
			var body string
			var r Reg
			if rows.Scan(&body) == nil && json.Unmarshal([]byte(body), &r) == nil && alive[r.Node] {
				remote = append(remote, r)
			}
		}
		rows.Close()
	}
	// Has anything the engine shows changed on the other servers?
	var sig strings.Builder
	for _, n := range nodes {
		if !n.Self && n.Alive {
			sig.WriteString(n.ID + strings.Join(n.Info.Busy, ",") + "|" + strings.Join(n.Info.Ringing, ",") + "|" + strings.Join(n.Info.Parked, ",") + ";")
		}
	}
	for _, r := range remote {
		sig.WriteString(r.Node + r.Ext + r.Contact + ";")
	}
	c.claimTrunks(ctx, st.OneTrunks, alive)
	var dead []string
	for _, n := range nodes {
		if !n.Alive && !n.Self {
			dead = append(dead, n.ID)
		}
	}
	c.syncCalls(ctx, self.ID, st.Calls, dead)
	c.mu.Lock()
	c.nodes, c.remote = nodes, remote
	changed := sig.String() != c.seen
	c.seen = sig.String()
	f := c.OnChange
	c.mu.Unlock()
	if changed && f != nil {
		f()
	}
}

// writeRegs replaces this server's registrations when they changed.
func (c *Cluster) writeRegs(ctx context.Context, self string, regs []Reg) {
	key, _ := json.Marshal(regs)
	c.mu.Lock()
	same := string(key) == c.lastReg
	c.mu.Unlock()
	if same {
		return
	}
	err := c.d.Tx(ctx, func(tx *db.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM registrations WHERE node = ?`, self); err != nil {
			return err
		}
		for _, r := range regs {
			r.Node = self
			b, _ := json.Marshal(r)
			if _, err := tx.Exec(ctx, `INSERT INTO registrations (node, ext, contact, body, expires_ms) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (node, ext, contact) DO UPDATE SET body = excluded.body, expires_ms = excluded.expires_ms`,
				self, r.Ext, r.Contact, string(b), r.Expires.UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		c.mu.Lock()
		c.lastReg = string(key)
		c.mu.Unlock()
	}
}

// HandOver saves this server's calls one last time and says it is gone, so a running server takes
// them over at its next beat instead of their being hung up. It returns the calls handed over, none
// when no other server is running.
func (c *Cluster) HandOver(calls map[string][]byte) map[string][]byte {
	others := false
	for _, n := range c.Nodes() {
		others = others || n.Alive && !n.Self
	}
	if !others || len(calls) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	self := c.Self().ID
	c.syncCalls(ctx, self, calls, nil)
	c.mu.Lock()
	c.handedOver = true
	c.mu.Unlock()
	c.leave()
	_ = c.d.Notify(ctx, channel, self)
	return calls
}

// leave says this server is gone, so the others stop sending it calls at once.
func (c *Cluster) leave() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	self := c.Self()
	// Give up the trunks this server registered, so another takes them at its next beat.
	c.mu.Lock()
	held := make([]string, 0, len(c.mine))
	for id := range c.mine {
		held = append(held, id)
	}
	c.mine = map[string]time.Time{}
	c.mu.Unlock()
	for _, id := range held {
		_ = c.d.MutateDoc(ctx, "trunk-lease/"+id, func(old []byte) ([]byte, error) {
			var l lease
			if json.Unmarshal(old, &l) != nil || l.Node != self.ID {
				return nil, nil
			}
			return json.Marshal(lease{})
		})
	}
	// Phones registered through the shared address stay listed: the server taking the address
	// adopts them. The others go.
	_, _ = c.d.Exec(ctx, `DELETE FROM registrations WHERE node = ? AND body NOT LIKE '%"via":%'`, self.ID)
	// Seen just long enough ago to count as gone: listed as stopped, not forgotten.
	_, _ = c.d.Exec(ctx, `UPDATE nodes SET seen_ms = ?, info = '{}' WHERE id = ?`, time.Now().Add(-Gone).UnixMilli(), self.ID)
}

// Forget removes a server that is gone for good from the list.
func (c *Cluster) Forget(id string) error {
	for _, n := range c.Nodes() {
		if n.ID == id && (n.Alive || n.Self) {
			return errors.New("that server is running: stop it first")
		}
	}
	_, err := c.d.Exec(context.Background(), `DELETE FROM nodes WHERE id = ?`, id)
	if err == nil {
		_, err = c.d.Exec(context.Background(), `DELETE FROM registrations WHERE node = ?`, id)
	}
	return err
}

// probe tries to connect to every other running server's SIP port, as calls between them will.
func (c *Cluster) probe(ctx context.Context) {
	out := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range c.Nodes() {
		if n.Self || !n.Alive {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			addr := n.SIPAddr
			if host, _, err := net.SplitHostPort(n.SIPAddr); err == nil && n.Info.TLSPort > 0 {
				addr = net.JoinHostPort(host, strconv.Itoa(n.Info.TLSPort))
			}
			d := net.Dialer{Timeout: 2 * time.Second}
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				mu.Lock()
				out[n.ID] = "cannot connect to " + addr + ": " + reason(err)
				mu.Unlock()
				return
			}
			conn.Close()
		}()
	}
	wg.Wait()
	c.mu.Lock()
	c.unreachable = out
	c.mu.Unlock()
}

// reason puts a connection error in a few words.
func reason(err error) string {
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused (nothing listening, or a firewall rejects it)"
	case os.IsTimeout(err) || errors.Is(err, context.DeadlineExceeded):
		return "no answer (a firewall may drop it)"
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "no route to that address"
	}
	return err.Error()
}

// Unreachable lists the other servers this one cannot connect to now.
func (c *Cluster) Unreachable() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.unreachable)
}
