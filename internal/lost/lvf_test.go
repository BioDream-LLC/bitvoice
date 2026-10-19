package lost

import (
	"slices"
	"strings"
	"testing"
)

// A road centerline (Main St, 100-198 even on the left and 101-199 odd on the right, in
// Springfield) and one address point (250 Oak Ave).
const gis = `{"type":"FeatureCollection","features":[
 {"type":"Feature","geometry":{"type":"LineString","coordinates":[[-89.6,39.8],[-89.59,39.8]]},
  "properties":{"RCL_NGUID":"rcl1","St_PreDir":"N","St_Name":"Main","St_PosTyp":"St",
   "FromAddr_L":100,"ToAddr_L":198,"Parity_L":"E","FromAddr_R":101,"ToAddr_R":199,"Parity_R":"O",
   "IncMuni_L":"Springfield","IncMuni_R":"Springfield","County_L":"Sangamon","County_R":"Sangamon",
   "State_L":"IL","State_R":"IL","PostCode_L":"62701","PostCode_R":"62701"}},
 {"type":"Feature","geometry":{"type":"Point","coordinates":[-89.61,39.81]},
  "properties":{"Site_NGUID":"ssap1","Add_Number":250,"St_Name":"Oak","St_PosTyp":"Ave",
   "Inc_Muni":"Springfield","County":"Sangamon","State":"IL","Post_Code":"62702"}}]}`

func TestValidate(t *testing.T) {
	a, err := ParseAddressGeoJSON([]byte(gis))
	if err != nil || len(a.Streets) != 1 || len(a.Points) != 1 {
		t.Fatalf("%v %+v", err, a)
	}
	cases := []struct {
		name             string
		c                Civic
		matched, invalid string
	}{
		{"point", Civic{"country": "US", "A1": "IL", "A3": "Springfield", "HNO": "250", "RD": "Oak", "STS": "Avenue"}, "address point", ""},
		{"range, spelled out", Civic{"A1": "IL", "A3": "SPRINGFIELD", "HNO": "122", "PRD": "North", "RD": "Main", "STS": "Street", "LOC": "Apt 4"}, "road centerline", ""},
		{"wrong parity side", Civic{"A1": "IL", "A3": "Springfield", "HNO": "123", "RD": "Main"}, "road centerline", ""},
		{"number off the street", Civic{"A1": "IL", "A3": "Springfield", "HNO": "900", "RD": "Main"}, "", "HNO"},
		{"no such street", Civic{"A1": "IL", "A3": "Springfield", "HNO": "1", "RD": "Elm"}, "", "RD"},
		{"no such town", Civic{"A1": "IL", "A3": "Shelbyville", "HNO": "122", "RD": "Main"}, "", "A3"},
		{"wrong postal code", Civic{"A1": "IL", "A3": "Springfield", "HNO": "122", "RD": "Main", "PC": "99999"}, "road centerline", "PC"},
	}
	for _, c := range cases {
		v := a.Validate(c.c)
		if v.Matched != c.matched || strings.Join(v.Invalid, " ") != c.invalid {
			t.Errorf("%s: %+v", c.name, v)
		}
	}
	if v := a.Validate(cases[1].c); !slices.Contains(v.Unchecked, "LOC") || !slices.Contains(v.Valid, "HNO") {
		t.Errorf("tokens: %+v", v)
	}
}

func TestLoSTValidation(t *testing.T) {
	a, _ := ParseAddressGeoJSON([]byte(gis))
	s := &Server{Source: "ecrf.test", Boundaries: func() []Boundary {
		return []Boundary{{ID: "b1", Service: "urn:service:sos", Name: "Springfield PSAP", URI: "sip:sos@psap.test", Cities: []string{"Springfield"}}}
	}, Addresses: func() *Addresses { return &a }}
	req := `<findService xmlns="urn:ietf:params:xml:ns:lost1" validateLocation="true"><location id="1" profile="civic">
<civicAddress xmlns="urn:ietf:params:xml:ns:pidf:geopriv10:civicAddr"><country>US</country><A1>IL</A1><A3>Springfield</A3>
<RD>Main</RD><HNO>900</HNO></civicAddress></location><service>urn:service:sos</service></findService>`
	out := string(s.Respond(strings.NewReader(req)))
	if !strings.Contains(out, "<uri>sip:sos@psap.test</uri>") || !strings.Contains(out, "<invalid>HNO</invalid>") || !strings.Contains(out, "<valid>A1 A3 RD</valid>") {
		t.Fatal(out)
	}
	s.Addresses = nil
	if out := string(s.Respond(strings.NewReader(req))); !strings.Contains(out, "locationValidationUnavailable") {
		t.Fatal(out)
	}
}

// Suggestions for addresses not found: a misspelt street, the nearest number a street has (on the
// right side), a street in the next town; nothing for a street nowhere near. A position checked
// against the address point: near, far (with the point nearest it), and no point to check against.
func TestSuggestAndPosition(t *testing.T) {
	a, _ := ParseAddressGeoJSON([]byte(gis))
	first := func(c Civic) Suggestion {
		t.Helper()
		s := a.Suggest(c, 3)
		if len(s) == 0 {
			t.Fatalf("no suggestion for %v", c)
		}
		return s[0]
	}
	if s := first(Civic{"A1": "IL", "A3": "Springfield", "HNO": "250", "RD": "Oka", "STS": "Ave"}); s.Civic["RD"] != "Oak" || s.Civic["HNO"] != "250" || !strings.Contains(s.Why, "similar name (Oak Ave, not Oka)") {
		t.Errorf("misspelt: %+v", s)
	}
	if s := first(Civic{"A1": "IL", "A3": "Springfield", "HNO": "900", "RD": "Main"}); s.Civic["HNO"] != "198" || s.Source != "road centerline" || !strings.Contains(s.Why, "nearest number") {
		t.Errorf("number off the street: %+v", s)
	}
	if s := first(Civic{"A1": "IL", "A3": "Springfield", "HNO": "901", "RD": "Main"}); s.Civic["HNO"] != "199" {
		t.Errorf("odd stays odd: %+v", s)
	}
	if s := a.Suggest(Civic{"A1": "IL", "A3": "Springfield", "HNO": "150", "RD": "Main"}, 3); len(s) == 0 || s[0].Civic["HNO"] != "150" {
		t.Errorf("even: %+v", s)
	}
	if s := first(Civic{"A1": "IL", "A3": "Shelbyville", "HNO": "122", "RD": "Main"}); s.Civic["A3"] != "Springfield" || !strings.Contains(s.Why, "in Springfield, not Shelbyville") {
		t.Errorf("next town: %+v", s)
	}
	if s := a.Suggest(Civic{"A1": "IL", "HNO": "1", "RD": "Washington"}, 3); len(s) != 0 {
		t.Errorf("a street nowhere near: %+v", s)
	}
	if s := a.Suggest(Civic{"A1": "WI", "HNO": "250", "RD": "Oak"}, 3); len(s) != 0 {
		t.Errorf("another state: %+v", s)
	}
	if editDistance("MAIN", "MIAN") != 1 || editDistance("OAK", "OKA") != 1 || editDistance("KITTEN", "SITTING") != 3 || editDistance("", "AB") != 2 || editDistance("ÉLM", "ELM") != 1 {
		t.Error("editDistance")
	}
	oak := Civic{"A1": "IL", "A3": "Springfield", "HNO": "250", "RD": "Oak"}
	if pc := a.CheckPosition(oak, 39.8102, -89.6101, 10, 0); !pc.Checked || !pc.Within || pc.Metres > 30 || pc.Limit != 110 {
		t.Errorf("near: %+v", pc)
	}
	pc := a.CheckPosition(oak, 39.82, -89.61, 20, 50)
	if !pc.Checked || pc.Within || pc.Metres < 1000 || pc.Limit != 70 || !strings.Contains(pc.Why, "do not agree") {
		t.Errorf("far: %+v", pc)
	}
	if pc := a.CheckPosition(Civic{"HNO": "122", "RD": "Main"}, 39.8, -89.6, 0, 0); pc.Checked || !strings.Contains(pc.Why, "no point") {
		t.Errorf("no point: %+v", pc)
	}
}
