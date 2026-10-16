package i3

import (
	"strings"
	"testing"
)

// An ALI record in a fixed layout (an example, as an ALI provider might send one): read by the
// PSAP's layout into a location, the class of service and the caller's name.
func TestParseALI(t *testing.T) {
	text := "\x02" + strings.Join([]string{
		"001 (717) 555-0100   10/15 14:05",
		"WPH2 VERIZON WIRELESS           ",
		"JOHNSON PAT                     ",
		"123 N   MARKET          ST      ",
		"APT 4B                          ",
		"HARRISBURG          PA 17101    ",
		"LAT +040.263300 LON -076.882300 ",
		"UNC 00025M ESN 0123             ",
	}, "\r\n") + "\r\n\x03"
	layout := []ALIField{{Name: "callback", Line: 1, Col: 5, Len: 14}, {Name: "class", Line: 2, Col: 1, Len: 4}, {Name: "company", Line: 2, Col: 6, Len: 26},
		{Name: "name", Line: 3, Col: 1}, {Name: "hno", Line: 4, Col: 1, Len: 8}, {Name: "rd", Line: 4, Col: 9, Len: 16}, {Name: "sts", Line: 4, Col: 25},
		{Name: "loc", Line: 5, Col: 1}, {Name: "city", Line: 6, Col: 1, Len: 20}, {Name: "state", Line: 6, Col: 21, Len: 2}, {Name: "zip", Line: 6, Col: 24, Len: 5},
		{Name: "lat", Line: 7, Col: 5, Len: 11}, {Name: "lon", Line: 7, Col: 21, Len: 11}, {Name: "unc", Line: 8, Col: 5, Len: 6}, {Name: "esn", Line: 8, Col: 16, Len: 4}}
	if err := CheckALILayout(layout); err != nil {
		t.Fatal(err)
	}
	r, err := ParseALI(text, layout)
	if err != nil {
		t.Fatal(err)
	}
	c := r.Location.Civic
	if r.Callback != "7175550100" || r.Class != "WPH2" || r.Name != "JOHNSON PAT" || r.Provider != "VERIZON WIRELESS" || c.Number != "123 N" && c.Number != "123" ||
		c.City != "HARRISBURG" || c.State != "PA" || c.PostalCode != "17101" || c.Location != "APT 4B" || *r.Location.Lat != 40.2633 || *r.Location.Lon != -76.8823 ||
		r.Location.Radius != 25 || r.Fields["esn"] != "0123" {
		t.Fatalf("%+v %+v", r, *c)
	}
	// A layout with one street field: the street is split into its parts.
	r, err = ParseALI("\x02(717) 555-0100\r\n123 N MAIN ST E\r\nHARRISBURG PA\r\n\x03", []ALIField{{Name: "callback", Line: 1, Col: 1}, {Name: "street", Line: 2, Col: 1}, {Name: "city", Line: 3, Col: 1, Len: 10}, {Name: "state", Line: 3, Col: 12, Len: 2}})
	if err != nil || r.Location.Civic.Number != "123" || r.Location.Civic.PreDir != "N" || r.Location.Civic.Street != "MAIN" || r.Location.Civic.StreetType != "ST" || r.Location.Civic.PostDir != "E" {
		t.Fatalf("street %+v %v", r.Location.Civic, err)
	}
	if _, err := ParseALI("\x02NO RECORD FOUND\r\n\x03", layout); err == nil || !strings.Contains(err.Error(), "no record") {
		t.Fatalf("no record: %v", err)
	}
	if err := CheckALILayout([]ALIField{{Name: "nonsense", Line: 1, Col: 1}}); err == nil {
		t.Fatal("an unknown field")
	}
	if got := string(ALIRequest(`\x02{pos2}{ani10}\x03`, "+1 (717) 555-0100", "7")); got != "\x0207"+"7175550100\x03" {
		t.Fatalf("request %q", got)
	}
	if got := string(ALIRequest("", "7175550100", "")); got != "\x027175550100\x03" {
		t.Fatalf("default request %q", got)
	}
}

func FuzzParseALI(f *testing.F) {
	f.Add("\x02(717) 555-0100\r\n123 MAIN ST\r\n\x03")
	layout := []ALIField{{Name: "callback", Line: 1, Col: 1}, {Name: "street", Line: 2, Col: 3, Len: 5}, {Name: "lat", Line: 3, Col: 200, Len: 200}}
	f.Fuzz(func(t *testing.T, s string) { _, _ = ParseALI(s, layout) })
}
