package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/prompts"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// maxMusicSeconds is the longest hold music track: fifteen minutes.
const maxMusicSeconds = 15 * 60

// soundRoutes serve the prompts callers hear, per language, and the hold music tracks.
func (s *Server) soundRoutes() {
	s.handle("GET /api/prompts", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg, _ := s.Store.Config()
		type lang struct {
			Code    string `json:"code"`
			Name    string `json:"name"`
			Shipped bool   `json:"shipped"`
		}
		langs := []lang{}
		for _, l := range prompts.Languages {
			langs = append(langs, lang{l.Code, l.Name, true})
		}
		for _, l := range cfg.System.PromptLanguages {
			if !prompts.Known(l) {
				langs = append(langs, lang{l, l, false})
			}
		}
		recorded := map[string][]string{}
		for _, box := range s.Store.Greetings() {
			rest, ok := strings.CutPrefix(box, "prompt-")
			if !ok {
				continue
			}
			for _, p := range prompts.All {
				if l, ok := strings.CutSuffix(rest, "-"+p.Key); ok {
					recorded[l] = append(recorded[l], p.Key)
					break
				}
			}
		}
		a := cfg.System.AI
		writeJSON(w, 200, map[string]any{"language": cfg.System.Language, "languages": langs, "prompts": prompts.All,
			"recorded": recorded, "speech": a.Enabled && a.SpeechModel != ""})
	})
	promptBox := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		lang, key := r.PathValue("lang"), r.PathValue("key")
		cfg, _ := s.Store.Config()
		if _, ok := prompts.Find(key); !ok || !slices.Contains(cfg.PromptLanguageCodes(shippedCodes()), lang) {
			writeError(w, http.StatusNotFound, "no prompt "+key+" in "+lang)
			return "", false
		}
		return prompts.Box(lang, key), true
	}
	s.handle("GET /api/prompts/{lang}/{key}", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		box, ok := promptBox(w, r)
		if !ok {
			return
		}
		b, err := s.Store.Greeting(box)
		if err != nil {
			writeError(w, http.StatusNotFound, "not recorded")
			return
		}
		serveWAV(w, r, box+".wav", b)
	})
	s.handle("PUT /api/prompts/{lang}/{key}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if box, ok := promptBox(w, r); ok {
			s.putAudio(w, r, u, box, "prompt.record")
		}
	})
	s.handle("DELETE /api/prompts/{lang}/{key}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		box, ok := promptBox(w, r)
		if !ok {
			return
		}
		if err := s.Store.SetGreeting(box, nil); err != nil && !errors.Is(err, store.ErrNotFound) {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "prompt.delete", box)
		w.WriteHeader(http.StatusNoContent)
	})
	// generate has the speech service say every prompt of a language BitVoice has the words of.
	s.handle("POST /api/prompts/{lang}/generate", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		var in struct {
			Overwrite bool `json:"overwrite"`
		}
		_ = readJSON(r, &in)
		lang := r.PathValue("lang")
		if !prompts.Known(lang) {
			writeError(w, http.StatusUnprocessableEntity, "BitVoice does not have the words of "+lang+": record or upload its prompts")
			return
		}
		cfg, _ := s.Store.Config()
		a := cfg.System.AI
		if !a.Enabled || a.SpeechModel == "" {
			writeError(w, http.StatusUnprocessableEntity, "set up a speech model in Settings, AI first")
			return
		}
		client := s.aiClient(a)
		made, skipped := 0, 0
		var failed []string
		for _, p := range prompts.All {
			box := prompts.Box(lang, p.Key)
			if _, err := s.Store.Greeting(box); err == nil && !in.Overwrite {
				skipped++
				continue
			}
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			wav, err := client.Speak(ctx, a.SpeechModel, a.Voice, p.Text[lang])
			cancel()
			var pcm []int16
			if err == nil {
				pcm, err = media.DecodeWAV(wav)
			}
			if err == nil {
				err = s.Store.SetGreeting(box, media.EncodeWAV(media.TrimSilence(pcm)))
			}
			if err != nil {
				failed = append(failed, p.Key)
				if r.Context().Err() != nil {
					break
				}
				continue
			}
			made++
		}
		s.audit(r, u.Username, "prompt.generate", lang)
		writeJSON(w, 200, map[string]any{"made": made, "skipped": skipped, "failed": failed})
	})

	// An extension's spoken name, which the dial-by-name directory says.
	s.handle("GET /api/extensions/{n}/name-audio", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		b, err := s.Store.Greeting(pbx.NameBox(r.PathValue("n")))
		if err != nil {
			writeError(w, http.StatusNotFound, "no name recorded")
			return
		}
		serveWAV(w, r, "name.wav", b)
	})
	s.handle("PUT /api/extensions/{n}/name-audio", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg, _ := s.Store.Config()
		if cfg.FindExtension(r.PathValue("n")) == nil {
			writeError(w, http.StatusNotFound, "no extension "+r.PathValue("n"))
			return
		}
		s.putAudio(w, r, u, pbx.NameBox(r.PathValue("n")), "extension.name")
	})
	s.handle("DELETE /api/extensions/{n}/name-audio", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.Store.SetGreeting(pbx.NameBox(r.PathValue("n")), nil); err != nil && !errors.Is(err, store.ErrNotFound) {
			writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.handle("GET /api/music", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		cfg, _ := s.Store.Config()
		out := map[string][]store.MusicFile{}
		for _, m := range cfg.System.Music {
			out[m.ID] = s.Store.MusicFiles(m.ID)
		}
		writeJSON(w, 200, out)
	})
	musicClass := func(w http.ResponseWriter, r *http.Request) bool {
		cfg, _ := s.Store.Config()
		if cfg.FindMusic(r.PathValue("class")) == nil {
			writeError(w, http.StatusNotFound, "no hold music "+r.PathValue("class")+": save the class first")
			return false
		}
		return true
	}
	// The internet radio streams playing to callers on hold now.
	s.handle("GET /api/music/streams", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		out := []pbx.StreamStatus{}
		if s.Engine != nil {
			out = append(out, s.Engine.MusicStreams()...)
		}
		writeJSON(w, 200, out)
	})
	s.handle("GET /api/music/{class}/{name}", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		b, err := s.Store.MusicAudio(r.PathValue("class"), r.PathValue("name"))
		if err != nil {
			writeError(w, http.StatusNotFound, "no such track")
			return
		}
		serveWAV(w, r, r.PathValue("name")+".wav", b)
	})
	s.handle("PUT /api/music/{class}/{name}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if !musicClass(w, r) {
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 40<<20))
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "the track is too large")
			return
		}
		pcm, err := media.DecodeAudio(raw)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		if len(pcm) < media.Rate {
			writeError(w, http.StatusUnprocessableEntity, "the track is shorter than a second")
			return
		}
		if len(pcm) > maxMusicSeconds*media.Rate {
			writeError(w, http.StatusUnprocessableEntity, "tracks can be up to fifteen minutes long")
			return
		}
		if err := s.Store.AddMusic(r.PathValue("class"), r.PathValue("name"), media.EncodeWAV(pcm)); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusUnprocessableEntity, "name the track with letters, digits, - and _ only")
				return
			}
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "music.add", r.PathValue("class")+"/"+r.PathValue("name"))
		writeJSON(w, 200, map[string]any{"seconds": len(pcm) / media.Rate})
	})
	s.handle("DELETE /api/music/{class}/{name}", true, func(w http.ResponseWriter, r *http.Request, u store.User) {
		if err := s.Store.DeleteMusic(r.PathValue("class"), r.PathValue("name")); err != nil {
			writeErr(w, err)
			return
		}
		s.audit(r, u.Username, "music.delete", r.PathValue("class")+"/"+r.PathValue("name"))
		w.WriteHeader(http.StatusNoContent)
	})
}

func shippedCodes() []string {
	out := make([]string, len(prompts.Languages))
	for i, l := range prompts.Languages {
		out[i] = l.Code
	}
	return out
}
