package pbx

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/ai"
	"github.com/biodream-llc/bitvoice/internal/media"
)

// Live transcripts (AI, Write out 911 calls live): each side of an answered 911 call is listened to as it
// arrives, cut where the speaker pauses, and each piece written out by the AI service and published as
// a call.transcript event, for the 911 screen and CAD. The recording's own transcript still comes after.

// TranscriptLine is one piece of a live transcript.
type TranscriptLine struct {
	Speaker string  `json:"speaker"` // "Caller", or the call-taker's name
	Side    string  `json:"side"`    // caller or taker
	Text    string  `json:"text"`
	Second  float64 `json:"second"` // from the answer
}

// liveTick is how often the sides are looked at; livePause is the pause that ends a piece, and
// liveLongest the longest piece before it is cut anyway.
var (
	liveTick    = 300 * time.Millisecond
	livePause   = 1500 * time.Millisecond
	liveLongest = 20 * time.Second
)

type liveSide struct {
	side, name string
	leg        *Leg
	capt       *media.Capture
	remove     func()
	buf        []int16
	start      int // samples since the answer where buf starts
}

func (s *liveSide) attach(l *Leg, name string) {
	if s.remove != nil {
		s.remove()
	}
	s.leg, s.name, s.capt = l, name, media.NewCapture(60)
	s.remove = l.ep.Listen(s.capt.Packet)
}

// liveTranscript runs for an answered 911 call when live transcripts are on, until the call ends.
func (c *Call) liveTranscript() {
	cfg := c.e.Config()
	a := cfg.System.AI
	if !a.Enabled || !a.LiveTranscripts {
		return
	}
	client := &ai.Client{BaseURL: a.BaseURL, Key: a.Key, Model: a.Model, AudioModel: a.AudioModel, Allow: c.e.AIAllow}
	rate := media.Rate
	caller := &liveSide{side: "caller", name: "Caller"}
	taker := &liveSide{side: "taker"}
	defer func() {
		for _, s := range []*liveSide{caller, taker} {
			if s.remove != nil {
				s.remove()
			}
		}
	}()
	var wg sync.WaitGroup
	gate := make(chan struct{}, 2) // pieces written out at once, for this call
	var warned sync.Once
	write := func(s *liveSide, pcm []int16, at int) {
		speaker, side := s.name, s.side
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate <- struct{}{}
			defer func() { <-gate }()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			text, err := client.Transcribe(ctx, media.EncodeWAV(pcm))
			if err != nil {
				warned.Do(func() { c.e.Log.Warn("live transcript: a piece was not written out", "call", c.ID, "err", err) })
				return
			}
			if text = strings.TrimSpace(text); text == "" {
				return
			}
			c.e.publish("call.transcript", map[string]any{"call": c.ID,
				"lines": []TranscriptLine{{Speaker: speaker, Side: side, Text: text, Second: float64(at) / float64(rate)}}})
		}()
	}
	// cut writes out what s has heard that is finished: speech followed by a pause, or a long piece.
	cut := func(s *liveSide, all bool) {
		segs := media.SpeechSegments(s.buf, rate)
		n := 0
		switch {
		case len(segs) == 0:
			if keep := rate; len(s.buf) > 5*rate { // nothing said: keep a second, in case speech starts
				n = len(s.buf) - keep
			}
		case all || len(s.buf)-segs[len(segs)-1].End >= int(livePause.Seconds()*float64(rate)):
			n = segs[len(segs)-1].End
			write(s, append([]int16(nil), s.buf[segs[0].Start:n]...), s.start+segs[0].Start)
		case len(s.buf)-segs[0].Start >= int(liveLongest.Seconds()*float64(rate)):
			n = len(s.buf)
			write(s, append([]int16(nil), s.buf[segs[0].Start:]...), s.start+segs[0].Start)
		}
		if all {
			n = len(s.buf)
		}
		s.buf, s.start = s.buf[n:], s.start+n
	}
	t := time.NewTicker(liveTick)
	defer t.Stop()
	for {
		c.mu.Lock()
		ended := c.ended
		cl, ce := c.caller, c.callee
		takerName := firstNonEmptyStr(c.takenName, "Call-taker")
		c.mu.Unlock()
		if !ended {
			if cl != nil && caller.leg != cl {
				caller.attach(cl, "Caller")
			}
			if ce != nil && taker.leg != ce { // a transfer: whoever has the call now
				name := takerName
				if taker.leg != nil {
					name = firstNonEmptyStr(ce.Name, ce.Number, "Call-taker")
				}
				taker.attach(ce, name)
			}
		}
		for _, s := range []*liveSide{caller, taker} {
			if s.capt != nil {
				s.buf = append(s.buf, s.capt.Drain()...)
				cut(s, ended)
			}
		}
		if ended {
			wg.Wait()
			return
		}
		<-t.C
	}
}
