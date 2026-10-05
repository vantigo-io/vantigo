package projects_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/projects"
)

// The billable milestones read (invoices work design D3): the milestones ready
// to invoice, row by row, each with its effective amount resolved here — the
// figure the holder compares with and freezes — read on the pool from this
// module's own tables.

func newBillableMilestones(t *testing.T, h *modtest.Harness) contracts.BillableMilestones {
	t.Helper()
	build := projects.Module().BillableMilestones
	if build == nil {
		t.Fatal("the module declares no billable milestones read")
	}
	return build(h.Deps())
}

// osloDay is the business day an instant falls on in Oslo, as a UTC midnight —
// the day a milestone is dated by.
func osloDay(t *testing.T, at time.Time) time.Time {
	t.Helper()
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Fatalf("load Europe/Oslo: %v", err)
	}
	at = at.In(oslo)
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
}

// The set is exactly the ready milestones — not planned, cancelled or
// invoiced ones — each with what a line is built from, and a percent
// milestone's amount resolved exactly: 33.33 % of 3 750.30 is 1 249.97.
// Until bounds them by the day they became ready.
func TestBillableMilestones_TheSetAndTheEffectiveAmount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "BM0001", 3750.30)
	share := readyMilestone(t, c, project.Id, map[string]any{
		"name": "Andel", "amount": nil, "percent": 33.33, "description": "Første tredjedel", "plannedDate": "2026-11-30",
	})
	flat := readyMilestone(t, c, project.Id, map[string]any{"name": "Fast", "amount": 500})
	createMilestone(t, c, project.Id, map[string]any{"name": "Planlagt"})
	movedMilestone(t, c, createMilestone(t, c, project.Id, map[string]any{"name": "Kansellert"}), milestoneCancelled, nil)
	movedMilestone(t, c, readyMilestone(t, c, project.Id, map[string]any{"name": "For hånd"}), milestoneInvoiced, nil)
	read := newBillableMilestones(t, h)

	page, err := read.BillableMilestones(context.Background(), contracts.BillableRequest{ProjectIDs: []int32{project.Id}})
	if err != nil {
		t.Fatalf("BillableMilestones: %v", err)
	}
	if page.More || len(page.Milestones) != 2 {
		t.Fatalf("page = %+v, want the two ready milestones", page)
	}
	got := page.Milestones[0]
	planned := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	if got.ID != int64(share.Id) || got.Revision != share.Revision || got.ProjectID != project.Id ||
		got.Name != "Andel" || got.Description != "Første tredjedel" ||
		got.PlannedDate == nil || !got.PlannedDate.Equal(planned) || share.ReadyAt == nil || !got.ReadyAt.Equal(*share.ReadyAt) ||
		got.Amount != "1249.97" || got.Currency != "NOK" {
		t.Errorf("the percent milestone = %+v, want it as a line is built from, at 1249.97 NOK", got)
	}
	if got := page.Milestones[1]; got.ID != int64(flat.Id) || got.Amount != "500.00" || got.Description != "" || got.PlannedDate != nil {
		t.Errorf("the flat milestone = %+v, want 500.00 and no description or date", got)
	}

	day := osloDay(t, *share.ReadyAt)
	for _, tc := range []struct {
		until time.Time
		want  int
	}{{day, 2}, {day.AddDate(0, 0, -1), 0}} {
		page, err := read.BillableMilestones(context.Background(), contracts.BillableRequest{ProjectIDs: []int32{project.Id}, Until: tc.until})
		if err != nil {
			t.Fatalf("BillableMilestones until %v: %v", tc.until, err)
		}
		if len(page.Milestones) != tc.want {
			t.Errorf("until %v: %d milestones, want %d", tc.until.Format(time.DateOnly), len(page.Milestones), tc.want)
		}
	}
}

// A read answers at most MaxBillableRows rows and says when there were more.
func TestBillableMilestones_More(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "BM0002")
	insert := func(from, to int) {
		h.Exec(t, `
			INSERT INTO projects.billing_milestones (
			    project_id, name, amount, amount_currency, status, position, ready_at,
			    ever_moved, created_by_user_id, created_at, updated_at)
			SELECT $1, 'M' || n, 100, 'NOK', 'ready', n, $2, true, gen_random_uuid(), $2, $2
			FROM generate_series($3::int, $4::int) AS n`, project.Id, h.Now(), from, to)
	}
	read := newBillableMilestones(t, h)

	insert(1, contracts.MaxBillableRows)
	page, err := read.BillableMilestones(context.Background(), contracts.BillableRequest{ProjectIDs: []int32{project.Id}})
	if err != nil {
		t.Fatalf("BillableMilestones: %v", err)
	}
	if page.More || len(page.Milestones) != contracts.MaxBillableRows {
		t.Errorf("exactly %d: %d rows, more %v, want all and no more", contracts.MaxBillableRows, len(page.Milestones), page.More)
	}

	insert(contracts.MaxBillableRows+1, contracts.MaxBillableRows+1)
	page, err = read.BillableMilestones(context.Background(), contracts.BillableRequest{ProjectIDs: []int32{project.Id}})
	if err != nil {
		t.Fatalf("BillableMilestones: %v", err)
	}
	if !page.More || len(page.Milestones) != contracts.MaxBillableRows {
		t.Errorf("one past: %d rows, more %v, want %d and more", len(page.Milestones), page.More, contracts.MaxBillableRows)
	}
}

// Read by ids, only what is still ready answers — a held source's freshness:
// a planned, an invoiced and an unknown milestone are simply absent, as is an
// id past what the column holds. A request Validate refuses is refused.
func TestBillableMilestones_ByIDs(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "BM0003")
	ready := readyMilestone(t, c, project.Id, map[string]any{"name": "Klar"})
	planned := createMilestone(t, c, project.Id, map[string]any{"name": "Planlagt"})
	invoiced := movedMilestone(t, c, readyMilestone(t, c, project.Id, map[string]any{"name": "Fakturert"}), milestoneInvoiced, nil)
	read := newBillableMilestones(t, h)

	page, err := read.BillableMilestones(context.Background(), contracts.BillableRequest{
		IDs: []int64{int64(ready.Id), int64(planned.Id), int64(invoiced.Id), 999999, math.MaxInt32 + 1 + int64(ready.Id)},
	})
	if err != nil {
		t.Fatalf("BillableMilestones: %v", err)
	}
	if page.More || len(page.Milestones) != 1 || page.Milestones[0].ID != int64(ready.Id) || page.Milestones[0].Amount != "100000.00" {
		t.Errorf("page = %+v, want only the ready milestone", page)
	}

	if _, err := read.BillableMilestones(context.Background(), contracts.BillableRequest{}); err == nil {
		t.Error("a request naming nothing was answered, want it refused")
	}
}
