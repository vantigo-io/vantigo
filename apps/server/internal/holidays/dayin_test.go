package holidays

import (
	"testing"
	"time"
)

func TestDayIn_IsTheZonesDayAtUTCMidnight(t *testing.T) {
	t.Parallel()
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatal(err)
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		at   time.Time
		loc  *time.Location
		want time.Time
	}{
		{"an evening UTC instant is already the next Oslo day in summer", time.Date(2026, time.June, 30, 22, 30, 0, 0, time.UTC), oslo, time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)},
		{"and in winter, one hour later", time.Date(2026, time.January, 31, 23, 30, 0, 0, time.UTC), oslo, time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC)},
		{"the same instant is still the previous day in New York", time.Date(2026, time.July, 1, 2, 0, 0, 0, time.UTC), newYork, time.Date(2026, time.June, 30, 0, 0, 0, 0, time.UTC)},
		{"a UTC midnight in UTC is itself", time.Date(2026, time.March, 29, 0, 0, 0, 0, time.UTC), time.UTC, time.Date(2026, time.March, 29, 0, 0, 0, 0, time.UTC)},
		{"an instant given in another zone is read as the instant it is", time.Date(2026, time.March, 29, 0, 30, 0, 0, newYork), oslo, time.Date(2026, time.March, 29, 0, 0, 0, 0, time.UTC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DayIn(tc.at, tc.loc)
			if !got.Equal(tc.want) || got.Location() != time.UTC || got.Hour() != 0 {
				t.Errorf("DayIn(%v, %v) = %v, want %v at UTC midnight", tc.at, tc.loc, got, tc.want)
			}
		})
	}
}
