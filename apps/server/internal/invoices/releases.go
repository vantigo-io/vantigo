package invoices

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is release on credit (invoices work design D8): a source is
// released only by the credit note that returns its line in full — the
// line's last return, creditBook.lastReturn, which total records per credit
// line as squared — and never by a price reduction or a partial return; a
// grouped line credited in part releases nothing until the rest is returned,
// and a milestone is released whole. The release is decided under the
// original's lock (creditIssueChecks) and written in the credit note's issue:
// line_releases on the credit side, the original's rows moved from invoiced
// to released, and each kind's holder taking its stamp back on the issue's
// transaction (writeback.go, releaseWork). A credit-note draft answers what
// its issue would release; a new invoice may pull released work again, and
// the wizard suggests a note naming what it replaces.

// release is one invoiced source a credit note's issue releases: the
// original's row, and the credit note's line that returns that row's line in
// full.
type release struct {
	rowID        int64
	creditLineID int64
	source       heldSource
}

// invoicedOn is the invoiced rows of a credit note's original, read on q: the
// work a credit note of it may release.
func invoicedOn(ctx context.Context, q *store.Queries, book creditBook) ([]store.InvoicesLineSource, error) {
	ids := slices.Sorted(maps.Keys(book.lines))
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := q.InvoicedSourcesOfLines(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read document %d's invoiced work: %w", book.original.ID, err)
	}
	return rows, nil
}

// releasesOf is what a credit note's lines release of the original's invoiced
// rows (D8): every row of an original line that one of lines returns in full
// — squared[i], lines[i]'s last return — by (kind, id), the rows' order. A
// line credited in part, or at a lower price, releases nothing.
func releasesOf(rows []store.InvoicesLineSource, lines []store.InvoicesLine, squared []bool) ([]release, error) {
	returned := map[int64]int64{}
	for i, l := range lines {
		if squared[i] && l.CreditsLineID != nil {
			returned[*l.CreditsLineID] = l.ID
		}
	}
	var out []release
	for _, r := range rows {
		creditLine, ok := returned[r.LineID]
		if !ok {
			continue
		}
		source, err := lineSource(r)
		if err != nil {
			return nil, err
		}
		out = append(out, release{rowID: r.ID, creditLineID: creditLine, source: source})
	}
	return out, nil
}

// lineSource is one line_sources row as this module reasons about it, without
// its line's position, which a release does not need.
func lineSource(r store.InvoicesLineSource) (heldSource, error) {
	quantity, amount, err := numericPair(r.Quantity, r.Amount)
	if err != nil {
		return heldSource{}, err
	}
	return heldSource{
		kind: contracts.WorkSourceKind(r.SourceKind), id: r.SourceID, revision: r.SourceRevision, subkind: r.SourceSubkind,
		projectID: r.ProjectID, quantity: quantity, amount: amount, currency: r.Currency, date: r.SourceDate.Time, state: r.State,
	}, nil
}

// withWouldRelease answers on a credit-note draft whose original bills
// invoiced work what its issue would release (D8, D17): the sources block —
// its counts zero, since a credit note holds no work of its own — with
// wouldRelease, the sources of every original line the draft returns in full
// as cd totalled it, [] for none. A credit of an invoice that bills no work
// answers no block.
func withWouldRelease(ctx context.Context, q *store.Queries, cd creditDraft, stored []store.InvoicesLine, resp *gen.InvoicesInvoiceResponse) error {
	rows, err := invoicedOn(ctx, q, cd.book)
	if err != nil || len(rows) == 0 {
		return err
	}
	releases, err := releasesOf(rows, stored, cd.totals.squared)
	if err != nil {
		return err
	}
	refs := make([]gen.InvoicesSourceRef, 0, len(releases))
	for _, r := range releases {
		refs = append(refs, gen.InvoicesSourceRef{Kind: string(r.source.kind), Id: r.source.id})
	}
	resp.Sources = &gen.InvoicesSourcesBlock{WouldRelease: &refs}
	return nil
}

// readReleaseWork reads, on the pool, before a credit note's issue
// transaction, what releasing work needs (D8, plan reading 20): the
// original's work and, only when any of it is invoiced, the holders and the
// issuer's name for their timeline events. An original that bills no
// invoiced work reads nothing more. Which sources are released is decided
// under the original's lock; what is invoiced then was invoiced now, since an
// issued original's rows only ever move from invoiced to released.
func (s *server) readReleaseWork(ctx context.Context, q *store.Queries, credit store.InvoicesInvoice) (issueWork, error) {
	rows, err := sourcesOf(ctx, q, *credit.CreditsInvoiceID)
	if err != nil {
		return issueWork{}, err
	}
	if !slices.ContainsFunc(rows, func(r heldSource) bool { return r.state == sourceInvoiced }) {
		return issueWork{}, nil
	}
	h, err := s.holders()
	if err != nil {
		s.deps.Logger.ErrorContext(ctx, "invoices: the invoiced-work holders are composed wrong", "error", err.Error())
		return issueWork{}, err
	}
	display, err := s.issuerName(ctx)
	if err != nil {
		return issueWork{}, err
	}
	return issueWork{rows: rows, holders: h, display: display}, nil
}

// releaseCreditWork is the release's step in a credit note's issue, under its
// lock, after every check and the number, before the document is written
// (D8): the releases recorded on the credit side (line_releases, the credit
// note's id and the line returning each source's line), the original's rows
// moved from invoiced to released, and each kind's holder handed them with
// ref — the original's id, number and issue date; the credit note's issue
// time and issuer. A credit note that releases nothing writes nothing.
func (s *server) releaseCreditWork(ctx context.Context, tx pgx.Tx, txq *store.Queries, creditID int64, work issueWork,
	releases []release, ref contracts.InvoiceRef,
) error {
	if len(releases) == 0 {
		return nil
	}
	if work.holders == nil {
		return fmt.Errorf("invoices: credit note %d releases work its original did not bill when read", creditID)
	}
	p := store.InsertLineReleasesParams{InvoiceID: creditID}
	rows := make([]heldSource, 0, len(releases))
	for _, r := range releases {
		p.CreditLineIds, p.LineSourceIds = append(p.CreditLineIds, r.creditLineID), append(p.LineSourceIds, r.rowID)
		rows = append(rows, r.source)
	}
	if err := txq.InsertLineReleases(ctx, p); err != nil {
		return fmt.Errorf("invoices: record credit note %d's releases: %w", creditID, err)
	}
	released, err := txq.ReleaseSources(ctx, p.LineSourceIds)
	if err != nil {
		return fmt.Errorf("invoices: release document %d's work: %w", ref.ID, err)
	}
	if len(released) != len(releases) {
		return fmt.Errorf("invoices: credit note %d released %d of document %d's %d sources", creditID, len(released), ref.ID, len(releases))
	}
	return s.releaseWork(ctx, tx, work.holders, ref, rows)
}

// rePullNote is the note the wizard suggests when a new invoice pulls
// released work again (D8) — "Erstatter faktura <n>, kreditert med
// kreditnota <c>" in nb, "Replaces invoice <n>, credited by credit note <c>"
// in en — for each (invoice, credit note) pair that last released one of
// sources, newest first, each once; "" when none of them was ever released.
// The note is a suggestion only: no new invoice is required to name the
// credit note.
//
// Task 8: nothing calls this yet; from-work does once its wizard is merged.
// In postInvoicesFromWork, only when the request carries no note — on a new
// draft, and on an append only when the target draft's note is empty — call
// rePullNote(ctx, q, <the buyer's line language, "en" or "nb">, <the
// request's sources as []sourceRef>) on the pool, before the transaction; a
// non-empty result becomes the draft's Note. Then extend
// TestRelease_TheWorkIsSelectableAgain with the view and from-work half (the
// released rows listed selectable, with no heldBy, and from-work over them a
// 201) and drive TestRelease_TheNoteSuggestionOnRePull through from-work.
func rePullNote(ctx context.Context, q *store.Queries, language string, sources []sourceRef) (string, error) {
	if len(sources) == 0 {
		return "", nil
	}
	p := store.ReleasedHistoryOfParams{}
	for _, r := range sources {
		p.Kinds, p.Ids = append(p.Kinds, string(r.kind)), append(p.Ids, r.id)
	}
	history, err := q.ReleasedHistoryOf(ctx, p)
	if err != nil {
		return "", fmt.Errorf("invoices: read the released work's history: %w", err)
	}
	format := "Erstatter faktura %d, kreditert med kreditnota %d"
	if language == "en" {
		format = "Replaces invoice %d, credited by credit note %d"
	}
	latest := map[sourceRef]bool{}
	seen := map[[2]int64]bool{}
	var notes []string
	for _, h := range history {
		source := sourceRef{kind: contracts.WorkSourceKind(h.SourceKind), id: h.SourceID}
		if latest[source] || h.OriginalNumber == nil || h.CreditNoteNumber == nil {
			continue
		}
		latest[source] = true
		pair := [2]int64{*h.OriginalNumber, *h.CreditNoteNumber}
		if seen[pair] {
			continue
		}
		seen[pair] = true
		notes = append(notes, fmt.Sprintf(format, pair[0], pair[1]))
	}
	return strings.Join(notes, ". "), nil
}
