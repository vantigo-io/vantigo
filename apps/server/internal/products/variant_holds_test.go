package products_test

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/products"
)

// This file pins inventory design I23 and §9 from Products' side: a variant
// a holder (Inventory) still keeps stock of cannot be deleted, and its
// product cannot turn from Goods into a Service. Inventory itself is never
// composed here — depguard keeps it out of this package — so a fake holder
// stands in through modtest.WithVariantReferenceHolders.

// fakeHolder holds the one variant whose id is in held, with hold; every
// other variant it holds nothing of. held is atomic because the variant's id
// is known only after the harness that carries the holder is built.
type fakeHolder struct {
	held atomic.Int32
	hold contracts.VariantHold
	// asked counts the questions, so a test can prove the holder was asked
	// inside the delete at all.
	asked atomic.Int32
	// purpose is the last purpose it was asked for.
	purpose atomic.Value
}

func (f *fakeHolder) VariantHeld(_ context.Context, tx pgx.Tx, variantID int32, purpose contracts.VariantHoldPurpose) (*contracts.VariantHold, error) {
	f.asked.Add(1)
	f.purpose.Store(purpose)
	if tx == nil {
		return nil, fmt.Errorf("fakeHolder: asked without a transaction")
	}
	if variantID != f.held.Load() {
		return nil, nil
	}
	hold := f.hold
	return &hold, nil
}

// productWithTwoVariants creates a product and adds a second variant, so a
// delete gets past the last-variant check, and answers both variants.
func productWithTwoVariants(t *testing.T, c *modtest.Client, taxCategoryID int32, name string) (productJSON, variantJSON, variantJSON) {
	t.Helper()
	created := createProduct(t, c, newProductBody(taxCategoryID, name, sku(t, "held-first")))
	add := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants", created.Id), map[string]any{
		"sku": sku(t, "held-second"), "unit": "pcs",
	})
	if add.Status != http.StatusCreated {
		t.Fatalf("add second variant: status %d body %s, want 201", add.Status, add.Body)
	}
	var second variantJSON
	add.JSON(&second)
	return created, created.Variants[0], second
}

func TestDeleteVariant_HeldByAHolder_IsRefusedWithVariantHasStock(t *testing.T) {
	t.Parallel()
	holder := &fakeHolder{hold: contracts.VariantHold{Quantity: "34.000", Locations: 3}}
	h := newHarness(t, modtest.WithVariantReferenceHolders(holder))
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created, _, second := productWithTwoVariants(t, c, taxCategoryID, "Held Widget")
	holder.held.Store(second.Id)

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, second.Id), nil)
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Variant has stock" {
		t.Errorf("Title = %q, want %q", problem.Title, "Variant has stock")
	}
	want := "This variant still has 34 pcs in stock at 3 locations. Move the stock or adjust it to zero before you delete the variant."
	if problem.Detail != want {
		t.Errorf("Detail = %q, want %q", problem.Detail, want)
	}
	if n := h.Count(t, `SELECT count(*) FROM products.product_variants WHERE id = $1`, second.Id); n != 1 {
		t.Errorf("the held variant is gone (%d rows), want it kept", n)
	}
}

// TestDeleteVariant_OnOpenDocuments_IsRefusedNamingTheKind pins orders design
// O30 on Products' side: the holder is asked for the delete, and an
// open-documents hold is refused in its own words.
func TestDeleteVariant_OnOpenDocuments_IsRefusedNamingTheKind(t *testing.T) {
	t.Parallel()
	holder := &fakeHolder{hold: contracts.VariantHold{Kind: contracts.VariantHoldKindOpenDocuments, Documents: 3}}
	h := newHarness(t, modtest.WithVariantReferenceHolders(holder))
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created, _, second := productWithTwoVariants(t, c, taxCategoryID, "Ordered Widget")
	holder.held.Store(second.Id)

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, second.Id), nil)
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	want := "This variant is on 3 open quotes or orders. Finish or cancel them before you delete the variant."
	if problem.Title != "Variant is on open documents" || problem.Detail != want {
		t.Errorf("problem = %q: %q, want %q: %q", problem.Title, problem.Detail, "Variant is on open documents", want)
	}
	if got := holder.purpose.Load(); got != contracts.VariantHoldDelete {
		t.Errorf("purpose = %v, want %v", got, contracts.VariantHoldDelete)
	}
}

func TestDeleteVariant_HeldAtOneLocation_SaysLocationInTheSingular(t *testing.T) {
	t.Parallel()
	holder := &fakeHolder{hold: contracts.VariantHold{Quantity: "2.500", Locations: 1}}
	h := newHarness(t, modtest.WithVariantReferenceHolders(holder))
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created, _, second := productWithTwoVariants(t, c, taxCategoryID, "Half Held Widget")
	holder.held.Store(second.Id)

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, second.Id), nil)
	var problem problemJSON
	r.JSON(&problem)
	want := "This variant still has 2.5 pcs in stock at 1 location. Move the stock or adjust it to zero before you delete the variant."
	if r.Status != http.StatusConflict || problem.Detail != want {
		t.Errorf("status %d detail %q, want 409 and %q", r.Status, problem.Detail, want)
	}
}

func TestDeleteVariant_NotHeld_IsDeletedAfterTheHolderIsAsked(t *testing.T) {
	t.Parallel()
	holder := &fakeHolder{hold: contracts.VariantHold{Quantity: "1", Locations: 1}}
	h := newHarness(t, modtest.WithVariantReferenceHolders(holder))
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created, first, second := productWithTwoVariants(t, c, taxCategoryID, "Free Widget")
	holder.held.Store(first.Id)

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, second.Id), nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("status %d body %s, want 204", r.Status, r.Body)
	}
	if holder.asked.Load() == 0 {
		t.Error("the holder was never asked")
	}
	if n := h.Count(t, `SELECT count(*) FROM products.product_variants WHERE id = $1`, second.Id); n != 0 {
		t.Errorf("the variant is still there (%d rows), want it deleted", n)
	}
}

// TestDeleteVariant_LastVariant_IsRefusedBeforeAnyHolderIsAsked pins I23's
// order: the last-variant 409 comes first, unchanged, and a holder is never
// asked about a variant that could not be deleted anyway.
func TestDeleteVariant_LastVariant_IsRefusedBeforeAnyHolderIsAsked(t *testing.T) {
	t.Parallel()
	holder := &fakeHolder{hold: contracts.VariantHold{Quantity: "1", Locations: 1}}
	h := newHarness(t, modtest.WithVariantReferenceHolders(holder))
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Only Widget", sku(t, "only")))
	holder.held.Store(created.Variants[0].Id)

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, created.Variants[0].Id), nil)
	var problem problemJSON
	r.JSON(&problem)
	if r.Status != http.StatusConflict || problem.Title != "Last variant" {
		t.Errorf("status %d title %q, want 409 Last variant", r.Status, problem.Title)
	}
	if n := holder.asked.Load(); n != 0 {
		t.Errorf("the holder was asked %d times, want none", n)
	}
}

func TestDeleteVariant_OfAnotherProduct_IsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	_, _, second := productWithTwoVariants(t, c, taxCategoryID, "Scoped Widget")
	other := createProduct(t, c, newProductBody(taxCategoryID, "Other Widget", sku(t, "other")))

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/%d/variants/%d", other.Id, second.Id), nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// pricedHolder is a holder that reads its answer inside the delete's
// transaction: it holds a variant that has any price row. The race test
// below has its "posting" write one, so the answer proves the delete read
// what the posting committed, not what it saw before it waited.
type pricedHolder struct{}

func (pricedHolder) VariantHeld(ctx context.Context, tx pgx.Tx, variantID int32, _ contracts.VariantHoldPurpose) (*contracts.VariantHold, error) {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM products.product_prices WHERE variant_id = $1`, variantID).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	return &contracts.VariantHold{Quantity: fmt.Sprint(n), Locations: 1}, nil
}

// TestDeleteVariant_WaitsForAPostingHoldingTheVariant is I22 and I23
// together: a posting that holds FOR SHARE on the variant through the real
// catalog's LockVariants makes the delete wait at its FOR UPDATE, and the
// delete then asks the holder after the posting has committed, so it sees
// the posting's stock and refuses.
func TestDeleteVariant_WaitsForAPostingHoldingTheVariant(t *testing.T) {
	h := newHarness(t, modtest.WithVariantReferenceHolders(pricedHolder{}))
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created, _, second := productWithTwoVariants(t, c, taxCategoryID, "Raced Widget")

	ctx := context.Background()
	posting, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("posting: begin: %v", err)
	}
	t.Cleanup(func() { _ = posting.Rollback(ctx) })
	catalog := products.Module().Products(h.Deps())
	locked, err := catalog.LockVariants(ctx, posting, []int32{second.Id})
	if err != nil || len(locked) != 1 {
		t.Fatalf("LockVariants = %+v, %v, want the one variant", locked, err)
	}
	if _, err := posting.Exec(ctx, `INSERT INTO products.product_prices (variant_id, currency, amount) VALUES ($1, 'NOK', 1)`, second.Id); err != nil {
		t.Fatalf("posting: write: %v", err)
	}

	answered := make(chan *modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		answered <- c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, second.Id), nil)
		close(finished)
	}()
	awaitLockWaiters(t, h, 1, finished)
	select {
	case r := <-answered:
		t.Fatalf("the delete answered %d while the posting held the variant", r.Status)
	default:
	}

	if err := posting.Commit(ctx); err != nil {
		t.Fatalf("posting: commit: %v", err)
	}
	select {
	case r := <-answered:
		var problem problemJSON
		r.JSON(&problem)
		if r.Status != http.StatusConflict || problem.Title != "Variant has stock" {
			t.Errorf("status %d title %q body %s, want 409 Variant has stock", r.Status, problem.Title, r.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the delete never answered after the posting committed")
	}
}

func TestUpdateProduct_GoodsToServiceWhileHeld_IsRefusedWithProductHasStock(t *testing.T) {
	t.Parallel()
	holder := &fakeHolder{hold: contracts.VariantHold{Quantity: "34.000", Locations: 3}}
	h := newHarness(t, modtest.WithVariantReferenceHolders(holder))
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created, _, second := productWithTwoVariants(t, c, taxCategoryID, "Stocked Widget")
	holder.held.Store(second.Id)

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d", created.Id), map[string]any{
		"name": "Stocked Widget", "type": "Service", "taxCategoryId": taxCategoryID,
	})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Product has stock" {
		t.Errorf("Title = %q, want %q", problem.Title, "Product has stock")
	}
	want := fmt.Sprintf("Variant %s still has 34 pcs in stock at 3 locations. Move the stock or adjust it to zero before you make the product a service.", second.Sku)
	if problem.Detail != want {
		t.Errorf("Detail = %q, want %q", problem.Detail, want)
	}
	if n := h.Count(t, `SELECT count(*) FROM products.products WHERE id = $1 AND type = 'Goods'`, created.Id); n != 1 {
		t.Error("the product is no longer Goods, want it unchanged")
	}
}

func TestUpdateProduct_GoodsToServiceNotHeld_IsAllowed(t *testing.T) {
	t.Parallel()
	holder := &fakeHolder{hold: contracts.VariantHold{Quantity: "1", Locations: 1}}
	h := newHarness(t, modtest.WithVariantReferenceHolders(holder))
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created, _, _ := productWithTwoVariants(t, c, taxCategoryID, "Empty Widget")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d", created.Id), map[string]any{
		"name": "Empty Widget", "type": "Service", "taxCategoryId": taxCategoryID,
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated productJSON
	r.JSON(&updated)
	if updated.Type != "Service" {
		t.Errorf("Type = %q, want Service", updated.Type)
	}
	if n := holder.asked.Load(); n != 2 {
		t.Errorf("the holder was asked %d times, want once per variant (2)", n)
	}
}

// lateVariantHolder stands in for a receipt to a variant added while a change
// from Goods to Service runs: the first time it is asked, it adds a variant
// to the product on a connection of its own, which commits at once, and
// answers nothing held; from then on it holds the variant it added.
type lateVariantHolder struct {
	pool      *pgxpool.Pool
	productID atomic.Int32
	added     atomic.Int32
	asked     atomic.Int32
}

func (f *lateVariantHolder) VariantHeld(ctx context.Context, _ pgx.Tx, variantID int32, _ contracts.VariantHoldPurpose) (*contracts.VariantHold, error) {
	if f.asked.Add(1) == 1 {
		var id int32
		if err := f.pool.QueryRow(ctx, `INSERT INTO products.product_variants (product_id, sku, unit, created_at, updated_at)
			VALUES ($1, $2, 'pcs', now(), now()) RETURNING id`, f.productID.Load(), fmt.Sprintf("late-%d", f.productID.Load())).Scan(&id); err != nil {
			return nil, fmt.Errorf("lateVariantHolder: add a variant: %w", err)
		}
		f.added.Store(id)
		return nil, nil
	}
	if variantID != f.added.Load() {
		return nil, nil
	}
	return &contracts.VariantHold{Quantity: "5", Locations: 1}, nil
}

// TestUpdateProduct_GoodsToServiceWithAVariantStockedMeanwhile_IsRefused pins
// the second question: a variant added after the variants were locked, and
// stocked before the update got the product, is asked about once the update
// holds the product; its stock refuses the change and rolls the update back.
func TestUpdateProduct_GoodsToServiceWithAVariantStockedMeanwhile_IsRefused(t *testing.T) {
	t.Parallel()
	holder := &lateVariantHolder{}
	h := newHarness(t, modtest.WithVariantReferenceHolders(holder))
	holder.pool = h.Pool()
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Late Widget", sku(t, "late-first")))
	holder.productID.Store(created.Id)

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d", created.Id), map[string]any{
		"name": "Late Widget", "type": "Service", "taxCategoryId": taxCategoryID,
	})
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	want := fmt.Sprintf("Variant late-%d still has 5 pcs in stock at 1 location. Move the stock or adjust it to zero before you make the product a service.", created.Id)
	if problem.Title != "Product has stock" || problem.Detail != want {
		t.Errorf("Title %q Detail %q, want Product has stock and %q", problem.Title, problem.Detail, want)
	}
	if n := holder.asked.Load(); n != 2 {
		t.Errorf("the holder was asked %d times, want twice: the first variant, then the one added", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM products.products WHERE id = $1 AND type = 'Goods'`, created.Id); n != 1 {
		t.Error("the product is no longer Goods, want the update rolled back")
	}
}

// TestUpdateProduct_ServiceToGoodsOrUnchangedType_NeverAsksTheHolder pins
// that only Goods→Service is guarded: a holder holding every variant does
// not stop a service turning into goods, nor an edit that keeps the type.
func TestUpdateProduct_ServiceToGoodsOrUnchangedType_NeverAsksTheHolder(t *testing.T) {
	t.Parallel()
	holder := &fakeHolder{hold: contracts.VariantHold{Quantity: "1", Locations: 1}}
	h := newHarness(t, modtest.WithVariantReferenceHolders(holder))
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	body := newProductBody(taxCategoryID, "Consulting", sku(t, "service"))
	body["type"] = "Service"
	created := createProduct(t, c, body)
	holder.held.Store(created.Variants[0].Id)

	for _, typ := range []string{"Goods", "Goods"} {
		r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d", created.Id), map[string]any{
			"name": "Consulting", "type": typ, "taxCategoryId": taxCategoryID,
		})
		if r.Status != http.StatusOK {
			t.Fatalf("PUT type %s: status %d body %s, want 200", typ, r.Status, r.Body)
		}
	}
	if n := holder.asked.Load(); n != 0 {
		t.Errorf("the holder was asked %d times, want none", n)
	}
}

// TestVariantHold_InTransit_RefusesTheDeleteAndTheChangeToService pins the
// inventory phase 2 design's I47 on Products' side: stock on its way between
// sites holds the variant from a delete and its product from turning into a
// service, each refused in its own words.
func TestVariantHold_InTransit_RefusesTheDeleteAndTheChangeToService(t *testing.T) {
	t.Parallel()
	holder := &fakeHolder{hold: contracts.VariantHold{Kind: contracts.VariantHoldKindInTransit, Quantity: "12.000", Locations: 1}}
	h := newHarness(t, modtest.WithVariantReferenceHolders(holder))
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created, _, second := productWithTwoVariants(t, c, taxCategoryID, "Travelling Widget")
	holder.held.Store(second.Id)

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, second.Id), nil)
	var problem problemJSON
	r.JSON(&problem)
	want := "This variant still has 12 pcs in transit between sites in 1 transfer. Receive or cancel the transfer before you delete the variant."
	if r.Status != http.StatusConflict || problem.Title != "Variant has stock" || problem.Detail != want {
		t.Errorf("delete = %d %q: %q, want 409 Variant has stock: %q", r.Status, problem.Title, problem.Detail, want)
	}

	holder.hold.Locations = 2
	r = c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d", created.Id), map[string]any{
		"name": "Travelling Widget", "type": "Service", "taxCategoryId": taxCategoryID,
	})
	r.JSON(&problem)
	want = fmt.Sprintf("Variant %s still has 12 pcs in transit between sites in 2 transfers. Receive or cancel the transfers before you make the product a service.", second.Sku)
	if r.Status != http.StatusConflict || problem.Title != "Product has stock" || problem.Detail != want {
		t.Errorf("change to Service = %d %q: %q, want 409 Product has stock: %q", r.Status, problem.Title, problem.Detail, want)
	}
	if got := holder.purpose.Load(); got != contracts.VariantHoldTypeChange {
		t.Errorf("purpose = %v, want %v", got, contracts.VariantHoldTypeChange)
	}
}
