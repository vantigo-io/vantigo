package products_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// This file pins PutProductsById's "did anything actually change" bump
// (products.go): matching internal/customers/customers.go:432-436's
// identical convention, updated_at moves only when a field genuinely
// changed — never unconditionally on every successful PUT, and never left
// untouched when something did change. Both directions are pinned so an
// "always bump" regression and a "never bump" regression are equally
// caught.

// TestPutProductsById_NoChange_LeavesUpdatedAtUnchanged pins the "never
// touch it for a no-op PUT" direction.
func TestPutProductsById_NoChange_LeavesUpdatedAtUnchanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Stable", sku(t, "stable")))

	h.Advance(time.Hour) // the clock moves; nothing about the product does

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d", created.Id), map[string]any{
		"name": created.Name, "type": created.Type, "taxCategoryId": taxCategoryID,
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated productJSON
	r.JSON(&updated)
	if !updated.UpdatedAt.Equal(created.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want unchanged %v (a no-op PUT must not move it)", updated.UpdatedAt, created.UpdatedAt)
	}
}

// TestPutProductsById_WithChange_UpdatesTimestamp pins the "do bump it when
// something changed" direction.
func TestPutProductsById_WithChange_UpdatesTimestamp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Changeable", sku(t, "changeable")))

	h.Advance(time.Hour)

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d", created.Id), map[string]any{
		"name": "Renamed", "type": created.Type, "taxCategoryId": taxCategoryID,
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated productJSON
	r.JSON(&updated)
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want after %v (the name actually changed)", updated.UpdatedAt, created.UpdatedAt)
	}
}

// TestPutProductsById_StatusChangeOnly_UpdatesTimestamp pins that a status-
// only change (a field UpdateProduct's own comparison must not overlook)
// also moves updated_at.
func TestPutProductsById_StatusChangeOnly_UpdatesTimestamp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Status Change", sku(t, "status-change")))

	h.Advance(time.Hour)

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d", created.Id), map[string]any{
		"name": created.Name, "type": created.Type, "taxCategoryId": taxCategoryID, "status": "Active",
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated productJSON
	r.JSON(&updated)
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want after %v (status changed from Draft to Active)", updated.UpdatedAt, created.UpdatedAt)
	}
}
