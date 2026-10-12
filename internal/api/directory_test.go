package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
)

func TestDirectory(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	// Validation names each field.
	code, out := admin.do("POST", "/api/directory", map[string]any{"name": "", "email": "x", "numbers": []any{map[string]any{"kind": "fax", "number": "12ab"}}})
	if code != 422 || !strings.Contains(asJSON(out), "numbers[0].kind") || !strings.Contains(asJSON(out), "numbers[0].number") || !strings.Contains(asJSON(out), `"name"`) || !strings.Contains(asJSON(out), `"email"`) {
		t.Fatalf("validation %d %v", code, out)
	}
	code, c := admin.do("POST", "/api/directory", map[string]any{"name": "Acme Plumbing", "company": "Acme", "numbers": []any{map[string]any{"kind": "work", "number": "+1 (212) 555-0199"}, map[string]any{"kind": "mobile", "number": "+12125550198"}}})
	if code != 201 || c["numbers"].([]any)[0].(map[string]any)["number"] != "+12125550199" {
		t.Fatalf("add %d %v", code, c)
	}
	id := c["id"].(string)
	if code, _ := admin.do("PUT", "/api/directory/"+id, map[string]any{"name": "Acme Plumbing Ltd", "numbers": []any{map[string]any{"number": "+12125550199"}}}); code != 200 {
		t.Fatal("update")
	}
	// The caller card and caller ID know the number, written another way.
	if n := s.NameFor("2125550199"); n != "Acme Plumbing Ltd" {
		t.Fatalf("name for: %q", n)
	}
	cfg, _ := s.Store.Config()
	if who, _ := s.lookup(context.Background(), cfg, "+1 212 555 0199"); who == nil || who.Source != "directory" {
		t.Fatalf("lookup %+v", who)
	}
	// CSV import: check, then add (a matching name updates), with a formula-looking cell escaped on export.
	csvBody := "Name,Company,Phone,Mobile,Email\nAcme Plumbing Ltd,Acme,+12125550100,,\nBob Smith,=HYPERLINK(1),02071234567,07700900123,bob@example.com\n,,,,\n"
	post := func(q, body string) (int, string) {
		req, _ := http.NewRequest("POST", ts.URL+"/api/directory/import"+q, strings.NewReader(body))
		req.Header.Set("X-BitVoice", "1")
		resp, err := admin.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, b := post("?check=1", csvBody); code != 200 || !strings.Contains(b, `"contacts":2`) {
		t.Fatalf("check %d %s", code, b)
	}
	if code, b := post("", "Name,Phone\nNo number,\nBad,12ab\n"); code != 422 || !strings.Contains(b, "line 2") || !strings.Contains(b, "line 3") {
		t.Fatalf("bad import %d %s", code, b)
	}
	if code, b := post("", csvBody); code != 200 || !strings.Contains(b, `"added":1`) || !strings.Contains(b, `"updated":1`) {
		t.Fatalf("import %d %s", code, b)
	}
	if got := len(s.Store.Directory()); got != 2 {
		t.Fatalf("%d contacts", got)
	}
	resp, _ := admin.http.Get(ts.URL + "/api/directory.csv")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "'=HYPERLINK(1)") || !strings.Contains(string(b), "07700900123") {
		t.Fatalf("export %s", b)
	}
	// Phones get the directory: extensions and contacts.
	if _, _, err := s.Store.Update("test", "phones", 0, func(cfg *config.Config) error {
		cfg.System.Provisioning = config.Provisioning{Enabled: true, Username: "phones", Password: "a-long-provisioning-pw"}
		cfg.Devices = []config.Device{{MAC: "805ec0112233", Vendor: "yealink", Extension: "100"}, {MAC: "000413aabbcc", Vendor: "snom", Extension: "101"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fetch := func(name string) string {
		req, _ := http.NewRequest("GET", ts.URL+"/provision/"+name, nil)
		req.SetBasicAuth("phones", "a-long-provisioning-pw")
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s: %v %v", name, err, resp)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if y := fetch("805ec0112233.cfg"); !strings.Contains(y, "remote_phonebook.data.1.url = http://phones:a-long-provisioning-pw@") || !strings.Contains(y, "/provision/phonebook-yealink.xml") {
		t.Fatalf("yealink config:\n%s", y)
	}
	if pb := fetch("phonebook-yealink.xml"); !strings.Contains(pb, "<Name>Front desk</Name><Telephone>100</Telephone>") || !strings.Contains(pb, "<Name>Bob Smith (mobile)</Name><Telephone>07700900123</Telephone>") {
		t.Fatalf("yealink book:\n%s", pb)
	}
	if sn := fetch("000413aabbcc.xml"); !strings.Contains(sn, "<tbook") || !strings.Contains(sn, "<name>Acme Plumbing Ltd</name><number>+12125550100</number>") {
		t.Fatalf("snom:\n%s", sn)
	}
	if g := fetch("phonebook.xml"); !strings.Contains(g, "<FirstName>Dr Lee</FirstName>") {
		t.Fatalf("grandstream book:\n%s", g)
	}
	if p := fetch("000000000000-directory.xml"); !strings.Contains(p, "<fn>Bob Smith (work)</fn><ln></ln><ct>02071234567</ct>") {
		t.Fatalf("polycom book:\n%s", p)
	}
	if code, _ := admin.do("DELETE", "/api/directory/"+id, nil); code != 204 {
		t.Fatal("delete")
	}
	if code, _ := admin.do("DELETE", "/api/directory/"+id, nil); code != 404 {
		t.Fatal("delete twice")
	}
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
