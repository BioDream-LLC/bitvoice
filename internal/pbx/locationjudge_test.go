package pbx

import (
	"testing"
	"time"

	"github.com/biodream-llc/bitvoice/internal/i3"
)

func pt(lat, lon, r float64, method string, at time.Time) *i3.Location {
	return &i3.Location{Lat: &lat, Lon: &lon, Radius: r, Method: method, Time: at}
}

func TestLocationCheck(t *testing.T) {
	now := time.Now()
	gps := pt(40.7128, -74.0060, 10, "GPS", now.Add(-30*time.Second))
	// A rough cell fix after a fresh precise GPS fix nearby: keep showing the GPS fix first.
	if c := judgeLocations(gps, pt(40.7130, -74.0062, 1500, "Cell", now), now); c.Show != "old" {
		t.Fatalf("%+v", c)
	}
	// The GPS fix is old: the caller may have moved.
	old := pt(40.7128, -74.0060, 10, "GPS", now.Add(-10*time.Minute))
	if c := judgeLocations(old, pt(40.7130, -74.0062, 1500, "Cell", now), now); c.Show != "new" {
		t.Fatalf("%+v", c)
	}
	// Two precise fixes 3 km apart: they disagree, show both.
	if c := judgeLocations(gps, pt(40.74, -74.0, 10, "GPS", now), now); c.Show != "both" || len(c.Reasons) == 0 {
		t.Fatalf("%+v", c)
	}
}
