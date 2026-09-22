package customers_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
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
		"identity": map[string]any{"country": "no", "type": "business", "id": "913456785", "name": "Stats AS", "source": "manual"},
	})
	c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name":     "Stats Person",
		"type":     "person",
		"identity": map[string]any{"country": "se", "type": "person", "id": "19770101-1234", "name": "Stats Person", "source": "manual"},
	})
	c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Stats Unknown"})

	r := c.Do(http.MethodGet, "/api/v1/customers/stats", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var stats statsJSON
	r.JSON(&stats)

	// The .NET test asserts lower bounds because its database is shared
	// across the whole test run; each Go test gets its own migrated
	// database (internal/modtest), so the exact three customers created
	// above are the only ones that exist and the counts can be pinned
	// exactly.
	if stats.TotalCount != 3 || stats.ActiveCount != 3 || stats.NewLast30DaysCount != 3 {
		t.Errorf("stats = %+v, want TotalCount=ActiveCount=NewLast30DaysCount=3", stats)
	}
	if stats.BusinessCount == nil || stats.PersonCount == nil || stats.MissingIdentityCount == nil || stats.DistinctCountryCount == nil {
		t.Fatalf("stats = %+v, want every identity figure non-nil for a legal-identity-view holder", stats)
	}
	// Business and person are counted on the customer type
	// (00007_customers_type.sql), so "Stats Unknown" — a business without a
	// legal identity — is a business as well as a missing identity: the two
	// type figures partition the total, and the identity figure overlaps
	// them. (.NET counted legal_type, where an identity-less customer was
	// neither.)
	if *stats.BusinessCount != 2 || *stats.PersonCount != 1 || *stats.MissingIdentityCount != 1 || *stats.DistinctCountryCount != 2 {
		t.Errorf("stats = %+v, want BusinessCount=2, PersonCount=MissingIdentityCount=1 and DistinctCountryCount=2 (no, se)", stats)
	}
	if stats.TotalCount != *stats.BusinessCount+*stats.PersonCount {
		t.Errorf("TotalCount = %d, want BusinessCount+PersonCount = %d",
			stats.TotalCount, *stats.BusinessCount+*stats.PersonCount)
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

	if stats.TotalCount != 1 {
		t.Errorf("TotalCount = %d, want 1 (this test's own isolated database)", stats.TotalCount)
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

// attentionItemJSON is CustomerStatsAttentionItem's five fields.
type attentionItemJSON struct {
	Id         string    `json:"id"`
	Type       string    `json:"type"`
	Title      string    `json:"title"`
	OccurredAt time.Time `json:"occurredAt"`
	EntityId   string    `json:"entityId"`
}

// getAttention reads GET .../stats/attention and decodes it, failing the
// test on anything but 200.
func getAttention(t *testing.T, c *modtest.Client) []attentionItemJSON {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/customers/stats/attention", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var items []attentionItemJSON
	r.JSON(&items)
	return items
}

// insertRegistryRecord writes a customer_registry_records row directly
// (Brreg in full design D4, task 3): the four attention types only need the
// three status flags, an optional deletion date and the record's own name,
// so the HTTP tests below build the row straight rather than driving it
// through a fake Brreg transport the way registry_test.go's end-to-end
// coverage of the refresh itself already does. deletedOn is "" for no
// deletion.
//
// organisation_number is read from the customer's own legal_id rather than
// written as a literal: a record only counts while it is still the company the
// identity names (fix round 2, C2), so a row with some other number is
// invisible to the attention list by design — which is its own test, in
// registry_test.go, not an accident every test here should inherit.
func insertRegistryRecord(t *testing.T, h *modtest.Harness, customerID int32, recordName string, bankrupt, underLiquidation, underForcedLiquidation bool, deletedOn string, fetchedAt time.Time) {
	t.Helper()
	var deleted pgtype.Date
	if deletedOn != "" {
		d, err := time.Parse("2006-01-02", deletedOn)
		if err != nil {
			t.Fatalf("insertRegistryRecord: bad deletedOn %q: %v", deletedOn, err)
		}
		deleted = pgtype.Date{Time: d, Valid: true}
	}
	h.Exec(t, `
		INSERT INTO customers.customer_registry_records
			(customer_id, organisation_number, name, vat_registered, bankrupt, under_liquidation, under_forced_liquidation, deleted_on, fetched_at)
		VALUES ($1, (SELECT legal_id FROM customers.customers WHERE id = $1), $2, false, $3, $4, $5, $6, $7)`,
		customerID, recordName, bankrupt, underLiquidation, underForcedLiquidation, deleted, fetchedAt)
}

// TestGetCustomersStatsAttention_ComputesTheFourTypes drives the endpoint
// end to end (design D4, task 3): a customer with no record at all yields
// nothing, one of each of the four types appears with the customer's own
// name as title (never the registry's) and the customer id as entityId,
// and the whole list is ordered newest fetch first.
func TestGetCustomersStatsAttention_ComputesTheFourTypes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	createCustomer(t, c, "No Record At All")

	bankrupt := createCustomerWithIdentity(t, c, "Bankrupt Co", "no", "111111111")
	liquidation := createCustomerWithIdentity(t, c, "Liquidation Co", "no", "222222222")
	deleted := createCustomerWithIdentity(t, c, "Deleted Co", "no", "333333333")
	renamed := createCustomerWithIdentity(t, c, "Renamed Co", "no", "444444444")

	base := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	insertRegistryRecord(t, h, bankrupt.Id, "Bankrupt Co", true, false, false, "", base.Add(3*time.Hour))
	insertRegistryRecord(t, h, liquidation.Id, "Liquidation Co", false, true, false, "", base.Add(2*time.Hour))
	insertRegistryRecord(t, h, deleted.Id, "Deleted Co", false, false, false, "2026-09-21", base.Add(1*time.Hour))
	insertRegistryRecord(t, h, renamed.Id, "Renamed Co AS", false, false, false, "", base)

	items := getAttention(t, c)
	if len(items) != 4 {
		t.Fatalf("items = %+v, want exactly 4 (the customer with no record contributes nothing)", items)
	}

	// The order is by occurredAt, and for the deleted one that is its deletion
	// date (2026-09-21) rather than its fetch (fix round 2, I3), which puts it
	// below the rename this test fetched an hour earlier but has no date for.
	want := []struct {
		id, typ, title, entityID string
	}{
		{fmt.Sprintf("registryBankrupt/%d", bankrupt.Id), "registryBankrupt", "Bankrupt Co", fmt.Sprintf("%d", bankrupt.Id)},
		{fmt.Sprintf("registryLiquidation/%d", liquidation.Id), "registryLiquidation", "Liquidation Co", fmt.Sprintf("%d", liquidation.Id)},
		{fmt.Sprintf("registryRenamed/%d", renamed.Id), "registryRenamed", "Renamed Co", fmt.Sprintf("%d", renamed.Id)},
		{fmt.Sprintf("registryDeleted/%d", deleted.Id), "registryDeleted", "Deleted Co", fmt.Sprintf("%d", deleted.Id)},
	}
	for i, w := range want {
		if items[i].Id != w.id || items[i].Type != w.typ || items[i].Title != w.title || items[i].EntityId != w.entityID {
			t.Errorf("items[%d] = %+v, want id=%s type=%s title=%s entityId=%s", i, items[i], w.id, w.typ, w.title, w.entityID)
		}
	}
}

// TestGetCustomersStatsAttention_BankruptcySuppressesLiquidation pins the
// controller ruling: one item per customer, bankruptcy wins over
// liquidation when a record carries both flags.
func TestGetCustomersStatsAttention_BankruptcySuppressesLiquidation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "Doubly Troubled AS", "no", "555555555")
	insertRegistryRecord(t, h, created.Id, "Doubly Troubled AS", true, true, true, "", time.Now())

	items := getAttention(t, c)
	if len(items) != 1 || items[0].Type != "registryBankrupt" {
		t.Fatalf("items = %+v, want exactly one registryBankrupt item", items)
	}
}

// TestGetCustomersStatsAttention_AnyStatusItemSuppressesRenamed pins the
// other half of the same ruling: a customer that is both bankrupt and
// renamed reports only the bankruptcy, its more useful single sentence.
func TestGetCustomersStatsAttention_AnyStatusItemSuppressesRenamed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "Also Renamed AS", "no", "666666666")
	insertRegistryRecord(t, h, created.Id, "A Completely Different Name AS", true, false, false, "", time.Now())

	items := getAttention(t, c)
	if len(items) != 1 || items[0].Type != "registryBankrupt" {
		t.Fatalf("items = %+v, want exactly one registryBankrupt item, not registryRenamed", items)
	}
}

// TestGetCustomersStatsAttention_ExcludesArchivedCustomers pins the
// join's own filter: an archived customer's record is still on file (a
// refresh never runs for one, but nothing deletes the row either), yet it
// must never reach the dashboard.
func TestGetCustomersStatsAttention_ExcludesArchivedCustomers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "Archived Bankrupt AS", "no", "777777777")
	insertRegistryRecord(t, h, created.Id, "Archived Bankrupt AS", true, false, false, "", time.Now())

	del := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil)
	if del.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s, want 204", del.Status, del.Body)
	}

	if items := getAttention(t, c); len(items) != 0 {
		t.Errorf("items = %+v, want none for an archived customer", items)
	}
}

// TestGetCustomersStatsAttention_ArchivingClearsEverything pins the "no
// dismiss state" design (D4): the list is computed live from the stored
// record against the current customer, so archiving a bankrupt customer
// clears its item exactly the way TestGetCustomersStatsAttention_ExcludesArchivedCustomers
// shows for one created already archived — this test instead watches the
// same item disappear across the transition.
func TestGetCustomersStatsAttention_ArchivingClearsEverything(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "About To Be Archived AS", "no", "888888888")
	insertRegistryRecord(t, h, created.Id, "About To Be Archived AS", true, false, false, "", time.Now())

	if items := getAttention(t, c); len(items) != 1 {
		t.Fatalf("items = %+v, want one item before archiving", items)
	}

	del := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil)
	if del.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s, want 204", del.Status, del.Body)
	}

	if items := getAttention(t, c); len(items) != 0 {
		t.Errorf("items = %+v, want none after archiving", items)
	}
}

// TestGetCustomersStatsAttention_RenameClearsWhenLegalIdentityIsUpdated
// pins registryRenamed's own clearing condition, the one of the four that
// is not "archive the customer": a PUT .../legal-identity that adopts the
// registry's name makes the two equal again, and the item disappears with
// no other write.
func TestGetCustomersStatsAttention_RenameClearsWhenLegalIdentityIsUpdated(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomerWithIdentity(t, c, "Old Name AS", "no", "999999999")
	insertRegistryRecord(t, h, created.Id, "New Name AS", false, false, false, "", time.Now())

	items := getAttention(t, c)
	if len(items) != 1 || items[0].Type != "registryRenamed" {
		t.Fatalf("items = %+v, want exactly one registryRenamed item", items)
	}

	update := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", created.Id), map[string]any{
		"country": "no", "type": "business", "id": "999999999", "name": "New Name AS", "source": "manual",
	})
	if update.Status != http.StatusOK {
		t.Fatalf("PUT legal-identity: status %d body %s, want 200", update.Status, update.Body)
	}

	if items := getAttention(t, c); len(items) != 0 {
		t.Errorf("items = %+v, want none once the legal name matches the registry's", items)
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
	// This test's own isolated database holds exactly the one customer
	// created above, so the counts are exact, not lower bounds.
	if summary.NewCustomers != 1 {
		t.Errorf("NewCustomers = %d, want 1", summary.NewCustomers)
	}
	if summary.TotalActiveCustomers != 1 {
		t.Errorf("TotalActiveCustomers = %d, want 1", summary.TotalActiveCustomers)
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
	// This test's own isolated database holds exactly the one customer
	// created above, all in the same UTC calendar day (the harness clock
	// only advanced by a second): exactly one bucket, exactly one count.
	if len(buckets) != 1 || buckets[0].Value != 1 {
		t.Errorf("buckets = %+v, want exactly one bucket with value 1", buckets)
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
