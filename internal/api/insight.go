package api

import (
	"context"
	"math"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/biodream-llc/bitvoice/internal/ai"
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
}

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
			c := &ai.Client{BaseURL: a.BaseURL, Key: a.Key, Model: a.Model, AudioModel: a.AudioModel}
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
	s.handle("GET /api/insight", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		if days <= 0 || days > 365 {
			days = 30
		}
		writeJSON(w, 200, map[string]any{"days": days, "agents": agentTalk(s.Store.Transcripts(time.Now().AddDate(0, 0, -days)))})
	})
}
