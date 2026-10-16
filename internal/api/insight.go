package api

import (
	"context"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// AgentTalk is how one person's analysed calls went on average: measures to look at, not a score.
type AgentTalk struct {
	Name         string  `json:"name"`
	Ext          string  `json:"ext,omitempty"`
	Calls        int     `json:"calls"`
	TalkShare    float64 `json:"talkShare"`    // percent of the talking done by them
	Longest      float64 `json:"longest"`      // their longest stretch, seconds, on average
	CutIns       float64 `json:"cutIns"`       // times they started over the caller, per call
	SilenceShare float64 `json:"silenceShare"` // percent of the call nobody spoke
	EndedUpset   int     `json:"endedUpset"`   // calls the caller ended frustrated or angry
	LookAt       int     `json:"lookAt"`       // moments flagged to look at
	WentWell     int     `json:"wentWell"`
	Coaching     int     `json:"coaching"` // supervisors' notes on their calls
}

// coachMu keeps two supervisors' notes on one call from overwriting each other.
var coachMu sync.Mutex

func upset(m string) bool { return m == "frustrated" || m == "angry" || m == "upset" }

func agentTalk(ts []store.CallTranscript) []AgentTalk {
	by := map[string]*AgentTalk{}
	type sums struct{ share, longest, cut, silence float64 }
	acc := map[string]*sums{}
	for _, t := range ts {
		if t.Talk == nil || t.Talk.Seconds < 10 {
			continue
		}
		k := firstNonEmpty(t.AnsweredExt, t.ToName, t.To)
		a, ok := by[k]
		if !ok {
			a = &AgentTalk{Name: firstNonEmpty(t.ToName, t.To), Ext: t.AnsweredExt}
			by[k], acc[k] = a, &sums{}
		}
		s := acc[k]
		a.Calls++
		a.Coaching += len(t.Coaching)
		if talked := t.Talk.CallerTalk + t.Talk.CalleeTalk; talked > 0 {
			s.share += 100 * t.Talk.CalleeTalk / talked
		}
		s.longest += t.Talk.LongestCallee
		s.cut += float64(t.Talk.CalleeCutIn)
		s.silence += 100 * t.Talk.Silence / t.Talk.Seconds
		if t.Insight != nil {
			if upset(t.Insight.MoodEnd) {
				a.EndedUpset++
			}
			for _, o := range t.Insight.Observations {
				switch o.Kind {
				case "look-at", "unanswered":
					a.LookAt++
				case "went-well":
					a.WentWell++
				}
			}
		}
	}
	out := []AgentTalk{}
	for k, a := range by {
		s, n := acc[k], float64(a.Calls)
		r := func(f float64) float64 { return math.Round(f*10) / 10 }
		a.TalkShare, a.Longest, a.CutIns, a.SilenceShare = r(s.share/n), r(s.longest/n), r(s.cut/n), r(s.silence/n)
		out = append(out, *a)
	}
	slices.SortFunc(out, func(x, y AgentTalk) int { return y.Calls - x.Calls })
	return out
}

func (s *Server) insightRoutes() {
	// Insight for one recorded call, on request: measures from the sound, and the AI's notes when on.
	s.handle("POST /api/recordings/{month}/{name}/insight", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		name := r.PathValue("month") + "/" + r.PathValue("name")
		t, err := s.Store.CallTranscript(name)
		if err != nil || t.Status != "done" {
			writeErr(w, config.Problems{{Field: "transcript", Message: "write the call out first (Transcript), then ask for insight"}})
			return
		}
		if wav, err := s.Store.Recording(name); err == nil {
			if left, right, rate, err := media.SplitStereoWAV(wav); err == nil {
				m := media.Measure(left, right, rate)
				t.Talk = &m
			}
		}
		a := s.config().System.AI
		if a.Enabled {
			ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			defer cancel()
			c := s.aiClient(a)
			in, err := c.CallInsight(ctx, t.TimedText())
			if err != nil {
				writeError(w, http.StatusBadGateway, "the AI service did not answer: "+err.Error())
				return
			}
			t.Insight = &in
		}
		if err := s.Store.SetCallTranscript(t); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.audit(r, u.Username, "transcript.insight", name)
		s.Bus.Publish("transcript.changed", map[string]string{"recording": name})
		writeJSON(w, 200, t)
	})
	// Supervisors' coaching notes on a call, written by administrators (viewers read them with the
	// transcript, as with timeline notes); any administrator may remove one.
	s.handle("POST /api/recordings/{month}/{name}/coaching", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		name := r.PathValue("month") + "/" + r.PathValue("name")
		var in struct {
			Second float64 `json:"second"`
			Note   string  `json:"note"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		in.Note = strings.TrimSpace(in.Note)
		var p config.Problems
		if in.Note == "" {
			p = append(p, config.Problem{Field: "note", Message: "write the note"})
		} else if len([]rune(in.Note)) > 1000 {
			p = append(p, config.Problem{Field: "note", Message: "keep the note under 1000 characters"})
		}
		coachMu.Lock()
		defer coachMu.Unlock()
		t, err := s.Store.CallTranscript(name)
		if err != nil || t.Status != "done" {
			p = append(p, config.Problem{Field: "transcript", Message: "write the call out first (Transcript), then add notes"})
		} else if in.Second < 0 || math.IsNaN(in.Second) || (t.Talk != nil && in.Second > t.Talk.Seconds+1) {
			p = append(p, config.Problem{Field: "second", Message: "choose a moment within the call"})
		}
		if len(p) > 0 {
			writeErr(w, p)
			return
		}
		t.Coaching = append(t.Coaching, store.CoachingNote{ID: config.NewID(), At: time.Now(), By: u.Username, Second: math.Round(in.Second), Note: in.Note})
		slices.SortStableFunc(t.Coaching, func(a, b store.CoachingNote) int { return int(a.Second - b.Second) })
		if err := s.Store.SetCallTranscript(t); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.audit(r, u.Username, "transcript.coaching_added", name)
		s.Bus.Publish("transcript.changed", map[string]string{"recording": name})
		writeJSON(w, 200, t)
	})
	s.handle("DELETE /api/recordings/{month}/{name}/coaching/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		name := r.PathValue("month") + "/" + r.PathValue("name")
		coachMu.Lock()
		defer coachMu.Unlock()
		t, err := s.Store.CallTranscript(name)
		if err != nil {
			writeError(w, http.StatusNotFound, "no such call")
			return
		}
		i := slices.IndexFunc(t.Coaching, func(n store.CoachingNote) bool { return n.ID == r.PathValue("id") })
		if i < 0 {
			writeError(w, http.StatusNotFound, "no such note")
			return
		}
		t.Coaching = slices.Delete(t.Coaching, i, i+1)
		if err := s.Store.SetCallTranscript(t); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.audit(r, u.Username, "transcript.coaching_removed", name)
		s.Bus.Publish("transcript.changed", map[string]string{"recording": name})
		writeJSON(w, 200, t)
	})
	s.handle("GET /api/insight", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		if days <= 0 || days > 365 {
			days = 30
		}
		writeJSON(w, 200, map[string]any{"days": days, "agents": agentTalk(s.Store.Transcripts(time.Now().AddDate(0, 0, -days)))})
	})
}
