package energy_test

import (
	"fmt"
	"net/http"
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
