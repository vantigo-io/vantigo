package invoices_test

import (
	"bytes"
	"context"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file tests brands on invoices (brands design B27, B28, B29, B29a;
// delivery E2) against Postgres and a modtest.FakeBrands: the default order,
// the per-document switch, the snapshot at issue and its immutability, the
// mail and the PDF reading the snapshot, a credit note's inheritance, a
// worker's draft, and that no campaign and no brand ever changes the seller.

// brandJSON is a document's brand as the wire carries it.
type brandJSON struct {
	ID          string  `json:"id"`
	Name        *string `json:"name"`
	Source      string  `json:"source"`
	Unavailable bool    `json:"unavailable"`
}

var (
	brandWorkspace = uuid.MustParse("00000000-0000-4000-8000-0000000000a1")
	brandNordlys   = uuid.MustParse("00000000-0000-4000-8000-0000000000a2")
	brandFjord     = uuid.MustParse("00000000-0000-4000-8000-0000000000a3")
	brandRetail    = uuid.MustParse("00000000-0000-4000-8000-0000000000a4")
)

// brandLogos are the brands' logos, each its own size so each hashes alone.
type brandLogos struct{ workspace, wordmark, markLight []byte }

// withBrands is an installation with a complete seller and three published
// brands: the workspace default (a light mark), Nordlys (a wordmark and a
// light mark) and Fjordkraft Demo (no logo at all).
func withBrands(t *testing.T, opts ...modtest.Option) (*harness, *modtest.FakeBrands, brandLogos) {
	t.Helper()
	fb := modtest.NewFakeBrands()
	logos := brandLogos{workspace: pngLogo(t, 200, 70), wordmark: pngLogo(t, 300, 60), markLight: pngLogo(t, 120, 120)}
	ref := func(b []byte) contracts.BrandLogoRef {
		fb.SetLogo(sha(b), contracts.BrandLogo{Bytes: b, ContentType: "image/png"})
		return contracts.BrandLogoRef{SHA256: sha(b), ContentType: "image/png"}
	}
	fb.Set(
		contracts.BrandEntry{ID: brandWorkspace, Name: "Kraft-Verket", Default: true, Logos: map[string]contracts.BrandLogoRef{"mark_light": ref(logos.workspace)}},
		contracts.BrandEntry{ID: brandNordlys, Name: "Nordlys", Logos: map[string]contracts.BrandLogoRef{"wordmark": ref(logos.wordmark), "mark_light": ref(logos.markLight)}},
		contracts.BrandEntry{ID: brandFjord, Name: "Fjordkraft Demo"},
	)
	h := newHarness(t, append([]modtest.Option{modtest.WithBrands(fb)}, opts...)...)
	saveSeller(t, h, completeSeller(1))
	return h, fb, logos
}

func brandOf(t *testing.T, inv invoiceJSON) brandJSON {
	t.Helper()
	if inv.Brand == nil {
		t.Fatalf("document %d carries no brand", inv.ID)
	}
	return *inv.Brand
}

// The brand a draft starts in is B28's order: the customer's own, its group's,
// the workspace default; an archived one is skipped and the draft says so.
func TestBrand_TheDefaultOrder(t *testing.T) {
	t.Parallel()
	h, fb, _ := withBrands(t)
	create := func() invoiceJSON {
		return createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	}

	if b := brandOf(t, create()); b.ID != brandWorkspace.String() || b.Source != "default" || b.Unavailable {
		t.Errorf("no choice = %+v, want the workspace default", b)
	}
	h.customers.setBrands(customerAcme, nil, &brandRetail)
	// An unknown group brand is skipped, with the notice.
	if b := brandOf(t, create()); b.ID != brandWorkspace.String() || b.Source != "default" || !b.Unavailable {
		t.Errorf("an unknown group brand = %+v, want the default with a notice", b)
	}
	h.customers.setBrands(customerAcme, nil, &brandFjord)
	if b := brandOf(t, create()); b.ID != brandFjord.String() || b.Source != "group" || b.Unavailable {
		t.Errorf("group = %+v, want Fjordkraft Demo from the group", b)
	}
	h.customers.setBrands(customerAcme, &brandNordlys, &brandFjord)
	if b := brandOf(t, create()); b.ID != brandNordlys.String() || b.Source != "customer" {
		t.Errorf("customer over group = %+v, want Nordlys from the customer", b)
	}

	// The customer's brand is archived since: the group's applies, with the notice.
	fb.Set(
		contracts.BrandEntry{ID: brandWorkspace, Name: "Kraft-Verket", Default: true},
		contracts.BrandEntry{ID: brandNordlys, Name: "Nordlys", State: contracts.BrandStateArchived},
		contracts.BrandEntry{ID: brandFjord, Name: "Fjordkraft Demo"},
	)
	d := create()
	if b := brandOf(t, d); b.ID != brandFjord.String() || b.Source != "group" || !b.Unavailable {
		t.Errorf("archived customer brand = %+v, want the group's with a notice", b)
	}
	if b := brandOf(t, getInvoice(t, h, d.ID)); !b.Unavailable || b.Name == nil || *b.Name != "Fjordkraft Demo" {
		t.Errorf("GET = %+v, want the notice kept", b)
	}
}

// The person writing the invoice switches the brand for this one document, on
// create and on replace; a brand that is not published is a 400 on brandId.
func TestBrand_ThePerDocumentSwitch(t *testing.T) {
	t.Parallel()
	h, fb, _ := withBrands(t)
	h.customers.setBrands(customerAcme, &brandNordlys, nil)

	body := draftBody(customerAcme, line("A", 1, 100, vat25))
	body["brandId"] = brandFjord.String()
	d := createDraft(t, h, body)
	if b := brandOf(t, d); b.ID != brandFjord.String() || b.Source != "chosen" {
		t.Fatalf("created with brandId = %+v, want chosen Fjordkraft Demo", b)
	}

	// A replace without brandId keeps the choice.
	put := draftBody(customerAcme, line("A", 1, 100, vat25))
	put["revision"], put["paymentTermsDays"] = d.Revision, 30
	res := creator(t, h).Do(http.MethodPut, invoicePath(d.ID), put)
	if res.Status != http.StatusOK {
		t.Fatalf("PUT = %d %s", res.Status, res.Body)
	}
	var kept invoiceJSON
	res.JSON(&kept)
	if b := brandOf(t, kept); b.ID != brandFjord.String() || b.Source != "chosen" {
		t.Errorf("replace without brandId = %+v, want the choice kept", b)
	}

	// A replace with another brand switches it.
	put["revision"], put["brandId"] = kept.Revision, brandWorkspace.String()
	res = creator(t, h).Do(http.MethodPut, invoicePath(d.ID), put)
	var switched invoiceJSON
	res.JSON(&switched)
	if b := brandOf(t, switched); res.Status != http.StatusOK || b.ID != brandWorkspace.String() || b.Source != "chosen" {
		t.Errorf("replace with brandId = %d %+v, want chosen workspace default", res.Status, b)
	}

	// Not published: refused on brandId, nothing saved.
	put["revision"], put["brandId"] = switched.Revision, brandRetail.String()
	if res = creator(t, h).Do(http.MethodPut, invoicePath(d.ID), put); res.Status != http.StatusBadRequest ||
		len(problemOf(t, res).Errors["brandId"]) == 0 {
		t.Errorf("an unknown brandId = %d %s, want a 400 on brandId", res.Status, res.Body)
	}
	body["brandId"] = brandRetail.String()
	if res = creator(t, h).Do(http.MethodPost, invoicesPath, body); res.Status != http.StatusBadRequest {
		t.Errorf("create with an unknown brandId = %d, want 400", res.Status)
	}

	// The brand archived between the choice and the issue: the issue falls
	// back to the customer's next in the order instead of refusing.
	other := createDraft(t, h, withBrand(draftBody(customerAcme, line("A", 1, 100, vat25)), brandWorkspace))
	fb.Set(
		contracts.BrandEntry{ID: brandWorkspace, Name: "Kraft-Verket", Default: true, State: contracts.BrandStateArchived},
		contracts.BrandEntry{ID: brandNordlys, Name: "Nordlys"},
	)
	if b := brandOf(t, getInvoice(t, h, other.ID)); !b.Unavailable {
		t.Errorf("a draft whose brand is archived = %+v, want unavailable", b)
	}
	inv := issued(t, h, other.ID)
	if b := brandOf(t, inv); b.ID != brandNordlys.String() || b.Unavailable {
		t.Errorf("issued after the archive = %+v, want the customer's Nordlys", b)
	}
}

func withBrand(body map[string]any, id uuid.UUID) map[string]any {
	body["brandId"] = id.String()
	return body
}

// snapshotOf is an issued document's brand columns.
func snapshotOf(t *testing.T, h *harness, id int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `SELECT coalesce(brand_id::text, '-') || '|' || coalesce(brand_name, '-') || '|' ||
		coalesce(brand_logo_object_key, '-') || '|' || coalesce(brand_logo_sha256, '-') FROM invoices.invoices WHERE id = $1`, id)
}

// The issue snapshots the brand's id, name and logo, the logo copied into
// invoices' own scope (the wordmark when the brand has one); renaming,
// re-logoing or archiving the brand later changes nothing of the issued
// document, and the trigger refuses an UPDATE of the snapshot.
func TestBrand_TheSnapshotAtIssueIsImmutable(t *testing.T) {
	t.Parallel()
	h, fb, _ := withBrands(t)
	h.customers.setBrands(customerAcme, &brandNordlys, nil)
	inv := issuedAcme(t, h)

	want := snapshotOf(t, h, inv.ID)
	key := modtest.One[string](t, h.Harness, `SELECT brand_logo_object_key FROM invoices.invoices WHERE id = $1`, inv.ID)
	if !strings.HasPrefix(want, brandNordlys.String()+"|Nordlys|branding/logo-") || !strings.HasPrefix(key, "branding/logo-") {
		t.Fatalf("snapshot = %s", want)
	}
	if w := storedWidth(t, h, key); w != 300 {
		t.Errorf("the copied logo is %d px wide, want the 300 px wordmark", w)
	}
	if b := brandOf(t, inv); b.ID != brandNordlys.String() || b.Name == nil || *b.Name != "Nordlys" || b.Source != "customer" || b.Unavailable {
		t.Errorf("response brand = %+v", b)
	}

	// The brand changes: renamed, another logo, archived.
	fb.Set(contracts.BrandEntry{ID: brandWorkspace, Name: "Kraft-Verket", Default: true},
		contracts.BrandEntry{ID: brandNordlys, Name: "Nordlys Renamed", State: contracts.BrandStateArchived})
	if got := snapshotOf(t, h, inv.ID); got != want {
		t.Errorf("after the brand changed the snapshot = %s, want %s", got, want)
	}
	if b := brandOf(t, getInvoice(t, h, inv.ID)); b.Name == nil || *b.Name != "Nordlys" || b.Unavailable {
		t.Errorf("GET after the change = %+v, want the snapshot", b)
	}
	if res := download(t, h, inv.ID); res.Status != http.StatusOK {
		t.Errorf("download = %d", res.Status)
	}

	for _, sql := range []string{
		`UPDATE invoices.invoices SET brand_name = 'Other' WHERE id = $1`,
		`UPDATE invoices.invoices SET brand_logo_object_key = NULL, brand_logo_sha256 = NULL WHERE id = $1`,
		`UPDATE invoices.invoices SET brand_id = NULL WHERE id = $1`,
	} {
		_, err := h.Pool().Exec(context.Background(), sql, inv.ID)
		if err == nil || !strings.Contains(err.Error(), "invoices: issued document is immutable") {
			t.Errorf("%s: %v, want the immutability refusal", sql, err)
		}
	}
}

// A brand with only a light mark prints that; one with no logo prints the
// seller's logo from the settings, its name still kept in the snapshot.
func TestBrand_TheLogoSlotAndTheFallback(t *testing.T) {
	t.Parallel()
	h, _, _ := withBrands(t)
	sellerLogo := withLogo(t, h, pngLogo(t, 240, 80)).Logo.Sha256

	// The workspace default has a light mark only.
	first := issuedAcme(t, h)
	key := modtest.One[string](t, h.Harness, `SELECT brand_logo_object_key FROM invoices.invoices WHERE id = $1`, first.ID)
	if w := storedWidth(t, h, key); w != 200 {
		t.Errorf("the copied logo is %d px wide, want the 200 px light mark", w)
	}

	h.customers.setBrands(customerAcme, &brandFjord, nil)
	second := issuedAcme(t, h)
	if got := snapshotOf(t, h, second.ID); got != brandFjord.String()+"|Fjordkraft Demo|-|-" {
		t.Errorf("snapshot = %s, want the name and no logo", got)
	}
	seller := `SELECT coalesce(seller_logo_sha256, '-') FROM invoices.invoices WHERE id = $1`
	if got := modtest.One[string](t, h.Harness, seller, second.ID); got != sellerLogo {
		t.Errorf("seller logo snapshot = %s, want the settings' %s", got, sellerLogo)
	}
}

// A mail of an issued invoice carries the snapshot's logo inline, and goes on
// doing so after the brand changed (the PDF and the reminder letters read the
// same snapshot through documentLogo).
func TestBrand_TheMailUsesTheSnapshot(t *testing.T) {
	t.Parallel()
	fake := &fakeSMTP{}
	h, fb, _ := withBrands(t, withChannel(fake.send))
	chooseChannel(t, h)
	h.customers.edit(customerAcme, func(p *contracts.CustomerBillingProfile) { p.InvoiceEmail = "faktura@acme.example" })
	h.customers.setBrands(customerAcme, &brandNordlys, nil)
	inv := issuedAcme(t, h)

	fb.Set(contracts.BrandEntry{ID: brandWorkspace, Name: "Kraft-Verket", Default: true})
	sent(t, h, inv.ID, nil)
	out := fake.last(t, 0).out
	if len(out.Attachments) != 2 {
		t.Fatalf("attachments = %d, want the PDF and the logo", len(out.Attachments))
	}
	key := modtest.One[string](t, h.Harness, `SELECT brand_logo_object_key FROM invoices.invoices WHERE id = $1`, inv.ID)
	if a := out.Attachments[1]; !a.Inline || a.ContentID != "logo" || sha(a.Content) != sha(h.objects.object(key)) {
		t.Errorf("the logo part is not the brand snapshot's logo")
	}
}

// A credit note inherits its invoice's brand snapshot, not a fresh resolution:
// the customer's default changing in between changes nothing.
func TestBrand_ACreditNoteInheritsItsInvoices(t *testing.T) {
	t.Parallel()
	h, _, _ := withBrands(t)
	h.customers.setBrands(customerAcme, &brandNordlys, nil)
	inv := issuedAcme(t, h)
	h.customers.setBrands(customerAcme, &brandFjord, nil)

	c := creditDraft(t, h, inv.ID)
	if b := brandOf(t, c); b.ID != brandNordlys.String() || b.Name == nil || *b.Name != "Nordlys" {
		t.Errorf("credit draft brand = %+v, want its invoice's", b)
	}
	// A credit note's brand is not switched.
	body := creditBody(c, creditLine(c.Lines[0]))
	body["brandId"] = brandFjord.String()
	if res := creator(t, h).Do(http.MethodPut, invoicePath(c.ID), body); res.Status != http.StatusBadRequest {
		t.Errorf("a credit note's brandId = %d, want 400", res.Status)
	}
	credit := issued(t, h, c.ID)
	if got, want := snapshotOf(t, h, credit.ID), snapshotOf(t, h, inv.ID); got != want {
		t.Errorf("credit note snapshot = %s, want its invoice's %s", got, want)
	}
}

// An invoice nobody chose a brand for — a quick invoice here, as a worker's
// draft — takes the customer's default, then the group's, then the
// workspace's, and is issued with its snapshot.
func TestBrand_AnInvoiceNobodyChoseGetsTheDefault(t *testing.T) {
	t.Parallel()
	h, _, _ := withBrands(t)
	h.customers.setBrands(customerAcme, nil, &brandNordlys)
	q := quickInvoiced(t, h, quickBody(customerAcme, line("A", 1, 100, vat25)))
	if b := brandOf(t, q.Invoice); b.ID != brandNordlys.String() || b.Source != "group" {
		t.Errorf("quick invoice brand = %+v, want the group's Nordlys", b)
	}
	if got := snapshotOf(t, h, q.Invoice.ID); !strings.HasPrefix(got, brandNordlys.String()+"|Nordlys|branding/logo-") {
		t.Errorf("quick invoice snapshot = %s", got)
	}
}

// Campaigns never apply to a document (B24): contracts.Brands answers the
// published version only, so whatever a campaign shows the app, an issued
// invoice's logo and name are the plain brand's. The fake stands in for the
// contract, which never returns an overlay by construction (contracts/brands.go);
// this asserts the document is made from exactly what it returns.
func TestBrand_NoCampaignAppliesToADocument(t *testing.T) {
	t.Parallel()
	h, _, _ := withBrands(t)
	h.customers.setBrands(customerAcme, &brandNordlys, nil)
	d := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	inv := issued(t, h, d.ID)
	key := modtest.One[string](t, h.Harness, `SELECT brand_logo_object_key FROM invoices.invoices WHERE id = $1`, inv.ID)
	if got := snapshotOf(t, h, inv.ID); !strings.Contains(got, "|Nordlys|") || storedWidth(t, h, key) != 300 {
		t.Errorf("snapshot = %s, want the plain brand's name and wordmark", got)
	}
}

// The brand changes a document's logo and nothing about the seller: every
// seller column is the settings' on documents in every brand (B29a).
func TestBrand_TheSellerStaysOne(t *testing.T) {
	t.Parallel()
	h, _, _ := withBrands(t)
	a := issuedAcme(t, h)
	h.customers.setBrands(customerAcme, &brandNordlys, nil)
	b := issuedAcme(t, h)
	sellers := `SELECT seller_legal_name || '|' || seller_organisation_number || '|' || coalesce(seller_bank_account, '') || '|' ||
		coalesce(seller_iban, '') || '|' || coalesce(seller_address_line1, '') || '|' || coalesce(seller_footer_text, '')
		FROM invoices.invoices WHERE id = $1`
	if x, y := modtest.One[string](t, h.Harness, sellers, a.ID), modtest.One[string](t, h.Harness, sellers, b.ID); x != y || x == "" {
		t.Errorf("seller of the default's invoice = %s, of Nordlys' = %s, want one identity", x, y)
	}
}

// The draft's preview prints with the brand's logo, and the editor's select is
// fed by the published brands, the default first.
func TestBrand_ThePreviewAndTheOptions(t *testing.T) {
	t.Parallel()
	h, _, _ := withBrands(t)
	h.customers.setBrands(customerAcme, &brandNordlys, nil)
	d := createDraft(t, h, draftBody(customerAcme, line("A", 1, 100, vat25)))
	if res := creator(t, h).Do(http.MethodGet, previewPath(d.ID), nil); res.Status != http.StatusOK {
		t.Errorf("preview = %d %s", res.Status, res.Body)
	}
	var opts []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		IsDefault bool   `json:"isDefault"`
	}
	res := creator(t, h).Do(http.MethodGet, invoicesPath+"/brands", nil)
	res.JSON(&opts)
	if res.Status != http.StatusOK || len(opts) != 3 || !opts[0].IsDefault || opts[0].Name != "Kraft-Verket" {
		t.Errorf("GET /invoices/brands = %d %+v, want three, the default first", res.Status, opts)
	}
	if res := h.SignIn(t, "invoices:access").Do(http.MethodGet, invoicesPath+"/brands", nil); res.Status != http.StatusForbidden {
		t.Errorf("GET /invoices/brands without invoices:create = %d, want 403", res.Status)
	}
}

// storedWidth is the width in pixels of the PNG stored under key.
func storedWidth(t *testing.T, h *harness, key string) int {
	t.Helper()
	cfg, err := png.DecodeConfig(bytes.NewReader(h.objects.object(key)))
	if err != nil {
		t.Fatalf("decode %s: %v", key, err)
	}
	return cfg.Width
}

// Without brands in the installation nothing changes: no brand on a document.
func TestBrand_NoBrandsNoBlock(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithBrands(modtest.NewFakeBrands()))
	saveSeller(t, h, completeSeller(1))
	inv := issuedAcme(t, h)
	if inv.Brand != nil {
		t.Errorf("brand = %+v, want none without a brands provider", inv.Brand)
	}
}

// An invoice made from orders starts in the orders' brand (brands design B28,
// delivery E3): the brand they were confirmed in, with source "order", when
// every order of the draft carries the same one and it is still published.
// Orders with different brands, or none, hand on none; a brand archived by now
// is skipped and the draft says so, falling to the customer's default order.
func TestBrand_AnInvoiceFromOrdersTakesTheOrdersBrand(t *testing.T) {
	t.Parallel()
	orders, holders := newFakeOrders(), newFakeHolders()
	h, fb, _ := withBrands(t, append(orders.options(), holders.options()...)...)
	branded := func(o contracts.DueOrder, brand *uuid.UUID) contracts.DueOrder {
		o.BrandID = brand
		return o
	}
	line1 := orderLine{id: 1, description: "Hylle", unit: "stk", quantity: "5", price: "1200.00000000", discount: "0.00", vatCode: vat25}
	line2 := orderLine{id: 2, description: "Bok", unit: "stk", quantity: "2", price: "399.00000000", discount: "0.00", vatCode: vat25}
	line3 := orderLine{id: 3, description: "Lampe", unit: "stk", quantity: "1", price: "99.00000000", discount: "0.00", vatCode: vat25}
	orders.put(branded(acmeOrder(order2210, 2210, "PO-1", "2026-09-01"), &brandNordlys), line1)
	orders.put(branded(acmeOrder(order2211, 2211, "PO-1", "2026-09-05"), &brandNordlys), line2)
	orders.put(branded(acmeOrder(order2212, 2212, "PO-1", "2026-09-06"), &brandFjord), line3)

	// The customer's own default is Fjordkraft; the orders were confirmed in
	// Nordlys, which wins.
	h.customers.setBrands(customerAcme, &brandFjord, nil)
	drafts := fromOrders(t, h, true, pick(order2210, 1, 5), pick(order2211, 2, 2))
	if len(drafts) != 1 {
		t.Fatalf("drafts %+v, want one", drafts)
	}
	if b := brandOf(t, getInvoice(t, h, drafts[0].InvoiceID)); b.ID != brandNordlys.String() || b.Source != "order" || b.Unavailable {
		t.Errorf("the draft's brand = %+v, want the orders' Nordlys, source order", b)
	}
	// Issued, the invoice carries the order's brand, name and logo.
	inv := issued(t, h, drafts[0].InvoiceID)
	if got := snapshotOf(t, h, inv.ID); !strings.HasPrefix(got, brandNordlys.String()+"|Nordlys|branding/logo-") {
		t.Errorf("snapshot = %s, want Nordlys with its logo", got)
	}

	// Orders of two brands hand on none: the customer's default order applies.
	orders.put(branded(acmeOrder(5213, 2213, "PO-1", "2026-09-01"), &brandNordlys), orderLine{id: 4, description: "Hylle", unit: "stk", quantity: "5", price: "1200.00000000", discount: "0.00", vatCode: vat25})
	drafts = fromOrders(t, h, true, pick(order2212, 3, 1), pick(5213, 4, 5))
	if b := brandOf(t, getInvoice(t, h, drafts[0].InvoiceID)); b.ID != brandFjord.String() || b.Source != "customer" {
		t.Errorf("mixed orders' brand = %+v, want the customer's default", b)
	}

	// An order's brand archived since: skipped, said so, the default order.
	fb.Set(
		contracts.BrandEntry{ID: brandWorkspace, Name: "Kraft-Verket", Default: true},
		contracts.BrandEntry{ID: brandNordlys, Name: "Nordlys", State: contracts.BrandStateArchived},
		contracts.BrandEntry{ID: brandFjord, Name: "Fjordkraft Demo"},
	)
	orders.put(branded(acmeOrder(5214, 2214, "PO-1", "2026-09-05"), &brandNordlys), orderLine{id: 5, description: "Bok", unit: "stk", quantity: "2", price: "399.00000000", discount: "0.00", vatCode: vat25})
	drafts = fromOrders(t, h, false, pick(5214, 5, 2))
	if b := brandOf(t, getInvoice(t, h, drafts[0].InvoiceID)); b.ID != brandFjord.String() || b.Source != "customer" || !b.Unavailable {
		t.Errorf("an archived order brand = %+v, want the customer's default, marked unavailable", b)
	}
}
