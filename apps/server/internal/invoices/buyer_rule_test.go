package invoices_test

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is delivery E-2 on the issue (invoices phase 6 design I37, I38;
// plan 2026-11-18-invoices-phase6-e-plan.md, steps E12–E14): the 2028 buyer
// rule of bokføringsforskriften § 5-1-2 as amended by FOR-2026-09-29-1933,
// the § 5-2-1 attention item, and the credit limit's warning. The harness's
// clock stands on 2026-09-12; the rule's day is reached by advancing it.

// toYear2028 is how far the harness's clock moves to stand in 2028.
const toYear2028 = 480 * 24 * time.Hour // 2028-01-05

func obligated(h *harness, customer int32, v bool) {
	h.customers.edit(customer, func(p *contracts.CustomerBillingProfile) {
		p.BookkeepingObligated, p.BookkeepingObligatedSource = &v, "registry"
	})
}

// Before 2028 an obligated buyer without an organisation number is a
// warning, on the draft and on the issue; from 2028, judged on the issue
// date, the issue refuses it, invoice and credit note alike. A buyer not
// known to be obligated still passes on its address.
func TestIssue_The2028BuyerRule(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	standard := line("A", 1, 100, vat25)
	obligated(h, customerForeign, true)

	draft := createDraft(t, h, draftBody(customerForeign, standard))
	if got := getInvoice(t, h, draft.ID); !slices.Contains(got.Warnings, "buyer_org_number_missing") {
		t.Errorf("draft warnings = %v, want buyer_org_number_missing", got.Warnings)
	}
	inv := issued(t, h, draft.ID)
	if !slices.Contains(inv.Warnings, "buyer_org_number_missing") {
		t.Errorf("issue warnings = %v, want buyer_org_number_missing", inv.Warnings)
	}
	credit := creditDraft(t, h, inv.ID)

	h.Advance(toYear2028)
	later := createDraft(t, h, draftBody(customerForeign, standard))
	if got := getInvoice(t, h, later.ID); !slices.Contains(got.Warnings, "buyer_org_number_required") {
		t.Errorf("2028 draft warnings = %v, want buyer_org_number_required", got.Warnings)
	}
	p := refusedWith(t, h, later.ID, "", "buyer_org_number_required")
	if p.Detail == "" {
		t.Error("the refusal has no detail")
	}
	refusedWith(t, h, credit.ID, "", "buyer_org_number_required")

	// The organisation number on file, or a buyer not obligated, issues.
	obligated(h, customerForeign, false)
	if inv := issued(t, h, later.ID); slices.Contains(inv.Warnings, "buyer_org_number_missing") {
		t.Errorf("a buyer known not to be obligated warns %v", inv.Warnings)
	}
	if inv := issued(t, h, createDraft(t, h, draftBody(customerPerson, standard)).ID); slices.Contains(inv.Warnings, "buyer_org_number_missing") {
		t.Errorf("a person warns %v", inv.Warnings)
	}

	// A business the register has said nothing of is warned of, never refused.
	h.customers.edit(customerForeign, func(p *contracts.CustomerBillingProfile) {
		p.BookkeepingObligated, p.BookkeepingObligatedSource = nil, "unknown"
	})
	if inv := issued(t, h, createDraft(t, h, draftBody(customerForeign, standard)).ID); !slices.Contains(inv.Warnings, "buyer_org_number_missing") {
		t.Errorf("an unknown business's issue warnings = %v, want buyer_org_number_missing", inv.Warnings)
	}
}

// fakeBacklog is contracts.OrderBacklog over set amounts.
type fakeBacklog struct {
	mu      sync.Mutex
	amounts []contracts.CurrencyAmount
}

func (f *fakeBacklog) UninvoicedForCustomer(context.Context, int32) ([]contracts.CurrencyAmount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.amounts, nil
}

type creditLimitJSON struct {
	Warnings    []string `json:"warnings"`
	CreditLimit *struct {
		Limit         string `json:"limit"`
		ExposureNok   string `json:"exposureNok"`
		Exceeded      bool   `json:"exceeded"`
		OrdersCounted bool   `json:"ordersCounted"`
		Uncounted     []struct {
			Currency, Amount string
		} `json:"uncounted"`
	} `json:"creditLimit"`
}

// The credit limit warns on a draft — its own gross counted with the open
// invoices and the order backlog — and on the issue, and never refuses.
func TestCreditLimit_WarnsOnTheDraftAndTheIssue(t *testing.T) {
	t.Parallel()
	backlog := &fakeBacklog{amounts: []contracts.CurrencyAmount{{Currency: "NOK", Amount: "300.00"}, {Currency: "EUR", Amount: "5.00"}}}
	h := newHarness(t, modtest.WithOrderBacklog(backlog))
	saveSeller(t, h, completeSeller(1))
	limit := "1000.00"
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.CreditLimit = &limit })

	// 500 net + 25 % = 625; with the backlog 925: within.
	first := createDraft(t, h, draftBody(customerAcme, line("A", 1, 500, vat25)))
	var got creditLimitJSON
	h.SignIn(t, "invoices:access").Do(http.MethodGet, invoicePath(first.ID), nil).JSON(&got)
	if got.CreditLimit == nil || got.CreditLimit.ExposureNok != "925.00" || got.CreditLimit.Exceeded || !got.CreditLimit.OrdersCounted ||
		len(got.CreditLimit.Uncounted) != 1 || got.CreditLimit.Uncounted[0].Currency != "EUR" {
		t.Fatalf("draft credit = %+v, want 925.00 within the limit, EUR uncounted", got.CreditLimit)
	}
	if slices.Contains(got.Warnings, "credit_limit_exceeded") {
		t.Errorf("warnings = %v, want no credit warning within the limit", got.Warnings)
	}
	issued(t, h, first.ID)

	// 625 open + 300 backlog + this draft's 125: past it, a warning only.
	second := createDraft(t, h, draftBody(customerAcme, line("B", 1, 100, vat25)))
	got = creditLimitJSON{}
	h.SignIn(t, "invoices:access").Do(http.MethodGet, invoicePath(second.ID), nil).JSON(&got)
	if got.CreditLimit == nil || got.CreditLimit.ExposureNok != "1050.00" || !got.CreditLimit.Exceeded || !slices.Contains(got.Warnings, "credit_limit_exceeded") {
		t.Fatalf("draft credit = %+v, warnings %v; want 1050.00 exceeded", got.CreditLimit, got.Warnings)
	}
	inv := issued(t, h, second.ID)
	if !slices.Contains(inv.Warnings, "credit_limit_exceeded") {
		t.Errorf("issue warnings = %v, want credit_limit_exceeded", inv.Warnings)
	}
}

// § 5-2-1 fourth paragraph from 2028: an invoice to an obligated buyer
// issued and not delivered seven days on is an attention item, until a
// delivery is recorded; one before 2028, or to a buyer not obligated, is not.
func TestAttention_IssuedAndNotDeliveredFrom2028(t *testing.T) {
	t.Parallel()
	h := readyToIssue(t)
	obligated(h, customerAcme, true)
	before := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	h.Advance(toYear2028)
	inv := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	issued(t, h, createDraft(t, h, draftBody(customerPerson, line("A", 1, 100, vat25))).ID)
	// Signed in again after each move of the clock: a session does not last.
	if items := ofType(attentionOf(t, h.SignIn(t, "invoices:access")), "invoiceUndelivered"); len(items) != 0 {
		t.Errorf("after a day = %v, want none", items)
	}
	h.Advance(8 * 24 * time.Hour)
	reader := h.SignIn(t, "invoices:access")
	items := ofType(attentionOf(t, reader), "invoiceUndelivered")
	if len(items) != 1 || items[0].EntityID != itoa(inv.ID) {
		t.Fatalf("after eight days = %+v, want invoice %d alone (not %d, issued before 2028)", items, inv.ID, before.ID)
	}
	plantEmail(t, h, inv.ID, h.Now())
	if items := ofType(attentionOf(t, reader), "invoiceUndelivered"); len(items) != 0 {
		t.Errorf("after the e-mail = %v, want none", items)
	}
}

// Consumers' undelivered invoices are filtered out before the candidates'
// limit, so however many there are they never hide an obligated buyer's —
// here the limit is two and there are three of them, all older. Not parallel:
// it sets the limit.
func TestAttention_ConsumersNeverCrowdOutAnObligatedBuyer(t *testing.T) {
	defer invoices.SetUndeliveredCandidates(2)()
	h := readyToIssue(t)
	obligated(h, customerAcme, true)
	h.Advance(toYear2028)
	for range 3 {
		issued(t, h, createDraft(t, h, draftBody(customerPerson, line("A", 1, 100, vat25))).ID)
	}
	h.Advance(time.Hour)
	inv := issued(t, h, createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25))).ID)
	h.Advance(8 * 24 * time.Hour)
	items := ofType(attentionOf(t, h.SignIn(t, "invoices:access")), "invoiceUndelivered")
	if len(items) != 1 || items[0].EntityID != itoa(inv.ID) {
		t.Errorf("items = %+v, want invoice %d", items, inv.ID)
	}
}
