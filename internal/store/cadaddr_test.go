package store

import (
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// An EIDO address in plain http to another computer, saved before that was refused, never stops
// BitVoice: the configuration still opens and other changes still save. Setting a new one is refused.
func TestOldUnsafeCADAddressKept(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// As saved by an older version, which had no such rule.
	cfg, _ := s.Config()
	cfg.System.PSAP.EIDOURL = "http://192.168.1.50:9450/eido"
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	if _, _, err := s.Update("admin", "rename", 0, func(c *config.Config) error { c.System.Name = "Renamed"; return nil }); err != nil {
		t.Fatalf("an unrelated change was refused: %v", err)
	}
	if _, _, err := s.Update("admin", "move", 0, func(c *config.Config) error { c.System.PSAP.EIDOURL = "http://192.168.1.51:9450/eido"; return nil }); err == nil || !strings.Contains(err.Error(), "eidoUrl") {
		t.Fatalf("a new plain-http address off this computer was saved: %v", err)
	}
	if _, _, err := s.Update("admin", "fix", 0, func(c *config.Config) error { c.System.PSAP.EIDOURL = "https://cad.example.gov/eido"; return nil }); err != nil {
		t.Fatalf("an https address was refused: %v", err)
	}
	s.Close()
	again, err := Open(dir)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	again.Close() // Windows cannot remove an open database file
}

// Messages written in the same millisecond keep the order they were written in.
func TestInboxMessagesKeepOrder(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := InboxConv{ID: "c1", Channel: "webchat", Contact: "v1", State: "open"}
	if err := s.PutConv(c); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if _, err := s.AddMsg(InboxMsg{Conv: c.ID, Text: string(rune('a' + i%26))}); err != nil {
			t.Fatal(err)
		}
	}
	ms, err := s.Msgs(c.ID, time.Time{}, 100)
	if err != nil || len(ms) != 50 {
		t.Fatalf("%d %v", len(ms), err)
	}
	for i, m := range ms {
		if m.Text != string(rune('a'+i%26)) {
			t.Fatalf("message %d is %q", i, m.Text)
		}
	}
}

// A token whose secret was made outside (the demo's nightly rebuild keeps the CAD's token) works like
// any other; a badly formed secret is refused.
func TestAddTokenSecret(t *testing.T) {
	s, err := OpenTest(t)
	if err != nil {
		t.Fatal(err)
	}
	secret := "bv_" + strings.Repeat("ab", 32)
	if _, err := s.AddTokenSecret("CAD", "admin", "setup", time.Time{}, true, secret); err != nil {
		t.Fatal(err)
	}
	if tok, ok := s.CheckToken(secret); !ok || !tok.OnBehalf || tok.Name != "CAD" {
		t.Fatalf("token %+v %v", tok, ok)
	}
	for _, bad := range []string{"bv_short", "xx_" + strings.Repeat("ab", 32), "bv_" + strings.Repeat("AB", 32)} {
		if _, err := s.AddTokenSecret("x", "admin", "setup", time.Time{}, false, bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}
