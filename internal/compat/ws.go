package compat

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// wsConn is the server end of a WebSocket (RFC 6455): enough for an event stream, sending text frames and
// answering pings and close. Frames from the client must be masked; larger than 64 KiB are refused.
type wsConn struct {
	c  net.Conn
	r  *bufio.Reader
	mu sync.Mutex
}

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// upgrade turns an HTTP request into a WebSocket, or writes an error and returns nil.
func upgrade(w http.ResponseWriter, r *http.Request) *wsConn {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || !headerHas(r.Header.Get("Connection"), "upgrade") ||
		r.Header.Get("Sec-WebSocket-Version") != "13" || r.Header.Get("Sec-WebSocket-Key") == "" {
		http.Error(w, "this address needs a WebSocket connection", http.StatusBadRequest)
		return nil
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "WebSocket not available here", http.StatusInternalServerError)
		return nil
	}
	sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + wsGUID))
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil
	}
	_ = conn.SetDeadline(time.Time{})
	_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
		base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n")
	if err := rw.Flush(); err != nil {
		conn.Close()
		return nil
	}
	return &wsConn{c: conn, r: rw.Reader}
}

func headerHas(v, token string) bool {
	for _, p := range strings.Split(v, ",") {
		if strings.EqualFold(strings.TrimSpace(p), token) {
			return true
		}
	}
	return false
}

func (ws *wsConn) writeFrame(op byte, payload []byte) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	hdr := []byte{0x80 | op}
	switch n := len(payload); {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n < 65536:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	_ = ws.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := ws.c.Write(append(hdr, payload...)); err != nil {
		return err
	}
	return nil
}

// WriteText sends one text message.
func (ws *wsConn) WriteText(b []byte) error { return ws.writeFrame(1, b) }

// readLoop answers pings and returns when the client closes or the connection fails.
func (ws *wsConn) readLoop() error {
	for {
		var h [2]byte
		if _, err := io.ReadFull(ws.r, h[:]); err != nil {
			return err
		}
		op, masked, n := h[0]&0x0f, h[1]&0x80 != 0, uint64(h[1]&0x7f)
		if !masked {
			return errors.New("unmasked frame from client")
		}
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(ws.r, b[:]); err != nil {
				return err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(ws.r, b[:]); err != nil {
				return err
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if n > 65536 {
			return errors.New("frame too large")
		}
		var key [4]byte
		if _, err := io.ReadFull(ws.r, key[:]); err != nil {
			return err
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(ws.r, data); err != nil {
			return err
		}
		for i := range data {
			data[i] ^= key[i%4]
		}
		switch op {
		case 8: // close: echo it and stop
			_ = ws.writeFrame(8, data[:min(len(data), 2)])
			return io.EOF
		case 9: // ping
			_ = ws.writeFrame(10, data)
		}
	}
}

func (ws *wsConn) Close() { _ = ws.c.Close() }
