package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Change is one setting that differs between two configurations. Secrets show as "(changed)".
type Change struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
}

var secretKey = regexp.MustCompile(`(?i)(secret|password|pin|key|sas)$`)

// Diff lists what differs from a to b, at most limit changes. List items are named by their id or number
// (extensions[100].name) so a change reads the same wherever the item sits in the list.
func Diff(a, b Config, limit int) []Change {
	fa, fb := map[string]string{}, map[string]string{}
	flatten("", toAny(a), fa)
	flatten("", toAny(b), fb)
	keys := map[string]bool{}
	for k := range fa {
		keys[k] = true
	}
	for k := range fb {
		keys[k] = true
	}
	// An item added or removed whole is one change, not one per field.
	gone := func(k string) bool {
		for i := strings.Index(k, "]"); i >= 0; i = nextClose(k, i) {
			root := k[:i+1]
			if root == k {
				return false
			}
			_, inA := fa[root]
			_, inB := fb[root]
			if inA != inB {
				return true
			}
		}
		return false
	}
	var out []Change
	for k := range keys {
		if fa[k] == fb[k] || gone(k) {
			continue
		}
		c := Change{Path: k, Before: fa[k], After: fb[k]}
		if secretPath(k) {
			c.Before, c.After = mask(c.Before), mask(c.After)
			if c.Before != "" && c.After != "" {
				c.After = "(changed)"
			}
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(x, y Change) int { return strings.Compare(x.Path, y.Path) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func nextClose(s string, after int) int {
	j := strings.Index(s[after+1:], "]")
	if j < 0 {
		return -1
	}
	return after + 1 + j
}

func mask(s string) string {
	if s == "" {
		return ""
	}
	return "(set)"
}

func toAny(c Config) any {
	b, _ := json.Marshal(c)
	var v any
	_ = json.Unmarshal(b, &v)
	return v
}

func flatten(prefix string, v any, out map[string]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			flatten(p, val, out)
		}
	case []any:
		if len(x) == 0 {
			out[prefix] = "" // an empty list and a missing one are the same setting
			return
		}
		for i, val := range x {
			key := fmt.Sprint(i)
			if m, ok := val.(map[string]any); ok {
				for _, f := range []string{"number", "id", "mac", "key", "name"} {
					if s, ok := m[f].(string); ok && s != "" {
						key = s
						break
					}
				}
				// The item itself, labelled, so adding or removing it reads as one line.
				label := key
				for _, f := range []string{"mac", "number"} {
					if s, ok := m[f].(string); ok && s != "" {
						label = s
					}
				}
				for _, f := range []string{"label", "name"} {
					if s, ok := m[f].(string); ok && s != "" {
						label = s
					}
				}
				out[prefix+"["+key+"]"] = label
				flatten(prefix+"["+key+"]", val, out)
				continue
			}
			// A list of plain values (patterns, members) reads best whole.
			b, _ := json.Marshal(x)
			out[prefix] = strings.Trim(string(b), "[]")
			return
		}
	case nil:
		out[prefix] = ""
	case string:
		out[prefix] = x
	default:
		b, _ := json.Marshal(x)
		out[prefix] = string(b)
	}
}

// secretPath says whether a flattened path names a secret; a menu option's key is the keypress, not one.
func secretPath(k string) bool {
	last := k[strings.LastIndex(k, ".")+1:]
	if last == "key" && strings.Contains(k, ".options[") {
		return false
	}
	return secretKey.MatchString(last)
}
