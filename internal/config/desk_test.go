package config

import "testing"

func TestDeskValidation(t *testing.T) {
	c := Default()
	c.Extensions = []Extension{{Number: "100", Name: "A", Secret: "a long enough secret 1", Enabled: true}}
	c.System.Desk.SCCP = SCCP{Enabled: true, Phones: []SCCPPhone{{Device: "00:11:22:aa:bb:cc", Extension: "100"}}}
	c.System.Desk.MGCP = MGCP{Enabled: true, Gateways: []MGCPGateway{{Name: "ATA1.Lan", Lines: []MGCPLine{{Endpoint: "AALN/1", Extension: "100"}}}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.System.Desk.SCCP.Phones[0].Device != "SEP001122AABBCC" || c.System.Desk.MGCP.Gateways[0].Name != "ata1.lan" || c.System.Desk.MGCP.Gateways[0].Lines[0].Endpoint != "aaln/1" {
		t.Fatalf("not normalised: %+v", c.System.Desk)
	}
	c.System.Desk.SCCP.Phones = append(c.System.Desk.SCCP.Phones, SCCPPhone{Device: "SEP001122AABBCC", Extension: "999"}, SCCPPhone{Device: "bogus", Extension: "100"})
	c.System.Desk.MGCP.Gateways[0].Lines = append(c.System.Desk.MGCP.Gateways[0].Lines, MGCPLine{Endpoint: "aaln/1@x", Extension: "100"})
	c.System.Desk.MGCP.Gateways = append(c.System.Desk.MGCP.Gateways, MGCPGateway{Name: "ata2"})
	err := c.Validate()
	ps, _ := err.(Problems)
	want := []string{"system.desk.sccp.phones[1].device", "system.desk.sccp.phones[1].extension", "system.desk.sccp.phones[2].device",
		"system.desk.mgcp.gateways[0].lines[1].endpoint", "system.desk.mgcp.gateways[1].lines"}
	for _, f := range want {
		found := false
		for _, p := range ps {
			if p.Field == f {
				found = true
			}
		}
		if !found {
			t.Errorf("no problem for %s in %v", f, err)
		}
	}
}
