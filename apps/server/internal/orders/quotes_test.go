package orders_test

import (
	"bytes"
	"math/big"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/docmath"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// A new quote takes the next number, snapshots the customer from the billing
// profile (O13) — the primary delivery address, the buyer reference, the
// resolved terms — and prices its catalog lines from the list price and the
// category's VAT code (O16), with docmath's amounts (O17). Its variants are
// locked first.
func TestCreateQuote_PricesFromTheCatalogAndSnapshotsTheCustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)

	food := productLine(varFood, 3)
	food.DiscountPercent = f64(12.5)
	free := line(gen.OrdersLineInputKindProduct)
	free.Name, free.Quantity, free.UnitPrice = str("Freight"), f64(1), f64(250)
	section := line(gen.OrdersLineInputKindSection)
	section.Name = str("Hardware")
	note := line(gen.OrdersLineInputKindNote)
	note.Description = str("Delivered on pallets")
	q := createQuote(t, c, custBusiness, section, productLine(varBolt, 2), food, free, note)

	if q.Number != 1 || q.Status != gen.OrdersQuoteStatusDraft || q.Revision != 1 {
		t.Errorf("number %d, status %s, revision %d; want 1, draft, 1", q.Number, q.Status, q.Revision)
	}
	if q.Customer.Name != "Acme AS" || q.YourReference != "PO-77" || q.PaymentTermsDays != 30 || q.Currency != "NOK" ||
		q.PricesIncludeVat || q.Customer.OrganisationNumber == nil || *q.Customer.OrganisationNumber != "987654321" {
		t.Errorf("snapshot %+v, reference %q, terms %d, currency %s; want Acme AS, PO-77, 30, NOK", q.Customer, q.YourReference, q.PaymentTermsDays, q.Currency)
	}
	if q.Customer.DeliveryAddressId == nil || *q.Customer.DeliveryAddressId != addrWarehouse {
		t.Errorf("delivery address %v, want the primary %d", q.Customer.DeliveryAddressId, addrWarehouse)
	}
	if q.Origin.Channel != "manual" {
		t.Errorf("channel %s, want manual", q.Origin.Channel)
	}
	bolt := lineByVariant(t, q, varBolt)
	if bolt.UnitPrice == nil || *bolt.UnitPrice != 100 || bolt.PriceSource == nil || *bolt.PriceSource != gen.OrdersLinePriceSourceCatalog ||
		bolt.VatCodeId == nil || *bolt.VatCodeId != codeStandard || bolt.LineNet != 200 || bolt.Description != "Galvanised" {
		t.Errorf("bolt line %+v; want 100 from the catalog, code 3, net 200, the variant's description", bolt)
	}
	// 3 × 33.335 = 100.005 → 100.01, 12.5 % of it 12.50, net 87.51 (O17).
	coffee := lineByVariant(t, q, varFood)
	if coffee.LineGross != 100.01 || coffee.LineAllowance != 12.5 || coffee.LineNet != 87.51 || *coffee.VatCodeId != codeFood {
		t.Errorf("coffee line gross %v allowance %v net %v; want 100.01, 12.50, 87.51 at code 31", coffee.LineGross, coffee.LineAllowance, coffee.LineNet)
	}
	freight := q.Lines[3]
	if freight.PriceSource == nil || *freight.PriceSource != gen.OrdersLinePriceSourceManual || freight.VatCodeId == nil || *freight.VatCodeId != codeStandard {
		t.Errorf("free-text line %+v; want manual at the standard code", freight)
	}
	// VAT per rate on the sum of the nets: 25 % of 450 and 15 % of 87.51.
	if q.Totals.Net != 537.51 || q.Totals.Vat != 125.63 || q.Totals.Gross != 663.14 || len(q.Totals.VatSummaries) != 2 {
		t.Errorf("totals %+v; want net 537.51, vat 125.63 (112.50 + 13.13), gross 663.14 over two rates", q.Totals)
	}
	if calls := h.catalog.lockCalls(); len(calls) != 1 || !slices.Equal(calls[0], []int32{varBolt, varFood}) {
		t.Errorf("lock calls %v, want one of [101 102]", calls)
	}
	got := createQuote(t, c, custBusiness)
	if got.Number != 2 {
		t.Errorf("the second quote's number = %d, want 2", got.Number)
	}
}

// A repeated create answers the first quote with 200 (O27); the same id for
// another customer is refused.
func TestCreateQuote_ARepeatedRequestAnswersTheFirstQuote(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	id := uuid.New()
	body := gen.OrdersQuoteInput{ClientRequestId: id, CustomerId: custBusiness}
	first := c.Do(http.MethodPost, quotesPath, body)
	mustStatus(t, first, http.StatusCreated, "create")
	again := c.Do(http.MethodPost, quotesPath, body)
	mustStatus(t, again, http.StatusOK, "repeat")
	if quoteOf(t, first).Id != quoteOf(t, again).Id {
		t.Error("the repeat made a second quote")
	}
	body.CustomerId = custPerson
	expectRefusal(t, c.Do(http.MethodPost, quotesPath, body), http.StatusConflict, "client_request_reused", "reuse")
	if n := h.Count(t, `SELECT count(*) FROM orders.quotes`); n != 1 {
		t.Errorf("%d quotes, want 1", n)
	}
}

// A create is refused for an unknown or disabled customer, for a line that
// names no variant the catalog has, and for an optional order line's rule
// (quotes may have them; the line codes are all named at once).
func TestCreateQuote_Refusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	res := c.Do(http.MethodPost, quotesPath, gen.OrdersQuoteInput{ClientRequestId: uuid.New(), CustomerId: custDisabled})
	expectRefusal(t, res, http.StatusBadRequest, "customer_disabled", "disabled customer")
	res = c.Do(http.MethodPost, quotesPath, gen.OrdersQuoteInput{ClientRequestId: uuid.New(), CustomerId: 999})
	expectRefusal(t, res, http.StatusBadRequest, "", "unknown customer")

	bad := []gen.OrdersLineInput{productLine(999, 1), productLine(varBolt, 0), productLine(varBolt, 1.2345)}
	res = c.Do(http.MethodPost, quotesPath, gen.OrdersQuoteInput{ClientRequestId: uuid.New(), CustomerId: custBusiness, Lines: &bad})
	p := expectRefusal(t, res, http.StatusBadRequest, "variant_not_found", "bad lines")
	if p.Lines == nil || len(*p.Lines) != 3 || (*p.Lines)[1].Code != "quantity_invalid" || (*p.Lines)[2].Index != 2 {
		t.Errorf("lines %+v; want three failures, the quantities quantity_invalid", p.Lines)
	}
	many := make([]gen.OrdersLineInput, 501)
	for i := range many {
		many[i] = productLine(varBolt, 1)
	}
	res = c.Do(http.MethodPost, quotesPath, gen.OrdersQuoteInput{ClientRequestId: uuid.New(), CustomerId: custBusiness, Lines: &many})
	mustStatus(t, res, http.StatusBadRequest, "too many lines")
	if n := h.Count(t, `SELECT count(*) FROM orders.quotes`); n != 0 {
		t.Errorf("%d quotes, want none", n)
	}
}

// Every request schema is closed: a property the contract does not name is
// refused, in a line too.
func TestCreateQuote_RefusesAPropertyTheContractDoesNotName(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	body := map[string]any{
		"clientRequestId": uuid.NewString(), "customerId": custBusiness, "colour": "red",
		"lines": []map[string]any{{"kind": "product", "variantId": varBolt, "quantity": 1, "discount": 10}},
	}
	p := expectRefusal(t, c.Do(http.MethodPost, quotesPath, body, modtest.SkipContract("the request is invalid on purpose")), http.StatusBadRequest, "", "unknown properties")
	if p.Errors == nil || (*p.Errors)["colour"] == nil || (*p.Errors)["lines[0].discount"] == nil {
		t.Errorf("errors %v; want colour and lines[0].discount", p.Errors)
	}
}

// Saving a draft refreshes the snapshot from the billing profile (O14), keeps
// a manual price, keeps a catalog price on the same day and in the same
// currency, and prices the catalog lines again when the currency moves (O16).
func TestUpdateQuote_RefreshesTheSnapshotAndRepricesWhenTheCurrencyMoves(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	manual := productLine(varFood, 2)
	manual.UnitPrice = f64(40)
	q := createQuote(t, c, custBusiness, productLine(varBolt, 1), manual)
	h.dir.update(custBusiness, func(p *contracts.CustomerBillingProfile) { p.Name = "Acme Holding AS" })
	h.catalog.setPrice(varBolt, "NOK", "120.00")

	keep := func(q gen.OrdersQuote) []gen.OrdersLineInput {
		var out []gen.OrdersLineInput
		for _, l := range q.Lines {
			id := l.Id
			out = append(out, gen.OrdersLineInput{Id: &id, Kind: gen.OrdersLineInputKind(l.Kind), VariantId: l.VariantId})
		}
		return out
	}
	res := c.Do(http.MethodPut, quotePath(q.Id), gen.OrdersQuoteUpdate{ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: q.Revision, CustomerId: custBusiness, Lines: keep(q)})
	mustStatus(t, res, http.StatusOK, "save")
	saved := quoteOf(t, res)
	if saved.Customer.Name != "Acme Holding AS" {
		t.Errorf("snapshot name %q, want the refreshed Acme Holding AS", saved.Customer.Name)
	}
	if b := lineByVariant(t, saved, varBolt); *b.UnitPrice != 100 || b.Id != q.Lines[0].Id {
		t.Errorf("bolt %v (id %d); want the catalog price kept on the same day, the same line", *b.UnitPrice, b.Id)
	}
	if f := lineByVariant(t, saved, varFood); *f.UnitPrice != 40 || *f.PriceSource != gen.OrdersLinePriceSourceManual {
		t.Errorf("coffee %v %s; want the manual 40 kept", *f.UnitPrice, *f.PriceSource)
	}

	res = c.Do(http.MethodPut, quotePath(q.Id), gen.OrdersQuoteUpdate{ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: q.Revision, CustomerId: custBusiness, Currency: str("EUR"), Lines: keep(saved)})
	mustStatus(t, res, http.StatusOK, "save in EUR")
	eur := quoteOf(t, res)
	if b := lineByVariant(t, eur, varBolt); b.UnitPrice == nil || *b.UnitPrice != 9.5 {
		t.Errorf("bolt in EUR %v, want the EUR list price 9.50", b.UnitPrice)
	}
	if f := lineByVariant(t, eur, varFood); *f.UnitPrice != 40 {
		t.Errorf("the manual price moved with the currency: %v", *f.UnitPrice)
	}

	expectRefusal(t, c.Do(http.MethodPut, quotePath(q.Id), gen.OrdersQuoteUpdate{ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: 7, CustomerId: custBusiness, Lines: keep(eur)}),
		http.StatusConflict, "version_conflict", "stale revision")
	res = c.Do(http.MethodPut, quotePath(q.Id), gen.OrdersQuoteUpdate{ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: 1, CustomerId: custBusiness, DeliveryAddressId: i32(999), Lines: keep(eur)})
	expectRefusal(t, res, http.StatusBadRequest, "", "an address the customer does not have")
	mustStatus(t, c.Do(http.MethodPut, quotePath(999999), gen.OrdersQuoteUpdate{ExpectedUpdatedAt: quoteAt(t, c, 999999), Revision: 1, CustomerId: custBusiness, Lines: nil}), http.StatusNotFound, "unknown quote")
}

// A customer who buys including VAT enters gross prices; they are stored
// net, at four decimals, and shown gross (O19).
func TestCreateQuote_APersonsPricesAreEnteredIncludingVAT(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	l := productLine(varBolt, 1)
	l.UnitPrice = f64(100)
	q := createQuote(t, c, custPerson, l)
	if !q.PricesIncludeVat || q.Language != gen.OrdersQuoteLanguageEn {
		t.Fatalf("a person's quote: includes VAT %v, language %s; want true, en", q.PricesIncludeVat, q.Language)
	}
	b := q.Lines[0]
	if *b.UnitPrice != 80 || b.DisplayUnitPrice == nil || *b.DisplayUnitPrice != 100 || q.Totals.Gross != 100 {
		t.Errorf("unit price %v, shown %v, gross %v; want 80 net, 100 shown, 100 gross", *b.UnitPrice, b.DisplayUnitPrice, q.Totals.Gross)
	}
}

// Optional lines are left out of the totals (O21) and printed in their own
// block; an order takes only the ones chosen at accept.
func TestQuote_OptionalLinesAreLeftOutOfTheTotalsAndChosenAtAccept(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	a, b := productLine(varFood, 1), productLine(varBolt, 1)
	yes := true
	a.Optional, b.Optional = &yes, &yes
	q := createQuote(t, c, custBusiness, productLine(varBolt, 2), a, b)
	if q.Totals.Net != 200 {
		t.Fatalf("net %v, want 200 without the optional lines", q.Totals.Net)
	}
	res := c.Do(http.MethodGet, quotePath(q.Id, "pdf"), nil)
	mustStatus(t, res, http.StatusOK, "draft PDF")
	if !bytes.HasPrefix(res.Body, []byte("%PDF")) {
		t.Fatal("the draft's PDF is not a PDF")
	}
	m, ok := pdfModels.find("Tilbud", "Tilbud "+strconv.FormatInt(q.Number, 10))
	if !ok || len(m.Rows) != 1 || len(m.OptionalRows) != 2 || m.Watermark != "UTKAST" {
		t.Errorf("model %+v; want one line, two optional, the draft mark", m)
	}

	sent := markSent(t, c, q)
	optionalA := sent.Lines[1].Id
	res = c.Do(http.MethodPost, quotePath(q.Id, "accept"), gen.OrdersAcceptInput{ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: sent.Revision, Into: gen.DraftOrder, OptionalLineIds: &[]int64{optionalA}})
	mustStatus(t, res, http.StatusOK, "accept")
	var accepted gen.OrdersAccepted
	res.JSON(&accepted)
	o := accepted.Order
	if len(o.Lines) != 2 || o.Lines[1].VariantId == nil || *o.Lines[1].VariantId != varFood || o.Lines[1].Optional {
		t.Errorf("order lines %+v; want the bolt and the chosen coffee, not optional", o.Lines)
	}
	if o.Totals.Net != 233.34 {
		t.Errorf("order net %v, want 233.34 (200 + 33.34)", o.Totals.Net)
	}
}

// Accept makes a draft order through the create command (O28) from the
// quote's frozen header and lines, its origin the quote at its revision
// (O27); the quote records it and orders.quote.accepted.v1 is published. A
// quote is accepted once, and only when sent.
func TestAcceptQuote_MakesADraftOrderOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	q := createQuote(t, c, custBusiness, productLine(varBolt, 2))
	expectRefusal(t, c.Do(http.MethodPost, quotePath(q.Id, "accept"), gen.OrdersAcceptInput{ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: 1, Into: gen.DraftOrder}),
		http.StatusConflict, "quote_not_sent", "accept a draft")
	sent := markSent(t, c, q)
	expectRefusal(t, c.Do(http.MethodPost, quotePath(q.Id, "accept"), gen.OrdersAcceptInput{ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: 1, Into: gen.DraftOrder, OptionalLineIds: &[]int64{sent.Lines[0].Id}}),
		http.StatusBadRequest, "", "a line that is not optional")

	res := c.Do(http.MethodPost, quotePath(q.Id, "accept"), gen.OrdersAcceptInput{ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: 1, Into: gen.DraftOrder})
	mustStatus(t, res, http.StatusOK, "accept")
	var a gen.OrdersAccepted
	res.JSON(&a)
	if a.Quote.Status != gen.OrdersQuoteStatusAccepted || a.Quote.AcceptedInto == nil || a.Quote.AcceptedInto.Id != a.Order.Id ||
		a.Quote.AcceptedInto.Number != a.Order.Number {
		t.Errorf("quote %+v; want accepted into the order", a.Quote)
	}
	o := a.Order
	wantSource := strconv.FormatInt(q.Id, 10) + "@1"
	if o.Status != gen.OrdersOrderStatusDraft || o.Number != 1 || o.Origin.Channel != "quote" || o.Origin.SourceId == nil || *o.Origin.SourceId != wantSource ||
		o.Customer.Name != "Acme AS" || o.YourReference != "PO-77" || o.Version != 1 {
		t.Errorf("order %+v; want draft 1 from quote %s with the snapshot", o, wantSource)
	}
	if o.Lines[0].UnitPrice == nil || *o.Lines[0].UnitPrice != 100 || *o.Lines[0].PriceSource != gen.OrdersLinePriceSourceCatalog {
		t.Errorf("order line %+v; want the frozen price", o.Lines[0])
	}
	if n := eventsNamed(t, h, "orders.quote.accepted", q.Id); n != 1 {
		t.Errorf("%d accepted events, want 1", n)
	}
	expectRefusal(t, c.Do(http.MethodPost, quotePath(q.Id, "accept"), gen.OrdersAcceptInput{ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: 1, Into: gen.DraftOrder}),
		http.StatusConflict, "quote_already_accepted", "accept again")
	got := c.Do(http.MethodGet, quotePath(q.Id), nil)
	mustStatus(t, got, http.StatusOK, "get")
	if g := quoteOf(t, got); g.AcceptedInto == nil || g.AcceptedInto.Number != o.Number {
		t.Errorf("the quote's acceptedInto %+v, want order %d", g.AcceptedInto, o.Number)
	}
}

// Accept and confirm needs orders:sales-confirm; with it, the quote becomes a
// confirmed order on one transaction (O18, O20): the order confirmed,
// orders.sales_order.confirmed.v1 published, and its confirmation stored.
func TestAcceptQuote_AndConfirmNeedsTheConfirmKey(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	q := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 1)))
	res := c.Do(http.MethodPost, quotePath(q.Id, "accept"), gen.OrdersAcceptInput{ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: 1, Into: gen.ConfirmedOrder})
	mustStatus(t, res, http.StatusForbidden, "accept and confirm without the key")
	boss := h.SignIn(t, confirmer...)
	res = boss.Do(http.MethodPost, quotePath(q.Id, "accept"), gen.OrdersAcceptInput{ExpectedUpdatedAt: quoteAt(t, boss, q.Id), Revision: 1, Into: gen.ConfirmedOrder})
	mustStatus(t, res, http.StatusOK, "accept and confirm")
	var got gen.OrdersAccepted
	res.JSON(&got)
	if got.Order.Status != gen.OrdersOrderStatusConfirmed || got.Order.SourceQuoteId == nil || *got.Order.SourceQuoteId != q.Id {
		t.Errorf("order status %s, source quote %v; want confirmed from quote %d", got.Order.Status, got.Order.SourceQuoteId, q.Id)
	}
	if n := eventsNamed(t, h, "orders.sales_order.confirmed", got.Order.Id); n != 1 {
		t.Errorf("%d confirmed events, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.documents WHERE owner_kind = 'order' AND kind = 'confirmation'`); n != 1 {
		t.Errorf("%d stored confirmations, want 1", n)
	}
}

// Every line amount Orders stores is docmath's own — the arithmetic Invoices
// uses — for any quantity, price and discount (O17): the line on an order and
// on its invoice are the same number by construction.
func TestLineAmounts_AreDocmathsForGeneratedLines(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	r := rand.New(rand.NewPCG(17, 42))
	var lines []gen.OrdersLineInput
	type want struct{ qty, price, discount string }
	var wants []want
	for range 60 {
		qty := float64(r.IntN(99999)+1) / 1000
		price := float64(r.IntN(9999999)) / 10000
		discount := float64(r.IntN(10001)) / 100
		l := line(gen.OrdersLineInputKindProduct)
		l.Name, l.Quantity, l.UnitPrice, l.DiscountPercent = str("Generated"), &qty, &price, &discount
		lines = append(lines, l)
		wants = append(wants, want{strconv.FormatFloat(qty, 'f', -1, 64), strconv.FormatFloat(price, 'f', -1, 64), strconv.FormatFloat(discount, 'f', -1, 64)})
	}
	q := createQuote(t, c, custBusiness, lines...)
	type stored struct{ Gross, Allowance, Net string }
	rows := make([]stored, 0, len(wants))
	pgRows, err := h.Pool().Query(t.Context(), `SELECT line_gross::text, line_allowance::text, line_net::text FROM orders.lines
		WHERE owner_kind = 'quote' AND owner_id = $1 ORDER BY position`, q.Id)
	if err != nil {
		t.Fatal(err)
	}
	for pgRows.Next() {
		var s stored
		if err := pgRows.Scan(&s.Gross, &s.Allowance, &s.Net); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, s)
	}
	pgRows.Close()
	rat := func(s string) *big.Rat { v, _ := new(big.Rat).SetString(s); return v }
	for i, w := range wants {
		a := docmath.Line(rat(w.qty), rat(w.price), rat(w.discount))
		if rows[i].Gross != a.Gross.FloatString(2) || rows[i].Allowance != a.Allowance.FloatString(2) || rows[i].Net != a.Net.FloatString(2) {
			t.Errorf("line %d (%+v): stored %+v, docmath %s/%s/%s", i, w, rows[i], a.Gross.FloatString(2), a.Allowance.FloatString(2), a.Net.FloatString(2))
		}
	}
}

// A line entered in a packaging stores the quantity in the unit and
// remembers the packaging (O15).
func TestCreateQuote_ALineInAPackaging(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	l := line(gen.OrdersLineInputKindProduct)
	l.VariantId, l.PackagingId, l.PackagingQuantity = i32(varBolt), i32(packCase), f64(3)
	q := createQuote(t, c, custBusiness, l)
	if got := q.Lines[0]; *got.Quantity != 45 || got.PackagingId == nil || *got.PackagingQuantity != 3 || got.LineNet != 4500 {
		t.Errorf("line %+v; want 45 pcs as 3 cases, net 4500", got)
	}
	bad := l
	bad.PackagingId = i32(99)
	res := c.Do(http.MethodPost, quotesPath, gen.OrdersQuoteInput{ClientRequestId: uuid.New(), CustomerId: custBusiness, Lines: &[]gen.OrdersLineInput{bad}})
	expectRefusal(t, res, http.StatusBadRequest, "packaging_not_found", "a packaging the variant does not have")
}

// The lists filter by customer, status, salesperson and a search, newest
// first.
func TestListQuotes_Filters(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	a := createQuote(t, c, custBusiness, productLine(varBolt, 1))
	b := createQuote(t, c, custPerson)
	markSent(t, c, a)
	page := func(query string) gen.OrdersQuotePage {
		res := c.Do(http.MethodGet, quotesPath+query, nil)
		mustStatus(t, res, http.StatusOK, "list "+query)
		var p gen.OrdersQuotePage
		res.JSON(&p)
		return p
	}
	if p := page(""); p.Total != 2 || p.Items[0].Id != b.Id {
		t.Errorf("all %+v; want two, newest first", p)
	}
	if p := page("?status=sent"); p.Total != 1 || p.Items[0].Id != a.Id || p.Items[0].Net != 100 {
		t.Errorf("sent %+v; want quote %d at 100", p, a.Id)
	}
	if p := page("?customerId=" + strconv.Itoa(int(custPerson))); p.Total != 1 || p.Items[0].Id != b.Id {
		t.Errorf("by customer %+v", p)
	}
	if p := page("?q=PO-77"); p.Total != 1 || p.Items[0].Id != a.Id {
		t.Errorf("by reference %+v; want the business's quote", p)
	}
	if p := page("?q=Ola"); p.Total != 1 {
		t.Errorf("by name %+v; want one", p)
	}
	mustStatus(t, c.Do(http.MethodGet, quotesPath+"?pageSize=500", nil, modtest.SkipContract("out of range on purpose")), http.StatusBadRequest, "page size")
}

// A quote never sent is deleted and leaves its number a gap; a sent one is
// cancelled instead. A draft or sent quote is cancelled; an answered one is
// not.
func TestQuote_DeleteCancelDeclineAndCopy(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	draft := createQuote(t, c, custBusiness, productLine(varBolt, 1))
	mustStatus(t, c.Do(http.MethodDelete, withUpdatedAt(quotePath(draft.Id), quoteAt(t, c, draft.Id)), nil), http.StatusNoContent, "delete a draft")
	mustStatus(t, c.Do(http.MethodGet, quotePath(draft.Id), nil), http.StatusNotFound, "get the deleted")
	if n := h.Count(t, `SELECT count(*) FROM orders.lines WHERE owner_kind = 'quote' AND owner_id = $1`, draft.Id); n != 0 {
		t.Errorf("%d lines left", n)
	}
	next := createQuote(t, c, custBusiness, productLine(varBolt, 1))
	if next.Number != 2 {
		t.Errorf("the next number = %d, want 2 (the deleted draft's 1 is a gap)", next.Number)
	}
	sent := markSent(t, c, next)
	expectRefusal(t, c.Do(http.MethodDelete, withUpdatedAt(quotePath(sent.Id), quoteAt(t, c, sent.Id)), nil), http.StatusConflict, "quote_not_draft", "delete a sent quote")

	res := c.Do(http.MethodPost, quotePath(sent.Id, "decline"), gen.OrdersDeclineInput{ExpectedUpdatedAt: quoteAt(t, c, sent.Id), Revision: 1, Reason: str("Too expensive")})
	mustStatus(t, res, http.StatusOK, "decline")
	if d := quoteOf(t, res); d.Status != gen.OrdersQuoteStatusDeclined || d.DeclineReason == nil || *d.DeclineReason != "Too expensive" {
		t.Errorf("declined %+v", d)
	}
	expectRefusal(t, c.Do(http.MethodPost, quotePath(sent.Id, "cancel"), gen.OrdersRevisionInput{ExpectedUpdatedAt: quoteAt(t, c, sent.Id), Revision: 1}), http.StatusConflict, "quote_not_sent", "cancel a declined quote")

	res = c.Do(http.MethodPost, quotePath(sent.Id, "copy"), nil)
	mustStatus(t, res, http.StatusCreated, "copy")
	cp := quoteOf(t, res)
	if cp.Status != gen.OrdersQuoteStatusDraft || cp.Number != 3 || cp.Origin.Channel != "copy" || cp.Origin.SourceId == nil ||
		*cp.Origin.SourceId != strconv.FormatInt(sent.Id, 10)+"@1" || len(cp.Lines) != 1 {
		t.Errorf("copy %+v; want draft 3 from the declined quote", cp)
	}
	res = c.Do(http.MethodPost, quotePath(cp.Id, "cancel"), gen.OrdersRevisionInput{ExpectedUpdatedAt: quoteAt(t, c, cp.Id), Revision: 1})
	mustStatus(t, res, http.StatusOK, "cancel a draft")
	if got := quoteOf(t, res); got.Status != gen.OrdersQuoteStatusCancelled || got.CancelledAt == nil {
		t.Errorf("cancelled %+v", got)
	}
	mustStatus(t, c.Do(http.MethodPost, quotePath(999999, "copy"), nil), http.StatusNotFound, "copy nothing")
}

// dateOf is a civil date for the API.
func dateOf(t time.Time) *openapi_types.Date { return &openapi_types.Date{Time: t} }
