package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/i3"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// A 911 answer: CAD gets the ALI spill over TCP and the EIDO over HTTPS, and the i3 log has the
// call's events.
func TestEmergencyToCAD(t *testing.T) {
	s, ts := newServer(t)
	s.DataDir = t.TempDir()
	ali := make(chan string, 1)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		b, _ := io.ReadAll(c)
		ali <- string(b)
	}()
	eido := make(chan map[string]any, 1)
	cad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var d map[string]any
		json.NewDecoder(r.Body).Decode(&d)
		if r.Header.Get("Authorization") != "Bearer cad-token" {
			w.WriteHeader(401)
			return
		}
		eido <- d
		w.WriteHeader(201)
	}))
	defer cad.Close()
	_, rev := s.Store.Config()
	if _, _, err := s.Store.Update("test", "psap", rev, func(c *config.Config) error {
		c.System.PSAP = config.PSAP{AgencyID: "psap.example.gov", ElementID: "bv.psap.example.gov", ALIAddress: l.Addr().String(), EIDOURL: cad.URL, EIDOToken: "cad-token"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lat, lon := 40.2633, -76.8823
	loc := &i3.Location{Lat: &lat, Lon: &lon, Radius: 25, Civic: &i3.Civic{Number: "123", Street: "Front", StreetType: "St", City: "Harrisburg", State: "PA"}}
	now := time.Now()
	s.EmergencyAnswered(pbx.EmergencyAnswer{Agent: "101", PIDF: []byte("<presence/>"), Call: pbx.CallInfo{ID: "c911", From: "+17175550100", To: "911",
		Start: now.Add(-12 * time.Second), Answer: now, Emergency: &pbx.Emergency{ESInet: true, Location: loc,
			Additional: i3.Additional{Service: &i3.Service{Types: []string{"wireless"}}, Providers: []i3.Provider{{Name: "Example Wireless"}}}}}})
	select {
	case a := <-ali:
		for _, w := range []string{"POS 101", "(717) 555-0100  WRLS", "123 FRONT ST", "HARRISBURG PA", "LAT +40.263300"} {
			if !strings.Contains(a, w) {
				t.Errorf("ALI has no %q:\n%s", w, a)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no ALI spill")
	}
	select {
	case d := <-eido:
		if d["$id"] != "urn:emergency:uid:incidentid:c911:bv.psap.example.gov" || d["locationComponent"] == nil {
			t.Fatalf("EIDO %v", d["$id"])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no EIDO")
	}
	time.Sleep(200 * time.Millisecond)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	code, out := admin.do("GET", "/api/psap/log", nil)
	items, _ := out["items"].([]any)
	var kinds []string
	for _, it := range items {
		kinds = append(kinds, it.(map[string]any)["logEventType"].(string))
	}
	all := strings.Join(kinds, ",")
	if code != 200 || !strings.Contains(all, "CallStateChangeLogEvent") || !strings.Contains(all, "EidoLogEvent") {
		t.Fatalf("log %d %s", code, all)
	}
}

func TestAnswerReport(t *testing.T) {
	cfg := config.Default()
	cfg.Extensions = []config.Extension{{Number: "101", Name: "Jo Lee"}}
	base := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	var cdrs []store.CDR
	for i, secs := range []int{5, 8, 12, 14, 18, 30, -1, 60} {
		c := store.CDR{Start: base.Add(time.Duration(i) * time.Minute), Emergency: true, AnsweredExt: "101", Answer: base.Add(time.Duration(i)*time.Minute + time.Second)}
		if secs >= 0 {
			c.Taken = c.Start.Add(time.Duration(secs) * time.Second) // a queue answered at once; the call-taker later
		} else {
			c.AnsweredExt = "" // the queue held the caller, nobody took it
		}
		cdrs = append(cdrs, c)
	}
	cdrs = append(cdrs, store.CDR{Start: base, Answer: base.Add(time.Second)}) // not a 911 call
	r := answerReport(cfg, cdrs, base, base.Add(time.Hour))
	if r.Calls != 8 || r.Answered != 7 || r.Abandoned != 1 || r.Target != 15 {
		t.Fatalf("%+v", r)
	}
	if int(r.WithinTarget+0.5) != 57 || int(r.Within20+0.5) != 71 || r.Longest != 60 {
		t.Fatalf("within %.1f / %.1f longest %.0f", r.WithinTarget, r.Within20, r.Longest)
	}
	if len(r.ByTaker) != 1 || r.ByTaker[0].Name != "Jo Lee" || r.ByTaker[0].Calls != 7 {
		t.Fatalf("takers %+v", r.ByTaker)
	}
	if r.Buckets[0] != 2 || r.Buckets[1] != 2 || r.Buckets[2] != 1 || r.Buckets[3] != 1 || r.Buckets[4] != 1 {
		t.Fatalf("buckets %v", r.Buckets)
	}
}

func TestPSAPLogFromEvents(t *testing.T) {
	s, _ := newServer(t)
	s.DataDir = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.RunPSAPLog(ctx)
	time.Sleep(50 * time.Millisecond)
	c := pbx.CallInfo{ID: "ab1", From: "+17175550100", To: "911", Start: time.Now(), Emergency: &pbx.Emergency{ESInet: true}}
	s.Bus.Publish("call.started", c)
	s.Bus.Publish("call.updated", c)
	s.Bus.Publish("call.ended", c)
	s.Bus.Publish("call.ended", pbx.CallInfo{ID: "office"}) // not a 911 call
	time.Sleep(200 * time.Millisecond)
	evs := s.i3Events(time.Now().UTC().Format("2006-01-02"))
	if len(evs) != 2 || evs[0]["logEventType"] != "CallEndLogEvent" || evs[1]["logEventType"] != "CallStartLogEvent" {
		t.Fatalf("%v", evs)
	}
	if ext, _ := evs[0]["extension"].(map[string]any); ext["abandoned"] != true {
		t.Fatalf("abandoned not marked: %v", evs[0])
	}
}
