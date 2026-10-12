package api

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"
)

// A Cisco BAT export, uploaded with a users file: previewed, then applied; the groups ring the
// people, the route goes out on the imported trunk, and a second import adds nothing.
func TestImportCallFlows(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	var tb bytes.Buffer
	tw := tar.NewWriter(&tb)
	for name, body := range map[string]string{
		"Line Group.csv":    "LINE GROUP NAME,RNA REVERSION TIMEOUT,DISTRIBUTION ALGORITHM,DIRECTORY NUMBER 1,DIRECTORY NUMBER 2\nSales_LG,12,Top Down,1201,1202\n",
		"Hunt List.csv":     "HUNT LIST NAME,LINE GROUP 1\nSales_HL,Sales_LG\n",
		"Hunt Pilot.csv":    "HUNT PILOT,HUNT LIST,ALERTING NAME\n5000,Sales_HL,Sales\n",
		"SIP Trunk.csv":     "DEVICE NAME,DESTINATION ADDRESS 1,DESTINATION PORT 1\nITSP,sip.carrier.example,5060\n",
		"Route Pattern.csv": "ROUTE PATTERN,GATEWAY OR ROUTE LIST,DISCARD DIGITS\n9.[2-9]XXXXXX,ITSP,PreDot\n",
	} {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	upload := func() map[string]any {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		f, _ := mw.CreateFormFile("file", "users.csv")
		f.Write([]byte("USER ID,FIRST NAME,LAST NAME,TELEPHONE NUMBER\njdoe,Jane,Doe,1201\nbsmith,Bob,Smith,1202\n"))
		f, _ = mw.CreateFormFile("file", "cucm-export.tar")
		f.Write(tb.Bytes())
		mw.Close()
		req, _ := http.NewRequest("POST", ts.URL+"/api/import/preview", &b)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("X-BitVoice", "1")
		resp, err := admin.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode != 200 {
			t.Fatalf("preview %d %v", resp.StatusCode, out)
		}
		return out
	}
	p := upload()
	res := p["result"].(map[string]any)
	for k, n := range map[string]int{"extensions": 2, "ringGroups": 1, "trunks": 1, "outboundRoutes": 1} {
		if got := len(res[k].([]any)); got != n {
			t.Fatalf("%s: %d, want %d (%v)", k, got, n, res["warnings"])
		}
	}
	pick := func(r map[string]any) map[string]any {
		out := map[string]any{}
		for _, k := range []string{"extensions", "trunks", "ringGroups", "inboundRoutes", "outboundRoutes"} {
			out[k] = r[k]
		}
		return out
	}
	code, out := admin.do("POST", "/api/import/apply", pick(res))
	if code != 200 || out["added"].(float64) != 5 {
		t.Fatalf("apply %d %v", code, out)
	}
	cfg, _ := s.Store.Config()
	g := cfg.FindRingGroup("5000")
	if g == nil || g.Strategy != "sequential" || len(g.Members) != 2 || g.RingSeconds != 12 {
		t.Fatalf("group %+v", g)
	}
	if len(cfg.OutboundRoutes) == 0 {
		t.Fatal("no route")
	}
	o := cfg.OutboundRoutes[len(cfg.OutboundRoutes)-1]
	if tr := cfg.FindTrunk(o.Trunks[0]); tr == nil || tr.Name != "ITSP" || o.Strip != 1 || o.Patterns[0] != "9[2-9]XXXXXX" {
		t.Fatalf("route %+v", o)
	}
	// Again: everything is already here.
	p = upload()
	if ex := p["exists"].(map[string]any); !ex["group:5000"].(bool) || !ex["trunk:ITSP"].(bool) || !ex["out:9[2-9]XXXXXX"].(bool) {
		t.Fatalf("exists %v", ex)
	}
	code, out = admin.do("POST", "/api/import/apply", pick(p["result"].(map[string]any)))
	if code != 200 || out["added"].(float64) != 0 {
		t.Fatalf("second apply %d %v", code, out)
	}
}
