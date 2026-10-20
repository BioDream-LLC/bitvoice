package api

import (
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

func TestQualityReport(t *testing.T) {
	cfg := config.Default()
	cfg.System.Timezone = "UTC"
	cfg.Sites = []config.Site{{ID: "s1", Name: "Head office"}, {ID: "s2", Name: "Warehouse"}}
	cfg.Extensions = []config.Extension{{Number: "100", Name: "A", Site: "s1"}, {Number: "200", Name: "B", Site: "s2"}, {Number: "300", Name: "C"}}
	cfg.Trunks = []config.Trunk{{ID: "t1", Name: "Carrier"}}
	day := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	var rs []store.CDR
	for h := 8; h < 18; h++ {
		lost, mos := uint64(5), 4.3
		if h == 14 || h == 15 {
			lost, mos = 400, 3.1 // the head office loses sound in the afternoon
		}
		rs = append(rs, store.CDR{Start: day.Add(time.Duration(h) * time.Hour), Quality: []store.LegQuality{
			{Ext: "100", Packets: 3000, Lost: lost, JitterMS: 8, MOS: mos},
			{Trunk: "t1", Packets: 3000, Lost: 3, JitterMS: 6, MOS: 4.4},
		}})
		rs = append(rs, store.CDR{Start: day.Add(time.Duration(h) * time.Hour), Quality: []store.LegQuality{{Ext: "200", Packets: 3000, Lost: 2, JitterMS: 45, MOS: 4.0}}})
	}
	rs = append(rs, store.CDR{Start: day, Quality: []store.LegQuality{{Ext: "300", Packets: 100, MOS: 4.4}}})
	rep := qualityReport(cfg, rs, 7)
	by := map[string]QualityPlace{}
	for _, p := range rep.Places {
		by[p.Name] = p
	}
	ho := by["Head office"]
	if ho.Status != "warn" && ho.Status != "fail" || !strings.Contains(ho.Diagnosis, "Packet loss on Head office's network") || !strings.Contains(ho.Diagnosis, "between 14:00 and 16:00") {
		t.Fatalf("head office: %+v", ho)
	}
	// The trunk half of the same calls arrived clean: the loss is on the head office's side, and it says why.
	if !strings.Contains(ho.Diagnosis, "The other half of the same calls arrived clean") || !strings.Contains(strings.Join(ho.Reasons, " "), "so the loss is on this place's side") {
		t.Fatalf("head office reasons: %+v", ho)
	}
	if c := by["Carrier"]; c.Status != "ok" || c.Kind != "provider" {
		t.Fatalf("carrier: %+v", c)
	}
	if w := by["Warehouse"]; w.Status != "warn" || !strings.Contains(w.Diagnosis, "jitter of 45 ms") {
		t.Fatalf("warehouse: %+v", w)
	}
	if n := by["Extensions with no site"]; n.Status != "idle" {
		t.Fatalf("no site: %+v", n)
	}
	if rep.Places[0].Name != "Head office" || rep.Calls != 21 || !strings.Contains(rep.Overall, "need a look") {
		t.Fatalf("report %+v", rep)
	}
	if qualityReport(cfg, nil, 7).Overall != "No calls with sound to judge in this time." {
		t.Fatal("empty")
	}
}

// Both halves of the same calls lost sound: not the site alone, which the diagnosis says.
func TestQualityBothHalvesLossy(t *testing.T) {
	cfg := config.Default()
	cfg.System.Timezone = "UTC"
	cfg.Sites = []config.Site{{ID: "s1", Name: "Head office"}}
	cfg.Extensions = []config.Extension{{Number: "100", Name: "A", Site: "s1"}}
	cfg.Trunks = []config.Trunk{{ID: "t1", Name: "Carrier"}}
	day := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	var rs []store.CDR
	for i := range 5 {
		rs = append(rs, store.CDR{Start: day.Add(time.Duration(i) * time.Minute), Quality: []store.LegQuality{
			{Ext: "100", Packets: 3000, Lost: 200, MOS: 3.2}, {Trunk: "t1", Packets: 3000, Lost: 180, MOS: 3.3}}})
	}
	rep := qualityReport(cfg, rs, 7)
	for _, p := range rep.Places {
		if !strings.Contains(p.Diagnosis, "not at "+p.Name+" alone") {
			t.Errorf("%s: %+v", p.Name, p)
		}
	}
}

// A provider whose sound never arrived is called out first, with firewall advice; the extensions
// on the same calls, which did send sound, are not blamed.
func TestQualityNoSound(t *testing.T) {
	cfg := config.Default()
	cfg.System.Timezone = "UTC"
	cfg.Extensions = []config.Extension{{Number: "100", Name: "A"}}
	cfg.Trunks = []config.Trunk{{ID: "t1", Name: "SIP.US"}}
	at := time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC)
	rs := []store.CDR{
		{Start: at, Quality: []store.LegQuality{{Ext: "100", Packets: 1000, MOS: 4.4}}, NoSound: []store.Side{{Trunk: "t1", Name: "SIP.US"}}},
		{Start: at, Quality: []store.LegQuality{{Ext: "100", Packets: 1000, MOS: 4.4}}, NoSound: []store.Side{{Trunk: "t1", Name: "SIP.US"}}},
		{Start: at, NoSound: []store.Side{{Trunk: "t1", Name: "SIP.US"}}}, // nothing measured on either side
	}
	rep := qualityReport(cfg, rs, 7)
	if rep.Calls != 3 || !strings.Contains(rep.Overall, "no sound at all") {
		t.Fatalf("report %+v", rep)
	}
	for _, p := range rep.Places {
		switch p.Name {
		case "SIP.US":
			if p.Status != "fail" || p.NoSound != 3 || p.Diagnosis != "3 answered calls had no sound at all from SIP.US." ||
				!strings.Contains(p.Advice, "media address ranges") || p.Reasons[0] != p.Diagnosis {
				t.Fatalf("provider %+v", p)
			}
		case "Extensions with no site":
			if p.NoSound != 0 || p.Status == "fail" {
				t.Fatalf("extensions blamed: %+v", p)
			}
		}
	}
}
