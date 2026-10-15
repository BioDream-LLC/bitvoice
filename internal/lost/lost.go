// Package lost is a LoST server (RFC 5222) for an NG911 ECRF: given a caller's location and a
// service (urn:service:sos and its sub-services), it answers with the PSAP or agency that serves
// that place, from service boundaries (GeoJSON polygons, as GIS exports them) or, for a civic
// address, from the boundary's place names. Standard library only.
package lost

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Boundary is one service area: who answers a service there.
type Boundary struct {
	ID       string         `json:"id"`
	Service  string         `json:"service"`  // urn:service:sos, urn:service:sos.police, ...
	Name     string         `json:"name"`     // the PSAP or agency's name
	URI      string         `json:"uri"`      // where calls go: sip:psap@example.gov
	Number   string         `json:"number"`   // the dial string shown with it (911)
	Polygons [][][2]float64 `json:"polygons"` // rings of lon, lat (GeoJSON order)
	Cities   []string       `json:"cities"`   // civic match: A3 values served (any case)
	Counties []string       `json:"counties"` // A2
	States   []string       `json:"states"`   // A1
	Expires  time.Duration  `json:"-"`
}

// contains says whether a point (lat, lon) is inside one of the boundary's polygons (even-odd rule).
func (b *Boundary) contains(lat, lon float64) bool {
	for _, ring := range b.Polygons {
		in := false
		for i, j := 0, len(ring)-1; i < len(ring); j, i = i, i+1 {
			xi, yi, xj, yj := ring[i][0], ring[i][1], ring[j][0], ring[j][1]
			if (yi > lat) != (yj > lat) && lon < (xj-xi)*(lat-yi)/(yj-yi)+xi {
				in = !in
			}
		}
		if in {
			return true
		}
	}
	return false
}

func fold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(v)) {
			return true
		}
	}
	return false
}

// matchesCivic says whether a civic address is served by this boundary: its city, else its
// county, within its state when states are listed.
func (b *Boundary) matchesCivic(state, county, city string) bool {
	if len(b.States) > 0 && !fold(b.States, state) {
		return false
	}
	if len(b.Cities) > 0 {
		return fold(b.Cities, city)
	}
	if len(b.Counties) > 0 {
		return fold(b.Counties, county)
	}
	return false
}

// ParseGeoJSON reads a FeatureCollection of Polygon and MultiPolygon features into boundaries for a
// service; each feature's properties may give name, uri, number and service (else the defaults).
func ParseGeoJSON(data []byte, service, defaultURI string) ([]Boundary, error) {
	var fc struct {
		Type     string `json:"type"`
		Features []struct {
			ID         any            `json:"id"`
			Properties map[string]any `json:"properties"`
			Geometry   struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("not GeoJSON: %w", err)
	}
	if fc.Type != "FeatureCollection" {
		return nil, errors.New("the GeoJSON must be a FeatureCollection of boundary polygons")
	}
	str := func(p map[string]any, keys ...string) string {
		for _, k := range keys {
			for pk, v := range p {
				if strings.EqualFold(pk, k) {
					if s, ok := v.(string); ok && s != "" {
						return s
					}
				}
			}
		}
		return ""
	}
	var out []Boundary
	for i, f := range fc.Features {
		b := Boundary{Service: firstNonEmpty(str(f.Properties, "service", "serviceurn", "ServiceURN"), service),
			Name:   str(f.Properties, "name", "agency", "DsplayName", "DisplayName", "agency_name", "psap"),
			URI:    firstNonEmpty(str(f.Properties, "uri", "routeuri", "RouteURI", "sip"), defaultURI),
			Number: firstNonEmpty(str(f.Properties, "number", "ServiceNum"), "911")}
		b.ID = fmt.Sprint(firstNonNil(f.ID, i+1))
		if b.Name == "" {
			b.Name = "Area " + b.ID
		}
		var rings [][][2]float64
		switch f.Geometry.Type {
		case "Polygon":
			var p [][][]float64
			if err := json.Unmarshal(f.Geometry.Coordinates, &p); err != nil {
				return nil, fmt.Errorf("feature %s: %w", b.ID, err)
			}
			rings = append(rings, toRings(p)...)
		case "MultiPolygon":
			var mp [][][][]float64
			if err := json.Unmarshal(f.Geometry.Coordinates, &mp); err != nil {
				return nil, fmt.Errorf("feature %s: %w", b.ID, err)
			}
			for _, p := range mp {
				rings = append(rings, toRings(p)...)
			}
		default:
			continue // points and lines are not boundaries
		}
		b.Polygons = rings
		out = append(out, b)
	}
	if len(out) == 0 {
		return nil, errors.New("the GeoJSON has no polygons")
	}
	return out, nil
}

// toRings keeps a polygon's outer ring (holes in PSAP boundaries are rare and BitVoice treats a
// hole as served by the same PSAP's neighbour boundary, which is listed separately).
func toRings(p [][][]float64) [][][2]float64 {
	if len(p) == 0 {
		return nil
	}
	var ring [][2]float64
	for _, c := range p[0] {
		if len(c) >= 2 {
			ring = append(ring, [2]float64{c[0], c[1]})
		}
	}
	return [][][2]float64{ring}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func firstNonNil(v ...any) any {
	for _, x := range v {
		if x != nil {
			return x
		}
	}
	return nil
}

// Server answers LoST queries from a set of boundaries.
type Server struct {
	Source     string // this server's name, in responses (an ECRF host name)
	Boundaries func() []Boundary
	Addresses  func() *Addresses // the LVF's data (nil: none)
	Now        func() time.Time
	// ForestGuide is the LoST address of the forest guide (RFC 5582) asked about a location no
	// boundary here covers: for a recursive query the answer is fetched from it, otherwise the asker
	// is redirected to it. Empty: notFound, as before.
	ForestGuide string
	Client      *http.Client // for the forest guide (a 5 second timeout when nil)
}

func (s *Server) addresses() *Addresses {
	if s.Addresses == nil {
		return nil
	}
	return s.Addresses()
}

// Query is a findService request, read.
type Query struct {
	Service             string
	Lat                 *float64
	Lon                 *float64
	State, County, City string
	Validate            bool
	Civic               Civic    // the whole civic address, for validation
	Recursive           bool     // the asker wants the answer fetched on its behalf
	Path                []string // the servers the query has already passed through
}

// node reading, namespace-free, as in package i3.
type el struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Text    string     `xml:",chardata"`
	Kids    []el       `xml:",any"`
}

func (e *el) find(name string) *el {
	for i := range e.Kids {
		if strings.EqualFold(e.Kids[i].XMLName.Local, name) {
			return &e.Kids[i]
		}
		if f := e.Kids[i].find(name); f != nil {
			return f
		}
	}
	return nil
}

func (e *el) attr(name string) string {
	for _, a := range e.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// ParseFindService reads a LoST findService request.
func ParseFindService(r io.Reader) (Query, error) {
	var root el
	if err := xml.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&root); err != nil {
		return Query{}, err
	}
	if root.XMLName.Local != "findService" {
		return Query{}, fmt.Errorf("unsupported request %s", root.XMLName.Local)
	}
	q := Query{Validate: root.attr("validateLocation") == "true", Recursive: root.attr("recursive") == "true"}
	if p := root.find("path"); p != nil {
		for _, v := range p.Kids {
			if strings.EqualFold(v.XMLName.Local, "via") {
				q.Path = append(q.Path, v.attr("source"))
			}
		}
	}
	if s := root.find("service"); s != nil {
		q.Service = strings.TrimSpace(s.Text)
	}
	if p := root.find("pos"); p != nil {
		f := strings.Fields(p.Text)
		if len(f) >= 2 {
			la, e1 := strconv.ParseFloat(f[0], 64)
			lo, e2 := strconv.ParseFloat(f[1], 64)
			if e1 == nil && e2 == nil {
				q.Lat, q.Lon = &la, &lo
			}
		}
	}
	if ca := root.find("civicAddress"); ca != nil {
		get := func(n string) string {
			if x := ca.find(n); x != nil {
				return strings.TrimSpace(x.Text)
			}
			return ""
		}
		q.State, q.County, q.City = get("A1"), get("A2"), get("A3")
		q.Civic = civicOf(ca)
	}
	return q, nil
}

// serviceMatches: a boundary for urn:service:sos serves its sub-services too, unless a more
// specific boundary is there.
func serviceMatches(have, want string) bool {
	return strings.EqualFold(have, want) || strings.HasPrefix(strings.ToLower(want), strings.ToLower(have)+".")
}

// Find is the boundary for a query: an exact service match before a parent service's.
func (s *Server) Find(q Query) (*Boundary, string) {
	if q.Service == "" {
		return nil, "badRequest"
	}
	if !strings.HasPrefix(strings.ToLower(q.Service), "urn:service:") {
		return nil, "serviceNotImplemented"
	}
	var best *Boundary
	for _, b := range s.Boundaries() {
		b := b
		if !serviceMatches(b.Service, q.Service) {
			continue
		}
		hit := false
		if q.Lat != nil {
			hit = b.contains(*q.Lat, *q.Lon)
		} else if q.City != "" || q.County != "" {
			hit = b.matchesCivic(q.State, q.County, q.City)
		}
		if !hit {
			continue
		}
		if best == nil || len(b.Service) > len(best.Service) {
			best = &b
		}
	}
	if best == nil {
		if q.Lat == nil && q.City == "" && q.County == "" {
			return nil, "locationProfileUnrecognized"
		}
		return nil, "notFound"
	}
	return best, ""
}

const ns = "urn:ietf:params:xml:ns:lost1"

// Respond answers a request body with a findServiceResponse or an errors document.
func (s *Server) Respond(body io.Reader) []byte {
	raw, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil {
		return s.errorDoc("badRequest", err.Error())
	}
	q, err := ParseFindService(bytes.NewReader(raw))
	if err != nil {
		return s.errorDoc("badRequest", err.Error())
	}
	for _, v := range q.Path {
		if strings.EqualFold(v, s.Source) {
			return s.errorDoc("loop", "this query has passed through "+s.Source+" before")
		}
	}
	b, code := s.Find(q)
	if b == nil && code == "notFound" && s.ForestGuide != "" {
		return s.forest(raw, q)
	}
	if b == nil {
		return s.errorDoc(code, "")
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	t := now().UTC()
	exp := b.Expires
	if exp == 0 {
		exp = 24 * time.Hour
	}
	var w bytes.Buffer
	w.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&w, `<findServiceResponse xmlns="%s"><mapping expires="%s" lastUpdated="%s" source="%s" sourceId="%s">`,
		ns, t.Add(exp).Format(time.RFC3339), t.Format(time.RFC3339), esc(s.Source), esc(b.ID))
	fmt.Fprintf(&w, `<displayName xml:lang="en">%s</displayName><service>%s</service><uri>%s</uri><serviceNumber>%s</serviceNumber></mapping>`,
		esc(b.Name), esc(q.Service), esc(b.URI), esc(b.Number))
	if q.Validate && q.Civic != nil {
		// The LVF's answer (RFC 5222 8.4.2), or a warning that it has no data to check against.
		if a := s.addresses(); a != nil && len(a.Streets)+len(a.Points) > 0 {
			v := a.Validate(q.Civic)
			w.WriteString(`<locationValidation>`)
			for _, x := range []struct {
				tag  string
				list []string
			}{{"valid", v.Valid}, {"invalid", v.Invalid}, {"unchecked", v.Unchecked}} {
				if len(x.list) > 0 {
					fmt.Fprintf(&w, `<%s>%s</%s>`, x.tag, esc(strings.Join(x.list, " ")), x.tag)
				}
			}
			w.WriteString(`</locationValidation>`)
		} else {
			fmt.Fprintf(&w, `<warnings source="%s"><locationValidationUnavailable message="no address data loaded"/></warnings>`, esc(s.Source))
		}
	}
	fmt.Fprintf(&w, `<path><via source="%s"/></path></findServiceResponse>`, esc(s.Source))
	return w.Bytes()
}

var (
	closeFind = regexp.MustCompile(`</([A-Za-z0-9_]+:)?findService>`)
	openPath  = regexp.MustCompile(`<([A-Za-z0-9_]+:)?path(\s[^>]*)?>`)
	closePath = regexp.MustCompile(`</([A-Za-z0-9_]+:)?path>`)
)

// forest takes a query no boundary here covers to the forest guide: asked on the asker's behalf
// when the query is recursive (this server added to its path, and to the answer's), or a redirect to
// it otherwise (RFC 5222 8.1, 13).
func (s *Server) forest(raw []byte, q Query) []byte {
	if !q.Recursive {
		target := s.ForestGuide
		if u, err := url.Parse(s.ForestGuide); err == nil && u.Host != "" {
			target = u.Hostname()
		}
		var w bytes.Buffer
		w.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
		fmt.Fprintf(&w, `<redirect xmlns="%s" target="%s" source="%s" message="no boundary here covers this location: ask the forest guide" xml:lang="en"/>`, ns, esc(target), esc(s.Source))
		return w.Bytes()
	}
	via := []byte(`<via source="` + esc(s.Source) + `"/>`)
	req := raw
	if loc := openPath.FindIndex(req); loc != nil {
		req = append(append(append([]byte{}, req[:loc[1]]...), via...), req[loc[1]:]...)
	} else if loc := closeFind.FindIndex(req); loc != nil {
		req = append(append(append([]byte{}, req[:loc[0]]...), append(append([]byte("<path>"), via...), []byte("</path>")...)...), req[loc[0]:]...)
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Post(s.ForestGuide, "application/lost+xml", bytes.NewReader(req))
	if err != nil {
		return s.errorDoc("serverTimeout", "the forest guide did not answer")
	}
	defer resp.Body.Close()
	ans, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode/100 != 2 {
		return s.errorDoc("serverError", "the forest guide's answer could not be read")
	}
	if loc := closePath.FindIndex(ans); loc != nil { // this server answered too: on the way back
		ans = append(append(append([]byte{}, ans[:loc[0]]...), via...), ans[loc[0]:]...)
	}
	return ans
}

func (s *Server) errorDoc(code, msg string) []byte {
	var w bytes.Buffer
	w.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&w, `<errors xmlns="%s" source="%s"><%s`, ns, esc(s.Source), code)
	if msg != "" {
		fmt.Fprintf(&w, ` message="%s"`, esc(msg))
	}
	w.WriteString(`/></errors>`)
	return w.Bytes()
}

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// Ask sends a findService to an outside ECRF and returns the URI it answers with.
func Ask(url string, q Query) (string, error) {
	var loc string
	if q.Lat != nil {
		loc = fmt.Sprintf(`<location id="l" profile="geodetic-2d"><Point xmlns="http://www.opengis.net/gml" srsName="urn:ogc:def:crs:EPSG::4326"><pos>%f %f</pos></Point></location>`, *q.Lat, *q.Lon)
	} else {
		loc = fmt.Sprintf(`<location id="l" profile="civic"><civicAddress xmlns="urn:ietf:params:xml:ns:pidf:geopriv10:civicAddr"><A1>%s</A1><A2>%s</A2><A3>%s</A3></civicAddress></location>`, esc(q.State), esc(q.County), esc(q.City))
	}
	body := `<?xml version="1.0"?><findService xmlns="` + ns + `" recursive="true" serviceBoundary="reference">` + loc + `<service>` + esc(q.Service) + `</service></findService>`
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(url, "application/lost+xml", strings.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("the ECRF did not answer: %w", err)
	}
	defer resp.Body.Close()
	var root el
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&root); err != nil {
		return "", fmt.Errorf("the ECRF's answer could not be read: %w", err)
	}
	if root.XMLName.Local == "errors" && len(root.Kids) > 0 {
		return "", errors.New(root.Kids[0].XMLName.Local)
	}
	if u := root.find("uri"); u != nil && strings.TrimSpace(u.Text) != "" {
		return strings.TrimSpace(u.Text), nil
	}
	return "", errors.New("the ECRF named no PSAP")
}
