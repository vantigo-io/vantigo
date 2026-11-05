package orders_test

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

func gridPath(product int32, query string) string {
	return fmt.Sprintf("/api/v1/orders/products/%d/grid%s", product, query)
}

// seedProduct adds a product's variants with option values, each priced
// NOK 10 but the last.
func seedProduct(h *harness, product int32, values ...map[string]string) {
	for i, v := range values {
		id := product + int32(i) + 1
		h.catalog.seed(contracts.VariantEntry{
			ID: id, ProductID: product, ProductName: "Skjorte", SKU: fmt.Sprintf("SK-%d", id), Unit: "pcs",
			ProductType: "Goods", ProductStatus: "Active", TaxCategoryID: catStandard, OptionValues: v,
		})
		if i < len(values)-1 {
			h.catalog.setPrice(id, "NOK", "10")
		}
	}
}

// A product with variants on two options is answered as a grid along them,
// each with its values in the order the variants name them; more options, an
// option with more than 30 values, or a variant without a value is a list
// (O34).
func TestVariantGrid_GridAndList(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, reader...)
	seedProduct(h, 900,
		map[string]string{"Size": "S", "Colour": "Red"}, map[string]string{"Size": "M", "Colour": "Red"},
		map[string]string{"Size": "S", "Colour": "Blue"}, map[string]string{"Size": "M", "Colour": "Blue"})
	res := c.Do(http.MethodGet, gridPath(900, "?currency=nok"), nil)
	mustStatus(t, res, http.StatusOK, "grid")
	var g gen.OrdersVariantGrid
	res.JSON(&g)
	if g.Mode != gen.Grid || g.ProductName != "Skjorte" || g.Currency != "NOK" || len(g.Options) != 2 ||
		g.Options[0].Name != "Colour" || strings.Join(g.Options[0].Values, ",") != "Red,Blue" ||
		g.Options[1].Name != "Size" || strings.Join(g.Options[1].Values, ",") != "S,M" || len(g.Cells) != 4 || len(g.Variants) != 0 {
		t.Fatalf("grid %+v", g)
	}
	if g.Cells[0].VariantId != 901 || g.Cells[0].Values["Size"] != "S" || g.Cells[0].Price == nil || *g.Cells[0].Price != 10 ||
		g.Cells[3].Price != nil {
		t.Errorf("cells %+v", g.Cells)
	}

	seedProduct(h, 910, map[string]string{"Size": "S", "Colour": "Red", "Fit": "Slim"})
	res = c.Do(http.MethodGet, gridPath(910, ""), nil)
	mustStatus(t, res, http.StatusOK, "three options")
	res.JSON(&g)
	if g.Mode != gen.List || len(g.Variants) != 1 || len(g.Cells) != 0 || len(g.Options) != 0 {
		t.Errorf("three options %+v; want a list", g)
	}

	var many []map[string]string
	for i := range 31 {
		many = append(many, map[string]string{"Length": strconv.Itoa(i)})
	}
	seedProduct(h, 1000, many...)
	res = c.Do(http.MethodGet, gridPath(1000, ""), nil)
	mustStatus(t, res, http.StatusOK, "31 values")
	res.JSON(&g)
	if g.Mode != gen.List || len(g.Variants) != 31 {
		t.Errorf("31 values: mode %s, %d variants; want a list", g.Mode, len(g.Variants))
	}

	seedProduct(h, 1100, map[string]string{"Size": "S"}, map[string]string{})
	res = c.Do(http.MethodGet, gridPath(1100, ""), nil)
	mustStatus(t, res, http.StatusOK, "a variant without a value")
	res.JSON(&g)
	if g.Mode != gen.List {
		t.Errorf("a variant without a value: %s; want a list", g.Mode)
	}

	mustStatus(t, c.Do(http.MethodGet, gridPath(9999, ""), nil), http.StatusNotFound, "no such product")
	expectRefusal(t, c.Do(http.MethodGet, gridPath(900, "?currency=NO"), nil), http.StatusBadRequest, "", "a bad currency")
	mustStatus(t, h.Client(t).Do(http.MethodGet, gridPath(900, ""), nil), http.StatusUnauthorized, "signed out")
}

// A line entered in a packaging stores the packaging's count and name and the
// quantity in the variant's unit — 3 cases of 15 is 45 — and the PDF prints
// it as "3 × Case à 15 (45 pcs)"; a packaging that is not the variant's is
// packaging_not_found (O15).
func TestPackagingEntry_ConvertsAndPrints(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	l := productLine(varBolt, 1)
	l.Quantity = nil
	l.PackagingId, l.PackagingQuantity = i32(packCase), f64(3)
	q := createQuote(t, c, custBusiness, l)
	got := q.Lines[0]
	if got.Quantity == nil || *got.Quantity != 45 || got.PackagingQuantity == nil || *got.PackagingQuantity != 3 ||
		got.PackagingName == nil || *got.PackagingName != "Case" || got.LineNet != 4500 {
		t.Fatalf("line %+v; want 45 pcs in 3 cases", got)
	}
	mustStatus(t, c.Do(http.MethodGet, quotePath(q.Id, "pdf"), nil), http.StatusOK, "the PDF")
	m, ok := pdfModels.find("Tilbud", "Tilbud "+strconv.FormatInt(q.Number, 10))
	if !ok || len(m.Rows) != 1 || !strings.Contains(m.Rows[0][0], "3 × Case à 15 (45 pcs)") || m.Rows[0][1] != "45" {
		t.Errorf("PDF rows %q; want the packaging printed", m.Rows)
	}

	bad := productLine(varFood, 1)
	bad.Quantity = nil
	bad.PackagingId, bad.PackagingQuantity = i32(packCase), f64(2)
	lines := []gen.OrdersLineInput{bad}
	expectRefusal(t, c.Do(http.MethodPost, quotesPath, gen.OrdersQuoteInput{ClientRequestId: uuid.New(), CustomerId: custBusiness, Lines: &lines}),
		http.StatusBadRequest, "packaging_not_found", "another variant's packaging")
}

// A packaging count whose quantity would need more than three decimals is
// quantity_invalid, so the stored quantity is always count × size exactly
// (O15).
func TestPackagingEntry_RefusesAnInexactQuantity(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	bolt, err := h.catalog.Variant(context.Background(), varBolt)
	if err != nil || bolt == nil {
		t.Fatalf("bolt: %v", err)
	}
	bolt.Packagings = append(bolt.Packagings, contracts.PackagingEntry{ID: 8, Name: "Roll", Kind: "roll", Quantity: "1.555"})
	h.catalog.seed(*bolt)

	l := productLine(varBolt, 1)
	l.Quantity = nil
	l.PackagingId, l.PackagingQuantity = i32(8), f64(1.5)
	lines := []gen.OrdersLineInput{l}
	expectRefusal(t, c.Do(http.MethodPost, quotesPath, gen.OrdersQuoteInput{ClientRequestId: uuid.New(), CustomerId: custBusiness, Lines: &lines}),
		http.StatusBadRequest, "quantity_invalid", "1.5 × 1.555")
	l.PackagingQuantity = f64(2)
	q := createQuote(t, c, custBusiness, l)
	if got := q.Lines[0]; got.Quantity == nil || *got.Quantity != 3.11 {
		t.Errorf("line %+v; want 3.11", got)
	}
}

// A saved line keeps a packaging the catalog has since removed — its size and
// name as the line has them — while a new line cannot name it (O15).
func TestPackagingEntry_KeepsARemovedPackaging(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	c := h.SignIn(t, quoter...)
	l := productLine(varBolt, 1)
	l.Quantity = nil
	l.PackagingId, l.PackagingQuantity = i32(packCase), f64(3)
	q := createQuote(t, c, custBusiness, l)

	bolt, err := h.catalog.Variant(context.Background(), varBolt)
	if err != nil || bolt == nil {
		t.Fatalf("bolt: %v", err)
	}
	bolt.Packagings = nil
	h.catalog.seed(*bolt)

	id := q.Lines[0].Id
	kept := gen.OrdersLineInput{Id: &id, Kind: gen.OrdersLineInputKindProduct, VariantId: i32(varBolt), PackagingId: i32(packCase), PackagingQuantity: f64(2)}
	res := c.Do(http.MethodPut, quotePath(q.Id), gen.OrdersQuoteUpdate{ExpectedUpdatedAt: q.UpdatedAt, Revision: q.Revision, CustomerId: custBusiness,
		Lines: []gen.OrdersLineInput{kept}})
	mustStatus(t, res, http.StatusOK, "save the line in its removed packaging")
	saved := quoteOf(t, res)
	if got := saved.Lines[0]; got.Quantity == nil || *got.Quantity != 30 || got.PackagingName == nil || *got.PackagingName != "Case" ||
		got.PackagingQuantity == nil || *got.PackagingQuantity != 2 {
		t.Errorf("line %+v; want 30 pcs as 2 cases", got)
	}

	fresh := productLine(varBolt, 1)
	fresh.Quantity = nil
	fresh.PackagingId, fresh.PackagingQuantity = i32(packCase), f64(1)
	expectRefusal(t, c.Do(http.MethodPut, quotePath(q.Id), gen.OrdersQuoteUpdate{ExpectedUpdatedAt: saved.UpdatedAt, Revision: saved.Revision, CustomerId: custBusiness,
		Lines: []gen.OrdersLineInput{kept, fresh}}), http.StatusBadRequest, "packaging_not_found", "a new line in a removed packaging")
}
