package energy_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// allEnergyPermissions is every permission the module declares — enough for
// any operation's static x-vantigo-access — for tests whose point is the
// handler's own behaviour, not the access layer.
var allEnergyPermissions = []string{
	"energy:consumption-manage", "energy:consumption-view",
	"energy:metering-points-manage", "energy:metering-points-view",
	"energy:meters-manage", "energy:meters-view",
	"energy:supply-periods-manage", "energy:supply-periods-view",
}

var gsrnCounter atomic.Uint64

// gsrn generates a unique 18-digit GSRN, the same shape
// EnergyEndpointsTests.Gsrn() produces ("7070575" plus digits).
func gsrn(t *testing.T) string {
	t.Helper()
	n := gsrnCounter.Add(1)
	return fmt.Sprintf("7070575%011d", n)[:18]
}

// createMeteringPoint creates a metering point with a fresh GSRN and
// returns its decoded response.
func createMeteringPoint(t *testing.T, c *modtest.Client) meteringPointJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/energy/metering-points", newMeteringPointBody(gsrn(t), "Test meter"))
	if r.Status != http.StatusCreated {
		t.Fatalf("create metering point: status %d body %s, want 201", r.Status, r.Body)
	}
	var point meteringPointJSON
	r.JSON(&point)
	return point
}

// Ported from EnergyEndpointsTests.Metering_point_crud_round_trip.
func TestMeteringPointCrudRoundTrip(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)

	g := gsrn(t)
	create := c.Do(http.MethodPost, "/api/v1/energy/metering-points", newMeteringPointBody(g, "Test meter"))
	if create.Status != http.StatusCreated {
		t.Fatalf("create: status %d body %s, want 201", create.Status, create.Body)
	}
	var point meteringPointJSON
	create.JSON(&point)
	if point.Gsrn != g {
		t.Errorf("Gsrn = %q, want %q", point.Gsrn, g)
	}
	if point.MeterNumber == nil || *point.MeterNumber != "Test meter" {
		t.Errorf("MeterNumber = %v, want \"Test meter\"", point.MeterNumber)
	}

	meters := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/%d/meters", point.Id), nil)
	var meterList []meterJSON
	meters.JSON(&meterList)
	if len(meterList) != 1 {
		t.Fatalf("meters = %d, want 1", len(meterList))
	}
	if meterList[0].MeterNumber != "Test meter" {
		t.Errorf("meter number = %q, want %q", meterList[0].MeterNumber, "Test meter")
	}

	get := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/energy/metering-points/%d", point.Id), nil)
	var got meteringPointJSON
	get.JSON(&got)
	if got.Gsrn != g {
		t.Errorf("get Gsrn = %q, want %q", got.Gsrn, g)
	}

	update := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/energy/metering-points/%d", point.Id), updateBody(g))
	if update.Status != http.StatusOK {
		t.Fatalf("update: status %d body %s, want 200", update.Status, update.Body)
	}
	var updated meteringPointJSON
	update.JSON(&updated)
	// PUT never touches meters: the current meter's number is unchanged
	// even though the request carried no meterNumber field at all.
	if updated.MeterNumber == nil || *updated.MeterNumber != "Test meter" {
		t.Errorf("after PUT, MeterNumber = %v, want unchanged \"Test meter\"", updated.MeterNumber)
	}
}

func TestGetMeteringPoint_NotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	r := c.Do(http.MethodGet, "/api/v1/energy/metering-points/999999", nil)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestCreateMeteringPoint_ValidationMessages pins every field's exact .NET
// message text (CreateMeteringPointEndpoint.cs's shared Validate()).
func TestCreateMeteringPoint_ValidationMessages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)

	cases := []struct {
		name       string
		body       map[string]any
		field      string
		wantErrMsg string
	}{
		{"missing gsrn", withField(newMeteringPointBody(gsrn(t), "M"), "gsrn", nil), "gsrn", "GSRN must contain exactly 18 digits."},
		{"short gsrn", withField(newMeteringPointBody(gsrn(t), "M"), "gsrn", "12345"), "gsrn", "GSRN must contain exactly 18 digits."},
		{"missing meterNumber", withField(newMeteringPointBody(gsrn(t), "M"), "meterNumber", nil), "meterNumber", "Meter number is required."},
		{"missing address", withField(newMeteringPointBody(gsrn(t), "M"), "address", nil), "address", "Address is required."},
		{"bad priceArea", withField(newMeteringPointBody(gsrn(t), "M"), "priceArea", "no1"), "priceArea", "Price area must contain two uppercase letters followed by one or two digits."},
		{"negative consumption", withField(newMeteringPointBody(gsrn(t), "M"), "expectedAnnualConsumptionKwh", -1.0), "expectedAnnualConsumptionKwh", "Expected annual consumption cannot be negative."},
		{"grid area too long", withField(newMeteringPointBody(gsrn(t), "M"), "gridArea", strings.Repeat("g", 65)), "gridArea", "Grid area cannot be longer than 64 characters."},
		{"consumption too large", withField(newMeteringPointBody(gsrn(t), "M"), "expectedAnnualConsumptionKwh", 1e11), "expectedAnnualConsumptionKwh", "Expected annual consumption must be less than 100000000000."},
		{"consumption rounding onto the limit", withField(newMeteringPointBody(gsrn(t), "M"), "expectedAnnualConsumptionKwh", 99999999999.9996), "expectedAnnualConsumptionKwh", "Expected annual consumption must be less than 100000000000."},
		{"bad latitude", withField(newMeteringPointBody(gsrn(t), "M"), "latitude", 200.0), "location", "Latitude must be between -90 and 90."},
		{"bad longitude", withField(newMeteringPointBody(gsrn(t), "M"), "longitude", 200.0), "location", "Longitude must be between -180 and 180."},
		{"bad connectionStatus", withField(newMeteringPointBody(gsrn(t), "M"), "connectionStatus", "Bogus"), "connectionStatus", "Connection status must be New, Connected or Disconnected."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := c.Do(http.MethodPost, "/api/v1/energy/metering-points", tc.body)
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var problem validationProblemJSON
			r.JSON(&problem)
			if problem.Title != "Invalid metering point" {
				t.Errorf("Title = %q, want %q", problem.Title, "Invalid metering point")
			}
			got := problem.Errors[tc.field]
			if len(got) != 1 || got[0] != tc.wantErrMsg {
				t.Errorf("Errors[%q] = %v, want [%q]", tc.field, got, tc.wantErrMsg)
			}
		})
	}
}

// TestCreateMeteringPoint_AddressFieldMessages pins the per-field address
// error keys and messages (MeteringPointRequest.cs's AddAddressError).
func TestCreateMeteringPoint_AddressFieldMessages(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)

	body := newMeteringPointBody(gsrn(t), "M")
	body["address"] = map[string]any{"streetAddress": "", "postalCode": "", "city": "", "countryCode": "NOR"}
	r := c.Do(http.MethodPost, "/api/v1/energy/metering-points", body)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	for _, field := range []string{"address.streetAddress", "address.postalCode", "address.city"} {
		got := problem.Errors[field]
		if len(got) != 1 || got[0] != "This field is required." {
			t.Errorf("Errors[%q] = %v, want [\"This field is required.\"]", field, got)
		}
	}
	got := problem.Errors["address.countryCode"]
	if len(got) != 1 || got[0] != "Country code must contain two letters." {
		t.Errorf("Errors[address.countryCode] = %v, want the two-letter message", got)
	}
}

// TestCreateMeteringPoint_DuplicateGsrn_ReturnsConflict pins the 409's
// title/detail text (CreateMeteringPointEndpoint.cs:19).
func TestCreateMeteringPoint_DuplicateGsrn_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	g := gsrn(t)
	if r := c.Do(http.MethodPost, "/api/v1/energy/metering-points", newMeteringPointBody(g, "First")); r.Status != http.StatusCreated {
		t.Fatalf("first create: status %d body %s, want 201", r.Status, r.Body)
	}
	r := c.Do(http.MethodPost, "/api/v1/energy/metering-points", newMeteringPointBody(g, "Second"))
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemJSON
	r.JSON(&problem)
	if problem.Title != "Duplicate GSRN" {
		t.Errorf("Title = %q, want %q", problem.Title, "Duplicate GSRN")
	}
	if problem.Detail != "A metering point with that GSRN already exists." {
		t.Errorf("Detail = %q, want the duplicate-GSRN message", problem.Detail)
	}
}

// TestUpdateMeteringPoint_ValidatesBeforeExistence pins
// UpdateMeteringPointEndpoint's ordering (energy inventory §1.1 line 32/§8
// oddity 3): a malformed body against a nonexistent id answers 400, not
// 404 — the opposite of ReplaceMeter (meters_test.go's mirror-image test).
// A mutation that swapped the order (existence-check first) would turn
// this 400 into a 404.
func TestUpdateMeteringPoint_ValidatesBeforeExistence(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)

	body := newMeteringPointBody(gsrn(t), "M")
	body["gsrn"] = "not-a-gsrn"
	r := c.Do(http.MethodPut, "/api/v1/energy/metering-points/999999", body)
	if r.Status != http.StatusBadRequest {
		t.Errorf("status %d body %s, want 400 (validate before existence)", r.Status, r.Body)
	}
}

func TestUpdateMeteringPoint_NotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)

	body := newMeteringPointBody(gsrn(t), "M")
	r := c.Do(http.MethodPut, "/api/v1/energy/metering-points/999999", body)
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestUpdateMeteringPoint_DuplicateGsrn_ReturnsConflict pins the excluding-self
// duplicate check (UpdateMeteringPointEndpoint.cs:18-19): updating a point to
// its own current GSRN must never conflict with itself.
func TestUpdateMeteringPoint_DuplicateGsrn_ReturnsConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)

	point := createMeteringPoint(t, c)
	other := createMeteringPoint(t, c)

	// Updating `other` to `point`'s own GSRN must conflict.
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/energy/metering-points/%d", other.Id), updateBody(point.Gsrn))
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409", r.Status, r.Body)
	}

	// Updating `point` to its own GSRN, unchanged, must not conflict with itself.
	same := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/energy/metering-points/%d", point.Id), updateBody(point.Gsrn))
	if same.Status != http.StatusOK {
		t.Errorf("self-update status %d body %s, want 200 (excluding self)", same.Status, same.Body)
	}
}

// TestUpdateMeteringPoint_RefusesWhatItsColumnsCannotHold: a grid area longer
// than its varchar(64) and a consumption beyond its numeric(14,3) are the
// request's 400, never the database's 500 — on the update as on the create.
func TestUpdateMeteringPoint_RefusesWhatItsColumnsCannotHold(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	body := updateBody(point.Gsrn)
	body["gridArea"] = "  " + strings.Repeat("g", 65) + "  "
	body["expectedAnnualConsumptionKwh"] = 2e11
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/energy/metering-points/%d", point.Id), body)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if got := problem.Errors["gridArea"]; len(got) != 1 || got[0] != "Grid area cannot be longer than 64 characters." {
		t.Errorf("Errors[gridArea] = %v", got)
	}
	if got := problem.Errors["expectedAnnualConsumptionKwh"]; len(got) != 1 || got[0] != "Expected annual consumption must be less than 100000000000." {
		t.Errorf("Errors[expectedAnnualConsumptionKwh] = %v", got)
	}

	// The limits themselves fit: 64 characters once trimmed, and the largest
	// value the column holds.
	body["gridArea"] = "  " + strings.Repeat("g", 64) + "  "
	body["expectedAnnualConsumptionKwh"] = 99999999999.999
	if ok := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/energy/metering-points/%d", point.Id), body); ok.Status != http.StatusOK {
		t.Errorf("at the limits: status %d body %s, want 200", ok.Status, ok.Body)
	}
}

func updateBody(g string) map[string]any {
	return map[string]any{
		"gsrn":      g,
		"address":   map[string]any{"streetAddress": "Testgata 1", "postalCode": "0001", "city": "Oslo", "countryCode": "NO"},
		"priceArea": "NO1",
	}
}

// TestPutMeteringPoint_DoesNotRequireMetersManage pins energy inventory §1.1
// line 32: PUT /{id} requires metering-points-manage, metering-points-view
// and meters-view, but *not* meters-manage — it never touches meters. A
// mutation adding meters-manage to the contract's x-vantigo-access (or a
// handler-level re-check of it) would not be caught by any test that always
// signs in with every permission; this one deliberately withholds it.
func TestPutMeteringPoint_DoesNotRequireMetersManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, admin)

	limited := h.SignIn(t, "energy:metering-points-manage", "energy:metering-points-view", "energy:meters-view")
	r := limited.Do(http.MethodPut, fmt.Sprintf("/api/v1/energy/metering-points/%d", point.Id), updateBody(point.Gsrn))
	if r.Status != http.StatusOK {
		t.Errorf("status %d body %s, want 200 without meters-manage", r.Status, r.Body)
	}
}

func TestGetMeteringPoints_InvalidPagination(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)

	cases := []string{"?page=0", "?pageSize=0", "?pageSize=101"}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			t.Parallel()
			r := c.Do(http.MethodGet, "/api/v1/energy/metering-points"+q, nil)
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var problem problemJSON
			r.JSON(&problem)
			if problem.Title != "Invalid query parameters" {
				t.Errorf("Title = %q, want %q", problem.Title, "Invalid query parameters")
			}
			want := "Page must be at least 1 and pageSize must be between 1 and 100."
			if problem.Detail != want {
				t.Errorf("Detail = %q, want %q", problem.Detail, want)
			}
		})
	}
}

func TestGetMeteringPoints_SearchMatchesGsrn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := h.SignIn(t, allEnergyPermissions...)
	point := createMeteringPoint(t, c)

	r := c.Do(http.MethodGet, "/api/v1/energy/metering-points?search="+point.Gsrn, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var list meteringPointListJSON
	r.JSON(&list)
	found := false
	for _, p := range list.Data {
		if p.Id == point.Id {
			found = true
		}
	}
	if !found {
		t.Errorf("search for %q did not find metering point %d among %d results", point.Gsrn, point.Id, len(list.Data))
	}
}

// withField is a small map-copying helper so table-driven test cases don't
// share (and mutate) newMeteringPointBody's map.
func withField(body map[string]any, field string, value any) map[string]any {
	out := map[string]any{}
	for k, v := range body {
		out[k] = v
	}
	if value == nil {
		delete(out, field)
	} else {
		out[field] = value
	}
	return out
}

// TestMeteringPoint_BillingFacts pins energy tariffs plan readings 5, 7 and
// 8 on the metering point: the grid tariff, household use and the tax class
// written on the create and the PUT and answered with the tariff's name and
// the derived VAT treatment, in the item, the list and the customer's
// points; a power tariff, an archived one new to the point and an unknown
// class refused; the archived tariff the point holds kept; and the PUT a
// full replace, so absent fields fall back to none, false and ordinary.
func TestMeteringPoint_BillingFacts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin := h.SignIn(t, tariffPermissions...)
	c := h.SignIn(t, allEnergyPermissions...)
	grid := createTariff(t, admin, gridTariffBody("Nett nord", "2026-01-01"))
	power := createTariff(t, admin, powerTariffBody("Spot", "2026-01-01"))
	archived := archivedTariff(t, admin, gridTariffBody("Old", "2025-01-01"))

	tromso := newMeteringPointBody(gsrn(t), "Tromsø meter")
	tromso["address"] = map[string]any{"streetAddress": "Storgata 1", "postalCode": "9008", "city": "Tromsø", "countryCode": "NO"}
	tromso["priceArea"] = "NO4"
	tromso["gridTariffId"] = grid.Id
	tromso["household"] = true
	tromso["electricityTaxClass"] = "exempt"
	r := c.Do(http.MethodPost, "/api/v1/energy/metering-points", tromso)
	if r.Status != http.StatusCreated {
		t.Fatalf("create: status %d body %s, want 201", r.Status, r.Body)
	}
	var point meteringPointJSON
	r.JSON(&point)
	if point.GridTariffId == nil || *point.GridTariffId != grid.Id || point.GridTariffName == nil || *point.GridTariffName != "Nett nord" ||
		!point.Household || point.ElectricityTaxClass != "exempt" || point.VatTreatment != "exempt_household_north" {
		t.Errorf("Tromsø household = %+v, want Nett nord, household, exempt and exempt_household_north", point)
	}

	oslo := createMeteringPoint(t, c)
	osloBody := withField(updateBody(oslo.Gsrn), "household", true)
	var osloPoint meteringPointJSON
	if put := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/energy/metering-points/%d", oslo.Id), osloBody); put.Status != http.StatusOK {
		t.Fatalf("put Oslo: status %d body %s", put.Status, put.Body)
	} else {
		put.JSON(&osloPoint)
	}
	if !osloPoint.Household || osloPoint.VatTreatment != "standard" || osloPoint.ElectricityTaxClass != "ordinary" || osloPoint.GridTariffId != nil {
		t.Errorf("Oslo household = %+v, want household, standard, ordinary and no grid tariff", osloPoint)
	}

	t.Run("refusals", func(t *testing.T) {
		wantFieldError(t, c.Do(http.MethodPost, "/api/v1/energy/metering-points",
			withField(newMeteringPointBody(gsrn(t), "M"), "gridTariffId", power.Id)),
			"gridTariffId", fmt.Sprintf("Tariff %d is a power tariff, not a grid tariff.", power.Id))
		wantFieldError(t, c.Do(http.MethodPost, "/api/v1/energy/metering-points",
			withField(newMeteringPointBody(gsrn(t), "M"), "gridTariffId", archived.Id)),
			"gridTariffId", fmt.Sprintf("Tariff %d is archived.", archived.Id))
		wantFieldError(t, c.Do(http.MethodPut, fmt.Sprintf("/api/v1/energy/metering-points/%d", oslo.Id),
			withField(updateBody(oslo.Gsrn), "gridTariffId", 999999)),
			"gridTariffId", "Tariff 999999 does not exist.")
		wantFieldError(t, c.Do(http.MethodPut, fmt.Sprintf("/api/v1/energy/metering-points/%d", oslo.Id),
			withField(updateBody(oslo.Gsrn), "electricityTaxClass", "half")),
			"electricityTaxClass", "Electricity tax class must be ordinary, reduced or exempt.")
	})

	// The grid tariff the point holds, archived since, is kept by a PUT that
	// repeats it.
	if a := admin.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/tariffs/%d/archive", grid.Id), nil); a.Status != http.StatusOK {
		t.Fatalf("archive: status %d", a.Status)
	}
	keep := updateBody(point.Gsrn)
	keep["address"] = tromso["address"]
	keep["gridTariffId"], keep["household"], keep["electricityTaxClass"] = grid.Id, true, "reduced"
	put := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/energy/metering-points/%d", point.Id), keep)
	if put.Status != http.StatusOK {
		t.Fatalf("put with the held archived tariff: status %d body %s, want 200", put.Status, put.Body)
	}
	var kept meteringPointJSON
	put.JSON(&kept)
	if kept.GridTariffId == nil || *kept.GridTariffId != grid.Id || kept.ElectricityTaxClass != "reduced" {
		t.Errorf("kept = %+v, want the archived grid tariff and reduced", kept)
	}

	// Supplied to customer 1001, the customer's points carry the facts too.
	if sp := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/energy/metering-points/%d/supply-periods", point.Id),
		map[string]any{"customerId": 1001, "start": h.Now()}); sp.Status != http.StatusCreated {
		t.Fatalf("supply: status %d body %s", sp.Status, sp.Body)
	}
	var customerPoints []customerMeteringPointJSON
	c.Do(http.MethodGet, "/api/v1/energy/customers/1001/metering-points", nil).JSON(&customerPoints)
	if len(customerPoints) != 1 || customerPoints[0].MeteringPoint.VatTreatment != "exempt_household_north" ||
		customerPoints[0].MeteringPoint.GridTariffName == nil || *customerPoints[0].MeteringPoint.GridTariffName != "Nett nord" {
		t.Errorf("customer's points = %+v", customerPoints)
	}
	var list meteringPointListJSON
	c.Do(http.MethodGet, "/api/v1/energy/metering-points?search="+point.Gsrn, nil).JSON(&list)
	if len(list.Data) != 1 || list.Data[0].GridTariffName == nil || *list.Data[0].GridTariffName != "Nett nord" || !list.Data[0].Household {
		t.Errorf("list = %+v", list.Data)
	}

	// A PUT without the three is none, false and ordinary.
	bare := updateBody(point.Gsrn)
	bare["address"] = tromso["address"]
	reset := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/energy/metering-points/%d", point.Id), bare)
	if reset.Status != http.StatusOK {
		t.Fatalf("put bare: status %d body %s", reset.Status, reset.Body)
	}
	var cleared meteringPointJSON
	reset.JSON(&cleared)
	if cleared.GridTariffId != nil || cleared.GridTariffName != nil || cleared.Household || cleared.ElectricityTaxClass != "ordinary" || cleared.VatTreatment != "standard" {
		t.Errorf("after a bare PUT = %+v, want none, false, ordinary and standard", cleared)
	}
	// Released, the archived tariff cannot be put back.
	wantFieldError(t, c.Do(http.MethodPut, fmt.Sprintf("/api/v1/energy/metering-points/%d", point.Id), keep),
		"gridTariffId", fmt.Sprintf("Tariff %d is archived.", grid.Id))
}
