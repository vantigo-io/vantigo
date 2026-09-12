package products_test

import (
	"fmt"
	"net/http"
	"testing"
)

// This file pins products inventory §2's money handling: numericFromFloat/
// floatFromNumeric (values.go) convert via the shortest round-tripping
// decimal text rather than rounding in Go, so Postgres's numeric(12,2)
// column scale is the only place rounding happens — exactly as .NET relied
// on. These exact values were confirmed empirically during the fix round
// that requested this test: 19.99, 0.07, 8.61 and 1234567.89 round-trip
// unchanged; 19.999 (three decimals) rounds to 20 at the numeric(12,2)
// column boundary.

// TestPrices_RoundTripExactDecimalAmounts pins that ordinary two-decimal
// (and whole-number-scale) amounts survive the float64<->pgtype.Numeric
// conversion and the numeric(12,2) column exactly.
func TestPrices_RoundTripExactDecimalAmounts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Money", sku(t, "money")))
	variant := created.Variants[0]
	basePath := fmt.Sprintf("/api/v1/products/%d/variants/%d/prices", created.Id, variant.Id)

	// Distinct currencies: same-currency, same-boundedness (open-ended)
	// prices would conflict with each other (pricing.go's Conflicts), which
	// is not what this test is about.
	cases := []struct {
		currency string
		amount   float64
	}{
		{"NOK", 19.99},
		{"SEK", 0.07},
		{"USD", 8.61},
		{"EUR", 1234567.89},
	}
	for _, tc := range cases {
		r := c.Do(http.MethodPost, basePath, map[string]any{"currency": tc.currency, "amount": tc.amount})
		if r.Status != http.StatusCreated {
			t.Fatalf("currency %s amount %v: status %d body %s, want 201", tc.currency, tc.amount, r.Status, r.Body)
		}
		var price priceJSON
		r.JSON(&price)
		if price.Amount != tc.amount {
			t.Errorf("currency %s: Amount = %v, want %v (exact round-trip)", tc.currency, price.Amount, tc.amount)
		}
	}
}

// TestPrices_AmountWithThreeDecimals_RoundsToColumnScale pins that a value
// with more precision than numeric(12,2) allows is rounded at the database,
// not truncated or rejected: 19.999 rounds to 20.
func TestPrices_AmountWithThreeDecimals_RoundsToColumnScale(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	created := createProduct(t, c, newProductBody(taxCategoryID, "Rounding", sku(t, "rounding")))
	variant := created.Variants[0]

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/products/%d/variants/%d/prices", created.Id, variant.Id),
		map[string]any{"currency": "NOK", "amount": 19.999})
	if r.Status != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", r.Status, r.Body)
	}
	var price priceJSON
	r.JSON(&price)
	if price.Amount != 20 {
		t.Errorf("Amount = %v, want 20 (numeric(12,2) rounds 19.999 at the column boundary)", price.Amount)
	}
}
