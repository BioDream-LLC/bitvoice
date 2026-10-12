package api

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/ai"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// RunAI transcribes and summarises new voicemail while the AI is switched on. It runs until ctx ends.
func (s *Server) RunAI(ctx context.Context) {
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.C:
			switch d := ev.Data.(type) {
			case store.Voicemail:
				if ev.Type == "voicemail.new" {
					go s.transcribe(ctx, d)
				}
			case store.CDR:
				if ev.Type == "call.recorded" {
					go s.transcribeCall(ctx, d, false)
				}
			}
		}
	}
}

func (s *Server) transcribe(ctx context.Context, v store.Voicemail) {
	cfg := s.config() // vault references resolved
	a := cfg.System.AI
	if !a.Enabled || !a.Voicemail {
		return
	}
	wav, err := s.Store.VoicemailAudio(v.Box, v.ID)
	if err != nil {
		return
	}
	c := &ai.Client{BaseURL: a.BaseURL, Key: a.Key, Model: a.Model, AudioModel: a.AudioModel}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	text, err := c.Transcribe(ctx, wav)
	summary := ""
	if err == nil {
		summary, err = c.Summarize(ctx, text)
	}
	msg := ""
	if err != nil {
		msg = err.Error()
		s.Log.Warn("voicemail transcript failed", "box", v.Box, "err", err)
	}
	var tri ai.Triage
	if err == nil && text != "" {
		if tri, err = c.TriageVoicemail(ctx, text); err != nil {
			s.Log.Warn("voicemail triage failed", "box", v.Box, "err", err)
			err = nil // the transcript and summary still stand
		}
	}
	if nv, err := s.Store.SetVoicemailText(v.Box, v.ID, text, summary, msg); err == nil {
		if tri.Urgency != "" {
			nv, _ = s.Store.SetVoicemailTriage(v.Box, v.ID, tri.Urgency, tri.Category)
		}
		s.Bus.Publish("voicemail.changed", map[string]any{"box": nv.Box})
		s.Bus.Publish("voicemail.transcribed", nv)
		if tri.Urgency == "urgent" {
			s.Bus.Publish("voicemail.urgent", map[string]any{"box": nv.Box, "from": nv.From, "fromName": nv.FromName, "summary": nv.Summary, "reason": tri.Reason})
		}
	}
}

// callWork limits how many recorded calls are transcribed at once, so a busy hour cannot flood the AI
// service; the rest wait their turn.
var callWork = make(chan struct{}, 2)

// transcribeCall turns a recorded call into a transcript (each side transcribed stretch by stretch, so
// who said what is known from the recording's channels, not guessed), then a summary and action items.
// force runs it even when automatic transcripts are off (an administrator asked for this one).
func (s *Server) transcribeCall(ctx context.Context, r store.CDR, force bool) {
	cfg := s.config()
	a := cfg.System.AI
	if !a.Enabled || (!a.Calls && !force) || r.Recording == "" {
		return
	}
	select {
	case callWork <- struct{}{}:
		defer func() { <-callWork }()
	case <-ctx.Done():
		return
	}
	t := store.CallTranscript{Recording: r.Recording, CallID: r.ID, At: r.Start, From: r.From, FromName: r.FromName,
		To: r.To, ToName: firstNonEmpty(r.ToName, r.AnsweredBy), Status: "pending"}
	if err := s.Store.SetCallTranscript(t); err != nil {
		return
	}
	s.Bus.Publish("transcript.changed", map[string]string{"recording": r.Recording})
	fail := func(err error) {
		s.Log.Warn("call transcript failed", "call", r.ID, "err", err)
		t.Status, t.Error, t.Done = "failed", err.Error(), time.Now().UTC()
		_ = s.Store.SetCallTranscript(t)
		s.Bus.Publish("transcript.changed", map[string]string{"recording": r.Recording})
	}
	wav, err := s.Store.Recording(r.Recording)
	if err != nil {
		fail(err)
		return
	}
	left, right, rate, err := media.SplitStereoWAV(wav)
	if err != nil {
		fail(err)
		return
	}
	talk := media.Measure(left, right, rate)
	t.Talk, t.AnsweredExt = &talk, r.AnsweredExt
	c := &ai.Client{BaseURL: a.BaseURL, Key: a.Key, Model: a.Model, AudioModel: a.AudioModel}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	var failed, pieces int
	var firstErr error
	t.Lines, failed, pieces, firstErr = transcribePCM(ctx, c, left, right, rate, 150) // about an hour of conversation
	if failed > 0 && failed == pieces {
		fail(firstErr)
		return
	}
	notes, err := c.SummarizeCall(ctx, t.Text())
	if err != nil {
		s.Log.Warn("call summary failed", "call", r.ID, "err", err) // the transcript still stands
		t.Error = "the summary could not be made: " + err.Error()
	}
	if failed > 0 {
		t.Error = strings.TrimSpace(t.Error + fmt.Sprintf(" %d of %d stretches of speech could not be transcribed.", failed, pieces))
	}
	t.Summary, t.ActionItems, t.Status, t.Done = notes.Summary, notes.ActionItems, "done", time.Now().UTC()
	if a.Insights {
		if in, err := c.CallInsight(ctx, t.TimedText()); err == nil {
			t.Insight = &in
		} else {
			s.Log.Warn("call insight failed", "call", r.ID, "err", err)
		}
	}
	if err := s.Store.SetCallTranscript(t); err != nil {
		s.Log.Warn("call transcript could not be saved", "call", r.ID, "err", err)
		return
	}
	s.Bus.Publish("transcript.changed", map[string]string{"recording": r.Recording})
	s.Bus.Publish("transcript.done", t) // the CRM webhook adds the summary to the call it logged
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// transcribePCM writes out who said what: each stretch of speech on the two sides, in order, four
// at a time, at most most stretches. It returns the lines and how many stretches failed of how many.
func transcribePCM(ctx context.Context, c *ai.Client, left, right []int16, rate, most int) (out []store.TranscriptLine, failed, total int, firstErr error) {
	type piece struct {
		side string
		seg  media.Segment
		pcm  []int16
	}
	var pieces []piece
	for _, side := range []struct {
		name string
		pcm  []int16
	}{{"caller", left}, {"callee", right}} {
		for _, sg := range media.SpeechSegments(side.pcm, rate) {
			pieces = append(pieces, piece{side.name, sg, side.pcm[sg.Start:sg.End]})
		}
	}
	slices.SortFunc(pieces, func(x, y piece) int { return x.seg.Start - y.seg.Start })
	if len(pieces) > most {
		pieces = pieces[:most]
	}
	lines := make([]store.TranscriptLine, len(pieces))
	errs := make([]error, len(pieces))
	var wg sync.WaitGroup
	gate := make(chan struct{}, 4)
	for i, pc := range pieces {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate <- struct{}{}
			defer func() { <-gate }()
			text, err := c.Transcribe(ctx, media.EncodeWAV(pc.pcm))
			lines[i], errs[i] = store.TranscriptLine{Second: float64(pc.seg.Start) / float64(rate), Side: pc.side, Text: strings.TrimSpace(text)}, err
		}()
	}
	wg.Wait()
	for i, l := range lines {
		if errs[i] != nil {
			failed++
			if firstErr == nil {
				firstErr = errs[i]
			}
			continue
		}
		if l.Text != "" {
			out = append(out, l)
		}
	}
	return out, failed, len(pieces), firstErr
}
