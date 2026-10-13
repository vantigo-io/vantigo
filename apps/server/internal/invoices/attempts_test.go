package invoices_test

import (
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
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
