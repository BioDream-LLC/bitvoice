package agi

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// speechState is a script's speech recognizer (SPEECH CREATE ... SPEECH DESTROY), which uses
// BitVoice's own speech to text, and its hold music.
type speechState struct {
	on       bool
	grammars map[string]bool
	settings map[string]string
	music    func()
}

func (s *session) cmdSpeechCreate(argv []string) bool {
	if len(argv) != 3 {
		return false
	}
	if _, ok := s.ch.Transcribe(nil); !ok {
		s.result(0) // no speech to text is set up in Settings, AI
		return true
	}
	if s.speech == nil {
		s.speech = &speechState{}
	}
	s.speech.on, s.speech.grammars, s.speech.settings = true, map[string]bool{}, map[string]string{}
	s.result(1)
	return true
}

func (s *session) cmdSpeechSet(argv []string) bool {
	if len(argv) != 4 {
		return false
	}
	if s.speech == nil || !s.speech.on {
		s.result(0)
		return true
	}
	s.speech.settings[argv[2]] = argv[3]
	s.result(1)
	return true
}

func (s *session) cmdSpeechDestroy(argv []string) bool {
	if s.speech == nil || !s.speech.on {
		s.result(0)
		return true
	}
	s.speech.on = false
	s.result(1)
	return true
}

// cmdSpeechGrammar accepts grammars without using them: BitVoice writes down whatever was said and
// gives it back as the one result (grammar "default"); the script compares the words.
func (s *session) cmdSpeechGrammar(argv []string) bool {
	if len(argv) < 4 {
		return false
	}
	if s.speech == nil || !s.speech.on {
		s.result(0)
		return true
	}
	s.speech.grammars[argv[3]] = argv[1] != "unload" && argv[1] != "deactivate"
	s.result(1)
	return true
}

// cmdSpeechRecognize plays the prompt, then records what the caller says until a pause (or the
// timeout, or a key) and writes it down.
func (s *session) cmdSpeechRecognize(argv []string) bool {
	if len(argv) < 4 || len(argv) > 5 {
		return false
	}
	if s.speech == nil || !s.speech.on {
		s.result(0)
		return true
	}
	timeout, err := strconv.Atoi(argv[3])
	if err != nil {
		return false
	}
	offset := 0
	if len(argv) == 5 {
		offset, _ = strconv.Atoi(argv[4])
	}
	endpos := 0
	if pcm, ok := s.sounds(argv[2]); ok {
		offset = min(max(offset, 0), len(pcm))
		k, played, hungup := s.ch.Play(pcm[offset:], "0123456789*#")
		endpos = offset + played
		if hungup {
			s.send(fmt.Sprintf("200 result=1 (hangup) endpos=%d", endpos))
			return true
		}
		if k != 0 {
			s.send(fmt.Sprintf("200 result=1 (digit) digit=%c endpos=%d", k, endpos))
			return true
		}
	}
	limit := 10 * time.Second
	if timeout > 0 {
		limit = time.Duration(min(timeout, 300)) * time.Second
	}
	pause := 2 * time.Second
	if v, err := strconv.ParseFloat(s.speech.settings["silence"], 64); err == nil && v > 0 {
		pause = time.Duration(v * float64(time.Second))
	}
	pcm, k, reason := s.ch.Record(limit, pause, "0123456789*#")
	switch reason {
	case "hangup":
		s.send(fmt.Sprintf("200 result=1 (hangup) endpos=%d", endpos))
		return true
	case "dtmf":
		s.send(fmt.Sprintf("200 result=1 (digit) digit=%c endpos=%d", k, endpos))
		return true
	}
	text, _ := s.ch.Transcribe(pcm)
	text = strings.TrimSpace(strings.ReplaceAll(text, `"`, "'"))
	if text == "" {
		s.send(fmt.Sprintf("200 result=1 (timeout) endpos=%d", endpos))
		return true
	}
	s.vars["SPEECH_TEXT(0)"], s.vars["SPEECH_SCORE(0)"], s.vars["SPEECH_GRAMMAR(0)"], s.vars["SPEECH(results)"] = text, "1000", "default", "1"
	s.send(fmt.Sprintf(`200 result=1 (speech) endpos=%d results=1 score0=1000 text0="%s" grammar0=default`, endpos, text))
	return true
}
