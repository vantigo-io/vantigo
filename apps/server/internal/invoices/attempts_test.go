package invoices_test

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/payments"
	"github.com/vantigo-io/vantigo/server/internal/payments/vipps/vippstest"
)

// TestAttempts_OnTheDocument: an issued invoice carries its attempts —
// reference, flow, state, amount and times — and never the address Vipps
// sends the payer to; every issued invoice carries the list, empty when it
// has none.
func TestAttempts_OnTheDocument(t *testing.T) {
	t.Parallel()
	h, _ := payReady(t)
	inv := issuedAcme(t, h)
	reader := h.SignIn(t, "invoices:access")
	res := reader.Do(http.MethodGet, invoicePath(inv.ID), nil)
	var before struct {
		PaymentAttempts []any `json:"paymentAttempts"`
	}
	res.JSON(&before)
	if before.PaymentAttempts == nil || len(before.PaymentAttempts) != 0 {
		t.Errorf("paymentAttempts before any = %v, want []", before.PaymentAttempts)
	}
	created := startedPay(t, h.Client(t), payToken(t, h, inv.ID))

	res = reader.Do(http.MethodGet, invoicePath(inv.ID), nil)
	var doc struct {
		PaymentAttempts []struct {
			ID        int64   `json:"id"`
			Reference string  `json:"reference"`
			Flow      string  `json:"flow"`
			State     string  `json:"state"`
			Amount    float64 `json:"amount"`
			CreatedBy *string `json:"createdByUserId"`
			ExpiresAt string  `json:"expiresAt"`
		} `json:"paymentAttempts"`
	}
	res.JSON(&doc)
	if len(doc.PaymentAttempts) != 1 {
		t.Fatalf("paymentAttempts = %+v, want one", doc.PaymentAttempts)
	}
	a := doc.PaymentAttempts[0]
	if a.Reference != created.Reference || a.Flow != "pay_link" || a.State != "created" || a.Amount != 2500 || a.CreatedBy != nil || a.ExpiresAt == "" {
		t.Errorf("the attempt = %+v", a)
	}
	body := string(res.Body)
	if strings.Contains(body, created.RedirectURL) || strings.Contains(strings.ToLower(body), "redirect") {
		t.Errorf("the document carries the payer's address: %s", body)
	}
	stored := modtest.One[string](t, h.Harness, `SELECT redirect_url FROM invoices.payment_attempts WHERE id = $1`, a.ID)
	if stored != created.RedirectURL {
		t.Errorf("redirect_url stored %q, want %q", stored, created.RedirectURL)
	}
}

// TestAttempts_LockOrder: the create locks the invoice, then shares the
// credentials (reading 8); the answer is recorded under the attempt's own
// lock, in a transaction of its own (D18's 4C rows). Not parallel: the lock
// seam is the package's.
func TestAttempts_LockOrder(t *testing.T) {
	h, _ := payReady(t)
	inv := issuedAcme(t, h)
	token := payToken(t, h, inv.ID)
	seen := &lockSeen{}
	defer invoices.SetLockTaken(seen.note)()

	created := startedPay(t, h.Client(t), token)
	id := modtest.One[int64](t, h.Harness, `SELECT id FROM invoices.payment_attempts WHERE reference = $1`, created.Reference)
	want := []string{"invoice " + strconv.FormatInt(inv.ID, 10), "payment_credentials 1", "payment_attempt " + strconv.FormatInt(id, 10)}
	if got := seen.take(); !slices.Equal(got, want) {
		t.Errorf("the create's locks = %v, want %v", got, want)
	}
}

// TestAttempts_NoCallUnderALock: the create's provider call is made after
// its transaction committed, and the credentials are read on the pool before
// it — never under a lock (the harness fails the test on one, and the call
// is recorded as made outside). Not parallel: an anonymous call carries no
// user, so another test's would be counted as this one's.
func TestAttempts_NoCallUnderALock(t *testing.T) {
	h, _ := payReady(t)
	startedPay(t, h.Client(t), payToken(t, h, issuedAcme(t, h).ID))
	found := false
	for _, call := range contractCalls.by(uuid.Nil) {
		if call.method == "payments.Create" {
			found = true
			if call.locked {
				t.Error("payments.Create was called under a lock")
			}
		}
	}
	if !found {
		t.Error("no payments.Create was recorded")
	}
}

// TestAttempts_MinorOf: amounts cross the port as whole øre only.
func TestAttempts_MinorOf(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]int64{"2500": 250000, "12.34": 1234, "0.01": 1} {
		v, _ := new(big.Rat).SetString(text)
		if got, err := invoices.MinorOf(v); err != nil || got != want {
			t.Errorf("minorOf(%s) = %d, %v; want %d", text, got, err, want)
		}
		if back := invoices.RatOfMinor(want); back.Cmp(v) != 0 {
			t.Errorf("ratOfMinor(%d) = %s, want %s", want, back.FloatString(2), text)
		}
	}
	if _, err := invoices.MinorOf(big.NewRat(1005, 1000)); err == nil {
		t.Error("minorOf(1.005) answered, want a refusal: not a whole øre")
	}
}

// Abandoning a stuck attempt (invoices payments and reminders design D16,
// B5, I20): invoices:manage, a reason and the acknowledgement that nothing
// was captured. A read of Vipps decides it wherever Vipps can be read —
// money captured is registered, a payment still capturable is refused — and
// only a capture never sent is abandoned unread, while Vipps cannot be.

// abandonPath is the abandon of attempt id.
func abandonPath(id int64) string {
	return fmt.Sprintf("%s/payment-attempts/%d/abandon", invoicesPath, id)
}

// abandonBody is an abandon's body: reason, and confirmNotCaptured when
// confirm is not nil.
func abandonBody(reason string, confirm any) map[string]any {
	body := map[string]any{"reason": reason}
	if confirm != nil {
		body["confirmNotCaptured"] = confirm
	}
	return body
}

// abandonJSON is an abandoned attempt as a client reads it.
type abandonJSON struct {
	ID                int64    `json:"id"`
	State             string   `json:"state"`
	ReservedAmount    *float64 `json:"reservedAmount"`
	AbandonedAt       *string  `json:"abandonedAt"`
	AbandonedByUserID *string  `json:"abandonedByUserId"`
	AbandonReason     *string  `json:"abandonReason"`
}

// abandoned abandons attempt id as a manager, failing unless 200.
func abandoned(t *testing.T, h *harness, id int64) abandonJSON {
	t.Helper()
	res := vippsManager(t, h).Do(http.MethodPost, abandonPath(id), abandonBody("Sjekket i Vipps-portalen", true))
	if res.Status != http.StatusOK {
		t.Fatalf("POST %s = %d %s, want 200", abandonPath(id), res.Status, res.Body)
	}
	var a abandonJSON
	res.JSON(&a)
	return a
}

// unknownAttempt is an approved attempt whose capture went unanswered for
// eight days: capture_unknown, 2500.00 reserved, still capturable at Vipps.
func unknownAttempt(t *testing.T, h *harness) (invoiceJSON, string) {
	t.Helper()
	inv, ref := approvedAttempt(t, h)
	h.vipps.Fail(vippstest.OpCapture, vippstest.Failure{Status: http.StatusBadGateway})
	cycle(t, h)
	for range 4 {
		h.Advance(2 * 24 * time.Hour)
		cycle(t, h)
	}
	h.vipps.Clear(vippstest.OpCapture)
	if a := attemptOf(t, h, ref); a.State != "capture_unknown" || text(a.Reserved) != "2500.00" {
		t.Fatalf("the attempt = %s reserved %s, want capture_unknown with 2500.00", a.State, text(a.Reserved))
	}
	return inv, ref
}

// setAttempt updates attempt reference's columns past its trigger, as a
// fixture: set is the SET list, $1 the reference.
func setAttempt(t *testing.T, h *harness, reference, set string) {
	t.Helper()
	h.Exec(t, `ALTER TABLE invoices.payment_attempts DISABLE TRIGGER tr_payment_attempts_immutable`)
	h.Exec(t, `UPDATE invoices.payment_attempts SET `+set+` WHERE reference = $1`, reference)
	h.Exec(t, `ALTER TABLE invoices.payment_attempts ENABLE TRIGGER tr_payment_attempts_immutable`)
}

// plantParkedCreate writes an on-site attempt of invoice id whose create
// was never answered and was given up: creating, never polled again.
func plantParkedCreate(t *testing.T, h *harness, id int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `
		INSERT INTO invoices.payment_attempts (invoice_id, provider, msn, flow, amount, create_key, cancel_key, state,
		    created_at, expires_at, failures, last_error)
		VALUES ($1, 'vipps', '123456', 'on_site', 2500, gen_random_uuid(), gen_random_uuid(), 'creating',
		    $2::timestamptz, $2::timestamptz + interval '10 minutes', 10, 'The create was sent again 10 times.')
		RETURNING reference`, id, h.Now())
}

// TestAbandon_Refusals: 400 without the acknowledgement or a reason of 1 to
// 500 characters; 404 for no attempt; 403 without invoices:manage; 409 for
// every state but capture_unknown, a stranded capture and a parked create —
// a capture the worker is carrying, one Vipps still holds capturable, one
// sent while Vipps cannot be read, a create Vipps knows — each leaving the
// attempt as it was, its lease free; a read Vipps fails is a 502.
func TestAbandon_Refusals(t *testing.T) {
	t.Parallel()
	h := workerReady(t)
	_, ref := unknownAttempt(t, h)
	a := attemptOf(t, h, ref)
	m := vippsManager(t, h)

	for name, body := range map[string]map[string]any{
		"no acknowledgement":    abandonBody("Sjekket", nil),
		"acknowledgement false": abandonBody("Sjekket", false),
		"an empty reason":       abandonBody("   ", true),
		"a reason of 501":       abandonBody(strings.Repeat("x", 501), true),
	} {
		if res := m.Do(http.MethodPost, abandonPath(a.ID), body); res.Status != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, res.Status, res.Body)
		}
	}
	if res := m.Do(http.MethodPost, abandonPath(a.ID), abandonBody(strings.Repeat("ø", 500), false)); res.Status != http.StatusBadRequest {
		t.Errorf("500 characters unacknowledged = %d, want 400", res.Status)
	}
	if res := m.Do(http.MethodPost, abandonPath(999999), abandonBody("Sjekket", true)); res.Status != http.StatusNotFound {
		t.Errorf("no attempt = %d %s, want 404", res.Status, res.Body)
	}
	if res := payer(t, h).Do(http.MethodPost, abandonPath(a.ID), abandonBody("Sjekket", true)); res.Status != http.StatusForbidden {
		t.Errorf("invoices:payments alone = %d, want 403", res.Status)
	}

	// Still capturable at Vipps: refused, the lease handed back.
	refusedAs(t, "a capture Vipps still holds", m.Do(http.MethodPost, abandonPath(a.ID), abandonBody("Sjekket", true)), "attempt_not_abandonable")
	if got := modtest.One[bool](t, h.Harness, `SELECT lease_id IS NULL FROM invoices.payment_attempts WHERE id = $1`, a.ID); !got {
		t.Error("the refused abandon kept its lease")
	}
	// A read Vipps fails: 502, nothing moved.
	h.vipps.Fail(vippstest.OpGet, vippstest.Failure{Status: http.StatusInternalServerError})
	if res := m.Do(http.MethodPost, abandonPath(a.ID), abandonBody("Sjekket", true)); res.Status != http.StatusBadGateway {
		t.Errorf("a failing read = %d %s, want 502", res.Status, res.Body)
	}
	h.vipps.Clear(vippstest.OpGet)
	if got := attemptOf(t, h, ref); got.State != "capture_unknown" || text(got.Reserved) != "2500.00" {
		t.Errorf("after the refusals = %s reserved %s, want capture_unknown, kept", got.State, text(got.Reserved))
	}

	// Every other state, and a capturing attempt the worker can carry.
	inv := issuedAcme(t, h)
	for _, state := range []string{"authorized", "capturing"} {
		r := plantReservation(t, h, inv.ID, state, "100")
		refusedAs(t, state, m.Do(http.MethodPost, abandonPath(attemptOf(t, h, r).ID), abandonBody("Sjekket", true)), "attempt_not_abandonable")
		setAttempt(t, h, r, "state = 'cancelled', reserved_amount = NULL, finished_at = now()")
	}
	_, doneRef := approvedAttempt(t, h)
	cycle(t, h)
	if attemptOf(t, h, doneRef).State != "captured" {
		t.Fatal("the attempt was not captured")
	}
	refusedAs(t, "captured", m.Do(http.MethodPost, abandonPath(attemptOf(t, h, doneRef).ID), abandonBody("Sjekket", true)), "attempt_not_abandonable")
	// A create still polled is not parked.
	polled := plantParkedCreate(t, h, issuedAcme(t, h).ID)
	setAttempt(t, h, polled, "next_poll_at = now()")
	refusedAs(t, "a create still polled", m.Do(http.MethodPost, abandonPath(attemptOf(t, h, polled).ID), abandonBody("Sjekket", true)), "attempt_not_abandonable")

	// A parked create Vipps knows and has not ended: handed back to the
	// worker, due now.
	known, knownRef := newAttempt(t, h)
	h.vipps.Approve(knownRef)
	setAttempt(t, h, knownRef, "next_poll_at = NULL")
	refusedAs(t, "a parked create Vipps knows", m.Do(http.MethodPost, abandonPath(attemptOf(t, h, knownRef).ID), abandonBody("Sjekket", true)), "attempt_not_abandonable")
	if got := attemptOf(t, h, knownRef); got.State != "created" || got.NextPollAt == nil || !got.NextPollAt.Equal(h.Now()) {
		t.Errorf("the parked create Vipps knows = %s next %v, want created and due now", got.State, got.NextPollAt)
	}
	cycle(t, h)
	cycle(t, h)
	if got := vippsPaymentsOf(t, h, known.ID); len(got) != 1 {
		t.Errorf("the handed-back payment's registrations = %v, want one", got)
	}
}

// TestAbandon_CapturedMoneyIsRegistered: a capture_unknown attempt whose
// read shows money captured is never abandoned — the capture is registered
// as the worker registers it, and the abandon refused.
func TestAbandon_CapturedMoneyIsRegistered(t *testing.T) {
	t.Parallel()
	h := workerReady(t)
	inv, ref := unknownAttempt(t, h)
	if _, err := portal(t, h).Capture(context.Background(), ref, payments.NOK(250000), "portal-capture"); err != nil {
		t.Fatalf("the portal capture: %v", err)
	}
	p := refusedAs(t, "an abandon of captured money", vippsManager(t, h).Do(http.MethodPost, abandonPath(attemptOf(t, h, ref).ID),
		abandonBody("Sjekket", true)), "attempt_not_abandonable")
	if !strings.Contains(p.Detail, "2500.00") {
		t.Errorf("the refusal's detail = %q, want the captured amount named", p.Detail)
	}
	if a := attemptOf(t, h, ref); a.State != "captured" || text(a.Captured) != "2500.00" || a.Reserved != nil {
		t.Errorf("the attempt = %s captured %s, want captured 2500.00", a.State, text(a.Captured))
	}
	if got := vippsPaymentsOf(t, h, inv.ID); len(got) != 1 || !strings.HasPrefix(got[0], "2500.00 ") {
		t.Errorf("the Vipps payments = %v, want one of 2500.00", got)
	}
}

// TestAbandon_ReleasesTheReservation: a capture_unknown attempt Vipps shows
// ended with nothing captured is abandoned — who, when and why recorded,
// the reservation released — and a manual payment of the whole amount is
// then accepted. Stranded with no capture ever sent — the switch off, or
// made under another sales unit — it is abandoned unread; stranded with a
// capture sent, refused until Vipps can be read. A parked create Vipps does
// not know is abandoned, and frees the invoice's on-site slot.
func TestAbandon_ReleasesTheReservation(t *testing.T) {
	t.Parallel()
	t.Run("capture_unknown ended at Vipps", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv, ref := unknownAttempt(t, h)
		refusedAs(t, "a manual payment while unknown", payer(t, h).Do(http.MethodPost, paymentsPath(inv.ID), pay(2500, "2026-09-12")), "invoice_settled")
		if _, err := portal(t, h).Cancel(context.Background(), ref, "portal-cancel"); err != nil {
			t.Fatalf("cancel in the portal: %v", err)
		}
		got := abandoned(t, h, attemptOf(t, h, ref).ID)
		if got.State != "abandoned" || got.ReservedAmount != nil || got.AbandonedAt == nil || got.AbandonedByUserID == nil ||
			text(got.AbandonReason) != "Sjekket i Vipps-portalen" {
			t.Errorf("the abandoned attempt = %+v, want abandoned, unreserved, who, when and why", got)
		}
		a := attemptOf(t, h, ref)
		if a.State != "abandoned" || a.Reserved != nil || a.NextPollAt != nil || a.FinishedAt == nil {
			t.Errorf("the row = %+v, want abandoned, released, never polled, finished", a)
		}
		registered(t, h, inv.ID, pay(2500, "2026-09-12"))
		before := len(h.vipps.Requests())
		cycle(t, h)
		if n := len(h.vipps.Requests()) - before; n != 0 {
			t.Errorf("%d requests for an abandoned attempt", n)
		}
	})
	t.Run("stranded by the switch", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, modtest.WithEnv("INVOICES_VIPPS_ENABLED", "0"))
		saveSeller(t, h, completeSeller(1))
		inv := issuedAcme(t, h)
		ref := plantReservation(t, h, inv.ID, "capturing", "2500")
		got := abandoned(t, h, attemptOf(t, h, ref).ID)
		if got.State != "abandoned" {
			t.Errorf("the stranded capture = %s, want abandoned", got.State)
		}
		registered(t, h, inv.ID, pay(2500, "2026-09-12"))
		sent := plantReservation(t, h, issuedAcme(t, h).ID, "capture_unknown", "2500")
		setAttempt(t, h, sent, "capture_attempts = 1")
		refusedAs(t, "a stranded capture once sent", vippsManager(t, h).Do(http.MethodPost, abandonPath(attemptOf(t, h, sent).ID),
			abandonBody("Sjekket", true)), "attempt_not_abandonable")
		if a := attemptOf(t, h, sent); a.State != "capture_unknown" || a.Reserved == nil {
			t.Errorf("the capture once sent = %s reserved %s, want kept", a.State, text(a.Reserved))
		}
	})
	t.Run("stranded by another sales unit", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv := issuedAcme(t, h)
		ref := plantReservation(t, h, inv.ID, "capturing", "2500")
		setAttempt(t, h, ref, "msn = '999999'")
		before := len(h.vipps.Requests())
		if got := abandoned(t, h, attemptOf(t, h, ref).ID); got.State != "abandoned" {
			t.Errorf("the foreign capture = %s, want abandoned", got.State)
		}
		if n := len(h.vipps.Requests()) - before; n != 0 {
			t.Errorf("%d requests sent for another sales unit's attempt", n)
		}
	})
	t.Run("a parked release, released in the portal", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv, ref := approvedAttempt(t, h)
		registered(t, h, inv.ID, pay(1000, "2026-09-12"))
		h.vipps.Fail(vippstest.OpCancel, vippstest.Failure{Status: http.StatusBadRequest, Code: "6000"})
		cycle(t, h)
		if a := attemptOf(t, h, ref); !a.ReleasePending || a.NextPollAt != nil {
			t.Fatalf("the release = pending %v next %v, want parked", a.ReleasePending, a.NextPollAt)
		}
		stranded := func() int { return len(ofType(attentionOf(t, payer(t, h)), "paymentReservationStranded")) }
		if stranded() != 1 {
			t.Fatal("no paymentReservationStranded for the parked release")
		}
		h.vipps.Clear(vippstest.OpCancel)
		if _, err := portal(t, h).Cancel(context.Background(), ref, "portal-release"); err != nil {
			t.Fatalf("release in the portal: %v", err)
		}
		refusedAs(t, "an abandon of a parked release", vippsManager(t, h).Do(http.MethodPost, abandonPath(attemptOf(t, h, ref).ID),
			abandonBody("Frigitt i portalen", true)), "attempt_not_abandonable")
		if a := attemptOf(t, h, ref); a.State != "captured" || a.NextPollAt == nil || !a.NextPollAt.Equal(h.Now()) {
			t.Fatalf("the parked release = %s next %v, want captured and handed back, due now", a.State, a.NextPollAt)
		}
		cycle(t, h)
		if a := attemptOf(t, h, ref); a.ReleasePending || text(a.Released) != "1000.00" || stranded() != 0 {
			t.Errorf("after the worker's read = pending %v released %s, %d items; want released 1000.00 and no item",
				a.ReleasePending, text(a.Released), stranded())
		}
	})
	t.Run("a parked create Vipps does not know", func(t *testing.T) {
		t.Parallel()
		h := workerReady(t)
		inv := issuedAcme(t, h)
		ref := plantParkedCreate(t, h, inv.ID)
		if _, err := h.Pool().Exec(context.Background(), `INSERT INTO invoices.payment_attempts (invoice_id, provider, msn, flow, amount,
			    create_key, cancel_key, state, created_at, expires_at)
			VALUES ($1, 'vipps', '123456', 'on_site', 2500, gen_random_uuid(), gen_random_uuid(), 'creating', now(), now())`, inv.ID); err == nil {
			t.Fatal("a second live on-site attempt was accepted beside the parked one")
		}
		if got := abandoned(t, h, attemptOf(t, h, ref).ID); got.State != "abandoned" {
			t.Errorf("the parked create = %s, want abandoned", got.State)
		}
		if vippsCalls(h, ref, http.MethodGet, ref) != 1 {
			t.Errorf("the parked create was abandoned without one read of Vipps")
		}
		plantParkedCreate(t, h, inv.ID) // the on-site slot is free again
	})
}

// TestAbandon_LockOrder: an abandon locks the attempt alone (D18), and its
// read of Vipps is made outside any lock. Not parallel: the lock seam is
// the package's.
func TestAbandon_LockOrder(t *testing.T) {
	h := workerReady(t)
	_, ref := unknownAttempt(t, h)
	if _, err := portal(t, h).Cancel(context.Background(), ref, "portal-cancel"); err != nil {
		t.Fatalf("cancel in the portal: %v", err)
	}
	id := attemptOf(t, h, ref).ID
	seen := &lockSeen{}
	defer invoices.SetLockTaken(seen.note)()
	before := lockedContractCalls.count()
	abandoned(t, h, id)
	if got, want := seen.take(), []string{"payment_attempt " + strconv.FormatInt(id, 10)}; !slices.Equal(got, want) {
		t.Errorf("the abandon's locks = %v, want %v", got, want)
	}
	if calls := lockedContractCalls.since(before); len(calls) > 0 {
		t.Errorf("calls made inside a transaction:\n%s", strings.Join(calls, "\n"))
	}
}
