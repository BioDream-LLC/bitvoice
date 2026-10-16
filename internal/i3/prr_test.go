package i3

import (
	"testing"
	"time"
)

// The examples of NENA-STA-010.3f section 3.3.3.3, as a ruleset (the standard writes "routeAction"
// in some of them: action names are matched without regard to case).
const examplePolicy = `{
  "policyOwner": "psap.example.gov", "policyType": "OriginationRoutePolicy", "policyQueueName": "sip:esrp@esrp.example.gov",
  "policyRules": [
    {"description": "Call is probably spam.", "id": "AA56i12", "priority": 7,
     "conditions": [{"conditionType": "CallSuspicionCondition", "scoreFrom": 70, "scoreTo": 100}],
     "actions": [{"actionType": "RouteAction", "recipientUri": "sip:special-treatment@psap.example.gov"}]},
    {"id": "AA56i11", "priority": 6,
     "conditions": [{"conditionType": "MimeBodyCondition", "mimeList": ["application/common-alerting-protocol+xml"]}],
     "actions": [{"actionType": "routeAction", "recipientUri": "sip:psap@home.example.gov"}]},
    {"id": "AA56i10", "priority": 5,
     "conditions": [
       {"conditionType": "QueueStateCondition", "queue": "sip:answering-machine@home.foo-bar.com", "condition": "EQ", "value": "Active"},
       {"conditionType": "TimePeriodCondition", "dateStart": "2019-11-04T02:00:00-05:00", "timeStart": "08:00:00", "timeEnd": "18:00:00",
        "weekdayList": "MO,TU,WE,TH,FR", "dateEnd": "2020-03-10T01:59:59-05:00"}],
     "actions": [{"actionType": "routeAction", "recipientUri": "sip:answering-machine@home.foo-bar.com"}]},
    {"id": "AA56i222", "priority": 3,
     "conditions": [{"conditionType": "LostServiceUrnCondition", "urn": "urn:emergency:service:sos.psap"}],
     "actions": [{"actionType": "InvokePolicyAction", "policyType": "NormalNexthopRoutePolicy"}]},
    {"id": "default", "priority": 0,
     "actions": [{"actionType": "RouteAction", "recipientUri": "sip:default@psap.example.gov", "rnaTimer": 15}, {"actionType": "LogAction", "message": "default route"}]}
  ]}`

const nextHopPolicy = `{
  "policyOwner": "county.example.gov", "policyType": "NormalNextHopRoutePolicy", "policyQueueName": "sip:county@psap.example.gov",
  "policyRules": [
    {"id": "closed", "priority": 2,
     "conditions": [{"conditionType": "TimePeriodCondition", "dateStart": "2026-01-01T00:00:00-05", "dateEnd": "2026-12-31T23:59:59-05",
       "timeStart": "22:00:00", "timeEnd": "23:59:59"}],
     "actions": [{"actionType": "RouteAction", "recipientUri": "sip:night@backup.example.gov"}]},
    {"id": "open", "priority": 1, "actions": [{"actionType": "RouteAction", "recipientUri": "sip:county@psap.example.gov"}]}
  ]}`

func mustPolicy(t *testing.T, s string) Policy {
	t.Helper()
	p, err := ParsePolicy([]byte(s), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPolicyRoutingRules(t *testing.T) {
	p := mustPolicy(t, examplePolicy)
	hop := mustPolicy(t, nextHopPolicy)
	fetch := func(pt, q, id string) (Policy, bool) {
		if pt == hop.PolicyType && q == hop.PolicyQueueName {
			return hop, true
		}
		return Policy{}, false
	}
	routes := func(os []Outcome) (out []string) {
		for _, o := range os {
			out = append(out, o.Kind+":"+o.URI)
		}
		return
	}
	wed := time.Date(2019, 11, 6, 10, 0, 0, 0, time.FixedZone("", -5*3600))
	cases := []struct {
		name  string
		facts CallFacts
		want  []string
	}{
		{"spam first, then the default", CallFacts{Now: wed, Suspicion: 80}, []string{"route:sip:special-treatment@psap.example.gov", "route:sip:default@psap.example.gov"}},
		{"a score out of range", CallFacts{Now: wed, Suspicion: 69}, []string{"route:sip:default@psap.example.gov"}},
		{"CAP body", CallFacts{Now: wed, BodyTypes: []string{"application/common-alerting-protocol+xml; charset=utf-8"}}, []string{"route:sip:psap@home.example.gov", "route:sip:default@psap.example.gov"}},
		{"queue active in the hours", CallFacts{Now: wed, QueueState: func(string) string { return "Active" }}, []string{"route:sip:answering-machine@home.foo-bar.com", "route:sip:default@psap.example.gov"}},
		{"queue active out of hours", CallFacts{Now: wed.Add(9 * time.Hour), QueueState: func(string) string { return "Active" }}, []string{"route:sip:default@psap.example.gov"}},
		{"queue state unknown: Unreachable", CallFacts{Now: wed}, []string{"route:sip:default@psap.example.gov"}},
		{"Saturday", CallFacts{Now: wed.Add(3 * 24 * time.Hour), QueueState: func(string) string { return "Active" }}, []string{"route:sip:default@psap.example.gov"}},
	}
	for _, c := range cases {
		got := routes(Evaluate(p, c.facts, fetch))
		if !equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	// LoST then the next hop's own policy: closed at night, open by day.
	lost := func(urn string) (string, bool) {
		return "sip:county@psap.example.gov", urn == "urn:emergency:service:sos.psap"
	}
	night := time.Date(2026, 3, 3, 22, 30, 0, 0, time.FixedZone("", -5*3600))
	got := routes(Evaluate(p, CallFacts{Now: night, Lost: lost}, fetch))
	if !equal(got, []string{"route:sip:night@backup.example.gov", "route:sip:county@psap.example.gov", "route:sip:default@psap.example.gov"}) {
		t.Errorf("next hop at night: %v", got)
	}
	got = routes(Evaluate(p, CallFacts{Now: night.Add(-10 * time.Hour), Lost: lost}, fetch))
	if !equal(got, []string{"route:sip:county@psap.example.gov", "route:sip:default@psap.example.gov"}) {
		t.Errorf("next hop by day: %v", got)
	}
	// LoST answers, but no policy for that next hop: the rule fails.
	got = routes(Evaluate(p, CallFacts{Now: night, Lost: func(string) (string, bool) { return "sip:other@x", true }}, fetch))
	if !equal(got, []string{"route:sip:default@psap.example.gov"}) {
		t.Errorf("no next hop policy: %v", got)
	}
	o := Evaluate(p, CallFacts{Now: wed}, fetch)
	if o[0].RNATimer != 15 || len(o[0].Log) != 1 || o[0].Rule != "default" {
		t.Errorf("default rule %+v", o[0])
	}
}

func TestPolicyConditions(t *testing.T) {
	lat, lon := 40.2733, -76.8840
	loc := &Location{Lat: &lat, Lon: &lon, Radius: 50, Civic: &Civic{Country: "US", State: "PA", City: "Harrisburg", Street: "Market"}}
	facts := CallFacts{Now: time.Now(), RequestURI: "urn:service:sos.ecall.automatic", CallSource: "carrier.example.net", IncomingQueue: "sip:q@esrp",
		Header:     func(n string) []string { return map[string][]string{"user-agent": {"Phone/2.1 (example)"}}[n] },
		Additional: map[string]map[string]string{"EmergencyCallData.ServiceInfo": {"ServiceType": "relay"}, "EmergencyCallData.VEDS": {"Speed": "88"}},
		Location:   loc,
		SDP:        SDPFacts{Offered: true, Audio: true, RTT: true, LangAudio: []string{"es", "en-US"}, LangRTT: []string{"en"}}}
	geo := `<presence xmlns="urn:ietf:params:xml:ns:pidf" xmlns:gp="urn:ietf:params:xml:ns:pidf:geopriv10" xmlns:gml="http://www.opengis.net/gml" xmlns:gs="http://www.opengis.net/pidflo/1.0" entity="pres:x@y"><tuple id="a"><status><gp:geopriv><gp:location-info><gs:Circle srsName="urn:ogc:def:crs:EPSG::4326"><gml:pos>40.2733 -76.8845</gml:pos><gs:radius uom="urn:ogc:def:uom:EPSG::9001">200</gs:radius></gs:Circle></gp:location-info></gp:geopriv></status></tuple></presence>`
	civic := `<presence xmlns="urn:ietf:params:xml:ns:pidf" xmlns:gp="urn:ietf:params:xml:ns:pidf:geopriv10" xmlns:ca="urn:ietf:params:xml:ns:pidf:geopriv10:civicAddr" entity="pres:x@y"><tuple id="a"><status><gp:geopriv><gp:location-info><ca:civicAddress><ca:country>US</ca:country><ca:A1>PA</ca:A1><ca:A3>Harrisburg</ca:A3></ca:civicAddress></gp:location-info></gp:geopriv></status></tuple></presence>`
	civicNo := `<presence xmlns="urn:ietf:params:xml:ns:pidf" xmlns:gp="urn:ietf:params:xml:ns:pidf:geopriv10" xmlns:ca="urn:ietf:params:xml:ns:pidf:geopriv10:civicAddr" entity="pres:x@y"><tuple id="a"><status><gp:geopriv><gp:location-info><ca:civicAddress><ca:country>US</ca:country><ca:A3>harrisburg</ca:A3></ca:civicAddress></gp:location-info></gp:geopriv></status></tuple></presence>`
	for _, c := range []struct {
		cond map[string]any
		want bool
	}{
		{map[string]any{"conditionType": "RequestUriCondition", "operator": "SS", "content": "ECALL"}, true},
		{map[string]any{"conditionType": "RequestUriCondition", "operator": "EQ", "content": "urn:service:sos"}, false},
		{map[string]any{"conditionType": "RequestUriCondition", "operator": "EQ", "content": "urn:service:sos", "negation": true}, true},
		{map[string]any{"conditionType": "CallSourceCondition", "operator": "SS", "content": "CARRIER"}, true},
		{map[string]any{"conditionType": "IncomingQueueCondition", "operator": "NE", "content": "sip:q@esrp"}, false},
		{map[string]any{"conditionType": "SipHeaderCondition", "field": "user-agent", "operator": "SS", "content": "phone/2"}, true},
		{map[string]any{"conditionType": "SipHeaderCondition", "field": "x-none", "operator": "EQ", "content": ""}, false},
		{map[string]any{"conditionType": "AdditionalDataCondition", "type": "EmergencyCallData.ServiceInfo", "operator": "EQ", "element": "ServiceType", "content": "RELAY"}, true},
		{map[string]any{"conditionType": "AdditionalDataCondition", "type": "EmergencyCallData.VEDS", "operator": "exists"}, true},
		{map[string]any{"conditionType": "AdditionalDataCondition", "type": "EmergencyCallData.Comment", "operator": "missing"}, true},
		{map[string]any{"conditionType": "AdditionalDataCondition", "type": "EmergencyCallData.VEDS", "operator": "GT", "element": "Speed", "content": "100"}, true},
		{map[string]any{"conditionType": "AdditionalDataCondition", "type": "EmergencyCallData.VEDS", "operator": "GT", "element": "Speed", "content": "fast"}, false},
		{map[string]any{"conditionType": "LocationCondition", "location": []any{map[string]any{"profile": "geodetic", "lo": geo}}}, true},
		{map[string]any{"conditionType": "LocationCondition", "location": []any{map[string]any{"profile": "civic", "lo": civic}}}, true},
		{map[string]any{"conditionType": "LocationCondition", "location": []any{map[string]any{"profile": "civic", "lo": civicNo}}}, false}, // octet for octet
		{map[string]any{"conditionType": "SdpOfferCondition", "audio": true, "rtt": true}, true},
		{map[string]any{"conditionType": "SdpOfferCondition", "video": true}, false},
		{map[string]any{"conditionType": "SdpOfferCondition", "langAudio": []any{"en"}}, true},
		{map[string]any{"conditionType": "SdpOfferCondition", "langAudio": []any{"fr"}}, false},
		{map[string]any{"conditionType": "SdpOfferCondition", "langAudioPref": map[string]any{"langTest": "es", "langList": []any{"en", "es", "fr"}}}, true},
		{map[string]any{"conditionType": "SdpOfferCondition", "langAudioPref": map[string]any{"langTest": "en", "langList": []any{"en", "es"}}}, false},
		{map[string]any{"conditionType": "SecurityPostureCondition", "service": "x", "condition": "EQ", "value": "Green"}, true},
		{map[string]any{"conditionType": "ServiceStateCondition", "service": "x", "condition": "EQ", "value": "Unreachable"}, true},
		{map[string]any{"conditionType": "BodyPartCondition", "contentType": "application/json", "operator": "exists"}, false},
		{map[string]any{"conditionType": "NormalNextHopCondition", "operator": "EQ", "content": ""}, true},
	} {
		st := &evalState{facts: facts, seen: map[string]bool{}}
		if got := st.holds([]map[string]any{c.cond}); got != c.want {
			t.Errorf("%v: %v, want %v", c.cond, got, c.want)
		}
	}
}

func TestPolicyChecks(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		doc  string
		code int
	}{
		{`{"policyOwner":"a.gov","policyType":"Nonsense","policyRules":[]}`, 451},
		{`{"policyType":"OtherRoutePolicy","policyId":"x","policyRules":[{"id":"1","priority":1,"actions":[{"actionType":"BusyAction"}]}]}`, 452},
		{`{"policyOwner":"a.gov","policyType":"OtherRoutePolicy","policyRules":[{"id":"1","priority":1,"actions":[{"actionType":"BusyAction"}]}]}`, 437},
		{`{"policyOwner":"a.gov","policyType":"OtherRoutePolicy","policyId":"x","policyRules":[{"id":"1","priority":1,"actions":[{"actionType":"BusyAction"}]},{"id":"2","priority":1,"actions":[{"actionType":"BusyAction"}]}]}`, 436},
		{`{"policyOwner":"a.gov","policyType":"OtherRoutePolicy","policyId":"x","policyRules":[{"id":"1","priority":1,"actions":[{"actionType":"BusyAction"},{"actionType":"RouteAction","recipientUri":"sip:a@b"}]}]}`, 437},
		{`{"policyOwner":"a.gov","policyType":"OtherRoutePolicy","policyId":"x","policyExpirationTime":"2020-01-01T00:00:00Z","policyRules":[{"id":"1","priority":1,"actions":[{"actionType":"BusyAction"}]}]}`, 459},
	} {
		_, err := ParsePolicy([]byte(c.doc), now)
		pe, ok := err.(*PolicyError)
		if !ok || pe.Code != c.code {
			t.Errorf("%s: %v, want %d", c.doc, err, c.code)
		}
	}
	// a busy rule stops the list
	p := mustPolicy(t, `{"policyOwner":"a.gov","policyType":"OtherRoutePolicy","policyId":"x","policyRules":[{"id":"1","priority":5,"actions":[{"actionType":"BusyAction"}]},{"id":"2","priority":1,"actions":[{"actionType":"RouteAction","recipientUri":"sip:a@b"}]}]}`)
	if o := Evaluate(p, CallFacts{Now: now}, nil); len(o) != 1 || o[0].Kind != "busy" {
		t.Errorf("busy: %+v", o)
	}
	// a policy that invokes itself is not followed round
	p = mustPolicy(t, `{"policyOwner":"a.gov","policyType":"OtherRoutePolicy","policyId":"x","policyRules":[{"id":"1","priority":5,"actions":[{"actionType":"InvokePolicyAction","policyType":"OtherRoutePolicy","policyId":"x"}]},{"id":"2","priority":1,"actions":[{"actionType":"RouteAction","recipientUri":"sip:a@b"}]}]}`)
	if o := Evaluate(p, CallFacts{Now: now}, func(string, string, string) (Policy, bool) { return p, true }); len(o) != 1 || o[0].URI != "sip:a@b" {
		t.Errorf("loop: %+v", o)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
