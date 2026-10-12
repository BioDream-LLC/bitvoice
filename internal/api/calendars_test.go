package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/config"
)

func TestCalendars(t *testing.T) {
	s, ts := newServer(t)
	admin := newClient(t, ts.URL)
	if code, _ := admin.do("POST", "/api/setup/complete", setupBody()); code != 200 {
		t.Fatal("setup")
	}
	now := time.Now().UTC()
	z := func(t time.Time) string { return t.Format("20060102T150405Z") }
	feed := fmt.Sprintf("BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:a\r\nSUMMARY:Budget review\r\nDTSTART:%s\r\nDTEND:%s\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n", z(now.Add(-10*time.Minute)), z(now.Add(50*time.Minute)))
	cal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/broken.ics" {
			w.Write([]byte("<html>sign in</html>"))
			return
		}
		w.Write([]byte(feed))
	}))
	defer cal.Close()
	_, rev := s.Store.Config()
	if _, _, err := s.Store.Update("test", "cal", rev, func(c *config.Config) error {
		c.FindExtension("100").Calendar = config.ExtCalendar{Secret: cal.URL + "/me.ics"}
		c.FindExtension("101").Calendar = config.ExtCalendar{Secret: cal.URL + "/broken.ics"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.readCalendars(context.Background())
	_, out := admin.do("GET", "/api/away", nil)
	items := out["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["ext"] != "100" || items[0].(map[string]any)["reason"] != "Budget review" {
		t.Fatalf("away %v", out)
	}
	if p := out["problems"].(map[string]any)["101"]; p == nil || !strings.Contains(p.(string), "does not give a calendar") {
		t.Fatalf("problems %v", out["problems"])
	}
	// Meetings not counted: free again.
	_, rev = s.Store.Config()
	s.Store.Update("test", "cal", rev, func(c *config.Config) error { c.FindExtension("100").Calendar.IgnoreMeetings = true; return nil })
	s.readCalendars(context.Background())
	if len(s.Engine.Aways()) != 0 {
		t.Fatalf("still away %+v", s.Engine.Aways())
	}
	// Do not disturb by hand, and the check before saving.
	if code, _ := admin.do("PUT", "/api/away/101", map[string]any{"on": true}); code != 204 {
		t.Fatal("dnd")
	}
	if a := s.Engine.Aways(); len(a) != 1 || a[0].Reason != "Do not disturb" {
		t.Fatalf("dnd %+v", a)
	}
	code, chk := admin.do("POST", "/api/away/check", map[string]any{"secret": cal.URL + "/me.ics"})
	if code != 200 || chk["events"].(float64) != 1 {
		t.Fatalf("check %d %v", code, chk)
	}
	if code, _ := admin.do("POST", "/api/away/check", map[string]any{"secret": cal.URL + "/broken.ics"}); code != 422 {
		t.Fatal("bad calendar")
	}
	bad := config.Default()
	bad.Extensions = []config.Extension{{Number: "100", Name: "A", Secret: "s3cret-s3cret", Calendar: config.ExtCalendar{Secret: "ftp://x", WhenBusy: "forward"}}}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "calendar.secret") || !strings.Contains(err.Error(), "calendar.forwardTo") {
		t.Fatalf("validation %v", err)
	}
}
