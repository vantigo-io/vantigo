package purchasing_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// The search takes % and _ as the characters they are; clearing a
// confirmation is logged as such; closing the last open line of an order
// received in full makes it received with its change-log row, and publishes
// no closed event.
func TestSmallRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, buyer...)
	plain := markSent(t, c, createOrder(t, c, supNorsk, variantLine(varBolt, "1")))
	odd := markSent(t, c, createOrder(t, c, supNorsk, variantLine(varBolt, "1")))
	for o, ref := range map[*gen.PurchasingOrder]string{&plain: "AB-1", &odd: "A_B%1"} {
		res := c.Do(http.MethodPut, orderPath(o.Id, "confirmation"), gen.PurchasingConfirmationInput{ExpectedUpdatedAt: o.UpdatedAt, SupplierReference: str(ref)})
		mustStatus(t, res, http.StatusOK, "confirm "+ref)
		*o = orderOf(t, res)
	}
	for _, q := range []string{"_", "%25", "A_B"} {
		var page gen.PurchasingOrderPage
		res := c.Do(http.MethodGet, ordersPath+"?q="+q, nil)
		mustStatus(t, res, http.StatusOK, "search "+q)
		res.JSON(&page)
		if page.Total != 1 || page.Items[0].Id != odd.Id {
			t.Errorf("search %q = %d orders", q, page.Total)
		}
	}

	res := c.Do(http.MethodPut, orderPath(plain.Id, "confirmation"), gen.PurchasingConfirmationInput{ExpectedUpdatedAt: plain.UpdatedAt})
	mustStatus(t, res, http.StatusOK, "clear the confirmation")
	hist := historyOf(t, c, plain.Id)
	if last := hist.Changes[0].Actions[0].Action; last != "confirmation_cleared" {
		t.Errorf("the clearing is logged as %q", last)
	}

	h.Exec(t, `UPDATE purchasing.order_lines SET received = quantity WHERE order_id = $1`, odd.Id)
	odd = getOrder(t, c, odd.Id)
	res = c.Do(http.MethodPost, orderPath(odd.Id, "lines", itoa(odd.Lines[0].Id), "close"), version(odd))
	mustStatus(t, res, http.StatusOK, "close the last line")
	if o := orderOf(t, res); o.Status != gen.PurchasingOrderStatusReceived {
		t.Fatalf("after its last line = %s", o.Status)
	}
	if hist := historyOf(t, c, odd.Id); hist.Changes[0].Actions[0].Action != "received" {
		t.Errorf("the end is logged as %+v", hist.Changes[0].Actions)
	}
	if n := eventsNamed(t, h, "purchasing.order.closed", odd.Id); n != 0 {
		t.Errorf("closed events = %d, want 0", n)
	}
}
