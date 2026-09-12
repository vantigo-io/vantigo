package customers_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// This file pins behaviour customers inventory §1.4 and §2.4 document that
// TimelineEndpointsTests.cs's 10 tests (timeline_test.go) do not themselves
// exercise: per-endpoint validation/existence ordering, the 404 surface for
// an unknown customer or entry, and the exact wording of the "immutable" and
// "missing expectedRevision" refusals. Each assertion is on the literal
// message text, not just the status code, since a status-only assertion
// cannot tell one of the three concurrency guards (or the immutable check)
// apart from another that happens to answer the same code.

// Ported from Integration/TimelineEndpointsTests.cs (ordering only; no
// single .NET test asserts this combination directly, but customers
// inventory §1.4 pins it: "a malformed cursor against a nonexistent
// customer id returns 404, not 400 (existence checked first)").
func TestGetCustomersByIdTimeline_ExistenceCheckedBeforeCursorDecode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodGet, "/api/v1/customers/999999/timeline?cursor=not-base64!!", nil)
	if r.Status != http.StatusNotFound {
		t.Fatalf("status %d body %s, want 404 (existence wins over a malformed cursor)", r.Status, r.Body)
	}
}

func TestGetCustomersByIdTimeline_UnknownCustomer_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	r := c.Do(http.MethodGet, "/api/v1/customers/999999/timeline", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d, want 404", r.Status)
	}
}

func TestGetCustomersByIdTimeline_LimitOutOfRange_Returns400WithExactMessage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Limit Co")

	for _, limit := range []int{0, 101} {
		r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?limit=%d", customer.Id, limit), nil)
		if r.Status != http.StatusBadRequest {
			t.Fatalf("limit=%d: status %d, want 400", limit, r.Status)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		want := "Limit must be between 1 and 100"
		if got := problem.Errors["limit"]; len(got) != 1 || got[0] != want {
			t.Errorf("limit=%d: errors[limit] = %v, want [%q]", limit, got, want)
		}
	}
}

// Ported from Integration/TimelineEndpointsTests.cs (Create's existence
// check; TimelineEndpoints.Create returns 404 once validation passes).
func TestPostCustomersByIdTimeline_UnknownCustomer_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	r := c.Do(http.MethodPost, "/api/v1/customers/999999/timeline", map[string]any{
		"eventType": "note", "occurredOn": "2026-07-27", "note": "orphan",
	})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// GetCustomersByIdTimelineByEntryId only shows active entries
// (TimelineEndpoints.Get, customers inventory §2.4): a soft-deleted manual
// entry answers 404, the same as one that never existed.
func TestGetCustomersByIdTimelineByEntryId_DeletedEntry_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Deleted Get Co")
	entry := createManual(t, c, customer.Id, "2026-07-27", "will be deleted")

	del := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/timeline/%d?expectedRevision=1", customer.Id, entry.Id), nil)
	if del.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d, want 204", del.Status)
	}

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customer.Id, entry.Id), nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("get soft-deleted entry: status %d, want 404", r.Status)
	}
}

// Revisions shows a soft-deleted entry's full history, unlike Get
// (TimelineEndpoints.Revisions, customers inventory §1.1's Notes column and
// §2.4).
func TestGetCustomersByIdTimelineByEntryIdRevisions_VisibleAfterDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Revisions Visible Co")
	entry := createManual(t, c, customer.Id, "2026-07-27", "will be deleted")
	c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/timeline/%d?expectedRevision=1", customer.Id, entry.Id), nil)

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline/%d/revisions", customer.Id, entry.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("revisions after delete: status %d body %s, want 200", r.Status, r.Body)
	}
	var revisions timelineRevisionListJSON
	r.JSON(&revisions)
	if len(revisions.Data) != 2 {
		t.Errorf("revisions count = %d, want 2 (create, delete)", len(revisions.Data))
	}
}

func TestPutCustomersByIdTimelineByEntryId_UnknownEntry_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Update Unknown Co")
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/timeline/999999", customer.Id), map[string]any{
		"eventType": "note", "occurredOn": "2026-07-27", "note": "n/a", "expectedRevision": 1,
	})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

func TestDeleteCustomersByIdTimelineByEntryId_UnknownEntry_Returns404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Delete Unknown Co")
	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/timeline/999999?expectedRevision=1", customer.Id), nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TimelineEndpoints.Delete treats a missing expectedRevision as a revision
// mismatch, never as "no check requested"
// (`expectedRevision is null || ... != entry.CurrentRevision`), and reports
// it with the literal word "missing" in place of a number
// (TimelineEndpoints.cs:258).
func TestDeleteCustomersByIdTimelineByEntryId_MissingExpectedRevision_ReportsMissing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Missing Revision Co")
	entry := createManual(t, c, customer.Id, "2026-07-27", "needs a revision")

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customer.Id, entry.Id), nil)
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemDetailsJSON
	r.JSON(&problem)
	if problemTitle(problem.Title) != "Timeline revision conflict" {
		t.Errorf("Title = %q, want %q", problemTitle(problem.Title), "Timeline revision conflict")
	}
	want := "The timeline entry has revision 1; the supplied expectedRevision was missing."
	if problemTitle(problem.Detail) != want {
		t.Errorf("Detail = %q, want %q", problemTitle(problem.Detail), want)
	}
}

// The immutable check fires for a soft-deleted entry too, with Delete's own
// wording, distinct from Update's (TimelineEndpoints.cs:257 vs :212).
func TestDeleteCustomersByIdTimelineByEntryId_AlreadyDeleted_IsImmutable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Already Deleted Co")
	entry := createManual(t, c, customer.Id, "2026-07-27", "delete twice")
	first := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/timeline/%d?expectedRevision=1", customer.Id, entry.Id), nil)
	if first.Status != http.StatusNoContent {
		t.Fatalf("first delete: status %d, want 204", first.Status)
	}

	second := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/timeline/%d?expectedRevision=2", customer.Id, entry.Id), nil)
	if second.Status != http.StatusConflict {
		t.Fatalf("second delete: status %d body %s, want 409", second.Status, second.Body)
	}
	var problem problemDetailsJSON
	second.JSON(&problem)
	if problemTitle(problem.Title) != "Timeline entry is immutable" {
		t.Errorf("Title = %q, want %q", problemTitle(problem.Title), "Timeline entry is immutable")
	}
	if problemTitle(problem.Detail) != "Generated, deleted, or voided timeline entries cannot be deleted." {
		t.Errorf("Detail = %q, want the immutable-delete message", problemTitle(problem.Detail))
	}
}

// A note over 10000 characters and a sourceUrl over 2048 characters are
// distinct field errors (TimelineEndpoints.cs:394-397, :417-420), not one
// generic "too long" message.
func TestPostCustomersByIdTimeline_OverlongFields_Return400WithExactMessages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Overlong Co")

	longNote := strings.Repeat("a", 10001)
	longURL := "https://example.test/" + strings.Repeat("a", 2048)

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/timeline", customer.Id), map[string]any{
		"eventType": "note", "occurredOn": "2026-07-27", "note": longNote, "sourceUrl": longURL,
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if got := problem.Errors["note"]; len(got) != 1 || got[0] != "A note cannot be longer than 10000 characters" {
		t.Errorf("errors[note] = %v, want the length message", got)
	}
	if got := problem.Errors["sourceUrl"]; len(got) != 1 || got[0] != "SourceUrl cannot be longer than 2048 characters" {
		t.Errorf("errors[sourceUrl] = %v, want the length message", got)
	}
}

// occurredAt in the future is rejected even when occurredOn is a valid past
// date (TimelineEndpoints.cs:400-403).
func TestPostCustomersByIdTimeline_OccurredAtInFuture_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Future Instant Co")

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/timeline", customer.Id), map[string]any{
		"eventType": "note", "occurredOn": "2026-07-27", "occurredAt": "2027-01-01T00:00:00Z", "note": "from the future",
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if got := problem.Errors["occurredAt"]; len(got) != 1 || got[0] != "An occurrence instant cannot be in the future" {
		t.Errorf("errors[occurredAt] = %v, want the future-instant message", got)
	}
}
