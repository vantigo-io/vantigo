package orders_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

const warningsPath = "/api/v1/orders/warnings"

func customerWarningPath(id int32) string { return fmt.Sprintf("%s/customers/%d", warningsPath, id) }
func productWarningPath(id int32) string  { return fmt.Sprintf("%s/products/%d", warningsPath, id) }

// setWarning sets a warning through the API as a settings holder.
func setWarning(t *testing.T, c *modtest.Client, path string, level gen.OrdersWarningInputLevel, text string) gen.OrdersWarning {
	t.Helper()
	res := c.Do(http.MethodPut, path, gen.OrdersWarningInput{Level: level, Text: text})
	mustStatus(t, res, http.StatusOK, "set warning")
	var w gen.OrdersWarning
	res.JSON(&w)
	return w
}

// A warning is set, read, listed and removed under orders:settings, each with
// its subject's name as it was; only orders:access reads them (O33).
func TestWarnings_SetReadListRemove(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	admin := h.SignIn(t, settingsKey...)
	reader := h.SignIn(t, reader...)

	mustStatus(t, reader.Do(http.MethodPut, customerWarningPath(custBusiness), gen.OrdersWarningInput{Level: "info", Text: "x"}), http.StatusForbidden, "a reader sets")
	w := setWarning(t, admin, customerWarningPath(custBusiness), gen.OrdersWarningInputLevelInfo, "  Ring før levering  ")
	if w.Scope != gen.OrdersWarningScopeCustomer || w.Id != custBusiness || w.Name != "Acme AS" || w.Text != "Ring før levering" ||
		w.Level != gen.OrdersWarningLevelInfo || w.UpdatedBy == nil {
		t.Errorf("customer warning %+v", w)
	}
	w = setWarning(t, admin, customerWarningPath(custBusiness), gen.OrdersWarningInputLevelBlock, "Betaler ikke")
	if w.Level != gen.OrdersWarningLevelBlock || w.Text != "Betaler ikke" {
		t.Errorf("replaced warning %+v", w)
	}
	p := setWarning(t, admin, productWarningPath(varBolt), gen.OrdersWarningInputLevelInfo, "Utgående vare")
	if p.Scope != gen.OrdersWarningScopeProduct || p.Id != varBolt || p.Name != "Bolt M8" {
		t.Errorf("product warning %+v", p)
	}

	res := reader.Do(http.MethodGet, customerWarningPath(custBusiness), nil)
	mustStatus(t, res, http.StatusOK, "a reader reads")
	mustStatus(t, reader.Do(http.MethodGet, productWarningPath(varBolt), nil), http.StatusOK, "read the product's")
	res = reader.Do(http.MethodGet, warningsPath, nil)
	mustStatus(t, res, http.StatusOK, "list")
	var list gen.OrdersWarningList
	res.JSON(&list)
	if len(list.Items) != 2 || list.Items[0].Scope != gen.OrdersWarningScopeCustomer || list.Items[1].Scope != gen.OrdersWarningScopeProduct {
		t.Errorf("list %+v; want the customer's, then the product's", list.Items)
	}
	res = reader.Do(http.MethodGet, warningsPath+"?scope=product", nil)
	mustStatus(t, res, http.StatusOK, "list products")
	res.JSON(&list)
	if len(list.Items) != 1 || list.Items[0].Id != varBolt {
		t.Errorf("products %+v", list.Items)
	}

	expectRefusal(t, admin.Do(http.MethodPut, customerWarningPath(custBusiness), gen.OrdersWarningInput{Level: "info", Text: "   "}), http.StatusBadRequest, "", "an empty text")
	expectRefusal(t, admin.Do(http.MethodPut, customerWarningPath(custBusiness), gen.OrdersWarningInput{Level: "info", Text: strings.Repeat("x", 501)}), http.StatusBadRequest, "", "a long text")
	mustStatus(t, admin.Do(http.MethodPut, customerWarningPath(9999), gen.OrdersWarningInput{Level: "info", Text: "x"}), http.StatusNotFound, "no such customer")
	mustStatus(t, admin.Do(http.MethodPut, productWarningPath(9999), gen.OrdersWarningInput{Level: "info", Text: "x"}), http.StatusNotFound, "no such product")

	mustStatus(t, reader.Do(http.MethodDelete, customerWarningPath(custBusiness), nil), http.StatusForbidden, "a reader removes")
	mustStatus(t, admin.Do(http.MethodDelete, customerWarningPath(custBusiness), nil), http.StatusNoContent, "remove")
	mustStatus(t, admin.Do(http.MethodDelete, customerWarningPath(custBusiness), nil), http.StatusNotFound, "remove again")
	mustStatus(t, admin.Do(http.MethodDelete, productWarningPath(varBolt), nil), http.StatusNoContent, "remove the product's")
	mustStatus(t, reader.Do(http.MethodGet, customerWarningPath(custBusiness), nil), http.StatusNotFound, "read a removed one")
	mustStatus(t, reader.Do(http.MethodGet, productWarningPath(varBolt), nil), http.StatusNotFound, "read a removed product's")
}

// A quote and an order carry the warnings of their customer and the products
// on their lines, and no other (O33).
func TestWarnings_OnDocuments(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	admin := h.SignIn(t, settingsKey...)
	c := h.SignIn(t, append(boss, "orders:quotes-manage")...)
	setWarning(t, admin, customerWarningPath(custBusiness), gen.OrdersWarningInputLevelInfo, "Ring før levering")
	setWarning(t, admin, productWarningPath(varBolt), gen.OrdersWarningInputLevelBlock, "Tilbakekalt")
	setWarning(t, admin, productWarningPath(varFood), gen.OrdersWarningInputLevelInfo, "Ikke på denne")

	q := createQuote(t, c, custBusiness, productLine(varBolt, 1))
	if len(q.Warnings) != 2 || q.Warnings[0].Scope != gen.OrdersWarningScopeCustomer || q.Warnings[1].Id != varBolt ||
		q.Lines[0].ProductId == nil || *q.Lines[0].ProductId != varBolt {
		t.Errorf("quote warnings %+v, line product %v", q.Warnings, q.Lines[0].ProductId)
	}
	o := createOrder(t, c, custPerson, productLine(varFood, 1))
	if len(o.Warnings) != 1 || o.Warnings[0].Id != varFood {
		t.Errorf("order warnings %+v; want only the coffee's", o.Warnings)
	}
	res := c.Do(http.MethodGet, quotePath(q.Id), nil)
	mustStatus(t, res, http.StatusOK, "get")
	if got := quoteOf(t, res); len(got.Warnings) != 2 {
		t.Errorf("read quote warnings %+v", got.Warnings)
	}
}

// A block warning refuses mark as sent, send, confirm and accept into a
// confirmed order (blocked_by_warning, with its text); an info warning never
// does. Only an orders:settings holder overrides one, with a reason, which a
// quote's internal note and an order's change log keep (O33).
func TestWarnings_BlockAndOverride(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	chooseChannel(t, h)
	admin := h.SignIn(t, settingsKey...)
	seller := h.SignIn(t, append(boss, "orders:quotes-manage")...)
	chief := h.SignIn(t, append(boss, "orders:quotes-manage", "orders:settings")...)
	setWarning(t, admin, customerWarningPath(custBusiness), gen.OrdersWarningInputLevelBlock, "Betaler ikke")

	// The quote.
	q := createQuote(t, seller, custBusiness, productLine(varBolt, 1))
	p := expectRefusal(t, seller.Do(http.MethodPost, quotePath(q.Id, "mark-sent"), gen.OrdersMarkSentInput{
		ExpectedUpdatedAt: q.UpdatedAt, Revision: q.Revision,
	}), http.StatusConflict, "blocked_by_warning", "mark sent")
	if p.Detail == nil || !strings.Contains(*p.Detail, "Betaler ikke") {
		t.Errorf("detail %v; want the warning's text", p.Detail)
	}
	res := seller.Do(http.MethodPost, quotePath(q.Id, "send"), gen.OrdersSendInput{
		ExpectedUpdatedAt: q.UpdatedAt, Revision: &q.Revision, Recipient: "kari@acme.example",
	})
	expectRefusal(t, res, http.StatusConflict, "blocked_by_warning", "send")
	if n := len(h.mail.Sent()); n != 0 {
		t.Errorf("%d mails went out for a blocked quote", n)
	}
	yes := true
	expectRefusal(t, seller.Do(http.MethodPost, quotePath(q.Id, "mark-sent"), gen.OrdersMarkSentInput{
		ExpectedUpdatedAt: q.UpdatedAt, Revision: q.Revision, OverrideWarnings: &yes, OverrideReason: str("Forhåndsbetalt"),
	}), http.StatusConflict, "blocked_by_warning", "an override without orders:settings")
	expectRefusal(t, chief.Do(http.MethodPost, quotePath(q.Id, "mark-sent"), gen.OrdersMarkSentInput{
		ExpectedUpdatedAt: q.UpdatedAt, Revision: q.Revision, OverrideWarnings: &yes,
	}), http.StatusBadRequest, "", "an override without a reason")
	res = chief.Do(http.MethodPost, quotePath(q.Id, "mark-sent"), gen.OrdersMarkSentInput{
		ExpectedUpdatedAt: q.UpdatedAt, Revision: q.Revision, OverrideWarnings: &yes, OverrideReason: str("Forhåndsbetalt"),
	})
	mustStatus(t, res, http.StatusOK, "an override")
	if sent := quoteOf(t, res); sent.Status != gen.OrdersQuoteStatusSent || !strings.Contains(sent.InternalNote, "Forhåndsbetalt") ||
		!strings.Contains(sent.InternalNote, "Betaler ikke") {
		t.Errorf("overridden quote %s, note %q", sent.Status, sent.InternalNote)
	}

	// The order.
	o := createOrder(t, seller, custBusiness, productLine(varBolt, 1))
	expectRefusal(t, seller.Do(http.MethodPost, orderPath(o.Id, "confirm"), gen.OrdersConfirmInput{
		ExpectedUpdatedAt: o.UpdatedAt, Version: o.Version,
	}), http.StatusConflict, "blocked_by_warning", "confirm")
	res = chief.Do(http.MethodPost, orderPath(o.Id, "confirm"), gen.OrdersConfirmInput{
		ExpectedUpdatedAt: o.UpdatedAt, Version: o.Version, OverrideWarnings: &yes, OverrideReason: str("Avtalt med økonomi"),
	})
	mustStatus(t, res, http.StatusOK, "confirm with an override")
	res = chief.Do(http.MethodGet, orderPath(o.Id, "changes"), nil)
	mustStatus(t, res, http.StatusOK, "changes")
	var log gen.OrdersChangeList
	res.JSON(&log)
	found := false
	for _, e := range log.Items {
		for _, a := range e.Actions {
			if a.Kind == "warning_overridden" && a.To != nil && *a.To == "Avtalt med økonomi" && a.From != nil && strings.Contains(*a.From, "Betaler ikke") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("change log %+v; want the override", log.Items)
	}

	// Accept into a confirmed order is a confirm; into a draft it is not.
	q2 := createQuote(t, chief, custBusiness, productLine(varBolt, 1))
	res = chief.Do(http.MethodPost, quotePath(q2.Id, "mark-sent"), gen.OrdersMarkSentInput{
		ExpectedUpdatedAt: q2.UpdatedAt, Revision: q2.Revision, OverrideWarnings: &yes, OverrideReason: str("OK"),
	})
	mustStatus(t, res, http.StatusOK, "mark sent")
	q2 = quoteOf(t, res)
	expectRefusal(t, seller.Do(http.MethodPost, quotePath(q2.Id, "accept"), gen.OrdersAcceptInput{
		ExpectedUpdatedAt: q2.UpdatedAt, Revision: q2.Revision, Into: gen.OrdersAcceptInputIntoConfirmedOrder,
	}), http.StatusConflict, "blocked_by_warning", "accept and confirm")
	res = chief.Do(http.MethodPost, quotePath(q2.Id, "accept"), gen.OrdersAcceptInput{
		ExpectedUpdatedAt: q2.UpdatedAt, Revision: q2.Revision, Into: gen.OrdersAcceptInputIntoConfirmedOrder, OverrideWarnings: &yes, OverrideReason: str("OK"),
	})
	mustStatus(t, res, http.StatusOK, "accept and confirm with an override")

	// An info warning never blocks.
	setWarning(t, admin, customerWarningPath(custBusiness), gen.OrdersWarningInputLevelInfo, "Bare til info")
	confirmOrder(t, seller, createOrder(t, seller, custBusiness, productLine(varBolt, 1)))
	markSent(t, seller, createQuote(t, seller, custBusiness, productLine(varBolt, 1)))

	// A product's block blocks too.
	setWarning(t, admin, productWarningPath(varFood), gen.OrdersWarningInputLevelBlock, "Tilbakekalt")
	o2 := createOrder(t, seller, custPerson, productLine(varFood, 1))
	expectRefusal(t, seller.Do(http.MethodPost, orderPath(o2.Id, "confirm"), gen.OrdersConfirmInput{
		ExpectedUpdatedAt: o2.UpdatedAt, Version: o2.Version,
	}), http.StatusConflict, "blocked_by_warning", "confirm a recalled product")
}

// A block set while a quote's mail is going out does not undo the send: the
// customer has the quote, so it is sent; mark as sent, with no mail, is
// judged again under the quote's lock (O33).
func TestWarnings_ABlockDuringTheMailKeepsTheQuoteSent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{communications: true})
	chooseChannel(t, h)
	seller := h.SignIn(t, append(boss, "orders:quotes-manage")...)
	q := createQuote(t, seller, custBusiness, productLine(varBolt, 1))
	h.onSend = func() {
		h.Exec(t, `INSERT INTO orders.customer_warnings (customer_id, level, text, created_at, updated_at)
			VALUES ($1, 'block', 'Betaler ikke', now(), now())`, custBusiness)
	}
	res := seller.Do(http.MethodPost, quotePath(q.Id, "send"), gen.OrdersSendInput{
		ExpectedUpdatedAt: q.UpdatedAt, Revision: &q.Revision, Recipient: "kari@acme.example",
	})
	mustStatus(t, res, http.StatusOK, "send while a block is set")
	h.onSend = nil
	if sent := quoteOf(t, res); sent.Status != gen.OrdersQuoteStatusSent || strings.Contains(sent.InternalNote, "Betaler ikke") {
		t.Errorf("quote %s, note %q; want it sent and no override noted", sent.Status, sent.InternalNote)
	}
	if n := len(h.mail.Sent()); n != 1 {
		t.Errorf("%d mails; want the one", n)
	}
}

// An override goes first in a quote's internal note, so a note at its 4000
// characters still keeps the whole override (O33).
func TestWarnings_OverrideFitsAFullNote(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	admin := h.SignIn(t, settingsKey...)
	chief := h.SignIn(t, append(boss, "orders:quotes-manage", "orders:settings")...)
	setWarning(t, admin, customerWarningPath(custBusiness), gen.OrdersWarningInputLevelBlock, "Betaler ikke")
	q := createQuote(t, chief, custBusiness, productLine(varBolt, 1))
	h.Exec(t, `UPDATE orders.quotes SET internal_note = $2 WHERE id = $1`, q.Id, strings.Repeat("n", 3990))
	yes := true
	res := chief.Do(http.MethodPost, quotePath(q.Id, "mark-sent"), gen.OrdersMarkSentInput{
		ExpectedUpdatedAt: quoteAt(t, chief, q.Id), Revision: q.Revision, OverrideWarnings: &yes, OverrideReason: str("Forhåndsbetalt"),
	})
	mustStatus(t, res, http.StatusOK, "an override on a full note")
	note := quoteOf(t, res).InternalNote
	first, _, _ := strings.Cut(note, "\n")
	if !strings.Contains(first, "Betaler ikke") || !strings.HasSuffix(first, "Forhåndsbetalt") || len([]rune(note)) != 4000 {
		t.Errorf("note starts %q, %d characters; want the whole override first, cut to 4000", first, len([]rune(note)))
	}
}

// A product warning whose product is gone is listed as orphaned; it cannot be
// set again, but is removed (O33).
func TestWarnings_AnOrphanedProductWarning(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	admin := h.SignIn(t, settingsKey...)
	setWarning(t, admin, productWarningPath(varBolt), gen.OrdersWarningInputLevelInfo, "Utgående vare")
	setWarning(t, admin, productWarningPath(varFood), gen.OrdersWarningInputLevelBlock, "Tilbakekalt")
	h.catalog.remove(varFood)

	res := admin.Do(http.MethodGet, warningsPath+"?scope=product", nil)
	mustStatus(t, res, http.StatusOK, "list")
	var list gen.OrdersWarningList
	res.JSON(&list)
	orphaned := map[int32]bool{}
	for _, w := range list.Items {
		orphaned[w.Id] = w.Orphaned != nil && *w.Orphaned
	}
	if len(list.Items) != 2 || orphaned[varBolt] || !orphaned[varFood] {
		t.Errorf("list %+v; want only the coffee's orphaned", list.Items)
	}
	mustStatus(t, admin.Do(http.MethodPut, productWarningPath(varFood), gen.OrdersWarningInput{Level: "info", Text: "x"}), http.StatusNotFound, "set an orphan")
	mustStatus(t, admin.Do(http.MethodDelete, productWarningPath(varFood), nil), http.StatusNoContent, "remove an orphan")
}

// A merge moves the absorbed customer's warning to a survivor without one, its
// name blanked; beside a survivor's own it is dropped, but a block over an
// info is merged into a block; an erase deletes the customer's (O33).
func TestWarnings_MergeAndErase(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	admin := h.SignIn(t, settingsKey...)
	holder := orders.Module().CustomerReferences(h.Deps())
	repoint := func(from, into int32) []contracts.RepointedReferences {
		var moved []contracts.RepointedReferences
		inTx(t, h, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			moved, err = holder.RepointCustomer(ctx, tx, from, into)
			return err
		})
		return moved
	}

	setWarning(t, admin, customerWarningPath(custPerson), gen.OrdersWarningInputLevelInfo, "Fra Ola")
	moved := repoint(custPerson, custBusiness)
	if moved[2] != (contracts.RepointedReferences{Kind: "orders.warnings", Count: 1}) {
		t.Errorf("moved %+v; want the warning", moved)
	}
	res := admin.Do(http.MethodGet, customerWarningPath(custBusiness), nil)
	mustStatus(t, res, http.StatusOK, "the survivor's")
	var w gen.OrdersWarning
	res.JSON(&w)
	if w.Text != "Fra Ola" || w.Name != "" {
		t.Errorf("survivor's warning %+v; want Ola's text, the absorbed name blanked", w)
	}

	// A block over the survivor's info makes it a block with both texts.
	setWarning(t, admin, customerWarningPath(custDisabled), gen.OrdersWarningInputLevelBlock, "Fra Gone")
	moved = repoint(custDisabled, custBusiness)
	if moved[2].Count != 1 {
		t.Errorf("moved %+v; want the block merged", moved)
	}
	res = admin.Do(http.MethodGet, customerWarningPath(custBusiness), nil)
	res.JSON(&w)
	if w.Level != gen.OrdersWarningLevelBlock || w.Text != "Fra Ola\nFra Gone" ||
		h.Count(t, `SELECT count(*) FROM orders.customer_warnings WHERE customer_id = $1`, custDisabled) != 0 {
		t.Errorf("survivor's warning %+v; want the block with both texts, the absorbed one's dropped", w)
	}

	// Anything else beside the survivor's own is dropped.
	setWarning(t, admin, customerWarningPath(custDisabled), gen.OrdersWarningInputLevelInfo, "Bare info")
	moved = repoint(custDisabled, custBusiness)
	if moved[2].Count != 0 {
		t.Errorf("moved %+v; want the survivor's kept", moved)
	}
	res = admin.Do(http.MethodGet, customerWarningPath(custBusiness), nil)
	res.JSON(&w)
	if w.Level != gen.OrdersWarningLevelBlock || w.Text != "Fra Ola\nFra Gone" ||
		h.Count(t, `SELECT count(*) FROM orders.customer_warnings WHERE customer_id = $1`, custDisabled) != 0 {
		t.Errorf("survivor's warning %+v; the absorbed info must be dropped", w)
	}

	data := orders.Module().CustomerPersonalData(h.Deps())
	section, err := data.ExportCustomerData(context.Background(), custBusiness)
	if err != nil || section == nil {
		t.Fatalf("export of a customer with only a warning: %v, %v", section, err)
	}
	var erased []contracts.ErasedData
	inTx(t, h, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		erased, err = data.EraseCustomerData(ctx, tx, custBusiness)
		return err
	})
	if !slices.Contains(erased, contracts.ErasedData{Kind: "orders.warnings", Count: 1}) {
		t.Errorf("erased %+v; want the warning", erased)
	}
	mustStatus(t, admin.Do(http.MethodGet, customerWarningPath(custBusiness), nil), http.StatusNotFound, "after the erase")
}
