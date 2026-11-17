package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	accgen "github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/purchasing"
	purgen "github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// The invoice match (purchasing delivery D) against the real Accounting:
// Register invoice drafts Accounting's supplier invoice through
// SupplierInvoices.CreateDraft with the supplier's default account and the
// order number as its reference; Accounting's supplier-invoice page shows
// Purchasing's badge through SupplierInvoiceMatches; posting it publishes
// accounting.supplier_invoice.posted, which fixes the match, and the posted
// invoice's worker sets the supplier item's last price from the posted line.
func TestPurchasingMatch_RegisterPostAndBadgeThroughTheRealBooks(t *testing.T) {
	keys := append(append([]string{}, purchasingPermissions...), "accounting:read", "accounting:post", "accounting:close",
		"purchasing:invoices-match", "purchasing:supplier-items-manage")
	h, c, _, _ := installInvoices(t, []string{modCustomers, modProducts, modInventory, modInvoices, modAccounting, modPurchasing}, keys)
	p := purchasingInstallation{h: h, c: c}
	p.tax = taxCategory(t, c, "Standard", 0.25, 1)
	var site struct {
		Id             int64 `json:"id"`
		RootLocationId int64 `json:"rootLocationId"`
	}
	okJSON(t, c, http.MethodPost, "/api/v1/inventory/sites", map[string]any{"code": "OSL", "name": "Oslo", "kind": "warehouse"}, &site)
	p.site, p.loc = site.Id, site.RootLocationId
	if res := c.Do(http.MethodPost, "/api/v1/accounting/setup", map[string]any{"booksStart": "2026-01-01", "vatTerm": "bimonthly", "language": "nb"}); res.Status != http.StatusCreated {
		t.Fatalf("setup = %d %s", res.Status, res.Body)
	}
	var chart accgen.AccountingAccountList
	okJSON(t, c, http.MethodGet, "/api/v1/accounting/accounts", nil, &chart)
	var goods int32
	for _, a := range chart.Items {
		if a.Number == "6300" {
			goods = a.Id
		}
	}
	var sup struct {
		Id int64 `json:"id"`
	}
	okJSON(t, c, http.MethodPost, "/api/v1/accounting/suppliers", map[string]any{"name": "Norsk Stål AS", "defaultAccountId": goods}, &sup)
	p.supplier = sup.Id

	bolt := p.product(t, "Bolt M8", "BOLT-M8", nil)
	var item purgen.PurchasingSupplierItem
	okJSON(t, c, http.MethodPost, "/api/v1/purchasing/supplier-items", purgen.PurchasingSupplierItemInput{SupplierId: p.supplier,
		VariantId: bolt.variant, Price: ptrS("4")}, &item)
	line := purLine(bolt.variant, "10")
	line.UnitPrice = ptrS("5")
	o := p.sentOrder(t, line)
	p.receive(t, o.Id, p.receiptLine(o.Lines[0].Id, "10"))

	today := h.Now().UTC()
	var reg purgen.PurchasingInvoiceRegistration
	okJSON(t, c, http.MethodPost, fmt.Sprintf("%s/%d/register-invoice", purOrdersPath, o.Id), purgen.PurchasingInvoiceRegisterInput{
		ClientRequestId: uuid.New(), InvoiceNumber: "55120", InvoiceDate: openapi_types.Date{Time: today}}, &reg)
	if reg.Lines != 1 || reg.Total != "50.00" {
		t.Fatalf("registration = %+v", reg)
	}
	invPath := fmt.Sprintf("/api/v1/accounting/supplier-invoices/%d", reg.SupplierInvoiceId)
	var inv accgen.AccountingSupplierInvoice
	okJSON(t, c, http.MethodGet, invPath, nil, &inv)
	if inv.State != "draft" || inv.Reference == nil || *inv.Reference != fmt.Sprint(*o.Number) || len(inv.Lines) != 1 ||
		inv.Lines[0].AccountId != goods || inv.PurchaseMatch == nil || len(inv.PurchaseMatch.Orders) != 1 ||
		inv.PurchaseMatch.Orders[0].Id != o.Id || inv.PurchaseMatch.Differences != 0 {
		t.Fatalf("Accounting's draft = %+v", inv)
	}

	// Posted with a dearer line: the match is fixed, the price differs, and
	// the last price is the posted line's.
	okJSON(t, c, http.MethodPut, invPath, map[string]any{"supplierId": p.supplier, "kind": "invoice", "invoiceNumber": "55120",
		"invoiceDate": today.Format("2006-01-02"), "total": "60.00", "reference": fmt.Sprint(*o.Number), "revision": inv.Revision,
		"lines": []map[string]any{{"accountId": goods, "amount": "60.00", "entry": "net", "text": inv.Lines[0].Text}}}, &inv)
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("file", "faktura.pdf")
	_, _ = part.Write([]byte("%PDF-1.4 faktura"))
	_ = w.Close()
	if res := c.Do(http.MethodPost, invPath+"/files", nil, modtest.RawBody(w.FormDataContentType(), body.Bytes())); res.Status != http.StatusCreated {
		t.Fatalf("attach = %d %s", res.Status, res.Body)
	}
	okJSON(t, c, http.MethodPost, invPath+"/post", nil, &inv)
	d := h.Deps()
	if err := events.NewDispatcher(h.Pool(), purchasing.Module().EventHandlers(d), d.Logger, h.Now).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The worker runs its first cycle at once; the deadline ends it before
	// the next.
	mods := make([]module.Module, 0, 6)
	for _, n := range []string{modCustomers, modProducts, modInventory, modInvoices, modAccounting, modPurchasing} {
		mods = append(mods, moduleNamed(t, n))
	}
	ran := false
	for _, wk := range module.Workers(d, mods...) {
		if wk.Name() == "purchasing.invoice-posted" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := wk.Run(ctx)
			cancel()
			if err != nil {
				t.Fatalf("the posted invoice's worker: %v", err)
			}
			ran = true
		}
	}
	if !ran {
		t.Fatal("module.Workers built no purchasing.invoice-posted")
	}
	var panel purgen.PurchasingInvoicePanel
	okJSON(t, c, http.MethodGet, fmt.Sprintf("/api/v1/purchasing/supplier-invoices/%d/match", reg.SupplierInvoiceId), nil, &panel)
	if len(panel.Matches) != 1 || panel.Matches[0].State != "posted" || *panel.Matches[0].LineNet != "60.00" || panel.Differences != 1 {
		t.Fatalf("the panel after posting = %+v", panel)
	}
	okJSON(t, c, http.MethodGet, invPath, nil, &inv)
	if inv.PurchaseMatch == nil || inv.PurchaseMatch.Differences != 1 {
		t.Fatalf("the badge after posting = %+v", inv.PurchaseMatch)
	}
	var after purgen.PurchasingSupplierItem
	okJSON(t, c, http.MethodGet, fmt.Sprintf("/api/v1/purchasing/supplier-items/%d", item.Id), nil, &after)
	if after.Price == nil || *after.Price != "6.00" || after.PriceSetBy == nil || string(*after.PriceSetBy) != "invoice" {
		t.Fatalf("the last price = %+v", after)
	}
	if o := p.order(t, o.Id); o.Lines[0].Invoiced != "10" {
		t.Fatalf("invoiced = %s", o.Lines[0].Invoiced)
	}
}

func ptrS(s string) *string { return &s }
