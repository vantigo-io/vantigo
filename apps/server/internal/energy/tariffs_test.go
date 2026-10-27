package energy_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// tariffPermissions is allEnergyPermissions with the tariffs' two.
var tariffPermissions = append(slices.Clone(allEnergyPermissions), "energy:tariffs-manage", "energy:tariffs-view")

// tariffJSON decodes Tariff.
type tariffJSON struct {
	Id          int32   `json:"id"`
	Kind        string  `json:"kind"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
	Usage       struct {
		SupplyPeriods  int32 `json:"supplyPeriods"`
		MeteringPoints int32 `json:"meteringPoints"`
	} `json:"usage"`
	Versions []struct {
		Id         int32   `json:"id"`
		ValidFrom  string  `json:"validFrom"`
		ValidTo    *string `json:"validTo"`
		Components []struct {
			Id           int32    `json:"id"`
			Position     int32    `json:"position"`
			Kind         string   `json:"kind"`
			Name         string   `json:"name"`
			Rate         *float64 `json:"rate"`
			RateNight    *float64 `json:"rateNight"`
			DayFrom      *int32   `json:"dayFrom"`
			DayTo        *int32   `json:"dayTo"`
			WeekdaysOnly bool     `json:"weekdaysOnly"`
			Tiers        *[]struct {
				FromKw   float64 `json:"fromKw"`
				PerMonth float64 `json:"perMonth"`
			} `json:"tiers"`
			RateReduced  *float64 `json:"rateReduced"`
			RateBusiness *float64 `json:"rateBusiness"`
		} `json:"components"`
	} `json:"versions"`
}

// tariffSummaryJSON decodes TariffSummary.
type tariffSummaryJSON struct {
	Id             int32  `json:"id"`
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	VersionCount   int32  `json:"versionCount"`
	CurrentVersion *struct {
		ValidFrom string  `json:"validFrom"`
		ValidTo   *string `json:"validTo"`
	} `json:"currentVersion"`
	Usage struct {
		SupplyPeriods  int32 `json:"supplyPeriods"`
		MeteringPoints int32 `json:"meteringPoints"`
	} `json:"usage"`
}

// powerTariffBody is a spot tariff with a markup, one open version from from.
func powerTariffBody(name, from string) json.RawMessage {
	return json.RawMessage(`{"kind":"power","name":"` + name + `","description":"Spotpris med påslag","versions":[{"validFrom":"` + from + `","components":[` +
		`{"kind":"spot_energy","name":"Spotpris"},{"kind":"markup","name":"Påslag","rate":0.049},{"kind":"monthly_fee","name":"Fastbeløp","rate":39}]}]}`)
}

// gridTariffBody is a grid tariff with every grid kind, one open version from from.
func gridTariffBody(name, from string) json.RawMessage {
	return json.RawMessage(`{"kind":"grid","name":"` + name + `","versions":[{"validFrom":"` + from + `","components":[` +
		`{"kind":"grid_energy","name":"Energiledd","rate":0.3925,"rateNight":0.3125,"dayFrom":6,"dayTo":22,"weekdaysOnly":true},` +
		`{"kind":"grid_capacity","name":"Kapasitetsledd","tiers":[{"fromKw":0,"perMonth":135},{"fromKw":2,"perMonth":200},{"fromKw":5,"perMonth":325}]},` +
		`{"kind":"electricity_tax","name":"Elavgift","rate":0.0979,"rateReduced":0.0056},` +
		`{"kind":"enova_levy","name":"Enova","rate":0.01,"rateBusiness":800}]}]}`)
}

func createTariff(t *testing.T, c *modtest.Client, body json.RawMessage) tariffJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/energy/tariffs", body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create tariff: status %d body %s, want 201", r.Status, r.Body)
	}
	var tariff tariffJSON
	r.JSON(&tariff)
	return tariff
}

// TestTariffs_CreateAndRead pins the create and the read (reading 4): 201
// with the tariff as stored — active, the description, every component's
// fields in position order — and GET answering the same.
func TestTariffs_CreateAndRead(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, tariffPermissions...)

	cr := c.Do(http.MethodPost, "/api/v1/energy/tariffs", gridTariffBody("  Nettleie 2026  ", "2026-01-01"))
	if cr.Status != http.StatusCreated {
		t.Fatalf("create: status %d body %s, want 201", cr.Status, cr.Body)
	}
	var created tariffJSON
	cr.JSON(&created)
	if created.Kind != "grid" || created.Name != "Nettleie 2026" || created.Status != "active" || created.Description != nil {
		t.Errorf("header = %+v, want grid, the trimmed name, active and no description", created)
	}
	if len(created.Versions) != 1 || created.Versions[0].ValidFrom != "2026-01-01" || created.Versions[0].ValidTo != nil {
		t.Fatalf("versions = %+v, want one open from 2026-01-01", created.Versions)
	}
	cs := created.Versions[0].Components
	if len(cs) != 4 {
		t.Fatalf("components = %d, want 4", len(cs))
	}
	for i, comp := range cs {
		if comp.Position != int32(i) {
			t.Errorf("component %d position = %d", i, comp.Position)
		}
	}
	e := cs[0]
	if e.Kind != "grid_energy" || e.Rate == nil || *e.Rate != 0.3925 || e.RateNight == nil || *e.RateNight != 0.3125 ||
		e.DayFrom == nil || *e.DayFrom != 6 || e.DayTo == nil || *e.DayTo != 22 || !e.WeekdaysOnly || e.Tiers != nil {
		t.Errorf("grid_energy = %+v", e)
	}
	if tiers := cs[1].Tiers; tiers == nil || len(*tiers) != 3 || (*tiers)[2].FromKw != 5 || (*tiers)[2].PerMonth != 325 || cs[1].Rate != nil {
		t.Errorf("grid_capacity = %+v", cs[1])
	}
	if cs[2].RateReduced == nil || *cs[2].RateReduced != 0.0056 || cs[3].RateBusiness == nil || *cs[3].RateBusiness != 800 {
		t.Errorf("tax and levy = %+v, %+v", cs[2], cs[3])
	}

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/tariffs/%d", created.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get: status %d body %s, want 200", r.Status, r.Body)
	}
	if !jsonEqual(t, r.Body, cr.Body) {
		t.Errorf("get = %s, want what the create answered %s", r.Body, cr.Body)
	}

	if r := c.Do(http.MethodGet, "/api/v1/energy/tariffs/999999", nil); r.Status != http.StatusNotFound {
		t.Errorf("get unknown: status %d, want 404", r.Status)
	}
}

// TestTariffs_CreateRefusesABadBody pins the 400's keys on the wire: each
// refusal under its place in the body.
func TestTariffs_CreateRefusesABadBody(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, tariffPermissions...)

	r := c.Do(http.MethodPost, "/api/v1/energy/tariffs", json.RawMessage(`{"kind":"grid","name":"N","versions":[`+
		`{"validFrom":"2026-01-01","validTo":"2026-07-01","components":[{"kind":"grid_energy","name":"E","rate":1}]},`+
		`{"validFrom":"2026-07-01","components":[{"kind":"spot_energy","name":"S"},{"kind":"grid_capacity","name":"C","tiers":[{"fromKw":1,"perMonth":1}]}]}]}`))
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := map[string]string{
		"versions[1].components[0].kind":            "A grid tariff cannot hold spot_energy.",
		"versions[1].components[1].tiers[0].fromKw": "The first tier must start at 0 kW.",
	}
	if len(problem.Errors) != len(want) {
		t.Errorf("errors = %v, want exactly %v", problem.Errors, want)
	}
	for k, msg := range want {
		if got := problem.Errors[k]; len(got) != 1 || got[0] != msg {
			t.Errorf("errors[%s] = %v, want %q", k, got, msg)
		}
	}

	overlap := c.Do(http.MethodPost, "/api/v1/energy/tariffs", json.RawMessage(`{"kind":"power","name":"N","versions":[`+
		`{"validFrom":"2026-01-01","components":[{"kind":"spot_energy","name":"S"}]},`+
		`{"validFrom":"2026-07-01","components":[{"kind":"spot_energy","name":"S"}]}]}`))
	if overlap.Status != http.StatusBadRequest {
		t.Fatalf("overlap: status %d body %s, want 400", overlap.Status, overlap.Body)
	}
	var overlapProblem validationProblemJSON
	overlap.JSON(&overlapProblem)
	if got := overlapProblem.Errors["versions[1].validFrom"]; len(overlapProblem.Errors) != 1 || len(got) != 1 || got[0] != "The version overlaps versions[0], valid from 2026-01-01." {
		t.Errorf("overlap errors = %v", overlapProblem.Errors)
	}
}

// TestTariffs_List pins GET /tariffs (reading 4): active first, then kind,
// then name; the filters; the version count; the version valid today in
// Europe/Oslo, not in UTC; the usage; and a 400 for a word that is neither.
func TestTariffs_List(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// The harness clock is 2026-09-12 12:00 UTC; at 22:30 UTC it is already
	// the 13th in Oslo.
	h.Advance(10*time.Hour + 30*time.Minute)
	c := h.SignIn(t, tariffPermissions...)

	powerB := createTariff(t, c, json.RawMessage(`{"kind":"power","name":"B kraft","versions":[`+
		`{"validFrom":"2026-01-01","validTo":"2026-09-13","components":[{"kind":"spot_energy","name":"S"}]},`+
		`{"validFrom":"2026-09-13","components":[{"kind":"fixed_energy","name":"F","rate":1.1}]}]}`))
	powerA := createTariff(t, c, powerTariffBody("A kraft", "2027-01-01"))
	grid := createTariff(t, c, gridTariffBody("A nett", "2026-01-01"))
	archived := createTariff(t, c, powerTariffBody("0 old", "2025-01-01"))
	if r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/tariffs/%d/archive", archived.Id), nil); r.Status != http.StatusOK {
		t.Fatalf("archive: status %d body %s", r.Status, r.Body)
	}
	point := createMeteringPoint(t, c)
	if r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/energy/metering-points/%d", point.Id),
		withField(updateBody(point.Gsrn), "gridTariffId", grid.Id)); r.Status != http.StatusOK {
		t.Fatalf("assign grid tariff: status %d body %s", r.Status, r.Body)
	}

	list := func(query string) []tariffSummaryJSON {
		t.Helper()
		r := c.Do(http.MethodGet, "/api/v1/energy/tariffs"+query, nil)
		if r.Status != http.StatusOK {
			t.Fatalf("list %s: status %d body %s", query, r.Status, r.Body)
		}
		var rows []tariffSummaryJSON
		r.JSON(&rows)
		return rows
	}
	ids := func(rows []tariffSummaryJSON) []int32 {
		out := []int32{}
		for _, r := range rows {
			out = append(out, r.Id)
		}
		return out
	}

	all := list("")
	if got, want := ids(all), []int32{grid.Id, powerA.Id, powerB.Id, archived.Id}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v (active first, then kind, then name)", got, want)
	}
	byID := map[int32]tariffSummaryJSON{}
	for _, r := range all {
		byID[r.Id] = r
	}
	if b := byID[powerB.Id]; b.VersionCount != 2 || b.CurrentVersion == nil || b.CurrentVersion.ValidFrom != "2026-09-13" || b.CurrentVersion.ValidTo != nil {
		t.Errorf("B kraft = %+v, want 2 versions and the one from 2026-09-13 current (Oslo's day)", b)
	}
	if a := byID[powerA.Id]; a.CurrentVersion != nil {
		t.Errorf("A kraft current = %+v, want none before 2027", a.CurrentVersion)
	}
	if g := byID[grid.Id]; g.Usage.MeteringPoints != 1 || g.Usage.SupplyPeriods != 0 || g.CurrentVersion == nil {
		t.Errorf("A nett = %+v, want one metering point and a current version", g)
	}

	if got, want := ids(list("?kind=power")), []int32{powerA.Id, powerB.Id, archived.Id}; !slices.Equal(got, want) {
		t.Errorf("kind=power = %v, want %v", got, want)
	}
	if got, want := ids(list("?status=archived")), []int32{archived.Id}; !slices.Equal(got, want) {
		t.Errorf("status=archived = %v, want %v", got, want)
	}
	if got, want := ids(list("?kind=grid&status=active")), []int32{grid.Id}; !slices.Equal(got, want) {
		t.Errorf("kind=grid&status=active = %v, want %v", got, want)
	}

	r := c.Do(http.MethodGet, "/api/v1/energy/tariffs?kind=water&status=gone", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("unknown words: status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if len(problem.Errors["kind"]) != 1 || len(problem.Errors["status"]) != 1 {
		t.Errorf("errors = %v, want kind and status", problem.Errors)
	}
}

// TestPutTariff pins the PUT (reading 4): the body judged first, then 404,
// then the kind; the versions and components replaced whole; and an
// archived tariff still edited, staying archived.
func TestPutTariff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, tariffPermissions...)
	tariff := createTariff(t, c, powerTariffBody("Kraft", "2026-01-01"))
	path := fmt.Sprintf("/api/v1/energy/tariffs/%d", tariff.Id)

	replace := json.RawMessage(`{"kind":"power","name":"Kraft fast","description":"  Fastpris  ","versions":[` +
		`{"validFrom":"2026-07-01","components":[{"kind":"fixed_energy","name":"Fastpris","rate":1.4999999}]},` +
		`{"validFrom":"2026-01-01","validTo":"2026-07-01","components":[{"kind":"spot_energy","name":"Spot"},{"kind":"markup","name":"Påslag","rate":0.02}]}]}`)
	r := c.Do(http.MethodPut, path, replace)
	if r.Status != http.StatusOK {
		t.Fatalf("put: status %d body %s, want 200", r.Status, r.Body)
	}
	var got tariffJSON
	r.JSON(&got)
	if got.Name != "Kraft fast" || got.Description == nil || *got.Description != "Fastpris" || got.Status != "active" {
		t.Errorf("header = %+v", got)
	}
	if len(got.Versions) != 2 || got.Versions[0].ValidFrom != "2026-01-01" || got.Versions[0].ValidTo == nil ||
		*got.Versions[0].ValidTo != "2026-07-01" || got.Versions[1].ValidFrom != "2026-07-01" {
		t.Fatalf("versions = %+v, want the two in valid_from order", got.Versions)
	}
	if got.Versions[0].Id == tariff.Versions[0].Id {
		t.Errorf("the old version %d survived the replace", tariff.Versions[0].Id)
	}
	if cs := got.Versions[0].Components; len(cs) != 2 || cs[0].Kind != "spot_energy" || cs[1].Kind != "markup" {
		t.Errorf("first version's components = %+v", cs)
	}
	if cs := got.Versions[1].Components; len(cs) != 1 || cs[0].Rate == nil || *cs[0].Rate != 1.5 {
		t.Errorf("second version's components = %+v, want fixed_energy at 1.5 (six decimals)", cs)
	}
	if again := c.Do(http.MethodGet, path, nil); again.Status != http.StatusOK || !jsonEqual(t, again.Body, r.Body) {
		t.Errorf("get after put = %s, want the PUT's answer %s", again.Body, r.Body)
	}

	if nf := c.Do(http.MethodPut, "/api/v1/energy/tariffs/999999", replace); nf.Status != http.StatusNotFound {
		t.Errorf("unknown: status %d, want 404", nf.Status)
	}
	// A bad body is judged before existence.
	if bad := c.Do(http.MethodPut, "/api/v1/energy/tariffs/999999", json.RawMessage(`{"kind":"power","name":""}`)); bad.Status != http.StatusBadRequest {
		t.Errorf("bad body on unknown: status %d, want 400", bad.Status)
	}

	kind := c.Do(http.MethodPut, path, gridTariffBody("Kraft", "2026-01-01"))
	if kind.Status != http.StatusBadRequest {
		t.Fatalf("kind change: status %d body %s, want 400", kind.Status, kind.Body)
	}
	var kindProblem validationProblemJSON
	kind.JSON(&kindProblem)
	if got := kindProblem.Errors["kind"]; len(kindProblem.Errors) != 1 || len(got) != 1 || got[0] != "A tariff's kind cannot be changed." {
		t.Errorf("kind errors = %v", kindProblem.Errors)
	}

	overlap := c.Do(http.MethodPut, path, json.RawMessage(`{"kind":"power","name":"N","versions":[`+
		`{"validFrom":"2026-01-01","validTo":"2026-08-01","components":[{"kind":"spot_energy","name":"S"}]},`+
		`{"validFrom":"2026-07-01","components":[{"kind":"spot_energy","name":"S"}]}]}`))
	if overlap.Status != http.StatusBadRequest {
		t.Fatalf("overlap: status %d body %s, want 400", overlap.Status, overlap.Body)
	}
	var overlapProblem validationProblemJSON
	overlap.JSON(&overlapProblem)
	if len(overlapProblem.Errors) != 1 || len(overlapProblem.Errors["versions[1].validFrom"]) != 1 {
		t.Errorf("overlap errors = %v, want versions[1].validFrom", overlapProblem.Errors)
	}

	badComponent := c.Do(http.MethodPut, path, json.RawMessage(`{"kind":"power","name":"N","versions":[`+
		`{"validFrom":"2026-01-01","components":[{"kind":"spot_energy","name":"S","rate":1},{"kind":"markup","name":"M"}]}]}`))
	if badComponent.Status != http.StatusBadRequest {
		t.Fatalf("bad component: status %d body %s, want 400", badComponent.Status, badComponent.Body)
	}
	var componentProblem validationProblemJSON
	badComponent.JSON(&componentProblem)
	if errs := componentProblem.Errors; len(errs) != 2 || len(errs["versions[0].components[0].rate"]) != 1 || len(errs["versions[0].components[1].rate"]) != 1 {
		t.Errorf("bad component errors = %v, want components[0].rate and components[1].rate", errs)
	}

	// Nothing refused was written.
	var after tariffJSON
	c.Do(http.MethodGet, path, nil).JSON(&after)
	if after.Name != "Kraft fast" || len(after.Versions) != 2 {
		t.Errorf("after the refusals = %+v, want the accepted PUT unchanged", after)
	}

	// An archived tariff is still edited, and stays archived.
	if a := c.Do(http.MethodPost, path+"/archive", nil); a.Status != http.StatusOK {
		t.Fatalf("archive: status %d", a.Status)
	}
	edited := c.Do(http.MethodPut, path, powerTariffBody("Kraft rettet", "2026-01-01"))
	if edited.Status != http.StatusOK {
		t.Fatalf("put archived: status %d body %s, want 200", edited.Status, edited.Body)
	}
	edited.JSON(&got)
	if got.Status != "archived" || got.Name != "Kraft rettet" || len(got.Versions) != 1 {
		t.Errorf("archived after put = %+v, want archived, renamed, one version", got)
	}
}

// TestArchiveTariff pins the archive (reading 4): 404, else archived, and
// archiving again answers the same tariff unchanged.
func TestArchiveTariff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, tariffPermissions...)
	tariff := createTariff(t, c, powerTariffBody("Kraft", "2026-01-01"))
	path := fmt.Sprintf("/api/v1/energy/tariffs/%d/archive", tariff.Id)

	first := c.Do(http.MethodPost, path, nil)
	if first.Status != http.StatusOK {
		t.Fatalf("archive: status %d body %s, want 200", first.Status, first.Body)
	}
	var got tariffJSON
	first.JSON(&got)
	if got.Status != "archived" || len(got.Versions) != 1 {
		t.Errorf("archived = %+v", got)
	}
	h.Advance(time.Hour)
	second := c.Do(http.MethodPost, path, nil)
	if second.Status != http.StatusOK || !jsonEqual(t, second.Body, first.Body) {
		t.Errorf("archive again: status %d body %s, want 200 and %s", second.Status, second.Body, first.Body)
	}
	if r := c.Do(http.MethodPost, "/api/v1/energy/tariffs/999999/archive", nil); r.Status != http.StatusNotFound {
		t.Errorf("unknown: status %d, want 404", r.Status)
	}
}

// TestDeleteTariff pins the delete (reading 4): an unused tariff goes with
// its versions (204, then 404); one named by a supply period — even a
// cancelled one — or by a metering point is 409 "Tariff in use".
func TestDeleteTariff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, tariffPermissions...)

	unused := createTariff(t, c, powerTariffBody("Unused", "2026-01-01"))
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/energy/tariffs/%d", unused.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete unused: status %d body %s, want 204", r.Status, r.Body)
	}
	if r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/tariffs/%d", unused.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("get deleted: status %d, want 404", r.Status)
	}
	if r := c.Do(http.MethodDelete, "/api/v1/energy/tariffs/999999", nil); r.Status != http.StatusNotFound {
		t.Errorf("delete unknown: status %d, want 404", r.Status)
	}

	inUse := func(t *testing.T, id int32, wantDetail string) {
		t.Helper()
		r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/energy/tariffs/%d", id), nil)
		if r.Status != http.StatusConflict {
			t.Fatalf("delete in use: status %d body %s, want 409", r.Status, r.Body)
		}
		var problem problemJSON
		r.JSON(&problem)
		if problem.Title != "Tariff in use" || problem.Detail != wantDetail {
			t.Errorf("problem = %+v, want Tariff in use and %q", problem, wantDetail)
		}
		if g := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/tariffs/%d", id), nil); g.Status != http.StatusOK {
			t.Errorf("the refused tariff: status %d, want 200", g.Status)
		}
	}

	t.Run("by a supply period, even cancelled", func(t *testing.T) {
		power := createTariff(t, c, powerTariffBody("Held", "2026-01-01"))
		point := createMeteringPoint(t, c)
		r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
			map[string]any{"customerId": 1001, "start": h.Now(), "tariffId": power.Id})
		if r.Status != http.StatusCreated {
			t.Fatalf("create period: status %d body %s", r.Status, r.Body)
		}
		var period supplyPeriodJSON
		r.JSON(&period)
		if d := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods/%d", point.Id, period.Id), nil); d.Status != http.StatusNoContent {
			t.Fatalf("cancel period: status %d", d.Status)
		}
		inUse(t, power.Id, "The tariff is named by 1 supply period and 0 metering points. Archive it instead.")
	})

	t.Run("by a metering point", func(t *testing.T) {
		grid := createTariff(t, c, gridTariffBody("Nett", "2026-01-01"))
		for range 2 {
			point := createMeteringPoint(t, c)
			if r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/energy/metering-points/%d", point.Id),
				withField(updateBody(point.Gsrn), "gridTariffId", grid.Id)); r.Status != http.StatusOK {
				t.Fatalf("assign: status %d body %s", r.Status, r.Body)
			}
		}
		inUse(t, grid.Id, "The tariff is named by 0 supply periods and 2 metering points. Archive it instead.")
	})
}

// TestDeleteTariff_BackstopNamesTheForeignKeys pins the names the delete's
// backstop matches (tariffUseConstraints) against the schema: were the
// foreign keys named otherwise, a delete racing an assignment would answer
// a 500, not the 409.
func TestDeleteTariff_BackstopNamesTheForeignKeys(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rows, err := h.Pool().Query(t.Context(),
		`SELECT conname FROM pg_constraint WHERE confrelid = 'energy.tariffs'::regclass AND contype = 'f' AND conrelid <> 'energy.tariff_versions'::regclass ORDER BY conname`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"metering_points_grid_tariff_id_fkey", "supply_periods_tariff_id_fkey"}; !slices.Equal(names, want) {
		t.Errorf("foreign keys on energy.tariffs = %v, want %v", names, want)
	}
}

// jsonEqual reports whether a and b are the same JSON value.
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("decode %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return fmt.Sprint(va) == fmt.Sprint(vb)
}
