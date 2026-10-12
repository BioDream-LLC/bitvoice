// Package importer reads other phone systems' configuration and turns it into BitVoice extensions and
// trunks, so a site can move without retyping: FreePBX's Bulk Handler extension CSV, Asterisk's
// pjsip.conf and sip.conf, 3CX's users CSV, and the CSV exports of Cisco Unified CM, Avaya IP Office,
// Avaya Communication Manager and Mitel MiVoice Business. It reports what it could not bring over instead of guessing.
package importer

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// Result is what an import found.
type Result struct {
	Format     string             `json:"format"`
	Extensions []config.Extension `json:"extensions"`
	Trunks     []config.Trunk     `json:"trunks"`
	// Call flows: groups, incoming numbers and dial patterns from systems that export them.
	RingGroups     []config.RingGroup     `json:"ringGroups"`
	InboundRoutes  []config.InboundRoute  `json:"inboundRoutes"`
	OutboundRoutes []config.OutboundRoute `json:"outboundRoutes"`
	Warnings       []string               `json:"warnings"`
}

var numberRe = regexp.MustCompile(`^[0-9]{2,8}$`)

// Parse detects the format from the file name and content. Lists in the result are never nil.
func Parse(name string, data []byte) (Result, error) {
	r, err := parse(name, data)
	if r.Extensions == nil {
		r.Extensions = []config.Extension{}
	}
	if r.Trunks == nil {
		r.Trunks = []config.Trunk{}
	}
	fill(&r)
	return r, err
}

func fill(r *Result) {
	if r.Extensions == nil {
		r.Extensions = []config.Extension{}
	}
	if r.Trunks == nil {
		r.Trunks = []config.Trunk{}
	}
	if r.RingGroups == nil {
		r.RingGroups = []config.RingGroup{}
	}
	if r.InboundRoutes == nil {
		r.InboundRoutes = []config.InboundRoute{}
	}
	if r.OutboundRoutes == nil {
		r.OutboundRoutes = []config.OutboundRoute{}
	}
	if r.Warnings == nil {
		r.Warnings = []string{}
	}
}

func parse(name string, data []byte) (Result, error) {
	lower := strings.ToLower(name)
	text := string(data)
	first, _, _ := strings.Cut(strings.ToLower(strings.TrimPrefix(text, "\xef\xbb\xbf")), "\n")
	if v, _ := vendorFor(data); v != nil {
		return parseVendor(v, data)
	}
	switch {
	case strings.Contains(first, "firstname") && strings.Contains(first, "number"):
		return parse3CX(data)
	case strings.HasSuffix(lower, ".csv") || looksCSV(text):
		return parseCSV(data)
	case strings.Contains(lower, "pjsip") || strings.Contains(text, "type=endpoint") || strings.Contains(text, "type = endpoint"):
		return parsePJSIP(data), nil
	case strings.Contains(lower, "sip") || strings.Contains(text, "type=friend") || strings.Contains(text, "type=peer"):
		return parseSIPConf(data), nil
	}
	return Result{}, fmt.Errorf("this is not a file BitVoice can import: use a FreePBX Bulk Handler extensions CSV, a 3CX users CSV, pjsip.conf, sip.conf, or a CSV export from Cisco Unified CM, Avaya IP Office, Avaya Communication Manager or Mitel MiVoice Business")
}

func looksCSV(text string) bool {
	first, _, _ := strings.Cut(text, "\n")
	first = strings.ToLower(first)
	return strings.Contains(first, "extension") && strings.Contains(first, ",")
}

// parseCSV reads FreePBX's Bulk Handler export (Admin > Bulk Handler > Export > Extensions).
func parseCSV(data []byte) (Result, error) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true // exports from older systems leave quotes in caller IDs unescaped
	head, err := r.Read()
	if err != nil {
		return Result{}, fmt.Errorf("the CSV has no header row: %w", err)
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	get := func(row []string, names ...string) string {
		for _, n := range names {
			if i, ok := col[n]; ok && i < len(row) {
				if v := strings.TrimSpace(row[i]); v != "" {
					return v
				}
			}
		}
		return ""
	}
	if _, ok := col["extension"]; !ok {
		return Result{}, fmt.Errorf("the CSV has no \"extension\" column")
	}
	res := Result{Format: "FreePBX Bulk Handler CSV"}
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
		num := get(row, "extension")
		if !numberRe.MatchString(num) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("line %d: %q is not an extension number BitVoice uses (2 to 8 digits)", line, num))
			continue
		}
		x := config.Extension{Number: num, Name: get(row, "name", "description"), Enabled: true, Outbound: true,
			Secret: get(row, "secret", "devinfo_secret", "password"), Email: get(row, "email"),
			CallerID: digits(get(row, "outboundcid"))}
		if x.Name == "" {
			x.Name = "Extension " + num
		}
		vm := strings.ToLower(get(row, "voicemail"))
		x.Voicemail.Enabled = vm != "" && vm != "novm" && vm != "disabled" && vm != "no"
		if pin := get(row, "vmpwd", "voicemail_vmpwd"); regexp.MustCompile(`^[0-9]{4,10}$`).MatchString(pin) {
			x.Voicemail.PIN = pin
		}
		if x.Voicemail.Enabled {
			x.NoAnswer = &config.Destination{Type: "voicemail", Target: num}
		}
		if len(x.Secret) < 12 {
			if x.Secret != "" {
				res.Warnings = append(res.Warnings, fmt.Sprintf("extension %s: its SIP password is shorter than 12 characters, so BitVoice makes a new one; re-enter it in the phone", num))
			}
			x.Secret = ""
		}
		res.Extensions = append(res.Extensions, x)
	}
	return res, nil
}

func digits(s string) string {
	// "Name" <2125550100> or plain digits.
	if i := strings.LastIndexByte(s, '<'); i >= 0 {
		s = strings.TrimSuffix(s[i+1:], ">")
	}
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' || r == '+' && b.Len() == 0 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// section is one [name] block of an Asterisk configuration file.
type section struct {
	name      string
	template  bool     // [name](!)
	inherits  []string // [name](a,b)
	values    map[string][]string
	lineOrder int
}

func (s *section) get(k string) string {
	if v := s.values[k]; len(v) > 0 {
		return v[len(v)-1]
	}
	return ""
}

var sectionRe = regexp.MustCompile(`^\[([^\]]+)\]\s*(?:\(([^)]*)\))?`)

// parseINI reads Asterisk's configuration syntax, applying template inheritance.
func parseINI(data []byte) ([]*section, []string) {
	var out []*section
	var registers []string
	byName := map[string]*section{}
	var cur *section
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if i := strings.IndexByte(line, ';'); i >= 0 && !strings.Contains(line[:i], "\\") {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#include") || strings.HasPrefix(line, "#exec") {
			continue
		}
		if m := sectionRe.FindStringSubmatch(line); m != nil {
			cur = &section{name: strings.TrimSpace(m[1]), values: map[string][]string{}, lineOrder: n}
			for _, opt := range strings.Split(m[2], ",") {
				opt = strings.TrimSpace(opt)
				switch {
				case opt == "!":
					cur.template = true
				case opt != "" && opt != "+":
					cur.inherits = append(cur.inherits, opt)
				}
			}
			for _, parent := range cur.inherits {
				if p := byName[parent]; p != nil {
					for k, v := range p.values {
						cur.values[k] = append([]string(nil), v...)
					}
				}
			}
			out = append(out, cur)
			if !cur.template {
				byName[cur.name] = cur
			} else {
				byName[cur.name] = cur
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(k), ">")))
		v = strings.TrimSpace(strings.TrimPrefix(v, ">"))
		if cur == nil || cur.name == "general" {
			if k == "register" {
				registers = append(registers, v)
			}
			if cur == nil {
				continue
			}
		}
		cur.values[k] = append(cur.values[k], v)
	}
	return out, registers
}

// callerName reads the name from an Asterisk callerid: "Front <Desk>" <200>, Front Desk <200> or a bare name.
func callerName(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, `"`) {
		if j := strings.IndexByte(v[1:], '"'); j >= 0 {
			return strings.TrimSpace(v[1 : j+1])
		}
	}
	if i := strings.LastIndexByte(v, '<'); i >= 0 && strings.HasSuffix(v, ">") {
		v = v[:i]
	}
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(v), `"`))
}

func parsePJSIP(data []byte) Result {
	secs, _ := parseINI(data)
	res := Result{Format: "Asterisk pjsip.conf"}
	// pjsip.conf reuses a name for several sections ([200] is an endpoint and an aor), so look up by type.
	byType := map[string]map[string]*section{}
	for _, s := range secs {
		if !s.template {
			typ := s.get("type")
			if byType[typ] == nil {
				byType[typ] = map[string]*section{}
			}
			byType[typ][s.name] = s
		}
	}
	find := func(typ, name string) *section { return byType[typ][name] }
	regFor := map[string]*section{} // endpoint name -> registration
	for _, s := range secs {
		if !s.template && s.get("type") == "registration" {
			ep := s.get("endpoint")
			if ep == "" {
				ep = s.name
			}
			regFor[ep] = s
		}
	}
	for _, s := range secs {
		if s.template || s.get("type") != "endpoint" {
			continue
		}
		var auth *section
		if a := s.get("auth"); a != "" {
			auth = find("auth", a)
		}
		if a := s.get("outbound_auth"); auth == nil && a != "" {
			auth = find("auth", a)
		}
		if reg, isTrunk := regFor[s.name]; isTrunk || !numberRe.MatchString(s.name) {
			t := config.Trunk{Name: s.name, Enabled: true, Transport: "udp"}
			if reg != nil {
				t.Register = true
				if host, port, ok := hostOf(reg.get("server_uri")); ok {
					t.Host, t.Port = host, port
				}
				if a := find("auth", reg.get("outbound_auth")); a != nil {
					auth = a
				}
				if u, _, ok := hostOf(reg.get("client_uri")); ok && auth == nil {
					t.Username = u
				}
			}
			if t.Host == "" {
				if aor := find("aor", strings.Split(s.get("aors"), ",")[0]); aor != nil {
					if host, port, ok := hostOf(aor.get("contact")); ok {
						t.Host, t.Port = host, port
					}
				}
			}
			if auth != nil {
				t.Username, t.Password = auth.get("username"), auth.get("password")
			}
			if strings.Contains(strings.ToLower(s.get("transport")), "tcp") {
				t.Transport = "tcp"
			} else if strings.Contains(strings.ToLower(s.get("transport")), "tls") {
				t.Transport = "tls"
			}
			if t.Host == "" {
				res.Warnings = append(res.Warnings, fmt.Sprintf("endpoint %s looks like a trunk but has no server address; skipped", s.name))
				continue
			}
			res.Trunks = append(res.Trunks, t)
			continue
		}
		x := config.Extension{Number: s.name, Name: callerName(s.get("callerid")), Enabled: true, Outbound: true}
		if x.Name == "" {
			x.Name = "Extension " + s.name
		}
		if auth != nil {
			x.Secret = auth.get("password")
		}
		if mb := s.get("mailboxes"); mb != "" {
			x.Voicemail.Enabled = true
			x.NoAnswer = &config.Destination{Type: "voicemail", Target: x.Number}
		}
		if len(x.Secret) < 12 {
			if x.Secret != "" {
				res.Warnings = append(res.Warnings, fmt.Sprintf("extension %s: its SIP password is shorter than 12 characters, so BitVoice makes a new one", x.Number))
			}
			x.Secret = ""
		}
		if strings.Contains(s.get("media_encryption"), "sdes") {
			x.SecureMedia = true
		}
		res.Extensions = append(res.Extensions, x)
	}
	// A registration with no endpoint of its name is still a carrier account: bring it over as a trunk.
	endpoints := map[string]bool{}
	for _, s := range secs {
		if !s.template && s.get("type") == "endpoint" {
			endpoints[s.name] = true
		}
	}
	for ep, reg := range regFor {
		if endpoints[ep] {
			continue
		}
		host, port, ok := hostOf(reg.get("server_uri"))
		if !ok {
			res.Warnings = append(res.Warnings, fmt.Sprintf("registration %s has no server address; skipped", reg.name))
			continue
		}
		t := config.Trunk{Name: reg.name, Enabled: true, Transport: "udp", Register: true, Host: host, Port: port}
		if u, _, ok := hostOf(reg.get("client_uri")); ok {
			t.Username = u
		}
		if a := find("auth", reg.get("outbound_auth")); a != nil {
			t.Username, t.Password = a.get("username"), a.get("password")
		}
		res.Trunks = append(res.Trunks, t)
	}
	sortResult(&res)
	return res
}

// hostOf reads user and host and port from sip:user@host:port;params or host:port.
func hostOf(uri string) (string, int, bool) {
	uri = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(uri, "sips:"), "sip:"))
	if uri == "" {
		return "", 0, false
	}
	uri, _, _ = strings.Cut(uri, ";")
	if _, h, ok := strings.Cut(uri, "@"); ok {
		uri = h
	}
	host, port := uri, 5060
	if i := strings.LastIndexByte(uri, ':'); i > 0 && !strings.Contains(uri[i+1:], "]") {
		if p, err := strconv.Atoi(uri[i+1:]); err == nil {
			host, port = uri[:i], p
		}
	}
	return strings.Trim(host, "[]"), port, host != ""
}

func parseSIPConf(data []byte) Result {
	secs, registers := parseINI(data)
	res := Result{Format: "Asterisk sip.conf"}
	for _, s := range secs {
		if s.template || s.name == "general" || s.name == "authentication" {
			continue
		}
		typ := s.get("type")
		if typ != "friend" && typ != "peer" && typ != "user" {
			continue
		}
		if numberRe.MatchString(s.name) && (s.get("host") == "dynamic" || s.get("host") == "") {
			x := config.Extension{Number: s.name, Name: callerName(s.get("callerid")), Secret: s.get("secret"), Enabled: true, Outbound: true}
			if x.Name == "" {
				x.Name = "Extension " + s.name
			}
			if s.get("mailbox") != "" {
				x.Voicemail.Enabled = true
				x.NoAnswer = &config.Destination{Type: "voicemail", Target: x.Number}
			}
			if len(x.Secret) < 12 {
				if x.Secret != "" {
					res.Warnings = append(res.Warnings, fmt.Sprintf("extension %s: its SIP password is shorter than 12 characters, so BitVoice makes a new one", x.Number))
				}
				x.Secret = ""
			}
			res.Extensions = append(res.Extensions, x)
			continue
		}
		if h := s.get("host"); h != "" && h != "dynamic" {
			port := 5060
			if p, err := strconv.Atoi(s.get("port")); err == nil {
				port = p
			}
			user := s.get("defaultuser")
			if user == "" {
				user = s.get("username")
			}
			res.Trunks = append(res.Trunks, config.Trunk{Name: s.name, Enabled: true, Host: h, Port: port, Transport: "udp",
				Username: user, Password: s.get("secret"), FromDomain: s.get("fromdomain")})
		}
	}
	// register => user[:secret[:authuser]]@host[:port][/extension]
	for _, r := range registers {
		r, _, _ = strings.Cut(r, "/")
		cred, hostport, ok := strings.Cut(r, "@")
		if !ok {
			continue
		}
		parts := strings.Split(cred, ":")
		host, port, _ := hostOf(hostport)
		matched := false
		for i := range res.Trunks {
			if res.Trunks[i].Host == host {
				res.Trunks[i].Register = true
				matched = true
			}
		}
		if !matched {
			t := config.Trunk{Name: host, Enabled: true, Host: host, Port: port, Transport: "udp", Register: true, Username: parts[0]}
			if len(parts) > 1 {
				t.Password = parts[1]
			}
			if len(parts) > 2 {
				t.AuthUser = parts[2]
			}
			res.Trunks = append(res.Trunks, t)
		}
	}
	sortResult(&res)
	return res
}

func sortResult(r *Result) {
	sort.SliceStable(r.Extensions, func(i, j int) bool { return r.Extensions[i].Number < r.Extensions[j].Number })
}

// parse3CX reads 3CX's users export (Users > Export, v18 and v20): Number, FirstName, LastName,
// EmailAddress, OutboundCallerID, AuthPassword, and the voicemail columns where present.
func parse3CX(data []byte) (Result, error) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	head, err := r.Read()
	if err != nil {
		return Result{}, fmt.Errorf("the CSV has no header row: %w", err)
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	get := func(row []string, names ...string) string {
		for _, n := range names {
			if i, ok := col[n]; ok && i < len(row) {
				if v := strings.TrimSpace(row[i]); v != "" {
					return v
				}
			}
		}
		return ""
	}
	yes := func(v string) bool {
		v = strings.ToLower(v)
		return v == "1" || v == "true" || v == "yes" || v == "y"
	}
	res := Result{Format: "3CX users CSV"}
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
		num := get(row, "number", "extension")
		if !numberRe.MatchString(num) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("line %d: %q is not an extension number BitVoice uses (2 to 8 digits)", line, num))
			continue
		}
		name := strings.TrimSpace(get(row, "firstname") + " " + get(row, "lastname"))
		if name == "" {
			name = "Extension " + num
		}
		x := config.Extension{Number: num, Name: name, Enabled: !yes(get(row, "disabled")), Outbound: true,
			Email: get(row, "emailaddress", "email"), CallerID: digits(get(row, "outboundcallerid")),
			Secret: get(row, "authpassword", "sippassword", "authpass")}
		vm := get(row, "vmenable", "voicemailenabled", "vmenabled")
		x.Voicemail.Enabled = vm == "" || yes(vm) // 3CX has voicemail on unless switched off
		if pin := get(row, "vmpin", "voicemailpin"); regexp.MustCompile(`^[0-9]{4,10}$`).MatchString(pin) {
			x.Voicemail.PIN = pin
		}
		x.Voicemail.Email = yes(get(row, "vmemailoptions", "vmemail")) && x.Email != ""
		if x.Voicemail.Enabled {
			x.NoAnswer = &config.Destination{Type: "voicemail", Target: num}
		}
		if len(x.Secret) < 12 {
			if x.Secret != "" {
				res.Warnings = append(res.Warnings, fmt.Sprintf("extension %s: its SIP password is shorter than 12 characters, so BitVoice makes a new one; re-enter it in the phone", num))
			}
			x.Secret = ""
		}
		if get(row, "mobilenumber") != "" {
			res.Warnings = append(res.Warnings, fmt.Sprintf("extension %s: has a mobile number in 3CX; add it to the extension's forwarding by hand", num))
		}
		res.Extensions = append(res.Extensions, x)
	}
	if len(res.Extensions) > 0 {
		res.Warnings = append(res.Warnings, "3CX's users export holds extensions only: set up trunks, ring groups and queues again in BitVoice")
	}
	return res, nil
}
