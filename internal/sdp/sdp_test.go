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
	s := New("10.0.0.1", 30000, 7, []Codec{{PT: 0, Name: "PCMU", ClockRate: 8000, Channels: 1}}, 101)
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

func TestFmtpAndAMR(t *testing.T) {
	raw := "v=0\r\no=- 1 1 IN IP4 10.0.0.2\r\ns=-\r\nc=IN IP4 10.0.0.2\r\nt=0 0\r\nm=audio 4000 RTP/AVP 97 98 101\r\n" +
		"a=rtpmap:97 AMR-WB/16000\r\na=fmtp:97 octet-align=1; mode-set=0,1,2\r\na=rtpmap:98 AMR/8000\r\na=rtpmap:101 telephone-event/8000\r\n"
	s, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	cs := s.Audio().Codecs()
	if cs[0].Name != "AMR-WB" || cs[0].ClockRate != 16000 || cs[0].Fmtp != "octet-align=1; mode-set=0,1,2" || cs[1].Fmtp != "" {
		t.Fatalf("%+v", cs)
	}
	if l := (Codec{PT: 98, Name: "AMR"}).Attributes(); len(l) != 2 || l[1].Value != "fmtp:98 octet-align=1" {
		t.Fatalf("our AMR offer %+v", l)
	}
	if l := cs[0].Attributes(); l[0].Value != "rtpmap:97 AMR-WB/16000" || l[1].Value != "fmtp:97 octet-align=1; mode-set=0,1,2" {
		t.Fatalf("an answer keeps the offered fmtp %+v", l)
	}
}

func TestILBCOffer(t *testing.T) {
	if l := (Codec{PT: 97, Name: "ILBC"}).Attributes(); l[0].Value != "rtpmap:97 iLBC/8000" || l[1].Value != "fmtp:97 mode=20" {
		t.Fatalf("%+v", l)
	}
	if l := (Codec{PT: 97, Name: "iLBC", Fmtp: "mode=30"}).Attributes(); l[1].Value != "fmtp:97 mode=30" {
		t.Fatalf("answer keeps the mode %+v", l)
	}
}

func TestSpeex(t *testing.T) {
	if l := (Codec{PT: 98, Name: "SPEEX"}).Attributes(); len(l) != 1 || l[0].Value != "rtpmap:98 speex/8000" {
		t.Fatalf("%+v", l)
	}
	if l := (Codec{PT: 98, Name: "speex", Fmtp: "mode=3"}).Attributes(); len(l) != 2 || l[1].Value != "fmtp:98 mode=3" {
		t.Fatalf("answer keeps the mode %+v", l)
	}
	m := &Media{Type: "audio", Formats: []string{"98", "99"}, Lines: []Line{{'a', "rtpmap:98 speex/16000"}, {'a', "rtpmap:99 speex/8000"}}}
	if cs := m.Codecs(); cs[0].Name != "speex16" || cs[1].Name != "speex" {
		t.Fatalf("wideband Speex is not narrowband: %+v", cs)
	}
}
