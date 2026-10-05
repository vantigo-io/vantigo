package timetracking_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
	timetracking "github.com/vantigo-io/vantigo/server/internal/time"
)

// newBillableHours is the provider as Compose builds it: over the pool, with
// no directory at all.
func newBillableHours(t *testing.T) (contracts.BillableHours, *pgxpool.Pool) {
	t.Helper()
	pool, _ := testdb.Migrated(t)
	build := timetracking.Module().BillableHours
	if build == nil {
		t.Fatal("the time module declares no BillableHours provider")
	}
	return build(module.Deps{Pool: pool}), pool
}

// billableIDs is the page's ids, in the page's order.
func billableIDs(page contracts.BillableHoursPage) []int64 {
	ids := make([]int64, 0, len(page.Hours))
	for _, h := range page.Hours {
		ids = append(ids, h.ID)
	}
	return ids
}

func TestBillableHours_TheSet(t *testing.T) {
	t.Parallel()
	p, pool := newBillableHours(t)
	const note = "Privat: tannlegen kl. 14"
	in := seedWork(t, pool, workRow{
		hours: "7.50", rate: "1200.00", multiplier: "150.00", note: note, line: lineFixed,
		workType: workTypeOvertime, typeName: workTypeOvertimeName, taskTitle: taskSpecificationTitle,
	})
	plain := seedWork(t, pool, workRow{date: "2026-09-15"})
	for _, r := range []workRow{
		{notBilled: true}, {noRate: true}, {status: "draft"}, {status: "submitted"}, {status: "rejected"},
		{status: "invoiced"}, {status: "invoiced", invoiceID: 7001, number: 10001},
		{project: projectEuro},
	} {
		seedWork(t, pool, r)
	}

	page, err := p.BillableHours(context.Background(), contracts.BillableRequest{ProjectIDs: []int32{projectKraftVerket, projectInternal}})
	if err != nil {
		t.Fatalf("BillableHours: %v", err)
	}
	if got := billableIDs(page); !slices.Equal(got, []int64{in, plain}) || page.More {
		t.Fatalf("ids = %v more %v, want [%d %d] (approved, billable, priced, on the projects asked) and no more", got, page.More, in, plain)
	}
	got := page.Hours[0]
	var revision int32
	if err := pool.QueryRow(context.Background(), `SELECT revision FROM time.entries WHERE id = $1`, in).Scan(&revision); err != nil {
		t.Fatalf("read revision: %v", err)
	}
	if got.Revision != revision || got.ProjectID != projectKraftVerket || got.BillingLineID == nil || *got.BillingLineID != lineFixed ||
		!got.Date.Equal(time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)) || got.HoursHundredths != 750 ||
		got.BillRate != "1200.00" || got.Currency != "NOK" || got.BillMultiplierPercent == nil || *got.BillMultiplierPercent != "150.00" ||
		got.WorkTypeID == nil || *got.WorkTypeID != workTypeOvertime || got.WorkTypeName != workTypeOvertimeName ||
		got.TaskTitle != taskSpecificationTitle || got.Amount != "13500.00000000" {
		t.Errorf("hour = %+v, want every column of entry %d as logged, 7.5 × 1200 × 150 %% = 13500 exactly", got, in)
	}
	if plainHour := page.Hours[1]; plainHour.BillMultiplierPercent != nil || plainHour.WorkTypeID != nil || plainHour.WorkTypeName != "" || plainHour.Amount != "1800.000000" {
		t.Errorf("ordinary hour = %+v, want no multiplier, no type and 2 × 900 = 1800", plainHour)
	}
	if strings.Contains(fmt.Sprintf("%+v", page), "tannlegen") {
		t.Error("the page carries the entry's note, which is the person's own text")
	}
}

func TestBillableHours_More(t *testing.T) {
	t.Parallel()
	p, pool := newBillableHours(t)
	ctx := context.Background()
	seed := func(project int32, n int) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO time.entries (user_id, project_id, entry_date, hours, billable, bill_rate, bill_currency, rate_source, status, created_at, updated_at)
			SELECT gen_random_uuid(), $1, DATE '2026-09-14', 1.00, true, 900.00, 'NOK', 'project', 'approved', now(), now()
			FROM generate_series(1, $2)`, project, n); err != nil {
			t.Fatalf("seed %d entries: %v", n, err)
		}
	}
	seed(projectKraftVerket, contracts.MaxBillableRows)
	page, err := p.BillableHours(ctx, contracts.BillableRequest{ProjectIDs: []int32{projectKraftVerket}})
	if err != nil {
		t.Fatalf("BillableHours: %v", err)
	}
	if len(page.Hours) != contracts.MaxBillableRows || page.More {
		t.Errorf("exactly the cap: %d rows, more %v; want %d and no more", len(page.Hours), page.More, contracts.MaxBillableRows)
	}
	seed(projectKraftVerket, 1)
	page, err = p.BillableHours(ctx, contracts.BillableRequest{ProjectIDs: []int32{projectKraftVerket}})
	if err != nil {
		t.Fatalf("BillableHours: %v", err)
	}
	if len(page.Hours) != contracts.MaxBillableRows || !page.More {
		t.Errorf("one past the cap: %d rows, more %v; want %d and more", len(page.Hours), page.More, contracts.MaxBillableRows)
	}
}

func TestBillableHours_ByIDs(t *testing.T) {
	t.Parallel()
	p, pool := newBillableHours(t)
	billable := seedWork(t, pool, workRow{})
	elsewhere := seedWork(t, pool, workRow{project: projectEuro, currency: "EUR"})
	invoiced := seedWork(t, pool, workRow{status: "invoiced", invoiceID: 7001, number: 10001})
	unbilled := seedWork(t, pool, workRow{notBilled: true})
	submitted := seedWork(t, pool, workRow{status: "submitted"})

	page, err := p.BillableHours(context.Background(), contracts.BillableRequest{IDs: []int64{invoiced, elsewhere, billable, unbilled, submitted, 987654}})
	if err != nil {
		t.Fatalf("BillableHours: %v", err)
	}
	if got := billableIDs(page); !slices.Equal(got, []int64{billable, elsewhere}) || page.More {
		t.Errorf("ids = %v, want only the still billable [%d %d], whatever project", got, billable, elsewhere)
	}

	for _, bad := range []contracts.BillableRequest{
		{},
		{ProjectIDs: []int32{projectKraftVerket}, IDs: []int64{billable}},
		{IDs: []int64{billable, billable}},
	} {
		if _, err := p.BillableHours(context.Background(), bad); err == nil {
			t.Errorf("BillableHours(%+v) = nil error, want the request refused", bad)
		}
	}
}

func TestBillableHours_Until(t *testing.T) {
	t.Parallel()
	p, pool := newBillableHours(t)
	early := seedWork(t, pool, workRow{date: "2026-09-14"})
	onTheDay := seedWork(t, pool, workRow{date: "2026-09-15"})
	late := seedWork(t, pool, workRow{date: "2026-09-16"})
	until := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)

	page, err := p.BillableHours(context.Background(), contracts.BillableRequest{ProjectIDs: []int32{projectKraftVerket}, Until: until})
	if err != nil {
		t.Fatalf("BillableHours: %v", err)
	}
	if got := billableIDs(page); !slices.Equal(got, []int64{early, onTheDay}) {
		t.Errorf("ids = %v, want [%d %d], the work dated on or before %s", got, early, onTheDay, until.Format(time.DateOnly))
	}

	// The bound holds for a read by ids too.
	page, err = p.BillableHours(context.Background(), contracts.BillableRequest{IDs: []int64{early, onTheDay, late}, Until: until})
	if err != nil {
		t.Fatalf("BillableHours by ids: %v", err)
	}
	if got := billableIDs(page); !slices.Equal(got, []int64{early, onTheDay}) {
		t.Errorf("by ids: ids = %v, want [%d %d], the work dated on or before %s", got, early, onTheDay, until.Format(time.DateOnly))
	}
}

// TestBillableHours_TheExactAmount: the amount carries every decimal the
// numerics multiply to — eight — and is the very figure the holder accepts.
func TestBillableHours_TheExactAmount(t *testing.T) {
	t.Parallel()
	p, pool := newBillableHours(t)
	id := seedWork(t, pool, workRow{hours: "1.25", rate: "100.33", multiplier: "150.25"})
	page, err := p.BillableHours(context.Background(), contracts.BillableRequest{IDs: []int64{id}})
	if err != nil || len(page.Hours) != 1 {
		t.Fatalf("BillableHours: %+v, %v", page, err)
	}
	hour := page.Hours[0]
	if hour.Amount != "188.43228125" {
		t.Errorf("amount = %s, want 1.25 × 100.33 × 150.25 %% = 188.43228125 exactly", hour.Amount)
	}
	holder, _ := newHolder(t)
	inRolledBackTx(t, pool, func(ctx context.Context, tx pgx.Tx) {
		source := contracts.WorkSource{Kind: contracts.WorkSourceHours, ID: hour.ID, Revision: hour.Revision, ProjectID: hour.ProjectID, Currency: hour.Currency, Amount: hour.Amount}
		if err := holder.MarkInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{source}); err != nil {
			t.Errorf("MarkInvoiced of the amount BillableHours answered: %v", err)
		}
	})
}
