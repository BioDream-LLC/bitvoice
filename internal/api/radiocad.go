package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/biodream-llc/bitvoice/internal/store"
)

// Radio data to CAD: the radio events, as they happen, POSTed to the address CAD gives (System.
// Radio.CADURL), each as {"type": ..., "agency": ..., "data": {...}}. An emergency is tried three
// times; a CAD that is down never holds up the console.

// RunRadioCAD sends radio events to CAD until ctx ends.
func (s *Server) RunRadioCAD(ctx context.Context) {
	sub := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(sub)
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.C:
			if ev.Type != "radio.unit" && ev.Type != "radio.emergency" && ev.Type != "radio.emergency.ack" && ev.Type != "radio.transmission" {
				continue
			}
			cfg := s.config()
			url, secret := cfg.System.Radio.CADURL, cfg.System.Radio.CADSecret
			if url == "" {
				continue
			}
			body, _ := json.Marshal(map[string]any{"type": ev.Type, "agency": agencyID(cfg), "data": ev.Data})
			tries := 1
			if ev.Type == "radio.emergency" {
				tries = 3
			}
			go func(typ string) {
				var err error
				for i := 0; i < tries; i++ {
					if err = postRadioEvent(ctx, url, secret, body); err == nil {
						return
					}
					time.Sleep(time.Duration(i+1) * time.Second)
				}
				s.Log.Warn("radio: CAD did not take an event", "type", typ, "err", err)
				s.Bus.Publish("radio.cad_failed", map[string]string{"type": typ, "error": err.Error()})
			}(ev.Type)
		}
	}
}

func postRadioEvent(ctx context.Context, url, secret string, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("CAD said %s", resp.Status)
	}
	return nil
}

// radioLogRoutes serves the radio log, for the console page and for CAD to read what it missed.
func (s *Server) radioLogRoutes() {
	s.handle("GET /api/radio/log", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		q := store.RadioLogQuery{Channel: r.URL.Query().Get("channel"), Search: r.URL.Query().Get("q")}
		if v, err := time.Parse(time.RFC3339, r.URL.Query().Get("since")); err == nil {
			q.Since = v
		}
		if v, err := time.Parse(time.RFC3339, r.URL.Query().Get("until")); err == nil {
			q.Until = v
		}
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
			q.Limit = n
		}
		out, err := s.Store.RadioLog(q)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": out})
	})
}
