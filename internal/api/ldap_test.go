package api

import (
	"os"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// The company directory against OpenLDAP (~/.bvtest/ldap; BITVOICE_TEST_LDAP and BITVOICE_TEST_LDAP_CA):
// Test reads people without changing anything, Sync now makes Bob's extension and fills the ring
// group as one revision, and people sign in with their directory passwords as the groups say.
func TestCompanyDirectory(t *testing.T) {
	addr, ca := os.Getenv("BITVOICE_TEST_LDAP"), os.Getenv("BITVOICE_TEST_LDAP_CA")
	if addr == "" {
		t.Skip("set BITVOICE_TEST_LDAP to the OpenLDAP test server")
	}
	pem, err := os.ReadFile(ca)
	if err != nil {
		t.Fatal(err)
	}
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup", code)
	}
	dir := config.LDAP{Enabled: true, URL: "ldap://" + addr, StartTLS: true, CACert: string(pem), BindDN: "cn=admin,dc=example,dc=org",
		BindPassword: "admin-password", BaseDN: "dc=example,dc=org", SyncExtensions: true, NumberDigits: 3,
		SignIn: true, AdminGroups: []string{"pbx-admins"}, ViewerGroups: []string{"sales"}}
	if code, out := admin.do("POST", "/api/ldap/test", dir); code != 200 || out["ok"] != true || out["people"] != float64(1203) || out["withNumber"] != float64(2) || out["secure"] != true {
		t.Fatalf("test %d %v", code, out)
	}
	bad := dir
	bad.BindPassword = "wrong"
	if _, out := admin.do("POST", "/api/ldap/test", bad); out["ok"] != false {
		t.Fatalf("a wrong password tested fine: %v", out)
	}
	if _, _, err := s.Store.Update("test", "directory", 0, func(c *config.Config) error {
		c.RingGroups = append(c.RingGroups, config.RingGroup{ID: "rg", Number: "650", Name: "Sales", RingSeconds: 20, Members: []string{"100"}})
		dir.RingGroups = []config.LDAPGroup{{Group: "sales", RingGroup: "650"}}
		c.System.LDAP = dir
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The password is kept when the screen sends the settings back without it.
	_, set := admin.do("GET", "/api/settings", nil)
	sys := set["system"].(map[string]any)
	sys["ldap"].(map[string]any)["bindPassword"] = ""
	if code, out := admin.do("PUT", "/api/settings", map[string]any{"system": sys, "security": set["security"], "rev": set["rev"]}); code != 200 {
		t.Fatalf("save %d %v", code, out)
	}
	if c, _ := s.Store.Config(); c.System.LDAP.BindPassword != "admin-password" {
		t.Fatal("the reading account's password was lost on save")
	}
	code, rep := admin.do("POST", "/api/ldap/sync", nil)
	if code != 200 || rep["error"] != nil && rep["error"] != "" || len(rep["created"].([]any)) != 2 {
		t.Fatalf("sync %d %v", code, rep)
	}
	c, _ := s.Store.Config()
	if x := c.FindExtension("202"); x == nil || x.LDAP != "bob" {
		t.Fatalf("bob's extension %+v", x)
	}
	if g := c.FindRingGroup("650"); len(g.Members) != 2 {
		t.Fatalf("sales %v", g.Members)
	}
	if _, again := admin.do("POST", "/api/ldap/sync", nil); again["changed"] != false {
		t.Fatalf("a second sync changed things: %v", again)
	}

	// Sign-in: Alice is an administrator, Bob a viewer; a wrong password and an injected name fail.
	alice := newClient(t, ts.URL)
	if code, me := alice.do("POST", "/api/auth/login", map[string]string{"username": "alice", "password": "alice-password"}); code != 200 || me["role"] != "admin" {
		t.Fatalf("alice %d %v", code, me)
	}
	bob := newClient(t, ts.URL)
	if code, me := bob.do("POST", "/api/auth/login", map[string]string{"username": "bob", "password": "bob-password"}); code != 200 || me["role"] != "viewer" {
		t.Fatalf("bob %d %v", code, me)
	}
	if code, _ := bob.do("PUT", "/api/settings", map[string]any{}); code != 403 {
		t.Fatalf("a viewer changed settings: %d", code)
	}
	for _, try := range [][2]string{{"bob", "wrong"}, {"carol", "carol-password"}, {"*", "bob-password"}, {"bob", ""}} {
		if code, _ := newClient(t, ts.URL).do("POST", "/api/auth/login", map[string]string{"username": try[0], "password": try[1]}); code != 401 {
			t.Errorf("%q signed in: %d", try, code)
		}
	}
}
