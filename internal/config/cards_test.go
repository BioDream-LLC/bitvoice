package config

import "testing"

func TestCardsValidation(t *testing.T) {
	c := Default()
	c.Extensions = []Extension{{Number: "100", Name: "Kitchen", Secret: "a long enough secret 1", Enabled: true}}
	c.System.Cards = Cards{Enabled: true, Token: "abcdefghijklmnopqrstuvwxyz012345", Channels: []CardChannel{{Channel: 1, Kind: "phone", Extension: "100"}, {Channel: 2, Kind: "line", Number: "+12125550100"}}}
	c.Trunks = []Trunk{{ID: "cards", Name: "Analogue lines", Enabled: true, Protocol: "dahdi"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if tr := c.Trunks[0]; tr.Host != "127.0.0.1" || tr.Port != DefaultCardLinePort || tr.Register {
		t.Fatalf("card trunk %+v", tr)
	}
	c.System.Cards.Token = "short"
	c.System.Cards.Channels = append(c.System.Cards.Channels, CardChannel{Channel: 2, Kind: "phone", Extension: "999"}, CardChannel{Channel: 3, Kind: "fax"})
	ps, _ := c.Validate().(Problems)
	for _, f := range []string{"system.cards.token", "system.cards.channels[2].channel", "system.cards.channels[2].extension", "system.cards.channels[3].kind"} {
		found := false
		for _, p := range ps {
			found = found || p.Field == f
		}
		if !found {
			t.Errorf("no problem for %s: %v", f, ps)
		}
	}
	c.System.Cards = Cards{}
	ps, _ = c.Validate().(Problems)
	found := false
	for _, p := range ps {
		found = found || p.Field == "trunks[0].protocol"
	}
	if !found {
		t.Error("a card trunk with cards off was accepted")
	}
}
