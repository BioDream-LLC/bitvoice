package i3

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The legacy ALI link: a PSAP asks its ALI controller (or the ALI database's data link) for a
// callback number's record and gets a block of text back, framed by STX and ETX. The layout of that
// text differs between ALI providers and states (there is no one national layout), so it is read by
// a layout the PSAP sets: each field by its line and columns. The request is a template too.

// ALIField is where one field sits in the ALI text: line and column from 1, length (0: to the end
// of the line).
type ALIField struct {
	Name string `json:"name"` // one of ALIFieldNames
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Len  int    `json:"len"`
}

// ALIFieldNames are the fields a layout can name.
var ALIFieldNames = []string{"callback", "class", "name", "hno", "hns", "prd", "rd", "sts", "pod", "loc", "unit", "floor", "building",
	"city", "county", "state", "zip", "lat", "lon", "unc", "company", "esn", "pilot", "street"}

// ALIRecord is a record read from ALI text.
type ALIRecord struct {
	Fields   map[string]string `json:"fields"`
	Location *Location         `json:"location,omitempty"`
	Class    string            `json:"class,omitempty"`
	Name     string            `json:"name,omitempty"`
	Callback string            `json:"callback,omitempty"`
	Provider string            `json:"provider,omitempty"`
}

// CheckALILayout says what is wrong with a layout.
func CheckALILayout(fields []ALIField) error {
	seen := map[string]bool{}
	for i, f := range fields {
		known := false
		for _, n := range ALIFieldNames {
			known = known || n == f.Name
		}
		switch {
		case !known:
			return fmt.Errorf("field %d: %q is not a field ALI records have here", i+1, f.Name)
		case seen[f.Name]:
			return fmt.Errorf("field %d: %s is placed twice", i+1, f.Name)
		case f.Line < 1 || f.Line > 40 || f.Col < 1 || f.Col > 200 || f.Len < 0 || f.Len > 200:
			return fmt.Errorf("field %d (%s): line 1 to 40, column 1 to 200, length 0 to 200", i+1, f.Name)
		}
		seen[f.Name] = true
	}
	if len(fields) == 0 {
		return errors.New("place at least the callback number or the street")
	}
	return nil
}

// ALILines splits ALI text into its lines: the frame (STX, ETX) and any other control characters
// are dropped, and lines end at CR, LF or CR LF.
func ALILines(text string) []string {
	if i := strings.IndexByte(text, 0x02); i >= 0 {
		text = text[i+1:]
	}
	if i := strings.IndexByte(text, 0x03); i >= 0 {
		text = text[:i]
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, l)
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

var (
	digitsOnly = regexp.MustCompile(`\D`)
	numberHead = regexp.MustCompile(`^(\d+)\s*([A-Z0-9/-]{0,3})\s+(.*)$`)
	dirs       = map[string]bool{"N": true, "S": true, "E": true, "W": true, "NE": true, "NW": true, "SE": true, "SW": true}
)

// ParseALI reads ALI text by a layout.
func ParseALI(text string, layout []ALIField) (ALIRecord, error) {
	lines := ALILines(text)
	if len(lines) == 0 {
		return ALIRecord{}, errors.New("no ALI text")
	}
	rec := ALIRecord{Fields: map[string]string{}}
	for _, f := range layout {
		if f.Line > len(lines) {
			continue
		}
		l := []rune(lines[f.Line-1])
		if f.Col > len(l) {
			continue
		}
		end := len(l)
		if f.Len > 0 && f.Col-1+f.Len < end {
			end = f.Col - 1 + f.Len
		}
		if v := strings.TrimSpace(string(l[f.Col-1 : end])); v != "" {
			rec.Fields[f.Name] = v
		}
	}
	g := rec.Fields
	if strings.Contains(strings.ToUpper(strings.Join(lines, " ")), "NO RECORD FOUND") && g["rd"] == "" && g["street"] == "" {
		return rec, errors.New("the ALI database has no record for that number")
	}
	rec.Callback = digitsOnly.ReplaceAllString(g["callback"], "")
	rec.Class, rec.Name, rec.Provider = strings.ToUpper(g["class"]), g["name"], g["company"]
	c := Civic{Country: "US", Number: g["hno"], NumberSuf: g["hns"], PreDir: g["prd"], Street: g["rd"], StreetType: g["sts"], PostDir: g["pod"],
		Location: g["loc"], Unit: g["unit"], Floor: g["floor"], Building: g["building"], City: g["city"], County: g["county"], State: g["state"], PostalCode: g["zip"]}
	if c.Street == "" && g["street"] != "" {
		splitStreet(g["street"], &c)
	}
	var loc Location
	if !c.Empty() && (c.Street != "" || c.City != "") {
		cc := c
		loc.Civic = &cc
	}
	lat, e1 := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(g["lat"], "+")), 64)
	lon, e2 := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(g["lon"], "+")), 64)
	if e1 == nil && e2 == nil && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 && !(lat == 0 && lon == 0) {
		loc.Lat, loc.Lon = &lat, &lon
		loc.Shape = "Point"
		if u, err := strconv.ParseFloat(strings.TrimRight(strings.TrimSpace(g["unc"]), "Mm "), 64); err == nil && u > 0 {
			loc.Radius, loc.Shape = u, "Circle"
		}
	}
	if loc.Civic != nil || loc.HasPoint() {
		loc.Method = "ALI"
		loc.ProvidedBy = rec.Provider
		rec.Location = &loc
	}
	if rec.Location == nil {
		return rec, errors.New("the ALI text has no address or position where the layout looks")
	}
	return rec, nil
}

// splitStreet reads "123 N MAIN ST" into its parts (number, direction, name, type).
func splitStreet(s string, c *Civic) {
	s = strings.Join(strings.Fields(strings.ToUpper(s)), " ")
	if m := numberHead.FindStringSubmatch(s); m != nil {
		c.Number, s = m[1], m[3]
		switch {
		case dirs[m[2]]:
			s = m[2] + " " + m[3] // "123 N MAIN": the N is the direction, not a suffix
		case m[2] != "":
			c.NumberSuf = m[2]
		}
	}
	w := strings.Fields(s)
	if len(w) > 1 && dirs[w[0]] {
		c.PreDir, w = w[0], w[1:]
	}
	if len(w) > 1 && dirs[w[len(w)-1]] {
		c.PostDir, w = w[len(w)-1], w[:len(w)-1]
	}
	if len(w) > 1 && streetTypes[w[len(w)-1]] {
		c.StreetType, w = w[len(w)-1], w[:len(w)-1]
	}
	c.Street = strings.Join(w, " ")
}

var streetTypes = map[string]bool{"ST": true, "AVE": true, "AV": true, "RD": true, "DR": true, "LN": true, "BLVD": true, "CT": true, "PL": true,
	"WAY": true, "HWY": true, "PKWY": true, "CIR": true, "TER": true, "TRL": true, "PIKE": true, "STREET": true, "AVENUE": true, "ROAD": true, "DRIVE": true}

// ALIRequest fills in a request template: {ani10} the ten-digit callback number, {ani7} its last
// seven, {ani} the number as it is, {pos} the position, {pos2} and {pos3} it zero-padded; \x02, \x03,
// \r and \n as written. Empty: STX, the ten digits, ETX.
func ALIRequest(template, number, position string) []byte {
	if template == "" {
		template = `\x02{ani10}\x03`
	}
	d := digitsOnly.ReplaceAllString(number, "")
	if len(d) == 11 && d[0] == '1' {
		d = d[1:]
	}
	ani7 := d
	if len(d) > 7 {
		ani7 = d[len(d)-7:]
	}
	pad := func(n int) string {
		p := digitsOnly.ReplaceAllString(position, "")
		for len(p) < n {
			p = "0" + p
		}
		if len(p) > n {
			p = p[len(p)-n:]
		}
		return p
	}
	r := strings.NewReplacer(`\x02`, "\x02", `\x03`, "\x03", `\r`, "\r", `\n`, "\n", "{ani10}", d, "{ani7}", ani7, "{ani}", number,
		"{pos2}", pad(2), "{pos3}", pad(3), "{pos}", position)
	return []byte(r.Replace(template))
}
