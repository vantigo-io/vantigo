package products_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/products"
)

// This file pins inventory design I24 from Products' side: a variant's
// packagings, the quantity derived from what a packaging is built from and
// kept by every change, the titled refusals (Packaging cycle, Packaging in
// use, Barcode taken), the barcode's uniqueness across variants and
// packagings under the products:barcodes advisory lock, and the packagings
// every catalog read carries.

// packagingJSON decodes ProductPackagingResponse.
type packagingJSON struct {
	Id                  int32    `json:"id"`
	Name                string   `json:"name"`
	Kind                string   `json:"kind"`
	ContainsPackagingId *int32   `json:"containsPackagingId"`
	ContainsCount       *int32   `json:"containsCount"`
	Quantity            float64  `json:"quantity"`
	Barcode             *string  `json:"barcode"`
	WeightKg            *float64 `json:"weightKg"`
	LengthCm            *float64 `json:"lengthCm"`
	WidthCm             *float64 `json:"widthCm"`
	HeightCm            *float64 `json:"heightCm"`
	SortOrder           int32    `json:"sortOrder"`
}

// packagingsPath is a variant's packagings collection, or one of them.
func packagingsPath(productID, variantID int32, packagingID ...int32) string {
	p := fmt.Sprintf("/api/v1/products/%d/variants/%d/packagings", productID, variantID)
	if len(packagingID) > 0 {
		p += fmt.Sprintf("/%d", packagingID[0])
	}
	return p
}

// packagingFixture is a product of one variant to hang packagings on.
func packagingFixture(t *testing.T) (*modtest.Harness, *modtest.Client, productJSON) {
	t.Helper()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	return h, c, createProduct(t, c, newProductBody(taxCategoryID, "Screws", sku(t, "pack")))
}

// addPackaging posts body and answers the packaging, failing on anything but
// 201.
func addPackaging(t *testing.T, c *modtest.Client, productID, variantID int32, body map[string]any) packagingJSON {
	t.Helper()
	r := c.Do(http.MethodPost, packagingsPath(productID, variantID), body)
	if r.Status != http.StatusCreated {
		t.Fatalf("add packaging %v: status %d body %s, want 201", body, r.Status, r.Body)
	}
	var p packagingJSON
	r.JSON(&p)
	return p
}

// listPackagings answers the variant's packagings by id.
func listPackagings(t *testing.T, c *modtest.Client, productID, variantID int32) map[int32]packagingJSON {
	t.Helper()
	r := c.Do(http.MethodGet, packagingsPath(productID, variantID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list packagings: status %d body %s", r.Status, r.Body)
	}
	var list []packagingJSON
	r.JSON(&list)
	out := map[int32]packagingJSON{}
	for _, p := range list {
		out[p.Id] = p
	}
	return out
}

// wantProblem asserts a 409 with the title.
func wantProblem(t *testing.T, r *modtest.Response, title string) problemJSON {
	t.Helper()
	var problem problemJSON
	r.JSON(&problem)
	if r.Status != http.StatusConflict || problem.Title != title {
		t.Fatalf("status %d title %q body %s, want 409 %q", r.Status, problem.Title, r.Body, title)
	}
	return problem
}

func TestPackagings_CreateListReplaceDelete(t *testing.T) {
	t.Parallel()
	_, c, product := packagingFixture(t)
	variant := product.Variants[0]
	barcode := newValidGTIN13(t)

	box := addPackaging(t, c, product.Id, variant.Id, map[string]any{
		"name": " Box ", "kind": "CASE", "quantity": 15, "barcode": barcode,
		"weightKg": 2.5, "lengthCm": 30, "widthCm": 20, "heightCm": 10,
	})
	if box.Name != "Box" || box.Kind != "case" || box.Quantity != 15 || box.Barcode == nil || *box.Barcode != barcode ||
		box.ContainsPackagingId != nil || box.WeightKg == nil || *box.WeightKg != 2.5 || box.SortOrder != 0 {
		t.Errorf("created %+v, want the trimmed name, the lower-cased kind and every field", box)
	}
	inner := addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Inner", "kind": "inner", "quantity": 0.125, "sortOrder": -1})

	r := c.Do(http.MethodGet, packagingsPath(product.Id, variant.Id), nil)
	var list []packagingJSON
	r.JSON(&list)
	if r.Status != http.StatusOK || len(list) != 2 || list[0].Id != inner.Id || list[1].Id != box.Id {
		t.Fatalf("list = %d %+v, want inner (sort -1) before box (sort 0)", r.Status, list)
	}

	// The product and the variant list embed them, in the same order.
	got := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/products/%d", product.Id), nil)
	var fetched productJSON
	got.JSON(&fetched)
	if len(fetched.Variants) != 1 || len(fetched.Variants[0].Packagings) != 2 || fetched.Variants[0].Packagings[0].Id != inner.Id {
		t.Errorf("product's variant packagings = %+v, want inner then box", fetched.Variants)
	}
	variants := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/products/%d/variants", product.Id), nil)
	var variantList []variantJSON
	variants.JSON(&variantList)
	if len(variantList) != 1 || len(variantList[0].Packagings) != 2 {
		t.Errorf("variant list = %+v, want the variant with both packagings", variantList)
	}

	put := c.Do(http.MethodPut, packagingsPath(product.Id, variant.Id, box.Id), map[string]any{
		"name": "Carton", "kind": "other", "quantity": 12.5,
	})
	var replaced packagingJSON
	put.JSON(&replaced)
	if put.Status != http.StatusOK || replaced.Name != "Carton" || replaced.Kind != "other" || replaced.Quantity != 12.5 ||
		replaced.Barcode != nil || replaced.WeightKg != nil || replaced.SortOrder != 0 {
		t.Errorf("replace = %d %+v, want a whole replace keeping the sort order", put.Status, replaced)
	}

	del := c.Do(http.MethodDelete, packagingsPath(product.Id, variant.Id, box.Id), nil)
	if del.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s, want 204", del.Status, del.Body)
	}
	if left := listPackagings(t, c, product.Id, variant.Id); len(left) != 1 {
		t.Errorf("after the delete %+v, want only inner", left)
	}
}

func TestPackagings_NotUnderTheVariantOrProduct_IsNotFound(t *testing.T) {
	t.Parallel()
	h, c, product := packagingFixture(t)
	variant := product.Variants[0]
	other := createProduct(t, c, newProductBody(insertTaxCategory(t, h, "Other rate", 0.15), "Nails", sku(t, "pack-other")))
	box := addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Box", "kind": "case", "quantity": 15})
	body := map[string]any{"name": "Box", "kind": "case", "quantity": 10}

	for _, r := range []*modtest.Response{
		c.Do(http.MethodGet, packagingsPath(other.Id, variant.Id), nil),
		c.Do(http.MethodPost, packagingsPath(other.Id, variant.Id), body),
		c.Do(http.MethodPut, packagingsPath(other.Id, variant.Id, box.Id), body),
		c.Do(http.MethodPut, packagingsPath(other.Id, other.Variants[0].Id, box.Id), body),
		c.Do(http.MethodDelete, packagingsPath(other.Id, other.Variants[0].Id, box.Id), nil),
		c.Do(http.MethodDelete, packagingsPath(product.Id, variant.Id, 999_999), nil),
	} {
		if r.Status != http.StatusNotFound {
			t.Errorf("status %d body %s, want 404", r.Status, r.Body)
		}
	}
}

func TestPackagings_Validation(t *testing.T) {
	t.Parallel()
	_, c, product := packagingFixture(t)
	variant := product.Variants[0]
	other := addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Box", "kind": "case", "quantity": 15})

	cases := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"blank name", map[string]any{"name": "  ", "kind": "case", "quantity": 1}, "name"},
		{"long name", map[string]any{"name": "Ø1234567890123456789012345678901234567890", "kind": "case", "quantity": 1}, "name"},
		{"unknown kind", map[string]any{"name": "Box", "kind": "crate", "quantity": 1}, "kind"},
		{"neither", map[string]any{"name": "Box", "kind": "case"}, "quantity"},
		{"both", map[string]any{"name": "Box", "kind": "case", "quantity": 1, "containsPackagingId": other.Id, "containsCount": 2}, "quantity"},
		{"zero", map[string]any{"name": "Box", "kind": "case", "quantity": 0}, "quantity"},
		{"four decimals", map[string]any{"name": "Box", "kind": "case", "quantity": 1.0005}, "quantity"},
		{"too large", map[string]any{"name": "Box", "kind": "case", "quantity": 100_000_000_000}, "quantity"},
		{"count without packaging", map[string]any{"name": "Box", "kind": "case", "containsCount": 2}, "containsPackagingId"},
		{"packaging without count", map[string]any{"name": "Box", "kind": "case", "containsPackagingId": other.Id}, "containsCount"},
		{"count of one", map[string]any{"name": "Box", "kind": "case", "containsPackagingId": other.Id, "containsCount": 1}, "containsCount"},
		{"bad check digit", map[string]any{"name": "Box", "kind": "case", "quantity": 1, "barcode": "4006381333932"}, "barcode"},
		{"negative weight", map[string]any{"name": "Box", "kind": "case", "quantity": 1, "weightKg": -1}, "weightKg"},
		{"negative height", map[string]any{"name": "Box", "kind": "case", "quantity": 1, "heightCm": -0.1}, "heightCm"},
		{"unknown contained", map[string]any{"name": "Box", "kind": "case", "containsPackagingId": 999_999, "containsCount": 2}, "containsPackagingId"},
	}
	for _, tc := range cases {
		// Some bodies break the schema's own bounds on purpose.
		r := c.Do(http.MethodPost, packagingsPath(product.Id, variant.Id), tc.body, modtest.SkipContract("a deliberately invalid packaging"))
		var problem validationProblemJSON
		r.JSON(&problem)
		if r.Status != http.StatusBadRequest || len(problem.Errors[tc.field]) == 0 {
			t.Errorf("%s: status %d body %s, want 400 on %s", tc.name, r.Status, r.Body, tc.field)
		}
	}
	if left := listPackagings(t, c, product.Id, variant.Id); len(left) != 1 {
		t.Errorf("after the refusals %d packagings, want only the first", len(left))
	}
}

func TestPackagings_ContainedPackagingOfAnotherVariant_IsRefused(t *testing.T) {
	t.Parallel()
	h, c, _ := packagingFixture(t)
	product, first, second := productWithTwoVariants(t, c, insertTaxCategory(t, h, "Two rate", 0.25), "Two Screws")
	box := addPackaging(t, c, product.Id, first.Id, map[string]any{"name": "Box", "kind": "case", "quantity": 15})

	r := c.Do(http.MethodPost, packagingsPath(product.Id, second.Id), map[string]any{
		"name": "Pallet", "kind": "pallet", "containsPackagingId": box.Id, "containsCount": 40,
	})
	var problem validationProblemJSON
	r.JSON(&problem)
	if r.Status != http.StatusBadRequest || len(problem.Errors["containsPackagingId"]) == 0 {
		t.Errorf("status %d body %s, want 400 on containsPackagingId", r.Status, r.Body)
	}
}

func TestPackagings_AtMostTenPerVariant(t *testing.T) {
	t.Parallel()
	_, c, product := packagingFixture(t)
	variant := product.Variants[0]
	for i := range 10 {
		addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": fmt.Sprintf("Box %d", i), "kind": "case", "quantity": i + 1})
	}
	r := c.Do(http.MethodPost, packagingsPath(product.Id, variant.Id), map[string]any{"name": "Box 11", "kind": "case", "quantity": 11})
	var problem validationProblemJSON
	r.JSON(&problem)
	if r.Status != http.StatusBadRequest || len(problem.Errors["packagings"]) == 0 {
		t.Errorf("eleventh: status %d body %s, want 400 on packagings", r.Status, r.Body)
	}
}

// TestPackagings_DerivedQuantityFollowsTheChain pins the derivation: a
// pallet of 40 boxes of 15 holds 600; the box changed to 12 makes it 480 in
// the same write, and a change further down the chain reaches the pallet
// through the box, exactly (2.4 × 5 × 40).
func TestPackagings_DerivedQuantityFollowsTheChain(t *testing.T) {
	t.Parallel()
	_, c, product := packagingFixture(t)
	variant := product.Variants[0]

	box := addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Box", "kind": "case", "quantity": 15})
	pallet := addPackaging(t, c, product.Id, variant.Id, map[string]any{
		"name": "Euro pallet", "kind": "pallet", "containsPackagingId": box.Id, "containsCount": 40,
	})
	if pallet.Quantity != 600 || pallet.ContainsPackagingId == nil || *pallet.ContainsPackagingId != box.Id || *pallet.ContainsCount != 40 {
		t.Fatalf("pallet = %+v, want 40 boxes holding 600", pallet)
	}

	put := c.Do(http.MethodPut, packagingsPath(product.Id, variant.Id, box.Id), map[string]any{"name": "Box", "kind": "case", "quantity": 12})
	if put.Status != http.StatusOK {
		t.Fatalf("change the box: status %d body %s", put.Status, put.Body)
	}
	if got := listPackagings(t, c, product.Id, variant.Id)[pallet.Id].Quantity; got != 480 {
		t.Errorf("pallet after the box became 12 = %v, want 480", got)
	}

	inner := addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Inner", "kind": "inner", "quantity": 3})
	put = c.Do(http.MethodPut, packagingsPath(product.Id, variant.Id, box.Id), map[string]any{
		"name": "Box", "kind": "case", "containsPackagingId": inner.Id, "containsCount": 5,
	})
	if put.Status != http.StatusOK {
		t.Fatalf("build the box from inners: status %d body %s", put.Status, put.Body)
	}
	all := listPackagings(t, c, product.Id, variant.Id)
	if all[box.Id].Quantity != 15 || all[pallet.Id].Quantity != 600 {
		t.Errorf("box %v pallet %v, want 15 and 600", all[box.Id].Quantity, all[pallet.Id].Quantity)
	}

	put = c.Do(http.MethodPut, packagingsPath(product.Id, variant.Id, inner.Id), map[string]any{"name": "Inner", "kind": "inner", "quantity": 2.4})
	if put.Status != http.StatusOK {
		t.Fatalf("change the inner: status %d body %s", put.Status, put.Body)
	}
	all = listPackagings(t, c, product.Id, variant.Id)
	if all[box.Id].Quantity != 12 || all[pallet.Id].Quantity != 480 {
		t.Errorf("box %v pallet %v, want 12 and 480 through the chain", all[box.Id].Quantity, all[pallet.Id].Quantity)
	}
}

func TestPackagings_DependantOverflow_RollsTheChangeBack(t *testing.T) {
	t.Parallel()
	_, c, product := packagingFixture(t)
	variant := product.Variants[0]
	box := addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Box", "kind": "case", "quantity": 1})
	addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Huge", "kind": "pallet", "containsPackagingId": box.Id, "containsCount": 1_000_000})

	r := c.Do(http.MethodPut, packagingsPath(product.Id, variant.Id, box.Id), map[string]any{"name": "Box", "kind": "case", "quantity": 99_999_999})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	if got := listPackagings(t, c, product.Id, variant.Id)[box.Id].Quantity; got != 1 {
		t.Errorf("box after the refusal = %v, want 1: the change must roll back", got)
	}
}

func TestPackagings_Cycle_IsRefused(t *testing.T) {
	t.Parallel()
	_, c, product := packagingFixture(t)
	variant := product.Variants[0]
	inner := addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Inner", "kind": "inner", "quantity": 3})
	box := addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Box", "kind": "case", "containsPackagingId": inner.Id, "containsCount": 5})
	pallet := addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Pallet", "kind": "pallet", "containsPackagingId": box.Id, "containsCount": 40})

	r := c.Do(http.MethodPut, packagingsPath(product.Id, variant.Id, inner.Id), map[string]any{
		"name": "Inner", "kind": "inner", "containsPackagingId": pallet.Id, "containsCount": 2,
	})
	problem := wantProblem(t, r, "Packaging cycle")
	if problem.Detail != "'Inner' cannot hold 'Pallet', which is built from it." {
		t.Errorf("detail %q", problem.Detail)
	}
	self := c.Do(http.MethodPut, packagingsPath(product.Id, variant.Id, box.Id), map[string]any{
		"name": "Box", "kind": "case", "containsPackagingId": box.Id, "containsCount": 2,
	})
	if p := wantProblem(t, self, "Packaging cycle"); p.Detail != "'Box' cannot hold itself." {
		t.Errorf("self detail %q", p.Detail)
	}
}

func TestPackagings_DeleteOneAnotherIsBuiltFrom_IsRefused(t *testing.T) {
	t.Parallel()
	_, c, product := packagingFixture(t)
	variant := product.Variants[0]
	box := addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Box", "kind": "case", "quantity": 15})
	addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Pallet", "kind": "pallet", "containsPackagingId": box.Id, "containsCount": 40})

	r := c.Do(http.MethodDelete, packagingsPath(product.Id, variant.Id, box.Id), nil)
	if p := wantProblem(t, r, "Packaging in use"); p.Detail != "'Pallet' is built from 'Box'. Change or remove 'Pallet' first." {
		t.Errorf("detail %q", p.Detail)
	}
	if len(listPackagings(t, c, product.Id, variant.Id)) != 2 {
		t.Error("the refused delete removed something")
	}
}

// TestPackagings_BarcodeTakenAcrossTables pins the barcode's uniqueness
// across both tables, both ways: a packaging cannot take a variant's GTIN or
// another packaging's ("Barcode taken"), and a variant — added, changed or
// created with its product — cannot take a packaging's ("Duplicate
// barcode", the title variant writes have always answered).
func TestPackagings_BarcodeTakenAcrossTables(t *testing.T) {
	t.Parallel()
	h, c, _ := packagingFixture(t)
	taxCategoryID := insertTaxCategory(t, h, "Barcode rate", 0.25)
	variantBarcode := newValidGTIN13(t)
	product := createProduct(t, c, map[string]any{
		"name": "Barcoded", "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{{"sku": sku(t, "bc"), "barcode": variantBarcode}},
	})
	variant := product.Variants[0]

	r := c.Do(http.MethodPost, packagingsPath(product.Id, variant.Id), map[string]any{"name": "Box", "kind": "case", "quantity": 15, "barcode": variantBarcode})
	if p := wantProblem(t, r, "Barcode taken"); p.Detail != fmt.Sprintf("A variant already carries the barcode '%s'.", variantBarcode) {
		t.Errorf("detail %q", p.Detail)
	}

	packagingBarcode := newValidGTIN13(t)
	box := addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Box", "kind": "case", "quantity": 15, "barcode": packagingBarcode})
	r = c.Do(http.MethodPost, packagingsPath(product.Id, variant.Id), map[string]any{"name": "Box 2", "kind": "case", "quantity": 15, "barcode": packagingBarcode})
	if p := wantProblem(t, r, "Barcode taken"); p.Detail != fmt.Sprintf("A packaging already carries the barcode '%s'.", packagingBarcode) {
		t.Errorf("detail %q", p.Detail)
	}
	// The packaging keeps its own barcode through a replace.
	keep := c.Do(http.MethodPut, packagingsPath(product.Id, variant.Id, box.Id), map[string]any{"name": "Box", "kind": "case", "quantity": 16, "barcode": packagingBarcode})
	if keep.Status != http.StatusOK {
		t.Errorf("replace keeping its barcode: status %d body %s", keep.Status, keep.Body)
	}

	add := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants", product.Id), map[string]any{"sku": sku(t, "bc-2"), "barcode": packagingBarcode})
	if p := wantProblem(t, add, "Duplicate barcode"); p.Detail != fmt.Sprintf("A packaging with barcode '%s' already exists.", packagingBarcode) {
		t.Errorf("add variant detail %q", p.Detail)
	}
	change := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d/variants/%d", product.Id, variant.Id), map[string]any{"sku": variant.Sku, "barcode": packagingBarcode})
	wantProblem(t, change, "Duplicate barcode")
	keepOwn := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/products/%d/variants/%d", product.Id, variant.Id), map[string]any{"sku": variant.Sku, "barcode": variantBarcode})
	if keepOwn.Status != http.StatusOK {
		t.Errorf("variant keeping its barcode: status %d body %s", keepOwn.Status, keepOwn.Body)
	}
	create := c.Do(http.MethodPost, "/api/v1/products", map[string]any{
		"name": "Clash", "type": "Goods", "taxCategoryId": taxCategoryID,
		"variants": []map[string]any{{"sku": sku(t, "bc-3"), "barcode": packagingBarcode}},
	})
	wantProblem(t, create, "Duplicate barcode")
}

// TestPackagings_ConcurrentWritesOfOneGTIN_OneIsRefused races a packaging
// and a variant taking one GTIN — no unique index spans the two tables, so
// only the products:barcodes advisory lock keeps both from winning. A gate
// holds the lock first, so both requests are proven to queue on it before
// either checks; released, exactly one answers 201 and the other its 409.
func TestPackagings_ConcurrentWritesOfOneGTIN_OneIsRefused(t *testing.T) {
	t.Parallel()
	h, c, product := packagingFixture(t)
	variant := product.Variants[0]
	barcode := newValidGTIN13(t)

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('products:barcodes'))`); err != nil {
		t.Fatalf("gate: lock: %v", err)
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(
			func() *modtest.Response {
				return c.Do(http.MethodPost, packagingsPath(product.Id, variant.Id), map[string]any{"name": "Box", "kind": "case", "quantity": 15, "barcode": barcode})
			},
			func() *modtest.Response {
				return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants", product.Id), map[string]any{"sku": sku(t, "race-gtin"), "barcode": barcode})
			},
		)
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	created, refused := 0, 0
	for i, r := range responses {
		var problem problemJSON
		r.JSON(&problem)
		switch {
		case r.Status == http.StatusCreated:
			created++
		case r.Status == http.StatusConflict && (i == 0 && problem.Title == "Barcode taken" || i == 1 && problem.Title == "Duplicate barcode"):
			refused++
		default:
			t.Errorf("response %d: status %d body %s", i, r.Status, r.Body)
		}
	}
	if created != 1 || refused != 1 {
		t.Errorf("created %d refused %d, want one of each", created, refused)
	}
}

func TestCatalog_PackagingsOnEveryRead_AndLookupByPackagingBarcode(t *testing.T) {
	t.Parallel()
	h, c, product := packagingFixture(t)
	variant := product.Variants[0]
	barcode := newValidGTIN13(t)
	box := addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Box", "kind": "case", "quantity": 15, "barcode": barcode})
	pallet := addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Pallet", "kind": "pallet", "containsPackagingId": box.Id, "containsCount": 40})
	catalog := products.Module().Products(h.Deps())
	ctx := context.Background()

	one, err := catalog.Variant(ctx, variant.Id)
	if err != nil || one == nil || len(one.Packagings) != 2 {
		t.Fatalf("Variant = %+v, %v, want both packagings", one, err)
	}
	if p := one.Packagings[1]; p.ID != pallet.Id || p.Quantity != "600.000" || p.Kind != "pallet" || p.SortOrder != 1 {
		t.Errorf("pallet entry = %+v, want 600.000 as three-place text", p)
	}
	if p := one.Packagings[0]; p.Barcode == nil || *p.Barcode != barcode || p.Quantity != "15.000" {
		t.Errorf("box entry = %+v", p)
	}
	many, err := catalog.Variants(ctx, []int32{variant.Id})
	if err != nil || len(many) != 1 || len(many[0].Packagings) != 2 {
		t.Errorf("Variants = %+v, %v", many, err)
	}
	ofProduct, err := catalog.VariantsOfProduct(ctx, product.Id)
	if err != nil || len(ofProduct) != 1 || len(ofProduct[0].Packagings) != 2 {
		t.Errorf("VariantsOfProduct = %+v, %v", ofProduct, err)
	}
	found, err := catalog.SearchVariants(ctx, variant.Sku, contracts.VariantSearch{Types: []string{"Goods"}, Limit: 10, IncludeInactive: true})
	if err != nil || len(found) != 1 || len(found[0].Packagings) != 2 {
		t.Errorf("SearchVariants = %+v, %v", found, err)
	}

	matches, err := catalog.LookupCode(ctx, " "+barcode+" ")
	if err != nil || len(matches) != 1 {
		t.Fatalf("LookupCode(packaging barcode) = %+v, %v, want one match", matches, err)
	}
	if m := matches[0]; m.Variant.ID != variant.Id || m.Packaging == nil || m.Packaging.ID != box.Id || len(m.Variant.Packagings) != 2 {
		t.Errorf("match = %+v, want the variant with the box as the packaging", m)
	}
	bySku, err := catalog.LookupCode(ctx, variant.Sku)
	if err != nil || len(bySku) != 1 || bySku[0].Packaging != nil || len(bySku[0].Variant.Packagings) != 2 {
		t.Errorf("LookupCode(sku) = %+v, %v, want the variant with its packagings and no packaging match", bySku, err)
	}
}

func TestCatalog_LockVariants_CarriesPackagings(t *testing.T) {
	t.Parallel()
	h, c, product := packagingFixture(t)
	variant := product.Variants[0]
	addPackaging(t, c, product.Id, variant.Id, map[string]any{"name": "Box", "kind": "case", "quantity": 0.5})

	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	locked, err := products.Module().Products(h.Deps()).LockVariants(ctx, tx, []int32{variant.Id})
	if err != nil || len(locked) != 1 || len(locked[0].Packagings) != 1 || locked[0].Packagings[0].Quantity != "0.500" {
		t.Errorf("LockVariants = %+v, %v, want the variant with its box of 0.500", locked, err)
	}
}

// TestPackagings_WriteWaitsForAPostingHoldingTheVariant pins the lock a
// packaging write takes first: the variant's row FOR UPDATE, so it waits
// for a posting holding the variant FOR SHARE (LockVariants), which thus
// converts at packagings that cannot change under it.
func TestPackagings_WriteWaitsForAPostingHoldingTheVariant(t *testing.T) {
	t.Parallel()
	h, c, product := packagingFixture(t)
	variant := product.Variants[0]

	ctx := context.Background()
	posting, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("posting: begin: %v", err)
	}
	t.Cleanup(func() { _ = posting.Rollback(ctx) })
	if _, err := products.Module().Products(h.Deps()).LockVariants(ctx, posting, []int32{variant.Id}); err != nil {
		t.Fatalf("LockVariants: %v", err)
	}

	answered := make(chan *modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		answered <- c.Do(http.MethodPost, packagingsPath(product.Id, variant.Id), map[string]any{"name": "Box", "kind": "case", "quantity": 15})
		close(finished)
	}()
	awaitLockWaiters(t, h, 1, finished)
	select {
	case r := <-answered:
		t.Fatalf("the packaging write answered %d while the posting held the variant", r.Status)
	default:
	}
	if err := posting.Commit(ctx); err != nil {
		t.Fatalf("posting: commit: %v", err)
	}
	select {
	case r := <-answered:
		if r.Status != http.StatusCreated {
			t.Errorf("status %d body %s, want 201 once the posting committed", r.Status, r.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the packaging write never answered after the posting committed")
	}
}
