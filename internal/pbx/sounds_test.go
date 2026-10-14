package pbx

import (
	"testing"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/prompts"
	"github.com/biodream-llc/bitvoice/internal/store"
)

type fakeMusic map[string][]int16 // track name -> audio, all in class "jazz"

func (f fakeMusic) MusicFiles(class string) []store.MusicFile {
	if class != "jazz" {
		return nil
	}
	return []store.MusicFile{{Name: "a"}, {Name: "b"}}
}

func (f fakeMusic) MusicAudio(class, name string) ([]byte, error) {
	return media.EncodeWAV(f[name]), nil
}

func TestHoldMusicClasses(t *testing.T) {
	cfg := testConfig()
	cfg.System.Music = []config.MusicClass{{ID: "jazz", Name: "Jazz"}, {ID: "calm", Name: "Calm"}}
	cfg.System.HoldMusic = "jazz"
	cfg.Extensions[1].HoldMusic = "tone"
	cfg.Extensions[2].HoldMusic = "calm"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if holdClassOf(&cfg, "100") != "jazz" || holdClassOf(&cfg, "101") != "" || holdClassOf(&cfg, "102") != "calm" {
		t.Fatal("hold class resolution")
	}
	if holdClass(&cfg, "missing") != "jazz" {
		t.Fatal("a removed class should fall back to the system's")
	}
	e := &Engine{Music: fakeMusic{"a": make([]int16, 800), "b": make([]int16, 1600)}}
	src := e.musicSource(&cfg, "jazz")
	if src == nil {
		t.Fatal("no source")
	}
	if a, b, a2 := len(src()), len(src()), len(src()); a != 800 || b != 1600 || a2 != 800 {
		t.Fatalf("tracks in order and round again: %d %d %d", a, b, a2)
	}
	if e.musicSource(&cfg, "calm") != nil {
		t.Fatal("a class with no tracks should play the tone")
	}
	bad := cfg
	bad.System.HoldMusic = "nope"
	if bad.Validate() == nil {
		t.Fatal("unknown hold music accepted")
	}
}

type fakePrompts map[string][]int16

func (f fakePrompts) Greeting(box string) ([]byte, error) {
	if pcm, ok := f[box]; ok {
		return media.EncodeWAV(pcm), nil
	}
	return nil, store.ErrNotFound
}

func TestPromptFallbacks(t *testing.T) {
	cfg := testConfig()
	cfg.System.Language = "es"
	e := &Engine{Mail: promptMail{fakePrompts{
		prompts.Box("es", "goodbye"): make([]int16, 100),
		prompts.Box("en", "goodbye"): make([]int16, 200),
		prompts.Box("en", "digit-1"): make([]int16, 300),
		prompts.Box("fr", "digit-1"): make([]int16, 400),
	}}}
	if n := len(e.promptAudio(&cfg, "fr", "goodbye")); n != 100 {
		t.Fatalf("French missing: the system's Spanish should play, got %d", n)
	}
	if n := len(e.promptAudio(&cfg, "fr", "digit-1")); n != 400 {
		t.Fatalf("French digit, got %d", n)
	}
	if n := len(e.promptAudio(&cfg, "de", "digit-1")); n != 300 {
		t.Fatalf("English last, got %d", n)
	}
	pcm, ok := e.promptSamples(&cfg, "es", "goodbye", "invalid", "digit-1")
	if !ok || len(pcm) != 100+300+media.Rate/8 {
		t.Fatalf("joined %d %v", len(pcm), ok)
	}
	if _, ok := e.promptSamples(&cfg, "es", "invalid"); ok {
		t.Fatal("nothing recorded should report false")
	}
	if got := prompts.Digits("4a2"); len(got) != 2 || got[0] != "digit-4" || got[1] != "digit-2" {
		t.Fatal(got)
	}
	for _, p := range prompts.All {
		for _, l := range prompts.Languages {
			if p.Text[l.Code] == "" {
				t.Fatalf("prompt %s has no %s words", p.Key, l.Code)
			}
		}
		if len(prompts.Box("pt-BR", p.Key)) > 40 {
			t.Fatalf("box name too long for %s", p.Key)
		}
	}
}

// promptMail is a Mailbox that only has greetings.
type promptMail struct{ fakePrompts }

func (promptMail) AddVoicemail(v store.Voicemail, wav []byte) (store.Voicemail, error) { return v, nil }
func (promptMail) Voicemails(string) ([]store.Voicemail, error)                        { return nil, nil }
func (promptMail) VoicemailAudio(string, string) ([]byte, error)                       { return nil, store.ErrNotFound }
func (promptMail) VoicemailCounts(string) (int, int)                                   { return 0, 0 }
func (promptMail) SetHeard(string, string, bool) (store.Voicemail, error) {
	return store.Voicemail{}, nil
}
func (promptMail) DeleteVoicemail(string, string) error { return nil }
