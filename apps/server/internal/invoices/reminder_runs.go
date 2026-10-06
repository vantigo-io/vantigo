package invoices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the reminder runs (invoices payments and reminders design
// D10): the preview, which judges the overdue set as the list does and says
// what each letter would be today, nothing written; and the run, which makes
// the letters the caller saw — one invoice per transaction, judged again
// under its lock, so a payment, a hold or another run that got there first
// is seen. A letter is created without its facts: they are written when it
// is sent (the worker) or printed (a print batch), since its deadline runs
// from its sending and a fee is judged on its date (amendment 12).
//
// The run's order (D18): every directory read before any lock — the billing
// profiles, one per distinct customer — then the run row, then per item the
// invoice FOR UPDATE and the letter inserted, then the counts set once.

// The run's refusals, its skips and the letter's channel warnings.
const (
	codeRemindersDisabled          = "reminders_disabled"
	codeCollectionRatesOutdated    = "collection_rates_outdated"
	codeCollectionRegimeUnreviewed = "collection_regime_unreviewed"
	codeBankImportStale            = "bank_import_stale"

	cannotRunTitle       = "The reminder run cannot be made"
	invalidRunTitle      = "Invalid reminder run"
	skipCustomerAnonymed = "customer_anonymised"
	skipActionChanged    = "action_changed"

	warningReminderEmailMissing = "reminder_email_missing"
	warningMailUnavailable      = "mail_unavailable"

	channelEmail = "email"
	channelPaper = "paper"
)

// maxRunItems is how many invoices one run takes at most (D10).
const maxRunItems = 500

// runItemAfterLock is called inside a run item's transaction right after the
// invoice is locked, so a race test can hold the item there while another
// write waits on the invoice; an error rolls the item back and ends the run.
// nil in production.
var runItemAfterLock func(ctx context.Context, invoiceID int64) error

// recipientOf is where a customer's letters go (D10), read through the
// directory before any lock: paper when the billing profile's
// ReminderDelivery says paper; otherwise e-mail to ReminderEmail — paper
// with reminder_email_missing when it has none, paper with mail_unavailable
// when this installation cannot send e-mail. A customer the directory no
// longer knows has no address. An error is the directory's.
func (s *server) recipientOf(ctx context.Context, customerID int32) (channel, recipient string, warnings []string, err error) {
	profile, err := s.customerProfile(ctx, customerID)
	if err != nil {
		return "", "", nil, fmt.Errorf("invoices: read customer %d's billing profile: %w", customerID, err)
	}
	switch {
	case profile != nil && profile.ReminderDelivery == channelPaper:
		return channelPaper, "", []string{}, nil
	case !s.mailAvailable():
		return channelPaper, "", []string{warningMailUnavailable}, nil
	case profile == nil || strings.TrimSpace(profile.ReminderEmail) == "":
		return channelPaper, "", []string{warningReminderEmailMissing}, nil
	}
	return channelEmail, strings.TrimSpace(profile.ReminderEmail), []string{}, nil
}

// letterLanguage is a letter's language (plan reading 14): the buyer
// snapshot's when it is English, Norwegian otherwise.
func letterLanguage(buyerLanguage *string) string {
	if buyerLanguage != nil && strings.EqualFold(strings.TrimSpace(*buyerLanguage), "en") {
		return "en"
	}
	return "nb"
}

// carriesCharge is whether a letter would claim a fee, the compensation or
// interest: what a run on stale bank data needs confirmed (D10).
func carriesCharge(f *reminderrules.LetterFacts) bool {
	return f != nil && (f.Fee.Sign() > 0 || f.Compensation.Sign() > 0 || f.Interest.Sign() > 0)
}

// interestSegment is one interest segment as a letter's interest_segments
// column stores it, a JSON array of them: days as 2006-01-02, the rate and
// the base as exact decimal text. Whatever writes a letter's facts (the
// worker, the print batch) writes this shape.
type interestSegment struct {
	From string `json:"from"`
	To   string `json:"to"`
	Rate string `json:"rate"`
	Base string `json:"base"`
}

// storedSegmentsWire is a letter's stored segments on the wire.
func storedSegmentsWire(raw []byte) ([]gen.InvoicesInterestSegment, error) {
	var stored []interestSegment
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("invoices: read a letter's interest segments: %w", err)
	}
	out := make([]gen.InvoicesInterestSegment, 0, len(stored))
	for _, s := range stored {
		from, err := time.Parse(time.DateOnly, s.From)
		if err != nil {
			return nil, fmt.Errorf("invoices: read a letter's interest segment: %w", err)
		}
		to, err := time.Parse(time.DateOnly, s.To)
		if err != nil {
			return nil, fmt.Errorf("invoices: read a letter's interest segment: %w", err)
		}
		rate, ok := new(big.Rat).SetString(s.Rate)
		base, ok2 := new(big.Rat).SetString(s.Base)
		if !ok || !ok2 {
			return nil, errors.New("invoices: read a letter's interest segment: not a decimal")
		}
		out = append(out, gen.InvoicesInterestSegment{
			From: wireDate(from), To: wireDate(to), Rate: floatFromRat(rate, 2), Base: floatFromRat(base, 2),
		})
	}
	return out, nil
}

// reminderWire is a stored letter on the wire; its recipient only when
// showRecipient.
func reminderWire(r store.InvoicesReminder, showRecipient bool) (gen.InvoicesReminder, error) {
	w := gen.InvoicesReminder{
		Id: r.ID, InvoiceId: r.InvoiceID, Sequence: int32(r.Sequence), Level: gen.InvoicesReminderLevel(r.Level),
		AnnouncesCollection: r.AnnouncesCollection, Channel: gen.InvoicesReminderChannel(r.Channel),
		Language: strings.TrimSpace(r.Language), Status: gen.InvoicesReminderStatus(r.Status), RunId: r.RunID,
		PrintBatchId: r.PrintBatchID, CreatedAt: r.CreatedAt, CreatedBy: r.CreatedByUserID,
		SentOn: wireDateOf(r.SentOn), Deadline: wireDateOf(r.Deadline), InterestFrom: wireDateOf(r.InterestFrom),
		Attempts: r.Attempts, NextAttemptAt: r.NextAttemptAt, LastError: r.LastError, FailedAt: r.FailedAt,
		SentAt: r.SentAt, WithdrawnAt: r.WithdrawnAt, WithdrawnBy: r.WithdrawnByUserID, WithdrawalReason: r.WithdrawalReason,
	}
	if showRecipient {
		w.Recipient = ptr(r.Recipient)
	}
	if r.Regime != nil {
		w.Regime = ptr(gen.InvoicesReminderRegime(*r.Regime))
	}
	if r.FeeKind != nil {
		w.FeeKind = ptr(gen.InvoicesReminderFeeKind(*r.FeeKind))
	}
	if r.ChargeNotes != nil {
		w.ChargeNotes = ptr(slices.Clone(r.ChargeNotes))
	}
	for _, f := range []struct {
		dst **float64
		n   pgtype.Numeric
	}{
		{&w.PrincipalOpen, r.PrincipalOpen}, {&w.Fee, r.Fee}, {&w.Compensation, r.Compensation},
		{&w.ChargesEarlier, r.ChargesEarlier}, {&w.Interest, r.Interest}, {&w.InterestWaived, r.InterestWaived},
		{&w.InterestPaid, r.InterestPaid}, {&w.Inkassosats, r.Inkassosats}, {&w.Total, r.Total},
	} {
		v, err := ratOrNil(f.n)
		if err != nil {
			return gen.InvoicesReminder{}, fmt.Errorf("invoices: read letter %d's facts: %w", r.ID, err)
		}
		if v != nil {
			*f.dst = ptr(floatFromRat(v, 2))
		}
	}
	if len(r.InterestSegments) > 0 {
		segs, err := storedSegmentsWire(r.InterestSegments)
		if err != nil {
			return gen.InvoicesReminder{}, err
		}
		w.InterestSegments = &segs
	}
	return w, nil
}

// optionalDay is a nullable date column's value: NULL for nil.
func optionalDay(d *time.Time) pgtype.Date {
	if d == nil {
		return pgtype.Date{}
	}
	return pgDate(*d)
}

// reminderRunWire is a run row on the wire.
func reminderRunWire(r store.InvoicesReminderRun) gen.InvoicesReminderRun {
	return gen.InvoicesReminderRun{
		Id: r.ID, RunOn: wireDate(r.RunOn.Time), CreatedAt: r.CreatedAt, CreatedBy: r.CreatedByUserID,
		Letters: r.Letters, Skipped: r.Skipped, LastBookedOn: wireDateOf(r.LastBookedOn),
		StaleImportAcknowledged: r.StaleImportAcknowledged,
	}
}

// recipients reads where each distinct customer's letters go, one directory
// call per customer, before any lock.
type recipient struct {
	channel, address string
	warnings         []string
}

func (s *server) recipients(ctx context.Context, customers []int32) (map[int32]recipient, error) {
	out := make(map[int32]recipient, len(customers))
	for _, id := range customers {
		if _, done := out[id]; done {
			continue
		}
		channel, address, warnings, err := s.recipientOf(ctx, id)
		if err != nil {
			return nil, err
		}
		out[id] = recipient{channel: channel, address: address, warnings: warnings}
	}
	return out, nil
}

// PostInvoicesReminderRuns Preview or make a reminder run
// (POST /api/v1/invoices/reminder-runs)
func (s *server) PostInvoicesReminderRuns(ctx context.Context, req gen.PostInvoicesReminderRunsRequestObject) (gen.PostInvoicesReminderRunsResponseObject, error) {
	now := s.deps.Clock() // the run's one clock read (D18)
	today := businessDay(now)
	if req.Body.DryRun {
		return s.previewRun(ctx, today)
	}
	return s.makeRun(ctx, *req.Body, now, today)
}

// previewRun is the preview (D10): the overdue set judged as the list judges
// it, the letters due today with their channel and recipient, the rest
// blocked or waiting. Nothing is written and no lock is taken.
func (s *server) previewRun(ctx context.Context, today time.Time) (gen.PostInvoicesReminderRunsResponseObject, error) {
	j, refusal, err := s.judgeOverdue(ctx, store.New(s.deps.Pool), today, overdueFilter{})
	switch {
	case err != nil:
		return nil, err
	case refusal != nil:
		return gen.PostInvoicesReminderRuns409ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	var customers []int32
	for _, i := range j.items {
		if i.out.Letter != nil {
			customers = append(customers, i.row.CustomerID)
		}
	}
	to, err := s.recipients(ctx, customers)
	if err != nil {
		return nil, err
	}
	preview := gen.InvoicesReminderRunPreview{
		Letters: []gen.InvoicesRunPreviewLetter{}, BlockedOrWaiting: []gen.InvoicesRunPreviewHeld{},
		Freshness: freshnessWire(j.fresh, j.staleDays), Warnings: []gen.InvoicesReminderRunPreviewWarnings{},
	}
	for _, w := range j.warnings {
		preview.Warnings = append(preview.Warnings, gen.InvoicesReminderRunPreviewWarnings(w))
	}
	for _, i := range j.items {
		number, name := int64(0), ""
		if i.row.Number != nil {
			number = *i.row.Number
		}
		if i.row.BuyerName != nil {
			name = *i.row.BuyerName
		}
		switch {
		case i.out.Letter != nil:
			r := to[i.row.CustomerID]
			l := gen.InvoicesRunPreviewLetter{
				InvoiceId: i.row.ID, Number: number, CustomerId: i.row.CustomerID, BuyerName: name,
				Action: gen.InvoicesRunPreviewLetterAction(i.out.Action), Letter: letterFactsWire(*i.out.Letter),
				ChargeNotes: make([]gen.InvoicesRunPreviewLetterChargeNotes, 0, len(i.out.ChargeNotes)),
				Channel:     gen.InvoicesRunPreviewLetterChannel(r.channel), Recipient: r.address,
				Warnings: make([]gen.InvoicesRunPreviewLetterWarnings, 0, len(r.warnings)),
			}
			for _, n := range i.out.ChargeNotes {
				l.ChargeNotes = append(l.ChargeNotes, gen.InvoicesRunPreviewLetterChargeNotes(n))
			}
			for _, w := range r.warnings {
				l.Warnings = append(l.Warnings, gen.InvoicesRunPreviewLetterWarnings(w))
			}
			preview.Letters = append(preview.Letters, l)
		case i.out.Action == reminderrules.ActionBlocked || i.out.Action == reminderrules.ActionWaiting:
			preview.BlockedOrWaiting = append(preview.BlockedOrWaiting, gen.InvoicesRunPreviewHeld{
				InvoiceId: i.row.ID, Number: number, CustomerId: i.row.CustomerID, BuyerName: name,
				NextAction: nextActionWire(i.out),
			})
		}
	}
	return gen.PostInvoicesReminderRuns200JSONResponse(preview), nil
}

// parseRunItems runs D10's field rules over a run's items: 1 to 500, each
// invoice once, each action a letter's.
func parseRunItems(items *[]gen.InvoicesReminderRunItem) map[string][]string {
	var errs map[string][]string
	if items == nil || len(*items) == 0 {
		return withFieldError(errs, "items", "A run needs at least one invoice")
	}
	if len(*items) > maxRunItems {
		return withFieldError(errs, "items", fmt.Sprintf("A run takes at most %d invoices", maxRunItems))
	}
	seen := make(map[int64]bool, len(*items))
	for _, it := range *items {
		switch {
		case seen[it.InvoiceId]:
			errs = withFieldError(errs, "items", fmt.Sprintf("Invoice %d is in the run twice", it.InvoiceId))
		case !it.Action.Valid():
			errs = withFieldError(errs, "items", fmt.Sprintf("Invoice %d's action is reminder or collection_notice", it.InvoiceId))
		}
		seen[it.InvoiceId] = true
	}
	return errs
}

// makeRun is the run (D10): its refusals in order — the fields (each item an
// issued invoice among them), then reminders_disabled, then on a pool pre-pass over its items (no lock)
// collection_rates_outdated, collection_regime_unreviewed and
// bank_import_stale (plan reading 27) — the profiles read, the run row, one
// transaction per item, the counts set once.
func (s *server) makeRun(ctx context.Context, body gen.InvoicesReminderRunRequest, now, today time.Time) (gen.PostInvoicesReminderRunsResponseObject, error) {
	if errs := parseRunItems(body.Items); errs != nil {
		return gen.PostInvoicesReminderRuns400ApplicationProblemPlusJSONResponse(invalid(invalidRunTitle, errs)), nil
	}
	items := *body.Items
	q := store.New(s.deps.Pool)
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.InvoiceId)
	}
	// Each item an issued invoice, read with its customer, whose profile is
	// read below.
	owners, err := q.InvoiceCustomers(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the run's invoices: %w", err)
	}
	customerOf := make(map[int64]int32, len(owners))
	customers := make([]int32, 0, len(owners))
	for _, o := range owners {
		customerOf[o.ID] = o.CustomerID
		customers = append(customers, o.CustomerID)
	}
	var missing []string
	for _, id := range ids {
		if _, ok := customerOf[id]; !ok {
			missing = append(missing, fmt.Sprint(id))
		}
	}
	if missing != nil {
		return gen.PostInvoicesReminderRuns400ApplicationProblemPlusJSONResponse(invalid(invalidRunTitle, withFieldError(nil, "items",
			"Not an issued invoice: "+strings.Join(missing, ", ")))), nil
	}
	settings, _, err := s.reminderSettings(ctx, q)
	if err != nil {
		return nil, err
	}
	if !settings.Enabled {
		return gen.PostInvoicesReminderRuns409ApplicationProblemPlusJSONResponse(conflict(codeRemindersDisabled, cannotRunTitle,
			"Reminders are switched off in the reminder settings.")), nil
	}

	// The pre-pass: every item judged on the pool, before anything is
	// written (plan reading 27).
	ins, err := s.ruleInputs(ctx, s.deps.Pool, ids, today)
	if err != nil {
		return nil, err
	}
	outcomes := make(map[int64]reminderrules.Outcome, len(items))
	for _, it := range items {
		outcomes[it.InvoiceId] = reminderrules.Next(ins[it.InvoiceId])
	}
	for _, it := range items {
		if o := outcomes[it.InvoiceId]; o.Outdated != nil {
			p := conflict(codeCollectionRatesOutdated, cannotRunTitle, fmt.Sprintf(
				"A letter of this run needs the %s rate for %s, and no rate row covers it. Add the rate, or wait for the release that brings it.",
				o.Outdated.Kind, o.Outdated.HalfYear))
			p.Kind, p.HalfYear = &o.Outdated.Kind, &o.Outdated.HalfYear
			return gen.PostInvoicesReminderRuns409ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	for _, it := range items {
		if slices.Contains(outcomes[it.InvoiceId].Reasons, reminderrules.ReasonRegimeUnreviewed) {
			return gen.PostInvoicesReminderRuns409ApplicationProblemPlusJSONResponse(conflict(codeCollectionRegimeUnreviewed, cannotRunTitle, fmt.Sprintf(
				"A letter of this run would carry a fee or be a collection notice after %s, the day the collection-law regime was last reviewed. A manager reviews it in the reminder settings.",
				settings.RegimeReviewedThrough.Format(time.DateOnly)))), nil
		}
	}
	fresh, err := s.bankFreshness(ctx, q, today, settings.StaleImportDays)
	if err != nil {
		return nil, err
	}
	acknowledged := body.AcknowledgeStaleImport != nil && *body.AcknowledgeStaleImport
	if fresh.Stale && !acknowledged && slices.ContainsFunc(items, func(it gen.InvoicesReminderRunItem) bool {
		return carriesCharge(outcomes[it.InvoiceId].Letter)
	}) {
		p := conflict(codeBankImportStale, cannotRunTitle,
			"No bank file was ever imported, and letters of this run claim charges. Import the latest bank file, or confirm the run.")
		if fresh.LastBookedOn != nil {
			p.Detail = ptr(fmt.Sprintf("The latest imported booking is from %s, and letters of this run claim charges. Import the latest bank file, or confirm the run.",
				fresh.LastBookedOn.Format(time.DateOnly)))
			p.LastBookedOn = ptr(wireDate(*fresh.LastBookedOn))
		}
		return gen.PostInvoicesReminderRuns409ApplicationProblemPlusJSONResponse(p), nil
	}

	// The profiles, one directory call per customer, before any lock.
	to, err := s.recipients(ctx, customers)
	if err != nil {
		return nil, err
	}

	by := callerID(ctx)
	run, err := q.InsertReminderRun(ctx, store.InsertReminderRunParams{
		RunOn: pgDate(today), CreatedAt: now, CreatedByUserID: by,
		LastBookedOn: optionalDay(fresh.LastBookedOn), StaleImportAcknowledged: acknowledged,
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: write the reminder run: %w", err)
	}
	result := gen.InvoicesReminderRunResult{Created: []gen.InvoicesReminder{}, Skipped: []gen.InvoicesReminderRunSkip{}}
	for _, it := range items {
		letter, skip, err := s.runItem(ctx, run.ID, it, to[customerOf[it.InvoiceId]], by, now, today)
		if err != nil {
			return nil, err
		}
		if skip != "" {
			result.Skipped = append(result.Skipped, gen.InvoicesReminderRunSkip{InvoiceId: it.InvoiceId, Reason: gen.InvoicesReminderRunSkipReason(skip)})
			continue
		}
		w, err := reminderWire(letter, true)
		if err != nil {
			return nil, err
		}
		result.Created = append(result.Created, w)
	}
	counted, err := q.SetRunCounts(ctx, store.SetRunCountsParams{
		ID: run.ID, Letters: ptr(int32(len(result.Created))), Skipped: ptr(int32(len(result.Skipped))), //nolint:gosec // at most 500
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: count reminder run %d: %w", run.ID, err)
	}
	result.Run = reminderRunWire(counted)
	return gen.PostInvoicesReminderRuns201JSONResponse(result), nil
}

// runItem is one item of a run in its own transaction (D10, D18): the
// invoice FOR UPDATE, then every figure after it — the anonymisation marker
// (skipped customer_anonymised), the engine's input and outcome on today,
// an action other than the item's skipped action_changed — then the letter
// inserted, queued for e-mail (due at once) or awaiting print for paper,
// without facts. It answers the letter, or the skip's reason.
func (s *server) runItem(ctx context.Context, runID int64, it gen.InvoicesReminderRunItem, to recipient, by uuid.UUID, now, today time.Time) (store.InvoicesReminder, string, error) {
	var letter store.InvoicesReminder
	skip := ""
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		inv, err := lockInvoice(ctx, txq, it.InvoiceId)
		if err != nil {
			return fmt.Errorf("invoices: lock document %d: %w", it.InvoiceId, err)
		}
		if hook := runItemAfterLock; hook != nil {
			if err := hook(ctx, inv.ID); err != nil {
				return err
			}
		}
		erased, err := txq.CustomerErased(ctx, inv.CustomerID)
		if err != nil {
			return fmt.Errorf("invoices: read whether customer %d is anonymised: %w", inv.CustomerID, err)
		}
		if erased {
			skip = skipCustomerAnonymed
			return nil
		}
		in, err := s.ruleInputLocked(ctx, txq, inv, today, 0)
		if err != nil {
			return err
		}
		out := reminderrules.Next(in)
		if string(out.Action) != string(it.Action) || out.Letter == nil {
			skip = skipActionChanged
			return nil
		}
		sequence, err := txq.NextSequence(ctx, inv.ID)
		if err != nil {
			return fmt.Errorf("invoices: number document %d's next letter: %w", inv.ID, err)
		}
		params := store.InsertReminderParams{
			InvoiceID: inv.ID, RunID: runID, Sequence: sequence, Level: string(out.Letter.Level),
			AnnouncesCollection: out.Letter.AnnouncesCollection, Channel: to.channel, Recipient: to.address,
			Language: letterLanguage(inv.BuyerLanguage), CreatedAt: now, CreatedByUserID: by,
			Status: reminderrules.StatusAwaitingPrint,
		}
		if to.channel == channelEmail {
			params.Status, params.NextAttemptAt = reminderrules.StatusQueued, &now
		}
		if letter, err = txq.InsertReminder(ctx, params); err != nil {
			return fmt.Errorf("invoices: write document %d's letter: %w", inv.ID, err)
		}
		return nil
	})
	return letter, skip, err
}

// GetInvoicesReminderRuns List the reminder runs
// (GET /api/v1/invoices/reminder-runs)
func (s *server) GetInvoicesReminderRuns(ctx context.Context, req gen.GetInvoicesReminderRunsRequestObject) (gen.GetInvoicesReminderRunsResponseObject, error) {
	p := req.Params
	if errs := validatePageParams(p.Page, p.PageSize); len(errs) > 0 {
		return gen.GetInvoicesReminderRuns400ApplicationProblemPlusJSONResponse(apicommon.Problem(invalidQueryTitle, strings.Join(errs, " "))), nil
	}
	page, pageSize := pageParams(p.Page, p.PageSize)
	q := store.New(s.deps.Pool)
	total, err := q.CountReminderRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: count the reminder runs: %w", err)
	}
	rows, err := q.ListReminderRuns(ctx, store.ListReminderRunsParams{PageOffset: (page - 1) * pageSize, PageSize: pageSize})
	if err != nil {
		return nil, fmt.Errorf("invoices: list the reminder runs: %w", err)
	}
	data := make([]gen.InvoicesReminderRun, 0, len(rows))
	for _, r := range rows {
		data = append(data, reminderRunWire(r))
	}
	return gen.GetInvoicesReminderRuns200JSONResponse(gen.PaginatedResponseOfInvoicesReminderRun{
		Data: data, Pagination: apicommon.Pagination(page, pageSize, total),
	}), nil
}

// GetInvoicesReminderRunsById Get a reminder run
// (GET /api/v1/invoices/reminder-runs/{id})
func (s *server) GetInvoicesReminderRunsById(ctx context.Context, req gen.GetInvoicesReminderRunsByIdRequestObject) (gen.GetInvoicesReminderRunsByIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	run, err := q.GetReminderRun(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetInvoicesReminderRunsById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invoices: read reminder run %d: %w", req.Id, err)
	}
	rows, err := q.LettersOfRun(ctx, run.ID)
	if err != nil {
		return nil, fmt.Errorf("invoices: read reminder run %d's letters: %w", run.ID, err)
	}
	letters := make([]gen.InvoicesReminder, 0, len(rows))
	for _, r := range rows {
		l, err := reminderWire(r, true)
		if err != nil {
			return nil, err
		}
		letters = append(letters, l)
	}
	return gen.GetInvoicesReminderRunsById200JSONResponse(gen.InvoicesReminderRunDetail{Run: reminderRunWire(run), Letters: letters}), nil
}
