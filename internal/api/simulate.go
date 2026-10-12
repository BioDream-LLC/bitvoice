package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

func (s *Server) simulateRoutes() {
	// Where would this call go? Nothing rings; scripts run in their sandbox as for a real call.
	s.handle("POST /api/simulate", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			From         string `json:"from"`
			To           string `json:"to"`
			Trunk        string `json:"trunk"`
			At           string `json:"at"` // YYYY-MM-DDTHH:MM in the system's time zone; empty is now
			AssumePhones bool   `json:"assumePhones"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		var p config.Problems
		in.From, in.To = strings.TrimSpace(in.From), strings.TrimSpace(in.To)
		if !dialable(strings.TrimPrefix(in.From, "+")) {
			p = append(p, config.Problem{Field: "from", Message: "enter the caller: an extension or a phone number"})
		}
		if in.To == "" || len(in.To) > 32 {
			p = append(p, config.Problem{Field: "to", Message: "enter the number they dial"})
		}
		cfg, _ := s.Store.Config()
		var at time.Time
		if in.At != "" {
			t, err := time.ParseInLocation("2006-01-02T15:04", in.At, cfg.System.Location())
			if err != nil {
				p = append(p, config.Problem{Field: "at", Message: "choose a date and time"})
			}
			at = t
		}
		if in.Trunk != "" && cfg.FindTrunk(in.Trunk) == nil {
			p = append(p, config.Problem{Field: "trunk", Message: "choose one of the trunks"})
		}
		if len(p) > 0 {
			writeErr(w, p)
			return
		}
		writeJSON(w, 200, s.Engine.Simulate(pbx.SimInput{From: in.From, To: in.To, Trunk: in.Trunk, At: at, AssumePhones: in.AssumePhones}))
	})
}
