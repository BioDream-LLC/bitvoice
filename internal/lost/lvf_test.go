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
