package config

import "testing"

// A new phone system shows no public-safety screens; one with a profile set up keeps them.
func TestPublicSafetyInUse(t *testing.T) {
	c := Default()
	if c.System.PublicSafety || c.PublicSafetyInUse() != "" {
		t.Fatalf("a new system has public safety on: %v %q", c.System.PublicSafety, c.PublicSafetyInUse())
	}
	c.System.Radio.Console = "7000"
	if got := c.PublicSafetyInUse(); got != "the radio console" {
		t.Fatalf("radio console: %q", got)
	}
	c = Default()
	c.System.Core.ECRF = true
	if got := c.PublicSafetyInUse(); got != "NG911 Core Services" {
		t.Fatalf("core: %q", got)
	}
	c = Default()
	c.System.PSAP.AgencyID = "psap.county.example.gov"
	if got := c.PublicSafetyInUse(); got != "the 911 profile" {
		t.Fatalf("psap: %q", got)
	}
	c = Default()
	c.Trunks = append(c.Trunks, Trunk{ID: "esinet", ESInet: true})
	if got := c.PublicSafetyInUse(); got != "the 911 profile" {
		t.Fatalf("esinet trunk: %q", got)
	}
}
