package api

import (
	"net/http"
	"strings"
	"testing"
)

// Checking an address on the Core services page: a misspelt street gets suggestions from the GIS
// data; a position given with the address is checked against its address point.
func TestValidateSuggestsAndChecksPosition(t *testing.T) {
	_, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	gis := `{"type":"FeatureCollection","features":[{"type":"Feature","geometry":{"type":"Point","coordinates":[-89.61,39.81]},
"properties":{"Site_NGUID":"ssap1","Add_Number":250,"St_Name":"Oak","St_PosTyp":"Ave","Inc_Muni":"Springfield","State":"IL"}}]}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/core/addresses", strings.NewReader(gis))
	req.Header.Set("X-BitVoice", "1")
	if resp, err := c.http.Do(req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("upload %v %v", resp, err)
	}
	code, out := c.do("POST", "/api/core/validate", map[string]string{"A1": "IL", "A3": "Springfield", "HNO": "250", "RD": "Oka", "lat": "39.83", "lon": "-89.61"})
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	sug, _ := out["suggestions"].([]any)
	if len(sug) != 1 || sug[0].(map[string]any)["civic"].(map[string]any)["RD"] != "Oak" {
		t.Fatalf("suggestions %v", out)
	}
	if pos, _ := out["position"].(map[string]any); pos == nil || pos["checked"] != false {
		t.Fatalf("an address not found has no point to check against: %v", out["position"])
	}
	_, out = c.do("POST", "/api/core/validate", map[string]string{"A1": "IL", "A3": "Springfield", "HNO": "250", "RD": "Oak", "lat": "39.83", "lon": "-89.61", "radius": "50"})
	pos, _ := out["position"].(map[string]any)
	if out["matched"] != "address point" || out["suggestions"] != nil || pos == nil || pos["within"] != false || pos["limit"] != float64(150) || pos["metres"].(float64) < 2000 {
		t.Fatalf("far: %v", out)
	}
	if code, out := c.do("POST", "/api/core/validate", map[string]string{"RD": "Oak", "lat": "north", "lon": "1"}); code != 422 || !strings.Contains(asJSON(out), "a number") {
		t.Fatalf("a bad position: %d %v", code, out)
	}
}
