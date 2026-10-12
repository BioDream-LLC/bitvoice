package i3

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDereference(t *testing.T) {
	calls := 0
	lis := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || !strings.Contains(string(b), "locationRequest") || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/held+xml") {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/held+xml")
		io.WriteString(w, `<locationResponse xmlns="urn:ietf:params:xml:ns:geopriv:held">`+circlePIDF+`</locationResponse>`)
	}))
	defer lis.Close()
	l, raw, err := Dereference(context.Background(), lis.Client(), lis.URL+"/loc/abc")
	if err != nil || !l.HasPoint() || l.Radius != 35 || !strings.HasPrefix(string(raw), "<presence") {
		t.Fatalf("%+v %v %q", l, err, raw[:20])
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<error xmlns="urn:ietf:params:xml:ns:geopriv:held" code="locationUnknown"/>`)
	}))
	defer bad.Close()
	if _, _, err := Dereference(context.Background(), bad.Client(), bad.URL); err == nil || !strings.Contains(err.Error(), "locationUnknown") {
		t.Fatalf("error: %v", err)
	}
	if _, _, err := Dereference(context.Background(), nil, "sip:lis@example.net"); err == nil {
		t.Fatal("sip reference fetched")
	}
}
