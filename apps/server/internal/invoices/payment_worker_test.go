package invoices_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/invoices/reminderrules"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/payments"
	"github.com/vantigo-io/vantigo/server/internal/payments/vipps"
	"github.com/vantigo-io/vantigo/server/internal/payments/vipps/vippstest"
)

// The invoices-payments worker (invoices payments and reminders design D16)
// and the reservation it holds in the open amount (D2, D8): every test
// against vippstest, through the harness's transport, on the fixed clock
// moved with h.Advance.

// attemptRow is a payment attempt as the worker leaves it.
type attemptRow struct {
	ID                                       int64
	State                                    string
	Reserved, Authorized, Captured, Released *string
	ReleasePending                           bool
	CaptureKey                               *string
	CaptureAttempts, Polls                   int32
	Failures, InsufficientFunds              int32
	NextPollAt, RefundWatchUntil, FinishedAt *time.Time
	LastError                                *string
}

// attemptOf reads the attempt under reference.
func attemptOf(t *testing.T, h *harness, reference string) attemptRow {
	t.Helper()
	var a attemptRow
	err := h.Pool().QueryRow(context.Background(), `
		SELECT id, state, reserved_amount::text, authorized_amount::text, captured_amount::text, cancelled_amount::text,
		       release_pending, capture_key::text, capture_attempts, polls, next_poll_at, refund_watch_until, finished_at, last_error,
		       failures, insufficient_funds
		FROM invoices.payment_attempts WHERE reference = $1`, reference).Scan(
		&a.ID, &a.State, &a.Reserved, &a.Authorized, &a.Captured, &a.Released, &a.ReleasePending, &a.CaptureKey,
		&a.CaptureAttempts, &a.Polls, &a.NextPollAt, &a.RefundWatchUntil, &a.FinishedAt, &a.LastError,
		&a.Failures, &a.InsufficientFunds)
	if err != nil {
		t.Fatalf("read attempt %s: %v", reference, err)
	}
	return a
}

// text is *s, or "NULL".
func text(s *string) string {
	if s == nil {
		return "NULL"
	}
	return *s
}

// workerReady is an installation offering Vipps whose vippstest dates its
// events on the harness's clock.
func workerReady(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	h, _ := payReady(t, opts...)
	h.vipps.SetNow(h.Now)
	return h
}

// newAttempt issues a 2500.00 invoice to Acme and starts a Vipps payment of
// it on the pay page: an attempt created at vippstest, first read 5 s on.
func newAttempt(t *testing.T, h *harness) (invoiceJSON, string) {
	t.Helper()
	inv := issuedAcme(t, h)
	created := startedPay(t, h.Client(t), payToken(t, h, inv.ID))
	return inv, created.Reference
}

// approvedAttempt is newAttempt approved by the payer and due.
func approvedAttempt(t *testing.T, h *harness) (invoiceJSON, string) {
	t.Helper()
	inv, reference := newAttempt(t, h)
	if !h.vipps.Approve(reference) {
		t.Fatalf("vippstest did not approve %s", reference)
	}
	h.Advance(5 * time.Second)
	return inv, reference
}

// cycle runs one worker cycle: every due attempt handled.
func cycle(t *testing.T, h *harness) {
	t.Helper()
	if err := invoices.NewPaymentWorker(h.Deps()).RunCycle(context.Background()); err != nil {
		t.Fatalf("a payments worker cycle: %v", err)
	}
}

// step runs one claim, failing unless an attempt was claimed.
func step(t *testing.T, h *harness) {
	t.Helper()
	ok, err := invoices.NewPaymentWorker(h.Deps()).ProcessOne(context.Background())
	if err != nil || !ok {
		t.Fatalf("a payments worker claim = %v, %v; want one claimed", ok, err)
	}
}

// vippsPaymentsOf is invoice id's live Vipps payments, "amount paid_on reference".
func vippsPaymentsOf(t *testing.T, h *harness, id int64) []string {
	t.Helper()
	return texts(t, h, `SELECT concat_ws(' ', amount, paid_on, reference) FROM invoices.payments
		WHERE invoice_id = $1 AND source = 'vipps' AND removed_at IS NULL ORDER BY id`, id)
}

// vippsCalls is how many requests of method to the path ending in suffix
// vippstest received for reference.
func vippsCalls(h *harness, reference, method, suffix string) int {
	n := 0
	for _, r := range h.vipps.Requests() {
		if r.Method == method && strings.Contains(r.Path, "/payments/"+reference) && strings.HasSuffix(strings.SplitN(r.Path, "?", 2)[0], suffix) {
			n++
		}
	}
	return n
}

// plantReservation writes an on-site attempt of invoice id in state with
// amount authorized and reserved — capturing or capture_unknown hold a
// reservation, authorized none — due now, and answers its reference.
func plantReservation(t *testing.T, h *harness, id int64, state, amount string) string {
	t.Helper()
	reserved := "NULL"
	if state == "capturing" || state == "capture_unknown" {
		reserved = amount
	}
	return modtest.One[string](t, h.Harness, `
		INSERT INTO invoices.payment_attempts (invoice_id, provider, msn, flow, amount, cancel_key, state,
		    created_at, expires_at, authorized_amount, authorized_at, reserved_amount, capture_key, next_poll_at)
		VALUES ($1, 'vipps', '123456', 'on_site', $3::numeric, gen_random_uuid(), $2::text, $4::timestamptz,
		    $4::timestamptz + interval '10 minutes', $3::numeric, $4::timestamptz, `+reserved+`::numeric,
		    CASE WHEN $2::text IN ('capturing', 'capture_unknown') THEN gen_random_uuid() END, $4::timestamptz)
		RETURNING reference`, id, state, amount, h.Now())
}

// TestPayments_ReservationCountsInOpen (D2, I4, reading 4): a capture in
// progress lowers what a manual registration may take, and a refusal names
// the Vipps payment being completed; a reservation of the whole open amount
// refuses a manual payment and a hand-off as invoice_settled with the same
// sentence. The document's open amount and state do not see a reservation.
func TestPayments_ReservationCountsInOpen(t *testing.T) {
	t.Parallel()
	h := workerReady(t)
	inv := issuedAcme(t, h) // 2500.00
	plantReservation(t, h, inv.ID, "capturing", "1000")

	p := refusedAs(t, "a payment above the rest", payer(t, h).Do(http.MethodPost, paymentsPath(inv.ID), pay(2000, "2026-09-12")), "payment_exceeds_open")
	if p.OpenAmount == nil || *p.OpenAmount != 1500 || !strings.Contains(p.Detail, "A Vipps payment of 1000.00 is being completed") {
		t.Errorf("the refusal = %+v (open %v), want open 1500 and the Vipps payment named", p, money(p.OpenAmount))
	}
	if got := getInvoice(t, h, inv.ID); money(got.OpenAmount) != 2500.0 || got.State != "open" {
		t.Errorf("the document = %s open %v, want open 2500: a reservation is not a payment", got.State, money(got.OpenAmount))
	}
	registered(t, h, inv.ID, pay(1500, "2026-09-12"))

	whole := issuedAcme(t, h)
	plantReservation(t, h, whole.ID, "capture_unknown", "2500")
	p = refusedAs(t, "a payment of a fully reserved invoice", payer(t, h).Do(http.MethodPost, paymentsPath(whole.ID), pay(1, "2026-09-12")), "invoice_settled")
	if !strings.Contains(p.Detail, "A Vipps payment of 2500.00 is being completed") || strings.Contains(p.Detail, "cover its total") {
		t.Errorf("the refusal's detail = %q, want the Vipps payment named, not a paid invoice", p.Detail)
	}
	plantManualDelivery(t, h, whole.ID, "2026-09-12")
	ho := conflictAs(t, "a hand-off of a fully reserved invoice", chargePayer(t, h).Do(http.MethodPost, handoffPath(whole.ID),
		handoffBody("2026-09-12", "Kredinor AS")), "invoice_settled")
	if !strings.Contains(ho.Detail, "A Vipps payment of 2500.00 is being completed") || !strings.Contains(ho.Detail, "registered or released") {
		t.Errorf("the hand-off's detail = %q, want the Vipps payment named and when to try again", ho.Detail)
	}
	if got := getInvoice(t, h, whole.ID); money(got.OpenAmount) != 2500.0 || got.State != "open" {
		t.Errorf("the fully reserved document = %s open %v, want open 2500", got.State, money(got.OpenAmount))
	}
}

// TestOpen_ReservationCountedOnce (the review's decision 1): open is gross
// less credited, paid and the live reservations, each reservation once — on
// the pay page's read, an attempt's create, the overdue list and the rule
// input alike.
func TestOpen_ReservationCountedOnce(t *testing.T) {
	t.Parallel()
	h := workerReady(t)
	inv := issuedAcme(t, h) // 2500.00
	token := payToken(t, h, inv.ID)
	plantPayment(t, h, inv.ID, "500", "2026-09-12")
	plantReservation(t, h, inv.ID, "capturing", "1000")

	if page, _ := readPay(t, h.Client(t), token); page.OpenAmount != 1000 || !page.Payable {
		t.Errorf("the read's openAmount = %v (payable %v), want 1000", page.OpenAmount, page.Payable)
	}
	created := startedPay(t, h.Client(t), token)
	if a := attemptOf(t, h, created.Reference); a.State != "created" {
		t.Errorf("the new attempt = %s, want created", a.State)
	}
	if amount := modtest.One[string](t, h.Harness, `SELECT amount::text FROM invoices.payment_attempts WHERE reference = $1`, created.Reference); amount != "1000.00" {
		t.Errorf("the attempt's amount = %s, want 1000.00", amount)
	}
	in := poolInput(t, h, inv.ID)
	if len(in.Reservations) != 1 || in.Reservations[0].Amount.Cmp(rat("1000")) != 0 {
		t.Errorf("the rule input's reservations = %v, want one of 1000", in.Reservations)
	}

	remindersOn(t, h, "")
	overdue := deliveredOn(t, h, 9001, customerAcme, "2026-08-03") // 1000.00, a reminder today
	plantReservation(t, h, overdue, "capturing", "400")
	full := deliveredOn(t, h, 9002, customerAcme, "2026-08-03")
	plantReservation(t, h, full, "capture_unknown", "1000")
	list := overdueOf(t, reader(t, h), "")
	if ids := overdueIDs(list); !slices.Contains(ids, overdue) || slices.Contains(ids, full) {
		t.Fatalf("the overdue list = %v, want %d and not the fully reserved %d", ids, overdue, full)
	}
	for _, item := range list.Items {
		if item.InvoiceID == overdue && item.PrincipalOpen != 600 {
			t.Errorf("the overdue item's principalOpen = %v, want 600", item.PrincipalOpen)
		}
	}
}

// TestRuleInput_ReadsReservations (D8): both doors read every live
// reservation — capturing and capture_unknown, never a released or a
// captured one — and agree; a reservation of the whole open amount leaves
// the engine nothing to remind of.
func TestRuleInput_ReadsReservations(t *testing.T) {
	t.Parallel()
	h := workerReady(t)
	inv := issuedAcme(t, h)
	plantLiveAttempt(t, h, inv.ID, "capturing", "700")
	plantLiveAttempt(t, h, inv.ID, "capture_unknown", "300")
	plantLiveAttempt(t, h, inv.ID, "authorized", "")
	pool, locked := poolInput(t, h, inv.ID), lockedInput(t, h, inv.ID, 0)
	var amounts []string
	for _, r := range pool.Reservations {
		amounts = append(amounts, r.Amount.FloatString(2))
	}
	if !slices.Equal(amounts, []string{"700.00", "300.00"}) {
		t.Errorf("the pool read's reservations = %v, want 700.00 and 300.00", amounts)
	}
	if rendered(pool) != rendered(locked) {
		t.Errorf("the two doors disagree:\npool   %s\nlocked %s", rendered(pool), rendered(locked))
	}

	remindersOn(t, h, "")
	due := deliveredOn(t, h, 9101, customerAcme, "2026-08-03")
	plantReservation(t, h, due, "capturing", "1000")
	if out := reminderrules.Next(poolInput(t, h, due)); out.Action != reminderrules.ActionNone || out.Letter != nil {
		t.Errorf("the engine on a fully reserved invoice = %v, want no letter", out.Action)
	}
}

// TestPaymentWorker_EachState: D16's machine against vippstest, the clock
// advanced through each cadence.
func TestPaymentWorker_EachState(t *testing.T) {
	t.Parallel()

	t.Run("created, the cadence and Vipps' own ends", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		_, waiting := newAttempt(t, h)
		_, aborted := newAttempt(t, h)
		_, terminated := newAttempt(t, h)
		h.vipps.Script(aborted, vippstest.Aborted)
		h.vipps.Script(terminated, vippstest.Terminated)
		h.Advance(5 * time.Second)
		cycle(t, h)
		if a := attemptOf(t, h, waiting); a.State != "created" || a.NextPollAt == nil || !a.NextPollAt.Equal(h.Now().Add(2*time.Second)) {
			t.Errorf("an unanswered request = %s next %v, want created, read again in 2 s", a.State, a.NextPollAt)
		}
		for ref, want := range map[string]string{aborted: "aborted", terminated: "terminated"} {
			if a := attemptOf(t, h, ref); a.State != want || a.NextPollAt != nil || a.FinishedAt == nil {
				t.Errorf("%s = %s next %v, want %s and finished", ref, a.State, a.NextPollAt, want)
			}
		}
		// Past its ten minutes Vipps has not said EXPIRED: it is never
		// expired locally, and is read again until Vipps ends it.
		h.Advance(11 * time.Minute)
		cycle(t, h)
		if a := attemptOf(t, h, waiting); a.State != "created" || a.NextPollAt == nil {
			t.Errorf("past expires_at, Vipps still CREATED = %s next %v, want created and polled", a.State, a.NextPollAt)
		}
		h.vipps.Script(waiting, vippstest.Expired)
		h.Advance(10 * time.Minute)
		cycle(t, h)
		if a := attemptOf(t, h, waiting); a.State != "expired" || a.NextPollAt != nil {
			t.Errorf("Vipps' EXPIRED = %s, want expired", a.State)
		}
	})

	t.Run("a capture registered once", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv, ref := approvedAttempt(t, h)
		cycle(t, h)
		a := attemptOf(t, h, ref)
		if a.State != "captured" || text(a.Captured) != "2500.00" || a.Reserved != nil || a.ReleasePending ||
			a.RefundWatchUntil == nil || !a.RefundWatchUntil.Equal(h.Now().Add(30*24*time.Hour)) {
			t.Fatalf("the attempt = %+v, want captured 2500.00, no reservation, no release, watched 30 days", a)
		}
		want := []string{"2500.00 2026-09-12 " + ref}
		if got := vippsPaymentsOf(t, h, inv.ID); !slices.Equal(got, want) {
			t.Errorf("the Vipps payments = %v, want %v", got, want)
		}
		if got := getInvoice(t, h, inv.ID); got.State != "paid" {
			t.Errorf("the invoice = %s, want paid", got.State)
		}
		h.Advance(25 * time.Hour)
		cycle(t, h)
		if got := vippsPaymentsOf(t, h, inv.ID); !slices.Equal(got, want) || vippsCalls(h, ref, http.MethodPost, "/capture") != 1 {
			t.Errorf("a day on: payments %v, %d captures; want one of each", got, vippsCalls(h, ref, http.MethodPost, "/capture"))
		}
		if n := h.Count(t, `SELECT count(*) FROM invoices.payments WHERE source = 'vipps' AND registered_by_user_id IS NULL
			AND payment_attempt_id = $1`, a.ID); n != 1 {
			t.Errorf("%d payments name the attempt with no user, want 1", n)
		}
	})

	t.Run("a lost capture answer is registered once on the next read", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv, ref := approvedAttempt(t, h)
		h.vippsWire.dropNextCaptures(1)
		cycle(t, h)
		if a := attemptOf(t, h, ref); a.State != "capturing" || text(a.Reserved) != "2500.00" || a.CaptureAttempts != 1 {
			t.Fatalf("after the lost answer = %+v, want capturing, 2500.00 reserved, one capture sent", a)
		}
		if got := vippsPaymentsOf(t, h, inv.ID); len(got) != 0 {
			t.Fatalf("payments %v before the read", got)
		}
		h.Advance(10 * time.Minute)
		cycle(t, h)
		if got := vippsPaymentsOf(t, h, inv.ID); len(got) != 1 || vippsCalls(h, ref, http.MethodPost, "/capture") != 1 {
			t.Errorf("payments %v after %d captures, want one payment and no second capture", got, vippsCalls(h, ref, http.MethodPost, "/capture"))
		}
	})

	t.Run("less open than authorized: the rest released", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv, ref := approvedAttempt(t, h)
		registered(t, h, inv.ID, pay(1000, "2026-09-12"))
		cycle(t, h)
		a := attemptOf(t, h, ref)
		if a.State != "captured" || text(a.Captured) != "1500.00" || a.ReleasePending || text(a.Released) != "1000.00" {
			t.Errorf("the attempt = %s captured %s released %s pending %v, want 1500.00 captured and 1000.00 released",
				a.State, text(a.Captured), text(a.Released), a.ReleasePending)
		}
		if got := vippsPaymentsOf(t, h, inv.ID); len(got) != 1 || !strings.HasPrefix(got[0], "1500.00 ") {
			t.Errorf("the Vipps payments = %v, want 1500.00", got)
		}
	})

	t.Run("a definitive partial capture registered as captured", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv, ref := approvedAttempt(t, h)
		h.vipps.Fail(vippstest.OpCapture, vippstest.Failure{Times: 1, Body: []byte(fmt.Sprintf(`{"reference":%q,"pspReference":"1",
			"state":"AUTHORIZED","amount":{"currency":"NOK","value":250000},"aggregate":{"authorizedAmount":{"currency":"NOK","value":250000},
			"capturedAmount":{"currency":"NOK","value":100000},"cancelledAmount":{"currency":"NOK","value":0},
			"refundedAmount":{"currency":"NOK","value":0}}}`, ref))})
		cycle(t, h)
		a := attemptOf(t, h, ref)
		if a.State != "captured" || text(a.Captured) != "1000.00" || a.ReleasePending || a.Released == nil {
			t.Errorf("the attempt = %s captured %s pending %v released %s, want 1000.00 captured and the rest released",
				a.State, text(a.Captured), a.ReleasePending, text(a.Released))
		}
		if got := vippsPaymentsOf(t, h, inv.ID); len(got) != 1 || !strings.HasPrefix(got[0], "1000.00 ") {
			t.Errorf("the Vipps payments = %v, want 1000.00", got)
		}
	})

	t.Run("6260 a few times, then capture_failed", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv, ref := approvedAttempt(t, h)
		h.vipps.Fail(vippstest.OpCapture, vippstest.Failure{Status: http.StatusBadRequest, Code: vippstest.CodeInsufficientFunds})
		cycle(t, h)
		if a := attemptOf(t, h, ref); a.State != "capturing" || text(a.Reserved) != "2500.00" {
			t.Fatalf("after one 6260 = %s reserved %s, want capturing and retried", a.State, text(a.Reserved))
		}
		for range 3 {
			h.Advance(10 * time.Minute)
			cycle(t, h)
		}
		a := attemptOf(t, h, ref)
		if a.State != "capture_failed" || a.Reserved != nil || a.ReleasePending || a.Released == nil || a.CaptureAttempts != 3 {
			t.Fatalf("after three 6260 = %+v, want capture_failed, released, three captures", a)
		}
		if got := vippsPaymentsOf(t, h, inv.ID); len(got) != 0 {
			t.Errorf("payments %v after a failed capture", got)
		}
		registered(t, h, inv.ID, pay(2500, "2026-09-12"))
	})

	t.Run("6280 retried until the cut-off, then capture_failed", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		_, ref := approvedAttempt(t, h)
		h.vipps.Fail(vippstest.OpCapture, vippstest.Failure{Status: http.StatusBadRequest, Code: vippstest.CodeCaptureFailed})
		cycle(t, h)
		for range 5 {
			h.Advance(2 * 24 * time.Hour)
			cycle(t, h)
		}
		if a := attemptOf(t, h, ref); a.State != "capturing" || a.CaptureAttempts != 6 {
			t.Fatalf("ten days of 6280 = %s, %d captures; want capturing, retried — a 6280 is not an unknown outcome", a.State, a.CaptureAttempts)
		}
		h.Advance(21 * 24 * time.Hour)
		cycle(t, h)
		if a := attemptOf(t, h, ref); a.State != "capture_failed" || a.Reserved != nil {
			t.Errorf("past the 30 days = %s reserved %s, want capture_failed", a.State, text(a.Reserved))
		}
	})

	t.Run("a 5xx for seven days: capture_unknown, then a read resolves it", func(t *testing.T) {
		t.Parallel()
		for _, captured := range []bool{true, false} {
			h := workerReady(t)
			inv, ref := approvedAttempt(t, h)
			h.vipps.Fail(vippstest.OpCapture, vippstest.Failure{Status: http.StatusBadGateway})
			cycle(t, h)
			for range 4 {
				h.Advance(2 * 24 * time.Hour)
				cycle(t, h)
			}
			a := attemptOf(t, h, ref)
			if a.State != "capture_unknown" || text(a.Reserved) != "2500.00" || a.NextPollAt == nil || !a.NextPollAt.Equal(h.Now().Add(time.Hour)) {
				t.Fatalf("eight days of 502 = %s reserved %s next %v, want capture_unknown, the reservation kept, hourly",
					a.State, text(a.Reserved), a.NextPollAt)
			}
			refusedAs(t, "a manual payment while unknown", payer(t, h).Do(http.MethodPost, paymentsPath(inv.ID), pay(2500, "2026-09-12")), "invoice_settled")
			h.vipps.Clear(vippstest.OpCapture)
			if !captured {
				// Vipps' own read ends it: the reservation released in the
				// portal, nothing captured.
				if _, err := portal(t, h).Cancel(context.Background(), ref, "portal-cancel"); err != nil {
					t.Fatalf("cancel in the portal: %v", err)
				}
			}
			h.Advance(time.Hour)
			cycle(t, h)
			a = attemptOf(t, h, ref)
			if captured && (a.State != "captured" || len(vippsPaymentsOf(t, h, inv.ID)) != 1) {
				t.Errorf("the read resolving it captured = %s, payments %v", a.State, vippsPaymentsOf(t, h, inv.ID))
			}
			if !captured && (a.State != "capture_failed" || a.Reserved != nil) {
				t.Errorf("the read resolving it not capturable = %s reserved %s, want capture_failed", a.State, text(a.Reserved))
			}
		}
	})

	t.Run("a release retried until done", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv, ref := approvedAttempt(t, h)
		registered(t, h, inv.ID, pay(1000, "2026-09-12"))
		h.vipps.Fail(vippstest.OpCancel, vippstest.Failure{Status: http.StatusInternalServerError})
		cycle(t, h)
		if a := attemptOf(t, h, ref); a.State != "captured" || !a.ReleasePending || a.NextPollAt == nil {
			t.Fatalf("a failing release = %s pending %v next %v, want captured, the release pending and polled", a.State, a.ReleasePending, a.NextPollAt)
		}
		h.Advance(10 * time.Minute)
		cycle(t, h)
		if a := attemptOf(t, h, ref); !a.ReleasePending || a.Failures != 2 {
			t.Fatalf("the release ended while Vipps refused it, or its failures = %d, want 2 in a row", a.Failures)
		}
		h.vipps.Clear(vippstest.OpCancel)
		h.Advance(20 * time.Minute)
		cycle(t, h)
		if a := attemptOf(t, h, ref); a.ReleasePending || text(a.Released) != "1000.00" || a.Failures != 0 {
			t.Errorf("the release = pending %v released %s failures %d, want done, 1000.00, the failures reset",
				a.ReleasePending, text(a.Released), a.Failures)
		}
	})

	t.Run("nothing open: cancelling, then cancelled", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv, ref := approvedAttempt(t, h)
		registered(t, h, inv.ID, pay(2500, "2026-09-12"))
		cycle(t, h)
		if a := attemptOf(t, h, ref); a.State != "cancelled" || text(a.Released) != "2500.00" || a.FinishedAt == nil {
			t.Errorf("the attempt = %s released %s, want cancelled, 2500.00 released", a.State, text(a.Released))
		}
		if vippsCalls(h, ref, http.MethodPost, "/capture") != 0 {
			t.Error("a capture was sent for an invoice with nothing open")
		}
	})
}

// TestPaymentWorker_CaptureUnknownOnEveryOtherError (I2): a 401, 403, 404,
// 409, 423, 429 or an undecodable body from Capture is no capture failure:
// the attempt stays capturing with its reservation and its key, and after
// seven days is capture_unknown.
func TestPaymentWorker_CaptureUnknownOnEveryOtherError(t *testing.T) {
	t.Parallel()
	for name, f := range map[string]vippstest.Failure{
		"401":                 {Status: http.StatusUnauthorized},
		"403":                 {Status: http.StatusForbidden},
		"404":                 {Status: http.StatusNotFound},
		"409":                 {Status: http.StatusConflict, Code: "4150"},
		"423":                 {Status: http.StatusLocked},
		"429":                 {Status: http.StatusTooManyRequests, RetryAfter: "1"},
		"an undecodable body": {Status: http.StatusOK, Body: []byte("not a payment")},
		"6260 on a 409":       {Status: http.StatusConflict, Code: vippstest.CodeInsufficientFunds},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := workerReady(t)
			_, ref := approvedAttempt(t, h)
			h.vipps.Fail(vippstest.OpCapture, f)
			cycle(t, h)
			first := attemptOf(t, h, ref)
			if first.State != "capturing" || text(first.Reserved) != "2500.00" || first.CaptureKey == nil {
				t.Fatalf("after one = %s reserved %s, want capturing with the reservation", first.State, text(first.Reserved))
			}
			for range 4 {
				h.Advance(2 * 24 * time.Hour)
				cycle(t, h)
			}
			a := attemptOf(t, h, ref)
			if a.State != "capture_unknown" || text(a.Reserved) != "2500.00" || text(a.CaptureKey) != text(first.CaptureKey) {
				t.Errorf("eight days on = %s reserved %s key %s, want capture_unknown, the reservation and key %s kept",
					a.State, text(a.Reserved), text(a.CaptureKey), text(first.CaptureKey))
			}
		})
	}
}

// TestPaymentWorker_RedecidesBeforeFirstCapture (m3): a credit note issued
// between the decision and the first capture lowers the reservation with a
// new key, or cancels when nothing is left; once a capture was sent the
// amount is fixed and the credit leaves a refund due.
func TestPaymentWorker_RedecidesBeforeFirstCapture(t *testing.T) {
	t.Parallel()
	decided := func(t *testing.T, h *harness) (invoiceJSON, string, attemptRow) {
		t.Helper()
		inv, ref := approvedAttempt(t, h)
		step(t, h) // authorized
		step(t, h) // the decision
		a := attemptOf(t, h, ref)
		if a.State != "capturing" || text(a.Reserved) != "2500.00" || a.CaptureAttempts != 0 {
			t.Fatalf("the decision = %+v, want capturing 2500.00, no capture sent", a)
		}
		return inv, ref, a
	}
	t.Run("lowered", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv, ref, before := decided(t, h)
		issued(t, h, creditUnits(t, h, inv.ID, 1).ID) // 1250.00
		cycle(t, h)
		a := attemptOf(t, h, ref)
		if a.State != "captured" || text(a.Captured) != "1250.00" || text(a.CaptureKey) == text(before.CaptureKey) {
			t.Errorf("the attempt = %s captured %s key %s (was %s), want 1250.00 under a new key",
				a.State, text(a.Captured), text(a.CaptureKey), text(before.CaptureKey))
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv, ref, _ := decided(t, h)
		issued(t, h, creditUnits(t, h, inv.ID, 2).ID)
		cycle(t, h)
		if a := attemptOf(t, h, ref); a.State != "cancelled" || vippsCalls(h, ref, http.MethodPost, "/capture") != 0 {
			t.Errorf("the attempt = %s, want cancelled with no capture sent", a.State)
		}
	})
	t.Run("fixed once a capture was sent", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv, ref, _ := decided(t, h)
		h.vippsWire.dropNextCaptures(1)
		step(t, h)
		sent := attemptOf(t, h, ref)
		issued(t, h, creditUnits(t, h, inv.ID, 1).ID)
		h.Advance(10 * time.Minute)
		cycle(t, h)
		a := attemptOf(t, h, ref)
		if a.State != "captured" || text(a.Captured) != "2500.00" || text(a.CaptureKey) != text(sent.CaptureKey) {
			t.Errorf("the attempt = %s captured %s, want the 2500.00 sent, its key kept", a.State, text(a.Captured))
		}
		if got := getInvoice(t, h, inv.ID); money(got.OpenAmount) != -1250.0 {
			t.Errorf("the invoice's open = %v, want -1250: a refund due", money(got.OpenAmount))
		}
	})
}

// TestPaymentWorker_DrainsEveryDueRow (m5): twenty due attempts are each
// read in one cycle, with no pacing between them.
func TestPaymentWorker_DrainsEveryDueRow(t *testing.T) {
	t.Parallel()
	h := workerReady(t)
	inv := issuedAcme(t, h)
	for range 20 {
		h.Exec(t, `INSERT INTO invoices.payment_attempts (invoice_id, provider, msn, flow, pay_link_id, return_nonce_sha256,
			amount, cancel_key, state, created_at, expires_at, next_poll_at)
			SELECT $1, 'vipps', '123456', 'pay_link', id, repeat('0', 64), 100, gen_random_uuid(), 'created', $2::timestamptz,
			    $2::timestamptz + interval '10 minutes', $2::timestamptz
			FROM invoices.pay_links WHERE invoice_id = $1 AND revoked_at IS NULL`, inv.ID, h.Now())
	}
	started := time.Now()
	cycle(t, h)
	if took := time.Since(started); took > 10*time.Second {
		t.Errorf("the cycle took %s", took)
	}
	if n := h.Count(t, `SELECT count(*) FROM invoices.payment_attempts WHERE invoice_id = $1 AND polls = 1 AND next_poll_at > $2`, inv.ID, h.Now()); n != 20 {
		t.Errorf("%d of 20 attempts read in the cycle", n)
	}
}

// TestPaymentWorker_PaidOnIsTheCaptureEvent (m10): a capture whose answer
// was lost and whose outcome stayed unknown for a month is dated by Vipps'
// capture event, not by the day it is registered — and by the claim's day
// when the event log cannot be read.
func TestPaymentWorker_PaidOnIsTheCaptureEvent(t *testing.T) {
	t.Parallel()
	for _, logRead := range []bool{true, false} {
		h := workerReady(t)
		inv, ref := approvedAttempt(t, h)
		h.vippsWire.dropNextCaptures(1)
		cycle(t, h)
		h.vipps.Fail(vippstest.OpGet, vippstest.Failure{Status: http.StatusInternalServerError})
		h.vipps.Fail(vippstest.OpCapture, vippstest.Failure{Status: http.StatusInternalServerError})
		h.Advance(8 * 24 * time.Hour)
		cycle(t, h)
		if a := attemptOf(t, h, ref); a.State != "capture_unknown" {
			t.Fatalf("eight days on = %s, want capture_unknown", a.State)
		}
		h.Advance(22 * 24 * time.Hour)
		h.vipps.Clear(vippstest.OpGet)
		h.vipps.Clear(vippstest.OpCapture)
		if !logRead {
			h.vipps.Fail(vippstest.OpEvents, vippstest.Failure{Status: http.StatusInternalServerError})
		}
		cycle(t, h)
		want := "2500.00 2026-09-12 " + ref
		if !logRead {
			want = "2500.00 " + h.Now().Format(time.DateOnly) + " " + ref
		}
		if got := vippsPaymentsOf(t, h, inv.ID); !slices.Equal(got, []string{want}) {
			t.Errorf("the event log read %v: payments %v, want %s", logRead, got, want)
		}
	}
}

// TestPaymentWorker_ExactlyOnce: two workers whose leases run out at once
// both claim each attempt; each capture is still registered once, and the
// attempt's id refuses a second payment.
func TestPaymentWorker_ExactlyOnce(t *testing.T) {
	h := workerReady(t)
	defer invoices.SetPaymentWorkerLease(0)()
	var invs []invoiceJSON
	for range 3 {
		inv, _ := approvedAttempt(t, h)
		invs = append(invs, inv)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			w := invoices.NewPaymentWorker(h.Deps())
			for range 20 {
				if err := w.RunCycle(context.Background()); err != nil {
					t.Errorf("a cycle: %v", err)
				}
			}
		})
	}
	wg.Wait()
	for _, inv := range invs {
		if got := vippsPaymentsOf(t, h, inv.ID); len(got) != 1 {
			t.Errorf("invoice %d's Vipps payments = %v, want one", inv.ID, got)
		}
	}
	id := modtest.One[int64](t, h.Harness, `SELECT payment_attempt_id FROM invoices.payments WHERE invoice_id = $1 AND source = 'vipps'`, invs[0].ID)
	_, err := h.Pool().Exec(context.Background(), `INSERT INTO invoices.payments (invoice_id, paid_on, amount, currency, source,
		payment_attempt_id, registered_at) VALUES ($1, '2026-09-12', 1, 'NOK', 'vipps', $2, now())`, invs[0].ID, id)
	if err == nil || !strings.Contains(err.Error(), "23505") {
		t.Errorf("a second payment of attempt %d = %v, want a unique violation", id, err)
	}
}

// TestPaymentWorker_ForeignMSNLeftAlone (I20): an attempt made under a sales
// unit that is no longer the stored one is never sent to the stored one:
// it is left as it is and looked at again in an hour.
func TestPaymentWorker_ForeignMSNLeftAlone(t *testing.T) {
	t.Parallel()
	h := workerReady(t)
	inv := issuedAcme(t, h)
	ref := plantReservation(t, h, inv.ID, "authorized", "2500")
	h.Exec(t, `ALTER TABLE invoices.payment_attempts DISABLE TRIGGER tr_payment_attempts_immutable`)
	h.Exec(t, `UPDATE invoices.payment_attempts SET msn = '999999' WHERE reference = $1`, ref)
	h.Exec(t, `ALTER TABLE invoices.payment_attempts ENABLE TRIGGER tr_payment_attempts_immutable`)
	before := len(h.vipps.Requests())
	cycle(t, h)
	a := attemptOf(t, h, ref)
	if a.State != "authorized" || a.NextPollAt == nil || !a.NextPollAt.Equal(h.Now().Add(time.Hour)) {
		t.Errorf("the foreign attempt = %s next %v, want authorized, an hour on", a.State, a.NextPollAt)
	}
	if n := len(h.vipps.Requests()) - before; n != 0 {
		t.Errorf("%d requests sent for a foreign sales unit's attempt", n)
	}
}

// TestPaymentWorker_KeysPersistedBeforeEachCall (the review's decision 3):
// every Capture and Cancel carries the key the attempt already stores, and
// every Capture is counted in a commit before it — so a lowered
// reservation's new key is stored before its capture, and the key can never
// change once one is sent.
func TestPaymentWorker_KeysPersistedBeforeEachCall(t *testing.T) {
	t.Parallel()
	h := workerReady(t)
	inv, ref := approvedAttempt(t, h)
	step(t, h)
	step(t, h)
	issued(t, h, creditUnits(t, h, inv.ID, 1).ID)
	conn := ownConn(t, h)
	var mu sync.Mutex
	var seen []string
	captures := 0
	h.vippsWire.observeRequests(func(r *http.Request) {
		path := r.URL.Path
		if r.Method != http.MethodPost || !strings.Contains(path, ref) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		var captureKey, cancelKey string
		var attempts int
		if err := conn.QueryRow(context.Background(), `SELECT coalesce(capture_key::text, ''), cancel_key::text, capture_attempts
			FROM invoices.payment_attempts WHERE reference = $1`, ref).Scan(&captureKey, &cancelKey, &attempts); err != nil {
			t.Errorf("read the attempt: %v", err)
			return
		}
		key := r.Header.Get("Idempotency-Key")
		switch {
		case strings.HasSuffix(path, "/capture"):
			captures++
			if key != captureKey || attempts < captures {
				t.Errorf("capture %d carried %s with %s stored and %d counted", captures, key, captureKey, attempts)
			}
		case strings.HasSuffix(path, "/cancel"):
			if key != cancelKey {
				t.Errorf("a cancel carried %s with %s stored", key, cancelKey)
			}
		}
		seen = append(seen, path[strings.LastIndex(path, "/")+1:])
	})
	h.vippsWire.dropNextCaptures(1)
	cycle(t, h)
	h.Advance(time.Minute)
	h.vipps.Fail(vippstest.OpGet, vippstest.Failure{Status: http.StatusInternalServerError, Times: 1})
	cycle(t, h)
	h.Advance(10 * time.Minute)
	cycle(t, h)
	mu.Lock()
	defer mu.Unlock()
	if captures < 1 || !slices.Contains(seen, "cancel") {
		t.Errorf("the calls = %v, want at least a capture and the release", seen)
	}
	if a := attemptOf(t, h, ref); a.State != "captured" || text(a.Captured) != "1250.00" {
		t.Errorf("the attempt = %s captured %s, want 1250.00", a.State, text(a.Captured))
	}
}

// TestPaymentWorker_LockOrder (D18): the decision, the pre-capture
// transaction and the registration take the attempt, then its invoice;
// every other write the attempt alone; nothing else.
func TestPaymentWorker_LockOrder(t *testing.T) {
	h := workerReady(t)
	inv, ref := approvedAttempt(t, h)
	registered(t, h, inv.ID, pay(1000, "2026-09-12")) // a release, too
	seen := &lockSeen{}
	defer invoices.SetLockTaken(seen.note)()
	cycle(t, h)
	a := attemptOf(t, h, ref)
	att, invoice := fmt.Sprintf("payment_attempt %d", a.ID), fmt.Sprintf("invoice %d", inv.ID)
	want := []string{att, att, invoice, att, invoice, att, invoice, att}
	if got := seen.take(); !slices.Equal(got, want) {
		t.Errorf("the locks = %v, want %v", got, want)
	}
}

// TestPaymentWorker_NoCallUnderALock (MB rule 10): every provider call of a
// whole capture is made outside the worker's transactions.
func TestPaymentWorker_NoCallUnderALock(t *testing.T) {
	h := workerReady(t)
	inv, _ := approvedAttempt(t, h)
	registered(t, h, inv.ID, pay(1000, "2026-09-12"))
	before := lockedContractCalls.count()
	all := len(contractCalls.byMethod("payments.Get", "payments.Capture", "payments.Cancel", "payments.CaptureTime"))
	cycle(t, h)
	if calls := lockedContractCalls.since(before); len(calls) > 0 {
		t.Errorf("calls made inside a transaction:\n%s", strings.Join(calls, "\n"))
	}
	var methods []string
	for _, c := range contractCalls.byMethod("payments.Get", "payments.Capture", "payments.Cancel", "payments.CaptureTime")[all:] {
		methods = append(methods, c.method)
		if c.locked {
			t.Errorf("%s was made under a lock", c.method)
		}
	}
	for _, m := range []string{"payments.Get", "payments.Capture", "payments.CaptureTime", "payments.Cancel"} {
		if !slices.Contains(methods, m) {
			t.Errorf("the worker's calls = %v, want %s among them", methods, m)
		}
	}
}

// TestPaymentWorker_CreateNeverExpiresLocally (the review's decision 5): an
// attempt whose create's outcome was never learned is read back; Vipps'
// 404 past its ten minutes is no proof — the create is sent again under the
// stored key, and only Vipps' own end finishes it.
func TestPaymentWorker_CreateNeverExpiresLocally(t *testing.T) {
	t.Parallel()
	h := workerReady(t)
	inv := issuedAcme(t, h)
	h.vipps.Fail(vippstest.OpCreate, vippstest.Failure{Status: http.StatusBadGateway, Times: 1})
	res := startPay(h.Client(t), payToken(t, h, inv.ID))
	if res.Status != http.StatusBadGateway {
		t.Fatalf("the create = %d %s, want 502", res.Status, res.Body)
	}
	ref := modtest.One[string](t, h.Harness, `SELECT reference FROM invoices.payment_attempts WHERE invoice_id = $1`, inv.ID)
	h.Advance(5 * time.Second)
	cycle(t, h)
	if a := attemptOf(t, h, ref); a.State != "creating" {
		t.Fatalf("a 404 before its ten minutes = %s, want creating", a.State)
	}
	h.Advance(11 * time.Minute)
	cycle(t, h)
	a := attemptOf(t, h, ref)
	if a.State != "created" || a.NextPollAt == nil {
		t.Fatalf("past ten minutes = %s, want created again under the stored key, still polled", a.State)
	}
	if n := len(vippsCreates(t, h)); n != 2 {
		t.Errorf("%d creates sent, want the first and one more", n)
	}
	h.vipps.Script(ref, vippstest.Expired)
	h.Advance(time.Hour)
	cycle(t, h)
	if a := attemptOf(t, h, ref); a.State != "expired" {
		t.Errorf("Vipps' end = %s, want expired", a.State)
	}
}

// TestPaymentWorker_ReleaseNeverPossibleIsParked (the review's decision 6):
// a release Vipps refuses for good while its read still shows an amount
// unreleased is parked — never polled again, still live to the credentials
// — and a refusal its read explains (nothing left) ends the release with
// the amount it reports.
func TestPaymentWorker_ReleaseNeverPossibleIsParked(t *testing.T) {
	t.Parallel()
	h := workerReady(t)
	inv, ref := approvedAttempt(t, h)
	registered(t, h, inv.ID, pay(1000, "2026-09-12"))
	h.vipps.Fail(vippstest.OpCancel, vippstest.Failure{Status: http.StatusBadRequest, Code: "6000"})
	cycle(t, h)
	a := attemptOf(t, h, ref)
	if a.State != "captured" || !a.ReleasePending || a.NextPollAt != nil || a.LastError == nil {
		t.Errorf("the refused release = %s pending %v next %v, want captured, pending, parked with a reason", a.State, a.ReleasePending, a.NextPollAt)
	}
	res := vippsManager(t, h).Do(http.MethodDelete, vippsSettingsPath, nil)
	refusedAs(t, "removing the credentials", res, "payment_attempts_active")
}

// portal is a Vipps client for the test's sales unit, beside the module's:
// what a person does in the Vipps business portal.
func portal(t *testing.T, h *harness) *vipps.Client {
	t.Helper()
	c, err := vipps.New(vipps.Options{
		BaseURL: h.vipps.URL(), ClientID: vippstest.ClientID, ClientSecret: vippstest.ClientSecret,
		SubscriptionKey: vippstest.SubscriptionKey, MSN: vippstest.MSN, Transport: h.vipps.Transport(), Now: h.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestPaymentWorker_CapturedMoneyIsAlwaysRegistered (the review's HIGH):
// money Vipps shows captured on an attempt not yet captured is registered —
// on a capture_failed attempt whose capture landed after all, and on a
// cancelling attempt captured in the portal — never closed as a release.
func TestPaymentWorker_CapturedMoneyIsAlwaysRegistered(t *testing.T) {
	t.Parallel()
	t.Run("after capture_failed", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv, ref := approvedAttempt(t, h)
		h.vipps.Fail(vippstest.OpCapture, vippstest.Failure{Status: http.StatusBadRequest, Code: vippstest.CodeInsufficientFunds})
		h.vipps.Fail(vippstest.OpCancel, vippstest.Failure{Status: http.StatusInternalServerError})
		for range 4 {
			cycle(t, h)
			h.Advance(10 * time.Minute)
		}
		if a := attemptOf(t, h, ref); a.State != "capture_failed" || !a.ReleasePending {
			t.Fatalf("after three 6260 = %s pending %v, want capture_failed, its release pending", a.State, a.ReleasePending)
		}
		h.vipps.Clear(vippstest.OpCapture)
		if _, err := portal(t, h).Capture(context.Background(), ref, payments.NOK(250000), "late-capture"); err != nil {
			t.Fatalf("the late capture: %v", err)
		}
		h.vipps.Clear(vippstest.OpCancel)
		cycle(t, h)
		a := attemptOf(t, h, ref)
		if a.State != "captured" || text(a.Captured) != "2500.00" || a.ReleasePending {
			t.Errorf("the attempt = %s captured %s pending %v, want captured 2500.00", a.State, text(a.Captured), a.ReleasePending)
		}
		if got := vippsPaymentsOf(t, h, inv.ID); len(got) != 1 || !strings.HasPrefix(got[0], "2500.00 ") {
			t.Errorf("the Vipps payments = %v, want one of 2500.00", got)
		}
	})
	t.Run("a portal capture on a cancelling attempt", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv, ref := approvedAttempt(t, h)
		registered(t, h, inv.ID, pay(2500, "2026-09-12"))
		h.vipps.Fail(vippstest.OpCancel, vippstest.Failure{Status: http.StatusInternalServerError})
		cycle(t, h)
		if a := attemptOf(t, h, ref); a.State != "cancelling" {
			t.Fatalf("the attempt = %s, want cancelling", a.State)
		}
		if _, err := portal(t, h).Capture(context.Background(), ref, payments.NOK(100000), "portal-capture"); err != nil {
			t.Fatalf("the portal capture: %v", err)
		}
		h.vipps.Clear(vippstest.OpCancel)
		h.Advance(10 * time.Minute)
		cycle(t, h)
		a := attemptOf(t, h, ref)
		if a.State != "captured" || text(a.Captured) != "1000.00" || a.ReleasePending || text(a.Released) != "1500.00" {
			t.Errorf("the attempt = %s captured %s pending %v released %s, want captured 1000.00, the rest 1500.00 released",
				a.State, text(a.Captured), a.ReleasePending, text(a.Released))
		}
		if got := vippsPaymentsOf(t, h, inv.ID); len(got) != 1 || !strings.HasPrefix(got[0], "1000.00 ") {
			t.Errorf("the Vipps payments = %v, want one of 1000.00", got)
		}
	})
}

// TestPaymentWorker_GuaranteeAloneEndsNothing (the review's HIGH): the
// capture guarantee passing on our clock is no answer from Vipps. With a
// capture sent and a read of AUTHORIZED, nothing captured, the attempt stays
// capture_unknown with its reservation; with none sent, the capture is sent.
func TestPaymentWorker_GuaranteeAloneEndsNothing(t *testing.T) {
	t.Parallel()
	t.Run("a capture sent", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		_, ref := approvedAttempt(t, h)
		h.vipps.Fail(vippstest.OpCapture, vippstest.Failure{Status: http.StatusBadGateway})
		cycle(t, h)
		h.Advance(8 * 24 * time.Hour)
		cycle(t, h)
		h.vipps.GuaranteeCapture(ref, h.Now().Add(-time.Hour))
		h.Advance(time.Hour)
		cycle(t, h)
		if a := attemptOf(t, h, ref); a.State != "capture_unknown" || text(a.Reserved) != "2500.00" {
			t.Errorf("past the guarantee = %s reserved %s, want capture_unknown, the reservation kept", a.State, text(a.Reserved))
		}
	})
	t.Run("none sent", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv, ref := approvedAttempt(t, h)
		h.vipps.GuaranteeCapture(ref, h.Now().Add(-time.Hour))
		cycle(t, h)
		if a := attemptOf(t, h, ref); a.State != "captured" || len(vippsPaymentsOf(t, h, inv.ID)) != 1 {
			t.Errorf("past the guarantee, nothing sent = %s, payments %v; want the capture sent and registered", a.State, vippsPaymentsOf(t, h, inv.ID))
		}
	})
}

// TestPaymentWorker_RegistrationFailureRecorded (the review's MEDIUM): a
// registration that fails keeps the attempt and its reservation, records
// why in last_error and backs off; once it can, it registers.
func TestPaymentWorker_RegistrationFailureRecorded(t *testing.T) {
	t.Parallel()
	h := workerReady(t)
	inv, ref := approvedAttempt(t, h)
	h.Exec(t, `CREATE FUNCTION invoices.test_refuse_vipps() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'test: registration refused'; END $$`)
	h.Exec(t, `CREATE TRIGGER tr_test_refuse_vipps BEFORE INSERT ON invoices.payments FOR EACH ROW
		WHEN (NEW.source = 'vipps') EXECUTE FUNCTION invoices.test_refuse_vipps()`)
	if err := invoices.NewPaymentWorker(h.Deps()).RunCycle(context.Background()); err == nil {
		t.Error("the cycle with a failing registration answered no error")
	}
	a := attemptOf(t, h, ref)
	if a.State != "capturing" || text(a.Reserved) != "2500.00" || !strings.Contains(text(a.LastError), "registration refused") ||
		a.NextPollAt == nil || !a.NextPollAt.After(h.Now()) || a.Failures != 1 {
		t.Fatalf("after a failed registration = %s reserved %s error %s next %v failures %d; want capturing, kept, recorded, backed off",
			a.State, text(a.Reserved), text(a.LastError), a.NextPollAt, a.Failures)
	}
	h.Exec(t, `DROP TRIGGER tr_test_refuse_vipps ON invoices.payments`)
	h.Advance(10 * time.Minute)
	cycle(t, h)
	if got := vippsPaymentsOf(t, h, inv.ID); len(got) != 1 || attemptOf(t, h, ref).State != "captured" {
		t.Errorf("once it can: payments %v, attempt %s; want it registered", got, attemptOf(t, h, ref).State)
	}
}

// TestPaymentWorker_InsufficientFundsCountsOnlyItsAnswers (the review's
// LOW): two unknown outcomes and then one 6260 leave the capture tried
// again; three 6260 answers end it.
func TestPaymentWorker_InsufficientFundsCountsOnlyItsAnswers(t *testing.T) {
	t.Parallel()
	h := workerReady(t)
	_, ref := approvedAttempt(t, h)
	h.vipps.Fail(vippstest.OpCapture, vippstest.Failure{Status: http.StatusBadGateway, Times: 2})
	cycle(t, h)
	h.Advance(10 * time.Minute)
	cycle(t, h)
	h.vipps.Fail(vippstest.OpCapture, vippstest.Failure{Status: http.StatusBadRequest, Code: vippstest.CodeInsufficientFunds})
	h.Advance(10 * time.Minute)
	cycle(t, h)
	if a := attemptOf(t, h, ref); a.State != "capturing" || a.InsufficientFunds != 1 || a.CaptureAttempts != 3 {
		t.Fatalf("two unknowns and a 6260 = %s, %d 6260, %d captures; want capturing, one 6260 of three captures",
			a.State, a.InsufficientFunds, a.CaptureAttempts)
	}
	for range 2 {
		h.Advance(10 * time.Minute)
		cycle(t, h)
	}
	if a := attemptOf(t, h, ref); a.State != "capture_failed" || a.InsufficientFunds != 3 {
		t.Errorf("three 6260 = %s, %d; want capture_failed", a.State, a.InsufficientFunds)
	}
}

// TestPaymentWorker_RecreateParkedAfterTenTries (the review's LOW): a
// create sent again that keeps conflicting is parked after ten tries, with
// why.
func TestPaymentWorker_RecreateParkedAfterTenTries(t *testing.T) {
	t.Parallel()
	h := workerReady(t)
	inv := issuedAcme(t, h)
	h.vipps.Fail(vippstest.OpCreate, vippstest.Failure{Status: http.StatusBadGateway, Times: 1})
	startPay(h.Client(t), payToken(t, h, inv.ID))
	ref := modtest.One[string](t, h.Harness, `SELECT reference FROM invoices.payment_attempts WHERE invoice_id = $1`, inv.ID)
	h.vipps.Fail(vippstest.OpCreate, vippstest.Failure{Status: http.StatusConflict, Code: "4010"})
	h.Advance(11 * time.Minute)
	for range 12 {
		cycle(t, h)
		h.Advance(11 * time.Minute)
	}
	a := attemptOf(t, h, ref)
	if a.State != "creating" || a.NextPollAt != nil || a.LastError == nil {
		t.Errorf("ten conflicting creates = %s next %v error %s, want creating, parked with why", a.State, a.NextPollAt, text(a.LastError))
	}
	if n := len(vippsCreates(t, h)); n != 11 {
		t.Errorf("%d creates sent, want the first and ten more", n)
	}
}

// TestPaymentWorker_EraseBeforeRegistration (the review): a customer erased
// between the authorization and the registration still gets the payment —
// one vipps payment, its note blank.
func TestPaymentWorker_EraseBeforeRegistration(t *testing.T) {
	t.Parallel()
	h := workerReady(t)
	inv, ref := approvedAttempt(t, h)
	step(t, h)
	step(t, h)
	eraseOf(t, h, h.Deps(), customerAcme)
	cycle(t, h)
	if a := attemptOf(t, h, ref); a.State != "captured" {
		t.Errorf("the attempt = %s, want captured", a.State)
	}
	if got := texts(t, h, `SELECT concat_ws(' ', amount, source, '[' || note || ']') FROM invoices.payments WHERE invoice_id = $1`, inv.ID); !slices.Equal(got, []string{"2500.00 vipps []"}) {
		t.Errorf("the payments = %v, want one vipps payment with a blank note", got)
	}
}
