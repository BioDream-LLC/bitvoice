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
