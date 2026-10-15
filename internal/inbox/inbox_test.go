package inbox

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func sign(body []byte, secret string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestSignatureAndChallenge(t *testing.T) {
	b := []byte(`{"object":"page"}`)
	if !VerifySignature(b, sign(b, "s3cret"), "s3cret") || VerifySignature(b, sign(b, "other"), "s3cret") || VerifySignature(b, "", "s3cret") {
		t.Fatal("signature")
	}
	q := url.Values{"hub.mode": {"subscribe"}, "hub.verify_token": {"tok"}, "hub.challenge": {"12345"}}
	if Challenge(q, "tok") != "12345" || Challenge(q, "nope") != "" {
		t.Fatal("challenge")
	}
}

// Webhook bodies as Meta documents them.
func TestParse(t *testing.T) {
	wa := []byte(`{"object":"whatsapp_business_account","entry":[{"id":"1","changes":[{"field":"messages","value":{"messaging_product":"whatsapp",
		"metadata":{"display_phone_number":"15550001111","phone_number_id":"106540352242922"},
		"contacts":[{"profile":{"name":"Ann Smith"},"wa_id":"447700900123"}],
		"messages":[{"from":"447700900123","id":"wamid.HBgM","timestamp":"1700000000","type":"text","text":{"body":"Is the shop open on Sunday?"}},
		{"from":"447700900123","id":"wamid.2","timestamp":"1700000001","type":"image","image":{"id":"x"}}]}}]}]}`)
	ms, err := ParseWhatsApp(wa)
	if err != nil || len(ms) != 2 || ms[0].From != "+447700900123" || ms[0].Name != "Ann Smith" || ms[0].Text != "Is the shop open on Sunday?" ||
		ms[0].To != "106540352242922" || ms[1].Text != "[image]" {
		t.Fatalf("%+v %v", ms, err)
	}
	fb := []byte(`{"object":"page","entry":[{"id":"123","time":1700000000000,"messaging":[
		{"sender":{"id":"PSID1"},"recipient":{"id":"123"},"timestamp":1700000000000,"message":{"mid":"m_1","text":"hello"}},
		{"sender":{"id":"123"},"recipient":{"id":"PSID1"},"timestamp":1700000000001,"message":{"mid":"m_2","text":"our reply","is_echo":true}}]}]}`)
	ms, err = ParseMessenger(fb)
	if err != nil || len(ms) != 1 || ms[0].From != "PSID1" || ms[0].Text != "hello" {
		t.Fatalf("%+v %v", ms, err)
	}
}

// Sending: the Cloud API's and the Send API's requests, as Meta expects them.
func TestSend(t *testing.T) {
	var got map[string]any
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.RequestURI(), r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		got = map[string]any{}
		json.Unmarshal(b, &got)
		if got["to"] == "bad" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"message":"Re-engagement message","code":131047}}`))
			return
		}
		w.Write([]byte(`{"messages":[{"id":"wamid.out"}],"message_id":"m_out"}`))
	}))
	defer srv.Close()
	GraphBase = srv.URL
	id, err := SendWhatsApp(context.Background(), "1065", "TOKEN", "+447700900123", "Yes, 10 to 4")
	if err != nil || id != "wamid.out" || path != "/1065/messages" || auth != "Bearer TOKEN" || got["to"] != "447700900123" || got["messaging_product"] != "whatsapp" {
		t.Fatalf("%v %s %s %s %v", err, id, path, auth, got)
	}
	if _, err := SendWhatsApp(context.Background(), "1065", "TOKEN", "bad", "x"); err == nil || err.Error() != "Re-engagement message (code 131047)" {
		t.Fatalf("error %v", err)
	}
	id, err = SendMessenger(context.Background(), "PAGETOKEN", "PSID1", "Hi")
	if err != nil || path != "/me/messages?access_token=PAGETOKEN" || got["messaging_type"] != "RESPONSE" {
		t.Fatalf("%v %s %v", err, path, got)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"entry":[{"changes":[{"value":{"messages":[{"from":"1","type":"text","text":{"body":"x"}}]}}]}]}`))
	f.Fuzz(func(t *testing.T, b []byte) { ParseWhatsApp(b); ParseMessenger(b) })
}
