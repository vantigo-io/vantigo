package invoices

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the dashboard's attention items (invoices payments and
// reminders design D12; plan reading 29), in the item shape every module's
// /stats/attention shares: the 20 most overdue invoices and every refund due
// for every caller, and — for a caller holding invoices:payments, never a
// 403 for one without — the bank files with open lines, the failed letters,
// the letters held by a cause, the print batches not confirmed posted, and
// the Vipps payments needing a person (attentionPayments). Every item is derived from the rows as they are, so none needs dismissing:
// each clears when what it is about is done. One clock read; every read on
// the pool with no lock, the refunds' charges through the rule-input loader
// on a snapshot of its own, as the overdue list reads them.

// attentionOverdueCap is how many overdue invoices attention names (D12).
const attentionOverdueCap = 20

// heldCauses is each held_reason as attention names the cause, the wire's
// camelCase (D12).
var heldCauses = map[string]string{
	reminderrules.ReasonRatesOutdated:    "collectionRatesOutdated",
	reminderrules.ReasonRegimeUnreviewed: "collectionRegimeUnreviewed",
}

// GetInvoicesStatsAttention Get the invoices dashboard attention items
// (GET /api/v1/invoices/stats/attention)
func (s *server) GetInvoicesStatsAttention(ctx context.Context, _ gen.GetInvoicesStatsAttentionRequestObject) (gen.GetInvoicesStatsAttentionResponseObject, error) {
	now := s.deps.Clock()
	today := businessDay(now)
	q := store.New(s.deps.Pool)
	items := []gen.InvoicesStatsAttentionItem{}
	overdue, err := s.attentionOverdue(ctx, q, today)
	if err != nil {
		return nil, err
	}
	items = append(items, overdue...)
	refunds, err := s.attentionRefundDue(ctx, q, today)
	if err != nil {
		return nil, err
	}
	items = append(items, refunds...)
	if !s.has(ctx, "invoices:payments") {
		return gen.GetInvoicesStatsAttention200JSONResponse(items), nil
	}
	queue, err := attentionQueue(ctx, q, today)
	if err != nil {
		return nil, err
	}
	vipps, err := s.attentionPayments(ctx, q, now)
	if err != nil {
		return nil, err
	}
	items = append(items, queue...)
	return gen.GetInvoicesStatsAttention200JSONResponse(append(items, vipps...)), nil
}

// attentionItem is one item; count is left out when it is 0.
func attentionItem(typ gen.InvoicesStatsAttentionItemType, entity, title string, at time.Time, count int32) gen.InvoicesStatsAttentionItem {
	item := gen.InvoicesStatsAttentionItem{
		Id: string(typ) + "/" + entity, Type: typ, EntityId: entity, Title: title, OccurredAt: at.UTC(),
	}
	if count > 0 {
		item.Count = ptr(count)
	}
	return item
}

// idString is a row id as an item's entity id.
func idString(id int64) string { return strconv.FormatInt(id, 10) }

// attentionOverdue is invoiceOverdue: the 20 most overdue invoices on today,
// each from the day after its effective due date E. An invoice overdue by
// its state whose E has not passed — due on a Saturday, today the Sunday or
// the Monday — is not yet an item; E moves only with the due date, so those
// are the newest due of the 20 read and the cap stands.
func (s *server) attentionOverdue(ctx context.Context, q *store.Queries, today time.Time) ([]gen.InvoicesStatsAttentionItem, error) {
	rows, err := q.AttentionOverdue(ctx, pgDate(today))
	if err != nil {
		return nil, fmt.Errorf("invoices: read the most overdue invoices: %w", err)
	}
	items := make([]gen.InvoicesStatsAttentionItem, 0, min(len(rows), attentionOverdueCap))
	for _, r := range rows {
		from := reminderrules.EffectiveDue(utcDay(r.DueDate.Time)).AddDate(0, 0, 1)
		if from.After(today) {
			continue
		}
		items = append(items, attentionItem(gen.InvoiceOverdue, idString(r.ID), orEmpty(r.BuyerName), from, 0))
	}
	return items, nil
}

// attentionRefundDue is invoiceRefundDue: every issued invoice whose open
// amount is below zero, or whose charges' refund due — D9's formula, the
// charges block's — is above zero, uncapped (D12, M16; plan reading 29).
func (s *server) attentionRefundDue(ctx context.Context, q *store.Queries, today time.Time) ([]gen.InvoicesStatsAttentionItem, error) {
	rows, err := q.AttentionRefundDue(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the invoices with a refund due: %w", err)
	}
	var withCharges []int64
	for _, r := range rows {
		if r.HasChargePayments {
			withCharges = append(withCharges, r.ID)
		}
	}
	ins := map[int64]reminderrules.Input{}
	if len(withCharges) > 0 {
		if ins, err = s.ruleInputs(ctx, s.deps.Pool, withCharges, today); err != nil {
			return nil, err
		}
	}
	items := []gen.InvoicesStatsAttentionItem{}
	for _, r := range rows {
		open, err := ratFromNumeric(r.OpenAmount)
		if err != nil {
			return nil, err
		}
		due := open.Sign() < 0
		if in, ok := ins[r.ID]; ok && !due {
			due = reminderrules.Charges(in.Letters, in.Waivers, in.ChargePayments).RefundDue.Sign() > 0
		}
		if due {
			items = append(items, attentionItem(gen.InvoiceRefundDue, idString(r.ID), orEmpty(r.BuyerName), r.ChangedAt, 0))
		}
	}
	return items, nil
}

// attentionQueue is the items for invoices:payments alone: the bank files
// with open lines, the failed letters, the letters held by a cause and the
// unposted print batches.
func attentionQueue(ctx context.Context, q *store.Queries, today time.Time) ([]gen.InvoicesStatsAttentionItem, error) {
	items := []gen.InvoicesStatsAttentionItem{}
	files, err := q.AttentionBankFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the bank files with open lines: %w", err)
	}
	for _, f := range files {
		items = append(items, attentionItem(gen.BankTransactionsOpen, idString(f.ID), bookingDays(f.FirstBookedOn.Time,
			f.LastBookedOn.Time, f.FirstBookedOn.Valid, f.LastBookedOn.Valid), f.UploadedAt, f.OpenLines))
	}
	failed, err := q.AttentionFailedLetters(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the failed letters: %w", err)
	}
	for _, l := range failed {
		item := attentionItem(gen.ReminderFailed, idString(l.InvoiceID), orEmpty(l.BuyerName), derefTime(l.FailedAt), 0)
		item.Id = string(gen.ReminderFailed) + "/" + idString(l.ID)
		items = append(items, item)
	}
	held, err := q.AttentionHeldLetters(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the held letters: %w", err)
	}
	for _, h := range held {
		cause, ok := heldCauses[h.HeldReason]
		if !ok {
			return nil, fmt.Errorf("invoices: letters held for an unknown reason %q", h.HeldReason)
		}
		items = append(items, attentionItem(gen.RemindersHeld, cause, cause, h.Since, h.Letters))
	}
	batches, err := q.AttentionUnpostedBatches(ctx, pgDate(today))
	if err != nil {
		return nil, fmt.Errorf("invoices: read the unposted print batches: %w", err)
	}
	for _, b := range batches {
		items = append(items, attentionItem(gen.ReminderBatchUnposted, idString(b.ID), b.PostOn.Time.Format(time.DateOnly),
			b.PostOn.Time.AddDate(0, 0, 2), b.Letters))
	}
	return items, nil
}

// captureFailedWindow is how long a failed capture is an attention item
// (plan reading 18).
const captureFailedWindow = 14 * 24 * time.Hour

// attentionPayments is the Vipps payments' four types (D12, D16), for
// invoices:payments alone, each item about one attempt — id
// '<type>/<attemptId>', entity its invoice, title the buyer's name:
// paymentCaptureUnknown (a capture of unknown outcome, and captured money
// whose registration failed), paymentCaptureFailed (a failed capture of the
// last 14 days on an invoice still open), paymentReservationStranded (a
// reservation the worker cannot carry — the switch off, another sales unit —
// and an attempt parked for a person) and vippsRefundSeen (a refund seen by
// the watch while its payment is live).
func (s *server) attentionPayments(ctx context.Context, q *store.Queries, now time.Time) ([]gen.InvoicesStatsAttentionItem, error) {
	items := []gen.InvoicesStatsAttentionItem{}
	add := func(typ gen.InvoicesStatsAttentionItemType, attempt, invoice int64, buyer *string, at time.Time) {
		item := attentionItem(typ, idString(invoice), orEmpty(buyer), at, 0)
		item.Id = string(typ) + "/" + idString(attempt)
		items = append(items, item)
	}
	unknown, err := q.AttentionCaptureUnknown(ctx, registrationFailed)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the Vipps captures of unknown outcome: %w", err)
	}
	for _, r := range unknown {
		add(gen.PaymentCaptureUnknown, r.ID, r.InvoiceID, r.BuyerName, r.OccurredAt)
	}
	failed, err := q.AttentionCaptureFailed(ctx, store.AttentionCaptureFailedParams{
		Since: now.Add(-captureFailedWindow), RegistrationFailed: registrationFailed,
	})
	if err != nil {
		return nil, fmt.Errorf("invoices: read the failed Vipps captures: %w", err)
	}
	for _, r := range failed {
		add(gen.PaymentCaptureFailed, r.ID, r.InvoiceID, r.BuyerName, r.OccurredAt)
	}
	candidates, err := q.AttentionStrandedCandidates(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the Vipps reservations: %w", err)
	}
	if len(candidates) > 0 {
		enabled := s.deps.Config != nil && s.deps.Config.InvoicesVippsEnabled
		stored := ""
		row, err := q.GetPaymentCredentials(ctx)
		switch {
		case err == nil:
			settings, err := vippsSettingsOf(row)
			if err != nil {
				return nil, err
			}
			stored = settings.MSN
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("invoices: read the Vipps credentials: %w", err)
		}
		for _, r := range candidates {
			if r.Parked || !enabled || r.Msn != stored {
				add(gen.PaymentReservationStranded, r.ID, r.InvoiceID, r.BuyerName, r.OccurredAt)
			}
		}
	}
	refunds, err := q.AttentionRefundSeen(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the Vipps refunds: %w", err)
	}
	for _, r := range refunds {
		add(gen.VippsRefundSeen, r.ID, r.InvoiceID, r.BuyerName, r.OccurredAt)
	}
	return items, nil
}

// bookingDays is a bank file's booking days as an item names it: one day,
// or the first and the last; "" for a file with no booked line.
func bookingDays(first, last time.Time, hasFirst, hasLast bool) string {
	switch {
	case !hasFirst || !hasLast:
		return ""
	case first.Equal(last):
		return first.Format(time.DateOnly)
	default:
		return first.Format(time.DateOnly) + " – " + last.Format(time.DateOnly)
	}
}

// derefTime is t, or the zero time for none.
func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
