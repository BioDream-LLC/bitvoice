package api

import (
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// QualityPlace is how calls sounded at one site, or on one provider's line.
type QualityPlace struct {
	Kind     string  `json:"kind"` // site or provider
	Name     string  `json:"name"`
	Calls    int     `json:"calls"`
	MOS      float64 `json:"mos"`     // average
	Poor     int     `json:"poor"`    // calls that sounded poor (MOS under 3.6)
	LossPct  float64 `json:"lossPct"` // of sound packets that never arrived
	JitterMS float64 `json:"jitterMs"`
	Hours    [24]struct {
		Calls   int     `json:"calls"`
		LossPct float64 `json:"lossPct"`
		Poor    int     `json:"poor"`
	} `json:"hours"` // by hour of the day, in the system's time zone
	Status    string `json:"status"`    // ok, warn, fail, idle (too few calls)
	Diagnosis string `json:"diagnosis"` // in plain words
	Advice    string `json:"advice,omitempty"`
}

// QualityReport is call quality over a window, by place, with an overall reading.
type QualityReport struct {
	Days    int            `json:"days"`
	Calls   int            `json:"calls"`
	Places  []QualityPlace `json:"places"`
	Overall string         `json:"overall"`
}

const poorMOS = 3.6

type acc struct {
	place          *QualityPlace
	mos, jit       float64
	packets, lost  uint64
	hourPk, hourLo [24]uint64
}

// qualityReport reads call records into quality by site and by provider, with a diagnosis for each.
func qualityReport(cfg config.Config, rs []store.CDR, days int) QualityReport {
	loc := cfg.System.Location()
	rep := QualityReport{Days: days, Places: []QualityPlace{}}
	places := map[string]*acc{}
	get := func(kind, name string) *acc {
		k := kind + "\x00" + name
		if a, ok := places[k]; ok {
			return a
		}
		a := &acc{place: &QualityPlace{Kind: kind, Name: name}}
		places[k] = a
		return a
	}
	for _, r := range rs {
		if len(r.Quality) == 0 {
			continue
		}
		rep.Calls++
		h := r.Start.In(loc).Hour()
		for _, q := range r.Quality {
			var a *acc
			switch {
			case q.Ext != "":
				name := "Extensions with no site"
				if x := cfg.FindExtension(q.Ext); x != nil && x.Site != "" {
					name = x.Site
					for _, s := range cfg.Sites {
						if s.ID == x.Site {
							name = s.Name
						}
					}
				}
				a = get("site", name)
			case q.Trunk != "":
				name := q.Trunk
				if t := cfg.FindTrunk(q.Trunk); t != nil {
					name = t.Name
				}
				a = get("provider", name)
			default:
				continue
			}
			p := a.place
			p.Calls++
			a.mos += q.MOS
			a.jit += q.JitterMS
			a.packets += q.Packets + q.Lost
			a.lost += q.Lost
			a.hourPk[h] += q.Packets + q.Lost
			a.hourLo[h] += q.Lost
			p.Hours[h].Calls++
			if q.MOS > 0 && q.MOS < poorMOS {
				p.Poor++
				p.Hours[h].Poor++
			}
		}
	}
	lossy := 0
	for _, a := range places {
		p := a.place
		n := float64(p.Calls)
		p.MOS, p.JitterMS = round1(a.mos/n), round1(a.jit/n)
		if a.packets > 0 {
			p.LossPct = round1(100 * float64(a.lost) / float64(a.packets))
		}
		for h := range 24 {
			if a.hourPk[h] > 0 {
				p.Hours[h].LossPct = round1(100 * float64(a.hourLo[h]) / float64(a.hourPk[h]))
			}
		}
		diagnose(p, a)
		if p.LossPct >= 2 && p.Calls >= 3 {
			lossy++
		}
		rep.Places = append(rep.Places, *p)
	}
	slices.SortFunc(rep.Places, func(x, y QualityPlace) int {
		if c := rank(y.Status) - rank(x.Status); c != 0 {
			return c
		}
		if c := cmpFloat(y.LossPct, x.LossPct); c != 0 {
			return c // the worst first
		}
		if c := y.Calls - x.Calls; c != 0 {
			return c
		}
		return strings.Compare(x.Name, y.Name)
	})
	switch {
	case rep.Calls == 0:
		rep.Overall = "No calls with sound to judge in this time."
	case lossy > 1 && lossy == countWithCalls(rep.Places, 3):
		rep.Overall = "Every place loses sound at once, which points to what they share: the internet link, or the provider."
	case lossy > 0 || anyStatus(rep.Places, "warn", "fail"):
		rep.Overall = "Most calls sound fine; the places marked below need a look."
	default:
		rep.Overall = "Calls sound good everywhere."
	}
	return rep
}

func diagnose(p *QualityPlace, a *acc) {
	where := p.Name + "'s network"
	advice := "Check the internet link and the network equipment at " + p.Name + "; something else using the link heavily at those times (backups, video, large downloads) is a common cause. Giving voice priority (QoS) on the router helps."
	if p.Kind == "provider" {
		where = "the line from " + p.Name
		advice = "Sound is lost between " + p.Name + " and this system: check this system's internet link first, then ask " + p.Name + " about their side."
	}
	poorPct := 100 * float64(p.Poor) / float64(p.Calls)
	switch {
	case p.Calls < 3:
		p.Status, p.Diagnosis = "idle", "Too few calls to judge."
	case p.LossPct >= 2:
		p.Status = "warn"
		if p.LossPct >= 5 || poorPct >= 25 {
			p.Status = "fail"
		}
		p.Diagnosis = fmt.Sprintf("Packet loss on %s: %.1f%% of the sound never arrived%s.", where, p.LossPct, when(p, a))
		p.Advice = advice
	case p.JitterMS >= 30:
		p.Status = "warn"
		p.Diagnosis = fmt.Sprintf("Uneven delay on %s (jitter of %.0f ms): words may sound choppy%s.", where, p.JitterMS, when(p, a))
		p.Advice = "This is often Wi-Fi, or an internet link that is full at times. Use a cable for desk phones where you can, and give voice priority (QoS) on the router."
	case poorPct >= 10:
		p.Status = "warn"
		p.Diagnosis = fmt.Sprintf("%.0f%% of calls at %s sounded poor, without one clear cause.", poorPct, p.Name)
		p.Advice = "Look at the poor calls in Call history (the Quality column) for a pattern: a phone, a time, a codec."
	default:
		p.Status = "ok"
		p.Diagnosis = fmt.Sprintf("Calls sound good (an average score of %.1f out of 5).", p.MOS)
	}
}

// when says when the trouble happens, if most of it falls in a few hours: " mostly between 14:00 and 16:00".
func when(p *QualityPlace, a *acc) string {
	worst, best := -1, uint64(0)
	for h := range 24 {
		if a.hourLo[h] > best {
			worst, best = h, a.hourLo[h]
		}
	}
	if worst < 0 || a.lost == 0 {
		return ""
	}
	rate := func(h int) float64 {
		if a.hourPk[h] == 0 {
			return 0
		}
		return float64(a.hourLo[h]) / float64(a.hourPk[h])
	}
	lo, hi := worst, worst
	for lo > 0 && rate(lo-1) >= rate(worst)/2 {
		lo--
	}
	for hi < 23 && rate(hi+1) >= rate(worst)/2 {
		hi++
	}
	var in uint64
	for h := lo; h <= hi; h++ {
		in += a.hourLo[h]
	}
	if hi-lo > 5 || float64(in) < 0.6*float64(a.lost) {
		return "" // spread through the day
	}
	return fmt.Sprintf(", mostly between %02d:00 and %02d:00", lo, hi+1)
}

func rank(s string) int { return map[string]int{"fail": 3, "warn": 2, "ok": 1, "idle": 0}[s] }

func round1(f float64) float64 { return math.Round(f*10) / 10 }

func countWithCalls(ps []QualityPlace, n int) int {
	c := 0
	for _, p := range ps {
		if p.Calls >= n {
			c++
		}
	}
	return c
}

func anyStatus(ps []QualityPlace, ss ...string) bool {
	for _, p := range ps {
		if slices.Contains(ss, p.Status) {
			return true
		}
	}
	return false
}

func (s *Server) qualityRoutes() {
	s.handle("GET /api/quality", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		if days <= 0 || days > 90 {
			days = 7
		}
		rs, err := s.Store.CDRs(store.CDRQuery{Since: time.Now().AddDate(0, 0, -days), Limit: 5000})
		if err != nil {
			writeErr(w, err)
			return
		}
		cfg, _ := s.Store.Config()
		writeJSON(w, 200, qualityReport(cfg, rs, days))
	})
}
