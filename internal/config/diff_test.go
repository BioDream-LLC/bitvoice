package config

import "testing"

func TestDiff(t *testing.T) {
	a := Default()
	a.Extensions = []Extension{{Number: "100", Name: "Reception", Secret: "s1"}, {Number: "101", Name: "Lee"}}
	b := a.Clone()
	b.Extensions[0].Name = "Front desk"
	b.Extensions[0].Secret = "s2"
	b.Extensions = b.Extensions[:1]
	got := map[string]Change{}
	for _, c := range Diff(a, b, 100) {
		got[c.Path] = c
	}
	if c := got["extensions[100].name"]; c.Before != "Reception" || c.After != "Front desk" {
		t.Errorf("name change: %+v", c)
	}
	if c := got["extensions[100].secret"]; c.Before != "(set)" || c.After != "(changed)" {
		t.Errorf("secret shown: %+v", c)
	}
	if c := got["extensions[101]"]; c.Before != "Lee" || c.After != "" {
		t.Errorf("removed extension: %+v", c)
	}
	if _, ok := got["extensions[101].name"]; ok {
		t.Error("a removed extension is listed field by field")
	}
	b.System.Name = `Acme <Dental> & "Sons"`
	for _, c := range Diff(a, b, 100) {
		if c.Path == "system.name" && c.After != `Acme <Dental> & "Sons"` {
			t.Errorf("name escaped: %q", c.After)
		}
	}
	if len(Diff(a, a, 100)) != 0 {
		t.Error("identical configs differ")
	}
}
