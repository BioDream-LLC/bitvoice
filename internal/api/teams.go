package api

import (
	"net/http"
	"time"

	"github.com/biodream-llc/bitvoice/internal/acme"
	"github.com/biodream-llc/bitvoice/internal/store"
)

func (s *Server) certificateRoutes() {
	s.handle("GET /api/certificate", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if s.Certs == nil {
			writeError(w, 404, "this server has no certificate")
			return
		}
		writeJSON(w, 200, s.Certs.Info())
	})
	// The Let's Encrypt manager: what it last did, and "try now".
	s.handle("GET /api/certificate/acme", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		st := acme.Status{State: "off"}
		if s.ACMEStatus != nil {
			st = s.ACMEStatus()
		}
		writeJSON(w, 200, st)
	})
	s.handle("POST /api/certificate/acme", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if s.ACMEWake == nil {
			writeError(w, 404, "this server gets no certificates itself")
			return
		}
		s.ACMEWake()
		s.audit(r, u.Username, "certificate.acme", "check now")
		writeJSON(w, 202, map[string]bool{"ok": true})
	})
	// Install a certificate from a public authority (with its chain) and its key, used at once for the
	// web interface, SIP over TLS and connections out (Teams).
	s.handle("PUT /api/certificate", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if s.Certs == nil {
			writeError(w, 404, "this server has no certificate")
			return
		}
		var in struct {
			Cert string `json:"cert"`
			Key  string `json:"key"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		info, err := s.Certs.Install(in.Cert, in.Key)
		if err != nil {
			writeError(w, 422, err.Error())
			return
		}
		s.audit(r, u.Username, "certificate.install", info.Subject)
		writeJSON(w, 200, info)
	})
}

// teamsProblems checks each Teams trunk: a public certificate that covers its SBC name, and Microsoft
// answering.
func (s *Server) teamsProblems(add func(id, level, title, detail, link string)) {
	cfg := s.config()
	states := map[string]string{}
	if s.Engine != nil {
		for _, t := range s.Engine.TrunkStatus() {
			states[t.ID] = t.State
		}
	}
	for _, t := range cfg.Trunks {
		if !t.Enabled || !t.Teams {
			continue
		}
		id := "teams-" + t.ID
		switch {
		case s.Certs == nil:
			add(id+"-cert", "fail", "Teams: no certificate", "Teams Direct Routing needs a TLS certificate for "+t.SBCName+".", "/trunks")
		case s.Certs.Info().SelfSigned:
			add(id+"-cert", "fail", "Teams: the certificate is self-signed", "Microsoft accepts only a certificate from a public authority for "+t.SBCName+". Install one in Settings, Certificate.", "/trunks")
		case !s.Certs.Covers(t.SBCName):
			add(id+"-cert", "fail", "Teams: the certificate does not cover "+t.SBCName, "Install a certificate whose names include "+t.SBCName+" (or a wildcard for its domain).", "/trunks")
		case time.Until(s.Certs.Info().NotAfter) < 21*24*time.Hour:
			add(id+"-cert", "warn", "Teams: the certificate ends soon", "It ends on "+s.Certs.Info().NotAfter.Format("2 January 2006")+"; Teams calls stop then. Install a new one.", "/trunks")
		}
		if st := states[t.ID]; st != "" && st != "reachable" {
			add(id+"-up", "warn", "Teams: Microsoft is not answering "+t.Name, "BitVoice sends OPTIONS to "+t.Host+" every minute and gets no answer. Check that the SBC is added in Teams (Get-CsOnlinePSTNGateway), that port 5061 is open both ways to 52.112.0.0/14 and 52.120.0.0/14, and the certificate.", "/trunks")
		}
	}
}
