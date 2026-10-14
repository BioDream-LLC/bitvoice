package config

import (
	"fmt"
	"reflect"
	"strings"
)

// Secrets and configuration kept in Git: an export for a repository leaves every secret out, and a
// configuration read back from one keeps the secrets already here wherever it has none.

// isSecretField says whether a field holds a secret, by its JSON name (as Diff masks them).
func isSecretField(owner reflect.Type, f reflect.StructField) bool {
	name := strings.Split(f.Tag.Get("json"), ",")[0]
	if owner == reflect.TypeOf(MenuOption{}) && name == "key" {
		return false // the key callers press
	}
	return f.Type.Kind() == reflect.String && secretKey.MatchString(name)
}

// StripSecrets blanks every secret in a configuration.
func StripSecrets(c *Config) { walkSecrets(reflect.ValueOf(c).Elem(), reflect.Value{}, false) }

// KeepSecrets fills every secret that c leaves empty from cur, matching list items by id, number, mac
// or key as Diff does.
func KeepSecrets(c *Config, cur Config) {
	walkSecrets(reflect.ValueOf(c).Elem(), reflect.ValueOf(&cur).Elem(), true)
}

func walkSecrets(v, from reflect.Value, keep bool) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		var f reflect.Value
		if from.IsValid() && !from.IsNil() {
			f = from.Elem()
		}
		walkSecrets(v.Elem(), f, keep)
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			if !sf.IsExported() {
				continue
			}
			fv := v.Field(i)
			var ff reflect.Value
			if from.IsValid() {
				ff = from.Field(i)
			}
			if sf.Type.Kind() == reflect.Slice && sf.Type.Elem().Kind() == reflect.String && secretKey.MatchString(strings.TrimSuffix(strings.Split(sf.Tag.Get("json"), ",")[0], "s")) {
				// A list of secrets (a PIN set's PINs).
				switch {
				case !keep:
					fv.Set(reflect.Zero(fv.Type()))
				case fv.Len() == 0 && ff.IsValid():
					fv.Set(ff)
				}
				continue
			}
			if isSecretField(t, sf) {
				switch {
				case !keep:
					fv.SetString("")
				case fv.String() == "" && ff.IsValid():
					fv.SetString(ff.String())
				}
				continue
			}
			walkSecrets(fv, ff, keep)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			var match reflect.Value
			if from.IsValid() {
				k := itemKey(v.Index(i))
				for j := 0; j < from.Len() && k != ""; j++ {
					if itemKey(from.Index(j)) == k {
						match = from.Index(j)
						break
					}
				}
			}
			walkSecrets(v.Index(i), match, keep)
		}
	}
}

// itemKey is what identifies a list item: its id, number, mac or key field.
func itemKey(v reflect.Value) string {
	if v.Kind() != reflect.Struct {
		return ""
	}
	for _, name := range []string{"ID", "Number", "MAC", "Key", "Username"} {
		if f := v.FieldByName(name); f.IsValid() && f.Kind() == reflect.String && f.String() != "" {
			return name + "=" + f.String()
		}
	}
	return ""
}

// ResolveSecrets replaces every secret that is a vault reference with the value resolve gives, and
// returns the fields it could not read (they are left empty, so nothing uses a reference as a password).
func ResolveSecrets(c *Config, isRef func(string) bool, resolve func(string) (string, error)) map[string]error {
	failed := map[string]error{}
	var walk func(v reflect.Value, path string)
	walk = func(v reflect.Value, path string) {
		switch v.Kind() {
		case reflect.Pointer:
			if !v.IsNil() {
				walk(v.Elem(), path)
			}
		case reflect.Struct:
			t := v.Type()
			for i := 0; i < t.NumField(); i++ {
				sf := t.Field(i)
				if !sf.IsExported() {
					continue
				}
				name := strings.Split(sf.Tag.Get("json"), ",")[0]
				p := strings.TrimPrefix(path+"."+name, ".")
				fv := v.Field(i)
				if isSecretField(t, sf) {
					if s := fv.String(); isRef(s) {
						val, err := resolve(s)
						if err != nil {
							failed[p] = err
							val = ""
						}
						fv.SetString(val)
					}
					continue
				}
				walk(fv, p)
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(reflect.ValueOf(c).Elem(), "")
	return failed
}
