package api

import (
	"bufio"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
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
	s.mux.HandleFunc("GET /i3log/Versions", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3logGate(w, r); !ok {
			return
		}
		writeJSON(w, 200, map[string]any{"versions": []map[string]any{{"major": 3, "minor": 0}}, "vendor": "BioDream LLC", "serviceInfo": map[string]any{"product": "BitVoice", "version": s.Version}})
	})
	s.mux.HandleFunc("POST /i3log/LogEvents", func(w http.ResponseWriter, r *http.Request) {
		cfg, ok := s.i3logGate(w, r)
		if !ok {
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			i3logError(w, 413, "the log event is larger than 1 MB")
			return
		}
		jws := strings.TrimSpace(string(body))
		if strings.HasPrefix(jws, "{") { // the JWS inside a JSON body
			var wrap map[string]string
			if json.Unmarshal(body, &wrap) == nil {
				jws = firstNonEmpty(wrap["jws"], wrap["logEvent"])
			}
		}
		pool := s.pcaPool(cfg)
		signed, err := i3.VerifyJWS(jws, pool, time.Now())
		if err != nil {
			i3logError(w, 400, err.Error())
			return
		}
		var ev map[string]any
		if json.Unmarshal(signed.Payload, &ev) != nil {
			i3logError(w, 400, "the log event is not JSON")
			return
		}
		if t, _ := ev["logEventType"].(string); t == "" {
			i3logError(w, 400, "the log event has no logEventType")
			return
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		rec := ReceivedLogEvent{ID: "urn:emergency:uid:logid:" + config.NewID() + ":" + elementID(cfg), Received: time.Now().UTC(), From: host,
			Signer: signed.Subject(), Chained: signed.Chained, JWS: jws, Event: ev}
		line, _ := json.Marshal(rec)
		if s.DataDir == "" {
			i3logError(w, 503, "this server has no data folder to keep log events in")
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
			i3logError(w, 500, "the log event could not be kept")
			return
		}
		writeJSON(w, 201, map[string]any{"logEventId": rec.ID})
	})
	s.mux.HandleFunc("GET /i3log/LogEvents/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3logGate(w, r); !ok {
			return
		}
		id := r.PathValue("id")
		days, _ := filepath.Glob(filepath.Join(s.i3Dir(), "received-*.jsonl"))
		sort.Sort(sort.Reverse(sort.StringSlice(days)))
		for _, d := range days {
			day := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(d), "received-"), ".jsonl")
			for _, ev := range s.receivedOn(day) {
				if ev.ID == id {
					w.Header().Set("Content-Type", "application/jose")
					io.WriteString(w, ev.JWS)
					return
				}
			}
		}
		i3logError(w, 404, "no log event with that ID")
	})
	s.mux.HandleFunc("GET /i3log/LogEvents", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.i3logGate(w, r); !ok {
			return
		}
		q := r.URL.Query()
		start, end := time.Now().UTC().Add(-24*time.Hour), time.Now().UTC()
		if t, err := time.Parse(time.RFC3339, q.Get("start")); err == nil {
			start = t
		}
		if t, err := time.Parse(time.RFC3339, q.Get("end")); err == nil {
			end = t
		}
		if end.Sub(start) > 31*24*time.Hour {
			i3logError(w, 400, "ask for 31 days at most")
			return
		}
		callID, incID := q.Get("callId"), q.Get("incidentId")
		out := []map[string]any{}
		for d := start.Truncate(24 * time.Hour); !d.After(end); d = d.Add(24 * time.Hour) {
			for _, ev := range s.receivedOn(d.Format("2006-01-02")) {
				if ev.Received.Before(start) || ev.Received.After(end) {
					continue
				}
				if callID != "" && ev.Event["callId"] != callID || incID != "" && ev.Event["incidentId"] != incID {
					continue
				}
				out = append(out, map[string]any{"logEventId": ev.ID, "logEvent": ev.JWS})
			}
		}
		writeJSON(w, 200, map[string]any{"count": len(out), "logEventContainers": out})
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
