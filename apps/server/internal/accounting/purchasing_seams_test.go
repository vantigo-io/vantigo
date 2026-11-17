package accounting_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

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
	expectProblem(t, put(b, s.Revision, map[string]any{"name": s.Name, "bankAccount": ""}), http.StatusForbidden, "supplier_fields_need_post")
	expectProblem(t, put(b, s.Revision, map[string]any{"name": s.Name, "defaultAccountId": chart["6000"].Id}), http.StatusForbidden, "supplier_fields_need_post")
	if res := b.Do(http.MethodGet, base+"/suppliers/lookup/brreg?q=x", nil); res.Status == http.StatusForbidden {
		t.Errorf("the buyer's lookup = %d", res.Status)
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
		payload->>'reference' FROM platform.events WHERE module = 'accounting' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var name, agg, supplierID, copyID, total, reference string
		if err := rows.Scan(&name, &agg, &supplierID, &copyID, &total, &reference); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.Join([]string{name, agg, supplierID, copyID, total, reference}, "|"))
	}
	rows.Close()
	sid, iid := fmt.Sprint(s.Id), fmt.Sprint(id)
	want := []string{"accounting.supplier_invoice.posted|" + iid + "|" + sid + "||1250.00|PO 1001",
		"accounting.supplier_invoice.reversed|" + iid + "|" + sid + "|" + fmt.Sprint(copied.Id) + "|1250.00|PO 1001"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("events =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	_ = other.Id

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
