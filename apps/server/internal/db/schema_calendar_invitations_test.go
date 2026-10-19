package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// calendarInvitationsVersion is 00056_calendar_invitations.sql, the calendar's
// attendee kinds and cancellations
// (docs/superpowers/specs/2026-10-19-calendar-invitations-design.md, D1, D6).
const calendarInvitationsVersion = 56

// TestCalendarInvitations_AppliesAndIsIdempotent proves the invitations
// migration applies, rolls back and re-applies cleanly, and pins what the
// module's attendee queries rest on: user_id nullable, the kind defaulting to
// user so A's rows are colleagues, the columns of the outside kinds and of an
// invitation's record, the four partial unique indexes that replace A's one
// (one row per user, per contact and per address on an appointment, one
// attendee per RSVP token), and the appointment's cancelled_at.
func TestCalendarInvitations_AppliesAndIsIdempotent(t *testing.T) {
	url := testdb.URL(t)
	applyUpDownUp(t, url, calendarInvitationsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	column := func(table, name string) []string {
		return staffCollect(t, ctx, pool, "calendar."+table+"."+name, `
			SELECT data_type || ':' || is_nullable || ':' || coalesce(column_default, '') FROM information_schema.columns
			WHERE table_schema = 'calendar' AND table_name = $1 AND column_name = $2`, table, name)
	}
	for _, want := range []struct {
		table, name, shape string
	}{
		{"appointment_attendees", "user_id", "uuid:YES:"},
		{"appointment_attendees", "kind", "character varying:NO:'user'::character varying"},
		{"appointment_attendees", "contact_id", "integer:YES:"},
		{"appointment_attendees", "email", "character varying:YES:"},
		{"appointment_attendees", "name", "character varying:YES:"},
		{"appointment_attendees", "rsvp_token_sha256", "character:YES:"},
		{"appointment_attendees", "rsvp_link_ciphertext", "text:YES:"},
		{"appointment_attendees", "invited_at", "timestamp with time zone:YES:"},
		{"appointment_attendees", "invited_sequence", "integer:YES:"},
		{"appointment_attendees", "send_failed_at", "timestamp with time zone:YES:"},
		{"appointments", "cancelled_at", "timestamp with time zone:YES:"},
	} {
		if got := column(want.table, want.name); !equalStrings(got, []string{want.shape}) {
			t.Errorf("calendar.%s.%s = %v, want %s", want.table, want.name, got, want.shape)
		}
	}

	for _, want := range []struct {
		name      string
		columns   []string
		predicate string
	}{
		{"ux_appointment_attendees_user", []string{"appointment_id", "user_id"}, "WHERE (user_id IS NOT NULL)"},
		{"ux_appointment_attendees_contact", []string{"appointment_id", "contact_id"}, "WHERE (contact_id IS NOT NULL)"},
		{"ux_appointment_attendees_email", []string{"appointment_id", "email"}, "WHERE ((kind)::text = 'email'::text)"},
		{"ux_appointment_attendees_rsvp", []string{"rsvp_token_sha256"}, "WHERE (rsvp_token_sha256 IS NOT NULL)"},
	} {
		if cols := indexColumns(t, ctx, pool, "calendar", want.name); !equalStrings(cols, want.columns) {
			t.Errorf("%s columns = %v, want %v", want.name, cols, want.columns)
		}
		def := indexDefinition(t, ctx, pool, "calendar", want.name)
		if !strings.HasPrefix(def, "CREATE UNIQUE INDEX ") || !strings.HasSuffix(def, want.predicate) {
			t.Errorf("%s = %q, want a unique index ending %q", want.name, def, want.predicate)
		}
	}
	if gone := staffCollect(t, ctx, pool, "A's attendee index", `
		SELECT indexname FROM pg_indexes WHERE schemaname = 'calendar' AND indexname = 'ux_appointment_attendees'`); len(gone) != 0 {
		t.Errorf("ux_appointment_attendees = %v, want it replaced by the per-kind indexes", gone)
	}
}

// TestCalendarInvitations_DownDeletesTheOutsideKinds proves the down path
// holds when outside attendees exist: their rows, which have no user_id, are
// deleted before user_id is NOT NULL again, and a colleague's row survives.
func TestCalendarInvitations_DownDeletesTheOutsideKinds(t *testing.T) {
	url := testdb.URL(t)
	migrateTo(t, url, calendarInvitationsVersion)

	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	var appointment int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO calendar.appointments (owner_user_id, title, all_day, starts_at, ends_at, time_zone,
			visibility, transparency, ical_uid, created_at, updated_at)
		VALUES (gen_random_uuid(), 'Kundemøte', false, now(), now() + interval '1 hour', 'Europe/Oslo',
			'shared', 'busy', 'down@vantigo', now(), now())
		RETURNING id`).Scan(&appointment); err != nil {
		t.Fatalf("insert an appointment: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO calendar.appointment_attendees (appointment_id, kind, user_id, contact_id, email, status, created_at) VALUES
			($1, 'user', gen_random_uuid(), NULL, NULL, 'needs-action', now()),
			($1, 'contact', NULL, 7, NULL, 'needs-action', now()),
			($1, 'email', NULL, NULL, 'kari@example.com', 'needs-action', now())`, appointment); err != nil {
		t.Fatalf("insert one attendee of each kind: %v", err)
	}
	pool.Close()

	migrateTo(t, url, calendarInvitationsVersion-1)

	pool, err = db.Open(ctx, url)
	if err != nil {
		t.Fatalf("reopen pool: %v", err)
	}
	defer pool.Close()
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM calendar.appointment_attendees WHERE appointment_id = $1`, appointment).Scan(&left); err != nil {
		t.Fatalf("count the attendees: %v", err)
	}
	if left != 1 {
		t.Errorf("attendees after the down = %d, want the colleague's row alone", left)
	}
}
