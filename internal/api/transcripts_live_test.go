package api

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// With BITVOICE_LIVE_AI_KEY and two speech files (BITVOICE_LIVE_CALLER_WAV, BITVOICE_LIVE_AGENT_WAV),
// a real model transcribes a recorded call and finds its action items. Skipped otherwise.
func TestLiveCallTranscript(t *testing.T) {
	key, cw, aw := os.Getenv("BITVOICE_LIVE_AI_KEY"), os.Getenv("BITVOICE_LIVE_CALLER_WAV"), os.Getenv("BITVOICE_LIVE_AGENT_WAV")
	if key == "" || cw == "" || aw == "" {
		t.Skip("set BITVOICE_LIVE_AI_KEY, BITVOICE_LIVE_CALLER_WAV and BITVOICE_LIVE_AGENT_WAV")
	}
	read := func(p string) []int16 {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		pcm, err := media.DecodeWAV(b)
		if err != nil {
			t.Fatal(err)
		}
		return pcm
	}
	caller, agent := read(cw), read(aw)
	gap := make([]int16, 8000)
	left := append(append(append([]int16{}, gap...), caller...), make([]int16, len(agent)+16000)...)
	right := append(make([]int16, len(gap)+len(caller)+8000), agent...)
	s, _ := newServer(t)
	_, rev := s.Store.Config()
	if _, _, err := s.Store.Update("test", "ai", rev, func(c *config.Config) error {
		c.System.AI = config.AI{Enabled: true, BaseURL: "https://openrouter.ai/api/v1", Key: key, Calls: true,
			Model: "meta-llama/llama-3.3-70b-instruct", AudioModel: "google/gemini-2.5-flash"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	name, err := s.Store.AddRecording("live1", start, media.EncodeStereoWAV(left, right))
	if err != nil {
		t.Fatal(err)
	}
	cdr := store.CDR{ID: "live1", Start: start, From: "+12125550123", FromName: "Ana Diaz", To: "100", ToName: "Front desk", Recording: name}
	s.transcribeCall(context.Background(), cdr, false)
	tr, err := s.Store.CallTranscript(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("status %s %s\n%s\nsummary: %s\nactions: %q", tr.Status, tr.Error, tr.Text(), tr.Summary, tr.ActionItems)
	if tr.Status != "done" || len(tr.Lines) < 2 || tr.Lines[0].Side != "caller" || tr.Lines[len(tr.Lines)-1].Side != "callee" {
		t.Fatalf("transcript %+v", tr)
	}
	all := strings.ToLower(tr.Text())
	if !strings.Contains(all, "boiler") || !strings.Contains(all, "plumber") {
		t.Fatal("the words are not in the transcript")
	}
	if len(tr.ActionItems) == 0 || !strings.Contains(strings.ToLower(tr.Summary), "boiler") {
		t.Fatal("no summary or action items")
	}
	if got := s.Store.SearchTranscripts("plumber", 5); len(got) != 1 {
		t.Fatalf("search found %d", len(got))
	}
}

// TestLiveBriefing: a real model briefs the person a call is transferred to, from the call's audio.
func TestLiveBriefing(t *testing.T) {
	key, cw, aw := os.Getenv("BITVOICE_LIVE_AI_KEY"), os.Getenv("BITVOICE_LIVE_CALLER_WAV"), os.Getenv("BITVOICE_LIVE_AGENT_WAV")
	if key == "" || cw == "" || aw == "" {
		t.Skip("set BITVOICE_LIVE_AI_KEY, BITVOICE_LIVE_CALLER_WAV and BITVOICE_LIVE_AGENT_WAV")
	}
	read := func(p string) []int16 {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		pcm, err := media.DecodeWAV(b)
		if err != nil {
			t.Fatal(err)
		}
		return pcm
	}
	caller, agent := read(cw), read(aw)
	left := append(append(make([]int16, 8000), caller...), make([]int16, len(agent)+16000)...)
	right := append(make([]int16, 16000+len(caller)), agent...)
	s, _ := newServer(t)
	_, rev := s.Store.Config()
	if _, _, err := s.Store.Update("test", "ai", rev, func(c *config.Config) error {
		c.System.AI = config.AI{Enabled: true, BaseURL: "https://openrouter.ai/api/v1", Key: key, Briefings: true,
			Model: "meta-llama/llama-3.3-70b-instruct", AudioModel: "google/gemini-2.5-flash"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.Brief(pbx.BriefRequest{CallID: "live", Target: "101", From: "+12125550123", Caller: left, Callee: right})
	b, ok := s.briefingFor("live")
	if !ok {
		t.Fatal("no briefing")
	}
	t.Logf("briefing: %s", b.Summary)
	if len(strings.Fields(b.Summary)) > 60 {
		t.Fatalf("too long for a briefing: %q", b.Summary)
	}
}
