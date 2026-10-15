package pbx

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/biodream-llc/bitvoice/internal/media"
	"github.com/biodream-llc/bitvoice/internal/store"
	"github.com/biodream-llc/bitvoice/internal/tty"
)

// A 911 call can carry text as well as voice: a TTY on the voice line (deaf and hard-of-hearing
// callers' text telephones), real-time text beside the voice (RFC 4103), or a text-to-911 conversation
// from a text control centre (MSRP or SIP MESSAGE). All of them are one conversation on the call, shown
// to the call-taker beside the location and answered from the same box.

// textQuiet is how long a pause ends the caller's line: TTY and real-time text arrive a character at a
// time with no end-of-message mark (a TTY user types GA, "go ahead", when done).
const textQuiet = 4 * time.Second

// startText listens for a TTY on a 911 call's voice, from the moment it arrives. Called once the
// caller's media has started.
func (c *Call) startText() {
	c.mu.Lock()
	emergency := c.i3 != nil || c.esinet
	c.mu.Unlock()
	if !emergency || c.e.Config().System.PSAP.TTYOff || c.caller == nil {
		return
	}
	d := tty.NewDecoder()
	var dec media.Decoders
	stop := c.caller.ep.Listen(func(pkt []byte) {
		if len(pkt) < 12 || pkt[0]&0x30 != 0 || pkt[0]&0x0f != 0 {
			return
		}
		pt := int(pkt[1] & 0x7f)
		if !media.Transcodable(pt) {
			return
		}
		pcm := dec.Narrowband(pt, pkt[12:])
		if s := d.Feed(pcm); s != "" {
			c.textHeard("tty", s)
		}
	})
	go func() {
		<-c.cancelled
		stop()
	}()
}

// textHeard adds characters from the caller to the conversation: to the line being typed, or a new one
// after a pause or a new line. A backspace takes the last character back.
func (c *Call) textHeard(via, s string) {
	now := time.Now()
	c.mu.Lock()
	first := !c.textOn[via]
	if c.textOn == nil {
		c.textOn = map[string]bool{}
	}
	c.textOn[via] = true
	for _, r := range s {
		n := len(c.text)
		open := n > 0 && c.text[n-1].Open && c.text[n-1].From == "caller" && c.text[n-1].Via == via
		if open && now.Sub(c.textAt) > textQuiet {
			c.text[n-1].Open, open = false, false
		}
		switch r {
		case '\n', '\r':
			if open {
				c.text[n-1].Open = false
			}
			continue
		case '\b':
			if open {
				t := c.text[n-1].Text
				_, size := utf8.DecodeLastRuneInString(t)
				c.text[n-1].Text = t[:len(t)-size]
			}
			continue
		}
		if !open {
			if r == ' ' {
				continue
			}
			c.text = append(c.text, TextLine{At: now, From: "caller", Via: via, Open: true})
			n++
		}
		if len(c.text[n-1].Text) < maxTextLine {
			c.text[n-1].Text += string(r)
		}
	}
	c.textAt = now
	c.mu.Unlock()
	if first {
		c.e.Log.Info("911 call carries text", "call", c.ID, "via", via)
	}
	c.e.publish("call.updated", c.info())
}

// maxTextLine bounds one line of a conversation.
const maxTextLine = 4000

// TextLine is one turn of a 911 call's text conversation.
type TextLine = store.TextLine

// SendCallText sends the call-taker's text to a 911 caller, the way the caller's text came (or by TTY when
// via is "tty"), and adds it to the conversation.
func (e *Engine) SendCallText(id, via, text, ext, name string) (TextLine, error) {
	e.mu.Lock()
	c := e.calls[id]
	e.mu.Unlock()
	if c == nil {
		return TextLine{}, errors.New("no such call")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return TextLine{}, errors.New("there is no text to send")
	}
	if len(text) > maxTextLine {
		return TextLine{}, errors.New("the text is too long")
	}
	c.mu.Lock()
	if via == "" {
		via = c.textVia()
	}
	ended := c.ended
	c.mu.Unlock()
	if ended {
		return TextLine{}, errors.New("the call has ended")
	}
	var err error
	switch via {
	case "tty":
		text = tty.Sendable(text)
		if strings.TrimSpace(text) == "" {
			return TextLine{}, errors.New("a TTY has capital letters, digits and some punctuation only: nothing here can be sent")
		}
		err = c.sendTTY(text)
	default:
		if f := c.textSender(via); f != nil {
			err = f(text)
		} else {
			err = errors.New("this call has no text connection of that kind")
		}
	}
	if err != nil {
		return TextLine{}, err
	}
	l := TextLine{At: time.Now(), From: "taker", Name: firstNonEmptyStr(name, ext), Via: via, Text: text}
	c.mu.Lock()
	if n := len(c.text); n > 0 && c.text[n-1].Open {
		c.text[n-1].Open = false // the caller's line ends when the call-taker answers it
	}
	c.text = append(c.text, l)
	c.mu.Unlock()
	e.publish("call.updated", c.info())
	return l, nil
}

// textVia is how text reaches this caller: the session's own text channel when there is one, else TTY.
// Called with c.mu held.
func (c *Call) textVia() string {
	for _, v := range []string{"msrp", "sip", "rtt"} {
		if c.textOn[v] {
			return v
		}
	}
	return "tty"
}

// textSender returns how to send text by via on this call, nil when the call has no such channel.
func (c *Call) textSender(via string) func(string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.senders[via]
}

// sendTTY plays text as TTY tones to the caller. The call-taker's side is muted meanwhile (TTYs are
// half duplex), and the call-taker hears the tones go.
func (c *Call) sendTTY(text string) error {
	c.mu.Lock()
	a, l := c.caller, c.callee
	c.mu.Unlock()
	if a == nil || l == nil {
		return errors.New("answer the call before sending text")
	}
	c.ttySend.Lock() // one message at a time
	defer c.ttySend.Unlock()
	pcm := tty.Encode(text)
	pt := 0
	if strings.EqualFold(a.codecName(), "PCMA") {
		pt = 8
	}
	connect(a, nil)
	connect(l, nil)
	p := media.NewPlayer(a.ep, pt)
	p.PlayPCM(pcm)
	select {
	case <-p.Done():
	case <-c.cancelled:
	}
	p.Stop()
	c.mu.Lock()
	ok := !c.ended && c.callee == l
	c.mu.Unlock()
	if ok {
		connect(a, l)
	}
	return nil
}
