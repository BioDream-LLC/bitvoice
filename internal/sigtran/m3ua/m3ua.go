// Package m3ua is an M3UA application server process (RFC 4666): SS7's MTP3 users (SCCP, ISUP)
// carried over IP to a signalling gateway or an IP STP. It connects over TCP (Go's standard library
// has no SCTP; STPs such as OsmoSTP take M3UA over TCP too), brings the ASP up, registers its routing
// key when the STP asks for that (RFC 4666 3.6, dynamic registration), activates it and then carries
// DATA both ways, answering heartbeats.
package m3ua

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Message classes and types (RFC 4666 3.1.2).
const (
	classMGMT  = 0
	classXFER  = 1
	classSSNM  = 2
	classASPSM = 3
	classASPTM = 4
	classRKM   = 9

	typeERR  = 0
	typeNTFY = 1
	typeDATA = 1

	typeDUNA = 1
	typeDAVA = 2

	typeASPUP    = 1
	typeASPDN    = 2
	typeBEAT     = 3
	typeASPUPAck = 4
	typeBEATAck  = 6

	typeASPAC    = 1
	typeASPACAck = 3

	typeREGReq = 1
	typeREGRsp = 2
)

// Parameter tags (RFC 4666 3.2).
const (
	tagInfo           = 0x0004
	tagRC             = 0x0006
	tagHeartbeat      = 0x0009
	tagTrafficMode    = 0x000b
	tagErrorCode      = 0x000c
	tagStatus         = 0x000d
	tagAffectedPC     = 0x0012
	tagRoutingKey     = 0x0207
	tagRegResult      = 0x0208
	tagLocalRKID      = 0x020a
	tagDPC            = 0x020b
	tagServiceInd     = 0x020c
	tagProtocolData   = 0x0210
	tagRegStatus      = 0x0212
	trafficLoadshare  = 2
	statusASActive    = 3 // NTFY status information, type 1 (AS state change)
	statusTypeASState = 1
)

// Label is an MTP3 routing label as M3UA carries it, with the service indicator, network indicator,
// message priority and signalling link selection.
type Label struct {
	OPC, DPC int
	SI, NI   int
	MP, SLS  int
}

// Config is this ASP: its point code and network indicator, and the routing context the STP gave it
// (0: none configured, registered dynamically).
type Config struct {
	Addr string // the STP's host:port
	OPC  int
	NI   int
	RC   uint32
	// Keys are the point codes this ASP takes traffic for, each registered as a routing key when no
	// RC is configured: OPC, and those behind it when it is an STP. Empty: OPC alone.
	Keys []int
	// Heartbeat sends BEAT this often (0: none; the STP may send its own).
	Heartbeat time.Duration
}

// ErrNotActive is returned by Send before the ASP is active.
var ErrNotActive = errors.New("m3ua: the link to the STP is not active")

// ASP is one association to the STP.
type ASP struct {
	cfg Config
	// Receive is called for each DATA message (in the reading goroutine: do not block).
	Receive func(l Label, data []byte)
	// Unavailable is told of destinations the STP reports unavailable (DUNA) or available again (DAVA).
	Unavailable func(pc int, down bool)
	// Error is told of the STP's ERR messages (a message it could not take); the association stays up.
	Error func(code uint32)

	mu     sync.Mutex
	conn   net.Conn
	rc     uint32
	rcs    map[int]uint32 // point code -> routing context, registered
	active bool
	wait   map[[2]int]chan msg
}

type param struct {
	tag uint16
	val []byte
}

type msg struct {
	class, typ int
	params     []param
}

func (m msg) get(tag uint16) ([]byte, bool) {
	for _, p := range m.params {
		if p.tag == tag {
			return p.val, true
		}
	}
	return nil, false
}

// New makes an ASP; Run connects it.
func New(cfg Config) *ASP { return &ASP{cfg: cfg, wait: map[[2]int]chan msg{}} }

func encode(class, typ int, ps ...param) []byte {
	b := []byte{1, 0, byte(class), byte(typ), 0, 0, 0, 0}
	for _, p := range ps {
		b = binary.BigEndian.AppendUint16(b, p.tag)
		b = binary.BigEndian.AppendUint16(b, uint16(4+len(p.val)))
		b = append(b, p.val...)
		for len(b)%4 != 0 {
			b = append(b, 0)
		}
	}
	binary.BigEndian.PutUint32(b[4:], uint32(len(b)))
	return b
}

func u32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

func decodeParams(b []byte) ([]param, error) {
	var out []param
	for len(b) >= 4 {
		tag, n := binary.BigEndian.Uint16(b), int(binary.BigEndian.Uint16(b[2:]))
		if n < 4 || n > len(b) {
			return nil, errors.New("m3ua: bad parameter length")
		}
		out = append(out, param{tag, append([]byte(nil), b[4:n]...)})
		n = (n + 3) &^ 3
		if n > len(b) {
			n = len(b)
		}
		b = b[n:]
	}
	return out, nil
}

func read(r *bufio.Reader) (msg, error) {
	var h [8]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return msg{}, err
	}
	n := int(binary.BigEndian.Uint32(h[4:]))
	if h[0] != 1 || n < 8 || n > 1<<16 {
		return msg{}, errors.New("m3ua: not an M3UA message")
	}
	body := make([]byte, n-8)
	if _, err := io.ReadFull(r, body); err != nil {
		return msg{}, err
	}
	ps, err := decodeParams(body)
	return msg{int(h[2]), int(h[3]), ps}, err
}

func (a *ASP) write(b []byte) error {
	a.mu.Lock()
	c := a.conn
	a.mu.Unlock()
	if c == nil {
		return ErrNotActive
	}
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := c.Write(b)
	return err
}

// request sends a message and waits for the answer of the given class and type.
func (a *ASP) request(b []byte, class, typ int) (msg, error) {
	ch := make(chan msg, 1)
	a.mu.Lock()
	a.wait[[2]int{class, typ}] = ch
	a.mu.Unlock()
	if err := a.write(b); err != nil {
		return msg{}, err
	}
	select {
	case m := <-ch:
		return m, nil
	case <-time.After(5 * time.Second):
		return msg{}, fmt.Errorf("m3ua: no answer from the STP (class %d type %d)", class, typ)
	}
}

// Active says whether the ASP carries traffic now.
func (a *ASP) Active() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.active
}

// RC is the routing context in use (configured or registered).
func (a *ASP) RC() uint32 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rc
}

// Run connects to the STP and keeps the association up, reconnecting after a failure, until ctx ends.
// up is told each time it becomes active or stops (may be nil).
func (a *ASP) Run(ctx context.Context, up func(bool, error)) {
	for ctx.Err() == nil {
		err := a.session(ctx, up)
		if up != nil {
			up(false, err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(3 * time.Second):
		}
	}
}

func (a *ASP) session(ctx context.Context, up func(bool, error)) error {
	d := net.Dialer{Timeout: 5 * time.Second}
	c, err := d.DialContext(ctx, "tcp", a.cfg.Addr)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.conn, a.rc, a.active = c, a.cfg.RC, false
	a.mu.Unlock()
	defer func() {
		c.Close()
		a.mu.Lock()
		a.conn, a.active = nil, false
		a.mu.Unlock()
	}()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	done := make(chan error, 1)
	go func() { done <- a.reader(bufio.NewReader(c)) }()
	if err := a.bringUp(); err != nil {
		return err
	}
	if up != nil {
		up(true, nil)
	}
	var beat <-chan time.Time
	if a.cfg.Heartbeat > 0 {
		t := time.NewTicker(a.cfg.Heartbeat)
		defer t.Stop()
		beat = t.C
	}
	for {
		select {
		case err := <-done:
			return err
		case <-beat:
			if _, err := a.request(encode(classASPSM, typeBEAT, param{tagHeartbeat, []byte("bitvoice")}), classASPSM, typeBEATAck); err != nil {
				return err
			}
		}
	}
}

// bringUp: ASP Up, a routing key registered when none is configured, ASP Active.
func (a *ASP) bringUp() error {
	if _, err := a.request(encode(classASPSM, typeASPUP), classASPSM, typeASPUPAck); err != nil {
		return err
	}
	rc := a.cfg.RC
	rcs := map[int]uint32{}
	var list []byte
	if rc != 0 {
		list = u32(rc)
	} else {
		// Dynamic registration (RFC 4666 3.6.1): one routing key per point code, in one request.
		keys := a.cfg.Keys
		if len(keys) == 0 {
			keys = []int{a.cfg.OPC}
		}
		var ps []param
		for i, pc := range keys {
			var key []byte
			key = appendParam(key, tagLocalRKID, u32(uint32(i+1)))
			key = appendParam(key, tagTrafficMode, u32(trafficLoadshare))
			key = appendParam(key, tagDPC, u32(uint32(pc)&0xFFFFFF))
			ps = append(ps, param{tagRoutingKey, key})
		}
		m, err := a.request(encode(classRKM, typeREGReq, ps...), classRKM, typeREGRsp)
		if err != nil {
			return err
		}
		for _, p := range m.params {
			if p.tag != tagRegResult {
				continue
			}
			inner, _ := decodeParams(p.val)
			r := msg{params: inner}
			id, _ := r.get(tagLocalRKID)
			st, _ := r.get(tagRegStatus)
			v, _ := r.get(tagRC)
			if len(id) != 4 || len(st) != 4 || len(v) != 4 {
				return errors.New("m3ua: a registration result without its key, status or routing context")
			}
			n := int(binary.BigEndian.Uint32(id)) - 1
			if n < 0 || n >= len(keys) {
				continue
			}
			if s := binary.BigEndian.Uint32(st); s != 0 {
				return fmt.Errorf("m3ua: the STP refused the routing key for point code %d (status %d)", keys[n], s)
			}
			rcs[keys[n]] = binary.BigEndian.Uint32(v)
			list = append(list, v...)
		}
		if len(rcs) != len(keys) {
			return fmt.Errorf("m3ua: the STP registered %d of %d routing keys", len(rcs), len(keys))
		}
		rc = rcs[keys[0]]
	}
	if _, err := a.request(encode(classASPTM, typeASPAC, param{tagTrafficMode, u32(trafficLoadshare)}, param{tagRC, list}),
		classASPTM, typeASPACAck); err != nil {
		return err
	}
	a.mu.Lock()
	a.rc, a.rcs, a.active = rc, rcs, true
	a.mu.Unlock()
	return nil
}

func appendParam(b []byte, tag uint16, v []byte) []byte {
	b = binary.BigEndian.AppendUint16(b, tag)
	b = binary.BigEndian.AppendUint16(b, uint16(4+len(v)))
	b = append(b, v...)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func (a *ASP) reader(r *bufio.Reader) error {
	for {
		m, err := read(r)
		if err != nil {
			return err
		}
		switch {
		case m.class == classXFER && m.typ == typeDATA:
			v, ok := m.get(tagProtocolData)
			if !ok || len(v) < 12 || a.Receive == nil {
				continue
			}
			a.Receive(Label{OPC: int(binary.BigEndian.Uint32(v)), DPC: int(binary.BigEndian.Uint32(v[4:])),
				SI: int(v[8]), NI: int(v[9]), MP: int(v[10]), SLS: int(v[11])}, v[12:])
			continue
		case m.class == classASPSM && m.typ == typeBEAT:
			hb, _ := m.get(tagHeartbeat)
			_ = a.write(encode(classASPSM, typeBEATAck, param{tagHeartbeat, hb}))
			continue
		case m.class == classSSNM && (m.typ == typeDUNA || m.typ == typeDAVA):
			if v, ok := m.get(tagAffectedPC); ok && a.Unavailable != nil {
				for i := 0; i+4 <= len(v); i += 4 {
					a.Unavailable(int(binary.BigEndian.Uint32(v[i:])&0xFFFFFF), m.typ == typeDUNA)
				}
			}
			continue
		case m.class == classMGMT && m.typ == typeERR:
			code, _ := m.get(tagErrorCode)
			if len(code) == 4 && a.Error != nil {
				a.Error(binary.BigEndian.Uint32(code))
			}
			continue
		}
		a.mu.Lock()
		ch := a.wait[[2]int{m.class, m.typ}]
		delete(a.wait, [2]int{m.class, m.typ})
		a.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
}

// Send carries an MTP3 user message to the STP.
func (a *ASP) Send(l Label, data []byte) error {
	a.mu.Lock()
	active, rc := a.active, a.rc
	if r, ok := a.rcs[l.OPC]; ok {
		rc = r // the routing key of the point code it comes from
	}
	a.mu.Unlock()
	if !active {
		return ErrNotActive
	}
	pd := binary.BigEndian.AppendUint32(nil, uint32(l.OPC))
	pd = binary.BigEndian.AppendUint32(pd, uint32(l.DPC))
	pd = append(pd, byte(l.SI), byte(l.NI), byte(l.MP), byte(l.SLS))
	pd = append(pd, data...)
	return a.write(encode(classXFER, typeDATA, param{tagRC, u32(rc)}, param{tagProtocolData, pd}))
}
