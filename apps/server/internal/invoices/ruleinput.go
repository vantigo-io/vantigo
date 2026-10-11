package invoices

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is the rule-input loader (invoices payments and reminders design
// D8): every fact the reminder engine judges an invoice on, read from the
// store and mapped onto reminderrules.Input — the engine's one door (plan
// reading 2). No other code builds an Input: the overdue list, the run's
// preview and the run read a whole list through ruleInputs, in one snapshot,
// in a handful of statements whatever its length; the bank match's
// deadline-met waiver, the hand-off's export, the letter's dispatch and the
// posted re-judge read one invoice through ruleInputLocked, under its lock.
//
// The contract it keeps (reminderrules.Input; the tests run Validate on what
// it reads): every day a UTC midnight of the Oslo calendar day — a date
// column through utcDay, an instant through businessDay; every amount but a
// letter's non-nil; no policy row is ModeNormal; Rates every row of every
// kind; Payments, Credits and ChargePayments the live ones only.
//
// The buyer's language is not an input: the engine judges no letter by it,
// and the letter's dispatch reads it from the invoice row it holds locked.

// ratesRead is told every time the engine's collection rates are read, so a
// test can pin a read against the locks around it — a print batch's letter
// reads them only after it has shared the rows in force (D6, plan reading
// 6). nil in production.
var ratesRead func(ctx context.Context)

// ruleInputs reads the engine's Input on day L for every issued invoice
// among ids, in one read-only REPEATABLE READ transaction opened on on (the
// pool), so a whole list is judged on one snapshot — a payment committed
// between two of its statements is seen by all of them or by none. An id
// that is not an issued invoice (a draft, a credit note, none) is absent
// from the answer. Each statement reads every invoice at once
// (ruleinput.sql), so the count is the same for one invoice as for
// thousands. Exclude is zero.
func (s *server) ruleInputs(ctx context.Context, on db.TxBeginner, ids []int64, L time.Time) (map[int64]reminderrules.Input, error) {
	var out map[int64]reminderrules.Input
	err := readTx(ctx, on, func(ctx context.Context, q *store.Queries) error {
		var err error
		out, err = s.readRuleInputs(ctx, q, ids, L)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ruleInputLocked reads the engine's Input for inv on day L with txq, whose
// transaction holds inv FOR UPDATE (lockInvoice) — the caller takes the lock
// first, so every figure read here is read after it, and a payment, a
// letter, a waiver, a hold or a hand-off another transaction held the
// invoice for has committed; what the caller's own transaction wrote before
// is read too. exclude is the letter being dispatched or posted (zero for
// none): it stays among the letters, and Input.Exclude names it so the
// engine leaves it out of flight. An inv that is not an issued invoice is an
// error: no caller judges one.
func (s *server) ruleInputLocked(ctx context.Context, txq *store.Queries, inv store.InvoicesInvoice, L time.Time, exclude int64) (reminderrules.Input, error) {
	ins, err := s.readRuleInputs(ctx, txq, []int64{inv.ID}, L)
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

// readRuleInputs is both doors' read with q: the issued invoices among ids,
// the settings and the rates, then every fact of every invoice, one
// statement each.
func (s *server) readRuleInputs(ctx context.Context, q *store.Queries, ids []int64, L time.Time) (map[int64]reminderrules.Input, error) {
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
	if hook := ratesRead; hook != nil {
		hook(ctx)
	}
	rates, err := ratesOf(rateRows)
	if err != nil {
		return nil, err
	}
	ins := make(ruleInputSet, len(invs))
	found := make([]int64, 0, len(invs))
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
		found = append(found, r.ID)
	}
	if err := readRuleFacts(ctx, q, found, ins); err != nil {
		return nil, err
	}
	for id, in := range ins {
		slices.SortStableFunc(in.Deliveries, time.Time.Compare)
		out[id] = *in
	}
	return out, nil
}

// ruleInputSet is the inputs being read, by invoice.
type ruleInputSet map[int64]*reminderrules.Input

// of is invoice id's input; a row of an invoice the set does not hold is an
// error — every statement reads only the invoices RuleInvoices answered.
func (ins ruleInputSet) of(id int64, what string) (*reminderrules.Input, error) {
	in, ok := ins[id]
	if !ok {
		return nil, fmt.Errorf("invoices: %s of document %d, which the rules do not judge", what, id)
	}
	return in, nil
}

// readRuleFacts reads every invoice's credits, payments, deliveries, letters,
// waivers, charge payments, holds, hand-offs and policy into ins, one
// statement each.
func readRuleFacts(ctx context.Context, q *store.Queries, ids []int64, ins ruleInputSet) error {
	credits, err := q.RuleCredits(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the credit notes: %w", err)
	}
	for _, r := range credits {
		in, err := ins.of(r.InvoiceID, "a credit note")
		if err != nil {
			return err
		}
		gross, err := ratFromNumeric(r.GrossTotal)
		if err != nil {
			return fmt.Errorf("invoices: read a credit note of document %d: %w", r.InvoiceID, err)
		}
		in.Credits = append(in.Credits, reminderrules.Credit{IssueDate: utcDay(r.IssueDate.Time), Gross: gross})
	}

	payments, err := q.RulePayments(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the payments: %w", err)
	}
	for _, r := range payments {
		in, err := ins.of(r.InvoiceID, "a payment")
		if err != nil {
			return err
		}
		amount, err := ratFromNumeric(r.Amount)
		if err != nil {
			return fmt.Errorf("invoices: read a payment of document %d: %w", r.InvoiceID, err)
		}
		in.Payments = append(in.Payments, reminderrules.Payment{PaidOn: utcDay(r.PaidOn.Time), OrderedOn: dayOrNil(r.OrderedOn), Amount: amount})
	}

	deliveries, err := q.RuleDeliveries(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the deliveries: %w", err)
	}
	for _, r := range deliveries {
		in, err := ins.of(r.InvoiceID, "a delivery")
		if err != nil {
			return err
		}
		day, err := deliveryDay(r.InvoiceID, r.Kind, r.At, r.DeliveredOn)
		if err != nil {
			return err
		}
		in.Deliveries = append(in.Deliveries, day)
	}

	letters, err := q.RuleLetters(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the letters: %w", err)
	}
	for _, r := range letters {
		in, err := ins.of(r.InvoiceID, "a letter")
		if err != nil {
			return err
		}
		l, err := letterOf(r)
		if err != nil {
			return err
		}
		in.Letters = append(in.Letters, l)
	}

	waivers, err := q.RuleWaivers(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the waivers: %w", err)
	}
	for _, r := range waivers {
		in, err := ins.of(r.InvoiceID, "a waiver")
		if err != nil {
			return err
		}
		w, err := waiverOf(r)
		if err != nil {
			return err
		}
		in.Waivers = append(in.Waivers, w)
	}

	chargePayments, err := q.RuleChargePayments(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the charge payments: %w", err)
	}
	for _, r := range chargePayments {
		in, err := ins.of(r.InvoiceID, "a charge payment")
		if err != nil {
			return err
		}
		p, err := chargePaymentOf(r)
		if err != nil {
			return err
		}
		in.ChargePayments = append(in.ChargePayments, p)
	}

	holds, err := q.RuleHolds(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the holds: %w", err)
	}
	for _, r := range holds {
		in, err := ins.of(r.InvoiceID, "a hold")
		if err != nil {
			return err
		}
		in.OnHold, in.ChargesBarred = r.OnHold, r.ChargesBarred
	}

	handoffs, err := q.RuleHandoffs(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the hand-offs: %w", err)
	}
	for _, id := range handoffs {
		in, err := ins.of(id, "a hand-off")
		if err != nil {
			return err
		}
		in.HandedOff = true
	}

	policies, err := q.RulePolicies(ctx, ids)
	if err != nil {
		return fmt.Errorf("invoices: read the reminder policies: %w", err)
	}
	for _, r := range policies {
		in, err := ins.of(r.InvoiceID, "a reminder policy")
		if err != nil {
			return err
		}
		in.Mode = reminderrules.Mode(r.Mode)
	}
	return nil
}
