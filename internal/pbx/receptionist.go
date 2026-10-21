package pbx

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/ai"
	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// The AI receptionist answers, says it is automated, and talks with the caller: it answers from the
// approved information, takes a message or transfers to a person. Pressing 0 or asking for a person
// always reaches the person destination, and so does any failure of the AI service: the receptionist
// never stands between a caller and a human.

// wantsPerson spots a caller asking for a human: "person" on its own (the greeting tells them to say it),
// or a clear request such as "can I talk to someone", "a real person", "operator". "Can someone call me
// back" is not one.
var wantsPerson = regexp.MustCompile(`(?i)^\W*(a\s+|the\s+)?(person|human|operator|agent|receptionist)(\s+please)?\W*$|` +
	`\b(operator|representative|human being|a human|real person|live person|actual person)\b|` +
	`\b(speak|talk|get through|put me through|connect me|transfer me)\b.{0,20}\b(person|someone|somebody|human|agent|receptionist|anyone|anybody|staff)\b`)

// sayYes and sayNo read a caller's answer to "shall I book that?".
var (
	sayYes = regexp.MustCompile(`(?i)\b(yes|yeah|yep|yup|correct|right|sure|please do|go ahead|book it|that's it|that is it|perfect|ok|okay|sounds good)\b`)
	sayNo  = regexp.MustCompile(`(?i)\b(no|nope|not|wrong|change|actually|wait)\b`)
)

// readBack is what the receptionist says to confirm a callback, built by BitVoice (not the model) so
// what is booked is exactly what the caller heard.
func readBack(cb *ai.CallbackRequest) string {
	var b strings.Builder
	b.WriteString("Let me check. ")
	if cb.Name != "" {
		b.WriteString("Your name is " + cb.Name + ". ")
	}
	b.WriteString("We will call you on " + spokenNumber(cb.Number) + ". ")
	if cb.When != "" {
		b.WriteString("A good time is " + cb.When + ". ")
	}
	if cb.Reason != "" {
		b.WriteString("It is about " + cb.Reason + ". ")
	}
	b.WriteString("Shall I book that? Please say yes or no.")
	return b.String()
}

// spokenNumber spaces the digits so speech reads them one by one.
func spokenNumber(n string) string {
	var b strings.Builder
	for i, r := range strings.TrimPrefix(n, "+") {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// listen captures what the caller says: it starts when they speak and ends after 0.8 s of quiet, or
// at most 15 s. A key press ends it at once. It returns the audio, the key, and whether the call ended.
func (c *Call) listen(capt *media.Capture, wait time.Duration) ([]int16, byte, bool) {
	const frame = 1600 // 200 ms
	start := time.Now()
	spoke := false
	quietSince := time.Time{}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-c.cancelled:
			return nil, 0, true
		case k := <-capt.Keys:
			return nil, k, false
		case <-tick.C:
		}
		s := capt.Samples()
		level := 0.0
		if len(s) >= frame {
			var sum float64
			for _, v := range s[len(s)-frame:] {
				sum += float64(v) * float64(v)
			}
			level = math.Sqrt(sum / frame)
		}
		loud := level > 700
		switch {
		case loud:
			spoke, quietSince = true, time.Time{}
		case spoke && quietSince.IsZero():
			quietSince = time.Now()
		}
		if spoke && !quietSince.IsZero() && time.Since(quietSince) > 800*time.Millisecond {
			return s, 0, false
		}
		if time.Since(start) > 15*time.Second || (!spoke && time.Since(start) > wait) {
			if spoke {
				return s, 0, false
			}
			return nil, 0, false
		}
	}
}

func (c *Call) runReceptionist(id string) {
	cfg := c.e.Config()
	r := cfg.FindReceptionist(id)
	if r == nil {
		c.hangup("receptionist removed", "system")
		return
	}
	a := cfg.System.AI
	client := c.e.Speech.Client(a, c.e.AIAllow)
	var history []string
	toPerson := func(why string) {
		c.e.Log.Info("receptionist hands the call to a person", "call", c.ID, "why", why)
		c.caller.ep.Record(nil)
		if len(history) > 0 && r.Person.Type == "extension" {
			// What the caller told the assistant, so the person answering need not ask again.
			var b strings.Builder
			for i, h := range history {
				who := "Caller"
				if i%2 == 1 {
					who = "Assistant"
				}
				fmt.Fprintf(&b, "%s: %s\n", who, h)
			}
			c.e.brief(BriefRequest{CallID: c.ID, Target: r.Person.Target, From: c.from, FromName: c.fromName, By: r.Name, Text: b.String()})
		}
		c.goTo(cfg, r.Person)
	}
	if !a.Enabled || a.SpeechModel == "" {
		toPerson("AI or speech is switched off")
		return
	}
	capt := media.NewCapture(20)
	c.caller.ep.Record(capt.Packet)
	speak := func(text string) (byte, bool, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		wav, err := client.Speak(ctx, a.SpeechModel, a.Voice, text)
		if err != nil {
			c.e.Log.Warn("receptionist could not speak", "err", err)
			return 0, false, false
		}
		pcm, err := media.DecodeWAV(wav)
		if err != nil {
			return 0, false, false
		}
		k, ended := c.play(pcm, capt.Keys)
		return k, ended, true
	}
	greet := r.Greeting
	if greet == "" {
		greet = "Hello, you have reached " + cfg.System.Name + ". I am an automated assistant. Say person, or press 0, at any time to reach someone. How can I help?"
	}
	say := greet
	silences := 0
	for turn := 0; turn < 8; turn++ {
		k, ended, ok := speak(say)
		if ended {
			return
		}
		if !ok {
			toPerson("speech failed")
			return
		}
		if k == '0' {
			toPerson("pressed 0")
			return
		}
		if len(history) > 0 {
			history = append(history, say)
		}
		capt = c.freshCapture(capt)
		pcm, key, ended := c.listen(capt, 7*time.Second)
		switch {
		case ended:
			return
		case key == '0':
			toPerson("pressed 0")
			return
		case pcm == nil:
			silences++
			if silences >= 2 {
				toPerson("the caller said nothing")
				return
			}
			say = "Sorry, I did not hear anything. How can I help?"
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		text, err := client.Transcribe(ctx, media.EncodeWAV(pcm))
		cancel()
		if err != nil {
			toPerson("transcription failed")
			return
		}
		if wantsPerson.MatchString(text) {
			speak("Of course, connecting you now.")
			toPerson("asked for a person")
			return
		}
		history = append(history, text)
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		next, err := client.Reception(ctx, cfg.System.Name, r.Knowledge, c.from, history)
		cancel()
		if err != nil {
			c.e.Log.Warn("receptionist model failed", "call", c.ID, "err", err)
			toPerson("the model failed")
			return
		}
		// What was said stays out of the log; only what the receptionist decided.
		c.e.Log.Info("receptionist turn", "call", c.ID, "turn", turn, "action", next.Action)
		switch next.Action {
		case "transfer":
			speak(firstNonEmpty(next.Say, "Connecting you now."))
			toPerson("the assistant transferred")
			return
		case "message":
			if r.MessageBox == "" {
				speak("I will put you through to someone instead.")
				toPerson("no mailbox for messages")
				return
			}
			speak(firstNonEmpty(next.Say, "Please leave your message after the tone."))
			c.caller.ep.Record(nil)
			c.leaveMessage(r.MessageBox)
			return
		case "callback":
			if c.e.Callback == nil {
				speak("I cannot book callbacks, so I will put you through to someone.")
				toPerson("callbacks are not stored")
				return
			}
			cb := next.Callback
			question := readBack(cb)
			_, ended, ok := speak(question)
			if ended {
				return
			}
			if !ok {
				toPerson("speech failed")
				return
			}
			capt = c.freshCapture(capt)
			pcm, key, ended := c.listen(capt, 7*time.Second)
			if ended {
				return
			}
			if key == '0' {
				toPerson("pressed 0")
				return
			}
			answer := ""
			if pcm != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				answer, err = client.Transcribe(ctx, media.EncodeWAV(pcm))
				cancel()
				if err != nil {
					toPerson("transcription failed")
					return
				}
			}
			if !sayYes.MatchString(answer) || sayNo.MatchString(answer) {
				// Not a clear yes: carry on talking, so the caller can correct a detail.
				history = append(history, question, firstNonEmpty(answer, "(no answer)"))
				say = "Sorry, what should I change?"
				if answer == "" {
					say = "I did not hear you. Shall I book the callback? Please say yes or no."
				}
				continue
			}
			if err := c.e.Callback(store.Callback{Receptionist: r.Name, CallID: c.ID, Name: cb.Name, Number: cb.Number, When: cb.When, Reason: cb.Reason}); err != nil {
				c.e.Log.Warn("receptionist could not book a callback", "err", err)
				speak("Sorry, I could not book that. I will put you through to someone.")
				toPerson("the callback could not be saved")
				return
			}
			speak("Thank you. It is booked, and someone will call you back. Goodbye.")
			c.hangup("the receptionist booked a callback", "system")
			return
		case "goodbye":
			speak(firstNonEmpty(next.Say, "Thank you for calling. Goodbye."))
			c.hangup("the receptionist said goodbye", "system")
			return
		}
		say = firstNonEmpty(next.Say, "Is there anything else?")
	}
	toPerson("a long conversation")
}

// freshCapture starts a new capture for the next turn, so the last answer is not heard again.
func (c *Call) freshCapture(old *media.Capture) *media.Capture {
	old.Stop()
	n := media.NewCapture(20)
	c.caller.ep.Record(n.Packet)
	return n
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
