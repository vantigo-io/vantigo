package expenses_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the travel claim itself (design §3.6): recording one, reading
// it, replacing it, deleting it and listing them, plus who may see each. What
// belongs to a claim's *lines* is claims_lines_test.go's, and the project
// rules are claims_projects_test.go's.

// A claim is recorded as a draft with no lines, and answers everything it was
// given back.
func TestExpensesClaims_ARecordedClaimIsADraftWithNoLines(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)

	claim := createClaim(t, owner, nil)
	switch {
	case claim.Purpose != "Montasje hos kunden":
		t.Errorf("purpose = %q, want the body's", claim.Purpose)
	case claim.Destination == nil || *claim.Destination != "Bergen":
		t.Errorf("destination = %v, want Bergen", claim.Destination)
	case claim.Abroad:
		t.Errorf("abroad = true, want false by default")
	case claim.Status != "draft":
		t.Errorf("status = %q, want draft", claim.Status)
	case claim.Owner.UserId != ownerID:
		t.Errorf("owner = %v, want the caller %v", claim.Owner.UserId, ownerID)
	case claim.Revision != 1:
		t.Errorf("revision = %d, want 1", claim.Revision)
	case len(claim.Lines) != 0:
		t.Errorf("lines = %+v, want none", claim.Lines)
	case len(claim.Totals) != 0:
		t.Errorf("totals = %+v, want none on a claim with no lines", claim.Totals)
	}
	// A claim with no lines carries an empty array rather than null, so a
	// client never has to tell "none" from "not answered".
	raw := rawClaim(t, owner, claim.Id)
	for _, key := range []string{"lines", "totals"} {
		if value, ok := raw[key]; !ok {
			t.Errorf("%s is absent, want an empty array", key)
		} else if list, ok := value.([]any); !ok || len(list) != 0 {
			t.Errorf("%s = %v, want an empty array", key, value)
		}
	}
	// Nothing was decided and nothing was paid, so neither stamp is there at
	// all — absent, never null.
	for _, key := range []string{"decision", "reimbursement", "submittedAt", "abroadDayRate", "abroadCurrency"} {
		if _, ok := raw[key]; ok {
			t.Errorf("%s = %v, want it absent on a fresh draft", key, raw[key])
		}
	}
}

// A trip abroad is paid at the claim's own day rate, in its own currency; the
// two are required together and refused together.
func TestExpensesClaims_AbroadCarriesItsOwnDayRate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	claim := createClaim(t, owner, map[string]any{
		"abroad": true, "abroadDayRate": 812.50, "abroadCurrency": "SEK",
	})
	switch {
	case !claim.Abroad:
		t.Errorf("abroad = false, want true")
	case claim.AbroadDayRate == nil || *claim.AbroadDayRate != 812.50:
		t.Errorf("abroadDayRate = %v, want 812.50", claim.AbroadDayRate)
	case claim.AbroadCurrency == nil || *claim.AbroadCurrency != "SEK":
		t.Errorf("abroadCurrency = %v, want SEK", claim.AbroadCurrency)
	}
}

// The validation table of design §3.6 and Global Constraints, each rule on the
// field it belongs to.
func TestExpensesClaims_TheBodyIsRefusedFieldByField(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	longText := ""
	for range 201 {
		longText += "a"
	}
	for _, tc := range []struct {
		name      string
		overrides map[string]any
		field     string
		contains  string
	}{
		{"no purpose", map[string]any{"purpose": "  "}, "purpose", "required"},
		{"purpose too long", map[string]any{"purpose": longText}, "purpose", "at most 200 characters"},
		{"destination too long", map[string]any{"destination": longText}, "destination", "at most 200 characters"},
		{
			"return before departure",
			map[string]any{"returnAt": "2026-03-09T06:00:00Z"},
			"returnAt", "after the departure",
		},
		{
			"return equal to departure",
			map[string]any{"returnAt": "2026-03-09T07:00:00Z"},
			"returnAt", "after the departure",
		},
		{
			"trip longer than a year and a day",
			map[string]any{"returnAt": "2027-03-11T16:00:00Z"},
			"returnAt", "at most 366 days",
		},
		{
			"abroad with no day rate",
			map[string]any{"abroad": true, "abroadCurrency": "SEK"},
			"abroadDayRate", "needs the day rate",
		},
		{
			"abroad with no currency",
			map[string]any{"abroad": true, "abroadDayRate": 500.0},
			"abroadCurrency", "needs the currency",
		},
		{
			"a day rate of zero",
			map[string]any{"abroad": true, "abroadDayRate": 0.0, "abroadCurrency": "SEK"},
			"abroadDayRate", "greater than zero",
		},
		{
			"a day rate with three decimals",
			map[string]any{"abroad": true, "abroadDayRate": 500.125, "abroadCurrency": "SEK"},
			"abroadDayRate", "at most 2 decimals",
		},
		{
			"a currency that is not a code",
			map[string]any{"abroad": true, "abroadDayRate": 500.0, "abroadCurrency": "kroner"},
			"abroadCurrency", "three-letter",
		},
		{
			"a day rate on a domestic trip",
			map[string]any{"abroadDayRate": 500.0},
			"abroadDayRate", "belongs to a claim abroad",
		},
		{
			"a currency on a domestic trip",
			map[string]any{"abroadCurrency": "SEK"},
			"abroadCurrency", "belongs to a claim abroad",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := refusedClaim(t, owner, http.MethodPost, claimsPath, claimBody(tc.overrides))
			if !mentions(errs[tc.field], tc.contains) {
				t.Errorf("errors = %v, want %s to mention %q", errs, tc.field, tc.contains)
			}
		})
	}
}

// Every failure is collected, so one round trip reports every problem with a
// body rather than the first.
func TestExpensesClaims_EveryFailureIsReportedAtOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	errs := refusedClaim(t, owner, http.MethodPost, claimsPath, claimBody(map[string]any{
		"purpose":       "",
		"returnAt":      "2026-03-08T07:00:00Z",
		"abroadDayRate": 100.0,
	}))
	for _, field := range []string{"purpose", "returnAt", "abroadDayRate"} {
		if len(errs[field]) == 0 {
			t.Errorf("errors = %v, want one on %s too", errs, field)
		}
	}
}

// A claim is visible to its owner, to a manager of its project, and to
// view-all, approve and manage. Anybody else gets the bare 404 an unknown id
// gets — byte for byte, so neither its existence nor whose it is leaks.
func TestExpensesClaims_VisibilityIsTheEntryRuleAppliedToTheClaim(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	claim := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})

	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	otherManager, _ := signInAs(t, h, projectEuro, roleManager)
	viewAll, _ := signIn(t, h, "expenses:view-all")
	approver, _ := signIn(t, h, "expenses:approve")
	admin, _ := signIn(t, h, "expenses:manage")
	stranger, _ := signIn(t, h)

	for _, tc := range []struct {
		name string
		c    *modtest.Client
	}{
		{"the owner", owner},
		{"its project's manager", manager},
		{"view-all", viewAll},
		{"approve", approver},
		{"manage", admin},
	} {
		if r := tc.c.Do(http.MethodGet, claimPath(claim.Id), nil); r.Status != http.StatusOK {
			t.Errorf("%s reading the claim: status %d body %s, want 200", tc.name, r.Status, r.Body)
		}
	}
	// And the two who may not: a stranger, and a manager of another project.
	for _, tc := range []struct {
		name string
		c    *modtest.Client
	}{
		{"a stranger", stranger},
		{"a manager of another project", otherManager},
	} {
		r := tc.c.Do(http.MethodGet, claimPath(claim.Id), nil)
		if r.Status != http.StatusNotFound {
			t.Errorf("%s reading the claim: status %d body %s, want 404", tc.name, r.Status, r.Body)
		}
		// Byte for byte the answer an id that does not exist gets.
		missing := tc.c.Do(http.MethodGet, claimPath(claim.Id+9999), nil)
		if missing.Status != r.Status || string(missing.Body) != string(r.Body) {
			t.Errorf("%s: an unknown id answered %d %q, the invisible one %d %q; want them identical",
				tc.name, missing.Status, missing.Body, r.Status, r.Body)
		}
	}
	// A claim nobody may see is not in the list either: the list applies the
	// same predicate in SQL.
	if page := listClaims(t, stranger, ""); page.Pagination.TotalCount != 0 {
		t.Errorf("a stranger's list holds %d claims, want none", page.Pagination.TotalCount)
	}
}

// A replace is a full replace of the header, guarded by the revision it was
// read at.
func TestExpensesClaims_AReplaceIsGuardedByTheRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)

	updated := updateClaim(t, owner, claim.Id, map[string]any{
		"purpose":     "Service hos kunden",
		"destination": nil,
		"revision":    claim.Revision,
	})
	switch {
	case updated.Purpose != "Service hos kunden":
		t.Errorf("purpose = %q, want the replaced one", updated.Purpose)
	case updated.Destination != nil:
		t.Errorf("destination = %v, want it cleared by the full replace", *updated.Destination)
	case updated.Revision != claim.Revision+1:
		t.Errorf("revision = %d, want %d", updated.Revision, claim.Revision+1)
	}

	// The revision it was read at has moved on.
	stale := owner.Do(http.MethodPut, claimPath(claim.Id), claimBody(map[string]any{"revision": claim.Revision}))
	if stale.Status != http.StatusConflict {
		t.Fatalf("a stale replace: status %d body %s, want 409", stale.Status, stale.Body)
	}
	// And an absent revision is refused on the field rather than answered with
	// a conflict against a revision nobody read.
	errs := refusedClaim(t, owner, http.MethodPut, claimPath(claim.Id), claimBody(nil))
	if !mentions(errs["revision"], "required") {
		t.Errorf("errors = %v, want revision to say it is required", errs)
	}
}

// Who may change a claim: its owner, and expenses:manage. Somebody who may see
// it and not change it gets the access layer's own 403.
func TestExpensesClaims_OnlyTheOwnerOrManageMayChangeIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	claim := createClaim(t, owner, map[string]any{"projectId": projectKraftVerket})

	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		var body any
		if method == http.MethodPut {
			body = claimBody(map[string]any{"revision": claim.Revision})
		}
		if r := manager.Do(method, claimPath(claim.Id), body); r.Status != http.StatusForbidden {
			t.Errorf("%s by the project's manager: status %d body %s, want 403", method, r.Status, r.Body)
		}
	}

	admin, _ := signIn(t, h, "expenses:manage")
	updateClaim(t, admin, claim.Id, map[string]any{"revision": claim.Revision})
}

// A claim that has moved past being editable is a 400 naming its status — a
// fact about the claim the caller can act on, not a bare 403.
func TestExpensesClaims_ASubmittedClaimIsNoLongerItsOwnersToChange(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)
	// The claim flow is a later task's, so the status is seeded: every rule
	// here is written against the column, which is what makes that legitimate.
	seedClaimStatus(t, h, claim.Id, "submitted")

	errs := refusedClaim(t, owner, http.MethodPut, claimPath(claim.Id),
		claimBody(map[string]any{"revision": claim.Revision + 1}))
	if !mentions(errs["status"], "submitted") {
		t.Errorf("errors = %v, want status to name the claim's own status", errs)
	}
	errs = refusedClaim(t, owner, http.MethodDelete, claimPath(claim.Id), nil)
	if !mentions(errs["status"], "submitted") {
		t.Errorf("delete errors = %v, want status to name the claim's own status", errs)
	}

	// A rejected claim is its owner's again, and saving it makes it a draft.
	seedClaimStatus(t, h, claim.Id, "rejected")
	current := getClaim(t, owner, claim.Id)
	if !current.Capabilities.CanEdit {
		t.Errorf("capabilities = %+v, want a rejected claim to be editable", current.Capabilities)
	}
	updated := updateClaim(t, owner, claim.Id, map[string]any{"revision": current.Revision})
	if updated.Status != "draft" {
		t.Errorf("status = %q, want a saved rejected claim to be a draft again", updated.Status)
	}
}

// The period lock is judged on the day the trip departed — the day every one
// of its lines is judged on too.
func TestExpensesClaims_ThePeriodLockIsJudgedOnTheDeparture(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	putSettings(t, admin, settingsBody(map[string]any{"lockedBefore": "2026-03-10"}))
	owner, _ := signIn(t, h)

	errs := refusedClaim(t, owner, http.MethodPost, claimsPath, claimBody(nil))
	if !mentions(errs["departureAt"], "2026-03-10") {
		t.Errorf("errors = %v, want departureAt to name the lock date", errs)
	}
	// The lock date itself is open.
	createClaim(t, owner, map[string]any{
		"departureAt": "2026-03-10T07:00:00Z", "returnAt": "2026-03-11T16:00:00Z",
	})
	// And expenses:manage works past it.
	locked := createClaim(t, admin, nil)

	// Neither into nor out of the lock: a claim that departs inside the closed
	// period cannot be moved there either.
	open := createClaim(t, owner, map[string]any{
		"departureAt": "2026-03-12T07:00:00Z", "returnAt": "2026-03-13T16:00:00Z",
	})
	errs = refusedClaim(t, owner, http.MethodPut, claimPath(open.Id),
		claimBody(map[string]any{"revision": open.Revision}))
	if !mentions(errs["departureAt"], "2026-03-10") {
		t.Errorf("errors = %v, want departureAt to refuse a move into the closed period", errs)
	}
	_ = locked
}

// **Which day a claim's instants name.** A claim stores two timestamptz, which
// keep the instant and not the offset it was typed in, so there is exactly one
// derivation available and this module uses it everywhere: the **UTC calendar
// day**. A departure of 2026-03-01T00:30+02:00 is 2026-02-28T22:30Z, so every
// date derived from it — the day the period lock judges, the day the list's
// from/to filter matches, and the first day a per diem line may fall on — is
// 2026-02-28, not 2026-03-01.
//
// Every other test here uses Z instants, which cannot tell the two apart; this
// one is the offset case, and it is the reason the SQL filter is written
// `(departure_at AT TIME ZONE 'UTC')::date` and the Go rule `utcDay(...)`.
func TestExpensesClaims_ADeparturesDayIsItsUTCDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, _ := signIn(t, h)

	// Typed as the first of March in Oslo, which is the last of February in
	// UTC — and the lock closes everything before the first.
	const departure = "2026-03-01T00:30:00+02:00"
	putSettings(t, admin, settingsBody(map[string]any{"lockedBefore": "2026-03-01"}))
	errs := refusedClaim(t, owner, http.MethodPost, claimsPath, claimBody(map[string]any{
		"departureAt": departure, "returnAt": "2026-03-02T12:00:00Z",
	}))
	if !mentions(errs["departureAt"], "2026-03-01") {
		t.Errorf("errors = %v, want the lock to judge the departure on its UTC day, 2026-02-28", errs)
	}

	// With the lock a day earlier the very same trip saves, and the list finds
	// it under the UTC day too — 02-28, not 03-01.
	putSettings(t, admin, settingsBody(map[string]any{"lockedBefore": "2026-02-28"}))
	claim := createClaim(t, owner, map[string]any{
		"departureAt": departure, "returnAt": "2026-03-02T12:00:00Z",
	})
	if got := claimIDsOf(listClaims(t, owner, "?from=2026-02-28&to=2026-02-28")); len(got) != 1 || got[0] != claim.Id {
		t.Errorf("claims departing on 2026-02-28 = %v, want [%d]", got, claim.Id)
	}
	if got := claimIDsOf(listClaims(t, owner, "?from=2026-03-01")); len(got) != 0 {
		t.Errorf("claims departing on or after 2026-03-01 = %v, want none", got)
	}

	// And the trip's first day — what a per diem line may be dated on — is the
	// same 2026-02-28. The day before it is outside the trip.
	addLine(t, owner, claim.Id, perDiemBody(map[string]any{"entryDate": "2026-02-28"}))
	errs = refusedEntry(t, owner, http.MethodPost, entriesPath,
		perDiemBody(map[string]any{"claimId": claim.Id, "entryDate": "2026-02-27"}))
	if len(errs["entryDate"]) == 0 {
		t.Errorf("errors = %v, want entryDate to put 2026-02-27 outside the trip", errs)
	}
}

// The list is the claims the caller may see, the most recent trip first, with
// the filters the contract declares.
func TestExpensesClaims_TheListIsPagedAndFiltered(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)

	march := createClaim(t, owner, nil)
	april := createClaim(t, owner, map[string]any{
		"departureAt": "2026-04-06T07:00:00Z", "returnAt": "2026-04-08T16:00:00Z",
	})
	seedClaimStatus(t, h, april.Id, "submitted")

	if got := claimIDsOf(listClaims(t, owner, "")); len(got) != 2 || got[0] != april.Id {
		t.Errorf("claims = %v, want the most recent trip first: [%d %d]", got, april.Id, march.Id)
	}
	if got := claimIDsOf(listClaims(t, owner, "?status=submitted")); len(got) != 1 || got[0] != april.Id {
		t.Errorf("submitted claims = %v, want [%d]", got, april.Id)
	}
	if got := claimIDsOf(listClaims(t, owner, "?from=2026-04-01")); len(got) != 1 || got[0] != april.Id {
		t.Errorf("claims from April = %v, want [%d]", got, april.Id)
	}
	if got := claimIDsOf(listClaims(t, owner, "?to=2026-03-31")); len(got) != 1 || got[0] != march.Id {
		t.Errorf("claims to March = %v, want [%d]", got, march.Id)
	}
	if got := claimIDsOf(listClaims(t, owner, fmt.Sprintf("?userId=%s", ownerID))); len(got) != 2 {
		t.Errorf("claims of their own = %v, want both", got)
	}
	if got := claimIDsOf(listClaims(t, owner, "?reimbursed=true")); len(got) != 0 {
		t.Errorf("reimbursed claims = %v, want none", got)
	}

	page := listClaims(t, owner, "?page=1&pageSize=1")
	if page.Pagination.TotalCount != 2 || len(page.Data) != 1 {
		t.Errorf("page = %+v, want one of two", page.Pagination)
	}
	// An unknown status is a mistake worth reporting, not a filter matching
	// nothing.
	if r := owner.Do(http.MethodGet, claimsPath+"?status=paid", nil); r.Status != http.StatusBadRequest {
		t.Errorf("an unknown status: status %d body %s, want 400", r.Status, r.Body)
	}
}

// Deleting a claim takes its lines, their receipt rows and — after the commit
// — the objects those rows named.
func TestExpensesClaims_DeleteTakesItsLinesAndTheirReceipts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)
	line := addLine(t, owner, claim.Id, outlayBody(nil))
	uploadReceipt(t, owner, line.Id, "kvittering.png", "image/png", testPNG(t, 4, 4))

	if h.objects.count() != 1 {
		t.Fatalf("the object store holds %d objects, want the one receipt", h.objects.count())
	}
	deleteClaim(t, owner, claim.Id)

	if r := owner.Do(http.MethodGet, claimPath(claim.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("reading the deleted claim: status %d, want 404", r.Status)
	}
	if r := owner.Do(http.MethodGet, entryPath(line.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("reading the deleted claim's line: status %d, want 404", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM expenses.attachments WHERE entry_id = $1`, line.Id); n != 0 {
		t.Errorf("attachment rows = %d, want the cascade to have taken them", n)
	}
	if h.objects.count() != 0 {
		t.Errorf("the object store holds %v, want the receipts removed after the commit", h.objects.keys())
	}
}

// The claim's totals are its lines' figures per currency, and nothing is ever
// converted.
func TestExpensesClaims_TotalsArePerCurrencyAndNeverConverted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)

	addLine(t, owner, claim.Id, outlayBody(map[string]any{"grossAmount": 1250.00}))
	addLine(t, owner, claim.Id, outlayBody(map[string]any{
		"grossAmount": 60.00, "currency": "EUR", "description": "Parkering",
	}))
	// A company-paid outlay is in the gross and owes the employee nothing.
	addLine(t, owner, claim.Id, outlayBody(map[string]any{
		"grossAmount": 400.00, "paidBy": "company", "description": "Firmakort",
	}))

	claim = getClaim(t, owner, claim.Id)
	if len(claim.Lines) != 3 {
		t.Fatalf("lines = %d, want three", len(claim.Lines))
	}
	byCurrency := map[string]currencyTotalJSON{}
	for _, total := range claim.Totals {
		byCurrency[total.Currency] = total
	}
	if got := byCurrency["NOK"]; got.Gross != 1650 || got.OwedToEmployee != 1250 {
		t.Errorf("NOK total = %+v, want gross 1650 and 1250 owed", got)
	}
	if got := byCurrency["EUR"]; got.Gross != 60 || got.OwedToEmployee != 60 {
		t.Errorf("EUR total = %+v, want gross 60 and 60 owed", got)
	}
	// The list answers the same figures and how many lines there are.
	page := listClaims(t, owner, "")
	if len(page.Data) != 1 || page.Data[0].LineCount != 3 {
		t.Fatalf("list = %+v, want one claim holding three lines", page.Data)
	}
	if len(page.Data[0].Totals) != 2 {
		t.Errorf("list totals = %+v, want one line per currency", page.Data[0].Totals)
	}
}

// A claim's lines are not loose drafts on the dashboard. Their own status
// column stays at its default and is never read, so counting them there would
// report five drafts for a trip whose owner can do nothing with one of them on
// its own — and the one thing that is actionable, the claim, would not be in
// the figure at all.
func TestExpensesClaims_ItsLinesAreNotCountedAsLooseDrafts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)

	standalone := createEntry(t, owner, outlayBody(nil))
	before := getStats(t, owner)
	if before.Draft != 1 {
		t.Fatalf("drafts = %d, want the one standalone expense", before.Draft)
	}

	claim := createClaim(t, owner, nil)
	for range 5 {
		addLine(t, owner, claim.Id, outlayBody(nil))
	}
	after := getStats(t, owner)
	if after.Draft != 1 {
		t.Errorf("drafts = %d after five lines were added to a trip, want the one standalone expense",
			after.Draft)
	}
	// The same figure on the summary card, which reads the same query.
	if summary := getStatsSummary(t, owner, ""); summary.MyDrafts != 1 {
		t.Errorf("the summary says %d drafts, want 1", summary.MyDrafts)
	}
	// And the standalone expense is still counted, so the filter narrowed
	// rather than emptied.
	if got := getEntry(t, owner, standalone.Id); got.Status != "draft" {
		t.Errorf("the standalone expense is %s, want it still a draft", got.Status)
	}
}

// The lines come back in the order the claim shows them — the day they
// happened, then as they were recorded — through the one entry renderer, with
// the capabilities a single read of each would answer.
func TestExpensesClaims_LinesAreOrderedAndRenderedLikeAnyExpense(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	claim := createClaim(t, owner, nil)

	second := addLine(t, owner, claim.Id, outlayBody(map[string]any{"entryDate": "2026-03-10"}))
	first := addLine(t, owner, claim.Id, mileageBody(map[string]any{"entryDate": "2026-03-09"}))
	also := addLine(t, owner, claim.Id, outlayBody(map[string]any{"entryDate": "2026-03-10", "description": "Mat"}))

	claim = getClaim(t, owner, claim.Id)
	want := []int64{first.Id, second.Id, also.Id}
	got := make([]int64, 0, len(claim.Lines))
	for _, line := range claim.Lines {
		got = append(got, line.Id)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("lines = %v, want %v — the day first, then as recorded", got, want)
	}
	for _, line := range claim.Lines {
		switch {
		case line.ClaimId == nil || *line.ClaimId != claim.Id:
			t.Errorf("line %d claimId = %v, want the claim's", line.Id, line.ClaimId)
		case !line.Capabilities.CanEdit:
			t.Errorf("line %d cannot be edited, want it to follow a draft claim", line.Id)
		case line.Capabilities.CanSubmit:
			t.Errorf("line %d says it can be submitted; the claim is the unit that moves", line.Id)
		}
		// A mileage line inside a claim is priced exactly as a standalone one.
		if line.Kind == "mileage" && (line.Rate == nil || *line.Rate != 5.30) {
			t.Errorf("line %d rate = %v, want the dated table's 5.30", line.Id, line.Rate)
		}
	}
}
