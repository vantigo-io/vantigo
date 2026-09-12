package products_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// productJSON decodes ProductResponse (Endpoints/Products/Dtos/ProductResponse.cs).
type productJSON struct {
	Id              int32              `json:"id"`
	Name            string             `json:"name"`
	Sku             *string            `json:"sku"`
	Type            string             `json:"type"`
	Status          string             `json:"status"`
	Unit            *string            `json:"unit"`
	StandardCost    *float64           `json:"standardCost"`
	TaxCategory     taxCategoryRefJSON `json:"taxCategory"`
	Description     *string            `json:"description"`
	Category        *categoryRefJSON   `json:"category"`
	Barcode         *string            `json:"barcode"`
	WeightKg        *float64           `json:"weightKg"`
	LengthCm        *float64           `json:"lengthCm"`
	WidthCm         *float64           `json:"widthCm"`
	HeightCm        *float64           `json:"heightCm"`
	EffectivePrices []priceJSON        `json:"effectivePrices"`
	Variants        []variantJSON      `json:"variants"`
	CreatedAt       time.Time          `json:"createdAt"`
	UpdatedAt       time.Time          `json:"updatedAt"`
}

type taxCategoryRefJSON struct {
	Id   int32   `json:"id"`
	Name string  `json:"name"`
	Kind string  `json:"kind"`
	Rate float64 `json:"rate"`
}

type categoryRefJSON struct {
	Id   int32  `json:"id"`
	Name string `json:"name"`
}

// variantJSON decodes ProductVariantResponse.
type variantJSON struct {
	Id              int32             `json:"id"`
	Sku             string            `json:"sku"`
	Barcode         *string           `json:"barcode"`
	Unit            string            `json:"unit"`
	StandardCost    *float64          `json:"standardCost"`
	WeightKg        *float64          `json:"weightKg"`
	LengthCm        *float64          `json:"lengthCm"`
	WidthCm         *float64          `json:"widthCm"`
	HeightCm        *float64          `json:"heightCm"`
	OptionValues    map[string]string `json:"optionValues"`
	EffectivePrices []priceJSON       `json:"effectivePrices"`
	CreatedAt       time.Time         `json:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
}

// priceJSON decodes ProductPriceResponse.
type priceJSON struct {
	Id        int32      `json:"id"`
	Currency  string     `json:"currency"`
	Amount    float64    `json:"amount"`
	ValidFrom *time.Time `json:"validFrom"`
	ValidTo   *time.Time `json:"validTo"`
}

type validationProblemJSON struct {
	Errors map[string][]string `json:"errors"`
}

type problemJSON struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

type productListJSON struct {
	Data       []productJSON `json:"data"`
	Pagination struct {
		Page            int  `json:"page"`
		PageSize        int  `json:"pageSize"`
		TotalCount      int  `json:"totalCount"`
		TotalPages      int  `json:"totalPages"`
		HasNextPage     bool `json:"hasNextPage"`
		HasPreviousPage bool `json:"hasPreviousPage"`
	} `json:"pagination"`
}

// newProductBody is CreateProductEndpointTests' NewProduct helper: a
// single-variant product body with the given name and sku, status
// defaulting to "Draft" when not overridden.
func newProductBody(taxCategoryID int32, name, sku string, status ...string) map[string]any {
	body := map[string]any{
		"name":          name,
		"type":          "Goods",
		"taxCategoryId": taxCategoryID,
		"variants":      []map[string]any{{"sku": sku}},
	}
	if len(status) > 0 {
		body["status"] = status[0]
	}
	return body
}

// createProduct posts body and requires 201, returning the decoded product.
func createProduct(t *testing.T, c *modtest.Client, body map[string]any) productJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/products", body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create product: status %d body %s", r.Status, r.Body)
	}
	var created productJSON
	r.JSON(&created)
	return created
}
