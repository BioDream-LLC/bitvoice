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

// A lookup by number names the device by its telephone number (RFC 6155).
func TestLocateNumber(t *testing.T) {
	var got string
	lis := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		io.WriteString(w, `<locationResponse xmlns="urn:ietf:params:xml:ns:geopriv:held">`+circlePIDF+`</locationResponse>`)
	}))
	defer lis.Close()
	l, _, err := LocateNumber(context.Background(), lis.Client(), lis.URL, "(717) 555-0100")
	if err != nil || l.Radius != 35 {
		t.Fatalf("%+v %v", l, err)
	}
	if !strings.Contains(got, `<device xmlns="urn:ietf:params:xml:ns:geopriv:held:id"><tel>7175550100</tel></device>`) {
		t.Fatalf("request %s", got)
	}
	if _, _, err := LocateNumber(context.Background(), nil, lis.URL, "911"); err == nil {
		t.Fatal("looked up a number that is no number")
	}
}
