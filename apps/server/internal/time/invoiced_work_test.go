package timetracking_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
	timetracking "github.com/vantigo-io/vantigo/server/internal/time"
)

// The invoiced-work holder's tests (invoices work design D1, module-boundaries
// rule 10). Each builds the holder the way Compose builds a disabled module's
// — from Deps carrying a logger and nothing else, Pool nil, so it can only
// write through the transaction it is handed — and stamps and releases real
// rows through a transaction on the test database that the test rolls back.

// issuedAt is the issue's own clock in every test: a moment that is neither
// the database's now() nor the test's, so a stamp written with any other
// clock shows.
var issuedAt = time.Date(2026, 10, 5, 10, 30, 0, 0, time.UTC)

// invoiceRef is the stamp every test writes: invoice 7001, number 10001.
func invoiceRef() contracts.InvoiceRef {
	return contracts.InvoiceRef{
		ID: 7001, Number: 10001,
		IssueDate: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		IssuedAt:  issuedAt,
		IssuedBy:  uuid.New(),
	}
}

// newHolder is the holder as a disabled module's Deps build it, with its
// logger writing into the buffer it answers.
func newHolder(t *testing.T) (contracts.InvoicedWorkHolder, *syncLog) {
	t.Helper()
	build := timetracking.Module().InvoicedWork
	if build == nil {
		t.Fatal("the time module declares no InvoicedWork holder")
	}
	log := &syncLog{}
	return build(module.Deps{Logger: slog.New(slog.NewJSONHandler(log, nil))}), log
}

// syncLog is a buffer a logger may write from the holder while the test reads.
type syncLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// warnings is how many lines the logger wrote at WARN.
func (l *syncLog) warnings() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Count(l.buf.String(), `"level":"WARN"`)
}

// inRolledBackTx runs fn in a transaction on pool that is always rolled
// back, so nothing a test writes outlives it.
func inRolledBackTx(t *testing.T, pool *pgxpool.Pool, fn func(ctx context.Context, tx pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fn(ctx, tx)
}

// queryRower is a pool or a transaction.
type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// workRow is one entry seeded straight into time.entries. The zero value is
// an approved, billable entry of two hours at 900.00 NOK on 1001.
type workRow struct {
	status     string
	notBilled  bool
	noRate     bool
	hours      string
	rate       string
	currency   any
	multiplier any
	project    int32
	date       string
	note       any
	workType   any
	typeName   any
	taskTitle  any
	line       any
	invoiceID  any
	number     any
}

// seedWork inserts r for a fresh user and answers its id.
func seedWork(t *testing.T, q queryRower, r workRow) int64 {
	t.Helper()
	if r.status == "" {
		r.status = "approved"
	}
	if r.hours == "" {
		r.hours = "2.00"
	}
	var rate any = "900.00"
	if r.rate != "" {
		rate = r.rate
	}
	if r.noRate {
		rate = nil
	}
	if r.currency == nil {
		r.currency = "NOK"
	}
	if r.project == 0 {
		r.project = projectKraftVerket
	}
	if r.date == "" {
		r.date = workDay
	}
	var approvedAt, approvedBy, invoicedAt any
	if r.status == "approved" || r.status == "invoiced" {
		approvedAt, approvedBy = "2026-09-20T08:00:00Z", uuid.New()
	}
	if r.status == "invoiced" {
		invoicedAt = "2026-09-30T08:00:00Z"
	}
	var id int64
	if err := q.QueryRow(context.Background(), `
		INSERT INTO time.entries
		    (user_id, project_id, billing_line_id, entry_date, hours, note, billable, bill_rate, bill_currency,
		     rate_source, status, approved_by_user_id, approved_at, invoiced_at, invoiced_invoice_id, invoiced_number,
		     work_type_id, work_type_name, bill_multiplier_percent, task_title, created_at, updated_at)
		VALUES ($1, $2, $3::integer, $4::date, $5::numeric, $6, $7, $8::numeric, $9,
		        'project', $10, $11::uuid, $12::timestamptz, $13::timestamptz, $14::bigint, $15::bigint,
		        $16::integer, $17, $18::numeric, $19, now(), now())
		RETURNING id`,
		uuid.New(), r.project, r.line, r.date, r.hours, r.note, !r.notBilled, rate, r.currency,
		r.status, approvedBy, approvedAt, invoicedAt, r.invoiceID, r.number,
		r.workType, r.typeName, r.multiplier, r.taskTitle).Scan(&id); err != nil {
		t.Fatalf("seed an entry: %v", err)
	}
	return id
}

// sourceOf is id as a draft would have read it: its revision, project,
// currency and exact amount, as BillableHours answers them.
func sourceOf(t *testing.T, q queryRower, id int64) contracts.WorkSource {
	t.Helper()
	s := contracts.WorkSource{Kind: contracts.WorkSourceHours, ID: id}
	if err := q.QueryRow(context.Background(), `
		SELECT revision, project_id, COALESCE(bill_currency, '')::text,
		       COALESCE((hours * bill_rate * (COALESCE(bill_multiplier_percent, 100) * 0.01))::text, '0')
		FROM time.entries WHERE id = $1`, id).Scan(&s.Revision, &s.ProjectID, &s.Currency, &s.Amount); err != nil {
		t.Fatalf("read entry %d as a source: %v", id, err)
	}
	return s
}

// entryState is what the holder writes on an entry.
type entryState struct {
	Status     string
	InvoicedAt *time.Time
	InvoiceID  *int64
	Number     *int64
	Revision   int32
	UpdatedAt  time.Time
	ApprovedAt *time.Time
	ApprovedBy *uuid.UUID
}

func stateOf(t *testing.T, q queryRower, id int64) entryState {
	t.Helper()
	var s entryState
	if err := q.QueryRow(context.Background(), `
		SELECT status, invoiced_at, invoiced_invoice_id, invoiced_number, revision, updated_at, approved_at, approved_by_user_id
		FROM time.entries WHERE id = $1`, id).Scan(
		&s.Status, &s.InvoicedAt, &s.InvoiceID, &s.Number, &s.Revision, &s.UpdatedAt, &s.ApprovedAt, &s.ApprovedBy); err != nil {
		t.Fatalf("read entry %d: %v", id, err)
	}
	return s
}

// wantRefusal fails the test unless err is a *contracts.WorkSourceRefusal
// with code for entry id.
func wantRefusal(t *testing.T, err error, code string, id int64) {
	t.Helper()
	var refusal *contracts.WorkSourceRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("err = %v, want a *contracts.WorkSourceRefusal %s", err, code)
	}
	if refusal.Code != code || refusal.Source.ID != id {
		t.Errorf("refusal = %s for entry %d (%s), want %s for entry %d", refusal.Code, refusal.Source.ID, refusal.Detail, code, id)
	}
}

func TestInvoicedWork_StampsApprovedEntriesAtIssuedAt(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	holder, _ := newHolder(t)
	if got := holder.Kinds(); !slices.Equal(got, []contracts.WorkSourceKind{contracts.WorkSourceHours}) {
		t.Errorf("Kinds() = %v, want [time.entry]", got)
	}
	inRolledBackTx(t, pool, func(ctx context.Context, tx pgx.Tx) {
		a, b := seedWork(t, tx, workRow{}), seedWork(t, tx, workRow{multiplier: "150.00"})
		before := stateOf(t, tx, a)
		// Given in reverse: the holder locks and judges in id order whatever
		// order the issue hands them in.
		if err := holder.MarkInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{sourceOf(t, tx, b), sourceOf(t, tx, a)}); err != nil {
			t.Fatalf("MarkInvoiced: %v", err)
		}
		for _, id := range []int64{a, b} {
			got := stateOf(t, tx, id)
			if got.Status != "invoiced" {
				t.Errorf("entry %d status = %s, want invoiced", id, got.Status)
			}
			if got.InvoiceID == nil || *got.InvoiceID != 7001 || got.Number == nil || *got.Number != 10001 {
				t.Errorf("entry %d invoiced by %v/%v, want invoice 7001 number 10001", id, got.InvoiceID, got.Number)
			}
			if got.InvoicedAt == nil || !got.InvoicedAt.Equal(issuedAt) {
				t.Errorf("entry %d invoiced_at = %v, want the issue's %v", id, got.InvoicedAt, issuedAt)
			}
			if !got.UpdatedAt.Equal(issuedAt) {
				t.Errorf("entry %d updated_at = %v, want the issue's %v", id, got.UpdatedAt, issuedAt)
			}
			if got.Revision != before.Revision+1 {
				t.Errorf("entry %d revision = %d, want %d", id, got.Revision, before.Revision+1)
			}
			if got.ApprovedAt == nil || got.ApprovedBy == nil {
				t.Errorf("entry %d lost its approval stamps", id)
			}
		}
	})
}

func TestInvoicedWork_JudgesAlreadyInvoicedFirst(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	holder, _ := newHolder(t)
	inRolledBackTx(t, pool, func(ctx context.Context, tx pgx.Tx) {
		// Stamped by hand and not billable either: named for the stamp.
		id := seedWork(t, tx, workRow{status: "invoiced", notBilled: true})
		err := holder.MarkInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{sourceOf(t, tx, id)})
		wantRefusal(t, err, contracts.SourceAlreadyInvoiced, id)
	})
}

func TestInvoicedWork_RefusesWhatIsNotInvoiceable(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	holder, _ := newHolder(t)
	for _, c := range []struct {
		name string
		row  workRow
	}{
		{"draft", workRow{status: "draft"}},
		{"submitted", workRow{status: "submitted"}},
		{"rejected", workRow{status: "rejected"}},
		{"not billable", workRow{notBilled: true}},
		{"no bill rate", workRow{noRate: true}},
	} {
		inRolledBackTx(t, pool, func(ctx context.Context, tx pgx.Tx) {
			id := seedWork(t, tx, c.row)
			err := holder.MarkInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{sourceOf(t, tx, id)})
			t.Run(c.name, func(t *testing.T) { wantRefusal(t, err, contracts.SourceNotInvoiceable, id) })
		})
	}
	inRolledBackTx(t, pool, func(ctx context.Context, tx pgx.Tx) {
		missing := contracts.WorkSource{Kind: contracts.WorkSourceHours, ID: 987654, Revision: 1, ProjectID: projectKraftVerket, Currency: "NOK", Amount: "1800"}
		err := holder.MarkInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{missing})
		t.Run("missing", func(t *testing.T) { wantRefusal(t, err, contracts.SourceNotInvoiceable, 987654) })
	})
}

func TestInvoicedWork_RefusesWhatChanged(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	holder, _ := newHolder(t)
	for _, c := range []struct {
		name   string
		change func(*contracts.WorkSource)
	}{
		{"revision", func(s *contracts.WorkSource) { s.Revision-- }},
		{"project", func(s *contracts.WorkSource) { s.ProjectID = projectEuro }},
		{"currency", func(s *contracts.WorkSource) { s.Currency = "EUR" }},
		// 1.25 h × 100.33 × 150.25 % is 188.43228125: off in the eighth decimal.
		{"amount", func(s *contracts.WorkSource) { s.Amount = "188.43228124" }},
	} {
		inRolledBackTx(t, pool, func(ctx context.Context, tx pgx.Tx) {
			id := seedWork(t, tx, workRow{hours: "1.25", rate: "100.33", multiplier: "150.25"})
			source := sourceOf(t, tx, id)
			if source.Amount != "188.43228125" {
				t.Fatalf("the seeded amount = %s, want 188.43228125", source.Amount)
			}
			c.change(&source)
			err := holder.MarkInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{source})
			t.Run(c.name, func(t *testing.T) { wantRefusal(t, err, contracts.SourceChanged, id) })
		})
	}
}

func TestInvoicedWork_AnEqualAmountInAnotherScaleIsNoChange(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	holder, _ := newHolder(t)
	for _, amount := range []string{"125.4125", "125.41250000", "125.412500000000", "0125.41250"} {
		inRolledBackTx(t, pool, func(ctx context.Context, tx pgx.Tx) {
			id := seedWork(t, tx, workRow{hours: "1.25", rate: "100.33"})
			source := sourceOf(t, tx, id)
			if source.Amount != "125.412500" {
				t.Fatalf("the seeded amount = %s, want 125.412500", source.Amount)
			}
			source.Amount = amount
			if err := holder.MarkInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{source}); err != nil {
				t.Errorf("amount %q: MarkInvoiced = %v, want the entry stamped", amount, err)
			}
		})
	}
}

func TestInvoicedWork_IgnoresThePeriodLock(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	holder, _ := newHolder(t)
	inRolledBackTx(t, pool, func(ctx context.Context, tx pgx.Tx) {
		if _, err := tx.Exec(ctx, `INSERT INTO time.settings (key, value) VALUES ('locked_before', '2026-10-01')`); err != nil {
			t.Fatalf("set the lock: %v", err)
		}
		id := seedWork(t, tx, workRow{})
		if err := holder.MarkInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{sourceOf(t, tx, id)}); err != nil {
			t.Fatalf("MarkInvoiced behind the lock: %v", err)
		}
		if err := holder.ReleaseInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{sourceOf(t, tx, id)}); err != nil {
			t.Fatalf("ReleaseInvoiced behind the lock: %v", err)
		}
		if got := stateOf(t, tx, id).Status; got != "approved" {
			t.Errorf("status = %s, want approved again", got)
		}
	})
}

func TestInvoicedWork_WritesNothingOnARefusal(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	holder, _ := newHolder(t)
	inRolledBackTx(t, pool, func(ctx context.Context, tx pgx.Tx) {
		ok, draft := seedWork(t, tx, workRow{}), seedWork(t, tx, workRow{status: "draft"})
		before := stateOf(t, tx, ok)
		err := holder.MarkInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{sourceOf(t, tx, ok), sourceOf(t, tx, draft)})
		wantRefusal(t, err, contracts.SourceNotInvoiceable, draft)
		if got := stateOf(t, tx, ok); !reflect.DeepEqual(got, before) {
			t.Errorf("the stampable entry = %+v after a refusal, want it untouched (%+v)", got, before)
		}
	})
}

func TestInvoicedWork_ReleaseReturnsToApproved(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	holder, _ := newHolder(t)
	inRolledBackTx(t, pool, func(ctx context.Context, tx pgx.Tx) {
		id := seedWork(t, tx, workRow{})
		approved := stateOf(t, tx, id)
		if err := holder.MarkInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{sourceOf(t, tx, id)}); err != nil {
			t.Fatalf("MarkInvoiced: %v", err)
		}
		releasedAt := issuedAt.Add(48 * time.Hour)
		ref := invoiceRef()
		ref.IssuedAt = releasedAt
		if err := holder.ReleaseInvoiced(ctx, tx, ref, []contracts.WorkSource{sourceOf(t, tx, id)}); err != nil {
			t.Fatalf("ReleaseInvoiced: %v", err)
		}
		got := stateOf(t, tx, id)
		if got.Status != "approved" || got.InvoicedAt != nil || got.InvoiceID != nil || got.Number != nil {
			t.Errorf("released entry = %+v, want approved with the three invoiced columns cleared", got)
		}
		if got.ApprovedAt == nil || !got.ApprovedAt.Equal(*approved.ApprovedAt) || got.ApprovedBy == nil || *got.ApprovedBy != *approved.ApprovedBy {
			t.Errorf("released entry approved %v by %v, want the approval stamps kept (%v by %v)", got.ApprovedAt, got.ApprovedBy, approved.ApprovedAt, approved.ApprovedBy)
		}
		if got.Revision != approved.Revision+2 || !got.UpdatedAt.Equal(releasedAt) {
			t.Errorf("released entry revision %d updated %v, want %d at the credit note's %v", got.Revision, got.UpdatedAt, approved.Revision+2, releasedAt)
		}
	})
}

func TestInvoicedWork_ReleaseToleratesAMissingStamp(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	holder, log := newHolder(t)
	inRolledBackTx(t, pool, func(ctx context.Context, tx pgx.Tx) {
		id := seedWork(t, tx, workRow{})
		before := stateOf(t, tx, id)
		if err := holder.ReleaseInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{sourceOf(t, tx, id)}); err != nil {
			t.Fatalf("ReleaseInvoiced of an entry without the stamp = %v, want it tolerated", err)
		}
		if got := stateOf(t, tx, id); !reflect.DeepEqual(got, before) {
			t.Errorf("entry = %+v, want it untouched (%+v)", got, before)
		}
	})
	if got := log.warnings(); got != 1 {
		t.Errorf("warnings logged = %d, want 1", got)
	}
}

func TestInvoicedWork_BuiltFromADisabledModulesDeps(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	// What a disabled module's Deps carries: the logger and nothing else.
	holder := timetracking.Module().InvoicedWork(module.Deps{Logger: slog.New(slog.DiscardHandler)})
	if timetracking.Module().BillableHours == nil {
		t.Error("the time module declares no BillableHours provider")
	}
	inRolledBackTx(t, pool, func(ctx context.Context, tx pgx.Tx) {
		id := seedWork(t, tx, workRow{})
		if err := holder.MarkInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{sourceOf(t, tx, id)}); err != nil {
			t.Fatalf("MarkInvoiced: %v", err)
		}
		if err := holder.ReleaseInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{sourceOf(t, tx, id)}); err != nil {
			t.Fatalf("ReleaseInvoiced: %v", err)
		}
		other := contracts.WorkSource{Kind: contracts.WorkSourceExpense, ID: id}
		if err := holder.MarkInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{other}); err == nil {
			t.Error("MarkInvoiced of an expenses.entry source = nil, want an error")
		}
	})
}

// TestInvoicedWork_MarksItsOwnLockedFlag: both directions mark their context
// with this module's own locked-transaction flag, so this module's fakes catch
// a directory call made under the holder's locks. Not parallel: the seam is
// the package's.
func TestInvoicedWork_MarksItsOwnLockedFlag(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	holder, _ := newHolder(t)
	var seen []bool
	defer timetracking.SetInvoicedWorkAfterLock(func(ctx context.Context) {
		seen = append(seen, timetracking.InLockedTx(ctx))
	})()
	inRolledBackTx(t, pool, func(ctx context.Context, tx pgx.Tx) {
		id := seedWork(t, tx, workRow{})
		if err := holder.MarkInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{sourceOf(t, tx, id)}); err != nil {
			t.Fatalf("MarkInvoiced: %v", err)
		}
		if err := holder.ReleaseInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{sourceOf(t, tx, id)}); err != nil {
			t.Fatalf("ReleaseInvoiced: %v", err)
		}
	})
	if !slices.Equal(seen, []bool{true, true}) {
		t.Errorf("InLockedTx under the holder's locks (mark, release) = %v, want [true true]", seen)
	}
}

// TestInvoicedWork_ReleaseLocksBeforeItWrites: a release takes its locks as a
// mark does before it decides anything. The entry carries another invoice's
// stamp, so the release's UPDATE would match nothing and never wait for the
// row; only the lock does — parked behind a raw FOR UPDATE on a connection of
// its own, pg_blocking_pids names it.
func TestInvoicedWork_ReleaseLocksBeforeItWrites(t *testing.T) {
	t.Parallel()
	pool, url := testdb.Migrated(t)
	holder, log := newHolder(t)
	ctx := context.Background()
	id := seedWork(t, pool, workRow{status: "invoiced", invoiceID: 7002, number: 10002})
	source := sourceOf(t, pool, id)

	raw, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = raw.Close(ctx) }()
	rawTx, err := raw.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the raw lock: %v", err)
	}
	defer func() { _ = rawTx.Rollback(ctx) }()
	if _, err := rawTx.Exec(ctx, `SELECT 1 FROM time.entries WHERE id = $1 FOR UPDATE`, id); err != nil {
		t.Fatalf("raw lock: %v", err)
	}
	var rawPID int32
	if err := rawTx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&rawPID); err != nil {
		t.Fatalf("raw pid: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var pid int32
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("pid: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- holder.ReleaseInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{source}) }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("the release finished (%v) while another transaction held the entry, want it parked behind the lock", err)
		default:
		}
		var blockers []int32
		if err := pool.QueryRow(ctx, `SELECT pg_blocking_pids($1)`, pid).Scan(&blockers); err != nil {
			t.Fatalf("pg_blocking_pids: %v", err)
		}
		if slices.Contains(blockers, rawPID) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the release is not waiting on the raw lock (blockers %v)", blockers)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := rawTx.Rollback(ctx); err != nil {
		t.Fatalf("release the raw lock: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("ReleaseInvoiced: %v", err)
	}
	if got := log.warnings(); got != 1 {
		t.Errorf("warnings logged = %d, want 1 for the entry another invoice stamped", got)
	}
	if got := stateOf(t, tx, id); got.InvoiceID == nil || *got.InvoiceID != 7002 {
		t.Errorf("entry invoiced by %v, want invoice 7002's stamp left as it is", got.InvoiceID)
	}
}

// TestApproval_AnInvoicedEntryIsStillRefused: the holder's release is the one
// way out of invoiced. An entry the holder stamped is refused by every manual
// transition exactly as one marked by hand is (approval.go).
func TestApproval_AnInvoicedEntryIsStillRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	everything, _ := signInAs(t, h, projectKraftVerket, roleManager, "time:approve", "time:manage")
	e := submittedEntry(t, owner, nil)
	approveEntries(t, everything, e.Id)
	stampThroughTheHolder(t, h, e.Id)

	for _, path := range []string{approvePath, rejectPath, unapprovePath} {
		errs := batchErrorsOf(t, everything, path, map[string]any{"ids": []int64{e.Id}, "reason": "Nei"})
		wantIDErrors(t, errs, map[int64]string{e.Id: "is invoiced"})
	}
	if got := getEntry(t, everything, e.Id); got.Status != "invoiced" {
		t.Errorf("entry = %s, want still invoiced", got.Status)
	}
}

// TestEntries_InvoicedByOnTheWire: an entry the invoices issue stamped names
// the invoice; one marked invoiced by hand has no invoiceBy at all.
func TestEntries_InvoicedByOnTheWire(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	approver, _ := signInAs(t, h, projectKraftVerket, roleManager)
	stamped, byHand := submittedEntry(t, owner, nil), submittedEntry(t, owner, map[string]any{"entryDate": "2026-09-15"})
	approveEntries(t, approver, stamped.Id, byHand.Id)
	stampThroughTheHolder(t, h, stamped.Id)
	setStatus(t, h, byHand.Id, "invoiced")

	got := getEntry(t, owner, stamped.Id)
	if got.Status != "invoiced" || got.InvoicedBy == nil || got.InvoicedBy.InvoiceId != 7001 || got.InvoicedBy.Number != 10001 {
		t.Errorf("stamped entry = %s invoicedBy %+v, want invoiced by invoice 7001 number 10001", got.Status, got.InvoicedBy)
	}
	if got.InvoicedAt == nil || !got.InvoicedAt.Equal(issuedAt) {
		t.Errorf("stamped entry invoicedAt = %v, want %v", got.InvoicedAt, issuedAt)
	}
	if _, ok := rawEntry(t, owner, byHand.Id)["invoicedBy"]; ok {
		t.Error("an entry marked invoiced by hand answers invoicedBy, want it absent")
	}
	if _, ok := rawEntry(t, owner, stamped.Id)["invoicedBy"]; !ok {
		t.Error("the stamped entry's raw body has no invoicedBy")
	}
}

// stampThroughTheHolder stamps id invoiced by invoice 7001 as the invoices
// issue would: read through BillableHours, marked by the holder, committed.
func stampThroughTheHolder(t *testing.T, h *harness, id int64) {
	t.Helper()
	ctx := context.Background()
	page, err := timetracking.Module().BillableHours(h.Deps()).BillableHours(ctx, contracts.BillableRequest{IDs: []int64{id}})
	if err != nil || len(page.Hours) != 1 {
		t.Fatalf("read entry %d as billable: %+v, %v", id, page, err)
	}
	hour := page.Hours[0]
	source := contracts.WorkSource{Kind: contracts.WorkSourceHours, ID: hour.ID, Revision: hour.Revision, ProjectID: hour.ProjectID, Currency: hour.Currency, Amount: hour.Amount}
	holder := timetracking.Module().InvoicedWork(module.Deps{Logger: slog.New(slog.DiscardHandler)})
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := holder.MarkInvoiced(ctx, tx, invoiceRef(), []contracts.WorkSource{source}); err != nil {
		t.Fatalf("MarkInvoiced: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}
