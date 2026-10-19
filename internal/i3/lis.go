package i3

import (
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A location information server (LIS): it answers HELD location requests (RFC 5985) from the records
// the authority keeps: each a telephone number, a network (an address range: a building's subnet, a
// floor's VLAN) or a device's MAC address, with its civic address and, when known, a position. A
// request names its device by number, IP address or MAC address (RFC 6155); a request naming none
// is answered for the requester's own address, as a LIS on an access network does.

// LISRecord is one number's location.
type LISRecord struct {
	Number  string  `json:"number,omitempty"`  // digits, as the device identity carries them
	Network string  `json:"network,omitempty"` // an address range, 10.1.2.0/24 (one address is a /32 or /128)
	MAC     string  `json:"mac,omitempty"`     // a device's MAC address, a0:12:34:56:78:90
	Civic   Civic   `json:"civic"`
	Lat     float64 `json:"lat,omitempty"`
	Lon     float64 `json:"lon,omitempty"`
	Radius  float64 `json:"radius,omitempty"` // metres
	Method  string  `json:"method,omitempty"` // Manual unless the file says
}

// Key is the record's place in the LIS: its number, network or MAC address.
func (r LISRecord) Key() string {
	switch {
	case r.Network != "":
		return "net:" + r.Network
	case r.MAC != "":
		return "mac:" + r.MAC
	}
	return r.Number
}

// Identity is the record's device as a person reads it.
func (r LISRecord) Identity() string { return firstNonEmpty(r.Number, r.Network, r.MAC) }

// NetworkKey reads an address range or one address as a record's network (masked, canonical).
func NetworkKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked().String(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return "", fmt.Errorf("%q is not an address or an address range (10.1.2.0/24)", s)
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()).String(), nil
}

// MACKey reads a MAC address in any usual form (a0:12:.., A0-12-.., a012.3456.7890) as a0:12:34:56:78:90.
func MACKey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if hw, err := net.ParseMAC(s); err == nil && len(hw) == 6 {
		return hw.String(), nil
	}
	d := strings.Map(func(r rune) rune {
		if strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return r
		}
		if strings.ContainsRune(":-. ", r) {
			return -1
		}
		return '!'
	}, s)
	if len(d) == 12 && !strings.Contains(d, "!") {
		if hw, err := net.ParseMAC(strings.Join([]string{d[0:2], d[2:4], d[4:6], d[6:8], d[8:10], d[10:12]}, ":")); err == nil {
			return hw.String(), nil
		}
	}
	return "", fmt.Errorf("%q is not a MAC address", s)
}

// NumberKey is how numbers are matched: digits only, a North American 11-digit number without its 1.
func NumberKey(n string) string {
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, n)
	if len(d) == 11 && d[0] == '1' {
		d = d[1:]
	}
	return d
}

// ParseLISCSV reads location records from a spreadsheet with a header row. Columns (any order, any
// case): number, and the civic address as house number (hno), street (rd), street type (sts),
// direction (prd), unit, floor, room, location (loc), building (bld), city (a3), county (a2), state
// (a1), postal code (pc), country, name (nam); optionally lat, lon, radius, method. Instead of a number a
// row may name a network (an address range, or one address) or a MAC address: the header then needs
// at least one of the number, network and mac columns.
func ParseLISCSV(r io.Reader) ([]LISRecord, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	head, err := cr.Read()
	if err != nil {
		return nil, errors.New("the file has no header row")
	}
	col := map[string]int{}
	alias := map[string]string{"hno": "number_house", "house number": "number_house", "house": "number_house", "rd": "street", "sts": "streettype",
		"street type": "streettype", "prd": "predir", "direction": "predir", "a3": "city", "a2": "county", "a1": "state", "pc": "postalcode",
		"postal code": "postalcode", "zip": "postalcode", "loc": "location", "bld": "building", "flr": "floor", "nam": "name", "latitude": "lat", "longitude": "lon",
		"phone": "number", "telephone": "number", "tn": "number", "callback": "number", "subnet": "network", "cidr": "network",
		"address range": "network", "ip": "network", "ip address": "network", "mac address": "mac", "hardware address": "mac"}
	for i, h := range head {
		k := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
		if a, ok := alias[k]; ok {
			k = a
		}
		col[k] = i
	}
	_, hasNum := col["number"]
	_, hasNet := col["network"]
	_, hasMAC := col["mac"]
	if !hasNum && !hasNet && !hasMAC {
		return nil, errors.New("the header has no number, network or mac column")
	}
	get := func(row []string, k string) string {
		if i, ok := col[k]; ok && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}
	var out []LISRecord
	seen := map[string]int{}
	for line := 2; ; line++ {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", line, err)
		}
		raw, rawNet, rawMAC := get(row, "number"), get(row, "network"), get(row, "mac")
		named := 0
		for _, v := range []string{raw, rawNet, rawMAC} {
			if v != "" {
				named++
			}
		}
		if named == 0 {
			continue // a blank line
		}
		if named > 1 {
			return nil, fmt.Errorf("line %d: name one of a number, a network or a MAC address, not several", line)
		}
		var n, network, mac string
		switch {
		case raw != "":
			n = NumberKey(raw)
			if len(n) < 7 || len(n) > 15 {
				return nil, fmt.Errorf("line %d: %q is not a telephone number", line, raw)
			}
		case rawNet != "":
			if network, err = NetworkKey(rawNet); err != nil {
				return nil, fmt.Errorf("line %d: %v", line, err)
			}
		default:
			if mac, err = MACKey(rawMAC); err != nil {
				return nil, fmt.Errorf("line %d: %v", line, err)
			}
		}
		rec := LISRecord{Number: n, Network: network, MAC: mac, Method: get(row, "method"), Civic: Civic{
			Number: get(row, "number_house"), Street: get(row, "street"), StreetType: get(row, "streettype"), PreDir: get(row, "predir"),
			Unit: get(row, "unit"), Floor: get(row, "floor"), Room: get(row, "room"), Location: get(row, "location"), Building: get(row, "building"),
			City: get(row, "city"), County: get(row, "county"), State: get(row, "state"), PostalCode: get(row, "postalcode"),
			Country: strings.ToUpper(get(row, "country")), Name: get(row, "name")}}
		if rec.Civic.Country == "" {
			rec.Civic.Country = "US"
		}
		for k, p := range map[string]*float64{"lat": &rec.Lat, "lon": &rec.Lon, "radius": &rec.Radius} {
			if v := get(row, k); v != "" {
				f, err := strconv.ParseFloat(v, 64)
				if err != nil {
					return nil, fmt.Errorf("line %d: %s %q is not a number", line, k, v)
				}
				*p = f
			}
		}
		if rec.Lat < -90 || rec.Lat > 90 || rec.Lon < -180 || rec.Lon > 180 || rec.Radius < 0 {
			return nil, fmt.Errorf("line %d: the position is not in degrees", line)
		}
		if rec.Civic.Street == "" && rec.Lat == 0 && rec.Lon == 0 {
			return nil, fmt.Errorf("line %d: no street and no position for %s", line, rec.Identity())
		}
		if i, dup := seen[rec.Key()]; dup {
			out[i] = rec // the last line for a number, network or MAC address wins
			continue
		}
		seen[rec.Key()] = len(out)
		out = append(out, rec)
	}
	return out, nil
}

// BuildPIDF writes a PIDF-LO (RFC 4119, 5139, 5491) for a device: its civic address and, when given,
// its position as a point, or a circle with a radius.
func BuildPIDF(entity string, rec LISRecord, now time.Time) []byte {
	var b bytes.Buffer
	esc := func(s string) string {
		var x bytes.Buffer
		_ = xml.EscapeText(&x, []byte(s))
		return x.String()
	}
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>`+"\n"+
		`<presence xmlns="urn:ietf:params:xml:ns:pidf" xmlns:gp="urn:ietf:params:xml:ns:pidf:geopriv10" `+
		`xmlns:ca="urn:ietf:params:xml:ns:pidf:geopriv10:civicAddr" xmlns:gml="http://www.opengis.net/gml" `+
		`xmlns:gs="http://www.opengis.net/pidflo/1.0" xmlns:dm="urn:ietf:params:xml:ns:pidf:data-model" entity="%s">`, esc(entity))
	b.WriteString(`<dm:device id="lis"><gp:geopriv><gp:location-info>`)
	c := rec.Civic
	if !c.Empty() {
		b.WriteString(`<ca:civicAddress xml:lang="en">`)
		for _, kv := range [][2]string{{"country", c.Country}, {"A1", c.State}, {"A2", c.County}, {"A3", c.City}, {"PRD", c.PreDir}, {"RD", c.Street},
			{"STS", c.StreetType}, {"HNO", c.Number}, {"LOC", c.Location}, {"NAM", c.Name}, {"PC", c.PostalCode}, {"BLD", c.Building},
			{"UNIT", c.Unit}, {"FLR", c.Floor}, {"ROOM", c.Room}} {
			if v := strings.TrimSpace(kv[1]); v != "" {
				fmt.Fprintf(&b, "<ca:%s>%s</ca:%s>", kv[0], esc(v), kv[0])
			}
		}
		b.WriteString(`</ca:civicAddress>`)
	}
	if rec.Lat != 0 || rec.Lon != 0 {
		pos := strconv.FormatFloat(rec.Lat, 'f', -1, 64) + " " + strconv.FormatFloat(rec.Lon, 'f', -1, 64)
		if rec.Radius > 0 {
			fmt.Fprintf(&b, `<gs:Circle srsName="urn:ogc:def:crs:EPSG::4326"><gml:pos>%s</gml:pos><gs:radius uom="urn:ogc:def:uom:EPSG::9001">%s</gs:radius></gs:Circle>`,
				pos, strconv.FormatFloat(rec.Radius, 'f', -1, 64))
		} else {
			fmt.Fprintf(&b, `<gml:Point srsName="urn:ogc:def:crs:EPSG::4326"><gml:pos>%s</gml:pos></gml:Point>`, pos)
		}
	}
	method := rec.Method
	if method == "" {
		method = "Manual"
	}
	fmt.Fprintf(&b, `</gp:location-info><gp:usage-rules/><gp:method>%s</gp:method></gp:geopriv>`, esc(method))
	switch {
	case rec.Number != "":
		fmt.Fprintf(&b, `<dm:deviceID>tel:%s</dm:deviceID>`, esc(rec.Number))
	case rec.MAC != "":
		fmt.Fprintf(&b, `<dm:deviceID>mac:%s</dm:deviceID>`, strings.ReplaceAll(rec.MAC, ":", ""))
	}
	fmt.Fprintf(&b, `<dm:timestamp>%s</dm:timestamp></dm:device></presence>`, now.UTC().Format(time.RFC3339))
	return b.Bytes()
}

// HELDError is a HELD error response's code (RFC 5985 4.3): locationUnknown, badRequest,
// unsupportedMessage, notLocatable, generalLisError.
type HELDError string

func (e HELDError) Error() string { return string(e) }

// HELDDevice reads a HELD locationRequest and returns the telephone number its device identity
// names (RFC 6155 tel, or a tel: URI), or a HELDError.
func HELDDevice(body []byte) (string, error) {
	root, err := parseXML(body)
	if err != nil || root.find("locationRequest") == nil {
		return "", HELDError("badRequest")
	}
	dev := root.find("device")
	if dev == nil {
		return "", HELDError("notLocatable") // by the requester's own address: not a service of this LIS
	}
	if n := strings.TrimSpace(dev.val("tel")); n != "" {
		return n, nil
	}
	if u := strings.TrimSpace(dev.val("uri")); strings.HasPrefix(strings.ToLower(u), "tel:") {
		return strings.SplitN(u[4:], ";", 2)[0], nil
	}
	return "", HELDError("notLocatable")
}

// HELDTarget is the device a HELD request asks about: by number, by IP address or by MAC address
// (RFC 6155); all empty when the request names none (the requester itself).
type HELDTarget struct {
	Tel string
	IP  netip.Addr
	MAC string
}

// HELDQuery reads a HELD locationRequest's device identity, or a HELDError. A request with no device
// element asks for the requester's own location.
func HELDQuery(body []byte) (HELDTarget, error) {
	root, err := parseXML(body)
	if err != nil || root.find("locationRequest") == nil {
		return HELDTarget{}, HELDError("badRequest")
	}
	dev := root.find("device")
	if dev == nil {
		return HELDTarget{}, nil
	}
	var t HELDTarget
	if n := strings.TrimSpace(dev.val("tel")); n != "" {
		t.Tel = n
	} else if u := strings.TrimSpace(dev.val("uri")); strings.HasPrefix(strings.ToLower(u), "tel:") {
		t.Tel = strings.SplitN(u[4:], ";", 2)[0]
	}
	if s := strings.TrimSpace(dev.val("ip")); s != "" {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return HELDTarget{}, HELDError("badRequest")
		}
		t.IP = a.Unmap()
	}
	if s := strings.TrimSpace(dev.val("mac")); s != "" {
		m, err := MACKey(s)
		if err != nil {
			return HELDTarget{}, HELDError("badRequest")
		}
		t.MAC = m
	}
	if t.Tel == "" && !t.IP.IsValid() && t.MAC == "" {
		return HELDTarget{}, HELDError("notLocatable") // an identity of a kind this LIS does not keep
	}
	return t, nil
}

// LISIndex finds records by number, by MAC address, and by address within the most specific
// network that holds it.
type LISIndex struct {
	by   map[string]LISRecord
	bits []int // the networks' prefix lengths, longest first
}

// NewLISIndex indexes records kept by Key.
func NewLISIndex(by map[string]LISRecord) *LISIndex {
	ix := &LISIndex{by: by}
	seen := map[int]bool{}
	for _, r := range by {
		if p, err := netip.ParsePrefix(r.Network); err == nil && !seen[p.Bits()] {
			seen[p.Bits()] = true
			ix.bits = append(ix.bits, p.Bits())
		}
	}
	slices.Sort(ix.bits)
	slices.Reverse(ix.bits)
	return ix
}

// Find returns the record for a target: its number, its MAC address, then its address (or, when the
// request named nothing, the requester's) in the narrowest network listed.
func (ix *LISIndex) Find(t HELDTarget, requester netip.Addr) (LISRecord, bool) {
	if t.Tel != "" {
		if r, ok := ix.by[NumberKey(t.Tel)]; ok {
			return r, true
		}
	}
	if t.MAC != "" {
		if r, ok := ix.by["mac:"+t.MAC]; ok {
			return r, true
		}
	}
	a := t.IP
	if !a.IsValid() && t.Tel == "" && t.MAC == "" {
		a = requester.Unmap()
	}
	if a.IsValid() {
		return ix.Address(a)
	}
	return LISRecord{}, false
}

// Address is the record of the narrowest network holding a.
func (ix *LISIndex) Address(a netip.Addr) (LISRecord, bool) {
	a = a.Unmap()
	for _, bits := range ix.bits {
		if bits > a.BitLen() {
			continue
		}
		p, err := a.Prefix(bits)
		if err != nil {
			continue
		}
		if r, ok := ix.by["net:"+p.String()]; ok {
			return r, true
		}
	}
	return LISRecord{}, false
}

// HELDResponse wraps a PIDF-LO in a locationResponse.
func HELDResponse(pidf []byte) []byte {
	doc := pidf
	if i := bytes.Index(doc, []byte("?>")); bytes.HasPrefix(doc, []byte("<?xml")) && i > 0 {
		doc = bytes.TrimSpace(doc[i+2:])
	}
	return append(append([]byte(`<?xml version="1.0" encoding="UTF-8"?>`+"\n"+`<locationResponse xmlns="urn:ietf:params:xml:ns:geopriv:held">`), doc...), []byte(`</locationResponse>`)...)
}

// HELDErrorResponse is a HELD error document.
func HELDErrorResponse(code HELDError, message string) []byte {
	var x bytes.Buffer
	_ = xml.EscapeText(&x, []byte(message))
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<error xmlns="urn:ietf:params:xml:ns:geopriv:held" code="` + string(code) + `"><message xml:lang="en">` + x.String() + `</message></error>`)
}

// PointPIDF is a PIDF-LO holding a position only (a geocoder's answer).
func PointPIDF(entity string, lat, lon float64, now time.Time) string {
	return string(BuildPIDF(entity, LISRecord{Lat: lat, Lon: lon, Method: "Derived"}, now))
}

// CivicPIDF is a PIDF-LO holding a civic address only.
func CivicPIDF(entity string, c Civic, now time.Time) string {
	return string(BuildPIDF(entity, LISRecord{Civic: c, Method: "Derived"}, now))
}
