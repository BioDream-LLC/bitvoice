package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
)

// A FreePBX 17 backup, as Backup & Restore writes it, moved over whole: previewed, every item
// applied, and the result is a working configuration (it passed the same checks as any change).
func TestImportFreePBXBackup(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	// A new server, as a migration starts: set up with one extension of its own and no phone line.
	setup := setupBody()
	setup["extensions"] = []map[string]any{{"number": "199", "name": "IT desk"}}
	delete(setup, "trunk")
	if code, out := admin.do("POST", "/api/setup/complete", setup); code != 200 {
		t.Fatal("setup", out)
	}
	const name = "20261001-100000-1790000000-17.0.19-1234567.tar.gz"
	backup, err := os.ReadFile("../importer/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	preview := func() map[string]any {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		f, _ := mw.CreateFormFile("file", name)
		f.Write(backup)
		mw.Close()
		req, _ := http.NewRequest("POST", ts.URL+"/api/import/preview", &b)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("X-BitVoice", "1")
		resp, err := admin.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode != 200 {
			t.Fatalf("preview %d %v", resp.StatusCode, out)
		}
		return out
	}
	p := preview()
	res := p["result"].(map[string]any)
	if !strings.HasPrefix(res["format"].(string), "FreePBX backup") {
		t.Fatalf("format %v", res["format"])
	}
	body := map[string]any{}
	for _, k := range []string{"extensions", "trunks", "ringGroups", "queues", "inboundRoutes", "outboundRoutes"} {
		body[k] = res[k]
	}
	code, out := admin.do("POST", "/api/import/apply", body)
	// 3 extensions, 2 trunks, 3 outbound routes, 2 groups, 1 queue, 4 incoming routes.
	if code != 200 || out["added"].(float64) != 15 {
		t.Fatalf("apply %d %v", code, out)
	}
	cfg, _ := s.Store.Config()
	if x := cfg.FindExtension("100"); x == nil || x.Secret != "e1f2a3b4c5d6e7f8a9b0" || x.Voicemail.PIN != "4321" {
		t.Fatalf("100 %+v", x)
	}
	if x := cfg.FindExtension("101"); x == nil || len(x.Secret) < 12 {
		t.Fatalf("101 should have a new password: %+v", x)
	}
	g := cfg.FindRingGroup("600")
	if g == nil || g.NoAnswer == nil || g.NoAnswer.Type != "voicemail" || g.NoAnswer.Target != "100" {
		t.Fatalf("600 %+v", g)
	}
	q := cfg.FindQueue("400")
	if q == nil || q.Strategy != "round-robin" || q.Penalties["102"] != 3 || q.Timeout != nil {
		t.Fatalf("400 %+v", q)
	}
	routes := map[string]string{}
	for _, r := range cfg.InboundRoutes {
		routes[r.DID] = r.Destination.Type + " " + r.Destination.Target
	}
	if routes["3125550100"] != "ringgroup 600" || routes["3125550102"] != "queue 400" || routes[""] != "hangup " {
		t.Fatalf("inbound %v", routes)
	}
	carrier := ""
	for _, tr := range cfg.Trunks {
		if tr.Name == "Carrier" {
			carrier = tr.ID
		}
	}
	found := false
	for _, r := range cfg.OutboundRoutes {
		if r.Name == "National (2)" && r.Patterns[0] == "9NXXNXXXXXX" && r.Strip == 1 && r.Prepend == "1" && r.Trunks[0] == carrier {
			found = true
		}
	}
	if !found {
		t.Fatalf("outbound %+v", cfg.OutboundRoutes)
	}
	// The same backup again: everything is already here.
	p = preview()
	if ex := p["exists"].(map[string]any); !ex["100"].(bool) || !ex["queue:400"].(bool) || !ex["group:600"].(bool) || !ex["trunk:Carrier"].(bool) {
		t.Fatalf("exists %v", ex)
	}
	if code, out = admin.do("POST", "/api/import/apply", body); code != 200 || out["added"].(float64) != 0 {
		t.Fatalf("again %d %v", code, out)
	}
}

// A real FreePBX backup (BITVOICE_TEST_FREEPBX_BACKUP; one made by FreePBX 17.0.33 Backup &
// Restore is kept in the test oracles): read as a stream (real backups are mostly sound files and
// call history), previewed and applied in one go; the result is a configuration that passes the
// same checks as any change, every extension with a password, and every incoming route reaching
// something imported.
func TestImportRealFreePBXBackup(t *testing.T) {
	p := os.Getenv("BITVOICE_TEST_FREEPBX_BACKUP")
	if p == "" {
		t.Skip("BITVOICE_TEST_FREEPBX_BACKUP is not set")
	}
	backup, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	setup := setupBody()
	setup["extensions"] = []map[string]any{{"number": "199", "name": "IT desk"}}
	delete(setup, "trunk")
	if code, out := admin.do("POST", "/api/setup/complete", setup); code != 200 {
		t.Fatal("setup", out)
	}
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	f, _ := mw.CreateFormFile("file", "backup.tar.gz")
	f.Write(backup)
	mw.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/import/preview", &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-BitVoice", "1")
	resp, err := admin.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 200 {
		t.Fatalf("preview %d %v", resp.StatusCode, out)
	}
	res := out["result"].(map[string]any)
	body := map[string]any{}
	n := 0
	for _, k := range []string{"extensions", "trunks", "ringGroups", "queues", "inboundRoutes", "outboundRoutes"} {
		body[k] = res[k]
		if l, ok := res[k].([]any); ok {
			n += len(l)
		}
	}
	if n == 0 {
		t.Fatalf("nothing to import: %v", res)
	}
	code, applied := admin.do("POST", "/api/import/apply", body)
	if code != 200 || applied["added"].(float64) != float64(n) {
		t.Fatalf("apply %d %v (want %d added)", code, applied, n)
	}
	cfg, _ := s.Store.Config()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the imported configuration does not validate: %v", err)
	}
	for _, x := range cfg.Extensions {
		if len(x.Secret) < 8 {
			t.Errorf("extension %s has no usable password", x.Number)
		}
	}
	for _, r := range cfg.InboundRoutes {
		if r.Destination.Type == "" {
			t.Errorf("incoming route %s reaches nothing", r.Name)
		}
	}
	t.Logf("%s: %d items imported", res["format"], n)
}
