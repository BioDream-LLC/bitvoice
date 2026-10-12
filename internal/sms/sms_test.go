package sms

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParts(t *testing.T) {
	for text, want := range map[string]int{"hi": 1, strings.Repeat("a", 160): 1, strings.Repeat("a", 161): 2, strings.Repeat("é", 70): 1, strings.Repeat("é", 71): 2} {
		if got := Parts(text); got != want {
			t.Errorf("%d characters: %d parts, want %d", len([]rune(text)), got, want)
		}
	}
}

func TestTextMagicInbound(t *testing.T) {
	p, _ := New("textmagic", "u", "k", "")
	r := httptest.NewRequest("POST", "/", strings.NewReader("id=9&sender=447700900123&receiver=12125550100&text=Hello"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	in, err := p.Inbound(r)
	if err != nil || in.From != "+447700900123" || in.To != "+12125550100" || in.Text != "Hello" || in.ID != "9" {
		t.Fatalf("%+v %v", in, err)
	}
	if _, err := p.Inbound(httptest.NewRequest("POST", "/", strings.NewReader(`{"text":"x"}`))); err == nil {
		t.Fatal("a message with no sender was taken")
	}
	if _, err := New("nope", "", "", ""); err == nil {
		t.Fatal("unknown provider")
	}
}

func TestTextMagicTime(t *testing.T) {
	p, _ := New("textmagic", "u", "k", "")
	in, err := p.Inbound(httptest.NewRequest("POST", "/", strings.NewReader(`{"id":1,"sender":"1212","receiver":"1","messageTime":"2026-10-10T22:43:30+0000","text":"x"}`)))
	if err != nil || in.At.Format("15:04:05") != "22:43:30" {
		t.Fatalf("%v %v", in.At, err)
	}
}
