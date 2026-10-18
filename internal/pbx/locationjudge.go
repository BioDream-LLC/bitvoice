package pbx

import (
	"math"
	"strings"
	"time"

	"github.com/biodream-llc/bitvoice/internal/i3"
	"github.com/biodream-llc/bitvoice/internal/reason"
)

// LocationCheck is what the caller location check decided when an emergency caller's location changed: which to
// show first, and why. Both locations stay on the call.
type LocationCheck struct {
	Show     string       `json:"show"` // new, old or both
	Text     string       `json:"text"`
	Reasons  []string     `json:"reasons"`
	Previous *i3.Location `json:"previous,omitempty"`
}

func measured(l *i3.Location) bool {
	switch strings.ToLower(l.Method) {
	case "gps", "a-gps", "gnss", "wifi", "hybrid", "device", "dbh":
		return true
	}
	return false
}

// metres between two points (haversine).
func metres(a, b *i3.Location) float64 {
	const r = 6371000.0
	la1, la2 := *a.Lat*math.Pi/180, *b.Lat*math.Pi/180
	dla, dlo := la2-la1, (*b.Lon-*a.Lon)*math.Pi/180
	h := math.Sin(dla/2)*math.Sin(dla/2) + math.Cos(la1)*math.Cos(la2)*math.Sin(dlo/2)*math.Sin(dlo/2)
	return 2 * r * math.Asin(math.Sqrt(h))
}

// judgeLocations puts an old and a new location to the location rulebook.
func judgeLocations(old, now *i3.Location, at time.Time) LocationCheck {
	f := map[string]reason.Truth{
		"new_measured": truth(measured(now)),
		"old_measured": truth(measured(old)),
		"new_precise":  truth(now.HasPoint() && now.Radius > 0 && now.Radius <= 100),
		"old_precise":  truth(old.HasPoint() && old.Radius > 0 && old.Radius <= 100),
	}
	if !old.Time.IsZero() {
		f["old_stale"] = truth(at.Sub(old.Time) > 2*time.Minute)
	} // unknown when the first location carried no time
	if old.HasPoint() && now.HasPoint() {
		f["far_apart"] = truth(metres(old, now) > old.Radius+now.Radius+50)
	} else {
		f["far_apart"] = reason.No // a civic address against a point is not compared
	}
	v := reason.Location.Decide(f)
	return LocationCheck{Show: v.Action, Text: v.Text, Reasons: v.Reasons}
}
