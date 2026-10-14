package api

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" database/sql driver, already BitVoice's

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// csvColumns are Asterisk's Master.csv columns, so tools made for Asterisk call records read the file.
var csvColumns = []string{"accountcode", "src", "dst", "dcontext", "clid", "channel", "dstchannel", "lastapp", "lastdata",
	"start", "answer", "end", "duration", "billsec", "disposition", "amaflags", "uniqueid", "userfield"}

// asteriskDisposition writes BitVoice's outcome the way Asterisk does.
func asteriskDisposition(d string) string {
	switch d {
	case "answered":
		return "ANSWERED"
	case "busy":
		return "BUSY"
	case "failed":
		return "FAILED"
	}
	return "NO ANSWER"
}

// asteriskRow is a call record as Asterisk's cdr columns.
func asteriskRow(r store.CDR) []string {
	at := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format("2006-01-02 15:04:05")
	}
	clid := r.From
	if r.FromName != "" {
		clid = fmt.Sprintf("%q <%s>", r.FromName, r.From)
	}
	channel := "SIP/" + firstNonEmpty(r.Ext, r.From)
	dst := "SIP/" + firstNonEmpty(r.AnsweredExt, r.To)
	if r.Trunk != "" && r.Direction == "outbound" {
		dst = "SIP/" + r.Trunk
	}
	return []string{firstNonEmpty(r.Account, r.Department), r.From, r.To, r.Direction, clid, channel, dst, "Dial", r.To,
		at(r.Start), at(r.Answer), at(r.End), strconv.Itoa(r.Duration), strconv.Itoa(r.Billsec),
		asteriskDisposition(r.Disposition), "3", r.ID, r.Cause}
}

// cdrOut holds the outputs' connections between records.
type cdrOut struct {
	mu     sync.Mutex
	syslog net.Conn
	slKey  string
	pg     *sql.DB
	pgKey  string
	pgMade bool
}

// RunCDROutputs sends each call record, as it is logged, to syslog, the day's CSV file and an
// external PostgreSQL table, as the settings say.
func (s *Server) RunCDROutputs(ctx context.Context) {
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
	defer s.cdrOut.close()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.C:
			r, ok := ev.Data.(store.CDR)
			if !ok || ev.Type != "call.logged" {
				continue
			}
			o := s.config().System.CDROutputs
			if o.Syslog.Host != "" {
				if err := s.cdrOut.toSyslog(o.Syslog, s.config().System.Name, r); err != nil && s.Log != nil {
					s.Log.Warn("call record not sent to syslog", "err", err)
				}
			}
			if o.CSV {
				if err := s.writeCSV(r); err != nil && s.Log != nil {
					s.Log.Warn("call record not written to CSV", "err", err)
				}
			}
			if o.Postgres.URL != "" {
				pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				if err := s.cdrOut.toPostgres(pctx, o.Postgres, r); err != nil && s.Log != nil {
					s.Log.Warn("call record not written to PostgreSQL", "err", err)
				}
				cancel()
			}
		}
	}
}

func (o *cdrOut) close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.syslog != nil {
		o.syslog.Close()
	}
	if o.pg != nil {
		o.pg.Close()
	}
}

var hostSafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// toSyslog sends the record as one RFC 5424 message, its JSON as the text; over TCP and TLS framed by
// octet counting (RFC 6587).
func (o *cdrOut) toSyslog(sl config.CDRSyslog, system string, r store.CDR) error {
	port := sl.Port
	if port == 0 {
		port = 514
		if sl.Transport == "tls" {
			port = 6514
		}
	}
	addr := net.JoinHostPort(sl.Host, strconv.Itoa(port))
	key := sl.Transport + "|" + addr
	body, _ := json.Marshal(r)
	host := hostSafe.ReplaceAllString(system, "-")
	if host == "" {
		host = "bitvoice"
	}
	msg := fmt.Sprintf("<%d>1 %s %s bitvoice - cdr - %s", sl.Facility*8+6, time.Now().UTC().Format(time.RFC3339Nano), host, body)
	o.mu.Lock()
	defer o.mu.Unlock()
	for try := 0; try < 2; try++ {
		if o.syslog == nil || o.slKey != key {
			if o.syslog != nil {
				o.syslog.Close()
			}
			var c net.Conn
			var err error
			d := net.Dialer{Timeout: 5 * time.Second}
			switch sl.Transport {
			case "tls":
				c, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: sl.Host, MinVersion: tls.VersionTLS12})
			case "tcp":
				c, err = d.Dial("tcp", addr)
			default:
				c, err = d.Dial("udp", addr)
			}
			if err != nil {
				o.syslog = nil
				return err
			}
			o.syslog, o.slKey = c, key
		}
		out := msg
		if sl.Transport != "udp" {
			out = strconv.Itoa(len(msg)) + " " + msg
		}
		_ = o.syslog.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := o.syslog.Write([]byte(out)); err != nil {
			o.syslog.Close()
			o.syslog = nil
			continue // the connection dropped: once more on a new one
		}
		return nil
	}
	return errors.New("the syslog server did not take the message")
}

// toPostgres inserts the record, creating the table (Asterisk's cdr columns) the first time.
func (o *cdrOut) toPostgres(ctx context.Context, pg config.CDRPostgres, r store.CDR) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.pg == nil || o.pgKey != pg.URL {
		if o.pg != nil {
			o.pg.Close()
		}
		db, err := sql.Open("pgx", pg.URL)
		if err != nil {
			return err
		}
		db.SetMaxOpenConns(2)
		o.pg, o.pgKey, o.pgMade = db, pg.URL, false
	}
	table := pg.Table // checked against a strict pattern when the settings were saved
	if !o.pgMade {
		_, err := o.pg.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+table+` (
			calldate timestamptz NOT NULL, clid text, src text, dst text, dcontext text, channel text, dstchannel text,
			lastapp text, lastdata text, duration integer, billsec integer, disposition text, amaflags integer,
			accountcode text, uniqueid text PRIMARY KEY, userfield text, answer timestamptz, "end" timestamptz,
			direction text, trunk text, cost double precision, mos double precision)`)
		if err != nil {
			return err
		}
		o.pgMade = true
	}
	row := asteriskRow(r)
	var answer any
	if !r.Answer.IsZero() {
		answer = r.Answer
	}
	_, err := o.pg.ExecContext(ctx, `INSERT INTO `+table+` (calldate, clid, src, dst, dcontext, channel, dstchannel, lastapp, lastdata,
		duration, billsec, disposition, amaflags, accountcode, uniqueid, userfield, answer, "end", direction, trunk, cost, mos)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22) ON CONFLICT (uniqueid) DO NOTHING`,
		r.Start, row[4], row[1], row[2], row[3], row[5], row[6], row[7], row[8], r.Duration, r.Billsec, row[14], 3,
		row[0], r.ID, row[17], answer, r.End, r.Direction, r.Trunk, r.Cost, r.MOS)
	return err
}

// csvDir is where the day files go.
func (s *Server) csvDir() string { return filepath.Join(s.Store.Dir(), "cdr-csv") }

var csvMu sync.Mutex

// writeCSV appends the record to its day's file (UTC days, as Asterisk's file is one stream).
func (s *Server) writeCSV(r store.CDR) error {
	csvMu.Lock()
	defer csvMu.Unlock()
	if err := os.MkdirAll(s.csvDir(), 0o700); err != nil {
		return err
	}
	p := filepath.Join(s.csvDir(), r.Start.UTC().Format(time.DateOnly)+".csv")
	_, statErr := os.Stat(p)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if os.IsNotExist(statErr) {
		_ = w.Write(csvColumns)
	}
	row := asteriskRow(r)
	for i := range row {
		row[i] = csvSafe(row[i])
	}
	_ = w.Write(row)
	w.Flush()
	return w.Error()
}

var dayRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func (s *Server) cdrOutputRoutes() {
	s.handle("GET /api/cdr/csv", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		entries, _ := os.ReadDir(s.csvDir())
		type day struct {
			Day   string `json:"day"`
			Bytes int64  `json:"bytes"`
		}
		out := []day{}
		for _, e := range entries {
			d, ok := strings.CutSuffix(e.Name(), ".csv")
			if !ok || !dayRe.MatchString(d) {
				continue
			}
			if info, err := e.Info(); err == nil {
				out = append(out, day{d, info.Size()})
			}
		}
		slices.SortFunc(out, func(a, b day) int { return strings.Compare(b.Day, a.Day) })
		writeJSON(w, 200, map[string]any{"items": out})
	})
	s.handle("GET /api/cdr/csv/{day}", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		d := strings.TrimSuffix(r.PathValue("day"), ".csv")
		if !dayRe.MatchString(d) {
			writeError(w, http.StatusNotFound, "no such day")
			return
		}
		b, err := os.ReadFile(filepath.Join(s.csvDir(), d+".csv"))
		if err != nil {
			writeError(w, http.StatusNotFound, "no calls written that day")
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="cdr-`+d+`.csv"`)
		_, _ = w.Write(b)
	})
	// Try the outputs: a test record to syslog and PostgreSQL.
	s.handle("POST /api/cdr/outputs/test", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		o := s.config().System.CDROutputs
		rec := store.CDR{ID: "test-" + strconv.FormatInt(time.Now().UnixNano(), 36), Start: time.Now().UTC(), End: time.Now().UTC(),
			Direction: "test", From: "100", To: "test", Disposition: "answered", Cause: "test record from Settings"}
		res := map[string]string{}
		if o.Syslog.Host != "" {
			if err := s.cdrOut.toSyslog(o.Syslog, s.config().System.Name, rec); err != nil {
				res["syslog"] = err.Error()
			} else {
				res["syslog"] = "ok"
			}
		}
		if o.Postgres.URL != "" {
			ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			if err := s.cdrOut.toPostgres(ctx, o.Postgres, rec); err != nil {
				res["postgres"] = err.Error()
			} else {
				res["postgres"] = "ok"
			}
			cancel()
		}
		writeJSON(w, 200, res)
	})
}
