// Package i3 reads and writes what NG911 calls carry, with nothing but Go's standard library:
// caller location as PIDF-LO (RFC 4119, 5139, 5491), by value in the INVITE or by reference
// (fetched with HELD, RFC 5985 and 6753), the Additional Data that comes with it (RFC 7852), the
// Emergency Incident Data Object a PSAP hands to CAD (NENA-STA-024), and NENA i3 log events
// (NENA-STA-010).
package i3

import (
	"bytes"
	"encoding/xml"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// node is an XML element by local name (namespaces ignored: senders differ in prefixes, and the
// element names in these documents do not clash).
type node struct {
	name  string
	attrs map[string]string
	text  string
	kids  []*node
}

func parseXML(b []byte) (*node, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	root := &node{}
	stack := []*node{root}
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: t.Name.Local, attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.attrs[a.Name.Local] = a.Value
			}
			top := stack[len(stack)-1]
			top.kids = append(top.kids, n)
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			top := stack[len(stack)-1]
			top.text += string(t)
		}
	}
	return root, nil
}

// find is the first element with this name, anywhere below.
func (n *node) find(name string) *node {
	if n == nil {
		return nil
	}
	for _, k := range n.kids {
		if strings.EqualFold(k.name, name) {
			return k
		}
		if f := k.find(name); f != nil {
			return f
		}
	}
	return nil
}

func (n *node) all(name string) []*node {
	var out []*node
	if n == nil {
		return nil
	}
	for _, k := range n.kids {
		if strings.EqualFold(k.name, name) {
			out = append(out, k)
		}
		out = append(out, k.all(name)...)
	}
	return out
}

func (n *node) val(name string) string {
	if f := n.find(name); f != nil {
		return strings.TrimSpace(f.text)
	}
	return ""
}

// Civic is a civic address (RFC 5139 and the NENA CLDXF fields it carries).
type Civic struct {
	Country     string `json:"country,omitempty"`         // two letters
	State       string `json:"state,omitempty"`           // A1
	County      string `json:"county,omitempty"`          // A2
	City        string `json:"city,omitempty"`            // A3
	District    string `json:"district,omitempty"`        // A4, A5, A6 when used
	Number      string `json:"number,omitempty"`          // HNO
	NumberSuf   string `json:"numberSuffix,omitempty"`    // HNS
	PreDir      string `json:"preDir,omitempty"`          // PRD
	PreMod      string `json:"preMod,omitempty"`          // PRM
	Street      string `json:"street,omitempty"`          // RD (or A6 in older senders)
	StreetType  string `json:"streetType,omitempty"`      // STS
	PostDir     string `json:"postDir,omitempty"`         // POD
	PostMod     string `json:"postMod,omitempty"`         // POM
	Landmark    string `json:"landmark,omitempty"`        // LMK
	Location    string `json:"location,omitempty"`        // LOC: suite, apartment, more
	Name        string `json:"name,omitempty"`            // NAM: a business or resident name
	PostalCode  string `json:"postalCode,omitempty"`      // PC
	Building    string `json:"building,omitempty"`        // BLD
	Unit        string `json:"unit,omitempty"`            // UNIT
	Floor       string `json:"floor,omitempty"`           // FLR
	Room        string `json:"room,omitempty"`            // ROOM
	Seat        string `json:"seat,omitempty"`            // SEAT
	PlaceType   string `json:"placeType,omitempty"`       // PLC
	PostalCommu string `json:"postalCommunity,omitempty"` // PCN
}

// Street is the street line: number, direction, name, type.
func (c Civic) StreetLine() string {
	parts := []string{c.Number + c.NumberSuf, c.PreMod, c.PreDir, c.Street, c.StreetType, c.PostDir, c.PostMod}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

// String is the address on one line, as a call-taker reads it.
func (c Civic) String() string {
	var parts []string
	if s := c.StreetLine(); s != "" {
		parts = append(parts, s)
	}
	for _, p := range []string{join(" ", "Bldg", c.Building), join(" ", "Unit", c.Unit), join(" ", "Floor", c.Floor), join(" ", "Room", c.Room), c.Location} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	city := strings.TrimSpace(strings.Join(strings.Fields(c.City+" "+c.State+" "+c.PostalCode), " "))
	if city != "" {
		parts = append(parts, city)
	}
	if c.Landmark != "" {
		parts = append(parts, "("+c.Landmark+")")
	}
	return strings.Join(parts, ", ")
}

func join(sep, label, v string) string {
	if v == "" {
		return ""
	}
	return label + sep + v
}

// Empty reports an address with nothing in it.
func (c Civic) Empty() bool { return c == Civic{} }

// Location is where a caller is, as their network or device said.
type Location struct {
	Civic      *Civic       `json:"civic,omitempty"`
	Lat        *float64     `json:"lat,omitempty"`
	Lon        *float64     `json:"lon,omitempty"`
	Alt        *float64     `json:"alt,omitempty"`
	Radius     float64      `json:"radius,omitempty"`     // metres around the point (a circle, or an ellipse's major axis)
	Shape      string       `json:"shape,omitempty"`      // Point, Circle, Ellipse, Polygon, Sphere, ArcBand
	Polygon    [][2]float64 `json:"polygon,omitempty"`    // lat, lon
	Confidence int          `json:"confidence,omitempty"` // percent, when given
	Method     string       `json:"method,omitempty"`     // GPS, Cell, Wiremap, Manual, DHCP, ...
	ProvidedBy string       `json:"providedBy,omitempty"`
	Time       time.Time    `json:"time,omitempty"`
	Entity     string       `json:"entity,omitempty"`    // the presentity: whose location
	Device     string       `json:"device,omitempty"`    // a device identity, when the location is a device's
	Retention  string       `json:"retention,omitempty"` // retention-expiry
}

// HasPoint reports a geodetic position.
func (l Location) HasPoint() bool { return l.Lat != nil && l.Lon != nil }

// ParsePIDF reads a PIDF-LO document. It takes every location in it, the first civic address and
// the first geodetic shape; a document may carry both (a phone's GPS and the address it was
// registered at).
func ParsePIDF(b []byte) (Location, error) {
	root, err := parseXML(b)
	if err != nil {
		return Location{}, err
	}
	p := root.find("presence")
	if p == nil {
		return Location{}, errNotPIDF
	}
	loc := Location{Entity: p.attrs["entity"]}
	if dev := root.find("device"); dev != nil {
		loc.Device = dev.val("deviceID")
	}
	if t := root.val("timestamp"); t != "" {
		loc.Time, _ = time.Parse(time.RFC3339, t)
	}
	loc.Method = root.val("method")
	if pb := root.find("provided-by"); pb != nil {
		loc.ProvidedBy = strings.TrimSpace(collectText(pb))
	}
	loc.Retention = root.val("retention-expiry")
	if ca := root.find("civicAddress"); ca != nil {
		loc.Civic = parseCivic(ca)
	}
	for _, shape := range []string{"Point", "Circle", "Ellipse", "Polygon", "Sphere", "Ellipsoid", "ArcBand", "Prism"} {
		g := root.find(shape)
		if g == nil {
			continue
		}
		loc.Shape = shape
		switch shape {
		case "Polygon", "Prism":
			pts := parsePosList(g)
			loc.Polygon = pts
			if len(pts) > 0 {
				// The centre of a polygon, for a map pin: the mean of its corners.
				var la, lo float64
				for _, q := range pts {
					la, lo = la+q[0], lo+q[1]
				}
				la, lo = la/float64(len(pts)), lo/float64(len(pts))
				loc.Lat, loc.Lon = &la, &lo
				for _, q := range pts {
					loc.Radius = math.Max(loc.Radius, metres(la, lo, q[0], q[1]))
				}
			}
		default:
			if pos := g.val("pos"); pos != "" {
				f := strings.Fields(pos)
				if len(f) >= 2 {
					la, e1 := strconv.ParseFloat(f[0], 64)
					lo, e2 := strconv.ParseFloat(f[1], 64)
					if e1 == nil && e2 == nil && la >= -90 && la <= 90 && lo >= -180 && lo <= 180 {
						loc.Lat, loc.Lon = &la, &lo
					}
					if len(f) >= 3 {
						if al, err := strconv.ParseFloat(f[2], 64); err == nil {
							loc.Alt = &al
						}
					}
				}
			}
			for _, r := range []string{"radius", "semiMajorAxis", "outerRadius"} {
				if n := g.find(r); n != nil {
					loc.Radius = toMetres(n)
					break
				}
			}
		}
		break
	}
	if c := root.find("confidence"); c != nil {
		loc.Confidence, _ = strconv.Atoi(strings.TrimSpace(c.text))
	}
	if loc.Civic == nil && !loc.HasPoint() {
		return loc, errNoLocation
	}
	return loc, nil
}

func collectText(n *node) string {
	s := n.text
	for _, k := range n.kids {
		s += " " + collectText(k)
	}
	return strings.Join(strings.Fields(s), " ")
}

func parseCivic(ca *node) *Civic {
	get := func(names ...string) string {
		for _, n := range names {
			for _, k := range ca.kids {
				if strings.EqualFold(k.name, n) {
					if v := strings.TrimSpace(k.text); v != "" {
						return v
					}
				}
			}
		}
		return ""
	}
	c := &Civic{Country: get("country"), State: get("A1"), County: get("A2"), City: get("A3"),
		District: strings.TrimSpace(get("A4") + " " + get("A5")), Number: get("HNO"), NumberSuf: get("HNS"),
		PreDir: get("PRD"), PreMod: get("PRM"), Street: get("RD", "A6"), StreetType: get("STS"), PostDir: get("POD"),
		PostMod: get("POM"), Landmark: get("LMK"), Location: get("LOC"), Name: get("NAM"), PostalCode: get("PC"),
		Building: get("BLD"), Unit: get("UNIT"), Floor: get("FLR"), Room: get("ROOM"), Seat: get("SEAT"),
		PlaceType: get("PLC"), PostalCommu: get("PCN")}
	if c.Empty() {
		return nil
	}
	return c
}

func parsePosList(g *node) [][2]float64 {
	var nums []float64
	if pl := g.val("posList"); pl != "" {
		for _, f := range strings.Fields(pl) {
			if v, err := strconv.ParseFloat(f, 64); err == nil {
				nums = append(nums, v)
			}
		}
	} else {
		for _, p := range g.all("pos") {
			for _, f := range strings.Fields(p.text) {
				if v, err := strconv.ParseFloat(f, 64); err == nil {
					nums = append(nums, v)
				}
			}
		}
	}
	dim := 2
	if g.name == "Prism" || strings.Contains(g.find("posList").attrSafe("srsDimension"), "3") {
		dim = 3
	}
	var out [][2]float64
	for i := 0; i+dim-1 < len(nums); i += dim {
		out = append(out, [2]float64{nums[i], nums[i+1]})
	}
	if len(out) > 1 && out[0] == out[len(out)-1] {
		out = out[:len(out)-1] // a polygon ring repeats its first corner
	}
	return out
}

func (n *node) attrSafe(k string) string {
	if n == nil {
		return ""
	}
	return n.attrs[k]
}

// toMetres reads a length with its unit (EPSG 9001 metres, or kilometres and feet when said).
func toMetres(n *node) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(n.text), 64)
	if err != nil {
		return 0
	}
	switch u := n.attrs["uom"]; {
	case strings.HasSuffix(u, "9036"): // kilometre
		return v * 1000
	case strings.HasSuffix(u, "9002"): // foot
		return v * 0.3048
	}
	return v
}

// metres is the distance between two points on the earth (haversine).
func metres(la1, lo1, la2, lo2 float64) float64 {
	const r = 6371008.8
	rad := math.Pi / 180
	dla, dlo := (la2-la1)*rad, (lo2-lo1)*rad
	a := math.Sin(dla/2)*math.Sin(dla/2) + math.Cos(la1*rad)*math.Cos(la2*rad)*math.Sin(dlo/2)*math.Sin(dlo/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}

type i3Error string

func (e i3Error) Error() string { return string(e) }

const (
	errNotPIDF    = i3Error("not a PIDF-LO document")
	errNoLocation = i3Error("the location document holds no address and no position")
)
