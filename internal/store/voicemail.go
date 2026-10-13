package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Voicemail is one message left for an extension. The audio is a WAV file beside its metadata.
type Voicemail struct {
	ID       string    `json:"id"`
	Box      string    `json:"box"` // extension number
	From     string    `json:"from"`
	FromName string    `json:"fromName"`
	At       time.Time `json:"at"`
	Seconds  int       `json:"seconds"`
	Heard    bool      `json:"heard"`
	CallID   string    `json:"callId"`
	// Transcript and Summary come from the AI model when it is switched on.
	Transcript string `json:"transcript,omitempty"`
	Summary    string `json:"summary,omitempty"`
	AIError    string `json:"aiError,omitempty"`
	// Urgency (urgent, normal, low) and Category come from the AI's triage.
	Urgency  string `json:"urgency,omitempty"`
	Category string `json:"category,omitempty"`
}

// SetVoicemailText records a message's transcript and summary (or why there is none).
func (s *Store) SetVoicemailText(box, id, transcript, summary, aiErr string) (Voicemail, error) {
	var v Voicemail
	p, err := s.voicemailPath(box, id, ".json")
	if err != nil {
		return v, err
	}
	if err := readJSON(p, &v); err != nil {
		return v, err
	}
	v.Transcript, v.Summary, v.AIError = transcript, summary, aiErr
	return v, writeJSON(p, v)
}

// ErrNotFound is returned for a message or greeting that does not exist.
var ErrNotFound = errors.New("not found")

var safeName = regexp.MustCompile(`^[0-9A-Za-z*#+_-]{1,40}$`)

func (s *Store) boxDir(box string) (string, error) {
	if !safeName.MatchString(box) {
		return "", ErrNotFound
	}
	d := filepath.Join(s.dir, "voicemail", box)
	return d, os.MkdirAll(d, 0o700)
}

// AddVoicemail stores a new message.
func (s *Store) AddVoicemail(v Voicemail, wav []byte) (Voicemail, error) {
	d, err := s.boxDir(v.Box)
	if err != nil {
		return v, err
	}
	if v.ID == "" {
		v.ID = fmt.Sprintf("%s-%04x", v.At.UTC().Format("20060102T150405"), time.Now().UnixNano()&0xffff)
	}
	if !safeName.MatchString(v.ID) {
		return v, ErrNotFound
	}
	if err := s.roomFor(len(wav)); err != nil {
		return v, err
	}
	if err := writeFile(filepath.Join(d, v.ID+".wav"), wav); err != nil {
		return v, err
	}
	return v, writeJSON(filepath.Join(d, v.ID+".json"), v)
}

// Voicemails lists a box's messages, newest first. An empty box lists every box.
func (s *Store) Voicemails(box string) ([]Voicemail, error) {
	boxes := []string{box}
	if box == "" {
		entries, _ := os.ReadDir(filepath.Join(s.dir, "voicemail"))
		boxes = boxes[:0]
		for _, e := range entries {
			if e.IsDir() {
				boxes = append(boxes, e.Name())
			}
		}
	}
	out := []Voicemail{}
	for _, b := range boxes {
		if !safeName.MatchString(b) {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(s.dir, "voicemail", b))
		if err != nil {
			continue
		}
		for _, e := range entries {
			name, ok := strings.CutSuffix(e.Name(), ".json")
			if !ok {
				continue
			}
			var v Voicemail
			if readJSON(filepath.Join(s.dir, "voicemail", b, e.Name()), &v) == nil && v.ID == name {
				out = append(out, v)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

// VoicemailCounts returns new and old message counts for a box (for the message-waiting light).
func (s *Store) VoicemailCounts(box string) (fresh, old int) {
	vs, _ := s.Voicemails(box)
	for _, v := range vs {
		if v.Heard {
			old++
		} else {
			fresh++
		}
	}
	return fresh, old
}

func (s *Store) voicemailPath(box, id, ext string) (string, error) {
	if !safeName.MatchString(box) || !safeName.MatchString(id) {
		return "", ErrNotFound
	}
	p := filepath.Join(s.dir, "voicemail", box, id+ext)
	if _, err := os.Stat(p); err != nil {
		return "", ErrNotFound
	}
	return p, nil
}

// VoicemailAudio returns a message's WAV file.
func (s *Store) VoicemailAudio(box, id string) ([]byte, error) {
	p, err := s.voicemailPath(box, id, ".wav")
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// SetHeard marks a message heard or new.
func (s *Store) SetHeard(box, id string, heard bool) (Voicemail, error) {
	var v Voicemail
	p, err := s.voicemailPath(box, id, ".json")
	if err != nil {
		return v, err
	}
	if err := readJSON(p, &v); err != nil {
		return v, err
	}
	v.Heard = heard
	return v, writeJSON(p, v)
}

// DeleteVoicemail removes a message and its audio.
func (s *Store) DeleteVoicemail(box, id string) error {
	p, err := s.voicemailPath(box, id, ".json")
	if err != nil {
		return err
	}
	var v Voicemail
	if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &v) == nil && s.VoicemailHeld(box, v.At, v.From) {
		return ErrHeld
	}
	os.Remove(strings.TrimSuffix(p, ".json") + ".wav")
	return os.Remove(p)
}

// Greeting returns a box's recorded greeting, or ErrNotFound when it uses the default.
func (s *Store) Greeting(box string) ([]byte, error) {
	if !safeName.MatchString(box) {
		return nil, ErrNotFound
	}
	b, err := os.ReadFile(filepath.Join(s.dir, "voicemail", box, "greeting.wav"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return b, err
}

// SetGreeting stores a greeting; nil removes it (back to the default).
func (s *Store) SetGreeting(box string, wav []byte) error {
	d, err := s.boxDir(box)
	if err != nil {
		return err
	}
	p := filepath.Join(d, "greeting.wav")
	if wav == nil {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeFile(p, wav)
}

// Greetings lists the boxes with a recorded greeting.
func (s *Store) Greetings() []string {
	var out []string
	entries, _ := os.ReadDir(filepath.Join(s.dir, "voicemail"))
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(s.dir, "voicemail", e.Name(), "greeting.wav")); err == nil {
			out = append(out, e.Name())
		}
	}
	return slices.Clip(out)
}

// SetVoicemailTriage records the AI's urgency and category for a message.
func (s *Store) SetVoicemailTriage(box, id, urgency, category string) (Voicemail, error) {
	var v Voicemail
	p, err := s.voicemailPath(box, id, ".json")
	if err != nil {
		return v, err
	}
	if err := readJSON(p, &v); err != nil {
		return v, err
	}
	v.Urgency, v.Category = urgency, category
	return v, writeJSON(p, v)
}
