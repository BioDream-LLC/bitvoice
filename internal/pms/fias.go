// Package pms links BitVoice to a hotel's property management system over FIAS, the record protocol
// Oracle Opera and many other hotel systems speak: each record is STX, a two-letter type and
// |-separated fields with two-letter names, then ETX. The hotel system listens; BitVoice connects,
// describes itself (LD, LR), and keeps the link up with LA. It receives check-ins (GI), changes (GC),
// check-outs (GO), wake-up bookings (WR, WC) and database resyncs (DS..DE), and sends wake-up
// results (WA), room status (RE) and call charges (PS).
package pms

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	stx = 0x02
	etx = 0x03
)

// Record is one FIAS record: its type and fields.
type Record struct {
	Type   string
	Fields map[string]string
}

// Encode frames a record. Fields are written in name order, so records are the same every time.
func (r Record) Encode() []byte {
	var b strings.Builder
	b.WriteByte(stx)
	b.WriteString(r.Type)
	b.WriteByte('|')
	keys := make([]string, 0, len(r.Fields))
	for k := range r.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if r.Type == "LR" {
		// A link record's order is fixed by the specification: LR|RIxx|FLxxxx|.
		sort.SliceStable(keys, func(i, j int) bool { return keys[i] == "RI" && keys[j] != "RI" })
	}
	for _, k := range keys {
		v := strings.NewReplacer("|", "/", "\x02", "", "\x03", "").Replace(r.Fields[k])
		b.WriteString(k + v + "|")
	}
	b.WriteByte(etx)
	return []byte(b.String())
}

// Parse reads a record's text (between STX and ETX).
func Parse(s string) (Record, error) {
	parts := strings.Split(s, "|")
	if len(parts[0]) != 2 {
		return Record{}, fmt.Errorf("pms: not a FIAS record: %q", s)
	}
	r := Record{Type: parts[0], Fields: map[string]string{}}
	for _, f := range parts[1:] {
		if len(f) >= 2 {
			r.Fields[f[:2]] = f[2:]
		}
	}
	return r, nil
}

// Handler is told what the hotel system says.
type Handler interface {
	CheckIn(room, name, account, language string)
	CheckOut(room string)
	WakeSet(room string, at time.Time)
	WakeClear(room string)
}

// Link is a FIAS link to the hotel system, reconnecting when it drops.
type Link struct {
	Addr    string
	Handler Handler
	Log     *slog.Logger
	Loc     *time.Location // the hotel's time zone, for dates and times on the link

	mu      sync.Mutex
	conn    net.Conn
	up      bool
	last    time.Time // last record from the hotel system
	seq     int       // posting sequence
	pending []Record  // records kept while the link is down
}

// Status is the link's state, for the screen.
type Status struct {
	Up       bool      `json:"up"`
	LastSeen time.Time `json:"lastSeen,omitzero"`
	Waiting  int       `json:"waiting"` // records kept until the link is back
}

func (l *Link) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Status{Up: l.up, LastSeen: l.last, Waiting: len(l.pending)}
}

func (l *Link) loc() *time.Location {
	if l.Loc == nil {
		return time.Local
	}
	return l.Loc
}

func (l *Link) stamp(f map[string]string, t time.Time) map[string]string {
	t = t.In(l.loc())
	f["DA"], f["TI"] = t.Format("060102"), t.Format("150405")
	return f
}

// Send writes a record now, or keeps it (up to a thousand) until the link is back.
func (l *Link) Send(r Record) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn != nil && l.up {
		_ = l.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := l.conn.Write(r.Encode()); err == nil {
			return
		}
	}
	if len(l.pending) < 1000 {
		l.pending = append(l.pending, r)
	}
}

// WakeResult tells the hotel system how a wake-up call went: OK answered, NR no answer.
func (l *Link) WakeResult(room string, at time.Time, answered bool) {
	as := "NR"
	if answered {
		as = "OK"
	}
	l.Send(Record{Type: "WA", Fields: l.stamp(map[string]string{"RN": room, "AS": as}, at)})
}

// WakeSet tells the hotel system a guest booked a wake-up call from the room phone.
func (l *Link) WakeSet(room string, at time.Time) {
	l.Send(Record{Type: "WR", Fields: l.stamp(map[string]string{"RN": room}, at)})
}

// RoomStatus sends housekeeping's status for a room (RS 1 to 9, as the hotel system defines them).
func (l *Link) RoomStatus(room, status string) {
	l.Send(Record{Type: "RE", Fields: l.stamp(map[string]string{"RN": room, "RS": status}, time.Now())})
}

// Charge posts a call to the guest's bill: amount in cents, number dialled, duration.
func (l *Link) Charge(room, dialled string, cents int64, dur time.Duration, at time.Time) {
	l.mu.Lock()
	l.seq++
	seq := l.seq
	l.mu.Unlock()
	h, m, s := int(dur.Hours()), int(dur.Minutes())%60, int(dur.Seconds())%60
	// Dialled digits are digits only (no + or spaces), at most 20. The sales outlet is left out: it
	// is numeric and only needed when one interface posts more than one kind of charge.
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, dialled)
	if len(digits) > 20 {
		digits = digits[:20]
	}
	l.Send(Record{Type: "PS", Fields: l.stamp(map[string]string{"RN": room, "P#": strconv.Itoa(seq), "PT": "C",
		"TA": strconv.FormatInt(cents, 10), "DD": digits, "DU": fmt.Sprintf("%02d%02d%02d", h, m, s)}, at)})
}

// Run keeps the link up until ctx ends.
func (l *Link) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := l.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if l.Log != nil {
			l.Log.Warn("hotel system link down", "addr", l.Addr, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (l *Link) session(ctx context.Context) error {
	d := net.Dialer{Timeout: 10 * time.Second}
	c, err := d.DialContext(ctx, "tcp", l.Addr)
	if err != nil {
		return err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	l.mu.Lock()
	l.conn = c
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.conn, l.up = nil, false
		l.mu.Unlock()
	}()
	now := time.Now()
	hello := []Record{
		{Type: "LD", Fields: l.stamp(map[string]string{"V#": "1.0", "IF": "PB"}, now)},
		{Type: "LR", Fields: map[string]string{"RI": "GI", "FL": "RNG#GNGLGS"}},
		{Type: "LR", Fields: map[string]string{"RI": "GO", "FL": "RNG#GS"}},
		{Type: "LR", Fields: map[string]string{"RI": "GC", "FL": "RNG#GNGLROGS"}},
		{Type: "LR", Fields: map[string]string{"RI": "WR", "FL": "RNDATI"}},
		{Type: "LR", Fields: map[string]string{"RI": "WC", "FL": "RNDATI"}},
		{Type: "LR", Fields: map[string]string{"RI": "WA", "FL": "RNDATIAS"}},
		{Type: "LR", Fields: map[string]string{"RI": "RE", "FL": "RNRSDATI"}},
		{Type: "LR", Fields: map[string]string{"RI": "PS", "FL": "RNP#PTTADDDUDATI"}},
		{Type: "LA", Fields: l.stamp(map[string]string{}, now)},
	}
	for _, r := range hello {
		if _, err := c.Write(r.Encode()); err != nil {
			return err
		}
	}
	l.mu.Lock()
	l.up = true
	pending := l.pending
	l.pending = nil
	l.mu.Unlock()
	for _, r := range pending {
		l.Send(r)
	}
	if l.Log != nil {
		l.Log.Info("hotel system link up", "addr", l.Addr)
	}
	// Keep-alive: an LA every minute; the hotel system answers with its own.
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				l.mu.Lock()
				same := l.conn == c
				l.mu.Unlock()
				if !same {
					return
				}
				l.Send(Record{Type: "LA", Fields: l.stamp(map[string]string{}, time.Now())})
			}
		}
	}()
	br := bufio.NewReader(c)
	for {
		if _, err := br.ReadBytes(stx); err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("closed by the hotel system")
			}
			return err
		}
		body, err := br.ReadBytes(etx)
		if err != nil {
			return err
		}
		r, err := Parse(string(body[:len(body)-1]))
		if err != nil {
			continue
		}
		l.mu.Lock()
		l.last = time.Now()
		l.mu.Unlock()
		l.handle(r)
	}
}

// handle acts on one record from the hotel system.
func (l *Link) handle(r Record) {
	f := r.Fields
	room := strings.TrimSpace(f["RN"])
	switch r.Type {
	case "GI", "GC":
		if room != "" && l.Handler != nil {
			l.Handler.CheckIn(room, strings.TrimSpace(f["GN"]), f["G#"], language(f["GL"]))
		}
		if r.Type == "GC" && f["RO"] != "" && f["RO"] != room && l.Handler != nil {
			l.Handler.CheckOut(strings.TrimSpace(f["RO"])) // a room move: out of the old room
		}
	case "GO":
		if room != "" && l.Handler != nil {
			l.Handler.CheckOut(room)
		}
	case "WR":
		if t, ok := l.when(f["DA"], f["TI"]); ok && room != "" && l.Handler != nil {
			l.Handler.WakeSet(room, t)
		}
	case "WC":
		if room != "" && l.Handler != nil {
			l.Handler.WakeClear(room)
		}
	case "LS", "DR": // the hotel system (re)started the link, or asks for a resync: say hello again
		if r.Type == "LS" {
			l.Send(Record{Type: "LA", Fields: l.stamp(map[string]string{}, time.Now())})
		}
	}
}

// when reads a FIAS date (yymmdd, today when empty) and time (hhmm or hhmmss).
func (l *Link) when(da, ti string) (time.Time, bool) {
	now := time.Now().In(l.loc())
	if len(ti) < 4 {
		return time.Time{}, false
	}
	h, err1 := strconv.Atoi(ti[:2])
	m, err2 := strconv.Atoi(ti[2:4])
	if err1 != nil || err2 != nil || h > 23 || m > 59 {
		return time.Time{}, false
	}
	// Seconds are kept: the wake-up answer must give back the time exactly as requested (HHMMSS).
	sec := 0
	if len(ti) >= 6 {
		if v, err := strconv.Atoi(ti[4:6]); err == nil && v <= 59 {
			sec = v
		}
	}
	y, mo, d := now.Date()
	if len(da) == 6 {
		t, err := time.ParseInLocation("060102", da, l.loc())
		if err != nil {
			return time.Time{}, false
		}
		y, mo, d = t.Date()
	}
	at := time.Date(y, mo, d, h, m, sec, 0, l.loc())
	if len(da) != 6 && !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at, true
}

// language turns FIAS's guest language (EA English, ES, FR, GE...) into a language code.
func language(gl string) string {
	switch strings.ToUpper(strings.TrimSpace(gl)) {
	case "EA", "EN", "E":
		return "en"
	case "SP", "ES":
		return "es"
	case "FR", "F":
		return "fr"
	case "GE", "DE", "G":
		return "de"
	case "IT", "I":
		return "it"
	case "PO", "PT":
		return "pt"
	case "DU", "NL":
		return "nl"
	case "PL":
		return "pl"
	}
	return ""
}
