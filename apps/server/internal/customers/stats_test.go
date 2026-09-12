package customers_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// This file ports Integration/CustomerStatsAndStatusTests.cs (customers
// inventory §7). UpdatingStatus_PersistsAndBumpsUpdatedAt_AndRecordsTimelineEvent
// also asserted the generated event by fetching GET .../timeline, a Task 9
// operation; here the customers.customers_timeline_entries row is read
// directly instead, since Task 6 writes generated events (customer create/
// update/archive own that write path, timeline_events.go) but does not
// expose them over HTTP yet.

type statsJSON struct {
	TotalCount           int  `json:"totalCount"`
	ActiveCount          int  `json:"activeCount"`
	NewLast30DaysCount   int  `json:"newLast30DaysCount"`
	BusinessCount        *int `json:"businessCount"`
	PersonCount          *int `json:"personCount"`
	MissingIdentityCount *int `json:"missingIdentityCount"`
	DistinctCountryCount *int `json:"distinctCountryCount"`
}

// Ported from Integration/CustomerStatsAndStatusTests.cs.
// Stats_ReturnsGlobalCounts_WithIdentityFiguresForPermittedCaller.
func TestStats_ReturnsGlobalCounts_WithIdentityFiguresForPermittedCaller(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name":     "Stats Business",
		"identity": map[string]any{"country": "no", "type": "business", "id": "913456789", "name": "Stats AS", "source": "manual"},
	})
	c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name":     "Stats Person",
		"identity": map[string]any{"country": "se", "type": "person", "id": "19770101-1234", "name": "Stats Person", "source": "manual"},
	})
	c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Stats Unknown"})

	r := c.Do(http.MethodGet, "/api/v1/customers/stats", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var stats statsJSON
	r.JSON(&stats)

	// The shared database may contain customers from other tests, so the
	// counts are asserted as lower bounds and for internal consistency
	// rather than exact values, as the .NET test does.
	if stats.TotalCount < 3 || stats.ActiveCount < 3 || stats.NewLast30DaysCount < 3 {
		t.Errorf("stats = %+v, want TotalCount/ActiveCount/NewLast30DaysCount >= 3", stats)
	}
	if stats.BusinessCount == nil || stats.PersonCount == nil || stats.MissingIdentityCount == nil || stats.DistinctCountryCount == nil {
		t.Fatalf("stats = %+v, want every identity figure non-nil for a legal-identity-view holder", stats)
	}
	if *stats.BusinessCount < 1 || *stats.PersonCount < 1 || *stats.MissingIdentityCount < 1 || *stats.DistinctCountryCount < 2 {
		t.Errorf("stats = %+v, want BusinessCount/PersonCount/MissingIdentityCount >= 1 and DistinctCountryCount >= 2", stats)
	}
	if stats.TotalCount != *stats.BusinessCount+*stats.PersonCount+*stats.MissingIdentityCount {
		t.Errorf("TotalCount = %d, want BusinessCount+PersonCount+MissingIdentityCount = %d",
			stats.TotalCount, *stats.BusinessCount+*stats.PersonCount+*stats.MissingIdentityCount)
	}
}

// Ported from Integration/CustomerStatsAndStatusTests.cs.
// Stats_OmitsIdentityFigures_WithoutLegalIdentityViewPermission.
func TestStats_OmitsIdentityFigures_WithoutLegalIdentityViewPermission(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	createCustomer(t, owner, "Stats Viewer Customer")

	viewer := h.SignIn(t, "customers:view")
	r := viewer.Do(http.MethodGet, "/api/v1/customers/stats", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var stats statsJSON
	r.JSON(&stats)

	if stats.TotalCount < 1 {
		t.Errorf("TotalCount = %d, want >= 1", stats.TotalCount)
	}
	if stats.BusinessCount != nil || stats.PersonCount != nil || stats.MissingIdentityCount != nil || stats.DistinctCountryCount != nil {
		t.Errorf("stats = %+v, want every identity figure nil without legal-identity-view", stats)
	}
}

// Ported from Integration/CustomerStatsAndStatusTests.cs.
// Customer_DefaultsToActive_AndCarriesTimestamps.
func TestCustomer_DefaultsToActive_AndCarriesTimestamps(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Status Default")

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil)
	var customer customerJSON
	r.JSON(&customer)
	if customer.Status != "active" {
		t.Errorf("Status = %q, want active", customer.Status)
	}
	if customer.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero, want the harness clock's instant")
	}
	if !customer.CreatedAt.Equal(customer.UpdatedAt) {
		t.Errorf("CreatedAt = %v, UpdatedAt = %v, want them equal on creation", customer.CreatedAt, customer.UpdatedAt)
	}
}

// Ported from Integration/CustomerStatsAndStatusTests.cs.
// UpdatingStatus_PersistsAndBumpsUpdatedAt_AndRecordsTimelineEvent.
func TestUpdatingStatus_PersistsAndBumpsUpdatedAt_AndRecordsTimelineEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Status Flip")

	before := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil)
	var beforeCustomer customerJSON
	before.JSON(&beforeCustomer)

	h.Advance(time.Second) // Deps.Clock only moves when told to; UpdatedAt must strictly advance.
	update := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{
		"name": "Status Flip", "status": "disabled",
	})
	if update.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", update.Status, update.Body)
	}

	after := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil)
	var afterCustomer customerJSON
	after.JSON(&afterCustomer)
	if afterCustomer.Status != "disabled" {
		t.Errorf("Status = %q, want disabled", afterCustomer.Status)
	}
	if !afterCustomer.UpdatedAt.After(beforeCustomer.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want after %v", afterCustomer.UpdatedAt, beforeCustomer.UpdatedAt)
	}
	if !afterCustomer.CreatedAt.Equal(beforeCustomer.CreatedAt) {
		t.Errorf("CreatedAt changed from %v to %v, want unchanged", beforeCustomer.CreatedAt, afterCustomer.CreatedAt)
	}

	events := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries
		WHERE customer_id = $1 AND event_type = 'customer.status_changed'`, created.Id)
	if events == 0 {
		t.Error("no customer.status_changed timeline entry was recorded")
	}
}

// Ported from Integration/CustomerStatsAndStatusTests.cs.
// UpdatingWithInvalidStatus_ReturnsValidationProblem.
func TestUpdatingWithInvalidStatus_ReturnsValidationProblem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Status Invalid")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{
		"name": "Status Invalid", "status": "deleted",
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if _, ok := problem.Errors["status"]; !ok {
		t.Errorf("errors = %v, want a key \"status\"", problem.Errors)
	}
}

// The three tests below are not ports: customers inventory §7 lists no .NET
// test class for CustomerStatsEndpoints.cs's summary/timeseries/attention
// (a repo-wide search of Customers.Module.Tests turns up none), so these
// three operations reached this task with no test to port. They still need
// direct coverage — module.Compose's coverage gate requires every
// implemented operation be exercised by a contract-validated exchange.

// TestGetCustomersStatsAttention_IsAlwaysEmpty pins the stub
// (CustomerStatsEndpoints.cs:111-112, inventory §8.5): always `[]`,
// regardless of what data exists.
func TestGetCustomersStatsAttention_IsAlwaysEmpty(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createCustomer(t, c, "Attention Probe")

	r := c.Do(http.MethodGet, "/api/v1/customers/stats/attention", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var items []map[string]any
	r.JSON(&items)
	if len(items) != 0 {
		t.Errorf("items = %v, want an empty array", items)
	}
}

type summaryJSON struct {
	From                      time.Time `json:"from"`
	To                        time.Time `json:"to"`
	TotalActiveCustomers      int       `json:"totalActiveCustomers"`
	TotalActiveCustomersDelta int       `json:"totalActiveCustomersDelta"`
	NewCustomers              int       `json:"newCustomers"`
	NewCustomersDelta         int       `json:"newCustomersDelta"`
	NewContacts               int       `json:"newContacts"`
	NewContactsDelta          int       `json:"newContactsDelta"`
}

// TestGetCustomersStatsSummary_CountsNewCustomersWithinTheDefaultPeriod pins
// the default 30-day window (CustomerStatsEndpoints.cs:13,120-121): a
// customer created now counts toward newCustomers.
func TestGetCustomersStatsSummary_CountsNewCustomersWithinTheDefaultPeriod(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createCustomer(t, c, "Summary Probe")
	h.Advance(time.Second) // the period's default "to" is now; created_at must fall strictly before it.

	r := c.Do(http.MethodGet, "/api/v1/customers/stats/summary", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var summary summaryJSON
	r.JSON(&summary)
	if summary.NewCustomers < 1 {
		t.Errorf("NewCustomers = %d, want >= 1", summary.NewCustomers)
	}
	if summary.TotalActiveCustomers < 1 {
		t.Errorf("TotalActiveCustomers = %d, want >= 1", summary.TotalActiveCustomers)
	}
	if !summary.From.Before(summary.To) {
		t.Errorf("From = %v, To = %v, want From before To", summary.From, summary.To)
	}
}

// TestGetCustomersStatsSummary_InvalidPeriodIsRejected pins
// TryNormalizePeriod's 400 (CustomerStatsEndpoints.cs:122-134): from after
// to answers "Invalid period".
func TestGetCustomersStatsSummary_InvalidPeriodIsRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/customers/stats/summary?from=2026-09-12T00:00:00Z&to=2026-09-01T00:00:00Z", nil)
	if r.Status != http.StatusBadRequest {
		t.Errorf("status %d body %s, want 400", r.Status, r.Body)
	}
}

// TestGetCustomersStatsTimeseries_BucketsNewCustomersByDay pins the
// newCustomers metric grouping by UTC calendar day
// (CustomerStatsEndpoints.cs:87-98).
func TestGetCustomersStatsTimeseries_BucketsNewCustomersByDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createCustomer(t, c, "Timeseries Probe")
	h.Advance(time.Second) // the period's default "to" is now; created_at must fall strictly before it.

	r := c.Do(http.MethodGet, "/api/v1/customers/stats/timeseries?metric=newCustomers", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var buckets []struct {
		Date  string `json:"date"`
		Value int64  `json:"value"`
	}
	r.JSON(&buckets)
	var total int64
	for _, b := range buckets {
		total += b.Value
	}
	if total < 1 {
		t.Errorf("bucket total = %d, want >= 1 for the customer just created", total)
	}
}

// TestGetCustomersStatsTimeseries_InvalidMetricIsRejected pins the metric
// check that runs after the period is normalized
// (CustomerStatsEndpoints.cs:78-85).
func TestGetCustomersStatsTimeseries_InvalidMetricIsRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/customers/stats/timeseries?metric=bogus", nil)
	if r.Status != http.StatusBadRequest {
		t.Errorf("status %d body %s, want 400", r.Status, r.Body)
	}
}
