package lost

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Two counties side by side, and a police service area inside the east one.
const areas = `{"type":"FeatureCollection","features":[
 {"type":"Feature","id":"west","properties":{"name":"West County 911","uri":"sip:sos@west.example.gov"},
  "geometry":{"type":"Polygon","coordinates":[[[-77.0,40.0],[-76.9,40.0],[-76.9,40.5],[-77.0,40.5],[-77.0,40.0]]]}},
 {"type":"Feature","id":"east","properties":{"name":"East County 911","uri":"sip:sos@east.example.gov"},
  "geometry":{"type":"MultiPolygon","coordinates":[[[[-76.9,40.0],[-76.8,40.0],[-76.8,40.5],[-76.9,40.5],[-76.9,40.0]]]]}},
 {"type":"Feature","id":"pd","properties":{"name":"City Police","uri":"sip:police@city.example.gov","service":"urn:service:sos.police"},
  "geometry":{"type":"Polygon","coordinates":[[[-76.89,40.2],[-76.85,40.2],[-76.85,40.3],[-76.89,40.3],[-76.89,40.2]]]}},
 {"type":"Feature","id":"pt","properties":{"name":"a point"},"geometry":{"type":"Point","coordinates":[-76.9,40.1]}}]}`

func query(service, pos, civic string) string {
	loc := `<location id="1" profile="geodetic-2d"><p:Point xmlns:p="http://www.opengis.net/gml" srsName="urn:ogc:def:crs:EPSG::4326"><p:pos>` + pos + `</p:pos></p:Point></location>`
	if civic != "" {
		loc = `<location id="1" profile="civic"><civicAddress xmlns="urn:ietf:params:xml:ns:pidf:geopriv10:civicAddr">` + civic + `</civicAddress></location>`
	}
	return `<?xml version="1.0"?><findService xmlns="urn:ietf:params:xml:ns:lost1" recursive="true" serviceBoundary="value">` + loc + `<service>` + service + `</service></findService>`
}

func TestLoST(t *testing.T) {
	bs, err := ParseGeoJSON([]byte(areas), "urn:service:sos", "")
	if err != nil || len(bs) != 3 {
		t.Fatalf("%d boundaries, %v", len(bs), err)
	}
	bs[0].Cities, bs[0].States = []string{"Westville"}, []string{"PA"}
	s := &Server{Source: "ecrf.example.gov", Boundaries: func() []Boundary { return bs }}
	for _, c := range []struct{ service, pos, civic, want string }{
		{"urn:service:sos", "40.2 -76.95", "", "sip:sos@west.example.gov"},
		{"urn:service:sos", "40.2 -76.82", "", "sip:sos@east.example.gov"},
		{"urn:service:sos.police", "40.25 -76.87", "", "sip:police@city.example.gov"}, // the specific service wins
		{"urn:service:sos.fire", "40.25 -76.87", "", "sip:sos@east.example.gov"},      // falls back to sos
		{"urn:service:sos", "41.5 -76.82", "", "<notFound"},
		{"urn:service:sos", "", "<A1>PA</A1><A3>westville</A3>", "sip:sos@west.example.gov"},
		{"urn:service:sos", "", "<A1>NJ</A1><A3>Westville</A3>", "<notFound"},
		{"tel:911", "40.2 -76.95", "", "<serviceNotImplemented"},
	} {
		got := string(s.Respond(strings.NewReader(query(c.service, c.pos, c.civic))))
		if !strings.Contains(got, c.want) {
			t.Errorf("%s at %q %q: want %s in\n%s", c.service, c.pos, c.civic, c.want, got)
		}
	}
	if got := string(s.Respond(strings.NewReader("<listServices/>"))); !strings.Contains(got, "<badRequest") {
		t.Errorf("unsupported request: %s", got)
	}
}

func TestAsk(t *testing.T) {
	bs, _ := ParseGeoJSON([]byte(areas), "urn:service:sos", "")
	s := &Server{Source: "ecrf.test", Boundaries: func() []Boundary { return bs }}
	ecrf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(s.Respond(r.Body)) }))
	defer ecrf.Close()
	la, lo := 40.2, -76.82
	if uri, err := Ask(ecrf.URL, Query{Service: "urn:service:sos", Lat: &la, Lon: &lo}); err != nil || uri != "sip:sos@east.example.gov" {
		t.Fatalf("%q %v", uri, err)
	}
	la = 45
	if _, err := Ask(ecrf.URL, Query{Service: "urn:service:sos", Lat: &la, Lon: &lo}); err == nil || err.Error() != "notFound" {
		t.Fatalf("outside: %v", err)
	}
}
