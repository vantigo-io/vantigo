package customers_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

// This file ports Integration/CustomerArchivalTests.cs (customers inventory
// §7). Its fourth test,
// The_customer_directory_resolves_archived_customers_flagged_as_archived, is
// already ported as directory_test.go's TestDirectory_ArchivedCustomerStillResolves
// (from the Task 3/5 directory work) — same assertion, an archived customer
// resolves through the directory flagged Archived with its name intact — so
// it is not duplicated here.

// Ported from Integration/CustomerArchivalTests.cs.
// Delete_archives_the_customer_and_keeps_it_resolvable_by_id.
func TestDeleteCustomer_ArchivesAndKeepsResolvableById(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Archive candidate")

	del := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil)
	if del.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s, want 204", del.Status, del.Body)
	}

	got := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil)
	var customer customerJSON
	got.JSON(&customer)
	if customer.Status != "archived" {
		t.Errorf("Status = %q, want archived", customer.Status)
	}

	again := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil)
	if again.Status != http.StatusNoContent {
		t.Errorf("second delete: status %d body %s, want 204 (idempotent)", again.Status, again.Body)
	}
}

// Ported from Integration/CustomerArchivalTests.cs.
// Archived_customers_are_hidden_from_the_default_listing_but_included_on_request.
func TestArchivedCustomer_HiddenFromDefaultListing_IncludedOnRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	name := fmt.Sprintf("Archived listing probe %d", h.Now().UnixNano())
	created := createCustomer(t, c, name)
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d, want 204", r.Status)
	}

	defaultList := c.Do(http.MethodGet, "/api/v1/customers?search="+url.QueryEscape(name), nil)
	var withoutArchived customerListJSON
	defaultList.JSON(&withoutArchived)
	if len(withoutArchived.Data) != 0 {
		t.Errorf("default listing data = %+v, want empty", withoutArchived.Data)
	}

	withArchived := c.Do(http.MethodGet, "/api/v1/customers?search="+url.QueryEscape(name)+"&includeArchived=true", nil)
	var included customerListJSON
	withArchived.JSON(&included)
	if len(included.Data) != 1 || included.Data[0].Status != "archived" {
		t.Errorf("includeArchived=true data = %+v, want one archived entry", included.Data)
	}
}

// Ported from Integration/CustomerArchivalTests.cs.
// An_archived_customer_can_be_reactivated_through_update.
func TestArchivedCustomer_CanBeReactivatedThroughUpdate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Unarchive candidate")
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d, want 204", r.Status)
	}

	update := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{
		"name": "Unarchive candidate", "status": "active",
	})
	if update.Status != http.StatusOK {
		t.Fatalf("update: status %d body %s, want 200", update.Status, update.Body)
	}

	got := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", created.Id), nil)
	var customer customerJSON
	got.JSON(&customer)
	if customer.Status != "active" {
		t.Errorf("Status = %q, want active", customer.Status)
	}
}
