package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/i3"
)

// The LIS: records uploaded as a spreadsheet are served to HELD requests by number from allowed
// addresses only; a PSAP's lookup by number (i3.LocateNumber, as the 911 profile does it) gets them.
func TestLIS(t *testing.T) {
	_, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	held := ts.URL + "/held"
	if _, _, err := i3.LocateNumber(context.Background(), nil, held, "7175550100"); err == nil {
		t.Fatal("the LIS answered while off")
	}
	_, cfg := c.do("GET", "/api/settings", nil)
	core := cfg["system"].(map[string]any)["core"].(map[string]any)
	core["lis"], core["allowFrom"] = true, []string{"10.9.9.9"}
	if code, out := c.do("PUT", "/api/settings", cfg); code != 200 {
		t.Fatalf("save %d %v", code, out)
	}
	csv := "number,hno,rd,sts,a3,a1,lat,lon,radius\n7175550100,123,Front,St,Harrisburg,PA,40.2633,-76.8823,20\n"
	req, _ := http.NewRequest("POST", ts.URL+"/api/core/lis", strings.NewReader(csv))
	req.Header.Set("X-BitVoice", "1")
	resp, err := c.http.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("upload %v %v", resp, err)
	}
	if _, _, err := i3.LocateNumber(context.Background(), nil, held, "7175550100"); err == nil {
		t.Fatal("answered an address that is not allowed")
	}
	_, cfg = c.do("GET", "/api/settings", nil) // a fresh copy: the saved one has a new revision
	cfg["system"].(map[string]any)["core"].(map[string]any)["allowFrom"] = []string{"127.0.0.1"}
	if code, out := c.do("PUT", "/api/settings", cfg); code != 200 {
		t.Fatalf("save %d %v", code, out)
	}
	loc, raw, err := i3.LocateNumber(context.Background(), nil, held, "+1 (717) 555-0100")
	if err != nil || loc.Civic == nil || loc.Civic.Number != "123" || loc.Radius != 20 || !bytes.Contains(raw, []byte("<presence")) {
		t.Fatalf("%+v %v", loc, err)
	}
	if _, _, err := i3.LocateNumber(context.Background(), nil, held, "2125550100"); err == nil || !strings.Contains(err.Error(), "locationUnknown") {
		t.Fatalf("an unknown number: %v", err)
	}
	r, _ := http.Post(held, "application/held+xml", strings.NewReader("<locationRequest xmlns=\"urn:ietf:params:xml:ns:geopriv:held\"/>"))
	b, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(b), `code="notLocatable"`) {
		t.Fatalf("by the requester's address: %s", b)
	}
	if _, out := c.do("GET", "/api/core/lis?number=7175550100", nil); out["records"] != float64(1) || out["found"] == nil {
		t.Fatalf("summary %v", out)
	}
}
