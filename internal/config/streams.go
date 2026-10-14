package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// AudioStream sends calls to a voice bot or speech service over AudioSocket (Asterisk's protocol): BitVoice
// answers, connects to Address, and streams the caller's audio and key presses both ways. A bot that
// cannot be reached, or breaks off without hanging up, hands the caller to Fallback: never dropped.
type AudioStream struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Number  string `json:"number"`  // optional: dial it directly
	Address string `json:"address"` // host:port of the bot
	// Fallback is where the caller goes when the bot fails; required.
	Fallback Destination `json:"fallback"`
}

// FindAudioStream returns a stream by ID.
func (c *Config) FindAudioStream(id string) *AudioStream {
	for i := range c.AudioStreams {
		if c.AudioStreams[i].ID == id {
			return &c.AudioStreams[i]
		}
	}
	return nil
}

// FindAudioStreamNumber returns a stream by the number dialled.
func (c *Config) FindAudioStreamNumber(n string) *AudioStream {
	for i := range c.AudioStreams {
		if n != "" && c.AudioStreams[i].Number == n {
			return &c.AudioStreams[i]
		}
	}
	return nil
}

func (c *Config) validateStreams(p *Problems, claim func(field, number, owner string), checkID func(field string, id *string), checkDest func(field string, d *Destination)) {
	for i := range c.AudioStreams {
		s := &c.AudioStreams[i]
		f := fmt.Sprintf("audioStreams[%d]", i)
		checkID(f+".id", &s.ID)
		s.Name = strings.TrimSpace(s.Name)
		if s.Name == "" {
			p.add(f+".name", "enter a name")
		}
		if s.Number != "" {
			if !numberRe.MatchString(s.Number) {
				p.add(f+".number", "numbers are 2 to 8 digits")
			} else {
				claim(f+".number", s.Number, "audio stream "+s.Name)
			}
		}
		s.Address = strings.TrimSpace(s.Address)
		host, port, err := net.SplitHostPort(s.Address)
		if n, perr := strconv.Atoi(port); err != nil || host == "" || perr != nil || n < 1 || n > 65535 {
			p.add(f+".address", "enter the bot's address and port, such as 10.0.0.9:9092 or bot.example.com:9092")
		}
		if s.Fallback.Type == "" {
			p.add(f+".fallback", "choose where callers go if the bot cannot take the call")
		} else if s.Fallback.Type == "audiosocket" {
			p.add(f+".fallback", "the fallback cannot be another audio stream")
		} else {
			checkDest(f+".fallback", &s.Fallback)
		}
	}
}
