package importer

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// vendor describes a CSV export from another phone system: how to recognise its header, and which
// of its columns hold what. Columns are matched by name (case and spacing ignored), first match wins.
type vendor struct {
	format string
	// match reports whether a header row is this vendor's.
	match                                             func(h map[string]bool) bool
	number, name, first, last, email, pin, mac, model []string
	// rows that are not people (a CM station of type "virtual", a Cisco device with no line).
	skip func(get func(...string) string) string
	// phones says what becomes of the phones listed (none of these vendors' phones are provisioned).
	phones string
}

var vendors = []vendor{
	{
		// Cisco Unified CM: Bulk Administration > Phones > Export Phones > All Details.
		format: "Cisco Unified CM phones export (BAT)",
		match:  func(h map[string]bool) bool { return h["device name"] && h["directory number 1"] },
		number: []string{"directory number 1"}, name: []string{"alerting name 1", "line text label 1", "display 1", "line description 1", "description"},
		mac: []string{"device name"}, model: []string{"device type", "phone type"},
		phones: "Cisco phones run Cisco's own firmware, which BitVoice does not set up: re-flash them with Cisco's third-party SIP (multiplatform) firmware, or replace them, and add them on Phones",
	},
	{
		// Cisco Unified CM: Bulk Administration > Users > Export Users.
		format: "Cisco Unified CM users export (BAT)",
		match:  func(h map[string]bool) bool { return h["user id"] && (h["telephone number"] || h["primary extension"]) },
		number: []string{"primary extension", "telephone number"}, first: []string{"first name"}, last: []string{"last name"},
		email: []string{"mail id", "email"}, pin: []string{"pin"},
	},
	{
		// Avaya IP Office Manager: File > Import/Export > Export, Users (user.csv).
		format: "Avaya IP Office users export",
		match:  func(h map[string]bool) bool { return h["full name"] && h["extension"] },
		number: []string{"extension"}, name: []string{"full name", "name"}, email: []string{"voicemail email", "email address", "email"},
		pin: []string{"voicemail code", "login code"},
	},
	{
		// Avaya Aura Communication Manager: list station, exported from the SAT or System Manager as CSV.
		format: "Avaya Communication Manager station list",
		match:  func(h map[string]bool) bool { return (h["extension"] || h["ext"]) && h["port"] && h["type"] },
		number: []string{"extension", "ext"}, name: []string{"name"}, model: []string{"type"},
		skip: func(get func(...string) string) string {
			if t := strings.ToLower(get("type")); t == "virtual" || strings.HasPrefix(t, "vdn") {
				return "a " + t + " station, not a phone"
			}
			return ""
		},
		phones: "Avaya H.323 and DCP phones do not speak SIP; Avaya SIP phones (J100 series) can register to BitVoice by hand",
	},
	{
		// Mitel MiVoice Business: System Data Management, User and Device Configuration, Export.
		format: "Mitel MiVoice Business users and devices export",
		match: func(h map[string]bool) bool {
			return (h["first name"] || h["last name"]) && (h["number"] || h["directory number"] || h["prime number"])
		},
		number: []string{"number", "directory number", "prime number"}, first: []string{"first name"}, last: []string{"last name"},
		email: []string{"email address", "email"}, pin: []string{"voice mail passcode", "voicemail passcode"},
		mac: []string{"mac address"}, model: []string{"device type", "set type"},
		phones: "Mitel phones run Mitel's own firmware, which BitVoice does not set up; Mitel 6800/6900 phones can register to BitVoice by hand in SIP mode",
	},
}

func headerKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(s, "\xef\xbb\xbf")))
	s = strings.Trim(s, `"`)
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "_", " ")), " ")
}

// vendorFor recognises a vendor CSV by its header row.
func vendorFor(data []byte) (*vendor, []string) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	r.FieldsPerRecord, r.LazyQuotes = -1, true
	head, err := r.Read()
	if err != nil || len(head) < 2 {
		return nil, nil
	}
	h := map[string]bool{}
	for _, x := range head {
		h[headerKey(x)] = true
	}
	for i := range vendors {
		if vendors[i].match(h) {
			return &vendors[i], head
		}
	}
	return nil, nil
}

var pinRe = regexp.MustCompile(`^[0-9]{4,10}$`)

func parseVendor(v *vendor, data []byte) (Result, error) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	r.FieldsPerRecord, r.LazyQuotes = -1, true
	head, _ := r.Read()
	col := map[string]int{}
	for i, x := range head {
		if _, seen := col[headerKey(x)]; !seen {
			col[headerKey(x)] = i
		}
	}
	res := Result{Format: v.format}
	seen := map[string]bool{}
	phones := 0
	line := 1
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("line %d: %v", line, err))
			continue
		}
		get := func(names ...string) string {
			for _, n := range names {
				if i, ok := col[n]; ok && i < len(row) {
					if s := strings.TrimSpace(row[i]); s != "" {
						return s
					}
				}
			}
			return ""
		}
		if strings.Join(row, "") == "" {
			continue
		}
		if v.skip != nil {
			if why := v.skip(get); why != "" {
				res.Warnings = append(res.Warnings, fmt.Sprintf("line %d: left out (%s)", line, why))
				continue
			}
		}
		num := digits(get(v.number...))
		mac := get(v.mac...)
		if num == "" {
			if mac != "" {
				res.Warnings = append(res.Warnings, fmt.Sprintf("line %d: %s has no number, left out", line, mac))
			}
			continue
		}
		if !numberRe.MatchString(num) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("line %d: %q is not an extension number BitVoice uses (2 to 8 digits)", line, num))
			continue
		}
		if mac != "" {
			phones++
		}
		if seen[num] {
			continue // a second phone or line appearance of the same extension
		}
		seen[num] = true
		name := get(v.name...)
		if name == "" {
			name = strings.TrimSpace(get(v.first...) + " " + get(v.last...))
		}
		if name == "" {
			name = "Extension " + num
		}
		x := config.Extension{Number: num, Name: name, Enabled: true, Outbound: true, Email: get(v.email...)}
		if !strings.Contains(x.Email, "@") {
			x.Email = ""
		}
		if pin := get(v.pin...); pinRe.MatchString(pin) {
			x.Voicemail.PIN = pin
		}
		// Every person gets a mailbox, as on the systems they come from.
		x.Voicemail.Enabled = true
		x.NoAnswer = &config.Destination{Type: "voicemail", Target: num}
		res.Extensions = append(res.Extensions, x)
	}
	if len(res.Extensions) > 0 {
		res.Warnings = append(res.Warnings, "SIP passwords are not in this export: BitVoice makes a new one for each extension, to enter in the phones (or let BitVoice set the phones up)")
	}
	if phones > 0 && v.phones != "" {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%d phones are listed: %s", phones, v.phones))
	}
	if len(res.Extensions) == 0 {
		return res, fmt.Errorf("this %s has no extensions BitVoice could read", v.format)
	}
	sortResult(&res)
	return res, nil
}
