package timetracking_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

const ratesPath = "/api/v1/time/rates"

func ratePath(id int32) string { return fmt.Sprintf("%s/%d", ratesPath, id) }

func userRatesPath(userID uuid.UUID) string { return ratesPath + "/users/" + userID.String() }

// rateJSON decodes TimeRateResponse.
type rateJSON struct {
	Id          int32     `json:"id"`
	UserId      uuid.UUID `json:"userId"`
	DisplayName string    `json:"displayName"`
	ValidFrom   string    `json:"validFrom"`
	BillRate    *float64  `json:"billRate"`
	CostRate    *float64  `json:"costRate"`
	Currency    string    `json:"currency"`
}

// addRate posts body as a new rate card row and fails the test unless it was
// created.
func addRate(t *testing.T, c *modtest.Client, body map[string]any) rateJSON {
	t.Helper()
	r := c.Do(http.MethodPost, ratesPath, body)
	if r.Status != http.StatusCreated {
		t.Fatalf("add rate %v: status %d body %s, want 201", body, r.Status, r.Body)
	}
	var rate rateJSON
	r.JSON(&rate)
	return rate
}

// listRates reads path (a rates list) and fails the test unless it answered
// 200.
func listRates(t *testing.T, c *modtest.Client, path string) []rateJSON {
	t.Helper()
	r := c.Do(http.MethodGet, path, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s, want 200", path, r.Status, r.Body)
	}
	var rates []rateJSON
	r.JSON(&rates)
	return rates
}

func rateIDs(rates []rateJSON) []int32 {
	ids := make([]int32, 0, len(rates))
	for _, r := range rates {
		ids = append(ids, r.Id)
	}
	return ids
}

func TestTimeRates_CreateListUpdateDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "time:manage")
	_, perID := signIn(t, h)
	_, anneID := signIn(t, h)
	setDisplayName(t, h, perID, "Per")
	setDisplayName(t, h, anneID, "Anne")

	first := addRate(t, admin, map[string]any{"userId": perID, "validFrom": "2026-01-01", "billRate": 1100, "currency": " nok "})
	if first.Id == 0 || first.UserId != perID || first.DisplayName != "Per" || first.ValidFrom != "2026-01-01" ||
		deref(first.BillRate) != 1100.0 || first.CostRate != nil || first.Currency != "NOK" {
		t.Errorf("created = %+v, want Per's 1100 NOK bill rate from 2026-01-01, upper-cased, no cost rate", first)
	}
	second := addRate(t, admin, map[string]any{"userId": perID, "validFrom": "2026-09-01", "billRate": 1200, "costRate": 700, "currency": "NOK"})
	anne := addRate(t, admin, map[string]any{"userId": anneID, "validFrom": "2026-03-01", "costRate": 650.5, "currency": "EUR"})

	if got := rateIDs(listRates(t, admin, userRatesPath(perID))); !slices.Equal(got, []int32{second.Id, first.Id}) {
		t.Errorf("Per's rates = %v, want [%d %d], the latest first", got, second.Id, first.Id)
	}
	if got := rateIDs(listRates(t, admin, ratesPath+"?userId="+perID.String())); !slices.Equal(got, []int32{second.Id, first.Id}) {
		t.Errorf("rates?userId=Per = %v, want [%d %d]", got, second.Id, first.Id)
	}
	if got := rateIDs(listRates(t, admin, ratesPath)); !slices.Equal(got, []int32{anne.Id, second.Id, first.Id}) {
		t.Errorf("all rates = %v, want Anne's then Per's latest first", got)
	}

	r := admin.Do(http.MethodPut, ratePath(first.Id), map[string]any{"validFrom": "2026-02-01", "costRate": 600, "currency": "eur"})
	if r.Status != http.StatusOK {
		t.Fatalf("update: status %d body %s, want 200", r.Status, r.Body)
	}
	var updated rateJSON
	r.JSON(&updated)
	if updated.Id != first.Id || updated.UserId != perID || updated.ValidFrom != "2026-02-01" ||
		updated.BillRate != nil || deref(updated.CostRate) != 600.0 || updated.Currency != "EUR" {
		t.Errorf("updated = %+v, want Per's 600 EUR cost rate from 2026-02-01 and no bill rate", updated)
	}

	// One row per person and day, on a create and on an update.
	errs := validationErrors(t, admin.Do(http.MethodPost, ratesPath,
		map[string]any{"userId": perID, "validFrom": "2026-09-01", "billRate": 1, "currency": "NOK"}), "Invalid rate")
	if len(errs["validFrom"]) != 1 {
		t.Errorf("duplicate create: errors = %v, want one on validFrom", errs)
	}
	errs = validationErrors(t, admin.Do(http.MethodPut, ratePath(first.Id),
		map[string]any{"validFrom": "2026-09-01", "billRate": 1, "currency": "NOK"}), "Invalid rate")
	if len(errs["validFrom"]) != 1 {
		t.Errorf("duplicate update: errors = %v, want one on validFrom", errs)
	}
	// Another person's day is theirs.
	addRate(t, admin, map[string]any{"userId": anneID, "validFrom": "2026-09-01", "billRate": 1, "currency": "NOK"})

	if r := admin.Do(http.MethodDelete, ratePath(second.Id), nil); r.Status != http.StatusNoContent {
		t.Errorf("delete: status %d, want 204", r.Status)
	}
	if got := rateIDs(listRates(t, admin, userRatesPath(perID))); !slices.Equal(got, []int32{first.Id}) {
		t.Errorf("after delete: %v, want [%d]", got, first.Id)
	}
	if r := admin.Do(http.MethodDelete, ratePath(second.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("delete again: status %d, want 404", r.Status)
	}
	if r := admin.Do(http.MethodPut, ratePath(second.Id), map[string]any{"validFrom": "2026-01-01", "billRate": 1, "currency": "NOK"}); r.Status != http.StatusNotFound {
		t.Errorf("update a deleted row: status %d, want 404", r.Status)
	}
	if got := listRates(t, admin, userRatesPath(uuid.New())); len(got) != 0 {
		t.Errorf("an unknown person's rates = %+v, want none", got)
	}
}

func TestPostTimeRates_InvalidBody_IsRefusedOnTheField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "time:manage")
	_, userID := signIn(t, h)

	valid := func(overrides map[string]any) map[string]any {
		body := map[string]any{"userId": userID, "validFrom": "2026-01-01", "billRate": 1100, "currency": "NOK"}
		for k, v := range overrides {
			if v == nil {
				delete(body, k)
			} else {
				body[k] = v
			}
		}
		return body
	}
	for _, tc := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"no rate at all", valid(map[string]any{"billRate": nil}), "billRate"},
		{"a zero bill rate", valid(map[string]any{"billRate": 0}), "billRate"},
		{"a negative cost rate", valid(map[string]any{"costRate": -5}), "costRate"},
		{"three decimals", valid(map[string]any{"billRate": 1100.125}), "billRate"},
		{"too large", valid(map[string]any{"billRate": 1e10}), "billRate"},
		{"no currency", valid(map[string]any{"currency": ""}), "currency"},
		{"a currency that is no code", valid(map[string]any{"currency": "N0K"}), "currency"},
		{"no validFrom", valid(map[string]any{"validFrom": nil}), "validFrom"},
		{"no userId", valid(map[string]any{"userId": nil}), "userId"},
		{"an unknown user", valid(map[string]any{"userId": uuid.New()}), "userId"},
	} {
		errs := validationErrors(t, admin.Do(http.MethodPost, ratesPath, tc.body), "Invalid rate")
		if len(errs[tc.field]) != 1 {
			t.Errorf("%s: errors = %v, want one on %s", tc.name, errs, tc.field)
		}
	}
	if got := listRates(t, admin, ratesPath); len(got) != 0 {
		t.Errorf("rates = %+v, want nothing stored", got)
	}
}

func TestPostTimeRates_DisabledUser_KeepsAnEditableRateCard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "time:manage")
	_, userID := signIn(t, h)
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, userID)

	rate := addRate(t, admin, map[string]any{"userId": userID, "validFrom": "2025-01-01", "costRate": 500, "currency": "NOK"})
	if r := admin.Do(http.MethodPut, ratePath(rate.Id), map[string]any{"validFrom": "2025-01-01", "costRate": 550, "currency": "NOK"}); r.Status != http.StatusOK {
		t.Errorf("update: status %d body %s, want 200", r.Status, r.Body)
	}
}

func TestTimeRates_WithoutTimeManage_IsForbidden(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "time:manage")
	_, userID := signIn(t, h)
	rate := addRate(t, admin, map[string]any{"userId": userID, "validFrom": "2026-01-01", "billRate": 1000, "currency": "NOK"})
	body := map[string]any{"userId": userID, "validFrom": "2026-02-01", "billRate": 1000, "currency": "NOK"}

	for _, perms := range [][]string{nil, {"time:view-all", "time:approve"}} {
		c, _ := signIn(t, h, perms...)
		for _, req := range []struct {
			method, path string
			body         any
		}{
			{http.MethodGet, ratesPath, nil},
			{http.MethodGet, userRatesPath(userID), nil},
			{http.MethodPost, ratesPath, body},
			{http.MethodPut, ratePath(rate.Id), body},
			{http.MethodDelete, ratePath(rate.Id), nil},
		} {
			if r := c.Do(req.method, req.path, req.body); r.Status != http.StatusForbidden {
				t.Errorf("%v %s %s: status %d, want 403", perms, req.method, req.path, r.Status)
			}
		}
	}
}

// A rate card row prices what is saved from then on, from its validFrom:
// a new entry takes it, a draft takes it on its next save, and a submitted
// entry never moves (D3).
func TestTimeRates_ANewRate_PricesNewEntriesAndDraftsButNeverASubmittedOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "time:manage")
	member, memberID := signInAs(t, h, projectEuro, roleMember)
	euro := func(date string) map[string]any {
		return map[string]any{"projectId": projectEuro, "entryDate": date}
	}

	addRate(t, admin, map[string]any{"userId": memberID, "validFrom": "2026-01-01", "billRate": 1000, "currency": "EUR"})
	submitted := submittedEntry(t, member, euro("2026-09-16"))
	draft := createEntry(t, member, euro("2026-09-17"))
	wantRate(t, submitted, 1000, "EUR", "person")
	wantRate(t, draft, 1000, "EUR", "person")

	later := addRate(t, admin, map[string]any{"userId": memberID, "validFrom": "2026-09-15", "billRate": 1200, "currency": "EUR"})
	wantRate(t, createEntry(t, member, euro("2026-09-14")), 1000, "EUR", "person")
	wantRate(t, createEntry(t, member, euro("2026-09-15")), 1200, "EUR", "person")
	wantRate(t, getEntry(t, member, submitted.Id), 1000, "EUR", "person")
	wantRate(t, getEntry(t, member, draft.Id), 1000, "EUR", "person")
	wantRate(t, updateEntry(t, member, draft, map[string]any{"hours": 3}), 1200, "EUR", "person")

	// Changing the row, too, moves nothing submitted.
	if r := admin.Do(http.MethodPut, ratePath(later.Id), map[string]any{"validFrom": "2026-09-15", "billRate": 1300, "currency": "EUR"}); r.Status != http.StatusOK {
		t.Fatalf("update rate: status %d body %s", r.Status, r.Body)
	}
	wantRate(t, getEntry(t, member, submitted.Id), 1000, "EUR", "person")
	wantRate(t, createEntry(t, member, euro("2026-09-18")), 1300, "EUR", "person")
}
