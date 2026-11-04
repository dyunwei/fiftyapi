package server

import (
	"testing"
	"time"
)

func TestNextMorningUsesLocalCalendar(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ now, want string }{
		{"2026-11-03T06:59:59-05:00", "2026-11-03T07:00:00-05:00"},
		{"2026-11-03T07:00:00-05:00", "2026-11-04T07:00:00-05:00"},
		{"2026-10-31T08:00:00-04:00", "2026-11-01T07:00:00-05:00"},
	} {
		now, err := time.Parse(time.RFC3339, tc.now)
		if err != nil {
			t.Fatal(err)
		}
		if got := nextMorning(now.In(zone)).Format(time.RFC3339); got != tc.want {
			t.Errorf("got %s want %s", got, tc.want)
		}
	}
}
