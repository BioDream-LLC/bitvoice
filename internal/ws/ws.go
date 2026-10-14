// Package ws is the server side of WebSocket (RFC 6455), for SIP over WebSocket (RFC 7118): the
// handshake on an HTTP request, then whole messages in and out. Only the standard library: SHA-1 for
// the accept key, as the RFC requires.
package ws

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

const guid = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Opcodes.
const (
	OpText   = 1
	OpBinary = 2
	opClose  = 8
	opPing   = 9
	opPong   = 10
)

// ErrClosed is returned once the peer closed the connection.
var ErrClosed = errors.New("ws: closed")

// Conn is one WebSocket connection.
type Conn struct {
	c       net.Conn
	r       *bufio.Reader
	wmu     sync.Mutex
	MaxSize int // largest message accepted (default 64 KiB)
	// Protocol is the subprotocol agreed in the handshake.
	Protocol string
	Remote   string
}

// Accept answers the opening handshake, agreeing to one of protocols (the client must offer it), and
// takes over the connection. On failure it has written the HTTP error.
func Accept(w http.ResponseWriter, r *http.Request, protocols ...string) (*Conn, error) {
	if r.Method != http.MethodGet || !headerHas(r.Header, "Connection", "upgrade") || !headerHas(r.Header, "Upgrade", "websocket") {
		http.Error(w, "a WebSocket upgrade is expected here", http.StatusBadRequest)
		return nil, errors.New("ws: not an upgrade")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		http.Error(w, "WebSocket version 13 only", http.StatusUpgradeRequired)
		return nil, errors.New("ws: version")
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if b, err := base64.StdEncoding.DecodeString(key); err != nil || len(b) != 16 {
		http.Error(w, "bad Sec-WebSocket-Key", http.StatusBadRequest)
		return nil, errors.New("ws: key")
	}
	proto := ""
	if len(protocols) > 0 {
		for _, p := range strings.Split(strings.Join(r.Header.Values("Sec-WebSocket-Protocol"), ","), ",") {
			if p = strings.TrimSpace(p); slices.Contains(protocols, strings.ToLower(p)) {
				proto = p
				break
			}
		}
		if proto == "" {
			http.Error(w, "offer the subprotocol "+strings.Join(protocols, " or "), http.StatusBadRequest)
			return nil, errors.New("ws: no subprotocol")
		}
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "WebSocket is not available here", http.StatusInternalServerError)
		return nil, errors.New("ws: cannot hijack")
	}
	c, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum([]byte(key + guid))
	resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
		base64.StdEncoding.EncodeToString(sum[:]) + "\r\n"
	if proto != "" {
		resp += "Sec-WebSocket-Protocol: " + proto + "\r\n"
	}
	resp += "\r\n"
	_ = c.SetDeadline(time.Time{})
	if _, err := c.Write([]byte(resp)); err != nil {
		c.Close()
		return nil, err
	}
	return &Conn{c: c, r: rw.Reader, MaxSize: 64 << 10, Protocol: proto, Remote: c.RemoteAddr().String()}, nil
}

func headerHas(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, t := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

// NetConn is the connection underneath (its addresses).
func (c *Conn) NetConn() net.Conn { return c.c }

// Read returns the next whole message (text or binary), answering pings and closes on the way.
// idle is how long it waits for anything at all.
func (c *Conn) Read(idle time.Duration) (op int, msg []byte, err error) {
	var buf []byte
	first := 0
	for {
		_ = c.c.SetReadDeadline(time.Now().Add(idle))
		var h [2]byte
		if _, err := io.ReadFull(c.r, h[:]); err != nil {
			return 0, nil, err
		}
		fin, opcode := h[0]&0x80 != 0, int(h[0]&0x0F)
		if h[0]&0x70 != 0 {
			c.closeWith(1002)
			return 0, nil, errors.New("ws: reserved bits set")
		}
		if h[1]&0x80 == 0 {
			c.closeWith(1002)
			return 0, nil, errors.New("ws: client frames must be masked")
		}
		n := uint64(h[1] & 0x7F)
		switch n {
		case 126:
			var e [2]byte
			if _, err := io.ReadFull(c.r, e[:]); err != nil {
				return 0, nil, err
			}
			n = uint64(binary.BigEndian.Uint16(e[:]))
		case 127:
			var e [8]byte
			if _, err := io.ReadFull(c.r, e[:]); err != nil {
				return 0, nil, err
			}
			n = binary.BigEndian.Uint64(e[:])
		}
		if opcode >= 8 && (n > 125 || !fin) {
			c.closeWith(1002)
			return 0, nil, errors.New("ws: bad control frame")
		}
		if n > uint64(c.MaxSize) || uint64(len(buf))+n > uint64(c.MaxSize) {
			c.closeWith(1009)
			return 0, nil, errors.New("ws: message too big")
		}
		var mask [4]byte
		if _, err := io.ReadFull(c.r, mask[:]); err != nil {
			return 0, nil, err
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(c.r, payload); err != nil {
			return 0, nil, err
		}
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
		switch opcode {
		case opPing:
			_ = c.write(opPong, payload)
			continue
		case opPong:
			continue
		case opClose:
			code := []byte{0x03, 0xE8}
			if len(payload) >= 2 {
				code = payload[:2]
			}
			_ = c.write(opClose, code)
			c.c.Close()
			return 0, nil, ErrClosed
		case 0: // continuation
			if first == 0 {
				c.closeWith(1002)
				return 0, nil, errors.New("ws: continuation without a start")
			}
		case OpText, OpBinary:
			if first != 0 {
				c.closeWith(1002)
				return 0, nil, errors.New("ws: new message inside a fragmented one")
			}
			first = opcode
		default:
			c.closeWith(1002)
			return 0, nil, errors.New("ws: unknown opcode")
		}
		buf = append(buf, payload...)
		if fin {
			return first, buf, nil
		}
	}
}

// Write sends one whole message.
func (c *Conn) Write(op int, b []byte) error { return c.write(op, b) }

func (c *Conn) write(op int, b []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	h := []byte{0x80 | byte(op)}
	switch n := len(b); {
	case n < 126:
		h = append(h, byte(n))
	case n < 1<<16:
		h = append(h, 126)
		h = binary.BigEndian.AppendUint16(h, uint16(n))
	default:
		h = append(h, 127)
		h = binary.BigEndian.AppendUint64(h, uint64(n))
	}
	_ = c.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := c.c.Write(append(h, b...))
	return err
}

// Ping sends a ping, to keep NATs and proxies from closing an idle connection.
func (c *Conn) Ping() error { return c.write(opPing, nil) }

func (c *Conn) closeWith(code uint16) {
	_ = c.write(opClose, binary.BigEndian.AppendUint16(nil, code))
	c.c.Close()
}

// Close ends the connection with a normal closure.
func (c *Conn) Close() error {
	c.closeWith(1000)
	return nil
}
