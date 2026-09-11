package identity

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestRecordOperationalEvent proves the upsert OperationalEventService.RecordAsync
// performed (SV/OperationalEventService.cs:18-29): one row per kind, a later
// call replacing the earlier occurred_at rather than adding a second row.
// It also proves the error a malformed kind causes (the CHECK constraint on
// identity.operational_events.kind) is swallowed, not returned or panicked:
// recordOperationalEvent has no error return at all, so a caller (Task 18's
// OIDC sign-in, Task 19's SCIM request) can never see one.
func TestRecordOperationalEvent(t *testing.T) {
	srv, pool := newInternalServer(t)
	ctx := context.Background()
	const kind = "test.operational-event"
	count := func() int {
		return countRows(t, pool, `SELECT count(*) FROM identity.operational_events WHERE kind = $1`, kind)
	}

	srv.recordOperationalEvent(ctx, kind)
	if n := count(); n != 1 {
		t.Fatalf("rows after the first record = %d, want 1", n)
	}

	later := internalNow.Add(time.Hour)
	srv.deps.Clock = func() time.Time { return later }
	srv.recordOperationalEvent(ctx, kind)
	if n := count(); n != 1 {
		t.Fatalf("rows after the second record = %d, want still 1 (an upsert, not a second row)", n)
	}
	var occurredAt time.Time
	if err := pool.QueryRow(ctx, `SELECT occurred_at FROM identity.operational_events WHERE kind = $1`, kind).Scan(&occurredAt); err != nil {
		t.Fatal(err)
	}
	if !occurredAt.Equal(later) {
		t.Errorf("occurred_at = %v, want the later call's %v", occurredAt, later)
	}

	invalid := strings.Repeat("k", 65) // over the CHECK constraint's 64
	srv.recordOperationalEvent(ctx, invalid)
	if n := countRows(t, pool, `SELECT count(*) FROM identity.operational_events WHERE kind = $1`, invalid); n != 0 {
		t.Errorf("rows for a kind that violates the CHECK constraint = %d, want 0 (the error is swallowed)", n)
	}
}
