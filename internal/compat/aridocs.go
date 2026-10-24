package compat

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// aridocs.json describes the ARI's interface as ARI client libraries read it from /ari/api-docs
// (Swagger 1.2): each resource's operations with their nicknames and parameters, and the models'
// fields. It holds names and types only, so libraries such as ari-client can build their calls.
//
//go:embed aridocs.json
var ariDocsJSON []byte

var (
	ariDocsOnce sync.Once
	ariDocs     map[string]map[string]any
)

func loadARIDocs() map[string]map[string]any {
	ariDocsOnce.Do(func() { _ = json.Unmarshal(ariDocsJSON, &ariDocs) })
	return ariDocs
}

// apiDocs serves /ari/api-docs/resources.json and /ari/api-docs/{resource}.json, with the base path
// this request reached BitVoice by.
func (a *ARI) apiDocs(w http.ResponseWriter, r *http.Request, file string) {
	docs := loadARIDocs()
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	base := scheme + "://" + r.Host + "/ari"
	name := strings.TrimSuffix(file, ".json")
	if name == "resources" {
		var names []string
		for n := range docs {
			names = append(names, n)
		}
		sort.Strings(names)
		apis := []map[string]string{}
		for _, n := range names {
			apis = append(apis, map[string]string{"path": "/api-docs/" + n + ".{format}", "description": n})
		}
		ariJSON(w, map[string]any{"apiVersion": "2.0.0", "swaggerVersion": "1.2", "basePath": base, "apis": apis})
		return
	}
	d, ok := docs[name]
	if !ok {
		ariError(w, http.StatusNotFound, "no such resource")
		return
	}
	out := map[string]any{}
	for k, v := range d {
		out[k] = v
	}
	out["basePath"] = base
	ariJSON(w, out)
}
