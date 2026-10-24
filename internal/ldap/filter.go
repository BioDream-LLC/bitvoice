package ldap

import (
	"encoding/hex"
	"errors"
	"strings"
)

// Filters (RFC 4515) as text, turned into what goes on the wire (RFC 4511 section 4.5.1.7).

// EscapeFilter makes a value safe inside a filter: *, (, ), \ and NUL become \2a, \28, \29, \5c, \00.
func EscapeFilter(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		switch c := v[i]; c {
		case '*', '(', ')', '\\', 0:
			b.WriteString(`\` + hex.EncodeToString([]byte{c}))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// ErrFilter is a filter that is not RFC 4515 text.
var ErrFilter = errors.New("ldap: not a filter, such as (&(objectClass=user)(sAMAccountName=jo))")

// compileFilter turns a filter's text into its encoding.
func compileFilter(s string) (*element, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return prim(classContext|7, []byte("objectClass")), nil // (objectClass=*)
	}
	if s[0] != '(' {
		s = "(" + s + ")"
	}
	e, rest, err := parseFilter(s, 0)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(rest) != "" {
		return nil, ErrFilter
	}
	return e, nil
}

func parseFilter(s string, depth int) (*element, string, error) {
	if depth > 20 || len(s) < 3 || s[0] != '(' {
		return nil, "", ErrFilter
	}
	s = s[1:]
	switch s[0] {
	case '&', '|':
		tag := byte(classContext | constructed)
		if s[0] == '|' {
			tag |= 1
		}
		s = s[1:]
		var kids []*element
		for len(s) > 0 && s[0] == '(' {
			k, rest, err := parseFilter(s, depth+1)
			if err != nil {
				return nil, "", err
			}
			kids = append(kids, k)
			s = rest
		}
		if len(s) == 0 || s[0] != ')' || len(kids) == 0 {
			return nil, "", ErrFilter
		}
		return &element{tag: tag, children: kids}, s[1:], nil
	case '!':
		k, rest, err := parseFilter(s[1:], depth+1)
		if err != nil || len(rest) == 0 || rest[0] != ')' {
			return nil, "", ErrFilter
		}
		return &element{tag: classContext | constructed | 2, children: []*element{k}}, rest[1:], nil
	}
	end := strings.IndexByte(s, ')')
	if end < 0 {
		return nil, "", ErrFilter
	}
	item, rest := s[:end], s[end+1:]
	e, err := parseItem(item)
	return e, rest, err
}

func unescape(v string) (string, error) {
	if !strings.Contains(v, `\`) {
		return v, nil
	}
	var b []byte
	for i := 0; i < len(v); i++ {
		if v[i] != '\\' {
			b = append(b, v[i])
			continue
		}
		if i+3 > len(v) {
			return "", ErrFilter
		}
		x, err := hex.DecodeString(v[i+1 : i+3])
		if err != nil {
			return "", ErrFilter
		}
		b = append(b, x...)
		i += 2
	}
	return string(b), nil
}

// parseItem is one comparison: attr=value, attr=*, attr=a*b*c, attr>=v, attr<=v, attr~=v, or an
// extensible match attr:rule:=value (Active Directory's 1.2.840.113556.1.4.1941, say).
func parseItem(item string) (*element, error) {
	var op string
	i := strings.IndexAny(item, "=<>~")
	if i <= 0 {
		return nil, ErrFilter
	}
	attr := item[:i]
	switch {
	case strings.HasPrefix(item[i:], ">="):
		op = ">="
	case strings.HasPrefix(item[i:], "<="):
		op = "<="
	case strings.HasPrefix(item[i:], "~="):
		op = "~="
	case item[i] == '=':
		op = "="
	default:
		return nil, ErrFilter
	}
	value := item[i+len(op):]
	if strings.ContainsAny(value, "(") {
		return nil, ErrFilter // RFC 4515: ( in a value is written \28
	}
	if strings.Contains(attr, ":") { // extensible
		if op != "=" || !strings.HasSuffix(attr, ":") {
			return nil, ErrFilter
		}
		parts := strings.Split(strings.TrimSuffix(attr, ":"), ":")
		v, err := unescape(value)
		if err != nil {
			return nil, err
		}
		var kids []*element
		typ, dn, rule := parts[0], false, ""
		for _, p := range parts[1:] {
			if strings.EqualFold(p, "dn") {
				dn = true
			} else {
				rule = p
			}
		}
		if rule != "" {
			kids = append(kids, prim(classContext|1, []byte(rule)))
		}
		if typ != "" {
			kids = append(kids, prim(classContext|2, []byte(typ)))
		}
		kids = append(kids, prim(classContext|3, []byte(v)))
		if dn {
			kids = append(kids, prim(classContext|4, []byte{0xff}))
		}
		return &element{tag: classContext | constructed | 9, children: kids}, nil
	}
	if strings.ContainsAny(attr, " ()") {
		return nil, ErrFilter
	}
	switch op {
	case ">=", "<=", "~=":
		v, err := unescape(value)
		if err != nil {
			return nil, err
		}
		tag := map[string]byte{">=": 5, "<=": 6, "~=": 8}[op]
		return &element{tag: classContext | constructed | tag, children: []*element{octets(attr), octets(v)}}, nil
	}
	if value == "*" {
		return prim(classContext|7, []byte(attr)), nil
	}
	if strings.Contains(value, "*") {
		pieces := strings.Split(value, "*")
		var subs []*element
		for k, p := range pieces {
			if p == "" {
				continue
			}
			v, err := unescape(p)
			if err != nil {
				return nil, err
			}
			tag := byte(1) // any
			switch k {
			case 0:
				tag = 0 // initial
			case len(pieces) - 1:
				tag = 2 // final
			}
			subs = append(subs, prim(classContext|tag, []byte(v)))
		}
		return &element{tag: classContext | constructed | 4, children: []*element{octets(attr), sequence(subs...)}}, nil
	}
	v, err := unescape(value)
	if err != nil {
		return nil, err
	}
	return &element{tag: classContext | constructed | 3, children: []*element{octets(attr), octets(v)}}, nil
}

// CheckFilter says whether a filter's text is RFC 4515 text.
func CheckFilter(s string) error {
	_, err := compileFilter(s)
	return err
}
