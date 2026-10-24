package facts

import "strings"

// Summary writes the facts as short sentences, saying only what they say: "Fire at 418 maple
// avenue. Nobody hurt. Hazards: fire. Callback 540-555-0142." Empty when there are none.
func Summary(fs []Fact) string {
	by := map[string][]Fact{}
	for _, f := range fs {
		by[f.Kind] = append(by[f.Kind], f)
	}
	var out []string
	what := ""
	if in := by["incident"]; len(in) > 0 {
		what = upper(in[0].Value)
	}
	switch loc := by["location"]; {
	case what != "" && len(loc) > 0:
		out = append(out, what+" at "+title(loc[0].Value)+".")
	case what != "":
		out = append(out, what+".")
	case len(loc) > 0:
		out = append(out, "At "+title(loc[0].Value)+".")
	}
	if in := by["injured"]; len(in) > 0 {
		switch v := in[0].Value; v {
		case "none":
			out = append(out, "Nobody hurt.")
		case "1":
			out = append(out, "One person hurt.")
		default:
			out = append(out, v+" people hurt.")
		}
	}
	list := func(kind, label string) {
		var vs []string
		for _, f := range by[kind] {
			v := f.Value
			if f.Reported {
				v += " (reported)"
			}
			vs = append(vs, v)
		}
		if len(vs) > 0 {
			out = append(out, label+": "+strings.Join(vs, ", ")+".")
		}
	}
	list("condition", "Condition")
	list("hazard", "Hazards")
	list("vehicle", "Vehicles")
	if r := by["relation"]; len(r) > 0 {
		if r[0].Value == "self" {
			out = append(out, "The caller is the one in need.")
		} else {
			out = append(out, "Caller: "+r[0].Value+".")
		}
	}
	if c := by["callback"]; len(c) > 0 && len(c[0].Value) == 10 {
		v := c[0].Value
		out = append(out, "Callback "+v[:3]+"-"+v[3:6]+"-"+v[6:]+".")
	}
	return strings.Join(out, " ")
}

func upper(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// title capitalises a place's words ("418 maple avenue" -> "418 Maple Avenue").
func title(s string) string {
	w := strings.Fields(s)
	for i, x := range w {
		switch x {
		case "and", "at", "near", "by", "the", "on", "of":
			continue
		}
		w[i] = upper(x)
	}
	return strings.Join(w, " ")
}
