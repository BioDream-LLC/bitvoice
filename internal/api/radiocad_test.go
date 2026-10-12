package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
	"github.com/biodream-llc/bitvoice/internal/pbx"
	"github.com/biodream-llc/bitvoice/internal/store"
)

// Radio events reach CAD as JSON with the bearer token, and the radio log is readable by API.
func TestRadioDataToCAD(t *testing.T) {
	got := make(chan map[string]any, 4)
	auth := make(chan string, 4)
	cad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		auth <- r.Header.Get("Authorization")
		got <- m
	}))
	defer cad.Close()
	s, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, out := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatalf("setup: %d %v", code, out)
	}
	if _, _, err := s.Store.Update("test", "CAD", 0, func(cf *config.Config) error {
		cf.System.Radio.CADURL, cf.System.Radio.CADSecret = cad.URL, "cad-shared-secret"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.RunRadioCAD(ctx)
	time.Sleep(50 * time.Millisecond) // subscribed
	s.Bus.Publish("radio.emergency", pbx.RadioUnitEvent{Channel: "tg", ChannelName: "County fire", Unit: "bee07.ad5.0000c5", UnitID: 197, Emergency: true})
	select {
	case m := <-got:
		d, _ := m["data"].(map[string]any)
		if m["type"] != "radio.emergency" || d["unitId"] != float64(197) || d["emergency"] != true {
			t.Fatalf("CAD got %v", m)
		}
		if a := <-auth; a != "Bearer cad-shared-secret" {
			t.Fatalf("authorization %q", a)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("CAD got nothing")
	}

	s.Store.AddTransmission(store.Transmission{ID: "x1", Channel: "tg", ChannelName: "County fire", Direction: "rx", Start: time.Now().UTC(), Unit: "bee07.ad5.0000c5"})
	code, out := c.do("GET", "/api/radio/log?q=0000c5", nil)
	if items, _ := out["items"].([]any); code != 200 || len(items) != 1 {
		t.Fatalf("radio log %d %v", code, out)
	}
	// The CAD secret is not shown to a viewer (it is a secret by its name).
	view := config.Default()
	view.System.Radio.CADSecret = "x"
	config.StripSecrets(&view)
	if view.System.Radio.CADSecret != "" {
		t.Fatal("cadSecret is not treated as a secret")
	}
}
