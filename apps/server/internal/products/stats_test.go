package products_test

import (
	"net/http"
	"testing"
	"time"
)

// This file is not a port: products inventory §6 lists no .NET test class
// for ProductStatsEndpoints.cs at all (a repo-wide search of
// Products.Module.Tests turns up none — stats.go's own doc comment notes
// this, the same gap customers/stats.go documents for its own module).
// These tests are this task's own, written directly against
// ProductStatsEndpoints.cs's behavior; module.Compose's coverage gate
// requires every implemented operation be exercised by a contract-validated
// exchange regardless.

// TestGetProductsStatsAttention_IsAlwaysEmpty pins the stub
// (ProductStatsEndpoints.cs:103-104, products inventory §1.2/§7 oddity 4):
// always `[]`, regardless of what data exists.
func TestGetProductsStatsAttention_IsAlwaysEmpty(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	createProduct(t, c, newProductBody(taxCategoryID, "Attention Probe", sku(t, "attention")))

	r := c.Do(http.MethodGet, "/api/v1/products/stats/attention", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var items []map[string]any
	r.JSON(&items)
	if len(items) != 0 {
		t.Errorf("items = %v, want an empty array", items)
	}
}

type productStatsSummaryJSON struct {
	From                     time.Time        `json:"from"`
	To                       time.Time        `json:"to"`
	TotalActiveProducts      int              `json:"totalActiveProducts"`
	TotalActiveProductsDelta int              `json:"totalActiveProductsDelta"`
	NewProducts              int              `json:"newProducts"`
	NewProductsDelta         int              `json:"newProductsDelta"`
	StatusCounts             map[string]int64 `json:"statusCounts"`
	StatusCountDeltas        map[string]int64 `json:"statusCountDeltas"`
}

// TestGetProductsStatsSummary_CountsNewProductsWithinTheDefaultPeriod pins
// the default 30-day window (ProductStatsEndpoints.cs:13,124-125): a
// product created now counts toward newProducts and, being Draft by
// default, toward statusCounts["draft"] but not totalActiveProducts.
func TestGetProductsStatsSummary_CountsNewProductsWithinTheDefaultPeriod(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	createProduct(t, c, newProductBody(taxCategoryID, "Summary Probe", sku(t, "summary")))
	h.Advance(time.Second) // the period's default "to" is now; created_at must fall strictly before it.

	r := c.Do(http.MethodGet, "/api/v1/products/stats/summary", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var summary productStatsSummaryJSON
	r.JSON(&summary)

	// This test's own isolated database (internal/modtest) holds exactly the
	// one product created above, so the counts are exact, not lower bounds.
	if summary.NewProducts != 1 {
		t.Errorf("NewProducts = %d, want 1", summary.NewProducts)
	}
	if summary.TotalActiveProducts != 0 {
		t.Errorf("TotalActiveProducts = %d, want 0 (the product defaults to Draft, not Active)", summary.TotalActiveProducts)
	}
	if !summary.From.Before(summary.To) {
		t.Errorf("From = %v, To = %v, want From before To", summary.From, summary.To)
	}

	// Every ProductStatus value is present, 0 when no product has it — a
	// mutation that only emitted keys for statuses actually present would
	// still pass every other assertion here but drop "active"/"discontinued".
	for _, status := range []string{"draft", "active", "discontinued"} {
		if _, ok := summary.StatusCounts[status]; !ok {
			t.Errorf("StatusCounts is missing key %q, want present (0 or otherwise)", status)
		}
		if _, ok := summary.StatusCountDeltas[status]; !ok {
			t.Errorf("StatusCountDeltas is missing key %q, want present (0 or otherwise)", status)
		}
	}
	if summary.StatusCounts["draft"] != 1 {
		t.Errorf(`StatusCounts["draft"] = %d, want 1`, summary.StatusCounts["draft"])
	}
	if summary.StatusCountDeltas["draft"] != 1 {
		t.Errorf(`StatusCountDeltas["draft"] = %d, want 1 (1 now minus 0 at period start)`, summary.StatusCountDeltas["draft"])
	}
}

// TestGetProductsStatsSummary_InvalidPeriodIsRejected pins
// TryNormalizePeriod's 400 (ProductStatsEndpoints.cs:126-138): from after to
// answers "Invalid period".
func TestGetProductsStatsSummary_InvalidPeriodIsRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/products/stats/summary?from=2026-09-12T00:00:00Z&to=2026-09-01T00:00:00Z", nil)
	if r.Status != http.StatusBadRequest {
		t.Errorf("status %d body %s, want 400", r.Status, r.Body)
	}
}

// TestGetProductsStatsTimeseries_BucketsNewProductsByDay pins the
// newProducts metric grouping by UTC calendar day
// (ProductStatsEndpoints.cs:92-97).
func TestGetProductsStatsTimeseries_BucketsNewProductsByDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	taxCategoryID := insertTaxCategory(t, h, "Standard rate", 0.25)
	createProduct(t, c, newProductBody(taxCategoryID, "Timeseries Probe", sku(t, "timeseries")))
	h.Advance(time.Second) // the period's default "to" is now; created_at must fall strictly before it.

	r := c.Do(http.MethodGet, "/api/v1/products/stats/timeseries?metric=newProducts", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var buckets []struct {
		Date  string `json:"date"`
		Value int64  `json:"value"`
	}
	r.JSON(&buckets)
	// This test's own isolated database holds exactly the one product
	// created above, all in the same UTC calendar day (the harness clock
	// only advanced by a second): exactly one bucket, exactly one count.
	if len(buckets) != 1 || buckets[0].Value != 1 {
		t.Errorf("buckets = %+v, want exactly one bucket with value 1", buckets)
	}
}

// TestGetProductsStatsTimeseries_MetricIsCaseInsensitive pins
// string.Equals(metric, "newProducts", OrdinalIgnoreCase)
// (ProductStatsEndpoints.cs:84).
func TestGetProductsStatsTimeseries_MetricIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/products/stats/timeseries?metric=NEWPRODUCTS", nil)
	if r.Status != http.StatusOK {
		t.Errorf("status %d body %s, want 200", r.Status, r.Body)
	}
}

// TestGetProductsStatsTimeseries_InvalidMetricIsRejected pins the metric
// check that runs after the period is normalized (ProductStatsEndpoints.cs:78-90),
// with its exact detail text.
func TestGetProductsStatsTimeseries_InvalidMetricIsRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/products/stats/timeseries?metric=bogus", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Invalid metric" || problem.Detail != "Metric must be: newProducts." {
		t.Errorf("problem = %+v, want title %q detail %q", problem, "Invalid metric", "Metric must be: newProducts.")
	}
}

// TestGetProductsStatsTimeseries_InvalidPeriodRunsBeforeInvalidMetric pins
// the order ProductStatsEndpoints.Timeseries checks its two 400 conditions
// (:83-90): a request that is both an invalid period and an invalid metric
// must answer "Invalid period", never "Invalid metric".
func TestGetProductsStatsTimeseries_InvalidPeriodRunsBeforeInvalidMetric(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/products/stats/timeseries?metric=bogus&from=2026-09-12T00:00:00Z&to=2026-09-01T00:00:00Z", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Invalid period" {
		t.Errorf("Title = %q, want %q (the period check must run first)", problem.Title, "Invalid period")
	}
}
