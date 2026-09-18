package timetracking

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/time/store"
)

// TestEntryResponse_UnknownProject_OmitsTheTrackableCode proves an entry
// whose project the directory no longer resolves keeps its line code but
// quotes no trackable code: without the project's code there is no pair to
// quote, and "-PM" would be a code nobody ever wrote.
func TestEntryResponse_UnknownProject_OmitsTheTrackableCode(t *testing.T) {
	t.Parallel()
	line := int32(3001)
	row := store.TimeEntry{
		ID: 1, UserID: uuid.New(), ProjectID: 1001, BillingLineID: &line,
		EntryDate: pgtype.Date{Time: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC), Valid: true},
		Hours:     numericFromCents(200), RateSource: sourceNone, Status: statusDraft, Revision: 1,
	}
	names := entryNames{
		users:    map[uuid.UUID]contracts.UserEntry{},
		projects: map[int32]contracts.ProjectEntry{},
		lines:    map[int32]contracts.BillingLineEntry{line: {ID: line, ProjectID: 1001, Code: "PM"}},
	}

	resp, err := entryResponse(row, entryAccess{}, names)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ProjectName != unknownProject {
		t.Errorf("projectName = %q, want %q", resp.ProjectName, unknownProject)
	}
	if resp.BillingLineCode == nil || *resp.BillingLineCode != "PM" {
		t.Errorf("billingLineCode = %v, want PM", resp.BillingLineCode)
	}
	if resp.TrackableCode != nil {
		t.Errorf("trackableCode = %q, want it absent when the project's code is unknown", *resp.TrackableCode)
	}
}
