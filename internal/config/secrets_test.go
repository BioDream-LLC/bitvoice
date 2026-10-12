package config

import "testing"

func TestSecretsForGit(t *testing.T) {
	cur := Default()
	cur.Extensions = []Extension{{ID: "a", Number: "100", Secret: "s100", Voicemail: Voicemail{PIN: "1234"}}, {ID: "b", Number: "101", Secret: "s101"}}
	cur.Trunks = []Trunk{{ID: "t", Name: "T", Password: "tp"}}
	cur.System.Email.Password = "mailpw"
	cur.Menus = []Menu{{ID: "m", Name: "Main", Options: []MenuOption{{Key: "1", Label: "Sales"}}}}
	out := cur.Clone()
	StripSecrets(&out)
	if out.Extensions[0].Secret != "" || out.Extensions[0].Voicemail.PIN != "" || out.Trunks[0].Password != "" || out.System.Email.Password != "" {
		t.Fatalf("secrets left: %+v", out.Extensions[0])
	}
	if out.Extensions[0].Number != "100" || out.Menus[0].Options[0].Key != "1" {
		t.Fatal("non-secret field lost")
	}
	// Read back from Git, with the extensions in another order and a new one with its own secret.
	back := out.Clone()
	back.Extensions = []Extension{back.Extensions[1], back.Extensions[0], {ID: "c", Number: "102", Secret: "new"}}
	KeepSecrets(&back, cur)
	if back.Extensions[0].Secret != "s101" || back.Extensions[1].Secret != "s100" || back.Extensions[1].Voicemail.PIN != "1234" ||
		back.Extensions[2].Secret != "new" || back.Trunks[0].Password != "tp" || back.System.Email.Password != "mailpw" {
		t.Fatalf("secrets not kept: %+v %+v", back.Extensions, back.Trunks)
	}
}
