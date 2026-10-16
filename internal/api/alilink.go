package api

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/i3"
	"github.com/biodream-llc/bitvoice/internal/serial"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// The legacy links: the ALI spill out to CAD, and the ALI query to an ALI controller, each over TCP
// or a serial port.

type linkConn interface {
	io.ReadWriteCloser
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}

func dialLink(ctx context.Context, addr string) (linkConn, error) {
	if strings.HasPrefix(addr, "serial:") {
		c, err := serial.Parse(addr)
		if err != nil {
			return nil, err
		}
		return serial.Open(c)
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}

// sendALI sends the ALI spill to CAD.
func sendALI(addr string, a i3.ALI) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := dialLink(ctx, addr)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_, err = c.Write(a.Spill())
	return err
}

// aliLayout is the ALI link's layout as i3 reads it.
func aliLayout(l config.ALILink) []i3.ALIField {
	out := make([]i3.ALIField, len(l.Layout))
	for i, f := range l.Layout {
		out[i] = i3.ALIField{Name: f.Name, Line: f.Line, Col: f.Col, Len: f.Len}
	}
	return out
}

// QueryALI asks the ALI link for a callback number's record (the engine's ALIQuery hook): the
// request, then the answer up to its ETX (or a quiet half second after text with no frame).
func (s *Server) QueryALI(ctx context.Context, number, position string) (i3.ALIRecord, error) {
	l := s.config().System.PSAP.ALILink
	if l.Address == "" {
		return i3.ALIRecord{}, errors.New("no ALI link is set")
	}
	text, err := aliExchange(ctx, l, number, position)
	if err != nil {
		return i3.ALIRecord{}, err
	}
	return i3.ParseALI(text, aliLayout(l))
}

func aliExchange(ctx context.Context, l config.ALILink, number, position string) (string, error) {
	wait := time.Duration(l.TimeoutS) * time.Second
	if wait == 0 {
		wait = 4 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	c, err := dialLink(ctx, l.Address)
	if err != nil {
		return "", err
	}
	defer c.Close()
	deadline, _ := ctx.Deadline()
	_ = c.SetWriteDeadline(deadline)
	if _, err := c.Write(i3.ALIRequest(l.Request, number, position)); err != nil {
		return "", err
	}
	var got []byte
	buf := make([]byte, 512)
	for {
		rd := deadline
		if len(got) > 0 && !strings.ContainsRune(string(got), 0x02) {
			if q := time.Now().Add(500 * time.Millisecond); q.Before(rd) {
				rd = q // unframed text: a quiet half second ends it
			}
		}
		_ = c.SetReadDeadline(rd)
		n, err := c.Read(buf)
		got = append(got, buf[:n]...)
		if i := strings.IndexByte(string(got), 0x02); i >= 0 && strings.IndexByte(string(got[i:]), 0x03) > 0 {
			return string(got), nil
		}
		if len(got) > 8<<10 {
			return "", errors.New("the ALI answer is too long")
		}
		if err != nil {
			if len(got) > 0 && !strings.ContainsRune(string(got), 0x02) {
				return string(got), nil
			}
			if len(got) == 0 {
				return "", errors.New("the ALI link did not answer in time")
			}
			return "", errors.New("the ALI answer was cut off")
		}
	}
}

func (s *Server) aliRoutes() {
	// Try the link with a number: what came back, and what the layout reads in it.
	s.handle("POST /api/psap/ali/test", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Number string         `json:"number"`
			Link   config.ALILink `json:"link"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if in.Link.Address == "" {
			in.Link = s.config().System.PSAP.ALILink
		}
		if in.Link.Address == "" {
			writeError(w, 400, "set the ALI link's address first")
			return
		}
		text, err := aliExchange(r.Context(), in.Link, in.Number, "")
		if err != nil {
			writeJSON(w, 200, map[string]any{"error": err.Error()})
			return
		}
		rec, perr := i3.ParseALI(text, aliLayout(in.Link))
		out := map[string]any{"text": strings.Join(i3.ALILines(text), "\n"), "record": rec}
		if perr != nil {
			out["error"] = perr.Error()
		}
		writeJSON(w, 200, out)
	})
	// Read a sample by a layout, with no link: for setting the layout up.
	s.handle("POST /api/psap/ali/parse", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Text   string            `json:"text"`
			Layout []config.ALIField `json:"layout"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		rec, err := i3.ParseALI(in.Text, aliLayout(config.ALILink{Layout: in.Layout}))
		out := map[string]any{"lines": i3.ALILines(in.Text), "record": rec}
		if err != nil {
			out["error"] = err.Error()
		}
		writeJSON(w, 200, out)
	})
}
