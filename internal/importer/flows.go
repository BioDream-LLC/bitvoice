package importer

import (
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

// Call flows from other systems: hunt and ring groups (with their members, ring mode and overflow),
// SIP trunks, incoming routes (a phone number to a group or a person) and outbound dial patterns.
//
// The exports differ by vendor and by release, so files are read by their column names, with the
// names each vendor's own screens and bulk tools use; several files may come together (Cisco's BAT
// export is a .tar of CSV files named after what they hold), and references between them are
// followed: a Cisco hunt pilot names a hunt list, which names line groups, which list the numbers.
// Anything not understood is reported, never guessed.

// table is one CSV file: its name, its columns by key, and its rows.
type table struct {
	name string
	col  map[string]int
	keys []string // in file order
	rows [][]string
}

func readTable(name string, data []byte) (*table, error) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	r.FieldsPerRecord, r.LazyQuotes = -1, true
	head, err := r.Read()
	if err != nil {
		return nil, err
	}
	t := &table{name: name, col: map[string]int{}}
	for i, h := range head {
		k := headerKey(h)
		if _, seen := t.col[k]; !seen && k != "" {
			t.col[k] = i
			t.keys = append(t.keys, k)
		}
	}
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		if strings.TrimSpace(strings.Join(row, "")) != "" {
			t.rows = append(t.rows, row)
		}
	}
	return t, nil
}

func (t *table) has(names ...string) bool {
	for _, n := range names {
		if _, ok := t.col[n]; ok {
			return true
		}
	}
	return false
}

// get is the first non-empty value among these columns in a row.
func (t *table) get(row []string, names ...string) string {
	for _, n := range names {
		if i, ok := t.col[n]; ok && i < len(row) {
			if s := strings.TrimSpace(row[i]); s != "" {
				return s
			}
		}
	}
	return ""
}

// numbered is every value of columns "prefix 1", "prefix 2" ... in order.
func (t *table) numbered(row []string, prefixes ...string) []string {
	type kv struct {
		n int
		v string
	}
	var found []kv
	for _, k := range t.keys {
		for _, p := range prefixes {
			rest, ok := strings.CutPrefix(k, p+" ")
			if !ok {
				continue
			}
			n, err := strconv.Atoi(rest)
			if err != nil {
				continue
			}
			if v := t.get(row, k); v != "" {
				found = append(found, kv{n, v})
			}
		}
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].n < found[j].n })
	var out []string
	for _, f := range found {
		out = append(out, f.v)
	}
	return out
}

// Column names, as the vendors' exports and screens call them (matched without case or spacing).
var (
	colGroupNumber = []string{"hunt pilot", "pilot number", "pilot", "ring group directory number", "hunt group directory number",
		"hunt group number", "group extension", "group number", "extension", "directory number", "number", "pattern"}
	colGroupName = []string{"hunt group name", "ring group name", "group name", "line group name", "name", "alerting name",
		"display name", "description"}
	colStrategy  = []string{"ring mode", "distribution algorithm", "hunt mode", "hunt type", "ring group type", "group type", "ring type", "strategy", "hunting"}
	colRingTime  = []string{"rna reversion timeout", "no answer time", "no answer timeout", "ring time", "ring seconds", "cascade timer", "timeout"}
	colOverflow  = []string{"forward hunt no answer destination", "overflow destination", "overflow", "fallback", "no answer destination", "forward no answer destination", "voicemail", "coverage path"}
	colMembers   = []string{"members", "member list", "user list", "extensions", "group members"}
	colMemberN   = []string{"member", "directory number", "dn", "extension", "user", "line group member"}
	colMemberOne = []string{"member directory number", "member number", "member extension", "member"}

	colTrunkName = []string{"device name", "trunk name", "line name", "name", "description"}
	colTrunkHost = []string{"destination address", "destination address 1", "destination address ipv4 1", "gateway address", "proxy address",
		"itsp proxy address", "itsp domain name", "sip proxy", "host", "ip address", "far end ip address", "registrar"}
	colTrunkPort = []string{"destination port", "destination port 1", "port", "remote port", "far end port"}
	colTrunkUser = []string{"user name", "username", "account", "authentication name", "sip user name"}

	colOutPattern = []string{"route pattern", "dial pattern", "pattern", "code", "dialled digits", "dialed digits", "digits"}
	colOutTrunk   = []string{"gateway or route list", "gateway/route list", "route list", "gateway", "trunk", "line group id", "route", "trunk group"}
	colOutDiscard = []string{"discard digits", "strip", "digits to strip", "delete digits"}
	colOutPrefix  = []string{"prefix digits", "prefix digits outgoing calls", "prepend", "insert digits", "prefix"}

	colInNumber = []string{"incoming number", "did", "did number", "called number", "dialled number", "dialed number", "incoming dnis", "dnis"}
	colInDest   = []string{"destination", "forward destination", "called party", "target", "answering point", "route to"}
)

// kind says what a table holds, from its columns and its file name.
func kindOf(t *table) string {
	n := strings.ToLower(t.name)
	switch {
	case strings.Contains(n, "hunt list") || strings.Contains(n, "huntlist") || t.has("hunt list name") && t.has("line group 1", "line group"):
		return "huntlist"
	case strings.Contains(n, "line group") || strings.Contains(n, "linegroup") || t.has("line group name"):
		return "linegroup"
	case t.has(colInNumber...) && t.has(colInDest...):
		return "inbound"
	case (strings.Contains(n, "route pattern") || strings.Contains(n, "routepattern") || t.has("route pattern", "dial pattern")) && t.has(colOutTrunk...):
		return "outbound"
	case strings.Contains(n, "trunk") || t.has(colTrunkHost...) && t.has("device name", "trunk name", "line name"):
		return "trunk"
	case t.has(colMemberOne...) && t.has("ring group directory number", "hunt group directory number", "hunt group number", "group number", "pilot number"):
		return "members"
	case t.has(colGroupNumber...) && (t.has(colMembers...) || t.has(colStrategy...) || t.has("hunt list")) || strings.Contains(n, "hunt pilot") || strings.Contains(n, "huntpilot"):
		return "group"
	}
	return ""
}

// strategyOf turns a vendor's ring mode into BitVoice's: everyone at once, or one after another.
func strategyOf(mode string) (string, string) {
	m := strings.ToLower(strings.TrimSpace(mode))
	switch {
	case m == "":
		return "all", ""
	case strings.Contains(m, "broadcast") || strings.Contains(m, "collective") || m == "group" || strings.Contains(m, "ring all") ||
		strings.Contains(m, "simultaneous") || strings.Contains(m, "parallel") || m == "all":
		return "all", ""
	case strings.Contains(m, "top down") || strings.Contains(m, "sequential") || m == "hunt" || strings.Contains(m, "linear") ||
		strings.Contains(m, "terminal") || strings.Contains(m, "cascade") || m == "ddc" || m == "sequential":
		return "sequential", ""
	case strings.Contains(m, "circular") || strings.Contains(m, "rotary") || m == "circ":
		return "sequential", "rang in turn (circular); BitVoice's ring groups start at the first member each time: a queue with round robin does turns"
	case strings.Contains(m, "longest") || strings.Contains(m, "ucd") || strings.Contains(m, "ead") || strings.Contains(m, "most idle"):
		return "sequential", "rang the longest idle first; a ring group starts at the first member: a queue with longest idle does this"
	}
	return "all", "ring mode " + mode + " is not one BitVoice knows; set to everyone at once"
}

var splitMembers = regexp.MustCompile(`[;,|\s]+`)

// memberNumber is the extension number in a member entry: Cisco writes "1001/Internal_PT" or
// "1001 in Internal_PT", IP Office writes user names (resolved by the caller), others just numbers.
func memberNumber(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "/@"); i > 0 {
		s = s[:i]
	}
	if i := strings.Index(strings.ToLower(s), " in "); i > 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// ciscoPattern turns a Cisco UCM route pattern into a BitVoice dial pattern and the digits to strip:
// X, [2-9] and digits are the same; ! (one or more digits) is BitVoice's final dot; a dot marks
// where digits before it are discarded (PreDot); @ (the national plan) has no equal.
func ciscoPattern(p, discard string) (pat string, strip int, why string) {
	p = strings.TrimSpace(strings.TrimPrefix(p, "\\"))
	if strings.Contains(p, "@") {
		return "", 0, "uses @ (the national numbering plan): write the patterns it stands for in Call routing"
	}
	if strings.ContainsAny(strings.TrimSuffix(p, "#"), "?*#+") {
		return "", 0, "uses ?, * or + repeats, which BitVoice patterns do not have"
	}
	pre := 0
	if dot := strings.Index(p, "."); dot >= 0 {
		pre = len(regexp.MustCompile(`\[[^\]]*\]|.`).FindAllString(p[:dot], -1))
		p = p[:dot] + p[dot+1:]
	}
	p = strings.TrimSuffix(p, "#")
	if strings.HasSuffix(p, "!") {
		p = strings.TrimSuffix(p, "!") + "."
	}
	if strings.Contains(p, "!") {
		return "", 0, "has ! before its end, which BitVoice patterns do not have"
	}
	if d := strings.ToLower(discard); strings.Contains(d, "predot") || strings.Contains(d, "pre-dot") {
		strip = pre
	}
	return p, strip, ""
}

// parseFlows reads one or more tables into ring groups, trunks and routes.
func parseFlows(tables []*table, res *Result, known map[string]string) {
	lineGroups := map[string]config.RingGroup{} // by name: members and mode
	huntLists := map[string][]string{}          // by name: line group names in order
	var pilots []struct {
		rg   config.RingGroup
		list string
	}
	extra := map[string][]string{} // group number: members from a separate members file
	trunkNames := map[string]bool{}
	warn := func(f string, a ...any) { res.Warnings = append(res.Warnings, fmt.Sprintf(f, a...)) }
	userNumber := func(s string) string {
		s = memberNumber(s)
		if n, ok := known[strings.ToLower(s)]; ok {
			return n
		}
		return digits(s)
	}
	membersOf := func(t *table, row []string) []string {
		var raw []string
		if v := t.get(row, colMembers...); v != "" {
			raw = splitMembers.Split(v, -1)
		}
		raw = append(raw, t.numbered(row, colMemberN...)...)
		var out []string
		for _, m := range raw {
			if n := userNumber(m); numberRe.MatchString(n) {
				out = append(out, n)
			}
		}
		return out
	}
	ringTime := func(t *table, row []string) int {
		n, _ := strconv.Atoi(digits(t.get(row, colRingTime...)))
		if n < 5 || n > 300 {
			return 0
		}
		return n
	}
	for _, t := range tables {
		k := kindOf(t)
		if k == "" {
			warn("%s: not a file BitVoice reads call flows from (its columns: %s)", t.name, strings.Join(first(t.keys, 6), ", "))
			continue
		}
		for _, row := range t.rows {
			switch k {
			case "linegroup":
				name := t.get(row, "line group name", "name")
				strat, why := strategyOf(t.get(row, colStrategy...))
				if why != "" {
					warn("line group %s %s", name, why)
				}
				lg := lineGroups[name]
				lg.Name, lg.Strategy = name, strat
				if s := ringTime(t, row); s > 0 {
					lg.RingSeconds = s
				}
				lg.Members = append(lg.Members, membersOf(t, row)...)
				lineGroups[name] = lg
			case "huntlist":
				name := t.get(row, "hunt list name", "name")
				huntLists[name] = append(huntLists[name], t.numbered(row, "line group")...)
				if v := t.get(row, "line group"); v != "" {
					huntLists[name] = append(huntLists[name], v)
				}
			case "group":
				num := digits(t.get(row, colGroupNumber...))
				if !numberRe.MatchString(num) {
					if raw := t.get(row, colGroupNumber...); raw != "" {
						warn("%s: group %q has no 2 to 8 digit number, left out", t.name, raw)
					}
					continue
				}
				name := t.get(row, colGroupName...)
				if name == "" {
					name = "Group " + num
				}
				strat, why := strategyOf(t.get(row, colStrategy...))
				if why != "" {
					warn("group %s %s", num, why)
				}
				rg := config.RingGroup{Number: num, Name: name, Strategy: strat, Members: membersOf(t, row), RingSeconds: ringTime(t, row)}
				if ov := t.get(row, colOverflow...); ov != "" {
					if n := userNumber(ov); numberRe.MatchString(n) {
						rg.NoAnswer = &config.Destination{Type: "extension", Target: n} // resolved to its kind at apply
					} else {
						warn("group %s overflows to %q, which is not a number here: set its no-answer destination by hand", num, ov)
					}
				}
				pilots = append(pilots, struct {
					rg   config.RingGroup
					list string
				}{rg, t.get(row, "hunt list", "hunt list name")})
			case "members":
				g := digits(t.get(row, "ring group directory number", "hunt group directory number", "hunt group number", "group number", "pilot number"))
				if m := userNumber(t.get(row, colMemberOne...)); numberRe.MatchString(g) && numberRe.MatchString(m) {
					extra[g] = append(extra[g], m)
				}
			case "trunk":
				name := t.get(row, colTrunkName...)
				host := t.get(row, colTrunkHost...)
				if name == "" || host == "" {
					warn("%s: a trunk without a name or an address, left out", t.name)
					continue
				}
				if trunkNames[name] {
					continue
				}
				trunkNames[name] = true
				port, _ := strconv.Atoi(digits(t.get(row, colTrunkPort...)))
				if port == 5060 {
					port = 0
				}
				tr := config.Trunk{Name: name, Host: host, Port: port, Transport: "udp", Enabled: false, Username: t.get(row, colTrunkUser...)}
				if strings.Contains(strings.ToLower(t.get(row, "transport", "outgoing transport type", "sip trunk security profile")), "tls") {
					tr.Transport = "tls"
				}
				res.Trunks = append(res.Trunks, tr)
			case "outbound":
				raw := t.get(row, colOutPattern...)
				pat, strip, why := ciscoPattern(raw, t.get(row, colOutDiscard...))
				if why != "" {
					warn("route pattern %s %s", raw, why)
					continue
				}
				if n, err := strconv.Atoi(digits(t.get(row, colOutDiscard...))); err == nil && strip == 0 && n > 0 && n < 10 {
					strip = n
				}
				via := t.get(row, colOutTrunk...)
				res.OutboundRoutes = append(res.OutboundRoutes, config.OutboundRoute{Name: "To " + raw, Patterns: []string{pat}, Strip: strip,
					Prepend: digitsPlus(t.get(row, colOutPrefix...)), Trunks: []string{via}})
			case "inbound":
				did := digitsPlus(t.get(row, colInNumber...))
				dest := userNumber(t.get(row, colInDest...))
				if did == "" || !numberRe.MatchString(dest) {
					warn("%s: incoming number %q goes to %q, which is not a number here: add it in Call routing", t.name, t.get(row, colInNumber...), t.get(row, colInDest...))
					continue
				}
				res.InboundRoutes = append(res.InboundRoutes, config.InboundRoute{Name: "Calls to " + did, DID: did,
					Destination: config.Destination{Type: "extension", Target: dest}})
			}
		}
	}
	// Cisco: each hunt pilot rings its hunt list's line groups, in order.
	for _, p := range pilots {
		rg := p.rg
		if p.list != "" {
			lgs, ok := huntLists[p.list]
			if !ok {
				warn("hunt pilot %s uses hunt list %s, which is not in these files: export Hunt List and Line Group with it", rg.Number, p.list)
			}
			for i, name := range lgs {
				lg, ok := lineGroups[name]
				if !ok {
					warn("hunt list %s names line group %s, which is not in these files", p.list, name)
					continue
				}
				if i == 0 {
					rg.Strategy = lg.Strategy
					if rg.RingSeconds == 0 {
						rg.RingSeconds = lg.RingSeconds
					}
				} else if len(lgs) > 1 && i == 1 {
					warn("hunt pilot %s rings %d line groups one after another; BitVoice rings their members as one group", rg.Number, len(lgs))
				}
				rg.Members = append(rg.Members, lg.Members...)
			}
		}
		rg.Members = append(rg.Members, extra[rg.Number]...)
		rg.Members = unique(rg.Members)
		if rg.RingSeconds == 0 {
			rg.RingSeconds = 20
		}
		if len(rg.Members) == 0 {
			warn("group %s %s has no members in these files, left out", rg.Number, rg.Name)
			continue
		}
		res.RingGroups = append(res.RingGroups, rg)
	}
	for g := range extra {
		found := false
		for _, rg := range res.RingGroups {
			found = found || rg.Number == g
		}
		if !found {
			warn("members are listed for group %s, which is not in these files", g)
		}
	}
	// Outbound routes name a trunk (or a route list) by name: only trunks in these files resolve.
	for i := range res.OutboundRoutes {
		r := &res.OutboundRoutes[i]
		if !trunkNames[r.Trunks[0]] {
			warn("route %s goes out on %q: choose the trunk for it after importing", r.Patterns[0], r.Trunks[0])
		}
	}
	if len(res.Trunks) > 0 {
		warn("trunks come in switched off and without passwords (exports do not carry them): fill in each one's details, then switch it on")
	}
}

func digitsPlus(s string) string {
	plus := strings.HasPrefix(strings.TrimSpace(s), "+")
	d := digits(s)
	if plus && d != "" {
		return "+" + d
	}
	return d
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func first(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
