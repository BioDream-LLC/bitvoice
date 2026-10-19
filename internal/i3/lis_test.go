package i3

import (
	"net/netip"
	"strings"
	"testing"
	"time"
)

// Records from a spreadsheet, written as PIDF-LO and read back the same; a HELD request by number
// (as LocateNumber sends it) names that number.
func TestLIS(t *testing.T) {
	recs, err := ParseLISCSV(strings.NewReader("\ufeffPhone,HNO,RD,STS,Unit,A3,A1,PC,lat,lon,radius\n" +
		"(717) 555-0100,123,Front,St,Apt 4 & 5,Harrisburg,PA,17101,40.2633,-76.8823,15\n" +
		"1-570-555-0199,9,Main,St,,Scranton,PA,,,,\n" +
		"717 555 0100,125,Front,St,,Harrisburg,PA,,40.2633,-76.8823,15\n"))
	if err != nil || len(recs) != 2 || recs[0].Number != "7175550100" || recs[0].Civic.Number != "125" || recs[1].Number != "5705550199" {
		t.Fatalf("%+v %v", recs, err)
	}
	recs[0].Civic.Unit = "Apt 4 & 5"
	loc, err := ParsePIDF(BuildPIDF("pres:7175550100@lis.example.gov", recs[0], time.Now()))
	if err != nil || loc.Civic == nil || loc.Civic.Unit != "Apt 4 & 5" || loc.Civic.Number != "125" || !loc.HasPoint() || *loc.Lat != 40.2633 || loc.Radius != 15 {
		t.Fatalf("%+v %v", loc, err)
	}
	if loc, err := ParsePIDF(BuildPIDF("pres:x", recs[1], time.Now())); err != nil || loc.HasPoint() || loc.Civic.City != "Scranton" {
		t.Fatalf("civic only %+v %v", loc, err)
	}
	req := `<locationRequest xmlns="urn:ietf:params:xml:ns:geopriv:held"><locationType>any</locationType><device xmlns="urn:ietf:params:xml:ns:geopriv:held:id"><tel>7175550100</tel></device></locationRequest>`
	if n, err := HELDDevice([]byte(req)); err != nil || n != "7175550100" {
		t.Fatalf("%q %v", n, err)
	}
	if _, err := HELDDevice([]byte(`<locationRequest xmlns="urn:ietf:params:xml:ns:geopriv:held"/>`)); err != HELDError("notLocatable") {
		t.Fatalf("no device: %v", err)
	}
	if _, err := HELDDevice([]byte(`<nope/>`)); err != HELDError("badRequest") {
		t.Fatalf("not HELD: %v", err)
	}
	for _, bad := range []string{"network,rd\n10.0.0.300/8,Main\n", "mac,rd\nzz:12:34:56:78:90,Main\n", "number,network,rd\n7175550100,10.0.0.0/8,Main\n", "number,rd\n12,Main\n", "rd\nMain\n", "number,rd,lat,lon\n7175550100,,95,1\n", "number\n7175550100\n"} {
		if _, err := ParseLISCSV(strings.NewReader(bad)); err == nil {
			t.Errorf("took %q", bad)
		}
	}
}

// Records by network and MAC address: the narrowest network holding an address wins; a MAC address
// is read in any usual form; a request by RFC 6155 ip or mac names that device.
func TestLISByNetwork(t *testing.T) {
	recs, err := ParseLISCSV(strings.NewReader("subnet,MAC address,rd,flr\n10.1.0.0/16,,Main,\n10.1.2.9/24,,Main,2\n2001:db8::/48,,Main,6\n192.0.2.5,,Main,1\n,a0:12:34:56:78:90,Main,4\n"))
	if err != nil || len(recs) != 5 || recs[1].Network != "10.1.2.0/24" || recs[3].Network != "192.0.2.5/32" || recs[4].MAC != "a0:12:34:56:78:90" {
		t.Fatalf("%+v %v", recs, err)
	}
	by := map[string]LISRecord{}
	for _, r := range recs {
		by[r.Key()] = r
	}
	ix := NewLISIndex(by)
	for addr, want := range map[string]string{"10.1.2.3": "10.1.2.0/24", "10.1.9.9": "10.1.0.0/16", "192.0.2.5": "192.0.2.5/32", "::ffff:10.1.2.3": "10.1.2.0/24", "2001:db8:0:1::5": "2001:db8::/48", "10.2.0.1": ""} {
		r, ok := ix.Find(HELDTarget{}, netip.MustParseAddr(addr))
		if (want == "") == ok || r.Network != want {
			t.Errorf("%s: %+v %v", addr, r, ok)
		}
	}
	for _, s := range []string{"A0-12-34-56-78-90", "a012.3456.7890", "A01234567890", "a0:12:34:56:78:90"} {
		if m, err := MACKey(s); err != nil || m != "a0:12:34:56:78:90" {
			t.Errorf("%s: %q %v", s, m, err)
		}
	}
	q, err := HELDQuery([]byte(`<locationRequest xmlns="urn:ietf:params:xml:ns:geopriv:held"><device xmlns="urn:ietf:params:xml:ns:geopriv:held:id"><ip v="6">2001:db8:0:1::5</ip></device></locationRequest>`))
	if err != nil || q.IP != netip.MustParseAddr("2001:db8:0:1::5") {
		t.Fatalf("%+v %v", q, err)
	}
	if r, ok := ix.Find(q, netip.MustParseAddr("10.1.2.3")); !ok || r.Network != "2001:db8::/48" {
		t.Fatalf("the named device, not the requester: %+v", r)
	}
	q, _ = HELDQuery([]byte(`<locationRequest xmlns="urn:ietf:params:xml:ns:geopriv:held"><device xmlns="urn:ietf:params:xml:ns:geopriv:held:id"><mac>A0-12-34-56-78-90</mac></device></locationRequest>`))
	if r, ok := ix.Find(q, netip.Addr{}); !ok || r.Civic.Floor != "4" || !strings.Contains(string(BuildPIDF("pres:x", r, time.Now())), "<dm:deviceID>mac:a01234567890</dm:deviceID>") {
		t.Fatalf("by MAC %+v %v", r, ok)
	}
	if q, err := HELDQuery([]byte(`<locationRequest xmlns="urn:ietf:params:xml:ns:geopriv:held"/>`)); err != nil || q.Tel != "" || q.IP.IsValid() {
		t.Fatalf("no device: the requester %+v %v", q, err)
	}
}
