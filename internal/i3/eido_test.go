package i3

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEIDO(t *testing.T) {
	loc, _ := ParsePIDF([]byte(civicPIDF))
	now := time.Date(2026, 10, 12, 13, 1, 0, 0, time.UTC)
	b, err := EIDO(Incident{ID: "abc123", Agency: "psap.example.gov", Element: "bitvoice.psap.example.gov", Queue: "911",
		From: "+17175550100", Agent: "jlee", Position: "101", Start: now.Add(-40 * time.Second), Answer: now.Add(-30 * time.Second),
		Location: &loc, LocationRaw: []byte(civicPIDF), Additional: Additional{Providers: []Provider{{Name: "Example"}}}, Comments: []string{"caller on 2nd floor"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if d["$id"] != "urn:emergency:uid:incidentid:abc123:bitvoice.psap.example.gov" || d["eidoVersion"] != "1.0" {
		t.Fatalf("%v", d["$id"])
	}
	call := d["callComponent"].([]any)[0].(map[string]any)
	if call["answerDate"] != "2026-10-12T13:00:30.000Z" || call["direction"] != "incoming" {
		t.Fatalf("call %v", call)
	}
	for _, k := range []string{"locationComponent", "agentComponent", "callbackComponent", "additionalDataComponent", "notesComponent"} {
		if d[k] == nil {
			t.Errorf("no %s", k)
		}
	}
	if agent := d["agentComponent"].([]any)[0].(map[string]any); agent["$id"] != "jlee@psap.example.gov" {
		t.Fatalf("agent %v", agent)
	}
}

func TestALISpill(t *testing.T) {
	loc, _ := ParsePIDF([]byte(circlePIDF))
	c, _ := ParsePIDF([]byte(civicPIDF))
	loc.Civic = c.Civic
	b := string(ALI{Position: "101", Callback: "+17175550100", Time: time.Date(2026, 10, 12, 9, 0, 5, 0, time.UTC), Class: "WRLS", Location: &loc, Provider: "Example Wireless"}.Spill())
	if b[0] != 2 || b[len(b)-1] != 3 {
		t.Fatal("not framed")
	}
	for _, want := range []string{"POS 101  10/12 09:00:05", "(717) 555-0100  WRLS", "123 N FRONT ST", "FLR 2", "HARRISBURG PA 17101", "LAT +40.263300", "UNC 35M CONF 90%", "CO EXAMPLE WIRELESS"} {
		if !strings.Contains(b, want) {
			t.Errorf("no %q in\n%s", want, b)
		}
	}
	for _, l := range strings.Split(b[1:len(b)-1], "\r\n") {
		if len(l) > 32 {
			t.Errorf("line longer than 32: %q", l)
		}
	}
}
