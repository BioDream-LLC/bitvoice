package pbx

import (
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/script"
)

// ScriptRun is the outcome of one script run, published for the script editor's live log.
type ScriptRun struct {
	Script   string    `json:"script"`
	At       time.Time `json:"at"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	Decision string    `json:"decision"`
	Error    string    `json:"error,omitempty"`
	Log      []string  `json:"log,omitempty"`
	Millis   float64   `json:"millis"`
}

// scriptCall is the call a destination script is deciding.
type scriptCall struct {
	from, fromName, to, direction, trunk string
	real                                 bool // a real call (not a simulation): calls forwarded out are judged
}

// runScript lets a script choose the destination; anything wrong uses the script's fallback.
func (e *Engine) runScript(cfg *config.Config, s *config.Script, depth int, sc scriptCall) plan {
	now := time.Now().In(cfg.System.Location())
	in := script.Call{From: sc.from, FromName: sc.fromName, To: sc.to, Direction: sc.direction, Trunk: sc.trunk,
		Weekday: int(now.Weekday()), Hour: now.Hour(), Minute: now.Minute(), Date: now.Format("2006-01-02")}
	start := time.Now()
	d, err := script.Route(s.Language, s.Source, in)
	run := ScriptRun{Script: s.ID, At: start.UTC(), From: in.From, To: in.To, Log: d.Log,
		Millis: float64(time.Since(start).Microseconds()) / 1000}
	next := s.Fallback
	switch {
	case err != nil:
		run.Error = err.Error()
		e.Log.Warn("routing script failed; using its fallback", "script", s.Name, "err", err)
	case d.Type == "" || d.Type == "script":
	default:
		cand := config.Destination{Type: d.Type, Target: d.Target}
		if p := e.resolveFor(cfg, cand, depth+1, sc); p.reject != 404 && p.reject != 482 {
			run.Decision = d.Type + " " + d.Target
			e.publish("script.ran", run)
			return because(p, "The script "+s.Name+" sent the call to "+destinationName(cfg, cand)+".")
		}
		run.Error = "the script chose " + d.Type + " " + d.Target + ", which does not exist"
	}
	run.Decision = "fallback: " + next.Type + " " + next.Target
	e.publish("script.ran", run)
	why := "The script " + s.Name + " chose nothing, so its fallback took the call: " + destinationName(cfg, next) + "."
	if run.Error != "" {
		why = "The script " + s.Name + " failed (" + run.Error + "), so its fallback took the call: " + destinationName(cfg, next) + "."
	}
	return because(e.resolveFor(cfg, next, depth+1, sc), why)
}
