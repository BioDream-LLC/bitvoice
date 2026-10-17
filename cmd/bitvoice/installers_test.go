package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The installers carry what the rest of the build carries: the systemd unit of deploy/cloud, and
// BioDream's release public key as the Makefile builds it into releases.
func TestInstallersMatchTheBuild(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	sh, ps, unit, mk := read("../../deploy/install.sh"), read("../../deploy/install.ps1"), read("../../deploy/cloud/bitvoice.service"), read("../../Makefile")

	m := regexp.MustCompile(`(?s)<<UNIT\n(.*?)UNIT\n`).FindStringSubmatch(sh)
	if m == nil {
		t.Fatal("install.sh writes no unit")
	}
	if got := strings.ReplaceAll(m[1], "$HTTPS_LINUX", ":443"); got != unit {
		t.Errorf("install.sh's unit differs from deploy/cloud/bitvoice.service:\n%s", got)
	}
	key := regexp.MustCompile(`(?m)^BIODREAM_KEY := (\S+)$`).FindStringSubmatch(mk)
	if key == nil {
		t.Fatal("no BIODREAM_KEY in the Makefile")
	}
	for name, s := range map[string]string{"install.sh": sh, "install.ps1": ps} {
		if n := strings.Count(s, key[1]); n < 2 {
			t.Errorf("%s names the release key %d times, want it pinned (and compared)", name, n)
		}
	}
}
