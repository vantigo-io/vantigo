package accounting_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/accounting"
	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The seams Purchasing reads and writes the books through (purchasing design
// P1, P2, P5–P7, A1, A2, A8, A12): the supplier register before the books
// and for a buyer, its holders and its personal data, the supplier
// invoices' contract and events, the match badge, and the SAF-T import
// taking a supplier made first.

// buyer is a buyer's set: keep the suppliers, nothing of the books.
var buyer = []string{"accounting:suppliers-manage"}

// directoryOf is the module's SupplierDirectory as Compose builds it.
func directoryOf(h *harness) contracts.SupplierDirectory {
	return accounting.Module().SupplierDirectory(h.Deps())
}

// supplierInvoicesOf is the module's SupplierInvoices as Compose builds it.
func supplierInvoicesOf(h *harness) contracts.SupplierInvoices {
	return accounting.Module().SupplierInvoices(h.Deps())
}

// The register answers before the books are set up, to a buyer as to a
// poster; the books' own fields are the poster's, and the defaults wait for
// the books.
func TestSuppliers_BeforeTheBooksAndForABuyer(t *testing.T) {
	h := newHarness(t)
	b := h.SignIn(t, buyer...)
	res := b.Do(http.MethodPost, base+"/suppliers", map[string]any{"name": "Rørlegger Hansen AS", "organisationNumber": "923609016",
		"addressLine": "Storgata 1", "email": "post@hansen.no", "paymentTermsDays": 20})
	if res.Status != http.StatusCreated {
		t.Fatalf("a buyer's supplier before the books = %d %s", res.Status, res.Body)
	}
	var s gen.AccountingSupplier
	res.JSON(&s)
	if s.Number != 1 || s.RetainedUntil != nil {
		t.Fatalf("supplier = %+v", s)
	}
	expectProblem(t, b.Do(http.MethodPost, base+"/suppliers", map[string]any{"name": "B", "bankAccount": "12345678903"}),
		http.StatusForbidden, "supplier_fields_need_post")
	expectProblem(t, h.SignIn(t, reader...).Do(http.MethodPost, base+"/suppliers", map[string]any{"name": "C"}),
		http.StatusForbidden, "suppliers_manage_required")
	expectProblem(t, h.SignIn(t, poster...).Do(http.MethodPost, base+"/suppliers", map[string]any{"name": "D", "defaultAccountId": 1001}),
		http.StatusConflict, "books_not_set_up")
	r := h.SignIn(t, reader...)
	if res := r.Do(http.MethodGet, base+"/suppliers", nil); res.Status != http.StatusOK {
		t.Fatalf("the list before the books = %d %s", res.Status, res.Body)
	}
	if res := r.Do(http.MethodGet, fmt.Sprintf("%s/suppliers/%d", base, s.Id), nil); res.Status != http.StatusOK {
		t.Fatalf("a supplier before the books = %d", res.Status)
	}
	expectProblem(t, r.Do(http.MethodGet, base+"/accounts", nil), http.StatusConflict, "books_not_set_up")
	res = b.Do(http.MethodPost, base+"/suppliers", map[string]any{"name": "Ola Nordmann", "isPerson": true})
	var person gen.AccountingSupplier
	res.JSON(&person)
	expectProblem(t, h.SignIn(t, accountant...).Do(http.MethodPost, fmt.Sprintf("%s/suppliers/%d/anonymise", base, person.Id), nil),
		http.StatusConflict, "books_not_set_up")

	// The directory reads it too, with no permission of its own.
	d := directoryOf(h)
	got, err := d.Supplier(t.Context(), s.Id)
	if err != nil || got == nil || got.Name != "Rørlegger Hansen AS" || got.OrganisationNumber != "923609016" || got.AddressLine != "Storgata 1" ||
		got.PaymentTermsDays != 20 || !got.Active || got.IsPerson || got.Anonymised {
		t.Fatalf("Supplier = %+v, %v", got, err)
	}
	if none, err := d.Supplier(t.Context(), 99999); none != nil || err != nil {
		t.Errorf("an unknown supplier = %+v, %v", none, err)
	}
	if found, err := d.SearchSuppliers(t.Context(), "hansen", 10); err != nil || len(found) != 1 || found[0].ID != s.Id || found[0].Number != 1 {
		t.Errorf("SearchSuppliers = %+v, %v", found, err)
	}
	if found, err := d.Suppliers(t.Context(), []int64{99999, s.Id}); err != nil || len(found) != 1 {
		t.Errorf("Suppliers = %+v, %v", found, err)
	}

	// After the setup the poster sets the bank account and the default
	// account; a buyer's change leaving them out keeps them, one sending
	// another value is refused.
	a := h.SignIn(t, accountant...)
	setUp(t, a, "nb")
	chart := accounts(t, a)
	put := func(c *modtest.Client, rev int32, body map[string]any) *modtest.Response {
		body["revision"] = rev
		return c.Do(http.MethodPut, fmt.Sprintf("%s/suppliers/%d", base, s.Id), body)
	}
	res = put(a, s.Revision, map[string]any{"name": s.Name, "organisationNumber": "923609016", "bankAccount": "12345678903",
		"defaultAccountId": chart["6300"].Id})
	if res.Status != http.StatusOK {
		t.Fatalf("the poster's change = %d %s", res.Status, res.Body)
	}
	res.JSON(&s)
	res = put(b, s.Revision, map[string]any{"name": "Hansen Rør AS", "organisationNumber": "923609016"})
	if res.Status != http.StatusOK {
		t.Fatalf("the buyer's change = %d %s", res.Status, res.Body)
	}
	res.JSON(&s)
	if s.Name != "Hansen Rør AS" || deref(s.BankAccount) != "12345678903" || s.DefaultAccountId == nil || *s.DefaultAccountId != chart["6300"].Id {
		t.Errorf("after the buyer's change = %+v", s)
	}
	// The directory answers the revision, which is all a buyer's change
	// needs: a buyer without accounting:read writes it from what the
	// directory said (A26).
	entry, err := d.Supplier(t.Context(), s.Id)
	if err != nil || entry.Revision != s.Revision {
		t.Fatalf("the directory's revision = %+v, %v; want %d", entry, err, s.Revision)
	}
	if found, _ := d.SearchSuppliers(t.Context(), "hansen", 10); len(found) != 1 || found[0].Revision != s.Revision {
		t.Errorf("SearchSuppliers' revision = %+v", found)
	}
	res = put(b, entry.Revision, map[string]any{"name": entry.Name, "organisationNumber": entry.OrganisationNumber, "city": "Bergen"})
	if res.Status != http.StatusOK {
		t.Fatalf("the buyer's change from the directory's revision = %d %s", res.Status, res.Body)
	}
	res.JSON(&s)
	expectProblem(t, put(b, s.Revision, map[string]any{"name": s.Name, "bankAccount": ""}), http.StatusForbidden, "supplier_fields_need_post")
	expectProblem(t, put(b, s.Revision, map[string]any{"name": s.Name, "defaultAccountId": chart["6000"].Id}), http.StatusForbidden, "supplier_fields_need_post")
}

// The buyer's scope on the register, the either-or of the writes, and the
// virtual employees the writes admit (purchasing delivery 0): a caller
// holding neither key is refused create, change and lookup; a buyer's change
// may not touch IBAN, BIC, the tax code, the person flag or the active flag
// and keeps them when it leaves them out; a kept default is not checked again;
// an agent holding either key calls the three writes, one holding neither is
// refused.
func TestSuppliers_TheBuyersScopeAndAgents(t *testing.T) {
	h := newHarness(t, modtest.WithTransport(brregTransport{}), modtest.WithBackoff(func(int) time.Duration { return 0 }))
	a := h.SignIn(t, accountant...)
	setUp(t, a, "nb")
	bk := &books{h: h, c: a, chart: accounts(t, a), codes: taxCodes(t, a)}
	s := supplier(t, bk, map[string]any{"name": "Varer AS", "bankAccount": "12345678903", "iban": "NO9386011117947",
		"bic": "DNBANOKK", "defaultAccountId": bk.acc("6300"), "defaultTaxCodeId": bk.code("1"), "isPerson": true})
	path := fmt.Sprintf("%s/suppliers/%d", base, s.Id)

	// Neither key, or post without read: no write and no lookup.
	for _, c := range []*modtest.Client{h.SignIn(t), h.SignIn(t, "accounting:post")} {
		expectProblem(t, c.Do(http.MethodPost, base+"/suppliers", map[string]any{"name": "X"}), http.StatusForbidden, "suppliers_manage_required")
		expectProblem(t, c.Do(http.MethodPut, path, map[string]any{"name": "X", "revision": s.Revision}), http.StatusForbidden, "suppliers_manage_required")
		expectProblem(t, c.Do(http.MethodGet, base+"/suppliers/lookup/brreg?q=equinor", nil), http.StatusForbidden, "suppliers_manage_required")
	}

	b := h.SignIn(t, buyer...)
	if res := b.Do(http.MethodGet, base+"/suppliers/lookup/brreg?q=equinor", nil); res.Status != http.StatusOK {
		t.Fatalf("the buyer's lookup = %d %s", res.Status, res.Body)
	}
	for field, value := range map[string]any{"iban": "NO8330001234567", "bic": "NDEANOKK", "defaultTaxCodeId": bk.code("11"),
		"isPerson": false, "active": false} {
		expectProblem(t, b.Do(http.MethodPut, path, map[string]any{"name": s.Name, "revision": s.Revision, field: value}),
			http.StatusForbidden, "supplier_fields_need_post")
	}

	// The accountant makes the default account inactive; the buyer's rename
	// keeps it, and the flags, and is not refused for it. A poster naming
	// the inactive account anew is.
	h.Exec(t, `UPDATE accounting.accounts SET active = false WHERE id = $1`, bk.acc("6300"))
	res := b.Do(http.MethodPut, path, map[string]any{"name": "Varer Norge AS", "revision": s.Revision})
	if res.Status != http.StatusOK {
		t.Fatalf("the buyer's rename over an inactive default = %d %s", res.Status, res.Body)
	}
	res.JSON(&s)
	if s.Name != "Varer Norge AS" || !s.IsPerson || !s.Active || deref(s.Iban) != "NO9386011117947" || s.DefaultAccountId == nil ||
		*s.DefaultAccountId != bk.acc("6300") || s.DefaultTaxCodeId == nil || *s.DefaultTaxCodeId != bk.code("1") {
		t.Errorf("after the buyer's rename = %+v", s)
	}
	res = a.Do(http.MethodPut, path, map[string]any{"name": s.Name, "revision": s.Revision, "bankAccount": "12345678903",
		"iban": "NO9386011117947", "bic": "DNBANOKK", "defaultAccountId": bk.acc("6300"), "defaultTaxCodeId": bk.code("1"), "isPerson": true})
	if res.Status != http.StatusOK {
		t.Fatalf("the poster's change keeping the inactive default = %d %s", res.Status, res.Body)
	}
	res.JSON(&s)
	h.Exec(t, `UPDATE accounting.accounts SET active = false WHERE id = $1`, bk.acc("6000"))
	expectProblem(t, a.Do(http.MethodPut, path, map[string]any{"name": s.Name, "revision": s.Revision, "defaultAccountId": bk.acc("6000")}),
		http.StatusBadRequest, "account_inactive")

	// A buyer's new supplier may be a person's, in another currency.
	res = b.Do(http.MethodPost, base+"/suppliers", map[string]any{"name": "Kari Nordmann", "isPerson": true, "currency": "EUR"})
	if res.Status != http.StatusCreated {
		t.Fatalf("a buyer's person supplier = %d %s", res.Status, res.Body)
	}
	var person gen.AccountingSupplier
	res.JSON(&person)
	if !person.IsPerson || person.Currency != "EUR" {
		t.Errorf("the buyer's person supplier = %+v", person)
	}

	// The virtual employees: either key calls the three writes, neither is
	// refused by the handler.
	for _, keys := range [][]string{{"accounting:post", "accounting:read"}, buyer} {
		ctx, _ := h.AgentContext(t, nil, keys...)
		status, body, err := h.AgentCall(t, ctx, "createAccountingSupplier", nil, nil, map[string]any{"name": "Agentens leverandør " + keys[0]})
		if err != nil || status != http.StatusCreated {
			t.Fatalf("%v: an agent's create = %d %s, %v", keys, status, body, err)
		}
		var made gen.AccountingSupplier
		if err := json.Unmarshal(body, &made); err != nil {
			t.Fatal(err)
		}
		status, body, err = h.AgentCall(t, ctx, "updateAccountingSupplier", map[string]any{"id": made.Id}, nil,
			map[string]any{"name": made.Name + " AS", "revision": made.Revision})
		if err != nil || status != http.StatusOK {
			t.Errorf("%v: an agent's change = %d %s, %v", keys, status, body, err)
		}
		status, body, err = h.AgentCall(t, ctx, "lookupAccountingSupplierBrreg", nil, nil, nil)
		if err != nil || status != http.StatusBadRequest {
			t.Errorf("%v: an agent's lookup without a query = %d %s, %v (want the handler's 400)", keys, status, body, err)
		}
	}
	ctx, _ := h.AgentContext(t, nil, reader...)
	for _, call := range []struct {
		op   string
		path map[string]any
		body any
	}{{"createAccountingSupplier", nil, map[string]any{"name": "X"}},
		{"updateAccountingSupplier", map[string]any{"id": s.Id}, map[string]any{"name": "X", "revision": s.Revision}},
		{"lookupAccountingSupplierBrreg", nil, nil}} {
		if status, body, err := h.AgentCall(t, ctx, call.op, call.path, nil, call.body); err != nil || status != http.StatusForbidden ||
			!strings.Contains(string(body), "suppliers_manage_required") {
			t.Errorf("%s by an agent holding neither = %d %s, %v", call.op, status, body, err)
		}
	}
}

// fakeSupplierHolder holds a supplier for each purpose it is set to, and
// checks it is asked while the supplier is taken FOR UPDATE.
type fakeSupplierHolder struct {
	h      *harness
	mu     sync.Mutex
	holds  map[contracts.SupplierHoldPurpose]bool
	locked []bool
}

func (f *fakeSupplierHolder) SupplierHeld(ctx context.Context, tx pgx.Tx, id int64, purpose contracts.SupplierHoldPurpose) (*contracts.SupplierHold, error) {
	if tx == nil {
		return nil, errors.New("a holder asked without a transaction")
	}
	// Another connection may not share the row: the caller holds it FOR
	// UPDATE.
	_, err := f.h.Pool().Exec(ctx, `SELECT id FROM accounting.suppliers WHERE id = $1 FOR SHARE NOWAIT`, id)
	var pg *pgconn.PgError
	f.mu.Lock()
	defer f.mu.Unlock()
	f.locked = append(f.locked, errors.As(err, &pg) && pg.Code == "55P03")
	if f.holds[purpose] {
		return &contracts.SupplierHold{Kind: contracts.SupplierHoldKindOpenDocuments, Documents: 2}, nil
	}
	return nil, nil
}

// fakeSupplierData answers an export section and records its erases.
type fakeSupplierData struct {
	mu     sync.Mutex
	erased []int64
}

func (f *fakeSupplierData) ExportSupplierData(_ context.Context, id int64) (any, error) {
	return map[string]any{"orders": []string{fmt.Sprint("PO-", id)}}, nil
}

func (f *fakeSupplierData) EraseSupplierData(_ context.Context, tx pgx.Tx, id int64) ([]contracts.ErasedData, error) {
	if tx == nil {
		return nil, errors.New("an erase without a transaction")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.erased = append(f.erased, id)
	return []contracts.ErasedData{{Kind: "purchasing.orders", Count: 1}}, nil
}

// A supplier's delete and anonymisation take it FOR UPDATE, then ask the
// holders; the export asks every module's section, the anonymisation every
// module's erase (purchasing design P7, A8).
func TestSuppliers_HoldersAndPersonalData(t *testing.T) {
	holder := &fakeSupplierHolder{holds: map[contracts.SupplierHoldPurpose]bool{contracts.SupplierHoldDelete: true, contracts.SupplierHoldAnonymise: true}}
	data := &fakeSupplierData{}
	h := newHarness(t, modtest.WithSupplierReferenceHolders(holder),
		modtest.WithSupplierPersonalData(contracts.SupplierPersonalDataHolder{Module: "purchasing", Data: data}))
	holder.h = h
	c := h.SignIn(t, accountant...)
	setUp(t, c, "nb")
	b := &books{h: h, c: c}
	s := supplier(t, b, map[string]any{"name": "Ola Nordmann", "isPerson": true})

	expectProblem(t, c.Do(http.MethodDelete, fmt.Sprintf("%s/suppliers/%d", base, s.Id), nil), http.StatusConflict, "supplier_has_documents")
	expectProblem(t, c.Do(http.MethodPost, fmt.Sprintf("%s/suppliers/%d/anonymise", base, s.Id), nil), http.StatusConflict, "supplier_has_open_documents")
	if len(data.erased) != 0 {
		t.Fatalf("erased while held: %v", data.erased)
	}
	res := c.Do(http.MethodGet, fmt.Sprintf("%s/suppliers/%d/export", base, s.Id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("export = %d %s", res.Status, res.Body)
	}
	var export gen.AccountingSupplierExport
	res.JSON(&export)
	if _, ok := export.Modules["purchasing"]; !ok {
		t.Errorf("the export's modules = %+v, want purchasing's section", export.Modules)
	}

	holder.mu.Lock()
	holder.holds = nil
	holder.mu.Unlock()
	res = c.Do(http.MethodPost, fmt.Sprintf("%s/suppliers/%d/anonymise", base, s.Id), nil)
	if res.Status != http.StatusOK {
		t.Fatalf("anonymise = %d %s", res.Status, res.Body)
	}
	if len(data.erased) != 1 || data.erased[0] != s.Id {
		t.Errorf("erased = %v", data.erased)
	}
	other := supplier(t, b, map[string]any{"name": "Gone AS"})
	if res := c.Do(http.MethodDelete, fmt.Sprintf("%s/suppliers/%d", base, other.Id), nil); res.Status != http.StatusNoContent {
		t.Fatalf("delete = %d %s", res.Status, res.Body)
	}
	holder.mu.Lock()
	defer holder.mu.Unlock()
	for i, locked := range holder.locked {
		if !locked {
			t.Errorf("holder call %d ran while the supplier was not held FOR UPDATE", i)
		}
	}
	if len(holder.locked) != 4 {
		t.Errorf("holder calls = %d, want 4", len(holder.locked))
	}
}

// LockSuppliers takes the rows FOR SHARE on the caller's transaction: a
// delete waits for it.
func TestSupplierDirectory_LockSuppliers(t *testing.T) {
	b := newBooks(t)
	s := b.aSupplier(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	tx, err := b.h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	got, err := directoryOf(b.h).LockSuppliers(ctx, tx, []int64{s.Id, 99999, s.Id})
	if err != nil || len(got) != 1 || got[0].ID != s.Id {
		t.Fatalf("LockSuppliers = %+v, %v", got, err)
	}
	if _, err := b.h.Pool().Exec(ctx, `SELECT id FROM accounting.suppliers WHERE id = $1 FOR UPDATE NOWAIT`, s.Id); err == nil {
		t.Error("the supplier could be taken FOR UPDATE while locked FOR SHARE")
	}
}

// fakeMatches answers a match for the invoices it names.
type fakeMatches struct {
	invoices map[int64]contracts.SupplierInvoiceMatch
}

func (f fakeMatches) Summary(_ context.Context, ids []int64) ([]contracts.SupplierInvoiceMatch, error) {
	var out []contracts.SupplierInvoiceMatch
	for _, id := range ids {
		if m, ok := f.invoices[id]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// The supplier invoices' contract (P6, A1, A9): a draft made through the
// draft's own rules, the supplier's defaults on the lines, the refusals as
// the contract's, the invoice read back with each line's net; the badge from
// the match slot, absent without it; the two events on the posting's and the
// reversal's transactions (P5).
func TestSupplierInvoices_ContractEventsAndBadge(t *testing.T) {
	matches := fakeMatches{invoices: map[int64]contracts.SupplierInvoiceMatch{}}
	h := newHarness(t, modtest.WithSupplierInvoiceMatches(matches))
	c := h.SignIn(t, accountant...)
	setUp(t, c, "nb")
	b := &books{h: h, c: c, chart: accounts(t, c), codes: taxCodes(t, c)}
	plain := b.aSupplier(t)
	s := supplier(t, b, map[string]any{"name": "Varer AS", "defaultAccountId": b.acc("6300"), "defaultTaxCodeId": b.code("1")})
	_, user := h.SignInUser(t, accountant...)
	ci := supplierInvoicesOf(h)
	day := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	d := contracts.SupplierInvoiceDraft{SupplierID: s.Id, Kind: "invoice", Number: "INV-1", InvoiceDate: day, Total: "1250.00",
		Reference: "PO 1001", By: user, Lines: []contracts.SupplierInvoiceDraftLine{{Text: "Bolts", Amount: "1000.00"}}}
	id, err := ci.CreateDraft(t.Context(), d)
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	inv, err := ci.Invoice(t.Context(), id)
	if err != nil || inv == nil || inv.State != "draft" || inv.Reference != "PO 1001" || len(inv.Lines) != 1 ||
		inv.Lines[0].Entry != "net" || inv.Lines[0].Net != "1000.00" || inv.Lines[0].Gross != "1250.00" || inv.Lines[0].AccountID != b.acc("6300") {
		t.Fatalf("Invoice = %+v, %v", inv, err)
	}
	var r *contracts.SupplierInvoiceRefusal
	if _, err := ci.CreateDraft(t.Context(), d); !errors.As(err, &r) || r.Code != contracts.SupplierInvoiceDuplicate {
		t.Errorf("the same number again = %v", err)
	}
	none := d
	none.SupplierID, none.Number = plain.Id, "INV-2"
	if _, err := ci.CreateDraft(t.Context(), none); !errors.As(err, &r) || r.Code != contracts.SupplierInvoiceAccountMissing || len(r.Fields) != 1 {
		t.Errorf("a supplier with no default account = %+v", err)
	}
	bad := d
	bad.Number, bad.Currency = "INV-3", "EUR"
	if _, err := ci.CreateDraft(t.Context(), bad); !errors.As(err, &r) || r.Code != "exchange_rate_required" || len(r.Fields) == 0 {
		t.Errorf("another currency without its rate = %+v", err)
	}
	unknown := d
	unknown.SupplierID, unknown.Number = 99999, "INV-4"
	if _, err := ci.CreateDraft(t.Context(), unknown); !errors.As(err, &r) || r.Code != contracts.SupplierInvoiceInvalid ||
		len(r.Fields) != 1 || r.Fields[0].Field != "supplierId" {
		t.Errorf("an unknown supplier = %+v", err)
	}
	nobody := d
	nobody.Number, nobody.By = "INV-5", uuid.Nil
	if _, err := ci.CreateDraft(t.Context(), nobody); err == nil || errors.As(err, &r) {
		t.Errorf("a draft for no person = %v, want a plain error", err)
	}
	if got, err := ci.Invoice(t.Context(), 99999); got != nil || err != nil {
		t.Errorf("an unknown invoice = %+v, %v", got, err)
	}

	// The badge: on the page and in the list, for the matched invoice only.
	matches.invoices[id] = contracts.SupplierInvoiceMatch{InvoiceID: id, Differences: 1,
		Orders: []contracts.MatchedOrder{{ID: 7, Number: "1001"}}}
	var page gen.AccountingSupplierInvoice
	c.Do(http.MethodGet, fmt.Sprintf("%s/supplier-invoices/%d", base, id), nil).JSON(&page)
	if page.PurchaseMatch == nil || page.PurchaseMatch.Differences != 1 || len(page.PurchaseMatch.Orders) != 1 || page.PurchaseMatch.Orders[0].Number != "1001" {
		t.Errorf("the page's match = %+v", page.PurchaseMatch)
	}
	other := draft(t, b, map[string]any{"supplierId": s.Id, "kind": "invoice", "invoiceNumber": "X-1", "invoiceDate": "2026-03-02",
		"total": "100.00", "lines": []map[string]any{{"accountId": b.acc("6300"), "amount": "100.00"}}})
	var list gen.AccountingSupplierInvoiceList
	c.Do(http.MethodGet, base+"/supplier-invoices", nil).JSON(&list)
	for _, it := range list.Items {
		if (it.PurchaseMatch != nil) != (it.Id == id) {
			t.Errorf("invoice %d's badge = %+v", it.Id, it.PurchaseMatch)
		}
	}

	// The events: posted, then reversed with its copy.
	postInvoice(t, b, id)
	res := c.Do(http.MethodPost, fmt.Sprintf("%s/supplier-invoices/%d/reverse-and-copy", base, id), map[string]any{"date": "2026-03-10", "reason": "Wrong"})
	if res.Status != http.StatusCreated {
		t.Fatalf("reverse and copy = %d %s", res.Status, res.Body)
	}
	var copied gen.AccountingSupplierInvoice
	res.JSON(&copied)
	rows, err := h.Pool().Query(t.Context(), `SELECT name, aggregate_id, payload->>'supplierId', coalesce(payload->>'copyId', ''), payload->>'total',
		payload ? 'reference' FROM platform.events WHERE module = 'accounting' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var name, agg, supplierID, copyID, total string
		var reference bool
		if err := rows.Scan(&name, &agg, &supplierID, &copyID, &total, &reference); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.Join([]string{name, agg, supplierID, copyID, total, fmt.Sprint(reference)}, "|"))
	}
	rows.Close()
	sid, iid := fmt.Sprint(s.Id), fmt.Sprint(id)
	want := []string{"accounting.supplier_invoice.posted|" + iid + "|" + sid + "||1250.00|false",
		"accounting.supplier_invoice.reversed|" + iid + "|" + sid + "|" + fmt.Sprint(copied.Id) + "|1250.00|false"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("events =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Deleting a draft publishes accounting.supplier_invoice.deleted (A79),
	// with no free text.
	if res := c.Do(http.MethodDelete, fmt.Sprintf("%s/supplier-invoices/%d", base, other.Id), nil); res.Status != http.StatusNoContent {
		t.Fatalf("delete the draft = %d %s", res.Status, res.Body)
	}
	var deleted string
	if err := h.Pool().QueryRow(t.Context(), `SELECT name || '|' || aggregate_id || '|' || (payload->>'number') || '|' || (payload->>'total') || '|' ||
		(payload ? 'reference')::text FROM platform.events WHERE module = 'accounting' ORDER BY id DESC LIMIT 1`).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("accounting.supplier_invoice.deleted|%d|X-1|100.00|false", other.Id); deleted != want {
		t.Errorf("the deletion's event = %s, want %s", deleted, want)
	}

	// Without the match slot there is no badge.
	plainH := newBooks(t)
	sup := plainH.aSupplier(t)
	inv2 := draft(t, plainH, map[string]any{"supplierId": sup.Id, "kind": "invoice", "invoiceNumber": "Y-1", "invoiceDate": "2026-03-02",
		"total": "100.00", "lines": []map[string]any{{"accountId": plainH.acc("6300"), "amount": "100.00"}}})
	if inv2.PurchaseMatch != nil {
		t.Errorf("a badge with no provider: %+v", inv2.PurchaseMatch)
	}
}

// TestSaftImport_SupplierMadeFirst (A2): a supplier a buyer made before the
// books were set up is taken by the import as the file's supplier with its
// organisation number, so the import neither fails nor makes it twice; one of
// several of the same name is picked in the apply's suppliers.
func TestSaftImport_SupplierMadeFirst(t *testing.T) {
	old := saftBooks(t)
	old.ledger(t)
	_, files := old.export(t, map[string]any{"fiscalYear": 2026})

	for _, byName := range []bool{false, true} {
		h := newHarness(t, modtest.WithSalesTax(fakeSalesTax{seller: fjordSeller}))
		c := h.SignIn(t, accountant...)
		made := func(body map[string]any) gen.AccountingSupplier {
			res := h.SignIn(t, buyer...).Do(http.MethodPost, base+"/suppliers", body)
			if res.Status != http.StatusCreated {
				t.Fatalf("a buyer's supplier = %d %s", res.Status, res.Body)
			}
			var s gen.AccountingSupplier
			res.JSON(&s)
			return s
		}
		var want gen.AccountingSupplier
		if byName {
			made(map[string]any{"name": "Verksted AS"})
			want = made(map[string]any{"name": "verksted  as"})
		} else {
			want = made(map[string]any{"name": "Verksted Hansen", "organisationNumber": "923456783"})
		}
		preview := uploadOK(t, c, "fjord.xml", files[0])
		if len(preview.Suppliers) != 1 {
			t.Fatalf("preview suppliers = %+v", preview.Suppliers)
		}
		sp := preview.Suppliers[0]
		body := map[string]any{"vatTerm": "bimonthly", "language": "nb", "fiscalYearStartMonth": 1}
		if byName {
			if sp.Match != gen.SaftSupplierAmbiguous || len(sp.Candidates) != 2 || sp.SupplierId != nil {
				t.Fatalf("by name: preview supplier = %+v", sp)
			}
			expectProblem(t, c.Do(http.MethodPost, fmt.Sprintf("%s/saft-imports/%d/apply", base, preview.Id),
				map[string]any{"vatTerm": "bimonthly", "language": "nb", "fiscalYearStartMonth": 1, "suppliers": map[string]any{sp.FileSupplierId: 99999}}),
				http.StatusBadRequest, "saft_import_supplier_unknown")
			body["suppliers"] = map[string]any{sp.FileSupplierId: want.Id}
		} else if sp.Match != gen.SaftSupplierMatched || deref(sp.SupplierId) != want.Id || deref(sp.SupplierNumber) != want.Number {
			t.Fatalf("by number: preview supplier = %+v", sp)
		}
		res := c.Do(http.MethodPost, fmt.Sprintf("%s/saft-imports/%d/apply", base, preview.Id), body)
		if res.Status != http.StatusCreated {
			t.Fatalf("apply (by name %v) = %d %s", byName, res.Status, res.Body)
		}
		var sup gen.AccountingSupplierList
		c.Do(http.MethodGet, base+"/suppliers", nil).JSON(&sup)
		wantCount := 1
		if byName {
			wantCount = 2
		}
		if int(sup.Total) != wantCount {
			t.Errorf("by name %v: suppliers after the import = %+v", byName, sup.Items)
		}
		var items gen.AccountingOpenItemList
		c.Do(http.MethodGet, base+"/open-items", nil).JSON(&items)
		found := false
		for _, it := range items.Items {
			if it.SupplierId != nil {
				found = found || *it.SupplierId == want.Id
				if *it.SupplierId != want.Id {
					t.Errorf("by name %v: an open item of supplier %d, want %d", byName, *it.SupplierId, want.Id)
				}
			}
		}
		if !found {
			t.Errorf("by name %v: no open item names the supplier made first", byName)
		}
	}
}

// TestSaftImport_SupplierPicksAndBankFields (purchasing delivery 0): two of
// the file's suppliers matching one of the register's are both ambiguous and
// block Apply until picked; a pick names a supplier of the file, and a
// supplier of the register once; an inactive supplier is a valid pick; a
// register supplier taken gets the file's bank account where its own is
// empty and keeps its own where it has one; and one deleted between the
// preview and the apply's transaction is a conflict.
func TestSaftImport_SupplierPicksAndBankFields(t *testing.T) {
	old := saftBooks(t)
	old.ledger(t)
	second := old.supplier(t, "Verksted AS", "")
	old.sys(t, "supplier_invoice", "2026-02-03", accounting.Source{Module: "accounting", Kind: "supplier_invoice", ID: 3},
		accounting.Line{Role: "payables", Amount: rat("-500"), SupplierID: &second},
		accounting.Line{AccountID: old.acc("7700"), Amount: rat("500")})
	_, files := old.export(t, map[string]any{"fiscalYear": 2026})

	setUpRegister := func(t *testing.T) (*harness, *modtest.Client, gen.AccountingSupplier, gen.AccountingSupplier, gen.AccountingSaftImport) {
		t.Helper()
		h := newHarness(t, modtest.WithSalesTax(fakeSalesTax{seller: fjordSeller}))
		c := h.SignIn(t, accountant...)
		made := func(body map[string]any) gen.AccountingSupplier {
			res := c.Do(http.MethodPost, base+"/suppliers", body)
			if res.Status != http.StatusCreated {
				t.Fatalf("a supplier = %d %s", res.Status, res.Body)
			}
			var s gen.AccountingSupplier
			res.JSON(&s)
			return s
		}
		kept := made(map[string]any{"name": "Verksted AS", "organisationNumber": "923456783", "bankAccount": "86011117947"})
		idle := made(map[string]any{"name": "Gammel leverandør"})
		res := c.Do(http.MethodPut, fmt.Sprintf("%s/suppliers/%d", base, idle.Id), map[string]any{"name": idle.Name, "active": false, "revision": idle.Revision})
		if res.Status != http.StatusOK {
			t.Fatalf("make it inactive = %d %s", res.Status, res.Body)
		}
		preview := uploadOK(t, c, "fjord.xml", files[0])
		return h, c, kept, idle, preview
	}
	apply := func(c *modtest.Client, id int64, picks map[string]any) *modtest.Response {
		body := map[string]any{"vatTerm": "bimonthly", "language": "nb", "fiscalYearStartMonth": 1}
		if picks != nil {
			body["suppliers"] = picks
		}
		return c.Do(http.MethodPost, fmt.Sprintf("%s/saft-imports/%d/apply", base, id), body)
	}

	h, c, kept, idle, preview := setUpRegister(t)
	if len(preview.Suppliers) != 2 {
		t.Fatalf("preview suppliers = %+v", preview.Suppliers)
	}
	var withOrg, without string
	for _, sp := range preview.Suppliers {
		if sp.Match != gen.SaftSupplierAmbiguous || sp.SupplierId != nil || len(sp.Candidates) != 1 || sp.Candidates[0].Id != kept.Id {
			t.Errorf("a supplier both match = %+v", sp)
		}
		if sp.OrganisationNumber != nil {
			withOrg = sp.FileSupplierId
		} else {
			without = sp.FileSupplierId
		}
	}
	blocked := false
	for _, b := range preview.Blockers {
		blocked = blocked || strings.Contains(b, "pick which it is")
	}
	if !blocked || withOrg == "" || without == "" {
		t.Fatalf("preview blockers = %v, suppliers %q %q", preview.Blockers, withOrg, without)
	}
	expectProblem(t, apply(c, preview.Id, nil), http.StatusConflict, "saft_import_ambiguous_suppliers")
	expectProblem(t, apply(c, preview.Id, map[string]any{withOrg: kept.Id}), http.StatusConflict, "saft_import_ambiguous_suppliers")
	expectProblem(t, apply(c, preview.Id, map[string]any{withOrg: kept.Id, without: kept.Id}), http.StatusBadRequest, "saft_import_supplier_picked_twice")
	expectProblem(t, apply(c, preview.Id, map[string]any{withOrg: kept.Id, without: 0, "no-such": 0}), http.StatusBadRequest, "saft_import_file_supplier_unknown")

	res := apply(c, preview.Id, map[string]any{withOrg: kept.Id, without: idle.Id})
	if res.Status != http.StatusCreated {
		t.Fatalf("apply = %d %s", res.Status, res.Body)
	}
	read := func(id int64) gen.AccountingSupplier {
		var s gen.AccountingSupplier
		c.Do(http.MethodGet, fmt.Sprintf("%s/suppliers/%d", base, id), nil).JSON(&s)
		return s
	}
	if got := read(kept.Id); deref(got.BankAccount) != "86011117947" || got.Revision != kept.Revision {
		t.Errorf("the register's own bank account = %q (revision %d, was %d), want it kept", deref(got.BankAccount), got.Revision, kept.Revision)
	}
	if got := read(idle.Id); deref(got.BankAccount) != "12345678903" || got.Active {
		t.Errorf("the inactive supplier picked = %+v, want the file's bank account and still inactive", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM accounting.suppliers`); n != 2 {
		t.Errorf("suppliers after the import = %d, want the two of the register", n)
	}

	// A supplier deleted after the plan was read, before the apply's
	// transaction takes the import: 409, nothing applied.
	h2, c2, kept2, _, preview2 := setUpRegister(t)
	var file2 string
	for _, sp := range preview2.Suppliers {
		if sp.OrganisationNumber != nil {
			file2 = sp.FileSupplierId
		}
	}
	other := ""
	for _, sp := range preview2.Suppliers {
		if sp.FileSupplierId != file2 {
			other = sp.FileSupplierId
		}
	}
	tx, err := h2.Pool().Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), `SELECT 1 FROM accounting.saft_imports WHERE id = $1 FOR UPDATE`, preview2.Id); err != nil {
		t.Fatal(err)
	}
	done := make(chan *modtest.Response, 1)
	go func() { done <- apply(c2, preview2.Id, map[string]any{file2: kept2.Id, other: 0}) }()
	deadline := time.Now().Add(10 * time.Second)
	for h2.Count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%LockSaftImport%'`) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the apply never waited on the import's lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := tx.Exec(t.Context(), `DELETE FROM accounting.suppliers WHERE id = $1`, kept2.Id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	expectProblem(t, <-done, http.StatusConflict, "saft_import_supplier_gone")
}
