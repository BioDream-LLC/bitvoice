package config

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// guiOnlyByOtherName lists settings the web interface sets under a name of its own, or that are
// not settings at all (kept by BitVoice itself). Each entry says why; anything else in the
// configuration must be named somewhere in the web interface, or it could only be set by a file or
// the API, which rule 1 forbids.
var guiOnlyByOtherName = map[string]string{
	"Config.schema":   "the configuration format's version, kept by BitVoice itself",
	"Destination.via": "set only inside the engine, for a forwarding hop; never saved from a screen",
}

// The first rule of the project: everything is configured in the web interface. Every field of the
// configuration must be named in the web interface's code, so a setting with no screen fails here.
func TestEverySettingHasAScreen(t *testing.T) {
	src := filepath.Join("..", "..", "web", "src")
	var code strings.Builder
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasSuffix(p, ".test.ts") || p == filepath.Join(src, "manual.ts") || p == filepath.Join(src, "figures.ts") {
			return nil
		}
		if strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".tsx") {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			code.Write(b)
		}
		return nil
	})
	if err != nil {
		t.Skip("no web sources here:", err)
	}
	all := code.String()
	seen := map[reflect.Type]bool{}
	missing := map[string][]string{}
	var walk func(rt reflect.Type, where string)
	walk = func(rt reflect.Type, where string) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Map || rt.Kind() == reflect.Array {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] || rt.PkgPath() != reflect.TypeOf(Config{}).PkgPath() {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" || !f.IsExported() {
				continue
			}
			if name == "" {
				walk(f.Type, where) // embedded
				continue
			}
			key := rt.Name() + "." + name
			if _, ok := guiOnlyByOtherName[key]; !ok && !regexp.MustCompile(`\b`+regexp.QuoteMeta(name)+`\b`).MatchString(all) {
				missing[rt.Name()] = append(missing[rt.Name()], name)
			}
			walk(f.Type, where+"."+name)
		}
	}
	walk(reflect.TypeOf(Config{}), "")
	var lines []string
	for k, v := range missing {
		lines = append(lines, k+": "+strings.Join(v, ", "))
	}
	sort.Strings(lines)
	if len(lines) > 0 {
		t.Errorf("settings no screen names (give each a screen, or say in guiOnlyByOtherName why not):\n%s", strings.Join(lines, "\n"))
	}
}
