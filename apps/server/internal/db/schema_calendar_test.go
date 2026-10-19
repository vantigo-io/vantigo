package db_test

import (
	"context"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// calendarBaselineVersion is 00055_calendar_baseline.sql, the calendar
// module's schema (docs/superpowers/specs/2026-10-19-calendar-design.md, D2).
const calendarBaselineVersion = 55

// TestCalendarBaseline_AppliesAndIsIdempotent proves the calendar baseline
// applies, rolls back and re-applies cleanly, and pins what the module's
// queries rest on: the three tables, the unique indexes its writes key on
// (one ical_uid per appointment, one exception per occurrence day — the one
// attendee row per user is 00056's, pinned by
// TestCalendarInvitations_AppliesAndIsIdempotent), the two indexes its window
// reads go through, and the cascades that make deleting an appointment take
// its attendees and exceptions with it.
func TestCalendarBaseline_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, calendarBaselineVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	tables := staffCollect(t, ctx, pool, "calendar tables", `
		SELECT table_name FROM information_schema.tables WHERE table_schema = 'calendar' ORDER BY table_name`)
	if want := []string{"appointment_attendees", "appointment_exceptions", "appointments"}; !equalStrings(tables, want) {
		t.Errorf("calendar tables = %v, want %v", tables, want)
	}

	for _, want := range []struct {
		name    string
		columns []string
	}{
		{"ux_appointments_ical_uid", []string{"ical_uid"}},
		{"ix_appointments_owner_window", []string{"owner_user_id", "starts_at", "start_date"}},
		{"ix_appointment_attendees_user", []string{"user_id"}},
		{"ux_appointment_exceptions", []string{"appointment_id", "occurrence_date"}},
	} {
		if cols := indexColumns(t, ctx, pool, "calendar", want.name); !equalStrings(cols, want.columns) {
			t.Errorf("%s columns = %v, want %v", want.name, cols, want.columns)
		}
	}

	cascades := staffCollect(t, ctx, pool, "calendar foreign keys", `
		SELECT conrelid::regclass::text || ':' || confdeltype::text FROM pg_constraint
		WHERE contype = 'f' AND connamespace = 'calendar'::regnamespace ORDER BY 1`)
	if want := []string{"calendar.appointment_attendees:c", "calendar.appointment_exceptions:c"}; !equalStrings(cascades, want) {
		t.Errorf("calendar foreign keys = %v, want both child tables ON DELETE CASCADE", cascades)
	}
}
