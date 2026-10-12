package lost

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The LVF (Location Validation Function, NENA i3): checks a civic address against the 911
// authority's GIS data before it is used, with LoST's validateLocation (RFC 5222 section 8.4.2).
// The data is NENA-STA-006 GIS: road centerlines with left and right address ranges, and
// site/structure address points.

// Street is a road centerline segment: its name and, for each side, an address range, its parity
// (O odd, E even, B both, Z no addresses) and the area that side lies in.
type Street struct {
	ID      string `json:"id"`
	PreDir  string `json:"preDir,omitempty"`
	Name    string `json:"name"`
	Type    string `json:"type,omitempty"`
	PostDir string `json:"postDir,omitempty"`
	Left    Side   `json:"left"`
	Right   Side   `json:"right"`
}

// Side is one side of a road segment.
type Side struct {
	From, To int
	Parity   string `json:",omitempty"`
	City     string `json:",omitempty"`
	County   string `json:",omitempty"`
	State    string `json:",omitempty"`
	Postal   string `json:",omitempty"`
}

// Point is a site/structure address point.
type Point struct {
	ID        string  `json:"id"`
	Number    int     `json:"number"`
	NumberSuf string  `json:"numberSuffix,omitempty"`
	PreDir    string  `json:"preDir,omitempty"`
	Name      string  `json:"name"`
	Type      string  `json:"type,omitempty"`
	PostDir   string  `json:"postDir,omitempty"`
	City      string  `json:"city,omitempty"`
	County    string  `json:"county,omitempty"`
	State     string  `json:"state,omitempty"`
	Postal    string  `json:"postal,omitempty"`
	Lat       float64 `json:"lat,omitempty"`
	Lon       float64 `json:"lon,omitempty"`
}

// Addresses is the LVF's data.
type Addresses struct {
	Streets []Street `json:"streets"`
	Points  []Point  `json:"points"`
}

// ParseAddressGeoJSON reads NENA GIS road centerlines (features with FromAddr_L) and address
// points (features with Add_Number) from one GeoJSON FeatureCollection.
func ParseAddressGeoJSON(data []byte) (Addresses, error) {
	var fc struct {
		Type     string `json:"type"`
		Features []struct {
			Properties map[string]any `json:"properties"`
			Geometry   struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	var out Addresses
	if err := json.Unmarshal(data, &fc); err != nil {
		return out, fmt.Errorf("not GeoJSON: %v", err)
	}
	if fc.Type != "FeatureCollection" {
		return out, errors.New("expected a GeoJSON FeatureCollection")
	}
	for i, f := range fc.Features {
		p := props(f.Properties)
		id := p.s("RCL_NGUID", "Site_NGUID", "NGUID", "id")
		if id == "" {
			id = "f" + strconv.Itoa(i+1)
		}
		switch {
		case p.has("FromAddr_L") || p.has("FromAddr_R"):
			out.Streets = append(out.Streets, Street{ID: id,
				PreDir: p.s("St_PreDir"), Name: p.s("St_Name"), Type: p.s("St_PosTyp"), PostDir: p.s("St_PosDir"),
				Left: Side{From: p.n("FromAddr_L"), To: p.n("ToAddr_L"), Parity: p.s("Parity_L"),
					City: p.s("IncMuni_L", "Inc_Muni_L"), County: p.s("County_L"), State: p.s("State_L"), Postal: p.s("PostCode_L")},
				Right: Side{From: p.n("FromAddr_R"), To: p.n("ToAddr_R"), Parity: p.s("Parity_R"),
					City: p.s("IncMuni_R", "Inc_Muni_R"), County: p.s("County_R"), State: p.s("State_R"), Postal: p.s("PostCode_R")}})
		case p.has("Add_Number"):
			pt := Point{ID: id, Number: p.n("Add_Number"), NumberSuf: p.s("AddNum_Suf"),
				PreDir: p.s("St_PreDir"), Name: p.s("St_Name"), Type: p.s("St_PosTyp"), PostDir: p.s("St_PosDir"),
				City: p.s("Inc_Muni", "IncMuni"), County: p.s("County"), State: p.s("State"), Postal: p.s("Post_Code", "PostCode")}
			var c []float64
			if f.Geometry.Type == "Point" && json.Unmarshal(f.Geometry.Coordinates, &c) == nil && len(c) >= 2 {
				pt.Lon, pt.Lat = c[0], c[1]
			}
			out.Points = append(out.Points, pt)
		}
	}
	if len(out.Streets)+len(out.Points) == 0 {
		return out, errors.New("no road centerlines (FromAddr_L ...) or address points (Add_Number ...) in the file")
	}
	return out, nil
}

type props map[string]any

func (p props) has(k string) bool { _, ok := p[k]; return ok }

func (p props) s(keys ...string) string {
	for _, k := range keys {
		switch v := p[k].(type) {
		case string:
			if v = strings.TrimSpace(v); v != "" {
				return v
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return ""
}

func (p props) n(k string) int {
	switch v := p[k].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	}
	return 0
}

// Civic is a civic address as LoST carries it, by PIDF-LO token (country, A1, A2, A3, PRD, RD,
// STS, POD, HNO, HNS, PC, LOC, ...).
type Civic map[string]string

// Validation is the outcome: the tokens found valid, those found invalid, those not checked, and
// the address as the GIS data has it when one matched.
type Validation struct {
	Valid     []string `json:"valid"`
	Invalid   []string `json:"invalid"`
	Unchecked []string `json:"unchecked"`
	Matched   string   `json:"matched,omitempty"` // "address point" or "road centerline"
	Found     Civic    `json:"found,omitempty"`
}

// OK says whether the address was found.
func (v Validation) OK() bool { return v.Matched != "" && len(v.Invalid) == 0 }

// checked are the tokens the LVF checks; others are reported unchecked.
var checked = []string{"country", "A1", "A2", "A3", "PRD", "RD", "STS", "POD", "HNO", "HNS", "PC"}

var abbrev = map[string]string{
	"NORTH": "N", "SOUTH": "S", "EAST": "E", "WEST": "W", "NORTHEAST": "NE", "NORTHWEST": "NW", "SOUTHEAST": "SE", "SOUTHWEST": "SW",
	"STREET": "ST", "AVENUE": "AVE", "AV": "AVE", "ROAD": "RD", "DRIVE": "DR", "BOULEVARD": "BLVD", "LANE": "LN", "COURT": "CT",
	"PLACE": "PL", "CIRCLE": "CIR", "HIGHWAY": "HWY", "PARKWAY": "PKWY", "TERRACE": "TER", "TRAIL": "TRL", "SQUARE": "SQ",
	"PIKE": "PIKE", "ALLEY": "ALY", "EXPRESSWAY": "EXPY", "FREEWAY": "FWY", "LOOP": "LOOP", "WAY": "WAY", "CROSSING": "XING",
	"SAINT": "ST", "MOUNT": "MT", "FORT": "FT",
}

// norm makes names comparable: upper case, single spaces, no dots, USPS abbreviations.
func norm(s string) string {
	f := strings.Fields(strings.ToUpper(strings.ReplaceAll(s, ".", "")))
	for i, w := range f {
		if a, ok := abbrev[w]; ok {
			f[i] = a
		}
	}
	return strings.Join(f, " ")
}

func same(a, b string) bool { return norm(a) == norm(b) }

// streetMatches compares the street name and, where both give them, the directionals and type.
func streetMatches(c Civic, preDir, name, typ, postDir string) bool {
	if !same(c["RD"], name) {
		return false
	}
	for _, x := range [][2]string{{c["PRD"], preDir}, {c["STS"], typ}, {c["POD"], postDir}} {
		if x[0] != "" && x[1] != "" && !same(x[0], x[1]) {
			return false
		}
	}
	return true
}

func inRange(n int, s Side) bool {
	lo, hi := min(s.From, s.To), max(s.From, s.To)
	if s.Parity == "Z" || hi == 0 || n < lo || n > hi {
		return false
	}
	switch strings.ToUpper(s.Parity) {
	case "O":
		return n%2 == 1
	case "E":
		return n%2 == 0
	}
	return true
}

// Validate checks a civic address against the data.
func (a *Addresses) Validate(c Civic) Validation {
	v := Validation{Valid: []string{}, Invalid: []string{}, Unchecked: []string{}}
	for k, val := range c {
		if val != "" && !slices.Contains(checked, k) {
			v.Unchecked = append(v.Unchecked, k)
		}
	}
	slices.Sort(v.Unchecked)
	given := func(k string) bool { return c[k] != "" }
	num, numErr := strconv.Atoi(strings.TrimSpace(c["HNO"]))
	area := func(city, county, state, postal string) map[string]bool {
		return map[string]bool{"A1": !given("A1") || same(c["A1"], state) || state == "",
			"A2": !given("A2") || same(c["A2"], county) || county == "",
			"A3": !given("A3") || same(c["A3"], city) || city == "",
			"PC": !given("PC") || c["PC"] == postal || postal == ""}
	}
	verdict := func(ok map[string]bool, matched string, found Civic) Validation {
		for _, k := range checked {
			if !given(k) {
				continue
			}
			if good, known := ok[k]; known && !good {
				v.Invalid = append(v.Invalid, k)
			} else if known || (k == "country" && matched != "") {
				v.Valid = append(v.Valid, k)
			} else {
				v.Unchecked = append(v.Unchecked, k)
			}
		}
		v.Matched, v.Found = matched, found
		return v
	}
	// An address point is the best evidence: the exact number on the street, in its area.
	if numErr == nil {
		for i := range a.Points {
			p := &a.Points[i]
			if p.Number != num || !streetMatches(c, p.PreDir, p.Name, p.Type, p.PostDir) {
				continue
			}
			ok := area(p.City, p.County, p.State, p.Postal)
			ok["HNO"], ok["RD"], ok["PRD"], ok["STS"], ok["POD"] = true, true, true, true, true
			if ok["A3"] && ok["A1"] {
				ok["HNS"] = !given("HNS") || same(c["HNS"], p.NumberSuf)
				return verdict(ok, "address point", Civic{"HNO": strconv.Itoa(p.Number) + p.NumberSuf, "PRD": p.PreDir, "RD": p.Name,
					"STS": p.Type, "POD": p.PostDir, "A3": p.City, "A2": p.County, "A1": p.State, "PC": p.Postal})
			}
		}
	}
	// Then a road centerline whose range on one side holds the number.
	streetInArea := false
	for _, s := range a.Streets {
		if !streetMatches(c, s.PreDir, s.Name, s.Type, s.PostDir) {
			continue
		}
		for _, side := range []Side{s.Left, s.Right} {
			ok := area(side.City, side.County, side.State, side.Postal)
			if !ok["A3"] || !ok["A1"] {
				continue
			}
			streetInArea = true
			if numErr == nil && inRange(num, side) {
				ok["HNO"], ok["RD"], ok["PRD"], ok["STS"], ok["POD"] = true, true, true, true, true
				return verdict(ok, "road centerline", Civic{"HNO": strconv.Itoa(num), "PRD": s.PreDir, "RD": s.Name, "STS": s.Type,
					"POD": s.PostDir, "A3": side.City, "A2": side.County, "A1": side.State, "PC": side.Postal})
			}
		}
	}
	// Not found: say which part is wrong. The street exists in the area: the number is wrong.
	cityKnown, stateKnown, countyKnown := !given("A3"), !given("A1"), !given("A2")
	note := func(city, county, state string) {
		cityKnown = cityKnown || same(city, c["A3"])
		countyKnown = countyKnown || same(county, c["A2"])
		stateKnown = stateKnown || same(state, c["A1"])
	}
	for _, s := range a.Streets {
		for _, side := range []Side{s.Left, s.Right} {
			note(side.City, side.County, side.State)
		}
	}
	for _, p := range a.Points {
		note(p.City, p.County, p.State)
	}
	ok := map[string]bool{"A3": cityKnown, "A2": countyKnown, "A1": stateKnown}
	if cityKnown {
		ok["RD"] = streetInArea
		if streetInArea {
			ok["HNO"] = false
		}
	}
	return verdict(ok, "", nil)
}

// civicOf turns a LoST or PIDF-LO civicAddress element's children into tokens.
func civicOf(ca *el) Civic {
	c := Civic{}
	for _, ch := range ca.Kids {
		if t := strings.TrimSpace(ch.Text); t != "" {
			c[ch.XMLName.Local] = t
		}
	}
	return c
}
