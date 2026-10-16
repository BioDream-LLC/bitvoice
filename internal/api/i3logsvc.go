package api

import (
	"bufio"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/i3"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// The i3 logging service (NENA-STA-010, NG911 Core Services): other elements send their log events
// signed (a compact JWS) to /i3log/LogEvents; each is checked (the signature, and the signer's chain to
// the PCA when its roots are set), kept a line a day in DATA/i3log/received-YYYY-MM-DD.jsonl, and can
// be read back by ID, or listed by time, call or incident.

// ReceivedLogEvent is one log event the service took.
type ReceivedLogEvent struct {
	ID       string         `json:"logEventId"`
	Received time.Time      `json:"received"`
	From     string         `json:"from"`    // the sender's address
	Signer   string         `json:"signer"`  // the signing certificate's subject
	Chained  bool           `json:"chained"` // the signer's certificate chains to the PCA roots
	JWS      string         `json:"jws"`
	Event    map[string]any `json:"event"`
}

func (s *Server) pcaPool(cfg config.Config) *x509.CertPool {
	r := strings.TrimSpace(cfg.System.Core.PCARoots)
	if r == "" {
		return nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(r)) {
		return nil
	}
	return pool
}

func (s *Server) receivedFile(day string) string {
	return filepath.Join(s.i3Dir(), "received-"+day+".jsonl")
}

func (s *Server) receivedOn(day string) []ReceivedLogEvent {
	f, err := os.Open(s.receivedFile(day))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []ReceivedLogEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		var ev ReceivedLogEvent
		if json.Unmarshal(sc.Bytes(), &ev) == nil {
			out = append(out, ev)
		}
	}
	return out
}

// i3logGate lets a request through when the service is on and the sender is allowed.
func (s *Server) i3logGate(w http.ResponseWriter, r *http.Request) (config.Config, bool) {
	cfg := s.config()
	if !cfg.System.Core.Logging {
		http.NotFound(w, r)
		return cfg, false
	}
	if !allowed(cfg.System.Core.AllowFrom, r.RemoteAddr) {
		http.Error(w, "not allowed", http.StatusForbidden)
		return cfg, false
	}
	return cfg, true
}

func i3logError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"code": code, "reason": msg})
}

func (s *Server) i3logRoutes() {
	versions := func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3logGate(w, r); !ok {
			return
		}
		writeJSON(w, 200, s.versions([]string{"EdDSA", "none"})) // a logging service may take unsigned events (5.10)
	}
	s.mux.HandleFunc("GET /i3log/Versions", versions)
	s.mux.HandleFunc("POST /i3log/LogEvents", func(w http.ResponseWriter, r *http.Request) {
		cfg, ok := s.i3logGate(w, r)
		if !ok {
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			i3logError(w, 461, "the log event is larger than 1 MB")
			return
		}
		jws, alg, err := i3.ReadJWS(body)
		if err != nil {
			i3logError(w, 460, err.Error())
			return
		}
		var payload []byte
		var signer string
		chained := false
		switch alg {
		case "none":
			if payload, err = i3.UnsignedPayload(jws); err != nil {
				i3logError(w, 460, err.Error())
				return
			}
		case "":
			i3logError(w, 438, "the log event names no algorithm")
			return
		default:
			signed, err := i3.VerifyJWS(jws, s.pcaPool(cfg), time.Now())
			if err != nil {
				code := 434
				if strings.Contains(err.Error(), "algorithm") {
					code = 438
				}
				i3logError(w, code, err.Error())
				return
			}
			payload, signer, chained = signed.Payload, signed.Subject(), signed.Chained
		}
		var ev map[string]any
		if json.Unmarshal(payload, &ev) != nil {
			i3logError(w, 460, "the log event is not JSON")
			return
		}
		if t, _ := ev["logEventType"].(string); t == "" {
			i3logError(w, 460, "the log event has no logEventType")
			return
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		rec := ReceivedLogEvent{ID: "urn:emergency:uid:logid:" + config.NewID() + ":" + elementID(cfg), Received: time.Now().UTC(), From: host,
			Signer: signer, Chained: chained, JWS: jws, Event: ev}
		line, _ := json.Marshal(rec)
		if s.DataDir == "" {
			i3logError(w, 454, "this server has no data folder to keep log events in")
			return
		}
		s.i3Mu.Lock()
		err = os.MkdirAll(s.i3Dir(), 0o700)
		var f *os.File
		if err == nil {
			f, err = os.OpenFile(s.receivedFile(rec.Received.Format("2006-01-02")), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		}
		if err == nil {
			_, err = f.Write(append(line, '\n'))
			f.Close()
		}
		s.i3Mu.Unlock()
		if err != nil {
			s.Log.Error("i3 logging service: cannot keep a log event", "err", err)
			i3logError(w, 454, "the log event could not be kept")
			return
		}
		writeJSON(w, 201, rec.ID) // the logEventId, as a JSON string
	})
	s.mux.HandleFunc("GET /i3log/LogEvents/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3logGate(w, r); !ok {
			return
		}
		id := r.PathValue("id")
		for _, ev := range s.receivedAll(time.Time{}, time.Now().Add(time.Hour)) {
			if ev.ID == id {
				flat, _ := i3.Flat(ev.JWS)
				writeJSON(w, 200, flat)
				return
			}
		}
		i3logError(w, 404, "no log event with that ID")
	})
	s.mux.HandleFunc("GET /i3log/LogEvents", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3logGate(w, r); !ok {
			return
		}
		q := r.URL.Query()
		from, to, code, msg := timeWindow(q)
		if code != 0 {
			i3logError(w, code, msg)
			return
		}
		typ := q.Get("logEventType")
		var out []map[string]any
		for _, ev := range s.receivedAll(from, to) {
			if typ != "" && ev.Event["logEventType"] != typ {
				continue
			}
			flat, _ := i3.Flat(ev.JWS)
			out = append(out, map[string]any{"logEventId": ev.ID, "logEvent": flat})
		}
		if len(out) == 0 {
			i3logError(w, 404, "no log events")
			return
		}
		items, total := page(r, out)
		writeJSON(w, 200, map[string]any{"count": len(items), "totalCount": total, "logEventContainers": items})
	})
	ids := func(path, field string, pick func(ev ReceivedLogEvent, q url.Values) []string) {
		s.mux.HandleFunc("GET /i3log/"+path, func(w http.ResponseWriter, r *http.Request) {
			if _, ok := s.i3logGate(w, r); !ok {
				return
			}
			q := r.URL.Query()
			from, to, code, msg := timeWindow(q)
			if code != 0 {
				i3logError(w, code, msg)
				return
			}
			if q.Get("area") != "" {
				i3logError(w, 467, "searching by area is not supported here")
				return
			}
			seen := map[string]bool{}
			var out []string
			for _, ev := range s.receivedAll(from, to) {
				for _, v := range pick(ev, q) {
					if v != "" && !seen[v] {
						seen[v] = true
						out = append(out, v)
					}
				}
			}
			if len(out) == 0 {
				i3logError(w, 404, "none found")
				return
			}
			items, total := page(r, out)
			res := map[string]any{"count": len(items), "totalCount": total, field: items}
			if field == "incidentIds" {
				res["logEventIds"] = items // the published YAML names the list so too
			}
			writeJSON(w, 200, res)
		})
	}
	str := func(m map[string]any, k string) string { v, _ := m[k].(string); return v }
	ids("LogEventIds", "logEventIds", func(ev ReceivedLogEvent, q url.Values) []string {
		if c := q.Get("callId"); c != "" && str(ev.Event, "callId") != c {
			return nil
		}
		if i := q.Get("incidentId"); i != "" && str(ev.Event, "incidentId") != i {
			return nil
		}
		return []string{ev.ID}
	})
	ids("CallIds", "callIds", func(ev ReceivedLogEvent, q url.Values) []string {
		if i := q.Get("incidentId"); i != "" && str(ev.Event, "incidentId") != i {
			return nil
		}
		return []string{str(ev.Event, "callId")}
	})
	ids("IncidentIds", "incidentIds", func(ev ReceivedLogEvent, q url.Values) []string { return []string{str(ev.Event, "incidentId")} })
	ids("AgencyIds", "agencyIds", func(ev ReceivedLogEvent, q url.Values) []string {
		if c := q.Get("callId"); c != "" && str(ev.Event, "callId") != c {
			return nil
		}
		if i := q.Get("incidentId"); i != "" && str(ev.Event, "incidentId") != i {
			return nil
		}
		return []string{str(ev.Event, "agencyId")}
	})
	s.mux.HandleFunc("GET /i3log/Conversations", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3logGate(w, r); !ok {
			return
		}
		call := r.URL.Query().Get("callId")
		if call == "" {
			i3logError(w, 454, "give the callId")
			return
		}
		var b strings.Builder
		found, text := false, false
		for _, ev := range s.receivedAll(time.Time{}, time.Now().Add(time.Hour)) {
			if str(ev.Event, "callId") != call {
				continue
			}
			found = true
			if str(ev.Event, "logEventType") != "MessageLogEvent" {
				continue
			}
			text = true
			who := firstNonEmpty(str(ev.Event, "direction"), "")
			body := firstNonEmpty(str(ev.Event, "text"), str(ev.Event, "message"), str(ev.Event, "body"))
			fmt.Fprintf(&b, "<p><time>%s</time> <b>%s</b> %s</p>", html.EscapeString(str(ev.Event, "timestamp")), html.EscapeString(who), html.EscapeString(body))
		}
		switch {
		case !found:
			i3logError(w, 404, "no such call")
		case !text:
			i3logError(w, 464, "no text in this call")
		default:
			writeJSON(w, 200, "<div class=\"conversation\">"+b.String()+"</div>")
		}
	})
	// The Core services page's view of what the service took.
	s.handle("GET /api/core/i3log", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		day := r.URL.Query().Get("day")
		if _, err := time.Parse("2006-01-02", day); err != nil {
			day = time.Now().UTC().Format("2006-01-02")
		}
		evs := s.receivedOn(day)
		items := make([]map[string]any, 0, len(evs))
		for i := len(evs) - 1; i >= 0; i-- {
			ev := evs[i]
			items = append(items, map[string]any{"logEventId": ev.ID, "received": ev.Received, "from": ev.From, "signer": ev.Signer, "chained": ev.Chained,
				"type": ev.Event["logEventType"], "elementId": ev.Event["elementId"], "agencyId": ev.Event["agencyId"], "callId": ev.Event["callId"]})
		}
		writeJSON(w, 200, map[string]any{"day": day, "items": items})
	})
}

// receivedAll is every log event received from one time to another, oldest first.
func (s *Server) receivedAll(from, to time.Time) []ReceivedLogEvent {
	days, _ := filepath.Glob(filepath.Join(s.i3Dir(), "received-*.jsonl"))
	sort.Strings(days)
	var out []ReceivedLogEvent
	for _, d := range days {
		day := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(d), "received-"), ".jsonl")
		t, err := time.Parse("2006-01-02", day)
		if err != nil || t.Add(24*time.Hour).Before(from) || t.After(to) {
			continue
		}
		for _, ev := range s.receivedOn(day) {
			if !ev.Received.Before(from) && !ev.Received.After(to) {
				out = append(out, ev)
			}
		}
	}
	return out
}

// timeWindow reads startTime and endTime (the last day when neither is given).
func timeWindow(q url.Values) (time.Time, time.Time, int, string) {
	from, to := time.Now().UTC().Add(-24*time.Hour), time.Now().UTC().Add(time.Minute)
	if v := q.Get("startTime"); v != "" {
		t, err := i3.ParseTimestamp(v)
		if err != nil {
			return from, to, 465, "startTime is not a timestamp"
		}
		from = t
		if q.Get("endTime") == "" {
			to = from.Add(31 * 24 * time.Hour)
		}
	}
	if v := q.Get("endTime"); v != "" {
		t, err := i3.ParseTimestamp(v)
		if err != nil {
			return from, to, 465, "endTime is not a timestamp"
		}
		to = t
	}
	if to.Before(from) {
		return from, to, 466, "endTime is before startTime"
	}
	return from, to, 0, ""
}
