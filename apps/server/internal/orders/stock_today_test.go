package orders_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// TestDelivery_PassesTodayAndTheOverride is rule 24 from Orders' side
// (manufacturing phase 2 M62): a delivery's issue carries the business day
// and whether the poster holds inventory:expired-override, both read on the
// pool before the transaction — the fake ledger refuses a call without today,
// so every reserve and issue in this suite proves the first.
func TestDelivery_PassesTodayAndTheOverride(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	for _, override := range []bool{false, true} {
		keys := slices.Clone(stockKeys)
		if override {
			keys = append(keys, "inventory:expired-override")
		}
		c := h.SignIn(t, keys...)
		setDefaultSite(t, c, siteOslo)
		h.stock.put(varBolt, locA1, "5")
		o := createOrder(t, c, custBusiness, productLine(varBolt, 1))
		mustStatus(t, confirmWith(t, c, o, true), http.StatusOK, "confirm")
		res := c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
		mustStatus(t, res, http.StatusCreated, "create delivery")
		d := deliveryOf(t, res)
		mustStatus(t, c.Do(http.MethodPost, deliveryPath(d.Id, "post"), gen.OrdersVersionInput{Version: d.Version, ExpectedUpdatedAt: d.UpdatedAt}),
			http.StatusOK, "post")
		issue, ok := h.stock.lastIssue()
		if !ok || issue.ExpiredOverride != override || issue.Today.Format(time.DateOnly) != h.Now().Format(time.DateOnly) {
			t.Errorf("override %v: issue today %s override %v, want %s and %v", override, issue.Today, issue.ExpiredOverride, h.Now().Format(time.DateOnly), override)
		}
		for _, l := range issue.Lines {
			if l.LotID != 0 || l.AutoLot || l.AllowExpired {
				t.Errorf("an issue line names a lot before layer O: %+v", l)
			}
		}
	}
}
