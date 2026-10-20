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
	"github.com/biodream-llc/bitvoice/internal/reason"
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
	// NoSound counts answered calls in which no sound at all came from here.
	NoSound   int    `json:"noSound,omitempty"`
	Status    string `json:"status"`    // ok, warn, fail, idle (too few calls)
	Diagnosis string `json:"diagnosis"` // in plain words
	Advice    string `json:"advice,omitempty"`
	// Reasons is how the diagnosis was reached (the call-quality rulebook), in plain words.
	Reasons []string `json:"reasons,omitempty"`
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
	// For this place's lossy calls, how the other half of the same call fared: clean, lossy too, or not measured.
	otherClean, otherLossy, otherSeen int
}

func lossPct(q store.LegQuality) float64 {
	if q.Packets+q.Lost == 0 {
		return 0
	}
	return 100 * float64(q.Lost) / float64(q.Packets+q.Lost)
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
	placeOf := func(ext, trunk string) *acc {
		switch {
		case ext != "":
			name := "Extensions with no site"
			if x := cfg.FindExtension(ext); x != nil && x.Site != "" {
				name = x.Site
				for _, s := range cfg.Sites {
					if s.ID == x.Site {
						name = s.Name
					}
				}
			}
			return get("site", name)
		case trunk != "":
			name := trunk
			if t := cfg.FindTrunk(trunk); t != nil {
				name = t.Name
			}
			return get("provider", name)
		}
		return nil
	}
	for _, r := range rs {
		for _, s := range r.NoSound {
			if a := placeOf(s.Ext, s.Trunk); a != nil {
				a.place.NoSound++
			}
		}
		if len(r.Quality) == 0 {
			if len(r.NoSound) > 0 {
				rep.Calls++
			}
			continue
		}
		rep.Calls++
		h := r.Start.In(loc).Hour()
		legAcc := make([]*acc, len(r.Quality))
		for i, q := range r.Quality {
			a := placeOf(q.Ext, q.Trunk)
			if a == nil {
				continue
			}
			legAcc[i] = a
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
		// Each lossy half of the call against the other half: where was the sound lost?
		for i, q := range r.Quality {
			if legAcc[i] == nil || lossPct(q) < 2 {
				continue
			}
			clean, lossy, seen := false, false, false
			for j, o := range r.Quality {
				if j == i || legAcc[j] == nil || legAcc[j] == legAcc[i] || o.Packets+o.Lost == 0 {
					continue
				}
				seen = true
				switch l := lossPct(o); {
				case l < 0.5:
					clean = true
				case l >= 2:
					lossy = true
				}
			}
			if seen {
				legAcc[i].otherSeen++
				if lossy {
					legAcc[i].otherLossy++
				} else if clean {
					legAcc[i].otherClean++
				}
			}
		}
	}
	lossy := 0
	for _, a := range places {
		p := a.place
		if n := float64(p.Calls); n > 0 {
			p.MOS, p.JitterMS = round1(a.mos/n), round1(a.jit/n)
		}
		if a.packets > 0 {
			p.LossPct = round1(100 * float64(a.lost) / float64(a.packets))
		}
		for h := range 24 {
			if a.hourPk[h] > 0 {
				p.Hours[h].LossPct = round1(100 * float64(a.hourLo[h]) / float64(a.hourPk[h]))
			}
		}
		if p.LossPct >= 2 && p.Calls >= 3 {
			lossy++
		}
	}
	withCalls := 0
	for _, a := range places {
		if a.place.Calls >= 3 {
			withCalls++
		}
	}
	for _, a := range places {
		diagnose(a.place, a, lossy > 1 && lossy == withCalls)
		rep.Places = append(rep.Places, *a.place)
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
	case anyNoSound(rep.Places):
		rep.Overall = "Some answered calls had no sound at all from one side: see the places marked below."
	case lossy > 1 && lossy == countWithCalls(rep.Places, 3):
		rep.Overall = "Every place loses sound at once, which points to what they share: the internet link, or the provider."
	case lossy > 0 || anyStatus(rep.Places, "warn", "fail"):
		rep.Overall = "Most calls sound fine; the places marked below need a look."
	default:
		rep.Overall = "Calls sound good everywhere."
	}
	return rep
}

// diagnose puts a place's measures to the call-quality rulebook (internal/reason) and words its answer.
// everywhere is every place with calls losing sound at once.
func diagnose(p *QualityPlace, a *acc, everywhere bool) {
	where := p.Name + "'s network"
	advice := "Check the internet link and the network equipment at " + p.Name + "; something else using the link heavily at those times (backups, video, large downloads) is a common cause. Giving voice priority (QoS) on the router helps."
	if p.Kind == "provider" {
		where = "the line from " + p.Name
		advice = "Sound is lost between " + p.Name + " and this system: check this system's internet link first, then ask " + p.Name + " about their side."
	}
	poorPct := 0.0
	if p.Calls > 0 {
		poorPct = 100 * float64(p.Poor) / float64(p.Calls)
	}
	facts := map[string]reason.Truth{
		"few_calls":        truthOf(p.Calls < 3),
		"loss_high":        truthOf(p.LossPct >= 2),
		"jitter_high":      truthOf(p.JitterMS >= 30),
		"poor_many":        truthOf(poorPct >= 10),
		"everywhere_lossy": truthOf(everywhere),
	}
	// The other half of the lossy calls: measured only when the calls had one (an extension calling the
	// echo test has none). Clean or lossy for most of them; neither when they were mixed.
	if a.otherSeen > 0 {
		facts["other_side_clean"] = truthOf(3*a.otherClean >= 2*a.otherSeen)
		facts["other_side_lossy"] = truthOf(3*a.otherLossy >= 2*a.otherSeen)
	}
	v := reason.Quality.Decide(facts)
	p.Reasons = v.Reasons
	if p.NoSound > 0 {
		// No sound at all is not poor sound: the media never arrived. Said first, whatever else is true.
		p.Status = "fail"
		calls := "1 answered call"
		if p.NoSound > 1 {
			calls = strconv.Itoa(p.NoSound) + " answered calls"
		}
		p.Diagnosis = calls + " had no sound at all from " + p.Name + "."
		p.Advice = "The call connected but its sound never arrived: almost always a firewall or NAT router that lets SIP through and blocks the media. Check that the phones' router does not rewrite SIP (SIP ALG off) and that the media ports (Settings, UDP) are open."
		if p.Kind == "provider" {
			p.Advice = "The call connected but " + p.Name + "'s sound never arrived: almost always this system's firewall (or the cloud's security group) letting SIP in and keeping the media out. A provider's media often comes from other addresses than its SIP servers: ask " + p.Name + " for its media address ranges and allow UDP to the media ports (Settings) from them."
		}
		p.Reasons = append([]string{p.Diagnosis}, p.Reasons...)
		return
	}
	switch v.Action {
	case "idle":
		p.Status, p.Diagnosis = "idle", "Too few calls to judge."
	case "here", "shared":
		p.Status = "warn"
		if p.LossPct >= 5 || poorPct >= 25 {
			p.Status = "fail"
		}
		p.Diagnosis = fmt.Sprintf("Packet loss on %s: %.1f%% of the sound never arrived%s.", where, p.LossPct, when(p, a))
		p.Advice = advice
		if v.Action == "shared" {
			p.Diagnosis = fmt.Sprintf("%.1f%% of the sound never arrived%s, and not at %s alone: the other half of the same calls, or every other place, lost sound too.", p.LossPct, when(p, a), p.Name)
			p.Advice = "Look at what these calls share: this system's own internet link and router first, then the provider."
		} else if facts["other_side_clean"] == reason.Yes {
			p.Diagnosis += " The other half of the same calls arrived clean, so it is on this side."
		}
	case "jitter":
		p.Status = "warn"
		p.Diagnosis = fmt.Sprintf("Uneven delay on %s (jitter of %.0f ms): words may sound choppy%s.", where, p.JitterMS, when(p, a))
		p.Advice = "This is often Wi-Fi, or an internet link that is full at times. Use a cable for desk phones where you can, and give voice priority (QoS) on the router."
	case "poor":
		p.Status = "warn"
		p.Diagnosis = fmt.Sprintf("%.0f%% of calls at %s sounded poor, without one clear cause.", poorPct, p.Name)
		p.Advice = "Look at the poor calls in Call history (the Quality column) for a pattern: a phone, a time, a codec."
	case "unsure":
		p.Status = "warn"
		p.Diagnosis = "Sound is being lost, but the measures do not say where."
		p.Advice = "Look at the poor calls in Call history for a pattern."
	default:
		p.Status = "ok"
		p.Diagnosis = fmt.Sprintf("Calls sound good (an average score of %.1f out of 5).", p.MOS)
	}
}

func truthOf(b bool) reason.Truth {
	if b {
		return reason.Yes
	}
	return reason.No
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

func anyNoSound(ps []QualityPlace) bool {
	for _, p := range ps {
		if p.NoSound > 0 {
			return true
		}
	}
	return false
}
