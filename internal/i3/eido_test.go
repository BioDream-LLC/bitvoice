package i3

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

// EIDOs checked against NENA's published EIDO schema (NENA911/EIDO-JSON, Schema/openapi.yaml), by a
// validator kept outside the repo: BITVOICE_TEST_EIDO names a directory holding validate.js (ajv)
// and the schema as nena/EIDO-JSON/Schema/openapi.yaml beside it.
func TestEIDOAgainstNENASchema(t *testing.T) {
	dir := os.Getenv("BITVOICE_TEST_EIDO")
	if dir == "" {
		t.Skip("BITVOICE_TEST_EIDO not set")
	}
	loc, _ := ParsePIDF([]byte(civicPIDF))
	now := time.Date(2026, 10, 12, 13, 1, 0, 0, time.UTC)
	full := Incident{ID: "abc123", Agency: "psap.example.gov", Element: "bitvoice.psap.example.gov", Queue: "911",
		From: "+17175550100", Agent: "jlee", Position: "101", Start: now.Add(-40 * time.Second), Answer: now.Add(-30 * time.Second),
		Location: &loc, LocationRaw: []byte(civicPIDF), Additional: Additional{Providers: []Provider{{Name: "Example"}}},
		Comments: []string{"caller on 2nd floor <b>"},
		Media: []MediaRef{{ID: "1", URL: "https://psap.example.gov/m/tok/1", Purpose: "caller-photo"},
			{ID: "live", URL: "https://psap.example.gov/whep/tok", Purpose: "caller-video-live"}}}
	for name, in := range map[string]Incident{"full": full, "bare": {ID: "x1", Agency: "psap.example.gov", Element: "e", From: "+1", Start: now}} {
		b, err := EIDO(in, now)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("node", filepath.Join(dir, "validate.js"), filepath.Join(dir, "..", "nena", "EIDO-JSON", "Schema", "openapi.yaml"), "EmergencyIncidentDataObjectType")
		cmd.Stdin = bytes.NewReader(b)
		out, err := cmd.CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "ok" {
			t.Errorf("%s: %v\n%s\n%s", name, err, out, b)
		}
	}
}
