package importer

// FreePBX full backups (Admin > Backup & Restore), as FreePBX 15, 16 and 17 write them: a .tar.gz with
// modulejson/<Module>.json per module (the module's configs, rows as its tables hold them) and
// files/... (mysqldump files a module asked for, and copies of files such as voicemail.conf). Older
// FreePBX (13 and before) wrote "legacy" backups: a .tgz with a manifest and mysqldump files of the
// whole asterisk database. A plain mysqldump of the asterisk database is read the same way.
//
// What is read, by FreePBX table: users (extensions), devices and sip (each device's SIP password),
// voicemail.conf (PINs and email), trunks with pjsip or sip (carrier accounts), outbound_routes with
// their patterns and trunks, incoming (DIDs), ringgroups, queues_config and queues_details.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/config"
)

type fpbxRow map[string]string

// fpbxBackup is what a FreePBX backup holds, as tables of rows.
type fpbxBackup struct {
	kind      string // "FreePBX backup", "FreePBX legacy backup", "FreePBX database dump"
	version   string
	tables    map[string][]fpbxRow
	routes    []fpbxRoute
	voicemail []byte // voicemail.conf
}

type fpbxRoute struct {
	row      fpbxRow
	patterns []fpbxRow
	trunks   []string // trunk ids in order
}

// isFreePBXBackup says whether a file is a FreePBX backup or a dump of its database.
func isFreePBXBackup(f File) bool {
	data := f.Data
	if bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return false
		}
		head := make([]byte, 64<<10)
		n, _ := io.ReadFull(zr, head)
		data = head[:n]
	}
	if len(data) > 262 && string(data[257:262]) == "ustar" {
		// The first entries of a tar are enough: FreePBX writes modulejson (or the manifest) first.
		tr := tar.NewReader(bytes.NewReader(data))
		for i := 0; i < 20; i++ {
			h, err := tr.Next()
			if err != nil {
				break
			}
			n := strings.TrimPrefix(path.Clean("/"+h.Name), "/")
			if n == "modulejson" || strings.HasPrefix(n, "modulejson/") || n == "manifest" || strings.HasSuffix(n, ".sql") || strings.HasSuffix(n, ".sql.gz") {
				return true
			}
		}
		return false
	}
	return isAsteriskDump(data)
}

func isAsteriskDump(data []byte) bool {
	head := data
	if len(head) > 1<<20 {
		head = head[:1<<20]
	}
	return bytes.Contains(head, []byte("`users`")) && (bytes.Contains(head, []byte("CREATE TABLE")) || bytes.Contains(head, []byte("INSERT INTO")))
}

// readFreePBX opens a backup (or dump) into its tables.
func readFreePBX(f File) (*fpbxBackup, error) {
	data := f.Data
	if bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		var err error
		if data, err = gunzip(data); err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	b := &fpbxBackup{tables: map[string][]fpbxRow{}}
	if !(len(data) > 262 && string(data[257:262]) == "ustar") {
		b.kind = "FreePBX database dump"
		if err := b.addDump(data); err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		return b, nil
	}
	tr := tar.NewReader(bytes.NewReader(data))
	total := 0
	var modules = map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: not a backup BitVoice can open: %w", f.Name, err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		n := strings.TrimPrefix(path.Clean("/"+h.Name), "/")
		want := strings.HasPrefix(n, "modulejson/") && strings.HasSuffix(n, ".json") || n == "manifest" || n == "metadata.json" ||
			strings.HasSuffix(n, ".sql") || strings.HasSuffix(n, ".sql.gz") || strings.HasSuffix(n, "etc/asterisk/voicemail.conf") || n == "voicemail.conf"
		if !want {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(tr, maxUnpacked))
		if err != nil {
			return nil, err
		}
		if total += len(body); total > maxUnpacked {
			return nil, errors.New("the backup is too large once opened")
		}
		switch {
		case strings.HasPrefix(n, "modulejson/"):
			modules[strings.ToLower(strings.TrimSuffix(path.Base(n), ".json"))] = body
		case n == "manifest" || n == "metadata.json":
			var m map[string]any
			if json.Unmarshal(body, &m) == nil {
				for _, k := range []string{"pbx_version", "version", "frameworkversion"} {
					if v, ok := m[k].(string); ok && b.version == "" {
						b.version = v
					}
				}
			}
			if n == "manifest" && b.kind == "" {
				b.kind = "FreePBX legacy backup"
			}
		case strings.HasSuffix(n, "voicemail.conf"):
			b.voicemail = body
		default:
			if strings.HasSuffix(n, ".gz") {
				if body, err = gunzip(body); err != nil {
					return nil, fmt.Errorf("%s: %w", n, err)
				}
			}
			if base := strings.ToLower(path.Base(n)); strings.Contains(base, "cdr") || strings.Contains(base, "cel") || strings.Contains(base, "queuelog") {
				continue // call history: not configuration
			}
			if err := b.addDump(body); err != nil {
				return nil, fmt.Errorf("%s: %w", n, err)
			}
		}
	}
	if len(modules) > 0 {
		b.kind = "FreePBX backup"
		if err := b.addModules(modules); err != nil {
			return nil, err
		}
	}
	if b.kind == "" {
		b.kind = "FreePBX legacy backup"
	}
	if len(b.routes) == 0 {
		b.routesFromTables()
	}
	return b, nil
}

func gunzip(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("not a gzip file")
	}
	out, err := io.ReadAll(io.LimitReader(zr, maxUnpacked+1))
	if err != nil {
		return nil, err
	}
	if len(out) > maxUnpacked {
		return nil, errors.New("the backup is too large once opened")
	}
	return out, nil
}

// addModules reads modulejson files: each is {"configs": {...}, ...}.
func (b *fpbxBackup) addModules(mods map[string][]byte) error {
	configs := func(name string) map[string]any {
		var m struct {
			Configs map[string]any `json:"configs"`
		}
		if body, ok := mods[name]; ok {
			d := json.NewDecoder(bytes.NewReader(body))
			d.UseNumber()
			_ = d.Decode(&m)
		}
		return m.Configs
	}
	if core := configs("core"); core != nil {
		if u, ok := core["Users"].(map[string]any); ok {
			b.setRows("users", u["users"])
		}
		if t, ok := core["Trunks"].(map[string]any); ok {
			b.setRows("trunks", t["trunks"])
			b.setRows("trunk_dialpatterns", t["dialpatterns"])
			if tech, ok := t["techTables"].(map[string]any); ok {
				b.setRows("pjsip", tech["pjsip"])
			}
		}
		b.setRows("incoming", core["Did"])
		if rs, ok := core["Routing"].([]any); ok {
			for _, r := range rs {
				m, ok := r.(map[string]any)
				if !ok {
					continue
				}
				rt := fpbxRoute{row: rowOf(m), patterns: rowsOf(m["patterns"])}
				switch ts := m["trunks"].(type) {
				case []any:
					for _, t := range ts {
						rt.trunks = append(rt.trunks, scalar(t))
					}
				case map[string]any: // PHP arrays with gaps come out as objects
					keys := make([]string, 0, len(ts))
					for k := range ts {
						keys = append(keys, k)
					}
					sort.Slice(keys, func(i, j int) bool { a, _ := strconv.Atoi(keys[i]); c, _ := strconv.Atoi(keys[j]); return a < c })
					for _, k := range keys {
						rt.trunks = append(rt.trunks, scalar(ts[k]))
					}
				}
				b.routes = append(b.routes, rt)
			}
		}
	}
	if rg := configs("ringgroups"); rg != nil {
		b.setRows("ringgroups", rg["groups"])
	}
	if q := configs("queues"); q != nil {
		if ts, ok := q["tables"].(map[string]any); ok {
			b.setRows("queues_config", ts["queues_config"])
			b.setRows("queues_details", ts["queues_details"])
		}
	}
	return nil
}

func (b *fpbxBackup) setRows(table string, v any) {
	if rows := rowsOf(v); len(rows) > 0 {
		b.tables[table] = rows
	}
}

func rowsOf(v any) []fpbxRow {
	var out []fpbxRow
	switch l := v.(type) {
	case []any:
		for _, r := range l {
			if m, ok := r.(map[string]any); ok {
				out = append(out, rowOf(m))
			}
		}
	case map[string]any: // keyed rows (getAllDIDs keys by number in some versions)
		keys := make([]string, 0, len(l))
		for k := range l {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if m, ok := l[k].(map[string]any); ok {
				out = append(out, rowOf(m))
			}
		}
	}
	return out
}

func rowOf(m map[string]any) fpbxRow {
	r := fpbxRow{}
	for k, v := range m {
		r[strings.ToLower(k)] = scalar(v)
	}
	return r
}

func scalar(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "1"
		}
		return "0"
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

// routesFromTables builds outbound routes from the database tables (legacy backups and dumps).
func (b *fpbxBackup) routesFromTables() {
	pats := map[string][]fpbxRow{}
	for _, p := range b.tables["outbound_route_patterns"] {
		pats[p["route_id"]] = append(pats[p["route_id"]], p)
	}
	type seqTrunk struct {
		seq   int
		trunk string
	}
	trunks := map[string][]seqTrunk{}
	for _, t := range b.tables["outbound_route_trunks"] {
		n, _ := strconv.Atoi(t["seq"])
		trunks[t["route_id"]] = append(trunks[t["route_id"]], seqTrunk{n, t["trunk_id"]})
	}
	seq := map[string]int{}
	for _, s := range b.tables["outbound_route_sequence"] {
		seq[s["route_id"]], _ = strconv.Atoi(s["seq"])
	}
	for _, r := range b.tables["outbound_routes"] {
		id := r["route_id"]
		ts := trunks[id]
		sort.SliceStable(ts, func(i, j int) bool { return ts[i].seq < ts[j].seq })
		rt := fpbxRoute{row: r, patterns: pats[id]}
		for _, t := range ts {
			rt.trunks = append(rt.trunks, t.trunk)
		}
		b.routes = append(b.routes, rt)
	}
	sort.SliceStable(b.routes, func(i, j int) bool { return seq[b.routes[i].row["route_id"]] < seq[b.routes[j].row["route_id"]] })
}

// parseFreePBX turns a FreePBX backup into what BitVoice imports.
func parseFreePBX(f File) (Result, error) {
	b, err := readFreePBX(f)
	if err != nil {
		return Result{}, err
	}
	res := Result{Format: b.kind}
	if b.version != "" {
		res.Format += " (FreePBX " + b.version + ")"
	}
	warn := func(format string, a ...any) { res.Warnings = append(res.Warnings, fmt.Sprintf(format, a...)) }

	// Each device's settings: sip rows are (id, keyword, data).
	sip := map[string]map[string]string{}
	for _, r := range b.tables["sip"] {
		if sip[r["id"]] == nil {
			sip[r["id"]] = map[string]string{}
		}
		sip[r["id"]][strings.ToLower(r["keyword"])] = r["data"]
	}
	devices := map[string]fpbxRow{}
	for _, d := range b.tables["devices"] {
		devices[d["id"]] = d
	}
	boxes := voicemailBoxes(b.voicemail)

	// Extensions: FreePBX "users" (the person and number) with their device's SIP password.
	users := b.tables["users"]
	if len(users) == 0 {
		for _, d := range b.tables["devices"] {
			users = append(users, fpbxRow{"extension": d["id"], "name": d["description"], "voicemail": "novm"})
		}
	}
	for _, u := range users {
		num := u["extension"]
		if !numberRe.MatchString(num) {
			warn("extension %q is not a number BitVoice uses (2 to 8 digits); left out", num)
			continue
		}
		x := config.Extension{Number: num, Name: strings.TrimSpace(u["name"]), Enabled: true, Outbound: true,
			CallerID: digits(u["outboundcid"])}
		if x.Name == "" {
			x.Name = "Extension " + num
		}
		if d, ok := devices[num]; ok {
			switch t := strings.ToLower(d["tech"]); t {
			case "pjsip", "sip", "":
			default:
				warn("extension %s is a %s device; BitVoice brings it over as a SIP extension", num, strings.ToUpper(t))
			}
		}
		x.Secret = sip[num]["secret"]
		if n, err := strconv.Atoi(u["ringtimer"]); err == nil && n > 0 {
			x.RingSeconds = n
		}
		if box, ok := boxes[num]; ok && strings.ToLower(u["voicemail"]) != "novm" {
			x.Voicemail.Enabled = true
			if regexp.MustCompile(`^[0-9]{4,10}$`).MatchString(box.pin) {
				x.Voicemail.PIN = box.pin
			}
			x.Email = box.email
			x.Voicemail.Email = box.email != "" && box.attach
			x.NoAnswer = &config.Destination{Type: "voicemail", Target: num}
		} else if v := strings.ToLower(u["voicemail"]); v != "" && v != "novm" && b.voicemail == nil {
			x.Voicemail.Enabled = true // the backup left voicemail.conf out: the box still exists
			x.NoAnswer = &config.Destination{Type: "voicemail", Target: num}
		}
		if len(x.Secret) < 12 {
			if x.Secret != "" {
				warn("extension %s: its SIP password is shorter than 12 characters, so BitVoice makes a new one; re-enter it in the phone", num)
			}
			x.Secret = ""
		}
		if strings.EqualFold(sip[num]["media_encryption"], "sdes") {
			x.SecureMedia = true
		}
		res.Extensions = append(res.Extensions, x)
	}

	// Trunks: the trunks table names them; pjsip (or sip peer/register rows) holds the account.
	pj := map[string]map[string]string{}
	for _, r := range b.tables["pjsip"] {
		if pj[r["id"]] == nil {
			pj[r["id"]] = map[string]string{}
		}
		pj[r["id"]][strings.ToLower(r["keyword"])] = r["data"]
	}
	trunkName := map[string]string{}
	for _, tr := range b.tables["trunks"] {
		id, name := tr["trunkid"], strings.TrimSpace(tr["name"])
		if name == "" {
			name = tr["channelid"]
		}
		trunkName[id] = name
		t := config.Trunk{Name: name, Enabled: strings.ToLower(tr["disabled"]) != "on", Transport: "udp", CallerID: digits(tr["outcid"])}
		if n, err := strconv.Atoi(tr["maxchans"]); err == nil && n > 0 {
			t.MaxChannels = n
		}
		switch tech := strings.ToLower(tr["tech"]); tech {
		case "pjsip":
			p := pj[id]
			t.Host = p["sip_server"]
			t.Port, _ = strconv.Atoi(p["sip_server_port"])
			t.Username, t.Password = p["username"], p["secret"]
			if a := p["auth_username"]; a != "" && a != t.Username {
				t.AuthUser = a
			}
			t.Register = strings.ToLower(p["registration"]) == "send"
			t.FromDomain = p["from_domain"]
			if strings.Contains(strings.ToLower(p["transport"]), "tcp") {
				t.Transport = "tcp"
			} else if strings.Contains(strings.ToLower(p["transport"]), "tls") {
				t.Transport = "tls"
			}
			t.Codecs = codecsOf(p["codecs"])
		case "sip":
			p := sip["tr-peer-"+id]
			t.Host = p["host"]
			t.Port, _ = strconv.Atoi(p["port"])
			t.Username, t.Password = firstOf(p["defaultuser"], p["username"]), p["secret"]
			t.FromDomain = p["fromdomain"]
			if strings.EqualFold(p["transport"], "tcp") {
				t.Transport = "tcp"
			}
			if reg := sip["tr-reg-"+id]["register"]; reg != "" {
				t.Register = true
			}
			t.Codecs = codecsOf(p["allow"])
		case "iax2", "iax":
			t.Protocol = "iax2"
			p := sip["tr-peer-"+id]
			if len(p) == 0 {
				warn("trunk %s is IAX2; enter its address and account by hand", name)
			}
			t.Host = p["host"]
		case "dahdi":
			t.Protocol = "dahdi"
			warn("trunk %s is a DAHDI trunk: it comes over as the Telephony cards trunk; set up the card in Telephony cards", name)
		default:
			warn("trunk %s is a %s trunk, which BitVoice does not bring over", name, tech)
			continue
		}
		if t.Host == "" && t.Protocol != "dahdi" {
			warn("trunk %s has no server address; brought over switched off", name)
			t.Enabled = false
		}
		res.Trunks = append(res.Trunks, t)
	}
	if len(b.tables["trunk_dialpatterns"]) > 0 {
		warn("trunk dial-pattern rules are not brought over; BitVoice's outbound routes strip and add digits instead")
	}

	// Outbound routes: one BitVoice route per (strip, prepend) the patterns of a FreePBX route use.
	for _, rt := range b.routes {
		name := strings.TrimSpace(rt.row["name"])
		var trunks []string
		for _, id := range rt.trunks {
			if n, ok := trunkName[id]; ok {
				trunks = append(trunks, n)
			}
		}
		if len(trunks) == 0 {
			warn("route %s uses no trunk this backup holds; left out", name)
			continue
		}
		if rt.row["password"] != "" {
			warn("route %s asks for a PIN; set a PIN set on it in BitVoice", name)
		}
		type key struct {
			strip   int
			prepend string
		}
		var order []key
		groups := map[key][]string{}
		for _, p := range rt.patterns {
			if p["match_cid"] != "" {
				warn("route %s: pattern %s%s also matches caller ID %s; BitVoice routes by the number dialled only", name, p["match_pattern_prefix"], p["match_pattern_pass"], p["match_cid"])
			}
			pat := p["match_pattern_prefix"] + p["match_pattern_pass"]
			if pat == "" {
				continue
			}
			k := key{patternLen(p["match_pattern_prefix"]), p["prepend_digits"]}
			if _, ok := groups[k]; !ok {
				order = append(order, k)
			}
			groups[k] = append(groups[k], pat)
		}
		if len(order) == 0 {
			warn("route %s has no dial patterns; left out", name)
			continue
		}
		for i, k := range order {
			n := name
			if len(order) > 1 {
				n = fmt.Sprintf("%s (%d)", name, i+1)
			}
			res.OutboundRoutes = append(res.OutboundRoutes, config.OutboundRoute{Name: n, Patterns: groups[k], Strip: k.strip, Prepend: k.prepend, Trunks: trunks})
		}
	}

	// Ring groups.
	for _, g := range b.tables["ringgroups"] {
		num := g["grpnum"]
		if !numberRe.MatchString(num) {
			warn("ring group %q is not a number BitVoice uses; left out", num)
			continue
		}
		rg := config.RingGroup{Number: num, Name: strings.TrimSpace(g["description"]), Strategy: "all"}
		if rg.Name == "" {
			rg.Name = "Group " + num
		}
		switch s := strings.ToLower(g["strategy"]); s {
		case "ringall", "ringall-prim", "":
		case "hunt", "hunt-prim", "memoryhunt", "memoryhunt-prim", "firstavailable", "firstnotonhold":
			rg.Strategy = "sequential"
		default:
			warn("ring group %s rings %s; BitVoice rings everyone at once", num, s)
		}
		rg.RingSeconds, _ = strconv.Atoi(g["grptime"])
		for _, m := range strings.Split(g["grplist"], "-") {
			m = strings.TrimSpace(m)
			if m == "" {
				continue
			}
			if strings.HasSuffix(m, "#") {
				warn("ring group %s also rings outside number %s; add it in BitVoice as a Follow me or a trunk call", num, strings.TrimSuffix(m, "#"))
				continue
			}
			rg.Members = append(rg.Members, m)
		}
		if d, ok := fpbxDestination(g["postdest"]); ok && d.Type != "hangup" {
			rg.NoAnswer = &d
		}
		res.RingGroups = append(res.RingGroups, rg)
	}

	// Queues: queues_config names each, queues_details (id, keyword, data) holds members and settings.
	details := map[string][]fpbxRow{}
	for _, d := range b.tables["queues_details"] {
		details[d["id"]] = append(details[d["id"]], d)
	}
	for _, qc := range b.tables["queues_config"] {
		num := qc["extension"]
		if !numberRe.MatchString(num) {
			warn("queue %q is not a number BitVoice uses; left out", num)
			continue
		}
		q := config.Queue{Number: num, Name: strings.TrimSpace(qc["descr"]), Strategy: "longest-idle", Penalties: map[string]int{}}
		if q.Name == "" {
			q.Name = "Queue " + num
		}
		q.MaxWaitSeconds, _ = strconv.Atoi(qc["maxwait"])
		for _, d := range details[num] {
			v := d["data"]
			switch strings.ToLower(d["keyword"]) {
			case "member":
				// Local/101@from-queue/n,0 (the number, then the penalty); also SIP/101 or PJSIP/101.
				m, pen, _ := strings.Cut(v, ",")
				if i := strings.Index(m, "/"); i >= 0 {
					m = m[i+1:]
				}
				m, _, _ = strings.Cut(m, "@")
				m, _, _ = strings.Cut(m, "/")
				if !numberRe.MatchString(m) {
					warn("queue %s: member %s is not an extension; left out", num, v)
					continue
				}
				q.Agents = append(q.Agents, m)
				if p, err := strconv.Atoi(strings.TrimSpace(pen)); err == nil && p > 0 {
					q.Penalties[m] = min(p, 100)
				}
			case "strategy":
				switch strings.ToLower(v) {
				case "ringall":
					q.Strategy = "ring-all"
				case "leastrecent":
					q.Strategy = "longest-idle"
				case "fewestcalls":
					q.Strategy = "fewest-calls"
				case "random":
					q.Strategy = "random"
				case "wrandom":
					q.Strategy = "weighted-random"
				case "rrmemory", "rrordered", "rrpoison":
					q.Strategy = "round-robin"
				case "linear":
					q.Strategy = "linear"
				}
			case "timeout":
				q.RingSeconds, _ = strconv.Atoi(v)
			case "wrapuptime":
				q.WrapUpSeconds, _ = strconv.Atoi(v)
			case "weight":
				q.Weight, _ = strconv.Atoi(v)
			}
		}
		if len(q.Penalties) == 0 {
			q.Penalties = nil
		}
		// After the longest wait a BitVoice queue sends callers to voicemail or says goodbye.
		if d, ok := fpbxDestination(qc["dest"]); ok && (d.Type == "voicemail" || d.Type == "hangup") {
			q.Timeout = &d
		} else if qc["dest"] != "" {
			warn("queue %s: after the longest wait FreePBX sends callers to %s; a BitVoice queue sends them to a voicemail box or says goodbye, so choose one", num, qc["dest"])
		}
		res.Queues = append(res.Queues, q)
	}

	// Incoming routes (DIDs).
	for _, in := range b.tables["incoming"] {
		did := digits(in["extension"])
		if in["extension"] != "" && did == "" {
			warn("incoming route %q is a pattern; BitVoice matches whole numbers, so add it by hand", in["extension"])
			continue
		}
		if in["cidnum"] != "" {
			warn("incoming route %s also matches caller ID %s; BitVoice routes by the number called only", firstOf(did, "(any)"), in["cidnum"])
		}
		d, ok := fpbxDestination(in["destination"])
		if !ok {
			warn("incoming route %s goes to %s, which BitVoice does not bring over (IVRs, time conditions and the like): set its destination by hand", firstOf(did, "(any)"), in["destination"])
			continue
		}
		name := strings.TrimSpace(in["description"])
		if name == "" {
			name = firstOf(did, "Any number")
		}
		res.InboundRoutes = append(res.InboundRoutes, config.InboundRoute{Name: name, DID: did, Destination: d})
	}
	fill(&res)
	sortResult(&res)
	if len(res.Extensions)+len(res.Trunks)+len(res.RingGroups)+len(res.Queues)+len(res.InboundRoutes)+len(res.OutboundRoutes) == 0 {
		return res, errors.New("this FreePBX backup holds no extensions, trunks, groups, queues or routes BitVoice could read")
	}
	return res, nil
}

// fpbxDestination reads a FreePBX destination (context,exten,priority).
func fpbxDestination(s string) (config.Destination, bool) {
	parts := strings.Split(strings.TrimSpace(s), ",")
	if len(parts) < 2 {
		return config.Destination{}, false
	}
	ctx, ext := parts[0], parts[1]
	switch {
	case ctx == "from-did-direct" || ctx == "ext-local" && numberRe.MatchString(ext) || ctx == "from-internal" && numberRe.MatchString(ext):
		return config.Destination{Type: "extension", Target: ext}, true
	case ctx == "ext-local" && (strings.HasPrefix(ext, "vmu") || strings.HasPrefix(ext, "vmb") || strings.HasPrefix(ext, "vms") || strings.HasPrefix(ext, "vmi")):
		return config.Destination{Type: "voicemail", Target: ext[3:]}, true
	case ctx == "ext-group":
		return config.Destination{Type: "ringgroup", Target: ext}, true
	case ctx == "ext-queues":
		return config.Destination{Type: "queue", Target: ext}, true
	case ctx == "app-blackhole":
		return config.Destination{Type: "hangup"}, true
	}
	return config.Destination{}, false
}

type vmBox struct {
	pin, email string
	attach     bool
}

// voicemailBoxes reads voicemail.conf: "100 => 1234,Full Name,email,pager,attach=yes|saycid=no".
func voicemailBoxes(conf []byte) map[string]vmBox {
	out := map[string]vmBox{}
	attachAll := false
	ctx := ""
	for _, line := range strings.Split(string(conf), "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, ";"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			ctx = strings.Trim(line, "[]")
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(strings.TrimPrefix(v, ">"))
		if ctx == "general" {
			if strings.EqualFold(k, "attach") {
				attachAll = strings.EqualFold(v, "yes")
			}
			continue
		}
		if ctx == "zonemessages" || !numberRe.MatchString(k) {
			continue
		}
		f := strings.Split(v, ",")
		box := vmBox{pin: strings.TrimSpace(f[0]), attach: attachAll}
		if len(f) > 2 {
			box.email = strings.TrimSpace(f[2])
		}
		if len(f) > 4 {
			for _, o := range strings.Split(f[4], "|") {
				ok, ov, _ := strings.Cut(o, "=")
				if strings.EqualFold(strings.TrimSpace(ok), "attach") {
					box.attach = strings.EqualFold(strings.TrimSpace(ov), "yes")
				}
			}
		}
		out[k] = box
	}
	return out
}

// patternLen counts the digits a dial-pattern prefix stands for ([1-3] is one digit).
func patternLen(p string) int {
	n := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '[' {
			if j := strings.IndexByte(p[i:], ']'); j >= 0 {
				i += j
			}
		}
		n++
	}
	return n
}

func codecsOf(s string) []string {
	var out []string
	for _, c := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ',' || r == '&' || r == ' ' }) {
		switch c {
		case "ulaw":
			out = append(out, "PCMU")
		case "alaw":
			out = append(out, "PCMA")
		case "g722":
			out = append(out, "G722")
		case "opus":
			out = append(out, "opus")
		case "g729":
			out = append(out, "G729")
		}
	}
	return out
}

func firstOf(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// addDump reads a mysqldump: CREATE TABLE gives each table's columns, INSERT INTO its rows.
func (b *fpbxBackup) addDump(data []byte) error {
	cols := map[string][]string{}
	s := string(data)
	found := false
	for i := 0; i < len(s); {
		j := strings.IndexAny(s[i:], "CI")
		if j < 0 {
			break
		}
		i += j
		switch {
		case strings.HasPrefix(s[i:], "CREATE TABLE "):
			name, rest, ok := sqlName(s[i+len("CREATE TABLE "):])
			if !ok {
				i++
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(rest), "IF NOT EXISTS") {
				i++
				continue
			}
			end := strings.Index(rest, "\n)")
			if end < 0 {
				end = len(rest)
			}
			var cs []string
			for _, line := range strings.Split(rest[:end], "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "`") {
					if c, _, ok := sqlName(line); ok {
						cs = append(cs, strings.ToLower(c))
					}
				}
			}
			cols[name] = cs
			i += len("CREATE TABLE ")
		case strings.HasPrefix(s[i:], "INSERT INTO "):
			name, rest, ok := sqlName(s[i+len("INSERT INTO "):])
			if !ok {
				i++
				continue
			}
			rest = strings.TrimLeft(rest, " ")
			cs := cols[name]
			if strings.HasPrefix(rest, "(") { // --complete-insert: the columns are named
				end := strings.Index(rest, ")")
				if end < 0 {
					return errors.New("a broken INSERT")
				}
				cs = nil
				for _, c := range strings.Split(rest[1:end], ",") {
					cs = append(cs, strings.ToLower(strings.Trim(strings.TrimSpace(c), "`")))
				}
				rest = strings.TrimLeft(rest[end+1:], " ")
			}
			if !strings.HasPrefix(rest, "VALUES") {
				i++
				continue
			}
			rows, n, err := sqlValues(rest[len("VALUES"):])
			if err != nil {
				return fmt.Errorf("table %s: %w", name, err)
			}
			for _, vals := range rows {
				if cs == nil {
					break // rows of a table whose columns are not known: not one BitVoice reads
				}
				r := fpbxRow{}
				for k, v := range vals {
					if k < len(cs) {
						r[cs[k]] = v
					}
				}
				b.tables[name] = append(b.tables[name], r)
			}
			found = true
			i = len(s) - len(rest) + len("VALUES") + n
		default:
			i++
		}
	}
	if !found && len(cols) == 0 {
		return errors.New("no tables in this SQL file")
	}
	return nil
}

// sqlName reads a `quoted` name, returning it and what follows.
func sqlName(s string) (string, string, bool) {
	if !strings.HasPrefix(s, "`") {
		return "", s, false
	}
	end := strings.IndexByte(s[1:], '`')
	if end < 0 {
		return "", s, false
	}
	return s[1 : 1+end], s[2+end:], true
}

// sqlValues reads (v,v,...),(...); returning the rows and how many bytes it used.
func sqlValues(s string) ([][]string, int, error) {
	var rows [][]string
	i := 0
	for {
		for i < len(s) && (s[i] == ' ' || s[i] == '\n' || s[i] == '\r' || s[i] == '\t' || s[i] == ',') {
			i++
		}
		if i >= len(s) {
			return rows, i, errors.New("the INSERT never ends")
		}
		if s[i] == ';' {
			return rows, i + 1, nil
		}
		if s[i] != '(' {
			return rows, i, fmt.Errorf("unexpected %q in an INSERT", s[i])
		}
		i++
		var row []string
		for {
			for i < len(s) && s[i] == ' ' {
				i++
			}
			if i >= len(s) {
				return rows, i, errors.New("the INSERT never ends")
			}
			if s[i] == '\'' {
				var sb strings.Builder
				i++
				for {
					if i >= len(s) {
						return rows, i, errors.New("a string never ends")
					}
					c := s[i]
					if c == '\\' && i+1 < len(s) {
						i++
						switch s[i] {
						case 'n':
							sb.WriteByte('\n')
						case 'r':
							sb.WriteByte('\r')
						case 't':
							sb.WriteByte('\t')
						case '0':
							sb.WriteByte(0)
						case 'Z':
							sb.WriteByte(26)
						default:
							sb.WriteByte(s[i])
						}
						i++
						continue
					}
					if c == '\'' {
						if i+1 < len(s) && s[i+1] == '\'' {
							sb.WriteByte('\'')
							i += 2
							continue
						}
						i++
						break
					}
					sb.WriteByte(c)
					i++
				}
				row = append(row, sb.String())
			} else {
				j := i
				for j < len(s) && s[j] != ',' && s[j] != ')' {
					j++
				}
				v := strings.TrimSpace(s[i:j])
				if strings.EqualFold(v, "NULL") {
					v = ""
				}
				row = append(row, v)
				i = j
			}
			for i < len(s) && s[i] == ' ' {
				i++
			}
			if i < len(s) && s[i] == ',' {
				i++
				continue
			}
			if i < len(s) && s[i] == ')' {
				i++
				break
			}
			return rows, i, errors.New("a broken row in an INSERT")
		}
		rows = append(rows, row)
	}
}
