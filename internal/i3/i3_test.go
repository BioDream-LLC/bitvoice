package i3

import (
	"net/textproto"
	"strings"
	"testing"
)

const civicPIDF = `<?xml version="1.0"?>
<presence xmlns="urn:ietf:params:xml:ns:pidf" xmlns:gp="urn:ietf:params:xml:ns:pidf:geopriv10"
  xmlns:ca="urn:ietf:params:xml:ns:pidf:geopriv10:civicAddr" entity="pres:caller@example.net">
 <tuple id="a"><status><gp:geopriv><gp:location-info><ca:civicAddress>
  <ca:country>US</ca:country><ca:A1>PA</ca:A1><ca:A3>Harrisburg</ca:A3>
  <ca:HNO>123</ca:HNO><ca:PRD>N</ca:PRD><ca:RD>Front</ca:RD><ca:STS>St</ca:STS>
  <ca:FLR>2</ca:FLR><ca:PC>17101</ca:PC>
 </ca:civicAddress></gp:location-info><gp:method>Manual</gp:method></gp:geopriv></status>
 <timestamp>2026-10-12T13:00:00Z</timestamp></tuple></presence>`

const circlePIDF = `<presence xmlns="urn:ietf:params:xml:ns:pidf" xmlns:gml="http://www.opengis.net/gml"
  xmlns:gs="http://www.opengis.net/pidflo/1.0" entity="pres:x@y">
 <dm:device xmlns:dm="urn:ietf:params:xml:ns:pidf:data-model" id="d"><gp:geopriv xmlns:gp="urn:ietf:params:xml:ns:pidf:geopriv10">
  <gp:location-info><gs:Circle srsName="urn:ogc:def:crs:EPSG::4326"><gml:pos>40.2633 -76.8823</gml:pos>
  <gs:radius uom="urn:ogc:def:uom:EPSG::9001">35</gs:radius></gs:Circle>
  <con:confidence xmlns:con="urn:ietf:params:xml:ns:geopriv:conf">90</con:confidence></gp:location-info>
  <gp:method>GPS</gp:method></gp:geopriv><dm:deviceID>mac:00112233aabb</dm:deviceID></dm:device></presence>`

func TestParsePIDF(t *testing.T) {
	l, err := ParsePIDF([]byte(civicPIDF))
	if err != nil {
		t.Fatal(err)
	}
	if got := l.Civic.String(); got != "123 N Front St, Floor 2, Harrisburg PA 17101" || l.Method != "Manual" {
		t.Fatalf("civic %q method %q", got, l.Method)
	}
	l, err = ParsePIDF([]byte(circlePIDF))
	if err != nil {
		t.Fatal(err)
	}
	if !l.HasPoint() || *l.Lat != 40.2633 || *l.Lon != -76.8823 || l.Radius != 35 || l.Shape != "Circle" || l.Confidence != 90 || l.Device != "mac:00112233aabb" {
		t.Fatalf("%+v", l)
	}
	if _, err := ParsePIDF([]byte(`<presence xmlns="urn:ietf:params:xml:ns:pidf"/>`)); err == nil {
		t.Fatal("empty location accepted")
	}
}

type hdr map[string][]string

func (h hdr) Get(k string) string {
	if v := h[textproto.CanonicalMIMEHeaderKey(k)]; len(v) > 0 {
		return v[0]
	}
	return ""
}
func (h hdr) GetAll(k string) []string { return h[textproto.CanonicalMIMEHeaderKey(k)] }

func TestFromInvite(t *testing.T) {
	provider := `<ProviderInfo xmlns="urn:ietf:params:xml:ns:EmergencyCallData:ProviderInfo"><DataProviderString>Example Wireless</DataProviderString>` +
		`<ProviderID>12345</ProviderID><TypeOfProvider>Telecom Provider</TypeOfProvider><ContactURI>tel:+18005551212</ContactURI></ProviderInfo>`
	body := strings.Join([]string{
		"--b", "Content-Type: application/sdp", "", "v=0", "o=- 1 1 IN IP4 192.0.2.1", "s=-", "c=IN IP4 192.0.2.1", "t=0 0", "m=audio 4000 RTP/AVP 0", "",
		"--b", "Content-Type: application/pidf+xml", "Content-ID: <loc1@example.net>", "", circlePIDF, "",
		"--b", "Content-Type: application/pidf+xml", "Content-ID: <loc2@example.net>", "", civicPIDF, "",
		"--b", "Content-Type: application/EmergencyCallData.ProviderInfo+xml", "Content-ID: <pi@example.net>", "", provider, "",
		"--b--", ""}, "\r\n")
	h := hdr{
		"Geolocation":         {"<cid:loc1@example.net>, <https://lis.example.net/loc/abc>"},
		"Geolocation-Routing": {"yes"},
		"Call-Info": {`<cid:pi@example.net>;purpose=EmergencyCallData.ProviderInfo, <https://adr.example.net/svc/1>;purpose=EmergencyCallData.ServiceInfo`,
			`<sip:x@example.net>;purpose=icon`},
	}
	c := FromInvite(h, "urn:service:sos", "multipart/mixed;boundary=b", []byte(body))
	if !strings.HasPrefix(string(c.SDP), "v=0") {
		t.Fatalf("sdp %q", c.SDP)
	}
	if c.Location == nil || !c.Location.HasPoint() || c.Location.Civic == nil || c.Location.Civic.City != "Harrisburg" {
		t.Fatalf("location %+v problems %v", c.Location, c.Problems)
	}
	if c.LocationURI != "https://lis.example.net/loc/abc" || !c.Routing || c.Service != "urn:service:sos" {
		t.Fatalf("uri %q routing %v service %q", c.LocationURI, c.Routing, c.Service)
	}
	if len(c.Additional.Providers) != 1 || c.Additional.Providers[0].Name != "Example Wireless" || len(c.Additional.References) != 1 {
		t.Fatalf("additional %+v", c.Additional)
	}
}
