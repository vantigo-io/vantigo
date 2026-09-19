package expenses_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// This file is the entries themselves, without a project anywhere in them:
// what an outlay and a mileage line are, what they refuse, what they compute
// and who may change them. The project link is entries_projects_test.go's, and
// who may see them entries_visibility_test.go's.

// The default outlay body names a category by id, which only holds as long as
// the migration seeds Materials first.
func TestExpensesEntries_TheDefaultBodyNamesASeededCategory(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signIn(t, h)

	categories := listCategories(t, employee)
	if len(categories) == 0 || categories[0].Id != materialsCategory || categories[0].Name != "Materials" {
		t.Fatalf("the first seeded category = %+v, want Materials with id %d", categories, materialsCategory)
	}
}

func TestExpensesEntries_AnOutlayIsRecordedAndReadBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, employeeID := signIn(t, h)

	created := createEntry(t, employee, outlayBody(map[string]any{
		"supplier": "Elektrogrossisten AS", "vatAmount": 250.00,
	}))
	if created.Kind != "outlay" || created.EntryDate != "2026-03-10" {
		t.Fatalf("created = %+v, want the body back", created)
	}
	if created.GrossAmount != 1250 || created.VatAmount == nil || *created.VatAmount != 250 {
		t.Errorf("gross/vat = %v/%v, want 1250 and 250", created.GrossAmount, created.VatAmount)
	}
	if created.NetAmount != 1000 {
		t.Errorf("netAmount = %v, want gross minus VAT", created.NetAmount)
	}
	if created.OwedToEmployee != 1250 {
		t.Errorf("owedToEmployee = %v, want the gross of an outlay the employee paid", created.OwedToEmployee)
	}
	if created.Category == nil || created.Category.Id != materialsCategory || created.Category.Name != "Materials" {
		t.Errorf("category = %+v, want the named one with its name", created.Category)
	}
	if created.Supplier == nil || *created.Supplier != "Elektrogrossisten AS" {
		t.Errorf("supplier = %v, want the one given", created.Supplier)
	}
	if created.PaidBy == nil || *created.PaidBy != "employee" {
		t.Errorf("paidBy = %v, want employee", created.PaidBy)
	}
	if created.Status != "draft" || created.Revision != 1 {
		t.Errorf("status/revision = %q/%d, want a draft at revision 1", created.Status, created.Revision)
	}
	if created.AttachmentCount != 0 {
		t.Errorf("attachmentCount = %d, want none yet", created.AttachmentCount)
	}
	if created.Owner.UserId != employeeID || !created.Owner.Active {
		t.Errorf("owner = %+v, want the caller, active", created.Owner)
	}
	if created.Billable {
		t.Error("billable = true, want false — nothing named a project")
	}
	if created.Billing != nil {
		t.Errorf("billing = %+v, want none without a project", created.Billing)
	}
	if !created.Capabilities.CanEdit || !created.Capabilities.CanDelete || !created.Capabilities.CanSubmit {
		t.Errorf("capabilities = %+v, want the owner's own draft to be theirs to change", created.Capabilities)
	}

	if got := getEntry(t, employee, created.Id); got.Id != created.Id || got.NetAmount != created.NetAmount {
		t.Errorf("read back = %+v, want what was created", got)
	}
}

// A company-paid outlay is the company's cost, and the employee is owed
// nothing for it.
func TestExpensesEntries_ACompanyPaidOutlayOwesTheEmployeeNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signIn(t, h)

	created := createEntry(t, employee, outlayBody(map[string]any{"paidBy": "company"}))
	if created.OwedToEmployee != 0 {
		t.Errorf("owedToEmployee = %v, want nothing — the company paid it", created.OwedToEmployee)
	}
	if created.NetAmount != 1250 {
		t.Errorf("netAmount = %v, want the gross when no VAT was entered", created.NetAmount)
	}
	if created.VatAmount != nil {
		t.Errorf("vatAmount = %v, want it absent rather than zero", created.VatAmount)
	}
}

// Mileage is priced from the rate table for the entry's own date: the amount,
// the rate and the passenger rate are all the server's, never the client's.
func TestExpensesEntries_MileageIsPricedFromTheRateTable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signIn(t, h)

	created := createEntry(t, employee, mileageBody(nil))
	if created.DistanceKm == nil || *created.DistanceKm != 120 {
		t.Fatalf("distanceKm = %v, want 120", created.DistanceKm)
	}
	if created.Rate == nil || *created.Rate != 5.30 {
		t.Errorf("rate = %v, want the seeded 5.30", created.Rate)
	}
	// 120 × 5.30, no passengers.
	if created.GrossAmount != 636 {
		t.Errorf("grossAmount = %v, want 120 × 5.30 = 636", created.GrossAmount)
	}
	if created.NetAmount != 636 || created.OwedToEmployee != 636 {
		t.Errorf("net/owed = %v/%v, want the amount — mileage carries no VAT and is always owed",
			created.NetAmount, created.OwedToEmployee)
	}
	if created.Currency != "NOK" {
		t.Errorf("currency = %q, want the installation's default", created.Currency)
	}
	if created.Category != nil || created.PaidBy != nil || created.Supplier != nil {
		t.Errorf("created = %+v, want no category, payer or supplier on a mileage line", created)
	}
}

func TestExpensesEntries_MileageCountsItsPassengers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signIn(t, h)

	created := createEntry(t, employee, mileageBody(map[string]any{
		"distanceKm": 12.3, "passengers": 2, "fromPlace": "Bergen", "toPlace": "Voss",
	}))
	if created.PassengerRate == nil || *created.PassengerRate != 1.00 {
		t.Errorf("passengerRate = %v, want the seeded 1.00", created.PassengerRate)
	}
	if created.Passengers == nil || *created.Passengers != 2 {
		t.Errorf("passengers = %v, want 2", created.Passengers)
	}
	// 12.3 × 5.30 = 65.19, plus 12.3 × 1.00 × 2 = 24.60.
	if created.GrossAmount != 89.79 {
		t.Errorf("grossAmount = %v, want 89.79", created.GrossAmount)
	}
	if created.FromPlace == nil || *created.FromPlace != "Bergen" || created.ToPlace == nil || *created.ToPlace != "Voss" {
		t.Errorf("places = %v → %v, want Bergen → Voss", created.FromPlace, created.ToPlace)
	}
}

// A mileage line on a day the table prices nothing cannot be recorded, and
// says so on the date rather than failing.
func TestExpensesEntries_MileageWithNoRateForTheDayIsRefusedOnTheDate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signIn(t, h)

	// The seeded rates start on 2026-01-01; the day before them has none.
	errs := refusedEntry(t, employee, http.MethodPost, entriesPath,
		mileageBody(map[string]any{"entryDate": "2025-12-31"}))
	if len(errs["entryDate"]) == 0 {
		t.Errorf("errors = %v, want one on entryDate", errs)
	}
}

// Passengers need a passenger rate in force; without one the line says so on
// the passengers rather than quietly counting them as free.
func TestExpensesEntries_PassengersWithNoPassengerRateAreRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	employee, _ := signIn(t, h)

	seeded := ratesOfKind(listRates(t, admin), "mileage_passenger")[0]
	if r := admin.Do(http.MethodDelete, ratePath(seeded.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete the passenger rate: status %d body %s, want 204", r.Status, r.Body)
	}

	errs := refusedEntry(t, employee, http.MethodPost, entriesPath, mileageBody(map[string]any{"passengers": 1}))
	if len(errs["passengers"]) == 0 {
		t.Errorf("errors = %v, want one on passengers", errs)
	}
	// Without passengers the same line is fine.
	createEntry(t, employee, mileageBody(nil))
}

// The per-field rules of design §3.1 and Global Constraints, each reported on
// its own field and all of them at once.
func TestExpensesEntries_RefusesAFieldTheKindCannotCarry(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signIn(t, h)

	for name, tc := range map[string]struct {
		body  map[string]any
		field string
	}{
		"no kind":                    {outlayBody(map[string]any{"kind": ""}), "kind"},
		"a kind nobody knows":        {outlayBody(map[string]any{"kind": "parking"}), "kind"},
		"a travel claim's per diem":  {outlayBody(map[string]any{"kind": "per_diem"}), "kind"},
		"no date":                    {outlayBody(map[string]any{"entryDate": nil}), "entryDate"},
		"no description":             {outlayBody(map[string]any{"description": "  "}), "description"},
		"a description over 500":     {outlayBody(map[string]any{"description": longText(501)}), "description"},
		"no category on an outlay":   {outlayBody(map[string]any{"categoryId": nil}), "categoryId"},
		"a category nobody has":      {outlayBody(map[string]any{"categoryId": 999999}), "categoryId"},
		"a supplier over 200":        {outlayBody(map[string]any{"supplier": longText(201)}), "supplier"},
		"no payer on an outlay":      {outlayBody(map[string]any{"paidBy": nil}), "paidBy"},
		"a payer nobody knows":       {outlayBody(map[string]any{"paidBy": "somebody"}), "paidBy"},
		"no currency on an outlay":   {outlayBody(map[string]any{"currency": nil}), "currency"},
		"a currency of two letters":  {outlayBody(map[string]any{"currency": "NO"}), "currency"},
		"no gross on an outlay":      {outlayBody(map[string]any{"grossAmount": nil}), "grossAmount"},
		"a gross of zero":            {outlayBody(map[string]any{"grossAmount": 0}), "grossAmount"},
		"a gross below zero":         {outlayBody(map[string]any{"grossAmount": -1}), "grossAmount"},
		"a gross of three decimals":  {outlayBody(map[string]any{"grossAmount": 10.005}), "grossAmount"},
		"a gross over the ceiling":   {outlayBody(map[string]any{"grossAmount": 10_000_000_000.0}), "grossAmount"},
		"VAT below zero":             {outlayBody(map[string]any{"vatAmount": -1}), "vatAmount"},
		"VAT over the gross":         {outlayBody(map[string]any{"vatAmount": 1250.01}), "vatAmount"},
		"VAT of three decimals":      {outlayBody(map[string]any{"vatAmount": 1.005}), "vatAmount"},
		"a distance on an outlay":    {outlayBody(map[string]any{"distanceKm": 10}), "distanceKm"},
		"a place on an outlay":       {outlayBody(map[string]any{"fromPlace": "Bergen"}), "fromPlace"},
		"passengers on an outlay":    {outlayBody(map[string]any{"passengers": 2}), "passengers"},
		"a claim on anything":        {outlayBody(map[string]any{"claimId": 1}), "claimId"},
		"no distance on mileage":     {mileageBody(map[string]any{"distanceKm": nil}), "distanceKm"},
		"a distance of zero":         {mileageBody(map[string]any{"distanceKm": 0}), "distanceKm"},
		"a distance over 9999.9":     {mileageBody(map[string]any{"distanceKm": 10000.0}), "distanceKm"},
		"a distance of two decimals": {mileageBody(map[string]any{"distanceKm": 1.25}), "distanceKm"},
		"passengers below zero":      {mileageBody(map[string]any{"passengers": -1}), "passengers"},
		"passengers over eight":      {mileageBody(map[string]any{"passengers": 9}), "passengers"},
		"a place over 200":           {mileageBody(map[string]any{"fromPlace": longText(201)}), "fromPlace"},
		"a category on mileage":      {mileageBody(map[string]any{"categoryId": materialsCategory}), "categoryId"},
		"a supplier on mileage":      {mileageBody(map[string]any{"supplier": "Shell"}), "supplier"},
		"a payer on mileage":         {mileageBody(map[string]any{"paidBy": "company"}), "paidBy"},
		"a gross on mileage":         {mileageBody(map[string]any{"grossAmount": 100}), "grossAmount"},
		"VAT on mileage":             {mileageBody(map[string]any{"vatAmount": 10}), "vatAmount"},
		"another currency on mileage": {
			mileageBody(map[string]any{"currency": "EUR"}), "currency",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			errs := refusedEntry(t, employee, http.MethodPost, entriesPath, tc.body)
			if len(errs[tc.field]) == 0 {
				t.Errorf("errors = %v, want one on %s", errs, tc.field)
			}
		})
	}
}

// Every failing field is reported, not just the first.
func TestExpensesEntries_ReportsEveryFailingFieldAtOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signIn(t, h)

	errs := refusedEntry(t, employee, http.MethodPost, entriesPath, outlayBody(map[string]any{
		"description": "", "paidBy": "nobody", "grossAmount": -5,
	}))
	for _, field := range []string{"description", "paidBy", "grossAmount"} {
		if len(errs[field]) == 0 {
			t.Errorf("errors = %v, want one on %s too", errs, field)
		}
	}
}

// A mileage line may carry the default currency explicitly; it is only another
// one that is refused.
func TestExpensesEntries_MileageAcceptsTheDefaultCurrencySpelledOut(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signIn(t, h)

	created := createEntry(t, employee, mileageBody(map[string]any{"currency": "nok"}))
	if created.Currency != "NOK" {
		t.Errorf("currency = %q, want it upper-cased", created.Currency)
	}
}

// A deactivated category stays on the lines that already have it and is
// refused on new ones (design §8).
func TestExpensesEntries_ADeactivatedCategoryIsRefusedOnNewLines(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	employee, _ := signIn(t, h)

	category := createCategory(t, admin, map[string]any{"name": "Ferge"})
	existing := createEntry(t, employee, outlayBody(map[string]any{"categoryId": category.Id}))
	updateCategory(t, admin, category.Id, map[string]any{"name": "Ferge", "active": false, "position": category.Position})

	errs := refusedEntry(t, employee, http.MethodPost, entriesPath, outlayBody(map[string]any{"categoryId": category.Id}))
	if len(errs["categoryId"]) == 0 {
		t.Errorf("errors = %v, want one on categoryId", errs)
	}
	// The line that already carries it still reads, and still saves.
	if got := getEntry(t, employee, existing.Id); got.Category == nil || got.Category.Id != category.Id {
		t.Errorf("category on the existing line = %+v, want it kept", got.Category)
	}
	updateEntry(t, employee, existing.Id, outlayBody(map[string]any{
		"categoryId": category.Id, "revision": existing.Revision,
	}))
}

func TestExpensesEntries_AnUpdateIsAFullReplaceGuardedByTheRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signIn(t, h)

	created := createEntry(t, employee, outlayBody(map[string]any{"supplier": "Elektrogrossisten AS"}))
	updated := updateEntry(t, employee, created.Id, outlayBody(map[string]any{
		"revision": created.Revision, "grossAmount": 800.00, "vatAmount": 160.00, "supplier": nil,
		"description": "Kabel, kontakter og et skjøteskinne",
	}))
	if updated.Revision != created.Revision+1 {
		t.Errorf("revision = %d, want it moved on from %d", updated.Revision, created.Revision)
	}
	if updated.GrossAmount != 800 || updated.NetAmount != 640 {
		t.Errorf("gross/net = %v/%v, want 800 and 640", updated.GrossAmount, updated.NetAmount)
	}
	if updated.Supplier != nil {
		t.Errorf("supplier = %v, want a replace to clear what was left out", updated.Supplier)
	}

	// The revision just used is stale now.
	r := employee.Do(http.MethodPut, entryPath(created.Id), outlayBody(map[string]any{"revision": created.Revision}))
	if r.Status != http.StatusConflict {
		t.Fatalf("a stale revision: status %d body %s, want 409", r.Status, r.Body)
	}
	var problem struct {
		Title  string `json:"title"`
		Status int32  `json:"status"`
	}
	r.JSON(&problem)
	if problem.Title != "Expense revision conflict" || problem.Status != http.StatusConflict {
		t.Errorf("problem = %+v, want the module's conflict problem", problem)
	}

	// A body with no revision at all is a field error, not a conflict against
	// a revision nobody read.
	errs := refusedEntry(t, employee, http.MethodPut, entryPath(created.Id), outlayBody(map[string]any{"revision": nil}))
	if len(errs["revision"]) == 0 {
		t.Errorf("errors = %v, want one on revision", errs)
	}
}

// A mileage line's amount follows the table on every save while it is a draft
// (decision X4 freezes it at submit, a later delivery).
func TestExpensesEntries_MileageIsRepricedOnEverySaveWhileDraft(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	employee, _ := signIn(t, h)

	created := createEntry(t, employee, mileageBody(nil))
	if created.GrossAmount != 636 {
		t.Fatalf("grossAmount = %v, want 120 × 5.30", created.GrossAmount)
	}
	createRate(t, admin, map[string]any{"kind": "mileage", "validFrom": "2026-03-01", "value": 6.00})

	updated := updateEntry(t, employee, created.Id, mileageBody(map[string]any{"revision": created.Revision}))
	if updated.Rate == nil || *updated.Rate != 6.00 || updated.GrossAmount != 720 {
		t.Errorf("rate/gross = %v/%v, want the new rate and 120 × 6.00", updated.Rate, updated.GrossAmount)
	}
}

func TestExpensesEntries_ADraftIsDeletedByItsOwner(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signIn(t, h)

	created := createEntry(t, employee, outlayBody(nil))
	if r := employee.Do(http.MethodDelete, entryPath(created.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete the draft: status %d body %s, want 204", r.Status, r.Body)
	}
	if r := employee.Do(http.MethodGet, entryPath(created.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("read the deleted entry: status %d, want 404", r.Status)
	}
	if r := employee.Do(http.MethodDelete, entryPath(created.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("delete it again: status %d, want 404", r.Status)
	}
}

func TestExpensesEntries_AnUnknownIdIsA404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	employee, _ := signIn(t, h)

	for _, tc := range []struct {
		method string
		body   any
	}{
		{http.MethodGet, nil},
		{http.MethodPut, outlayBody(map[string]any{"revision": 1})},
		{http.MethodDelete, nil},
	} {
		if r := employee.Do(tc.method, entryPath(999_999), tc.body); r.Status != http.StatusNotFound {
			t.Errorf("%s an entry nobody has: status %d body %s, want 404", tc.method, r.Status, r.Body)
		}
	}
}

// The period lock holds back every mutating path for everyone but
// expenses:manage, and an update is judged on the date it had as well as the
// one it is being given.
func TestExpensesEntries_ThePeriodLockHoldsBackEveryWriteButManages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	employee, _ := signIn(t, h)

	before := createEntry(t, employee, outlayBody(map[string]any{"entryDate": "2026-01-15"}))
	after := createEntry(t, employee, outlayBody(map[string]any{"entryDate": "2026-03-10"}))
	putSettings(t, admin, settingsBody(map[string]any{"lockedBefore": "2026-02-01"}))

	errs := refusedEntry(t, employee, http.MethodPost, entriesPath, outlayBody(map[string]any{"entryDate": "2026-01-20"}))
	if len(errs["entryDate"]) == 0 {
		t.Errorf("recording into a locked period: errors = %v, want one on entryDate", errs)
	}
	// The locked entry itself is no longer the employee's to change...
	if r := employee.Do(http.MethodPut, entryPath(before.Id),
		outlayBody(map[string]any{"revision": before.Revision, "entryDate": "2026-01-15"})); r.Status != http.StatusForbidden {
		t.Errorf("edit a locked entry: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := employee.Do(http.MethodDelete, entryPath(before.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("delete a locked entry: status %d body %s, want 403", r.Status, r.Body)
	}
	if locked := getEntry(t, employee, before.Id); locked.Capabilities.CanEdit || locked.Capabilities.CanDelete {
		t.Errorf("capabilities = %+v, want nothing on an entry before the lock", locked.Capabilities)
	}
	// ...and an open one may not be moved back into the locked period.
	errs = refusedEntry(t, employee, http.MethodPut, entryPath(after.Id),
		outlayBody(map[string]any{"revision": after.Revision, "entryDate": "2026-01-20"}))
	if len(errs["entryDate"]) == 0 {
		t.Errorf("moving an entry into a locked period: errors = %v, want one on entryDate", errs)
	}

	// expenses:manage works past it, on both sides of the date.
	updateEntry(t, admin, before.Id, outlayBody(map[string]any{
		"revision": before.Revision, "entryDate": "2026-01-15", "grossAmount": 99.00,
	}))
	createEntry(t, admin, outlayBody(map[string]any{"entryDate": "2026-01-20"}))
}

// The owner defaults to the caller; naming somebody else is expenses:manage's,
// and the person named must be one identity still knows.
func TestExpensesEntries_RecordingForAColleagueNeedsManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, adminID := signIn(t, h, "expenses:manage")
	_, colleagueID := signIn(t, h)
	employee, _ := signIn(t, h)

	errs := refusedEntry(t, employee, http.MethodPost, entriesPath,
		outlayBody(map[string]any{"userId": colleagueID.String()}))
	if len(errs["userId"]) == 0 {
		t.Errorf("an employee recording for somebody else: errors = %v, want one on userId", errs)
	}

	created := createEntry(t, admin, outlayBody(map[string]any{"userId": colleagueID.String()}))
	if created.Owner.UserId != colleagueID {
		t.Errorf("owner = %+v, want the colleague", created.Owner)
	}
	if created.Owner.UserId == adminID {
		t.Error("the recorder became the owner, want the person it concerns")
	}

	// A user nobody has, and one identity has disabled, are both refused.
	errs = refusedEntry(t, admin, http.MethodPost, entriesPath,
		outlayBody(map[string]any{"userId": uuid.New().String()}))
	if len(errs["userId"]) == 0 {
		t.Errorf("a user id nobody has: errors = %v, want one on userId", errs)
	}
	_, disabled := signIn(t, h)
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, disabled)
	errs = refusedEntry(t, admin, http.MethodPost, entriesPath,
		outlayBody(map[string]any{"userId": disabled.String()}))
	if len(errs["userId"]) == 0 {
		t.Errorf("a disabled user: errors = %v, want one on userId", errs)
	}
}

// expenses:manage edits and deletes anyone's draft; an ordinary colleague
// cannot even see it (entries_visibility_test.go), and the owner's own
// capabilities say so.
func TestExpensesEntries_ManageEditsAndDeletesSomebodyElsesDraft(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	employee, employeeID := signIn(t, h)

	created := createEntry(t, employee, outlayBody(nil))
	updated := updateEntry(t, admin, created.Id, outlayBody(map[string]any{
		"revision": created.Revision, "grossAmount": 42.00,
	}))
	if updated.Owner.UserId != employeeID {
		t.Errorf("owner = %+v, want an edit to leave the entry with its owner", updated.Owner)
	}
	if r := admin.Do(http.MethodDelete, entryPath(created.Id), nil); r.Status != http.StatusNoContent {
		t.Errorf("delete somebody else's draft as manage: status %d body %s, want 204", r.Status, r.Body)
	}
}

// longText is n characters, for the length rules.
func longText(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
