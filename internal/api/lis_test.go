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
	if !strings.Contains(string(b), `code="locationUnknown"`) {
		t.Fatalf("by the requester's address, with no networks listed: %s", b)
	}
	if _, out := c.do("GET", "/api/core/lis?number=7175550100", nil); out["records"] != float64(1) || out["found"] == nil {
		t.Fatalf("summary %v", out)
	}

	// By network address (RFC 5985: the requester's own; RFC 6155: an ip or mac identity).
	nets := "network,mac,hno,rd,sts,unit,floor,a3,a1\n" +
		"127.0.0.0/8,,1,Loopback,Way,,,Harrisburg,PA\n" +
		"10.20.0.0/16,,100,Campus,Dr,,,Harrisburg,PA\n" +
		"10.20.3.0/24,,100,Campus,Dr,,3,Harrisburg,PA\n" +
		",A0-12-34-56-78-90,100,Campus,Dr,Room 301,3,Harrisburg,PA\n"
	req, _ = http.NewRequest("POST", ts.URL+"/api/core/lis", strings.NewReader(nets))
	req.Header.Set("X-BitVoice", "1")
	if resp, err := c.http.Do(req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("upload networks %v %v", resp, err)
	}
	ask := func(device string) string {
		body := `<?xml version="1.0"?><locationRequest xmlns="urn:ietf:params:xml:ns:geopriv:held" responseTime="8"><locationType exact="true">civic</locationType>` + device + `</locationRequest>`
		r, err := http.Post(held, "application/held+xml", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return string(b)
	}
	id := func(x string) string {
		return `<device xmlns="urn:ietf:params:xml:ns:geopriv:held:id">` + x + `</device>`
	}
	for _, c := range []struct{ device, want string }{
		{"", "<ca:RD>Loopback</ca:RD>"},                                     // the requester, 127.0.0.1
		{id(`<ip v="4">10.20.3.17</ip>`), "<ca:FLR>3</ca:FLR>"},             // the narrowest network
		{id(`<ip v="4">10.20.9.1</ip>`), "<ca:HNO>100</ca:HNO>"},            // the campus
		{id(`<mac>A0-12-34-56-78-90</mac>`), "<ca:UNIT>Room 301</ca:UNIT>"}, // the device itself
		{id(`<ip v="4">10.21.0.1</ip>`), `code="locationUnknown"`},
		{id(`<ip v="4">not-an-address</ip>`), `code="badRequest"`},
		{id(`<nai>joe@example.com</nai>`), `code="notLocatable"`},
	} {
		if got := ask(c.device); !strings.Contains(got, c.want) {
			t.Errorf("%s: want %s in %s", c.device, c.want, got)
		}
	}
	if got := ask(id(`<ip v="4">10.20.3.17</ip>`)); strings.Contains(got, "<ca:UNIT>") {
		t.Errorf("an address took the MAC address's record: %s", got)
	}
	for q, want := range map[string]string{"10.20.3.200": "3", "a012.3456.7890": "3", "10.20.0.0/16": ""} {
		_, out := c.do("GET", "/api/core/lis?number="+q, nil)
		f, _ := out["found"].(map[string]any)
		if f == nil || f["civic"].(map[string]any)["floor"] != func() any {
			if want == "" {
				return nil
			}
			return want
		}() {
			t.Errorf("look up %s: %v", q, out)
		}
	}
	if _, out := c.do("GET", "/api/core/lis", nil); out["networks"] != float64(3) || out["macs"] != float64(1) || out["numbers"] != float64(1) {
		t.Fatalf("counts %v", out)
	}
	if got := ask(""); !strings.Contains(got, `entity="pres:127-0-0-1@Acme-Dental"`) {
		t.Errorf("the entity is not a URI: %s", got)
	}
}
