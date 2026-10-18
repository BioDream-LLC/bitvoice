package api

import (
	"fmt"
	"github.com/biodream-llc/bitvoice/internal/config"
	"net/http"
	"time"

	"github.com/biodream-llc/bitvoice/internal/store"
)

func (s *Server) serverRoutes() {
	s.handle("GET /api/servers", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if s.Cluster == nil {
			writeJSON(w, 200, map[string]any{"shared": false, "postgres": s.Store.DB().Postgres})
			return
		}
		writeJSON(w, 200, map[string]any{"shared": true, "postgres": true, "self": s.Cluster.Self(), "nodes": s.Cluster.Nodes(),
			"address": s.sharedStatus()})
	})
	s.handle("PUT /api/servers/self", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if s.Cluster == nil {
			writeError(w, 409, "this server runs alone: move the data into PostgreSQL first (Settings, Database)")
			return
		}
		var in struct {
			Name       string `json:"name"`
			SIPAddr    string `json:"sipAddr"`
			PublicAddr string `json:"publicAddr"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		n, err := s.Cluster.SetSelf(in.Name, in.SIPAddr, in.PublicAddr)
		if err != nil {
			writeError(w, 422, err.Error())
			return
		}
		if s.Engine != nil {
			s.Engine.Apply(s.config()) // SIP and SDP carry the new public address from now on
		}
		s.audit(r, u.Username, "server.renamed", n.Name+" at "+n.SIPAddr+publicNote(n.PublicAddr))
		writeJSON(w, 200, n)
	})
	s.handle("DELETE /api/servers/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if s.Cluster == nil {
			writeError(w, 404, "no such server")
			return
		}
		if err := s.Cluster.Forget(r.PathValue("id")); err != nil {
			writeError(w, 409, err.Error())
			return
		}
		s.audit(r, u.Username, "server.forgotten", r.PathValue("id"))
		w.WriteHeader(204)
	})
}

func publicNote(a string) string {
	if a == "" {
		return ""
	}
	return ", public address " + a
}

// serverProblems warns about servers that stopped, and a shared address nobody can hold.
func (s *Server) serverProblems(add func(id, level, title, detail, link string)) {
	if s.Cluster == nil {
		return
	}
	if st := s.sharedStatus(); st.Error != "" {
		add("shared-address", "fail", "This server cannot hold the shared address", st.Error, "/servers")
	}
	names := map[string]string{}
	for _, n := range s.Cluster.Nodes() {
		names[n.ID] = n.Name
	}
	for _, n := range s.Cluster.Nodes() {
		if !n.Alive {
			continue
		}
		for id, why := range n.Info.Unreachable {
			if names[id] == "" {
				continue
			}
			add("server-reach-"+n.ID+"-"+id, "fail", n.Name+" cannot reach "+names[id],
				"Calls between them fail, and neither can carry on the other's calls: "+why+
					". Let the servers reach each other's SIP port (TCP, and TLS when they have certificates) and media ports (UDP) through any firewall between them.", "/servers")
		}
	}
	for _, n := range s.Cluster.Nodes() {
		if n.Alive {
			continue
		}
		ago := "just now"
		if d := time.Since(n.Seen); d >= time.Minute {
			ago = d.Round(time.Minute).String() + " ago"
		}
		add("server-"+n.ID, "fail", n.Name+" is not running",
			fmt.Sprintf("It last checked in %s. The running servers carried on its calls; its phones carry on if they were given a backup server, the DNS name or the shared address (Servers explains how). Start it again, or forget it if it is gone for good.", ago), "/servers")
	}
}

// backupServer is another running server for phones to fall back on (host:port), or "".
func (s *Server) backupServer() string {
	if s.Cluster == nil {
		return ""
	}
	for _, n := range s.Cluster.Nodes() {
		if !n.Self && n.Alive {
			return n.SIPAddr
		}
	}
	return ""
}

// backupFor is the backup server to give phones: none when the servers share one address.
func (s *Server) backupFor(cfg config.Config) string {
	if cfg.System.Cluster.SharedAddress != "" {
		return ""
	}
	return s.backupServer()
}
