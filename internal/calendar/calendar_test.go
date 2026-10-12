package calendar

import (
	"testing"
	"time"
)

const feed = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" +
	// A weekly stand-up, Mon/Wed 09:00-09:30 New York, with one Wednesday skipped and one Monday moved.
	"BEGIN:VEVENT\r\nUID:standup\r\nSUMMARY:Stand-up\r\nDTSTART;TZID=Eastern Standard Time:20261005T090000\r\nDTEND;TZID=Eastern Standard Time:20261005T093000\r\n" +
	"RRULE:FREQ=WEEKLY;BYDAY=MO,WE;UNTIL=20261231T000000Z\r\nEXDATE;TZID=Eastern Standard Time:20261014T090000\r\n" +
	"BEGIN:VALARM\r\nTRIGGER:-PT15M\r\nEND:VALARM\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:standup\r\nRECURRENCE-ID;TZID=Eastern Standard Time:20261019T090000\r\nSUMMARY:Stand-up (moved)\r\n" +
	"DTSTART;TZID=Eastern Standard Time:20261019T110000\r\nDTEND;TZID=Eastern Standard Time:20261019T113000\r\nEND:VEVENT\r\n" +
	// A day off, a free lunch, a cancelled call and an hour-long call written with DURATION, folded.
	"BEGIN:VEVENT\r\nUID:off\r\nSUMMARY:Holiday\r\nDTSTART;VALUE=DATE:20261016\r\nDTEND;VALUE=DATE:20261017\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:lunch\r\nSUMMARY:Lunch\r\nTRANSP:TRANSPARENT\r\nDTSTART:20261015T160000Z\r\nDTEND:20261015T170000Z\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:x\r\nSTATUS:CANCELLED\r\nSUMMARY:Gone\r\nDTSTART:20261015T180000Z\r\nDTEND:20261015T190000Z\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:review\r\nSUMMARY:Quarterly re\r\n view\r\nDTSTART:20261015T200000Z\r\nDURATION:PT1H\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestParse(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	from := time.Date(2026, 10, 12, 0, 0, 0, 0, ny)
	bs := Parse(feed, from, from.AddDate(0, 0, 14), ny)
	at := func(m time.Month, d, h, min int) time.Time { return time.Date(2026, m, d, h, min, 0, 0, ny) }
	check := func(t0 time.Time, want string, away bool) {
		t.Helper()
		b, ok := Now(bs, t0)
		if want == "" {
			if ok {
				t.Errorf("%v: busy with %q, want free", t0, b.Summary)
			}
			return
		}
		if !ok || b.Summary != want || b.Away != away {
			t.Errorf("%v: %q %v (found %v), want %q", t0, b.Summary, b.Away, ok, want)
		}
	}
	check(at(10, 12, 9, 10), "Stand-up", false) // Monday
	check(at(10, 13, 9, 10), "", false)         // Tuesday: not a stand-up day
	check(at(10, 14, 9, 10), "", false)         // the skipped Wednesday
	check(at(10, 21, 9, 29), "Stand-up", false) // a later Wednesday
	check(at(10, 19, 9, 10), "", false)         // moved away from 9 ...
	check(at(10, 19, 11, 10), "Stand-up (moved)", false)
	check(at(10, 16, 14, 0), "Holiday", true)
	check(at(10, 15, 12, 30), "", false) // free lunch (16:30 UTC)
	check(at(10, 15, 14, 30), "", false) // cancelled
	check(at(10, 15, 16, 30), "Quarterly review", false)
	check(at(10, 15, 17, 1), "", false)
}

func TestDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{"PT1H30M": 90 * time.Minute, "P1D": 24 * time.Hour, "P1W": 7 * 24 * time.Hour, "PT45S": 45 * time.Second, "bad": 0} {
		if got := duration(in); got != want {
			t.Errorf("%s: %v want %v", in, got, want)
		}
	}
}

// Google Calendar's own format (all-day dates, CRLF): an event shown as free (TRANSP:TRANSPARENT,
// as public holiday calendars mark every day) never makes anyone away; a busy one does.
func TestGoogleFormat(t *testing.T) {
	feed := "BEGIN:VCALENDAR\r\nPRODID:-//Google Inc//Google Calendar 70.9054//EN\r\nVERSION:2.0\r\nCALSCALE:GREGORIAN\r\n" +
		"METHOD:PUBLISH\r\nX-WR-TIMEZONE:UTC\r\n" +
		"BEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20260704\r\nDTEND;VALUE=DATE:20260705\r\nUID:a@google.com\r\nCLASS:PUBLIC\r\n" +
		"STATUS:CONFIRMED\r\nSUMMARY:Independence Day\r\nTRANSP:TRANSPARENT\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nDTSTART;VALUE=DATE:20260706\r\nDTEND;VALUE=DATE:20260708\r\nUID:b@google.com\r\n" +
		"STATUS:CONFIRMED\r\nSUMMARY:Holiday\r\nTRANSP:OPAQUE\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	busy := Parse(feed, from, from.AddDate(0, 1, 0), time.UTC)
	if len(busy) != 1 || busy[0].Summary != "Holiday" || !busy[0].Away {
		t.Fatalf("got %+v", busy)
	}
	if _, ok := Now(busy, time.Date(2026, 7, 7, 15, 0, 0, 0, time.UTC)); !ok {
		t.Fatal("not away on the 7th")
	}
	if _, ok := Now(busy, time.Date(2026, 7, 4, 15, 0, 0, 0, time.UTC)); ok {
		t.Fatal("away on a day shown as free")
	}
}
