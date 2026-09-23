package customers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file ports Integration/TimelineEndpointsTests.cs (customers inventory
// §7): all 10 tests. It also adds validation-edge and guard-specific tests
// that assert byte-exact .NET message text, per this task's brief: every
// prior task in this module shipped correct code with toothless tests, so
// the .NET wording (not just the status code) is what a mutation here must
// break to be caught.

type timelineEntryJSON struct {
	Id              int32           `json:"id"`
	EventType       string          `json:"eventType"`
	Provenance      string          `json:"provenance"`
	Producer        string          `json:"producer"`
	OccurredOn      string          `json:"occurredOn"`
	OccurredAt      *time.Time      `json:"occurredAt"`
	Summary         *string         `json:"summary"`
	Note            *string         `json:"note"`
	SourceUrl       *string         `json:"sourceUrl"`
	Payload         json.RawMessage `json:"payload"`
	CurrentRevision int32           `json:"currentRevision"`
	State           string          `json:"state"`
	ActorKind       string          `json:"actorKind"`
	ActorDisplay    *string         `json:"actorDisplay"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

type timelineListJSON struct {
	Data       []timelineEntryJSON `json:"data"`
	NextCursor *string             `json:"nextCursor"`
}

type timelineRevisionJSON struct {
	Revision         int32      `json:"revision"`
	EventType        string     `json:"eventType"`
	Action           string     `json:"action"`
	ChangedAt        time.Time  `json:"changedAt"`
	Provenance       string     `json:"provenance"`
	State            string     `json:"state"`
	ActorDisplayName string     `json:"actorDisplayName"`
	DeletedAt        *time.Time `json:"deletedAt"`
}

type timelineRevisionListJSON struct {
	Data []timelineRevisionJSON `json:"data"`
}

type problemDetailsJSON struct {
	Title  *string `json:"title"`
	Detail *string `json:"detail"`
}

func problemTitle(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// createManualEntry posts body to the customer's timeline and returns the
// created entry, failing the test on anything but 201.
func createManualEntry(t *testing.T, c *modtest.Client, customerID int32, body map[string]any) timelineEntryJSON {
	t.Helper()
	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/timeline", customerID), body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create timeline entry: status %d body %s, want 201", r.Status, r.Body)
	}
	var entry timelineEntryJSON
	r.JSON(&entry)
	return entry
}

func createManual(t *testing.T, c *modtest.Client, customerID int32, occurredOn, note string, occurredAt ...string) timelineEntryJSON {
	t.Helper()
	body := map[string]any{"eventType": "note", "occurredOn": occurredOn, "note": note}
	if len(occurredAt) > 0 {
		body["occurredAt"] = occurredAt[0]
	}
	return createManualEntry(t, c, customerID, body)
}

// TestPutTimelineEntry_InvalidBodyAgainstMissingEntry_Returns400 pins
// TimelineEndpoints.Update's order (timeline.go validates the manual entry
// before it loads the row): a body that is both invalid and aimed at a
// missing entry id answers 400, never the 404 the lookup would produce if the
// two steps were swapped. Not a .NET port — no test in
// TimelineEndpointsTests.cs combines an invalid body with a missing entry.
func TestPutTimelineEntry_InvalidBodyAgainstMissingEntry_Returns400(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Ordering Co")

	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/timeline/999999", customer.Id), map[string]any{
		"eventType": "note", "occurredOn": "2026-07-27", "note": "", "expectedRevision": 1,
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400 (validation must run before the lookup)", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if _, ok := problem.Errors["note"]; !ok {
		t.Errorf("errors = %v, want a key \"note\"", problem.Errors)
	}
}

// Ported from Integration/TimelineEndpointsTests.cs.
// ManualTimelineEntry_CanBeEditedAndSoftDeletedWithHistory.
func TestManualTimelineEntry_CanBeEditedAndSoftDeletedWithHistory(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	wantDisplay := userDisplayName(t, h, userID)
	customer := createCustomer(t, c, "Timeline Entry Co")

	created := createManualEntry(t, c, customer.Id, map[string]any{
		"eventType": "note", "occurredOn": "2026-07-27", "note": "First note", "sourceUrl": "https://example.test/source",
	})
	if created.ActorKind != "user" {
		t.Errorf("ActorKind = %q, want user", created.ActorKind)
	}
	if str(created.ActorDisplay) != wantDisplay {
		t.Errorf("ActorDisplay = %q, want %q (the signed-in caller)", str(created.ActorDisplay), wantDisplay)
	}
	if created.CurrentRevision != 1 {
		t.Errorf("CurrentRevision = %d, want 1", created.CurrentRevision)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Errorf("CreatedAt/UpdatedAt zero, want set")
	}

	update := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customer.Id, created.Id), map[string]any{
		"eventType": "interaction.call", "occurredOn": "2026-07-27", "occurredAt": "2026-07-27T12:00:00+02:00",
		"note": "Updated note", "expectedRevision": 1,
	})
	if update.Status != http.StatusOK {
		t.Fatalf("update: status %d body %s, want 200", update.Status, update.Body)
	}
	var updated timelineEntryJSON
	update.JSON(&updated)
	if updated.CurrentRevision != 2 {
		t.Errorf("CurrentRevision = %d, want 2", updated.CurrentRevision)
	}
	wantAt := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	if updated.OccurredAt == nil || !updated.OccurredAt.Equal(wantAt) {
		t.Errorf("OccurredAt = %v, want %v", updated.OccurredAt, wantAt)
	}

	detail := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customer.Id, created.Id), nil)
	if detail.Status != http.StatusOK {
		t.Fatalf("get: status %d body %s, want 200", detail.Status, detail.Body)
	}
	var got timelineEntryJSON
	detail.JSON(&got)
	if got.EventType != "interaction.call" || got.Provenance != "manual" {
		t.Errorf("EventType/Provenance = %q/%q, want interaction.call/manual", got.EventType, got.Provenance)
	}
	if str(got.Note) != "Updated note" || str(got.Summary) != "Updated note" {
		t.Errorf("Note/Summary = %q/%q, want \"Updated note\" both", str(got.Note), str(got.Summary))
	}
	if got.ActorKind != "user" || got.CurrentRevision != 2 {
		t.Errorf("ActorKind/CurrentRevision = %q/%d, want user/2", got.ActorKind, got.CurrentRevision)
	}
	if got.OccurredAt == nil || !got.OccurredAt.Equal(wantAt) {
		t.Errorf("OccurredAt = %v, want %v", got.OccurredAt, wantAt)
	}
	if string(got.Payload) != "null" && len(got.Payload) != 0 {
		t.Errorf("Payload = %s, want null or absent", got.Payload)
	}

	stale := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customer.Id, created.Id), map[string]any{
		"eventType": "note", "occurredOn": "2026-07-27", "note": "stale", "expectedRevision": 1,
	})
	if stale.Status != http.StatusConflict {
		t.Fatalf("stale update: status %d body %s, want 409", stale.Status, stale.Body)
	}
	if ct := stale.Header("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	var staleProblem problemDetailsJSON
	stale.JSON(&staleProblem)
	if problemTitle(staleProblem.Title) != "Timeline revision conflict" {
		t.Errorf("Title = %q, want %q", problemTitle(staleProblem.Title), "Timeline revision conflict")
	}
	wantDetail := "The timeline entry has revision 2; the supplied expectedRevision was 1."
	if problemTitle(staleProblem.Detail) != wantDetail {
		t.Errorf("Detail = %q, want %q", problemTitle(staleProblem.Detail), wantDetail)
	}

	deleted := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/timeline/%d?expectedRevision=2", customer.Id, created.Id), nil)
	if deleted.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s, want 204", deleted.Status, deleted.Body)
	}

	feed := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline", customer.Id), nil)
	var feedList timelineListJSON
	feed.JSON(&feedList)
	for _, e := range feedList.Data {
		if e.Id == created.Id {
			t.Errorf("deleted entry %d still present in feed", created.Id)
		}
	}

	revisions := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline/%d/revisions", customer.Id, created.Id), nil)
	var revList timelineRevisionListJSON
	revisions.JSON(&revList)
	if len(revList.Data) != 3 {
		t.Fatalf("revisions count = %d, want 3", len(revList.Data))
	}
	wantRevisions := []int32{1, 2, 3}
	wantActions := []string{"create", "update", "delete"}
	for i, rev := range revList.Data {
		if rev.Revision != wantRevisions[i] {
			t.Errorf("revision[%d].Revision = %d, want %d", i, rev.Revision, wantRevisions[i])
		}
		if rev.Action != wantActions[i] {
			t.Errorf("revision[%d].Action = %q, want %q", i, rev.Action, wantActions[i])
		}
		if rev.ActorDisplayName != wantDisplay {
			t.Errorf("revision[%d].ActorDisplayName = %q, want %q (the one caller who created, updated and deleted this entry)", i, rev.ActorDisplayName, wantDisplay)
		}
		if rev.ChangedAt.IsZero() {
			t.Errorf("revision[%d].ChangedAt zero, want set", i)
		}
	}
	if revList.Data[len(revList.Data)-1].State != "deleted" {
		t.Errorf("last revision State = %q, want deleted", revList.Data[len(revList.Data)-1].State)
	}
}

// TestPostTimelineEntry_PersistsTheActorUserID proves a manual entry's
// actor_user_id column, not just its response actorKind/actorDisplay, names
// the caller who wrote it (customers foundation design D1) — the response
// alone cannot tell a real per-user id apart from a coincidentally matching
// display name.
func TestPostTimelineEntry_PersistsTheActorUserID(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	customer := createCustomer(t, c, "Timeline Actor Persistence Co")

	created := createManual(t, c, customer.Id, "2026-07-27", "note")

	got := modtest.One[string](t, h, `SELECT actor_user_id::text FROM customers.customers_timeline_entries WHERE id = $1`, created.Id)
	if got != userID.String() {
		t.Errorf("actor_user_id = %s, want %s (the signed-in caller)", got, userID)
	}
}

// TestTimelineEntry_RevisionsAttributeEachWriterSeparately proves D1's
// second half: the entry row keeps its original author, but each revision
// carries the actor of *that* revision — an edit or delete by somebody else
// than the entry's creator shows up in the history under their own name.
func TestTimelineEntry_RevisionsAttributeEachWriterSeparately(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	author, authorID := authenticatedClientWithID(t, h)
	authorName := userDisplayName(t, h, authorID)
	customer := createCustomer(t, author, "Timeline Multi Writer Co")
	created := createManual(t, author, customer.Id, "2026-07-27", "written by author")

	editor, editorID := authenticatedClientWithID(t, h)
	editorName := userDisplayName(t, h, editorID)

	update := editor.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customer.Id, created.Id), map[string]any{
		"eventType": "note", "occurredOn": "2026-07-27", "note": "edited by editor", "expectedRevision": 1,
	})
	if update.Status != http.StatusOK {
		t.Fatalf("update by second user: status %d body %s, want 200", update.Status, update.Body)
	}
	var updated timelineEntryJSON
	update.JSON(&updated)
	// The entry itself keeps its original author, unaffected by who edited it.
	if str(updated.ActorDisplay) != authorName {
		t.Errorf("entry ActorDisplay after update = %q, want %q (the original author, unchanged)", str(updated.ActorDisplay), authorName)
	}

	deleted := editor.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/timeline/%d?expectedRevision=2", customer.Id, created.Id), nil)
	if deleted.Status != http.StatusNoContent {
		t.Fatalf("delete by second user: status %d body %s, want 204", deleted.Status, deleted.Body)
	}

	revisions := author.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline/%d/revisions", customer.Id, created.Id), nil)
	var revList timelineRevisionListJSON
	revisions.JSON(&revList)
	if len(revList.Data) != 3 {
		t.Fatalf("revisions count = %d, want 3", len(revList.Data))
	}
	wantActors := []string{authorName, editorName, editorName}
	for i, rev := range revList.Data {
		if rev.ActorDisplayName != wantActors[i] {
			t.Errorf("revision[%d].ActorDisplayName = %q, want %q", i, rev.ActorDisplayName, wantActors[i])
		}
	}
}

// TestGeneratedTimelineEvents_CarryTheActingUser proves every generated
// customer.* event names the signed-in caller whose action produced it
// (customers foundation design D1), while provenance/producer stay exactly
// what they were before D1 — generated, customers.api.
func TestGeneratedTimelineEvents_CarryTheActingUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	wantDisplay := userDisplayName(t, h, userID)

	customer := createCustomer(t, c, "Timeline Generated Actor Co")
	contact := createContact(t, c, map[string]any{"firstName": "Generated", "lastName": "Actor"})
	if r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", customer.Id), map[string]any{"name": "Timeline Generated Actor Co Renamed"}); r.Status != http.StatusOK {
		t.Fatalf("rename customer: status %d body %s, want 200", r.Status, r.Body)
	}
	attachContact(t, c, customer.Id, contact.Id, "CEO")
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", customer.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive customer: status %d body %s, want 204", r.Status, r.Body)
	}

	feed := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?limit=100", customer.Id), nil)
	var feedList timelineListJSON
	feed.JSON(&feedList)

	want := []string{"customer.created", "customer.updated", "customer.contact_attached", "customer.status_changed"}
	seen := map[string]bool{}
	for _, e := range feedList.Data {
		if e.Provenance != "generated" {
			continue
		}
		seen[e.EventType] = true
		if e.Producer != "customers.api" {
			t.Errorf("%s: Producer = %q, want customers.api", e.EventType, e.Producer)
		}
		if e.ActorKind != "user" {
			t.Errorf("%s: ActorKind = %q, want user", e.EventType, e.ActorKind)
		}
		if str(e.ActorDisplay) != wantDisplay {
			t.Errorf("%s: ActorDisplay = %q, want %q", e.EventType, str(e.ActorDisplay), wantDisplay)
		}
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("feed missing generated event type %q, got %v", w, feedList.Data)
		}
	}
}

// Ported from Integration/TimelineEndpointsTests.cs.
// ManualTimelineEntry_RejectsInvalidAndFutureInput.
func TestManualTimelineEntry_RejectsInvalidAndFutureInput(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Validation Co")

	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/timeline", customer.Id), map[string]any{
		"eventType": "not.allowed", "occurredOn": "2999-01-01", "note": " ", "sourceUrl": "ftp://example.test/file",
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	for _, key := range []string{"eventType", "occurredOn", "note", "sourceUrl"} {
		if _, ok := problem.Errors[key]; !ok {
			t.Errorf("errors missing key %q, got %v", key, problem.Errors)
		}
	}
	wantEventType := "EventType must be one of: registry.change, interaction.call, interaction.meeting, interaction.email, note, other"
	if got := problem.Errors["eventType"]; len(got) != 1 || got[0] != wantEventType {
		t.Errorf("errors[eventType] = %v, want [%q]", got, wantEventType)
	}
	if got := problem.Errors["occurredOn"]; len(got) != 1 || got[0] != "An occurrence date cannot be in the future" {
		t.Errorf("errors[occurredOn] = %v, want future-date message", got)
	}
	if got := problem.Errors["note"]; len(got) != 1 || got[0] != "A nonblank note or description is required" {
		t.Errorf("errors[note] = %v, want nonblank message", got)
	}
	if got := problem.Errors["sourceUrl"]; len(got) != 1 || got[0] != "SourceUrl must be an absolute http(s) URL" {
		t.Errorf("errors[sourceUrl] = %v, want absolute-URL message", got)
	}

	mismatched := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/timeline", customer.Id), map[string]any{
		"eventType": "note", "occurredOn": "2026-07-27", "occurredAt": "2026-07-28T00:00:00Z", "note": "UTC date mismatch",
	})
	if mismatched.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", mismatched.Status, mismatched.Body)
	}
	var mismatchProblem validationProblemJSON
	mismatched.JSON(&mismatchProblem)
	if got := mismatchProblem.Errors["occurredAt"]; len(got) != 1 || got[0] != "OccurredAt must have the same UTC calendar date as occurredOn" {
		t.Errorf("errors[occurredAt] = %v, want calendar-date mismatch message", got)
	}
}

// Ported from Integration/TimelineEndpointsTests.cs.
// Timeline_UsesOpaqueCursorAndStableNewestFirstOrder.
func TestTimeline_UsesOpaqueCursorAndStableNewestFirstOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Cursor Co")
	createManual(t, c, customer.Id, "2026-07-26", "old")
	createManual(t, c, customer.Id, "2026-07-28", "newest")
	createManual(t, c, customer.Id, "2026-07-27", "middle")

	first := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?limit=2", customer.Id), nil)
	var firstList timelineListJSON
	first.JSON(&firstList)
	found := false
	for _, e := range firstList.Data {
		if str(e.Note) == "newest" {
			found = true
		}
	}
	if !found {
		t.Errorf("first page missing %q", "newest")
	}
	if firstList.NextCursor == nil || *firstList.NextCursor == "" {
		t.Fatalf("NextCursor empty, want set")
	}
	if contains(*firstList.NextCursor, "2026-07-27") {
		t.Errorf("NextCursor %q leaks the date, want opaque", *firstList.NextCursor)
	}

	second := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?limit=2&cursor=%s", customer.Id, url.QueryEscape(*firstList.NextCursor)), nil)
	var secondList timelineListJSON
	second.JSON(&secondList)
	var notes []string
	for _, e := range secondList.Data {
		if e.Note != nil {
			notes = append(notes, *e.Note)
		}
	}
	if len(notes) != 2 || notes[0] != "middle" || notes[1] != "old" {
		t.Errorf("second page notes = %v, want [middle old]", notes)
	}
	if secondList.NextCursor != nil {
		t.Errorf("NextCursor = %v, want nil", secondList.NextCursor)
	}

	malformed := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?cursor=bad", customer.Id), nil)
	if malformed.Status != http.StatusBadRequest {
		t.Errorf("malformed cursor: status %d, want 400", malformed.Status)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

// Ported from Integration/TimelineEndpointsTests.cs.
// Timeline_CursorPreservesMixedOccurredAtBoundary.
func TestTimeline_CursorPreservesMixedOccurredAtBoundary(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Boundary Co")
	createManual(t, c, customer.Id, "2026-07-27", "date-only")
	createManual(t, c, customer.Id, "2026-07-27", "with-time", "2026-07-27T09:00:00Z")

	first := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?eventType=note&limit=1", customer.Id), nil)
	var firstList timelineListJSON
	first.JSON(&firstList)
	if len(firstList.Data) != 1 || str(firstList.Data[0].Note) != "with-time" {
		t.Fatalf("first page = %+v, want [with-time]", firstList.Data)
	}
	second := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?eventType=note&limit=1&cursor=%s", customer.Id, url.QueryEscape(*firstList.NextCursor)), nil)
	var secondList timelineListJSON
	second.JSON(&secondList)
	if len(secondList.Data) != 1 || str(secondList.Data[0].Note) != "date-only" {
		t.Fatalf("second page = %+v, want [date-only]", secondList.Data)
	}
}

// Ported from Integration/TimelineEndpointsTests.cs.
// GeneratedTimelineEvents_CoverCustomerAndContactAssociationLifecycle.
func TestGeneratedTimelineEvents_CoverCustomerAndContactAssociationLifecycle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Generated Co")
	contact := createContact(t, c, map[string]any{"firstName": "Timeline", "lastName": "Generated"})
	c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", customer.Id), map[string]any{"name": "Timeline updated"})
	c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customer.Id), map[string]any{"contactId": contact.Id, "title": "CEO"})
	c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/contacts/%d", customer.Id, contact.Id), map[string]any{"title": "CTO"})
	c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/contacts/%d", customer.Id, contact.Id), nil)

	feed := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?limit=100", customer.Id), nil)
	var feedList timelineListJSON
	feed.JSON(&feedList)

	want := []string{"customer.created", "customer.updated", "customer.contact_attached", "customer.contact_relationship_updated", "customer.contact_detached"}
	seen := map[string]bool{}
	var attached *timelineEntryJSON
	for i, e := range feedList.Data {
		seen[e.EventType] = true
		if e.EventType == "customer.contact_attached" {
			attached = &feedList.Data[i]
		}
	}
	for _, w := range want {
		if !seen[w] {
			t.Errorf("feed missing event type %q, got %v", w, feedList.Data)
		}
	}
	if attached == nil {
		t.Fatalf("no customer.contact_attached event found")
	}
	payloadStr := string(attached.Payload)
	if !contains(payloadStr, fmt.Sprintf(`"contactId":%d`, contact.Id)) {
		t.Errorf("attached payload %s missing contactId %d", payloadStr, contact.Id)
	}
	if !contains(payloadStr, "displayName") {
		t.Errorf("attached payload %s missing displayName", payloadStr)
	}
	if !contains(str(attached.Summary), "Contact linked") {
		t.Errorf("attached summary %q missing %q", str(attached.Summary), "Contact linked")
	}

	immutable := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customer.Id, attached.Id), map[string]any{
		"eventType": "note", "occurredOn": "2026-07-27", "note": "cannot edit", "expectedRevision": 1,
	})
	if immutable.Status != http.StatusConflict {
		t.Fatalf("edit generated entry: status %d body %s, want 409", immutable.Status, immutable.Body)
	}
	if ct := immutable.Header("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	var immutableProblem problemDetailsJSON
	immutable.JSON(&immutableProblem)
	if problemTitle(immutableProblem.Title) != "Timeline entry is immutable" {
		t.Errorf("Title = %q, want %q", problemTitle(immutableProblem.Title), "Timeline entry is immutable")
	}
	if problemTitle(immutableProblem.Detail) != "Generated, deleted, or voided timeline entries cannot be edited." {
		t.Errorf("Detail = %q, want the immutable-edit message", problemTitle(immutableProblem.Detail))
	}
}

// Ported from Integration/TimelineEndpointsTests.cs.
// CustomerUpdatesDescribeLegalIdentityChanges.
func TestCustomerUpdatesDescribeLegalIdentityChanges(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Legal Co")

	updateCustomerIdentity := func(identity map[string]any) {
		r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", customer.Id), map[string]any{"name": "Timeline", "identity": identity})
		if r.Status != http.StatusOK {
			t.Fatalf("update customer identity: status %d body %s, want 200", r.Status, r.Body)
		}
	}
	updateCustomerIdentity(map[string]any{"country": "no", "type": "business", "id": "123456785", "name": "Legal AS", "source": "manual"})
	updateCustomerIdentity(map[string]any{"country": "no", "type": "business", "id": "987654325", "name": "New Legal AS", "source": "brreg"})
	c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", customer.Id), map[string]any{"name": "Timeline final"})
	c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/legal-identity", customer.Id), nil)

	feed := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?eventType=customer.updated&limit=100", customer.Id), nil)
	var feedList timelineListJSON
	feed.JSON(&feedList)

	var sawAdded, sawUpdated, sawRemoved bool
	for _, e := range feedList.Data {
		summary := str(e.Summary)
		switch {
		case contains(summary, "legal identity added"):
			sawAdded = true
		case contains(summary, "legal identity updated"):
			sawUpdated = true
		case contains(summary, "legal identity removed"):
			sawRemoved = true
		}
		if contains(summary, "legal identity") && !contains(string(e.Payload), "legalIdentity") {
			t.Errorf("entry %q payload %s missing legalIdentity", summary, e.Payload)
		}
	}
	if !sawAdded || !sawUpdated || !sawRemoved {
		t.Errorf("added/updated/removed = %v/%v/%v, want all true", sawAdded, sawUpdated, sawRemoved)
	}
}

// Ported from Integration/TimelineEndpointsTests.cs.
// TimelineFiltersApplyBeforeKeysetPagination.
func TestTimelineFiltersApplyBeforeKeysetPagination(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Filter Co")
	createManual(t, c, customer.Id, "2026-07-25", "manual old")
	createManual(t, c, customer.Id, "2026-07-26", "manual middle")
	createManual(t, c, customer.Id, "2026-07-27", "manual new")

	q := fmt.Sprintf("/api/v1/customers/%d/timeline?provenance=manual&eventType=note&occurredFrom=2026-07-26&occurredTo=2026-07-27&limit=1", customer.Id)
	first := c.Do(http.MethodGet, q, nil)
	var firstList timelineListJSON
	first.JSON(&firstList)
	if len(firstList.Data) != 1 || str(firstList.Data[0].Note) != "manual new" {
		t.Fatalf("first page = %+v, want [manual new]", firstList.Data)
	}
	if firstList.NextCursor == nil {
		t.Fatalf("NextCursor nil, want set")
	}

	second := c.Do(http.MethodGet, q+"&cursor="+url.QueryEscape(*firstList.NextCursor), nil)
	var secondList timelineListJSON
	second.JSON(&secondList)
	if len(secondList.Data) != 1 || str(secondList.Data[0].Note) != "manual middle" {
		t.Fatalf("second page = %+v, want [manual middle]", secondList.Data)
	}

	generated := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?provenance=generated&eventType=customer.created&limit=10", customer.Id), nil)
	var generatedList timelineListJSON
	generated.JSON(&generatedList)
	for _, e := range generatedList.Data {
		if e.Provenance != "generated" {
			t.Errorf("entry Provenance = %q, want generated", e.Provenance)
		}
	}

	invalid := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?provenance=bogus&eventType=%%20&occurredFrom=2026-07-28&occurredTo=2026-07-27", customer.Id), nil)
	if invalid.Status != http.StatusBadRequest {
		t.Fatalf("invalid filters: status %d body %s, want 400", invalid.Status, invalid.Body)
	}
	var problem validationProblemJSON
	invalid.JSON(&problem)
	for _, key := range []string{"provenance", "eventType[0]", "occurredFrom"} {
		if _, ok := problem.Errors[key]; !ok {
			t.Errorf("errors missing key %q, got %v", key, problem.Errors)
		}
	}
}

// Ported from Integration/TimelineEndpointsTests.cs.
// TimelineCursorRejectsOtherCustomerAndDifferentFilter.
func TestTimelineCursorRejectsOtherCustomerAndDifferentFilter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	first := createCustomer(t, c, "Timeline Cursor First Co")
	second := createCustomer(t, c, "Timeline Cursor Second Co")
	createManual(t, c, first.Id, "2026-07-27", "first")
	createManual(t, c, first.Id, "2026-07-26", "second")
	createManual(t, c, second.Id, "2026-07-27", "other customer")

	page := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?eventType=note&limit=1", first.Id), nil)
	var pageList timelineListJSON
	page.JSON(&pageList)
	cursor := url.QueryEscape(*pageList.NextCursor)

	otherCustomer := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?eventType=note&limit=1&cursor=%s", second.Id, cursor), nil)
	if otherCustomer.Status != http.StatusBadRequest {
		t.Errorf("cursor against other customer: status %d, want 400", otherCustomer.Status)
	}

	differentFilter := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?eventType=other&limit=1&cursor=%s", first.Id, cursor), nil)
	if differentFilter.Status != http.StatusBadRequest {
		t.Fatalf("cursor against different filter: status %d, want 400", differentFilter.Status)
	}
	var problem validationProblemJSON
	differentFilter.JSON(&problem)
	if _, ok := problem.Errors["cursor"]; !ok {
		t.Errorf("errors missing key %q, got %v", "cursor", problem.Errors)
	}
}

// Ported from Integration/TimelineEndpointsTests.cs.
// SemanticallyUnchangedCustomerAndRelationshipUpdatesDoNotCreateEvents.
func TestSemanticallyUnchangedCustomerAndRelationshipUpdatesDoNotCreateEvents(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Timeline Noop Co")
	contact := createContact(t, c, map[string]any{"firstName": "Noop", "lastName": "Contact"})
	attachContact(t, c, customer.Id, contact.Id, "CEO")

	before := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?limit=100", customer.Id), nil)
	var beforeList timelineListJSON
	before.JSON(&beforeList)

	getResp := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", customer.Id), nil)
	var got customerJSON
	getResp.JSON(&got)

	customerUpdate := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", customer.Id), map[string]any{"name": got.Name})
	if customerUpdate.Status != http.StatusOK {
		t.Fatalf("noop customer update: status %d, want 200", customerUpdate.Status)
	}
	relationshipUpdate := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/contacts/%d", customer.Id, contact.Id), map[string]any{"title": "CEO"})
	if relationshipUpdate.Status != http.StatusOK {
		t.Fatalf("noop relationship update: status %d, want 200", relationshipUpdate.Status)
	}

	after := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline?limit=100", customer.Id), nil)
	var afterList timelineListJSON
	after.JSON(&afterList)
	if len(afterList.Data) != len(beforeList.Data) {
		t.Errorf("entry count = %d, want %d (no-op updates must not create events)", len(afterList.Data), len(beforeList.Data))
	}
}

// Ported from Integration/TimelineEndpointsTests.cs.
// DeleteContact_EmitsRemovalForEveryAssociatedCustomer.
func TestTimelineDeleteContact_EmitsRemovalForEveryAssociatedCustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Removal", "lastName": "Contact"})
	first := createCustomer(t, c, "Timeline Removal First Co")
	second := createCustomer(t, c, "Timeline Removal Second Co")
	attachContact(t, c, first.Id, contact.Id, "CEO")
	attachContact(t, c, second.Id, contact.Id, "CEO")

	deleted := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/contacts/%d", contact.Id), nil)
	if deleted.Status != http.StatusNoContent {
		t.Fatalf("delete contact: status %d body %s, want 204", deleted.Status, deleted.Body)
	}

	for _, cust := range []createdCustomerJSON{first, second} {
		feed := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline", cust.Id), nil)
		var feedList timelineListJSON
		feed.JSON(&feedList)
		var removed *timelineEntryJSON
		for i, e := range feedList.Data {
			if e.EventType == "customer.contact_removed" {
				removed = &feedList.Data[i]
			}
		}
		if removed == nil {
			t.Fatalf("customer %d: no customer.contact_removed event, got %v", cust.Id, feedList.Data)
		}
		payloadStr := string(removed.Payload)
		if !contains(payloadStr, fmt.Sprintf(`"contactId":%d`, contact.Id)) {
			t.Errorf("customer %d: removed payload %s missing contactId %d", cust.Id, payloadStr, contact.Id)
		}
		if !contains(payloadStr, "displayName") {
			t.Errorf("customer %d: removed payload %s missing displayName", cust.Id, payloadStr)
		}
		if cust.Id == first.Id && !contains(str(removed.Summary), "Contact removed") {
			t.Errorf("removed summary %q missing %q", str(removed.Summary), "Contact removed")
		}
	}
}
