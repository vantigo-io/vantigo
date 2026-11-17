package purchasing_test

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// A draft takes the supplier's currency and language, the only rule's
// defaults and the supplier item's number, price, packaging and lead time;
// a line's quantity is in the variant's unit and its net rounded to two.
func TestCreate_DraftTakesTheDefaults(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, buyer...)
	res := c.Do(http.MethodPost, itemsPath, gen.PurchasingSupplierItemInput{SupplierId: supNorsk, VariantId: varBolt,
		SupplierItemNumber: str("NS-88"), Price: str("12.5"), PackagingId: i32(packBox), LeadTimeDays: i32(7),
		MinimumQuantity: str("5"), Multiple: str("5")})
	mustStatus(t, res, http.StatusCreated, "create supplier item")

	o := createOrder(t, c, supNorsk, variantLine(varBolt, "3"), textLine("Frakt", "1", "250"))
	if o.Status != gen.PurchasingOrderStatusDraft || o.Number != nil || o.Currency != "NOK" || o.Language != "nb" {
		t.Fatalf("draft = %s number %v %s %s", o.Status, o.Number, o.Currency, o.Language)
	}
	if o.SupplierName != "Norsk Stål AS" || !o.SupplierActive || o.SiteName != "Hovedlager" {
		t.Errorf("supplier/site = %q %v %q", o.SupplierName, o.SupplierActive, o.SiteName)
	}
	if o.DeliveryAddress.Line1 == nil || *o.DeliveryAddress.Line1 != "Storgata 1" {
		t.Errorf("delivery address = %+v, want the company's", o.DeliveryAddress)
	}
	l := o.Lines[0]
	if deref(l.SupplierItemNumber) != "NS-88" || l.UnitPrice != "12.50" || l.PackagingId == nil || *l.PackagingId != packBox ||
		l.Quantity != "30" || l.OrderQuantity != "3" || l.LineNet != "37.50" || l.ExpectedOn == nil {
		t.Errorf("line = %+v", l)
	}
	if len(l.Warnings) != 2 {
		t.Errorf("warnings = %v, want below_minimum and off_multiple", l.Warnings)
	}
	if o.NetTotal != "287.50" {
		t.Errorf("net total = %s, want 287.50", o.NetTotal)
	}
	german := createOrder(t, c, supGerman)
	if german.Currency != "EUR" || german.Language != "en" {
		t.Errorf("a German supplier's order = %s %s, want EUR en", german.Currency, german.Language)
	}
	// The variants were locked first, then the supplier.
	if len(h.catalog.lockCalls()) == 0 || len(h.dir.lockCalls()) == 0 {
		t.Errorf("locks: catalog %v, directory %v", h.catalog.lockCalls(), h.dir.lockCalls())
	}
}

// Every refusal of a draft's create and save.
func TestCreate_Refusals(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, buyer...)
	post := func(in gen.PurchasingOrderInput) *responseT { return c.Do(http.MethodPost, ordersPath, in) }
	expectRefusal(t, post(gen.PurchasingOrderInput{SupplierId: supInactive, SiteId: i64(siteMain)}), http.StatusConflict, "supplier_inactive", "inactive supplier")
	expectRefusal(t, post(gen.PurchasingOrderInput{SupplierId: 999, SiteId: i64(siteMain)}), http.StatusBadRequest, "supplier_not_found", "unknown supplier")
	expectRefusal(t, post(gen.PurchasingOrderInput{SupplierId: supNorsk}), http.StatusBadRequest, "site_not_found", "no site with two active")
	expectRefusal(t, post(gen.PurchasingOrderInput{SupplierId: supNorsk, SiteId: i64(siteArchived)}), http.StatusBadRequest, "site_archived", "archived site")
	expectRefusal(t, post(gen.PurchasingOrderInput{SupplierId: supNorsk, SiteId: i64(siteMain), Currency: str("KRONER")}), http.StatusBadRequest, "currency_invalid", "currency")
	lines := []gen.PurchasingLineInput{variantLine(varOld, "1"), variantLine(999, "1"), variantLine(varBolt, "0"), variantLine(varService, "2")}
	p := expectRefusal(t, post(gen.PurchasingOrderInput{SupplierId: supNorsk, SiteId: i64(siteMain), Lines: &lines}), http.StatusBadRequest, "variant_discontinued", "lines")
	if p.Lines == nil || len(*p.Lines) != 3 {
		t.Fatalf("failing lines = %+v, want three (the service is ordered)", p.Lines)
	}
	many := make([]gen.PurchasingLineInput, 501)
	for i := range many {
		many[i] = textLine("x", "1", "1")
	}
	expectRefusal(t, post(gen.PurchasingOrderInput{SupplierId: supNorsk, SiteId: i64(siteMain), Lines: &many}), http.StatusBadRequest, "too_many_lines", "501 lines")
}

// The order's states, each operation tried in each, with its refusal.
func TestOrder_StatesAsATable(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, buyer...)

	draft := createOrder(t, c, supNorsk, variantLine(varBolt, "4"))
	sent := markSent(t, c, createOrder(t, c, supNorsk, variantLine(varBolt, "4"), variantLine(varNut, "2")))
	closed := markSent(t, c, createOrder(t, c, supNorsk, variantLine(varBolt, "1")))
	res := c.Do(http.MethodPost, orderPath(closed.Id, "close"), version(closed))
	mustStatus(t, res, http.StatusOK, "close")
	closed = orderOf(t, res)
	cancelled := markSent(t, c, createOrder(t, c, supNorsk, variantLine(varBolt, "1")))
	res = c.Do(http.MethodPost, orderPath(cancelled.Id, "cancel"), gen.PurchasingCancelInput{ExpectedUpdatedAt: cancelled.UpdatedAt})
	mustStatus(t, res, http.StatusOK, "cancel")
	cancelled = getOrder(t, c, cancelled.Id)
	if closed.Status != gen.PurchasingOrderStatusClosed || cancelled.Status != gen.PurchasingOrderStatusCancelled || sent.Number == nil {
		t.Fatalf("states = %s %s, number %v", closed.Status, cancelled.Status, sent.Number)
	}
	for _, l := range closed.Lines {
		if l.ClosedAt == nil || l.Open != "0" {
			t.Errorf("a closed order's line %d is open: %+v", l.Id, l)
		}
	}

	type op struct {
		name   string
		do     func(o gen.PurchasingOrder) *responseT
		allows map[gen.PurchasingOrderStatus]bool
		status int
		code   string
	}
	ops := []op{
		{"delete", func(o gen.PurchasingOrder) *responseT {
			return c.Do(http.MethodDelete, withUpdatedAt(orderPath(o.Id), o.UpdatedAt), nil)
		}, map[gen.PurchasingOrderStatus]bool{gen.PurchasingOrderStatusDraft: true}, http.StatusConflict, "order_not_draft"},
		{"mark sent", func(o gen.PurchasingOrder) *responseT {
			return c.Do(http.MethodPost, orderPath(o.Id, "mark-sent"), gen.PurchasingMarkSentInput{ClientRequestId: uuid.New(), ExpectedUpdatedAt: o.UpdatedAt})
		}, map[gen.PurchasingOrderStatus]bool{gen.PurchasingOrderStatusDraft: true}, http.StatusConflict, "order_not_draft"},
		{"save", func(o gen.PurchasingOrder) *responseT { return c.Do(http.MethodPut, orderPath(o.Id), updateOf(o)) },
			map[gen.PurchasingOrderStatus]bool{gen.PurchasingOrderStatusDraft: true, gen.PurchasingOrderStatusSent: true}, http.StatusConflict, "order_not_sent"},
		{"confirm", func(o gen.PurchasingOrder) *responseT {
			return c.Do(http.MethodPut, orderPath(o.Id, "confirmation"), gen.PurchasingConfirmationInput{ExpectedUpdatedAt: o.UpdatedAt, SupplierReference: str("SO-1")})
		}, map[gen.PurchasingOrderStatus]bool{gen.PurchasingOrderStatusSent: true}, http.StatusConflict, "order_not_sent"},
		{"send again", func(o gen.PurchasingOrder) *responseT {
			return c.Do(http.MethodPost, orderPath(o.Id, "send-again"), gen.PurchasingSendAgainInput{ExpectedUpdatedAt: o.UpdatedAt, Mail: new(bool)})
		}, map[gen.PurchasingOrderStatus]bool{gen.PurchasingOrderStatusSent: true}, http.StatusConflict, "order_not_sent"},
		{"close", func(o gen.PurchasingOrder) *responseT {
			return c.Do(http.MethodPost, orderPath(o.Id, "close"), version(o))
		},
			map[gen.PurchasingOrderStatus]bool{gen.PurchasingOrderStatusSent: true}, http.StatusConflict, "order_not_sent"},
		{"reopen", func(o gen.PurchasingOrder) *responseT {
			return c.Do(http.MethodPost, orderPath(o.Id, "reopen"), version(o))
		},
			map[gen.PurchasingOrderStatus]bool{gen.PurchasingOrderStatusClosed: true}, http.StatusConflict, "order_not_closed"},
		{"cancel", func(o gen.PurchasingOrder) *responseT {
			return c.Do(http.MethodPost, orderPath(o.Id, "cancel"), gen.PurchasingCancelInput{ExpectedUpdatedAt: o.UpdatedAt})
		}, map[gen.PurchasingOrderStatus]bool{gen.PurchasingOrderStatusSent: true}, http.StatusConflict, "order_not_sent"},
	}
	for _, o := range []gen.PurchasingOrder{draft, sent, closed, cancelled} {
		for _, op := range ops {
			if op.allows[o.Status] {
				continue
			}
			fresh := getOrder(t, c, o.Id)
			expectRefusal(t, op.do(fresh), op.status, op.code, op.name+" of a "+string(o.Status)+" order")
		}
	}
	// What each allows, on fresh orders: a closed order reopens to sent.
	res = c.Do(http.MethodPost, orderPath(closed.Id, "reopen"), version(getOrder(t, c, closed.Id)))
	mustStatus(t, res, http.StatusOK, "reopen")
	if o := orderOf(t, res); o.Status != gen.PurchasingOrderStatusSent || o.Lines[0].ClosedAt != nil {
		t.Errorf("reopened = %s, line closed %v", o.Status, o.Lines[0].ClosedAt)
	}
	// A stale read is a version conflict.
	expectRefusal(t, c.Do(http.MethodPost, orderPath(sent.Id, "close"), gen.PurchasingVersionInput{ExpectedUpdatedAt: sent.UpdatedAt.Add(-1)}), http.StatusConflict, "version_conflict", "stale close")
	res = c.Do(http.MethodDelete, withUpdatedAt(orderPath(draft.Id), draft.UpdatedAt), nil)
	mustStatus(t, res, http.StatusNoContent, "delete a draft")
	expectRefusal(t, c.Do(http.MethodGet, orderPath(draft.Id), nil), http.StatusNotFound, "not_found", "the deleted draft")
}

// A sent order takes new lines and a line's quantity, price and day, every
// change logged; it keeps its supplier and currency, refuses a line left out
// or below what is received, and a closed line does not change.
func TestUpdate_SentOrderWithinItsLimits(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, buyer...)
	o := markSent(t, c, createOrder(t, c, supNorsk, variantLine(varBolt, "4"), variantLine(varNut, "10")))

	u := updateOf(o)
	u.Lines[0].OrderQuantity, u.Lines[0].UnitPrice = "6", str("3.25")
	u.Lines = append(u.Lines, textLine("Frakt", "1", "100"))
	u.Note = "Ring før levering"
	res := c.Do(http.MethodPut, orderPath(o.Id), u)
	mustStatus(t, res, http.StatusOK, "save a sent order")
	o = orderOf(t, res)
	if len(o.Lines) != 3 || o.Lines[0].Quantity != "6" || o.NetTotal != "119.50" {
		t.Fatalf("saved = %d lines, %s, %s", len(o.Lines), o.Lines[0].Quantity, o.NetTotal)
	}
	res = c.Do(http.MethodGet, orderPath(o.Id, "history"), nil)
	mustStatus(t, res, http.StatusOK, "history")
	var hist gen.PurchasingOrderHistory
	res.JSON(&hist)
	if len(hist.Changes) < 3 {
		t.Fatalf("changes = %d, want created, marked sent and the save", len(hist.Changes))
	}
	actions := map[string]int{}
	for _, a := range hist.Changes[0].Actions {
		actions[a.Action]++
	}
	if actions["line_added"] != 1 || actions["line_changed"] != 2 || actions["updated"] != 1 {
		t.Errorf("the save's actions = %v", actions)
	}

	u = updateOf(o)
	u.SupplierId = supGerman
	expectRefusal(t, c.Do(http.MethodPut, orderPath(o.Id), u), http.StatusConflict, "supplier_changed", "another supplier")
	u = updateOf(o)
	u.Currency = "EUR"
	expectRefusal(t, c.Do(http.MethodPut, orderPath(o.Id), u), http.StatusConflict, "currency_fixed", "another currency")
	u = updateOf(o)
	u.Lines = u.Lines[1:]
	expectRefusal(t, c.Do(http.MethodPut, orderPath(o.Id), u), http.StatusBadRequest, "line_not_found", "a line left out")

	h.Exec(t, `UPDATE purchasing.order_lines SET received = 5 WHERE id = $1`, o.Lines[0].Id)
	o = getOrder(t, c, o.Id)
	u = updateOf(o)
	u.Lines[0].OrderQuantity = "4"
	expectRefusal(t, c.Do(http.MethodPut, orderPath(o.Id), u), http.StatusBadRequest, "quantity_below_received", "below received")
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "cancel"), gen.PurchasingCancelInput{ExpectedUpdatedAt: o.UpdatedAt}), http.StatusConflict, "order_has_receipts", "cancel with a receipt")

	res = c.Do(http.MethodPost, orderPath(o.Id, "lines", itoa(o.Lines[1].Id), "close"), version(o))
	mustStatus(t, res, http.StatusOK, "close a line")
	o = orderOf(t, res)
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "lines", itoa(o.Lines[1].Id), "close"), version(o)), http.StatusConflict, "line_closed", "close it again")
	u = updateOf(o)
	u.Lines[1].OrderQuantity = "20"
	expectRefusal(t, c.Do(http.MethodPut, orderPath(o.Id), u), http.StatusConflict, "line_closed", "change a closed line")
}

// A draft's currency changes until a line has a price; its lines may be
// removed; its supplier may change, held FOR SHARE.
func TestUpdate_Draft(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, buyer...)
	o := createOrder(t, c, supNorsk, textLine("Kaffe", "1", "0"))
	u := updateOf(o)
	u.Currency = "EUR"
	u.SupplierId = supGerman
	res := c.Do(http.MethodPut, orderPath(o.Id), u)
	mustStatus(t, res, http.StatusOK, "change a free draft's currency and supplier")
	o = orderOf(t, res)
	if o.Currency != "EUR" || o.SupplierName != "Schrauben GmbH" {
		t.Errorf("saved = %s %s", o.Currency, o.SupplierName)
	}
	u = updateOf(o)
	u.Lines[0].UnitPrice = str("2")
	res = c.Do(http.MethodPut, orderPath(o.Id), u)
	mustStatus(t, res, http.StatusOK, "price a line")
	o = orderOf(t, res)
	u = updateOf(o)
	u.Currency = "NOK"
	expectRefusal(t, c.Do(http.MethodPut, orderPath(o.Id), u), http.StatusConflict, "currency_fixed", "currency after a price")
	u = updateOf(o)
	u.Lines = []gen.PurchasingLineInput{}
	res = c.Do(http.MethodPut, orderPath(o.Id), u)
	mustStatus(t, res, http.StatusOK, "remove the lines")
	if o = orderOf(t, res); len(o.Lines) != 0 {
		t.Errorf("lines = %d, want 0", len(o.Lines))
	}
	u = updateOf(o)
	u.SupplierId = supInactive
	expectRefusal(t, c.Do(http.MethodPut, orderPath(o.Id), u), http.StatusConflict, "supplier_inactive", "an inactive supplier")
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "mark-sent"), gen.PurchasingMarkSentInput{ClientRequestId: uuid.New(), ExpectedUpdatedAt: o.UpdatedAt}),
		http.StatusBadRequest, "order_empty", "send an empty order")
}

// The confirmation is a fact, and ends Unconfirmed; Late is an open line
// past its day; the list filters by both, and Copy makes a draft.
func TestConfirmLateListAndCopy(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, buyer...)
	o := createOrder(t, c, supNorsk, variantLine(varBolt, "4"), variantLine(varNut, "1"))
	h.Exec(t, `UPDATE purchasing.order_lines SET expected_on = $2 WHERE order_id = $1`, o.Id, h.Now().AddDate(0, 0, 1))
	o = markSent(t, c, getOrder(t, c, o.Id))
	if o.Late || o.Unconfirmed {
		t.Fatalf("a fresh order is late %v unconfirmed %v", o.Late, o.Unconfirmed)
	}
	// Eight days on: the order was sent, and its line expected, in the past.
	h.Exec(t, `UPDATE purchasing.orders SET sent_at = sent_at - interval '8 days' WHERE id = $1`, o.Id)
	h.Exec(t, `UPDATE purchasing.order_lines SET expected_on = $2 WHERE order_id = $1`, o.Id, h.Now().AddDate(0, 0, -7))
	o = getOrder(t, c, o.Id)
	if !o.Late || !o.Unconfirmed {
		t.Fatalf("after eight days late %v unconfirmed %v", o.Late, o.Unconfirmed)
	}
	var page gen.PurchasingOrderPage
	res := c.Do(http.MethodGet, ordersPath+"?late=true&unconfirmed=true&mine=true", nil)
	mustStatus(t, res, http.StatusOK, "list")
	res.JSON(&page)
	if page.Total != 1 || !page.Items[0].Late || !page.Items[0].Unconfirmed {
		t.Fatalf("list = %+v", page)
	}
	res = c.Do(http.MethodGet, "/api/v1/purchasing/summary", nil)
	mustStatus(t, res, http.StatusOK, "summary")
	var sum gen.PurchasingSummary
	res.JSON(&sum)
	if sum.Open != 1 || sum.Late != 1 || sum.Unconfirmed != 1 || sum.MineUnconfirmed != 1 {
		t.Errorf("summary = %+v", sum)
	}
	day := openapi_types.Date{Time: h.Now()}
	res = c.Do(http.MethodPut, orderPath(o.Id, "confirmation"), gen.PurchasingConfirmationInput{ExpectedUpdatedAt: o.UpdatedAt,
		SupplierReference: str("SO-77"), ConfirmedOn: &day, Lines: &[]gen.PurchasingConfirmationLine{{LineId: o.Lines[0].Id, ConfirmedOn: &day}}})
	mustStatus(t, res, http.StatusOK, "confirm")
	if o = orderOf(t, res); o.Unconfirmed || deref(o.SupplierReference) != "SO-77" || o.Lines[0].ConfirmedOn == nil {
		t.Errorf("confirmed = %+v", o)
	}
	res = c.Do(http.MethodGet, ordersPath+"?q=SO-77", nil)
	res.JSON(&page)
	if page.Total != 1 {
		t.Errorf("search by the supplier's reference = %d", page.Total)
	}
	expectRefusal(t, c.Do(http.MethodPut, orderPath(o.Id, "confirmation"), gen.PurchasingConfirmationInput{ExpectedUpdatedAt: o.UpdatedAt,
		Lines: &[]gen.PurchasingConfirmationLine{{LineId: 1}}}), http.StatusBadRequest, "line_not_found", "an unknown line")

	h.catalog.update(varNut, func(v *contracts.VariantEntry) { v.ProductStatus = "Discontinued" })
	res = c.Do(http.MethodPost, orderPath(o.Id, "copy"), nil)
	mustStatus(t, res, http.StatusCreated, "copy")
	cp := orderOf(t, res)
	if cp.Status != gen.PurchasingOrderStatusDraft || len(cp.Lines) != 1 || cp.CopiedFromId == nil || *cp.CopiedFromId != o.Id {
		t.Errorf("copy = %s, %d lines (the discontinued one left out), from %v", cp.Status, len(cp.Lines), cp.CopiedFromId)
	}
}

// Every operation asks its key (§8).
func TestPermissions_PerOperation(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	owner := h.SignIn(t, buyer...)
	o := createOrder(t, owner, supNorsk, variantLine(varBolt, "1"))
	r := h.SignIn(t, reader...)
	m := h.SignIn(t, manager...)
	mustStatus(t, r.Do(http.MethodGet, orderPath(o.Id), nil), http.StatusOK, "a reader reads")
	mustStatus(t, r.Do(http.MethodPost, ordersPath, gen.PurchasingOrderInput{SupplierId: supNorsk}), http.StatusForbidden, "a reader creates")
	mustStatus(t, m.Do(http.MethodPost, orderPath(o.Id, "mark-sent"), gen.PurchasingMarkSentInput{ClientRequestId: uuid.New(), ExpectedUpdatedAt: o.UpdatedAt}),
		http.StatusForbidden, "a manager sends")
	mustStatus(t, m.Do(http.MethodPost, itemsPath, gen.PurchasingSupplierItemInput{SupplierId: supNorsk, VariantId: varNut}), http.StatusForbidden, "a manager writes items")
	mustStatus(t, owner.Do(http.MethodGet, "/api/v1/purchasing/settings", nil), http.StatusForbidden, "a buyer reads the settings")
	mustStatus(t, h.SignIn(t).Do(http.MethodGet, ordersPath, nil), http.StatusForbidden, "no key")
	a := h.SignIn(t, admin...)
	mustStatus(t, a.Do(http.MethodGet, "/api/v1/purchasing/settings", nil), http.StatusOK, "the admin reads the settings")

	// Every operation of the contract, under each key alone: refused
	// unless the operation asks that very key — so orders-manage is refused
	// Send, Send again, Cancel and Mark as sent, and access every write.
	doc, err := openapi.Load(context.Background(), "purchasing")
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"purchasing:access", "purchasing:orders-manage", "purchasing:orders-send", "purchasing:supplier-items-manage", "purchasing:settings"}
	clients := map[string]*modtest.Client{}
	for _, k := range keys {
		clients[k] = h.SignIn(t, k)
	}
	param := regexp.MustCompile(`\{[^}]+\}`)
	operations := 0
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			rule, _ := op.Extensions["x-vantigo-access"].(string)
			asked, ok := strings.CutPrefix(rule, "permission:")
			if !ok {
				t.Errorf("%s %s asks no permission", method, path)
				continue
			}
			operations++
			target := param.ReplaceAllString(path, itoa(o.Id))
			var body any
			if op.RequestBody != nil {
				body = map[string]any{}
			}
			for _, k := range keys {
				if k == asked {
					continue
				}
				if res := clients[k].Do(method, target, body); res.Status != http.StatusForbidden {
					t.Errorf("%s %s (%s) with only %s = %d, want 403", method, path, op.OperationID, k, res.Status)
				}
			}
		}
	}
	if operations < 32 {
		t.Errorf("covered %d operations, want every one of the contract's 32", operations)
	}
}

// The reads around an order: sites, suppliers, buyers, meta and the catalog
// search with the supplier's item.
func TestReads(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, buyer...)
	mustStatus(t, c.Do(http.MethodPost, itemsPath, gen.PurchasingSupplierItemInput{SupplierId: supNorsk, VariantId: varNut, Price: str("1")}), http.StatusCreated, "item")
	createOrder(t, c, supNorsk, variantLine(varNut, "1"))

	var sites gen.PurchasingSiteList
	res := c.Do(http.MethodGet, "/api/v1/purchasing/sites", nil)
	mustStatus(t, res, http.StatusOK, "sites")
	res.JSON(&sites)
	if len(sites.Items) != 3 {
		t.Errorf("sites = %d", len(sites.Items))
	}
	var sups gen.PurchasingSupplierList
	res = c.Do(http.MethodGet, "/api/v1/purchasing/suppliers?q=norsk", nil)
	mustStatus(t, res, http.StatusOK, "suppliers")
	res.JSON(&sups)
	if len(sups.Items) != 1 || sups.Items[0].SupplierItemCount != 1 {
		t.Errorf("suppliers = %+v", sups.Items)
	}
	h.dir.update(supNorsk, func(e *contracts.SupplierEntry) { e.Revision = 7 })
	res = c.Do(http.MethodGet, "/api/v1/purchasing/suppliers/501", nil)
	mustStatus(t, res, http.StatusOK, "supplier")
	var sup gen.PurchasingSupplier
	res.JSON(&sup)
	if sup.Revision != 7 {
		t.Errorf("the supplier's revision = %d, want the register's 7", sup.Revision)
	}
	expectRefusal(t, c.Do(http.MethodGet, "/api/v1/purchasing/suppliers/999", nil), http.StatusNotFound, "not_found", "unknown supplier")
	var buyers gen.PurchasingBuyerList
	res = c.Do(http.MethodGet, "/api/v1/purchasing/buyers", nil)
	mustStatus(t, res, http.StatusOK, "buyers")
	res.JSON(&buyers)
	if len(buyers.Items) == 0 {
		t.Error("no buyers")
	}
	var meta gen.PurchasingMeta
	res = c.Do(http.MethodGet, "/api/v1/purchasing/meta", nil)
	mustStatus(t, res, http.StatusOK, "meta")
	res.JSON(&meta)
	if meta.SendingAvailable || meta.BaseCurrency != "NOK" || meta.PriceTolerancePercent != "2" {
		t.Errorf("meta = %+v", meta)
	}
	var variants gen.PurchasingVariantList
	res = c.Do(http.MethodGet, "/api/v1/purchasing/variants?supplierId=501&onlySupplierItems=true", nil)
	mustStatus(t, res, http.StatusOK, "variants")
	res.JSON(&variants)
	if len(variants.Items) != 1 || variants.Items[0].SupplierItem == nil {
		t.Errorf("the supplier's variants = %+v", variants.Items)
	}
	res = c.Do(http.MethodGet, "/api/v1/purchasing/variants?q=M8&supplierId=501", nil)
	mustStatus(t, res, http.StatusOK, "search")
	res.JSON(&variants)
	if len(variants.Items) != 2 {
		t.Errorf("search = %d", len(variants.Items))
	}
}
