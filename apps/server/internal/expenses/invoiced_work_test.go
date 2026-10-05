package expenses_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// This file is module-boundaries rule 10 on this module's side (invoices work
// design D1): the holder the invoices issue stamps expense lines through, and
// the credit note releases them through. Every test builds it the way a
// disabled module's Deps would — a logger and nothing else, Pool nil — so it
// can only ever write through the transaction it is handed, and stamps and
// releases real rows through a transaction the test rolls back.

// workDB is one migrated database for a holder test: the pool the test opens
// its own transactions on, and the URL a raw lock-holder connects with.
type workDB struct {
	pool *pgxpool.Pool
	url  string
}

func newWorkDB(t *testing.T) workDB {
	t.Helper()
	pool, url := testdb.Migrated(t)
	return workDB{pool: pool, url: url}
}

func (d workDB) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := d.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// line is one expense line seeded straight into expenses.entries, every
// field a holder or a billable read looks at within reach. The zero value
// is an approved, billable, priced outlay of 1100.00 NOK on the ordinary
// customer project, standalone and not invoiced.
type line struct {
	claim         *int64
	kind          string // "" is an outlay
	status        string // "" is approved
	notBillable   bool
	unpriced      bool
	billAmount    string // "" is 1100.00
	project       *int32 // nil is projectKraftVerket
	noProject     bool
	currency      string // "" is NOK
	date          string // "" is expenseDay
	gross, vat    string // "" is 1000.00 and no VAT
	supplier      string
	invoiceNumber string
	distanceKm    string
	markup        string
	billRatePerKm string
	// handInvoiced marks it invoiced by hand, with reference when one is
	// given; invoiceID and invoiceNumberStamp stamp it as the invoices issue
	// does.
	handInvoiced       bool
	reference          string
	invoiceID          int64
	invoiceNumberStamp int64
}

func orNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// seedLine inserts l and answers its id.
func seedLine(t *testing.T, pool *pgxpool.Pool, l line) int64 {
	t.Helper()
	kind := cmpOr(l.kind, "outlay")
	status := cmpOr(l.status, "approved")
	var bill any = cmpOr(l.billAmount, "1100.00")
	if l.unpriced {
		bill = nil
	}
	var project any = int32(projectKraftVerket)
	if l.project != nil {
		project = *l.project
	}
	if l.noProject {
		project = nil
	}
	var paidBy any
	switch kind {
	case "outlay":
		paidBy = "employee"
	case "supplier_invoice":
		paidBy = "company"
	}
	var invoicedAt any
	if l.handInvoiced || l.invoiceID != 0 {
		invoicedAt = time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	}
	var invoiceID, invoiceNumber any
	if l.invoiceID != 0 {
		invoiceID, invoiceNumber = l.invoiceID, l.invoiceNumberStamp
	}
	var id int64
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO expenses.entries (user_id, created_by_user_id, claim_id, kind, entry_date, description,
		    currency, gross_amount, vat_amount, paid_by, supplier, supplier_invoice_number, distance_km,
		    project_id, billable, markup_percent, bill_rate_per_km, bill_amount, status,
		    invoiced_at, invoice_reference, invoiced_invoice_id, invoiced_number, created_at, updated_at)
		VALUES ($1, $1, $2, $3, $4::date, 'Stillas', $5, $6::numeric, $7::numeric, $8, $9, $10, $11::numeric,
		    $12, $13, $14::numeric, $15::numeric, $16::numeric, $17,
		    $18, $19, $20, $21, now(), now())
		RETURNING id`,
		uuid.New(), l.claim, kind, cmpOr(l.date, expenseDay), cmpOr(l.currency, "NOK"),
		cmpOr(l.gross, "1000.00"), orNil(l.vat), paidBy, orNil(l.supplier), orNil(l.invoiceNumber),
		orNil(l.distanceKm), project, !l.notBillable, orNil(l.markup), orNil(l.billRatePerKm), bill, status,
		invoicedAt, orNil(l.reference), invoiceID, invoiceNumber).Scan(&id); err != nil {
		t.Fatalf("seed an expense line: %v", err)
	}
	return id
}

func cmpOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// seedClaim inserts a travel claim on the ordinary customer project in status
// and answers its id.
func seedClaim(t *testing.T, pool *pgxpool.Pool, status string) *int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO expenses.claims (user_id, created_by_user_id, purpose, departure_at, return_at,
		    project_id, status, created_at, updated_at)
		VALUES ($1, $1, 'Befaring', now(), now(), $2, $3, now(), now())
		RETURNING id`, uuid.New(), int32(projectKraftVerket), status).Scan(&id); err != nil {
		t.Fatalf("seed a travel claim: %v", err)
	}
	return &id
}

// logBuffer is a logger the release's warnings can be counted in.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) warnings() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Count(b.buf.String(), "level=WARN")
}

// holder is the expenses holder built as Compose builds it for a module that
// is switched off: from Deps carrying a logger and nothing else.
func holder(t *testing.T, logs *logBuffer) contracts.InvoicedWorkHolder {
	t.Helper()
	build := expenses.Module().InvoicedWork
	if build == nil {
		t.Fatal("the expenses module declares no invoiced work holder")
	}
	var logger *slog.Logger
	if logs != nil {
		logger = slog.New(slog.NewTextHandler(logs, nil))
	} else {
		logger = slog.New(slog.DiscardHandler)
	}
	return build(module.Deps{Logger: logger})
}

// begin opens the transaction a holder runs on; the test rolls it back.
func begin(t *testing.T, pool *pgxpool.Pool) pgx.Tx {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

var (
	issuedAt  = time.Date(2026, 10, 5, 10, 30, 0, 0, time.UTC)
	creditAt  = time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
	issuerID  = uuid.MustParse("6f1d0c1e-5b0a-4b8e-9d0e-1a2b3c4d5e6f")
	invoiceRf = contracts.InvoiceRef{
		ID: 7, Number: 10042, IssueDate: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		IssuedAt: issuedAt, IssuedBy: issuerID, IssuedByDisplay: "Kari Nordmann",
	}
	releaseRf = contracts.InvoiceRef{
		ID: 7, Number: 10042, IssueDate: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		IssuedAt: creditAt, IssuedBy: issuerID, IssuedByDisplay: "Kari Nordmann",
	}
)

// source is the line id as the draft read it: the zero line's billing facts.
func source(id int64) contracts.WorkSource {
	return contracts.WorkSource{
		Kind: contracts.WorkSourceExpense, ID: id, Revision: 1, ProjectID: projectKraftVerket,
		Currency: "NOK", Amount: "1100.00", ExpenseKind: "outlay",
	}
}

// stampOf is the invoiced stamp of one line as tx sees it.
type stampOf struct {
	invoicedAt        *time.Time
	invoicedBy        *uuid.UUID
	reference         *string
	invoiceID, number *int64
	revision          int32
	updatedAt         time.Time
}

func readStamp(t *testing.T, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id int64,
) stampOf {
	t.Helper()
	var s stampOf
	if err := q.QueryRow(context.Background(), `
		SELECT invoiced_at, invoiced_by_user_id, invoice_reference, invoiced_invoice_id, invoiced_number,
		       revision, updated_at
		FROM expenses.entries WHERE id = $1`, id).Scan(
		&s.invoicedAt, &s.invoicedBy, &s.reference, &s.invoiceID, &s.number, &s.revision, &s.updatedAt); err != nil {
		t.Fatalf("read expense %d's stamp: %v", id, err)
	}
	return s
}

// refusal is err as the holder's one refusal type, failing the test when it
// is anything else.
func refusal(t *testing.T, err error) *contracts.WorkSourceRefusal {
	t.Helper()
	var r *contracts.WorkSourceRefusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want a *contracts.WorkSourceRefusal", err)
	}
	return r
}

// TestInvoicedWork_StampsAtIssuedAtWithTheIssuer: the stamp is the issue's —
// its time, its issuer, its invoice — with no hand-typed reference, the
// revision one up, on a standalone line and on a travel claim's line alike.
func TestInvoicedWork_StampsAtIssuedAtWithTheIssuer(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	standalone := seedLine(t, d.pool, line{})
	claim := seedClaim(t, d.pool, "approved")
	claimLine := seedLine(t, d.pool, line{claim: claim, status: "draft"})
	tx := begin(t, d.pool)

	claimSource := source(claimLine)
	if err := holder(t, nil).MarkInvoiced(t.Context(), tx, invoiceRf,
		[]contracts.WorkSource{claimSource, source(standalone)}); err != nil {
		t.Fatalf("MarkInvoiced: %v", err)
	}
	for _, id := range []int64{standalone, claimLine} {
		s := readStamp(t, tx, id)
		switch {
		case s.invoicedAt == nil || !s.invoicedAt.Equal(issuedAt):
			t.Errorf("expense %d invoiced_at = %v, want the issue's %v", id, s.invoicedAt, issuedAt)
		case s.invoicedBy == nil || *s.invoicedBy != issuerID:
			t.Errorf("expense %d invoiced by %v, want the issuer", id, s.invoicedBy)
		case s.invoiceID == nil || *s.invoiceID != 7 || s.number == nil || *s.number != 10042:
			t.Errorf("expense %d invoice = %v/%v, want 7/10042", id, s.invoiceID, s.number)
		case s.reference != nil:
			t.Errorf("expense %d reference = %q, want none", id, *s.reference)
		case s.revision != 2:
			t.Errorf("expense %d revision = %d, want 2", id, s.revision)
		case !s.updatedAt.Equal(issuedAt):
			t.Errorf("expense %d updated_at = %v, want the issue's %v", id, s.updatedAt, issuedAt)
		}
	}
}

// TestInvoicedWork_JudgesAlreadyInvoicedFirst: a line already invoiced is
// named for that, whatever else is wrong with it — a per diem day marked by
// hand is source_already_invoiced, not "a per diem day is never invoiced" —
// and the detail names the invoice or the hand-typed reference.
func TestInvoicedWork_JudgesAlreadyInvoicedFirst(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	claim := seedClaim(t, d.pool, "approved")
	for _, c := range []struct {
		name   string
		line   line
		detail string
	}{
		{"a per diem day marked by hand", line{claim: claim, kind: "per_diem", notBillable: true, unpriced: true, handInvoiced: true}, "marked invoiced"},
		{"a line marked by hand with a reference", line{handInvoiced: true, reference: "F-118"}, `"F-118"`},
		{"a line on another invoice", line{status: "draft", invoiceID: 3, invoiceNumberStamp: 10017}, "invoice 10017"},
	} {
		id := seedLine(t, d.pool, c.line)
		tx := begin(t, d.pool)
		src := source(id)
		src.ExpenseKind = cmpOr(c.line.kind, "outlay")
		r := refusal(t, holder(t, nil).MarkInvoiced(t.Context(), tx, invoiceRf, []contracts.WorkSource{src}))
		if r.Code != contracts.SourceAlreadyInvoiced || !strings.Contains(r.Detail, c.detail) || r.Source.ID != id {
			t.Errorf("%s: refusal = %+v, want %s naming %s", c.name, r, contracts.SourceAlreadyInvoiced, c.detail)
		}
	}
}

// TestInvoicedWork_RefusesWhatIsNotInvoiceable: every reason invoicedRefusal
// has — and a line that is gone — is source_not_invoiceable, with the manual
// door's own message.
func TestInvoicedWork_RefusesWhatIsNotInvoiceable(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	submittedClaim := seedClaim(t, d.pool, "submitted")
	approvedClaim := seedClaim(t, d.pool, "approved")
	for _, c := range []struct {
		name string
		line line
		says string
	}{
		{"a per diem day", line{claim: approvedClaim, kind: "per_diem"}, "per diem"},
		{"a draft", line{status: "draft"}, "this one is draft"},
		{"a submitted line", line{status: "submitted"}, "this one is submitted"},
		{"a rejected line", line{status: "rejected"}, "this one is rejected"},
		{"a line of a submitted claim", line{claim: submittedClaim, status: "draft"}, "this one is submitted"},
		{"a line nobody bills", line{notBillable: true}, "billable"},
		{"an unpriced line", line{kind: "mileage", unpriced: true, distanceKm: "12.0"}, "priced"},
	} {
		id := seedLine(t, d.pool, c.line)
		tx := begin(t, d.pool)
		src := source(id)
		src.ExpenseKind = cmpOr(c.line.kind, "outlay")
		r := refusal(t, holder(t, nil).MarkInvoiced(t.Context(), tx, invoiceRf, []contracts.WorkSource{src}))
		if r.Code != contracts.SourceNotInvoiceable || !strings.Contains(r.Detail, c.says) {
			t.Errorf("%s: refusal = %+v, want %s saying %q", c.name, r, contracts.SourceNotInvoiceable, c.says)
		}
	}
	tx := begin(t, d.pool)
	r := refusal(t, holder(t, nil).MarkInvoiced(t.Context(), tx, invoiceRf, []contracts.WorkSource{source(987654)}))
	if r.Code != contracts.SourceNotInvoiceable {
		t.Errorf("a line that is gone: refusal = %+v, want %s", r, contracts.SourceNotInvoiceable)
	}
}

// TestInvoicedWork_UnchangedByAReimbursementsRevisionBump: the revision is
// display-only here. A payroll run bumps it and changes nothing billed, so a
// draft that read the line before the run still invoices it.
func TestInvoicedWork_UnchangedByAReimbursementsRevisionBump(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	id := seedLine(t, d.pool, line{})
	d.exec(t, `UPDATE expenses.entries SET reimbursed_at = now(), reimbursement_date = DATE '2026-04-30',
	    reimbursed_by_user_id = $2, revision = revision + 1 WHERE id = $1`, id, uuid.New())
	tx := begin(t, d.pool)
	if err := holder(t, nil).MarkInvoiced(t.Context(), tx, invoiceRf, []contracts.WorkSource{source(id)}); err != nil {
		t.Fatalf("MarkInvoiced after a reimbursement: %v, want the line stamped", err)
	}
	if s := readStamp(t, tx, id); s.revision != 3 || s.invoiceID == nil {
		t.Errorf("stamp = %+v, want invoice 7 at revision 3", s)
	}
}

// TestInvoicedWork_ChangedByEachBillingFact: the bill amount, the project,
// the currency and the kind are what an invoice line was built from; each one
// moved is source_changed. The amount is compared by value, so the same
// amount written to another scale is no change.
func TestInvoicedWork_ChangedByEachBillingFact(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	for _, c := range []struct {
		name    string
		mutate  func(*contracts.WorkSource)
		changed bool
	}{
		{"the bill amount", func(s *contracts.WorkSource) { s.Amount = "1100.01" }, true},
		{"the project", func(s *contracts.WorkSource) { s.ProjectID = projectEuro }, true},
		{"the currency", func(s *contracts.WorkSource) { s.Currency = "EUR" }, true},
		{"the kind", func(s *contracts.WorkSource) { s.ExpenseKind = "supplier_invoice" }, true},
		{"the same amount at another scale", func(s *contracts.WorkSource) { s.Amount = "1100" }, false},
		{"the same amount at a longer scale", func(s *contracts.WorkSource) { s.Amount = "1100.00000000" }, false},
		{"another revision", func(s *contracts.WorkSource) { s.Revision = 9 }, false},
	} {
		id := seedLine(t, d.pool, line{})
		tx := begin(t, d.pool)
		src := source(id)
		c.mutate(&src)
		err := holder(t, nil).MarkInvoiced(t.Context(), tx, invoiceRf, []contracts.WorkSource{src})
		if !c.changed {
			if err != nil {
				t.Errorf("%s: %v, want the line stamped", c.name, err)
			}
			continue
		}
		if r := refusal(t, err); r.Code != contracts.SourceChanged {
			t.Errorf("%s: refusal = %+v, want %s", c.name, r, contracts.SourceChanged)
		}
	}
}

// TestInvoicedWork_WritesNothingOnARefusal: the first refusal answers and no
// line is stamped, not even the ones judged fine before it.
func TestInvoicedWork_WritesNothingOnARefusal(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	fine := seedLine(t, d.pool, line{})
	draft := seedLine(t, d.pool, line{status: "draft"})
	tx := begin(t, d.pool)
	err := holder(t, nil).MarkInvoiced(t.Context(), tx, invoiceRf, []contracts.WorkSource{source(fine), source(draft)})
	if r := refusal(t, err); r.Source.ID != draft {
		t.Errorf("refusal = %+v, want the draft's", r)
	}
	if s := readStamp(t, tx, fine); s.invoicedAt != nil || s.revision != 1 {
		t.Errorf("the fine line = %+v, want it untouched", s)
	}
}

// TestInvoicedWork_ReleaseClearsTheFive: a credit note that returns the line
// takes back everything the stamp wrote — what UnmarkEntryInvoiced clears and
// the invoice's id and number — at the credit note's time, the revision one
// up again.
func TestInvoicedWork_ReleaseClearsTheFive(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	id := seedLine(t, d.pool, line{})
	tx := begin(t, d.pool)
	h := holder(t, nil)
	if err := h.MarkInvoiced(t.Context(), tx, invoiceRf, []contracts.WorkSource{source(id)}); err != nil {
		t.Fatalf("MarkInvoiced: %v", err)
	}
	if err := h.ReleaseInvoiced(t.Context(), tx, releaseRf, []contracts.WorkSource{source(id)}); err != nil {
		t.Fatalf("ReleaseInvoiced: %v", err)
	}
	s := readStamp(t, tx, id)
	if s.invoicedAt != nil || s.invoicedBy != nil || s.reference != nil || s.invoiceID != nil || s.number != nil {
		t.Errorf("after the release the stamp = %+v, want all five cleared", s)
	}
	if s.revision != 3 || !s.updatedAt.Equal(creditAt) {
		t.Errorf("after the release revision %d updated_at %v, want 3 at the credit note's %v", s.revision, s.updatedAt, creditAt)
	}
	// Ready again: the line is billable as it was before the invoice.
	var ready bool
	if err := tx.QueryRow(t.Context(), `
		SELECT expenses.ready_to_invoice(status, billable, kind, bill_amount) AND invoiced_at IS NULL
		FROM expenses.entries WHERE id = $1`, id).Scan(&ready); err != nil || !ready {
		t.Errorf("released line ready to invoice = %v (%v), want true", ready, err)
	}
}

// TestInvoicedWork_ReleaseTolerates: a line that does not carry the credited
// invoice's stamp — marked by hand, on another invoice, never invoiced, gone —
// is left exactly as it is and logged, and the release does not fail: a
// credit note is never blocked.
func TestInvoicedWork_ReleaseTolerates(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	byHand := seedLine(t, d.pool, line{handInvoiced: true, reference: "F-1"})
	other := seedLine(t, d.pool, line{invoiceID: 3, invoiceNumberStamp: 10017})
	never := seedLine(t, d.pool, line{})
	stamped := seedLine(t, d.pool, line{invoiceID: 7, invoiceNumberStamp: 10042})
	tx := begin(t, d.pool)
	logs := &logBuffer{}
	err := holder(t, logs).ReleaseInvoiced(t.Context(), tx, releaseRf, []contracts.WorkSource{
		source(byHand), source(other), source(never), source(987654), source(stamped),
	})
	if err != nil {
		t.Fatalf("ReleaseInvoiced: %v, want it to tolerate", err)
	}
	for _, id := range []int64{byHand, other, never} {
		if s := readStamp(t, tx, id); s.revision != 1 {
			t.Errorf("expense %d = %+v, want it untouched", id, s)
		}
	}
	if s := readStamp(t, tx, stamped); s.invoiceID != nil || s.revision != 2 {
		t.Errorf("the stamped line = %+v, want it released", s)
	}
	if got := logs.warnings(); got != 4 {
		t.Errorf("warnings = %d, want one per line left as it was (4)", got)
	}
}

// TestInvoicedWork_IgnoresThePeriodLock: a stamp is not an edit of the
// expense, so a line dated before the period lock is stamped and released
// all the same.
func TestInvoicedWork_IgnoresThePeriodLock(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	id := seedLine(t, d.pool, line{date: "2026-01-15"})
	d.exec(t, `UPDATE expenses.settings SET locked_before = DATE '2026-06-01'`)
	tx := begin(t, d.pool)
	h := holder(t, nil)
	if err := h.MarkInvoiced(t.Context(), tx, invoiceRf, []contracts.WorkSource{source(id)}); err != nil {
		t.Fatalf("MarkInvoiced before the lock date: %v", err)
	}
	if err := h.ReleaseInvoiced(t.Context(), tx, releaseRf, []contracts.WorkSource{source(id)}); err != nil {
		t.Fatalf("ReleaseInvoiced before the lock date: %v", err)
	}
	if s := readStamp(t, tx, id); s.revision != 3 {
		t.Errorf("revision = %d, want 3: stamped and released", s.revision)
	}
}

// TestInvoicedWork_BuiltFromADisabledModulesDeps: Compose builds the holder
// of every module given, a switched-off one too, from Deps with nothing in
// them but the logger — or not even that.
func TestInvoicedWork_BuiltFromADisabledModulesDeps(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	h := expenses.Module().InvoicedWork(module.Deps{})
	if kinds := h.Kinds(); len(kinds) != 1 || kinds[0] != contracts.WorkSourceExpense {
		t.Errorf("Kinds = %v, want [%s]", kinds, contracts.WorkSourceExpense)
	}
	id := seedLine(t, d.pool, line{})
	tx := begin(t, d.pool)
	if err := h.MarkInvoiced(t.Context(), tx, invoiceRf, []contracts.WorkSource{source(id)}); err != nil {
		t.Fatalf("MarkInvoiced: %v", err)
	}
	if err := h.ReleaseInvoiced(t.Context(), tx, releaseRf, []contracts.WorkSource{source(id + 1000)}); err != nil {
		t.Fatalf("ReleaseInvoiced with no logger: %v", err)
	}
	other := source(id)
	other.Kind = contracts.WorkSourceHours
	if err := h.MarkInvoiced(t.Context(), tx, invoiceRf, []contracts.WorkSource{other}); err == nil {
		t.Error("a time entry handed to the expenses holder was accepted, want an error")
	}
}

// TestInvoicedWork_MarksItsOwnLockedFlag: under the holder's locks the
// context is this module's locked transaction, in both directions, so the
// module's own contract-call hook would catch a directory read made there.
// Not parallel: the seam is the package's.
func TestInvoicedWork_MarksItsOwnLockedFlag(t *testing.T) {
	d := newWorkDB(t)
	id := seedLine(t, d.pool, line{})
	var seen []bool
	defer expenses.SetInvoicedWorkAfterLock(func(ctx context.Context) {
		seen = append(seen, expenses.InLockedTx(ctx))
	})()
	tx := begin(t, d.pool)
	h := holder(t, nil)
	if err := h.MarkInvoiced(t.Context(), tx, invoiceRf, []contracts.WorkSource{source(id)}); err != nil {
		t.Fatalf("MarkInvoiced: %v", err)
	}
	if err := h.ReleaseInvoiced(t.Context(), tx, releaseRf, []contracts.WorkSource{source(id)}); err != nil {
		t.Fatalf("ReleaseInvoiced: %v", err)
	}
	if len(seen) != 2 || !seen[0] || !seen[1] {
		t.Errorf("locked flag after the locks = %v, want [true true] (the mark, then the release)", seen)
	}
}

// rawLock holds a row lock from a connection of its own, outside every pool,
// until release is called.
type rawLock struct {
	conn *pgx.Conn
	tx   pgx.Tx
	pid  uint32
}

func lockRaw(t *testing.T, url, sql string, args ...any) *rawLock {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect the lock holder: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the lock holder: %v", err)
	}
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("take the raw lock: %v", err)
	}
	return &rawLock{conn: conn, tx: tx, pid: conn.PgConn().PID()}
}

func (l *rawLock) commit(t *testing.T) {
	t.Helper()
	if err := l.tx.Commit(context.Background()); err != nil {
		t.Fatalf("release the raw lock: %v", err)
	}
}

// waitBlockedBy waits until the backend pid waits on blocker, and fails the
// test when it never does.
func waitBlockedBy(t *testing.T, pool *pgxpool.Pool, pid, blocker uint32) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		if err := pool.QueryRow(context.Background(),
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

// rowHeld probes a row with FOR UPDATE NOWAIT from a connection of its own:
// true when somebody holds it (55P03), false when the probe gets it.
func rowHeld(t *testing.T, url, sql string, args ...any) bool {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect the probe: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the probe: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, sql, args...)
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), "55P03") {
		return true
	}
	t.Fatalf("probe: %v", err)
	return false
}

// parkBehindTheClaim runs one holder call on its own transaction while a raw
// lock holds the line's travel claim, and proves the order: the call waits on
// the claim, and meanwhile the line itself is held by nobody — the claim is
// taken before any line. Then the claim goes, and the call finishes.
func parkBehindTheClaim(t *testing.T, d workDB, claimID, lineID int64,
	call func(ctx context.Context, tx pgx.Tx) error,
) pgx.Tx {
	t.Helper()
	raw := lockRaw(t, d.url, `SELECT id FROM expenses.claims WHERE id = $1 FOR UPDATE`, claimID)
	tx := begin(t, d.pool)
	pid := tx.Conn().PgConn().PID()
	done := make(chan error, 1)
	go func() { done <- call(context.Background(), tx) }()
	waitBlockedBy(t, d.pool, pid, raw.pid)
	if rowHeld(t, d.url, `SELECT id FROM expenses.entries WHERE id = $1 FOR UPDATE NOWAIT`, lineID) {
		t.Error("the line is held while the call waits on its claim: the claim must be locked first")
	}
	raw.commit(t)
	if err := <-done; err != nil {
		t.Fatalf("the call after the claim was released: %v", err)
	}
	if !rowHeld(t, d.url, `SELECT id FROM expenses.entries WHERE id = $1 FOR UPDATE NOWAIT`, lineID) {
		t.Error("the line is not held after the call: it must be locked before the write")
	}
	return tx
}

// TestInvoicedWork_LocksClaimThenLines: the mark takes the module's order —
// the claim of a claim's line first, then the line.
func TestInvoicedWork_LocksClaimThenLines(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	claim := seedClaim(t, d.pool, "approved")
	id := seedLine(t, d.pool, line{claim: claim, status: "draft"})
	h := holder(t, nil)
	tx := parkBehindTheClaim(t, d, *claim, id, func(ctx context.Context, tx pgx.Tx) error {
		return h.MarkInvoiced(ctx, tx, invoiceRf, []contracts.WorkSource{source(id)})
	})
	if s := readStamp(t, tx, id); s.invoiceID == nil {
		t.Errorf("stamp = %+v, want the line stamped once the claim was free", s)
	}
}

// TestInvoicedWork_ReleaseLocksAsTheMarkDoes: the release takes the same
// locks in the same order before it writes.
func TestInvoicedWork_ReleaseLocksAsTheMarkDoes(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	claim := seedClaim(t, d.pool, "approved")
	id := seedLine(t, d.pool, line{claim: claim, status: "draft", invoiceID: 7, invoiceNumberStamp: 10042})
	h := holder(t, nil)
	tx := parkBehindTheClaim(t, d, *claim, id, func(ctx context.Context, tx pgx.Tx) error {
		return h.ReleaseInvoiced(ctx, tx, releaseRf, []contracts.WorkSource{source(id)})
	})
	if s := readStamp(t, tx, id); s.invoiceID != nil {
		t.Errorf("stamp = %+v, want the line released once the claim was free", s)
	}
}

// TestInvoicedWork_ALineMovedBetweenReadAndLockIsChanged: the holder reads
// which claim a line is on before it locks anything. Were the line to move
// between that read and its lock — no door of this module moves one, so the
// test does it by hand while the mark waits on the line — it would be judged
// under a claim the holder does not hold, so it is source_changed instead.
func TestInvoicedWork_ALineMovedBetweenReadAndLockIsChanged(t *testing.T) {
	t.Parallel()
	d := newWorkDB(t)
	id := seedLine(t, d.pool, line{})
	elsewhere := seedClaim(t, d.pool, "approved")
	raw := lockRaw(t, d.url, `SELECT id FROM expenses.entries WHERE id = $1 FOR UPDATE`, id)
	tx := begin(t, d.pool)
	pid := tx.Conn().PgConn().PID()
	h := holder(t, nil)
	done := make(chan error, 1)
	go func() {
		done <- h.MarkInvoiced(context.Background(), tx, invoiceRf, []contracts.WorkSource{source(id)})
	}()
	waitBlockedBy(t, d.pool, pid, raw.pid)
	if _, err := raw.tx.Exec(context.Background(), `UPDATE expenses.entries SET claim_id = $2 WHERE id = $1`, id, *elsewhere); err != nil {
		t.Fatalf("move the line: %v", err)
	}
	raw.commit(t)
	if r := refusal(t, <-done); r.Code != contracts.SourceChanged {
		t.Errorf("refusal = %+v, want %s", r, contracts.SourceChanged)
	}
}
