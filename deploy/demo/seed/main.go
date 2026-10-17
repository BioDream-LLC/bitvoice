// Command seed fills a new BitVoice server with an invented small business, for the public demo:
// people, ring groups, a queue, a menu, a schedule, a call script, a switched-off phone line with its
// routes, a read-only visitor login, a browser-phone extension for visitors, and a few hundred calls
// made by the built-in call generator so the reports, wallboard and call quality screens have data.
// Everything is invented. It runs on the server itself (setup is open to this computer only):
//
//	go run ./deploy/demo/seed -url https://127.0.0.1 -admin-password-file /root/bitvoice-demo-admin
//
// The administrator's password is made up and written to that file (mode 600); the visitor signs in
// as demo / try-bitvoice-demo and uses the browser phone as extension 150 / try-the-demo-phone.
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"time"
)

type client struct {
	base string
	http *http.Client
}

func (c *client) do(method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	req.Header.Set("X-BitVoice", "1")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func main() {
	url := flag.String("url", "https://127.0.0.1", "the server's web interface")
	pwFile := flag.String("admin-password-file", "", "where to write the administrator's made-up password (mode 600)")
	domain := flag.String("domain", "", "the demo's public name, for a Let's Encrypt certificate (optional)")
	email := flag.String("email", "", "who Let's Encrypt writes to about the certificate (with -domain)")
	calls := flag.Int("calls", 120, "calls the generator makes for the reports")
	public := flag.String("public-address", "", "the address browsers reach the server at (a cloud server's public IP, behind its NAT)")
	flag.Parse()
	if err := run(*url, *pwFile, *domain, *email, *public, *calls); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run(url, pwFile, domain, email, public string, calls int) error {
	jar, _ := cookiejar.New(nil)
	c := &client{base: strings.TrimRight(url, "/"), http: &http.Client{Jar: jar, Timeout: 30 * time.Second,
		// The server's own certificate, on this computer: until Let's Encrypt gives it one.
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}}
	for i := 0; ; i++ {
		if c.do("GET", "/api/health/live", nil, nil) == nil {
			break
		}
		if i > 60 {
			return errors.New("the server did not answer")
		}
		time.Sleep(time.Second)
	}
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	admin := base64.RawURLEncoding.EncodeToString(b)
	if pwFile != "" {
		if err := os.WriteFile(pwFile, []byte(admin+"\n"), 0o600); err != nil {
			return err
		}
	}
	people := []map[string]any{
		{"number": "100", "name": "Front desk"}, {"number": "101", "name": "Avery Brooks"}, {"number": "102", "name": "Jordan Kim"},
		{"number": "103", "name": "Sam Rivera"}, {"number": "104", "name": "Taylor Chen"}, {"number": "105", "name": "Morgan Patel"},
		{"number": "150", "name": "Demo visitor"},
	}
	if err := c.do("POST", "/api/setup/complete", map[string]any{
		"organisation": "Northwind Dental (demo)", "purpose": "office", "country": "us", "timezone": "America/Chicago",
		"admin": map[string]any{"username": "admin", "name": "Demo administrator", "password": admin}, "extensions": people,
		"publicAddress": public,
	}, nil); err != nil {
		return err
	}
	if err := c.do("POST", "/api/auth/login", map[string]any{"username": "admin", "password": admin}, nil); err != nil {
		return err
	}
	if err := c.do("POST", "/api/users", map[string]any{"username": "demo", "name": "Demo visitor", "role": "viewer", "password": "try-bitvoice-demo"}, nil); err != nil {
		return err
	}
	// The visitor's browser phone: extension 150 with a known password, and departments for the others.
	var exts struct {
		Items []map[string]any `json:"items"`
	}
	if err := c.do("GET", "/api/extensions", nil, &exts); err != nil {
		return err
	}
	dept := map[string]string{"100": "Reception", "101": "Hygiene", "102": "Hygiene", "103": "Billing", "104": "Billing", "105": "Dentists"}
	for _, x := range exts.Items {
		n := x["number"].(string)
		x["department"] = dept[n]
		if n == "150" {
			x["secret"] = "try-the-demo-phone"
			x["voicemail"] = map[string]any{"enabled": false}
			x["noAnswer"] = nil
			x["outbound"] = false
		}
		if err := c.do("PUT", "/api/extensions/"+x["id"].(string), x, nil); err != nil {
			return err
		}
	}
	steps := []struct {
		path string
		body map[string]any
	}{
		{"/api/ring-groups", map[string]any{"number": "600", "name": "Reception team", "strategy": "all", "members": []string{"100", "101", "102"}, "ringSeconds": 20,
			"noAnswer": map[string]any{"type": "voicemail", "target": "100"}}},
		{"/api/queues", map[string]any{"number": "700", "name": "Billing questions", "agents": []string{"103", "104"}, "strategy": "longest-idle",
			"ringSeconds": 15, "wrapUpSeconds": 10, "maxWaitSeconds": 300, "announcePosition": true, "announceSeconds": 60,
			"timeout": map[string]any{"type": "voicemail", "target": "103"}}},
		{"/api/scripts", map[string]any{"name": "Front door (call script)", "language": "js", "mode": "call", "number": "810",
			"source":   "const k = collect({ max: 1, timeout: 5, prompt: \"beep\" })\nlog(\"pressed \" + k)\nif (k === \"1\") return { type: \"ringgroup\", target: \"600\" }\nif (k === \"2\") return { type: \"queue\", target: \"700\" }\nplay(\"confirm\")\nhangup()",
			"fallback": map[string]any{"type": "ringgroup", "target": "600"}}},
		{"/api/trunks", map[string]any{"name": "Phone line (demo, off)", "enabled": false, "host": "sip.carrier.example", "port": 5060, "transport": "udp",
			"username": "northwind", "password": "not-a-real-password", "register": true}},
	}
	for _, s := range steps {
		if err := c.do("POST", s.path, s.body, nil); err != nil {
			return err
		}
	}
	var menus struct {
		Item map[string]any `json:"item"`
	}
	if err := c.do("POST", "/api/menus", map[string]any{"number": "800", "name": "Main menu", "dialExtensions": true, "timeoutSeconds": 5, "repeats": 2,
		"options": []map[string]any{
			{"key": "1", "label": "Appointments", "destination": map[string]any{"type": "ringgroup", "target": "600"}},
			{"key": "2", "label": "Billing", "destination": map[string]any{"type": "queue", "target": "700"}},
			{"key": "0", "label": "Front desk", "destination": map[string]any{"type": "extension", "target": "100"}}},
		"timeout": map[string]any{"type": "extension", "target": "100"}}, &menus); err != nil {
		return err
	}
	menuID, _ := menus.Item["id"].(string)
	var sched struct {
		Item map[string]any `json:"item"`
	}
	if err := c.do("POST", "/api/schedules", map[string]any{"name": "Office hours",
		"hours":  []map[string]any{{"days": []int{1, 2, 3, 4, 5}, "start": "08:00", "end": "17:30"}, {"days": []int{6}, "start": "09:00", "end": "12:00"}},
		"open":   map[string]any{"type": "menu", "target": menuID},
		"closed": map[string]any{"type": "voicemail", "target": "100"}}, &sched); err != nil {
		return err
	}
	var trunks struct {
		Items []map[string]any `json:"items"`
	}
	_ = c.do("GET", "/api/trunks", nil, &trunks)
	if len(trunks.Items) > 0 {
		tid := trunks.Items[0]["id"].(string)
		_ = c.do("POST", "/api/inbound-routes", map[string]any{"name": "Main number", "did": "3125550100", "trunk": tid,
			"destination": map[string]any{"type": "schedule", "target": sched.Item["id"]}}, nil)
		_ = c.do("POST", "/api/outbound-routes", map[string]any{"name": "Local and national", "patterns": []string{"NXXNXXXXXX", "1NXXNXXXXXX"}, "trunks": []string{tid}}, nil)
	}
	if domain != "" {
		var s map[string]any
		if err := c.do("GET", "/api/settings", nil, &s); err != nil {
			return err
		}
		sys := s["system"].(map[string]any)
		sys["acme"] = map[string]any{"enabled": true, "domains": []string{domain}, "email": email}
		if err := c.do("PUT", "/api/settings", s, nil); err != nil {
			return err
		}
	}
	// History for the reports: the generator rings the team and the queue, inside BitVoice only.
	for _, lt := range []map[string]any{
		{"from": "105", "to": "600", "answerAs": "101", "calls": calls / 2, "rate": 2, "concurrent": 4, "holdSeconds": 8},
		{"from": "105", "to": "700", "answerAs": "103", "calls": calls / 2, "rate": 2, "concurrent": 4, "holdSeconds": 8},
	} {
		if err := c.do("POST", "/api/loadtest", lt, nil); err != nil {
			return fmt.Errorf("call generator: %w", err)
		}
		for i := 0; i < 600; i++ {
			var st struct {
				Run *struct {
					Running bool `json:"running"`
				} `json:"run"`
			}
			time.Sleep(time.Second)
			if c.do("GET", "/api/loadtest", nil, &st) == nil && (st.Run == nil || !st.Run.Running) {
				break
			}
		}
	}
	fmt.Println("demo ready: sign in as demo / try-bitvoice-demo; the browser phone is extension 150 / try-the-demo-phone")
	return nil
}
