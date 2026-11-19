package orders_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// This file is the credit limit at confirm (invoices phase 6 design I38; plan
// 2026-11-18-invoices-phase6-e-plan.md, steps E13, E14): the computed warning
// credit_limit_exceeded, its override, its figures, and contracts.OrderBacklog.

// creditReceivables is contracts.CustomerReceivables with a NOK and a EUR
// figure, or an error.
type creditReceivables struct {
	mu  sync.Mutex
	nok string
	err error
}

func (r *creditReceivables) ReceivablesForCustomer(context.Context, int32) (contracts.CustomerReceivableTotals, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return contracts.CustomerReceivableTotals{}, r.err
	}
	return contracts.CustomerReceivableTotals{Currencies: []contracts.CurrencyReceivables{
		{Currency: "EUR", Outstanding: "10.00"}, {Currency: "NOK", Outstanding: r.nok},
	}, AsOf: "2026-11-18"}, nil
}

func (r *creditReceivables) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

func setCreditLimit(h *harness, limit string) {
	h.dir.update(custBusiness, func(p *contracts.CustomerBillingProfile) { p.CreditLimit = &limit })
}

func creditWarningOf(o gen.OrdersOrder) *gen.OrdersWarning {
	for _, w := range o.Warnings {
		if w.Scope == gen.OrdersWarningScopeCredit {
			return &w
		}
	}
	return nil
}

// Within the limit a confirm goes through; past it, it is refused
// credit_limit_exceeded with the figures, a draft shows it at info, and the
// warnings' own override confirms it, logged.
func TestCredit_ConfirmRefusesPastTheLimitAndTheOverrideConfirms(t *testing.T) {
	t.Parallel()
	receivables := &creditReceivables{nok: "500.00"}
	h := newHarness(t, harnessOptions{receivables: receivables})
	setCreditLimit(h, "1000.00")
	seller := h.SignIn(t, append(boss, "invoices:access")...)
	chief := h.SignIn(t, append(boss, "orders:settings", "invoices:access")...)

	// 500 open + 4 bolts at 125 gross: exactly the limit, which is not past it.
	confirmOrder(t, seller, createOrder(t, seller, custBusiness, productLine(varBolt, 4)))

	o := createOrder(t, seller, custBusiness, productLine(varBolt, 1))
	w := creditWarningOf(getOrder(t, seller, o.Id))
	if w == nil || w.Level != gen.OrdersWarningLevelInfo || w.Code == nil || *w.Code != gen.CreditLimitExceeded {
		t.Fatalf("draft's credit warning = %+v, want credit_limit_exceeded at info", w)
	}
	if w.Credit == nil || w.Credit.ExposureNok != "1125.00" || w.Credit.Limit != "1000.00" || !w.Credit.InvoicesCounted ||
		!reflect.DeepEqual(w.Credit.Uncounted, []gen.OrdersCreditAmount{{Currency: "EUR", Amount: "10.00"}}) {
		t.Errorf("figures = %+v, want exposure 1125.00 of 1000.00, EUR 10.00 uncounted", w.Credit)
	}

	p := expectRefusal(t, seller.Do(http.MethodPost, orderPath(o.Id, "confirm"), gen.OrdersConfirmInput{
		ExpectedUpdatedAt: o.UpdatedAt, Version: o.Version,
	}), http.StatusConflict, "credit_limit_exceeded", "confirm past the limit")
	if p.Detail == nil || !strings.Contains(*p.Detail, "NOK 1125.00") {
		t.Errorf("detail %v, want the exposure", p.Detail)
	}
	yes := true
	expectRefusal(t, seller.Do(http.MethodPost, orderPath(o.Id, "confirm"), gen.OrdersConfirmInput{
		ExpectedUpdatedAt: o.UpdatedAt, Version: o.Version, OverrideWarnings: &yes, OverrideReason: str("Forskudd"),
	}), http.StatusConflict, "credit_limit_exceeded", "an override without orders:settings")
	res := chief.Do(http.MethodPost, orderPath(o.Id, "confirm"), gen.OrdersConfirmInput{
		ExpectedUpdatedAt: o.UpdatedAt, Version: o.Version, OverrideWarnings: &yes, OverrideReason: str("Forskudd betalt"),
	})
	mustStatus(t, res, http.StatusOK, "confirm with an override")
	res = chief.Do(http.MethodGet, orderPath(o.Id, "changes"), nil)
	mustStatus(t, res, http.StatusOK, "changes")
	var log gen.OrdersChangeList
	res.JSON(&log)
	found := false
	for _, e := range log.Items {
		for _, a := range e.Actions {
			if a.Kind == "warning_overridden" && a.From != nil && strings.Contains(*a.From, "Credit limit") && *a.To == "Forskudd betalt" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("change log %+v; want the credit override", log.Items)
	}

	// With a stored block beside it, the refusal is the warnings' own.
	setWarning(t, h.SignIn(t, settingsKey...), customerWarningPath(custBusiness), gen.OrdersWarningInputLevelBlock, "Betaler ikke")
	o2 := createOrder(t, seller, custBusiness, productLine(varBolt, 1))
	p = expectRefusal(t, seller.Do(http.MethodPost, orderPath(o2.Id, "confirm"), gen.OrdersConfirmInput{
		ExpectedUpdatedAt: o2.UpdatedAt, Version: o2.Version,
	}), http.StatusConflict, "blocked_by_warning", "two blocks")
	if p.Detail == nil || !strings.Contains(*p.Detail, "Betaler ikke") || !strings.Contains(*p.Detail, "Credit limit") {
		t.Errorf("detail %v, want both warnings", p.Detail)
	}
}

// When Invoices cannot be read the confirm warns and goes through: an outage
// must not stop sales. Without a limit nothing is asked at all.
func TestCredit_AReadFailureNeverRefuses(t *testing.T) {
	t.Parallel()
	receivables := &creditReceivables{nok: "99999.00"}
	h := newHarness(t, harnessOptions{receivables: receivables})
	seller := h.SignIn(t, boss...)
	confirmOrder(t, seller, createOrder(t, seller, custBusiness, productLine(varBolt, 1)))

	setCreditLimit(h, "10.00")
	receivables.fail(errors.New("invoices is down"))
	o := createOrder(t, seller, custBusiness, productLine(varBolt, 1))
	if w := creditWarningOf(getOrder(t, seller, o.Id)); w == nil || w.Code == nil || *w.Code != gen.CreditNotChecked || w.Level != gen.OrdersWarningLevelInfo {
		t.Errorf("draft's credit warning = %+v, want credit_not_checked at info", w)
	}
	confirmOrder(t, seller, o)
}

// With invoicing off only the order backlog counts, and the warning says so.
func TestCredit_WithoutInvoicesTheBacklogAloneCounts(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	setCreditLimit(h, "1000.00")
	seller := h.SignIn(t, append(boss, "invoices:access")...)
	confirmOrder(t, seller, createOrder(t, seller, custBusiness, productLine(varBolt, 8)))
	o := createOrder(t, seller, custBusiness, productLine(varBolt, 1))
	w := creditWarningOf(getOrder(t, seller, o.Id))
	if w == nil || w.Credit == nil || w.Credit.InvoicesCounted || w.Credit.ExposureNok != "1125.00" || !strings.Contains(w.Text, "invoicing is off") {
		t.Fatalf("warning = %+v, want the backlog's 1125.00, invoices not counted", w)
	}
	expectRefusal(t, seller.Do(http.MethodPost, orderPath(o.Id, "confirm"), gen.OrdersConfirmInput{
		ExpectedUpdatedAt: o.UpdatedAt, Version: o.Version,
	}), http.StatusConflict, "credit_limit_exceeded", "confirm past the limit")
}

// Raising a confirmed order past the limit is judged as a confirm is: refused
// credit_limit_exceeded, saved with the override; a caller without
// invoices:access sees the warning without the figures.
func TestCredit_RaisingAConfirmedOrderIsJudged(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{receivables: &creditReceivables{nok: "0.00"}})
	setCreditLimit(h, "100000.00")
	seller := h.SignIn(t, append(boss, "invoices:access")...)
	chief := h.SignIn(t, append(boss, "orders:settings", "invoices:access")...)

	// 8 bolts, 1 000 gross: within the limit.
	o := confirmOrder(t, seller, createOrder(t, seller, custBusiness, productLine(varBolt, 8)))
	raised := []gen.OrdersLineInput{keep(o)[0], productLine(varBolt, 4000)} // + 500 000 gross
	expectRefusal(t, save(seller, o, raised, nil), http.StatusConflict, "credit_limit_exceeded", "a raise past the limit")
	yes := true
	res := save(chief, o, raised, func(b *gen.OrdersOrderUpdate) {
		b.OverrideWarnings, b.OverrideReason = &yes, str("Bankgaranti")
	})
	mustStatus(t, res, http.StatusOK, "a raise with the override")

	// Lowering it again is never judged; a draft past the limit shows the
	// warning without figures to a caller without invoices:access.
	blind := h.SignIn(t, boss...)
	d := createOrder(t, blind, custBusiness, productLine(varBolt, 1))
	w := creditWarningOf(getOrder(t, blind, d.Id))
	if w == nil || w.Credit != nil || strings.Contains(w.Text, "NOK") {
		t.Errorf("warning without invoices:access = %+v, want it without figures", w)
	}
}

// contracts.OrderBacklog: confirmed orders' open gross per currency, drafts
// and what is invoiced or cancelled left out.
func TestOrderBacklog_UninvoicedForCustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	seller := h.SignIn(t, boss...)
	backlog := orders.Module().OrderBacklog(h.Deps())
	got, err := backlog.UninvoicedForCustomer(context.Background(), custBusiness)
	if err != nil || len(got) != 0 {
		t.Fatalf("nothing confirmed = %v, %v; want an empty list", got, err)
	}
	confirmed := confirmOrder(t, seller, createOrder(t, seller, custBusiness, productLine(varBolt, 5)))
	createOrder(t, seller, custBusiness, productLine(varBolt, 10)) // a draft counts for nothing
	// Two invoiced, one cancelled, one in a draft invoice — which counts its
	// own gross, so the backlog must not count it again: one bolt of five left.
	h.Exec(t, `UPDATE orders.lines SET qty_invoiced = 2, qty_cancelled = 1 WHERE owner_kind = 'order' AND owner_id = $1`, confirmed.Id)
	h.Exec(t, `INSERT INTO orders.invoice_allocations (line_id, order_id, quantity, unit_price, amount, vat_code_id, created_at, updated_at)
	           SELECT id, owner_id, 1, unit_price, 100, vat_code_id, now(), now() FROM orders.lines WHERE owner_kind = 'order' AND owner_id = $1`, confirmed.Id)
	got, err = backlog.UninvoicedForCustomer(context.Background(), custBusiness)
	if err != nil {
		t.Fatal(err)
	}
	if want := []contracts.CurrencyAmount{{Currency: "NOK", Amount: "125.00"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("backlog = %v, want %v", got, want)
	}
}
