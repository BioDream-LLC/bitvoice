package pbx

import (
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

func TestKeypadAndMatches(t *testing.T) {
	if k := config.Keypad("Müller-Lee"); k != "685537533" {
		t.Fatalf("keypad %q", k)
	}
	cfg := testConfig()
	cfg.Extensions[0].Name, cfg.Extensions[1].Name, cfg.Extensions[2].Name = "Ann Smith", "Bob Smythe", "Sam Jones"
	d := &config.Directory{Match: "last"}
	if m := directoryMatches(&cfg, d, "76"); len(m) != 2 || m[0].Name != "Ann Smith" {
		t.Fatalf("SMY/SMI %v", m)
	}
	d.Match = "first"
	if m := directoryMatches(&cfg, d, "726"); len(m) != 1 || m[0].Number != "102" {
		t.Fatalf("SAM %v", m)
	}
	d.Match, d.Members = "any", []string{"100"}
	if m := directoryMatches(&cfg, d, "266"); len(m) != 1 {
		t.Fatalf("members only %v", m)
	}
}

func TestDirectoryPutsThrough(t *testing.T) {
	cfg := testConfig()
	cfg.Extensions[1].Name, cfg.Extensions[2].Name = "Dr Lee", "Sam Jones"
	cfg.Directories = []config.Directory{{ID: "staff", Number: "411", Name: "Staff"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101", "102")
	d, code := ps[0].dial("411")
	if code != 200 {
		t.Fatalf("directory answered %d", code)
	}
	ps[0].gotRTP(time.Second)
	var seq uint16 = 1
	d.press(&seq, "566") // J O N
	if ps[2].waitRing(5*time.Second) == nil {
		t.Fatal("spelling JON did not ring Sam Jones")
	}
	ps[0].send(d.request("BYE"))
}

func TestDialInPINAndCallOut(t *testing.T) {
	cfg := testConfig()
	cfg.DialIns = []config.DialIn{{ID: "road", Number: "450", Name: "Road warriors", PIN: "4321", As: "100"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	e, _ := startEngine(t, cfg)
	ps := registered(t, e, "100", "101")
	// A wrong PIN three times: hung up.
	d, code := ps[0].dial("450")
	if code != 200 {
		t.Fatalf("dial-in answered %d", code)
	}
	ps[0].gotRTP(time.Second)
	var seq uint16 = 1
	for range 3 {
		d.press(&seq, "1111#")
		time.Sleep(1500 * time.Millisecond)
	}
	ps[0].waitBye(t, 6*time.Second)

	// The right PIN, then a number: it is called as extension 100.
	d, _ = ps[0].dial("450")
	ps[0].gotRTP(time.Second)
	seq = 1
	d.press(&seq, "4321#")
	time.Sleep(800 * time.Millisecond)
	d.press(&seq, "101#")
	if ps[1].waitRing(5*time.Second) == nil {
		t.Fatal("dial-in did not call 101")
	}
	ps[0].send(d.request("BYE"))
}

func TestDialInValidation(t *testing.T) {
	cfg := testConfig()
	cfg.DialIns = []config.DialIn{{Name: "Open", As: "100"}}
	if cfg.Validate() == nil {
		t.Fatal("a dial-in with neither PIN nor allowed callers was accepted")
	}
	cfg = testConfig()
	cfg.Extensions[0].Outbound = false
	cfg.DialIns = []config.DialIn{{Name: "X", PIN: "1234", As: "100"}}
	if cfg.Validate() == nil {
		t.Fatal("a dial-in as an extension without outside calls was accepted")
	}
	cfg = testConfig()
	cfg.DialIns = []config.DialIn{{Name: "X", AllowedCallers: []string{"+1 (212) 555-0199"}, As: "100"}}
	if err := cfg.Validate(); err != nil || cfg.DialIns[0].AllowedCallers[0] != "+12125550199" {
		t.Fatalf("%v %v", err, cfg.DialIns[0].AllowedCallers)
	}
}
