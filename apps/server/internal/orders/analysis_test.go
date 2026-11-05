package orders_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// analyst holds the analysis views (O35).
var analyst = []string{"orders:access", "orders:analysis-view"}

const analysisPath = "/api/v1/orders/analysis"

// period is a query for the days around the harness's today.
func period(h *harness) string {
	today := h.Now().UTC()
	return "from=" + today.AddDate(0, 0, -1).Format(time.DateOnly) + "&to=" + today.AddDate(0, 0, 1).Format(time.DateOnly)
}

// createEURQuote creates a EUR quote for the business customer.
func createEURQuote(t *testing.T, c *modtest.Client, lines ...gen.OrdersLineInput) gen.OrdersQuote {
	t.Helper()
	res := c.Do(http.MethodPost, quotesPath, gen.OrdersQuoteInput{ClientRequestId: uuid.New(), CustomerId: custBusiness, Currency: str("EUR"), Lines: &lines})
	mustStatus(t, res, http.StatusCreated, "EUR quote")
	return quoteOf(t, res)
}

// The hit rate counts and sums the quotes sent in the period by how they were
// answered, per salesperson and currency and per currency; a quote never sent
// is not among them, and the currencies are never added together (O35).
func TestAnalysis_HitRate(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, confirmer...)
	a := h.SignIn(t, analyst...)

	accepted := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 1)))
	res := c.Do(http.MethodPost, quotePath(accepted.Id, "accept"), gen.OrdersAcceptInput{
		ExpectedUpdatedAt: accepted.UpdatedAt, Revision: accepted.Revision, Into: gen.OrdersAcceptInputIntoDraftOrder,
	})
	mustStatus(t, res, http.StatusOK, "accept")
	declined := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 2)))
	mustStatus(t, c.Do(http.MethodPost, quotePath(declined.Id, "decline"), gen.OrdersDeclineInput{
		ExpectedUpdatedAt: declined.UpdatedAt, Revision: declined.Revision,
	}), http.StatusOK, "decline")
	markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 3)))
	createQuote(t, c, custBusiness, productLine(varBolt, 4))
	markSent(t, c, createEURQuote(t, c, productLine(varBolt, 1)))

	mustStatus(t, h.SignIn(t, reader...).Do(http.MethodGet, analysisPath+"/hit-rate?"+period(h), nil), http.StatusForbidden, "without analysis-view")
	res = a.Do(http.MethodGet, analysisPath+"/hit-rate?"+period(h), nil)
	mustStatus(t, res, http.StatusOK, "hit rate")
	var hr gen.OrdersHitRate
	res.JSON(&hr)
	if len(hr.Totals) != 2 || hr.Totals[0].Currency != "EUR" || hr.Totals[1].Currency != "NOK" {
		t.Fatalf("totals %+v", hr.Totals)
	}
	nok := hr.Totals[1]
	if nok.Sent != (gen.OrdersCountAmount{Count: 3, Amount: 600}) || nok.Accepted != (gen.OrdersCountAmount{Count: 1, Amount: 100}) ||
		nok.Declined != (gen.OrdersCountAmount{Count: 1, Amount: 200}) || nok.Open != (gen.OrdersCountAmount{Count: 1, Amount: 300}) ||
		nok.Expired.Count != 0 || nok.Cancelled.Count != 0 {
		t.Errorf("NOK %+v", nok)
	}
	if eur := hr.Totals[0]; eur.Sent != (gen.OrdersCountAmount{Count: 1, Amount: 9.5}) || eur.Open.Count != 1 {
		t.Errorf("EUR %+v", eur)
	}
	if len(hr.Rows) != 2 || hr.Rows[0].Salesperson == nil || hr.Rows[1].Sent.Count != 3 {
		t.Errorf("rows %+v; one per currency for the one salesperson", hr.Rows)
	}

	res = a.Do(http.MethodGet, analysisPath+"/hit-rate?"+period(h)+"&salespersonId="+uuid.NewString(), nil)
	mustStatus(t, res, http.StatusOK, "another salesperson")
	res.JSON(&hr)
	if len(hr.Rows) != 0 || len(hr.Totals) != 0 {
		t.Errorf("another salesperson's %+v; want nothing", hr)
	}

	res = a.Do(http.MethodGet, analysisPath+"/hit-rate?"+period(h)+"&format=csv", nil)
	mustStatus(t, res, http.StatusOK, "CSV")
	body := string(res.Body)
	if !strings.HasPrefix(res.Header("Content-Type"), "text/csv") || !strings.HasPrefix(body, "\ufeffsalesperson;currency;sentCount") ||
		!strings.Contains(body, "Total;NOK;3;600,00;1;100,00;1;200,00") || !strings.Contains(res.Header("Content-Disposition"), "orders-hit-rate-") {
		t.Errorf("CSV %q, %s", body, res.Header("Content-Type"))
	}

	today := h.Now().UTC()
	expectRefusal(t, a.Do(http.MethodGet, fmt.Sprintf("%s/hit-rate?from=%s&to=%s", analysisPath,
		today.Format(time.DateOnly), today.AddDate(0, 0, -1).Format(time.DateOnly)), nil), http.StatusBadRequest, "", "to before from")
}

// The intake sums the orders confirmed in the period, by month, salesperson,
// customer, product and the catalog's category, per currency (O35).
func TestAnalysis_Intake(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	a := h.SignIn(t, analyst...)
	confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 2), productLine(varFood, 1)))
	euro := []gen.OrdersLineInput{productLine(varBolt, 1)}
	res := c.Do(http.MethodPost, ordersPath, gen.OrdersOrderInput{ClientRequestId: uuid.New(), CustomerId: custBusiness, Currency: str("EUR"), Lines: &euro})
	mustStatus(t, res, http.StatusCreated, "EUR order")
	confirmOrder(t, c, orderOf(t, res))
	createOrder(t, c, custBusiness, productLine(varBolt, 5)) // a draft: not intake

	intake := func(groupBy string) gen.OrdersIntake {
		t.Helper()
		res := a.Do(http.MethodGet, analysisPath+"/intake?"+period(h)+"&groupBy="+groupBy, nil)
		mustStatus(t, res, http.StatusOK, "intake by "+groupBy)
		var in gen.OrdersIntake
		res.JSON(&in)
		return in
	}
	rows := func(in gen.OrdersIntake) string {
		var out []string
		for _, r := range in.Rows {
			out = append(out, fmt.Sprintf("%s/%s/%s/%d/%v", r.Key, r.Label, r.Currency, r.Count, r.Amount))
		}
		return strings.Join(out, " ")
	}
	month := h.Now().UTC().Format("2006-01")
	if got := rows(intake("month")); got != month+"/"+month+"/EUR/1/9.5 "+month+"/"+month+"/NOK/1/233.34" {
		t.Errorf("by month %s", got)
	}
	byProduct := intake("product")
	if got := rows(byProduct); got != "101/Bolt M8/EUR/1/9.5 101/Bolt M8/NOK/1/200 102/Coffee/NOK/1/33.34" {
		t.Errorf("by product %s", got)
	}
	if len(byProduct.Totals) != 2 || byProduct.Totals[1] != (gen.OrdersCurrencySum{Currency: "NOK", Count: 1, Amount: 233.34}) {
		t.Errorf("totals %+v", byProduct.Totals)
	}
	if got := rows(intake("category")); got != "//NOK/1/33.34 5/Hardware/EUR/1/9.5 5/Hardware/NOK/1/200" {
		t.Errorf("by category %s", got)
	}
	if got := rows(intake("customer")); got != fmt.Sprintf("%d/Acme AS/EUR/1/9.5 %d/Acme AS/NOK/1/233.34", custBusiness, custBusiness) {
		t.Errorf("by customer %s", got)
	}
	if in := intake("salesperson"); len(in.Rows) != 2 || in.Rows[0].Key == "" {
		t.Errorf("by salesperson %+v", in.Rows)
	}
	expectRefusal(t, a.Do(http.MethodGet, analysisPath+"/intake?"+period(h)+"&groupBy=week", nil), http.StatusBadRequest, "", "an unknown grouping")
	res = a.Do(http.MethodGet, analysisPath+"/intake?"+period(h)+"&groupBy=product&format=csv", nil)
	mustStatus(t, res, http.StatusOK, "CSV")
	if body := string(res.Body); !strings.Contains(body, "product;label;currency;orders;amount\r\n") || !strings.Contains(body, "102;Coffee;NOK;1;33,34\r\n") {
		t.Errorf("CSV %q", body)
	}
}

// The backlog values what confirmed orders have not delivered, and what they
// have delivered and not invoiced, aged by the latest delivery marked: 0–7,
// 8–30 and over 30 days (O35).
func TestAnalysis_Backlog(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 10)))
	deliver(t, c, o, o.Lines[0].Id, 4)
	confirmOrder(t, c, createOrder(t, c, custPerson, productLine(varFood, 3)))

	backlog := func() gen.OrdersBacklog {
		t.Helper()
		a := h.SignIn(t, analyst...) // a session lasts less than the days advanced
		res := a.Do(http.MethodGet, analysisPath+"/backlog", nil)
		mustStatus(t, res, http.StatusOK, "backlog")
		var b gen.OrdersBacklog
		res.JSON(&b)
		return b
	}
	b := backlog()
	if len(b.OrderedNotDelivered) != 1 || b.OrderedNotDelivered[0] != (gen.OrdersCurrencySum{Currency: "NOK", Count: 2, Amount: 700.01}) {
		t.Errorf("ordered, not delivered %+v; want 600 + 100.01", b.OrderedNotDelivered)
	}
	if len(b.DeliveredNotInvoiced) != 1 || b.DeliveredNotInvoiced[0] != (gen.OrdersBacklogAged{Currency: "NOK", Count: 1, Amount: 400, Days0To7: 400}) {
		t.Errorf("delivered, not invoiced %+v", b.DeliveredNotInvoiced)
	}
	h.Advance(10 * 24 * time.Hour)
	if got := backlog().DeliveredNotInvoiced[0]; got.Days0To7 != 0 || got.Days8To30 != 400 {
		t.Errorf("after 10 days %+v", got)
	}
	h.Advance(30 * 24 * time.Hour)
	if got := backlog().DeliveredNotInvoiced[0]; got.Days8To30 != 0 || got.Over30Days != 400 {
		t.Errorf("after 40 days %+v", got)
	}
	a := h.SignIn(t, analyst...)
	res := a.Do(http.MethodGet, analysisPath+"/backlog?format=csv", nil)
	mustStatus(t, res, http.StatusOK, "CSV")
	if body := string(res.Body); !strings.Contains(body, "deliveredNotInvoiced;NOK;1;400,00;0,00;0,00;400,00\r\n") {
		t.Errorf("CSV %q", body)
	}
	mustStatus(t, h.SignIn(t, reader...).Do(http.MethodGet, analysisPath+"/backlog", nil), http.StatusForbidden, "without analysis-view")
}
