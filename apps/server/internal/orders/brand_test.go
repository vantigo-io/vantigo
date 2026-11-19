package orders_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/orders"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// This file tests brands on quotes and orders (brands design B27, B28, B29,
// B29a; delivery E3) against Postgres and a modtest.FakeBrands: the default
// order, the per-document switch, the snapshot at send and at confirm and that
// it stays the same, the PDF and the mail printing it, the brand handed to
// Invoices, and that no campaign and no brand ever changes the seller.

var (
	brandWorkspace = uuid.MustParse("00000000-0000-4000-8000-0000000000b1")
	brandNordlys   = uuid.MustParse("00000000-0000-4000-8000-0000000000b2")
	brandFjord     = uuid.MustParse("00000000-0000-4000-8000-0000000000b3")
	brandRetail    = uuid.MustParse("00000000-0000-4000-8000-0000000000b4")
)

// brandLogos are the brands' logos, each its own size so each hashes alone.
type brandLogos struct{ workspace, wordmark, markLight []byte }

func grayPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func shaOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// everything is the keys of a person who quotes, sells, confirms and sets up
// subscriptions.
var everything = []string{"orders:access", "orders:quotes-manage", "orders:sales-manage", "orders:sales-confirm", "orders:subscriptions-manage"}

// brandHarness is an installation with four published brands: the workspace
// default (a light mark), Nordlys (a wordmark and a light mark), Fjordkraft
// Demo (no logo at all) and Retail (a light mark).
func brandHarness(t *testing.T, o harnessOptions) (*harness, *modtest.FakeBrands, brandLogos) {
	t.Helper()
	fb := modtest.NewFakeBrands()
	logos := brandLogos{workspace: grayPNG(t, 200, 70), wordmark: grayPNG(t, 300, 60), markLight: grayPNG(t, 120, 120)}
	ref := func(b []byte) contracts.BrandLogoRef {
		fb.SetLogo(shaOf(b), contracts.BrandLogo{Bytes: b, ContentType: "image/png"})
		return contracts.BrandLogoRef{SHA256: shaOf(b), ContentType: "image/png"}
	}
	fb.Set(
		contracts.BrandEntry{ID: brandWorkspace, Name: "Kraft-Verket", Default: true, Logos: map[string]contracts.BrandLogoRef{"mark_light": ref(logos.workspace)}},
		contracts.BrandEntry{ID: brandNordlys, Name: "Nordlys", Logos: map[string]contracts.BrandLogoRef{"wordmark": ref(logos.wordmark), "mark_light": ref(logos.markLight)}},
		contracts.BrandEntry{ID: brandFjord, Name: "Fjordkraft Demo"},
		contracts.BrandEntry{ID: brandRetail, Name: "Retail", Logos: map[string]contracts.BrandLogoRef{"mark_light": ref(logos.markLight)}},
	)
	o.brands = fb
	return newHarness(t, o), fb, logos
}

// modelOf is the words of the PDF a test was served.
func modelOf(t *testing.T, pdf []byte) orders.PDFModel {
	t.Helper()
	pdfModels.mu.Lock()
	defer pdfModels.mu.Unlock()
	m, ok := pdfModels.byPDF[sha256.Sum256(pdf)]
	if !ok {
		t.Fatal("no PDF model was rendered to these bytes")
	}
	return m
}

// logoKeys are the logos stored in this module's scope.
func logoKeys(h *harness) []string {
	var out []string
	for _, k := range h.objects.keys() {
		if strings.Contains(k, "branding/logo-") {
			out = append(out, k)
		}
	}
	return out
}

func newQuoteBody(customer int32, brand *uuid.UUID) gen.OrdersQuoteInput {
	lines := []gen.OrdersLineInput{}
	return gen.OrdersQuoteInput{ClientRequestId: uuid.New(), CustomerId: customer, Lines: &lines, BrandId: brand}
}

func brandOfQuote(t *testing.T, q gen.OrdersQuote) gen.OrdersBrand {
	t.Helper()
	if q.Brand == nil {
		t.Fatalf("quote %d carries no brand", q.Id)
	}
	return *q.Brand
}

func brandOfOrder(t *testing.T, o gen.OrdersOrder) gen.OrdersBrand {
	t.Helper()
	if o.Brand == nil {
		t.Fatalf("order %d carries no brand", o.Id)
	}
	return *o.Brand
}

// The brand a draft starts in is B28's order: the customer's own, its group's,
// the workspace default; an archived one is skipped and the draft says so.
func TestBrand_TheDefaultOrder(t *testing.T) {
	t.Parallel()
	h, fb, _ := brandHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	create := func() gen.OrdersQuote { return createQuote(t, c, custBusiness) }

	if b := brandOfQuote(t, create()); b.Id != brandWorkspace || b.Source != gen.OrdersBrandSourceDefault || b.Unavailable ||
		b.Name == nil || *b.Name != "Kraft-Verket" {
		t.Errorf("no choice = %+v, want the workspace default", b)
	}
	h.dir.setBrands(custBusiness, nil, &brandRetail)
	if b := brandOfQuote(t, create()); b.Id != brandRetail || b.Source != gen.OrdersBrandSourceGroup {
		t.Errorf("a group's brand = %+v", b)
	}
	h.dir.setBrands(custBusiness, &brandNordlys, &brandRetail)
	if b := brandOfQuote(t, create()); b.Id != brandNordlys || b.Source != gen.OrdersBrandSourceCustomer {
		t.Errorf("the customer's own brand = %+v, want it over the group's", b)
	}

	// The customer's brand is archived: the group's is next, and the draft says
	// a brand higher in the order was skipped.
	fb.Set(contracts.BrandEntry{ID: brandWorkspace, Name: "Kraft-Verket", Default: true},
		contracts.BrandEntry{ID: brandNordlys, Name: "Nordlys", State: contracts.BrandStateArchived},
		contracts.BrandEntry{ID: brandRetail, Name: "Retail"})
	if b := brandOfQuote(t, create()); b.Id != brandRetail || b.Source != gen.OrdersBrandSourceGroup || !b.Unavailable {
		t.Errorf("an archived own brand = %+v, want the group's, marked unavailable", b)
	}
	h.dir.setBrands(custBusiness, &brandNordlys, nil)
	if b := brandOfQuote(t, create()); b.Id != brandWorkspace || b.Source != gen.OrdersBrandSourceDefault || !b.Unavailable {
		t.Errorf("an archived own brand and no group = %+v, want the default, marked unavailable", b)
	}

	// An order starts in the same chain.
	s := h.SignIn(t, seller...)
	h.dir.setBrands(custBusiness, &brandRetail, nil)
	if b := brandOfOrder(t, createOrder(t, s, custBusiness, productLine(varBolt, 1))); b.Id != brandRetail || b.Source != gen.OrdersBrandSourceCustomer {
		t.Errorf("an order's brand = %+v, want the customer's", b)
	}
}

// A person may switch the brand for this one document; a brand that is not
// published is a field error, and a draft keeps a chosen brand when its
// customer changes. The editor's select is fed by the published brands.
func TestBrand_SwitchForOneDocument(t *testing.T) {
	t.Parallel()
	h, _, _ := brandHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	h.dir.setBrands(custBusiness, &brandRetail, nil)

	res := c.Do(http.MethodPost, quotesPath, newQuoteBody(custBusiness, &brandFjord))
	mustStatus(t, res, http.StatusCreated, "create in a chosen brand")
	q := quoteOf(t, res)
	if b := brandOfQuote(t, q); b.Id != brandFjord || b.Source != gen.OrdersBrandSourceChosen {
		t.Errorf("chosen = %+v", b)
	}
	unknown := uuid.New()
	expectRefusal(t, c.Do(http.MethodPost, quotesPath, newQuoteBody(custBusiness, &unknown)), http.StatusBadRequest, "", "an unknown brand")

	update := func(brand *uuid.UUID, customer int32) *modtest.Response {
		return c.Do(http.MethodPut, quotePath(q.Id), gen.OrdersQuoteUpdate{
			ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: q.Revision, CustomerId: customer, Lines: []gen.OrdersLineInput{}, BrandId: brand,
		})
	}
	mustStatus(t, update(nil, custBusiness), http.StatusOK, "save without a brand")
	if got := quoteOf(t, c.Do(http.MethodGet, quotePath(q.Id), nil)); brandOfQuote(t, got).Id != brandFjord {
		t.Errorf("a save without brandId moved the brand to %+v", got.Brand)
	}
	// The customer changes and the brand was chosen: it stays.
	mustStatus(t, update(nil, custPerson), http.StatusOK, "save for another customer")
	if got := quoteOf(t, c.Do(http.MethodGet, quotePath(q.Id), nil)); brandOfQuote(t, got).Id != brandFjord {
		t.Errorf("a chosen brand moved with the customer to %+v", got.Brand)
	}
	mustStatus(t, update(&brandNordlys, custPerson), http.StatusOK, "switch the brand")
	if got := quoteOf(t, c.Do(http.MethodGet, quotePath(q.Id), nil)); brandOfQuote(t, got).Id != brandNordlys {
		t.Errorf("switched = %+v", got.Brand)
	}
	expectRefusal(t, update(&unknown, custPerson), http.StatusBadRequest, "", "switch to an unknown brand")

	// A draft that took its brand from the defaults follows a new customer.
	h.dir.setBrands(custPerson, &brandRetail, nil)
	d := createQuote(t, c, custBusiness)
	mustStatus(t, c.Do(http.MethodPut, quotePath(d.Id), gen.OrdersQuoteUpdate{
		ExpectedUpdatedAt: quoteAt(t, c, d.Id), Revision: d.Revision, CustomerId: custPerson, Lines: []gen.OrdersLineInput{},
	}), http.StatusOK, "save for another customer")
	if got := quoteOf(t, c.Do(http.MethodGet, quotePath(d.Id), nil)); brandOfQuote(t, got).Id != brandRetail {
		t.Errorf("a defaulted brand did not follow the customer: %+v", got.Brand)
	}

	// The picker: the published brands, the workspace default first.
	var opts []gen.OrdersBrandOption
	res = c.Do(http.MethodGet, "/api/v1/orders/brands", nil)
	res.JSON(&opts)
	if res.Status != http.StatusOK || len(opts) != 4 || !opts[0].IsDefault || opts[0].Name != "Kraft-Verket" {
		t.Errorf("GET /orders/brands = %d %+v, want four, the default first", res.Status, opts)
	}
	mustStatus(t, h.SignIn(t).Do(http.MethodGet, "/api/v1/orders/brands", nil), http.StatusForbidden, "the picker without access")

	// An order is switched the same way, and a confirmed order's brand is frozen.
	s := h.SignIn(t, boss...)
	o := createOrder(t, s, custBusiness, productLine(varBolt, 1))
	res = save(s, o, keep(o), func(b *gen.OrdersOrderUpdate) { b.BrandId = &brandNordlys })
	mustStatus(t, res, http.StatusOK, "switch an order's brand")
	o = orderOf(t, res)
	if b := brandOfOrder(t, o); b.Id != brandNordlys || b.Source != gen.OrdersBrandSourceChosen {
		t.Errorf("order switched = %+v", b)
	}
	o = confirmOrder(t, s, o)
	expectRefusal(t, save(s, o, keep(o), func(b *gen.OrdersOrderUpdate) { b.BrandId = &brandFjord }), http.StatusBadRequest, "", "switch a confirmed order's brand")
	mustStatus(t, save(s, o, keep(o), func(b *gen.OrdersOrderUpdate) { b.BrandId = &brandNordlys }), http.StatusOK, "its own brand again")
}

// Sending a quote snapshots the brand's name and logo into this module's own
// scope, and a later change of the brand — a new logo, a new name, an archive —
// changes nothing of the sent quote: not its view, not its PDF, not the mail.
// The snapshot is immutable in the table.
func TestBrand_AQuoteSnapshotsAtSendAndStaysTheSame(t *testing.T) {
	t.Parallel()
	h, fb, logos := brandHarness(t, harnessOptions{communications: true})
	chooseChannel(t, h)
	c := h.SignIn(t, quoter...)
	h.dir.setBrands(custBusiness, &brandNordlys, nil)
	q := createQuote(t, c, custBusiness, productLine(varBolt, 1))

	// A draft previews with the brand as it is now: the wordmark as the brand
	// holds it, nothing copied yet.
	draft := c.Do(http.MethodGet, quotePath(q.Id, "pdf"), nil)
	mustStatus(t, draft, http.StatusOK, "the draft's PDF")
	if m := modelOf(t, draft.Body); !bytes.Equal(m.Logo, logos.wordmark) {
		t.Errorf("a draft prints a %d-byte logo, want the brand's wordmark", len(m.Logo))
	}
	if keys := logoKeys(h); len(keys) != 0 {
		t.Errorf("a preview copied %v", keys)
	}

	res := c.Do(http.MethodPost, quotePath(q.Id, "send"), gen.OrdersSendInput{ExpectedUpdatedAt: quoteAt(t, c, q.Id),
		Recipient: "kari@acme.example", Revision: &q.Revision})
	mustStatus(t, res, http.StatusOK, "send")
	sent := quoteOf(t, res)
	if b := brandOfQuote(t, sent); b.Id != brandNordlys || b.Name == nil || *b.Name != "Nordlys" || b.Unavailable || b.Source != gen.OrdersBrandSourceCustomer {
		t.Errorf("sent brand = %+v, want the Nordlys snapshot", b)
	}
	keys := logoKeys(h)
	if len(keys) != 1 {
		t.Fatalf("stored logos %v, want the one copy", keys)
	}
	copied := h.objects.object(keys[0])
	if cfg, err := png.DecodeConfig(bytes.NewReader(copied)); err != nil || cfg.Width != 300 {
		t.Errorf("the copied logo is %v wide (%v), want the wordmark's 300", cfg.Width, err)
	}
	key := modtest.One[string](t, h.Harness, `SELECT brand_logo_object_key FROM orders.quotes WHERE id = $1`, q.Id)
	if !strings.HasSuffix(keys[0], key) || !strings.HasPrefix(key, "branding/logo-") {
		t.Errorf("snapshot key %q, stored %q", key, keys[0])
	}

	mails := h.mail.Sent()
	if len(mails) != 1 || len(mails[0].Out.Attachments) != 1 {
		t.Fatalf("mails %+v, want the one with the PDF", mails)
	}
	mailed := mails[0].Out.Attachments[0].Content
	if m := modelOf(t, mailed); !bytes.Equal(m.Logo, copied) {
		t.Error("the mailed PDF does not print the snapshot's logo")
	}

	// The brand is renamed, given another logo and archived.
	other := grayPNG(t, 410, 90)
	fb.SetLogo(shaOf(other), contracts.BrandLogo{Bytes: other, ContentType: "image/png"})
	fb.Set(contracts.BrandEntry{ID: brandWorkspace, Name: "Kraft-Verket", Default: true},
		contracts.BrandEntry{ID: brandNordlys, Name: "Nordlys 2", State: contracts.BrandStateArchived,
			Logos: map[string]contracts.BrandLogoRef{"wordmark": {SHA256: shaOf(other), ContentType: "image/png"}}})
	got := quoteOf(t, c.Do(http.MethodGet, quotePath(q.Id), nil))
	if b := brandOfQuote(t, got); b.Id != brandNordlys || b.Name == nil || *b.Name != "Nordlys" || b.Unavailable {
		t.Errorf("brand after the change = %+v, want the snapshot as it was", b)
	}
	stored := c.Do(http.MethodGet, quotePath(q.Id, "pdf"), nil)
	mustStatus(t, stored, http.StatusOK, "the stored PDF")
	if !bytes.Equal(stored.Body, mailed) {
		t.Error("the stored PDF changed after the brand did")
	}
	proforma := c.Do(http.MethodGet, quotePath(q.Id, "proforma"), nil)
	mustStatus(t, proforma, http.StatusOK, "the pro forma")
	if m := modelOf(t, proforma.Body); !bytes.Equal(m.Logo, copied) {
		t.Error("a sent quote's pro forma does not print its snapshot")
	}
	if n := len(logoKeys(h)); n != 1 {
		t.Errorf("%d logos stored after the change, want the one copy", n)
	}

	// The snapshot is immutable in the table.
	for _, stmt := range []string{
		`UPDATE orders.quotes SET brand_name = 'Other' WHERE id = $1`,
		`UPDATE orders.quotes SET brand_logo_object_key = NULL, brand_logo_sha256 = NULL WHERE id = $1`,
		`UPDATE orders.quotes SET brand_id = NULL WHERE id = $1`,
	} {
		if _, err := h.Pool().Exec(context.Background(), stmt, q.Id); err == nil || !strings.Contains(err.Error(), "a sent quote keeps its brand") {
			t.Errorf("%s: %v, want the trigger's refusal", stmt, err)
		}
	}
}

// A revise opens the brand again, with no snapshot, and the next send takes a
// new one in the brand the person chose.
func TestBrand_AReviseOpensTheBrand(t *testing.T) {
	t.Parallel()
	h, _, _ := brandHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	h.dir.setBrands(custBusiness, &brandNordlys, nil)
	q := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 1)))
	if b := brandOfQuote(t, q); b.Name == nil || *b.Name != "Nordlys" {
		t.Fatalf("sent brand %+v", b)
	}
	res := c.Do(http.MethodPost, quotePath(q.Id, "revise"), gen.OrdersRevisionInput{ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: 1})
	mustStatus(t, res, http.StatusOK, "revise")
	d := quoteOf(t, res)
	if d.Status != gen.OrdersQuoteStatusDraft || d.Brand == nil || d.Brand.Id != brandNordlys {
		t.Fatalf("revised %+v, want a draft still in Nordlys", d.Brand)
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.quotes WHERE id = $1 AND brand_name IS NULL AND brand_logo_sha256 IS NULL`, q.Id); n != 1 {
		t.Error("a revise kept the snapshot")
	}
	mustStatus(t, c.Do(http.MethodPut, quotePath(q.Id), gen.OrdersQuoteUpdate{
		ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: 2, CustomerId: custBusiness, Lines: []gen.OrdersLineInput{productLine(varBolt, 1)}, BrandId: &brandFjord,
	}), http.StatusOK, "switch the brand of the revision")
	again := markSent(t, c, quoteOf(t, c.Do(http.MethodGet, quotePath(q.Id), nil)))
	if b := brandOfQuote(t, again); b.Id != brandFjord || b.Name == nil || *b.Name != "Fjordkraft Demo" {
		t.Errorf("resent brand %+v, want Fjordkraft Demo", b)
	}
}

// Confirming an order snapshots the brand as sending a quote does, and every
// version of the confirmation prints it, whatever becomes of the brand.
func TestBrand_AnOrderSnapshotsAtConfirmAndStaysTheSame(t *testing.T) {
	t.Parallel()
	h, fb, _ := brandHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	h.dir.setBrands(custBusiness, &brandNordlys, nil)
	o := createOrder(t, c, custBusiness, productLine(varBolt, 1))
	if keys := logoKeys(h); len(keys) != 0 {
		t.Fatalf("a draft copied %v", keys)
	}
	o = confirmOrder(t, c, o)
	if b := brandOfOrder(t, o); b.Id != brandNordlys || b.Name == nil || *b.Name != "Nordlys" || b.Unavailable {
		t.Errorf("confirmed brand = %+v, want the Nordlys snapshot", b)
	}
	keys := logoKeys(h)
	if len(keys) != 1 {
		t.Fatalf("stored logos %v, want the one copy", keys)
	}
	copied := h.objects.object(keys[0])

	fb.Set(contracts.BrandEntry{ID: brandWorkspace, Name: "Kraft-Verket", Default: true},
		contracts.BrandEntry{ID: brandNordlys, Name: "Renamed", State: contracts.BrandStateArchived})
	first := c.Do(http.MethodGet, orderPath(o.Id, "pdf"), nil)
	mustStatus(t, first, http.StatusOK, "the confirmation")
	if m := modelOf(t, first.Body); !bytes.Equal(m.Logo, copied) {
		t.Error("the confirmation does not print the snapshot's logo")
	}

	// A confirmed edit makes version 2, printed later from the snapshot.
	res := save(c, o, keep(o), func(b *gen.OrdersOrderUpdate) { b.ContactId = i32(contactKari) })
	mustStatus(t, res, http.StatusOK, "edit a confirmed order")
	o = orderOf(t, res)
	if o.Version != 2 {
		t.Fatalf("version %d, want 2", o.Version)
	}
	second := c.Do(http.MethodGet, orderPath(o.Id, "pdf"), nil)
	mustStatus(t, second, http.StatusOK, "the revised confirmation")
	if m := modelOf(t, second.Body); !bytes.Equal(m.Logo, copied) {
		t.Error("the revised confirmation does not print the snapshot's logo")
	}
	if b := brandOfOrder(t, orderOf(t, c.Do(http.MethodGet, orderPath(o.Id), nil))); b.Name == nil || *b.Name != "Nordlys" {
		t.Errorf("the order's brand moved to %+v", b)
	}

	for _, stmt := range []string{
		`UPDATE orders.orders SET brand_name = 'Other' WHERE id = $1`,
		`UPDATE orders.orders SET brand_id = NULL WHERE id = $1`,
	} {
		if _, err := h.Pool().Exec(context.Background(), stmt, o.Id); err == nil || !strings.Contains(err.Error(), "a confirmed order keeps its brand") {
			t.Errorf("%s: %v, want the trigger's refusal", stmt, err)
		}
	}
}

// An order made from a quote starts in the quote's brand; confirmed at once it
// carries the quote's snapshot, the same copy of the logo; a subscription made
// from one keeps the brand for its invoices.
func TestBrand_AnAcceptedQuoteHandsItsBrandOn(t *testing.T) {
	t.Parallel()
	h, _, _ := brandHarness(t, harnessOptions{})
	withServices(h)
	c := h.SignIn(t, everything...)
	h.dir.setBrands(custBusiness, &brandNordlys, nil)
	q := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 1)))
	// The customer's default moves on; the quote keeps the brand it was sent in.
	h.dir.setBrands(custBusiness, &brandRetail, nil)

	res := c.Do(http.MethodPost, quotePath(q.Id, "accept"), gen.OrdersAcceptInput{ExpectedUpdatedAt: quoteAt(t, c, q.Id), Revision: 1, Into: gen.OrdersAcceptInputIntoConfirmedOrder})
	mustStatus(t, res, http.StatusOK, "accept and confirm")
	var got gen.OrdersAccepted
	res.JSON(&got)
	if b := brandOfOrder(t, *got.Order); b.Id != brandNordlys || b.Name == nil || *b.Name != "Nordlys" {
		t.Errorf("the order's brand %+v, want the quote's snapshot", b)
	}
	same := modtest.One[bool](t, h.Harness, `SELECT o.brand_logo_object_key = q.brand_logo_object_key AND o.brand_logo_sha256 = q.brand_logo_sha256
		FROM orders.orders o, orders.quotes q WHERE o.id = $1 AND q.id = $2`, got.Order.Id, q.Id)
	if !same {
		t.Error("the order did not take the quote's copy of the logo")
	}
	if n := len(logoKeys(h)); n != 1 {
		t.Errorf("%d logos stored, want the one copy both share", n)
	}

	// Into a draft order: the brand the quote carried, snapshotted when confirmed.
	d := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 1)))
	res = c.Do(http.MethodPost, quotePath(d.Id, "accept"), gen.OrdersAcceptInput{ExpectedUpdatedAt: quoteAt(t, c, d.Id), Revision: 1, Into: gen.OrdersAcceptInputIntoDraftOrder})
	mustStatus(t, res, http.StatusOK, "accept into a draft")
	res.JSON(&got)
	if b := brandOfOrder(t, *got.Order); b.Id != brandRetail {
		t.Errorf("the draft order's brand %+v, want the quote's Retail", b)
	}

	// Into a subscription.
	sq := markSent(t, c, createQuote(t, c, custBusiness, productLine(varSupport, 1)))
	plan := &gen.OrdersSubscriptionPlan{Interval: gen.OrdersSubscriptionPlanIntervalYearly, Billing: gen.OrdersSubscriptionPlanBillingInAdvance, StartDate: date(2027, 1, 1)}
	res = c.Do(http.MethodPost, quotePath(sq.Id, "accept"), gen.OrdersAcceptInput{
		ExpectedUpdatedAt: quoteAt(t, c, sq.Id), Revision: 1, Into: gen.OrdersAcceptInputIntoSubscription, Plan: plan,
	})
	mustStatus(t, res, http.StatusOK, "accept into a subscription")
	res.JSON(&got)
	if id := modtest.One[uuid.UUID](t, h.Harness, `SELECT brand_id FROM orders.subscriptions WHERE id = $1`, got.Subscription.Id); id != brandRetail {
		t.Errorf("the subscription's brand %s, want the quote's", id)
	}
}

// Orders hands the brand to Invoices: DueOrder carries it, for a draft and for
// a confirmed order.
func TestBrand_DueOrderCarriesTheBrand(t *testing.T) {
	t.Parallel()
	h, _, _ := brandHarness(t, harnessOptions{})
	c := h.SignIn(t, boss...)
	provider := orders.Module().BillableOrders(h.Deps())
	h.dir.setBrands(custBusiness, &brandNordlys, nil)
	o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 3)))
	h.dir.setBrands(custBusiness, &brandRetail, nil)
	draft := createOrder(t, c, custBusiness, productLine(varBolt, 1))

	page, err := provider.DueOrders(context.Background(), contracts.DueOrdersRequest{OrderIDs: []int64{o.Id, draft.Id}})
	if err != nil || len(page.Orders) != 2 {
		t.Fatalf("due %+v, %v; want both orders", page, err)
	}
	for _, d := range page.Orders {
		want := brandNordlys
		if d.ID == draft.Id {
			want = brandRetail
		}
		if d.BrandID == nil || *d.BrandID != want {
			t.Errorf("order %d's due brand %v, want %s", d.ID, d.BrandID, want)
		}
	}
}

// Campaigns never apply to a document (B24): contracts.Brands answers the
// published version only, so whatever a campaign shows the app, a sent quote's
// logo and name are the plain brand's. The fake stands in for the contract,
// which never returns an overlay by construction (contracts/brands.go); this
// asserts the document is made from exactly what it returns. A brand changes
// the logo and nothing about the seller.
func TestBrand_NoCampaignAppliesAndTheSellerStaysOne(t *testing.T) {
	t.Parallel()
	h, _, logos := brandHarness(t, harnessOptions{})
	h.tax.sellerP = contracts.SellerProfile{LegalName: "Kraft-Verket AS", OrganisationNumber: "123456789", AddressLine1: "Storgata 1", PostalCode: "0150", City: "Oslo", Country: "no"}
	c := h.SignIn(t, quoter...)
	h.dir.setBrands(custBusiness, &brandNordlys, nil)
	a := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 1)))
	h.dir.setBrands(custBusiness, &brandRetail, nil)
	b := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 1)))

	pdf := func(q gen.OrdersQuote) orders.PDFModel {
		res := c.Do(http.MethodGet, quotePath(q.Id, "pdf"), nil)
		mustStatus(t, res, http.StatusOK, "pdf")
		return modelOf(t, res.Body)
	}
	pa, pb := pdf(a), pdf(b)
	if cfg, err := png.DecodeConfig(bytes.NewReader(pa.Logo)); err != nil || cfg.Width != 300 {
		t.Errorf("Nordlys' quote prints a logo %v wide, want the plain brand's wordmark %d bytes", cfg.Width, len(logos.wordmark))
	}
	if len(pa.Seller) == 0 || strings.Join(pa.Seller, "|") != strings.Join(pb.Seller, "|") || pa.Seller[0] != "Kraft-Verket AS" {
		t.Errorf("sellers %v and %v, want one identity in every brand", pa.Seller, pb.Seller)
	}
	for _, line := range pa.Seller {
		if strings.Contains(line, "Nordlys") {
			t.Errorf("the seller block names the brand: %q", line)
		}
	}
}

// Without brands in the installation nothing changes: no brand on a document,
// and a send and a confirm work as before. A brand with no logo prints none.
func TestBrand_NoBrandsNoBlock(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{brands: modtest.NewFakeBrands()})
	c := h.SignIn(t, everything...)
	q := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 1)))
	if q.Brand != nil {
		t.Errorf("quote brand %+v, want none without brands", q.Brand)
	}
	if o := confirmOrder(t, c, createOrder(t, c, custBusiness, productLine(varBolt, 1))); o.Brand != nil {
		t.Errorf("order brand %+v, want none without brands", o.Brand)
	}

	h, _, _ = brandHarness(t, harnessOptions{})
	c = h.SignIn(t, quoter...)
	h.dir.setBrands(custBusiness, &brandFjord, nil)
	sent := markSent(t, c, createQuote(t, c, custBusiness, productLine(varBolt, 1)))
	if b := brandOfQuote(t, sent); b.Name == nil || *b.Name != "Fjordkraft Demo" {
		t.Errorf("sent brand %+v", b)
	}
	res := c.Do(http.MethodGet, quotePath(sent.Id, "pdf"), nil)
	mustStatus(t, res, http.StatusOK, "the pdf")
	if m := modelOf(t, res.Body); m.Logo != nil {
		t.Error("a brand with no logo printed one")
	}
	if n := h.Count(t, `SELECT count(*) FROM orders.quotes WHERE id = $1 AND brand_logo_object_key IS NULL`, sent.Id); n != 1 {
		t.Error("a brand with no logo has a logo key")
	}
}
