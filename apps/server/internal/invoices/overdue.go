package invoices

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the overdue list (invoices payments and reminders design D12)
// and the reminder engine's wiring every reader of it shares: the run's
// preview (reminder_runs.go) judges the same set the same way, so the two
// never disagree. The whole overdue set is read on the pool through the
// rule-input loader — on one snapshot, a handful of statements whatever its
// size, no lock —
// and judged invoice by invoice by reminderrules.Next on today, before the
// action filter and the page apply (M7). One clock read per request.

// The overdue list's refusal and the warnings it and the preview carry.
const (
	codeTooManyOverdue  = "too_many_overdue"
	tooManyOverdueTitle = "Too many overdue invoices"

	warningCollectionRatesOutdated    = "collection_rates_outdated"
	warningCollectionRegimeUnreviewed = "collection_regime_unreviewed"
	warningBankDataStale              = "bank_data_stale"
	warningOcrWithoutKidPayments      = "ocr_without_kid_payments"
)

// maxOverdue is how many overdue invoices one request judges at most (D12).
const maxOverdue = 5000

// freshness is the bank data's (D10, I3): the latest booking day of any
// imported file, nil when none was ever imported; stale when it is more than
// stale_import_days before today, and always without one (m8); and the
// accounts imported as OCR giro, whose payments without a KID never arrive.
type freshness struct {
	LastBookedOn *time.Time
	Stale        bool
	OCRAccounts  []string
}

// bankFreshness reads the bank data's freshness on today with q (plan
// reading 28).
func (s *server) bankFreshness(ctx context.Context, q *store.Queries, today time.Time, staleDays int) (freshness, error) {
	last, err := q.LastBookedOn(ctx)
	if err != nil {
		return freshness{}, fmt.Errorf("invoices: read the bank data's last booking: %w", err)
	}
	accounts, err := q.OcrAccounts(ctx)
	if err != nil {
		return freshness{}, fmt.Errorf("invoices: read the OCR accounts: %w", err)
	}
	f := freshness{LastBookedOn: pgDateOf(last), OCRAccounts: accounts}
	if f.OCRAccounts == nil {
		f.OCRAccounts = []string{}
	}
	f.Stale = f.LastBookedOn == nil || f.LastBookedOn.Before(today.AddDate(0, 0, -staleDays))
	return f, nil
}

// freshnessWire is the freshness on the wire.
func freshnessWire(f freshness, staleDays int) gen.InvoicesBankFreshness {
	out := gen.InvoicesBankFreshness{Stale: f.Stale, StaleImportDays: int32(staleDays), OcrAccounts: f.OCRAccounts} //nolint:gosec // 1–30 days
	if f.LastBookedOn != nil {
		out.LastBookedOn = ptr(wireDate(*f.LastBookedOn))
	}
	return out
}

// overdueFilter is what narrows the overdue set before it is judged: a
// customer, a due date before a day, and whether paid invoices with charges
// outstanding join it.
type overdueFilter struct {
	customerID  *int32
	dueBefore   *time.Time
	withCharges bool
}

// judgedInvoice is one candidate, the engine's input on today and its
// outcome.
type judgedInvoice struct {
	row store.OverdueCandidatesRow
	in  reminderrules.Input
	out reminderrules.Outcome
}

// overdueJudgement is the whole overdue set judged on today, with what the
// list and the preview answer beside it.
type overdueJudgement struct {
	items     []judgedInvoice
	fresh     freshness
	warnings  []string
	staleDays int
}

// judgeOverdue reads and judges the overdue set on today (D12): counted
// first, too_many_overdue past maxOverdue; then the candidates, their
// inputs through ruleInputs and reminderrules.Next on each. A paid invoice
// the charges filter brought in stays only while its charges are
// outstanding. A refusal is answered as the problem, nothing else.
func (s *server) judgeOverdue(ctx context.Context, q *store.Queries, today time.Time, f overdueFilter) (overdueJudgement, *gen.InvoicesConflictProblem, error) {
	var j overdueJudgement
	params := store.CountOverdueCandidatesParams{CustomerID: f.customerID, Today: pgDate(today), WithCharges: f.withCharges}
	if f.dueBefore != nil {
		params.DueBefore = pgDate(*f.dueBefore)
	}
	n, err := q.CountOverdueCandidates(ctx, params)
	if err != nil {
		return j, nil, fmt.Errorf("invoices: count the overdue invoices: %w", err)
	}
	if n > maxOverdue {
		return j, ptr(conflict(codeTooManyOverdue, tooManyOverdueTitle, fmt.Sprintf(
			"%d invoices are overdue, and at most %d are judged at once. Narrow the list by customer or by due date.", n, maxOverdue))), nil
	}
	rows, err := q.OverdueCandidates(ctx, store.OverdueCandidatesParams(params))
	if err != nil {
		return j, nil, fmt.Errorf("invoices: read the overdue invoices: %w", err)
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	ins, err := s.ruleInputs(ctx, s.deps.Pool, ids, today)
	if err != nil {
		return j, nil, err
	}
	settings, _, err := s.reminderSettings(ctx, q)
	if err != nil {
		return j, nil, err
	}
	rateRows, err := q.RatesFor(ctx)
	if err != nil {
		return j, nil, fmt.Errorf("invoices: read the collection rates: %w", err)
	}
	rates, err := ratesOf(rateRows)
	if err != nil {
		return j, nil, err
	}
	j.staleDays = settings.StaleImportDays
	if j.fresh, err = s.bankFreshness(ctx, q, today, settings.StaleImportDays); err != nil {
		return j, nil, err
	}
	for _, r := range rows {
		in, ok := ins[r.ID]
		if !ok {
			continue // issued between the two reads is impossible; a vanished row is not judged
		}
		if principalOpenOf(in).Sign() <= 0 && reminderrules.Charges(in.Letters, in.Waivers, in.ChargePayments).Outstanding.Sign() <= 0 {
			continue // brought in by the charges filter, and nothing is outstanding
		}
		j.items = append(j.items, judgedInvoice{row: r, in: in, out: reminderrules.Next(in)})
	}
	differs := false
	for _, r := range rateRows {
		d, err := differsFromRelease(r)
		if err != nil {
			return j, nil, err
		}
		differs = differs || d
	}
	j.warnings = overdueWarnings(settings, rates, j.items, j.fresh, differs, today)
	return j, nil, nil
}

// overdueWarnings is what the list and the preview warn of (D10, D12):
// collection_rates_outdated while reminders are on and a rate the settings
// use has no row for today's half-year, or a judged letter is blocked by a
// missing one; collection_regime_unreviewed while reminders are on, today is
// under the 1988 regime past the review and a fee or the collection notice is
// in use, or a judged letter is blocked by it;
// collection_rate_differs_from_release; bank_data_stale; and
// ocr_without_kid_payments while an account is imported as OCR giro.
func overdueWarnings(settings reminderrules.Settings, rates []reminderrules.Rate, items []judgedInvoice, fresh freshness, differs bool, today time.Time) []string {
	blockedBy := func(reason string) bool {
		return slices.ContainsFunc(items, func(j judgedInvoice) bool { return slices.Contains(j.out.Reasons, reason) })
	}
	fees := settings.PersonCharge == reminderrules.ChargeFee || settings.BusinessCharge == reminderrules.ChargeFee
	var inUse []string
	if settings.LateInterest {
		inUse = append(inUse, reminderrules.KindLateInterest)
	}
	if settings.BusinessCharge == reminderrules.ChargeCompensation {
		inUse = append(inUse, reminderrules.KindCompensation)
	}
	if fees {
		inUse = append(inUse, reminderrules.KindInkassosats)
	}
	outdated := slices.ContainsFunc(inUse, func(kind string) bool {
		return reminderrules.Outdated(rates, kind, today, today) != nil
	})
	lapsed := reminderrules.RegimeOn(settings, today) == reminderrules.Regime1988 &&
		today.After(settings.RegimeReviewedThrough) && (fees || settings.CollectionNotice)
	warnings := []string{}
	if (settings.Enabled && outdated) || blockedBy(reminderrules.ReasonRatesOutdated) {
		warnings = append(warnings, warningCollectionRatesOutdated)
	}
	if (settings.Enabled && lapsed) || blockedBy(reminderrules.ReasonRegimeUnreviewed) {
		warnings = append(warnings, warningCollectionRegimeUnreviewed)
	}
	if differs {
		warnings = append(warnings, warningCollectionRateDiffersFromRelease)
	}
	if fresh.Stale {
		warnings = append(warnings, warningBankDataStale)
	}
	if len(fresh.OCRAccounts) > 0 {
		warnings = append(warnings, warningOcrWithoutKidPayments)
	}
	return warnings
}

// principalOpenOf is what an Input says is open of the principal: gross less
// the credit notes, the live payments and the live reservations — the
// engine's own figure.
func principalOpenOf(in reminderrules.Input) *big.Rat {
	open := new(big.Rat).Set(in.Invoice.Gross)
	for _, c := range in.Credits {
		open.Sub(open, c.Gross)
	}
	for _, p := range in.Payments {
		open.Sub(open, p.Amount)
	}
	for _, r := range in.Reservations {
		open.Sub(open, r.Amount)
	}
	return open
}

// interestTodayOf is the late interest accrued on in.L as a letter that day
// would claim it, nil when none applies, none has accrued yet, or a rate the
// period needs is missing (D8, D12).
func interestTodayOf(in reminderrules.Input) *float64 {
	total, from, _, outdated := reminderrules.Interest(in)
	if from == nil || outdated != nil {
		return nil
	}
	return ptr(floatFromRat(total, 2))
}

// daysOverdue is how many days today is past the effective due date E (D12),
// never below zero.
func daysOverdue(due, today time.Time) int32 {
	days := int32(today.Sub(reminderrules.EffectiveDue(due)) / (24 * time.Hour)) //nolint:gosec // a span of days
	return max(days, 0)
}

// nextActionWire is the engine's outcome on the wire.
func nextActionWire(out reminderrules.Outcome) gen.InvoicesNextAction {
	w := gen.InvoicesNextAction{
		Action:      gen.InvoicesNextActionAction(out.Action),
		Reasons:     make([]gen.InvoicesNextActionReasons, 0, len(out.Reasons)),
		ChargeNotes: make([]gen.InvoicesNextActionChargeNotes, 0, len(out.ChargeNotes)),
	}
	for _, r := range out.Reasons {
		w.Reasons = append(w.Reasons, gen.InvoicesNextActionReasons(r))
	}
	for _, n := range out.ChargeNotes {
		w.ChargeNotes = append(w.ChargeNotes, gen.InvoicesNextActionChargeNotes(n))
	}
	if !out.EarliestOn.IsZero() {
		w.EarliestOn = ptr(wireDate(out.EarliestOn))
	}
	if out.Outdated != nil {
		w.Outdated = &gen.InvoicesOutdatedRate{Kind: out.Outdated.Kind, HalfYear: out.Outdated.HalfYear}
	}
	if out.Letter != nil {
		w.Letter = ptr(letterFactsWire(*out.Letter))
	}
	return w
}

// letterFactsWire is a letter's facts as the engine gives them, on the wire.
func letterFactsWire(f reminderrules.LetterFacts) gen.InvoicesLetterFacts {
	w := gen.InvoicesLetterFacts{
		Level: gen.InvoicesLetterFactsLevel(f.Level), AnnouncesCollection: f.AnnouncesCollection,
		Regime: gen.InvoicesLetterFactsRegime(f.Regime), FeeKind: gen.InvoicesLetterFactsFeeKind(f.FeeKind),
		PrincipalOpen: floatFromRat(f.PrincipalOpen, 2), ChargesEarlier: floatFromRat(f.ChargesEarlier, 2),
		Fee: floatFromRat(f.Fee, 2), Compensation: floatFromRat(f.Compensation, 2),
		Interest: floatFromRat(f.Interest, 2), InterestWaived: floatFromRat(f.InterestWaived, 2),
		InterestPaid: floatFromRat(f.InterestPaid, 2), Total: floatFromRat(f.Total, 2),
		InterestSegments: segmentsWire(f.Segments), Deadline: wireDate(f.Deadline),
	}
	if f.Inkassosats != nil {
		w.Inkassosats = ptr(floatFromRat(f.Inkassosats, 2))
	}
	if f.InterestFrom != nil {
		w.InterestFrom = ptr(wireDate(*f.InterestFrom))
	}
	return w
}

// segmentsWire is interest segments on the wire.
func segmentsWire(segs []reminderrules.Segment) []gen.InvoicesInterestSegment {
	out := make([]gen.InvoicesInterestSegment, 0, len(segs))
	for _, s := range segs {
		out = append(out, gen.InvoicesInterestSegment{
			From: wireDate(s.From), To: wireDate(s.To), Rate: floatFromRat(s.Rate, 2), Base: floatFromRat(s.Base, 2),
		})
	}
	return out
}

// lastLetterWire is an invoice's latest letter that was not withdrawn, by
// sequence, nil when it has none.
func lastLetterWire(letters []reminderrules.Letter) *gen.InvoicesLastLetter {
	var last *reminderrules.Letter
	for i, l := range letters {
		if l.Status == reminderrules.StatusWithdrawn {
			continue
		}
		if last == nil || l.Sequence > last.Sequence {
			last = &letters[i]
		}
	}
	if last == nil {
		return nil
	}
	w := &gen.InvoicesLastLetter{
		Id: last.ID, Sequence: int32(last.Sequence), Level: gen.InvoicesLastLetterLevel(last.Level), //nolint:gosec // a smallint
		AnnouncesCollection: last.AnnouncesCollection, Status: gen.InvoicesLastLetterStatus(last.Status),
	}
	if last.SentOn != nil {
		w.SentOn = ptr(wireDate(*last.SentOn))
	}
	if last.Deadline != nil {
		w.Deadline = ptr(wireDate(*last.Deadline))
	}
	return w
}

// holdWire is a hold row on the wire (D11).
func holdWire(h store.InvoicesInvoiceHold) gen.InvoicesHold {
	return gen.InvoicesHold{
		Id: h.ID, Kind: gen.InvoicesHoldKind(h.Kind), Note: h.Note, PlacedAt: h.PlacedAt, PlacedBy: h.PlacedByUserID,
		LiftedAt: h.LiftedAt, LiftedBy: h.LiftedByUserID, LiftNote: h.LiftNote, ChargesAllowed: h.ChargesAllowed,
	}
}

// handoffWire is a hand-off row on the wire (D11).
func handoffWire(h store.InvoicesCollectionHandoff) gen.InvoicesHandoff {
	return gen.InvoicesHandoff{
		Id: h.ID, HandedOn: wireDate(h.HandedOn.Time), Agency: h.Agency, AgencyReference: h.AgencyReference,
		Note: h.Note, CreatedAt: h.CreatedAt, CreatedBy: h.CreatedByUserID, WithdrawnOn: wireDateOf(h.WithdrawnOn),
		WithdrawnBy: h.WithdrawnByUserID, WithdrawalReason: h.WithdrawalReason,
	}
}

// overdueItemWire is one judged invoice as the list answers it.
func overdueItemWire(j judgedInvoice, today time.Time) gen.InvoicesOverdueItem {
	r := j.row
	state := reminderrules.Charges(j.in.Letters, j.in.Waivers, j.in.ChargePayments)
	interest := interestTodayOf(j.in)
	charges := chargesWire(state)
	charges.InterestToday = interest
	item := gen.InvoicesOverdueItem{
		InvoiceId: r.ID, CustomerId: r.CustomerID, BuyerType: r.BuyerType,
		IssueDate: wireDate(r.IssueDate.Time), DueDate: wireDate(r.DueDate.Time),
		DaysOverdue:   daysOverdue(utcDay(r.DueDate.Time), today),
		PrincipalOpen: floatFromRat(principalOpenOf(j.in), 2), Charges: *charges, InterestToday: interest,
		Delivered: reminderrules.Delivered(j.in), LastLetter: lastLetterWire(j.in.Letters),
		NextAction: nextActionWire(j.out), PolicyMode: gen.InvoicesOverdueItemPolicyMode(j.in.Mode),
	}
	if r.Number != nil {
		item.Number = *r.Number
	}
	if r.BuyerName != nil {
		item.BuyerName = *r.BuyerName
	}
	return item
}

// GetInvoicesOverdue List the overdue invoices
// (GET /api/v1/invoices/overdue)
func (s *server) GetInvoicesOverdue(ctx context.Context, req gen.GetInvoicesOverdueRequestObject) (gen.GetInvoicesOverdueResponseObject, error) {
	p := req.Params
	if errs := validatePageParams(p.Page, p.PageSize); len(errs) > 0 {
		return gen.GetInvoicesOverdue400ApplicationProblemPlusJSONResponse(apicommon.Problem(invalidQueryTitle, strings.Join(errs, " "))), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)
	today := businessDay(s.deps.Clock())
	f := overdueFilter{customerID: p.CustomerId, withCharges: p.Charges != nil && *p.Charges == gen.GetInvoicesOverdueParamsCharges("outstanding")}
	if p.DueBefore != nil {
		f.dueBefore = ptr(utcDay(p.DueBefore.Time))
	}
	q := store.New(s.deps.Pool)
	j, refusal, err := s.judgeOverdue(ctx, q, today, f)
	switch {
	case err != nil:
		return nil, err
	case refusal != nil:
		return gen.GetInvoicesOverdue409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	// The action filter, then the page — after the whole set is judged (M7).
	kept := j.items
	if p.Action != nil {
		kept = slices.DeleteFunc(slices.Clone(kept), func(i judgedInvoice) bool { return string(i.out.Action) != string(*p.Action) })
	}
	total := len(kept)
	from := min(int((page-1)*pageSize), total)
	pageItems := kept[from:min(from+int(pageSize), total)]

	ids := make([]int64, 0, len(pageItems))
	for _, i := range pageItems {
		ids = append(ids, i.row.ID)
	}
	holds, err := q.LiveHolds(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the overdue invoices' holds: %w", err)
	}
	handoffs, err := q.LiveHandoffs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the overdue invoices' hand-offs: %w", err)
	}
	items := make([]gen.InvoicesOverdueItem, 0, len(pageItems))
	for _, i := range pageItems {
		item := overdueItemWire(i, today)
		for _, h := range holds {
			if h.InvoiceID == i.row.ID {
				item.Hold = ptr(holdWire(h))
			}
		}
		for _, h := range handoffs {
			if h.InvoiceID == i.row.ID {
				item.Handoff = ptr(handoffWire(h))
			}
		}
		items = append(items, item)
	}
	warnings := make([]gen.InvoicesOverdueResponseWarnings, 0, len(j.warnings))
	for _, w := range j.warnings {
		warnings = append(warnings, gen.InvoicesOverdueResponseWarnings(w))
	}
	return gen.GetInvoicesOverdue200JSONResponse(gen.InvoicesOverdueResponse{
		Items: items, Total: int32(total), Freshness: freshnessWire(j.fresh, j.staleDays), Warnings: warnings, //nolint:gosec // at most 5 000
	}), nil
}

// withReminders answers an issued invoice's letters and what the reminder
// engine says to do next today on resp (D8, D10), and the interest accrued
// to today in its charges block (D12): the engine's input through the
// rule-input loader on a snapshot of its own on the pool — every caller
// renders a document after its transaction has ended, never under a lock —
// and the letters with q. A letter's recipient is answered only to a caller
// holding invoices:payments.
func (s *server) withReminders(ctx context.Context, q *store.Queries, inv store.InvoicesInvoice, today time.Time, resp *gen.InvoicesInvoiceResponse) error {
	ins, err := s.ruleInputs(ctx, s.deps.Pool, []int64{inv.ID}, today)
	if err != nil {
		return err
	}
	in, ok := ins[inv.ID]
	if !ok {
		return fmt.Errorf("invoices: document %d is not an issued invoice the rules judge", inv.ID)
	}
	resp.NextAction = ptr(nextActionWire(reminderrules.Next(in)))
	if resp.Charges != nil {
		resp.Charges.InterestToday = interestTodayOf(in)
	}
	rows, err := q.LettersOfInvoice(ctx, inv.ID)
	if err != nil {
		return fmt.Errorf("invoices: read document %d's letters: %w", inv.ID, err)
	}
	showRecipient := len(rows) > 0 && s.has(ctx, "invoices:payments")
	letters := make([]gen.InvoicesReminder, 0, len(rows))
	for _, r := range rows {
		l, err := reminderWire(r, showRecipient)
		if err != nil {
			return err
		}
		letters = append(letters, l)
	}
	resp.Reminders = &letters
	return nil
}
