package manufacturing_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The module's reads beside its records (M7, M8, M10).
func TestMeta_StatsSitesAndVariantSearch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	all := h.SignIn(t, everything...)
	read := h.SignIn(t, reader...)

	var meta gen.ManufacturingMeta
	res := all.Do(http.MethodGet, base+"/meta", nil)
	mustStatus(t, res, http.StatusOK, "meta")
	res.JSON(&meta)
	if !meta.CanManageBoms || !meta.CanSeeCosts || !meta.CanManageOrders || !meta.CanWorkShopFloor || meta.Currency != "NOK" {
		t.Errorf("meta of everything = %+v", meta)
	}
	read.Do(http.MethodGet, base+"/meta", nil).JSON(&meta)
	if meta.CanManageBoms || meta.CanSeeCosts || meta.CanManageOrders || meta.CanWorkShopFloor {
		t.Errorf("meta of a reader = %+v, want nothing but access", meta)
	}

	var sites gen.ManufacturingSiteList
	res = read.Do(http.MethodGet, base+"/sites", nil)
	mustStatus(t, res, http.StatusOK, "sites")
	res.JSON(&sites)
	if len(sites.Items) != 2 || sites.Items[0].Code != "BRANCH" || sites.Items[1].RootLocationId != 10 {
		t.Errorf("sites = %+v, want BRANCH and MAIN, the archived one left out", sites.Items)
	}

	wc := createWorkCenter(t, all, workCenterInput("SAW", siteMain))
	createBom(t, all, bomInput(varFrame, "1", line(varTube, "2")))
	h.Exec(t, `UPDATE manufacturing.work_centers SET working_state = 'blocked' WHERE id = $1`, wc.Id)
	var stats gen.ManufacturingStats
	res = read.Do(http.MethodGet, base+"/stats", nil)
	mustStatus(t, res, http.StatusOK, "stats")
	res.JSON(&stats)
	if stats.ActiveWorkCenters != 1 || stats.BlockedWorkCenters != 1 || stats.ActiveBoms != 1 {
		t.Errorf("stats = %+v", stats)
	}

	// Goods of active products only, by SKU, with hasBom; the standard cost
	// only for costs-view.
	var found gen.ManufacturingVariantSearchList
	res = read.Do(http.MethodGet, base+"/variants?q=", nil)
	mustStatus(t, res, http.StatusOK, "variant search")
	res.JSON(&found)
	var skus []string
	for _, v := range found.Items {
		skus = append(skus, v.Sku)
		if v.StandardCost != nil {
			t.Errorf("a reader sees %s's standard cost %s", v.Sku, v.StandardCost)
		}
		if v.HasBom != (v.Id == varFrame) {
			t.Errorf("%s hasBom = %v", v.Sku, v.HasBom)
		}
	}
	if want := []string{"BIKE-1", "BOLT-1", "FRAME-1", "PAINT-1", "TUBE-1", "WHEEL-1"}; !slices.Equal(skus, want) {
		t.Errorf("search = %v, want %v", skus, want)
	}
	res = h.SignIn(t, costReader...).Do(http.MethodGet, base+"/variants?q=wheel&limit=5", nil)
	res.JSON(&found)
	if len(found.Items) != 1 || string(found.Items[0].StandardCost) != `"150.50"` {
		t.Errorf("search wheel with costs = %+v", found.Items)
	}
	res = read.Do(http.MethodGet, base+"/variants?q=e&limit=1", nil)
	res.JSON(&found)
	if len(found.Items) != 1 {
		t.Errorf("limit 1 answered %d", len(found.Items))
	}
}

// TestWorkCenter_CreateDefaultsAndReads is M8 and M9's defaults: capacity 1,
// efficiency 100, no setup, the default pattern, the code upper-cased, the
// site named.
func TestWorkCenter_CreateDefaultsAndReads(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	in := workCenterInput(" saw1 ", siteMain)
	w := createWorkCenter(t, c, in)
	if w.Code != "SAW1" || w.Capacity != 1 || w.TimeEfficiency != 100 || w.SetupMinutes != "0" || w.CleanupMinutes != "0" ||
		w.SiteCode != "MAIN" || w.SiteName != "Main" || w.WorkingState != gen.ManufacturingWorkingStateNormal || w.Status != gen.ManufacturingStatusActive || w.Revision != 1 {
		t.Errorf("created = %+v", w)
	}
	if len(w.Pattern.Monday) != 1 || w.Pattern.Monday[0] != (gen.ManufacturingInterval{Start: "07:00", End: "15:00"}) ||
		len(w.Pattern.Friday) != 1 || len(w.Pattern.Saturday) != 0 || len(w.Pattern.Sunday) != 0 {
		t.Errorf("default pattern = %+v", w.Pattern)
	}
	if w.MachineCostPerHour != nil || w.LabourCostPerHour != nil {
		t.Errorf("a manager without costs-view sees the rates: %s %s", w.MachineCostPerHour, w.LabourCostPerHour)
	}

	other := createWorkCenter(t, c, workCenterInput("PAINT", siteBranch))
	res := c.Do(http.MethodPost, workCenterPath(other.Id, "archive"), revisionInput(other.Revision))
	mustStatus(t, res, http.StatusOK, "archive")

	var list gen.ManufacturingWorkCenterList
	res = c.Do(http.MethodGet, workCentersPath, nil)
	mustStatus(t, res, http.StatusOK, "list")
	res.JSON(&list)
	if len(list.Items) != 1 || list.Items[0].Code != "SAW1" {
		t.Errorf("active list = %+v", list.Items)
	}
	c.Do(http.MethodGet, workCentersPath+"?status=all", nil).JSON(&list)
	if len(list.Items) != 2 || list.Items[0].Code != "PAINT" {
		t.Errorf("all, by code = %+v", list.Items)
	}
	c.Do(http.MethodGet, workCentersPath+"?status=archived&siteId=2&q=pai", nil).JSON(&list)
	if len(list.Items) != 1 || list.Items[0].Id != other.Id {
		t.Errorf("archived at the branch matching pai = %+v", list.Items)
	}
	c.Do(http.MethodGet, fmt.Sprintf("%s?status=all&siteId=%d&q=center", workCentersPath, siteMain), nil).JSON(&list)
	if len(list.Items) != 1 || list.Items[0].Id != w.Id {
		t.Errorf("at main matching the name = %+v", list.Items)
	}

	res = h.SignIn(t, reader...).Do(http.MethodGet, workCenterPath(w.Id), nil)
	mustStatus(t, res, http.StatusOK, "get")
	mustStatus(t, c.Do(http.MethodGet, workCenterPath(999999), nil), http.StatusNotFound, "get unknown")
	mustStatus(t, h.SignIn(t, reader...).Do(http.MethodPost, workCentersPath, workCenterInput("X", siteMain)), http.StatusForbidden, "create as a reader")
}

// TestWorkCenter_Validation is M8's field rules and M9's pattern rules, each
// on its own field, every failure at once.
func TestWorkCenter_Validation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	offContract := modtest.SkipContract("out of the contract's range on purpose")

	for _, tc := range []struct {
		name   string
		mutate func(*gen.ManufacturingWorkCenterInput)
		fields []string
		skip   bool
	}{
		{"code not A-Z0-9", func(in *gen.ManufacturingWorkCenterInput) { in.Code = "SAW-1" }, []string{"code"}, false},
		{"blank name", func(in *gen.ManufacturingWorkCenterInput) { in.Name = "  " }, []string{"name"}, false},
		{"capacity 0", func(in *gen.ManufacturingWorkCenterInput) { in.Capacity = i32(0) }, []string{"capacity"}, true},
		{"efficiency 501", func(in *gen.ManufacturingWorkCenterInput) { in.TimeEfficiency = i32(501) }, []string{"timeEfficiency"}, true},
		{"setup three decimals, cleanup negative", func(in *gen.ManufacturingWorkCenterInput) {
			in.SetupMinutes, in.CleanupMinutes = str("1.234"), str("-1")
		}, []string{"cleanupMinutes", "setupMinutes"}, false},
		{"setup not a number", func(in *gen.ManufacturingWorkCenterInput) { in.SetupMinutes = str("1e3") }, []string{"setupMinutes"}, false},
		{"pattern off the grid, ending first, 24:00 as a start", func(in *gen.ManufacturingWorkCenterInput) {
			p := defaultPatternInput()
			p.Monday = []gen.ManufacturingInterval{{Start: "07:10", End: "15:00"}}
			p.Tuesday = []gen.ManufacturingInterval{{Start: "15:00", End: "07:00"}}
			p.Sunday = []gen.ManufacturingInterval{{Start: "24:00", End: "24:00"}}
			in.Pattern = &p
		}, []string{"pattern.monday[0].start", "pattern.sunday[0].start", "pattern.tuesday[0].end"}, false},
		{"pattern overlapping", func(in *gen.ManufacturingWorkCenterInput) {
			p := defaultPatternInput()
			p.Wednesday = []gen.ManufacturingInterval{{Start: "12:00", End: "16:00"}, {Start: "07:00", End: "12:15"}}
			in.Pattern = &p
		}, []string{"pattern.wednesday"}, false},
		{"three intervals", func(in *gen.ManufacturingWorkCenterInput) {
			p := defaultPatternInput()
			p.Thursday = []gen.ManufacturingInterval{{Start: "06:00", End: "07:00"}, {Start: "08:00", End: "09:00"}, {Start: "10:00", End: "11:00"}}
			in.Pattern = &p
		}, []string{"pattern.thursday"}, true},
	} {
		in := workCenterInput("SAW", siteMain)
		tc.mutate(&in)
		var opts []modtest.RequestOption
		if tc.skip {
			opts = append(opts, offContract)
		}
		p := expectRefusal(t, c.Do(http.MethodPost, workCentersPath, in, opts...), http.StatusBadRequest, "", tc.name)
		var got []string
		if p.Errors != nil {
			for field := range *p.Errors {
				got = append(got, field)
			}
		}
		slices.Sort(got)
		if !slices.Equal(got, tc.fields) {
			t.Errorf("%s: fields %v, want %v", tc.name, got, tc.fields)
		}
	}

	// A sorted pattern is stored sorted; touching intervals and 24:00 as an
	// end pass.
	in := workCenterInput("NIGHT", siteMain)
	p := defaultPatternInput()
	p.Friday = []gen.ManufacturingInterval{{Start: "18:00", End: "24:00"}, {Start: "06:00", End: "18:00"}}
	in.Pattern = &p
	w := createWorkCenter(t, c, in)
	if w.Pattern.Friday[0].Start != "06:00" || w.Pattern.Friday[1].End != "24:00" {
		t.Errorf("friday = %+v, want sorted", w.Pattern.Friday)
	}

	// An unknown property is refused, never dropped.
	res := c.Do(http.MethodPost, workCentersPath, map[string]any{"code": "A", "name": "A", "siteId": siteMain, "speed": 3},
		modtest.SkipContract("an unknown property on purpose"))
	if p := expectRefusal(t, res, http.StatusBadRequest, "", "unknown property"); p.Errors == nil || (*p.Errors)["speed"] == nil {
		t.Errorf("unknown property: %+v", p.Errors)
	}

	// The site must be an active Inventory site.
	expectRefusal(t, c.Do(http.MethodPost, workCentersPath, workCenterInput("GONE", siteGone)), http.StatusBadRequest, "site_unknown", "archived site")
	p2 := expectRefusal(t, c.Do(http.MethodPost, workCentersPath, workCenterInput("NONE", 77)), http.StatusBadRequest, "site_unknown", "unknown site")
	if p2.Errors == nil || (*p2.Errors)["siteId"] == nil {
		t.Errorf("site_unknown names %v, want siteId", p2.Errors)
	}
}

func defaultPatternInput() gen.ManufacturingWeekPattern {
	day := []gen.ManufacturingInterval{{Start: "07:00", End: "15:00"}}
	return gen.ManufacturingWeekPattern{Monday: day, Tuesday: day, Wednesday: day, Thursday: day, Friday: day,
		Saturday: []gen.ManufacturingInterval{}, Sunday: []gen.ManufacturingInterval{}}
}

// TestWorkCenter_CodeTaken is M8's unique code, the second one refused on
// create and on update.
func TestWorkCenter_CodeTaken(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	expectRefusal(t, c.Do(http.MethodPost, workCentersPath, workCenterInput("saw", siteBranch)), http.StatusConflict, "work_center_code_taken", "create")
	other := createWorkCenter(t, c, workCenterInput("LATHE", siteMain))
	up := gen.ManufacturingWorkCenterUpdate{Code: "SAW", Name: "Lathe", SiteId: siteMain, Revision: other.Revision}
	expectRefusal(t, c.Do(http.MethodPut, workCenterPath(other.Id), up), http.StatusConflict, "work_center_code_taken", "update")
}

// TestWorkCenter_CostsShapingAndRequired is M38: the rates are absent without
// costs-view, a rate written without it is the 403 costs_view_required, and
// on an update an absent rate keeps what is stored while null clears it.
func TestWorkCenter_CostsShapingAndRequired(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	costly := h.SignIn(t, append(slices.Clone(manager), "manufacturing:costs-view")...)
	plain := h.SignIn(t, manager...)

	in := workCenterInput("CNC", siteMain)
	in.MachineCostPerHour = json.RawMessage(`"450.5"`)
	in.LabourCostPerHour = json.RawMessage(`"300"`)
	expectRefusal(t, plain.Do(http.MethodPost, workCentersPath, in), http.StatusForbidden, "costs_view_required", "create a rate without costs-view")
	in.MachineCostPerHour = json.RawMessage(`null`)
	in.LabourCostPerHour = nil
	expectRefusal(t, plain.Do(http.MethodPost, workCentersPath, in), http.StatusForbidden, "costs_view_required", "a null rate without costs-view")

	in.MachineCostPerHour, in.LabourCostPerHour = json.RawMessage(`"450.5"`), json.RawMessage(`"300"`)
	w := createWorkCenter(t, costly, in)
	if string(w.MachineCostPerHour) != `"450.50"` || string(w.LabourCostPerHour) != `"300.00"` {
		t.Errorf("rates = %s %s", w.MachineCostPerHour, w.LabourCostPerHour)
	}
	in.Code = "CNC2"
	in.MachineCostPerHour = json.RawMessage(`"1.005"`)
	expectRefusal(t, costly.Do(http.MethodPost, workCentersPath, in), http.StatusBadRequest, "", "a rate with three decimals")

	var seen gen.ManufacturingWorkCenter
	plain.Do(http.MethodGet, workCenterPath(w.Id), nil).JSON(&seen)
	if seen.MachineCostPerHour != nil || seen.LabourCostPerHour != nil {
		t.Errorf("without costs-view the rates are %s %s, want absent", seen.MachineCostPerHour, seen.LabourCostPerHour)
	}
	var raw map[string]json.RawMessage
	plain.Do(http.MethodGet, workCenterPath(w.Id), nil).JSON(&raw)
	if _, ok := raw["machineCostPerHour"]; ok {
		t.Error("machineCostPerHour is in the body, want it absent")
	}

	// A manager without costs-view edits the rest; the rates are kept.
	up := gen.ManufacturingWorkCenterUpdate{Code: "CNC", Name: "Router", SiteId: siteMain, Capacity: i32(2), Revision: w.Revision}
	res := plain.Do(http.MethodPut, workCenterPath(w.Id), up)
	mustStatus(t, res, http.StatusOK, "update without rates")
	res.JSON(&seen)
	costly.Do(http.MethodGet, workCenterPath(w.Id), nil).JSON(&seen)
	if string(seen.MachineCostPerHour) != `"450.50"` || seen.Name != "Router" || seen.Capacity != 2 || seen.Revision != 2 {
		t.Errorf("after an update without rates = %+v", seen)
	}
	// Null clears; a value replaces.
	up = gen.ManufacturingWorkCenterUpdate{Code: "CNC", Name: "Router", SiteId: siteMain, Revision: seen.Revision,
		MachineCostPerHour: json.RawMessage(`null`), LabourCostPerHour: json.RawMessage(`"310.25"`)}
	res = costly.Do(http.MethodPut, workCenterPath(w.Id), up)
	mustStatus(t, res, http.StatusOK, "update clearing one rate")
	res.JSON(&seen)
	if string(seen.MachineCostPerHour) != "null" || string(seen.LabourCostPerHour) != `"310.25"` {
		t.Errorf("after clearing = %s %s", seen.MachineCostPerHour, seen.LabourCostPerHour)
	}
	up.Revision = seen.Revision
	expectRefusal(t, plain.Do(http.MethodPut, workCenterPath(w.Id), up), http.StatusForbidden, "costs_view_required", "update a rate without costs-view")
}

// TestWorkCenter_RevisionAndWorkingState is the optimistic concurrency of an
// edit (revision_conflict) and M8's rule that an edit never moves the working
// state.
func TestWorkCenter_RevisionAndWorkingState(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	w := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	h.Exec(t, `UPDATE manufacturing.work_centers SET working_state = 'blocked' WHERE id = $1`, w.Id)
	up := gen.ManufacturingWorkCenterUpdate{Code: "SAW", Name: "Saw", SiteId: siteBranch, Revision: w.Revision}
	var got gen.ManufacturingWorkCenter
	res := c.Do(http.MethodPut, workCenterPath(w.Id), up)
	mustStatus(t, res, http.StatusOK, "update")
	res.JSON(&got)
	if got.WorkingState != gen.ManufacturingWorkingStateBlocked || got.SiteCode != "BRANCH" || got.Revision != 2 {
		t.Errorf("after the edit = %+v, want still blocked at BRANCH", got)
	}
	expectRefusal(t, c.Do(http.MethodPut, workCenterPath(w.Id), up), http.StatusConflict, "revision_conflict", "stale update")
	expectRefusal(t, c.Do(http.MethodPost, workCenterPath(w.Id, "archive"), revisionInput(1)), http.StatusConflict, "revision_conflict", "stale archive")
	expectRefusal(t, c.Do(http.MethodDelete, workCenterPath(w.Id)+"?revision=1", nil), http.StatusConflict, "revision_conflict", "stale delete")
	up.Revision = 2
	up.SiteId = siteGone
	expectRefusal(t, c.Do(http.MethodPut, workCenterPath(w.Id), up), http.StatusBadRequest, "site_unknown", "move to an archived site")
	// The site is judged against the stored one, so an unknown id is a 404
	// whatever site it names.
	mustStatus(t, c.Do(http.MethodPut, workCenterPath(999999), up), http.StatusNotFound, "unknown id with a bad site")
	up.SiteId = siteMain
	mustStatus(t, c.Do(http.MethodPut, workCenterPath(999999), up), http.StatusNotFound, "update unknown")
}

// TestWorkCenter_SiteHeldByActiveBoms is M12a from the work center's side: its
// site does not change while an active BoM has an operation at it
// (work_center_other_site on siteId); other edits do, and once the BoM is
// archived the site does too.
func TestWorkCenter_SiteHeldByActiveBoms(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	w := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	in := bomInput(varFrame, "1", line(varTube, "1"))
	in.Operations = []gen.ManufacturingBomOperationInput{operation("cut", 10, w.Id, "5")}
	b := createBom(t, c, in)

	up := gen.ManufacturingWorkCenterUpdate{Code: "SAW", Name: "Saw", SiteId: siteBranch, Revision: w.Revision}
	p := expectRefusal(t, c.Do(http.MethodPut, workCenterPath(w.Id), up), http.StatusBadRequest, "work_center_other_site", "move a used one")
	if p.Errors == nil || (*p.Errors)["siteId"] == nil {
		t.Errorf("errors = %v, want one on siteId", p.Errors)
	}
	up.SiteId = siteMain
	var got gen.ManufacturingWorkCenter
	res := c.Do(http.MethodPut, workCenterPath(w.Id), up)
	mustStatus(t, res, http.StatusOK, "rename in place")
	res.JSON(&got)

	mustStatus(t, c.Do(http.MethodPost, bomPath(b.Id, "archive"), revisionInput(b.Revision)), http.StatusOK, "archive the BoM")
	up.SiteId, up.Revision = siteBranch, got.Revision
	res = c.Do(http.MethodPut, workCenterPath(w.Id), up)
	mustStatus(t, res, http.StatusOK, "move once only an archived BoM names it")
	res.JSON(&got)
	if got.SiteCode != "BRANCH" {
		t.Errorf("site = %s, want BRANCH", got.SiteCode)
	}
}

// TestWorkCenter_DeleteArchiveReactivate is M8: a work center an operation
// names is never deleted (work_center_in_use), one no operation names is;
// reactivating one whose site is archived is site_archived (409); an edit
// keeps an archived site, a move to one is site_unknown.
func TestWorkCenter_DeleteArchiveReactivate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	used := createWorkCenter(t, c, workCenterInput("USED", siteMain))
	in := bomInput(varFrame, "1", line(varTube, "1"))
	in.Operations = []gen.ManufacturingBomOperationInput{operation("cut", 10, used.Id, "5")}
	createBom(t, c, in)
	expectRefusal(t, c.Do(http.MethodDelete, workCenterPath(used.Id)+"?revision=1", nil), http.StatusConflict, "work_center_in_use", "delete a used one")

	free := createWorkCenter(t, c, workCenterInput("FREE", siteMain))
	mustStatus(t, c.Do(http.MethodDelete, workCenterPath(free.Id)+"?revision=1", nil), http.StatusNoContent, "delete a free one")
	mustStatus(t, c.Do(http.MethodDelete, workCenterPath(free.Id)+"?revision=1", nil), http.StatusNotFound, "delete it again")

	branch := createWorkCenter(t, c, workCenterInput("BR", siteBranch))
	var w gen.ManufacturingWorkCenter
	res := c.Do(http.MethodPost, workCenterPath(branch.Id, "archive"), revisionInput(branch.Revision))
	mustStatus(t, res, http.StatusOK, "archive")
	res.JSON(&w)
	if w.Status != gen.ManufacturingStatusArchived || w.Revision != 2 {
		t.Errorf("archived = %+v", w)
	}
	h.stock.archiveSite(siteBranch)
	expectRefusal(t, c.Do(http.MethodPost, workCenterPath(branch.Id, "reactivate"), revisionInput(w.Revision)), http.StatusConflict, "site_archived", "reactivate at an archived site")
	// An edit keeps the archived site; a move goes to an active one only.
	up := gen.ManufacturingWorkCenterUpdate{Code: "BR", Name: "Branch saw", SiteId: siteBranch, Revision: w.Revision}
	res = c.Do(http.MethodPut, workCenterPath(branch.Id), up)
	mustStatus(t, res, http.StatusOK, "edit at the archived site")
	res.JSON(&w)
	up.SiteId, up.Revision = siteGone, w.Revision
	expectRefusal(t, c.Do(http.MethodPut, workCenterPath(branch.Id), up), http.StatusBadRequest, "site_unknown", "move to an archived site")

	res = c.Do(http.MethodPost, workCenterPath(used.Id, "archive"), revisionInput(used.Revision))
	mustStatus(t, res, http.StatusOK, "archive the used one")
	res.JSON(&w)
	res = c.Do(http.MethodPost, workCenterPath(used.Id, "reactivate"), revisionInput(w.Revision))
	mustStatus(t, res, http.StatusOK, "reactivate")
	res.JSON(&w)
	if w.Status != gen.ManufacturingStatusActive || w.Revision != 3 {
		t.Errorf("reactivated = %+v", w)
	}
	mustStatus(t, c.Do(http.MethodPost, workCenterPath(999999, "reactivate"), revisionInput(1)), http.StatusNotFound, "reactivate unknown")
	mustStatus(t, c.Do(http.MethodPost, workCenterPath(999999, "archive"), revisionInput(1)), http.StatusNotFound, "archive unknown")
}

// TestWorkCenter_Closures is M9's closures: by first day, toDate on or after
// fromDate, at most 100, and deleted one at a time.
func TestWorkCenter_Closures(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, manager...)
	w := createWorkCenter(t, c, workCenterInput("SAW", siteMain))
	day := func(s string) openapi_types.Date {
		d, err := time.Parse(time.DateOnly, s)
		if err != nil {
			t.Fatal(err)
		}
		return openapi_types.Date{Time: d}
	}
	path := workCenterPath(w.Id, "closures")
	res := c.Do(http.MethodPost, path, gen.ManufacturingClosureInput{FromDate: day("2026-07-06"), ToDate: day("2026-07-31"), Note: str("Fellesferie")})
	mustStatus(t, res, http.StatusCreated, "create")
	var july gen.ManufacturingClosure
	res.JSON(&july)
	mustStatus(t, c.Do(http.MethodPost, path, gen.ManufacturingClosureInput{FromDate: day("2026-03-02"), ToDate: day("2026-03-02")}), http.StatusCreated, "one day")
	p := expectRefusal(t, c.Do(http.MethodPost, path, gen.ManufacturingClosureInput{FromDate: day("2026-05-02"), ToDate: day("2026-05-01")}), http.StatusBadRequest, "", "backwards")
	if p.Errors == nil || (*p.Errors)["toDate"] == nil {
		t.Errorf("backwards names %v, want toDate", p.Errors)
	}

	var list gen.ManufacturingClosureList
	res = h.SignIn(t, reader...).Do(http.MethodGet, path, nil)
	mustStatus(t, res, http.StatusOK, "list")
	res.JSON(&list)
	if len(list.Items) != 2 || list.Items[0].FromDate.Format(time.DateOnly) != "2026-03-02" || list.Items[1].Note != "Fellesferie" {
		t.Errorf("closures = %+v", list.Items)
	}
	mustStatus(t, c.Do(http.MethodGet, workCenterPath(999999, "closures"), nil), http.StatusNotFound, "list of an unknown center")
	mustStatus(t, c.Do(http.MethodPost, workCenterPath(999999, "closures"), gen.ManufacturingClosureInput{FromDate: day("2026-01-01"), ToDate: day("2026-01-01")}), http.StatusNotFound, "create on an unknown center")

	mustStatus(t, c.Do(http.MethodDelete, pathOf(path, july.Id), nil), http.StatusNoContent, "delete")
	mustStatus(t, c.Do(http.MethodDelete, pathOf(path, july.Id), nil), http.StatusNotFound, "delete again")

	// Fill to 100; the 101st is closures_too_many.
	for i := range 99 {
		h.Exec(t, `INSERT INTO manufacturing.work_center_closures (work_center_id, from_date, to_date) VALUES ($1, $2, $2)`,
			w.Id, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i))
	}
	expectRefusal(t, c.Do(http.MethodPost, path, gen.ManufacturingClosureInput{FromDate: day("2028-01-01"), ToDate: day("2028-01-02")}),
		http.StatusBadRequest, "closures_too_many", "the 101st")
}
