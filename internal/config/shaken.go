package config

import (
	"crypto/rand"
	"fmt"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/screen"
)

// NewUUID returns a random (version 4) UUID.
func NewUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (c *Config) validateShaken(p *Problems) {
	for i := range c.Trunks {
		tr := &c.Trunks[i]
		s := &tr.Shaken
		f := fmt.Sprintf("trunks[%d].shaken", i)
		s.Attest = strings.ToUpper(strings.TrimSpace(s.Attest))
		switch s.Attest {
		case "", "A", "B", "C":
		default:
			p.add(f+".attest", "choose automatic, A, B or C")
		}
		if !s.Sign {
			continue
		}
		if s.OrigID == "" {
			s.OrigID = NewUUID()
		}
		x5u := s.X5U
		if x5u == "" {
			x5u = "https://localhost/stir/" + tr.ID + ".pem" // served here; only the key and certificate are checked
		}
		if _, err := screen.NewSigner(s.Key, s.Cert, x5u); err != nil {
			field := ".cert"
			if strings.Contains(err.Error(), "key") {
				field = ".key"
			}
			if strings.Contains(err.Error(), "x5u") {
				field = ".x5u"
			}
			p.add(f+field, "%s", err.Error())
		}
	}
}
