package sdp

import (
	"strings"
	"testing"
)

const offer = "v=0\r\n" +
	"o=- 3333 3333 IN IP4 192.168.1.20\r\n" +
	"s=Phone\r\n" +
	"c=IN IP4 192.168.1.20\r\n" +
	"t=0 0\r\n" +
	"m=audio 16384 RTP/AVP 0 8 101\r\n" +
	"c=IN IP4 192.168.1.20\r\n" +
	"a=rtpmap:101 telephone-event/8000\r\n" +
	"a=fmtp:101 0-15\r\n" +
	"a=sendonly\r\n" +
	"m=video 0 RTP/AVP 96\r\n" +
	"a=rtpmap:96 H264/90000\r\n"

func TestParseAndRewrite(t *testing.T) {
	s, err := Parse([]byte(offer))
	if err != nil {
		t.Fatal(err)
	}
	a := s.Audio()
	if a == nil || a.Port != 16384 || len(a.Formats) != 3 {
		t.Fatalf("audio: %+v", a)
	}
	if got := s.Connection(a); got != "192.168.1.20" {
		t.Fatalf("connection %q", got)
	}
	if s.Direction(a) != "sendonly" {
		t.Fatal("direction")
	}
	codecs := a.Codecs()
	if len(codecs) != 3 || codecs[0].Name != "PCMU" || codecs[1].Name != "PCMA" || codecs[2].Name != "telephone-event" {
		t.Fatalf("codecs %+v", codecs)
	}

	c := s.Clone()
	c.SetConnection("203.0.113.5")
	c.Audio().Port = 20000
	c.BumpVersion()
	out := string(c.Bytes())
	for _, want := range []string{"c=IN IP4 203.0.113.5\r\n", "m=audio 20000 RTP/AVP 0 8 101\r\n",
		"o=- 3333 3334 IN IP4 203.0.113.5\r\n", "m=video 0 RTP/AVP 96\r\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Count(out, "c=") != 1 {
		t.Errorf("media-level c= should be gone:\n%s", out)
	}
	if s.Audio().Port != 16384 {
		t.Error("Clone shared state with the original")
	}
}

func TestNew(t *testing.T) {
	s := New("10.0.0.1", 30000, 7, []Codec{{0, "PCMU", 8000, 1}}, 101)
	again, err := Parse(s.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if again.Audio().Port != 30000 || len(again.Audio().Codecs()) != 2 {
		t.Fatalf("%s", s.Bytes())
	}
}

func TestRejects(t *testing.T) {
	for _, bad := range []string{"", "o=x\r\n", "v=0\r\nm=audio x RTP/AVP 0\r\n", "v=0\r\nnonsense\r\n"} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(offer))
	f.Fuzz(func(t *testing.T, b []byte) {
		s, err := Parse(b)
		if err != nil {
			return
		}
		if _, err := Parse(s.Bytes()); err != nil {
			t.Fatalf("re-parse failed: %v", err)
		}
		for _, m := range s.Media {
			_ = m.Codecs()
			_ = s.Connection(m)
		}
		s.SetConnection("192.0.2.1")
		if _, err := Parse(s.Bytes()); err != nil {
			t.Fatalf("re-parse after rewrite failed: %v", err)
		}
	})
}

func TestCrypto(t *testing.T) {
	body := "v=0\r\no=- 1 1 IN IP4 10.0.0.1\r\ns=-\r\nc=IN IP4 10.0.0.1\r\nt=0 0\r\nm=audio 4000 RTP/SAVP 0\r\n" +
		"a=crypto:1 AES_CM_128_HMAC_SHA1_80 inline:WVNfX19zZW1jdGwgKCkgewkyMjA7fQp9CnVubGVz|2^20\r\n" +
		"a=crypto:2 AES_CM_128_HMAC_SHA1_32 inline:NzB4d1BINUAvLEw6UzF3WSJ+PSdFcGdUJShpX1Zj|2^20|1:4\r\n"
	s, err := Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	a := s.Audio()
	cs := a.Cryptos()
	if !a.Secure() || len(cs) != 1 || cs[0].Tag != 1 || len(cs[0].Key) != 30 {
		t.Fatalf("%+v", cs)
	}
	a.SetCrypto([]Crypto{{Tag: 3, Suite: "AES_CM_128_HMAC_SHA1_80", Key: cs[0].Key}})
	again, _ := Parse(s.Bytes())
	if got := again.Audio().Cryptos(); len(got) != 1 || got[0].Tag != 3 || string(got[0].Key) != string(cs[0].Key) {
		t.Fatalf("%+v", got)
	}
}
