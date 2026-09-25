package timetracking_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// An entry and its work type (work types design D3): checked and snapshotted
// at every save while draft or rejected, frozen from submit, the base rates
// left as the chain resolved them, and the multipliers shaped with the blocks
// they multiply. actuals_test.go carries what the sums make of it.

// snapshotOf is an entry's four work-type columns as stored, "-" for NULL.
func snapshotOf(t *testing.T, h *harness, id int64) string {
	t.Helper()
	return modtest.One[string](t, h.Harness, `
		SELECT coalesce(work_type_id::text, '-') || '|' || coalesce(work_type_name, '-') || '|' ||
		       coalesce(bill_multiplier_percent::text, '-') || '|' || coalesce(cost_multiplier_percent::text, '-')
		FROM time.entries WHERE id = $1`, id)
}

// D3's snapshot: the type's id and name, its multipliers beside the rates,
// the stored rates the base ones, rateSource untouched — the type multiplies
// whatever step won — and the effective rates for display.
func TestPostTimeEntries_SnapshotsTheWorkTypeBesideTheBaseRates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember, "time:manage")
	seedRate(t, h, ownerID, "2026-01-01", nil, 400.0, "NOK")

	e := createEntry(t, owner, map[string]any{"workTypeId": workTypeOvertime})

	if e.WorkType == nil || e.WorkType.Id != workTypeOvertime || e.WorkType.Name != workTypeOvertimeName {
		t.Errorf("workType = %+v, want %d %q", e.WorkType, workTypeOvertime, workTypeOvertimeName)
	}
	if e.RateSource != "project" {
		t.Errorf("rateSource = %q, want project: the work type is orthogonal to the chain", e.RateSource)
	}
	if b := e.Billing; b == nil || deref(b.BillRate) != 900.0 || deref(b.MultiplierPercent) != 150.0 || deref(b.EffectiveRate) != 1350.0 {
		t.Errorf("billing = %+v, want 900 at 150 %%, 1350 an hour", e.Billing)
	}
	if c := e.Cost; c == nil || deref(c.CostRate) != 400.0 || deref(c.MultiplierPercent) != 140.0 || deref(c.EffectiveRate) != 560.0 {
		t.Errorf("cost = %+v, want 400 at 140 %%, 560 an hour", e.Cost)
	}
	if got := snapshotOf(t, h, e.Id); got != "6001|Overtid 50 %|150.00|140.00" {
		t.Errorf("snapshot = %q", got)
	}
	if got := modtest.One[string](t, h.Harness, `SELECT bill_rate::text || '|' || cost_rate::text FROM time.entries WHERE id = $1`, e.Id); got != "900.00|400.00" {
		t.Errorf("stored rates = %q, want the base rates 900.00|400.00", got)
	}
}

// The effective rate is display, rounded half up once: 333.33 at 150 % is
// 499.995, shown 500.00. What the hours are worth is multiplied where it is
// summed (actuals_test.go): 749.99, not 1.5 × 500.00.
func TestPostTimeEntries_TheEffectiveRateIsForDisplay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectEuro, roleMember)
	seedRate(t, h, ownerID, "2026-01-01", 333.33, nil, "EUR")

	e := createEntry(t, owner, map[string]any{"projectId": projectEuro, "hours": 1.5, "workTypeId": workTypeEuro})
	if e.RateSource != "person" || e.Billing == nil || deref(e.Billing.BillRate) != 333.33 || deref(e.Billing.EffectiveRate) != 500.0 {
		t.Errorf("entry = %s %+v, want the person's 333.33 shown as 500.00 an hour", e.RateSource, e.Billing)
	}
	got, err := actualsProvider(t, h).Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectEuro, Currency: ptr("EUR")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	wantBucket(t, "draft", got.Totals.Draft, 150, "749.99", "0.00")
}

// D3's refusals, on the field: a type nobody has and one on another project
// are the one message, a retired one its own — on a create and an update.
func TestTimeEntries_RefuseAWorkTypeNotOnTheProjectOrRetired(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)

	for id, want := range map[int32]string{
		workTypeEuro:    "Work type is not on this project",
		workTypeUnknown: "Work type is not on this project",
		workTypeRetired: "Work type is no longer active",
	} {
		errs := fieldErrors(t, owner, entryBody(map[string]any{"workTypeId": id}))
		if !slices.Equal(errs["workTypeId"], []string{want}) {
			t.Errorf("workTypeId %d: errors %v, want %q", id, errs, want)
		}
	}

	e := createEntry(t, owner, nil)
	r := owner.Do(http.MethodPut, entryPath(e.Id), updateBody(e, map[string]any{"workTypeId": workTypeRetired}))
	var problem validationProblemJSON
	r.JSON(&problem)
	if r.Status != http.StatusBadRequest || !slices.Equal(problem.Errors["workTypeId"], []string{"Work type is no longer active"}) {
		t.Errorf("update to a retired type: status %d body %s, want 400 on workTypeId", r.Status, r.Body)
	}
}

// Ordinary hours: no workType key, no multiplier in either block, four NULLs;
// and a full replace that leaves workTypeId out takes a type off again.
func TestTimeEntries_WithoutAWorkType_StoreAndAnswerNone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember, "time:manage")
	seedRate(t, h, ownerID, "2026-01-01", nil, 400.0, "NOK")

	e := createEntry(t, owner, nil)
	raw := rawEntry(t, owner, e.Id)
	if _, ok := raw["workType"]; ok {
		t.Errorf("workType = %v, want the key absent", raw["workType"])
	}
	for _, block := range []string{"billing", "cost"} {
		fields, _ := raw[block].(map[string]any)
		for _, key := range []string{"multiplierPercent", "effectiveRate"} {
			if _, ok := fields[key]; ok {
				t.Errorf("%s.%s = %v, want it absent for ordinary hours", block, key, fields[key])
			}
		}
	}
	if got := snapshotOf(t, h, e.Id); got != "-|-|-|-" {
		t.Errorf("snapshot = %q, want four NULLs", got)
	}

	typed := createEntry(t, owner, map[string]any{"entryDate": "2026-09-15", "workTypeId": workTypeOvertime})
	cleared := updateEntry(t, owner, typed, map[string]any{"workTypeId": nil})
	if cleared.WorkType != nil || snapshotOf(t, h, typed.Id) != "-|-|-|-" {
		t.Errorf("after a replace without workTypeId: %+v, snapshot %q, want ordinary hours", cleared.WorkType, snapshotOf(t, h, typed.Id))
	}
}

// D3 on a non-billable entry: it keeps its cost multiplier — overtime costs
// the company whether or not it bills — and bills nothing. Both multipliers
// are snapshotted; the billing block carries the bill one and no effective
// rate, since the chain gives a non-billable entry no rate to multiply; and
// actuals cost the hours multiplied and bill them at 0.00.
func TestWorkTypes_ANonBillableEntryKeepsItsCostMultiplier(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember, "time:manage")
	seedRate(t, h, ownerID, "2026-01-01", nil, 400.0, "NOK")

	e := createEntry(t, owner, map[string]any{"billable": false, "workTypeId": workTypeOvertime})

	if got := snapshotOf(t, h, e.Id); got != "6001|Overtid 50 %|150.00|140.00" {
		t.Errorf("snapshot = %q, want both multipliers kept", got)
	}
	if c := e.Cost; c == nil || deref(c.MultiplierPercent) != 140.0 || deref(c.EffectiveRate) != 560.0 {
		t.Errorf("cost = %+v, want 400 at 140 %%, 560 an hour", e.Cost)
	}
	raw := rawEntry(t, owner, e.Id)
	billing, ok := raw["billing"].(map[string]any)
	if !ok {
		t.Fatalf("billing = %v, want the owner's block", raw["billing"])
	}
	if _, has := billing["effectiveRate"]; has {
		t.Errorf("billing.effectiveRate = %v, want it absent: a non-billable entry has no rate to multiply", billing["effectiveRate"])
	}
	if billing["multiplierPercent"] != 150.0 {
		t.Errorf("billing.multiplierPercent = %v, want the snapshotted 150", billing["multiplierPercent"])
	}

	got, err := actualsProvider(t, h).Actuals(t.Context(), contracts.ActualsRequest{ProjectID: projectKraftVerket, Currency: ptr("NOK")})
	if err != nil {
		t.Fatalf("actuals: %v", err)
	}
	// 2 h × 400 × 140 % = 1120.00; nothing is billed.
	wantBucket(t, "draft", got.Totals.Draft, 200, "0.00", "1120.00")
}

// D3's freeze: a multiplier changed in projects after submit does not move a
// submitted entry; a rejected entry is a draft again, and its next save
// snapshots the type as it now stands.
func TestWorkTypes_FrozenFromSubmit_AndSnapshottedAgainWhenRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	approver, _ := signIn(t, h, "time:approve")
	e := submittedEntry(t, owner, map[string]any{"workTypeId": workTypeOvertime})

	h.projects.setWorkType(workTypeOvertime, "Overtid", 175, 160)
	frozen := getEntry(t, owner, e.Id)
	if frozen.WorkType == nil || frozen.WorkType.Name != workTypeOvertimeName || deref(frozen.Billing.MultiplierPercent) != 150.0 {
		t.Errorf("submitted entry = %+v %+v, want the snapshot it was submitted with", frozen.WorkType, frozen.Billing)
	}
	if got := snapshotOf(t, h, e.Id); got != "6001|Overtid 50 %|150.00|140.00" {
		t.Errorf("snapshot = %q, want it untouched", got)
	}

	rejected := rejectEntries(t, approver, "Wrong day", e.Id)[0]
	resaved := updateEntry(t, owner, rejected, nil)
	if resaved.WorkType == nil || resaved.WorkType.Name != "Overtid" ||
		deref(resaved.Billing.MultiplierPercent) != 175.0 || deref(resaved.Billing.EffectiveRate) != 1575.0 {
		t.Errorf("resaved = %+v %+v, want the type as it now stands: Overtid, 175 %%, 1575", resaved.WorkType, resaved.Billing)
	}
	if got := snapshotOf(t, h, e.Id); got != "6001|Overtid|175.00|160.00" {
		t.Errorf("snapshot = %q, want it taken again", got)
	}
}

// D8's shaping, carried over: the type is on everyone's copy; the bill
// multiplier travels inside billing (owner, project manager), the cost one
// inside cost (time:view-all), each absent where its block is.
func TestWorkTypes_TheMultipliersAreShapedWithTheirBlocks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	viewAll, _ := signIn(t, h, "time:view-all")
	seedRate(t, h, ownerID, "2026-01-01", nil, 400.0, "NOK")
	e := createEntry(t, owner, map[string]any{"workTypeId": workTypeOvertime})

	for name, c := range map[string]*modtest.Client{"owner": owner, "manager": manager, "time:view-all": viewAll} {
		raw := rawEntry(t, c, e.Id)
		workType, _ := raw["workType"].(map[string]any)
		if workType["name"] != workTypeOvertimeName {
			t.Errorf("%s: workType = %v, want it on every copy", name, raw["workType"])
		}
		billing, seesBilling := raw["billing"].(map[string]any)
		cost, seesCost := raw["cost"].(map[string]any)
		switch name {
		case "owner", "manager":
			if !seesBilling || billing["multiplierPercent"] != 150.0 || seesCost {
				t.Errorf("%s: billing %v cost %v, want the bill multiplier and no cost block", name, raw["billing"], raw["cost"])
			}
		case "time:view-all":
			if seesBilling || !seesCost || cost["multiplierPercent"] != 140.0 || cost["effectiveRate"] != 560.0 {
				t.Errorf("%s: billing %v cost %v, want the cost multiplier and no billing block", name, raw["billing"], raw["cost"])
			}
		}
	}
}

// The approval queue renders entries through the same code, so its entries
// carry the type.
func TestGetTimeApprovals_CarriesTheWorkType(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	approver, _ := signIn(t, h, "time:approve")
	e := submittedEntry(t, owner, map[string]any{"workTypeId": workTypeWeekend})

	var found *entryJSON
	for _, g := range getApprovals(t, approver, "").Data {
		for i := range g.Entries {
			if g.Entries[i].Id == e.Id {
				found = &g.Entries[i]
			}
		}
	}
	if found == nil || found.WorkType == nil || found.WorkType.Id != workTypeWeekend || found.WorkType.Name != workTypeWeekendName {
		t.Errorf("queued entry = %+v, want it carrying %q", found, workTypeWeekendName)
	}
}

// The project summary's billed amount multiplies where it sums too: 2 h at
// 900 × 150 % and 1 h at 900 is 3600.
func TestGetTimeProjectSummary_BillsTheWorkTypesMultiplier(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := signInAs(t, h, projectKraftVerket, roleMember)
	manager, _ := signInAs(t, h, projectKraftVerket, roleManager)
	submittedEntry(t, owner, map[string]any{"hours": 2, "workTypeId": workTypeOvertime})
	submittedEntry(t, owner, map[string]any{"hours": 1, "entryDate": "2026-09-15"})

	s, _ := readProjectSummary(t, manager, projectKraftVerket)
	if s.Billing == nil || s.Billing.Amount != 3600 {
		t.Errorf("billing = %+v, want 3600", s.Billing)
	}
}
