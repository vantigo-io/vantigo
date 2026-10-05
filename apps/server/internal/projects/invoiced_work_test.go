package projects_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/projects"
)

// The invoices issue's write into this module (module-boundaries rule 10,
// invoices work design D1): the holder that stamps billing milestones with the
// invoice that invoiced them, inside the issue's own transaction, and takes
// the stamp back in the credit note's. Every holder here is built the way a
// disabled module's is — from Deps carrying a logger and nothing else, Pool
// nil — so it can only ever write through the transaction it is handed, and
// every case stamps real rows through a transaction the test owns.

// syncLog is a logger's buffer a test can read while the holder may write.
type syncLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *syncLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// newInvoicedWork is the module's holder built from a disabled module's Deps:
// the logger only.
func newInvoicedWork(t *testing.T) (contracts.InvoicedWorkHolder, *syncLog) {
	t.Helper()
	build := projects.Module().InvoicedWork
	if build == nil {
		t.Fatal("the module declares no invoiced-work holder")
	}
	logs := &syncLog{}
	return build(module.Deps{Logger: slog.New(slog.NewJSONHandler(logs, nil))}), logs
}

// begin opens a transaction on the harness's pool that is rolled back when
// the test ends, whatever it did.
func begin(t *testing.T, h *modtest.Harness) pgx.Tx {
	t.Helper()
	tx, err := h.Pool().Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

// rolledBack runs fn in a transaction rolled back as soon as fn returns: one
// case of several against the same rows, whose locks must not outlive it.
func rolledBack(t *testing.T, h *modtest.Harness, fn func(tx pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fn(tx)
}

// commitWith runs fn in a transaction it commits: a stamp the next case
// starts from.
func commitWith(t *testing.T, h *modtest.Harness, fn func(tx pgx.Tx) error) {
	t.Helper()
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		t.Fatalf("in the committed transaction: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// readyMilestone is one milestone moved to ready through the API, the only
// way a milestone gets its ready stamps.
func readyMilestone(t *testing.T, c *modtest.Client, projectID int32, overrides map[string]any) milestoneJSON {
	t.Helper()
	return movedMilestone(t, c, createMilestone(t, c, projectID, overrides), milestoneReady, nil)
}

// sourceOf is m as a draft would have read it, amount the decimal text the
// billable read answered.
func sourceOf(m milestoneJSON, amount string) contracts.WorkSource {
	return contracts.WorkSource{
		Kind: contracts.WorkSourceMilestone, ID: int64(m.Id), Revision: m.Revision,
		ProjectID: m.ProjectId, Currency: "NOK", Amount: amount,
	}
}

// issuedInvoice is the stamp of an invoice issued an hour after the
// harness's clock — so a stamp at IssuedAt and one at the clock can never be
// mistaken for each other.
func issuedInvoice(h *modtest.Harness) contracts.InvoiceRef {
	return contracts.InvoiceRef{
		ID: 501, Number: 10042,
		IssueDate:       time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		IssuedAt:        h.Now().Add(time.Hour).Truncate(time.Microsecond),
		IssuedBy:        uuid.New(),
		IssuedByDisplay: "Ingrid Fakturerer",
	}
}

// creditNoteOf is ref as the credit note returning its lines hands it back:
// the original's id, number and date, the credit note's issue time and issuer.
func creditNoteOf(ref contracts.InvoiceRef) contracts.InvoiceRef {
	ref.IssuedAt = ref.IssuedAt.Add(24 * time.Hour)
	ref.IssuedBy = uuid.New()
	ref.IssuedByDisplay = "Kari Krediterer"
	return ref
}

// refusalOf is err as the one refusal a holder may answer, failing otherwise.
func refusalOf(t *testing.T, err error) *contracts.WorkSourceRefusal {
	t.Helper()
	var refusal *contracts.WorkSourceRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("error = %v, want a *contracts.WorkSourceRefusal", err)
	}
	return refusal
}

// milestoneRow is the stamp's columns as the transaction sees them.
type milestoneRow struct {
	Status            string
	InvoicedAt        *time.Time
	InvoicedBy        *uuid.UUID
	InvoiceDate       *time.Time
	InvoicedAmount    *string
	InvoiceReference  *string
	InvoicedInvoiceID *int64
	InvoicedNumber    *int64
	Amount            *string
	AmountCurrency    *string
	Percent           *string
	EverMoved         bool
	Revision          int32
	UpdatedAt         time.Time
	ReadyAt           *time.Time
}

func readMilestoneRow(t *testing.T, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id int32) milestoneRow {
	t.Helper()
	var r milestoneRow
	if err := q.QueryRow(context.Background(), `
		SELECT status, invoiced_at, invoiced_by_user_id, invoice_date, invoiced_amount::text, invoice_reference,
		       invoiced_invoice_id, invoiced_number, amount::text, amount_currency, percent::text,
		       ever_moved, revision, updated_at, ready_at
		FROM projects.billing_milestones WHERE id = $1`, id).Scan(
		&r.Status, &r.InvoicedAt, &r.InvoicedBy, &r.InvoiceDate, &r.InvoicedAmount, &r.InvoiceReference,
		&r.InvoicedInvoiceID, &r.InvoicedNumber, &r.Amount, &r.AmountCurrency, &r.Percent,
		&r.EverMoved, &r.Revision, &r.UpdatedAt, &r.ReadyAt); err != nil {
		t.Fatalf("read milestone %d: %v", id, err)
	}
	return r
}

// timelineEntry is a project's newest timeline entry as the transaction sees
// it.
type timelineEntry struct {
	EventType    string
	ActorUserID  *uuid.UUID
	ActorDisplay string
	OccurredAt   time.Time
	Payload      map[string]any
}

func lastTimelineEntry(t *testing.T, tx pgx.Tx, projectID int32) timelineEntry {
	t.Helper()
	var e timelineEntry
	var payload []byte
	if err := tx.QueryRow(context.Background(), `
		SELECT event_type, actor_user_id, actor_display, occurred_at, payload
		FROM projects.timeline_entries WHERE project_id = $1 ORDER BY id DESC LIMIT 1`, projectID).Scan(
		&e.EventType, &e.ActorUserID, &e.ActorDisplay, &e.OccurredAt, &payload); err != nil {
		t.Fatalf("read the newest timeline entry: %v", err)
	}
	if err := json.Unmarshal(payload, &e.Payload); err != nil {
		t.Fatalf("decode the payload %s: %v", payload, err)
	}
	return e
}

// The stamp: a ready milestone becomes invoiced by the issuer at the issue's
// own instant, carrying the invoice's id, number and date and no free-text
// reference, with its effective amount frozen exactly — 33.33 % of 3 750.30 is
// 1 249.97 — and one milestone-invoiced entry by the issuer, at IssuedAt,
// naming the invoice's number. The holder has no directory to read: the
// issuer's name is ref's, and the harness's own check fails any contract call
// made under the module's locked flag.
func TestInvoicedWork_StampsAReadyMilestoneAndFreezesItsAmount(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "IW0001", 3750.30)
	m := readyMilestone(t, c, project.Id, map[string]any{"name": "Andel", "amount": nil, "percent": 33.33})
	holder, _ := newInvoicedWork(t)
	ref := issuedInvoice(h)
	tx := begin(t, h)

	if err := holder.MarkInvoiced(context.Background(), tx, ref, []contracts.WorkSource{sourceOf(m, "1249.97")}); err != nil {
		t.Fatalf("MarkInvoiced: %v", err)
	}

	got := readMilestoneRow(t, tx, m.Id)
	if got.Status != milestoneInvoiced {
		t.Errorf("status = %q, want invoiced", got.Status)
	}
	if got.InvoicedAt == nil || !got.InvoicedAt.Equal(ref.IssuedAt) {
		t.Errorf("invoiced_at = %v, want the issue's own %v", got.InvoicedAt, ref.IssuedAt)
	}
	if got.InvoicedBy == nil || *got.InvoicedBy != ref.IssuedBy {
		t.Errorf("invoiced_by_user_id = %v, want the issuer %v", got.InvoicedBy, ref.IssuedBy)
	}
	if got.InvoiceDate == nil || !got.InvoiceDate.Equal(ref.IssueDate) {
		t.Errorf("invoice_date = %v, want the issue date %v", got.InvoiceDate, ref.IssueDate)
	}
	if got.InvoicedAmount == nil || *got.InvoicedAmount != "1249.97" {
		t.Errorf("invoiced_amount = %v, want 1249.97 frozen", got.InvoicedAmount)
	}
	if got.InvoiceReference != nil {
		t.Errorf("invoice_reference = %q, want none: the invoice is named by its id and number", *got.InvoiceReference)
	}
	if got.InvoicedInvoiceID == nil || *got.InvoicedInvoiceID != ref.ID || got.InvoicedNumber == nil || *got.InvoicedNumber != ref.Number {
		t.Errorf("invoiced_invoice_id/number = %v/%v, want %d/%d", got.InvoicedInvoiceID, got.InvoicedNumber, ref.ID, ref.Number)
	}
	if !got.EverMoved || got.Revision != m.Revision+1 {
		t.Errorf("ever_moved %v revision %d, want true and %d", got.EverMoved, got.Revision, m.Revision+1)
	}
	if !got.UpdatedAt.Equal(ref.IssuedAt) {
		t.Errorf("updated_at = %v, want the issue's own %v", got.UpdatedAt, ref.IssuedAt)
	}

	entry := lastTimelineEntry(t, tx, project.Id)
	if entry.EventType != "milestone-invoiced" {
		t.Fatalf("newest timeline entry = %q, want milestone-invoiced", entry.EventType)
	}
	if entry.ActorUserID == nil || *entry.ActorUserID != ref.IssuedBy || entry.ActorDisplay != ref.IssuedByDisplay {
		t.Errorf("actor = %v %q, want the issuer %v %q", entry.ActorUserID, entry.ActorDisplay, ref.IssuedBy, ref.IssuedByDisplay)
	}
	if !entry.OccurredAt.Equal(ref.IssuedAt) {
		t.Errorf("occurred_at = %v, want IssuedAt %v, never the clock", entry.OccurredAt, ref.IssuedAt)
	}
	if entry.Payload["invoiceNumber"] != float64(ref.Number) || entry.Payload["milestoneId"] != float64(m.Id) {
		t.Errorf("payload = %v, want the milestone and invoiceNumber %d", entry.Payload, ref.Number)
	}
	if _, ok := entry.Payload["amount"]; ok {
		t.Errorf("payload = %v carries an amount", entry.Payload)
	}
}

// Already invoiced is judged first, so a milestone marked invoiced by hand is
// named for what it is — not merely "not ready", which it also is — even when
// the draft's revision is stale too; and a milestone another invoice stamped
// names that invoice's number.
func TestInvoicedWork_JudgesAlreadyInvoicedFirst(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "IW0002")
	byHand := readyMilestone(t, c, project.Id, map[string]any{"name": "For hånd"})
	stale := sourceOf(byHand, "100000.00")
	movedMilestone(t, c, byHand, milestoneInvoiced, nil)
	stamped := readyMilestone(t, c, project.Id, map[string]any{"name": "Fakturert"})
	holder, _ := newInvoicedWork(t)
	ref := issuedInvoice(h)
	tx := begin(t, h)

	refusal := refusalOf(t, holder.MarkInvoiced(context.Background(), tx, ref, []contracts.WorkSource{stale}))
	if refusal.Code != contracts.SourceAlreadyInvoiced || refusal.Source.ID != int64(byHand.Id) {
		t.Errorf("a milestone invoiced by hand: %+v, want source_already_invoiced", refusal)
	}

	if err := holder.MarkInvoiced(context.Background(), tx, ref, []contracts.WorkSource{sourceOf(stamped, "100000.00")}); err != nil {
		t.Fatalf("MarkInvoiced: %v", err)
	}
	again := sourceOf(stamped, "100000.00")
	again.Revision++
	refusal = refusalOf(t, holder.MarkInvoiced(context.Background(), tx, ref, []contracts.WorkSource{again}))
	if refusal.Code != contracts.SourceAlreadyInvoiced || !strings.Contains(refusal.Detail, "10042") {
		t.Errorf("a milestone an invoice stamped: %+v, want source_already_invoiced naming invoice 10042", refusal)
	}
}

// Anything not ready is not invoiceable — planned, cancelled, gone, or a
// percent milestone whose fixed price is gone (a row only data written past
// the project's guard can leave behind) — and a refusal writes nothing, not
// even the stamps of the sources it had judged fine.
func TestInvoicedWork_RefusesWhatIsNotReady(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "IW0003", 400000)
	planned := createMilestone(t, c, project.Id, map[string]any{"name": "Planlagt"})
	cancelled := movedMilestone(t, c, createMilestone(t, c, project.Id, map[string]any{"name": "Kansellert"}), milestoneCancelled, nil)
	fine := readyMilestone(t, c, project.Id, map[string]any{"name": "Klar"})
	unpriced := readyMilestone(t, c, project.Id, map[string]any{"name": "Andel", "amount": nil, "percent": 25})
	h.Exec(t, `UPDATE projects.projects SET fixed_price_amount = NULL WHERE id = $1`, project.Id)
	gone := sourceOf(fine, "100000.00")
	gone.ID = 999999
	holder, _ := newInvoicedWork(t)
	ref := issuedInvoice(h)

	for _, tc := range []struct {
		name   string
		source contracts.WorkSource
	}{
		{"planned", sourceOf(planned, "100000.00")},
		{"cancelled", sourceOf(cancelled, "100000.00")},
		{"gone", gone},
		{"a percent of a fixed price that is gone", sourceOf(unpriced, "100000.00")},
	} {
		rolledBack(t, h, func(tx pgx.Tx) {
			refusal := refusalOf(t, holder.MarkInvoiced(context.Background(), tx, ref,
				[]contracts.WorkSource{sourceOf(fine, "100000.00"), tc.source}))
			if refusal.Code != contracts.SourceNotInvoiceable || refusal.Source.ID != tc.source.ID {
				t.Errorf("%s: %+v, want source_not_invoiceable for %d", tc.name, refusal, tc.source.ID)
			}
			if got := readMilestoneRow(t, tx, fine.Id); got.Status != milestoneReady || got.Revision != fine.Revision {
				t.Errorf("%s: the fine milestone is %s at revision %d, want nothing written", tc.name, got.Status, got.Revision)
			}
		})
	}
}

// What the draft read has moved on: a percent milestone after a fixed-price
// edit (its own revision unchanged — the amount moved with the project), a
// stale revision, another currency or another project. The amount is
// compared by value, so the same figure in another scale is no change.
func TestInvoicedWork_APercentMilestoneAfterAFixedPriceEditIsChanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "IW0004", 400000)
	m := readyMilestone(t, c, project.Id, map[string]any{"name": "Andel", "amount": nil, "percent": 25})
	drafted := sourceOf(m, "100000.00")
	putProject(t, c, project, map[string]any{"fixedPriceAmount": 800000})
	if got := getMilestone(t, c, m.Id).Revision; got != m.Revision {
		t.Fatalf("the price edit moved the milestone's revision to %d", got)
	}
	holder, _ := newInvoicedWork(t)
	ref := issuedInvoice(h)

	stale, otherCurrency, otherProject := sourceOf(m, "200000.00"), sourceOf(m, "200000.00"), sourceOf(m, "200000.00")
	stale.Revision--
	otherCurrency.Currency = "EUR"
	otherProject.ProjectID = amountProject(t, c, "IW0005").Id
	for _, tc := range []struct {
		name   string
		source contracts.WorkSource
	}{
		{"the amount the fixed price moved", drafted},
		{"a stale revision", stale},
		{"another currency", otherCurrency},
		{"another project", otherProject},
	} {
		rolledBack(t, h, func(tx pgx.Tx) {
			refusal := refusalOf(t, holder.MarkInvoiced(context.Background(), tx, ref, []contracts.WorkSource{tc.source}))
			if refusal.Code != contracts.SourceChanged {
				t.Errorf("%s: %+v, want source_changed", tc.name, refusal)
			}
		})
	}

	tx := begin(t, h)
	if err := holder.MarkInvoiced(context.Background(), tx, ref, []contracts.WorkSource{sourceOf(m, "200000")}); err != nil {
		t.Fatalf("200000 against 200000.00 is one amount: %v", err)
	}
	if got := readMilestoneRow(t, tx, m.Id).InvoicedAmount; got == nil || *got != "200000.00" {
		t.Errorf("invoiced_amount = %v, want the 200000.00 the price now gives", got)
	}
}

// A reorder moves no revision (where a milestone sits is not part of its
// form), so a draft taken before it still stamps.
func TestInvoicedWork_AReorderIsNoChange(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "IW0006")
	first := readyMilestone(t, c, project.Id, map[string]any{"name": "Først"})
	second := readyMilestone(t, c, project.Id, map[string]any{"name": "Sist"})
	if r := moveMilestone(t, c, second, 1); r.Status != http.StatusOK {
		t.Fatalf("reorder: status %d body %s", r.Status, r.Body)
	}
	holder, _ := newInvoicedWork(t)
	tx := begin(t, h)

	if err := holder.MarkInvoiced(context.Background(), tx, issuedInvoice(h),
		[]contracts.WorkSource{sourceOf(first, "100000.00"), sourceOf(second, "100000.00")}); err != nil {
		t.Errorf("MarkInvoiced after a reorder: %v", err)
	}
}

// rawConn is a connection of its own, outside the harness's pool — a raw lock
// holder or a NOWAIT probe.
func rawConn(t *testing.T, h *modtest.Harness) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), h.Deps().Config.DatabaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// rawLockProject holds the project's row FOR NO KEY UPDATE — the lock every
// guarded write here takes first — on a connection of its own until release
// is called.
func rawLockProject(t *testing.T, h *modtest.Harness, projectID int32) (pid uint32, release func()) {
	t.Helper()
	ctx := context.Background()
	conn := rawConn(t, h)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the raw lock: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM projects.projects WHERE id = $1 FOR NO KEY UPDATE`, projectID); err != nil {
		t.Fatalf("raw-lock project %d: %v", projectID, err)
	}
	return conn.PgConn().PID(), func() { _ = tx.Rollback(ctx) }
}

// parkedBehind waits until the backend pid is blocked by blocker, as
// pg_blocking_pids answers, and fails the test after ten seconds.
func parkedBehind(t *testing.T, h *modtest.Harness, pid, blocker uint32) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		if err := h.Pool().QueryRow(context.Background(),
			`SELECT $2::int = ANY(pg_blocking_pids($1::int))`, int32(pid), int32(blocker)).Scan(&blocked); err != nil {
			t.Fatalf("pg_blocking_pids: %v", err)
		}
		if blocked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("backend %d never waited on %d", pid, blocker)
}

// milestoneHeld probes the milestone's row FOR UPDATE NOWAIT from a
// connection of its own: false when the probe takes it (nobody holds it),
// true on 55P03 (somebody does).
func milestoneHeld(t *testing.T, h *modtest.Harness, id int32) bool {
	t.Helper()
	ctx := context.Background()
	tx, err := rawConn(t, h).Begin(ctx)
	if err != nil {
		t.Fatalf("begin the probe: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `SELECT id FROM projects.billing_milestones WHERE id = $1 FOR UPDATE NOWAIT`, id)
	var pgErr *pgconn.PgError
	switch {
	case err == nil:
		return false
	case errors.As(err, &pgErr) && pgErr.Code == "55P03":
		return true
	default:
		t.Fatalf("probe milestone %d: %v", id, err)
		return false
	}
}

// parkHolder runs call — one direction of the holder — on a transaction of
// the pool's, after a raw lock holds the milestone's project: it waits until
// the holder is parked behind that lock, probes the milestone, then lets the
// holder through and answers what the probe saw and what the holder returned.
func parkHolder(t *testing.T, h *modtest.Harness, projectID, milestoneID int32,
	call func(ctx context.Context, tx pgx.Tx) error) (held bool, err error) {
	t.Helper()
	ctx := context.Background()
	tx := begin(t, h)
	var pid uint32
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("the holder's backend: %v", err)
	}
	blocker, release := rawLockProject(t, h, projectID)
	done := make(chan error, 1)
	go func() { done <- call(ctx, tx) }()
	parkedBehind(t, h, pid, blocker)
	held = milestoneHeld(t, h, milestoneID)
	release()
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the holder never finished after the project's lock was released")
	}
	return held, err
}

// The lock order, proved without pg_locks (an uncontended row lock never shows
// there): with the project held by a raw lock, the holder parks on it — and a
// NOWAIT probe of the milestone meanwhile takes it, so nothing below the
// project is locked yet. Projects first, then their milestones.
func TestInvoicedWork_LocksProjectsThenMilestones(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "IW0007")
	m := readyMilestone(t, c, project.Id, nil)
	holder, _ := newInvoicedWork(t)
	ref := issuedInvoice(h)

	held, err := parkHolder(t, h, project.Id, m.Id, func(ctx context.Context, tx pgx.Tx) error {
		return holder.MarkInvoiced(ctx, tx, ref, []contracts.WorkSource{sourceOf(m, "100000.00")})
	})
	if held {
		t.Error("the milestone was locked while the holder waited on its project: milestones before projects")
	}
	if err != nil {
		t.Errorf("MarkInvoiced once the project was free: %v", err)
	}
}

// The release takes the same locks in the same order before it writes — the
// project parks it with its milestone still free — and then returns the
// milestone to ready: the five invoice columns and the invoice's id and number
// cleared, the ready stamps kept, the percent kept while the fixed price is
// there, at the credit note's issue time, with a milestone-invoice-undone
// entry by the credit note's issuer.
func TestInvoicedWork_ReleaseLocksAsTheMarkDoes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "IW0008", 400000)
	m := readyMilestone(t, c, project.Id, map[string]any{"name": "Andel", "amount": nil, "percent": 25})
	holder, _ := newInvoicedWork(t)
	ref := issuedInvoice(h)
	commitWith(t, h, func(tx pgx.Tx) error {
		return holder.MarkInvoiced(context.Background(), tx, ref, []contracts.WorkSource{sourceOf(m, "100000.00")})
	})
	credit := creditNoteOf(ref)

	var tx pgx.Tx
	held, err := parkHolder(t, h, project.Id, m.Id, func(ctx context.Context, in pgx.Tx) error {
		tx = in
		return holder.ReleaseInvoiced(ctx, in, credit, []contracts.WorkSource{sourceOf(m, "100000.00")})
	})
	if held {
		t.Error("the milestone was locked while the release waited on its project")
	}
	if err != nil {
		t.Fatalf("ReleaseInvoiced: %v", err)
	}

	got := readMilestoneRow(t, tx, m.Id)
	if got.Status != milestoneReady {
		t.Errorf("status = %q, want ready", got.Status)
	}
	if got.InvoicedAt != nil || got.InvoicedBy != nil || got.InvoiceDate != nil || got.InvoicedAmount != nil ||
		got.InvoiceReference != nil || got.InvoicedInvoiceID != nil || got.InvoicedNumber != nil {
		t.Errorf("milestone = %+v, want every invoice column cleared", got)
	}
	if got.ReadyAt == nil || got.Percent == nil || *got.Percent != "25.00" || got.Amount != nil {
		t.Errorf("milestone = %+v, want its ready stamp and its percent kept", got)
	}
	if got.Revision != m.Revision+2 || !got.UpdatedAt.Equal(credit.IssuedAt) {
		t.Errorf("revision %d updated_at %v, want %d and the credit note's %v", got.Revision, got.UpdatedAt, m.Revision+2, credit.IssuedAt)
	}
	entry := lastTimelineEntry(t, tx, project.Id)
	if entry.EventType != "milestone-invoice-undone" || entry.ActorDisplay != credit.IssuedByDisplay ||
		entry.ActorUserID == nil || *entry.ActorUserID != credit.IssuedBy || !entry.OccurredAt.Equal(credit.IssuedAt) {
		t.Errorf("entry = %+v, want milestone-invoice-undone by the credit note's issuer at its issue time", entry)
	}
	if _, ok := entry.Payload["convertedToAmount"]; ok || entry.Payload["invoiceNumber"] != float64(ref.Number) {
		t.Errorf("payload = %v, want the invoice's number and no conversion", entry.Payload)
	}
}

// A percent milestone whose fixed price is gone since it was invoiced is
// released as the manual undo converts it: an amount milestone carrying what
// was invoiced, in the project's currency, the percent cleared, and the entry
// saying so by a flag.
func TestInvoicedWork_ReleaseConvertsWhenTheFixedPriceIsGone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := fixedPriceProject(t, c, "IW0009", 400000)
	m := readyMilestone(t, c, project.Id, map[string]any{"name": "Andel", "amount": nil, "percent": 25})
	holder, _ := newInvoicedWork(t)
	ref := issuedInvoice(h)
	commitWith(t, h, func(tx pgx.Tx) error {
		return holder.MarkInvoiced(context.Background(), tx, ref, []contracts.WorkSource{sourceOf(m, "100000.00")})
	})
	putProject(t, c, project, map[string]any{"billingType": "time-and-materials", "fixedPriceAmount": nil})
	tx := begin(t, h)

	if err := holder.ReleaseInvoiced(context.Background(), tx, creditNoteOf(ref), []contracts.WorkSource{sourceOf(m, "100000.00")}); err != nil {
		t.Fatalf("ReleaseInvoiced: %v", err)
	}

	got := readMilestoneRow(t, tx, m.Id)
	if got.Status != milestoneReady || got.Amount == nil || *got.Amount != "100000.00" || got.Percent != nil ||
		got.AmountCurrency == nil || *got.AmountCurrency != "NOK" {
		t.Errorf("milestone = %+v, want ready at the amount 100000.00 NOK and no percent", got)
	}
	if entry := lastTimelineEntry(t, tx, project.Id); entry.Payload["convertedToAmount"] != true {
		t.Errorf("payload = %v, want convertedToAmount", entry.Payload)
	}
}

// A credit note is never blocked: a project since left without a currency —
// where the manual undo would refuse — still has its milestone released, back
// to ready, and a warning says the manual move would have refused.
func TestInvoicedWork_ReleaseOnAProjectWithoutCurrencyStillReleases(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "IW0010")
	m := readyMilestone(t, c, project.Id, nil)
	holder, logs := newInvoicedWork(t)
	ref := issuedInvoice(h)
	commitWith(t, h, func(tx pgx.Tx) error {
		return holder.MarkInvoiced(context.Background(), tx, ref, []contracts.WorkSource{sourceOf(m, "100000.00")})
	})
	h.Exec(t, `UPDATE projects.projects SET currency = NULL WHERE id = $1`, project.Id)
	tx := begin(t, h)

	if err := holder.ReleaseInvoiced(context.Background(), tx, creditNoteOf(ref), []contracts.WorkSource{sourceOf(m, "100000.00")}); err != nil {
		t.Fatalf("ReleaseInvoiced refused: %v", err)
	}
	if got := readMilestoneRow(t, tx, m.Id); got.Status != milestoneReady || got.InvoicedInvoiceID != nil {
		t.Errorf("milestone = %+v, want released to ready", got)
	}
	if !strings.Contains(logs.String(), "manual undo would refuse") || !strings.Contains(logs.String(), `"level":"WARN"`) {
		t.Errorf("logs = %s, want a warning that the manual undo would have refused", logs.String())
	}
}

// A source that no longer carries the invoice's stamp — never stamped, stamped
// by another invoice, or gone — is left exactly as it is, with one warning
// each: the release tolerates it rather than blocking the credit note.
func TestInvoicedWork_ReleaseTolerates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "IW0011")
	ready := readyMilestone(t, c, project.Id, map[string]any{"name": "Klar"})
	other := readyMilestone(t, c, project.Id, map[string]any{"name": "Annen faktura"})
	holder, logs := newInvoicedWork(t)
	ref := issuedInvoice(h)
	otherRef := ref
	otherRef.ID, otherRef.Number = 502, 10043
	commitWith(t, h, func(tx pgx.Tx) error {
		return holder.MarkInvoiced(context.Background(), tx, otherRef, []contracts.WorkSource{sourceOf(other, "100000.00")})
	})
	gone := sourceOf(ready, "100000.00")
	gone.ID = 999999
	tx := begin(t, h)

	if err := holder.ReleaseInvoiced(context.Background(), tx, creditNoteOf(ref),
		[]contracts.WorkSource{sourceOf(ready, "100000.00"), sourceOf(other, "100000.00"), gone}); err != nil {
		t.Fatalf("ReleaseInvoiced: %v", err)
	}
	if got := readMilestoneRow(t, tx, ready.Id); got.Status != milestoneReady || got.Revision != ready.Revision {
		t.Errorf("the never-stamped milestone = %+v, want it untouched", got)
	}
	if got := readMilestoneRow(t, tx, other.Id); got.Status != milestoneInvoiced || got.InvoicedNumber == nil || *got.InvoicedNumber != 10043 {
		t.Errorf("another invoice's milestone = %+v, want its stamp kept", got)
	}
	if n := strings.Count(logs.String(), "no longer carries the invoice's stamp"); n != 3 {
		t.Errorf("%d warnings, want one per tolerated source: %s", n, logs.String())
	}
}

// The holder needs nothing but a logger — no pool, no clock, no directory —
// so a module switched off still stamps and releases its rows; and it claims
// exactly the milestone kind.
func TestInvoicedWork_BuiltFromADisabledModulesDeps(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "IW0012")
	m := readyMilestone(t, c, project.Id, nil)
	holder, _ := newInvoicedWork(t)
	ref := issuedInvoice(h)
	tx := begin(t, h)

	if got := holder.Kinds(); !slices.Equal(got, []contracts.WorkSourceKind{contracts.WorkSourceMilestone}) {
		t.Errorf("Kinds = %v, want [projects.milestone]", got)
	}
	if err := holder.MarkInvoiced(context.Background(), tx, ref, []contracts.WorkSource{sourceOf(m, "100000.00")}); err != nil {
		t.Fatalf("MarkInvoiced: %v", err)
	}
	if err := holder.ReleaseInvoiced(context.Background(), tx, creditNoteOf(ref), []contracts.WorkSource{sourceOf(m, "100000.00")}); err != nil {
		t.Fatalf("ReleaseInvoiced: %v", err)
	}
	if got := readMilestoneRow(t, tx, m.Id); got.Status != milestoneReady || got.Revision != m.Revision+2 {
		t.Errorf("milestone = %+v, want stamped and released", got)
	}
}

// Both directions mark their context with the module's own locked flag, so
// the module's contract-call hook (TestMain) would catch a directory read
// made under the holder's locks. Not parallel: the seam is package-level.
func TestInvoicedWork_MarksItsOwnLockedFlag(t *testing.T) {
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "IW0013")
	m := readyMilestone(t, c, project.Id, nil)
	holder, _ := newInvoicedWork(t)
	ref := issuedInvoice(h)
	var seen []bool
	projects.SetInvoicedWorkAfterLock(func(ctx context.Context) { seen = append(seen, projects.InLockedTx(ctx)) })
	t.Cleanup(func() { projects.SetInvoicedWorkAfterLock(nil) })
	tx := begin(t, h)

	if err := holder.MarkInvoiced(context.Background(), tx, ref, []contracts.WorkSource{sourceOf(m, "100000.00")}); err != nil {
		t.Fatalf("MarkInvoiced: %v", err)
	}
	if err := holder.ReleaseInvoiced(context.Background(), tx, creditNoteOf(ref), []contracts.WorkSource{sourceOf(m, "100000.00")}); err != nil {
		t.Fatalf("ReleaseInvoiced: %v", err)
	}
	if !slices.Equal(seen, []bool{true, true}) {
		t.Errorf("InLockedTx after the locks = %v, want true on the mark and on the release", seen)
	}
}

// Compose puts the module's two invoicing providers on every module's Deps:
// its holder in the many-provider InvoicedWork slot, and the billable
// milestones read in its single slot — each the module's own, proved by
// what it answers.
func TestModule_ComposesItsInvoicedWorkAndBillableMilestones(t *testing.T) {
	t.Parallel()
	var got module.Deps
	capture := module.Module{
		Name: "customers",
		Mount: func(d module.Deps) (http.Handler, error) {
			got = d
			return http.NotFoundHandler(), nil
		},
	}

	newHarness(t, modtest.WithModule(capture))

	if len(got.InvoicedWork) != 1 || !slices.Equal(got.InvoicedWork[0].Kinds(), []contracts.WorkSourceKind{contracts.WorkSourceMilestone}) {
		t.Errorf("Deps.InvoicedWork = %v, want projects' one milestone holder", got.InvoicedWork)
	}
	if got.BillableMilestones == nil {
		t.Fatal("Deps.BillableMilestones is nil, want projects' read")
	}
	if _, err := got.BillableMilestones.BillableMilestones(context.Background(), contracts.BillableRequest{IDs: []int64{1}}); err != nil {
		t.Errorf("the composed read: %v", err)
	}
}

// The manual door: undoing by hand the invoicing of a milestone the Invoices
// module stamped answers 409 invoiced_by_invoices naming the invoice — after
// the revision, which still answers its own code-less 409 first — and the
// milestone says so on the wire, without offering the undo.
func TestMilestoneStatus_AnInvoicesStampIsA409(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "IW0014")
	m := readyMilestone(t, c, project.Id, nil)
	holder, _ := newInvoicedWork(t)
	ref := issuedInvoice(h)
	commitWith(t, h, func(tx pgx.Tx) error {
		return holder.MarkInvoiced(context.Background(), tx, ref, []contracts.WorkSource{sourceOf(m, "100000.00")})
	})

	stamped := getMilestone(t, c, m.Id)
	if stamped.InvoicedByInvoice == nil || *stamped.InvoicedByInvoice != (milestoneInvoiceJSON{InvoiceId: ref.ID, Number: ref.Number}) {
		t.Errorf("invoicedByInvoice = %v, want invoice %d number %d", stamped.InvoicedByInvoice, ref.ID, ref.Number)
	}
	if stamped.Capabilities.CanUndoInvoiced {
		t.Error("canUndoInvoiced is true on a milestone only a credit note may take back")
	}

	type problem struct {
		Code          *string `json:"code"`
		InvoiceId     *int64  `json:"invoiceId"`
		InvoiceNumber *int64  `json:"invoiceNumber"`
	}
	staleRevision := stamped
	staleRevision.Revision--
	r := moveMilestoneStatus(t, c, staleRevision, milestoneReady, nil)
	var stale problem
	r.JSON(&stale)
	if r.Status != http.StatusConflict || stale.Code != nil {
		t.Errorf("a stale revision: status %d body %s, want the code-less revision 409", r.Status, r.Body)
	}

	r = moveMilestoneStatus(t, c, stamped, milestoneReady, nil)
	var refused problem
	r.JSON(&refused)
	if r.Status != http.StatusConflict || refused.Code == nil || *refused.Code != "invoiced_by_invoices" ||
		refused.InvoiceId == nil || *refused.InvoiceId != ref.ID || refused.InvoiceNumber == nil || *refused.InvoiceNumber != ref.Number {
		t.Errorf("the undo: status %d body %s, want 409 invoiced_by_invoices naming invoice %d", r.Status, r.Body, ref.Number)
	}
	if after := getMilestone(t, c, m.Id); after.Status != milestoneInvoiced || after.Revision != stamped.Revision {
		t.Errorf("milestone = %s at revision %d, want it untouched", after.Status, after.Revision)
	}
}

// A milestone marked invoiced by hand carries no invoice and undoes as it
// always did.
func TestMilestoneStatus_AHandStampUndoesAsBefore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, _ := signIn(t, h, "projects:create")
	project := amountProject(t, c, "IW0015")
	m := movedMilestone(t, c, readyMilestone(t, c, project.Id, nil), milestoneInvoiced,
		map[string]any{"invoiceReference": "F-2026-0042"})

	if m.InvoicedByInvoice != nil || !m.Capabilities.CanUndoInvoiced {
		t.Errorf("invoicedByInvoice %v canUndoInvoiced %v, want none and true", m.InvoicedByInvoice, m.Capabilities.CanUndoInvoiced)
	}
	if undone := movedMilestone(t, c, m, milestoneReady, nil); undone.Status != milestoneReady {
		t.Errorf("status = %q, want ready", undone.Status)
	}
}
