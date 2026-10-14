package pbx

import (
	"math/rand/v2"
	"strings"
	"sync"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// MusicLibrary stores hold music tracks. *store.Store implements it.
type MusicLibrary interface {
	MusicFiles(class string) []store.MusicFile
	MusicAudio(class, name string) ([]byte, error)
}

// holdClass is the hold music class to play: the first one named, else the system's. "" means the tone.
func holdClass(cfg *config.Config, named ...string) string {
	for _, n := range named {
		if n == "tone" {
			return ""
		}
		if n != "" && cfg.FindMusic(n) != nil {
			return n
		}
	}
	return cfg.System.HoldMusic
}

// musicSource returns the tracks of a class one after another (shuffled when the class says so), for
// Player.PlayStream; nil when the class has no tracks, so the tone plays.
func (e *Engine) musicSource(cfg *config.Config, class string) func() []int16 {
	mc := cfg.FindMusic(class)
	if mc == nil || e.Music == nil {
		return nil
	}
	files := e.Music.MusicFiles(class)
	if len(files) == 0 {
		return nil
	}
	if mc.Shuffle {
		rand.Shuffle(len(files), func(i, j int) { files[i], files[j] = files[j], files[i] })
	}
	var mu sync.Mutex
	i := 0
	return func() []int16 {
		mu.Lock()
		defer mu.Unlock()
		for range files { // skip a track that cannot be read, but stop after one round
			f := files[i%len(files)]
			i++
			if b, err := e.Music.MusicAudio(class, f.Name); err == nil {
				if pcm, err := media.DecodeWAV(b); err == nil && len(pcm) > 0 {
					return pcm
				}
			}
		}
		return nil
	}
}

// startHold plays hold music of a class to a leg (or the tone when the class has none) and returns
// the player.
func (e *Engine) startHold(cfg *config.Config, l *Leg, class string, tone media.Tone) *media.Player {
	pt := 0
	if strings.EqualFold(l.codecName(), "PCMA") {
		pt = 8
	}
	pl := media.NewPlayer(l.ep, pt)
	if src := e.musicSource(cfg, class); src != nil {
		pl.PlayStream(src, tone)
	} else {
		pl.Play(tone)
	}
	return pl
}

// holdClassOf is the class a party's extension plays when it puts someone on hold.
func holdClassOf(cfg *config.Config, ext string) string {
	if x := cfg.FindExtension(ext); x != nil {
		return holdClass(cfg, x.HoldMusic)
	}
	return holdClass(cfg)
}
