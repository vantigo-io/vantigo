package customers_test

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"
	_ "time/tzdata" // America/New_York wherever the tests run
)

// eveningBeforeInOslo is 23:30 UTC on Saturday 12 September, the harness
// clock's own day: already 01:30 on Sunday the 13th in Oslo, still the 12th
// under UTC and in New York (19:30).
var eveningBeforeInOslo = time.Date(2026, time.September, 12, 23, 30, 0, 0, time.UTC)

// "Today", for a follow-up, is the business day (time zones design D7): today
// in the workspace's business time zone, read through the business calendar
// on the request that judges it. At 23:30 UTC on the 12th it is the 13th in
// Oslo, so a follow-up due the 13th is due today there and one due the 12th
// is overdue, on the attention list and behind state=overdue alike. Under
// UTC and in New York it is still the 12th: the first is not due yet, and the
// second is due today rather than overdue.
func TestBusinessZone_AFollowUpsTodayFollowsTheWorkspace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.Advance(eveningBeforeInOslo.Sub(h.Now()))
	c, caller := authenticatedClientWithID(t, h)
	customer := createCustomer(t, c, "Business Day Co")
	dueThe13th := createWithFollowUp(t, c, customer.Id, "2026-09-12", "due the 13th", map[string]any{"dueOn": "2026-09-13", "assigneeUserId": caller})
	dueThe12th := createWithFollowUp(t, c, customer.Id, "2026-09-12", "due the 12th", map[string]any{"dueOn": "2026-09-12", "assigneeUserId": caller})

	for _, tc := range []struct {
		zone        string
		attention   []string
		overdueList []int32
	}{
		{
			zone: "Europe/Oslo",
			attention: []string{
				fmt.Sprintf("followUpDue/%d", dueThe13th.Id),
				fmt.Sprintf("followUpOverdue/%d", dueThe12th.Id),
			},
			overdueList: []int32{dueThe12th.Id},
		},
		{
			zone:        "UTC",
			attention:   []string{fmt.Sprintf("followUpDue/%d", dueThe12th.Id)},
			overdueList: []int32{},
		},
		{
			zone:        "America/New_York",
			attention:   []string{fmt.Sprintf("followUpDue/%d", dueThe12th.Id)},
			overdueList: []int32{},
		},
	} {
		h.SetBusinessTimeZone(t, tc.zone)

		attention := []string{}
		for _, item := range getAttention(t, c) {
			attention = append(attention, item.Id)
		}
		slices.Sort(attention)
		if !slices.Equal(attention, tc.attention) {
			t.Errorf("%s: attention = %v, want %v", tc.zone, attention, tc.attention)
		}

		overdue := []int32{}
		for _, row := range listFollowUps(t, c, url.Values{"state": {"overdue"}}).Data {
			overdue = append(overdue, row.EntryId)
		}
		if !slices.Equal(overdue, tc.overdueList) {
			t.Errorf("%s: state=overdue = %v, want %v", tc.zone, overdue, tc.overdueList)
		}
	}
}

// An entry's occurredAt falls on its occurredOn in the business time zone
// (time zones design D7): 23:30 UTC on 30 June is 1 July in Oslo and 30 June
// under UTC, so each zone accepts the one date and refuses the other. The
// bound occurredOn may not pass is the business day too: at 23:30 UTC on the
// 12th, the 13th is today in Oslo and the future under UTC.
func TestBusinessZone_AnEntrysDayFollowsTheWorkspace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.Advance(eveningBeforeInOslo.Sub(h.Now()))
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Occurred Co")
	path := fmt.Sprintf("/api/v1/customers/%d/timeline", customer.Id)
	post := func(occurredOn, occurredAt string) *writeResult {
		body := map[string]any{"eventType": "note", "occurredOn": occurredOn, "note": "late call"}
		if occurredAt != "" {
			body["occurredAt"] = occurredAt
		}
		r := c.Do(http.MethodPost, path, body)
		var problem validationProblemJSON
		if r.Status == http.StatusBadRequest {
			r.JSON(&problem)
		}
		return &writeResult{status: r.Status, errors: problem.Errors}
	}

	for _, tc := range []struct {
		zone           string
		accepted       string
		refused        string
		the13thIsToday bool
	}{
		{zone: "Europe/Oslo", accepted: "2026-07-01", refused: "2026-06-30", the13thIsToday: true},
		{zone: "UTC", accepted: "2026-06-30", refused: "2026-07-01"},
	} {
		h.SetBusinessTimeZone(t, tc.zone)

		if got := post(tc.accepted, "2026-06-30T23:30:00Z"); got.status != http.StatusCreated {
			t.Errorf("%s: occurredOn %s at 23:30 UTC on 30 June = %d %v, want 201", tc.zone, tc.accepted, got.status, got.errors)
		}
		got := post(tc.refused, "2026-06-30T23:30:00Z")
		if got.status != http.StatusBadRequest || !slices.Equal(got.errors["occurredAt"], []string{"OccurredAt must fall on occurredOn in the business time zone"}) {
			t.Errorf("%s: occurredOn %s at 23:30 UTC on 30 June = %d %v, want 400 on occurredAt", tc.zone, tc.refused, got.status, got.errors)
		}

		got = post("2026-09-13", "")
		switch {
		case tc.the13thIsToday && got.status != http.StatusCreated:
			t.Errorf("%s: occurredOn 2026-09-13 = %d %v, want 201 (today)", tc.zone, got.status, got.errors)
		case !tc.the13thIsToday && (got.status != http.StatusBadRequest || len(got.errors["occurredOn"]) != 1):
			t.Errorf("%s: occurredOn 2026-09-13 = %d %v, want 400 on occurredOn (the future)", tc.zone, got.status, got.errors)
		}
	}
}

type writeResult struct {
	status int
	errors map[string][]string
}

// An entry written consistent on one day and later read under a zone that
// calls it inconsistent is left as written, and the rule is checked on
// writes only: a PUT that re-sends its occurredOn and occurredAt unchanged
// is not judged against the new day — the follow-up assignee's division
// between validating a change and re-rendering stored state — while a PUT
// that changes either is.
func TestBusinessZone_AnUnchangedOccurrenceIsNotJudgedAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Written Under UTC Co")
	h.SetBusinessTimeZone(t, "UTC")
	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/timeline", customer.Id), map[string]any{
		"eventType": "note", "occurredOn": "2026-06-30", "occurredAt": "2026-06-30T23:30:00Z", "note": "written under UTC",
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("create under UTC: status %d body %s, want 201", r.Status, r.Body)
	}
	var entry followUpEntryJSON
	r.JSON(&entry)

	h.SetBusinessTimeZone(t, "Europe/Oslo")
	path := fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customer.Id, entry.Id)
	put := func(occurredOn, occurredAt string, revision int32) *writeResult {
		r := c.Do(http.MethodPut, path, map[string]any{
			"eventType": "note", "occurredOn": occurredOn, "occurredAt": occurredAt, "note": "edited under Oslo", "expectedRevision": revision,
		})
		var problem validationProblemJSON
		if r.Status == http.StatusBadRequest {
			r.JSON(&problem)
		}
		return &writeResult{status: r.Status, errors: problem.Errors}
	}

	if got := put("2026-06-30", "2026-06-30T23:30:00Z", entry.CurrentRevision); got.status != http.StatusOK {
		t.Fatalf("PUT re-sending the stored pair = %d %v, want 200", got.status, got.errors)
	}
	next := entry.CurrentRevision + 1
	for _, tc := range []struct{ name, occurredOn, occurredAt string }{
		{"a changed occurredAt", "2026-06-30", "2026-06-30T23:45:00Z"},
		{"a changed occurredOn", "2026-06-29", "2026-06-30T23:30:00Z"},
	} {
		got := put(tc.occurredOn, tc.occurredAt, next)
		if got.status != http.StatusBadRequest || !slices.Equal(got.errors["occurredAt"], []string{"OccurredAt must fall on occurredOn in the business time zone"}) {
			t.Errorf("PUT with %s = %d %v, want 400 on occurredAt", tc.name, got.status, got.errors)
		}
	}
	if got := put("2026-07-01", "2026-06-30T23:30:00Z", next); got.status != http.StatusOK {
		t.Errorf("PUT moving occurredOn to Oslo's day = %d %v, want 200", got.status, got.errors)
	}
}

// An anonymisation's day is judged against the business day too: at 23:30 UTC
// on the 12th, the 12th is already past in Oslo and still today under UTC, and
// a customer scheduled for the 13th is due in Oslo and not yet under UTC. The
// worker reads the zone before its lease holds a connection
// (TestAnonymisationWorker_RunsTheCycleOnTheOneConnectionItHolds).
func TestBusinessZone_AnAnonymisationsDayFollowsTheWorkspace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.Advance(eveningBeforeInOslo.Sub(h.Now()))
	c := authenticatedClient(t, h)
	dueThe13th := archivedPerson(t, c, "Kari Nordmann")
	scheduleOn(t, h, dueThe13th.Id, "2026-09-13")
	other := archivedPerson(t, c, "Ola Nordmann")
	scheduler := personalDataClient(t, h)

	h.SetBusinessTimeZone(t, "Europe/Oslo")
	if r := putAnonymisation(t, scheduler, other.Id, "2026-09-12"); r.Status != http.StatusBadRequest {
		t.Errorf("Oslo: schedule on the 12th = %d %s, want 400 (the past)", r.Status, r.Body)
	}
	h.SetBusinessTimeZone(t, "UTC")
	if r := putAnonymisation(t, scheduler, other.Id, "2026-09-12"); r.Status != http.StatusOK {
		t.Errorf("UTC: schedule on the 12th = %d %s, want 200 (today)", r.Status, r.Body)
	}

	anonymised := func() int {
		return h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND anonymised_at IS NOT NULL`, dueThe13th.Id)
	}
	runAnonymisation(t, h)
	if n := anonymised(); n != 0 {
		t.Errorf("UTC: the customer due the 13th anonymised = %d, want 0 (not due yet)", n)
	}
	h.SetBusinessTimeZone(t, "Europe/Oslo")
	runAnonymisation(t, h)
	if n := anonymised(); n != 1 {
		t.Errorf("Oslo: the customer due the 13th anonymised = %d, want 1 (due today)", n)
	}
}
