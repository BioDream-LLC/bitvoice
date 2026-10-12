package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/store"
)

// metrics counts what has happened since BitVoice started, for Prometheus and OpenTelemetry. Gauges
// (calls now, phones registered, trunks up, callers waiting) are read when asked.
type metrics struct {
	mu        sync.Mutex
	calls     map[[2]string]uint64 // direction, disposition
	talk      map[string]float64   // direction: seconds answered
	mosBucket [len(mosBounds) + 1]uint64
	mosSum    float64
	mosCount  uint64
	packets   uint64
	lost      uint64
	callbacks uint64
	texts     map[string]uint64 // in, out
}

var mosBounds = [...]float64{2, 2.5, 3, 3.5, 3.6, 4, 4.2, 4.4}

func (m *metrics) init() {
	m.calls = map[[2]string]uint64{}
	m.talk = map[string]float64{}
	m.texts = map[string]uint64{}
}

func (m *metrics) addCall(r store.CDR) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls[[2]string{r.Direction, r.Disposition}]++
	m.talk[r.Direction] += float64(r.Billsec)
	if r.MOS > 0 {
		i := 0
		for i < len(mosBounds) && r.MOS > mosBounds[i] {
			i++
		}
		m.mosBucket[i]++
		m.mosSum += r.MOS
		m.mosCount++
	}
	for _, q := range r.Quality {
		m.packets += q.Packets
		m.lost += q.Lost
	}
}

// RunMetrics counts events as they happen and, when an OpenTelemetry collector is set, pushes to it.
func (s *Server) RunMetrics(ctx context.Context) {
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.C:
			switch d := ev.Data.(type) {
			case store.CDR:
				if ev.Type == "call.logged" {
					s.metrics.addCall(d)
					s.noteAnswered(d)
				}
			case store.Callback:
				if ev.Type == "callback.new" {
					s.metrics.mu.Lock()
					s.metrics.callbacks++
					s.metrics.mu.Unlock()
				}
			}
		case now := <-tick.C:
			o := s.config().System.Metrics
			every := time.Duration(o.IntervalSeconds) * time.Second
			if every < 15*time.Second {
				every = 60 * time.Second
			}
			if o.OTLPURL != "" && now.Sub(last) >= every {
				last = now
				err := s.pushOTLP(ctx, s.samples())
				s.metricsMu.Lock()
				s.otlpErr = ""
				if err != nil {
					s.otlpErr = err.Error()
				}
				s.metricsMu.Unlock()
			}
		}
	}
}

// sample is one measurement: a gauge, a counter, or one bucket set of a histogram.
type sample struct {
	name, help, kind string // kind: gauge, counter, histogram
	labels           map[string]string
	value            float64
	buckets          []uint64 // histogram: count in each bucket (not cumulative), last is +Inf
	sum              float64
	count            uint64
}

func (s *Server) samples() []sample {
	var out []sample
	g := func(name, help string, v float64, kv ...string) {
		out = append(out, sample{name: name, help: help, kind: "gauge", labels: labels(kv), value: v})
	}
	c := func(name, help string, v float64, kv ...string) {
		out = append(out, sample{name: name, help: help, kind: "counter", labels: labels(kv), value: v})
	}
	g("bitvoice_up", "1 while BitVoice is running.", 1)
	g("bitvoice_uptime_seconds", "Seconds since BitVoice started.", time.Since(s.started).Seconds())
	states := map[string]float64{"ringing": 0, "answered": 0, "held": 0, "parked": 0}
	if s.Engine != nil {
		for _, ci := range s.Engine.Calls() {
			states[ci.State]++
		}
	}
	for _, st := range sortedKeys(states) {
		g("bitvoice_calls_active", "Calls in progress, by state.", states[st], "state", st)
	}
	if s.Engine != nil {
		g("bitvoice_phones_registered", "Phone registrations right now.", float64(len(s.Engine.Registrations())))
		for _, t := range s.Engine.TrunkStatus() {
			up := 0.0
			if t.State == "registered" || t.State == "reachable" {
				up = 1
			}
			g("bitvoice_trunk_up", "1 when the trunk is registered or answers OPTIONS.", up, "trunk", t.Name)
			g("bitvoice_trunk_latency_ms", "Round trip to the trunk, in milliseconds.", float64(t.LatencyMS), "trunk", t.Name)
		}
		for _, q := range s.Engine.Queues() {
			g("bitvoice_queue_waiting", "Callers waiting in the queue.", float64(len(q.Waiting)), "queue", q.Name)
			g("bitvoice_queue_agents", "Agents logged in to the queue.", float64(len(q.Agents)), "queue", q.Name)
			c("bitvoice_queue_answered_total", "Queue calls answered today.", float64(q.Totals.Answered), "queue", q.Name)
			c("bitvoice_queue_abandoned_total", "Queue callers who hung up waiting, today.", float64(q.Totals.Abandoned), "queue", q.Name)
		}
	}
	m := &s.metrics
	m.mu.Lock()
	keys := make([][2]string, 0, len(m.calls))
	for k := range m.calls {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	for _, k := range keys {
		c("bitvoice_calls_total", "Calls ended since start, by direction and outcome.", float64(m.calls[k]), "direction", k[0], "disposition", k[1])
	}
	for _, d := range sortedKeys(m.talk) {
		c("bitvoice_talk_seconds_total", "Seconds of answered calls, by direction.", m.talk[d], "direction", d)
	}
	c("bitvoice_rtp_packets_total", "Sound packets received on calls that ended.", float64(m.packets))
	c("bitvoice_rtp_lost_total", "Sound packets that never arrived, on calls that ended.", float64(m.lost))
	c("bitvoice_callbacks_total", "Callbacks booked.", float64(m.callbacks))
	for _, d := range sortedKeys(m.texts) {
		c("bitvoice_texts_total", "Text messages, by direction.", float64(m.texts[d]), "direction", d)
	}
	out = append(out, sample{name: "bitvoice_call_mos", help: "Call quality score (MOS, 1 to 5) of calls that ended.", kind: "histogram",
		buckets: append([]uint64(nil), m.mosBucket[:]...), sum: m.mosSum, count: m.mosCount})
	m.mu.Unlock()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	g("bitvoice_goroutines", "Goroutines running.", float64(runtime.NumGoroutine()))
	g("bitvoice_memory_bytes", "Memory in use by BitVoice.", float64(ms.Alloc))
	return out
}

func labels(kv []string) map[string]string {
	if len(kv) == 0 {
		return nil
	}
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// prometheusText writes samples in the Prometheus text exposition format.
func prometheusText(ss []sample) []byte {
	var b bytes.Buffer
	seen := map[string]bool{}
	for _, x := range ss {
		if !seen[x.name] {
			seen[x.name] = true
			fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", x.name, x.help, x.name, x.kind)
		}
		if x.kind == "histogram" {
			var cum uint64
			for i, n := range x.buckets {
				cum += n
				le := "+Inf"
				if i < len(mosBounds) {
					le = strconv.FormatFloat(mosBounds[i], 'f', -1, 64)
				}
				fmt.Fprintf(&b, "%s_bucket{le=%q} %d\n", x.name, le, cum)
			}
			fmt.Fprintf(&b, "%s_sum %s\n%s_count %d\n", x.name, num(x.sum), x.name, x.count)
			continue
		}
		fmt.Fprintf(&b, "%s%s %s\n", x.name, promLabels(x.labels), num(x.value))
	}
	return b.Bytes()
}

func promLabels(l map[string]string) string {
	if len(l) == 0 {
		return ""
	}
	parts := []string{}
	for _, k := range sortedKeys(l) {
		v := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(l[k])
		parts = append(parts, k+`="`+v+`"`)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// otlpJSON is the OpenTelemetry metrics export request (OTLP/HTTP, JSON encoding).
func otlpJSON(ss []sample, name string, start, now time.Time) []byte {
	attrs := func(l map[string]string) []map[string]any {
		out := []map[string]any{}
		for _, k := range sortedKeys(l) {
			out = append(out, map[string]any{"key": k, "value": map[string]any{"stringValue": l[k]}})
		}
		return out
	}
	t0, t1 := strconv.FormatInt(start.UnixNano(), 10), strconv.FormatInt(now.UnixNano(), 10)
	byName := map[string]map[string]any{}
	var order []string
	for _, x := range ss {
		m, ok := byName[x.name]
		if !ok {
			m = map[string]any{"name": x.name, "description": x.help}
			byName[x.name] = m
			order = append(order, x.name)
		}
		switch x.kind {
		case "gauge":
			g, _ := m["gauge"].(map[string]any)
			if g == nil {
				g = map[string]any{"dataPoints": []any{}}
				m["gauge"] = g
			}
			g["dataPoints"] = append(g["dataPoints"].([]any), map[string]any{"attributes": attrs(x.labels), "timeUnixNano": t1, "asDouble": x.value})
		case "counter":
			sm, _ := m["sum"].(map[string]any)
			if sm == nil {
				sm = map[string]any{"dataPoints": []any{}, "aggregationTemporality": 2, "isMonotonic": true}
				m["sum"] = sm
			}
			sm["dataPoints"] = append(sm["dataPoints"].([]any), map[string]any{"attributes": attrs(x.labels), "startTimeUnixNano": t0, "timeUnixNano": t1, "asDouble": x.value})
		case "histogram":
			counts := make([]string, len(x.buckets))
			for i, n := range x.buckets {
				counts[i] = strconv.FormatUint(n, 10)
			}
			m["histogram"] = map[string]any{"aggregationTemporality": 2, "dataPoints": []any{map[string]any{
				"startTimeUnixNano": t0, "timeUnixNano": t1, "count": strconv.FormatUint(x.count, 10), "sum": x.sum,
				"bucketCounts": counts, "explicitBounds": mosBounds[:]}}}
		}
	}
	metrics := make([]any, 0, len(order))
	for _, n := range order {
		metrics = append(metrics, byName[n])
	}
	body, _ := json.Marshal(map[string]any{"resourceMetrics": []any{map[string]any{
		"resource": map[string]any{"attributes": []any{
			map[string]any{"key": "service.name", "value": map[string]any{"stringValue": "bitvoice"}},
			map[string]any{"key": "service.instance.id", "value": map[string]any{"stringValue": name}},
		}},
		"scopeMetrics": []any{map[string]any{"scope": map[string]any{"name": "bitvoice"}, "metrics": metrics}},
	}}})
	return body
}

func (s *Server) pushOTLP(ctx context.Context, ss []sample) error {
	cfg := s.config()
	o := cfg.System.Metrics
	u := strings.TrimRight(o.OTLPURL, "/")
	if !strings.HasSuffix(u, "/v1/metrics") {
		u += "/v1/metrics"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(otlpJSON(ss, cfg.System.Name, s.started, time.Now())))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.OTLPHeader != "" && o.OTLPSecret != "" {
		req.Header.Set(o.OTLPHeader, o.OTLPSecret)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("the collector could not be reached: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("the collector answered %s", resp.Status)
	}
	return nil
}

func (s *Server) metricsRoutes() {
	// Prometheus: scrape with an API token (any role) as a bearer token, or a signed-in session.
	s.handle("GET /metrics", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Write(prometheusText(s.samples()))
	})
	s.handle("GET /api/metrics/dashboard", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		w.Header().Set("Content-Disposition", `attachment; filename="bitvoice-grafana.json"`)
		writeJSON(w, 200, grafanaDashboard())
	})
	s.handle("POST /api/metrics/test", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if s.config().System.Metrics.OTLPURL == "" {
			writeError(w, 400, "set the collector address and save first")
			return
		}
		if err := s.pushOTLP(r.Context(), s.samples()); err != nil {
			writeError(w, 502, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})
}

// grafanaDashboard is a ready-made Grafana dashboard for the Prometheus metrics.
func grafanaDashboard() map[string]any {
	type p struct{ title, expr, unit, kind string }
	ps := []p{
		{"Calls in progress", `sum by (state) (bitvoice_calls_active)`, "short", "timeseries"},
		{"Calls per hour, by outcome", `sum by (disposition) (increase(bitvoice_calls_total[1h]))`, "short", "timeseries"},
		{"Phones registered", `bitvoice_phones_registered`, "short", "stat"},
		{"Trunks up", `bitvoice_trunk_up`, "short", "stat"},
		{"Callers waiting", `sum by (queue) (bitvoice_queue_waiting)`, "short", "timeseries"},
		{"Sound lost", `100 * rate(bitvoice_rtp_lost_total[15m]) / clamp_min(rate(bitvoice_rtp_packets_total[15m]) + rate(bitvoice_rtp_lost_total[15m]), 1)`, "percent", "timeseries"},
		{"Average call quality (MOS)", `rate(bitvoice_call_mos_sum[1h]) / clamp_min(rate(bitvoice_call_mos_count[1h]), 1e-9)`, "short", "timeseries"},
		{"Talk time per hour", `sum by (direction) (increase(bitvoice_talk_seconds_total[1h]))`, "s", "timeseries"},
		{"Trunk round trip", `bitvoice_trunk_latency_ms`, "ms", "timeseries"},
	}
	panels := []any{}
	for i, x := range ps {
		panels = append(panels, map[string]any{
			"id": i + 1, "type": x.kind, "title": x.title,
			"datasource":  map[string]any{"type": "prometheus", "uid": "${DS}"},
			"gridPos":     map[string]any{"x": (i % 3) * 8, "y": (i / 3) * 8, "w": 8, "h": 8},
			"fieldConfig": map[string]any{"defaults": map[string]any{"unit": x.unit}, "overrides": []any{}},
			"targets":     []any{map[string]any{"refId": "A", "expr": x.expr, "legendFormat": "__auto"}},
		})
	}
	return map[string]any{
		"title": "BitVoice", "uid": "bitvoice", "schemaVersion": 39, "time": map[string]any{"from": "now-24h", "to": "now"},
		"templating": map[string]any{"list": []any{map[string]any{"name": "DS", "type": "datasource", "query": "prometheus", "label": "Prometheus"}}},
		"panels":     panels,
	}
}
