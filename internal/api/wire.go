package api

import (
	"context"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/pbx"
)

// WireEngine connects the engine to this server's store and services: voicemail, recordings, call
// records, callbacks, the directory, live state kept across restarts, faxes, 911 and the configuration
// in force. The main system and every hosted tenant are wired the same way.
func (s *Server) WireEngine() {
	eng, st := s.Engine, s.Store
	eng.Mail = st
	eng.Music = st
	eng.Recordings = st
	eng.SeenDevice = st.NoteDevice
	eng.Remote = st
	eng.Callback = s.BookCallback
	eng.NameFor = s.NameFor
	eng.LastAgent = s.LastAgent
	eng.Brief = s.Brief
	eng.AIAllow = s.AIAllow
	log := s.Log
	var desks []pbx.HotDesk
	if err := st.LoadState("hotdesk", &desks); err != nil {
		log.Warn("could not read hot desk sign-ins", "err", err)
	}
	eng.LoadHotDesks(desks)
	eng.SaveHotDesks = func(h []pbx.HotDesk) error { return st.SaveState("hotdesk", h) }
	var div pbx.DivertState
	if err := st.LoadState("divert", &div); err != nil {
		log.Warn("could not read call forwarding", "err", err)
	}
	eng.LoadDiverts(div)
	eng.SaveDiverts = func(d pbx.DivertState) error { return st.SaveState("divert", d) }
	eng.Mobile = s
	eng.SetEmergencyMode = s.SetEmergencyMode
	eng.OnFax = s.FaxReceived
	eng.OnEmergencyAnswer = s.EmergencyAnswered
	eng.FindService = s.FindService
	cfg, _ := st.Config()
	eng.Apply(s.Resolved(cfg))
	st.OnChange(func(c config.Config) { eng.Apply(s.Resolved(c)) })
}

// RunBackground starts the server's background work. main is the main system: only it holds the
// shared address of a cluster.
func (s *Server) RunBackground(ctx context.Context, main bool) {
	go s.RunAI(ctx)
	go s.RunCRM(ctx)
	go s.RunMetrics(ctx)
	go s.RunCosts(ctx)
	go s.RunCalendars(ctx)
	if main {
		go s.RunSharedAddress(ctx)
	}
	go s.RunArchive(ctx)
	go s.RunEmail(ctx)
	go s.RunFaxes(ctx)
	go s.RunFaxMail(ctx)
	go s.RunPSAPLog(ctx)
	go s.RunAudit(ctx)
	go s.RunRadioCAD(ctx)
	go s.RunGitOps(ctx)
	go s.RunEmergencyEmail(ctx)
}
