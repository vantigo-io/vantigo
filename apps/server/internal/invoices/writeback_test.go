package invoices_test

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The write-back at the issue (invoices work design D1, module-boundaries
// rule 10): the holders called on the issue's own transaction, after every
// check and the number and before the document is written, once per kind in
// the cross-module lock order; a refusal answered with its line and source,
// rolling the number back; the reads they need made before the transaction,
// and nothing but a holder's command made under it. The held rows are planted
// by SQL on drafts made through the API, as the sources tests do.

const project42 = 42

var (
	kindHours     = contracts.WorkSourceHours
	kindExpense   = contracts.WorkSourceExpense
	kindMilestone = contracts.WorkSourceMilestone
)

// workReady is an installation ready to issue work: a complete seller, the
// holders of kinds (every kind when none is named) and, unless projects is
// nil, the project directory.
func workReady(t *testing.T, holders *fakeHolders, projects *fakeProjects, kinds ...contracts.WorkSourceKind) *harness {
	t.Helper()
	opts := holders.options(kinds...)
	if projects != nil {
		opts = append(opts, modtest.WithProjects(projects))
	}
	h := newHarness(t, opts...)
	saveSeller(t, h, completeSeller(1))
	return h
}

// plantedDraft is an Acme draft of the sourced draft's three lines holding
// rows.
func plantedDraft(t *testing.T, h *harness, rows ...planted) invoiceJSON {
	t.Helper()
	d := createDraft(t, h, draftBody(customerAcme,
		line("Konsulenttimer", 7.5, 1200, vat25), line("Kjøregodtgjørelse", 1, 450, vat25), line("Milepæl", 1, 10000, vat25)))
	for _, p := range rows {
		plantSource(t, h, d.ID, p)
	}
	return d
}

// idsOf is the ids a command was handed, in its order.
func idsOf(sources []contracts.WorkSource) []int64 {
	out := []int64{}
	for _, s := range sources {
		out = append(out, s.ID)
	}
	return out
}

// noHolderCalled fails t when any holder was called.
func noHolderCalled(t *testing.T, holders *fakeHolders) {
	t.Helper()
	if got := holders.calledOrder(); len(got) != 0 {
		t.Errorf("holders called = %v, want none", got)
	}
}

// statesOf is a document's line sources' states, in insertion order.
func statesOf(t *testing.T, h *harness, id int64) []string {
	t.Helper()
	return modtest.One[[]string](t, h.Harness,
		`SELECT coalesce(array_agg(state ORDER BY id), '{}') FROM invoices.line_sources WHERE invoice_id = $1`, id)
}

func allAre(states []string, state string) bool {
	return len(states) > 0 && !slices.ContainsFunc(states, func(s string) bool { return s != state })
}

// Each kind's holder is called once, Projects then Expenses then Time — the
// cross-module lock order, whatever order they were composed in — after the
// number was allocated and before anything of the issue was written: the
// document is still a draft and no line's VAT snapshot is set when each one
// runs.
func TestIssue_CallsEachHolderOnceInTheLockOrder(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	inv := issued(t, h, sourcedDraft(t, h).ID)

	if got := holders.calledOrder(); !slices.Equal(got, contracts.InvoicedWorkOrder) {
		t.Fatalf("holders called = %v, want %v", got, contracts.InvoicedWorkOrder)
	}
	want := map[contracts.WorkSourceKind][]int64{kindMilestone: {milestone}, kindExpense: {mileage}, kindHours: {hourOne, hourTwo}}
	for _, k := range contracts.InvoicedWorkOrder {
		calls := holders.callsOf(k)
		if len(calls) != 1 {
			t.Errorf("%s: %d calls, want 1", k, len(calls))
			continue
		}
		c := calls[0]
		if c.op != "mark" || c.ref.Number != *inv.Number || !slices.Equal(idsOf(c.sources), want[k]) {
			t.Errorf("%s: %s of %v with number %d, want mark of %v with %d", k, c.op, idsOf(c.sources), c.ref.Number, want[k], *inv.Number)
		}
		if c.status != "draft" || c.snapshotted != 0 {
			t.Errorf("%s ran on a document %s with %d lines snapshotted, want a draft with none", k, c.status, c.snapshotted)
		}
	}
}

// The holders ride the issue's own transaction: the transaction id each one
// reads through the pgx.Tx it is handed is the one the issue's own hook reads
// through the issue's. Not parallel: the hook is the package's.
func TestIssue_TheHoldersRideTheIssuesTransaction(t *testing.T) {
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	draft := sourcedDraft(t, h)
	var mu sync.Mutex
	var issueXact string
	restore := invoices.SetIssueAfterAllocation(func(ctx context.Context, tx pgx.Tx, id int64) error {
		if id != draft.ID {
			return nil
		}
		var xact string
		err := tx.QueryRow(ctx, `SELECT pg_current_xact_id()::text`).Scan(&xact)
		mu.Lock()
		issueXact = xact
		mu.Unlock()
		return err
	})
	defer restore()

	issued(t, h, draft.ID)
	mu.Lock()
	defer mu.Unlock()
	if issueXact == "" {
		t.Fatal("the issue's hook read no transaction id")
	}
	for _, k := range contracts.InvoicedWorkOrder {
		calls := holders.callsOf(k)
		if len(calls) != 1 || calls[0].xact != issueXact || !calls[0].locked {
			t.Errorf("%s: calls %+v, want one in transaction %s, marked locked", k, calls, issueXact)
		}
	}
}

// The ref every holder is handed: the invoice's id, its number and issue
// date, the issuer and their name as the user directory knows them, and
// IssuedAt — the issue's own clock, read once and also the document's
// issued_at, though the clock moves while the holders run.
func TestIssue_TheRefHasTheNumberTheDateTheIssuerAndIssuedAt(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	draft := sourcedDraft(t, h)
	holders.duringCall(func() { h.Advance(time.Minute) })
	c, userID := h.SignInUser(t, "invoices:access", "invoices:issue")
	start := h.Now()

	res := c.Do(http.MethodPost, issuePath(draft.ID), map[string]any{})
	if res.Status != http.StatusOK {
		t.Fatalf("issue = %d %s, want 200", res.Status, res.Body)
	}
	var inv invoiceJSON
	res.JSON(&inv)
	display := modtest.One[string](t, h.Harness, `SELECT display_name FROM identity.users WHERE id = $1`, userID)
	issuedAt := modtest.One[time.Time](t, h.Harness, `SELECT issued_at FROM invoices.invoices WHERE id = $1`, draft.ID)
	if !issuedAt.Equal(start) {
		t.Errorf("issued_at = %v, want %v: the clock is read once", issuedAt, start)
	}
	for _, k := range contracts.InvoicedWorkOrder {
		calls := holders.callsOf(k)
		if len(calls) != 1 {
			t.Fatalf("%s: %d calls, want 1", k, len(calls))
		}
		ref := calls[0].ref
		if ref.ID != draft.ID || ref.Number != *inv.Number || ref.IssueDate.Format(time.DateOnly) != *inv.IssueDate ||
			!ref.IssuedAt.Equal(start) || ref.IssuedBy != userID || ref.IssuedByDisplay != display || display == "" {
			t.Errorf("%s: ref = %+v, want invoice %d number %d of %s at %v by %s (%q)", k, ref, draft.ID, *inv.Number,
				*inv.IssueDate, start, userID, display)
		}
	}
	// The sources as the draft holds them: amounts exact, an expense's kind.
	type want struct {
		revision int32
		amount   string
		subkind  string
	}
	wants := map[int64]want{hourOne: {2, "4800", ""}, hourTwo: {1, "4200", ""}, mileage: {3, "450", "mileage"}, milestone: {1, "10000", ""}}
	for _, k := range contracts.InvoicedWorkOrder {
		for _, s := range holders.callsOf(k)[0].sources {
			w := wants[s.ID]
			amount, ok := new(big.Rat).SetString(s.Amount)
			wantAmount, _ := new(big.Rat).SetString(w.amount)
			if s.Kind != k || s.Revision != w.revision || s.ProjectID != project41 || s.Currency != "NOK" ||
				!ok || amount.Cmp(wantAmount) != 0 || s.ExpenseKind != w.subkind {
				t.Errorf("%s %d = %+v, want revision %d, project 41, NOK, %s, kind %q", k, s.ID, s, w.revision, w.amount, w.subkind)
			}
		}
	}
}

// A holder's refusal is the issue's 409 with the holder's code, the first
// line holding the source and the source itself; the number rolls back — the
// next issue takes it — and no line source moves.
func TestIssue_ASourceRefusalIsA409WithItsLineAndSource(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	draft := sourcedDraft(t, h)
	before := counterNext(t, h)

	for _, tc := range []struct {
		kind     contracts.WorkSourceKind
		id       int64
		code     string
		position int32
	}{
		{kindHours, hourTwo, contracts.SourceChanged, 1},
		{kindExpense, mileage, contracts.SourceNotInvoiceable, 2},
		{kindMilestone, milestone, contracts.SourceAlreadyInvoiced, 3},
	} {
		holders.refuseSource(tc.kind, tc.id, tc.code)
		p := refusedWith(t, h, draft.ID, "", tc.code)
		holders.refuseSource(tc.kind, tc.id, "")
		if p.LinePosition == nil || *p.LinePosition != tc.position || p.SourceKind == nil || *p.SourceKind != string(tc.kind) ||
			p.SourceID == nil || *p.SourceID != tc.id {
			t.Errorf("%s: line %v, source %v %v; want line %d, %s %d", tc.code, p.LinePosition, p.SourceKind, p.SourceID, tc.position, tc.kind, tc.id)
		}
		if n := counterNext(t, h); n != before {
			t.Errorf("%s: counter next = %d, want %d: the number rolled back", tc.code, n, before)
		}
		if states := statesOf(t, h, draft.ID); !allAre(states, "held") {
			t.Errorf("%s: states = %v, want every source still held", tc.code, states)
		}
	}
	if inv := issued(t, h, draft.ID); *inv.Number != 1 {
		t.Errorf("the issue after the refusals = %d, want 1", *inv.Number)
	}
}

// Anything else a holder answers is a failure: a 500, the issue rolled back
// with its number, and no holder after it called.
func TestIssue_AHolderErrorIsA500AndRollsBack(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	draft := sourcedDraft(t, h)
	before := counterNext(t, h)
	holders.failWith(kindExpense, errors.New("injected"))

	res := issuer(t, h).Do(http.MethodPost, issuePath(draft.ID), map[string]any{}, modtest.SkipContract("a holder's failure answers the undeclared 500"))
	if res.Status != http.StatusInternalServerError {
		t.Fatalf("a holder's failure = %d %s, want 500", res.Status, res.Body)
	}
	if calls := holders.callsOf(kindHours); len(calls) != 0 {
		t.Errorf("time's holder after the failure = %d calls, want none", len(calls))
	}
	if n := counterNext(t, h); n != before {
		t.Errorf("counter next = %d, want %d", n, before)
	}
	if inv := getInvoice(t, h, draft.ID); inv.Status != "draft" || !allAre(statesOf(t, h, draft.ID), "held") {
		t.Errorf("after the failure: %s with %v, want a draft holding its work", inv.Status, statesOf(t, h, draft.ID))
	}
	holders.failWith(kindExpense, nil)
	if inv := issued(t, h, draft.ID); *inv.Number != 1 {
		t.Errorf("the issue after the failure = %d, want 1", *inv.Number)
	}
}

// A held source whose kind no holder claims is a composition bug: the issue
// fails closed — a 500 and an error log — before any number, never a skipped
// stamp.
func TestIssue_AnUnclaimedKindIsA500AndLogged(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects(), kindMilestone, kindExpense)
	draft := sourcedDraft(t, h)
	before := counterNext(t, h)

	res := issuer(t, h).Do(http.MethodPost, issuePath(draft.ID), map[string]any{}, modtest.SkipContract("an unclaimed kind answers the undeclared 500"))
	if res.Status != http.StatusInternalServerError {
		t.Fatalf("an unclaimed kind = %d %s, want 500", res.Status, res.Body)
	}
	if logs := h.Logs(); !strings.Contains(logs, `"level":"ERROR"`) || !strings.Contains(logs, "time.entry") {
		t.Errorf("logs = %s, want an error naming time.entry", logs)
	}
	noHolderCalled(t, holders)
	if n := counterNext(t, h); n != before {
		t.Errorf("counter next = %d, want %d", n, before)
	}
}

// A held source whose project no longer bills the draft's customer — moved to
// another, or gone — refuses the issue before a number exists, naming the
// first line holding a source of it and that source.
func TestIssue_SourceCustomerChangedBeforeANumber(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	projects := newFakeProjects()
	h := workReady(t, holders, projects)
	draft := plantedDraft(t, h,
		planted{position: 1, kind: "time.entry", id: hourOne, revision: 2, quantity: "4.00", amount: "4800", date: "2026-09-01"},
		planted{position: 3, kind: "projects.milestone", id: milestone, revision: 1, quantity: "1", amount: "10000", date: "2026-09-05", project: project42})
	before := counterNext(t, h)

	projects.edit(project42, func(p *contracts.ProjectEntry) { p.CustomerID = ptrTo(int32(customerNoTerms)) })
	for _, step := range []string{"moved to another customer", "gone"} {
		p := refusedWith(t, h, draft.ID, "", "source_customer_changed")
		if p.LinePosition == nil || *p.LinePosition != 3 || p.SourceKind == nil || *p.SourceKind != string(kindMilestone) ||
			p.SourceID == nil || *p.SourceID != milestone {
			t.Errorf("%s: line %v, source %v %v; want line 3, the milestone", step, p.LinePosition, p.SourceKind, p.SourceID)
		}
		if n := counterNext(t, h); n != before {
			t.Errorf("%s: counter next = %d, want %d: no number taken", step, n, before)
		}
		projects.drop(project42)
	}
	noHolderCalled(t, holders)
}

// With Projects switched off, a draft holding work cannot be judged and the
// issue fails closed before a number; a draft holding none issues as ever.
func TestIssue_ProjectsUnavailableFailsClosed(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, nil)
	draft := sourcedDraft(t, h)
	before := counterNext(t, h)

	refusedWith(t, h, draft.ID, "", "projects_unavailable")
	noHolderCalled(t, holders)
	if n := counterNext(t, h); n != before {
		t.Errorf("counter next = %d, want %d", n, before)
	}
	if inv := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID); *inv.Number != 1 {
		t.Errorf("a draft without work = number %d, want 1", *inv.Number)
	}
}

// A change to the draft's work between the reads before the transaction and
// its lock — a source moved to another line, or dropped — is invoice_changed,
// and no holder is called; the next issue judges what the draft holds then.
// Not parallel: the hook is the package's.
func TestIssue_ASaveSlippedBetweenIsInvoiceChanged(t *testing.T) {
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	draft := sourcedDraft(t, h)
	before := counterNext(t, h)
	var step atomic.Int32
	slips := map[int32]string{
		// hour two moves to line 2, its revision and amount as they were.
		1: `WITH gone AS (DELETE FROM invoices.line_sources WHERE invoice_id = $1 AND source_id = 502 RETURNING *)
			INSERT INTO invoices.line_sources (line_id, invoice_id, source_kind, source_id, source_revision, source_subkind,
				project_id, quantity, amount, currency, source_date)
			SELECT l.id, g.invoice_id, g.source_kind, g.source_id, g.source_revision, g.source_subkind, g.project_id,
				g.quantity, g.amount, g.currency, g.source_date
			FROM gone g JOIN invoices.lines l ON l.invoice_id = g.invoice_id AND l.position = 2`,
		// the mileage is dropped.
		2: `DELETE FROM invoices.line_sources WHERE invoice_id = $1 AND source_id = 601`,
	}
	restore := invoices.SetIssueBeforeLock(func(ctx context.Context, id int64) {
		if id != draft.ID {
			return
		}
		if sql, ok := slips[step.Load()]; ok {
			if _, err := h.Pool().Exec(ctx, sql, id); err != nil {
				t.Errorf("slip %d: %v", step.Load(), err)
			}
		}
	})
	defer restore()

	for _, s := range []int32{1, 2} {
		step.Store(s)
		refusedWith(t, h, draft.ID, "", "invoice_changed")
		noHolderCalled(t, holders)
		if n := counterNext(t, h); n != before {
			t.Errorf("slip %d: counter next = %d, want %d", s, n, before)
		}
	}
	step.Store(0)
	issued(t, h, draft.ID)
	if got := holders.calledOrder(); !slices.Equal(got, []contracts.WorkSourceKind{kindMilestone, kindHours}) {
		t.Errorf("holders called = %v, want the milestone's and the hours': the mileage was dropped", got)
	}
}

// Under the lock the issue applies the wizard's rule again from the projects
// it read: hours of a project now fixed-price, and any work of one now
// non-billable, are not selectable — the first line holding such a source,
// and the source, named; a fixed-price project's expenses and milestones
// still issue.
func TestIssue_AProjectTurnedFixedPriceOrNonBillableIsNotSelectable(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	projects := newFakeProjects()
	h := workReady(t, holders, projects)
	before := counterNext(t, h)
	notHours := func(offset int64) []planted {
		return []planted{
			{position: 2, kind: "expenses.entry", id: mileage + offset, revision: 3, subkind: ptrTo("mileage"), quantity: "90", amount: "450", date: "2026-09-03"},
			{position: 3, kind: "projects.milestone", id: milestone + offset, revision: 1, quantity: "1", amount: "10000", date: "2026-09-05"},
		}
	}
	notSelectable := func(draft int64, position int32, kind contracts.WorkSourceKind, id int64) {
		t.Helper()
		p := refusedWith(t, h, draft, "", "source_not_selectable")
		if p.LinePosition == nil || *p.LinePosition != position || p.SourceKind == nil || *p.SourceKind != string(kind) ||
			p.SourceID == nil || *p.SourceID != id {
			t.Errorf("line %v, source %v %v; want line %d, %s %d", p.LinePosition, p.SourceKind, p.SourceID, position, kind, id)
		}
		if n := counterNext(t, h); n != before {
			t.Errorf("counter next = %d, want %d", n, before)
		}
	}

	projects.edit(project41, func(p *contracts.ProjectEntry) { p.BillingType = "fixed-price" })
	notSelectable(sourcedDraft(t, h).ID, 1, kindHours, hourOne)
	noHolderCalled(t, holders)
	if inv := issued(t, h, plantedDraft(t, h, notHours(10)...).ID); *inv.Number != 1 {
		t.Errorf("a fixed-price project's expense and milestone = number %d, want 1", *inv.Number)
	}
	before = counterNext(t, h)

	projects.edit(project41, func(p *contracts.ProjectEntry) { p.BillingType = "non-billable" })
	notSelectable(plantedDraft(t, h, notHours(20)...).ID, 2, kindExpense, mileage+20)
	if got := holders.calledOrder(); !slices.Equal(got, []contracts.WorkSourceKind{kindMilestone, kindExpense}) {
		t.Errorf("holders called = %v, want only the fixed-price draft's two", got)
	}
}

// The issue moves the document's work from held to invoiced, in its own
// transaction: the sources block says so.
func TestIssue_SourcesMoveHeldToInvoiced(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	draft := sourcedDraft(t, h)
	issued(t, h, draft.ID)

	if states := statesOf(t, h, draft.ID); len(states) != 4 || !allAre(states, "invoiced") {
		t.Errorf("states = %v, want four invoiced", states)
	}
	doc := getWork(t, creator(t, h), draft.ID)
	if b := doc.Sources; b == nil || b.Count != 4 || b.Held != 0 || b.Invoiced != 4 || b.Released != 0 {
		t.Errorf("sources = %+v, want four invoiced", doc.Sources)
	}
}

// A draft holding no work calls no holder and reads nothing the issue did not
// read before work existed: no project, no user.
func TestIssue_ADraftWithoutSourcesCallsNoHolderAndReadsNoDirectoryMore(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	draft := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	c, userID := h.SignInUser(t, "invoices:access", "invoices:issue")

	if res := c.Do(http.MethodPost, issuePath(draft.ID), map[string]any{}); res.Status != http.StatusOK {
		t.Fatalf("issue = %d %s, want 200", res.Status, res.Body)
	}
	noHolderCalled(t, holders)
	profile := false
	for _, call := range contractCalls.by(userID) {
		switch {
		case call.method == "Directory.BillingProfile":
			profile = true
		case strings.HasPrefix(call.method, "ObjectStore."):
		default:
			t.Errorf("call %s, want only the billing profile and the object store", call.method)
		}
	}
	if !profile {
		t.Error("no billing profile read")
	}
}

// The harness's recorder fails a test on either kind of call the lock rule
// forbids — a call out of the module under a lock, and a holder's
// transaction-bound command outside one — and on neither of the allowed
// ones. Not parallel: a forbidden call recorded here would fail any harness
// open beside it, and it is forgotten when the test ends.
func TestContractCallHook_FailsALockedCallAndAnUnlockedTxCommand(t *testing.T) {
	ctx := context.Background()
	locked := invoices.LockedContext(ctx)
	before, beforeTx := lockedContractCalls.count(), lockedContractCalls.txCount()
	t.Cleanup(func() { lockedContractCalls.forget(before, beforeTx) })

	invoices.NoteContractCall(ctx, "Directory.BillingProfile")
	invoices.NoteTxCommand(locked, "InvoicedWork.time.entry.Mark")
	if calls, tx := lockedContractCalls.since(before), lockedContractCalls.txSince(beforeTx); len(calls) != 0 || len(tx) != 0 {
		t.Fatalf("the allowed calls recorded %d locked calls and %d unlocked commands, want none", len(calls), len(tx))
	}

	invoices.NoteContractCall(locked, "Directory.BillingProfile")
	invoices.NoteTxCommand(ctx, "InvoicedWork.time.entry.Mark")
	calls, tx := lockedContractCalls.since(before), lockedContractCalls.txSince(beforeTx)
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "Directory.BillingProfile\n") {
		t.Errorf("locked calls = %d %v, want the billing profile read under a lock", len(calls), calls)
	}
	if len(tx) != 1 || !strings.HasPrefix(tx[0], "InvoicedWork.time.entry.Mark\n") {
		t.Errorf("unlocked commands = %d %v, want the mark outside a lock", len(tx), tx)
	}
}

// Every read the issue of work needs is made before its transaction, and
// under the lock nothing but the holders' commands: the billing profile, the
// projects and the issuer's name first, unlocked; then each kind's mark,
// locked and bound to the transaction; then the object store, after the
// commit. The harness's cleanup checks the same on every test here.
func TestIssue_NoDirectoryCallUnderTheLock(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	draft := sourcedDraft(t, h)
	c, userID := h.SignInUser(t, "invoices:access", "invoices:issue")

	if res := c.Do(http.MethodPost, issuePath(draft.ID), map[string]any{}); res.Status != http.StatusOK {
		t.Fatalf("issue = %d %s, want 200", res.Status, res.Body)
	}
	var got []string
	for _, call := range contractCalls.by(userID) {
		if call.locked != call.txBound {
			t.Errorf("%s: locked %v, transaction-bound %v", call.method, call.locked, call.txBound)
		}
		if !strings.HasPrefix(call.method, "ObjectStore.") {
			got = append(got, call.method)
		}
	}
	want := []string{"Directory.BillingProfile", "Projects.Projects", "Users.User",
		"InvoicedWork.projects.milestone.Mark", "InvoicedWork.expenses.entry.Mark", "InvoicedWork.time.entry.Mark"}
	if !slices.Equal(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}
