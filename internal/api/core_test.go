package api

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
)

func TestCoreLoST(t *testing.T) {
	s, ts := newServer(t)
	s.DataDir = t.TempDir()
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	geo := `{"type":"FeatureCollection","features":[{"type":"Feature","id":"w","properties":{"name":"West 911"},
	 "geometry":{"type":"Polygon","coordinates":[[[-77,40],[-76.9,40],[-76.9,40.5],[-77,40.5],[-77,40]]]}}]}`
	post := func(url string, body string) (int, string) {
		req, _ := http.NewRequest("POST", ts.URL+url, strings.NewReader(body))
		req.Header.Set("X-BitVoice", "1")
		resp, err := admin.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := post("/api/core/boundaries?service=urn:service:sos", geo); code != 422 || !strings.Contains(body, "nowhere to send") {
		t.Fatalf("no uri: %d %s", code, body)
	}
	if code, body := post("/api/core/boundaries?service=urn:service:sos&uri=sip:sos@west.example.gov", geo); code != 200 {
		t.Fatalf("upload: %d %s", code, body)
	}
	code, out := admin.do("GET", "/api/core/find?lat=40.2&lon=-76.95", nil)
	if code != 200 || out["found"] != true {
		t.Fatalf("find %d %v", code, out)
	}
	q := `<findService xmlns="urn:ietf:params:xml:ns:lost1"><location id="l" profile="geodetic-2d"><Point xmlns="http://www.opengis.net/gml"><pos>40.2 -76.95</pos></Point></location><service>urn:service:sos</service></findService>`
	lostPost := func() (int, string) {
		resp, err := http.Post(ts.URL+"/lost", "application/lost+xml", bytes.NewReader([]byte(q)))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := lostPost(); code != 404 {
		t.Fatalf("LoST off answered %d", code)
	}
	_, rev := s.Store.Config()
	upd := func(allow []string) {
		_, rev2, err := s.Store.Update("test", "core", rev, func(c *config.Config) error {
			c.System.Core = config.Core{ECRF: true, Source: "ecrf.example.gov", AllowFrom: allow}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		rev = rev2
	}
	upd([]string{"10.0.0.0/8"})
	if code, _ := lostPost(); code != 403 {
		t.Fatalf("outsider answered %d", code)
	}
	upd([]string{"127.0.0.1"})
	if code, body := lostPost(); code != 200 || !strings.Contains(body, "<uri>sip:sos@west.example.gov</uri>") || !strings.Contains(body, `source="ecrf.example.gov"`) {
		t.Fatalf("LoST %d %s", code, body)
	}
}
