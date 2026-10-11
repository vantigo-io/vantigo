package staff_test

import (
	"net/http"
	"slices"
	"testing"
	"time"
)

// "Today" is the business time zone's calendar day (plan, the pre-flight's
// answer 3): computed once per request from the clock in Europe/Oslo and
// handed to SQL as a date, never the database's current_date. At 22:30 UTC
// on 24 October Oslo is already on the 25th (CEST, UTC+2): a person starting
// on the 25th is active and one who ended on the 24th has left, for the
// colleague's directory and for HR's status filter alike. After the clocks go
// back on 25 October Oslo is UTC+1, so 22:30 UTC is still the 25th there —
// a fixed offset would wrongly answer the 26th — and 23:30 UTC is the 26th.
func TestZone_TheListJudgedOnTheBusinessDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:manage")
	company(t, hr)
	starts25 := createPerson(t, hr, with(newPerson("Starts", "25th"), "employeeNumber", "Z-25"))
	createEmployment(t, hr, starts25.Id, newEmployment("2026-10-25", terms()))
	starts26 := createPerson(t, hr, with(newPerson("Starts", "26th"), "employeeNumber", "Z-26"))
	createEmployment(t, hr, starts26.Id, newEmployment("2026-10-26", terms()))
	ends24 := createPerson(t, hr, with(newPerson("Ends", "24th"), "employeeNumber", "Z-END"))
	e := createEmployment(t, hr, ends24.Id, newEmployment("2026-01-01", terms()))
	do(t, hr, http.MethodPost, endPath(e.Id), map[string]any{"endDate": "2026-10-24", "endReason": "employee_resigned"}, http.StatusOK, nil)

	for _, c := range []struct {
		at                        time.Time
		day                       string
		colleague, current, ended []string
	}{
		{time.Date(2026, 10, 24, 21, 30, 0, 0, time.UTC), "2026-10-24", []string{"Z-END"}, []string{"Z-25", "Z-26", "Z-END"}, nil},
		{time.Date(2026, 10, 24, 22, 30, 0, 0, time.UTC), "2026-10-25", []string{"Z-25"}, []string{"Z-25", "Z-26"}, []string{"Z-END"}},
		{time.Date(2026, 10, 25, 22, 30, 0, 0, time.UTC), "2026-10-25", []string{"Z-25"}, []string{"Z-25", "Z-26"}, []string{"Z-END"}},
		{time.Date(2026, 10, 25, 23, 30, 0, 0, time.UTC), "2026-10-26", []string{"Z-25", "Z-26"}, []string{"Z-25", "Z-26"}, []string{"Z-END"}},
	} {
		setClock(t, h, c.at)
		if got := today(t, h); got != c.day {
			t.Fatalf("at %s the harness's Oslo day is %s, want %s", c.at, got, c.day)
		}
		// Signed in afresh at each instant, so a session the clock has
		// carried past its lifetime is never the subject.
		manage, _ := signIn(t, h, "staff:manage")
		colleague, _ := signIn(t, h, "staff:view")
		if got := numbersListed(t, colleague, ""); !slices.Equal(got, c.colleague) {
			t.Errorf("at %s the colleague lists %v, want %v", c.at, got, c.colleague)
		}
		if got := numbersListed(t, manage, ""); !slices.Equal(got, c.current) {
			t.Errorf("at %s HR's current list is %v, want %v", c.at, got, c.current)
		}
		if got := numbersListed(t, manage, "?status=ended"); !slices.Equal(got, c.ended) {
			t.Errorf("at %s HR's ended list is %v, want %v", c.at, got, c.ended)
		}
	}
}
