package sms

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Each provider sends the request its API documents and reads its own received-text callback.
func TestProviders(t *testing.T) {
	cases := []struct {
		kind, account, app string
		wantPath, wantBody string // what the API must receive
		answer             string
		wantID             string
		inType, inBody     string // a received text as the provider posts it
	}{
		{kind: "twilio", wantPath: "/2010-04-01/Accounts/user/Messages.json", wantBody: "Body=hello", answer: `{"sid":"SM1"}`, wantID: "SM1",
			inType: "application/x-www-form-urlencoded", inBody: "MessageSid=SM2&From=%2B15550001&To=%2B15550002&Body=hi"},
		{kind: "signalwire", wantPath: "/api/laml/2010-04-01/Accounts/user/Messages.json", wantBody: "To=%2B15550001", answer: `{"sid":"SW1"}`, wantID: "SW1",
			inType: "application/x-www-form-urlencoded", inBody: "MessageSid=SW2&From=%2B15550001&To=%2B15550002&Body=hi"},
		{kind: "telnyx", wantPath: "/v2/messages", wantBody: `"text":"hello"`, answer: `{"data":{"id":"T1"}}`, wantID: "T1",
			inType: "application/json", inBody: `{"data":{"event_type":"message.received","payload":{"id":"T2","from":{"phone_number":"+15550001"},"to":[{"phone_number":"+15550002"}],"text":"hi","received_at":"2026-10-11T10:00:00Z"}}}`},
		{kind: "bandwidth", account: "acc", app: "app1", wantPath: "/api/v2/users/acc/messages", wantBody: `"applicationId":"app1"`, answer: `{"id":"B1"}`, wantID: "B1",
			inType: "application/json", inBody: `[{"type":"message-received","to":"+15550002","message":{"id":"B2","from":"+15550001","text":"hi","time":"2026-10-11T10:00:00Z"}}]`},
		{kind: "vonage", wantPath: "/sms/json", wantBody: "text=hello", answer: `{"messages":[{"status":"0","message-id":"V1"}]}`, wantID: "V1",
			inType: "application/x-www-form-urlencoded", inBody: "msisdn=15550001&to=15550002&text=hi&messageId=V2"},
		{kind: "plivo", wantPath: "/v1/Account/user/Message/", wantBody: `"dst":"15550001"`, answer: `{"message_uuid":["P1"]}`, wantID: "P1",
			inType: "application/x-www-form-urlencoded", inBody: "From=15550001&To=15550002&Text=hi&MessageUUID=P2"},
		{kind: "flowroute", wantPath: "/v2.1/messages", wantBody: `"body":"hello"`, answer: `{"data":{"id":"F1"}}`, wantID: "F1",
			inType: "application/vnd.api+json", inBody: `{"data":{"id":"F2","attributes":{"from":"15550001","to":"15550002","body":"hi"}}}`},
		{kind: "voipms", wantPath: "/api/v1/rest.php", wantBody: "dst=5550001", answer: `{"status":"success","sms":23}`, wantID: "23",
			inType: "application/x-www-form-urlencoded", inBody: "from=2125550001&to=2125550002&message=hi&id=9"},
		{kind: "webhook", wantPath: "/send", wantBody: `"to":"+15550001"`, answer: `{"id":"W1"}`, wantID: "W1",
			inType: "application/json", inBody: `{"id":"W2","from":"+15550001","to":"+15550002","text":"hi"}`},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			var gotPath, gotBody string
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				gotPath, gotBody = r.URL.Path, string(b)+r.URL.RawQuery
				w.Write([]byte(c.answer))
			}))
			defer api.Close()
			base := api.URL
			if c.kind == "webhook" {
				base += "/send"
			}
			to := "+15550001"
			if c.kind == "voipms" {
				to = "+12125550001" // ten digits after the 1, as VoIP.ms wants
				c.wantBody = "dst=2125550001"
			}
			p, err := NewWith(c.kind, "user", "key", base, c.account, c.app)
			if err != nil {
				t.Fatal(err)
			}
			id, err := p.Send(context.Background(), "+15550002", to, "hello")
			if err != nil || id != c.wantID {
				t.Fatalf("send: %q %v", id, err)
			}
			if gotPath != c.wantPath || !strings.Contains(gotBody, c.wantBody) {
				t.Fatalf("API got %s %q, want %s with %q", gotPath, gotBody, c.wantPath, c.wantBody)
			}
			r := httptest.NewRequest("POST", "/hook", strings.NewReader(c.inBody))
			r.Header.Set("Content-Type", c.inType)
			in, err := p.Inbound(r)
			if err != nil || in.Text != "hi" || !strings.HasPrefix(in.From, "+") || !strings.HasSuffix(in.From, "5550001") || in.To == "" {
				t.Fatalf("inbound: %+v %v", in, err)
			}
		})
	}
}

// A provider that refuses the account says so in plain words; receipts are ignored, not stored.
func TestProviderErrors(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer api.Close()
	p, _ := NewWith("twilio", "u", "k", api.URL, "", "")
	if _, err := p.Send(context.Background(), "+1", "+2", "x"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("got %v", err)
	}
	p, _ = NewWith("telnyx", "", "k", api.URL, "", "")
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"data":{"event_type":"message.finalized","payload":{}}}`))
	if _, err := p.Inbound(r); err != ErrIgnore {
		t.Fatalf("receipt: %v", err)
	}
	if _, err := NewWith("bandwidth", "u", "k", "", "", ""); err == nil {
		t.Fatal("Bandwidth without account")
	}
	if _, err := NewWith("webhook", "", "", "", "", ""); err == nil {
		t.Fatal("webhook without address")
	}
}

// Vonage takes the key and secret as HTTP Basic (its SMS specification 1.5.1), never in the body.
func TestVonageBasicAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		b, _ := io.ReadAll(r.Body)
		if !ok || u != "key1" || p != "secret1" || strings.Contains(string(b), "api_secret") {
			t.Errorf("auth %q %q %v, body %s", u, p, ok, b)
		}
		w.Write([]byte(`{"messages":[{"status":"0","message-id":"V1"}]}`))
	}))
	defer srv.Close()
	p, err := NewWith("vonage", "key1", "secret1", srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if id, err := p.Send(context.Background(), "+15550002", "+15550001", "hi"); err != nil || id != "V1" {
		t.Fatal(id, err)
	}
}
