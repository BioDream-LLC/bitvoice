package store

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SeenDevice is a kind of phone or app that has registered here, by its User-Agent: evidence that it works
// with this system, for the compatibility list.
type SeenDevice struct {
	UserAgent  string    `json:"userAgent"`
	Extensions []string  `json:"extensions"`
	First      time.Time `json:"first"`
	Last       time.Time `json:"last"`
	Count      int       `json:"count"`
}

func (s *Store) seenPath() string { return filepath.Join(s.dir, "seen-devices.json") }

// NoteDevice records a registration by a User-Agent. It writes at most once an hour per device.
func (s *Store) NoteDevice(userAgent, ext string) {
	ua := strings.TrimSpace(userAgent)
	if ua == "" || len(ua) > 200 {
		return
	}
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	var all map[string]*SeenDevice
	_ = s.readDoc(s.seenPath(), &all)
	if all == nil {
		all = map[string]*SeenDevice{}
	}
	now := time.Now().UTC()
	d := all[ua]
	if d == nil {
		if len(all) >= 500 {
			return
		}
		d = &SeenDevice{UserAgent: ua, First: now}
		all[ua] = d
	} else if now.Sub(d.Last) < time.Hour && contains(d.Extensions, ext) {
		return
	}
	d.Last = now
	d.Count++
	if !contains(d.Extensions, ext) && len(d.Extensions) < 50 {
		d.Extensions = append(d.Extensions, ext)
		sort.Strings(d.Extensions)
	}
	_ = s.writeDoc(s.seenPath(), all)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// SeenDevices lists every User-Agent seen, most recent first.
func (s *Store) SeenDevices() []SeenDevice {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	var all map[string]*SeenDevice
	_ = s.readDoc(s.seenPath(), &all)
	out := []SeenDevice{}
	for _, d := range all {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}
