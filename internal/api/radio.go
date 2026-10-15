package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/biodream-llc/bitvoice/internal/pbx"

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
	// Radio positions, from a radio data or AVL gateway (an API token with the gateway's own account).
	s.handle("POST /api/radio/locations", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in []pbx.RadioFix
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeError(w, 400, "the body is too large")
			return
		}
		if len(bytes.TrimSpace(body)) > 0 && bytes.TrimSpace(body)[0] == '{' { // one report, or a list
			var one pbx.RadioFix
			err = json.Unmarshal(body, &one)
			in = []pbx.RadioFix{one}
		} else {
			err = json.Unmarshal(body, &in)
		}
		if err != nil || len(in) == 0 || len(in) > 500 {
			writeError(w, 400, "send a position report, {\"unit\": 1205, \"lat\": 40.26, \"lon\": -76.88}, or a list of up to 500")
			return
		}
		out := make([]pbx.RadioFix, 0, len(in))
		for i, f := range in {
			g, err := s.Engine.RadioLocation(f)
			if err != nil {
				writeError(w, 400, fmt.Sprintf("report %d: %s", i+1, err))
				return
			}
			out = append(out, g)
		}
		writeJSON(w, 200, map[string]any{"items": out})
	})
	s.handle("GET /api/radio/locations", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		writeJSON(w, 200, map[string]any{"items": s.Engine.RadioLocations()})
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
