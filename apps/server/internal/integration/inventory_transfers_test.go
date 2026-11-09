package integration_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// Products and Inventory together on a transfer (inventory phase 2 design
// I47), against the real modules: a variant whose stock is on its way
// between two sites — nothing on any shelf, nothing reserved — is held by
// Inventory's holder, so Products refuses to delete it and to turn its
// product into a service; once the transfer is cancelled and the stock
// adjusted away, both are allowed.

func TestInventoryTransfer_HoldsTheVariantInProducts(t *testing.T) {
	t.Parallel()
	h := newInstallation(t, modProducts, modInventory)
	c, _ := h.SignInUser(t,
		"products:products-view", "products:products-manage", "products:variants-view", "products:variants-manage",
		"products:tax-categories-view", "products:tax-categories-manage", "products:pricing-view", "products:pricing-manage",
		"products:categories-view",
		"inventory:locations-view", "inventory:locations-manage", "inventory:stock-view", "inventory:stock-adjust",
		"inventory:receipts-manage", "inventory:transfers-manage",
	)
	var cat struct {
		Id int32 `json:"id"`
	}
	okJSON(t, c, http.MethodPost, "/api/v1/products/tax-categories", map[string]any{"name": "Standard", "kind": "Standard", "rate": 0.25}, &cat)
	var product struct {
		Id       int32 `json:"id"`
		Variants []struct {
			Id  int32  `json:"id"`
			Sku string `json:"sku"`
		} `json:"variants"`
	}
	okJSON(t, c, http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Pallet jack", "type": "Goods", "status": "Active", "taxCategoryId": cat.Id,
		"variants": []map[string]any{{"sku": "JACK-1"}, {"sku": "JACK-2"}},
	}, &product)
	variant := product.Variants[1]

	type site struct {
		Id             int64 `json:"id"`
		RootLocationId int64 `json:"rootLocationId"`
	}
	var osl, bgo site
	okJSON(t, c, http.MethodPost, "/api/v1/inventory/sites", map[string]any{"code": "OSL", "name": "Oslo", "kind": "warehouse"}, &osl)
	okJSON(t, c, http.MethodPost, "/api/v1/inventory/sites", map[string]any{"code": "BGO", "name": "Bergen", "kind": "warehouse"}, &bgo)
	okJSON(t, c, http.MethodPost, "/api/v1/inventory/movements/receipts", map[string]any{
		"clientRequestId": uuid.New(), "siteId": osl.Id, "toLocationId": osl.RootLocationId,
		"lines": []map[string]any{{"variantId": variant.Id, "quantity": 3}},
	}, nil)
	var transfer struct {
		Id       int64 `json:"id"`
		Revision int32 `json:"revision"`
		Lines    []struct {
			Id int64 `json:"id"`
		} `json:"lines"`
	}
	okJSON(t, c, http.MethodPost, "/api/v1/inventory/transfers", map[string]any{
		"fromSiteId": osl.Id, "toSiteId": bgo.Id, "lines": []map[string]any{{"variantId": variant.Id, "quantity": 3}},
	}, &transfer)
	transferPath := fmt.Sprintf("/api/v1/inventory/transfers/%d", transfer.Id)
	res := c.Do(http.MethodPost, transferPath+"/send", map[string]any{
		"lines": []map[string]any{{"lineId": transfer.Lines[0].Id, "fromLocationId": osl.RootLocationId}},
	}, modtest.Header("If-Match", fmt.Sprint(transfer.Revision)))
	if res.Status != http.StatusOK {
		t.Fatalf("send = %d %s", res.Status, res.Body)
	}
	res.JSON(&transfer)

	type problem struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	variantPath := fmt.Sprintf("/api/v1/products/%d/variants/%d", product.Id, variant.Id)
	res = c.Do(http.MethodDelete, variantPath, nil)
	var p problem
	res.JSON(&p)
	want := "This variant still has 3 pcs in transit between sites in 1 transfer. Receive or cancel the transfer before you delete the variant."
	if res.Status != http.StatusConflict || p.Detail != want {
		t.Errorf("delete = %d %q, want 409 %q", res.Status, p.Detail, want)
	}
	toService := map[string]any{"name": "Pallet jack", "type": "Service", "taxCategoryId": cat.Id}
	res = c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d", product.Id), toService)
	res.JSON(&p)
	want = "Variant JACK-2 still has 3 pcs in transit between sites in 1 transfer. Receive or cancel the transfer before you make the product a service."
	if res.Status != http.StatusConflict || p.Detail != want {
		t.Errorf("change to Service = %d %q, want 409 %q", res.Status, p.Detail, want)
	}

	// Cancelled, the stock is back on the shelf; adjusted away, nothing holds.
	res = c.Do(http.MethodPost, transferPath+"/cancel", map[string]any{"clientRequestId": uuid.New()}, modtest.Header("If-Match", fmt.Sprint(transfer.Revision)))
	if res.Status != http.StatusOK {
		t.Fatalf("cancel = %d %s", res.Status, res.Body)
	}
	okJSON(t, c, http.MethodPost, "/api/v1/inventory/movements/adjustments", map[string]any{
		"clientRequestId": uuid.New(), "siteId": osl.Id, "reason": "correction",
		"lines": []map[string]any{{"variantId": variant.Id, "locationId": osl.RootLocationId, "mode": "change", "quantity": -3}},
	}, nil)
	if res := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d", product.Id), toService); res.Status != http.StatusOK {
		t.Errorf("change to Service after the cancel = %d %s, want 200", res.Status, res.Body)
	}
	if res := c.Do(http.MethodDelete, variantPath, nil); res.Status != http.StatusNoContent {
		t.Errorf("delete after the cancel = %d %s, want 204", res.Status, res.Body)
	}
}
