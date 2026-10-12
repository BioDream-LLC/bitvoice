// Package calendar reads iCalendar (RFC 5545) feeds, the private addresses Outlook and Google
// Calendar give each calendar, to say whether someone is busy now: in a meeting, or away.
package calendar

import (
	"bufio"
	"strconv"
	"strings"
	"time"
)

// Busy is a stretch of time someone is not free.
type Busy struct {
	Start, End time.Time
	Summary    string
	// Away is true for time off (all-day events, or Outlook's out of office), false for meetings.
	Away bool
}

type event struct {
	uid, summary         string
	start, end           time.Time
	allDay, free, cancel bool
	oof                  bool
	dur                  time.Duration
	rrule                map[string]string
	exdates              map[int64]bool
	recurrenceID         time.Time
}

// Parse reads a feed and returns every busy stretch that overlaps [from, to), recurring events expanded.
func Parse(ics string, from, to time.Time, local *time.Location) []Busy {
	evs := parse(ics, local)
	moved := map[string]map[int64]bool{} // uid -> original starts replaced by an override
	for _, e := range evs {
		if !e.recurrenceID.IsZero() {
			if moved[e.uid] == nil {
				moved[e.uid] = map[int64]bool{}
			}
			moved[e.uid][e.recurrenceID.Unix()] = true
		}
	}
	var out []Busy
	add := func(e event, s time.Time) {
		end := s.Add(e.dur)
		if end.After(from) && s.Before(to) {
			out = append(out, Busy{Start: s, End: end, Summary: e.summary, Away: e.allDay || e.oof})
		}
	}
	for _, e := range evs {
		if e.cancel || e.free || e.start.IsZero() {
			continue
		}
		if e.dur <= 0 {
			e.dur = e.end.Sub(e.start)
			if e.dur <= 0 {
				if e.allDay {
					e.dur = 24 * time.Hour
				} else {
					continue
				}
			}
		}
		if e.rrule == nil || !e.recurrenceID.IsZero() {
			add(e, e.start)
			continue
		}
		for _, s := range expand(e, to) {
			if e.exdates[s.Unix()] || moved[e.uid][s.Unix()] {
				continue
			}
			add(e, s)
		}
	}
	return out
}

// Now says what someone is doing at t: the busy stretch that holds it (time off first), if any.
func Now(bs []Busy, t time.Time) (Busy, bool) {
	var best Busy
	found := false
	for _, b := range bs {
		if !t.Before(b.Start) && t.Before(b.End) && (!found || b.Away && !best.Away || b.End.After(best.End) && b.Away == best.Away) {
			best, found = b, true
		}
	}
	return best, found
}

func parse(ics string, local *time.Location) []event {
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(ics))
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(lines) > 0 {
			lines[len(lines)-1] += l[1:] // folded line
			continue
		}
		lines = append(lines, l)
	}
	var out []event
	var cur *event
	depth := 0 // inside a VALARM or other component within the event
	for _, l := range lines {
		name, params, value := split(l)
		switch {
		case name == "BEGIN" && value == "VEVENT":
			cur = &event{exdates: map[int64]bool{}}
			depth = 0
			continue
		case name == "END" && value == "VEVENT" && cur != nil:
			out = append(out, *cur)
			cur = nil
			continue
		case cur == nil:
			continue
		case name == "BEGIN":
			depth++
			continue
		case name == "END":
			depth--
			continue
		}
		if depth > 0 {
			continue
		}
		switch name {
		case "UID":
			cur.uid = value
		case "SUMMARY":
			cur.summary = unescape(value)
		case "DTSTART":
			cur.start, cur.allDay = when(value, params, local)
		case "DTEND":
			cur.end, _ = when(value, params, local)
		case "DURATION":
			cur.dur = duration(value)
		case "TRANSP":
			cur.free = strings.EqualFold(value, "TRANSPARENT")
		case "STATUS":
			cur.cancel = strings.EqualFold(value, "CANCELLED")
		case "X-MICROSOFT-CDO-BUSYSTATUS":
			switch strings.ToUpper(value) {
			case "FREE":
				cur.free = true
			case "OOF":
				cur.oof = true
			}
		case "RRULE":
			cur.rrule = map[string]string{}
			for _, kv := range strings.Split(value, ";") {
				k, v, _ := strings.Cut(kv, "=")
				cur.rrule[strings.ToUpper(k)] = strings.ToUpper(v)
			}
		case "EXDATE":
			for _, v := range strings.Split(value, ",") {
				if t, _ := when(v, params, local); !t.IsZero() {
					cur.exdates[t.Unix()] = true
				}
			}
		case "RECURRENCE-ID":
			cur.recurrenceID, _ = when(value, params, local)
		}
	}
	return out
}

// split cuts "DTSTART;TZID=Europe/London:20261012T090000" into its name, parameters and value.
func split(l string) (string, map[string]string, string) {
	head, value, _ := strings.Cut(l, ":")
	parts := strings.Split(head, ";")
	params := map[string]string{}
	for _, p := range parts[1:] {
		k, v, _ := strings.Cut(p, "=")
		params[strings.ToUpper(k)] = strings.Trim(v, `"`)
	}
	return strings.ToUpper(parts[0]), params, value
}

func when(v string, params map[string]string, local *time.Location) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if params["VALUE"] == "DATE" || len(v) == 8 {
		t, err := time.ParseInLocation("20060102", v, local)
		if err != nil {
			return time.Time{}, false
		}
		return t, true
	}
	if strings.HasSuffix(v, "Z") {
		t, _ := time.Parse("20060102T150405Z", v)
		return t, false
	}
	loc := local
	if tz := params["TZID"]; tz != "" {
		if l, err := time.LoadLocation(windowsZone(tz)); err == nil {
			loc = l
		}
	}
	t, _ := time.ParseInLocation("20060102T150405", v, loc)
	return t, false
}

// windowsZone turns the Windows zone names Outlook writes into IANA names (the common ones).
func windowsZone(tz string) string {
	if z, ok := map[string]string{
		"Pacific Standard Time": "America/Los_Angeles", "Mountain Standard Time": "America/Denver",
		"Central Standard Time": "America/Chicago", "Eastern Standard Time": "America/New_York",
		"US Mountain Standard Time": "America/Phoenix", "Alaskan Standard Time": "America/Anchorage",
		"Hawaiian Standard Time": "Pacific/Honolulu", "Atlantic Standard Time": "America/Halifax",
		"GMT Standard Time": "Europe/London", "W. Europe Standard Time": "Europe/Berlin",
		"Romance Standard Time": "Europe/Paris", "Central Europe Standard Time": "Europe/Budapest",
		"E. Europe Standard Time": "Europe/Chisinau", "FLE Standard Time": "Europe/Kiev",
		"India Standard Time": "Asia/Kolkata", "China Standard Time": "Asia/Shanghai",
		"Tokyo Standard Time": "Asia/Tokyo", "AUS Eastern Standard Time": "Australia/Sydney",
		"UTC": "UTC", "Greenwich Standard Time": "Atlantic/Reykjavik",
	}[tz]; ok {
		return z
	}
	return tz
}

// duration reads a DURATION such as PT1H30M or P1D.
func duration(v string) time.Duration {
	v = strings.TrimPrefix(strings.ToUpper(v), "+")
	if !strings.HasPrefix(v, "P") {
		return 0
	}
	var d time.Duration
	num := ""
	inTime := false
	for _, c := range v[1:] {
		switch {
		case c == 'T':
			inTime = true
		case c >= '0' && c <= '9':
			num += string(c)
		default:
			n, _ := strconv.Atoi(num)
			num = ""
			switch {
			case c == 'W':
				d += time.Duration(n) * 7 * 24 * time.Hour
			case c == 'D':
				d += time.Duration(n) * 24 * time.Hour
			case c == 'H' && inTime:
				d += time.Duration(n) * time.Hour
			case c == 'M' && inTime:
				d += time.Duration(n) * time.Minute
			case c == 'S' && inTime:
				d += time.Duration(n) * time.Second
			}
		}
	}
	return d
}

var weekdays = map[string]time.Weekday{"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday, "TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday}

// expand lists a recurring event's starts up to a limit: FREQ DAILY, WEEKLY (with BYDAY), MONTHLY
// (same day of the month) and YEARLY, with INTERVAL, COUNT and UNTIL.
func expand(e event, limit time.Time) []time.Time {
	r := e.rrule
	interval, _ := strconv.Atoi(r["INTERVAL"])
	if interval < 1 {
		interval = 1
	}
	count, _ := strconv.Atoi(r["COUNT"])
	var until time.Time
	if u := r["UNTIL"]; u != "" {
		until, _ = when(u, nil, e.start.Location())
		if len(u) == 8 {
			until = until.Add(24*time.Hour - time.Second)
		}
	}
	var days []time.Weekday
	for _, d := range strings.Split(r["BYDAY"], ",") {
		if w, ok := weekdays[strings.TrimLeft(d, "+-0123456789")]; ok {
			days = append(days, w)
		}
	}
	var out []time.Time
	emit := func(t time.Time) bool { // false when the series has ended
		if t.Before(e.start) {
			return true
		}
		if !until.IsZero() && t.After(until) || count > 0 && len(out) >= count || t.After(limit) || len(out) > 5000 {
			return false
		}
		out = append(out, t)
		return true
	}
	y, mo, d := e.start.Date()
	h, mi, s := e.start.Clock()
	loc := e.start.Location()
	at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, h, mi, s, 0, loc) }
	switch r["FREQ"] {
	case "DAILY":
		for i := 0; ; i += interval {
			if !emit(at(y, mo, d+i)) {
				return out
			}
		}
	case "WEEKLY":
		if len(days) == 0 {
			days = []time.Weekday{e.start.Weekday()}
		}
		weekStart := at(y, mo, d-int(e.start.Weekday()))
		for w := 0; ; w += interval {
			for wd := time.Sunday; wd <= time.Saturday; wd++ {
				for _, want := range days {
					if wd == want {
						t := weekStart.AddDate(0, 0, w*7+int(wd))
						if !emit(t) {
							return out
						}
					}
				}
			}
		}
	case "MONTHLY":
		for i := 0; ; i += interval {
			t := at(y, mo+time.Month(i), d)
			if t.Day() != d {
				continue // no such day that month
			}
			if !emit(t) {
				return out
			}
		}
	case "YEARLY":
		for i := 0; ; i += interval {
			if !emit(at(y+i, mo, d)) {
				return out
			}
		}
	}
	return []time.Time{e.start}
}

func unescape(s string) string {
	return strings.NewReplacer(`\n`, " ", `\N`, " ", `\,`, ",", `\;`, ";", `\\`, `\`).Replace(s)
}
