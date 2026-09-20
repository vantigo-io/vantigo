package expenses_test

import (
	"net/http"
	"testing"
)

// This file is decision X8's last sentence: an approver or an administrator may
// override the rate on one submitted mileage line, and the line records that,
// by whom, and what the table said. Employees cannot.

func TestExpensesRateOverride_RepricesTheLineAndRecordsWhoDidIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, approverID := signIn(t, h, "expenses:approve")

	mileage := createEntry(t, owner, mileageBody(map[string]any{"passengers": 2}))
	submitted := submitEntries(t, owner, mileage.Id)
	if submitted[0].GrossAmount != 876 {
		t.Fatalf("submitted = %+v, want it frozen at 876", submitted[0])
	}

	overridden := overrideRate(t, approver, mileage.Id, map[string]any{
		"rate": 8.00, "passengerRate": 2.00, "revision": submitted[0].Revision,
	})
	// 120 × 8.00 + 120 × 2.00 × 2 = 960 + 480.
	if overridden.GrossAmount != 1440 || overridden.Rate == nil || *overridden.Rate != 8.00 {
		t.Errorf("overridden = %+v, want it repriced to 1440 at 8.00", overridden)
	}
	if overridden.PassengerRate == nil || *overridden.PassengerRate != 2.00 {
		t.Errorf("passengerRate = %v, want the one given", overridden.PassengerRate)
	}
	if overridden.Status != "submitted" {
		t.Errorf("status = %q, want it still submitted", overridden.Status)
	}
	if overridden.RateOverride == nil {
		t.Fatalf("rateOverride = nil, want it recorded")
	}
	if overridden.RateOverride.ByUser.UserId != approverID {
		t.Errorf("rateOverride.byUser = %+v, want the approver", overridden.RateOverride.ByUser)
	}
	if overridden.RateOverride.TableValue == nil || *overridden.RateOverride.TableValue != 5.30 {
		t.Errorf("rateOverride.tableValue = %v, want the 5.30 the table said", overridden.RateOverride.TableValue)
	}

	// The owner sees it too — it is a fact about their expense.
	if seen := getEntry(t, owner, mileage.Id); seen.RateOverride == nil || seen.GrossAmount != 1440 {
		t.Errorf("the owner's copy = %+v, want the override shown", seen)
	}
}

// A second override does not overwrite what the table had said the first time.
func TestExpensesRateOverride_KeepsTheTableValueItFirstReplaced(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	mileage := createEntry(t, owner, mileageBody(nil))
	submitted := submitEntries(t, owner, mileage.Id)
	once := overrideRate(t, approver, mileage.Id, map[string]any{"rate": 8.00, "revision": submitted[0].Revision})
	twice := overrideRate(t, approver, mileage.Id, map[string]any{"rate": 9.00, "revision": once.Revision})

	if twice.RateOverride == nil || twice.RateOverride.TableValue == nil || *twice.RateOverride.TableValue != 5.30 {
		t.Errorf("rateOverride = %+v, want the table's own 5.30 still recorded", twice.RateOverride)
	}
	if twice.GrossAmount != 1080 {
		t.Errorf("gross = %v, want 120 × 9.00", twice.GrossAmount)
	}
}

// Design §5: overriding a rate needs approve (for that line) or manage. The
// owner as such cannot — but an owner who holds one of them may, because
// self-approval is allowed.
func TestExpensesRateOverride_IsNotTheOwnersUnlessTheyApprove(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approvingOwner, _ := signIn(t, h, "expenses:approve")

	mileage := createEntry(t, owner, mileageBody(nil))
	submitted := submitEntries(t, owner, mileage.Id)
	body := map[string]any{"rate": 8.00, "revision": submitted[0].Revision}
	if r := owner.Do(http.MethodPut, entryRatePath(mileage.Id), body); r.Status != http.StatusForbidden {
		t.Errorf("the owner overriding: status %d body %s, want 403", r.Status, r.Body)
	}
	if got := getEntry(t, owner, mileage.Id); got.Capabilities.CanOverrideRate {
		t.Errorf("canOverrideRate = true for the owner, want false")
	}

	own := createEntry(t, approvingOwner, mileageBody(nil))
	mine := submitEntries(t, approvingOwner, own.Id)
	overrideRate(t, approvingOwner, own.Id, map[string]any{"rate": 8.00, "revision": mine[0].Revision})
}

// A project's manager overrides on their own project's lines, and nowhere else.
func TestExpensesRateOverride_IsAProjectManagersOnTheirOwnProject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signIn(t, h)
	h.projects.addRole(projectKraftVerket, ownerID, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	stranger, _ := signInAs(t, h, projectEuro, roleManager)

	mileage := createEntry(t, owner, mileageBody(map[string]any{"projectId": projectKraftVerket}))
	submitted := submitEntries(t, owner, mileage.Id)
	body := map[string]any{"rate": 8.00, "revision": submitted[0].Revision}

	// A manager of another project cannot even see it.
	if r := stranger.Do(http.MethodPut, entryRatePath(mileage.Id), body); r.Status != http.StatusNotFound {
		t.Errorf("a stranger overriding: status %d body %s, want the unknown id's 404", r.Status, r.Body)
	}
	overrideRate(t, manager, mileage.Id, body)
}

// Only a submitted mileage line: an outlay has no rate, and a draft or an
// approved line is not where a decision is being made.
func TestExpensesRateOverride_IsForSubmittedMileageOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	outlay := createEntry(t, owner, outlayBody(nil))
	outlaySubmitted := submitEntries(t, owner, outlay.Id)
	errs := refusedEntry(t, approver, http.MethodPut, entryRatePath(outlay.Id),
		map[string]any{"rate": 8.00, "revision": outlaySubmitted[0].Revision})
	if len(errs["kind"]) == 0 {
		t.Errorf("errors = %v, want one on kind — an outlay carries no rate", errs)
	}

	draft := createEntry(t, owner, mileageBody(nil))
	errs = refusedEntry(t, approver, http.MethodPut, entryRatePath(draft.Id),
		map[string]any{"rate": 8.00, "revision": draft.Revision})
	if len(errs["status"]) == 0 {
		t.Errorf("errors = %v, want one on status — a draft is still following the table", errs)
	}

	approved := createEntry(t, owner, mileageBody(map[string]any{"description": "Godkjent tur"}))
	approvedBy(t, owner, approver, approved.Id)
	errs = refusedEntry(t, approver, http.MethodPut, entryRatePath(approved.Id),
		map[string]any{"rate": 8.00, "revision": getEntry(t, owner, approved.Id).Revision})
	if len(errs["status"]) == 0 {
		t.Errorf("errors = %v, want one on status — the decision has been made", errs)
	}
}

func TestExpensesRateOverride_HoldsTheRateToItsOwnRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	mileage := createEntry(t, owner, mileageBody(nil))
	submitted := submitEntries(t, owner, mileage.Id)
	revision := submitted[0].Revision

	for name, tc := range map[string]struct {
		body  map[string]any
		field string
	}{
		"no rate at all":     {map[string]any{"revision": revision}, "rate"},
		"zero":               {map[string]any{"rate": 0, "revision": revision}, "rate"},
		"negative":           {map[string]any{"rate": -1.00, "revision": revision}, "rate"},
		"a third decimal":    {map[string]any{"rate": 5.305, "revision": revision}, "rate"},
		"no revision":        {map[string]any{"rate": 8.00}, "revision"},
		"a passenger rate":   {map[string]any{"rate": 8.00, "passengerRate": 2.00, "revision": revision}, "passengerRate"},
		"a negative one too": {map[string]any{"rate": 8.00, "passengerRate": -1.00, "revision": revision}, "passengerRate"},
	} {
		t.Run(name, func(t *testing.T) {
			errs := refusedEntry(t, approver, http.MethodPut, entryRatePath(mileage.Id), tc.body)
			if len(errs[tc.field]) == 0 {
				t.Fatalf("errors = %v, want one on %s", errs, tc.field)
			}
		})
	}
}

// The revision guard: an override against a revision that has moved on is a
// 409, exactly as a replace is.
func TestExpensesRateOverride_IsGuardedByTheRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	mileage := createEntry(t, owner, mileageBody(nil))
	submitted := submitEntries(t, owner, mileage.Id)
	overrideRate(t, approver, mileage.Id, map[string]any{"rate": 8.00, "revision": submitted[0].Revision})

	r := approver.Do(http.MethodPut, entryRatePath(mileage.Id),
		map[string]any{"rate": 9.00, "revision": submitted[0].Revision})
	if r.Status != http.StatusConflict {
		t.Errorf("status %d body %s, want 409", r.Status, r.Body)
	}
}

// The period lock holds the override back like every other write.
func TestExpensesRateOverride_ThePeriodLockHoldsItBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin, _ := signIn(t, h, "expenses:manage")
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	mileage := createEntry(t, owner, mileageBody(nil))
	submitted := submitEntries(t, owner, mileage.Id)
	putSettings(t, admin, settingsBody(map[string]any{"lockedBefore": "2026-04-01"}))

	body := map[string]any{"rate": 8.00, "revision": submitted[0].Revision}
	errs := refusedEntry(t, approver, http.MethodPut, entryRatePath(mileage.Id), body)
	if len(errs["entryDate"]) == 0 {
		t.Errorf("errors = %v, want one naming the lock", errs)
	}
	// expenses:manage works past it.
	overrideRate(t, admin, mileage.Id, body)
}

// An edit undoes an override, because a rejected line is a draft again and a
// draft follows the table.
func TestExpensesRateOverride_IsUndoneByAnEdit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	mileage := createEntry(t, owner, mileageBody(nil))
	submitted := submitEntries(t, owner, mileage.Id)
	overrideRate(t, approver, mileage.Id, map[string]any{"rate": 8.00, "revision": submitted[0].Revision})
	rejectEntries(t, approver, "Feil rate", mileage.Id)

	current := getEntry(t, owner, mileage.Id)
	edited := updateEntry(t, owner, mileage.Id, mileageBody(map[string]any{"revision": current.Revision}))
	if edited.RateOverride != nil {
		t.Errorf("rateOverride = %+v, want it cleared by the edit", edited.RateOverride)
	}
	if edited.GrossAmount != 636 {
		t.Errorf("gross = %v, want the table's own 636 back", edited.GrossAmount)
	}
}

// The audit records what the table said about *both* rates an override can
// replace — decision X8 asks the line to record what the table said, and the
// passenger supplement is the half of it worth the most money on a full car.
func TestExpensesRateOverride_RecordsTheTablesPassengerSupplementToo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	mileage := createEntry(t, owner, mileageBody(map[string]any{"passengers": 3}))
	submitted := submitEntries(t, owner, mileage.Id)
	// 120 × 5.30 + 120 × 1.00 × 3 = 636 + 360.
	if submitted[0].GrossAmount != 996 {
		t.Fatalf("submitted = %+v, want it frozen at 996", submitted[0])
	}

	// Taking the supplement away is worth 360 kroner; the line records what it
	// took away.
	overridden := overrideRate(t, approver, mileage.Id, map[string]any{
		"rate": 5.30, "passengerRate": 0, "revision": submitted[0].Revision,
	})
	if overridden.GrossAmount != 636 {
		t.Errorf("gross = %v, want the supplement gone", overridden.GrossAmount)
	}
	audit := overridden.RateOverride
	if audit == nil || audit.TableValue == nil || *audit.TableValue != 5.30 {
		t.Fatalf("rateOverride = %+v, want the table's 5.30 recorded", audit)
	}
	if audit.PassengerTableValue == nil || *audit.PassengerTableValue != 1.00 {
		t.Errorf("passengerTableValue = %v, want the table's 1.00 recorded", audit.PassengerTableValue)
	}
}

// A rate override that leaves the supplement alone records nothing about it:
// absent, never a repeat of the value still on the line.
func TestExpensesRateOverride_RecordsNoPassengerSupplementItDidNotTouch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	mileage := createEntry(t, owner, mileageBody(map[string]any{"passengers": 3}))
	submitted := submitEntries(t, owner, mileage.Id)
	overridden := overrideRate(t, approver, mileage.Id,
		map[string]any{"rate": 8.00, "revision": submitted[0].Revision})

	if overridden.RateOverride == nil || overridden.RateOverride.PassengerTableValue != nil {
		t.Errorf("rateOverride = %+v, want no passenger audit — the supplement was not touched",
			overridden.RateOverride)
	}
	// 120 × 8.00 + 120 × 1.00 × 3, the supplement kept as it was frozen.
	if overridden.GrossAmount != 1320 {
		t.Errorf("gross = %v, want the kept supplement still in it", overridden.GrossAmount)
	}
}

// An unapprove makes a *fresh* draft: it carries nothing of the decision that
// was undone, the overridden rate's audit included. The figures stay until the
// next submit reprices them, and that submit clears the audit too — so an
// override never outlives the submission it was made on.
func TestExpensesRateOverride_IsForgottenByAnUnapprove(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	mileage := createEntry(t, owner, mileageBody(map[string]any{"passengers": 3}))
	submitted := submitEntries(t, owner, mileage.Id)
	overridden := overrideRate(t, approver, mileage.Id, map[string]any{
		"rate": 8.00, "passengerRate": 2.00, "revision": submitted[0].Revision,
	})
	// 120 × 8.00 + 120 × 2.00 × 3.
	if overridden.GrossAmount != 1680 || overridden.RateOverride == nil {
		t.Fatalf("overridden = %+v, want 1680 with the override recorded", overridden)
	}
	approveEntries(t, approver, mileage.Id)

	back := unapproveEntries(t, approver, mileage.Id)
	if back[0].Status != "draft" {
		t.Fatalf("unapproved = %+v, want a draft", back[0])
	}
	if back[0].RateOverride != nil {
		t.Errorf("rateOverride = %+v, want a fresh draft to carry nothing of the old decision",
			back[0].RateOverride)
	}
	// The figures are still the ones it was approved at, until something
	// recomputes them.
	if back[0].GrossAmount != 1680 {
		t.Errorf("gross = %v, want the frozen figures left where they were", back[0].GrossAmount)
	}
	if n := h.Count(t, `SELECT count(*) FROM expenses.entries WHERE id = $1
		AND rate_overridden_by_user_id IS NULL AND rate_table_value IS NULL
		AND passenger_rate_table_value IS NULL`, mileage.Id); n != 1 {
		t.Errorf("the audit columns were not cleared: %s", entryColumnsDump(t, h, mileage.Id))
	}

	again := submitEntries(t, owner, mileage.Id)
	if again[0].RateOverride != nil {
		t.Errorf("rateOverride = %+v, want nothing after the resubmit either", again[0].RateOverride)
	}
	if again[0].GrossAmount != 996 || again[0].Rate == nil || *again[0].Rate != 5.30 {
		t.Errorf("resubmitted = %+v, want it repriced from the table (996 at 5.30)", again[0])
	}
}

// The other half of the same rule: a rejected line's override is cleared by the
// submit that reprices it, so the audit can never describe a rate the line no
// longer carries.
func TestExpensesRateOverride_IsForgottenByTheNextSubmit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	mileage := createEntry(t, owner, mileageBody(nil))
	submitted := submitEntries(t, owner, mileage.Id)
	overrideRate(t, approver, mileage.Id, map[string]any{"rate": 8.00, "revision": submitted[0].Revision})
	rejectEntries(t, approver, "Feil rate", mileage.Id)

	// A rejection decides nothing about the rate, so the audit is still there.
	if got := getEntry(t, owner, mileage.Id); got.RateOverride == nil || got.GrossAmount != 960 {
		t.Fatalf("rejected = %+v, want the override still on it", got)
	}
	again := submitEntries(t, owner, mileage.Id)
	if again[0].RateOverride != nil {
		t.Errorf("rateOverride = %+v, want the submit to have cleared it", again[0].RateOverride)
	}
	if again[0].GrossAmount != 636 {
		t.Errorf("gross = %v, want it repriced from the table", again[0].GrossAmount)
	}
}

// The queue counts the lines whose rate an approver has changed, so the next
// one can see it at a glance.
func TestExpensesRateOverride_IsCountedInTheQueue(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	overridden := createEntry(t, owner, mileageBody(nil))
	plain := createEntry(t, owner, mileageBody(map[string]any{"description": "Uendret tur"}))
	submitted := submitEntries(t, owner, overridden.Id, plain.Id)
	overrideRate(t, approver, overridden.Id, map[string]any{"rate": 8.00, "revision": submitted[0].Revision})

	group := getApprovals(t, approver, "").Data[0]
	if group.OverriddenRates != 1 {
		t.Errorf("overriddenRates = %d, want the one line whose rate was changed", group.OverriddenRates)
	}
}

// Decisions X1 and X2: the override is the same override without the projects
// module — expenses:approve and expenses:manage decide it.
func TestExpensesRateOverride_WithoutProjects_IsTheSameOverride(t *testing.T) {
	t.Parallel()
	h := newHarnessWithoutProjects(t)
	owner, _ := signIn(t, h)
	approver, _ := signIn(t, h, "expenses:approve")

	mileage := createEntry(t, owner, mileageBody(nil))
	submitted := submitEntries(t, owner, mileage.Id)
	overridden := overrideRate(t, approver, mileage.Id, map[string]any{"rate": 8.00, "revision": submitted[0].Revision})
	if overridden.GrossAmount != 960 || overridden.RateOverride == nil {
		t.Errorf("overridden = %+v, want 120 × 8.00 with the override recorded", overridden)
	}
}

// An expense the caller may not see answers the unknown id's bare 404.
func TestExpensesRateOverride_AnUnknownIdIsA404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	approver, _ := signIn(t, h, "expenses:approve")

	r := approver.Do(http.MethodPut, entryRatePath(987654), map[string]any{"rate": 8.00, "revision": 1})
	if r.Status != http.StatusNotFound || len(r.Body) != 0 {
		t.Errorf("status %d body %s, want a bare 404", r.Status, r.Body)
	}
}
