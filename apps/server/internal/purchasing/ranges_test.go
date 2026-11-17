package purchasing_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// What a column cannot hold is a 400 naming it, never a 500: a supplier's
// item number, a saved text line's unit, a settings address, and an amount
// above numeric(14,2).
func TestRanges_Answer400(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, buyer...)
	long := variantLine(varBolt, "1")
	long.SupplierItemNumber = str(strings.Repeat("x", 61))
	p := expectRefusal(t, c.Do(http.MethodPost, ordersPath, gen.PurchasingOrderInput{SupplierId: supNorsk, SiteId: i64(siteMain),
		Lines: &[]gen.PurchasingLineInput{long}}), http.StatusBadRequest, "line_invalid", "a 61-character item number")
	if p.Lines == nil || len(*p.Lines) != 1 || (*p.Lines)[0].Index != 0 {
		t.Errorf("lines = %+v", p.Lines)
	}

	o := createOrder(t, c, supNorsk, textLine("Frakt", "1", "100"))
	u := updateOf(o)
	u.Lines[0].Unit = str(strings.Repeat("u", 21))
	expectRefusal(t, c.Do(http.MethodPut, orderPath(o.Id), u), http.StatusBadRequest, "line_invalid", "a 21-character unit on a saved line")
	u = updateOf(o)
	u.Lines[0].SupplierItemNumber = str(strings.Repeat("x", 61))
	expectRefusal(t, c.Do(http.MethodPut, orderPath(o.Id), u), http.StatusBadRequest, "line_invalid", "a 61-character item number on a saved line")

	big := textLine("Maskin", "999999999", "99999")
	expectRefusal(t, c.Do(http.MethodPost, ordersPath, gen.PurchasingOrderInput{SupplierId: supNorsk, SiteId: i64(siteMain),
		Lines: &[]gen.PurchasingLineInput{big}}), http.StatusBadRequest, "amount_out_of_range", "a line above numeric(14,2)")
	half := textLine("Maskin", "1", "600000000000")
	expectRefusal(t, c.Do(http.MethodPost, ordersPath, gen.PurchasingOrderInput{SupplierId: supNorsk, SiteId: i64(siteMain),
		Lines: &[]gen.PurchasingLineInput{half, half}}), http.StatusBadRequest, "amount_out_of_range", "a total above numeric(14,2)")

	a := h.SignIn(t, admin...)
	for name, addr := range map[string]gen.PurchasingAddress{
		"a 101-character city":    {City: str(strings.Repeat("c", 101))},
		"a 21-character postcode": {PostalCode: str(strings.Repeat("1", 21))},
		"a three-letter country":  {Country: str("NOR")},
	} {
		res := a.Do(http.MethodPut, "/api/v1/purchasing/settings", gen.PurchasingSettingsInput{OrderSeriesStart: 1, PriceTolerancePercent: "2",
			DeliveryAddresses: []gen.PurchasingSiteAddress{{SiteId: siteMain, Address: addr}}})
		expectRefusal(t, res, http.StatusBadRequest, "", name)
	}
}
