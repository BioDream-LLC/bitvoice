package ldapsync

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
)

func baseConfig() config.Config {
	c := config.Default()
	c.Extensions = []config.Extension{{ID: "x1", Number: "201", Name: "Alice Admin", Enabled: true, Secret: "a-long-sip-secret"},
		{ID: "x2", Number: "300", Name: "Front desk", Enabled: true, Secret: "a-long-sip-secret"}}
	c.RingGroups = []config.RingGroup{{ID: "g1", Number: "600", Name: "Sales", Members: []string{"300"}}}
	c.System.LDAP = config.LDAP{Enabled: true, URL: "ldaps://dir.example.com", BaseDN: "dc=example,dc=org", BindDN: "cn=reader", BindPassword: "pw",
		SyncExtensions: true, DisableMissing: true, NumberDigits: 3, RingGroups: []config.LDAPGroup{{Group: "sales", RingGroup: "600"}}}
	return c
}

func TestApply(t *testing.T) {
	cfg := baseConfig()
	people := []Person{
		{Login: "alice", Name: "Alice Admin", Email: "alice@example.org", Raw: "+1 (212) 555-0201"},
		{Login: "bob", Name: "Bob Builder", Raw: "202"},
		{Login: "carol", Name: "Carol", Raw: ""},
		{Login: "dave", Name: "Dave Front", Raw: "300"}, // someone else has 300
		{Login: "erin", Name: "Erin", Raw: "202"},       // the same number as bob
	}
	for i := range people {
		people[i].Number = numberOf(people[i].Raw, 3)
	}
	var r Report
	Apply(&cfg, people, map[string][]string{"sales": {"alice", "bob", "nobody"}}, &r)
	if !r.Changed || len(r.Created) != 1 || r.Created[0] != "202 Bob Builder" || len(r.Updated) != 2 || !strings.Contains(r.Updated[0], "now from the directory") {
		t.Fatalf("report %+v", r)
	}
	if x := cfg.FindExtension("201"); x.LDAP != "alice" || x.Name != "Alice Admin" {
		t.Fatalf("alice's extension %+v", x)
	}
	if x := cfg.FindExtension("300"); x.LDAP != "" || x.Name != "Front desk" {
		t.Fatalf("an extension of someone else was changed: %+v", x)
	}
	if x := cfg.FindExtension("202"); x == nil || x.Secret == "" || !x.Voicemail.Enabled || len(x.Voicemail.PIN) != 6 || x.LDAP != "bob" {
		t.Fatalf("bob's new extension %+v", x)
	}
	if g := cfg.FindRingGroup("600"); strings.Join(g.Members, ",") != "201,202" {
		t.Fatalf("ring group %v", g.Members)
	}
	problems := strings.Join(r.Problems, "|")
	if !strings.Contains(problems, "dave: extension 300 exists") || !strings.Contains(problems, "bob and erin both have number 202") {
		t.Fatalf("problems %q", r.Problems)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the synced configuration does not validate: %v", err)
	}

	// Run again: nothing changes. Bob leaves: his extension is turned off, and leaves the ring group.
	var again Report
	Apply(&cfg, people, map[string][]string{"sales": {"alice", "bob"}}, &again)
	if again.Changed {
		t.Fatalf("a second sync changed things: %+v", again)
	}
	var gone Report
	Apply(&cfg, people[:1], map[string][]string{"sales": {"alice", "bob"}}, &gone)
	if x := cfg.FindExtension("202"); x.Enabled || len(gone.Disabled) != 1 {
		t.Fatalf("bob gone: %+v %+v", x, gone)
	}
	if g := cfg.FindRingGroup("600"); strings.Join(g.Members, ",") != "201" {
		t.Fatalf("ring group after bob left %v", g.Members)
	}
	// Bob is back, with a new number: on again, renumbered.
	people[1].Raw, people[1].Number = "212", "212"
	var back Report
	Apply(&cfg, people[:2], map[string][]string{"sales": {"alice", "bob"}}, &back)
	if x := cfg.FindExtension("212"); x == nil || !x.Enabled || len(back.Enabled) != 1 {
		t.Fatalf("bob back: %+v %+v", x, back)
	}
}

func TestNumberOf(t *testing.T) {
	for raw, want := range map[[2]any]string{{"+1 (212) 555-0123", 4}: "0123", {"202", 0}: "202", {"x", 0}: "", {"1", 0}: "",
		{"+12125550123", 0}: "", {"٣٣٣", 0}: ""} {
		if got := numberOf(raw[0].(string), raw[1].(int)); got != want {
			t.Errorf("%v: %q, want %q", raw, got, want)
		}
	}
}

// Against OpenLDAP (~/.bvtest/ldap): BITVOICE_TEST_LDAP=127.0.0.1:1389 and BITVOICE_TEST_LDAP_CA.
func TestSyncAndSignInWithOpenLDAP(t *testing.T) {
	addr, ca := os.Getenv("BITVOICE_TEST_LDAP"), os.Getenv("BITVOICE_TEST_LDAP_CA")
	if addr == "" {
		t.Skip("set BITVOICE_TEST_LDAP to the OpenLDAP test server")
	}
	pem, err := os.ReadFile(ca)
	if err != nil {
		t.Fatal(err)
	}
	cfg := baseConfig()
	cfg.System.LDAP.URL, cfg.System.LDAP.StartTLS, cfg.System.LDAP.CACert = "ldap://"+addr, true, string(pem)
	cfg.System.LDAP.BindDN, cfg.System.LDAP.BindPassword = "cn=admin,dc=example,dc=org", "admin-password"
	cfg.System.LDAP.SignIn, cfg.System.LDAP.AdminGroups, cfg.System.LDAP.ViewerGroups = true, []string{"pbx-admins"}, []string{"cn=sales,ou=groups,dc=example,dc=org"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	r := Sync(context.Background(), &cfg)
	if r.Error != "" || r.People != 1203 || r.WithNumber != 2 || len(r.Created) != 1 || len(r.RingGroups) != 1 {
		t.Fatalf("sync %+v", r)
	}
	if x := cfg.FindExtension("202"); x == nil || x.LDAP != "bob" || x.Email != "bob@example.org" {
		t.Fatalf("bob %+v", x)
	}
	if g := cfg.FindRingGroup("600"); strings.Join(g.Members, ",") != "201,202" {
		t.Fatalf("sales %v", g.Members)
	}
	ctx := context.Background()
	if _, role, err := SignIn(ctx, cfg.System.LDAP, "alice", "alice-password"); err != nil || role != "admin" {
		t.Fatalf("alice %q %v", role, err)
	}
	if _, role, err := SignIn(ctx, cfg.System.LDAP, "bob", "bob-password"); err != nil || role != "viewer" {
		t.Fatalf("bob %q %v", role, err)
	}
	if _, _, err := SignIn(ctx, cfg.System.LDAP, "carol", "carol-password"); err != ErrNotAllowed {
		t.Fatalf("carol is in no group: %v", err)
	}
	for _, bad := range [][2]string{{"bob", "wrong"}, {"bob", ""}, {"nobody", "x"}, {"*", "bob-password"}, {"bob)(uid=*", "bob-password"}} {
		if _, _, err := SignIn(ctx, cfg.System.LDAP, bad[0], bad[1]); err != ErrWrongPassword {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

// Against Active Directory, as Samba 4 runs it (~/.bvtest/samba): BITVOICE_TEST_AD=127.0.0.1:2636 (LDAPS)
// and BITVOICE_TEST_AD_CA=~/.bvtest/samba/tls/ca.pem. Disabled accounts are left out, more than 1,000
// people are read through paging, nested groups count, and sign-in follows them.
func TestSyncAndSignInWithActiveDirectory(t *testing.T) {
	addr, ca := os.Getenv("BITVOICE_TEST_AD"), os.Getenv("BITVOICE_TEST_AD_CA")
	if addr == "" {
		t.Skip("set BITVOICE_TEST_AD to the Samba AD test server")
	}
	pem, err := os.ReadFile(ca)
	if err != nil {
		t.Fatal(err)
	}
	cfg := baseConfig()
	cfg.Extensions = nil
	cfg.RingGroups = []config.RingGroup{{ID: "g1", Number: "600", Name: "Sales", Members: []string{"999"}}}
	cfg.Extensions = []config.Extension{{ID: "x0", Number: "999", Name: "Placeholder", Enabled: true, Secret: "a-long-sip-secret"}}
	cfg.System.LDAP = config.LDAP{Enabled: true, Kind: "ad", URL: "ldaps://" + addr, CACert: string(pem), BindDN: "reader@example.test",
		BindPassword: "Reader-Passw0rd!", BaseDN: "DC=example,DC=test", SyncExtensions: true, DisableMissing: true, NumberDigits: 3,
		RingGroups: []config.LDAPGroup{{Group: "sales", RingGroup: "600"}},
		SignIn:     true, AdminGroups: []string{"pbx-admins"}, ViewerGroups: []string{"sales"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	r := Sync(context.Background(), &cfg)
	if r.Error != "" || r.People < 1100 || r.WithNumber != 3 || len(r.Created) != 3 {
		t.Fatalf("sync %+v", r)
	}
	if cfg.FindExtension("203") != nil {
		t.Fatal("a disabled account got an extension")
	}
	if g := cfg.FindRingGroup("600"); strings.Join(g.Members, ",") != "201,202,204" {
		t.Fatalf("sales, with its nested group: %v", g.Members)
	}
	ctx := context.Background()
	for login, want := range map[string]string{"alice": "admin", "bob": "viewer", "erin": "viewer"} {
		pw := strings.ToUpper(login[:1]) + login[1:] + "-Passw0rd!"
		if _, role, err := SignIn(ctx, cfg.System.LDAP, login, pw); err != nil || role != want {
			t.Errorf("%s: %q %v", login, role, err)
		}
	}
	if _, _, err := SignIn(ctx, cfg.System.LDAP, "dan", "Dan-Passw0rd!"); err != ErrWrongPassword {
		t.Fatalf("a disabled account signed in: %v", err)
	}
	if _, _, err := SignIn(ctx, cfg.System.LDAP, "reader", "Reader-Passw0rd!"); err != ErrNotAllowed {
		t.Fatalf("someone in no group: %v", err)
	}
	if _, _, err := SignIn(ctx, cfg.System.LDAP, "bob", "wrong"); err != ErrWrongPassword {
		t.Fatalf("a wrong password: %v", err)
	}
}
