package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/store"
)

const maxGreetingSeconds = 120

func (s *Server) voicemailRoutes() {
	s.handle("GET /api/voicemail", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		list, err := s.Store.Voicemails(r.URL.Query().Get("box"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": list, "greetings": s.Store.Greetings()})
	})
	s.handle("GET /api/voicemail/{box}/{id}/audio", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		b, err := s.Store.VoicemailAudio(r.PathValue("box"), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "no such message")
			return
		}
		serveWAV(w, r, "voicemail-"+r.PathValue("id")+".wav", b)
	})
	s.handle("PATCH /api/voicemail/{box}/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var body struct {
			Heard bool `json:"heard"`
		}
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		box := r.PathValue("box")
		v, err := s.Store.SetHeard(box, r.PathValue("id"), body.Heard)
		if err != nil {
			writeError(w, http.StatusNotFound, "no such message")
			return
		}
		s.Engine.MailboxChanged(box)
		writeJSON(w, 200, v)
	})
	s.handle("DELETE /api/voicemail/{box}/{id}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		box := r.PathValue("box")
		if err := s.Store.DeleteVoicemail(box, r.PathValue("id")); err != nil {
			if errors.Is(err, store.ErrHeld) {
				writeErr(w, err)
				return
			}
			writeError(w, http.StatusNotFound, "no such message")
			return
		}
		s.audit(r, u.Username, "voicemail.delete", "mailbox "+box)
		s.Engine.MailboxChanged(box)
		w.WriteHeader(http.StatusNoContent)
	})

	// Call recordings, by the name in the call record.
	s.handle("GET /api/recordings/{month}/{name}", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		b, err := s.Store.Recording(r.PathValue("month") + "/" + r.PathValue("name"))
		if err != nil {
			writeError(w, http.StatusNotFound, "no such recording")
			return
		}
		s.audit(r, u.Username, "recording.play", r.PathValue("month")+"/"+r.PathValue("name"))
		serveWAV(w, r, "call-"+r.PathValue("name")+".wav", b)
	})
	s.handle("DELETE /api/recordings/{month}/{name}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		name := r.PathValue("month") + "/" + r.PathValue("name")
		if err := s.Store.DeleteRecording(name); err != nil {
			if errors.Is(err, store.ErrHeld) {
				writeErr(w, err)
				return
			}
			writeError(w, http.StatusNotFound, "no such recording")
			return
		}
		s.audit(r, u.Username, "recording.delete", name)
		w.WriteHeader(http.StatusNoContent)
	})

	s.handle("GET /api/voicemail/{box}/greeting", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		b, err := s.Store.Greeting(r.PathValue("box"))
		if err != nil {
			writeError(w, http.StatusNotFound, "this mailbox uses the default greeting")
			return
		}
		serveWAV(w, r, "greeting.wav", b)
	})
	s.handle("PUT /api/voicemail/{box}/greeting", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		box := r.PathValue("box")
		cfg, _ := s.Store.Config()
		if cfg.FindExtension(box) == nil {
			writeError(w, http.StatusNotFound, "no extension "+box)
			return
		}
		s.putAudio(w, r, u, box, "voicemail.greeting")
	})
	// The "this call is recorded" notice played when recording's notice is announce.
	s.handle("GET /api/recording/notice", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		b, err := s.Store.Greeting("recording-notice")
		if err != nil {
			writeError(w, http.StatusNotFound, "no notice recorded")
			return
		}
		serveWAV(w, r, "notice.wav", b)
	})
	s.handle("PUT /api/recording/notice", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		s.putAudio(w, r, u, "recording-notice", "recording.notice")
	})
	s.handle("DELETE /api/recording/notice", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.Store.SetGreeting("recording-notice", nil); err != nil && !errors.Is(err, store.ErrNotFound) {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "recording.notice.reset", "")
		w.WriteHeader(http.StatusNoContent)
	})
	s.handle("GET /api/menus/{id}/prompt", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		b, err := s.Store.Greeting("menu-" + r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "this menu has no recorded prompt")
			return
		}
		serveWAV(w, r, "prompt.wav", b)
	})
	s.handle("PUT /api/menus/{id}/prompt", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg, _ := s.Store.Config()
		if cfg.FindMenu(r.PathValue("id")) == nil {
			writeError(w, http.StatusNotFound, "no such menu")
			return
		}
		s.putAudio(w, r, u, "menu-"+r.PathValue("id"), "menu.prompt")
	})
	s.handle("DELETE /api/menus/{id}/prompt", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.Store.SetGreeting("menu-"+r.PathValue("id"), nil); err != nil && !errors.Is(err, store.ErrNotFound) {
			writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.handle("DELETE /api/voicemail/{box}/greeting", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		box := r.PathValue("box")
		if err := s.Store.SetGreeting(box, nil); err != nil && !errors.Is(err, store.ErrNotFound) {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "voicemail.greeting.reset", "mailbox "+box)
		w.WriteHeader(http.StatusNoContent)
	})
}

// serveWAV serves audio with range support, so a browser can seek in a long message.
func serveWAV(w http.ResponseWriter, r *http.Request, name string, b []byte) {
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Content-Disposition", `inline; filename="`+name+`"`)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
}

// putAudio stores an uploaded recording (any WAV, converted to 8 kHz) as a greeting or menu prompt.
func (s *Server) putAudio(w http.ResponseWriter, r *http.Request, u store.User, box, action string) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 40<<20))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "the recording is too large")
		return
	}
	pcm, err := media.DecodeWAV(raw)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if len(pcm) < media.Rate/2 {
		writeError(w, http.StatusUnprocessableEntity, "the recording is shorter than half a second")
		return
	}
	if len(pcm) > maxGreetingSeconds*media.Rate {
		writeError(w, http.StatusUnprocessableEntity, "recordings can be up to two minutes long")
		return
	}
	if cfg, _ := s.Store.Config(); cfg.System.Audio.Voicemail {
		pcm = media.Clean(pcm, media.Rate)
	}
	if err := s.Store.SetGreeting(box, media.EncodeWAV(pcm)); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, u.Username, action, box)
	writeJSON(w, 200, map[string]any{"seconds": len(pcm) / media.Rate})
}
