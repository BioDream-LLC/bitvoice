package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/loadgen"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// loadState holds the call generator's current (or last) run.
type loadState struct {
	mu  sync.Mutex
	run *loadgen.Run
}

type loadRequest struct {
	From       string  `json:"from"`
	To         string  `json:"to"`
	AnswerAs   string  `json:"answerAs"`
	Calls      int     `json:"calls"`
	Rate       float64 `json:"rate"`
	Concurrent int     `json:"concurrent"`
	HoldSecs   int     `json:"holdSeconds"`
}

// localTarget says what a number reaches inside BitVoice, or an error when it would leave the building
// (a trunk or an emergency number): the generator never calls out.
func localTarget(cfg *config.Config, n string) (string, error) {
	if slices.Contains(cfg.System.EmergencyNumbers, n) {
		return "", errors.New("emergency numbers are never called by the generator")
	}
	s := cfg.System
	switch n {
	case "":
		return "", errors.New("choose a number to call")
	case s.EchoNumber:
		return "the echo test", nil
	case s.VoicemailNumber:
		return "voicemail", nil
	}
	for _, x := range cfg.Extensions {
		if x.Number == n {
			return "extension " + n, nil
		}
	}
	for _, g := range cfg.RingGroups {
		if g.Number == n {
			return "ring group " + g.Name, nil
		}
	}
	for _, q := range cfg.Queues {
		if q.Number == n {
			return "queue " + q.Name, nil
		}
	}
	for _, m := range cfg.Menus {
		if m.Number == n {
			return "menu " + m.Name, nil
		}
	}
	return "", errors.New(n + " is not a number inside BitVoice; the generator only calls extensions, groups, queues, menus, voicemail and the echo test")
}

func (s *Server) loadStatus() map[string]any {
	s.load.mu.Lock()
	run := s.load.run
	s.load.mu.Unlock()
	out := map[string]any{"run": nil, "limits": map[string]any{"calls": loadgen.MaxCalls, "concurrent": loadgen.MaxConcurrent,
		"rate": loadgen.MaxRate, "holdSeconds": int(loadgen.MaxHold.Seconds())}}
	if run != nil {
		out["run"] = run.Snapshot()
	}
	return out
}

func (s *Server) getLoad(w http.ResponseWriter, r *http.Request, u store.User) {
	writeJSON(w, 200, s.loadStatus())
}

func (s *Server) startLoad(w http.ResponseWriter, r *http.Request, u store.User) {
	var in loadRequest
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	cfg, _ := s.Store.Config()
	var p config.Problems
	from := cfg.FindExtension(in.From)
	if from == nil || !from.Enabled {
		p = append(p, config.Problem{Field: "from", Message: "choose an extension that is switched on"})
	}
	if _, err := localTarget(&cfg, in.To); err != nil {
		p = append(p, config.Problem{Field: "to", Message: err.Error()})
	}
	var answer *config.Extension
	if in.AnswerAs != "" {
		answer = cfg.FindExtension(in.AnswerAs)
		switch {
		case answer == nil || !answer.Enabled:
			p = append(p, config.Problem{Field: "answerAs", Message: "choose an extension that is switched on"})
		case in.AnswerAs == in.From:
			p = append(p, config.Problem{Field: "answerAs", Message: "use a different extension from the one calling"})
		}
	}
	plan := loadgen.Plan{From: in.From, To: in.To, AnswerAs: in.AnswerAs, Calls: in.Calls, Rate: in.Rate, Concurrent: in.Concurrent,
		Hold: time.Duration(in.HoldSecs) * time.Second}
	if err := plan.Check(); err != nil && plan.From != "" && plan.To != "" {
		field := "calls"
		switch {
		case in.Concurrent < 1 || in.Concurrent > loadgen.MaxConcurrent:
			field = "concurrent"
		case in.Rate <= 0 || in.Rate > loadgen.MaxRate:
			field = "rate"
		case in.HoldSecs < 0 || plan.Hold > loadgen.MaxHold:
			field = "holdSeconds"
		}
		p = append(p, config.Problem{Field: field, Message: err.Error()})
	}
	if len(p) > 0 {
		writeErr(w, p)
		return
	}
	s.load.mu.Lock()
	if s.load.run != nil && s.load.run.Snapshot().Running {
		s.load.mu.Unlock()
		writeError(w, http.StatusConflict, "a run is already going; stop it first")
		return
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	plan.Token = hex.EncodeToString(b)
	plan.From, plan.FromSecret = from.Number, from.Secret
	if answer != nil {
		plan.AnswerSecret = answer.Secret
	}
	plan.PBX = s.Engine.LocalSIP()
	s.Engine.SetLoadToken(plan.Token)
	run, err := loadgen.Start(plan)
	if err != nil {
		s.Engine.SetLoadToken("")
		s.load.mu.Unlock()
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.load.run = run
	s.load.mu.Unlock()
	go func() {
		<-run.Done()
		s.Engine.SetLoadToken("")
		res := run.Snapshot()
		s.Log.Info("call generator finished", "placed", res.Placed, "answered", res.Answered, "failed", res.Failed)
	}()
	s.audit(r, u.Username, "loadtest.start", strconv.Itoa(in.Calls)+" calls from "+in.From+" to "+in.To)
	writeJSON(w, 200, s.loadStatus())
}

func (s *Server) stopLoad(w http.ResponseWriter, r *http.Request, u store.User) {
	s.load.mu.Lock()
	run := s.load.run
	s.load.mu.Unlock()
	if run != nil {
		run.Stop()
		s.audit(r, u.Username, "loadtest.stop", "")
	}
	writeJSON(w, 200, s.loadStatus())
}
