package i3

import (
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// A location information server (LIS): it answers HELD location requests (RFC 5985) for devices
// named by telephone number (RFC 6155), from the records the authority keeps: each a number, its
// civic address and, when known, a position.

// LISRecord is one number's location.
type LISRecord struct {
	Number string  `json:"number"` // digits, as the device identity carries them
	Civic  Civic   `json:"civic"`
	Lat    float64 `json:"lat,omitempty"`
	Lon    float64 `json:"lon,omitempty"`
	Radius float64 `json:"radius,omitempty"` // metres
	Method string  `json:"method,omitempty"` // Manual unless the file says
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
// (a1), postal code (pc), country, name (nam); optionally lat, lon, radius, method.
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
		"postal code": "postalcode", "zip": "postalcode", "loc": "location", "bld": "building", "nam": "name", "latitude": "lat", "longitude": "lon",
		"phone": "number", "telephone": "number", "tn": "number", "callback": "number"}
	for i, h := range head {
		k := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
		if a, ok := alias[k]; ok {
			k = a
		}
		col[k] = i
	}
	if _, ok := col["number"]; !ok {
		return nil, errors.New("the header has no number column")
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
		raw := get(row, "number")
		if raw == "" {
			continue // a blank line
		}
		n := NumberKey(raw)
		if len(n) < 7 || len(n) > 15 {
			return nil, fmt.Errorf("line %d: %q is not a telephone number", line, get(row, "number"))
		}
		rec := LISRecord{Number: n, Method: get(row, "method"), Civic: Civic{
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
			return nil, fmt.Errorf("line %d: no street and no position for %s", line, n)
		}
		if i, dup := seen[n]; dup {
			out[i] = rec // the last line for a number wins
			continue
		}
		seen[n] = len(out)
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
	fmt.Fprintf(&b, `</gp:location-info><gp:usage-rules/><gp:method>%s</gp:method></gp:geopriv><dm:deviceID>tel:%s</dm:deviceID><dm:timestamp>%s</dm:timestamp></dm:device></presence>`,
		esc(method), esc(rec.Number), now.UTC().Format(time.RFC3339))
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
