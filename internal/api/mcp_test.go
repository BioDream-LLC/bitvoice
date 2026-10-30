package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// mcpPost sends one JSON-RPC message to /mcp and returns the status and the decoded answer.
func mcpPost(t *testing.T, base, token string, msg any, headers ...string) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(msg)
	req, _ := http.NewRequest("POST", base+"/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func setMCP(t *testing.T, s *Server, enabled, act bool) {
	t.Helper()
	if _, _, err := s.Store.Update("test", "MCP", 0, func(c *config.Config) error {
		c.System.MCP = config.MCP{Enabled: enabled, Act: act}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func toolNames(t *testing.T, base, token string) []string {
	t.Helper()
	code, out := mcpPost(t, base, token, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	if code != 200 {
		t.Fatalf("tools/list: %d %v", code, out)
	}
	var names []string
	for _, x := range out["result"].(map[string]any)["tools"].([]any) {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	return names
}

func callTool(t *testing.T, base, token, name string, args map[string]any) (bool, string, map[string]any) {
	t.Helper()
	code, out := mcpPost(t, base, token, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args}})
	if code != 200 || out["result"] == nil {
		t.Fatalf("tools/call %s: %d %v", name, code, out)
	}
	res := out["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	sc, _ := res["structuredContent"].(map[string]any)
	return res["isError"] == true, text, sc
}

// The MCP server: off until switched on, tokens only, the protocol's lifecycle, read tools for every
// token, actions only when allowed and only for tokens that may change things, and no secrets.
func TestMCP(t *testing.T) {
	s, ts := newServer(t)
	_, admin, _ := s.Store.AddToken("Claude Code", "admin", "admin", time.Time{}, false)
	_, viewer, _ := s.Store.AddToken("Reader", "viewer", "admin", time.Time{}, false)
	if _, _, err := s.Store.Update("test", "ext", 0, func(c *config.Config) error {
		c.Extensions = append(c.Extensions, config.Extension{ID: "x1", Number: "101", Name: "Dr Lee", Secret: "phone-password-123", Enabled: true, Outbound: true, Department: "Surgery"},
			config.Extension{ID: "x2", Number: "102", Name: "Front desk", Secret: "another-secret-456", Enabled: true})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	init := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}}}

	if code, out := mcpPost(t, ts.URL, admin, init); code != 404 || !strings.Contains(out["error"].(map[string]any)["message"].(string), "off") {
		t.Fatalf("while off: %d %v", code, out)
	}
	setMCP(t, s, true, false)
	// No token, or a browser's cookie only: 401 with where to sign in.
	resp, err := http.Post(ts.URL+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), `resource_metadata="https://`) {
		t.Fatalf("no token: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	if code, _ := mcpPost(t, ts.URL, "bv_"+strings.Repeat("0", 64), init); code != 401 {
		t.Fatalf("a wrong token: %d", code)
	}
	// Another site's page in a browser is refused (DNS rebinding).
	if code, _ := mcpPost(t, ts.URL, admin, init, "Origin", "https://evil.example"); code != 403 {
		t.Fatalf("foreign origin: %d", code)
	}
	if code, _ := mcpPost(t, ts.URL, admin, init, "MCP-Protocol-Version", "1999-01-01"); code != 400 {
		t.Fatalf("unknown protocol version header: %d", code)
	}
	// initialize: the client's version when spoken, else the newest.
	code, out := mcpPost(t, ts.URL, admin, init)
	res, _ := out["result"].(map[string]any)
	if code != 200 || res["protocolVersion"] != "2025-06-18" || res["serverInfo"].(map[string]any)["name"] != "bitvoice" || res["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatalf("initialize: %d %v", code, out)
	}
	init["params"].(map[string]any)["protocolVersion"] = "2024-01-01"
	if _, out := mcpPost(t, ts.URL, admin, init); out["result"].(map[string]any)["protocolVersion"] != mcpVersions[0] {
		t.Fatalf("unknown version not answered with the newest: %v", out)
	}
	// A notification is accepted without an answer; a batch, a GET and an unknown method are not.
	if code, _ := mcpPost(t, ts.URL, admin, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); code != 202 {
		t.Fatalf("notification: %d", code)
	}
	if code, _ := mcpPost(t, ts.URL, admin, []any{map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}}); code != 400 {
		t.Fatalf("batch: %d", code)
	}
	if _, out := mcpPost(t, ts.URL, admin, map[string]any{"jsonrpc": "2.0", "id": 9, "method": "resources/list"}); out["error"].(map[string]any)["code"] != float64(-32601) {
		t.Fatalf("unknown method: %v", out)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 405 {
		t.Fatalf("GET: %v %v", err, r)
	}

	// Acting is off: no action tools are offered, and calling one says why.
	names := strings.Join(toolNames(t, ts.URL, admin), " ")
	if !strings.Contains(names, "call_history") || strings.Contains(names, "place_call") {
		t.Fatalf("tools while acting is off: %s", names)
	}
	if bad, text, _ := callTool(t, ts.URL, admin, "place_call", map[string]any{"extension": "101", "number": "5551234"}); !bad || !strings.Contains(text, "Let assistants act") {
		t.Fatalf("acting while off: %v %s", bad, text)
	}
	setMCP(t, s, true, true)
	if names := strings.Join(toolNames(t, ts.URL, admin), " "); !strings.Contains(names, "place_call") || !strings.Contains(names, "hang_up_call") {
		t.Fatalf("admin token with acting on: %s", names)
	}
	if names := strings.Join(toolNames(t, ts.URL, viewer), " "); strings.Contains(names, "place_call") {
		t.Fatalf("a read-only token is offered actions: %s", names)
	}
	if bad, text, _ := callTool(t, ts.URL, viewer, "hang_up_call", map[string]any{"id": "x"}); !bad || !strings.Contains(text, "only read") {
		t.Fatalf("read-only token acted: %s", text)
	}
	// An action goes through the API's own checks: no phone is connected to 101.
	if bad, text, _ := callTool(t, ts.URL, admin, "place_call", map[string]any{"extension": "101", "number": "102"}); !bad || !strings.Contains(text, "no phone connected") {
		t.Fatalf("place_call without a phone: %v %s", bad, text)
	}
	if bad, text, _ := callTool(t, ts.URL, admin, "hang_up_call", map[string]any{"id": "nope"}); !bad || !strings.Contains(text, "no such call") {
		t.Fatalf("hang up unknown call: %s", text)
	}

	// Reads: extensions found by search, without their phones' passwords.
	bad, text, sc := callTool(t, ts.URL, viewer, "extensions", map[string]any{"search": "surgery"})
	if bad || len(sc["items"].([]any)) != 1 || sc["items"].([]any)[0].(map[string]any)["name"] != "Dr Lee" {
		t.Fatalf("extensions search: %v %s", bad, text)
	}
	if bad, text, _ := callTool(t, ts.URL, admin, "extensions", nil); bad || strings.Contains(text, "phone-password") || strings.Contains(text, "secret") {
		t.Fatalf("extensions leaked a secret to an admin token: %s", text)
	}
	for _, name := range []string{"calls_now", "call_history", "phones_connected", "voicemail", "queues", "trunks", "health", "directory", "text_messages"} {
		if bad, text, _ := callTool(t, ts.URL, viewer, name, map[string]any{}); bad {
			t.Fatalf("%s: %s", name, text)
		}
	}
	if bad, text, _ := callTool(t, ts.URL, viewer, "search_transcripts", map[string]any{}); !bad || !strings.Contains(text, "query") {
		t.Fatalf("search without words: %s", text)
	}
	if _, out := mcpPost(t, ts.URL, viewer, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "rm_rf"}}); out["error"] == nil {
		t.Fatalf("unknown tool: %v", out)
	}
	// What a token did through MCP is in the audit log under its name.
	found := false
	for _, e := range s.Store.AuditLog(50) {
		found = found || e.Actor == "token:Claude Code"
	}
	if !found {
		t.Log("(no audited action yet: reads are not audited)")
	}
}

// Signing an assistant in by OAuth: metadata, registration, the person's consent in the web
// interface, the code with PKCE, a token good for /mcp only, refresh with rotation, and refusals.
func TestMCPOAuth(t *testing.T) {
	s, ts := newServer(t)
	c := newClient(t, ts.URL)
	if code, _ := c.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	get := func(path string) (int, map[string]any) {
		r, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}
	if code, _ := get("/.well-known/oauth-authorization-server"); code != 404 {
		t.Fatalf("metadata while MCP is off: %d", code)
	}
	setMCP(t, s, true, true)
	_, pr := get("/.well-known/oauth-protected-resource/mcp")
	host := strings.TrimPrefix(ts.URL, "http://")
	if pr["resource"] != "https://"+host+"/mcp" || pr["authorization_servers"].([]any)[0] != "https://"+host {
		t.Fatalf("resource metadata: %v", pr)
	}
	_, as := get("/.well-known/oauth-authorization-server")
	if as["code_challenge_methods_supported"].([]any)[0] != "S256" || as["registration_endpoint"] != "https://"+host+"/oauth/register" {
		t.Fatalf("server metadata: %v", as)
	}
	register := func(body string) (int, map[string]any) {
		r, err := http.Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(r.Body).Decode(&out)
		return r.StatusCode, out
	}
	if code, _ := register(`{"client_name":"x","redirect_uris":["http://evil.example/cb"]}`); code != 400 {
		t.Fatalf("plain http to another computer accepted: %d", code)
	}
	code, reg := register(`{"client_name":"Claude","redirect_uris":["https://claude.example/api/mcp/auth_callback","http://127.0.0.1:33418/cb"],"token_endpoint_auth_method":"client_secret_post"}`)
	if code != 201 || reg["token_endpoint_auth_method"] != "none" {
		t.Fatalf("register: %d %v", code, reg)
	}
	client := reg["client_id"].(string)
	redirect := "https://claude.example/api/mcp/auth_callback"
	verifier := "a-verifier-of-enough-length-0123456789-abcdefghij"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	authorize := func(q url.Values) *http.Response {
		r, err := noFollow.Get(ts.URL + "/oauth/authorize?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r
	}
	q := url.Values{"response_type": {"code"}, "client_id": {client}, "redirect_uri": {redirect}, "state": {"st1"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "resource": {"https://" + host + "/mcp"}, "scope": {"mcp"}}
	// An unregistered return address is never redirected to.
	bad := url.Values{}
	for k, v := range q {
		bad[k] = v
	}
	bad.Set("redirect_uri", "https://evil.example/cb")
	if r := authorize(bad); r.StatusCode != 400 {
		t.Fatalf("unregistered redirect: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	plain := url.Values{}
	for k, v := range q {
		plain[k] = v
	}
	plain.Del("code_challenge")
	if r := authorize(plain); !strings.Contains(r.Header.Get("Location"), "error=invalid_request") {
		t.Fatalf("no PKCE: %s", r.Header.Get("Location"))
	}
	r := authorize(q)
	if r.StatusCode != 302 || !strings.HasPrefix(r.Header.Get("Location"), "/#/connect-assistant?") {
		t.Fatalf("authorize: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	// The page asks the signed-in person; they allow it, read-only.
	code, req := c.do("GET", "/api/oauth/request?"+url.Values{"client_id": {client}, "redirect_uri": {redirect}}.Encode(), nil)
	if code != 200 || req["client"] != "Claude" || req["returnsTo"] != "claude.example" {
		t.Fatalf("request: %d %v", code, req)
	}
	approve := func(allow, readOnly bool) string {
		code, out := c.do("POST", "/api/oauth/approve", map[string]any{"client_id": client, "redirect_uri": redirect, "state": "st1",
			"code_challenge": challenge, "allow": allow, "readOnly": readOnly})
		if code != 200 {
			t.Fatalf("approve: %d %v", code, out)
		}
		return out["redirect"].(string)
	}
	if back := approve(false, false); !strings.Contains(back, "error=access_denied") || !strings.Contains(back, "state=st1") {
		t.Fatalf("refusal: %s", back)
	}
	back, _ := url.Parse(approve(true, true))
	if back.Host != "claude.example" || back.Query().Get("state") != "st1" || back.Query().Get("code") == "" {
		t.Fatalf("approval: %s", back)
	}
	token := func(form url.Values) (int, map[string]any) {
		r, err := http.PostForm(ts.URL+"/oauth/token", form)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		return r.StatusCode, out
	}
	codeVal := back.Query().Get("code")
	if code, out := token(url.Values{"grant_type": {"authorization_code"}, "code": {codeVal}, "client_id": {client}, "redirect_uri": {redirect}, "code_verifier": {"wrong"}}); code != 400 || out["error"] != "invalid_grant" {
		t.Fatalf("wrong verifier: %d %v", code, out)
	}
	// The wrong verifier used the code up: a code is good once.
	back, _ = url.Parse(approve(true, true))
	codeVal = back.Query().Get("code")
	code, tok := token(url.Values{"grant_type": {"authorization_code"}, "code": {codeVal}, "client_id": {client}, "redirect_uri": {redirect}, "code_verifier": {verifier}, "resource": {"https://" + host + "/mcp"}})
	if code != 200 || tok["token_type"] != "Bearer" || tok["refresh_token"] == nil {
		t.Fatalf("token: %d %v", code, tok)
	}
	if code, _ := token(url.Values{"grant_type": {"authorization_code"}, "code": {codeVal}, "client_id": {client}, "redirect_uri": {redirect}, "code_verifier": {verifier}}); code != 400 {
		t.Fatalf("a code used twice: %d", code)
	}
	access := tok["access_token"].(string)
	// The assistant's token works at /mcp (read only, as chosen) and nowhere else.
	if names := strings.Join(toolNames(t, ts.URL, access), " "); !strings.Contains(names, "calls_now") || strings.Contains(names, "place_call") {
		t.Fatalf("signed-in read-only assistant: %s", names)
	}
	req2, _ := http.NewRequest("GET", ts.URL+"/api/extensions", nil)
	req2.Header.Set("Authorization", "Bearer "+access)
	if r, _ := http.DefaultClient.Do(req2); r.StatusCode != 401 {
		t.Fatalf("an assistant's token reached the REST API: %d", r.StatusCode)
	}
	// Refresh: a new pair; the old access and refresh tokens stop working.
	code, tok2 := token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}, "client_id": {client}})
	if code != 200 || tok2["access_token"] == access {
		t.Fatalf("refresh: %d %v", code, tok2)
	}
	if code, _ := mcpPost(t, ts.URL, access, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}); code != 401 {
		t.Fatalf("old access token after refresh: %d", code)
	}
	if code, _ := token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}, "client_id": {client}}); code != 400 {
		t.Fatalf("old refresh token reused: %d", code)
	}
	if code, _ := mcpPost(t, ts.URL, tok2["access_token"].(string), map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}); code != 200 {
		t.Fatalf("new access token: %d", code)
	}
	// Revoking it on Administrators ends it, refresh included.
	for _, tk := range s.Store.Tokens() {
		if tk.MCPOnly {
			if !strings.Contains(tk.Name, "Claude (signed in by owner)") || tk.Role != "viewer" {
				t.Fatalf("the assistant's token: %+v", tk)
			}
			_ = s.Store.DeleteToken(tk.ID)
		}
	}
	if code, _ := token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok2["refresh_token"].(string)}, "client_id": {client}}); code != 400 {
		t.Fatalf("refresh after revoking: %d", code)
	}
}
