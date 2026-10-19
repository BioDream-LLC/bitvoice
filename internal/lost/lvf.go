package lost

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
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
	MSAG     string `json:",omitempty"` // the MSAG community name (MSAGComm_L/R), for legacy E9-1-1
	ESN      string `json:",omitempty"` // the emergency service number (ESN_L/R)
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
					City: p.s("IncMuni_L", "Inc_Muni_L"), County: p.s("County_L"), State: p.s("State_L"), Postal: p.s("PostCode_L"),
					MSAG: p.s("MSAGComm_L"), ESN: p.s("ESN_L")},
				Right: Side{From: p.n("FromAddr_R"), To: p.n("ToAddr_R"), Parity: p.s("Parity_R"),
					City: p.s("IncMuni_R", "Inc_Muni_R"), County: p.s("County_R"), State: p.s("State_R"), Postal: p.s("PostCode_R"),
					MSAG: p.s("MSAGComm_R"), ESN: p.s("ESN_R")}})
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

// Geocode finds the address point a civic address names (the number on its street, in its area).
func (a *Addresses) Geocode(c Civic) (Point, bool) {
	num, err := strconv.Atoi(strings.TrimSpace(c["HNO"]))
	if err != nil {
		return Point{}, false
	}
	for _, p := range a.Points {
		if p.Number != num || !streetMatches(c, p.PreDir, p.Name, p.Type, p.PostDir) || p.Lat == 0 && p.Lon == 0 {
			continue
		}
		if c["A3"] != "" && p.City != "" && !same(c["A3"], p.City) || c["A1"] != "" && p.State != "" && !same(c["A1"], p.State) {
			continue
		}
		if c["HNS"] != "" && !same(c["HNS"], p.NumberSuf) {
			continue
		}
		return p, true
	}
	return Point{}, false
}

// Reverse finds the address point nearest a position, within maxM metres.
func (a *Addresses) Reverse(lat, lon, maxM float64) (Point, bool) {
	best, bestD := Point{}, maxM
	found := false
	for _, p := range a.Points {
		if p.Lat == 0 && p.Lon == 0 {
			continue
		}
		if d := metres(lat, lon, p.Lat, p.Lon); d <= bestD {
			best, bestD, found = p, d, true
		}
	}
	return best, found
}

func metres(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371008.8
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := (lat2-lat1)*math.Pi/180, (lon2-lon1)*math.Pi/180
	h := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(h)))
}

// ToMSAG writes a civic address as an MSAG address: "HNO PRD RD STS POD, COMMUNITY, STATE", upper
// case, the community from the centerline side whose range holds the number (NENA-STA-010.3
// Appendix A maps the two). False when no side holds it, or that side has no MSAG community.
func (a *Addresses) ToMSAG(c Civic) (string, bool) {
	num, err := strconv.Atoi(strings.TrimSpace(c["HNO"]))
	if err != nil {
		return "", false
	}
	for _, s := range a.Streets {
		if !streetMatches(c, s.PreDir, s.Name, s.Type, s.PostDir) {
			continue
		}
		for _, side := range []Side{s.Left, s.Right} {
			if side.MSAG == "" || !inRange(num, side) || c["A1"] != "" && side.State != "" && !same(c["A1"], side.State) {
				continue
			}
			if c["A3"] != "" && side.City != "" && !same(c["A3"], side.City) && !same(c["A3"], side.MSAG) {
				continue
			}
			street := strings.Join(strings.Fields(strings.Join([]string{strconv.Itoa(num) + c["HNS"], s.PreDir, s.Name, s.Type, s.PostDir}, " ")), " ")
			return strings.ToUpper(street + ", " + side.MSAG + ", " + side.State), true
		}
	}
	return "", false
}

// FromMSAG reads an MSAG address written as ToMSAG writes it back into a civic address.
func (a *Addresses) FromMSAG(m string) (Civic, bool) {
	parts := strings.Split(m, ",")
	if len(parts) < 2 {
		return nil, false
	}
	words := strings.Fields(parts[0])
	if len(words) < 2 {
		return nil, false
	}
	numText := words[0]
	digits := strings.TrimRightFunc(numText, func(r rune) bool { return r < '0' || r > '9' })
	num, err := strconv.Atoi(digits)
	if err != nil {
		return nil, false
	}
	community := strings.TrimSpace(parts[1])
	state := ""
	if len(parts) > 2 {
		state = strings.TrimSpace(parts[2])
	}
	rest := strings.Join(words[1:], " ")
	for _, s := range a.Streets {
		line := strings.Join(strings.Fields(strings.Join([]string{s.PreDir, s.Name, s.Type, s.PostDir}, " ")), " ")
		if !same(line, rest) {
			continue
		}
		for _, side := range []Side{s.Left, s.Right} {
			if !same(side.MSAG, community) || !inRange(num, side) || state != "" && side.State != "" && !same(side.State, state) {
				continue
			}
			c := Civic{"HNO": digits, "PRD": s.PreDir, "RD": s.Name, "STS": s.Type, "POD": s.PostDir, "A3": side.City, "A2": side.County,
				"A1": side.State, "PC": side.Postal, "country": "US"}
			if suf := strings.TrimPrefix(numText, digits); suf != "" {
				c["HNS"] = suf
			}
			for k, v := range c {
				if v == "" {
					delete(c, k)
				}
			}
			return c, true
		}
	}
	return nil, false
}

// Suggestion is an address the data holds that is close to one that was not found: a street
// name a letter or two away, the same street in the next town, or the nearest number the street
// has.
type Suggestion struct {
	Civic  Civic  `json:"civic"`
	Why    string `json:"why"`
	Source string `json:"source"` // address point or road centerline
	score  int
}

// Suggest lists up to n addresses close to one Validate did not find, best first.
func (a *Addresses) Suggest(c Civic, n int) []Suggestion {
	want := norm(c["RD"])
	if want == "" || n <= 0 {
		return nil
	}
	num, numErr := strconv.Atoi(strings.TrimSpace(c["HNO"]))
	var out []Suggestion
	seen := map[string]bool{}
	add := func(s Suggestion) {
		k := strings.Join([]string{s.Civic["HNO"], norm(s.Civic["PRD"]), norm(s.Civic["RD"]), norm(s.Civic["STS"]), norm(s.Civic["A3"])}, "|")
		if !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
	}
	// How far a street's name is from the one asked for: 0 the same, then letters changed; -1 too far.
	nameGap := func(name string) int {
		got := norm(name)
		if got == want {
			return 0
		}
		d := editDistance(got, want)
		if d > max(1, min(3, len([]rune(want))/3)) {
			return -1
		}
		return d
	}
	areaGap := func(city, state string) (int, string) {
		if c["A1"] != "" && state != "" && !same(c["A1"], state) {
			return -1, ""
		}
		if c["A3"] != "" && city != "" && !same(c["A3"], city) {
			return 4, "in " + city + ", not " + c["A3"]
		}
		return 0, ""
	}
	street := func(pre, name, typ, post string) string {
		return strings.Join(strings.Fields(strings.Join([]string{pre, name, typ, post}, " ")), " ")
	}
	reasons := func(nameD int, name string, areaWhy string, numWhy string) string {
		var r []string
		if nameD > 0 {
			r = append(r, "a street of a similar name ("+name+", not "+c["RD"]+")")
		}
		if numWhy != "" {
			r = append(r, numWhy)
		}
		if areaWhy != "" {
			r = append(r, areaWhy)
		}
		if len(r) == 0 {
			return "the same street"
		}
		return strings.Join(r, "; ")
	}
	for _, p := range a.Points {
		nd := nameGap(p.Name)
		ad, aw := areaGap(p.City, p.State)
		if nd < 0 || ad < 0 {
			continue
		}
		score, numWhy := nd*10+ad, ""
		if numErr == nil && p.Number != num {
			gap := p.Number - num
			if gap < 0 {
				gap = -gap
			}
			score += 1 + min(gap/2, 1000)
			numWhy = "the nearest number on " + street(p.PreDir, p.Name, p.Type, p.PostDir)
		}
		add(Suggestion{Civic: Civic{"HNO": strconv.Itoa(p.Number) + p.NumberSuf, "PRD": p.PreDir, "RD": p.Name, "STS": p.Type, "POD": p.PostDir,
			"A3": p.City, "A2": p.County, "A1": p.State, "PC": p.Postal}, Source: "address point", score: score,
			Why: reasons(nd, street(p.PreDir, p.Name, p.Type, p.PostDir), aw, numWhy)})
	}
	for _, s := range a.Streets {
		nd := nameGap(s.Name)
		if nd < 0 {
			continue
		}
		for _, side := range []Side{s.Left, s.Right} {
			ad, aw := areaGap(side.City, side.State)
			if ad < 0 || side.Parity == "Z" || max(side.From, side.To) == 0 {
				continue
			}
			hn, numWhy, score := num, "", nd*10+ad+2 // a range is weaker evidence than a point
			if numErr != nil || !inRange(num, side) {
				if numErr != nil {
					continue
				}
				hn = nearestInRange(num, side)
				gap := hn - num
				if gap < 0 {
					gap = -gap
				}
				score += 1 + min(gap/2, 1000)
				if hn%2 != num%2 {
					score += 2 // the other side of the street
				}
				numWhy = "the nearest number " + street(s.PreDir, s.Name, s.Type, s.PostDir) + " has on that side"
			}
			add(Suggestion{Civic: Civic{"HNO": strconv.Itoa(hn), "PRD": s.PreDir, "RD": s.Name, "STS": s.Type, "POD": s.PostDir,
				"A3": side.City, "A2": side.County, "A1": side.State, "PC": side.Postal}, Source: "road centerline", score: score,
				Why: reasons(nd, street(s.PreDir, s.Name, s.Type, s.PostDir), aw, numWhy)})
		}
	}
	slices.SortStableFunc(out, func(x, y Suggestion) int { return x.score - y.score })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// nearestInRange is the number in a side's range (and parity) closest to n.
func nearestInRange(n int, s Side) int {
	lo, hi := min(s.From, s.To), max(s.From, s.To)
	x := min(max(n, lo), hi)
	switch strings.ToUpper(s.Parity) {
	case "O":
		if x%2 == 0 {
			if x+1 <= hi {
				x++
			} else {
				x--
			}
		}
	case "E":
		if x%2 != 0 {
			if x+1 <= hi {
				x++
			} else {
				x--
			}
		}
	}
	return x
}

// editDistance is the edit distance between two strings, by rune, counting a swap of two
// neighbouring letters as one edit (optimal string alignment): MIAN is one from MAIN.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}

// PositionCheck is how a position agrees with a civic address: the distance from the address's
// own point in the data, and whether it is within the limit (the position's radius plus a
// tolerance). Checked is false when the data has no point for the address.
type PositionCheck struct {
	Checked bool    `json:"checked"`
	Metres  float64 `json:"metres,omitempty"`
	Limit   float64 `json:"limit,omitempty"`
	Within  bool    `json:"within"`
	Point   *Point  `json:"point,omitempty"`   // the address's own point
	Nearest *Point  `json:"nearest,omitempty"` // the address point nearest the position, when it is another
	Why     string  `json:"why"`
}

// CheckPosition compares a position (with its uncertainty radius, metres) with a civic address,
// allowing tolerance metres more (100 when 0): the size of a building plot and of GIS error.
func (a *Addresses) CheckPosition(c Civic, lat, lon, radius, tolerance float64) PositionCheck {
	if tolerance <= 0 {
		tolerance = 100
	}
	pc := PositionCheck{Limit: math.Round(radius + tolerance)}
	if near, ok := a.Reverse(lat, lon, 1000); ok {
		pc.Nearest = &near
	}
	p, ok := a.Geocode(c)
	if !ok {
		pc.Why = "the address data has no point for this address, so the position cannot be checked against it"
		return pc
	}
	pc.Checked, pc.Point = true, &p
	pc.Metres = math.Round(metres(lat, lon, p.Lat, p.Lon))
	pc.Within = pc.Metres <= pc.Limit
	if pc.Nearest != nil && pc.Nearest.ID == p.ID {
		pc.Nearest = nil
	}
	where := fmt.Sprintf("%d %s", p.Number, strings.Join(strings.Fields(strings.Join([]string{p.PreDir, p.Name, p.Type, p.PostDir}, " ")), " "))
	if pc.Within {
		pc.Why = fmt.Sprintf("the position is %.0f m from %s's address point, within %.0f m", pc.Metres, where, pc.Limit)
	} else {
		pc.Why = fmt.Sprintf("the position is %.0f m from %s's address point, more than %.0f m: the two do not agree", pc.Metres, where, pc.Limit)
		if n := pc.Nearest; n != nil {
			pc.Why += fmt.Sprintf("; the address point nearest the position is %d %s", n.Number,
				strings.Join(strings.Fields(strings.Join([]string{n.PreDir, n.Name, n.Type, n.PostDir}, " ")), " "))
		}
	}
	return pc
}
