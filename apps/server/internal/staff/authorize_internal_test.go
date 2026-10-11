package staff

import (
	"testing"
	"time"
)

// civilToday is the business zone's calendar day of an instant, as a date at
// midnight UTC — the shape a date parameter takes to SQL. Oslo is UTC+1 in
// winter and UTC+2 in summer, so the day turns an hour or two before UTC's,
// and on the night summer time ends the same Oslo day lasts twenty-five hours.
func TestCivilToday_IsTheZonesDay(t *testing.T) {
	t.Parallel()
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, time.June, 30, 21, 59, 0, 0, time.UTC), "2026-06-30"},
		{time.Date(2026, time.June, 30, 22, 0, 0, 0, time.UTC), "2026-07-01"},
		{time.Date(2026, time.January, 31, 22, 59, 0, 0, time.UTC), "2026-01-31"},
		{time.Date(2026, time.January, 31, 23, 0, 0, 0, time.UTC), "2026-02-01"},
		// Summer time ends at 01:00 UTC on 25 October 2026: 22:30 UTC on the
		// 24th is 00:30 on the 25th in Oslo, and 22:30 UTC on the 25th is
		// 23:30 on the 25th still.
		{time.Date(2026, time.October, 24, 22, 30, 0, 0, time.UTC), "2026-10-25"},
		{time.Date(2026, time.October, 25, 22, 30, 0, 0, time.UTC), "2026-10-25"},
	} {
		got := civilToday(c.at, oslo)
		if got.Format(time.DateOnly) != c.want || got.Location() != time.UTC || got.Hour() != 0 {
			t.Errorf("civilToday(%s) = %s, want %s at midnight UTC", c.at, got, c.want)
		}
	}
}
