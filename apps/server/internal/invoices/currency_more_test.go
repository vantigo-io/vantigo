package invoices_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// startForeignPayment registers amount of invoice id with what arrived in
// NOK on its own goroutine under a 30-second deadline.
func startForeignPayment(c *modtest.Client, id int64, amount, amountNOK float64) <-chan raceRequest {
	done := make(chan raceRequest, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		body := pay(amount, "2026-10-07")
		body["amountNok"] = amountNOK
		done <- raceRequest{c.Do(http.MethodPost, paymentsPath(id), body, modtest.Context(ctx))}
	}()
	return done
}

// A payment by hand and an apply from the queue on one EUR invoice (plan
// C12's race): the apply parked on its line while the manual payment of EUR
// 400 commits; released, the apply takes the invoice's lock and sees the
// payment, so its EUR 600 closes the invoice and clears the NOK rest the
// payment left — 11 500 − 4 600 = 6 900 — whatever arrived. open_nok() ends
// at exactly 0, and no deadlock.
func TestCurrency_APaymentRacesAnApply(t *testing.T) {
	h, _ := matchHarness(t, modtest.WithPoolMaxConns(2))
	euroOn(t, h, true)
	inv := euroInvoice(t, h)
	toMatchDay(h)
	c, manual := importer(t, h), payer(t, h)
	_, ids := camtLines(t, h, c, "RACE-EUR", noKidEntry(6, 6000, "Faktura i euro", "EUR"))
	line := ids["EUR"]
	probeConn := ownConn(t, h)
	before := deadlocks(t, probeConn)
	hook, parked := parkEach()
	restore := invoices.SetQueueAfterLineLock(hook)
	defer restore()

	alloc := allocate(inv, 600)
	alloc["amountNok"] = 6000
	applyDone := startAction(c, line, "apply", applyBody(alloc))
	p := waitParked(t, "the apply", parked)
	finished(t, "the manual payment", startForeignPayment(manual, inv.ID, 400, 4700), http.StatusOK)
	close(p.release)
	finished(t, "the apply", applyDone, http.StatusOK)

	if got := texts(t, h, `SELECT amount || ' ' || amount_nok || ' ' || exchange_difference_nok FROM invoices.payments
		WHERE invoice_id = $1 ORDER BY id`, inv.ID); !slices.Equal(got, []string{"400.00 4700.00 100.00", "600.00 6000.00 -900.00"}) {
		t.Errorf("the payments = %v, want the manual one's gain and the apply clearing the NOK rest", got)
	}
	if got := openNOK(t, h, inv.ID); got != "0.00" {
		t.Errorf("open in NOK = %s, want 0.00", got)
	}
	if after := deadlocks(t, probeConn); after != before {
		t.Errorf("Postgres broke %d deadlock(s)", after-before)
	}
}

// Two workers on one currency (plan C12): a fetch row another worker holds
// under a live lease is skipped — Norges Bank is not asked — and taken once
// the lease has run out.
func TestRatesWorker_ALeaseKeepsASecondWorkerOut(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	euroOn(t, h, false)
	h.Advance(72 * time.Hour)
	h.rates.Set("EUR", h.Now().Format(time.DateOnly), "11.8")
	h.Exec(t, `UPDATE invoices.rate_fetches SET lease_id = 'other', lease_until = $1 WHERE currency = 'EUR'`, h.Now().Add(5*time.Minute))
	w := invoices.NewRatesWorker(h.Deps())
	before := len(h.rates.Requests())
	if err := w.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(h.rates.Requests()) - before; n != 0 {
		t.Errorf("Norges Bank was asked %d times while another worker held EUR, want none", n)
	}
	h.Advance(6 * time.Minute)
	if err := w.RunCycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(h.rates.Requests()) - before; n != 1 {
		t.Errorf("Norges Bank was asked %d times after the lease ran out, want once", n)
	}
	if got := h.Count(t, `SELECT count(*) FROM invoices.rate_fetches WHERE currency = 'EUR' AND lease_id IS NULL`); got != 1 {
		t.Errorf("the fetch row is still leased")
	}
}

// An order in a currency turned on (design I20): not blocked, invoiced by
// the wizard in its currency at the day's rate, and by a billing run as a
// draft of its own beside the customer's NOK one.
func TestCurrency_AnOrderInEURIsInvoicedInEUR(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"wizard", "billing run"} {
		f := newOrdersFixture(t)
		euroOn(t, f.h, false)
		putTwoOrders(f.orders)
		euro := acmeOrder(order2212, 2212, "PO-1", "2026-09-02")
		euro.Currency = "EUR"
		f.orders.put(euro, orderLine{id: 4, description: "Lampe", unit: "stk", quantity: "1", price: "50.00000000", discount: "0.00", vatCode: vat25})

		var currencies []string
		if how == "wizard" {
			for _, d := range fromOrders(t, f.h, false, pick(order2212, 4, 1)) {
				inv := getInvoice(t, f.h, d.InvoiceID)
				currencies = append(currencies, inv.Currency)
				if inv.ExchangeRate != 11.5 {
					t.Errorf("the wizard's EUR draft is at %v, want 11.5", inv.ExchangeRate)
				}
			}
		} else {
			maker, _ := runMaker(t, f.h)
			startRun(t, maker, map[string]any{"issue": false, "source": "orders", "combine": true, "through": "2026-09-30"})
			carryAll(t, f.h)
			currencies = texts(t, f.h, `SELECT currency FROM invoices.invoices WHERE customer_id = $1 ORDER BY currency`, customerAcme)
		}
		want := []string{"EUR"}
		if how == "billing run" {
			want = []string{"EUR", "NOK"}
		}
		slices.Sort(currencies)
		if !slices.Equal(currencies, want) {
			t.Errorf("%s: drafts in %v, want %v", how, currencies, want)
		}
	}
}
