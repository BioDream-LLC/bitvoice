package sdp

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// Crypto is one a=crypto line (SDES, RFC 4568): a master key offered for SRTP.
type Crypto struct {
	Tag   int
	Suite string
	Key   []byte // master key followed by master salt, as the inline: parameter carries them
}

// Cryptos returns the media section's crypto lines that parse; lines with unknown forms are skipped.
func (m *Media) Cryptos() []Crypto {
	var out []Crypto
	for _, l := range m.Lines {
		v, ok := strings.CutPrefix(l.Value, "crypto:")
		if l.Type != 'a' || !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) < 3 {
			continue
		}
		tag, err := strconv.Atoi(f[0])
		if err != nil || tag < 0 || tag > 999999999 {
			continue
		}
		// key-params: inline:<base64>[|lifetime][|MKI:length]; only the first key is used.
		kp, ok := strings.CutPrefix(f[2], "inline:")
		if !ok {
			continue
		}
		kp, rest, _ := strings.Cut(kp, "|")
		if strings.Contains(rest, ":") {
			continue // an MKI: BitVoice does not send MKIs, and a phone using one cannot be understood
		}
		key, err := base64.StdEncoding.DecodeString(kp)
		if err != nil {
			if key, err = base64.RawStdEncoding.DecodeString(kp); err != nil {
				continue
			}
		}
		out = append(out, Crypto{Tag: tag, Suite: f[1], Key: key})
	}
	return out
}

// SetCrypto replaces the media section's crypto lines (none when c is nil).
func (m *Media) SetCrypto(c []Crypto) {
	kept := m.Lines[:0:0]
	for _, l := range m.Lines {
		if l.Type == 'a' && strings.HasPrefix(l.Value, "crypto:") {
			continue
		}
		kept = append(kept, l)
	}
	for _, x := range c {
		kept = append(kept, Line{Type: 'a', Value: fmt.Sprintf("crypto:%d %s inline:%s", x.Tag, x.Suite,
			base64.StdEncoding.EncodeToString(x.Key))})
	}
	m.Lines = kept
}

// Secure reports whether the media section uses SRTP (RTP/SAVP or RTP/SAVPF).
func (m *Media) Secure() bool {
	return strings.Contains(strings.ToUpper(m.Proto), "SAVP")
}
