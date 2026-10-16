package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/i3"
	"github.com/biodream-llc/bitvoice/internal/lost"
)

type i3Client struct {
	t    *testing.T
	base string
}

func (c i3Client) call(method, path string, body []byte, accept ...string) (int, []byte) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if len(accept) > 0 {
		req.Header.Set("Accept", accept[0])
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r.StatusCode, b
}

func jsonOf(v any) []byte { b, _ := json.Marshal(v); return b }

// The NENA i3 web services: each answers as its published OpenAPI definition says (checked against
// the NENA911 repositories when BITVOICE_TEST_NENA is set), and does what it is for.
func TestI3WebServices(t *testing.T) {
	s, ts := newServer(t)
	s.Engine.Policies, s.Engine.Dequeuers = s.PolicyFor, s.DequeuersFor
	c := newClient(t, ts.URL)
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	root, owner := testPCA(t, "esrp.state.example.gov")
	_, impostor := testPCA(t, "esrp.state.example.gov")
	_, cfg := c.do("GET", "/api/settings", nil)
	sys := cfg["system"].(map[string]any)
	core := sys["core"].(map[string]any)
	for _, k := range []string{"policyStore", "discrepancy", "geocode", "msag", "adr", "locator", "dequeue", "testCalls"} {
		core[k] = true
	}
	core["allowFrom"], core["pcaRoots"], core["drContact"] = []string{"127.0.0.1"}, root, "Core services desk, core@state.example.gov, +1 717 555 0199"
	core["queueUri"] = "sip:esrp@esrp.state.example.gov"
	_, trunks := c.do("GET", "/api/trunks", nil)
	trunkID := trunks["items"].([]any)[0].(map[string]any)["id"]
	core["psaps"] = []map[string]any{{"uri": "sip:sos@psap.county.example.gov", "name": "County", "trunk": trunkID}}
	core["bcf"] = map[string]any{"enabled": true, "badActors": true}
	sys["psap"].(map[string]any)["agencyId"] = "state.example.gov"
	if code, out := c.do("PUT", "/api/settings", cfg); code != 200 {
		t.Fatalf("save %d %v", code, out)
	}
	h := i3Client{t, ts.URL}

	// Versions of every service.
	for spec, path := range map[string]string{"Policy-Store/i3-policy-store.yaml": "/i3/PolicyStore/Versions", "discrepancy-reporting/i3-discrepancy-reports.yaml": "/i3/DiscrepancyReports/v1/Versions",
		"Geocode-Conversion-Service/i3-geocode-conversion.yaml": "/i3/Gcs/Versions", "MSAG-Conversion-Service/i3-msag-conversion.yaml": "/i3/Mcs/Versions",
		"additional-data-repository/i3-adr.yaml": "/i3/Adr/Versions", "service-agency-locator/i3-service-agency-locator.yaml": "/i3/ServiceAgencyLocator/Versions",
		"dequeue-registration-service/i3-dequeue.yaml": "/i3/DequeueRegistration/Versions", "test-call-service/i3-test-call.yaml": "/i3/TestCall/Versions",
		"bad-actor-service/i3-bad-actor.yaml": "/i3/BadActor/Versions"} {
		code, b := h.call("GET", path, nil)
		if code != 200 {
			t.Fatalf("%s %d", path, code)
		}
		nenaCheck(t, spec, "/Versions", "GET", "200", b)
	}

	// The Policy Store.
	const ps = "Policy-Store/i3-policy-store.yaml"
	policy := map[string]any{"policyOwner": "esrp.state.example.gov", "policyType": "OriginationRoutePolicy", "policyQueueName": "sip:esrp@esrp.state.example.gov",
		"policyRules": []any{map[string]any{"id": "r1", "priority": 1, "actions": []any{map[string]any{"actionType": "RouteAction", "recipientUri": "sip:sos@psap.county.example.gov"}}}}}
	signed := func(p map[string]any, cert any) []byte {
		jws, err := i3.SignJWS(jsonOf(p), owner)
		if cert != nil {
			jws, err = i3.SignJWS(jsonOf(p), impostor)
		}
		if err != nil {
			t.Fatal(err)
		}
		f, _ := i3.Flat(jws)
		return jsonOf(f)
	}
	body := signed(policy, nil)
	nenaCheck(t, ps, "/Policies", "POST", "request", body)
	if code, b := h.call("POST", "/i3/PolicyStore/v1/Policies", signed(policy, impostor)); code != 434 {
		t.Fatalf("an impostor's policy: %d %s", code, b)
	}
	if code, b := h.call("POST", "/i3/PolicyStore/v1/Policies", body); code != 201 {
		t.Fatalf("store: %d %s", code, b)
	}
	if code, _ := h.call("POST", "/i3/PolicyStore/v1/Policies", body); code != 437 {
		t.Fatalf("stored twice: %d", code)
	}
	code, b := h.call("GET", "/i3/PolicyStore/v1/Policies?policyType=OriginationRoutePolicy", nil)
	if code != 200 {
		t.Fatalf("get %d %s", code, b)
	}
	nenaCheck(t, ps, "/Policies", "GET", "200", b)
	var arr struct {
		Policies []i3.FlatJWS `json:"policies"`
	}
	json.Unmarshal(b, &arr)
	var sent i3.FlatJWS
	json.Unmarshal(body, &sent)
	if len(arr.Policies) != 1 || arr.Policies[0] != sent {
		t.Fatal("the policy came back altered")
	}
	if p, ok := s.PolicyFor("OriginationRoutePolicy", "sip:esrp@esrp.state.example.gov", ""); !ok || p.PolicyRules[0].ID != "r1" {
		t.Fatal("the ESRP does not see it")
	}
	code, b = h.call("GET", "/i3/PolicyStore/v1/PolicyEnums?policyType=OriginationRoutePolicy", nil)
	if code != 200 {
		t.Fatalf("enums %d", code)
	}
	nenaCheck(t, ps, "/PolicyEnums", "GET", "200", b)
	policy["policyRules"] = []any{map[string]any{"id": "r2", "priority": 2, "actions": []any{map[string]any{"actionType": "BusyAction"}}}}
	q := "?policyOwner=esrp.state.example.gov&policyType=OriginationRoutePolicy&policyQueueName=" + url.QueryEscape("sip:esrp@esrp.state.example.gov")
	if code, b := h.call("PUT", "/i3/PolicyStore/v1/Policies"+q, signed(policy, nil)); code != 200 {
		t.Fatalf("update %d %s", code, b)
	}
	if p, _ := s.PolicyFor("OriginationRoutePolicy", "sip:esrp@esrp.state.example.gov", ""); p.PolicyRules[0].ID != "r2" {
		t.Fatal("not updated")
	}
	if code, _ := h.call("GET", "/i3/PolicyStore/v1/Policies?policyType=Nonsense", nil); code != 451 {
		t.Fatalf("bad type %d", code)
	}
	if code, _ := h.call("DELETE", "/i3/PolicyStore/v1/Policies"+q, nil); code != 200 {
		t.Fatal("delete")
	}
	if _, ok := s.PolicyFor("OriginationRoutePolicy", "sip:esrp@esrp.state.example.gov", ""); ok {
		t.Fatal("still there after delete")
	}

	// Discrepancy reporting: a report filed with ourselves comes in, is resolved, and the resolution
	// arrives back at the reporter (ourselves).
	const drs = "discrepancy-reporting/i3-discrepancy-reports.yaml"
	report := map[string]any{"reportType": "LostDiscrepancyReport", "problemSeverity": "Minor", "problemService": "ecrf.state.example.gov",
		"query": "findService", "request": "<findService/>", "response": "<findServiceResponse/>", "problem": "RouteIncorrect", "problemComments": "routes to the wrong county"}
	code, out := c.do("POST", "/api/core/discrepancies", map[string]any{"to": ts.URL + "/i3/DiscrepancyReports/v1", "report": report})
	if code != 200 {
		t.Fatalf("file: %d %v", code, out)
	}
	nenaCheck(t, drs, "/Reports", "POST", "request", jsonOf(out["report"]))
	nenaCheck(t, drs, "/Reports", "POST", "201", jsonOf(out["response"]), "application/json")
	id := out["id"].(string)
	stq := "?agencyName=state.example.gov&discrepancyReportId=" + id
	code, b = h.call("GET", "/i3/DiscrepancyReports/v1/StatusUpdates"+stq, nil)
	if code != 200 {
		t.Fatalf("status %d %s", code, b)
	}
	nenaCheck(t, drs, "/StatusUpdates", "GET", "200", b)
	if code, _ := h.call("GET", "/i3/DiscrepancyReports/v1/Resolutions"+stq, nil); code != 475 {
		t.Fatal("a resolution before there is one")
	}
	if code, _ := h.call("GET", "/i3/DiscrepancyReports/v1/StatusUpdates?agencyName=other.gov&discrepancyReportId="+id, nil); code != 471 {
		t.Fatal("another agency read the status")
	}
	if code, out := c.do("POST", "/api/core/discrepancies/"+id+"/resolve", map[string]any{"resolution": "DataCorrected", "comments": "boundary fixed"}); code != 200 || out["error"] != nil {
		t.Fatalf("resolve %d %v", code, out)
	}
	code, b = h.call("GET", "/i3/DiscrepancyReports/v1/Resolutions"+stq, nil)
	if code != 200 {
		t.Fatalf("resolution %d %s", code, b)
	}
	nenaCheck(t, drs, "/Resolutions", "GET", "200", b)
	_, list := c.do("GET", "/api/core/discrepancies", nil)
	resolvedFiled := false
	for _, d := range list["items"].([]any) {
		m := d.(map[string]any)
		resolvedFiled = resolvedFiled || m["direction"] == "filed" && m["status"] == "resolved"
	}
	if !resolvedFiled {
		t.Fatalf("the resolution did not reach the reporter: %v", list)
	}
	if code, _ := h.call("POST", "/i3/DiscrepancyReports/v1/Reports", jsonOf(map[string]any{"reportType": "ImrDiscrepancyReport", "resolutionUri": "https://x", "discrepancyReportSubmitTimeStamp": "2026-01-01T00:00:00Z",
		"discrepancyReportId": "x1", "reportingAgencyName": "a.gov", "reportingContactJcard": "[]", "problemSeverity": "Minor"})); code != 470 {
		t.Fatalf("a report about a service not run here: %d", code)
	}

	// Geocode and MSAG conversion, from the LVF's address data.
	gis := `{"type":"FeatureCollection","features":[
	 {"type":"Feature","properties":{"Add_Number":120,"St_Name":"Market","St_PosTyp":"Street","St_PreDir":"N","Inc_Muni":"Harrisburg","County":"Dauphin","State":"PA","Post_Code":"17101"},"geometry":{"type":"Point","coordinates":[-76.884,40.2733]}},
	 {"type":"Feature","properties":{"FromAddr_L":101,"ToAddr_L":199,"Parity_L":"O","FromAddr_R":100,"ToAddr_R":198,"Parity_R":"E","St_PreDir":"N","St_Name":"Market","St_PosTyp":"Street",
	   "IncMuni_L":"Harrisburg","IncMuni_R":"Harrisburg","State_L":"PA","State_R":"PA","MSAGComm_L":"HARRISBURG CITY","MSAGComm_R":"HARRISBURG CITY"},"geometry":{"type":"LineString","coordinates":[[-76.885,40.273],[-76.883,40.274]]}}]}`
	addrs, err := lost.ParseAddressGeoJSON([]byte(gis))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.saveAddresses(&addrs); err != nil {
		t.Fatal(err)
	}
	civic := i3.CivicPIDF("pres:a@b", i3.Civic{Country: "US", State: "PA", City: "Harrisburg", Number: "120", PreDir: "N", Street: "Market", StreetType: "Street"}, time.Now())
	code, b = h.call("POST", "/i3/Gcs/v1/Geocode", jsonOf(civic))
	if code != 200 || !strings.Contains(string(b), "40.2733") {
		t.Fatalf("geocode %d %s", code, b)
	}
	nenaCheck(t, "Geocode-Conversion-Service/i3-geocode-conversion.yaml", "/Geocode", "POST", "200", b, "application/xml") // the object, as JSON
	code, b = h.call("POST", "/i3/Gcs/v1/ReverseGeocode", jsonOf(i3.PointPIDF("pres:a@b", 40.27331, -76.88401, time.Now())))
	if code != 200 || !strings.Contains(string(b), "Market") || !strings.Contains(string(b), "120") {
		t.Fatalf("reverse %d %s", code, b)
	}
	if code, b = h.call("POST", "/i3/Gcs/v1/Geocode", jsonOf(civic), "application/xml"); code != 200 || !strings.HasPrefix(string(b), "<GeodeticData><pidfLoGeo>") {
		t.Fatalf("as XML %d %s", code, b)
	}
	code, b = h.call("POST", "/i3/Mcs/v1/PidfloToMsag", jsonOf(civic))
	if code != 200 || !strings.Contains(string(b), "120 N MARKET STREET, HARRISBURG CITY, PA") {
		t.Fatalf("to MSAG %d %s", code, b)
	}
	code, b = h.call("POST", "/i3/Mcs/v1/MsagToPidfLo", jsonOf("120 N MARKET STREET, HARRISBURG CITY, PA"))
	if code != 200 || !strings.Contains(string(b), "Harrisburg") {
		t.Fatalf("from MSAG %d %s", code, b)
	}
	if code, _ := h.call("POST", "/i3/Mcs/v1/MsagToPidfLo", jsonOf("9999 NOWHERE RD, X, PA")); code != 468 {
		t.Fatal("no such MSAG address")
	}

	// The ADR.
	xmlBlock := `<sub:EmergencyCallData.SubscriberInfo xmlns:sub="urn:ietf:params:xml:ns:EmergencyCallData:SubscriberInfo" privacyRequested="false"><sub:SubscriberData><fn>Pat Example</fn></sub:SubscriberData></sub:EmergencyCallData.SubscriberInfo>`
	if code, out := c.do("POST", "/api/core/adr", map[string]any{"caller": "tel:+17175550100", "type": "EmergencyCallData.SubscriberInfo", "xml": xmlBlock}); code != 200 {
		t.Fatalf("adr add %d %v", code, out)
	}
	code, b = h.call("GET", "/i3/Adr/v1/AdditionalData?callerUri="+url.QueryEscape("sip:+1-717-555-0100@carrier.example.net;user=phone"), nil)
	if code != 200 || !strings.Contains(string(b), "Pat Example") {
		t.Fatalf("adr %d %s", code, b)
	}
	nenaCheck(t, "additional-data-repository/i3-adr.yaml", "/AdditionalData", "GET", "200", b, "application/xml")
	code, b = h.call("GET", "/i3/Adr/v1/AdditionalData?callerUri="+url.QueryEscape("tel:+17175550100"), nil, "application/json")
	if code != 200 {
		t.Fatal("by reference")
	}
	nenaCheck(t, "additional-data-repository/i3-adr.yaml", "/AdditionalData", "GET", "200", b, "application/json")
	if code, _ := h.call("GET", "/i3/Adr/v1/AdditionalData?callerUri="+url.QueryEscape("tel:+19995550100"), nil); code != 404 {
		t.Fatal("someone else's data")
	}

	// The Service/Agency Locator: this agency's own record.
	code, b = h.call("GET", "/i3/ServiceAgencyLocator/v1/LocatorRecordUris?serviceAgencyName=state.example.gov", nil)
	if code != 200 {
		t.Fatalf("locator %d %s", code, b)
	}
	nenaCheck(t, "service-agency-locator/i3-service-agency-locator.yaml", "/LocatorRecordUris", "GET", "200", b)
	code, b = h.call("GET", "/i3/ServiceAgencyLocator/v1/NameSets", nil)
	if code != 200 {
		t.Fatal("namesets")
	}
	nenaCheck(t, "service-agency-locator/i3-service-agency-locator.yaml", "/NameSets", "GET", "200", b)
	code, b = h.call("GET", "/i3/ServiceAgencyLocator/v1/LocatorRecords/self", nil)
	var rec map[string]any
	json.Unmarshal(b, &rec)
	if code != 200 || rec["loggerUri"] != ts.URL+"/i3log" && rec["loggerUri"] != "" || rec["serviceAgencyId"] != "state.example.gov" {
		t.Fatalf("record %d %s", code, b)
	}

	// Dequeue registration.
	const dq = "dequeue-registration-service/i3-dequeue.yaml"
	reg := map[string]any{"queueUri": "sip:sos@psap.county.example.gov", "dequeuerUri": "sip:position3@psap.county.example.gov", "expirationTime": 600, "dequeuePreference": 4}
	nenaCheck(t, dq, "/Registrations", "PUT", "request", jsonOf(reg))
	code, b = h.call("PUT", "/i3/DequeueRegistration/v1/Registrations", jsonOf(reg))
	if code != 200 {
		t.Fatalf("register %d %s", code, b)
	}
	nenaCheck(t, dq, "/Registrations", "PUT", "200", b)
	if ds := s.DequeuersFor("sip:sos@psap.county.example.gov"); len(ds) != 1 || ds[0].URI != "sip:position3@psap.county.example.gov" {
		t.Fatalf("dequeuers %+v", ds)
	}
	reg["dequeuePreference"] = 9
	if code, _ := h.call("PUT", "/i3/DequeueRegistration/v1/Registrations", jsonOf(reg)); code != 457 {
		t.Fatal("a preference out of range")
	}
	reg["dequeuePreference"], reg["queueUri"] = 4, "sip:other@x"
	if code, _ := h.call("PUT", "/i3/DequeueRegistration/v1/Registrations", jsonOf(reg)); code != 456 {
		t.Fatal("not our queue")
	}
	reg["queueUri"], reg["expirationTime"] = "sip:sos@psap.county.example.gov", 0
	h.call("PUT", "/i3/DequeueRegistration/v1/Registrations", jsonOf(reg))
	if len(s.DequeuersFor("sip:sos@psap.county.example.gov")) != 0 {
		t.Fatal("not deregistered")
	}

	// Test call requests.
	const tc = "test-call-service/i3-test-call.yaml"
	treq := map[string]any{"psapId": "psap.county.example.gov", "location": civic, "frequency": 60, "discrepancyRateLimit": 2,
		"startDate": time.Now().UTC().Format(time.RFC3339), "endDate": time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)}
	nenaCheck(t, tc, "/SendCallRequests/{psapId}", "PUT", "request", jsonOf(treq))
	if code, b := h.call("PUT", "/i3/TestCall/v1/SendTestCalls/psap.county.example.gov", jsonOf(treq)); code != 200 {
		t.Fatalf("test calls %d %s", code, b)
	}
	if _, out := c.do("GET", "/api/core/testcalls", nil); len(out["items"].([]any)) != 1 {
		t.Fatal("not listed")
	}
	treq["frequency"] = 1
	if code, _ := h.call("PUT", "/i3/TestCall/v1/SendCallRequests/psap.county.example.gov", jsonOf(treq)); code != 458 {
		t.Fatal("too often")
	}
	if code, _ := h.call("PUT", "/i3/TestCall/v1/SendCallRequests/nobody.example.gov", jsonOf(treq)); code != 442 {
		t.Fatal("unknown PSAP")
	}

	// The BCF's bad actor service.
	if code, _ := h.call("POST", "/i3/BadActor/v1/BadActors", jsonOf("nobody@bcf.example.gov")); code != 433 {
		t.Fatal("an unknown source")
	}
}
