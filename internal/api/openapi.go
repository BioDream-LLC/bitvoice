package api

import (
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// The OpenAPI 3.1 description of the API, built from the routes as they are registered and the Go
// types behind each collection, so it cannot drift from what the server does.

type routeDoc struct {
	method, path string
	admin, open  bool
}

type apiDocs struct {
	mu      sync.Mutex
	routes  []routeDoc
	schemas map[string]reflect.Type // collection path segment -> item type
}

func (s *Server) noteRoute(pattern string, admin, open bool) {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		return
	}
	s.docs.mu.Lock()
	s.docs.routes = append(s.docs.routes, routeDoc{method, path, admin, open})
	s.docs.mu.Unlock()
}

func (s *Server) noteSchema(path string, t reflect.Type) {
	s.docs.mu.Lock()
	if s.docs.schemas == nil {
		s.docs.schemas = map[string]reflect.Type{}
	}
	s.docs.schemas[path] = t
	s.docs.mu.Unlock()
}

var timeType = reflect.TypeOf(time.Time{})

// schemaOf is a JSON Schema for a Go type, from its json tags.
func schemaOf(t reflect.Type, defs map[string]any) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == timeType {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaOf(t.Elem(), defs)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaOf(t.Elem(), defs)}
	case reflect.Struct:
		name := t.Name()
		if name != "" {
			if _, done := defs[name]; !done {
				defs[name] = nil // reserve against recursion
				props := map[string]any{}
				for i := 0; i < t.NumField(); i++ {
					f := t.Field(i)
					if !f.IsExported() {
						continue
					}
					tag := strings.Split(f.Tag.Get("json"), ",")[0]
					if tag == "-" {
						continue
					}
					if tag == "" {
						tag = f.Name
					}
					props[tag] = schemaOf(f.Type, defs)
				}
				defs[name] = map[string]any{"type": "object", "properties": props}
			}
			return map[string]any{"$ref": "#/components/schemas/" + name}
		}
	}
	return map[string]any{}
}

var pathParam = regexp.MustCompile(`\{([a-zA-Z]+)\}`)

func (s *Server) openAPI(w http.ResponseWriter, r *http.Request) {
	s.docs.mu.Lock()
	routes := append([]routeDoc(nil), s.docs.routes...)
	schemas := map[string]reflect.Type{}
	for k, v := range s.docs.schemas {
		schemas[k] = v
	}
	s.docs.mu.Unlock()
	defs := map[string]any{}
	settings := schemaOf(reflect.TypeOf(settings{}), defs)
	whole := schemaOf(reflect.TypeOf(config.Config{}), defs)
	paths := map[string]map[string]any{}
	sort.Slice(routes, func(i, j int) bool { return routes[i].path+routes[i].method < routes[j].path+routes[j].method })
	for _, rt := range routes {
		if !strings.HasPrefix(rt.path, "/api/") {
			continue
		}
		op := map[string]any{"responses": map[string]any{"200": map[string]any{"description": "OK"}}}
		var params []any
		for _, m := range pathParam.FindAllStringSubmatch(rt.path, -1) {
			params = append(params, map[string]any{"name": m[1], "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
		}
		if params != nil {
			op["parameters"] = params
		}
		tag := strings.Split(strings.TrimPrefix(rt.path, "/api/"), "/")[0]
		op["tags"] = []string{tag}
		switch {
		case rt.open:
			op["security"] = []any{}
		case rt.admin:
			op["description"] = "Needs an administrator."
		}
		// Bodies and answers for the configuration collections and settings.
		item := schemas[tag]
		segments := strings.Count(strings.TrimPrefix(rt.path, "/api/"), "/")
		switch {
		case item != nil:
			sc := schemaOf(item, defs)
			if segments == 0 && rt.method == "GET" {
				op["responses"] = map[string]any{"200": jsonResp(map[string]any{"type": "object", "properties": map[string]any{"items": map[string]any{"type": "array", "items": sc}, "rev": map[string]any{"type": "integer"}}})}
			} else {
				if rt.method == "POST" || rt.method == "PUT" {
					op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": sc}}}
				}
				code := "200"
				if rt.method == "POST" && segments == 0 {
					code = "201"
				}
				if rt.method == "DELETE" {
					op["responses"] = map[string]any{"200": jsonResp(map[string]any{"type": "object", "properties": map[string]any{"rev": map[string]any{"type": "integer"}}})}
				} else {
					op["responses"] = map[string]any{code: jsonResp(map[string]any{"type": "object", "properties": map[string]any{"item": sc, "rev": map[string]any{"type": "integer"}}})}
				}
			}
			if rt.method == "PUT" || rt.method == "DELETE" {
				op["parameters"] = append(params, map[string]any{"name": "If-Match", "in": "header", "description": "The revision you last saw; a newer one answers 409.", "schema": map[string]any{"type": "string"}})
			}
		case rt.path == "/api/settings":
			if rt.method == "PUT" {
				op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": settings}}}
			}
			op["responses"] = map[string]any{"200": jsonResp(settings)}
		case rt.path == "/api/config/export":
			op["responses"] = map[string]any{"200": jsonResp(whole)}
		case rt.path == "/api/config/import":
			op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": whole}}}
			op["parameters"] = []any{map[string]any{"name": "dryRun", "in": "query", "description": "1: only say what would change.", "schema": map[string]any{"type": "string"}}}
		}
		op["responses"].(map[string]any)["422"] = map[string]any{"description": "Problems with the request, by field", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Error"}}}}
		p := pathParam.ReplaceAllString(rt.path, "{$1}")
		if paths[p] == nil {
			paths[p] = map[string]any{}
		}
		paths[p][strings.ToLower(rt.method)] = op
	}
	defs["Error"] = map[string]any{"type": "object", "properties": map[string]any{
		"message":  map[string]any{"type": "string"},
		"problems": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"field": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}}}},
	}}
	writeJSON(w, 200, map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{"title": "BitVoice API", "version": s.Version, "license": map[string]any{"name": "Apache-2.0"},
			"description": "Everything the web interface does, it does through this API. Authenticate with an API token (Administrators, API tokens) as `Authorization: Bearer bv_...`, or a signed-in session plus the `X-BitVoice: 1` header on changes."},
		"servers":    []any{map[string]any{"url": "/"}},
		"security":   []any{map[string]any{"bearer": []any{}}},
		"paths":      paths,
		"components": map[string]any{"schemas": defs, "securitySchemes": map[string]any{"bearer": map[string]any{"type": "http", "scheme": "bearer"}}},
	})
}

func jsonResp(schema any) map[string]any {
	return map[string]any{"description": "OK", "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
}
