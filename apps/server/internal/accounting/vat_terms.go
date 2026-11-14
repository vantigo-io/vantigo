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
	openapi_types "github.com/oapi-codegen/runtime/types"

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
		"remark": "Code %s: the sign is the opposite of the code's, so the line carries the remark kreditnota."},
	"nb": {"settlement": "Mva-oppgjør, termin %s", "reversal": "Tilbakeføring av mva-oppgjør, termin %s", "closed": "Mva-termin %s lukket",
		"reopened": "Mva-termin %s åpnet igjen: %s", "noOrg": "Invoices har ikke selgerens organisasjonsnummer: XML-filen er uten det.",
		"remark": "Kode %s: fortegnet er motsatt av kodens, så linjen har merknaden kreditnota."},
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

// termRow is a term's stored row, nil when it has none (never closed).
func termRow(ctx context.Context, q *store.Queries, t vatTerm, forUpdate bool) (*store.AccountingVatTerm, error) {
	params := store.GetVatTermParams{Year: t.Year, Term: t.Number}
	var row store.AccountingVatTerm
	var err error
	if forUpdate {
		row, err = q.GetVatTermForUpdate(ctx, store.GetVatTermForUpdateParams(params))
	} else {
		row, err = q.GetVatTerm(ctx, params)
	}
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// termItem is a term as the contract answers it; total is its return's
// total, as closed or as the books stand now.
func termItem(t vatTerm, row *store.AccountingVatTerm, settings store.AccountingSetting, today time.Time, total string,
	refs map[int64]string) gen.AccountingVatTermItem {
	out := gen.AccountingVatTermItem{Year: t.Year, Number: t.Number, PeriodStart: apiDate(t.Start), PeriodEnd: apiDate(t.End),
		PeriodName: t.period().Name(), DueDate: apiDate(t.due()), State: gen.AccountingVatTermStateOpen, Total: total,
		LockCovers: settings.LockedThrough != nil && !settings.LockedThrough.Before(t.End)}
	if row != nil && row.State == "closed" {
		out.State = gen.AccountingVatTermStateClosed
		out.ClosedAt, out.ClosedByUserId = row.ClosedAt, uuidPtr(row.ClosedByUserID)
		if row.SettlementVoucherID != nil {
			out.SettlementVoucher = refOf(refs, row.SettlementVoucherID)
		}
	}
	if row != nil && row.FiledAt != nil {
		out.FiledAt, out.FiledReference, out.FiledByUserId = apiDatePtr(row.FiledAt), row.FiledReference, uuidPtr(row.FiledByUserID)
	}
	out.NeedsClosing = out.State == gen.AccountingVatTermStateOpen && t.End.Before(today)
	out.NeedsFiling = out.FiledAt == nil && t.due().Before(today)
	return out
}

func uuidPtr[T ~[16]byte](u *T) *openapi_types.UUID {
	if u == nil {
		return nil
	}
	v := openapi_types.UUID(*u)
	return &v
}

// storedReturn is a closed term's return as its close stored it.
func storedReturn(row *store.AccountingVatTerm) (gen.AccountingVatReturn, error) {
	var r gen.AccountingVatReturn
	if err := json.Unmarshal(row.Figures, &r); err != nil {
		return r, fmt.Errorf("accounting: read term %d-%d's figures: %w", row.Year, row.Term, err)
	}
	return r, nil
}

// termItems is every term through today's with its row and total: as closed
// for a closed term, from the books for an open one.
func (s *server) termItems(ctx context.Context, q *store.Queries, settings store.AccountingSetting, today time.Time) ([]gen.AccountingVatTermItem, error) {
	terms := termsThrough(settings.VatTerm, settings.BooksStart, today)
	rows, err := q.ListVatTerms(ctx)
	if err != nil {
		return nil, err
	}
	byTerm := map[[2]int32]*store.AccountingVatTerm{}
	var ids []int64
	for i := range rows {
		byTerm[[2]int32{rows[i].Year, rows[i].Term}] = &rows[i]
		if rows[i].SettlementVoucherID != nil {
			ids = append(ids, *rows[i].SettlementVoucherID)
		}
	}
	refs, err := voucherRefs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	out := []gen.AccountingVatTermItem{}
	for _, t := range terms {
		row := byTerm[[2]int32{t.Year, t.Number}]
		var total string
		if row != nil && row.State == "closed" {
			r, err := storedReturn(row)
			if err != nil {
				return nil, err
			}
			total = r.Total
		} else {
			spec, err := computeSpecification(ctx, q, t.Start, t.End)
			if err != nil {
				return nil, err
			}
			total = spec.ret.Total.String()
		}
		out = append(out, termItem(t, row, settings, today, total, refs))
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

// seller is what the return says of the company: its organisation number,
// read from Invoices with no transaction open; "" when Invoices has none, or
// one the schema does not take.
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
// books stand), its specification, the close's checks and its history.
func (s *server) termDetail(ctx context.Context, q *store.Queries, t vatTerm, today time.Time, organisation string) (gen.AccountingVatTermDetail, error) {
	settings, err := q.GetSettings(ctx)
	if err != nil {
		return gen.AccountingVatTermDetail{}, err
	}
	row, err := termRow(ctx, q, t, false)
	if err != nil {
		return gen.AccountingVatTermDetail{}, err
	}
	spec, err := computeSpecification(ctx, q, t.Start, t.End)
	if err != nil {
		return gen.AccountingVatTermDetail{}, err
	}
	out := gen.AccountingVatTermDetail{Events: []gen.AccountingVatTermEvent{}}
	out.Specification, err = spec.view(ctx, q, s.now())
	if err != nil {
		return gen.AccountingVatTermDetail{}, err
	}
	closed := row != nil && row.State == "closed"
	if closed {
		if out.Return, err = storedReturn(row); err != nil {
			return gen.AccountingVatTermDetail{}, err
		}
	} else {
		out.Return = returnView(spec.ret, t, organisation, settings.ChartLanguage)
	}
	var ids []int64
	if row != nil && row.SettlementVoucherID != nil {
		ids = append(ids, *row.SettlementVoucherID)
	}
	var events []store.ListVatTermEventsRow
	if row != nil {
		if events, err = q.ListVatTermEvents(ctx, row.ID); err != nil {
			return gen.AccountingVatTermDetail{}, err
		}
	}
	refs, err := voucherRefs(ctx, q, ids)
	if err != nil {
		return gen.AccountingVatTermDetail{}, err
	}
	out.Term = termItem(t, row, settings, today, out.Return.Total, refs)
	for _, e := range events {
		ev := gen.AccountingVatTermEvent{Kind: gen.AccountingVatTermEventKind(e.Kind), At: e.At, ByUserId: openapi_types.UUID(e.ByUserID),
			Reason: e.Reason, FiledAt: apiDatePtr(e.FiledAt), FiledReference: e.FiledReference}
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
		LockAfterEnd: settings.LockedThrough != nil && !settings.LockedThrough.Before(t.End)}
	var err error
	if out.Attention, err = q.CountWaitingIn(ctx, store.CountWaitingInParams{FromDate: t.Start, ToDate: t.End}); err != nil {
		return out, err
	}
	earlier, err := earliestOpenBefore(ctx, q, settings, t)
	if err != nil {
		return out, err
	}
	if earlier != nil {
		item := termItem(*earlier, nil, settings, today, "0", nil)
		out.EarlierOpen = &item
	}
	return out, nil
}

// earliestOpenBefore is the earliest term before t that is not closed, nil
// when every one is (A22: terms close in order).
func earliestOpenBefore(ctx context.Context, q *store.Queries, settings store.AccountingSetting, t vatTerm) (*vatTerm, error) {
	rows, err := q.ListVatTerms(ctx)
	if err != nil {
		return nil, err
	}
	closed := map[[2]int32]bool{}
	for _, r := range rows {
		closed[[2]int32{r.Year, r.Term}] = r.State == "closed"
	}
	for _, e := range termsThrough(settings.VatTerm, settings.BooksStart, t.Start.AddDate(0, 0, -1)) {
		if e.before(t) && !closed[[2]int32{e.Year, e.Number}] {
			return &e, nil
		}
	}
	return nil, nil
}

// returnView is a return as the contract answers it, for term t.
func returnView(r mva.Return, t vatTerm, organisation, language string) gen.AccountingVatReturn {
	out := gen.AccountingVatReturn{Year: t.Year, PeriodName: t.period().Name(), PeriodStart: apiDate(t.Start), PeriodEnd: apiDate(t.End),
		Total: r.Total.String(), ExactTotal: money(r.ExactTotal), RoundingDifference: money(r.RoundingDifference()),
		Lines: []gen.AccountingVatReturnLine{}, Warnings: []string{}, Direction: gen.Zero}
	switch r.Total.Sign() {
	case 1:
		out.Direction = gen.Pay
	case -1:
		out.Direction = gen.Receive
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
		out.Lines = append(out.Lines, line)
	}
	return out
}

// GetAccountingVatTerm is a term's page.
func (s *server) GetAccountingVatTerm(ctx context.Context, req gen.GetAccountingVatTermRequestObject) (gen.GetAccountingVatTermResponseObject, error) {
	settings, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	if settings.VatTerm == "none" {
		return nil, notRegistered()
	}
	t, ok := lookupTerm(settings, req.Year, req.Number)
	if !ok {
		return nil, notFound()
	}
	today, err := s.today(ctx)
	if err != nil {
		return nil, err
	}
	if t.Start.After(today) {
		return nil, notFound()
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

// termForWrite resolves a write's term: the books have VAT terms and this is
// one of them.
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
// with an earlier term open, one with facts waiting under Needs attention,
// one the lock already covers and one whose VAT accounts do not reconcile;
// then it builds the return and its XML, posts the settlement voucher on the
// term's last day and moves the lock to it.
func (s *server) CloseAccountingVatTerm(ctx context.Context, req gen.CloseAccountingVatTermRequestObject) (gen.CloseAccountingVatTermResponseObject, error) {
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
	var posted *Posted
	err = s.withTx(ctx, func(tx pgx.Tx, q *store.Queries) error {
		settings, err := q.GetSettingsForUpdate(ctx)
		if err != nil {
			return err
		}
		row, err := termRow(ctx, q, t, true)
		if err != nil {
			return err
		}
		if row != nil && row.State == "closed" {
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
		spec, err := computeSpecification(ctx, q, t.Start, t.End)
		if err != nil {
			return err
		}
		if !spec.reconciled() {
			return s.unreconciled(ctx, q, t, spec)
		}
		closings := int32(1)
		if row != nil {
			closings = row.Closings + 1
		}
		view := returnView(spec.ret, t, organisation, settings.ChartLanguage)
		figures, err := json.Marshal(view)
		if err != nil {
			return err
		}
		xmlBytes, err := mva.XML(spec.ret, mva.Submission{Reference: fmt.Sprintf("vantigo-%s-%d", t.label(), closings),
			SystemName: "Vantigo", SystemVersion: buildinfo.Version, OrganisationNumber: organisation, Period: t.period()})
		if err != nil {
			return err
		}
		// The row first, so the settlement voucher's source names it.
		saved, err := q.CloseVatTerm(ctx, store.CloseVatTermParams{Year: t.Year, Term: t.Number, TermKind: t.Kind, PeriodStart: t.Start,
			PeriodEnd: t.End, Closings: closings, Figures: figures, ReturnXml: ptr(string(xmlBytes)), Now: &now, UserID: &by})
		if err != nil {
			return err
		}
		termID := saved.ID
		v := spec.settlement(t, settings.ChartLanguage, termID, closings, now)
		if len(v.Lines) > 0 {
			p, err := Post(ctx, tx, v)
			if err != nil {
				return err
			}
			posted = &p
			if _, err := q.CloseVatTerm(ctx, store.CloseVatTermParams{Year: t.Year, Term: t.Number, TermKind: t.Kind, PeriodStart: t.Start,
				PeriodEnd: t.End, Closings: closings, Figures: figures, ReturnXml: ptr(string(xmlBytes)), SettlementVoucherID: &p.ID,
				Now: &now, UserID: &by}); err != nil {
				return err
			}
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
		var voucherID *int64
		if posted != nil {
			voucherID = &posted.ID
		}
		if err := q.InsertVatTermEvent(ctx, store.InsertVatTermEventParams{VatTermID: termID, Kind: "closed", VoucherID: voucherID,
			Now: now, UserID: by}); err != nil {
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
// day, then the settlement voucher's reversal is posted on its last day.
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
		row, err := termRow(ctx, q, t, true)
		if err != nil {
			return err
		}
		if row == nil || row.State != "closed" {
			return conflict(codeVatTermNotClosed, "The term is open", "VAT term "+t.label()+" is not closed.")
		}
		if row.FiledAt != nil {
			return conflict(codeVatTermFiled, "The term is filed",
				"VAT term "+t.label()+" is marked as filed at Skatteetaten: post the correction in the open term instead.")
		}
		rows, err := q.ListVatTerms(ctx)
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
		if row.SettlementVoucherID != nil {
			v, err := reversal(ctx, q, *row.SettlementVoucherID, t.End, reason, settings.ChartLanguage, &by)
			if err != nil {
				return err
			}
			v.Description = fmt.Sprintf(vatWord(settings.ChartLanguage, "reversal"), t.label())
			v.Source = &Source{Module: moduleName, Kind: "vat_settlement_reversed", ID: row.ID, Part: fmt.Sprint(row.Closings)}
			v.Rule, v.RuleVersion, v.At = ruleVatSettlementReversed, 1, now
			p, err := Post(ctx, tx, v)
			if err != nil {
				return err
			}
			reversalID = &p.ID
		}
		if err := q.ReopenVatTerm(ctx, store.ReopenVatTermParams{ID: row.ID, Now: now}); err != nil {
			return err
		}
		if err := q.InsertVatTermEvent(ctx, store.InsertVatTermEventParams{VatTermID: row.ID, Kind: "reopened", VoucherID: reversalID,
			Reason: &reason, Now: now, UserID: by}); err != nil {
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
// (A22): the day and the reference. From then on it is not reopened, and the
// lock does not move back behind it.
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
		row, err := termRow(ctx, q, t, true)
		if err != nil {
			return err
		}
		if row == nil || row.State != "closed" {
			return conflict(codeVatTermNotClosed, "The term is open", "Close VAT term "+t.label()+" before marking it as filed.")
		}
		if row.FiledAt != nil {
			return conflict(codeVatTermFiled, "The term is filed", "VAT term "+t.label()+" is marked as filed already.")
		}
		if err := q.SetVatTermFiled(ctx, store.SetVatTermFiledParams{ID: row.ID, FiledAt: &filed, FiledReference: &reference,
			FiledByUserID: &by, Now: now}); err != nil {
			return err
		}
		if err := q.InsertVatTermEvent(ctx, store.InsertVatTermEventParams{VatTermID: row.ID, Kind: "filed", FiledAt: &filed,
			FiledReference: &reference, Now: now, UserID: by}); err != nil {
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
		row, err := termRow(ctx, q, t, true)
		if err != nil {
			return err
		}
		if row == nil || row.FiledAt == nil {
			return conflict(codeVatTermNotFiled, "The term is not filed", "VAT term "+t.label()+" is not marked as filed.")
		}
		if err := q.SetVatTermFiled(ctx, store.SetVatTermFiledParams{ID: row.ID, Now: now}); err != nil {
			return err
		}
		if err := q.InsertVatTermEvent(ctx, store.InsertVatTermEventParams{VatTermID: row.ID, Kind: "unfiled", Reason: &reason,
			Now: now, UserID: by}); err != nil {
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

// xmlDownload serves a return's XML as an attachment, never cached.
type xmlDownload struct {
	name string
	body []byte
}

func (r xmlDownload) VisitDownloadAccountingVatReturnResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="`+r.name+`"`)
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Content-Length", fmt.Sprint(len(r.body)))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(r.body)
	return err
}

// DownloadAccountingVatReturn is a closed term's XML as its close stored it.
func (s *server) DownloadAccountingVatReturn(ctx context.Context, req gen.DownloadAccountingVatReturnRequestObject) (gen.DownloadAccountingVatReturnResponseObject, error) {
	t, _, err := s.termForWrite(ctx, req.Year, req.Number)
	if err != nil {
		return nil, err
	}
	row, err := termRow(ctx, store.New(s.deps.Pool), t, false)
	if err != nil {
		return nil, err
	}
	if row == nil || row.State != "closed" || row.ReturnXml == nil {
		return nil, conflict(codeVatTermNotClosed, "The term is open", "Close VAT term "+t.label()+" to get its return as XML.")
	}
	return xmlDownload{name: fmt.Sprintf("mva-melding-%s.xml", t.label()), body: []byte(*row.ReturnXml)}, nil
}
