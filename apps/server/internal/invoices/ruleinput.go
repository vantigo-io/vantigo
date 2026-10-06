package invoices

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the rule-input loader (invoices payments and reminders design
// D8): every fact the reminder engine judges an invoice on, read from the
// store and mapped onto reminderrules.Input — the engine's one door (plan
// reading 2). No other code builds an Input: the overdue list, the run's
// preview and the run read a whole list through ruleInputs, on the pool, in a
// handful of statements whatever its length; the bank match's deadline-met
// waiver, the hand-off's export, the letter's dispatch and the posted
// re-judge read one invoice through ruleInputLocked, under its lock.
//
// The contract it keeps (reminderrules.Input; the tests run Validate on what
// it reads): every day a UTC midnight of the Oslo calendar day — a date
// column through utcDay, an instant through businessDay; every amount but a
// letter's non-nil; no policy row is ModeNormal; Rates every row of every
// kind; Payments, Credits and ChargePayments the live ones only.

// ruleInputs reads the engine's Input on day L for every issued invoice
// among ids, with q — the pool for a list, or a transaction. An id that is
// not an issued invoice (a draft, a credit note, none) is absent from the
// answer. Each statement reads every invoice at once (ruleinput.sql), so the
// count is the same for one invoice as for thousands. Exclude is zero.
func (s *server) ruleInputs(ctx context.Context, q *store.Queries, ids []int64, L time.Time) (map[int64]reminderrules.Input, error) {
	out := map[int64]reminderrules.Input{}
	if len(ids) == 0 {
		return out, nil
	}
	invs, err := q.RuleInvoices(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the invoices the rules judge: %w", err)
	}
	if len(invs) == 0 {
		return out, nil
	}
	settings, _, err := s.reminderSettings(ctx, q)
	if err != nil {
		return nil, err
	}
	rateRows, err := q.RatesFor(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the collection rates: %w", err)
	}
	rates, err := ratesOf(rateRows)
	if err != nil {
		return nil, err
	}
	ins := make(map[int64]*reminderrules.Input, len(invs))
	for _, r := range invs {
		gross, err := ratFromNumeric(r.GrossTotal)
		if err != nil {
			return nil, fmt.Errorf("invoices: read document %d's gross: %w", r.ID, err)
		}
		ins[r.ID] = &reminderrules.Input{
			L: L,
			Invoice: reminderrules.Invoice{
				IssueDate: utcDay(r.IssueDate.Time), DueDate: utcDay(r.DueDate.Time), Gross: gross,
				BuyerType: deref(r.BuyerType), BuyerOrganisationNumber: deref(r.BuyerOrganisationNumber),
				BuyerForeignID: deref(r.BuyerForeignID),
			},
			Settings: settings,
			Mode:     reminderrules.ModeNormal,
			Rates:    rates,
		}
	}
	found := make([]int64, 0, len(ins))
	for id := range ins {
		found = append(found, id)
	}
	slices.Sort(found)
	if err := readRuleFacts(ctx, q, found, ins); err != nil {
		return nil, err
	}
	for id, in := range ins {
		slices.SortStableFunc(in.Deliveries, time.Time.Compare)
		out[id] = *in
	}
	return out, nil
}

// readRuleFacts reads every invoice's credits, payments, deliveries, letters,
// waivers, charge payments, holds, hand-offs and policy into ins, one
// statement each.
func readRuleFacts(ctx context.Context, q *store.Queries, ids []int64, ins map[int64]*reminderrules.Input) error {
	credits, err := q.RuleCredits(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the credit notes: %w", err)
	}
	for _, r := range credits {
		gross, err := ratFromNumeric(r.GrossTotal)
		if err != nil {
			return fmt.Errorf("invoices: read a credit note of document %d: %w", r.InvoiceID, err)
		}
		in := ins[r.InvoiceID]
		in.Credits = append(in.Credits, reminderrules.Credit{IssueDate: utcDay(r.IssueDate.Time), Gross: gross})
	}

	payments, err := q.RulePayments(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the payments: %w", err)
	}
	for _, r := range payments {
		amount, err := ratFromNumeric(r.Amount)
		if err != nil {
			return fmt.Errorf("invoices: read a payment of document %d: %w", r.InvoiceID, err)
		}
		in := ins[r.InvoiceID]
		in.Payments = append(in.Payments, reminderrules.Payment{PaidOn: utcDay(r.PaidOn.Time), OrderedOn: dayOrNil(r.OrderedOn), Amount: amount})
	}

	deliveries, err := q.RuleDeliveries(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the deliveries: %w", err)
	}
	for _, r := range deliveries {
		day, err := deliveryDay(r.InvoiceID, r.Kind, r.At, r.DeliveredOn)
		if err != nil {
			return err
		}
		in := ins[r.InvoiceID]
		in.Deliveries = append(in.Deliveries, day)
	}

	letters, err := q.RuleLetters(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the letters: %w", err)
	}
	for _, r := range letters {
		l, err := letterOf(r)
		if err != nil {
			return err
		}
		in := ins[r.InvoiceID]
		in.Letters = append(in.Letters, l)
	}

	waivers, err := q.RuleWaivers(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the waivers: %w", err)
	}
	for _, r := range waivers {
		w, err := waiverOf(r)
		if err != nil {
			return err
		}
		in := ins[r.InvoiceID]
		in.Waivers = append(in.Waivers, w)
	}

	chargePayments, err := q.RuleChargePayments(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the charge payments: %w", err)
	}
	for _, r := range chargePayments {
		p, err := chargePaymentOf(r)
		if err != nil {
			return err
		}
		in := ins[r.InvoiceID]
		in.ChargePayments = append(in.ChargePayments, p)
	}

	holds, err := q.RuleHolds(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the holds: %w", err)
	}
	for _, r := range holds {
		in := ins[r.InvoiceID]
		in.OnHold, in.ChargesBarred = r.OnHold, r.ChargesBarred
	}

	handoffs, err := q.RuleHandoffs(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the hand-offs: %w", err)
	}
	for _, id := range handoffs {
		ins[id].HandedOff = true
	}

	policies, err := q.RulePolicies(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the reminder policies: %w", err)
	}
	for _, r := range policies {
		ins[r.InvoiceID].Mode = reminderrules.Mode(r.Mode)
	}
	return nil
}

// ruleInputLocked reads the engine's Input for inv on day L with txq, whose
// transaction holds inv FOR UPDATE (lockInvoice) — the caller takes the lock
// first, so every figure read here is read after it, and a payment, a
// letter, a waiver, a hold or a hand-off another transaction held the
// invoice for has committed. exclude is the letter being dispatched or
// posted (zero for none): it stays among the letters, and Input.Exclude
// names it so the engine leaves it out of flight. An inv that is not an
// issued invoice is an error: no caller judges one.
func (s *server) ruleInputLocked(ctx context.Context, txq *store.Queries, inv store.InvoicesInvoice, L time.Time, exclude int64) (reminderrules.Input, error) {
	ins, err := s.ruleInputs(ctx, txq, []int64{inv.ID}, L)
	if err != nil {
		return reminderrules.Input{}, err
	}
	in, ok := ins[inv.ID]
	if !ok {
		return reminderrules.Input{}, fmt.Errorf("invoices: document %d is not an issued invoice the rules judge", inv.ID)
	}
	in.Exclude = exclude
	return in, nil
}
