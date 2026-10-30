package push

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func pemPKCS8(t *testing.T, k any) string {
	b, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: b}))
}

func part(s string, i int) []byte {
	b, _ := base64.RawURLEncoding.DecodeString(strings.Split(s, ".")[i])
	return b
}

func TestAPNs(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	var got struct {
		path, topic, kind string
		body              map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("authorization"), "bearer ")
		sig := part(tok, 2)
		sum := sha256.Sum256([]byte(strings.Join(strings.Split(tok, ".")[:2], ".")))
		if len(sig) != 64 || !ecdsa.Verify(&key.PublicKey, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"reason":"InvalidProviderToken"}`))
			return
		}
		var hdr map[string]any
		_ = json.Unmarshal(part(tok, 0), &hdr)
		if hdr["kid"] != "KEY123" {
			w.WriteHeader(403)
			return
		}
		got.path, got.topic, got.kind = r.URL.Path, r.Header.Get("apns-topic"), r.Header.Get("apns-push-type")
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		if strings.HasSuffix(r.URL.Path, "/gone") {
			w.WriteHeader(410)
			_, _ = w.Write([]byte(`{"reason":"Unregistered"}`))
		}
	}))
	defer srv.Close()
	a := &APNs{KeyID: "KEY123", TeamID: "TEAM", Topic: "llc.biodream.bitvoice", Key: pemPKCS8(t, key), Base: srv.URL, Client: srv.Client()}
	err := a.Send(context.Background(), Message{Platform: "ios", Token: "abcd", CallID: "c1", From: "100", Server: "https://pbx.example"})
	if err != nil || got.path != "/3/device/abcd" || got.topic != "llc.biodream.bitvoice.voip" || got.kind != "voip" || got.body["callId"] != "c1" {
		t.Fatalf("%v %+v", err, got)
	}
	if err := a.Send(context.Background(), Message{Token: "gone"}); !errors.Is(err, ErrGone) {
		t.Fatalf("gone token: %v", err)
	}
	// A chat message is a normal alert (not a VoIP wake-up): the plain topic, the alert push type, and a title/body.
	if err := a.Send(context.Background(), Message{Platform: "ios", Token: "abcd", Kind: "chat", From: "100", Title: "Dana", Body: "Hello", Server: "https://pbx.example"}); err != nil {
		t.Fatal(err)
	}
	if got.topic != "llc.biodream.bitvoice" || got.kind != "alert" {
		t.Fatalf("chat push topic %q kind %q", got.topic, got.kind)
	}
	alert := got.body["aps"].(map[string]any)["alert"].(map[string]any)
	if alert["title"] != "Dana" || alert["body"] != "Hello" || got.body["kind"] != "chat" {
		t.Fatalf("chat body %+v", got.body)
	}
}

func TestFCM(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	var sent map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		a := r.Form.Get("assertion")
		sum := sha256.Sum256([]byte(strings.Join(strings.Split(a, ".")[:2], ".")))
		if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], part(a, 2)) != nil {
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"ya29.test","expires_in":3600}`))
	})
	mux.HandleFunc("/v1/projects/bv-test/messages:send", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ya29.test" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	sa, _ := json.Marshal(map[string]string{"project_id": "bv-test", "client_email": "push@bv-test.iam.gserviceaccount.com",
		"private_key": pemPKCS8(t, key), "token_uri": srv.URL + "/token"})
	f := &FCM{ServiceAccount: string(sa), Base: srv.URL, Client: srv.Client()}
	if err := f.Send(context.Background(), Message{Platform: "android", Token: "tok", CallID: "c2", From: "101"}); err != nil {
		t.Fatal(err)
	}
	msg := sent["message"].(map[string]any)
	if msg["token"] != "tok" || msg["data"].(map[string]any)["callId"] != "c2" || msg["android"].(map[string]any)["priority"] != "high" {
		t.Fatalf("sent %v", sent)
	}
	// A chat message carries a notification (shown when the app is in the background) and kind=chat data.
	if err := f.Send(context.Background(), Message{Platform: "android", Token: "tok", Kind: "chat", From: "101", Title: "Sam", Body: "Hi there"}); err != nil {
		t.Fatal(err)
	}
	msg = sent["message"].(map[string]any)
	note, _ := msg["notification"].(map[string]any)
	if note == nil || note["title"] != "Sam" || note["body"] != "Hi there" || msg["data"].(map[string]any)["kind"] != "chat" {
		t.Fatalf("chat sent %v", sent)
	}
}

func TestRelaySignatures(t *testing.T) {
	var got Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if _, err := Verify(r, body, func(k string) (string, bool) { return "s3cret-s3cret", k == "inst-1" }, time.Now()); err != nil {
			w.WriteHeader(401)
			return
		}
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(202)
	}))
	defer srv.Close()
	r := &Relay{URL: srv.URL, KeyID: "inst-1", Secret: "s3cret-s3cret"}
	if err := r.Send(context.Background(), Message{Platform: "ios", Token: "t", CallID: "c3"}); err != nil || got.CallID != "c3" {
		t.Fatalf("%v %+v", err, got)
	}
	bad := &Relay{URL: srv.URL, KeyID: "inst-1", Secret: "wrong"}
	if err := bad.Send(context.Background(), Message{}); err == nil {
		t.Fatal("wrong secret accepted")
	}
	// A replayed request from long ago is refused.
	body := []byte(`{}`)
	req, _ := http.NewRequest("POST", srv.URL, bytes.NewReader(body))
	old := "1000000000"
	req.Header.Set("X-BitVoice-Key", "inst-1")
	req.Header.Set("X-BitVoice-Time", old)
	req.Header.Set("X-BitVoice-Signature", Sign("s3cret-s3cret", old, body))
	if _, err := Verify(req, body, func(string) (string, bool) { return "s3cret-s3cret", true }, time.Now()); err == nil {
		t.Fatal("stale request accepted")
	}
}
