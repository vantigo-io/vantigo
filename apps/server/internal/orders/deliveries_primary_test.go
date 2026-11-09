package orders_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// TestDelivery_PrimaryLocationFirst is inventory phase 2 I55 on Orders'
// side: Stock marks the variant's primary location at the site, and a draft
// delivery proposes it when it holds the line, lists it first, and its pick
// list names it first, the rest by full code.
func TestDelivery_PrimaryLocationFirst(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{stock: true})
	c := h.SignIn(t, stockKeys...)
	setDefaultSite(t, c, siteOslo)
	h.stock.put(varBolt, locA1, "6")
	h.stock.put(varBolt, locA2, "4")
	h.stock.setPrimary(varBolt, locA2)

	o := createOrder(t, c, custBusiness, productLine(varBolt, 2))
	mustStatus(t, confirmWith(t, c, o, false), http.StatusOK, "confirm")
	res := c.Do(http.MethodPost, deliveriesPath, gen.OrdersDeliveryInput{OrderId: o.Id})
	mustStatus(t, res, http.StatusCreated, "create delivery")
	d := deliveryOf(t, res)
	if len(d.Lines) != 1 || len(d.Lines[0].Locations) != 2 || d.Lines[0].Locations[0].LocationId != locA2 || d.Lines[0].Locations[1].LocationId != locA1 {
		t.Fatalf("locations = %+v, want OSL-A-02 (primary) first", d.Lines)
	}
	if d.Lines[0].FromLocationId == nil || *d.Lines[0].FromLocationId != locA2 {
		t.Errorf("proposed from %v, want the primary location", d.Lines[0].FromLocationId)
	}

	mustStatus(t, c.Do(http.MethodGet, deliveryPath(d.Id, "pick-list"), nil), http.StatusOK, "pick list")
	m, ok := pdfModels.find("Plukkliste", fmt.Sprintf("Leveranse %d – Ordre %d", d.Number, o.Number), func(m orders.PDFModel) bool {
		return len(m.Rows) == 1
	})
	if !ok || len(m.Rows[0]) < 5 || !strings.HasPrefix(m.Rows[0][4], "OSL-A-02") || !strings.Contains(m.Rows[0][4], ", OSL-A-01") {
		t.Errorf("pick list %+v, %v; want the holding column primary first", m.Rows, ok)
	}
}
