package customers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is PUT and DELETE /customers/{id}/anonymisation (customers GDPR
// design D4) — the schedule, its refusals, its events, what calls it off — and
// the read-only rule an anonymised customer keeps. anonymisation_worker_test.go
// carries the anonymisation itself.

func putAnonymisation(t *testing.T, c *modtest.Client, id int32, anonymiseOn string) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/anonymisation", id), map[string]any{"anonymiseOn": anonymiseOn})
}

func deleteAnonymisation(t *testing.T, c *modtest.Client, id int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/anonymisation", id), nil)
}

// anonymisedCustomerJSON is customerJSON with the anonymisation (Task 4's
// field), for the tests that read it.
type anonymisedCustomerJSON struct {
	customerJSON
	Anonymisation *anonymisationJSON `json:"anonymisation"`
}

func customerAnswer(t *testing.T, r *modtest.Response) anonymisedCustomerJSON {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var c anonymisedCustomerJSON
	r.JSON(&c)
	return c
}

// archivedPerson is a private person, archived — the one customer design D4
// lets anybody schedule.
func archivedPerson(t *testing.T, c *modtest.Client, name string) createdCustomerJSON {
	t.Helper()
	person := createCustomerOfType(t, c, name, "person")
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", person.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive %s: status %d body %s", name, r.Status, r.Body)
	}
	return person
}

// markAnonymised makes a customer anonymised directly: the read-only rule is
// this file's subject, and the worker that really does it is the next file's.
func markAnonymised(t *testing.T, h *modtest.Harness, id int32) {
	t.Helper()
	h.Exec(t, `UPDATE customers.customers SET status = 'archived', anonymise_on = $2, anonymised_at = $3 WHERE id = $1`,
		id, h.Now().UTC().Format(time.DateOnly), h.Now())
}

type scheduledPayloadJSON struct {
	CustomerId          int32   `json:"customerId"`
	AnonymiseOn         string  `json:"anonymiseOn"`
	PreviousAnonymiseOn *string `json:"previousAnonymiseOn"`
}

// TestPutCustomersByIdAnonymisation_SchedulesAnArchivedPerson is design D4's
// happy path: the day goes on the customer, on its response and on its
// timeline, attributed to the caller, and the revision advances; the same day
// again writes nothing; another day moves it, and the event says from where.
func TestPutCustomersByIdAnonymisation_SchedulesAnArchivedPerson(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	person := archivedPerson(t, c, "Kari Nordmann")
	before := fetchCustomerJSON(t, c, person.Id)
	scheduler, schedulerID := h.SignInUser(t, "customers:view", "customers:personal-data", "customers:timeline-view")
	setDisplayName(t, h, schedulerID, "Siri Saksbehandler")

	scheduled := customerAnswer(t, putAnonymisation(t, scheduler, person.Id, day(h, 30)))
	if scheduled.Anonymisation == nil || scheduled.Anonymisation.AnonymiseOn != day(h, 30) || scheduled.Anonymisation.AnonymisedAt != nil {
		t.Errorf("anonymisation = %+v, want %s and not yet run", scheduled.Anonymisation, day(h, 30))
	}
	if scheduled.Revision != before.Revision+1 {
		t.Errorf("revision = %d (was %d), want one on", scheduled.Revision, before.Revision)
	}
	var got anonymisedCustomerJSON
	c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", person.Id), nil).JSON(&got)
	if got.Anonymisation == nil || got.Anonymisation.AnonymiseOn != day(h, 30) {
		t.Errorf("GET anonymisation = %+v, want the schedule", got.Anonymisation)
	}
	events := entriesOfType(timelineOf(t, c, person.Id), "customer.anonymisation_scheduled")
	if len(events) != 1 || str(events[0].Summary) != "Anonymisation scheduled for "+day(h, 30) || str(events[0].ActorDisplay) != "Siri Saksbehandler" {
		t.Fatalf("scheduled events = %+v, want one, by the caller", events)
	}
	var payload scheduledPayloadJSON
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil || payload.CustomerId != person.Id ||
		payload.AnonymiseOn != day(h, 30) || payload.PreviousAnonymiseOn != nil {
		t.Errorf("payload = %s, want {customerId, anonymiseOn} alone", events[0].Payload)
	}

	if again := customerAnswer(t, putAnonymisation(t, scheduler, person.Id, day(h, 30))); again.Revision != scheduled.Revision {
		t.Errorf("the same day again: revision %d, want %d — a no-op writes nothing", again.Revision, scheduled.Revision)
	}
	moved := customerAnswer(t, putAnonymisation(t, scheduler, person.Id, day(h, 60)))
	if moved.Anonymisation == nil || moved.Anonymisation.AnonymiseOn != day(h, 60) {
		t.Errorf("moved = %+v, want %s", moved.Anonymisation, day(h, 60))
	}
	events = entriesOfType(timelineOf(t, c, person.Id), "customer.anonymisation_scheduled")
	if len(events) != 2 || str(events[0].Summary) != fmt.Sprintf("Anonymisation moved from %s to %s", day(h, 30), day(h, 60)) {
		t.Errorf("scheduled events = %+v, want the move on top", events)
	}
	// Today is a day like any other: the worker takes it on its next cycle.
	if today := customerAnswer(t, putAnonymisation(t, scheduler, person.Id, day(h, 0))); today.Anonymisation.AnonymiseOn != day(h, 0) {
		t.Errorf("today = %+v", today.Anonymisation)
	}
}

// TestPutCustomersByIdAnonymisation_RefusesInOrder: the date is validated
// first, then the customer is looked up, then the read-only rule, a business,
// a customer that is not archived. None of them writes anything.
func TestPutCustomersByIdAnonymisation_RefusesInOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	scheduler := personalDataClient(t, h)
	person := archivedPerson(t, c, "Kari Nordmann")

	for raw, want := range map[string]string{
		"":           "AnonymiseOn must be an ISO date (yyyy-MM-dd)",
		"31.01.2027": "AnonymiseOn must be an ISO date (yyyy-MM-dd)",
		day(h, -1):   "An anonymisation date cannot be in the past",
	} {
		r := putAnonymisation(t, scheduler, person.Id, raw)
		var problem validationProblemJSON
		if r.JSON(&problem); r.Status != http.StatusBadRequest || len(problem.Errors["anonymiseOn"]) != 1 || problem.Errors["anonymiseOn"][0] != want {
			t.Errorf("anonymiseOn %q: status %d errors %v, want 400 %q", raw, r.Status, problem.Errors, want)
		}
	}
	if r := putAnonymisation(t, scheduler, 999999, day(h, 1)); r.Status != http.StatusNotFound {
		t.Errorf("unknown customer: status %d, want 404", r.Status)
	}

	business := createCustomer(t, c, "Acme AS")
	c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", business.Id), nil)
	refusedWith(t, putAnonymisation(t, scheduler, business.Id, day(h, 1)), "personal_data_not_a_person")

	active := createCustomerOfType(t, c, "Ola Nordmann", "person")
	problem := refusedWith(t, putAnonymisation(t, scheduler, active.Id, day(h, 1)), "personal_data_customer_active")
	if problem.Title != "Customer is not archived" || !strings.Contains(problem.Detail, "Archive it before scheduling") {
		t.Errorf("problem = %+v", problem)
	}

	survivor := createCustomerOfType(t, c, "Kari N.", "person")
	mergeOK(t, mergeClient(t, h), survivor.Id, person.Id)
	refusedWith(t, putAnonymisation(t, scheduler, person.Id, day(h, 1)), "customer_merged")

	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE anonymise_on IS NOT NULL`); n != 0 {
		t.Errorf("%d customers were scheduled by a refused request", n)
	}
	for _, keys := range [][]string{{"customers:view", "customers:update", "customers:delete"}, {"customers:personal-data"}} {
		if r := putAnonymisation(t, h.SignIn(t, keys...), survivor.Id, day(h, 1)); r.Status != http.StatusForbidden {
			t.Errorf("%v: status %d, want 403", keys, r.Status)
		}
	}
}

// TestDeleteCustomersByIdAnonymisation_CallsItOff: the schedule goes, the
// event says what it was, and a second cancel — nothing scheduled — writes
// nothing.
func TestDeleteCustomersByIdAnonymisation_CallsItOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	scheduler := personalDataClient(t, h)
	person := archivedPerson(t, c, "Kari Nordmann")
	scheduled := customerAnswer(t, putAnonymisation(t, scheduler, person.Id, day(h, 30)))

	cancelled := customerAnswer(t, deleteAnonymisation(t, scheduler, person.Id))
	if cancelled.Anonymisation != nil || cancelled.Revision != scheduled.Revision+1 {
		t.Errorf("cancelled = %+v at revision %d, want no anonymisation, one on from %d", cancelled.Anonymisation, cancelled.Revision, scheduled.Revision)
	}
	events := entriesOfType(timelineOf(t, c, person.Id), "customer.anonymisation_cancelled")
	if len(events) != 1 || str(events[0].Summary) != "Anonymisation cancelled; it was scheduled for "+day(h, 30) {
		t.Fatalf("cancelled events = %+v, want one naming the day", events)
	}
	if again := customerAnswer(t, deleteAnonymisation(t, scheduler, person.Id)); again.Revision != cancelled.Revision {
		t.Errorf("a second cancel: revision %d, want %d", again.Revision, cancelled.Revision)
	}
	if n := len(entriesOfType(timelineOf(t, c, person.Id), "customer.anonymisation_cancelled")); n != 1 {
		t.Errorf("cancelled events = %d, want still 1", n)
	}
	if r := deleteAnonymisation(t, scheduler, 999999); r.Status != http.StatusNotFound {
		t.Errorf("unknown customer: status %d, want 404", r.Status)
	}
}

// TestLeavingTheArchiveCallsTheScheduleOff is this plan's reading of design
// D4: an archived private person is what may be scheduled, so a restore — the
// customer PUT or a CSV row — or a change of type away from person takes the
// date off in the same transaction, recorded, rather than leaving it to fire
// the day the customer is archived again.
func TestLeavingTheArchiveCallsTheScheduleOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	scheduler := personalDataClient(t, h)

	restored := archivedPerson(t, c, "Kari Nordmann")
	customerAnswer(t, putAnonymisation(t, scheduler, restored.Id, day(h, 30)))
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", restored.Id), map[string]any{"name": "Kari Nordmann", "status": "active"})
	if got := customerAnswer(t, r); got.Status != "active" || got.Anonymisation != nil {
		t.Errorf("restored = %s with %+v, want active and nothing scheduled", got.Status, got.Anonymisation)
	}
	if n := len(entriesOfType(timelineOf(t, c, restored.Id), "customer.anonymisation_cancelled")); n != 1 {
		t.Errorf("a restore recorded %d cancellations, want 1", n)
	}

	// The CSV import restores through the same writeCustomerCore.
	imported := archivedPerson(t, c, "Per Nordmann")
	customerAnswer(t, putAnonymisation(t, scheduler, imported.Id, day(h, 30)))
	if result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		[]string{"customerNumber", "name", "status"},
		[]string{strconv.FormatInt(imported.CustomerNumber, 10), "Per Nordmann", "active"},
	))); result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("import = %+v, want the row updated", result)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND status = 'active' AND anonymise_on IS NULL`, imported.Id); n != 1 {
		t.Error("a CSV restore kept the anonymisation date")
	}
	if n := len(entriesOfType(timelineOf(t, c, imported.Id), "customer.anonymisation_cancelled")); n != 1 {
		t.Errorf("a CSV restore recorded %d cancellations, want 1", n)
	}

	retyped := archivedPerson(t, c, "Ola Nordmann")
	customerAnswer(t, putAnonymisation(t, scheduler, retyped.Id, day(h, 30)))
	if r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/type", retyped.Id), map[string]any{"type": "business"}); r.Status != http.StatusOK {
		t.Fatalf("type change: status %d body %s", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND anonymise_on IS NULL`, retyped.Id); n != 1 {
		t.Error("a business kept its anonymisation date")
	}
	if n := len(entriesOfType(timelineOf(t, c, retyped.Id), "customer.anonymisation_cancelled")); n != 1 {
		t.Errorf("a type change recorded %d cancellations, want 1", n)
	}
}

// A schedule made before the customer was merged away can still be called off:
// cancelling is the one write a merged-away customer takes.
func TestAScheduleMadeBeforeAMergeCanStillBeCalledOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	scheduler := personalDataClient(t, h)
	absorbed := archivedPerson(t, c, "Kari Nordmann")
	customerAnswer(t, putAnonymisation(t, scheduler, absorbed.Id, day(h, 30)))
	survivor := createCustomerOfType(t, c, "Kari N.", "person")
	mergeOK(t, mergeClient(t, h), survivor.Id, absorbed.Id)

	if got := customerAnswer(t, deleteAnonymisation(t, scheduler, absorbed.Id)); got.Anonymisation != nil || got.MergedInto == nil {
		t.Errorf("cancelled = %+v merged into %+v, want nothing scheduled and still merged away", got.Anonymisation, got.MergedInto)
	}
}

// TestAnAnonymisedCustomerIsReadOnly is design D4's read-only rule, through the
// same lock-time check a merged-away customer's refusal comes from: every write
// answers customer_anonymised — its schedule's included — a CSV row naming it
// is refused, it cannot be absorbed by a merge, archiving it again is the
// no-op it is for any archived customer, and its export still answers.
func TestAnAnonymisedCustomerIsReadOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := mergeClient(t, h)
	person := archivedPerson(t, c, "Anonymised person")
	markAnonymised(t, h, person.Id)
	id := person.Id

	writes := map[string]*modtest.Response{
		"restore":          c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", id), map[string]any{"name": "Kari Nordmann", "status": "active"}),
		"contact info":     putContactInfo(t, c, id, map[string]any{"email": "kari@example.test"}),
		"address":          c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/addresses", id), fullAddressBody("postal", nil)),
		"tags":             putCustomerTags(t, c, id, []string{createTag(t, c, map[string]any{"name": "Nabo"}).Id}),
		"timeline entry":   c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/timeline", id), map[string]any{"eventType": "note", "occurredOn": day(h, 0), "note": "Ringte"}),
		"schedule":         putAnonymisation(t, personalDataClient(t, h), id, day(h, 1)),
		"cancel":           deleteAnonymisation(t, personalDataClient(t, h), id),
		"merged elsewhere": postMerge(t, c, createCustomerOfType(t, c, "Kari N.", "person").Id, map[string]any{"sourceId": id}),
	}
	for name, r := range writes {
		problem := refusedWith(t, r, "customer_anonymised")
		if problem.Title != "Customer was anonymised" || !strings.Contains(problem.Detail, "anonymised on "+day(h, 0)) {
			t.Errorf("%s: problem = %+v", name, problem)
		}
	}

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		[]string{"customerNumber", "name"},
		[]string{strconv.FormatInt(person.CustomerNumber, 10), "Kari Nordmann"},
	)))
	if len(result.Errors) != 1 || result.Errors[0].Column != "customerNumber" ||
		result.Errors[0].Message != fmt.Sprintf("Customer %d was anonymised and takes no more changes", person.CustomerNumber) {
		t.Errorf("import = %+v, want the row refused on customerNumber", result)
	}
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", id), nil); r.Status != http.StatusNoContent {
		t.Errorf("archive again: status %d, want the 204 no-op", r.Status)
	}
	var file personalDataJSON
	if r := getPersonalData(t, personalDataClient(t, h), id); r.Status != http.StatusOK {
		t.Errorf("export: status %d body %s, want 200", r.Status, r.Body)
	} else if r.JSON(&file); file.Customer.Anonymisation == nil || file.Customer.Anonymisation.AnonymisedAt == nil {
		t.Errorf("export anonymisation = %+v, want it done", file.Customer.Anonymisation)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND status = 'archived' AND email IS NULL`, id); n != 1 {
		t.Error("a refused write changed the anonymised customer")
	}
}
