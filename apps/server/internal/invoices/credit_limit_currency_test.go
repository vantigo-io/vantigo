package invoices_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// A EUR invoice counts against the NOK limit at its own NOK (open_nok, at
// the rate it was issued at), and an order backlog in EUR at today's rate; a
// currency with no rate is listed as uncounted.
func TestCreditLimit_ForeignCurrencyCountsInNOK(t *testing.T) {
	t.Parallel()
	backlog := &fakeBacklog{amounts: []contracts.CurrencyAmount{{Currency: "EUR", Amount: "100.00"}, {Currency: "DKK", Amount: "50.00"}}}
	h := newHarness(t, modtest.WithOrderBacklog(backlog))
	saveSeller(t, h, completeSeller(1))
	euroOn(t, h, false)
	euroInvoice(t, h) // EUR 1000 at 11.5: NOK 11 500.00 open
	limit := "12000.00"
	h.customers.edit(customerEuro, func(p *contracts.CustomerBillingProfile) { p.CreditLimit = &limit })

	// 11 500 open + EUR 100 at 11.5 (1 150) + this draft, EUR 10 + 25 % at 11.5 (143.75).
	draft := createDraft(t, h, draftBody(customerEuro, line("A", 1, 10, vat25)))
	var got creditLimitJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, invoicePath(draft.ID), nil).JSON(&got)
	if got.CreditLimit == nil || got.CreditLimit.ExposureNok != "12793.75" || !got.CreditLimit.Exceeded {
		t.Fatalf("credit = %+v, want 12793.75 exceeded", got.CreditLimit)
	}
	if len(got.CreditLimit.Uncounted) != 1 || got.CreditLimit.Uncounted[0].Currency != "DKK" || got.CreditLimit.Uncounted[0].Amount != "50.00" {
		t.Errorf("uncounted = %+v, want DKK 50.00 alone", got.CreditLimit.Uncounted)
	}
}
