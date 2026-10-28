package calendar

import (
	"testing"
	"time"
)

func TestDescriptionOf_CutsAtTheKeptLineInEitherLanguage(t *testing.T) {
	t.Parallel()
	for body, want := range map[string]string{
		"Agenda\r\n\r\nKept by Vantigo — change it there:\r\nhttps://app/x": "Agenda",
		"Agenda  \n\nHoldes av Vantigo — endre det der:\nhttps://app/x":     "Agenda",
		"  Line one \n line two\t\n":                                        "Line one\n line two",
		"Kept by Vantigo — change it there:\nhttps://app/x":                 "",
		"No kept line at all":                                               "No kept line at all",
		// The link on the kept line itself, a no-break space, an indent.
		"Agenda\nKept by Vantigo — change it there: https://app/x":             "Agenda",
		"Agenda\n\u00a0Kept by\u00a0Vantigo — change it there:\nhttps://app/x": "Agenda",
		"Agenda\n   Holdes av Vantigo — endre det der: https://app/x":          "Agenda",
		// A line that merely holds the words, not at its start, is kept.
		"See: Kept by Vantigo — change it there:": "See: Kept by Vantigo — change it there:",
	} {
		if got := descriptionOf(body); got != want {
			t.Errorf("descriptionOf(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestAppointmentKey_ReadsASingleAndAnOccurrence(t *testing.T) {
	t.Parallel()
	if id, date, ok := appointmentKey("appointment:42"); !ok || id != 42 || date != nil {
		t.Errorf("single: %d %v %v", id, date, ok)
	}
	if id, date, ok := appointmentKey("appointment:42:2026-09-22"); !ok || id != 42 || date == nil || !date.Equal(time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("occurrence: %d %v %v", id, date, ok)
	}
	for _, k := range []string{"staff.absence:7", "appointment:", "appointment:x", "appointment:-1", "appointment:4:not-a-date"} {
		if _, _, ok := appointmentKey(k); ok {
			t.Errorf("%q read as an appointment's key", k)
		}
	}
}
