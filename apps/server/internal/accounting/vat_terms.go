package accounting

// The VAT terms (A22 as R10 amends it): derived from the settings' vat_term —
// six two-month terms a calendar year whatever the fiscal year, or one —
// from the term that holds books_start; a row in vat_terms once a term is
// closed. Close, reopen, filing and the XML are here; the figures are vat.go's.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/mva"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/buildinfo"
)

// vatTerm is one term as the settings derive it.
type vatTerm struct {
	Year, Number int32
	// Kind is bimonthly or yearly.
	Kind       string
	Start, End time.Time
}

// termOf is the term of kind that holds date.
func termOf(kind string, date time.Time) vatTerm {
	if kind == "yearly" {
		return vatTerm{Year: int32(date.Year()), Number: 1, Kind: kind,
			Start: time.Date(date.Year(), time.January, 1, 0, 0, 0, 0, time.UTC),
			End:   time.Date(date.Year(), time.December, 31, 0, 0, 0, 0, time.UTC)}
	}
	n := (int(date.Month()) + 1) / 2
	start := time.Date(date.Year(), time.Month(2*n-1), 1, 0, 0, 0, 0, time.UTC)
	return vatTerm{Year: int32(date.Year()), Number: int32(n), Kind: kind, Start: start, End: start.AddDate(0, 2, -1)}
}

// next is the term after t.
func (t vatTerm) next() vatTerm { return termOf(t.Kind, t.End.AddDate(0, 0, 1)) }

// period is t as mva names it.
func (t vatTerm) period() mva.Period {
	return mva.Period{Year: int(t.Year), Kind: t.Kind, Number: int(t.Number)}
}

// due is the day t's return and payment are due.
func (t vatTerm) due() time.Time { return mva.DueDate(t.period()) }

// label is t as the screens and the vouchers name it: 2026-3.
func (t vatTerm) label() string { return fmt.Sprintf("%d-%d", t.Year, t.Number) }

// coversFrom is the first day t's return covers: books_start for a term that
// starts before the books do (M2).
func (t vatTerm) coversFrom(booksStart time.Time) time.Time {
	if t.Start.Before(booksStart) {
		return booksStart
	}
	return t.Start
}

// before reports whether t comes before u.
func (t vatTerm) before(u vatTerm) bool { return t.End.Before(u.Start) }

// termsThrough is every term of kind from the one that holds booksStart
// through the one that holds through, oldest first; none for vat_term none.
func termsThrough(kind string, booksStart, through time.Time) []vatTerm {
	if kind != "bimonthly" && kind != "yearly" {
		return nil
	}
	var out []vatTerm
	for t := termOf(kind, booksStart); !t.Start.After(through); t = t.next() {
		out = append(out, t)
	}
	return out
}

// lookupTerm is the term year and number name in the books' terms, false
// when there is none: no VAT term, a number out of range, or a term before
// the books start.
func lookupTerm(settings store.AccountingSetting, year, number int32) (vatTerm, bool) {
	kind := settings.VatTerm
	max := int32(6)
	if kind == "yearly" {
		max = 1
	}
	if (kind != "bimonthly" && kind != "yearly") || number < 1 || number > max || year < 2000 || year > 2100 {
		return vatTerm{}, false
	}
	month := time.January
	if kind == "bimonthly" {
		month = time.Month(2*number - 1)
	}
	t := termOf(kind, time.Date(int(year), month, 1, 0, 0, 0, 0, time.UTC))
	if t.End.Before(settings.BooksStart) {
		return vatTerm{}, false
	}
	return t, true
}

// vatWords are the settlement voucher's words in the books' language.
var vatWords = map[string]map[string]string{
	"en": {"settlement": "VAT settlement, term %s", "reversal": "Reversal of the VAT settlement, term %s", "closed": "VAT term %s closed",
		"reopened": "VAT term %s reopened: %s", "noOrg": "Invoices has no organisation number for the seller: the XML is without it.",
		"deduction": "%s (deduction)",
		"remark":    "Code %s: the sign is the opposite of the code's, so the line carries the remark kreditnota.",
		"partial":   "The books start on %s: this term's return covers %s–%s only.",
		"rateCheck": "Code %s: the VAT is not the base × the rate, cut to whole kroner, within one krone (Skatteetaten's rule R019)."},
	"nb": {"settlement": "Mva-oppgjør, termin %s", "reversal": "Tilbakeføring av mva-oppgjør, termin %s", "closed": "Mva-termin %s lukket",
		"reopened": "Mva-termin %s åpnet igjen: %s", "noOrg": "Invoices har ikke selgerens organisasjonsnummer: XML-filen er uten det.",
		"deduction": "%s (fradrag)",
		"remark":    "Kode %s: fortegnet er motsatt av kodens, så linjen har merknaden kreditnota.",
		"partial":   "Regnskapet starter %s: mva-meldingen for denne terminen dekker bare %s–%s.",
		"rateCheck": "Kode %s: mvaen er ikke grunnlaget × satsen, kuttet til hele kroner, innenfor én krone (Skatteetatens regel R019)."},
}

func vatWord(language, key string) string {
	if w, ok := vatWords[language][key]; ok {
		return w
	}
	return vatWords["en"][key]
}

// The settlement voucher's rules (A14's description of what the system
// posts): each VAT account's movement in the term to vat_settlement, the
// return's whole kroner there and the øre the return cut on the rounding
// account; and its reversal, line for line.
const (
	ruleVatSettlement         = "vat.settlement"
	ruleVatSettlementReversed = "vat.settlement_reversed"
)

// notRegistered is the refusal of a VAT term in books without one.
func notRegistered() error {
	return conflict(codeVatNotRegistered, "The company is not VAT-registered",
		"The books' VAT term is none: there are no VAT terms to close.")
}

// termState is a term's stored row and the closing that stands on it; both
// nil for a term never closed, the closing nil for one reopened.
type termState struct {
	row     *store.AccountingVatTerm
	closing *store.AccountingVatTermClosing
}

func (st termState) closed() bool {
	return st.row != nil && st.row.State == "closed" && st.closing != nil
}

// termRow is a term's stored row with its standing closing.
func termRow(ctx context.Context, q *store.Queries, t vatTerm, forUpdate bool) (termState, error) {
	var row store.AccountingVatTerm
	var err error
	if forUpdate {
		row, err = q.GetVatTermForUpdate(ctx, store.GetVatTermForUpdateParams{Year: t.Year, Term: t.Number, TermKind: t.Kind})
	} else {
		row, err = q.GetVatTerm(ctx, store.GetVatTermParams{Year: t.Year, Term: t.Number, TermKind: t.Kind})
	}
	if noRows(err) {
		return termState{}, nil
	}
	if err != nil {
		return termState{}, err
	}
	st := termState{row: &row}
	if row.CurrentClosingID != nil {
		c, err := q.GetVatTermClosing(ctx, *row.CurrentClosingID)
		if err != nil {
			return termState{}, err
		}
		st.closing = &c
	}
	return st, nil
}

// termItem is a term as the contract answers it; total is its return's
// total, as closed or as the books stand now, nil where it is not computed
// (the meta's open term, L6).
func termItem(t vatTerm, st termState, settings store.AccountingSetting, today time.Time, total *string,
	refs map[int64]string) gen.AccountingVatTermItem {
	out := gen.AccountingVatTermItem{Year: t.Year, Number: t.Number, PeriodStart: apiDate(t.Start), PeriodEnd: apiDate(t.End),
		PeriodName: t.period().Name(), DueDate: apiDate(t.due()), State: gen.AccountingVatTermStateOpen, Total: total,
		LockCovers: settings.LockedThrough != nil && !settings.LockedThrough.Before(t.End),
		Partial:    t.Start.Before(settings.BooksStart), CoversFrom: apiDate(t.coversFrom(settings.BooksStart))}
	if st.closed() {
		out.State = gen.AccountingVatTermStateClosed
		out.ClosedAt, out.ClosedByUserId = &st.closing.ClosedAt, &st.closing.ClosedByUserID
		out.Closing = &st.closing.Closing
		if st.closing.SettlementVoucherID != nil {
			out.SettlementVoucher = refOf(refs, st.closing.SettlementVoucherID)
		}
	}
	if st.row != nil && st.row.FiledAt != nil {
		out.FiledAt, out.FiledReference, out.FiledByUserId = apiDatePtr(st.row.FiledAt), st.row.FiledReference, st.row.FiledByUserID
		if st.closing != nil && st.row.FiledClosingID != nil && *st.row.FiledClosingID == st.closing.ID {
			out.FiledClosing = &st.closing.Closing
		}
	}
	out.NeedsClosing = out.State == gen.AccountingVatTermStateOpen && t.End.Before(today)
	out.NeedsFiling = out.FiledAt == nil && t.due().Before(today)
	return out
}

// storedReturn is a closing's return as the close stored it.
func storedReturn(c *store.AccountingVatTermClosing) (gen.AccountingVatReturn, error) {
	var r gen.AccountingVatReturn
	if err := json.Unmarshal(c.Figures, &r); err != nil {
		return r, fmt.Errorf("accounting: read closing %d's figures: %w", c.ID, err)
	}
	return r, nil
}

// termRows is the books' term rows of their kind, with each closed one's
// stored total.
func termRows(ctx context.Context, q *store.Queries, kind string) (map[[2]int32]store.ListVatTermsRow, error) {
	rows, err := q.ListVatTerms(ctx, kind)
	if err != nil {
		return nil, err
	}
	out := map[[2]int32]store.ListVatTermsRow{}
	for _, r := range rows {
		out[[2]int32{r.Year, r.Term}] = r
	}
	return out, nil
}

// listState is a listed row as a termState without its closing's figures.
func listState(r store.ListVatTermsRow, ok bool, closing *store.AccountingVatTermClosing) termState {
	if !ok {
		return termState{}
	}
	row := store.AccountingVatTerm{ID: r.ID, Year: r.Year, Term: r.Term, TermKind: r.TermKind, PeriodStart: r.PeriodStart,
		PeriodEnd: r.PeriodEnd, State: r.State, Closings: r.Closings, CurrentClosingID: r.CurrentClosingID, FiledAt: r.FiledAt,
		FiledReference: r.FiledReference, FiledByUserID: r.FiledByUserID, FiledClosingID: r.FiledClosingID, UpdatedAt: r.UpdatedAt}
	return termState{row: &row, closing: closing}
}

// termItems is every term through today's with its total: as closed for a
// closed term, from the books for an open one.
func (s *server) termItems(ctx context.Context, q *store.Queries, settings store.AccountingSetting, today time.Time) ([]gen.AccountingVatTermItem, error) {
	terms := termsThrough(settings.VatTerm, settings.BooksStart, today)
	rows, err := termRows(ctx, q, settings.VatTerm)
	if err != nil {
		return nil, err
	}
	out := []gen.AccountingVatTermItem{}
	states := make([]termState, len(terms))
	var ids []int64
	for i, t := range terms {
		r, ok := rows[[2]int32{t.Year, t.Number}]
		var closing *store.AccountingVatTermClosing
		if ok && r.CurrentClosingID != nil {
			c, err := q.GetVatTermClosing(ctx, *r.CurrentClosingID)
			if err != nil {
				return nil, err
			}
			closing = &c
			if c.SettlementVoucherID != nil {
				ids = append(ids, *c.SettlementVoucherID)
			}
		}
		states[i] = listState(r, ok, closing)
	}
	refs, err := voucherRefs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	for i, t := range terms {
		var total string
		if states[i].closed() {
			total = rows[[2]int32{t.Year, t.Number}].ClosedTotal
		} else {
			spec, err := computeSpecification(ctx, q, t.Start, t.End)
			if err != nil {
				return nil, err
			}
			total = spec.ret.Total.String()
		}
		out = append(out, termItem(t, states[i], settings, today, &total, refs))
	}
	return out, nil
}

// ListAccountingVatTerms is the terms from the books' first through today's.
func (s *server) ListAccountingVatTerms(ctx context.Context, _ gen.ListAccountingVatTermsRequestObject) (gen.ListAccountingVatTermsResponseObject, error) {
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	out := gen.ListAccountingVatTerms200JSONResponse{Terms: []gen.AccountingVatTermItem{}}
	err = s.withReadTx(ctx, func(q *store.Queries) error {
		settings, err := q.GetSettings(ctx)
		if err != nil {
			return err
		}
		out.VatTerm = gen.AccountingVatTerm(settings.VatTerm)
		out.Terms, err = s.termItems(ctx, q, settings, today)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// sellerOrganisationNumber is what the return says of the company: its
// organisation number, read from Invoices with no transaction open; "" when
// Invoices has none, or one the schema does not take.
func (s *server) sellerOrganisationNumber(ctx context.Context) (string, error) {
	if s.deps.SalesTax == nil {
		return "", nil
	}
	seller, err := s.deps.SalesTax.Seller(ctx)
	if err != nil {
		return "", fmt.Errorf("accounting: read the seller: %w", err)
	}
	n := strings.NewReplacer(" ", "", ".", "").Replace(strings.TrimSpace(seller.OrganisationNumber))
	n = strings.TrimSuffix(strings.TrimPrefix(n, "NO"), "MVA")
	if !mva.ValidOrganisationNumber(n) {
		return "", nil
	}
	return n, nil
}

// termDetail is a term's page: its item, its return (as closed, or as the
// books stand), its specification, the close's checks, its closings and its
// history.
func (s *server) termDetail(ctx context.Context, q *store.Queries, t vatTerm, today time.Time, organisation string) (gen.AccountingVatTermDetail, error) {
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return gen.AccountingVatTermDetail{}, err
	}
	st, err := termRow(ctx, q, t, false)
	if err != nil {
		return gen.AccountingVatTermDetail{}, err
	}
	spec, err := computeSpecification(ctx, q, t.Start, t.End)
	if err != nil {
		return gen.AccountingVatTermDetail{}, err
	}
	out := gen.AccountingVatTermDetail{Events: []gen.AccountingVatTermEvent{}, Closings: []gen.AccountingVatClosing{}}
	out.Specification, err = spec.view(ctx, q, s.now())
	if err != nil {
		return gen.AccountingVatTermDetail{}, err
	}
	if st.closed() {
		if out.Return, err = storedReturn(st.closing); err != nil {
			return gen.AccountingVatTermDetail{}, err
		}
	} else {
		out.Return = returnView(spec.ret, t, settings, organisation)
	}
	var events []store.ListVatTermEventsRow
	var closings []store.ListVatTermClosingsRow
	var ids []int64
	if st.row != nil {
		if events, err = q.ListVatTermEvents(ctx, st.row.ID); err != nil {
			return gen.AccountingVatTermDetail{}, err
		}
		if closings, err = q.ListVatTermClosings(ctx, st.row.ID); err != nil {
			return gen.AccountingVatTermDetail{}, err
		}
		for _, c := range closings {
			if c.SettlementVoucherID != nil {
				ids = append(ids, *c.SettlementVoucherID)
			}
		}
	}
	refs, err := voucherRefs(ctx, q, ids)
	if err != nil {
		return gen.AccountingVatTermDetail{}, err
	}
	out.Term = termItem(t, st, settings, today, &out.Return.Total, refs)
	for _, c := range closings {
		out.Closings = append(out.Closings, gen.AccountingVatClosing{Closing: c.Closing, ClosedAt: c.ClosedAt, ClosedByUserId: c.ClosedByUserID,
			SettlementVoucher: refOf(refs, c.SettlementVoucherID), Total: c.Total,
			Current: st.row.CurrentClosingID != nil && *st.row.CurrentClosingID == c.ID,
			Filed:   st.row.FiledClosingID != nil && *st.row.FiledClosingID == c.ID})
	}
	for _, e := range events {
		ev := gen.AccountingVatTermEvent{Kind: gen.AccountingVatTermEventKind(e.Kind), At: e.At, ByUserId: e.ByUserID,
			Reason: e.Reason, FiledAt: apiDatePtr(e.FiledAt), FiledReference: e.FiledReference, Closing: e.ClosingNumber}
		if e.VoucherID != nil && e.VoucherFiscalYear != nil && e.VoucherNumber != nil {
			ev.Voucher = &gen.AccountingVoucherRef{Id: *e.VoucherID, Display: display(*e.VoucherFiscalYear, *e.VoucherNumber)}
		}
		out.Events = append(out.Events, ev)
	}
	out.Checks, err = closeChecks(ctx, q, settings, t, today, spec)
	if err != nil {
		return gen.AccountingVatTermDetail{}, err
	}
	return out, nil
}

// closeChecks is what a close of t would refuse now.
func closeChecks(ctx context.Context, q *store.Queries, settings store.AccountingSetting, t vatTerm, today time.Time,
	spec *specification) (gen.AccountingVatCloseChecks, error) {
	out := gen.AccountingVatCloseChecks{Ended: t.End.Before(today), Reconciled: spec.reconciled(),
		LockAfterEnd: settings.LockedThrough != nil && !settings.LockedThrough.Before(t.End),
		Partial:      t.Start.Before(settings.BooksStart), RateMissing: spec.rateMissing() != ""}
	var err error
	if out.Attention, err = q.CountWaitingIn(ctx, store.CountWaitingInParams{FromDate: t.Start, ToDate: t.End}); err != nil {
		return out, err
	}
	earlier, err := earliestOpenBefore(ctx, q, settings, t)
	if err != nil {
		return out, err
	}
	if earlier != nil {
		item := termItem(*earlier, termState{}, settings, today, nil, nil)
		out.EarlierOpen = &item
	}
	stale, err := staleTerm(ctx, q, settings)
	if err != nil {
		return out, err
	}
	if stale != nil {
		item := termItem(*stale, termState{}, settings, today, nil, nil)
		out.StaleTerm = &item
	}
	return out, nil
}

// earliestOpenBefore is the earliest term before t that is not closed, nil
// when every one is (A22: terms close in order).
func earliestOpenBefore(ctx context.Context, q *store.Queries, settings store.AccountingSetting, t vatTerm) (*vatTerm, error) {
	rows, err := termRows(ctx, q, settings.VatTerm)
	if err != nil {
		return nil, err
	}
	for _, e := range termsThrough(settings.VatTerm, settings.BooksStart, t.Start.AddDate(0, 0, -1)) {
		if r, ok := rows[[2]int32{e.Year, e.Number}]; e.before(t) && (!ok || r.State != "closed") {
			return &e, nil
		}
	}
	return nil, nil
}

// fingerprint is what of a return the books decide: its exact total and its
// lines' codes, sides, specifications and exact amounts — not its names,
// warnings or the seller's number.
func fingerprint(r gen.AccountingVatReturn) string {
	var b strings.Builder
	b.WriteString(r.ExactTotal)
	for _, l := range r.Lines {
		fmt.Fprintf(&b, "|%s/%s/%s/%s/%s", l.Code, l.Side, deref(l.Specification), deref(l.ExactBase), l.ExactVat)
	}
	return b.String()
}

// staleTerm is the first closed term whose figures, computed again from the
// postings, are not the return it was closed with (H1's defence in depth:
// the lock keeps a closed term's postings as they were, and this finds it if
// anything did not); nil when none.
func staleTerm(ctx context.Context, q *store.Queries, settings store.AccountingSetting) (*vatTerm, error) {
	rows, err := q.ClosedTermsFigures(ctx, settings.VatTerm)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		var stored gen.AccountingVatReturn
		if err := json.Unmarshal(r.Figures, &stored); err != nil {
			return nil, fmt.Errorf("accounting: read term %d-%d's figures: %w", r.Year, r.Term, err)
		}
		t, ok := lookupTerm(settings, r.Year, r.Term)
		if !ok {
			continue
		}
		spec, err := computeSpecification(ctx, q, t.Start, t.End)
		if err != nil {
			return nil, err
		}
		if fingerprint(returnView(spec.ret, t, settings, "")) != fingerprint(stored) {
			return &t, nil
		}
	}
	return nil, nil
}

// returnView is a return as the contract answers it, for term t.
func returnView(r mva.Return, t vatTerm, settings store.AccountingSetting, organisation string) gen.AccountingVatReturn {
	language := settings.ChartLanguage
	out := gen.AccountingVatReturn{Year: t.Year, PeriodName: t.period().Name(), PeriodStart: apiDate(t.Start), PeriodEnd: apiDate(t.End),
		Total: r.Total.String(), ExactTotal: money(r.ExactTotal), RoundingDifference: money(r.RoundingDifference()),
		Lines: []gen.AccountingVatReturnLine{}, Warnings: []string{}, Direction: gen.Zero}
	switch r.Total.Sign() {
	case 1:
		out.Direction = gen.Pay
	case -1:
		out.Direction = gen.Receive
	}
	if t.Start.Before(settings.BooksStart) {
		out.Warnings = append(out.Warnings, fmt.Sprintf(vatWord(language, "partial"), settings.BooksStart.Format(time.DateOnly),
			settings.BooksStart.Format(time.DateOnly), t.End.Format(time.DateOnly)))
	}
	if organisation != "" {
		out.OrganisationNumber = &organisation
	} else {
		out.Warnings = append(out.Warnings, vatWord(language, "noOrg"))
	}
	for _, l := range r.Lines {
		line := gen.AccountingVatReturnLine{Group: gen.AccountingVatGroup(l.Group), Code: l.Code, Name: l.Name,
			Side: gen.AccountingVatReturnLineSide(l.Side), TaxCodeId: l.TaxCodeID, Vat: l.VAT.String(), ExactVat: money(l.ExactVAT)}
		if l.ExactBase != nil {
			line.Base, line.ExactBase, line.Rate = ptr(l.Base.String()), ptr(money(l.ExactBase)), ptr(l.Rate)
		}
		if l.Specification != "" {
			line.Specification = ptr(l.Specification)
		}
		if l.Remark != "" {
			line.Remark = ptr(l.Remark)
			out.Warnings = append(out.Warnings, fmt.Sprintf(vatWord(language, "remark"), l.Code))
		}
		if !l.RateConsistent() {
			out.Warnings = append(out.Warnings, fmt.Sprintf(vatWord(language, "rateCheck"), l.Code))
		}
		out.Lines = append(out.Lines, line)
	}
	return out
}

// GetAccountingVatTerm is a term's page.
func (s *server) GetAccountingVatTerm(ctx context.Context, req gen.GetAccountingVatTermRequestObject) (gen.GetAccountingVatTermResponseObject, error) {
	t, today, err := s.termForWrite(ctx, req.Year, req.Number)
	if err != nil {
		return nil, err
	}
	organisation, err := s.sellerOrganisationNumber(ctx)
	if err != nil {
		return nil, err
	}
	var out gen.AccountingVatTermDetail
	err = s.withReadTx(ctx, func(q *store.Queries) error {
		out, err = s.termDetail(ctx, q, t, today, organisation)
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.GetAccountingVatTerm200JSONResponse(out), nil
}

// termForWrite resolves a request's term: the books have VAT terms and this
// is one of them, begun by today.
func (s *server) termForWrite(ctx context.Context, year, number int32) (vatTerm, time.Time, error) {
	settings, err := s.settings(ctx)
	if err != nil {
		return vatTerm{}, time.Time{}, err
	}
	if settings.VatTerm == "none" {
		return vatTerm{}, time.Time{}, notRegistered()
	}
	t, ok := lookupTerm(settings, year, number)
	if !ok {
		return vatTerm{}, time.Time{}, notFound()
	}
	today, err := s.today(ctx)
	if err != nil {
		return vatTerm{}, time.Time{}, err
	}
	if t.Start.After(today) {
		return vatTerm{}, time.Time{}, notFound()
	}
	return t, today, nil
}

// CloseAccountingVatTerm closes a term (A22, R10), in one transaction
// holding the settings row FOR UPDATE: every post in flight is waited for
// and none starts until the close commits, so the figures it snapshots are
// the books'. It refuses, in this order, a closed term, one not ended, one
// with an earlier term open, one that starts before the books without
// confirmPartial, one with facts waiting under Needs attention, one the lock
// already covers, any while a closed term's figures have changed since, one
// with a reported code without a rate, one whose VAT accounts do not
// reconcile and one whose return does not add up to the ledger; then it
// keeps the closing with the return and its XML, posts the settlement
// voucher on the term's last day and moves the lock to it.
func (s *server) CloseAccountingVatTerm(ctx context.Context, req gen.CloseAccountingVatTermRequestObject) (gen.CloseAccountingVatTermResponseObject, error) {
	t, today, err := s.termForWrite(ctx, req.Year, req.Number)
	if err != nil {
		return nil, err
	}
	confirmPartial := req.Body != nil && deref(req.Body.ConfirmPartial)
	organisation, err := s.sellerOrganisationNumber(ctx)
	if err != nil {
		return nil, err
	}
	now, by := s.now(), callerID(ctx)
	var out gen.AccountingVatTermDetail
	err = s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		settings, err := q.GetSettingsForUpdate(ctx)
		if err != nil {
			return err
		}
		st, err := termRow(ctx, q, t, true)
		if err != nil {
			return err
		}
		if st.closed() {
			return conflict(codeVatTermClosed, "The term is closed", "VAT term "+t.label()+" is closed already.")
		}
		if !t.End.Before(today) {
			return conflict(codeVatTermNotEnded, "The term has not ended",
				"VAT term "+t.label()+" ends on "+t.End.Format(time.DateOnly)+": close it after that day.")
		}
		earlier, err := earliestOpenBefore(ctx, q, settings, t)
		if err != nil {
			return err
		}
		if earlier != nil {
			return conflict(codeVatTermOutOfOrder, "An earlier term is open",
				"Close VAT term "+earlier.label()+" first: terms are closed in order.")
		}
		if t.Start.Before(settings.BooksStart) && !confirmPartial {
			return conflict(codeVatTermPartial, "The term starts before the books",
				"The books start on "+settings.BooksStart.Format(time.DateOnly)+": VAT term "+t.label()+"'s return covers "+
					settings.BooksStart.Format(time.DateOnly)+"–"+t.End.Format(time.DateOnly)+" only. Close it with confirmPartial.")
		}
		waiting, err := q.CountWaitingIn(ctx, store.CountWaitingInParams{FromDate: t.Start, ToDate: t.End})
		if err != nil {
			return err
		}
		if waiting > 0 {
			return conflict(codeVatTermAttention, "Facts wait under Needs attention",
				fmt.Sprintf("%d facts dated in VAT term %s wait under Needs attention: post or resolve them first.", waiting, t.label()))
		}
		if settings.LockedThrough != nil && !settings.LockedThrough.Before(t.End) {
			return conflict(codeVatTermLockAfterEnd, "The lock covers the term",
				"The books are locked through "+settings.LockedThrough.Format(time.DateOnly)+
					": move the lock back before "+t.End.Format(time.DateOnly)+", the term's last day, so the settlement can be posted on it.")
		}
		stale, err := staleTerm(ctx, q, settings)
		if err != nil {
			return err
		}
		if stale != nil {
			return conflict(codeVatTermStale, "A closed term has changed",
				"VAT term "+stale.label()+"'s figures are not the return it was closed with any more: reopen it and close it again first.")
		}
		spec, err := computeSpecification(ctx, q, t.Start, t.End)
		if err != nil {
			return err
		}
		if code := spec.rateMissing(); code != "" {
			return conflict(codeVatTermRateMissing, "A tax code has no rate",
				"Code "+code+" has no rate on a posting's date in VAT term "+t.label()+": give the code its rate first.")
		}
		if !spec.reconciled() {
			return s.unreconciled(ctx, q, t, spec)
		}
		if err := spec.consistent(); err != nil {
			s.logger().ErrorContext(ctx, "accounting: a VAT term's return does not add up to the ledger", "term", t.label(), "error", err.Error())
			return conflict(codeVatTermInconsistent, "The return does not add up to the books", err.Error())
		}
		row, err := q.EnsureVatTerm(ctx, store.EnsureVatTermParams{Year: t.Year, Term: t.Number, TermKind: t.Kind,
			PeriodStart: t.Start, PeriodEnd: t.End, Now: now})
		if err != nil {
			return err
		}
		closings := row.Closings + 1
		view := returnView(spec.ret, t, settings, organisation)
		figures, err := json.Marshal(view)
		if err != nil {
			return err
		}
		xmlBytes, err := mva.XML(spec.ret, mva.Submission{Reference: fmt.Sprintf("vantigo-%s-%d", t.label(), closings),
			SystemName: "Vantigo", SystemVersion: buildinfo.Version, OrganisationNumber: organisation, Period: t.period()})
		if err != nil {
			return err
		}
		var voucherID *int64
		if v := spec.settlement(t, settings.ChartLanguage, row.ID, closings, now); len(v.Lines) > 0 {
			p, err := Post(ctx, tx, v)
			if err != nil {
				return err
			}
			voucherID = &p.ID
		}
		closing, err := q.InsertVatTermClosing(ctx, store.InsertVatTermClosingParams{VatTermID: row.ID, Closing: closings, Figures: figures,
			ReturnXml: string(xmlBytes), SettlementVoucherID: voucherID, Now: now, UserID: by})
		if err != nil {
			return err
		}
		if err := q.SetVatTermClosed(ctx, store.SetVatTermClosedParams{ID: row.ID, Closings: closings, ClosingID: &closing.ID, Now: now}); err != nil {
			return err
		}
		// Then the lock (R10): the settlement is posted on the term's last
		// day before the lock passes it.
		end := t.End
		if err := q.SetLockedThrough(ctx, store.SetLockedThroughParams{LockedThrough: &end, Now: now}); err != nil {
			return err
		}
		reason := fmt.Sprintf(vatWord(settings.ChartLanguage, "closed"), t.label())
		if err := q.InsertLockEvent(ctx, store.InsertLockEventParams{LockedFrom: settings.LockedThrough, LockedTo: &end, Reason: &reason,
			Now: now, UserID: by}); err != nil {
			return err
		}
		if err := q.InsertVatTermEvent(ctx, store.InsertVatTermEventParams{VatTermID: row.ID, Kind: "closed", ClosingID: &closing.ID,
			VoucherID: voucherID, Now: now, UserID: by}); err != nil {
			return err
		}
		out, err = s.termDetail(ctx, q, t, today, organisation)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.logger().InfoContext(ctx, "accounting: a VAT term was closed", "term", t.label(), "total", out.Return.Total,
		"locked_through", t.End.Format(time.DateOnly))
	return gen.CloseAccountingVatTerm200JSONResponse(out), nil
}

// unreconciled is the refusal of a close whose VAT accounts' movement is not
// the return's VAT (R10), naming the vouchers that make the difference.
func (s *server) unreconciled(ctx context.Context, q *store.Queries, t vatTerm, spec *specification) error {
	rows, err := q.VatPostings(ctx, store.VatPostingsParams{FromDate: t.Start, ToDate: t.End, Unreconciled: true, RowOffset: 0, RowLimit: 20})
	if err != nil {
		return err
	}
	var names []string
	seen := map[string]bool{}
	for _, r := range rows {
		n := display(r.FiscalYear, r.Number)
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	detail := "The VAT accounts moved " + money(spec.movement) + " in VAT term " + t.label() + ", and the return counts " +
		money(spec.counted) + ". The difference comes from"
	if len(names) > 0 {
		detail += " vouchers " + strings.Join(names, ", ")
		if len(rows) > 0 && int(rows[0].Total) > len(rows) {
			detail += " and more"
		}
	} else {
		detail += " postings"
	}
	return conflict(codeVatTermUnreconciled, "The VAT accounts do not reconcile",
		detail+": a posting on a VAT account without a tax code the return counts. Correct it in the term first.")
}

// ReopenAccountingVatTerm opens the latest closed term again while it is not
// filed (A22, R10): the lock moves back to the day before the term's first
// day, then the settlement voucher's reversal is posted on its last day. The
// closing stays, with its return and XML (H2).
func (s *server) ReopenAccountingVatTerm(ctx context.Context, req gen.ReopenAccountingVatTermRequestObject) (gen.ReopenAccountingVatTermResponseObject, error) {
	reason, err := reasonOf(req.Body, "The term cannot be reopened")
	if err != nil {
		return nil, err
	}
	t, today, err := s.termForWrite(ctx, req.Year, req.Number)
	if err != nil {
		return nil, err
	}
	organisation, err := s.sellerOrganisationNumber(ctx)
	if err != nil {
		return nil, err
	}
	now, by := s.now(), callerID(ctx)
	var out gen.AccountingVatTermDetail
	err = s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		settings, err := q.GetSettingsForUpdate(ctx)
		if err != nil {
			return err
		}
		st, err := termRow(ctx, q, t, true)
		if err != nil {
			return err
		}
		if !st.closed() {
			return conflict(codeVatTermNotClosed, "The term is open", "VAT term "+t.label()+" is not closed.")
		}
		if st.row.FiledAt != nil {
			return conflict(codeVatTermFiled, "The term is filed",
				"VAT term "+t.label()+" is marked as filed at Skatteetaten: post the correction in the open term instead.")
		}
		rows, err := q.ListVatTerms(ctx, settings.VatTerm)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.State == "closed" && r.PeriodStart.After(t.End) {
				return conflict(codeVatTermNotLatest, "A later term is closed",
					fmt.Sprintf("VAT term %d-%d is closed: reopen it first.", r.Year, r.Term))
			}
		}
		// The lock first (R10), never forward: the day before the term's
		// first day, or the books' start lifts it.
		var target *time.Time
		if before := t.Start.AddDate(0, 0, -1); !before.Before(settings.BooksStart) {
			target = &before
		}
		if settings.LockedThrough != nil && (target == nil || target.Before(*settings.LockedThrough)) {
			if err := q.SetLockedThrough(ctx, store.SetLockedThroughParams{LockedThrough: target, Now: now}); err != nil {
				return err
			}
			why := fmt.Sprintf(vatWord(settings.ChartLanguage, "reopened"), t.label(), reason)
			if len([]rune(why)) > 500 {
				why = string([]rune(why)[:500])
			}
			if err := q.InsertLockEvent(ctx, store.InsertLockEventParams{LockedFrom: settings.LockedThrough, LockedTo: target,
				Reason: &why, Now: now, UserID: by}); err != nil {
				return err
			}
		}
		var reversalID *int64
		if st.closing.SettlementVoucherID != nil {
			v, err := reversal(ctx, q, *st.closing.SettlementVoucherID, t.End, reason, settings.ChartLanguage, &by)
			if err != nil {
				return err
			}
			v.Description = fmt.Sprintf(vatWord(settings.ChartLanguage, "reversal"), t.label())
			v.Source = &Source{Module: moduleName, Kind: "vat_settlement_reversed", ID: st.row.ID, Part: fmt.Sprint(st.closing.Closing)}
			v.Rule, v.RuleVersion, v.At = ruleVatSettlementReversed, 1, now
			p, err := Post(ctx, tx, v)
			if err != nil {
				return err
			}
			reversalID = &p.ID
		}
		if err := q.ReopenVatTerm(ctx, store.ReopenVatTermParams{ID: st.row.ID, Now: now}); err != nil {
			return err
		}
		if err := q.InsertVatTermEvent(ctx, store.InsertVatTermEventParams{VatTermID: st.row.ID, Kind: "reopened", ClosingID: &st.closing.ID,
			VoucherID: reversalID, Reason: &reason, Now: now, UserID: by}); err != nil {
			return err
		}
		out, err = s.termDetail(ctx, q, t, today, organisation)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.logger().InfoContext(ctx, "accounting: a VAT term was reopened", "term", t.label())
	return gen.ReopenAccountingVatTerm200JSONResponse(out), nil
}

// reasonOf is a reopening's or an unfiling's reason, 1 to 500 characters.
func reasonOf(body *gen.AccountingVatReasonInput, title string) (string, error) {
	if body == nil {
		return "", invalidField(title, "body", "", "A body is required.")
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		return "", invalidField(title, "reason", codeLockReasonRequired, "Say why.")
	}
	if len([]rune(reason)) > 500 {
		return "", invalidField(title, "reason", "", "At most 500 characters.")
	}
	return reason, nil
}

// FileAccountingVatTerm marks a closed term as filed by hand at Skatteetaten
// (A22): the day and the reference, on the closing that stands. From then on
// it is not reopened, and the lock does not move back behind it.
func (s *server) FileAccountingVatTerm(ctx context.Context, req gen.FileAccountingVatTermRequestObject) (gen.FileAccountingVatTermResponseObject, error) {
	if req.Body == nil {
		return nil, invalidField("The term cannot be marked as filed", "body", "", "A body is required.")
	}
	reference := strings.TrimSpace(req.Body.Reference)
	filed := day(req.Body.FiledAt)
	t, today, err := s.termForWrite(ctx, req.Year, req.Number)
	if err != nil {
		return nil, err
	}
	var f fieldErrors
	if reference == "" || len([]rune(reference)) > 100 {
		f.add("reference", "Skatteetaten's reference, 1 to 100 characters.")
	}
	if filed.After(today) {
		f.add("filedAt", "Not after today, "+today.Format(time.DateOnly)+".")
	}
	if !filed.After(t.End) {
		f.add("filedAt", "After the term's last day, "+t.End.Format(time.DateOnly)+".")
	}
	if err := f.refusal("The term cannot be marked as filed"); err != nil {
		return nil, err
	}
	organisation, err := s.sellerOrganisationNumber(ctx)
	if err != nil {
		return nil, err
	}
	now, by := s.now(), callerID(ctx)
	var out gen.AccountingVatTermDetail
	err = s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if _, err := q.GetSettingsForUpdate(ctx); err != nil {
			return err
		}
		st, err := termRow(ctx, q, t, true)
		if err != nil {
			return err
		}
		if !st.closed() {
			return conflict(codeVatTermNotClosed, "The term is open", "Close VAT term "+t.label()+" before marking it as filed.")
		}
		if st.row.FiledAt != nil {
			return conflict(codeVatTermFiled, "The term is filed", "VAT term "+t.label()+" is marked as filed already.")
		}
		if err := q.SetVatTermFiled(ctx, store.SetVatTermFiledParams{ID: st.row.ID, FiledAt: &filed, FiledReference: &reference,
			FiledByUserID: &by, FiledClosingID: &st.closing.ID, Now: now}); err != nil {
			return err
		}
		if err := q.InsertVatTermEvent(ctx, store.InsertVatTermEventParams{VatTermID: st.row.ID, Kind: "filed", ClosingID: &st.closing.ID,
			FiledAt: &filed, FiledReference: &reference, Now: now, UserID: by}); err != nil {
			return err
		}
		out, err = s.termDetail(ctx, q, t, today, organisation)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.logger().InfoContext(ctx, "accounting: a VAT term was marked as filed", "term", t.label(), "filed_at", filed.Format(time.DateOnly))
	return gen.FileAccountingVatTerm200JSONResponse(out), nil
}

// UnfileAccountingVatTerm takes a term's filing mark off, with a reason its
// history and the log keep.
func (s *server) UnfileAccountingVatTerm(ctx context.Context, req gen.UnfileAccountingVatTermRequestObject) (gen.UnfileAccountingVatTermResponseObject, error) {
	reason, err := reasonOf(req.Body, "The filing mark cannot be removed")
	if err != nil {
		return nil, err
	}
	t, today, err := s.termForWrite(ctx, req.Year, req.Number)
	if err != nil {
		return nil, err
	}
	organisation, err := s.sellerOrganisationNumber(ctx)
	if err != nil {
		return nil, err
	}
	now, by := s.now(), callerID(ctx)
	var out gen.AccountingVatTermDetail
	err = s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		if _, err := q.GetSettingsForUpdate(ctx); err != nil {
			return err
		}
		st, err := termRow(ctx, q, t, true)
		if err != nil {
			return err
		}
		if st.row == nil || st.row.FiledAt == nil {
			return conflict(codeVatTermNotFiled, "The term is not filed", "VAT term "+t.label()+" is not marked as filed.")
		}
		if err := q.SetVatTermFiled(ctx, store.SetVatTermFiledParams{ID: st.row.ID, Now: now}); err != nil {
			return err
		}
		if err := q.InsertVatTermEvent(ctx, store.InsertVatTermEventParams{VatTermID: st.row.ID, Kind: "unfiled", ClosingID: st.row.FiledClosingID,
			Reason: &reason, Now: now, UserID: by}); err != nil {
			return err
		}
		out, err = s.termDetail(ctx, q, t, today, organisation)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.logger().WarnContext(ctx, "accounting: a VAT term's filing mark was removed", "term", t.label(), "reason", reason)
	return gen.UnfileAccountingVatTerm200JSONResponse(out), nil
}

// xmlFile serves a return's XML as an attachment, never cached.
type xmlFile struct {
	name string
	body []byte
}

func (r xmlFile) write(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="`+r.name+`"`)
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Content-Length", fmt.Sprint(len(r.body)))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(r.body)
	return err
}

type xmlDownload struct{ xmlFile }

func (r xmlDownload) VisitDownloadAccountingVatReturnResponse(w http.ResponseWriter) error {
	return r.write(w)
}

type closingXMLDownload struct{ xmlFile }

func (r closingXMLDownload) VisitDownloadAccountingVatTermClosingResponse(w http.ResponseWriter) error {
	return r.write(w)
}

// DownloadAccountingVatReturn is a closed term's standing closing's XML.
func (s *server) DownloadAccountingVatReturn(ctx context.Context, req gen.DownloadAccountingVatReturnRequestObject) (gen.DownloadAccountingVatReturnResponseObject, error) {
	t, _, err := s.termForWrite(ctx, req.Year, req.Number)
	if err != nil {
		return nil, err
	}
	st, err := termRow(ctx, store.New(s.deps.Pool), t, false)
	if err != nil {
		return nil, err
	}
	if !st.closed() {
		return nil, conflict(codeVatTermNotClosed, "The term is open", "Close VAT term "+t.label()+" to get its return as XML.")
	}
	return xmlDownload{xmlFile{name: fmt.Sprintf("mva-melding-%s.xml", t.label()), body: []byte(st.closing.ReturnXml)}}, nil
}

// closingOf is one of a term's closings by its number; a bare 404 when the
// term has none of that number.
func (s *server) closingOf(ctx context.Context, year, number, closing int32) (vatTerm, store.AccountingVatTermClosing, error) {
	t, _, err := s.termForWrite(ctx, year, number)
	if err != nil {
		return vatTerm{}, store.AccountingVatTermClosing{}, err
	}
	q := store.New(s.deps.Pool)
	st, err := termRow(ctx, q, t, false)
	if err != nil {
		return vatTerm{}, store.AccountingVatTermClosing{}, err
	}
	if st.row == nil {
		return vatTerm{}, store.AccountingVatTermClosing{}, notFound()
	}
	c, err := q.GetVatTermClosingByNumber(ctx, store.GetVatTermClosingByNumberParams{VatTermID: st.row.ID, Closing: closing})
	if noRows(err) {
		return vatTerm{}, store.AccountingVatTermClosing{}, notFound()
	}
	return t, c, err
}

// GetAccountingVatTermClosing is one close's return as it was kept (H2).
func (s *server) GetAccountingVatTermClosing(ctx context.Context, req gen.GetAccountingVatTermClosingRequestObject) (gen.GetAccountingVatTermClosingResponseObject, error) {
	_, c, err := s.closingOf(ctx, req.Year, req.Number, req.Closing)
	if err != nil {
		return nil, err
	}
	r, err := storedReturn(&c)
	if err != nil {
		return nil, err
	}
	return gen.GetAccountingVatTermClosing200JSONResponse(r), nil
}

// DownloadAccountingVatTermClosing is one close's XML as it was kept (H2).
func (s *server) DownloadAccountingVatTermClosing(ctx context.Context, req gen.DownloadAccountingVatTermClosingRequestObject) (gen.DownloadAccountingVatTermClosingResponseObject, error) {
	t, c, err := s.closingOf(ctx, req.Year, req.Number, req.Closing)
	if err != nil {
		return nil, err
	}
	return closingXMLDownload{xmlFile{name: fmt.Sprintf("mva-melding-%s-%d.xml", t.label(), c.Closing), body: []byte(c.ReturnXml)}}, nil
}
