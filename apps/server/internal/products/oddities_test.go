package products_test

import (
	"fmt"
	"net/http"
	"testing"
)

// This file pins the three .NET oddities this task's dispatch names
// explicitly, each with its own test: PUT /products/{id} silently ignoring
// request.Variants (products inventory §7 oddity 2), the wrong Location
// header on price creation (oddity 3), and option-value keys camelCased on
// the way out (oddity 5, also touched inline by
// TestUpdateVariant_SkuChangeOnDraft_IsAllowed in variants_test.go).

// TestPutProductsById_IgnoresVariantsInRequestBody pins products inventory
// §7 oddity 2: UpdateProductEndpoint.cs:19 calls
// Validate(requireVariants:false, validateVariants:false) and the handler
// never reads request.Variants at all. A client PUTting a product with
// completely different variants gets a 200 with the variants completely
// unchanged — no error, no warning. This is a real .NET behaviour being
// pinned, not a gap this port should "fix".
func TestPutProductsById_IgnoresVariantsInRequestBody(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Variants Ignored", sku(t, "ignore-variants")))
	original := created.Variants[0]

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d", created.Id), map[string]any{
		"name": created.Name, "type": created.Type, "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{
			{"sku": sku(t, "should-be-ignored-1")},
			{"sku": sku(t, "should-be-ignored-2")},
		},
	})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var updated productJSON
	r.JSON(&updated)

	if len(updated.Variants) != 1 {
		t.Fatalf("len(Variants) = %d, want 1 — the PUT body's variants must be silently ignored, not applied", len(updated.Variants))
	}
	if updated.Variants[0].Id != original.Id || updated.Variants[0].Sku != original.Sku {
		t.Errorf("Variants[0] = %+v, want the original variant %+v, unchanged", updated.Variants[0], original)
	}

	// Not just the response projection: a fresh GET, and so the database
	// itself, must show no second variant row either.
	fresh := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/products/%d", created.Id), nil)
	var freshProduct productJSON
	fresh.JSON(&freshProduct)
	if len(freshProduct.Variants) != 1 {
		t.Errorf("GET after PUT: len(Variants) = %d, want 1", len(freshProduct.Variants))
	}
}

// TestPostProductsByIdVariantsByVariantIdPrices_LocationPointsAtCollectionNotThePrice
// pins products inventory §7 oddity 3, deliberately: .NET's
// AddProductPriceEndpoint.cs:52-54 returns
// TypedResults.Created($"/api/v1/products/{id}/variants/{variantId}/prices", …)
// — the URL of the *collection*, not .../prices/{price.Id}. Every other
// Created response in this module points at the actual new resource; this
// one does not, on purpose, because .NET does not either. This test pins
// the wrong value; making it point at the price would be an *unfaithful*
// port, not a fix.
func TestPostProductsByIdVariantsByVariantIdPrices_LocationPointsAtCollectionNotThePrice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Wrong Location", sku(t, "wrong-location")))
	variant := created.Variants[0]

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants/%d/prices", created.Id, variant.Id),
		map[string]any{"currency": "NOK", "amount": 100})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var price priceJSON
	r.JSON(&price)

	wantWrongLocation := fmt.Sprintf("/api/v1/products/%d/variants/%d/prices", created.Id, variant.Id)
	got := r.Header("Location")
	if got != wantWrongLocation {
		t.Errorf("Location = %q, want %q (the collection, not the price — this is the .NET bug being pinned)", got, wantWrongLocation)
	}
	if correctLocation := fmt.Sprintf("%s/%d", wantWrongLocation, price.Id); got == correctLocation {
		t.Errorf("Location = %q unexpectedly points at the new price itself; the deliberately-ported .NET defect must still be present", got)
	}
}

// TestCreateProduct_OptionValueKeysComeBackCamelCased pins products
// inventory §7 oddity 5: ASP.NET Core's JsonSerializerDefaults.Web applies
// PropertyNamingPolicy = CamelCase to Dictionary<string,string> *keys*, not
// just POCO property names — a multi-word key like "ShoeSize" comes back as
// "shoeSize", even though internally the client-supplied casing is what's
// compared (case-insensitively) and nothing here rewrites the stored key.
func TestCreateProduct_OptionValueKeysComeBackCamelCased(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)

	r := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Camel Cased", "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{
			{"sku": sku(t, "camel"), "optionValues": map[string]any{"ShoeSize": "42"}},
		},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var created productJSON
	r.JSON(&created)

	if got := created.Variants[0].OptionValues["shoeSize"]; got != "42" {
		t.Errorf(`OptionValues["shoeSize"] = %q, want "42" (client sent "ShoeSize"; the response must camelCase the key)`, got)
	}
	if _, stillPascalCase := created.Variants[0].OptionValues["ShoeSize"]; stillPascalCase {
		t.Error(`OptionValues still carries the original PascalCase key "ShoeSize" — it must be camelCased on the way out`)
	}
}
