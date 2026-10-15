package i3

import (
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
	for _, bad := range []string{"number,rd\n12,Main\n", "rd\nMain\n", "number,rd,lat,lon\n7175550100,,95,1\n", "number\n7175550100\n"} {
		if _, err := ParseLISCSV(strings.NewReader(bad)); err == nil {
			t.Errorf("took %q", bad)
		}
	}
}
