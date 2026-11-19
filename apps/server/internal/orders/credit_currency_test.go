package orders_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// fixedRates is contracts.ExchangeRates over set rates.
type fixedRates map[string]string

func (f fixedRates) Rate(_ context.Context, currency string, day time.Time) (contracts.ExchangeRate, bool, error) {
	r, ok := f[currency]
	return contracts.ExchangeRate{Currency: currency, Day: day, Rate: r, Source: "norges_bank"}, ok, nil
}

// An order in EUR counts against the NOK limit at today's rate; with no rate
// within seven days it counts for nothing and is listed as uncounted.
func TestCredit_AnOrderInEURCountsAtTodaysRate(t *testing.T) {
	t.Parallel()
	euro := func(c *modtest.Client) gen.OrdersOrder {
		lines := []gen.OrdersLineInput{productLine(varBolt, 40)} // EUR 475 gross
		res := c.Do(http.MethodPost, ordersPath, gen.OrdersOrderInput{ClientRequestId: uuid.New(), CustomerId: custBusiness, Currency: str("EUR"), Lines: &lines})
		mustStatus(t, res, http.StatusCreated, "create a EUR order")
		return orderOf(t, res)
	}

	rated := newHarness(t, harnessOptions{receivables: &creditReceivables{nok: "0.00"}, rates: fixedRates{"EUR": "10.0000000000"}})
	setCreditLimit(rated, "1000.00")
	seller := rated.SignIn(t, append(boss, "invoices:access")...)
	w := creditWarningOf(getOrder(t, seller, euro(seller).Id))
	if w == nil || w.Credit == nil || w.Credit.ExposureNok != "4850.00" || len(w.Credit.Uncounted) != 0 {
		t.Fatalf("with a rate = %+v, want EUR 475 + the open EUR 10 at 10: NOK 4850.00, nothing uncounted", w)
	}

	unrated := newHarness(t, harnessOptions{receivables: &creditReceivables{nok: "990.00"}, rates: fixedRates{}})
	setCreditLimit(unrated, "1000.00")
	seller = unrated.SignIn(t, append(boss, "invoices:access")...)
	confirmOrder(t, seller, euro(seller)) // within: the EUR counts for nothing
	o := createOrder(t, seller, custBusiness, productLine(varBolt, 1))
	w = creditWarningOf(getOrder(t, seller, o.Id))
	if w == nil || w.Credit == nil || w.Credit.ExposureNok != "1115.00" ||
		!reflect.DeepEqual(w.Credit.Uncounted, []gen.OrdersCreditAmount{{Currency: "EUR", Amount: "485.00"}}) {
		t.Errorf("without a rate = %+v, want NOK 1115.00 and EUR 485.00 (475 + 10 open) uncounted", w)
	}
}
