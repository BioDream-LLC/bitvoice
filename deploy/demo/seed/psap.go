package main

import (
	"bufio"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/store"
)

// The demo's 911 side (Settings > Public-safety profiles): an ESInet stand-in on this computer
// (bitvoice-demo-911) sends invented 911 calls, a call-taker extension answers them, a stand-in
// speech service writes them out live, and, when the CAD's addresses are given, EIDOs and radio events
// go to a CAD.

// cadSettings are read from a file of KEY=VALUE or key: value lines: EIDO_URL, EIDO_TOKEN, RADIO_URL,
// RADIO_SECRET (any case).
type cadSettings map[string]string

func readCAD(path string) (cadSettings, error) {
	cad := cadSettings{}
	if path == "" {
		return cad, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return cad, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		i := strings.IndexAny(l, "=:")
		if i < 0 {
			continue
		}
		k := strings.ToUpper(strings.TrimSpace(l[:i]))
		cad[k] = strings.Trim(strings.TrimSpace(l[i+1:]), `"'`)
	}
	return cad, sc.Err()
}

// prepareToken puts the CAD's token into a new, empty data folder before the server first starts, so
// a nightly rebuild keeps the token the CAD was given.
func prepareToken(dir, tokenFile, radioTokenFile string) error {
	s, err := store.Open(dir)
	if err != nil {
		return err
	}
	defer s.Close()
	for _, t := range []struct {
		file, name string
		onBehalf   bool
	}{{tokenFile, "bitdispatcher CAD (demo)", true}, {radioTokenFile, "Demo radio system (positions)", false}} {
		if t.file == "" {
			continue
		}
		b, err := os.ReadFile(t.file)
		if err != nil {
			return err
		}
		if _, err := s.AddTokenSecret(t.name, "admin", "demo setup", time.Time{}, t.onBehalf, strings.TrimSpace(string(b))); err != nil {
			return err
		}
	}
	return nil
}

func seedPSAP(c *client, botSecret, sttURL string, cad cadSettings) error {
	if err := c.do("POST", "/api/extensions", map[string]any{"number": "190", "name": "Robin Ellis (911 demo call-taker)",
		"enabled": true, "secret": botSecret, "department": "911 centre (demo)", "voicemail": map[string]any{"enabled": false}}, nil); err != nil {
		return err
	}
	var trunk struct {
		Item map[string]any `json:"item"`
	}
	if err := c.do("POST", "/api/trunks", map[string]any{"name": "ESInet (demo stand-in on this server)", "enabled": true, "host": "127.0.0.1",
		"port": 5190, "transport": "udp", "register": false, "esinet": true, "allowFrom": []string{"127.0.0.1"}}, &trunk); err != nil {
		return err
	}
	if err := c.do("POST", "/api/ring-groups", map[string]any{"number": "690", "name": "911 call-takers (demo)", "strategy": "all",
		"members": []string{"190"}, "ringSeconds": 60}, nil); err != nil {
		return err
	}
	if err := c.do("POST", "/api/inbound-routes", map[string]any{"name": "911 from the ESInet (demo)", "did": "", "trunk": trunk.Item["id"],
		"destination": map[string]any{"type": "ringgroup", "target": "690"}}, nil); err != nil {
		return err
	}
	var s map[string]any
	if err := c.do("GET", "/api/settings", nil, &s); err != nil {
		return err
	}
	sys := s["system"].(map[string]any)
	sys["publicSafety"] = true
	psap, _ := sys["psap"].(map[string]any)
	if psap == nil {
		psap = map[string]any{}
	}
	psap["agencyId"] = "psap.demo.bitvoice.org"
	psap["elementId"] = "demo.bitvoice.org"
	if u := cad["EIDO_URL"]; u != "" {
		psap["eidoUrl"], psap["eidoToken"] = u, cad["EIDO_TOKEN"]
	}
	sys["psap"] = psap
	// The radio console: one P25 talkgroup, whose home RFSS is the demo's stand-in radio system on this
	// computer (bitvoice-demo-911), with names for its invented units; every transmission recorded.
	radio := map[string]any{"console": "880", "record": true,
		"p25": map[string]any{"wacn": "BEE07", "system": "1D5", "rfss": 2, "unit": 0x1001, "sipPort": 5194},
		"channels": []map[string]any{{"id": "fire", "name": "County fire dispatch (demo)", "kind": "p25", "group": 0x0911,
			"port": 41000, "home": "127.0.0.1:5192", "homeRfss": 1, "color": "red"}},
		// The names bitdispatcher's demo matches to its units E1, E2, L1, BC1 and A5.
		"aliases": []map[string]any{{"unit": 1101, "name": "Engine 1"}, {"unit": 1102, "name": "Engine 2"},
			{"unit": 1201, "name": "Ladder 1"}, {"unit": 1401, "name": "Battalion 1"}, {"unit": 1505, "name": "Ambulance 5"},
			{"unit": 0x1001, "name": "Dispatch"}}}
	if u := cad["RADIO_URL"]; u != "" {
		radio["cadUrl"], radio["cadSecret"] = u, cad["RADIO_SECRET"]
	}
	sys["radio"] = radio
	ai, _ := sys["ai"].(map[string]any)
	if ai == nil {
		ai = map[string]any{}
	}
	// Live transcripts from the stand-in speech service: no model, no key, nothing leaves the server.
	ai["enabled"], ai["model"], ai["key"], ai["localSpeech"] = true, "", "", false
	ai["transcribeUrl"], ai["liveTranscripts"] = sttURL, true
	ai["calls"], ai["briefings"], ai["voicemail"], ai["insights"] = false, false, false, false
	sys["ai"] = ai
	return c.do("PUT", "/api/settings", s, nil)
}
