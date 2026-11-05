package orders_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// keepAt is o's lines as a save keeps them, the line of variant at qty.
func keepAt(o gen.OrdersOrder, variant int32, qty float64) []gen.OrdersLineInput {
	lines := keep(o)
	for i := range lines {
		if lines[i].VariantId != nil && *lines[i].VariantId == variant {
			lines[i].Quantity = f64(qty)
		}
	}
	return lines
}

// A confirmed order's kept line keeps what was confirmed (O16, O26): its SKU,
// name, unit and VAT stay when the catalog renames the variant or maps its
// category to another code, and a save is not refused when the variant is
// deleted; a new unit is refused. A draft follows the catalog.
func TestConfirmedSave_KeepsAKeptLineFrozen(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 1), productLine(varFood, 1)))
	draft := createOrder(t, c, custBusiness, productLine(varBolt, 1))

	renamed := contracts.VariantEntry{ID: varBolt, ProductID: varBolt, ProductName: "Bolt M8 Pro", SKU: "BOLT-M8-PRO", Unit: "box",
		ProductType: "Goods", ProductStatus: "Active", TaxCategoryID: catStandard}
	h.catalog.seed(renamed)
	h.tax.mapCategory(catStandard, codeFood)

	res := save(c, o, keepAt(o, varBolt, 2), nil)
	mustStatus(t, res, http.StatusOK, "save a kept line")
	got := orderOf(t, res)
	l := lineOf(t, got, varBolt)
	if deref(l.Sku) != "BOLT-M8" || deref(l.Name) != "Bolt M8" || l.Unit != "pcs" || deref(l.VatCode) != "3" ||
		l.VatRatePercent == nil || *l.VatRatePercent != 25 || l.Quantity == nil || *l.Quantity != 2 {
		t.Errorf("kept line %+v; want the confirmed SKU, name, unit and 25 %% VAT at quantity 2", l)
	}

	h.catalog.remove(varFood)
	res = save(c, got, keepAt(got, varBolt, 3), nil)
	mustStatus(t, res, http.StatusOK, "save with a kept line's variant deleted")
	got = orderOf(t, res)
	if f := lineOf(t, got, varFood); deref(f.Sku) != "FOOD-1" {
		t.Errorf("food line %+v; want it kept as confirmed", f)
	}
	lines := keep(got)
	lines[0].Unit = str("kg")
	expectRefusal(t, save(c, got, lines, nil), http.StatusBadRequest, "line_invalid", "a kept line's unit")

	res = save(c, draft, keep(draft), nil)
	mustStatus(t, res, http.StatusOK, "save the draft")
	if l := lineOf(t, orderOf(t, res), varBolt); deref(l.Sku) != "BOLT-M8-PRO" || deref(l.VatCode) != "31" {
		t.Errorf("draft line %+v; want it to follow the catalog", l)
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// A kept line never changes kind (line_kind_changed), on a quote, a draft
// order and a confirmed order alike.
func TestSave_ALineKeepsItsKind(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, append(boss, "orders:quotes-manage")...)
	q := createQuote(t, c, custBusiness, productLine(varBolt, 1))
	note := gen.OrdersLineInput{Id: &q.Lines[0].Id, Kind: gen.OrdersLineInputKindNote, Description: str("Now a note")}
	expectRefusal(t, c.Do(http.MethodPut, quotePath(q.Id), gen.OrdersQuoteUpdate{
		ExpectedUpdatedAt: q.UpdatedAt, Revision: q.Revision, CustomerId: custBusiness, Lines: []gen.OrdersLineInput{note},
	}), http.StatusConflict, "line_kind_changed", "quote")

	for _, confirm := range []bool{false, true} {
		o := createOrder(t, c, custBusiness, productLine(varBolt, 1))
		if confirm {
			o = confirmOrder(t, c, o)
		}
		note := gen.OrdersLineInput{Id: &o.Lines[0].Id, Kind: gen.OrdersLineInputKindNote, Description: str("Now a note")}
		expectRefusal(t, save(c, o, []gen.OrdersLineInput{note}, nil), http.StatusConflict, "line_kind_changed", "order")
	}
}

// Every write of a quote or an order sends the updatedAt it read
// (expectedUpdatedAt): a write made from an older read is refused with
// version_conflict, a delete too, even when the version did not move.
func TestWrites_RefuseAStaleUpdatedAt(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, append(boss, "orders:quotes-manage")...)
	q := createQuote(t, c, custBusiness, productLine(varBolt, 1))
	kept := []gen.OrdersLineInput{{Id: &q.Lines[0].Id, Kind: gen.OrdersLineInputKindProduct, VariantId: i32(varBolt), Quantity: f64(1)}}
	h.Advance(time.Second)
	res := c.Do(http.MethodPut, quotePath(q.Id), gen.OrdersQuoteUpdate{ExpectedUpdatedAt: q.UpdatedAt, Revision: 1, CustomerId: custBusiness, Lines: kept})
	mustStatus(t, res, http.StatusOK, "save")
	if saved := quoteOf(t, res); !saved.UpdatedAt.After(q.UpdatedAt) {
		t.Fatalf("updatedAt %v did not move past %v", saved.UpdatedAt, q.UpdatedAt)
	}
	expectRefusal(t, c.Do(http.MethodPut, quotePath(q.Id), gen.OrdersQuoteUpdate{ExpectedUpdatedAt: q.UpdatedAt, Revision: 1, CustomerId: custBusiness, Lines: kept}),
		http.StatusConflict, "version_conflict", "a draft saved from an older read")
	expectRefusal(t, c.Do(http.MethodPost, quotePath(q.Id, "mark-sent"), gen.OrdersMarkSentInput{ExpectedUpdatedAt: q.UpdatedAt, Revision: 1}),
		http.StatusConflict, "version_conflict", "mark sent from an older read")
	expectRefusal(t, c.Do(http.MethodDelete, withUpdatedAt(quotePath(q.Id), q.UpdatedAt), nil), http.StatusConflict, "version_conflict", "delete a quote")

	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 1)))
	h.Advance(time.Second)
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "lock"), gen.OrdersVersionInput{ExpectedUpdatedAt: o.UpdatedAt, Version: o.Version}), http.StatusOK, "lock")
	// The lock left the version at 1, and still makes the read stale.
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "hold"), gen.OrdersHoldInput{ExpectedUpdatedAt: o.UpdatedAt, Version: o.Version}),
		http.StatusConflict, "version_conflict", "hold from a read before the lock")
	d := createOrder(t, c, custBusiness, productLine(varBolt, 1))
	expectRefusal(t, c.Do(http.MethodDelete, withUpdatedAt(orderPath(d.Id), d.UpdatedAt.Add(-time.Second)), nil), http.StatusConflict, "version_conflict", "delete an order")
	mustStatus(t, c.Do(http.MethodDelete, withUpdatedAt(orderPath(d.Id), d.UpdatedAt), nil), http.StatusNoContent, "delete an order")
}

// A locked confirmed order is not cancelled (order_locked), and a held one
// takes no delivery (order_on_hold).
func TestCancelNeedsUnlockAndDeliveryNeedsRelease(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 2)))
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "lock"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: 1}), http.StatusOK, "lock")
	expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, "cancel"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: 1}),
		http.StatusConflict, "order_locked", "cancel a locked order")
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "unlock"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: 1}), http.StatusOK, "unlock")
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "hold"), gen.OrdersHoldInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: 1}), http.StatusOK, "hold")
	for _, path := range []string{"mark-delivered", "undo-delivered"} {
		expectRefusal(t, c.Do(http.MethodPost, orderPath(o.Id, path), gen.OrdersDeliveredInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: 1,
			Lines: []gen.OrdersDeliveredLine{{LineId: o.Lines[0].Id, Quantity: 1}}}), http.StatusConflict, "order_on_hold", path)
	}
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "release"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: 1}), http.StatusOK, "release")
	mustStatus(t, c.Do(http.MethodPost, orderPath(o.Id, "cancel"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, o.Id), Version: 1}), http.StatusOK, "cancel")
}

// Deleting a draft accepted from a quote puts the quote back to sent, so it
// is accepted again.
func TestDeleteDraftOrder_PutsItsQuoteBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, append(boss, "orders:quotes-manage")...)
	q := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 1)))
	res := c.Do(http.MethodPost, quotePath(q.Id, "accept"), gen.OrdersAcceptInput{ExpectedUpdatedAt: q.UpdatedAt, Revision: 1, Into: gen.DraftOrder})
	mustStatus(t, res, http.StatusOK, "accept")
	var a gen.OrdersAccepted
	res.JSON(&a)
	mustStatus(t, c.Do(http.MethodDelete, withUpdatedAt(orderPath(a.Order.Id), a.Order.UpdatedAt), nil), http.StatusNoContent, "delete the draft")
	res = c.Do(http.MethodGet, quotePath(q.Id), nil)
	back := quoteOf(t, res)
	if back.Status != gen.OrdersQuoteStatusSent || back.AcceptedInto != nil || back.AcceptedAt != nil {
		t.Fatalf("quote %+v; want it sent again, accepted into nothing", back)
	}
	mustStatus(t, c.Do(http.MethodPost, quotePath(q.Id, "accept"), gen.OrdersAcceptInput{ExpectedUpdatedAt: back.UpdatedAt, Revision: 1, Into: gen.DraftOrder}),
		http.StatusOK, "accept again")
	if n := eventsNamed(t, h, contracts.QuoteAccepted{}.EventName(), q.Id); n != 2 {
		t.Errorf("%d accepted events, want one per accept", n)
	}
}

// Revise locks the quote's variants, and so do marking and undoing delivery,
// which open a line again (MB rule 21, O30).
func TestReviseAndDelivery_LockTheVariants(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, append(boss, "orders:quotes-manage")...)
	q := markSent(t, c, createQuote(t, c, custBusiness, productLine(varFood, 1)))
	before := len(h.catalog.lockCalls())
	mustStatus(t, c.Do(http.MethodPost, quotePath(q.Id, "revise"), gen.OrdersRevisionInput{ExpectedUpdatedAt: q.UpdatedAt, Revision: 1}), http.StatusOK, "revise")
	if calls := h.catalog.lockCalls()[before:]; !slices.ContainsFunc(calls, func(ids []int32) bool { return slices.Contains(ids, varFood) }) {
		t.Errorf("revise locked %v; want the quote's variant", calls)
	}
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varFood, 2)))
	before = len(h.catalog.lockCalls())
	deliver(t, c, o, o.Lines[0].Id, 1)
	if calls := h.catalog.lockCalls()[before:]; !slices.ContainsFunc(calls, func(ids []int32) bool { return slices.Contains(ids, varFood) }) {
		t.Errorf("mark delivered locked %v; want the order's variant", calls)
	}
}

// Done is confirmed with every product line's cancelled and invoiced
// covering what was ordered: an order cancelled in what remains is done; one
// delivered and not invoiced is not. The list and the retention agree.
func TestDone_CountsACancelledRemainder(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	whole := confirmOrder(t, c, createOrder(t, c, custPerson, productLine(varBolt, 2)))
	res := c.Do(http.MethodPost, orderPath(whole.Id, "cancel-remaining"), gen.OrdersVersionInput{ExpectedUpdatedAt: whole.UpdatedAt, Version: 1})
	mustStatus(t, res, http.StatusOK, "cancel remaining")
	if !orderOf(t, res).Done {
		t.Error("an order cancelled whole in what remains is not done")
	}
	delivered := confirmOrder(t, c, createOrder(t, c, custPerson, productLine(varBolt, 2)))
	if got := deliver(t, c, delivered, delivered.Lines[0].Id, 2); got.Done {
		t.Error("a delivered, uninvoiced order is done")
	}
	res = c.Do(http.MethodGet, ordersPath+"?done=true", nil)
	mustStatus(t, res, http.StatusOK, "list done")
	var page gen.OrdersOrderPage
	res.JSON(&page)
	if len(page.Items) != 1 || page.Items[0].Id != whole.Id || !page.Items[0].Done {
		t.Errorf("done list %+v; want only the cancelled remainder", page.Items)
	}

	data := orders.Module().CustomerPersonalData(h.Deps())
	inTx(t, h, func(ctx context.Context, tx pgx.Tx) error {
		_, err := data.EraseCustomerData(ctx, tx, custPerson)
		return err
	})
	h.Exec(t, `UPDATE orders.orders SET updated_at = '2020-03-01T12:00:00Z' WHERE customer_id = $1`, custPerson)
	if _, n, err := orders.RunRetentionCycle(context.Background(), h.Deps()); err != nil || n != 1 {
		t.Fatalf("retention: %d, %v; want the done order only", n, err)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.orders WHERE id = $1 AND anonymised_at IS NOT NULL`, whole.Id); n != 1 {
		t.Error("the done order was not anonymised")
	}
}

// The erase keeps a confirmed order's stored PDFs through its retention and
// the retention worker deletes the stored PDFs of what was anonymised: a sent
// quote's on its next cycle, the order's once its retention has run.
func TestErase_DeletesTheStoredPDFsOfWhatWasAnonymised(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, append(boss, "orders:quotes-manage")...)
	q := markSent(t, c, createQuote(t, c, custPerson, productLine(varBolt, 1)))
	o := confirmOrder(t, c, createOrder(t, c, custPerson, productLine(varBolt, 1)))
	docs := func(kind string, id int64) int {
		return h.Count(t, `SELECT count(*) FROM orders.documents WHERE owner_kind = $1 AND owner_id = $2`, kind, id)
	}
	if docs("quote", q.Id) != 1 || docs("order", o.Id) != 1 || len(h.objects.keys()) != 2 {
		t.Fatalf("stored %d quote, %d order PDFs and %d objects; want one each", docs("quote", q.Id), docs("order", o.Id), len(h.objects.keys()))
	}
	data := orders.Module().CustomerPersonalData(h.Deps())
	inTx(t, h, func(ctx context.Context, tx pgx.Tx) error {
		_, err := data.EraseCustomerData(ctx, tx, custPerson)
		return err
	})
	ctx := context.Background()
	if _, _, err := orders.RunRetentionCycle(ctx, h.Deps()); err != nil {
		t.Fatal(err)
	}
	if docs("quote", q.Id) != 0 || docs("order", o.Id) != 1 || len(h.objects.keys()) != 1 {
		t.Fatalf("after the first cycle: %d quote, %d order PDFs, %d objects; want the order's alone", docs("quote", q.Id), docs("order", o.Id), len(h.objects.keys()))
	}
	h.Exec(t, `UPDATE orders.orders SET status = 'cancelled', cancelled_at = '2020-03-01T12:00:00Z' WHERE id = $1`, o.Id)
	if _, _, err := orders.RunRetentionCycle(ctx, h.Deps()); err != nil {
		t.Fatal(err)
	}
	if docs("order", o.Id) != 0 || len(h.objects.keys()) != 0 {
		t.Errorf("after the retention: %d order PDFs, %d objects; want none", docs("order", o.Id), len(h.objects.keys()))
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.erasures WHERE completed_at IS NOT NULL`); n != 1 {
		t.Error("the erasure was not completed")
	}
}

// The confirm stores the confirmation of the version it produced. A queued
// confirmation whose version the order moved past goes out at the version
// the order is at, logged; a cancelled order's and one with no recipient
// fail at once; a confirm whose order is cancelled before the delivery runs
// queues nothing.
func TestConfirmationMail_Versions(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	chooseChannel(t, h)
	c := h.SignIn(t, boss...)
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 2)))
	if n := h.Count(t, `SELECT count(*) FROM orders.documents WHERE owner_kind = 'order' AND owner_id = $1 AND version = 1`, o.Id); n != 1 {
		t.Fatalf("%d confirmations stored at version 1, want 1", n)
	}
	res := save(c, o, keepAt(o, varBolt, 3), nil)
	mustStatus(t, res, http.StatusOK, "edit")
	h.Exec(t, `DELETE FROM orders.documents WHERE owner_kind = 'order' AND owner_id = $1`, o.Id)
	ctx := context.Background()
	if _, err := orders.QueueOrderMailing(ctx, h.Deps(), o.Id, 1, "kari@acme.example", uuid.New()); err != nil {
		t.Fatal(err)
	}
	if n, _ := orders.RunMailCycle(ctx, h.Deps()); n != 1 {
		t.Fatalf("claimed %d, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.mailings m JOIN orders.documents d ON d.id = m.document_id
		WHERE m.owner_id = $1 AND m.status = 'sent' AND m.version = 2 AND d.version = 2`, o.Id); n != 1 {
		t.Errorf("%d mailings sent at version 2, want the superseded one", n)
	}
	if !strings.Contains(h.Logs(), "superseded") {
		t.Error("the superseded version was not logged")
	}

	if _, err := orders.QueueOrderMailing(ctx, h.Deps(), o.Id, 2, "", uuid.New()); err != nil {
		t.Fatal(err)
	}
	cancelled := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 1)))
	if _, err := orders.QueueOrderMailing(ctx, h.Deps(), cancelled.Id, 1, "kari@acme.example", uuid.New()); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, c.Do(http.MethodPost, orderPath(cancelled.Id, "cancel"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, cancelled.Id), Version: 1}), http.StatusOK, "cancel")
	if n, _ := orders.RunMailCycle(ctx, h.Deps()); n != 2 {
		t.Fatalf("claimed %d, want 2", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.mailings WHERE status = 'failed' AND attempts = 1`); n != 2 {
		t.Errorf("%d mailings failed at once, want the empty recipient's and the cancelled order's", n)
	}
	if len(h.mail.Sent()) != 1 {
		t.Errorf("%d mails sent, want the superseded one only", len(h.mail.Sent()))
	}

	// The handler skips a confirm whose order was cancelled before it ran.
	admin := h.SignIn(t, settingsKey...)
	ch := h.channel
	mustStatus(t, admin.Do(http.MethodPut, settingsPath, gen.OrdersSettingsInput{
		Revision: 1, QuoteSeriesStart: 1, OrderSeriesStart: 1, QuoteValidityDays: 30, DefaultPaymentTermsDays: 14,
		MailTexts: []gen.OrdersMailText{}, SendChannelId: &ch, ConfirmationOnConfirm: true,
	}), http.StatusOK, "settings")
	gone := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 1)))
	mustStatus(t, c.Do(http.MethodPost, orderPath(gone.Id, "cancel"), gen.OrdersVersionInput{ExpectedUpdatedAt: orderAt(t, c, gone.Id), Version: 1}), http.StatusOK, "cancel")
	dispatch := events.NewDispatcher(h.Pool(), orders.Module().EventHandlers(h.Deps()), h.Deps().Logger, h.Now)
	for range 2 {
		if err := dispatch.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.mailings WHERE owner_kind = 'order' AND owner_id = $1`, gone.Id); n != 0 {
		t.Errorf("%d mailings queued for an order cancelled before the delivery, want none", n)
	}
}
