package purchasing_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

func itemOf(t *testing.T, res *modtest.Response) gen.PurchasingSupplierItem {
	t.Helper()
	var it gen.PurchasingSupplierItem
	res.JSON(&it)
	return it
}

func itemPath(id int64) string { return fmt.Sprintf("%s/%d", itemsPath, id) }

// One item per supplier and variant; the first of a variant is preferred,
// preferred true on another moves it, false leaves it, and a delete of the
// preferred one hands it to the oldest other.
func TestSupplierItems_PreferredRule(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, buyer...)
	create := func(supplier int64, preferred bool) gen.PurchasingSupplierItem {
		t.Helper()
		res := c.Do(http.MethodPost, itemsPath, gen.PurchasingSupplierItemInput{SupplierId: supplier, VariantId: varBolt, Price: str("4.1234"), Preferred: &preferred})
		mustStatus(t, res, http.StatusCreated, "create")
		return itemOf(t, res)
	}
	first := create(supNorsk, false)
	if !first.Preferred || first.Currency != "NOK" || deref(first.Price) != "4.1234" || first.PriceSetBy == nil || *first.PriceSetBy != gen.Person {
		t.Fatalf("first = %+v", first)
	}
	expectRefusal(t, c.Do(http.MethodPost, itemsPath, gen.PurchasingSupplierItemInput{SupplierId: supNorsk, VariantId: varBolt}),
		http.StatusConflict, "supplier_item_exists", "a second item")
	second := create(supGerman, true)
	if !second.Preferred || second.Currency != "EUR" {
		t.Fatalf("second = %+v", second)
	}
	res := c.Do(http.MethodGet, itemPath(first.Id), nil)
	mustStatus(t, res, http.StatusOK, "get")
	first = itemOf(t, res)
	if first.Preferred {
		t.Error("the preference did not move")
	}
	res = c.Do(http.MethodPut, itemPath(second.Id), gen.PurchasingSupplierItemUpdate{Revision: second.Revision, Preferred: false, Price: str("5"), PackagingId: i32(packBox)})
	mustStatus(t, res, http.StatusOK, "update")
	second = itemOf(t, res)
	if !second.Preferred || deref(second.Price) != "5.00" || deref(second.PackagingName) != "Eske" || deref(second.PackagingQuantity) != "10" {
		t.Errorf("updated = %+v", second)
	}
	expectRefusal(t, c.Do(http.MethodPut, itemPath(second.Id), gen.PurchasingSupplierItemUpdate{Revision: 1, Preferred: true}), http.StatusConflict, "version_conflict", "stale")
	expectRefusal(t, c.Do(http.MethodPut, itemPath(second.Id), gen.PurchasingSupplierItemUpdate{Revision: second.Revision, Preferred: true, PackagingId: i32(99)}),
		http.StatusBadRequest, "packaging_not_found", "an unknown packaging")
	expectRefusal(t, c.Do(http.MethodPost, itemsPath, gen.PurchasingSupplierItemInput{SupplierId: supNorsk, VariantId: 999}), http.StatusBadRequest, "variant_not_found", "unknown variant")
	expectRefusal(t, c.Do(http.MethodPost, itemsPath, gen.PurchasingSupplierItemInput{SupplierId: 999, VariantId: varNut}), http.StatusBadRequest, "supplier_not_found", "unknown supplier")
	expectRefusal(t, c.Do(http.MethodPost, itemsPath, gen.PurchasingSupplierItemInput{SupplierId: supNorsk, VariantId: varNut, Price: str("1.23456")}), http.StatusBadRequest, "price_invalid", "five places")

	// The register's currency moves: the item is flagged.
	h.dir.update(supGerman, func(e *contracts.SupplierEntry) { e.Currency = "SEK" })
	var page gen.PurchasingSupplierItemPage
	res = c.Do(http.MethodGet, itemsPath+"?variantId=101", nil)
	mustStatus(t, res, http.StatusOK, "list")
	res.JSON(&page)
	if page.Total != 2 || page.Items[0].SupplierName != "Norsk Stål AS" || !page.Items[1].CurrencyDiffers {
		t.Errorf("list = %+v", page.Items)
	}
	res = c.Do(http.MethodGet, itemsPath+"?q=bolt&supplierId=501", nil)
	res.JSON(&page)
	if page.Total != 1 {
		t.Errorf("search = %d", page.Total)
	}

	mustStatus(t, c.Do(http.MethodDelete, itemPath(second.Id), nil), http.StatusNoContent, "delete")
	res = c.Do(http.MethodGet, itemPath(first.Id), nil)
	if first = itemOf(t, res); !first.Preferred {
		t.Error("the oldest other item was not made preferred")
	}
	expectRefusal(t, c.Do(http.MethodDelete, itemPath(second.Id), nil), http.StatusNotFound, "not_found", "delete again")
}

// The import's dry run answers each row and writes nothing; a commit with a
// failing row writes nothing; one with every row ok writes them all.
func TestImport_DryRunAndCommit(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, buyer...)
	mustStatus(t, c.Do(http.MethodPost, itemsPath, gen.PurchasingSupplierItemInput{SupplierId: supNorsk, VariantId: varNut, SupplierItemNumber: str("OLD")}),
		http.StatusCreated, "an item to update")
	path := func(dry bool) string {
		return fmt.Sprintf("%s/import?supplierId=%d&dryRun=%v", itemsPath, supNorsk, dry)
	}
	bad := []byte("sku,barcode,supplier_item_number,price,lead_time_days\n" +
		"BOLT-M8,,NS-1,12.50,5\n" +
		",7090000000077,NS-2,100,\n" +
		"NUT-M8,,NS-3,abc,\n" +
		"NOPE,,,,\n")
	res := c.Do(http.MethodPost, path(true), nil, modtest.RawBody("text/csv", bad))
	mustStatus(t, res, http.StatusOK, "dry run")
	var r gen.PurchasingImportResult
	res.JSON(&r)
	want := []gen.PurchasingImportOutcome{gen.Created, gen.DuplicateRow, gen.PriceInvalid, gen.VariantNotFound}
	if r.Ok || r.Committed || len(r.Rows) != 4 {
		t.Fatalf("dry run = %+v", r)
	}
	for i, w := range want {
		if r.Rows[i].Outcome != w || r.Rows[i].Row != int32(i+2) {
			t.Errorf("row %d = %s at %d, want %s", i, r.Rows[i].Outcome, r.Rows[i].Row, w)
		}
	}
	res = c.Do(http.MethodPost, path(false), nil, modtest.RawBody("text/csv", bad))
	res.JSON(&r)
	if r.Committed || h.Count(t, `SELECT count(*) FROM purchasing.supplier_items`) != 1 {
		t.Fatal("a commit with failing rows wrote")
	}
	good := []byte("sku,supplier_item_number,price,minimum_quantity\nBOLT-M8,NS-BOX,99.9,2\nNUT-M8,,1.5,\n")
	res = c.Do(http.MethodPost, path(false), nil, modtest.RawBody("text/csv", good))
	mustStatus(t, res, http.StatusOK, "commit")
	res.JSON(&r)
	if !r.Ok || !r.Committed || r.Created != 1 || r.Updated != 1 {
		t.Fatalf("commit = %+v", r)
	}
	var page gen.PurchasingSupplierItemPage
	res = c.Do(http.MethodGet, itemsPath+"?supplierId=501", nil)
	res.JSON(&page)
	for _, it := range page.Items {
		switch it.VariantId {
		case varNut:
			if deref(it.SupplierItemNumber) != "OLD" || deref(it.Price) != "1.50" || it.PriceSetBy == nil || *it.PriceSetBy != gen.Import {
				t.Errorf("updated = %+v (an empty cell keeps the value)", it)
			}
		case varBolt:
			if deref(it.SupplierItemNumber) != "NS-BOX" || deref(it.MinimumQuantity) != "2" || !it.Preferred {
				t.Errorf("created = %+v", it)
			}
		}
	}
	// A packaging's barcode sets the packaging.
	res = c.Do(http.MethodPost, fmt.Sprintf("%s/import?supplierId=%d&dryRun=false", itemsPath, supGerman), nil,
		modtest.RawBody("text/csv", []byte("barcode;price\n7090000000077;7\n")))
	mustStatus(t, res, http.StatusOK, "barcode import")
	res = c.Do(http.MethodGet, itemsPath+"?supplierId=502", nil)
	res.JSON(&page)
	if len(page.Items) != 1 || page.Items[0].PackagingId == nil || *page.Items[0].PackagingId != packBox {
		t.Errorf("barcode import = %+v", page.Items)
	}
	expectRefusal(t, c.Do(http.MethodPost, path(true), nil, modtest.RawBody("text/csv", []byte("name;price\nx;1\n"))), http.StatusBadRequest, "import_invalid", "a header without sku")
	expectRefusal(t, c.Do(http.MethodPost, fmt.Sprintf("%s/import?supplierId=999", itemsPath), nil, modtest.RawBody("text/csv", good)), http.StatusBadRequest, "supplier_not_found", "unknown supplier")
}
