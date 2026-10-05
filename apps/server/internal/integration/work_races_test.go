package integration_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The issue raced against every writer of its sources (invoices work design
// D1, module-boundaries rule 10), the real modules on both sides, on a pool of
// two connections serving only the two racing writers: any call that takes a
// second pool connection while its transaction holds locks — a directory
// read under a lock — waits for a connection the other writer holds, and the
// pair hangs to its deadline instead of passing on a larger pool.
//
// Each pair is held at a lock: a raw transaction on a connection of its own,
// outside the pool, takes the contested row; the first writer is started and
// seen waiting on it, then the second, seen waiting behind the first; the raw
// transaction commits, and Postgres hands the row to the first. Both must
// finish under their deadline, one outcome wins and the loser's refusal is the
// named one — never a deadlock (SQLSTATE 40P01). Row locks are proved by
// NOWAIT probes and the waits by pg_blocking_pids, each on a connection of
// its own; no pg_locks read stands as proof that a row is or is not locked.

// raceDeadline bounds every racing request: past it the request answers
// Status 0, which every race reads as a writer that never finished.
const raceDeadline = 20 * time.Second

// raceInstallation is the work installation on a pool of two.
func raceInstallation(t *testing.T) (*modtest.Harness, *modtest.Client) {
	t.Helper()
	h, kari, _ := workInstallation(t, modtest.WithPoolMaxConns(2))
	return h, kari
}

// ownConn is a connection of its own on the installation's database, outside
// its pool: a raw lock-holder's, a probe's or a pg_blocking_pids reader's.
func ownConn(t *testing.T, h *modtest.Harness) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), h.Deps().Config.DatabaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// rawLock is a transaction holding a row lock on a connection of its own
// until it is released.
type rawLock struct {
	tx  pgx.Tx
	pid uint32
}

// holdRow begins a transaction on a connection of its own and runs lockSQL —
// a row lock — in it.
func holdRow(t *testing.T, h *modtest.Harness, lockSQL string, args ...any) *rawLock {
	t.Helper()
	conn := ownConn(t, h)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the raw lock: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, lockSQL, args...); err != nil {
		t.Fatalf("the raw lock %q: %v", lockSQL, err)
	}
	return &rawLock{tx: tx, pid: conn.PgConn().PID()}
}

// release commits the raw transaction, handing the row on.
func (l *rawLock) release(t *testing.T) {
	t.Helper()
	if err := l.tx.Commit(context.Background()); err != nil {
		t.Fatalf("release the raw lock: %v", err)
	}
}

// newWaiter waits until a backend of the installation's database other than
// those known waits on a lock, and answers its pid.
func newWaiter(t *testing.T, probe *pgx.Conn, known ...uint32) uint32 {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var pids []uint32
		rows, err := probe.Query(context.Background(), `
			SELECT pid FROM pg_stat_activity
			WHERE datname = current_database() AND cardinality(pg_blocking_pids(pid)) > 0
			ORDER BY pid`)
		if err != nil {
			t.Fatalf("read the waiting backends: %v", err)
		}
		if pids, err = pgx.CollectRows(rows, pgx.RowTo[uint32]); err != nil {
			t.Fatalf("read the waiting backends: %v", err)
		}
		for _, pid := range pids {
			if !slices.Contains(known, pid) {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no backend besides %v came to wait on a lock within 10 s", known)
	return 0
}

// blockersOf is pg_blocking_pids(pid): who pid waits on.
func blockersOf(t *testing.T, probe *pgx.Conn, pid uint32) []uint32 {
	t.Helper()
	var pids []uint32
	if err := probe.QueryRow(context.Background(), `SELECT pg_blocking_pids($1)`, pid).Scan(&pids); err != nil {
		t.Fatalf("pg_blocking_pids(%d): %v", pid, err)
	}
	return pids
}

// racer is one writer of a race: its name and its request, made under ctx.
type racer struct {
	name string
	do   func(ctx context.Context) *modtest.Response
}

// race holds first and second at lock: first is started and seen waiting on
// the raw transaction, second is started and seen waiting behind first, the
// raw transaction commits and both run to their answers, each under
// raceDeadline. during, when set, runs while both wait. A writer still
// unanswered at its deadline answers Status 0.
func race(t *testing.T, h *modtest.Harness, lock *rawLock, first, second racer, during func(firstPID, secondPID uint32)) (a, b *modtest.Response) {
	t.Helper()
	probe := ownConn(t, h)
	start := func(r racer) chan *modtest.Response {
		done := make(chan *modtest.Response, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), raceDeadline)
			defer cancel()
			done <- r.do(ctx)
		}()
		return done
	}
	firstDone := start(first)
	firstPID := newWaiter(t, probe)
	if got := blockersOf(t, probe, firstPID); !slices.Equal(got, []uint32{lock.pid}) {
		t.Fatalf("%s waits on %v, want the raw lock %d", first.name, got, lock.pid)
	}
	secondDone := start(second)
	secondPID := newWaiter(t, probe, firstPID)
	if during != nil {
		during(firstPID, secondPID)
	} else if got := blockersOf(t, probe, secondPID); !slices.Contains(got, firstPID) {
		t.Fatalf("%s waits on %v, want it queued behind %s (%d)", second.name, got, first.name, firstPID)
	}
	lock.release(t)
	a, b = <-firstDone, <-secondDone
	for _, r := range []struct {
		name string
		res  *modtest.Response
	}{{first.name, a}, {second.name, b}} {
		if r.res.Status == 0 {
			t.Errorf("%s never finished: its %s deadline ran out", r.name, raceDeadline)
		}
		if r.res.Status >= 500 {
			t.Errorf("%s = %d %s", r.name, r.res.Status, r.res.Body)
		}
	}
	if logs := h.Logs(); strings.Contains(logs, "40P01") || strings.Contains(logs, "deadlock detected") {
		t.Errorf("the race deadlocked: %s", logs)
	}
	if n := deadlocks(t, probe); n != 0 {
		t.Errorf("Postgres broke %d deadlock(s) during the race (SQLSTATE 40P01), a writer retrying it or not", n)
	}
	return a, b
}

// deadlocks is how many deadlocks Postgres has detected in the installation's
// database — its cumulative statistics, which a backend reports within a
// second of going idle, so it is read after that settles: a deadlock the
// loser retried (the customers merge retries 40P01) leaves no other trace.
func deadlocks(t *testing.T, probe *pgx.Conn) int64 {
	t.Helper()
	time.Sleep(1500 * time.Millisecond)
	ctx := context.Background()
	if _, err := probe.Exec(ctx, `SELECT pg_stat_clear_snapshot()`); err != nil {
		t.Fatalf("clear the statistics snapshot: %v", err)
	}
	var n int64
	if err := probe.QueryRow(ctx, `SELECT deadlocks FROM pg_stat_database WHERE datname = current_database()`).Scan(&n); err != nil {
		t.Fatalf("read the deadlocks: %v", err)
	}
	return n
}

// issuer is the issue of draft id as a racer.
func issuer(c *modtest.Client, id int64) racer {
	return racer{name: "the issue", do: func(ctx context.Context) *modtest.Response {
		return c.Do(http.MethodPost, invoiceAt(id)+"/issue", map[string]any{}, modtest.Context(ctx))
	}}
}

// writer is another writer's request as a racer.
func writer(c *modtest.Client, name, method, path string, body any) racer {
	return racer{name: name, do: func(ctx context.Context) *modtest.Response {
		return c.Do(method, path, body, modtest.Context(ctx))
	}}
}

// conflict is the code and the source a 409 names.
type conflict struct {
	Code          string `json:"code"`
	Detail        string `json:"detail"`
	SourceKind    string `json:"sourceKind"`
	SourceID      int64  `json:"sourceId"`
	InvoiceID     int64  `json:"invoiceId"`
	InvoiceNumber int64  `json:"invoiceNumber"`
}

// conflictOf is res's 409 body, or a failed test.
func conflictOf(t *testing.T, what string, res *modtest.Response) conflict {
	t.Helper()
	var c conflict
	if res.Status != http.StatusConflict {
		t.Errorf("%s = %d %s, want 409", what, res.Status, res.Body)
		return c
	}
	res.JSON(&c)
	return c
}

// refusedSource asserts res is the issue's 409 code naming the source.
func refusedSource(t *testing.T, res *modtest.Response, code, kind string, id int64) {
	t.Helper()
	if c := conflictOf(t, "the issue", res); c.Code != code || c.SourceKind != kind || c.SourceID != id {
		t.Errorf("the issue = %+v, want %s on %s %d", c, code, kind, id)
	}
}

// issuedNow asserts res is the issue's 200 and answers the document.
func issuedNow(t *testing.T, res *modtest.Response) workDoc {
	t.Helper()
	var d workDoc
	if res.Status != http.StatusOK {
		t.Fatalf("the issue = %d %s, want 200", res.Status, res.Body)
	}
	res.JSON(&d)
	return d
}

// fieldErrors is a 400's errors.
func fieldErrors(t *testing.T, what string, res *modtest.Response) map[string][]string {
	t.Helper()
	var p struct {
		Errors map[string][]string `json:"errors"`
	}
	if res.Status != http.StatusBadRequest {
		t.Errorf("%s = %d %s, want 400", what, res.Status, res.Body)
		return nil
	}
	res.JSON(&p)
	return p.Errors
}

// stillADraft asserts the refused issue left its document a draft — the
// number rolled back with it.
func stillADraft(t *testing.T, c *modtest.Client, id int64) {
	t.Helper()
	if d := readWorkDoc(t, c, id); d.Status != "draft" || d.Number != nil {
		t.Errorf("the refused document = %s number %v, want still a draft, unnumbered", d.Status, d.Number)
	}
}

// expenseFixture is a work installation on a pool of two with one approved,
// re-billable, employee-paid expense held by a draft.
func expenseFixture(t *testing.T) (*modtest.Harness, *modtest.Client, workExpense, workDoc) {
	t.Helper()
	h, kari := raceInstallation(t)
	customer := invoiceNewBusiness(t, kari, "Fjord Nord", "Fjord Nord AS", "923609016").Id
	project := newWorkProject(t, kari, customer, "FN1000", nil)
	e := rebillableExpense(t, kari, project.Id, "2026-09-03", 1250)
	return h, kari, e, fromWork(t, kari, customer, workSource{"expenses.entry", e.Id, e.Revision})
}

const lockExpense = `SELECT id FROM expenses.entries WHERE id = $1 FOR UPDATE`

// An expense's manual mark against the issue holding it. The mark first: the
// issue finds the line marked by hand under its holder's lock and refuses
// source_already_invoiced, its number rolled back. The issue first: the mark
// — past its pre-read, parked inside its own locked transaction — finds the
// line stamped under its lock and answers 409 invoiced_by_invoices naming the
// invoice: the door's under-lock branch.
func TestWorkRace_IssueAgainstTheExpensesManualMark(t *testing.T) {
	t.Parallel()
	mark := func(c *modtest.Client, e workExpense) racer {
		return writer(c, "the manual mark", http.MethodPost, fmt.Sprintf("%s/%d/invoiced", expensesEntries, e.Id),
			map[string]any{"revision": e.Revision, "reference": "F-2026-1"})
	}
	t.Run("the mark first", func(t *testing.T) {
		t.Parallel()
		h, kari, e, draft := expenseFixture(t)
		marked, issue := race(t, h, holdRow(t, h, lockExpense, e.Id), mark(kari, e), issuer(kari, draft.ID), nil)
		if marked.Status != http.StatusOK {
			t.Errorf("the mark = %d %s, want 200", marked.Status, marked.Body)
		}
		refusedSource(t, issue, "source_already_invoiced", "expenses.entry", e.Id)
		stillADraft(t, kari, draft.ID)
		if got := readExpense(t, kari, e.Id); got.Billing == nil || got.Billing.Invoice == nil || got.Billing.Invoice.InvoicedBy != nil ||
			got.Billing.Invoice.Reference == nil || *got.Billing.Invoice.Reference != "F-2026-1" {
			t.Errorf("the expense = %+v, want marked by hand with F-2026-1", got.Billing)
		}
	})
	t.Run("the issue first", func(t *testing.T) {
		t.Parallel()
		h, kari, e, draft := expenseFixture(t)
		issue, marked := race(t, h, holdRow(t, h, lockExpense, e.Id), issuer(kari, draft.ID), mark(kari, e), nil)
		doc := issuedNow(t, issue)
		if c := conflictOf(t, "the mark", marked); c.Code != "invoiced_by_invoices" || c.InvoiceID != doc.ID || c.InvoiceNumber != *doc.Number {
			t.Errorf("the mark = %+v, want invoiced_by_invoices naming invoice %d number %d", c, doc.ID, *doc.Number)
		}
		if got := readExpense(t, kari, e.Id); got.Billing == nil || got.Billing.Invoice == nil || got.Billing.Invoice.InvoicedBy == nil ||
			got.Billing.Invoice.InvoicedBy.InvoiceId != doc.ID || got.Billing.Invoice.Reference != nil {
			t.Errorf("the expense = %+v, want stamped by invoice %d, no reference", got.Billing, doc.ID)
		}
	})
}

// A batch reimbursement of the held expense against the issue
// (expenses/flow.go's lockBatch, the holder's own order): no loser. The
// reimbursement moves the line's revision and nothing the bill reads, which
// the holder judges, so either order both commit, the line ends reimbursed
// and stamped, and the stamp is the issue's whatever the revision says.
func TestWorkRace_IssueAgainstABatchReimbursement(t *testing.T) {
	t.Parallel()
	reimburse := func(c *modtest.Client, e workExpense) racer {
		return writer(c, "the reimbursement", http.MethodPost, "/api/v1/expenses/reimbursed",
			map[string]any{"entryIds": []int64{e.Id}, "date": invoiceToday})
	}
	for _, issueFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "the reimbursement first", true: "the issue first"}[issueFirst], func(t *testing.T) {
			t.Parallel()
			h, kari, e, draft := expenseFixture(t)
			first, second := reimburse(kari, e), issuer(kari, draft.ID)
			if issueFirst {
				first, second = second, first
			}
			a, b := race(t, h, holdRow(t, h, lockExpense, e.Id), first, second, nil)
			issue, reimbursed := b, a
			if issueFirst {
				issue, reimbursed = a, b
			}
			doc := issuedNow(t, issue)
			if reimbursed.Status != http.StatusOK {
				t.Errorf("the reimbursement = %d %s, want 200", reimbursed.Status, reimbursed.Body)
			}
			got := readExpense(t, kari, e.Id)
			if got.Reimbursement == nil || got.Reimbursement.Date != invoiceToday || got.Revision != e.Revision+2 {
				t.Errorf("the expense = reimbursed %+v at revision %d, want reimbursed today, two writes past %d", got.Reimbursement, got.Revision, e.Revision)
			}
			if got.Billing == nil || got.Billing.Invoice == nil || got.Billing.Invoice.InvoicedBy == nil ||
				got.Billing.Invoice.InvoicedBy.InvoiceId != doc.ID || got.Billing.Invoice.InvoicedBy.Number != *doc.Number {
				t.Errorf("the expense's stamp = %+v, want invoice %d number %d", got.Billing, doc.ID, *doc.Number)
			}
		})
	}
}

// milestoneFixture is a work installation on a pool of two with project's
// one ready milestone, made by body, held by a draft.
func milestoneFixture(t *testing.T, project, milestone map[string]any) (*modtest.Harness, *modtest.Client, workProject, workMilestone, workDoc) {
	t.Helper()
	h, kari := raceInstallation(t)
	customer := invoiceNewBusiness(t, kari, "Fjord Nord", "Fjord Nord AS", "923609016").Id
	p := newWorkProject(t, kari, customer, "FN1000", project)
	m := newMilestone(t, kari, p.Id, "Fase 1", milestone)
	return h, kari, p, m, fromWork(t, kari, customer, workSource{"projects.milestone", m.Id, m.Revision})
}

const lockProject = `SELECT id FROM projects.projects WHERE id = $1 FOR NO KEY UPDATE`

// A milestone's manual ready → invoiced against the issue holding it, both
// taking the project's row first (withProjectLock, the holder's LockProject).
// The move first: the issue finds the milestone invoiced by hand and refuses
// source_already_invoiced. The issue first: the move, parked at the project
// row, finds the milestone stamped under its lock — at a revision the stamp
// moved on, so it answers the stale-revision 409 with the current one — and
// a move at that revision is the 400 on status: invoiced cannot move to
// invoiced.
func TestWorkRace_IssueAgainstAMilestoneMove(t *testing.T) {
	t.Parallel()
	move := func(c *modtest.Client, m workMilestone, revision int32) racer {
		return writer(c, "the move", http.MethodPost, fmt.Sprintf("%s/milestones/%d/status", projectsPath, m.Id),
			map[string]any{"status": "invoiced", "revision": revision, "invoiceReference": "F-2026-1"})
	}
	t.Run("the move first", func(t *testing.T) {
		t.Parallel()
		h, kari, p, m, draft := milestoneFixture(t, nil, map[string]any{"amount": 10000})
		moved, issue := race(t, h, holdRow(t, h, lockProject, p.Id), move(kari, m, m.Revision), issuer(kari, draft.ID), nil)
		if moved.Status != http.StatusOK {
			t.Errorf("the move = %d %s, want 200", moved.Status, moved.Body)
		}
		refusedSource(t, issue, "source_already_invoiced", "projects.milestone", m.Id)
		stillADraft(t, kari, draft.ID)
		if got := readMilestone(t, kari, p.Id, m.Id); got.Status != "invoiced" || got.InvoicedByInvoice != nil {
			t.Errorf("the milestone = %s by %+v, want invoiced by hand", got.Status, got.InvoicedByInvoice)
		}
	})
	t.Run("the issue first", func(t *testing.T) {
		t.Parallel()
		h, kari, p, m, draft := milestoneFixture(t, nil, map[string]any{"amount": 10000})
		issue, moved := race(t, h, holdRow(t, h, lockProject, p.Id), issuer(kari, draft.ID), move(kari, m, m.Revision), nil)
		doc := issuedNow(t, issue)
		got := readMilestone(t, kari, p.Id, m.Id)
		if c := conflictOf(t, "the move", moved); c.Code != "" ||
			!strings.HasPrefix(c.Detail, fmt.Sprintf("The project has revision %d;", got.Revision)) {
			t.Errorf("the move = %+v, want the stale-revision 409 naming revision %d: the stamp moved the revision", c, got.Revision)
		}
		if got.Status != "invoiced" || got.InvoicedByInvoice == nil || got.InvoicedByInvoice.InvoiceId != doc.ID {
			t.Fatalf("the milestone = %s by %+v, want stamped by invoice %d", got.Status, got.InvoicedByInvoice, doc.ID)
		}
		again := move(kari, m, got.Revision).do(context.Background())
		if errs := fieldErrors(t, "the move at the current revision", again); len(errs["status"]) == 0 {
			t.Errorf("the move at the current revision = %s, want a 400 on status", again.Body)
		}
	})
}

// A fixed-price project's price edit (PUT /projects/{id}, under LockProject)
// against the issue of a percent milestone, which the holder judges at the
// effective amount under the same project lock. The edit first: the
// milestone's amount moved with the price and the issue refuses
// source_changed. The issue first: the edit, parked at the project row,
// commits after it, and the invoiced milestone keeps the amount it was
// invoiced at.
func TestWorkRace_IssueAgainstAFixedPriceEdit(t *testing.T) {
	t.Parallel()
	fixedPrice := map[string]any{"billingType": "fixed-price", "fixedPriceAmount": 100000, "defaultBillRate": nil}
	edit := func(t *testing.T, c *modtest.Client, p workProject, customer int32) racer {
		var current workProject
		okJSON(t, c, http.MethodGet, fmt.Sprintf("%s/%d", projectsPath, p.Id), nil, &current)
		return writer(c, "the price edit", http.MethodPut, fmt.Sprintf("%s/%d", projectsPath, p.Id), map[string]any{
			"code": p.Code, "name": p.Name, "customerId": customer, "billingType": "fixed-price", "currency": "NOK",
			"fixedPriceAmount": 120000, "revision": current.Revision,
		})
	}
	customerOf := func(t *testing.T, c *modtest.Client, d workDoc) int32 { return readWorkDoc(t, c, d.ID).CustomerID }
	t.Run("the edit first", func(t *testing.T) {
		t.Parallel()
		h, kari, p, m, draft := milestoneFixture(t, fixedPrice, map[string]any{"percent": 30})
		edited, issue := race(t, h, holdRow(t, h, lockProject, p.Id), edit(t, kari, p, customerOf(t, kari, draft)), issuer(kari, draft.ID), nil)
		if edited.Status != http.StatusOK {
			t.Errorf("the edit = %d %s, want 200", edited.Status, edited.Body)
		}
		refusedSource(t, issue, "source_changed", "projects.milestone", m.Id)
		stillADraft(t, kari, draft.ID)
		if got := readMilestone(t, kari, p.Id, m.Id); got.EffectiveAmount == nil || *got.EffectiveAmount != 36000 {
			t.Errorf("the milestone's effective amount = %v, want 36 000 at the new price", got.EffectiveAmount)
		}
	})
	t.Run("the issue first", func(t *testing.T) {
		t.Parallel()
		h, kari, p, m, draft := milestoneFixture(t, fixedPrice, map[string]any{"percent": 30})
		issue, edited := race(t, h, holdRow(t, h, lockProject, p.Id), issuer(kari, draft.ID), edit(t, kari, p, customerOf(t, kari, draft)), nil)
		doc := issuedNow(t, issue)
		if edited.Status != http.StatusOK {
			t.Errorf("the edit = %d %s, want 200: it commits after the issue", edited.Status, edited.Body)
		}
		if got := readMilestone(t, kari, p.Id, m.Id); got.Status != "invoiced" || got.EffectiveAmount == nil || *got.EffectiveAmount != 30000 ||
			got.InvoicedByInvoice == nil || got.InvoicedByInvoice.InvoiceId != doc.ID {
			t.Errorf("the milestone = %s %v by %+v, want invoiced by %d at the frozen 30 000", got.Status, got.EffectiveAmount, got.InvoicedByInvoice, doc.ID)
		}
		if !same(doc.NetTotal, 30000) {
			t.Errorf("the invoice's net = %v, want 30 000", doc.NetTotal)
		}
	})
}

// hoursFixture is a work installation on a pool of two with one approved
// hour held by a draft.
func hoursFixture(t *testing.T) (*modtest.Harness, *modtest.Client, workProject, workHour, workDoc) {
	t.Helper()
	h, kari := raceInstallation(t)
	customer := invoiceNewBusiness(t, kari, "Fjord Nord", "Fjord Nord AS", "923609016").Id
	p := newWorkProject(t, kari, customer, "FN1000", nil)
	e := approvedHours(t, kari, kari, p.Id, "2026-09-01", 4)
	return h, kari, p, e, fromWork(t, kari, customer, workSource{"time.entry", e.Id, e.Revision})
}

const lockEntry = `SELECT id FROM time.entries WHERE id = $1 FOR UPDATE`

// A time unapprove against the issue holding the entry, both locking the
// entries by id (LockEntries). The unapprove first: the issue finds the entry
// a draft again and refuses source_not_invoiceable. The issue first: the
// unapprove, parked at the entry, finds it invoiced and refuses "Entry n is
// invoiced".
func TestWorkRace_IssueAgainstATimeUnapprove(t *testing.T) {
	t.Parallel()
	unapprove := func(c *modtest.Client, e workHour) racer {
		return writer(c, "the unapprove", http.MethodPost, timeEntries+"/unapprove", map[string]any{"ids": []int64{e.Id}})
	}
	t.Run("the unapprove first", func(t *testing.T) {
		t.Parallel()
		h, kari, _, e, draft := hoursFixture(t)
		undone, issue := race(t, h, holdRow(t, h, lockEntry, e.Id), unapprove(kari, e), issuer(kari, draft.ID), nil)
		if undone.Status != http.StatusOK {
			t.Errorf("the unapprove = %d %s, want 200", undone.Status, undone.Body)
		}
		refusedSource(t, issue, "source_not_invoiceable", "time.entry", e.Id)
		stillADraft(t, kari, draft.ID)
	})
	t.Run("the issue first", func(t *testing.T) {
		t.Parallel()
		h, kari, _, e, draft := hoursFixture(t)
		issue, undone := race(t, h, holdRow(t, h, lockEntry, e.Id), issuer(kari, draft.ID), unapprove(kari, e), nil)
		doc := issuedNow(t, issue)
		if errs := fieldErrors(t, "the unapprove", undone); !slices.Equal(errs["ids"], []string{fmt.Sprintf("Entry %d is invoiced", e.Id)}) {
			t.Errorf("the unapprove's errors = %v, want Entry %d is invoiced", errs, e.Id)
		}
		if got := readHour(t, kari, e.Id); got.Status != "invoiced" || got.InvoicedBy == nil || got.InvoicedBy.InvoiceId != doc.ID {
			t.Errorf("the entry = %s by %+v, want invoiced by %d", got.Status, got.InvoicedBy, doc.ID)
		}
	})
}

// A customers merge of the draft's customer against its issue, the draft
// holding a milestone of the absorbed customer's project. The issue is
// parked holding its document, after its own locks and before any source row
// — at the number counter, which a raw transaction holds — and the merge,
// calling the invoices module's holder before projects' (Compose's
// partition), comes to wait on that document: pg_blocking_pids(merge) is the
// issue, and a FOR NO KEY UPDATE NOWAIT probe of the project succeeds — the
// merge holds no project row while it waits, so the issue's holder takes it.
// No loser: both commit, the invoice and the project both naming the
// survivor afterwards.
func TestWorkRace_IssueAgainstACustomerMerge(t *testing.T) {
	t.Parallel()
	h, kari := raceInstallation(t)
	survivor := invoiceNewBusiness(t, kari, "Acme AS", "Acme AS", "923609016").Id
	absorbed := invoiceNewBusiness(t, kari, "Acme Norge AS", "Acme Norge AS", "987654325").Id
	p := newWorkProject(t, kari, absorbed, "ACME1000", nil)
	m := newMilestone(t, kari, p.Id, "Fase 1", map[string]any{"amount": 10000})
	draft := fromWork(t, kari, absorbed, workSource{"projects.milestone", m.Id, m.Revision})

	counter := holdRow(t, h, `
		INSERT INTO invoices.counters (counter_name, next_value) VALUES ('documents', 1)
		ON CONFLICT (counter_name) DO UPDATE SET next_value = invoices.counters.next_value`)
	merge := writer(kari, "the merge", http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/merge", survivor), map[string]any{"sourceId": absorbed})
	probe := ownConn(t, h)
	var held error
	issue, merged := race(t, h, counter, issuer(kari, draft.ID), merge, func(issuePID, mergePID uint32) {
		if got := blockersOf(t, probe, mergePID); !slices.Equal(got, []uint32{issuePID}) {
			t.Errorf("pg_blocking_pids(merge) = %v, want {issue %d}: the merge waits on the issue's document", got, issuePID)
		}
		held = noWait(probe, `SELECT id FROM projects.projects WHERE id = $1 FOR NO KEY UPDATE NOWAIT`, p.Id)
		if err := noWait(probe, `SELECT id FROM invoices.invoices WHERE id = $1 FOR UPDATE NOWAIT`, draft.ID); !isLockNotAvailable(err) {
			t.Errorf("a NOWAIT probe of the issue's document = %v, want 55P03: the issue holds it", err)
		}
	})
	if held != nil {
		t.Errorf("a FOR NO KEY UPDATE NOWAIT probe of the project while the merge waits = %v, want it free: the merge must hold no project row", held)
	}
	doc := issuedNow(t, issue)
	if merged.Status != http.StatusOK {
		t.Errorf("the merge = %d %s, want 200", merged.Status, merged.Body)
	}
	if got := readWorkDoc(t, kari, doc.ID); got.CustomerID != survivor {
		t.Errorf("the invoice names customer %d, want the survivor %d", got.CustomerID, survivor)
	}
	var project struct {
		CustomerID int32 `json:"customerId"`
	}
	okJSON(t, kari, http.MethodGet, fmt.Sprintf("%s/%d", projectsPath, p.Id), nil, &project)
	if project.CustomerID != survivor {
		t.Errorf("the project names customer %d, want the survivor %d", project.CustomerID, survivor)
	}
	if got := readMilestone(t, kari, p.Id, m.Id); got.InvoicedByInvoice == nil || got.InvoicedByInvoice.InvoiceId != doc.ID {
		t.Errorf("the milestone = %+v, want stamped by invoice %d", got.InvoicedByInvoice, doc.ID)
	}
}

// noWait runs a NOWAIT row lock in a transaction of its own on conn and rolls
// it back: nil when the row was free, the 55P03 error when another
// transaction holds it.
func noWait(conn *pgx.Conn, sql string, args ...any) error {
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, sql, args...)
	return err
}

// isLockNotAvailable reports whether err is SQLSTATE 55P03.
func isLockNotAvailable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "55P03"
}

// The window the issue's pre-read leaves open (invoices work design D1:
// "re-checks each project's BillingType from that read"; plan reading 19):
// hours of a project turned fixed-price after the issue read the projects —
// here, while the issue is parked at the entry's row behind a raw lock, past
// every check — are stamped and issued. The holder judges the entry, never
// the project's billing type, and the issue takes no project lock for hours,
// so nothing under the lock sees the change. This pins the window as it
// stands: D14's source_not_selectable judges the billing type as read before
// the transaction, as reading 7 accepts for a project's customer.
func TestWorkRace_ABillingTypeChangeAfterTheIssuesPreReadIsNotSeen(t *testing.T) {
	t.Parallel()
	h, kari, p, e, draft := hoursFixture(t)
	customer := readWorkDoc(t, kari, draft.ID).CustomerID
	lock := holdRow(t, h, lockEntry, e.Id)
	probe := ownConn(t, h)
	done := make(chan *modtest.Response, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), raceDeadline)
		defer cancel()
		done <- issuer(kari, draft.ID).do(ctx)
	}()
	issuePID := newWaiter(t, probe)
	if got := blockersOf(t, probe, issuePID); !slices.Equal(got, []uint32{lock.pid}) {
		t.Fatalf("the issue waits on %v, want the raw lock on the entry", got)
	}
	var current workProject
	okJSON(t, kari, http.MethodGet, fmt.Sprintf("%s/%d", projectsPath, p.Id), nil, &current)
	ctx, cancel := context.WithTimeout(context.Background(), raceDeadline)
	defer cancel()
	turned := kari.Do(http.MethodPut, fmt.Sprintf("%s/%d", projectsPath, p.Id), map[string]any{
		"code": p.Code, "name": p.Name, "customerId": customer, "billingType": "fixed-price", "currency": "NOK",
		"fixedPriceAmount": 50000, "revision": current.Revision,
	}, modtest.Context(ctx))
	if turned.Status != http.StatusOK {
		t.Fatalf("the turn to fixed price while the issue waits = %d %s, want 200", turned.Status, turned.Body)
	}
	lock.release(t)
	doc := issuedNow(t, <-done)
	if got := readHour(t, kari, e.Id); got.Status != "invoiced" || got.InvoicedBy == nil || got.InvoicedBy.InvoiceId != doc.ID {
		t.Errorf("the entry = %s by %+v, want invoiced by %d: the window as it stands", got.Status, got.InvoicedBy, doc.ID)
	}
}
