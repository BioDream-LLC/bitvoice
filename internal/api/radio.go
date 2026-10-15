package api

import (
	"net/http"
	"strconv"

	"github.com/biodream-llc/bitvoice/internal/store"
)

// The radio console's controls. A dispatcher joins by dialling the console number from their phone
// or the browser phone; these act on that call's position.
func (s *Server) radioRoutes() {
	s.handle("GET /api/radio", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, s.Engine.Console())
	})
	s.handle("POST /api/radio/ptt", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Call    string `json:"call"`
			Channel string `json:"channel"`
			On      bool   `json:"on"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.Engine.PTT(in.Call, in.Channel, in.On); err != nil {
			writeError(w, 409, err.Error())
			return
		}
		w.WriteHeader(204)
	})
	s.handle("POST /api/radio/simulselect", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Call     string   `json:"call"`
			Channels []string `json:"channels"`
			On       bool     `json:"on"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.Engine.SimulSelect(in.Call, in.Channels, in.On); err != nil {
			writeError(w, 409, err.Error())
			return
		}
		w.WriteHeader(204)
	})
	s.handle("POST /api/radio/channels/{id}/ack", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		a, err := s.Engine.AckEmergency(r.PathValue("id"), firstNonEmpty(u.Name, u.Username))
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
		s.audit(r, u.Username, "radio.emergency.ack", a.ChannelName+" "+a.Unit)
		writeJSON(w, 200, a)
	})
	s.handle("POST /api/radio/monitor", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Call     string   `json:"call"`
			Channels []string `json:"channels"` // null: all
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.Engine.Monitor(in.Call, in.Channels); err != nil {
			writeError(w, 409, err.Error())
			return
		}
		w.WriteHeader(204)
	})
	s.handle("POST /api/radio/patch", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			A, B string
			On   bool
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.Engine.Patch(in.A, in.B, in.On); err != nil {
			writeError(w, 409, err.Error())
			return
		}
		s.audit(r, u.Username, "radio.patch", in.A+" "+in.B)
		w.WriteHeader(204)
	})
	s.handle("POST /api/radio/tone", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Call    string `json:"call"`
			Channel string `json:"channel"`
			Tone    string `json:"tone"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.Engine.AlertTone(in.Call, in.Channel, in.Tone); err != nil {
			writeError(w, 409, err.Error())
			return
		}
		s.audit(r, u.Username, "radio.tone", in.Channel+" "+in.Tone)
		w.WriteHeader(204)
	})
	// A private (unit-to-unit) call from a dispatcher's position to a radio, and hanging one up.
	s.handle("POST /api/radio/unitcall", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Call      string `json:"call"`
			Unit      int    `json:"unit"`
			Emergency bool   `json:"emergency"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		id, err := s.Engine.UnitCall(in.Call, in.Unit, in.Emergency)
		if err != nil {
			writeError(w, 409, err.Error())
			return
		}
		s.audit(r, u.Username, "radio.unitcall", strconv.Itoa(in.Unit))
		writeJSON(w, 200, map[string]string{"channel": id})
	})
	s.handle("POST /api/radio/hangup", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Channel string `json:"channel"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.Engine.EndUnitCall(in.Channel); err != nil {
			writeError(w, 404, err.Error())
			return
		}
		s.audit(r, u.Username, "radio.hangup", in.Channel)
		w.WriteHeader(204)
	})
}
