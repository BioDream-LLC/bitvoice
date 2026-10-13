package store

import (
	"encoding/json"
	"errors"
	"github.com/biodream-llc/bitvoice/internal/dbtest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

func TestConfigRevisionsAndRevert(t *testing.T) {
	dir, spec := t.TempDir(), dbtest.Spec(t)
	s, err := OpenWith(dir, spec)
	if err != nil {
		t.Fatal(err)
	}
	_, rev := s.Config()
	if rev != 1 {
		t.Fatalf("fresh install revision %d", rev)
	}
	changed := 0
	s.OnChange(func(config.Config) { changed++ })
	_, rev2, err := s.Update("admin", "Add 100", rev, func(c *config.Config) error {
		c.Extensions = append(c.Extensions, config.Extension{Number: "100", Name: "Desk", Enabled: true})
		return nil
	})
	if err != nil || rev2 != 2 || changed != 1 {
		t.Fatalf("update: %v rev %d changed %d", err, rev2, changed)
	}
	if _, _, err := s.Update("admin", "stale", rev, func(*config.Config) error { return nil }); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision accepted: %v", err)
	}
	if _, _, err := s.Update("admin", "bad", 0, func(c *config.Config) error {
		c.Extensions = append(c.Extensions, config.Extension{Number: "100", Name: "Dup"})
		return nil
	}); err == nil {
		t.Fatal("invalid change saved")
	}

	// Reopen: state survives.
	s.Close()
	s2, err := OpenWith(dir, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close() // Windows cannot remove the test's folder while the database file is open
	c, rev := s2.Config()
	if rev != 2 || len(c.Extensions) != 1 || c.Extensions[0].Secret == "" {
		t.Fatalf("after reopen rev %d %+v", rev, c.Extensions)
	}
	if _, err := s2.Revert("admin", 1); err != nil {
		t.Fatal(err)
	}
	c, rev = s2.Config()
	if rev != 3 || len(c.Extensions) != 0 {
		t.Fatalf("revert: rev %d %d extensions", rev, len(c.Extensions))
	}
	h, _ := s2.History(10)
	if len(h) != 3 || h[0].Summary != "Restored revision 1" {
		t.Fatalf("history %+v", h)
	}
}

func TestUsers(t *testing.T) {
	s, err := OpenTest(t)
	if err != nil {
		t.Fatal(err)
	}
	if s.HasUsers() {
		t.Fatal("fresh store has users")
	}
	if err := s.PutUser(User{Username: "Admin", Role: "admin"}, "short"); err == nil {
		t.Fatal("weak password accepted")
	}
	if err := s.PutUser(User{Username: "Admin", Name: "Admin", Role: "admin"}, "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Authenticate("admin", "wrong password!"); ok {
		t.Fatal("wrong password accepted")
	}
	u, ok := s.Authenticate("ADMIN", "correct horse battery")
	if !ok || u.Hash != "" || u.Role != "admin" {
		t.Fatalf("login: %+v %v", u, ok)
	}
	if err := s.DeleteUser("admin"); err == nil {
		t.Fatal("deleted the last administrator")
	}
}

func TestCDRs(t *testing.T) {
	s, err := OpenTest(t)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		dir := "internal"
		if i%2 == 0 {
			dir = "inbound"
		}
		if err := s.AddCDR(CDR{ID: config.NewID(), Start: base.Add(time.Duration(i) * time.Hour), From: "100", To: "10" + string(rune('1'+i)), Direction: dir}); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := s.CDRs(CDRQuery{})
	if len(all) != 5 || !all[0].Start.After(all[4].Start) {
		t.Fatalf("order %+v", all)
	}
	in, _ := s.CDRs(CDRQuery{Direction: "inbound"})
	if len(in) != 3 {
		t.Fatalf("inbound %d", len(in))
	}
	q, _ := s.CDRs(CDRQuery{Search: "103"})
	if len(q) != 1 {
		t.Fatalf("search %d", len(q))
	}
}

func TestVoicemail(t *testing.T) {
	s, err := OpenTest(t)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	v, err := s.AddVoicemail(Voicemail{Box: "100", From: "101", At: at, Seconds: 3}, []byte("RIFFaudio"))
	if err != nil {
		t.Fatal(err)
	}
	s.AddVoicemail(Voicemail{Box: "100", From: "102", At: at.Add(time.Second)}, []byte("x"))
	list, _ := s.Voicemails("100")
	if len(list) != 2 || list[0].From != "102" {
		t.Fatalf("%+v", list)
	}
	if n, o := s.VoicemailCounts("100"); n != 2 || o != 0 {
		t.Fatal(n, o)
	}
	if _, err := s.SetHeard("100", v.ID, true); err != nil {
		t.Fatal(err)
	}
	if n, o := s.VoicemailCounts("100"); n != 1 || o != 1 {
		t.Fatal(n, o)
	}
	if b, _ := s.VoicemailAudio("100", v.ID); string(b) != "RIFFaudio" {
		t.Fatal(string(b))
	}
	if _, err := s.VoicemailAudio("../100", v.ID); err != ErrNotFound {
		t.Fatal("path escape not refused")
	}
	if err := s.DeleteVoicemail("100", v.ID); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.Voicemails(""); len(all) != 1 {
		t.Fatal(len(all))
	}
	if _, err := s.Greeting("100"); err != ErrNotFound {
		t.Fatal(err)
	}
	s.SetGreeting("100", []byte("hello"))
	if b, _ := s.Greeting("100"); string(b) != "hello" || len(s.Greetings()) != 1 {
		t.Fatal("greeting")
	}
	s.SetGreeting("100", nil)
	if _, err := s.Greeting("100"); err != ErrNotFound {
		t.Fatal(err)
	}
}

func TestRecordings(t *testing.T) {
	s, _ := OpenTest(t)
	name, err := s.AddRecording("abc@host/../x", time.Now(), []byte("RIFF"))
	if err != nil {
		t.Fatal(err)
	}
	if b, err := s.Recording(name); err != nil || string(b) != "RIFF" {
		t.Fatal(name, err)
	}
	if _, err := s.Recording("../../config"); err != ErrNotFound {
		t.Fatal("escape allowed")
	}
	if s.PruneRecordings(30) != 0 {
		t.Fatal("new recording pruned")
	}
	if err := s.DeleteRecording(name); err != nil {
		t.Fatal(err)
	}
}

func TestCheckPassword(t *testing.T) {
	for p, ok := range map[string]bool{
		"short": false, "aaaaaaaaaaaaaaaa": false, "abababababab": false, "Password12": false, "1234567890": false,
		"a very good password": true, "correct horse battery": true, "Tr0ub4dor&3x": true,
	} {
		if got := CheckPassword(p) == nil; got != ok {
			t.Errorf("%q: accepted=%v", p, got)
		}
	}
}

func TestLastAdminKept(t *testing.T) {
	s, err := OpenTest(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutUser(User{Username: "boss", Role: "admin"}, "a very good password"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutUser(User{Username: "boss", Role: "viewer"}, ""); err == nil {
		t.Fatal("the only administrator was demoted")
	}
	if err := s.PutUser(User{Username: "<b>x", Role: "viewer"}, "a very good password"); err == nil {
		t.Fatal("bad username accepted")
	}
	if err := s.PutUser(User{Username: "two", Role: "admin"}, "a very good password"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutUser(User{Username: "boss", Role: "viewer"}, ""); err != nil {
		t.Fatal(err)
	}
}

// An older data directory's files come into the database on first start, and are moved aside.
func TestImportFiles(t *testing.T) {
	dir := t.TempDir()
	w := func(name, body string) {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o700)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Extensions = []config.Extension{{Number: "100", Name: "Desk", Enabled: true, Secret: "s3cret-s3cret-1"}}
	r1, _ := json.Marshal(Revision{Rev: 1, Actor: "system", Summary: "New installation", Config: config.Default()})
	r2, _ := json.Marshal(Revision{Rev: 2, Actor: "admin", Summary: "Add 100", Config: cfg})
	w("revisions/000001.json", string(r1))
	w("revisions/000002.json", string(r2))
	w("config.json", string(r2))
	w("state.json", `{"instanceId":"abc","setupComplete":true}`)
	w("users.json", `[{"username":"admin","name":"Admin","role":"admin","hash":"x"}]`)
	w("directory.json", `[{"id":"c1","name":"Acme","numbers":["+12125550123"]}]`)
	w("cdr/2026-09.jsonl", `{"id":"a","start":"2026-09-01T10:00:00Z","direction":"inbound","from":"+12125550123","to":"100"}`+"\n"+
		`{"id":"b","start":"2026-09-02T10:00:00Z","direction":"outbound","from":"100","to":"+442071234567"}`+"\nnot json\n")
	w("audit.jsonl", `{"at":"2026-09-01T10:00:00Z","actor":"admin","action":"login"}`+"\n")
	w("texts/2026-09.jsonl", `{"id":"t1","at":"2026-09-01T10:00:00Z","direction":"in","our":"+12125550100","other":"+12125550123","body":"hi"}`+"\n")
	w("texts/state.json", `{"optedOut":{"+12125550100|+12125550199":true}}`)
	w("recordings/2026-09/a.wav", "RIFF")
	w("recordings/2026-09/a.json", `{"recording":"2026-09/a","status":"done","at":"2026-09-01T10:00:00Z","summary":"Leaking tap","lines":[{"second":1,"side":"caller","text":"my tap leaks"}]}`)

	spec := dbtest.Spec(t)
	s, err := OpenWith(dir, spec)
	if err != nil {
		t.Fatal(err)
	}
	if c, rev := s.Config(); rev != 2 || len(c.Extensions) != 1 {
		t.Fatalf("config rev %d %+v", rev, c.Extensions)
	}
	if h, _ := s.History(0); len(h) != 2 {
		t.Fatalf("history %d", len(h))
	}
	if st := s.State(); st.InstanceID != "abc" || !st.SetupComplete {
		t.Fatalf("state %+v", st)
	}
	if !s.HasUsers() || len(s.Directory()) != 1 {
		t.Fatal("users or contacts not imported")
	}
	if cs, _ := s.CDRs(CDRQuery{}); len(cs) != 2 || cs[0].ID != "b" {
		t.Fatalf("cdrs %+v", cs)
	}
	if hist, _ := s.CDRsFor("+12125550123", 10); len(hist) != 1 {
		t.Fatalf("history for a number %+v", hist)
	}
	if a := s.AuditLog(0); len(a) != 1 || a[0].Action != "login" {
		t.Fatalf("audit %+v", a)
	}
	if th := s.Thread("+12125550100", "+12125550123"); len(th) != 1 || !s.OptedOut("+12125550100", "+12125550199") {
		t.Fatalf("texts %+v", th)
	}
	if tr := s.SearchTranscripts("TAP leak", 5); len(tr) != 1 {
		t.Fatalf("transcript search %+v", tr)
	}
	// The files moved aside; the audio stayed.
	for _, gone := range []string{"users.json", "config.json", "cdr", "audit.jsonl", "texts/2026-09.jsonl", "recordings/2026-09/a.json"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); err == nil {
			t.Errorf("%s still in place", gone)
		}
		if _, err := os.Stat(filepath.Join(dir, "pre-database", gone)); err != nil {
			t.Errorf("%s not kept in pre-database: %v", gone, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "recordings/2026-09/a.wav")); err != nil {
		t.Fatal("the recording moved")
	}
	// A second start imports nothing again, even with the old files put back.
	s.Close()
	w("cdr/2026-09.jsonl", `{"id":"z","start":"2026-09-03T10:00:00Z","direction":"inbound","from":"1","to":"100"}`+"\n")
	s2, err := OpenWith(dir, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if cs, _ := s2.CDRs(CDRQuery{}); len(cs) != 2 {
		t.Fatalf("imported twice: %d records", len(cs))
	}
}

// Two servers sharing one PostgreSQL: changes saved at once never overwrite each other (one is told to
// reload), and each sees the other's change after Sync.
func TestSharedDatabase(t *testing.T) {
	spec := dbtest.Spec(t)
	if spec == "" {
		t.Skip("set BITVOICE_TEST_POSTGRES")
	}
	a, err := OpenWith(t.TempDir(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := OpenWith(t.TempDir(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	add := func(s *Store, n string) error {
		_, _, err := s.Update("admin", "add "+n, 0, func(c *config.Config) error {
			c.Extensions = append(c.Extensions, config.Extension{Number: n, Name: "x" + n, Enabled: true, Secret: "s3cret-s3cret-" + n})
			return nil
		})
		return err
	}
	if err := add(a, "100"); err != nil {
		t.Fatal(err)
	}
	// b has not synced: its save would be built on the old configuration, so it is refused.
	if err := add(b, "101"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	if !b.Sync() {
		t.Fatal("b did not pick up a's change")
	}
	if err := add(b, "101"); err != nil {
		t.Fatal(err)
	}
	a.Sync()
	if c, _ := a.Config(); len(c.Extensions) != 2 {
		t.Fatalf("a sees %d extensions", len(c.Extensions))
	}
}
