package invoices_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Release on credit (invoices work design D8): the credit note that returns a
// line in full — its line's last return, at the line's own price and
// discount — releases the line's invoiced work in its own issue: the release
// recorded on the credit side, the rows moved to released, and each kind's
// holder handed the original's ref with the credit note's issue time and
// issuer, on the issue's transaction. A partial return or a price reduction
// releases nothing. The originals are the sourced draft — 7.5 h on line 1,
// the mileage on line 2, the milestone on line 3 — issued with the fakes.

// releaseDocJSON is the part of a document the release tests read.
type releaseDocJSON struct {
	Status  string `json:"status"`
	Sources *struct {
		Count        int32     `json:"count"`
		Held         int32     `json:"held"`
		Invoiced     int32     `json:"invoiced"`
		Released     int32     `json:"released"`
		WouldRelease []refJSON `json:"wouldRelease"`
	} `json:"sources"`
	Lines []struct {
		Sources []sourceJSON `json:"sources"`
	} `json:"lines"`
}

func getRelease(t *testing.T, h *harness, id int64) releaseDocJSON {
	t.Helper()
	res := h.SignIn(t, "invoices:access").Do(http.MethodGet, invoicePath(id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("GET /invoices/%d = %d %s", id, res.Status, res.Body)
	}
	var doc releaseDocJSON
	res.JSON(&doc)
	return doc
}

// issuedWork is the sourced draft issued: an invoice billing the four.
func issuedWork(t *testing.T, h *harness) invoiceJSON {
	t.Helper()
	return issued(t, h, sourcedDraft(t, h).ID)
}

// creditOf is a credit-note draft of original saved with each original
// line's quantity as quantities gives it (by position; a line left out of
// quantities is credited in full), at its own price.
func creditOf(t *testing.T, h *harness, original int64, quantities map[int32]float64) invoiceJSON {
	t.Helper()
	c := creditDraft(t, h, original)
	if len(quantities) == 0 {
		return c
	}
	lines := []map[string]any{}
	for _, l := range c.Lines {
		cl := creditLine(l)
		if q, ok := quantities[l.Position]; ok {
			if q == 0 {
				continue
			}
			cl["quantity"] = q
		}
		lines = append(lines, cl)
	}
	return saveCredit(t, h, c, creditBody(c, lines...))
}

// releaseCalls is every release command, by kind.
func releaseCalls(holders *fakeHolders) map[contracts.WorkSourceKind][]holderCall {
	out := map[contracts.WorkSourceKind][]holderCall{}
	for _, k := range contracts.InvoicedWorkOrder {
		for _, c := range holders.callsOf(k) {
			if c.op == "release" {
				out[k] = append(out[k], c)
			}
		}
	}
	return out
}

// releasedIDs is every source id a release command was handed, by kind.
func releasedIDs(holders *fakeHolders) map[contracts.WorkSourceKind][]int64 {
	out := map[contracts.WorkSourceKind][]int64{}
	for k, calls := range releaseCalls(holders) {
		for _, c := range calls {
			out[k] = append(out[k], idsOf(c.sources)...)
		}
	}
	return out
}

// releaseRows is a credit note's line_releases as "credit-line-position:kind:id",
// in the order of the sources.
func releaseRows(t *testing.T, h *harness, creditID int64) []string {
	t.Helper()
	return modtest.One[[]string](t, h.Harness, `
		SELECT coalesce(array_agg(format('%s:%s:%s', l.position, s.source_kind, s.source_id) ORDER BY s.source_kind, s.source_id), '{}')
		FROM invoices.line_releases r
		JOIN invoices.lines l ON l.id = r.credit_line_id AND l.invoice_id = r.invoice_id
		JOIN invoices.line_sources s ON s.id = r.line_source_id
		WHERE r.invoice_id = $1`, creditID)
}

// stateBySource is a document's line sources' states by source id.
func stateBySource(t *testing.T, h *harness, id int64) map[int64]string {
	t.Helper()
	rows := modtest.One[[]string](t, h.Harness, `
		SELECT coalesce(array_agg(source_id || ':' || state ORDER BY source_id), '{}') FROM invoices.line_sources WHERE invoice_id = $1`, id)
	out := map[int64]string{}
	for _, r := range rows {
		id, state, _ := strings.Cut(r, ":")
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		out[n] = state
	}
	return out
}

// A credit note returning every line in full releases all four: each kind's
// holder once, in the lock order, with the original's id, number and issue
// date and the credit note's own issue time and issuer; line_releases carries
// the credit note's id and the credit line returning each source's line; the
// original's rows are released and its sources block says so; and the child
// trigger refuses any later write of the releases.
func TestRelease_AFullReturnReleases(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	original := issuedWork(t, h)
	c := creditOf(t, h, original.ID, nil)
	h.Advance(time.Hour)
	start := h.Now()
	client, userID := h.SignInUser(t, "invoices:access", "invoices:issue")

	res := client.Do(http.MethodPost, issuePath(c.ID), map[string]any{})
	if res.Status != http.StatusOK {
		t.Fatalf("issue the credit note = %d %s, want 200", res.Status, res.Body)
	}
	var credit invoiceJSON
	res.JSON(&credit)
	display := modtest.One[string](t, h.Harness, `SELECT display_name FROM identity.users WHERE id = $1`, userID)

	if marks := 3; len(holders.calledOrder()) != marks+3 || !slices.Equal(holders.calledOrder()[marks:], contracts.InvoicedWorkOrder) {
		t.Fatalf("holders called = %v, want the three marks then the three releases in the lock order", holders.calledOrder())
	}
	want := map[contracts.WorkSourceKind][]int64{kindMilestone: {milestone}, kindExpense: {mileage}, kindHours: {hourOne, hourTwo}}
	for _, k := range contracts.InvoicedWorkOrder {
		calls := releaseCalls(holders)[k]
		if len(calls) != 1 {
			t.Fatalf("%s: %d releases, want 1", k, len(calls))
		}
		r := calls[0]
		if !slices.Equal(idsOf(r.sources), want[k]) || !r.locked || r.status != "issued" {
			t.Errorf("%s: released %v (locked %v, on a %s document), want %v under the lock of an issued original", k, idsOf(r.sources), r.locked, r.status, want[k])
		}
		ref := r.ref
		if ref.ID != original.ID || ref.Number != *original.Number || ref.IssueDate.Format(time.DateOnly) != *original.IssueDate ||
			!ref.IssuedAt.Equal(start) || ref.IssuedBy != userID || ref.IssuedByDisplay != display || display == "" {
			t.Errorf("%s: ref = %+v, want invoice %d number %d of %s, released at %v by %s (%q)", k, ref, original.ID,
				*original.Number, *original.IssueDate, start, userID, display)
		}
	}
	wantRows := []string{"2:expenses.entry:601", "3:projects.milestone:701", "1:time.entry:501", "1:time.entry:502"}
	if got := releaseRows(t, h, credit.ID); !slices.Equal(got, wantRows) {
		t.Errorf("line_releases = %v, want %v", got, wantRows)
	}
	if states := statesOf(t, h, original.ID); len(states) != 4 || !allAre(states, "released") {
		t.Errorf("the original's states = %v, want four released", states)
	}
	doc := getRelease(t, h, original.ID)
	if b := doc.Sources; b == nil || b.Count != 4 || b.Invoiced != 0 || b.Released != 4 || b.WouldRelease != nil {
		t.Errorf("the original's sources = %+v, want four released", doc.Sources)
	}
	for _, l := range doc.Lines {
		for _, s := range l.Sources {
			if s.State != "released" {
				t.Errorf("the original's %s %d is %s, want released", s.Kind, s.ID, s.State)
			}
		}
	}
	if issuedCredit := getRelease(t, h, credit.ID); issuedCredit.Sources != nil {
		t.Errorf("the issued credit note's sources = %+v, want none: it holds no work", issuedCredit.Sources)
	}
	for _, sql := range []string{
		`DELETE FROM invoices.line_releases WHERE invoice_id = $1`,
		`UPDATE invoices.line_releases SET credit_line_id = credit_line_id WHERE invoice_id = $1`,
	} {
		if _, err := h.Pool().Exec(context.Background(), sql, credit.ID); err == nil || !strings.Contains(err.Error(), "issued document is immutable") {
			t.Errorf("%s = %v, want the child trigger's refusal", sql, err)
		}
	}
}

// A line returned in part releases nothing: 2.5 of the 7.5 hours credited
// leaves both hours invoiced, while the other two lines, returned in full,
// release theirs.
func TestRelease_APartialReturnReleasesNothing(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	original := issuedWork(t, h)
	issued(t, h, creditOf(t, h, original.ID, map[int32]float64{1: 2.5}).ID)

	got := releasedIDs(holders)
	if len(got[kindHours]) != 0 || !slices.Equal(got[kindExpense], []int64{mileage}) || !slices.Equal(got[kindMilestone], []int64{milestone}) {
		t.Errorf("released = %v, want the mileage and the milestone, no hour", got)
	}
	states := stateBySource(t, h, original.ID)
	if states[hourOne] != "invoiced" || states[hourTwo] != "invoiced" || states[mileage] != "released" || states[milestone] != "released" {
		t.Errorf("the original's states = %v, want the hours invoiced and the rest released", states)
	}
}

// A price reduction is never a return: every line credited at its full
// quantity but a lower price releases nothing, and no holder is asked to.
func TestRelease_APriceReductionReleasesNothing(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	original := issuedWork(t, h)
	c := creditDraft(t, h, original.ID)
	lines := []map[string]any{}
	for _, l := range c.Lines {
		cl := creditLine(l)
		cl["unitPrice"] = l.UnitPrice / 2
		lines = append(lines, cl)
	}
	issued(t, h, saveCredit(t, h, c, creditBody(c, lines...)).ID)

	if got := releaseCalls(holders); len(got) != 0 {
		t.Errorf("releases = %v, want none", got)
	}
	if states := statesOf(t, h, original.ID); !allAre(states, "invoiced") {
		t.Errorf("the original's states = %v, want every source still invoiced", states)
	}
}

// Two partial returns of the hours line: the first, 2.5 hours, releases
// nothing; the second, the 5 left, is the line's last return and releases
// both hours — and only them, the other lines kept out of both notes.
func TestRelease_TheLastOfTwoPartialReturnsReleases(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	original := issuedWork(t, h)
	issued(t, h, creditOf(t, h, original.ID, map[int32]float64{1: 2.5, 2: 0, 3: 0}).ID)
	if got := releaseCalls(holders); len(got) != 0 {
		t.Fatalf("the first part released %v, want nothing", got)
	}

	second := issued(t, h, creditOf(t, h, original.ID, map[int32]float64{1: 5, 2: 0, 3: 0}).ID)
	if got := releasedIDs(holders); len(got) != 1 || !slices.Equal(got[kindHours], []int64{hourOne, hourTwo}) {
		t.Errorf("released = %v, want the two hours", got)
	}
	if got := releaseRows(t, h, second.ID); !slices.Equal(got, []string{"1:time.entry:501", "1:time.entry:502"}) {
		t.Errorf("line_releases = %v, want the two hours on the second note's line 1", got)
	}
	states := stateBySource(t, h, original.ID)
	if states[hourOne] != "released" || states[hourTwo] != "released" || states[mileage] != "invoiced" || states[milestone] != "invoiced" {
		t.Errorf("the original's states = %v, want the hours released and the rest invoiced", states)
	}
}

// A milestone is released whole: half its line credited releases nothing,
// the other half releases the milestone, with its whole amount.
func TestRelease_AMilestoneWhole(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	original := issuedWork(t, h)
	issued(t, h, creditOf(t, h, original.ID, map[int32]float64{1: 0, 2: 0, 3: 0.5}).ID)
	if got := releaseCalls(holders); len(got) != 0 {
		t.Fatalf("half the milestone released %v, want nothing", got)
	}

	issued(t, h, creditOf(t, h, original.ID, map[int32]float64{1: 0, 2: 0, 3: 0.5}).ID)
	calls := releaseCalls(holders)[kindMilestone]
	if len(calls) != 1 || len(calls[0].sources) != 1 || calls[0].sources[0].ID != milestone || calls[0].sources[0].Amount != "10000.00000000" {
		t.Errorf("milestone releases = %+v, want the milestone whole, 10000", calls)
	}
	if states := stateBySource(t, h, original.ID); states[milestone] != "released" {
		t.Errorf("the milestone is %s, want released", states[milestone])
	}
}

// A holder that fails a release fails the credit note's issue: a 500, the
// number rolled back, nothing released or recorded, and the credit note still
// a draft that issues once the holder recovers.
func TestRelease_AHolderErrorRollsTheCreditNoteBack(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	original := issuedWork(t, h)
	c := creditOf(t, h, original.ID, nil)
	before := counterNext(t, h)
	holders.failWith(kindExpense, errors.New("injected"))

	res := issuer(t, h).Do(http.MethodPost, issuePath(c.ID), map[string]any{}, modtest.SkipContract("a holder's failure answers the undeclared 500"))
	if res.Status != http.StatusInternalServerError {
		t.Fatalf("a holder's failure = %d %s, want 500", res.Status, res.Body)
	}
	if n := counterNext(t, h); n != before {
		t.Errorf("counter next = %d, want %d", n, before)
	}
	if states := statesOf(t, h, original.ID); !allAre(states, "invoiced") {
		t.Errorf("the original's states = %v, want every source still invoiced", states)
	}
	if rows := releaseRows(t, h, c.ID); len(rows) != 0 {
		t.Errorf("line_releases = %v, want none", rows)
	}
	if doc := getRelease(t, h, c.ID); doc.Status != "draft" {
		t.Errorf("the credit note is %s, want a draft", doc.Status)
	}
	holders.failWith(kindExpense, nil)
	if credit := issued(t, h, c.ID); *credit.Number != before {
		t.Errorf("the credit note after the failure = number %d, want %d", *credit.Number, before)
	}
	if states := statesOf(t, h, original.ID); !allAre(states, "released") {
		t.Errorf("the original's states = %v, want every source released", states)
	}
}

// The holders' stamps are theirs: this module never reads them back, so a
// holder that takes back nothing — the fakes write nothing, as a source whose
// stamp was already gone would — still lets the credit note issue and
// release, and the release logs nothing.
func TestRelease_AHolderToleratesAMissingStamp(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	original := issuedWork(t, h)
	issued(t, h, creditOf(t, h, original.ID, nil).ID)

	if states := statesOf(t, h, original.ID); !allAre(states, "released") {
		t.Errorf("the original's states = %v, want every source released", states)
	}
	if logs := h.Logs(); strings.Contains(logs, `"level":"ERROR"`) {
		t.Errorf("logs = %s, want no error", logs)
	}
}

// A credit-note draft says what its issue would release: as copied, every
// line returned in full, all four; with the hours line returned in part, the
// mileage and the milestone; a credit note of an invoice that bills no work
// answers no sources block.
func TestRelease_ACreditDraftSaysWhatItWouldRelease(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	original := issuedWork(t, h)
	c := creditDraft(t, h, original.ID)
	all := []string{"expenses.entry:601", "projects.milestone:701", "time.entry:501", "time.entry:502"}

	doc := getRelease(t, h, c.ID)
	if b := doc.Sources; b == nil || b.Count != 0 || b.Held != 0 || b.Invoiced != 0 || b.Released != 0 || !slices.Equal(refsOf(b.WouldRelease), all) {
		t.Fatalf("the copied draft's sources = %+v, want none of its own and all four to release", doc.Sources)
	}
	l := creditLine(c.Lines[0])
	l["quantity"] = 2.5
	partial := saveCredit(t, h, c, creditBody(c, l, creditLine(c.Lines[1]), creditLine(c.Lines[2])))
	doc = getRelease(t, h, c.ID)
	if b := doc.Sources; b == nil || !slices.Equal(refsOf(b.WouldRelease), all[:2]) {
		t.Errorf("the partial draft's sources = %+v, want the mileage and the milestone to release", doc.Sources)
	}
	saveCredit(t, h, partial, creditBody(partial, l))
	doc = getRelease(t, h, c.ID)
	if b := doc.Sources; b == nil || b.WouldRelease == nil || len(b.WouldRelease) != 0 {
		t.Errorf("the draft returning nothing in full = %+v, want an empty wouldRelease", doc.Sources)
	}

	plain := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	if doc := getRelease(t, h, creditDraft(t, h, plain.ID).ID); doc.Sources != nil {
		t.Errorf("a credit of an invoice without work = %+v, want no sources block", doc.Sources)
	}
}

// Released work is uninvoiced again: no live row holds it any more, so the
// floor lets another draft hold it — which before the release it refused —
// and that draft issues, the holders marking it again. (The uninvoiced view
// and from-work read the same rows, through LiveSourcesFor and
// LiveSourcesElsewhere.)
func TestRelease_TheWorkIsSelectableAgain(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	original := issuedWork(t, h)
	q := store.New(h.Pool())
	ctx := context.Background()
	four := store.LiveSourcesElsewhereParams{Kinds: []string{"time.entry", "time.entry", "expenses.entry", "projects.milestone"},
		Ids: []int64{hourOne, hourTwo, mileage, milestone}}
	again := createDraft(t, h, draftBody(customerAcme, line("Konsulenttimer", 4, 1200, vat25)))
	hold := func() error {
		_, err := h.Pool().Exec(ctx, `
			INSERT INTO invoices.line_sources (line_id, invoice_id, source_kind, source_id, source_revision, project_id, quantity, amount, currency, source_date)
			SELECT l.id, l.invoice_id, 'time.entry', $2, 3, 41, 4, 4800, 'NOK', '2026-09-01' FROM invoices.lines l WHERE l.invoice_id = $1`,
			again.ID, hourOne)
		return err
	}

	four.InvoiceID = again.ID
	if live, err := q.LiveSourcesElsewhere(ctx, four); err != nil || len(live) != 4 {
		t.Fatalf("before the release: %d live rows elsewhere (%v), want 4", len(live), err)
	}
	if err := hold(); err == nil || !strings.Contains(err.Error(), "ux_line_sources_live") {
		t.Fatalf("holding an invoiced hour = %v, want the floor's unique violation", err)
	}
	issued(t, h, creditOf(t, h, original.ID, nil).ID)

	if live, err := q.LiveSourcesElsewhere(ctx, four); err != nil || len(live) != 0 {
		t.Errorf("after the release: %v live rows elsewhere (%v), want none", live, err)
	}
	if live, err := q.LiveSourcesFor(ctx, store.LiveSourcesForParams{Kinds: four.Kinds, Ids: four.Ids}); err != nil || len(live) != 0 {
		t.Errorf("after the release: %v live rows (%v), want none", live, err)
	}
	if err := hold(); err != nil {
		t.Fatalf("holding the released hour = %v, want it held", err)
	}
	reissued := issued(t, h, again.ID)
	marks := 0
	for _, c := range holders.callsOf(kindHours) {
		if c.op == "mark" && c.ref.ID == reissued.ID && slices.Equal(idsOf(c.sources), []int64{hourOne}) {
			marks++
		}
	}
	if marks != 1 {
		t.Errorf("the re-pulled hour was marked %d times on invoice %d, want once", marks, reissued.ID)
	}
}

// The note the wizard suggests when it pulls released work again names the
// invoice it replaces and the credit note that credited it, in the buyer's
// language; for work released twice, the newest release; for several, each
// pair once, newest first; for work never released, nothing.
func TestRelease_TheNoteSuggestionOnRePull(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	ctx := context.Background()
	note := func(language string, refs ...refJSON) string {
		t.Helper()
		var kinds []string
		var ids []int64
		for _, r := range refs {
			kinds, ids = append(kinds, r.Kind), append(ids, r.ID)
		}
		got, err := invoices.RePullNote(ctx, h.Pool(), language, kinds, ids)
		if err != nil {
			t.Fatalf("RePullNote: %v", err)
		}
		return got
	}
	original := issuedWork(t, h)
	if got := note("nb", refHourOne); got != "" {
		t.Errorf("invoiced work's note = %q, want none", got)
	}
	credit := issued(t, h, creditOf(t, h, original.ID, nil).ID)
	if *original.Number != 1 || *credit.Number != 2 {
		t.Fatalf("numbers %d and %d, want 1 and 2, or the texts below prove nothing", *original.Number, *credit.Number)
	}

	if got := note("nb", refHourOne, refMileage); got != "Erstatter faktura 1, kreditert med kreditnota 2" {
		t.Errorf("nb = %q", got)
	}
	if got := note("en", refMilestone); got != "Replaces invoice 1, credited by credit note 2" {
		t.Errorf("en = %q", got)
	}
	if got := note("nb", ref("time.entry", 999)); got != "" {
		t.Errorf("work never invoiced = %q, want none", got)
	}

	again := plantedDraft(t, h, planted{position: 1, kind: "time.entry", id: hourOne, revision: 3, quantity: "4.00", amount: "4800", date: "2026-09-01"})
	reissued := issued(t, h, again.ID)
	recredit := issued(t, h, creditOf(t, h, reissued.ID, nil).ID)
	if got := note("nb", refHourOne); got != "Erstatter faktura 3, kreditert med kreditnota 4" || *reissued.Number != 3 || *recredit.Number != 4 {
		t.Errorf("work released twice = %q, want the newest release only", got)
	}
	if got := note("en", refHourOne, refHourTwo); got != "Replaces invoice 3, credited by credit note 4. Replaces invoice 1, credited by credit note 2" {
		t.Errorf("two releases = %q, want each pair once, newest first", got)
	}
}

// A released source whose kind no holder claims any more is logged at error
// and skipped — its stamp stays where it is — and the credit note still
// issues, releasing the rest and recording every release on its side.
func TestRelease_AnUnclaimedKindIsLoggedAndSkipped(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	original := issuedWork(t, h)
	c := creditOf(t, h, original.ID, nil)
	holders.disclaim(kindHours)

	credit := issued(t, h, c.ID)
	if logs := h.Logs(); !strings.Contains(logs, `"level":"ERROR"`) || !strings.Contains(logs, "time.entry") ||
		!strings.Contains(logs, `"sourceIds":[501,502]`) {
		t.Errorf("logs = %s, want an error naming time.entry and its sources 501 and 502", logs)
	}
	got := releasedIDs(holders)
	if len(got[kindHours]) != 0 || !slices.Equal(got[kindExpense], []int64{mileage}) || !slices.Equal(got[kindMilestone], []int64{milestone}) {
		t.Errorf("released = %v, want the mileage and the milestone through their holders, the hours skipped", got)
	}
	if states := statesOf(t, h, original.ID); !allAre(states, "released") {
		t.Errorf("the original's states = %v, want every source released", states)
	}
	if rows := releaseRows(t, h, credit.ID); len(rows) != 4 {
		t.Errorf("line_releases = %v, want four", rows)
	}
}

// Everything a credit note's release needs is read before its transaction —
// the issuer's name, only when its original bills invoiced work — and under
// the lock nothing but the holders' releases, each bound to the transaction;
// then the object store, after the commit. A credit of an invoice without
// work reads no directory at all.
func TestRelease_NoDirectoryCallUnderTheLock(t *testing.T) {
	t.Parallel()
	holders := newFakeHolders()
	h := workReady(t, holders, newFakeProjects())
	original := issuedWork(t, h)
	c := creditOf(t, h, original.ID, nil)
	plain := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	plainCredit := creditDraft(t, h, plain.ID)

	calls := func(id int64) []string {
		t.Helper()
		client, userID := h.SignInUser(t, "invoices:access", "invoices:issue")
		if res := client.Do(http.MethodPost, issuePath(id), map[string]any{}); res.Status != http.StatusOK {
			t.Fatalf("issue = %d %s, want 200", res.Status, res.Body)
		}
		got := []string{}
		for _, call := range contractCalls.by(userID) {
			if call.locked != call.txBound {
				t.Errorf("%s: locked %v, transaction-bound %v", call.method, call.locked, call.txBound)
			}
			if !strings.HasPrefix(call.method, "ObjectStore.") {
				got = append(got, call.method)
			}
		}
		return got
	}
	want := []string{"Users.User",
		"InvoicedWork.projects.milestone.Release", "InvoicedWork.expenses.entry.Release", "InvoicedWork.time.entry.Release"}
	if got := calls(c.ID); !slices.Equal(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
	if got := calls(plainCredit.ID); len(got) != 0 {
		t.Errorf("a credit without work called %v, want nothing", got)
	}
}
