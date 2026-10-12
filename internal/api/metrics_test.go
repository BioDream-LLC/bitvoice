package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

func TestMetrics(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	s.metrics.addCall(store.CDR{Direction: "inbound", Disposition: "answered", Billsec: 30, MOS: 4.3, Quality: []store.LegQuality{{Packets: 1000, Lost: 10}}})
	s.metrics.addCall(store.CDR{Direction: "inbound", Disposition: "no-answer"})
	_, tok := admin.do("POST", "/api/tokens", map[string]any{"name": "Prometheus", "role": "viewer"})
	scrape := func(token string) (int, string) {
		req, _ := http.NewRequest("GET", ts.URL+"/metrics", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := scrape(""); code != 401 {
		t.Fatalf("metrics without a token: %d", code)
	}
	code, body := scrape(tok["secret"].(string))
	for _, want := range []string{
		"# TYPE bitvoice_calls_total counter",
		`bitvoice_calls_total{direction="inbound",disposition="answered"} 1`,
		`bitvoice_talk_seconds_total{direction="inbound"} 30`,
		`bitvoice_call_mos_bucket{le="4.4"} 1`, `bitvoice_call_mos_bucket{le="4.2"} 0`, "bitvoice_call_mos_count 1",
		"bitvoice_rtp_lost_total 10", "bitvoice_up 1",
	} {
		if code != 200 || !strings.Contains(body, want) {
			t.Fatalf("missing %q in %d:\n%s", want, code, body)
		}
	}
	// OpenTelemetry push, with its header.
	got := make(chan map[string]any, 1)
	col := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/metrics" || r.Header.Get("Authorization") != "Bearer otel" {
			w.WriteHeader(401)
			return
		}
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		got <- m
	}))
	defer col.Close()
	if code, _ := admin.do("POST", "/api/metrics/test", nil); code != 400 {
		t.Fatal("test with no collector")
	}
	cfg, rev := s.Store.Config()
	cfg.System.Metrics = config.Metrics{OTLPURL: col.URL, OTLPHeader: "Authorization"}
	cfg.System.Metrics.OTLPSecret = "Bearer otel"
	if _, _, err := s.Store.Update("test", "metrics", rev, func(c *config.Config) error { c.System.Metrics = cfg.System.Metrics; return nil }); err != nil {
		t.Fatal(err)
	}
	if code, out := admin.do("POST", "/api/metrics/test", nil); code != 200 {
		t.Fatalf("push %d %v", code, out)
	}
	select {
	case m := <-got:
		b, _ := json.Marshal(m)
		for _, want := range []string{`"service.name"`, `"bitvoice_calls_total"`, `"isMonotonic":true`, `"explicitBounds"`} {
			if !strings.Contains(string(b), want) {
				t.Fatalf("missing %s in %s", want, b)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing pushed")
	}
	if code, d := admin.do("GET", "/api/metrics/dashboard", nil); code != 200 || d["title"] != "BitVoice" {
		t.Fatalf("dashboard %d", code)
	}
	bad := config.Default()
	bad.System.Metrics = config.Metrics{OTLPURL: "ftp://x", OTLPSecret: "s", IntervalSeconds: 5}
	err := bad.Validate()
	if err == nil || !strings.Contains(err.Error(), "otlpUrl") || !strings.Contains(err.Error(), "intervalSeconds") || !strings.Contains(err.Error(), "otlpHeader") {
		t.Fatalf("validation %v", err)
	}
}
