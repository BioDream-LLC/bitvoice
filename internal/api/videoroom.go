package api

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// videoRoomRoutes serve SFU video rooms (a conference room with Video set to everyone's video):
// browsers send their camera, and get every other camera, one stream each.
// PhoneHeader names the browser phone asking (its ID, which only its own page holds): a video room
// opened from a phone (the mobile app, the staff portal) joins as that phone's extension.
const PhoneHeader = "X-BitVoice-Phone"

// roomRoute is handle for the video rooms, which a browser phone may also use as its extension.
func (s *Server) roomRoute(pattern string, f func(w http.ResponseWriter, r *http.Request, u store.User)) {
	s.noteRoute(pattern, false, false)
	signed := s.signedIn(pattern, false, f)
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(PhoneHeader)
		if id == "" {
			signed(w, r)
			return
		}
		bp := phoneByID(w, id)
		if bp == nil {
			return
		}
		if s.Tenant != nil && s.Tenant.Suspended {
			writeError(w, http.StatusForbidden, "this organisation's account is suspended; ask your provider")
			return
		}
		f(w, r, store.User{Username: "ext:" + bp.ext, Name: s.extName(bp.ext), Role: "phone"})
	})
}

func (s *Server) videoRoomRoutes() {
	// room checks the room exists and, for a room with a PIN, that the PIN came (X-Room-PIN).
	room := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		n := r.PathValue("number")
		cfg, _ := s.Store.Config()
		c := cfg.FindConference(n)
		if c == nil || c.Video != "sfu" {
			writeError(w, http.StatusNotFound, pbx.ErrNoSFURoom.Error())
			return "", false
		}
		if c.PIN != "" {
			pin := r.Header.Get("X-Room-PIN")
			okMember := subtle.ConstantTimeCompare([]byte(pin), []byte(c.PIN)) == 1
			okLeader := c.LeaderPIN != "" && subtle.ConstantTimeCompare([]byte(pin), []byte(c.LeaderPIN)) == 1
			if !okMember && !okLeader {
				writeError(w, http.StatusForbidden, "this room has a PIN")
				return "", false
			}
		}
		return n, true
	}
	fail := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, pbx.ErrNoSFURoom), errors.Is(err, pbx.ErrSFUGone):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, pbx.ErrSFUFull), errors.Is(err, pbx.ErrSFUScreenTaken):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusUnprocessableEntity, err.Error())
		}
	}
	s.roomRoute("GET /api/video-rooms/{number}", func(w http.ResponseWriter, r *http.Request, u store.User) {
		n, ok := room(w, r)
		if !ok {
			return
		}
		st, err := s.Engine.SFURoomOf(n)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, st)
	})
	// publish takes the camera, or a shared screen: {sdp, name, kind} -> {id, sdp}.
	s.roomRoute("POST /api/video-rooms/{number}/publish", func(w http.ResponseWriter, r *http.Request, u store.User) {
		n, ok := room(w, r)
		if !ok {
			return
		}
		var in struct {
			SDP  string `json:"sdp"`
			Name string `json:"name"`
			Kind string `json:"kind"` // camera (default) or screen
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = firstNonEmpty(u.Name, u.Username)
		}
		if len(name) > 60 {
			name = name[:60]
		}
		ans, id, err := s.Engine.SFUPublish(n, name, in.Kind, []byte(in.SDP), s.mediaHostFor(r), s.iceOptions(r))
		if err != nil {
			fail(w, err)
			return
		}
		if in.Kind == "screen" {
			s.audit(r, u.Username, "videoroom.screen", n)
		} else {
			s.audit(r, u.Username, "videoroom.publish", n)
		}
		writeJSON(w, 200, map[string]string{"id": id, "sdp": string(ans)})
	})
	// subscribe gets one camera: {publisher, sdp, layer} -> {id, sdp}.
	s.roomRoute("POST /api/video-rooms/{number}/subscribe", func(w http.ResponseWriter, r *http.Request, u store.User) {
		n, ok := room(w, r)
		if !ok {
			return
		}
		var in struct {
			Publisher string `json:"publisher"`
			SDP       string `json:"sdp"`
			Layer     string `json:"layer"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if in.Layer != "" && in.Layer != "high" && in.Layer != "low" {
			writeError(w, http.StatusUnprocessableEntity, "layer is high or low")
			return
		}
		ans, id, err := s.Engine.SFUSubscribe(n, in.Publisher, in.Layer, []byte(in.SDP), s.mediaHostFor(r), s.iceOptions(r))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"id": id, "sdp": string(ans)})
	})
	s.roomRoute("PATCH /api/video-rooms/{number}/subscribe/{id}", func(w http.ResponseWriter, r *http.Request, u store.User) {
		n, ok := room(w, r)
		if !ok {
			return
		}
		var in struct {
			Layer string `json:"layer"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if in.Layer != "high" && in.Layer != "low" {
			writeError(w, http.StatusUnprocessableEntity, "layer is high or low")
			return
		}
		if err := s.Engine.SFULayer(n, r.PathValue("id"), in.Layer); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.roomRoute("DELETE /api/video-rooms/{number}/{id}", func(w http.ResponseWriter, r *http.Request, u store.User) {
		n, ok := room(w, r)
		if !ok {
			return
		}
		if !s.Engine.SFULeave(n, r.PathValue("id")) {
			writeError(w, http.StatusNotFound, pbx.ErrSFUGone.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
