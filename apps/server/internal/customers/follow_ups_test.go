package customers_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is follow-ups design D1, D2 and D3 end to end: the follow-up a
// manual entry carries, the two paths that tick and untick it, the two
// attention types it feeds, and the list that answers "what is on my plate".
//
// Every date here is written relative to the harness clock (h.Now()), never as
// a literal: the overdue-vs-due-today split is a UTC calendar comparison, and a
// hard-coded date makes a test that passes today and fails on a day this
// module's clock is moved.

type followUpAssigneeJSON struct {
	UserId      uuid.UUID `json:"userId"`
	DisplayName string    `json:"displayName"`
	Active      bool      `json:"active"`
}

type followUpJSON struct {
	DueOn    string                `json:"dueOn"`
	Assignee *followUpAssigneeJSON `json:"assignee"`
	DoneAt   *time.Time            `json:"doneAt"`
}

// followUpEntryJSON is timelineEntryJSON's fields this file cares about plus
// the follow-up. A separate type rather than a widening of timelineEntryJSON:
// the tests already in timeline_test.go assert an entry's whole shape, and a
// field added there would have to be asserted in every one of them.
type followUpEntryJSON struct {
	Id              int32         `json:"id"`
	EventType       string        `json:"eventType"`
	Provenance      string        `json:"provenance"`
	OccurredOn      string        `json:"occurredOn"`
	Note            *string       `json:"note"`
	CurrentRevision int32         `json:"currentRevision"`
	State           string        `json:"state"`
	FollowUp        *followUpJSON `json:"followUp"`
}

type followUpRowJSON struct {
	EntryId      int32        `json:"entryId"`
	CustomerId   int32        `json:"customerId"`
	CustomerName string       `json:"customerName"`
	EventType    string       `json:"eventType"`
	OccurredOn   string       `json:"occurredOn"`
	Note         *string      `json:"note"`
	FollowUp     followUpJSON `json:"followUp"`
}

type followUpListJSON struct {
	Data       []followUpRowJSON `json:"data"`
	Pagination struct {
		Page       int32 `json:"page"`
		PageSize   int32 `json:"pageSize"`
		TotalCount int32 `json:"totalCount"`
		TotalPages int32 `json:"totalPages"`
	} `json:"pagination"`
}

// day is the UTC calendar date `offset` days from the harness clock, formatted
// the way the contract wants it.
func day(h *modtest.Harness, offset int) string {
	return h.Now().UTC().AddDate(0, 0, offset).Format("2006-01-02")
}

// createWithFollowUp posts a manual entry carrying followUp and returns it,
// failing the test on anything but 201.
func createWithFollowUp(t *testing.T, c *modtest.Client, customerID int32, occurredOn, note string, followUp map[string]any) followUpEntryJSON {
	t.Helper()
	body := map[string]any{"eventType": "note", "occurredOn": occurredOn, "note": note}
	if followUp != nil {
		body["followUp"] = followUp
	}
	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/timeline", customerID), body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create entry with %v: status %d body %s, want 201", followUp, r.Status, r.Body)
	}
	var entry followUpEntryJSON
	r.JSON(&entry)
	return entry
}

// followUpDone posts (done=true) or deletes (done=false) the entry's
// follow-up-done state and answers the raw response, so a test can assert a
// status this helper has no business deciding.
func followUpDone(t *testing.T, c *modtest.Client, customerID, entryID int32, done bool) *modtest.Response {
	t.Helper()
	path := fmt.Sprintf("/api/v1/customers/%d/timeline/%d/follow-up/done", customerID, entryID)
	if done {
		return c.Do(http.MethodPost, path, nil)
	}
	return c.Do(http.MethodDelete, path, nil)
}

// listFollowUps reads GET /customers/follow-ups with query, failing the test on
// anything but 200.
func listFollowUps(t *testing.T, c *modtest.Client, query url.Values) followUpListJSON {
	t.Helper()
	path := "/api/v1/customers/follow-ups"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	r := c.Do(http.MethodGet, path, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list follow-ups %v: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var list followUpListJSON
	r.JSON(&list)
	return list
}

// TestPostTimeline_CarriesAFollowUpAndAcceptsAFutureDate pins the two things
// design D1 says about dueOn that occurredOn does not: it is a strict
// yyyy-MM-dd, and it MAY be in the future — that is the whole point of a
// follow-up. The assignee comes back named from the directory, not as a bare id.
func TestPostTimeline_CarriesAFollowUpAndAcceptsAFutureDate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, caller := authenticatedClientWithID(t, h)
	setDisplayName(t, h, caller, "Kari Nordmann")
	customer := createCustomer(t, c, "Follow Up Co")

	entry := createWithFollowUp(t, c, customer.Id, day(h, 0), "Call about the renewal", map[string]any{
		"dueOn": day(h, 14), "assigneeUserId": caller,
	})
	if entry.FollowUp == nil {
		t.Fatalf("followUp is absent, want one: %+v", entry)
	}
	if entry.FollowUp.DueOn != day(h, 14) {
		t.Errorf("dueOn = %q, want %q (a follow-up may be in the future)", entry.FollowUp.DueOn, day(h, 14))
	}
	if entry.FollowUp.DoneAt != nil {
		t.Errorf("doneAt = %v, want absent on a new follow-up", entry.FollowUp.DoneAt)
	}
	if entry.FollowUp.Assignee == nil || entry.FollowUp.Assignee.DisplayName != "Kari Nordmann" || !entry.FollowUp.Assignee.Active {
		t.Errorf("assignee = %+v, want Kari Nordmann, active", entry.FollowUp.Assignee)
	}

	// An entry with no followUp key answers no followUp at all — omitted, never
	// null, like every other optional field this API answers with.
	plain := createWithFollowUp(t, c, customer.Id, day(h, 0), "Just a note", nil)
	if plain.FollowUp != nil {
		t.Errorf("followUp = %+v, want absent when the request carried none", plain.FollowUp)
	}
}

// TestPostTimeline_RefusesAMalformedDueDateAndAnUnusableAssignee pins the two
// field errors, both keyed the way design D1 names them, and the wording of the
// assignee's — the owner's own, because it is the same claim about the same
// directory.
func TestPostTimeline_RefusesAMalformedDueDateAndAnUnusableAssignee(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Bad Follow Up Co")
	gone := seedNamedUser(t, h, "Vanished Personsen")
	forgetUser(t, h, gone)
	disabled := seedNamedUser(t, h, "Disabled Personsen")
	disableUser(t, h, disabled)

	cases := []struct {
		name     string
		followUp map[string]any
		field    string
		message  string
	}{
		{"a looser date", map[string]any{"dueOn": "2026-9-1"}, "followUp.dueOn", "FollowUp.dueOn must be an ISO date (yyyy-MM-dd)"},
		{"no date at all", map[string]any{}, "followUp.dueOn", "FollowUp.dueOn must be an ISO date (yyyy-MM-dd)"},
		{"a user who does not exist", map[string]any{"dueOn": "2026-10-01", "assigneeUserId": gone}, "followUp.assigneeUserId", fmt.Sprintf("User %s does not exist", gone)},
		{"a disabled user", map[string]any{"dueOn": "2026-10-01", "assigneeUserId": disabled}, "followUp.assigneeUserId", fmt.Sprintf("User %s is disabled and cannot be given a follow-up", disabled)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/timeline", customer.Id), map[string]any{
				"eventType": "note", "occurredOn": day(h, 0), "note": "n", "followUp": tc.followUp,
			})
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var problem validationProblemJSON
			r.JSON(&problem)
			if msgs := problem.Errors[tc.field]; len(msgs) != 1 || msgs[0] != tc.message {
				t.Errorf("errors[%s] = %v, want [%q]", tc.field, msgs, tc.message)
			}
		})
	}
}

// TestPutTimeline_ReplacesTheFollowUpAndClearingItClearsDone is design D1's
// "set, replace or clear with the entry" and its one consequence: clearing the
// follow-up clears its done state, because done-ness without a follow-up is not
// a state this module has. The PUT is a full replace, so an omitted followUp
// clears it exactly as an omitted sourceUrl already clears that.
func TestPutTimeline_ReplacesTheFollowUpAndClearingItClearsDone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, caller := authenticatedClientWithID(t, h)
	setDisplayName(t, h, caller, "Kari Nordmann")
	other := seedNamedUser(t, h, "Ola Nordmann")
	customer := createCustomer(t, c, "Replace Follow Up Co")
	entry := createWithFollowUp(t, c, customer.Id, day(h, 0), "original", map[string]any{
		"dueOn": day(h, 3), "assigneeUserId": caller,
	})

	if r := followUpDone(t, c, customer.Id, entry.Id, true); r.Status != http.StatusOK {
		t.Fatalf("mark done: status %d body %s, want 200", r.Status, r.Body)
	}

	// Replaced: a new date, a new assignee — and the done stamp survives,
	// because the follow-up was kept, not cleared.
	replaced := putEntryWithFollowUp(t, c, customer.Id, entry.Id, 2, map[string]any{
		"dueOn": day(h, 9), "assigneeUserId": other,
	})
	if replaced.FollowUp == nil || replaced.FollowUp.DueOn != day(h, 9) {
		t.Fatalf("followUp = %+v, want dueOn %s", replaced.FollowUp, day(h, 9))
	}
	if replaced.FollowUp.Assignee == nil || replaced.FollowUp.Assignee.DisplayName != "Ola Nordmann" {
		t.Errorf("assignee = %+v, want Ola Nordmann", replaced.FollowUp.Assignee)
	}
	if replaced.FollowUp.DoneAt == nil {
		t.Errorf("doneAt = nil, want it kept: editing a ticked follow-up must not un-tick it")
	}

	// Cleared: no followUp key at all, which this PUT reads as "none".
	cleared := putEntryWithFollowUp(t, c, customer.Id, entry.Id, replaced.CurrentRevision, nil)
	if cleared.FollowUp != nil {
		t.Errorf("followUp = %+v, want absent after a PUT that carried none", cleared.FollowUp)
	}
	var doneAt *time.Time
	if err := h.Pool().QueryRow(t.Context(),
		`SELECT follow_up_done_at FROM customers.customers_timeline_entries WHERE id = $1`, entry.Id).Scan(&doneAt); err != nil {
		t.Fatalf("read follow_up_done_at: %v", err)
	}
	if doneAt != nil {
		t.Errorf("follow_up_done_at = %v, want NULL: clearing a follow-up clears its done state", doneAt)
	}

	// An explicit null is the same instruction, and says it out loud. It cannot
	// go through putEntryWithFollowUp, which omits the KEY for a nil map — and
	// "the key is absent" and "the key is null" are exactly the two forms this
	// assertion exists to prove are one instruction. So this one builds the body
	// itself, with a literal JSON null on the wire.
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customer.Id, entry.Id), map[string]any{
		"eventType": "note", "occurredOn": "2020-01-01", "note": "edited again",
		"expectedRevision": cleared.CurrentRevision, "followUp": nil,
	})
	if r.Status != http.StatusOK {
		t.Fatalf("explicit null followUp: status %d body %s, want 200", r.Status, r.Body)
	}
	var again followUpEntryJSON
	r.JSON(&again)
	if again.FollowUp != nil {
		t.Errorf("followUp = %+v, want absent after an explicit null", again.FollowUp)
	}
}

// putEntryWithFollowUp updates an entry, sending followUp when it is non-nil
// and omitting the key entirely when it is nil, and answers the entry.
func putEntryWithFollowUp(t *testing.T, c *modtest.Client, customerID, entryID, expectedRevision int32, followUp map[string]any) followUpEntryJSON {
	t.Helper()
	body := map[string]any{
		"eventType": "note", "occurredOn": "2020-01-01", "note": "edited", "expectedRevision": expectedRevision,
	}
	if followUp != nil {
		body["followUp"] = followUp
	}
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customerID, entryID), body)
	if r.Status != http.StatusOK {
		t.Fatalf("put entry with %v: status %d body %s, want 200", followUp, r.Status, r.Body)
	}
	var entry followUpEntryJSON
	r.JSON(&entry)
	return entry
}

// TestPutTimeline_KeepsADisabledAssigneeItIsNotChanging is design D1's "an
// assignee disabled after being given the follow-up keeps it", read through the
// entry's full-replace PUT: editing the note means echoing the whole follow-up
// back, so re-sending the assignee already stored must not be re-validated —
// otherwise a colleague being disabled would freeze every entry they hold. A
// DIFFERENT, disabled assignee is still refused, which is what tells the two
// apart.
func TestPutTimeline_KeepsADisabledAssigneeItIsNotChanging(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Disabled Assignee Co")
	holder := seedNamedUser(t, h, "Holder Personsen")
	stranger := seedNamedUser(t, h, "Stranger Personsen")
	entry := createWithFollowUp(t, c, customer.Id, day(h, 0), "original", map[string]any{
		"dueOn": day(h, 3), "assigneeUserId": holder,
	})

	disableUser(t, h, holder)
	disableUser(t, h, stranger)

	// The same assignee, a new note and a new date: accepted, and the assignee
	// is still theirs — reported inactive, never dropped.
	edited := putEntryWithFollowUp(t, c, customer.Id, entry.Id, 1, map[string]any{
		"dueOn": day(h, 8), "assigneeUserId": holder,
	})
	if edited.FollowUp == nil || edited.FollowUp.Assignee == nil {
		t.Fatalf("followUp = %+v, want the assignee kept", edited.FollowUp)
	}
	if edited.FollowUp.Assignee.UserId != holder || edited.FollowUp.Assignee.Active {
		t.Errorf("assignee = %+v, want %s, inactive", edited.FollowUp.Assignee, holder)
	}

	// Handing it to somebody else who is disabled is a different request, and
	// still refused.
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customer.Id, entry.Id), map[string]any{
		"eventType": "note", "occurredOn": "2020-01-01", "note": "reassigned",
		"expectedRevision": edited.CurrentRevision,
		"followUp":         map[string]any{"dueOn": day(h, 8), "assigneeUserId": stranger},
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("reassign to a disabled user: status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := fmt.Sprintf("User %s is disabled and cannot be given a follow-up", stranger)
	if msgs := problem.Errors["followUp.assigneeUserId"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[followUp.assigneeUserId] = %v, want [%q]", msgs, want)
	}
}

// TestFollowUpDone_IsIdempotentAndEachRealChangeIsARevision is design D1's own
// sentence, split into its four claims: the first tick bumps current_revision
// and appends a revision row naming who ticked it; the second tick answers 200
// and writes NOTHING; reopening mirrors both; and neither path takes an
// expectedRevision, so a stale client can still tick.
func TestFollowUpDone_IsIdempotentAndEachRealChangeIsARevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, caller := authenticatedClientWithID(t, h)
	setDisplayName(t, h, caller, "Kari Nordmann")
	customer := createCustomer(t, c, "Idempotent Co")
	entry := createWithFollowUp(t, c, customer.Id, day(h, 0), "Ring back", map[string]any{"dueOn": day(h, 1)})
	if entry.CurrentRevision != 1 {
		t.Fatalf("currentRevision = %d, want 1 on a fresh entry", entry.CurrentRevision)
	}

	first := followUpDone(t, c, customer.Id, entry.Id, true)
	if first.Status != http.StatusOK {
		t.Fatalf("first tick: status %d body %s, want 200", first.Status, first.Body)
	}
	var ticked followUpEntryJSON
	first.JSON(&ticked)
	if ticked.CurrentRevision != 2 {
		t.Errorf("currentRevision = %d, want 2: a tick is a revision of the entry", ticked.CurrentRevision)
	}
	if ticked.FollowUp == nil || ticked.FollowUp.DoneAt == nil {
		t.Fatalf("followUp = %+v, want a doneAt", ticked.FollowUp)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries_revisions WHERE customer_timeline_entry_id = $1`, entry.Id); n != 2 {
		t.Errorf("revision rows = %d, want 2", n)
	}
	// The revision row records WHO ticked it, which is the reason a tick is a
	// revision at all rather than a quiet column write.
	who := modtest.One[string](t, h, `SELECT actor_display FROM customers.customers_timeline_entries_revisions
	                                  WHERE customer_timeline_entry_id = $1 ORDER BY revision_number DESC LIMIT 1`, entry.Id)
	if want := userDisplayName(t, h, caller); who != want {
		t.Errorf("revision actor = %q, want %q", who, want)
	}
	// And the revision snapshot carries the follow-up as it stood.
	doneInRevision := modtest.One[bool](t, h, `SELECT follow_up_done_at IS NOT NULL FROM customers.customers_timeline_entries_revisions
	                                           WHERE customer_timeline_entry_id = $1 ORDER BY revision_number DESC LIMIT 1`, entry.Id)
	if !doneInRevision {
		t.Errorf("revision follow_up_done_at is NULL, want the tick snapshotted into history")
	}

	second := followUpDone(t, c, customer.Id, entry.Id, true)
	if second.Status != http.StatusOK {
		t.Fatalf("second tick: status %d body %s, want 200 (idempotent)", second.Status, second.Body)
	}
	var again followUpEntryJSON
	second.JSON(&again)
	if again.CurrentRevision != 2 {
		t.Errorf("currentRevision = %d, want 2: a no-op tick writes nothing", again.CurrentRevision)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries_revisions WHERE customer_timeline_entry_id = $1`, entry.Id); n != 2 {
		t.Errorf("revision rows = %d, want 2 after a no-op tick", n)
	}

	reopened := followUpDone(t, c, customer.Id, entry.Id, false)
	if reopened.Status != http.StatusOK {
		t.Fatalf("reopen: status %d body %s, want 200", reopened.Status, reopened.Body)
	}
	var open followUpEntryJSON
	reopened.JSON(&open)
	if open.CurrentRevision != 3 || open.FollowUp == nil || open.FollowUp.DoneAt != nil {
		t.Errorf("after reopen: revision %d followUp %+v, want revision 3 and no doneAt", open.CurrentRevision, open.FollowUp)
	}
	if r := followUpDone(t, c, customer.Id, entry.Id, false); r.Status != http.StatusOK {
		t.Errorf("second reopen: status %d body %s, want 200 (idempotent)", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries_revisions WHERE customer_timeline_entry_id = $1`, entry.Id); n != 3 {
		t.Errorf("revision rows = %d, want 3", n)
	}
}

// TestFollowUpDone_RefusesWhatHasNoFollowUpAndWhatIsNotManual pins the two
// refusals design D1 names, and that they are told apart: no follow-up is a
// 404 (the thing addressed does not exist), a generated or deleted entry is the
// timeline's own 409.
func TestFollowUpDone_RefusesWhatHasNoFollowUpAndWhatIsNotManual(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Refusal Co")

	plain := createWithFollowUp(t, c, customer.Id, day(h, 0), "no follow-up here", nil)
	if r := followUpDone(t, c, customer.Id, plain.Id, true); r.Status != http.StatusNotFound {
		t.Errorf("entry with no follow-up: status %d body %s, want 404", r.Status, r.Body)
	}
	if r := followUpDone(t, c, customer.Id, 999999, true); r.Status != http.StatusNotFound {
		t.Errorf("missing entry: status %d body %s, want 404", r.Status, r.Body)
	}

	// The customer's own creation wrote a generated entry; it has no follow-up
	// and never can, so the immutability answer must win over the 404 — a
	// caller who aimed at a generated entry needs to be told that, not told the
	// path does not exist.
	generated := modtest.One[int32](t, h, `SELECT id FROM customers.customers_timeline_entries
	                                       WHERE customer_id = $1 AND provenance = 'generated' ORDER BY id LIMIT 1`, customer.Id)
	r := followUpDone(t, c, customer.Id, generated, true)
	if r.Status != http.StatusConflict {
		t.Fatalf("generated entry: status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemDetailsJSON
	r.JSON(&problem)
	if problemTitle(problem.Title) != "Timeline entry is immutable" {
		t.Errorf("title = %q, want %q", problemTitle(problem.Title), "Timeline entry is immutable")
	}

	// A soft-deleted entry that HAD a follow-up is the same answer.
	deleted := createWithFollowUp(t, c, customer.Id, day(h, 0), "about to go", map[string]any{"dueOn": day(h, 2)})
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/timeline/%d?expectedRevision=1", customer.Id, deleted.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete entry: status %d body %s, want 204", r.Status, r.Body)
	}
	if r := followUpDone(t, c, customer.Id, deleted.Id, true); r.Status != http.StatusConflict {
		t.Errorf("deleted entry: status %d body %s, want 409", r.Status, r.Body)
	}
}

// TestFollowUpDone_NeedsTimelineManage pins the permission pair, and that a
// reader who may see a follow-up may not tick it.
func TestFollowUpDone_NeedsTimelineManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	writer := authenticatedClient(t, h)
	customer := createCustomer(t, writer, "Permission Co")
	entry := createWithFollowUp(t, writer, customer.Id, day(h, 0), "Ring back", map[string]any{"dueOn": day(h, 1)})

	reader := h.SignIn(t, "customers:view", "customers:timeline-view")
	if r := followUpDone(t, reader, customer.Id, entry.Id, true); r.Status != http.StatusForbidden {
		t.Errorf("timeline-view only: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := followUpDone(t, reader, customer.Id, entry.Id, false); r.Status != http.StatusForbidden {
		t.Errorf("timeline-view only, reopen: status %d body %s, want 403", r.Status, r.Body)
	}
}

// TestTimelineRevisions_CarryTheFollowUpPerRevision is design D1's "history
// stays point-in-time", read through the endpoint rather than the table: the
// revision taken before a follow-up moved still says what it said.
func TestTimelineRevisions_CarryTheFollowUpPerRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, caller := authenticatedClientWithID(t, h)
	setDisplayName(t, h, caller, "Kari Nordmann")
	customer := createCustomer(t, c, "History Co")
	entry := createWithFollowUp(t, c, customer.Id, day(h, 0), "original", map[string]any{
		"dueOn": day(h, 4), "assigneeUserId": caller,
	})
	putEntryWithFollowUp(t, c, customer.Id, entry.Id, 1, map[string]any{"dueOn": day(h, 40)})

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline/%d/revisions", customer.Id, entry.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("revisions: status %d body %s, want 200", r.Status, r.Body)
	}
	var list struct {
		Data []struct {
			Revision int32         `json:"revision"`
			FollowUp *followUpJSON `json:"followUp"`
		} `json:"data"`
	}
	r.JSON(&list)
	if len(list.Data) != 2 {
		t.Fatalf("len(data) = %d, want 2", len(list.Data))
	}
	if list.Data[0].FollowUp == nil || list.Data[0].FollowUp.DueOn != day(h, 4) {
		t.Errorf("revision 1 followUp = %+v, want dueOn %s", list.Data[0].FollowUp, day(h, 4))
	}
	if list.Data[0].FollowUp.Assignee == nil || list.Data[0].FollowUp.Assignee.DisplayName != "Kari Nordmann" {
		t.Errorf("revision 1 assignee = %+v, want Kari Nordmann", list.Data[0].FollowUp.Assignee)
	}
	if list.Data[1].FollowUp == nil || list.Data[1].FollowUp.DueOn != day(h, 40) {
		t.Errorf("revision 2 followUp = %+v, want dueOn %s", list.Data[1].FollowUp, day(h, 40))
	}
	if list.Data[1].FollowUp.Assignee != nil {
		t.Errorf("revision 2 assignee = %+v, want absent: the replace named nobody", list.Data[1].FollowUp.Assignee)
	}
}

// TestFollowUp_AnAssigneeDisabledOrForgottenAfterwardsKeepsIt is design D1's
// last sentence about the assignee, and the owner's own precedent: nothing is
// silently revoked, and a vanished account reads as "Unknown user", inactive —
// never as a 500 and never as an unassigned follow-up.
func TestFollowUp_AnAssigneeDisabledOrForgottenAfterwardsKeepsIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Aftermath Co")
	soonDisabled := seedNamedUser(t, h, "Soon Disabledsen")
	soonGone := seedNamedUser(t, h, "Soon Gonesen")
	disabledEntry := createWithFollowUp(t, c, customer.Id, day(h, 0), "a", map[string]any{"dueOn": day(h, 1), "assigneeUserId": soonDisabled})
	goneEntry := createWithFollowUp(t, c, customer.Id, day(h, 0), "b", map[string]any{"dueOn": day(h, 1), "assigneeUserId": soonGone})

	disableUser(t, h, soonDisabled)
	forgetUser(t, h, soonGone)

	got := fetchFollowUpEntry(t, c, customer.Id, disabledEntry.Id)
	if got.FollowUp == nil || got.FollowUp.Assignee == nil {
		t.Fatalf("followUp = %+v, want an assignee", got.FollowUp)
	}
	if got.FollowUp.Assignee.DisplayName != "Soon Disabledsen" || got.FollowUp.Assignee.Active {
		t.Errorf("assignee = %+v, want Soon Disabledsen, inactive", got.FollowUp.Assignee)
	}
	vanished := fetchFollowUpEntry(t, c, customer.Id, goneEntry.Id)
	if vanished.FollowUp == nil || vanished.FollowUp.Assignee == nil {
		t.Fatalf("followUp = %+v, want an assignee", vanished.FollowUp)
	}
	if vanished.FollowUp.Assignee.DisplayName != "Unknown user" || vanished.FollowUp.Assignee.Active {
		t.Errorf("assignee = %+v, want \"Unknown user\", inactive", vanished.FollowUp.Assignee)
	}
	if vanished.FollowUp.Assignee.UserId != soonGone {
		t.Errorf("assignee userId = %s, want %s: the id is kept even when the name cannot be", vanished.FollowUp.Assignee.UserId, soonGone)
	}
}

func fetchFollowUpEntry(t *testing.T, c *modtest.Client, customerID, entryID int32) followUpEntryJSON {
	t.Helper()
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customerID, entryID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get entry: status %d body %s, want 200", r.Status, r.Body)
	}
	var entry followUpEntryJSON
	r.JSON(&entry)
	return entry
}

// TestStatsAttention_ReportsTheCallersAndUnassignedFollowUpsOnly is design D2
// in full: two types split by a UTC calendar comparison, only open follow-ups,
// only the caller's or nobody's, never an archived customer's, with the item's
// own id/entityId/title/occurredAt shape.
func TestStatsAttention_ReportsTheCallersAndUnassignedFollowUpsOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, caller := authenticatedClientWithID(t, h)
	other := seedNamedUser(t, h, "Somebody Elsesen")
	mine := createCustomer(t, c, "Mine Co")
	theirs := createCustomer(t, c, "Theirs Co")
	archived := createCustomer(t, c, "Archived Co")

	overdue := createWithFollowUp(t, c, mine.Id, day(h, -10), "overdue", map[string]any{"dueOn": day(h, -3), "assigneeUserId": caller})
	dueToday := createWithFollowUp(t, c, mine.Id, day(h, -1), "due today", map[string]any{"dueOn": day(h, 0), "assigneeUserId": caller})
	unassigned := createWithFollowUp(t, c, mine.Id, day(h, -1), "nobody's", map[string]any{"dueOn": day(h, 0)})
	createWithFollowUp(t, c, mine.Id, day(h, -1), "later", map[string]any{"dueOn": day(h, 7), "assigneeUserId": caller})
	createWithFollowUp(t, c, theirs.Id, day(h, -1), "not mine", map[string]any{"dueOn": day(h, -1), "assigneeUserId": other})
	done := createWithFollowUp(t, c, mine.Id, day(h, -1), "finished", map[string]any{"dueOn": day(h, -5), "assigneeUserId": caller})
	if r := followUpDone(t, c, mine.Id, done.Id, true); r.Status != http.StatusOK {
		t.Fatalf("mark done: status %d body %s, want 200", r.Status, r.Body)
	}
	archivedEntry := createWithFollowUp(t, c, archived.Id, day(h, -1), "archived", map[string]any{"dueOn": day(h, -2), "assigneeUserId": caller})
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", archived.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive customer: status %d body %s, want 204", r.Status, r.Body)
	}
	_ = archivedEntry

	items := getAttention(t, c)
	byID := map[string]attentionItemJSON{}
	for _, item := range items {
		byID[item.Id] = item
	}

	wantOverdue := fmt.Sprintf("followUpOverdue/%d", overdue.Id)
	got, ok := byID[wantOverdue]
	if !ok {
		t.Fatalf("items = %+v, want one with id %q", items, wantOverdue)
	}
	if got.Type != "followUpOverdue" || got.Title != "Mine Co" || got.EntityId != fmt.Sprintf("%d", mine.Id) {
		t.Errorf("item = %+v, want type followUpOverdue, title \"Mine Co\", entityId %d", got, mine.Id)
	}
	// occurredAt is the DUE DATE at midnight UTC, not the moment the follow-up
	// was written: an overdue follow-up has to sort by how overdue it is.
	wantAt, err := time.Parse("2006-01-02", day(h, -3))
	if err != nil {
		t.Fatalf("parse want: %v", err)
	}
	if !got.OccurredAt.Equal(wantAt) {
		t.Errorf("occurredAt = %s, want %s (dueOn at midnight UTC)", got.OccurredAt, wantAt)
	}

	for _, id := range []string{
		fmt.Sprintf("followUpDue/%d", dueToday.Id),
		fmt.Sprintf("followUpDue/%d", unassigned.Id),
	} {
		if item, ok := byID[id]; !ok {
			t.Errorf("items = %+v, want one with id %q", items, id)
		} else if item.Type != "followUpDue" {
			t.Errorf("%s type = %q, want followUpDue", id, item.Type)
		}
	}
	for _, kind := range []string{"followUpOverdue", "followUpDue"} {
		for _, entry := range []int32{done.Id, archivedEntry.Id} {
			if _, ok := byID[fmt.Sprintf("%s/%d", kind, entry)]; ok {
				t.Errorf("items = %+v, want no %s for entry %d (done, or an archived customer's)", items, kind, entry)
			}
		}
	}
	// Another user's follow-up, and one not yet due, are nobody's business here.
	if len(items) != 3 {
		t.Errorf("len(items) = %d, want exactly 3: overdue, due today, unassigned", len(items))
	}

	// And the same list asked by somebody else — who may read the timeline —
	// answers only the unassigned one, because the rest are not theirs.
	stranger, _ := h.SignInUser(t, "customers:view", "customers:timeline-view")
	strangerItems := getAttention(t, stranger)
	if len(strangerItems) != 1 || strangerItems[0].Id != fmt.Sprintf("followUpDue/%d", unassigned.Id) {
		t.Errorf("stranger's items = %+v, want only the unassigned follow-up", strangerItems)
	}

	// A caller who may see customers but NOT the timeline is answered no
	// follow-up items at all: this endpoint admits customers:view, a follow-up
	// item names a timeline entry, and customers:timeline-view is what it takes
	// to see one — so the dashboard is not a way around that door. Not a 403:
	// the endpoint still answers, shaped, exactly as /stats omits its
	// identity-derived figures.
	outsider, _ := h.SignInUser(t, "customers:view")
	for _, item := range getAttention(t, outsider) {
		if strings.HasPrefix(item.Type, "followUp") {
			t.Errorf("customers:view only: item %+v, want no follow-up items at all", item)
		}
	}
}

// TestGetFollowUps_DefaultsToMyOpenOnes is design D3's defaults, its ordering
// and its row shape, all read off one request.
func TestGetFollowUps_DefaultsToMyOpenOnes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, caller := authenticatedClientWithID(t, h)
	setDisplayName(t, h, caller, "Kari Nordmann")
	other := seedNamedUser(t, h, "Somebody Elsesen")
	alpha := createCustomer(t, c, "Alpha Co")
	beta := createCustomer(t, c, "Beta Co")

	late := createWithFollowUp(t, c, alpha.Id, day(h, -9), "ring Alpha", map[string]any{"dueOn": day(h, -2), "assigneeUserId": caller})
	soon := createWithFollowUp(t, c, beta.Id, day(h, -1), "ring Beta", map[string]any{"dueOn": day(h, 5), "assigneeUserId": caller})
	createWithFollowUp(t, c, beta.Id, day(h, -1), "not mine", map[string]any{"dueOn": day(h, 1), "assigneeUserId": other})
	createWithFollowUp(t, c, beta.Id, day(h, -1), "nobody's", map[string]any{"dueOn": day(h, 1)})

	list := listFollowUps(t, c, nil)
	if len(list.Data) != 2 {
		t.Fatalf("data = %+v, want 2 rows (mine, open)", list.Data)
	}
	if list.Data[0].EntryId != late.Id || list.Data[1].EntryId != soon.Id {
		t.Errorf("order = %d, %d, want %d, %d (dueOn ascending)", list.Data[0].EntryId, list.Data[1].EntryId, late.Id, soon.Id)
	}
	row := list.Data[0]
	if row.CustomerId != alpha.Id || row.CustomerName != "Alpha Co" || row.EventType != "note" {
		t.Errorf("row = %+v, want Alpha Co's note", row)
	}
	if row.Note == nil || *row.Note != "ring Alpha" {
		t.Errorf("note = %v, want \"ring Alpha\"", row.Note)
	}
	if row.OccurredOn != day(h, -9) {
		t.Errorf("occurredOn = %q, want %q", row.OccurredOn, day(h, -9))
	}
	if row.FollowUp.Assignee == nil || row.FollowUp.Assignee.DisplayName != "Kari Nordmann" {
		t.Errorf("assignee = %+v, want Kari Nordmann", row.FollowUp.Assignee)
	}
	if list.Pagination.TotalCount != 2 || list.Pagination.Page != 1 || list.Pagination.PageSize != 25 {
		t.Errorf("pagination = %+v, want page 1, pageSize 25, totalCount 2", list.Pagination)
	}
}

// TestGetFollowUps_EveryFilterAndThePageBoundary walks design D3's filters one
// at a time, plus the archived rule and the note's 200-unit cut.
func TestGetFollowUps_EveryFilterAndThePageBoundary(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, caller := authenticatedClientWithID(t, h)
	other := seedNamedUser(t, h, "Somebody Elsesen")
	alpha := createCustomer(t, c, "Alpha Co")
	beta := createCustomer(t, c, "Beta Co")
	archived := createCustomer(t, c, "Archived Co")

	overdue := createWithFollowUp(t, c, alpha.Id, day(h, -9), "overdue", map[string]any{"dueOn": day(h, -2), "assigneeUserId": caller})
	future := createWithFollowUp(t, c, alpha.Id, day(h, -1), "future", map[string]any{"dueOn": day(h, 5), "assigneeUserId": caller})
	unassigned := createWithFollowUp(t, c, beta.Id, day(h, -1), "nobody's", map[string]any{"dueOn": day(h, 1)})
	theirs := createWithFollowUp(t, c, beta.Id, day(h, -1), "theirs", map[string]any{"dueOn": day(h, 1), "assigneeUserId": other})
	done := createWithFollowUp(t, c, alpha.Id, day(h, -1), "done", map[string]any{"dueOn": day(h, -4), "assigneeUserId": caller})
	if r := followUpDone(t, c, alpha.Id, done.Id, true); r.Status != http.StatusOK {
		t.Fatalf("mark done: status %d body %s, want 200", r.Status, r.Body)
	}
	archivedOpen := createWithFollowUp(t, c, archived.Id, day(h, -1), "archived open", map[string]any{"dueOn": day(h, 1), "assigneeUserId": caller})
	archivedDone := createWithFollowUp(t, c, archived.Id, day(h, -1), "archived done", map[string]any{"dueOn": day(h, 1), "assigneeUserId": caller})
	if r := followUpDone(t, c, archived.Id, archivedDone.Id, true); r.Status != http.StatusOK {
		t.Fatalf("mark archived done: status %d body %s, want 200", r.Status, r.Body)
	}
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", archived.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive customer: status %d body %s, want 204", r.Status, r.Body)
	}

	ids := func(list followUpListJSON) []int32 {
		out := make([]int32, 0, len(list.Data))
		for _, row := range list.Data {
			out = append(out, row.EntryId)
		}
		return out
	}
	equal := func(got, want []int32) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	cases := []struct {
		name  string
		query url.Values
		want  []int32
	}{
		{"state=open, the default, spelled out", url.Values{"state": {"open"}}, []int32{overdue.Id, future.Id}},
		{"state=overdue is a subset of open", url.Values{"state": {"overdue"}}, []int32{overdue.Id}},
		{"state=done, where an archived customer's follow-up survives", url.Values{"state": {"done"}}, []int32{done.Id, archivedDone.Id}},
		{"state=all, where an archived customer's OPEN one does not", url.Values{"state": {"all"}}, []int32{done.Id, overdue.Id, future.Id}},
		{"assignee=none", url.Values{"assignee": {"none"}}, []int32{unassigned.Id}},
		{"assignee=<uuid>", url.Values{"assignee": {other.String()}}, []int32{theirs.Id}},
		{"customerId narrows to one customer", url.Values{"customerId": {fmt.Sprint(alpha.Id)}}, []int32{overdue.Id, future.Id}},
		{"customerId and state together", url.Values{"customerId": {fmt.Sprint(alpha.Id)}, "state": {"done"}}, []int32{done.Id}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ids(listFollowUps(t, c, tc.query)); !equal(got, tc.want) {
				t.Errorf("entry ids = %v, want %v", got, tc.want)
			}
		})
	}
	// archivedOpen is created and never expected anywhere: state=open and
	// state=all both exclude it, which the two cases above assert by its
	// absence.
	_ = archivedOpen

	// Paging: one row per page, and the metadata that lets a control render.
	first := listFollowUps(t, c, url.Values{"state": {"all"}, "pageSize": {"1"}})
	if len(first.Data) != 1 || first.Pagination.TotalCount != 3 || first.Pagination.TotalPages != 3 {
		t.Errorf("page 1 = %+v / %+v, want 1 row of 3 across 3 pages", first.Data, first.Pagination)
	}
	second := listFollowUps(t, c, url.Values{"state": {"all"}, "pageSize": {"1"}, "page": {"2"}})
	if len(second.Data) != 1 || second.Data[0].EntryId == first.Data[0].EntryId {
		t.Errorf("page 2 = %+v, want a different single row from page 1 (%d)", second.Data, first.Data[0].EntryId)
	}
}

// TestGetFollowUps_CutsTheNoteAtTwoHundredUTF16Units is design D3's "first 200
// UTF-16 units", in the unit the design names — the same unit the entry's own
// 500-character summary is cut in, which a byte or rune count would get wrong
// for exactly the input below.
func TestGetFollowUps_CutsTheNoteAtTwoHundredUTF16Units(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, caller := authenticatedClientWithID(t, h)
	customer := createCustomer(t, c, "Long Note Co")
	// 150 astral characters: 150 runes, 300 UTF-16 code units, 600 bytes. A cut
	// at 200 units keeps 100 of them.
	note := strings.Repeat("𝄞", 150)
	createWithFollowUp(t, c, customer.Id, day(h, 0), note, map[string]any{"dueOn": day(h, 1), "assigneeUserId": caller})

	list := listFollowUps(t, c, nil)
	if len(list.Data) != 1 || list.Data[0].Note == nil {
		t.Fatalf("data = %+v, want one row with a note", list.Data)
	}
	if got := utf16.Encode([]rune(*list.Data[0].Note)); len(got) != 200 {
		t.Errorf("note length = %d UTF-16 units, want 200", len(got))
	}
	if want := strings.Repeat("𝄞", 100); *list.Data[0].Note != want {
		t.Errorf("note = %q, want the first 100 characters", *list.Data[0].Note)
	}
}

// TestGetFollowUps_RefusesAnUnusableQuery pins the parameter messages, which
// are query-parameter messages and therefore carry a trailing period and are
// joined into one detail — the list endpoint's own shape, not a field map.
func TestGetFollowUps_RefusesAnUnusableQuery(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	cases := []struct {
		query string
		want  string
	}{
		{"page=0", "'page' must be 1 or greater, but was 0."},
		{"pageSize=101", "'pageSize' must be between 1 and 100, but was 101."},
		{"assignee=Me", "'assignee' must be a user id, 'me' or 'none', but was 'Me'."},
		{"state=OPEN", "'state' must be one of 'open', 'overdue', 'done' or 'all', but was 'OPEN'."},
		{"customerId=0", "'customerId' must be 1 or greater, but was 0."},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			r := c.Do(http.MethodGet, "/api/v1/customers/follow-ups?"+tc.query, nil)
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var problem problemDetailsJSON
			r.JSON(&problem)
			if problem.Detail == nil || *problem.Detail != tc.want {
				t.Errorf("detail = %v, want %q", problem.Detail, tc.want)
			}
		})
	}
}

// TestGetFollowUps_NeedsBothDoors pins the access pair: the rows are timeline
// data and each names a customer, so customers:timeline-view alone is not
// enough and neither is customers:view.
func TestGetFollowUps_NeedsBothDoors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, permissions := range [][]string{{"customers:view"}, {"customers:timeline-view"}} {
		c := h.SignIn(t, permissions...)
		if r := c.Do(http.MethodGet, "/api/v1/customers/follow-ups", nil); r.Status != http.StatusForbidden {
			t.Errorf("%v: status %d body %s, want 403", permissions, r.Status, r.Body)
		}
	}
	both := h.SignIn(t, "customers:view", "customers:timeline-view")
	if r := both.Do(http.MethodGet, "/api/v1/customers/follow-ups", nil); r.Status != http.StatusOK {
		t.Errorf("both: status %d body %s, want 200", r.Status, r.Body)
	}
}
