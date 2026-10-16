package i3

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Policy Routing Rules (NENA-STA-010.3f section 3.3.3): an agency's rules for where its calls go,
// signed by the agency and kept in a Policy Store. A rule has conditions (all must hold, its time
// periods any of them) and actions; of the rules whose conditions hold, the one with the highest
// priority acts. When its route fails, the next one does.

// Policy is a Policy Store's policy object.
type Policy struct {
	PolicyOwner                string    `json:"policyOwner"`
	PolicyType                 string    `json:"policyType"`
	PolicyQueueName            string    `json:"policyQueueName,omitempty"`
	PolicyID                   string    `json:"policyId,omitempty"`
	PolicyExpirationTime       string    `json:"policyExpirationTime,omitempty"`
	PolicyLastModificationTime string    `json:"policyLastModificationTime,omitempty"`
	Description                string    `json:"description,omitempty"`
	PolicyRules                []PRRRule `json:"policyRules"`
}

// PRRRule is one rule.
type PRRRule struct {
	ID          string           `json:"id"`
	Priority    int              `json:"priority"`
	Description string           `json:"description,omitempty"`
	Conditions  []map[string]any `json:"conditions,omitempty"`
	Actions     []map[string]any `json:"actions"`
}

// RoutePolicyTypes are the policy types that hold routing rules (the registry's PRR formats).
var RoutePolicyTypes = []string{"OriginationRoutePolicy", "NormalNextHopRoutePolicy", "OtherRoutePolicy"}

// PolicyTypes is the Policy Type registry (section 10.33).
var PolicyTypes = append(slices.Clone(RoutePolicyTypes), "DequeueExpirationTime", "GISReplicas", "ECRF-LVFreplica", "TestCalls",
	"SIPcall", "LoST", "ElementState", "ServiceState", "HELDdereference", "AgentPresencePublish", "AgentPresencePut",
	"AgentPresenceSubscribe", "PolicyStore", "DiscrepancyReport", "QueueState", "DequeueRegistration")

// PolicyError is a refusal with the Policy Store's status code.
type PolicyError struct {
	Code   int
	Reason string
}

func (e *PolicyError) Error() string { return e.Reason }

func perr(code int, f string, a ...any) error { return &PolicyError{code, fmt.Sprintf(f, a...)} }

// ParsePolicy reads and checks a policy object, as a Policy Store must before storing it.
func ParsePolicy(b []byte, now time.Time) (Policy, error) {
	var p Policy
	if err := json.Unmarshal(b, &p); err != nil {
		return p, perr(437, "the policy is not a policy object: %v", err)
	}
	if !slices.Contains(PolicyTypes, p.PolicyType) {
		return p, perr(451, "unknown policy type %q", p.PolicyType)
	}
	if p.PolicyOwner == "" || strings.ContainsAny(p.PolicyOwner, " \t\r\n") {
		return p, perr(452, "the policy names no owner (an FQDN, or a URI holding one)")
	}
	route := slices.Contains(RoutePolicyTypes, p.PolicyType)
	switch {
	case p.PolicyType == "OtherRoutePolicy" && (p.PolicyID == "" || p.PolicyQueueName != ""):
		return p, perr(437, "an OtherRoutePolicy has a policyId and no policyQueueName")
	case (p.PolicyType == "OriginationRoutePolicy" || p.PolicyType == "NormalNextHopRoutePolicy") && (p.PolicyQueueName == "" || p.PolicyID != ""):
		return p, perr(437, "a %s has a policyQueueName and no policyId", p.PolicyType)
	}
	if p.PolicyExpirationTime != "" {
		t, err := ParseTimestamp(p.PolicyExpirationTime)
		if err != nil || !t.After(now) {
			return p, perr(459, "the policyExpirationTime is not a time in the future")
		}
	}
	if !route {
		return p, nil // XACML access policies are kept as they are; BitVoice does not run them
	}
	if len(p.PolicyRules) == 0 {
		return p, perr(437, "the policy has no rules")
	}
	seenPri, seenID := map[int]bool{}, map[string]bool{}
	for _, r := range p.PolicyRules {
		if r.ID == "" || seenID[r.ID] {
			return p, perr(437, "every rule needs an id of its own")
		}
		seenID[r.ID] = true
		if r.Priority < 0 || seenPri[r.Priority] {
			return p, perr(436, "rule %s: priorities are 0 or more and each is used once", r.ID)
		}
		seenPri[r.Priority] = true
		if len(r.Actions) == 0 {
			return p, perr(437, "rule %s has no actions", r.ID)
		}
		terminal := 0
		for _, a := range r.Actions {
			switch t := actionType(a); t {
			case "routeaction":
				if str(a, "recipientUri") == "" {
					return p, perr(437, "rule %s: a RouteAction needs a recipientUri", r.ID)
				}
				terminal++
			case "busyaction":
				terminal++
			case "invokepolicyaction":
				pt := str(a, "policyType")
				if !slices.ContainsFunc(RoutePolicyTypes, func(x string) bool {
					return strings.EqualFold(x, pt) || strings.EqualFold(pt, "OrginationRoutePolicy") && x == "OriginationRoutePolicy"
				}) {
					return p, perr(437, "rule %s: InvokePolicyAction policyType %q", r.ID, pt)
				}
				if strings.EqualFold(pt, "OtherRoutePolicy") != (str(a, "policyId") != "") {
					return p, perr(437, "rule %s: InvokePolicyAction takes a policyId with OtherRoutePolicy only", r.ID)
				}
				terminal++
			case "notifyaction":
				if str(a, "eventCode") == "" {
					return p, perr(437, "rule %s: a NotifyAction needs an eventCode", r.ID)
				}
			case "logaction":
			default:
				return p, perr(437, "rule %s: unknown action %q", r.ID, str(a, "actionType"))
			}
		}
		if terminal > 1 {
			return p, perr(437, "rule %s has more than one Route, Busy or Invoke Policy action", r.ID)
		}
		for _, c := range r.Conditions {
			if str(c, "conditionType") == "" {
				return p, perr(437, "rule %s: a condition has no conditionType", r.ID)
			}
			if strings.EqualFold(str(c, "conditionType"), "TimePeriodCondition") {
				if _, err := ParseTimestamp(str(c, "dateStart")); err != nil {
					return p, perr(437, "rule %s: TimePeriodCondition dateStart: %v", r.ID, err)
				}
				if _, err := ParseTimestamp(str(c, "dateEnd")); err != nil {
					return p, perr(437, "rule %s: TimePeriodCondition dateEnd: %v", r.ID, err)
				}
			}
		}
	}
	return p, nil
}

// Expired says whether a policy is past its expiration time.
func (p Policy) Expired(now time.Time) bool {
	if p.PolicyExpirationTime == "" {
		return false
	}
	t, err := ParseTimestamp(p.PolicyExpirationTime)
	return err == nil && !t.After(now)
}

// ParseTimestamp reads an i3 timestamp (RFC 3339; the standard's own examples also write an
// offset of hours alone, "-04", which is taken too).
func ParseTimestamp(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if n := len(s); n > 3 && (s[n-3] == '+' || s[n-3] == '-') {
		if t, err := time.Parse(time.RFC3339Nano, s+":00"); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a timestamp", s)
}

func actionType(a map[string]any) string { return strings.ToLower(str(a, "actionType")) }

func str(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func num(m map[string]any, k string) (float64, bool) {
	switch v := m[k].(type) {
	case float64:
		return v, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	return 0, false
}

// CallFacts is what the rules may test about a call.
type CallFacts struct {
	Now           time.Time
	RequestURI    string
	IncomingQueue string
	CallSource    string // the originating network's domain, as the ESRP determines it
	Suspicion     int    // Call-Info purpose=emergency-CallSuspicion; 0 when none
	Header        func(name string) []string
	BodyTypes     []string                     // MIME types of the message's body parts
	Additional    map[string]map[string]string // "EmergencyCallData.ServiceInfo" -> element -> value
	Location      *Location                    // the location for routing
	SDP           SDPFacts
	// Lost asks the ECRF for a service URN at the location for routing ("" and false when none).
	Lost func(urn string) (string, bool)
	// QueueState, ServiceState and SecurityPosture give what this element knows of other elements'
	// states; nil (or "") means not known: "Unreachable" and "Green" as the standard says.
	QueueState      func(uri string) string
	ServiceState    func(service string) string
	SecurityPosture func(service string) string
}

// SDPFacts are an INVITE's media and their languages (RFC 8373 hlang-send and hlang-recv).
type SDPFacts struct {
	Offered               bool
	Audio, Video, RTT, IM bool
	LangAudio, LangVideo  []string // in preference order
	LangRTT, LangIM       []string
}

// Outcome is what one executed rule decided.
type Outcome struct {
	Policy   string // owner/type/queue-or-id
	Rule     string
	Priority int
	Kind     string // route, busy, none (a rule with only notify or log actions)
	URI      string // route: the recipient
	RNATimer int    // seconds; 0 for the element's default
	Cause    string
	Notify   []map[string]any
	Log      []string
}

// FetchPolicy finds a stored policy for an Invoke Policy action (type, queue name or id).
type FetchPolicy func(policyType, queue, id string) (Policy, bool)

// Evaluate runs a ruleset for a call and returns the outcomes in the order they are tried: the
// highest-priority rule whose conditions hold first, then (for when its route fails) the next. An
// Invoke Policy action is followed into the other ruleset in place. Depth bounds runaway nesting.
func Evaluate(p Policy, f CallFacts, fetch FetchPolicy) []Outcome {
	st := &evalState{facts: f, fetch: fetch, seen: map[string]bool{}}
	return st.run(p, 0)
}

type evalState struct {
	facts   CallFacts
	fetch   FetchPolicy
	nextHop string // the "Normal-NextHop" variable
	hopSet  bool
	seen    map[string]bool // policies being evaluated (loops)
}

func policyKey(p Policy) string {
	return p.PolicyOwner + "/" + p.PolicyType + "/" + p.PolicyQueueName + p.PolicyID
}

func (st *evalState) run(p Policy, depth int) []Outcome {
	key := policyKey(p)
	if depth > 8 || st.seen[key] || p.Expired(st.facts.Now) {
		return nil
	}
	st.seen[key] = true
	defer delete(st.seen, key)
	rules := slices.Clone(p.PolicyRules)
	slices.SortStableFunc(rules, func(a, b PRRRule) int { return b.Priority - a.Priority })
	var out []Outcome
	for _, r := range rules {
		st.hopSet, st.nextHop = false, "" // each rule's LoST condition sets it anew
		if !st.holds(r.Conditions) {
			continue
		}
		o := Outcome{Policy: key, Rule: r.ID, Priority: r.Priority, Kind: "none"}
		var invoke map[string]any
		for _, a := range r.Actions {
			switch actionType(a) {
			case "routeaction":
				o.Kind, o.URI, o.Cause = "route", str(a, "recipientUri"), str(a, "cause")
				if n, ok := num(a, "rnaTimer"); ok && n > 0 {
					o.RNATimer = int(n)
				}
			case "busyaction":
				o.Kind = "busy"
			case "notifyaction":
				o.Notify = append(o.Notify, a)
			case "logaction":
				o.Log = append(o.Log, str(a, "message"))
			case "invokepolicyaction":
				invoke = a
			}
		}
		if invoke != nil {
			sub, ok := st.invoked(invoke)
			if !ok {
				continue // the rule fails: as if its conditions did not hold
			}
			inner := st.run(sub, depth+1)
			if len(inner) == 0 {
				continue
			}
			if len(o.Notify)+len(o.Log) > 0 {
				inner[0].Notify, inner[0].Log = append(o.Notify, inner[0].Notify...), append(o.Log, inner[0].Log...)
			}
			out = append(out, inner...)
			if inner[len(inner)-1].Kind == "busy" {
				return out
			}
			continue
		}
		out = append(out, o)
		if o.Kind == "busy" {
			return out // nothing after a busy is tried
		}
	}
	return out
}

func (st *evalState) invoked(a map[string]any) (Policy, bool) {
	if st.fetch == nil {
		return Policy{}, false
	}
	switch strings.ToLower(str(a, "policyType")) {
	case "originationroutepolicy", "orginationroutepolicy": // the standard spells it both ways
		return st.fetch("OriginationRoutePolicy", st.facts.IncomingQueue, "")
	case "normalnexthoproutepolicy":
		if !st.hopSet {
			return Policy{}, false
		}
		return st.fetch("NormalNextHopRoutePolicy", st.nextHop, "")
	case "otherroutepolicy":
		return st.fetch("OtherRoutePolicy", "", str(a, "policyId"))
	}
	return Policy{}, false
}

// holds: every condition but the time periods holds, and when there are time periods, one of them.
func (st *evalState) holds(conds []map[string]any) bool {
	times, timeOK := 0, false
	// The LoST condition goes first: the Normal-NextHop condition in the same rule reads its result.
	ordered := slices.Clone(conds)
	slices.SortStableFunc(ordered, func(a, b map[string]any) int {
		la := strings.EqualFold(str(a, "conditionType"), "LostServiceUrnCondition")
		lb := strings.EqualFold(str(b, "conditionType"), "LostServiceUrnCondition")
		switch {
		case la && !lb:
			return -1
		case lb && !la:
			return 1
		}
		return 0
	})
	for _, c := range ordered {
		ok := st.condition(c)
		if c["negation"] == true {
			ok = !ok
		}
		if strings.EqualFold(str(c, "conditionType"), "TimePeriodCondition") {
			times++
			timeOK = timeOK || ok
			continue
		}
		if !ok {
			if strings.EqualFold(str(c, "conditionType"), "LostServiceUrnCondition") {
				return false // "the evaluation of this rule stops"
			}
			return false
		}
	}
	return times == 0 || timeOK
}

func compare(op, have, want string) bool {
	h, w := strings.ToLower(have), strings.ToLower(want)
	switch strings.ToUpper(op) {
	case "EQ":
		return h == w
	case "SS":
		return strings.Contains(h, w)
	case "NE":
		return h != w
	}
	return false
}

// ordered compares numbers, else dates; false when either is neither.
func ordered(op, have, want string) bool {
	var c int
	if a, err := strconv.ParseFloat(strings.TrimSpace(have), 64); err == nil {
		b, err := strconv.ParseFloat(strings.TrimSpace(want), 64)
		if err != nil {
			return false
		}
		c = cmpF(a, b)
	} else if a, err := ParseTimestamp(have); err == nil {
		b, err := ParseTimestamp(want)
		if err != nil {
			return false
		}
		c = a.Compare(b)
	} else {
		return false
	}
	switch strings.ToUpper(op) {
	case "GT":
		return c > 0
	case "LT":
		return c < 0
	case "GE":
		return c >= 0
	case "LE":
		return c <= 0
	}
	return false
}

func cmpF(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (st *evalState) condition(c map[string]any) bool {
	f := st.facts
	switch strings.ToLower(str(c, "conditionType")) {
	case "timeperiodcondition":
		return timePeriod(c, f.Now)
	case "sipheadercondition":
		if f.Header == nil {
			return false
		}
		vals := f.Header(str(c, "field"))
		op := strings.ToUpper(str(c, "operator"))
		if op != "EQ" && op != "SS" {
			return false // "IS" needs the registry's tests, not built
		}
		for _, v := range vals {
			if compare(op, v, str(c, "content")) {
				return true
			}
		}
		return false
	case "additionaldatacondition":
		block, ok := f.Additional[str(c, "type")]
		op := strings.ToUpper(str(c, "operator"))
		switch op {
		case "EXISTS":
			return ok
		case "MISSING":
			return !ok
		}
		if !ok {
			return false
		}
		have, ok := lookupFold(block, str(c, "element"))
		if !ok {
			return false
		}
		switch op {
		case "EQ", "SS", "NE":
			return compare(op, have, str(c, "content"))
		case "GT", "LT", "GE", "LE":
			// "checks if the string in content ... is greater than the element contents"
			return ordered(op, str(c, "content"), have)
		}
		return false
	case "mimebodycondition":
		list, _ := c["mimeList"].([]any)
		for _, m := range list {
			ms, _ := m.(string)
			for _, t := range f.BodyTypes {
				if strings.EqualFold(strings.TrimSpace(strings.Split(t, ";")[0]), ms) {
					return true
				}
			}
		}
		return false
	case "locationcondition":
		return locationMatches(c, f.Location)
	case "callsuspicioncondition":
		from, ok1 := num(c, "scoreFrom")
		to, ok2 := num(c, "scoreTo")
		return ok1 && ok2 && float64(f.Suspicion) >= from && float64(f.Suspicion) <= to
	case "securityposturecondition":
		have := "Green"
		if f.SecurityPosture != nil {
			if s := f.SecurityPosture(str(c, "service")); s != "" {
				have = s
			}
		}
		return stateIs(str(c, "condition"), have, str(c, "value"))
	case "queuestatecondition":
		have := "Unreachable"
		if f.QueueState != nil {
			if s := f.QueueState(str(c, "queue")); s != "" {
				have = s
			}
		}
		return stateIs(str(c, "condition"), have, str(c, "value"))
	case "servicestatecondition":
		have := "Unreachable"
		if f.ServiceState != nil {
			if s := f.ServiceState(str(c, "service")); s != "" {
				have = s
			}
		}
		return stateIs(str(c, "condition"), have, str(c, "value"))
	case "lostserviceurncondition":
		if f.Lost == nil {
			return false
		}
		uri, ok := f.Lost(str(c, "urn"))
		if ok && uri != "" {
			st.nextHop, st.hopSet = uri, true
			return true
		}
		return false
	case "callsourcecondition":
		return compare(str(c, "operator"), f.CallSource, str(c, "content"))
	case "requesturicondition":
		return compare(str(c, "operator"), f.RequestURI, str(c, "content"))
	case "normalnexthopcondition":
		return compare(str(c, "operator"), st.nextHop, str(c, "content")) // undefined: ""
	case "incomingqueuecondition":
		return compare(str(c, "operator"), f.IncomingQueue, str(c, "content"))
	case "sdpoffercondition":
		return sdpOffer(c, f.SDP)
	}
	// BodyPartCondition, CapCondition, CallingNumberVerificationStatusCondition and extensions not
	// understood evaluate to false, as the standard says of what an evaluator does not understand.
	return false
}

func stateIs(cond, have, want string) bool {
	eq := strings.EqualFold(have, want)
	switch strings.ToUpper(cond) {
	case "EQ":
		return eq
	case "NE":
		return !eq
	}
	return false
}

func lookupFold(m map[string]string, k string) (string, bool) {
	for kk, v := range m {
		if strings.EqualFold(kk, k) {
			return v, true
		}
	}
	return "", false
}

var weekdays = map[string]time.Weekday{"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday,
	"TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday}

func timePeriod(c map[string]any, now time.Time) bool {
	start, err1 := ParseTimestamp(str(c, "dateStart"))
	end, err2 := ParseTimestamp(str(c, "dateEnd"))
	if err1 != nil || err2 != nil || now.Before(start) || now.After(end) {
		return false
	}
	local := now.In(start.Location()) // times of day share dateStart's offset
	if wl := str(c, "weekdayList"); wl != "" && end.Sub(start) >= 7*24*time.Hour {
		days, valid := map[time.Weekday]bool{}, 0
		for _, d := range strings.Split(wl, ",") {
			if wd, ok := weekdays[strings.ToUpper(strings.TrimSpace(d))]; ok {
				days[wd] = true
				valid++
			}
		}
		if valid > 0 && !days[local.Weekday()] {
			return false
		}
	}
	ts, te := str(c, "timeStart"), str(c, "timeEnd")
	if ts == "" {
		ts = "00:00:00"
	}
	if te == "" {
		te = "23:59:59"
	}
	a, okA := secs(ts)
	b, okB := secs(te)
	if !okA || !okB || a > b {
		return true // invalid or reversed times are ignored
	}
	s := local.Hour()*3600 + local.Minute()*60 + local.Second()
	return s >= a && s <= b
}

func secs(t string) (int, bool) {
	t, _, _ = strings.Cut(t, ".")
	p := strings.Split(t, ":")
	if len(p) != 3 {
		return 0, false
	}
	h, e1 := strconv.Atoi(p[0])
	m, e2 := strconv.Atoi(p[1])
	s, e3 := strconv.Atoi(p[2])
	if e1 != nil || e2 != nil || e3 != nil || h > 24 || m > 59 || s > 60 || h == 24 && (m > 0 || s > 0) {
		return 0, false
	}
	return h*3600 + m*60 + s, true
}

// locationMatches: any of the "location" children holds (RFC 6772 4.1 and 4.2).
func locationMatches(c map[string]any, loc *Location) bool {
	if loc == nil {
		return false
	}
	var kids []any
	switch v := c["location"].(type) {
	case []any:
		kids = v
	case map[string]any:
		kids = []any{v}
	}
	for _, k := range kids {
		m, ok := k.(map[string]any)
		if !ok || m["extension"] != nil {
			continue
		}
		want, err := ParsePIDF([]byte(str(m, "lo")))
		if err != nil {
			continue
		}
		switch strings.ToLower(str(m, "profile")) {
		case "geodetic":
			if loc.HasPoint() && want.HasPoint() && want.Radius > 0 {
				d := distanceM(*loc.Lat, *loc.Lon, *want.Lat, *want.Lon)
				if d+loc.Radius <= want.Radius {
					return true
				}
			}
		case "civic":
			if loc.Civic != nil && want.Civic != nil && civicWithin(*want.Civic, *loc.Civic) {
				return true
			}
		}
	}
	return false
}

// civicWithin: each element the rule gives exists in the call's address, octet for octet.
func civicWithin(want, have Civic) bool {
	wb, _ := json.Marshal(want)
	hb, _ := json.Marshal(have)
	var w, h map[string]string
	json.Unmarshal(wb, &w)
	json.Unmarshal(hb, &h)
	if len(w) == 0 {
		return false
	}
	for k, v := range w {
		if h[k] != v {
			return false
		}
	}
	return true
}

func distanceM(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371008.8
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := (lat2-lat1)*math.Pi/180, (lon2-lon1)*math.Pi/180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}

func langMatch(have []string, want string) bool {
	w := strings.ToLower(want)
	for _, h := range have {
		h = strings.ToLower(h)
		if h == w || strings.HasPrefix(h, w+"-") {
			return true
		}
	}
	return false
}

func strs(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func sdpOffer(c map[string]any, s SDPFacts) bool {
	if !s.Offered {
		return false
	}
	text := append(slices.Clone(s.LangRTT), s.LangIM...)
	media := map[string]struct {
		on    bool
		langs []string
	}{"Audio": {s.Audio, s.LangAudio}, "Video": {s.Video, s.LangVideo}, "Rtt": {s.RTT, s.LangRTT}, "Im": {s.IM, s.LangIM}, "Text": {s.RTT || s.IM, text}}
	for name, m := range media {
		if c[strings.ToLower(name)] == true && !m.on {
			return false
		}
		if ls := strs(c["lang"+name]); len(ls) > 0 {
			ok := false
			for _, l := range ls {
				ok = ok || langMatch(m.langs, l)
			}
			if !ok {
				return false
			}
		}
		if pref, ok := c["lang"+name+"Pref"].(map[string]any); ok {
			test, list := str(pref, "langTest"), strs(pref["langList"])
			best := ""
			for _, have := range m.langs { // the offer's order is the caller's preference
				for _, l := range list {
					if best == "" && langMatch([]string{have}, l) {
						best = l
					}
				}
			}
			if best == "" || !strings.EqualFold(best, test) {
				return false
			}
		}
	}
	return true
}

// ErrNoPolicy is returned when no stored policy applies.
var ErrNoPolicy = errors.New("i3: no policy")
