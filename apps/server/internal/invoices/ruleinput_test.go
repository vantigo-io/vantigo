package invoices_test

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
)

// ruleDay is a day of 2026 as the engine reads it: a UTC midnight.
func ruleDay(month time.Month, d int) time.Time {
	return time.Date(2026, month, d, 0, 0, 0, 0, time.UTC)
}

// ruleL is the day every rule-input test asks about.
var ruleL = ruleDay(time.October, 20)

// poolInput is the loader's pool read of invoice id on ruleL.
func poolInput(t *testing.T, h *harness, id int64) reminderrules.Input {
	t.Helper()
	got, err := invoices.RuleInputsForTest(context.Background(), h.Deps(), h.Pool(), []int64{id}, ruleL)
	if err != nil {
		t.Fatalf("ruleInputs(%d): %v", id, err)
	}
	in, ok := got[id]
	if !ok {
		t.Fatalf("ruleInputs(%d) = %v, want the invoice", id, got)
	}
	if err := reminderrules.Validate(in); err != nil {
		t.Fatalf("ruleInputs(%d) breaks the contract: %v", id, err)
	}
	return in
}

// lockedInput is the loader's read of invoice id under its lock, in a
// transaction of its own, exclude left out of flight; the transaction ends
// with the read, so the lock is not held past it.
func lockedInput(t *testing.T, h *harness, id, exclude int64) reminderrules.Input {
	t.Helper()
	tx, _ := rawTx(t, h)
	in, err := invoices.RuleInputLockedForTest(context.Background(), h.Deps(), tx, id, ruleL, exclude)
	_ = tx.Rollback(context.Background())
	if err != nil {
		t.Fatalf("ruleInputLocked(%d): %v", id, err)
	}
	if err := reminderrules.Validate(in); err != nil {
		t.Fatalf("ruleInputLocked(%d) breaks the contract: %v", id, err)
	}
	return in
}

// rendered is an input as text — every amount by its value, every day by
// its instant and zone — so two reads compare field by field.
func rendered(in reminderrules.Input) string { return fmt.Sprintf("%+v", in) }

// midnights asserts every day of in is a UTC midnight, field by field — what
// Validate checks, named here so a failure says which.
func midnights(t *testing.T, what string, in reminderrules.Input) {
	t.Helper()
	check := func(field string, d time.Time) {
		if d.Location() != time.UTC || !d.Equal(d.Truncate(24*time.Hour)) {
			t.Errorf("%s: %s = %v, want a UTC midnight", what, field, d)
		}
	}
	check("L", in.L)
	check("the issue date", in.Invoice.IssueDate)
	check("the due date", in.Invoice.DueDate)
	check("regime_reviewed_through", in.Settings.RegimeReviewedThrough)
	for _, c := range in.Credits {
		check("a credit note's day", c.IssueDate)
	}
	for _, p := range in.Payments {
		check("a payment's paid_on", p.PaidOn)
		if p.OrderedOn != nil {
			check("a payment's ordered_on", *p.OrderedOn)
		}
	}
	for _, d := range in.Deliveries {
		check("a delivery", d)
	}
	for _, l := range in.Letters {
		if l.SentOn != nil {
			check("a letter's sent_on", *l.SentOn)
		}
		if l.Deadline != nil {
			check("a letter's deadline", *l.Deadline)
		}
	}
	for _, p := range in.ChargePayments {
		check("a charge payment's paid_on", p.PaidOn)
	}
	for _, r := range in.Rates {
		check("a rate's valid_from", r.ValidFrom)
	}
}

// rat is a decimal for comparing an amount.
func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic(s)
	}
	return r
}

// plantBankPayment plants a payment of invoice id matched from an OCR line
// ordered on orderedOn and booked on paidOn.
func plantBankPayment(t *testing.T, h *harness, id int64, orderedOn, paidOn, amount string) {
	t.Helper()
	file := plantID(t, h, `
		INSERT INTO invoices.bank_files (format, sha256, file_identity, object_key, byte_size, accounts, transactions,
		    ignored, ignored_kinds, uploaded_by_user_id, uploaded_at)
		VALUES ('ocr', md5(random()::text) || md5(random()::text), md5(random()::text), 'bank-files/r.ocr', 400,
		    ARRAY['15032080119'], 1, 0, '{}', gen_random_uuid(), now()) RETURNING id`)
	line := plantID(t, h, `
		INSERT INTO invoices.bank_transactions (bank_file_id, line_ref, format, account, direction, booked_on, ordered_on,
		    amount, currency, fingerprint, ordinal, status)
		VALUES ($1, '1/1', 'ocr', '15032080119', 'credit', $2::date, $3::date, $4::numeric, 'NOK',
		    md5(random()::text) || md5(random()::text), 1, 'matched') RETURNING id`, file, paidOn, orderedOn, amount)
	h.Exec(t, `INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, source, bank_transaction_id,
		    registered_by_user_id, registered_at)
		VALUES ($1, $2::date, $3::numeric, 'NOK', 'ocr', $4, gen_random_uuid(), now())`, id, paidOn, amount, line)
}

// plantManualDelivery plants a live manual delivery of invoice id.
func plantManualDelivery(t *testing.T, h *harness, id int64, on string) {
	t.Helper()
	h.Exec(t, `INSERT INTO invoices.manual_deliveries (invoice_id, kind, delivered_on, recorded_by_user_id, recorded_at)
		VALUES ($1, 'handed_over', $2::date, gen_random_uuid(), now())`, id, on)
}

// plantLetterIn plants a letter of invoice id in status (any but sent; a
// printed one with its facts and a batch) and answers its id.
func plantLetterIn(t *testing.T, h *harness, id int64, sequence int, status string) int64 {
	t.Helper()
	switch status {
	case "queued":
		return plantQueued(t, h, id, sequence)
	case "awaiting_print":
		return plantID(t, h, `
			INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, language, created_at, created_by_user_id, status)
			VALUES ($1, $2, $3, 'reminder', 'paper', 'nb', now(), gen_random_uuid(), 'awaiting_print') RETURNING id`,
			id, plantRun(t, h), sequence)
	case "printed":
		batch := plantID(t, h, `INSERT INTO invoices.reminder_print_batches (post_on, created_at, created_by_user_id)
			VALUES (DATE '2026-10-05', now(), gen_random_uuid()) RETURNING id`)
		return plantID(t, h, `
			INSERT INTO invoices.reminders (invoice_id, run_id, print_batch_id, sequence, level, channel, language, created_at,
			    created_by_user_id, status, sent_on, deadline, regime, principal_open, fee_kind, charges_earlier, interest,
			    interest_waived, interest_paid, total)
			VALUES ($1, $2, $3, $4, 'reminder', 'paper', 'nb', now(), gen_random_uuid(), 'printed', DATE '2026-10-05',
			    DATE '2026-10-19', 'inkassolov_1988', 700, 'none', 0, 0, 0, 0, 700)
			RETURNING id`, id, plantRun(t, h), batch, sequence)
	case "withdrawn":
		return plantID(t, h, `
			INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, language, created_at, created_by_user_id,
			    status, withdrawn_at, withdrawal_reason)
			VALUES ($1, $2, $3, 'reminder', 'email', 'nb', now(), gen_random_uuid(), 'withdrawn', now(), 'on_hold') RETURNING id`,
			id, plantRun(t, h), sequence)
	case "failed":
		return plantID(t, h, `
			INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, language, created_at, created_by_user_id,
			    status, failed_at)
			VALUES ($1, $2, $3, 'reminder', 'email', 'nb', now(), gen_random_uuid(), 'failed', now()) RETURNING id`,
			id, plantRun(t, h), sequence)
	}
	t.Fatalf("no such status %q", status)
	return 0
}

// plantChargePayment plants a live manual charge payment of invoice id.
func plantChargePayment(t *testing.T, h *harness, id int64, paidOn, amount string) int64 {
	t.Helper()
	return plantID(t, h, `INSERT INTO invoices.charge_payments (invoice_id, paid_on, amount, currency, source,
		    registered_by_user_id, registered_at)
		VALUES ($1, $2::date, $3::numeric, 'NOK', 'manual', gen_random_uuid(), now()) RETURNING id`, id, paidOn, amount)
}

// richInvoice is an invoice with one of every fact the engine reads: a
// credit note, a manual payment and one from an OCR line with its
// ordered_on, an e-mail, an EHF and a manual delivery, a sent letter with a
// fee and a queued one, a fee waiver, a live and a removed charge payment, a
// lifted hold that barred charges and a live one, a hand-off, the customer's
// policy, settings changed from their defaults and a rate added.
func richInvoice(t *testing.T, h *harness) int64 {
	t.Helper()
	inv := thousand(t, h)
	issued(t, h, creditOf(t, h, inv.ID, map[int32]float64{1: 1}).ID)
	plantPayment(t, h, inv.ID, "200", "2026-09-20")
	plantBankPayment(t, h, inv.ID, "2026-09-25", "2026-09-27", "100")
	plantEmail(t, h, inv.ID, time.Date(2026, 9, 12, 22, 30, 0, 0, time.UTC)) // the 13th in Oslo
	plantEhf(t, h, inv.ID, "delivered", time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC))
	plantManualDelivery(t, h, inv.ID, "2026-09-15")
	sent := plantSent(t, h, inv.ID, sentFacts{1, "2026-10-01", "reminder_fee", "35", "", "4.50"})
	plantQueued(t, h, inv.ID, 2)
	h.Exec(t, `INSERT INTO invoices.charge_waivers (invoice_id, reminder_id, kind, amount, reason, waived_by_user_id, waived_at)
		VALUES ($1, $2, 'fee', 35, 'goodwill', gen_random_uuid(), now())`, inv.ID, sent)
	plantChargePayment(t, h, inv.ID, "2026-10-03", "2.25")
	removed := plantChargePayment(t, h, inv.ID, "2026-10-04", "1")
	h.Exec(t, `UPDATE invoices.charge_payments SET removed_at = now(), removed_by_user_id = gen_random_uuid(),
		removal_reason = 'Feil' WHERE id = $1`, removed)
	h.Exec(t, `INSERT INTO invoices.invoice_holds (invoice_id, kind, note, placed_at, placed_by_user_id)
		VALUES ($1, 'disputed', 'Bestrider', now(), gen_random_uuid())`, inv.ID)
	h.Exec(t, `UPDATE invoices.invoice_holds SET lifted_at = now(), lifted_by_user_id = gen_random_uuid(), charges_allowed = false,
		lift_note = '' WHERE invoice_id = $1`, inv.ID)
	h.Exec(t, `INSERT INTO invoices.invoice_holds (invoice_id, kind, note, placed_at, placed_by_user_id)
		VALUES ($1, 'disputed', 'Igjen', now(), gen_random_uuid())`, inv.ID)
	h.Exec(t, `INSERT INTO invoices.collection_handoffs (invoice_id, handed_on, agency, created_at, created_by_user_id)
		VALUES ($1, DATE '2026-10-10', 'Inkasso AS', now(), gen_random_uuid())`, inv.ID)
	h.Exec(t, `INSERT INTO invoices.customer_reminder_policies (customer_id, mode, updated_by_user_id, updated_at)
		VALUES ($1, 'no_charges', gen_random_uuid(), now())`, customerAcme)
	h.Exec(t, `UPDATE invoices.reminder_settings SET enabled = true, grace_days = 5, inkassolov_2026_from = DATE '2027-01-01'`)
	h.Exec(t, `SELECT invoices.seed_collection_rate('inkassosats', DATE '2030-01-01', 900, 'test')`)
	return inv.ID
}

// The same invoice read both ways — on the pool with another invoice beside
// it, and under its lock — gives the same Input, every field; each read
// keeps the contract (reminderrules.Validate) and every day is a UTC
// midnight. Every field is filled, so "the same" compares something.
func TestRuleInput_PoolAndLockedAgree(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	id := richInvoice(t, h)
	other := thousand(t, h)
	plantPayment(t, h, other.ID, "999", "2026-09-21")

	all, err := invoices.RuleInputsForTest(context.Background(), h.Deps(), h.Pool(), []int64{id, other.ID}, ruleL)
	if err != nil {
		t.Fatalf("ruleInputs: %v", err)
	}
	pool, locked := all[id], lockedInput(t, h, id, 0)
	for what, in := range map[string]reminderrules.Input{"the pool": pool, "the lock": locked, "the other": all[other.ID]} {
		if err := reminderrules.Validate(in); err != nil {
			t.Errorf("%s breaks the contract: %v", what, err)
		}
		midnights(t, what, in)
	}
	if rendered(pool) != rendered(locked) {
		t.Errorf("the pool and the lock disagree:\npool   %s\nlocked %s", rendered(pool), rendered(locked))
	}
	if n := len(all[other.ID].Payments); n != 1 || all[other.ID].Payments[0].Amount.Cmp(rat("999")) != 0 {
		t.Errorf("the other invoice's payments = %+v, want its own 999 alone", all[other.ID].Payments)
	}

	in := pool
	switch {
	case !in.L.Equal(ruleL), !in.Invoice.IssueDate.Equal(ruleDay(time.September, 12)), in.Invoice.DueDate.IsZero(),
		in.Invoice.Gross.Cmp(rat("1000")) != 0, in.Invoice.BuyerType == "":
		t.Errorf("the invoice = %+v on %v, want 1000 issued 12 September with its buyer", in.Invoice, in.L)
	case len(in.Credits) != 1, len(in.Payments) != 2, len(in.Deliveries) != 3, len(in.Letters) != 2,
		len(in.Waivers) != 1, len(in.ChargePayments) != 1, len(in.Rates) == 0:
		t.Errorf("the sets = %d credits, %d payments, %d deliveries, %d letters, %d waivers, %d charge payments, %d rates; "+
			"want 1, 2, 3, 2, 1, 1 (the removed one absent) and the rates",
			len(in.Credits), len(in.Payments), len(in.Deliveries), len(in.Letters), len(in.Waivers), len(in.ChargePayments), len(in.Rates))
	case in.Payments[1].OrderedOn == nil || !in.Payments[1].OrderedOn.Equal(ruleDay(time.September, 25)):
		t.Errorf("the OCR payment's ordered_on = %v, want 25 September", in.Payments[1].OrderedOn)
	case !in.OnHold, !in.HandedOff, !in.ChargesBarred, in.Mode != reminderrules.ModeNoCharges:
		t.Errorf("hold %v, hand-off %v, barred %v, mode %q; want a live hold, a hand-off, charges barred, no_charges",
			in.OnHold, in.HandedOff, in.ChargesBarred, in.Mode)
	case !in.Settings.Enabled, in.Settings.GraceDays != 5, in.Settings.Inkassolov2026From == nil:
		t.Errorf("the settings = %+v, want enabled, grace 5 and the 2026 day", in.Settings)
	case in.Exclude != 0:
		t.Errorf("Exclude = %d, want none", in.Exclude)
	}
}

// Each fact the engine reads changes the input when it comes: a credit note,
// a payment and its line's ordered_on, each kind of delivery, a letter of
// each status, a waiver, a charge payment — and a removed one goes again —
// a live hold, a barring lift, a hand-off, the policy, the settings and the
// rates. A loader that dropped any of them would leave the input as it was.
func TestRuleInput_ReadsEveryFact(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	id := inv.ID
	prev := poolInput(t, h, id)
	if prev.Mode != reminderrules.ModeNormal || prev.OnHold || prev.HandedOff || prev.ChargesBarred ||
		len(prev.Credits)+len(prev.Payments)+len(prev.Deliveries)+len(prev.Letters)+len(prev.Waivers)+len(prev.ChargePayments) != 0 {
		t.Fatalf("a bare invoice = %s, want nothing but the invoice, the settings and the rates, mode normal", rendered(prev))
	}
	if len(prev.Rates) == 0 {
		t.Fatalf("the seeded rates are not read")
	}
	var sent, chargePayment int64
	steps := []struct {
		what  string
		plant func()
		check func(in reminderrules.Input) bool
	}{
		{"a credit note", func() { issued(t, h, creditOf(t, h, id, map[int32]float64{1: 1}).ID) },
			func(in reminderrules.Input) bool {
				return len(in.Credits) == 1 && in.Credits[0].Gross.Cmp(rat("100")) == 0 &&
					in.Credits[0].IssueDate.Equal(ruleDay(time.September, 12))
			}},
		{"a manual payment", func() { plantPayment(t, h, id, "200", "2026-09-20") },
			func(in reminderrules.Input) bool {
				return len(in.Payments) == 1 && in.Payments[0].OrderedOn == nil && in.Payments[0].Amount.Cmp(rat("200")) == 0 &&
					in.Payments[0].PaidOn.Equal(ruleDay(time.September, 20))
			}},
		{"a payment from a line with its ordered_on", func() { plantBankPayment(t, h, id, "2026-09-25", "2026-09-27", "100") },
			func(in reminderrules.Input) bool {
				return len(in.Payments) == 2 && in.Payments[1].OrderedOn != nil &&
					in.Payments[1].OrderedOn.Equal(ruleDay(time.September, 25)) && in.Payments[1].PaidOn.Equal(ruleDay(time.September, 27))
			}},
		{"an e-mail", func() { plantEmail(t, h, id, time.Date(2026, 9, 12, 22, 30, 0, 0, time.UTC)) },
			func(in reminderrules.Input) bool {
				return slices.EqualFunc(in.Deliveries, []time.Time{ruleDay(time.September, 13)}, time.Time.Equal)
			}},
		{"a delivered EHF transmission", func() { plantEhf(t, h, id, "delivered", time.Date(2026, 9, 14, 23, 10, 0, 0, time.UTC)) },
			func(in reminderrules.Input) bool {
				return slices.EqualFunc(in.Deliveries, []time.Time{ruleDay(time.September, 13), ruleDay(time.September, 15)}, time.Time.Equal)
			}},
		{"a manual delivery", func() { plantManualDelivery(t, h, id, "2026-09-14") },
			func(in reminderrules.Input) bool {
				return slices.EqualFunc(in.Deliveries,
					[]time.Time{ruleDay(time.September, 13), ruleDay(time.September, 14), ruleDay(time.September, 15)}, time.Time.Equal)
			}},
		{"a sent letter", func() { sent = plantSent(t, h, id, sentFacts{1, "2026-10-01", "reminder_fee", "35", "", "4.50"}) },
			func(in reminderrules.Input) bool {
				if len(in.Letters) != 1 {
					return false
				}
				l := in.Letters[0]
				return l.ID == sent && l.Status == reminderrules.StatusSent &&
					l.SentOn != nil && l.SentOn.Equal(ruleDay(time.October, 1)) && l.Deadline != nil &&
					l.FeeKind == reminderrules.FeeReminder && l.Fee.Cmp(rat("35")) == 0 && l.Interest.Cmp(rat("4.50")) == 0 &&
					l.Compensation == nil
			}},
		{"a waiver", func() {
			h.Exec(t, `INSERT INTO invoices.charge_waivers (invoice_id, reminder_id, kind, amount, reason, waived_by_user_id, waived_at)
				VALUES ($1, $2, 'fee', 35, 'goodwill', gen_random_uuid(), now())`, id, sent)
		}, func(in reminderrules.Input) bool {
			return len(in.Waivers) == 1 && in.Waivers[0].ReminderID == sent && in.Waivers[0].Kind == reminderrules.WaiverFee &&
				in.Waivers[0].Amount.Cmp(rat("35")) == 0
		}},
		{"a charge payment", func() { chargePayment = plantChargePayment(t, h, id, "2026-10-03", "2.25") },
			func(in reminderrules.Input) bool {
				return len(in.ChargePayments) == 1 && in.ChargePayments[0].ID == chargePayment &&
					in.ChargePayments[0].Amount.Cmp(rat("2.25")) == 0 && in.ChargePayments[0].PaidOn.Equal(ruleDay(time.October, 3))
			}},
		{"a second charge payment", func() { plantChargePayment(t, h, id, "2026-10-04", "1") },
			func(in reminderrules.Input) bool { return len(in.ChargePayments) == 2 }},
		{"the second charge payment removed", func() {
			h.Exec(t, `UPDATE invoices.charge_payments SET removed_at = now(), removed_by_user_id = gen_random_uuid(),
				removal_reason = 'Feil' WHERE invoice_id = $1 AND id <> $2`, id, chargePayment)
		}, func(in reminderrules.Input) bool {
			return len(in.ChargePayments) == 1 && in.ChargePayments[0].ID == chargePayment
		}},
		{"a hold", func() {
			h.Exec(t, `INSERT INTO invoices.invoice_holds (invoice_id, kind, note, placed_at, placed_by_user_id)
				VALUES ($1, 'disputed', 'Bestrider', now(), gen_random_uuid())`, id)
		}, func(in reminderrules.Input) bool { return in.OnHold && !in.ChargesBarred }},
		{"a lift that bars charges", func() {
			h.Exec(t, `UPDATE invoices.invoice_holds SET lifted_at = now(), lifted_by_user_id = gen_random_uuid(),
				charges_allowed = false, lift_note = '' WHERE invoice_id = $1`, id)
		}, func(in reminderrules.Input) bool { return !in.OnHold && in.ChargesBarred }},
		{"a hand-off", func() {
			h.Exec(t, `INSERT INTO invoices.collection_handoffs (invoice_id, handed_on, agency, created_at, created_by_user_id)
				VALUES ($1, DATE '2026-10-10', 'Inkasso AS', now(), gen_random_uuid())`, id)
		}, func(in reminderrules.Input) bool { return in.HandedOff }},
		{"the policy", func() {
			h.Exec(t, `INSERT INTO invoices.customer_reminder_policies (customer_id, mode, updated_by_user_id, updated_at)
				VALUES ($1, 'none', gen_random_uuid(), now())`, customerAcme)
		}, func(in reminderrules.Input) bool { return in.Mode == reminderrules.ModeNone }},
		{"the settings", func() { h.Exec(t, `UPDATE invoices.reminder_settings SET grace_days = 7`) },
			func(in reminderrules.Input) bool { return in.Settings.GraceDays == 7 }},
		{"the rates", func() {
			h.Exec(t, `SELECT invoices.seed_collection_rate('inkassosats', DATE '2030-01-01', 900, 'test')`)
		},
			func(in reminderrules.Input) bool {
				return len(in.Rates) == len(prev.Rates)+1 && slices.ContainsFunc(in.Rates, func(r reminderrules.Rate) bool {
					return r.Kind == reminderrules.KindInkassosats && r.ValidFrom.Equal(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
				})
			}},
	}
	for _, s := range steps {
		s.plant()
		next := poolInput(t, h, id)
		midnights(t, s.what, next)
		if rendered(next) == rendered(prev) || !s.check(next) {
			t.Errorf("after %s the input = %s\nwas %s", s.what, rendered(next), rendered(prev))
		}
		prev = next
	}

	// A letter of each other status is read as it is: the engine tells the
	// sent from the ones in flight and the withdrawn itself.
	for i, status := range []string{"queued", "awaiting_print", "printed", "withdrawn", "failed"} {
		letter := plantLetterIn(t, h, id, i+2, status)
		next := poolInput(t, h, id)
		last := next.Letters[len(next.Letters)-1]
		if len(next.Letters) != len(prev.Letters)+1 || last.ID != letter || last.Status != status || last.Sequence != i+2 {
			t.Errorf("after a %s letter the letters = %+v, want it last", status, next.Letters)
		}
		if status == "printed" && (last.SentOn == nil || last.PrincipalOpen == nil || last.PrincipalOpen.Cmp(rat("700")) != 0) {
			t.Errorf("the printed letter = %+v, want its facts", last)
		}
		prev = next
	}
}

// The pool read is a handful of statements whatever the list's length: the
// count for one invoice equals the count for 200, each with a delivery, a
// payment and a letter.
func TestRuleInput_AHandfulOfStatements(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plantExportDocuments(t, h, 1, 200, "2026-09-12")
	run := plantRun(t, h)
	h.Exec(t, `INSERT INTO invoices.manual_deliveries (invoice_id, kind, delivered_on, recorded_by_user_id, recorded_at)
		SELECT id, 'posted', DATE '2026-09-12', gen_random_uuid(), now() FROM invoices.invoices`)
	h.Exec(t, `INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, registered_by_user_id, registered_at)
		SELECT id, DATE '2026-09-20', 1, 'NOK', gen_random_uuid(), now() FROM invoices.invoices`)
	h.Exec(t, `INSERT INTO invoices.reminders (invoice_id, run_id, sequence, level, channel, language, created_at, created_by_user_id, status)
		SELECT id, $1, 1, 'reminder', 'email', 'nb', now(), gen_random_uuid(), 'queued' FROM invoices.invoices`, run)
	var ids []int64
	rows, err := h.Pool().Query(context.Background(), `SELECT id FROM invoices.invoices ORDER BY id`)
	if err != nil {
		t.Fatalf("ids: %v", err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 200 {
		t.Fatalf("planted %d invoices, want 200", len(ids))
	}

	count := func(ids []int64) int {
		t.Helper()
		db, statements := invoices.CountingDB(h.Pool())
		got, err := invoices.RuleInputsForTest(context.Background(), h.Deps(), db, ids, ruleL)
		if err != nil {
			t.Fatalf("ruleInputs(%d ids): %v", len(ids), err)
		}
		for _, id := range ids {
			in, ok := got[id]
			if !ok || len(in.Deliveries) != 1 || len(in.Payments) != 1 || len(in.Letters) != 1 {
				t.Fatalf("invoice %d = %+v (read %v), want its delivery, payment and letter", id, in, ok)
			}
		}
		return statements()
	}
	one, all := count(ids[:1]), count(ids)
	if one != all || one > 12 {
		t.Errorf("ruleInputs read %d statements for one invoice and %d for 200, want the same handful (at most 12)", one, all)
	}
}

// The letter being dispatched or posted is left out of flight: read under
// the lock with exclude naming a queued letter, the input carries it and
// names it, and the engine no longer stops at letter_pending — which it does
// for the same invoice read with nothing excluded.
func TestRuleInput_ExcludeLeavesTheLetterOutOfFlight(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	inv := thousand(t, h)
	plantEmail(t, h, inv.ID, time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC))
	h.Exec(t, `UPDATE invoices.reminder_settings SET enabled = true`)
	letter := plantQueued(t, h, inv.ID, 1)

	plain := lockedInput(t, h, inv.ID, 0)
	if got := reminderrules.Next(plain); !slices.Contains(got.Reasons, reminderrules.ReasonLetterPending) {
		t.Fatalf("with nothing excluded Next = %+v, want letter_pending", got)
	}
	excluded := lockedInput(t, h, inv.ID, letter)
	if excluded.Exclude != letter || len(excluded.Letters) != 1 || excluded.Letters[0].ID != letter {
		t.Errorf("excluded = Exclude %d, letters %+v; want the letter named and still read", excluded.Exclude, excluded.Letters)
	}
	if got := reminderrules.Next(excluded); slices.Contains(got.Reasons, reminderrules.ReasonLetterPending) {
		t.Errorf("with the letter excluded Next = %+v, want it judged past the letter in flight", got)
	}
	if pool := poolInput(t, h, inv.ID); pool.Exclude != 0 {
		t.Errorf("the pool's Exclude = %d, want none", pool.Exclude)
	}
}
