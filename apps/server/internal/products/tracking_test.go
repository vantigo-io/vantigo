package products_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/products"
)

// The tracking mode (manufacturing phase 2 M49–M51): the fields on the
// variant, goods only, every mode open since layer M, a change held by the
// holders' third purpose, a change to Service resetting it, and the catalog
// answering it.

// trackedVariantJSON is the variant's tracking fields as the API answers them.
type trackedVariantJSON struct {
	Id             int32  `json:"id"`
	Tracking       string `json:"tracking"`
	ExpiryDays     *int32 `json:"expiryDays"`
	BestBeforeDays *int32 `json:"bestBeforeDays"`
}

// TestVariantTracking_FieldsAndModes is the offsets stored and answered,
// lot and serial accepted on every write that names a mode now that
// productions and deliveries capture lots (layer M removed L1a's interim
// guard), and tracking_requires_goods for a service's variant.
func TestVariantTracking_FieldsAndModes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Milk", sku(t, "milk")))
	v := created.Variants[0]
	path := fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, v.Id)

	r := c.Do(http.MethodPut, path, map[string]any{"sku": v.Sku, "unit": "l", "expiryDays": 30, "bestBeforeDays": 20})
	if r.Status != http.StatusOK {
		t.Fatalf("PUT offsets: %d %s", r.Status, r.Body)
	}
	var got trackedVariantJSON
	if err := json.Unmarshal(r.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Tracking != "none" || got.ExpiryDays == nil || *got.ExpiryDays != 30 || got.BestBeforeDays == nil || *got.BestBeforeDays != 20 {
		t.Errorf("variant = %+v", got)
	}
	r = c.Do(http.MethodPut, path, map[string]any{"sku": v.Sku, "unit": "l", "expiryDays": 0})
	if r.Status != http.StatusBadRequest {
		t.Errorf("an offset of 0 = %d, want 400", r.Status)
	}

	// Every mode, on every write that names one: no stock holds the
	// variant, so the change is not held.
	for _, mode := range []string{"lot", "serial", "none"} {
		r = c.Do(http.MethodPut, path, map[string]any{"sku": v.Sku, "unit": "l", "tracking": mode})
		_ = json.Unmarshal(r.Body, &got)
		if r.Status != http.StatusOK || got.Tracking != mode {
			t.Errorf("PUT tracking %s = %d %s", mode, r.Status, r.Body)
		}
	}
	r = c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants", created.Id), map[string]any{"sku": sku(t, "milk-2"), "unit": "l", "tracking": "lot"})
	_ = json.Unmarshal(r.Body, &got)
	if r.Status != http.StatusCreated || got.Tracking != "lot" {
		t.Errorf("POST a lot variant = %d %s, want 201 lot", r.Status, r.Body)
	}
	body := newProductBody(taxCategoryID, "Cheese", sku(t, "cheese"))
	body["variants"] = []map[string]any{{"sku": sku(t, "cheese"), "tracking": "serial"}}
	r = c.Do(http.MethodPost, "/api/v1/products", body)
	if r.Status != http.StatusCreated {
		t.Errorf("POST a product with a serial variant = %d %s, want 201", r.Status, r.Body)
	}
	r = c.Do(http.MethodPut, path, map[string]any{"sku": v.Sku, "unit": "l", "tracking": "batch"})
	if r.Status != http.StatusBadRequest {
		t.Errorf("an unknown mode = %d, want 400", r.Status)
	}

	// A service's variant is never tracked: 400.
	service := newProductBody(taxCategoryID, "Install", sku(t, "install"))
	service["type"] = "Service"
	s := createProduct(t, c, service)
	r = c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d/variants/%d", s.Id, s.Variants[0].Id),
		map[string]any{"sku": s.Variants[0].Sku, "unit": "h", "tracking": "lot"})
	if r.Status != http.StatusBadRequest || !jsonHas(r.Body, "tracking") {
		t.Errorf("a service's variant tracked = %d %s, want 400 on tracking", r.Status, r.Body)
	}
}

// TestVariantTracking_TypeJudgedUnderTheLock is the product type judged
// again under the variant's FOR UPDATE (L1a review L12): a change to Service
// holds the variant's lock when the update reads Goods before its
// transaction, commits, and the update — waiting on the lock — then reads
// Service and refuses tracking_requires_goods; the variant stays untracked.
func TestVariantTracking_TypeJudgedUnderTheLock(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Yoghurt", sku(t, "yoghurt")))
	v := created.Variants[0]

	ctx := context.Background()
	change, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = change.Rollback(ctx) })
	if _, err := change.Exec(ctx, `SELECT id FROM products.product_variants WHERE product_id = $1 ORDER BY id FOR UPDATE`, created.Id); err != nil {
		t.Fatal(err)
	}
	answered := make(chan *modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		answered <- c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, v.Id),
			map[string]any{"sku": v.Sku, "unit": "pcs", "tracking": "lot"})
		close(finished)
	}()
	awaitLockWaiters(t, h, 1, finished)
	if _, err := change.Exec(ctx, `UPDATE products.products SET type = 'Service' WHERE id = $1`, created.Id); err != nil {
		t.Fatal(err)
	}
	if err := change.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-answered:
		if r.Status != http.StatusBadRequest || !jsonHas(r.Body, "tracking") {
			t.Errorf("the update after the change to Service = %d %s, want 400 on tracking", r.Status, r.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the update never answered")
	}
	if got := modtest.One[string](t, h, `SELECT tracking FROM products.product_variants WHERE id = $1`, v.Id); got != "none" {
		t.Errorf("tracking = %s, want none", got)
	}
}

// TestVariantTracking_NewVariantTypeUnderTheLock is the same for a new
// variant (the review of M, MEDIUM-3): a change to Service holds the product
// when the add reads Goods before its transaction, commits, and the add —
// waiting on the product's FOR SHARE — then reads Service and refuses
// tracking_requires_goods; no variant is added.
func TestVariantTracking_NewVariantTypeUnderTheLock(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Kefir", sku(t, "kefir")))

	ctx := context.Background()
	change, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = change.Rollback(ctx) })
	if _, err := change.Exec(ctx, `UPDATE products.products SET type = 'Service' WHERE id = $1`, created.Id); err != nil {
		t.Fatal(err)
	}
	added := sku(t, "kefir-lot")
	answered := make(chan *modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		answered <- c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants", created.Id),
			map[string]any{"sku": added, "unit": "pcs", "tracking": "lot"})
		close(finished)
	}()
	awaitLockWaiters(t, h, 1, finished)
	if err := change.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-answered:
		if r.Status != http.StatusBadRequest || !jsonHas(r.Body, "tracking") {
			t.Errorf("the add after the change to Service = %d %s, want 400 on tracking", r.Status, r.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the add never answered")
	}
	if n := h.Count(t, `SELECT count(*) FROM products.product_variants WHERE sku = $1`, added); n != 0 {
		t.Errorf("%d variants added to a Service", n)
	}
}

// jsonHas reports whether a validation problem names field.
func jsonHas(body []byte, field string) bool {
	var p struct {
		Errors map[string][]string `json:"errors"`
	}
	return json.Unmarshal(body, &p) == nil && len(p.Errors[field]) > 0
}

// TestVariantTracking_ChangeIsHeld is M51: a tracking change asks the
// holders with the third purpose and is refused while one holds the
// variant; a write that leaves the mode as it is asks no one. The variant is
// made tracked through the store.
func TestVariantTracking_ChangeIsHeld(t *testing.T) {
	t.Parallel()
	holder := &fakeHolder{hold: contracts.VariantHold{Kind: contracts.VariantHoldKindReserved, Quantity: "5.000", Locations: 1}}
	h := newHarness(t, modtest.WithVariantReferenceHolders(holder))
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Flour", sku(t, "flour")))
	v := created.Variants[0]
	path := fmt.Sprintf("/api/v1/products/%d/variants/%d", created.Id, v.Id)
	h.Exec(t, `UPDATE products.product_variants SET tracking = 'lot' WHERE id = $1`, v.Id)
	holder.held.Store(v.Id)

	// The same mode, absent: nothing changes, no holder is asked.
	r := c.Do(http.MethodPut, path, map[string]any{"sku": v.Sku, "unit": "kg"})
	if r.Status != http.StatusOK || holder.asked.Load() != 0 {
		t.Fatalf("an update keeping the mode = %d %s, asked %d", r.Status, r.Body, holder.asked.Load())
	}
	var got trackedVariantJSON
	_ = json.Unmarshal(r.Body, &got)
	if got.Tracking != "lot" {
		t.Errorf("tracking = %s, want lot kept", got.Tracking)
	}
	// Back to none while reserved: tracking_held.
	r = c.Do(http.MethodPut, path, map[string]any{"sku": v.Sku, "unit": "kg", "tracking": "none"})
	var p problemJSON
	r.JSON(&p)
	if r.Status != http.StatusConflict || p.Title != "Tracking held" ||
		p.Detail != "This variant still has 5 kg reserved for orders at 1 site. Deliver or cancel the orders that reserve it before you change its tracking." {
		t.Errorf("a held change = %d %+v", r.Status, p)
	}
	if purpose, _ := holder.purpose.Load().(contracts.VariantHoldPurpose); purpose != contracts.VariantHoldTrackingChange {
		t.Errorf("purpose = %q, want tracking_change", purpose)
	}
	// Free: the change goes through.
	holder.held.Store(0)
	r = c.Do(http.MethodPut, path, map[string]any{"sku": v.Sku, "unit": "kg", "tracking": "none"})
	_ = json.Unmarshal(r.Body, &got)
	if r.Status != http.StatusOK || got.Tracking != "none" {
		t.Errorf("an unheld change = %d %s", r.Status, r.Body)
	}
}

// TestVariantTracking_ServiceResetsAndCatalog is a change to Service setting
// every variant's tracking to none in its transaction (M49), and the catalog
// answering the fields (M50).
func TestVariantTracking_ServiceResetsAndCatalog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Gadget", sku(t, "gadget")))
	v := created.Variants[0]
	h.Exec(t, `UPDATE products.product_variants SET tracking = 'serial', expiry_days = 7 WHERE id = $1`, v.Id)

	catalog := products.Module().Products(h.Deps())
	e, err := catalog.Variant(t.Context(), v.Id)
	if err != nil || e.Tracking != contracts.TrackingSerial || e.ExpiryDays == nil || *e.ExpiryDays != 7 || e.BestBeforeDays != nil {
		t.Errorf("catalog = %+v, %v", e, err)
	}
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d", created.Id), map[string]any{
		"name": "Gadget", "type": "Service", "taxCategoryId": taxCategoryID,
	})
	if r.Status != http.StatusOK {
		t.Fatalf("to Service: %d %s", r.Status, r.Body)
	}
	if got := modtest.One[string](t, h, `SELECT tracking FROM products.product_variants WHERE id = $1`, v.Id); got != "none" {
		t.Errorf("tracking after the change to Service = %s", got)
	}
}
