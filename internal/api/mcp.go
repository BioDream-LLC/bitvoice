package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/biodream-llc/bitvoice/internal/store"
)

// BitVoice's Model Context Protocol server (https://modelcontextprotocol.io), Streamable HTTP at
// /mcp, JSON responses only. An AI assistant (Claude, ChatGPT, Gemini, Cursor ...) connects with an
// API token or by signing in (OAuth), lists the tools and calls them. Every tool is one of the REST
// API's own routes, run in-process with the caller's credentials: what an assistant may see and
// change is exactly what its token may, and its changes are in the audit log under its name.

// mcpVersions are the protocol revisions this server speaks, newest first.
var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26"}

// mcpTool is one tool: its description for the model, and the API request it makes.
type mcpTool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
	// act: it changes something (needs Settings, Let assistants act, and a token that may change things).
	act bool
	// call turns the arguments into the API request: method, path with query, JSON body (or nil).
	call func(a mcpArgs) (method, path string, body any, err string)
}

type mcpArgs map[string]any

func (a mcpArgs) str(k string) string {
	v, _ := a[k].(string)
	return strings.TrimSpace(v)
}

func (a mcpArgs) num(k string, def, most int) int {
	n := def
	switch v := a[k].(type) {
	case float64:
		n = int(v)
	case string:
		if x, err := strconv.Atoi(v); err == nil {
			n = x
		}
	}
	if n < 1 {
		n = def
	}
	return min(n, most)
}

func props(p map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": p, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func readOnly() map[string]any {
	return map[string]any{"readOnlyHint": true, "destructiveHint": false, "openWorldHint": false}
}

// mcpTools is the tool list. Reads first, then actions.
var mcpTools = []mcpTool{
	{Name: "calls_now", Title: "Calls in progress",
		Description: "The calls going on now in the phone system: who is calling whom, how long, which queue or extension, and the call's id (for hang_up_call).",
		InputSchema: props(map[string]any{}), Annotations: readOnly(),
		call: func(mcpArgs) (string, string, any, string) { return "GET", "/api/calls", nil, "" }},
	{Name: "call_history", Title: "Call history",
		Description: "Past calls, newest first: time, caller, number called, who answered, how long, the outcome and why it went where it went. Search by number or name.",
		InputSchema: props(map[string]any{
			"search":    strProp("A phone number, part of one, or a name to look for. Empty: all calls."),
			"direction": map[string]any{"type": "string", "enum": []string{"inbound", "outbound", "internal"}, "description": "Only calls in this direction."},
			"since":     strProp("Only calls from this time on (RFC 3339, such as 2026-10-30T00:00:00Z)."),
			"limit":     intProp("How many calls, 1 to 100 (default 20)."),
		}), Annotations: readOnly(),
		call: func(a mcpArgs) (string, string, any, string) {
			q := url.Values{"limit": {strconv.Itoa(a.num("limit", 20, 100))}}
			for _, k := range []string{"search", "direction", "since"} {
				if v := a.str(k); v != "" {
					q.Set(k, v)
				}
			}
			return "GET", "/api/cdr?" + q.Encode(), nil, ""
		}},
	{Name: "extensions", Title: "Extensions",
		Description: "The extensions (people and phones): number, name, department, email, whether enabled. Use it to find someone's extension number.",
		InputSchema: props(map[string]any{"search": strProp("Part of a name, number or department. Empty: all.")}), Annotations: readOnly(),
		call: func(mcpArgs) (string, string, any, string) { return "GET", "/api/extensions", nil, "" }},
	{Name: "phones_connected", Title: "Phones connected",
		Description: "Which phones are registered (connected) now, for which extension, from which address and with which device.",
		InputSchema: props(map[string]any{}), Annotations: readOnly(),
		call: func(mcpArgs) (string, string, any, string) { return "GET", "/api/registrations", nil, "" }},
	{Name: "voicemail", Title: "Voicemail",
		Description: "Voicemail messages: mailbox, caller, time, length, heard or not, and the transcript when there is one.",
		InputSchema: props(map[string]any{"box": strProp("Only this mailbox (an extension number). Empty: every mailbox.")}), Annotations: readOnly(),
		call: func(a mcpArgs) (string, string, any, string) {
			p := "/api/voicemail"
			if b := a.str("box"); b != "" {
				p += "?box=" + url.QueryEscape(b)
			}
			return "GET", p, nil, ""
		}},
	{Name: "search_transcripts", Title: "Search call transcripts",
		Description: "Search what was said on recorded calls and voicemail (their transcripts): the calls whose words match, with the matching passages.",
		InputSchema: props(map[string]any{"query": strProp("Words to look for."), "limit": intProp("How many, 1 to 50 (default 10).")}, "query"), Annotations: readOnly(),
		call: func(a mcpArgs) (string, string, any, string) {
			if a.str("query") == "" {
				return "", "", nil, "say what to look for (query)"
			}
			return "GET", "/api/transcripts?" + url.Values{"q": {a.str("query")}, "limit": {strconv.Itoa(a.num("limit", 10, 50))}}.Encode(), nil, ""
		}},
	{Name: "queues", Title: "Queues now",
		Description: "Each call queue now: calls waiting and for how long, agents logged in, busy or free, paused.",
		InputSchema: props(map[string]any{}), Annotations: readOnly(),
		call: func(mcpArgs) (string, string, any, string) { return "GET", "/api/queues/status", nil, "" }},
	{Name: "trunks", Title: "Phone lines (trunks)",
		Description: "The phone lines to providers (SIP trunks): registered or not, reachable, calls on each.",
		InputSchema: props(map[string]any{}), Annotations: readOnly(),
		call: func(mcpArgs) (string, string, any, string) { return "GET", "/api/trunks/status", nil, "" }},
	{Name: "health", Title: "System health",
		Description: "BitVoice's own checks: what is working, what needs attention, and why.",
		InputSchema: props(map[string]any{}), Annotations: readOnly(),
		call: func(mcpArgs) (string, string, any, string) { return "GET", "/api/health", nil, "" }},
	{Name: "directory", Title: "Company directory",
		Description: "The company phone book: people and outside contacts with their numbers.",
		InputSchema: props(map[string]any{"search": strProp("Part of a name or number. Empty: all.")}), Annotations: readOnly(),
		call: func(mcpArgs) (string, string, any, string) { return "GET", "/api/directory", nil, "" }},
	{Name: "text_messages", Title: "Text messages",
		Description: "Text message (SMS) conversations: with a number, the messages to and from it, else the latest conversations.",
		InputSchema: props(map[string]any{"our": strProp("Our number, as on the conversation."), "other": strProp("The other side's number.")}), Annotations: readOnly(),
		call: func(a mcpArgs) (string, string, any, string) {
			if a.str("our") != "" && a.str("other") != "" {
				return "GET", "/api/texts/thread?" + url.Values{"our": {a.str("our")}, "other": {a.str("other")}}.Encode(), nil, ""
			}
			return "GET", "/api/texts", nil, ""
		}},

	{Name: "place_call", Title: "Place a call", act: true,
		Description: "Click to call: rings the extension's phone first and, when someone there picks up, calls the number for them, as if they had dialled it (their routes, caller ID and limits apply).",
		InputSchema: props(map[string]any{"extension": strProp("The extension whose phone rings first, such as 101."), "number": strProp("The number to call for them.")}, "extension", "number"),
		Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true},
		call: func(a mcpArgs) (string, string, any, string) {
			if a.str("extension") == "" || a.str("number") == "" {
				return "", "", nil, "name the extension and the number"
			}
			return "POST", "/api/calls", map[string]string{"extension": a.str("extension"), "number": a.str("number")}, ""
		}},
	{Name: "send_text", Title: "Send a text message", act: true,
		Description: "Sends a text message (SMS) from one of the organisation's texting numbers.",
		InputSchema: props(map[string]any{"from": strProp("Our texting number to send from."), "to": strProp("The number to send to."), "text": strProp("The message.")}, "from", "to", "text"),
		Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": true},
		call: func(a mcpArgs) (string, string, any, string) {
			return "POST", "/api/texts", map[string]string{"from": a.str("from"), "to": a.str("to"), "text": a.str("text")}, ""
		}},
	{Name: "hang_up_call", Title: "Hang up a call", act: true,
		Description: "Ends a call in progress (its id from calls_now).",
		InputSchema: props(map[string]any{"id": strProp("The call's id, from calls_now.")}, "id"),
		Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": false},
		call: func(a mcpArgs) (string, string, any, string) {
			if a.str("id") == "" {
				return "", "", nil, "give the call's id (from calls_now)"
			}
			return "DELETE", "/api/calls/" + url.PathEscape(a.str("id")), nil, ""
		}},
}

func findMCPTool(name string) *mcpTool {
	for i := range mcpTools {
		if mcpTools[i].Name == name {
			return &mcpTools[i]
		}
	}
	return nil
}

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeRPC(w http.ResponseWriter, status int, id json.RawMessage, result any, e *rpcError) {
	out := map[string]any{"jsonrpc": "2.0"}
	if id != nil {
		out["id"] = id
	}
	if e != nil {
		out["error"] = e
	} else {
		out["result"] = result
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(out)
}

// mcpRoutes serves /mcp.
func (s *Server) mcpRoutes(m *http.ServeMux) {
	m.HandleFunc("/mcp", s.mcp)
}

func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.Store.Config()
	if !cfg.System.MCP.Enabled {
		writeRPC(w, http.StatusNotFound, nil, nil, &rpcError{-32000, "AI assistants (MCP) are off: an administrator switches them on in Settings, AI assistants (MCP)"})
		return
	}
	// A page on another site must never reach this through a browser (DNS rebinding): only no
	// Origin (programs) or this server's own.
	if o := r.Header.Get("Origin"); o != "" && !sameOrigin(o, r) {
		writeRPC(w, http.StatusForbidden, nil, nil, &rpcError{-32000, "requests from web pages of other sites are refused"})
		return
	}
	if r.Method != http.MethodPost {
		// No stream of server messages (GET) and no sessions to end (DELETE).
		w.Header().Set("Allow", "POST")
		writeRPC(w, http.StatusMethodNotAllowed, nil, nil, &rpcError{-32000, "send JSON-RPC messages with POST"})
		return
	}
	if v := r.Header.Get("MCP-Protocol-Version"); v != "" && !contains(mcpVersions, v) {
		writeRPC(w, http.StatusBadRequest, nil, nil, &rpcError{-32600, "unsupported MCP-Protocol-Version " + v + "; this server speaks " + strings.Join(mcpVersions, ", ")})
		return
	}
	// Only bearer tokens: an assistant never rides on a browser's sign-in cookie.
	u, ok := s.mcpUser(r)
	if !ok {
		s.mcpChallenge(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeRPC(w, http.StatusBadRequest, nil, nil, &rpcError{-32700, "the request could not be read"})
		return
	}
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '[' {
		writeRPC(w, http.StatusBadRequest, nil, nil, &rpcError{-32600, "send one JSON-RPC message per request (batches are not part of MCP since 2025-06-18)"})
		return
	}
	var msg rpcMsg
	if err := json.Unmarshal(body, &msg); err != nil || msg.JSONRPC != "2.0" {
		writeRPC(w, http.StatusBadRequest, nil, nil, &rpcError{-32700, "not a JSON-RPC 2.0 message"})
		return
	}
	if msg.ID == nil || string(msg.ID) == "null" {
		// A notification (notifications/initialized, cancelled ...) or a response: nothing to answer.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
			ClientInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"clientInfo"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		v := mcpVersions[0]
		if contains(mcpVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		s.Log.Info("MCP: an assistant connected", "client", p.ClientInfo.Name, "version", p.ClientInfo.Version, "as", u.Username, "protocol", v)
		writeRPC(w, 200, msg.ID, map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "bitvoice", "title": "BitVoice", "version": s.Version},
			"instructions": "BitVoice is this organisation's phone system. Use the tools to look at calls, call history, " +
				"voicemail, transcripts, queues, lines and health. Phone numbers are as dialled or in E.164 (+15551234567). " +
				"Actions (placing calls, texts, hanging up) need an administrator to allow them; say so when one is refused.",
		}, nil)
	case "ping":
		writeRPC(w, 200, msg.ID, map[string]any{}, nil)
	case "tools/list":
		act := cfg.System.MCP.Act && u.Role == "admin"
		list := []mcpTool{}
		for _, t := range mcpTools {
			if t.act && !act {
				continue // tools it may not use are not offered, so the model does not plan with them
			}
			list = append(list, t)
		}
		writeRPC(w, 200, msg.ID, map[string]any{"tools": list}, nil)
	case "tools/call":
		var p struct {
			Name      string  `json:"name"`
			Arguments mcpArgs `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			writeRPC(w, 200, msg.ID, nil, &rpcError{-32602, "params: name and arguments"})
			return
		}
		t := findMCPTool(p.Name)
		if t == nil {
			writeRPC(w, 200, msg.ID, nil, &rpcError{-32602, "no tool named " + p.Name})
			return
		}
		writeRPC(w, 200, msg.ID, s.mcpCall(r, cfg.System.MCP.Act, u, t, p.Arguments), nil)
	default:
		writeRPC(w, 200, msg.ID, nil, &rpcError{-32601, "no method " + msg.Method})
	}
}

// mcpUser is the caller: an API token, or an access token from signing in (OAuth).
func (s *Server) mcpUser(r *http.Request) (store.User, bool) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return store.User{}, false
	}
	return s.user(r.WithContext(context.WithValue(r.Context(), mcpCtxKey{}, true)))
}

// mcpChallenge answers a request without a valid token (401), saying where to sign in for
// assistants that sign in by OAuth (RFC 9728).
func (s *Server) mcpChallenge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+s.externalBase(r)+`/.well-known/oauth-protected-resource/mcp"`)
	writeRPC(w, http.StatusUnauthorized, nil, nil, &rpcError{-32001, "send an API token (Authorization: Bearer bv_...), made on Administrators, API tokens"})
}

// mcpCall runs one tool through the API's own route, as the caller.
func (s *Server) mcpCall(r *http.Request, actOn bool, u store.User, t *mcpTool, a mcpArgs) map[string]any {
	fail := func(msg string) map[string]any {
		return map[string]any{"content": []map[string]any{{"type": "text", "text": msg}}, "isError": true}
	}
	if t.act && !actOn {
		return fail("an administrator has not let assistants act (Settings, AI assistants, Let assistants act): reading only")
	}
	if t.act && u.Role != "admin" {
		return fail("this token may only read: actions need a token that may change things")
	}
	method, path, body, bad := t.call(a)
	if bad != "" {
		return fail(bad)
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	in, err := http.NewRequestWithContext(context.WithValue(r.Context(), mcpCtxKey{}, true), method, path, rd)
	if err != nil {
		return fail("the request could not be made: " + err.Error())
	}
	in.Header.Set("Authorization", r.Header.Get("Authorization"))
	in.Header.Set("Content-Type", "application/json")
	in.Header.Set(csrfHeader, "1")
	in.RemoteAddr, in.Host, in.TLS = r.RemoteAddr, r.Host, r.TLS
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, in)
	out := rec.Body.Bytes()
	if rec.Code >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(out, &e) == nil && e.Message != "" {
			return fail(e.Message)
		}
		return fail(http.StatusText(rec.Code))
	}
	var v any
	if json.Unmarshal(out, &v) != nil {
		return fail("BitVoice answered something that is not JSON")
	}
	v = mcpTrim(t.Name, a, v)
	text, _ := json.Marshal(v)
	res := map[string]any{"content": []map[string]any{{"type": "text", "text": mcpCap(string(text))}}}
	if obj, ok := v.(map[string]any); ok {
		res["structuredContent"] = obj
	}
	return res
}

// mcpTrim narrows an answer to what the tool's arguments asked for (lists filtered by search), and
// keeps secrets out whatever the token's role.
func mcpTrim(tool string, a mcpArgs, v any) any {
	obj, ok := v.(map[string]any)
	if !ok {
		return v
	}
	items, ok := obj["items"].([]any)
	if !ok {
		return v
	}
	q := strings.ToLower(a.str("search"))
	keep := []any{}
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if tool == "extensions" {
			// What a model needs to find people; never the phone's password or provisioning keys.
			m = pick(m, "number", "name", "department", "email", "enabled", "kind", "site", "floor", "room", "callerId", "language")
		}
		if q != "" && (tool == "extensions" || tool == "directory") {
			b, _ := json.Marshal(m)
			if !strings.Contains(strings.ToLower(string(b)), q) {
				continue
			}
		}
		keep = append(keep, m)
	}
	out := map[string]any{}
	for k, x := range obj {
		if k != "rev" {
			out[k] = x
		}
	}
	out["items"] = keep
	return out
}

func pick(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}

// mcpCap keeps one answer to about 60,000 characters: a model's context is not a database dump.
func mcpCap(s string) string {
	const most = 60000
	if len(s) <= most {
		return s
	}
	cut := most
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n[cut: the answer was longer; ask for fewer items or search more narrowly]"
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// sameOrigin says whether a browser Origin is this server as the request reached it.
func sameOrigin(origin string, r *http.Request) bool {
	o, err := url.Parse(origin)
	return err == nil && strings.EqualFold(o.Host, r.Host)
}

// externalBase is this server's address as the request reached it (https://host[:port], and the
// path of a hosted organisation).
func (s *Server) externalBase(r *http.Request) string {
	return "https://" + r.Host + strings.TrimSuffix(s.cookiePath(r), "/")
}

// mcpInfoRoutes tell the settings screen what /mcp offers.
func (s *Server) mcpInfoRoutes() {
	s.handle("GET /api/mcp", false, func(w http.ResponseWriter, r *http.Request, u store.User) {
		type tool struct {
			Name        string `json:"name"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Act         bool   `json:"act"`
		}
		list := []tool{}
		for _, t := range mcpTools {
			list = append(list, tool{t.Name, t.Title, t.Description, t.act})
		}
		writeJSON(w, 200, map[string]any{"address": s.externalBase(r) + "/mcp", "tools": list, "versions": mcpVersions})
	})
}
