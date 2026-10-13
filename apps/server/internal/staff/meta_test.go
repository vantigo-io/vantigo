package staff_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// The module is mounted at /api/v1/staff with every operation behind
// staff:access: no session is the access layer's 401, a session without the
// permission its 403, and staff:access alone reads the metadata.
func TestModule_MountsBehindStaffAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	if r := h.Client(t).Do(http.MethodGet, metaPath, nil); r.Status != http.StatusUnauthorized {
		t.Errorf("no session: status %d, want 401", r.Status)
	}
	outsider, _ := h.SignInUser(t, "staff:view", "staff:manage")
	if r := outsider.Do(http.MethodGet, metaPath, nil); r.Status != http.StatusForbidden || r.Code() != "forbidden" {
		t.Errorf("without staff:access: status %d code %q, want the access layer's 403", r.Status, r.Code())
	}
	member, _ := signIn(t, h)
	getMeta(t, member)
}

// GET /meta answers what the caller may do, one capability per permission,
// and the structure to a caller holding staff:view or staff:manage — the
// directory and HR, who name units and departments in their lists and forms.
// Without either the key is absent, not empty or null.
func TestMeta_CapabilitiesAndTheStructureFollowTheCallersPermissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	hr, _ := signIn(t, h, "staff:view", "staff:manage")
	do(t, hr, http.MethodPost, legalEntitiesPath, map[string]any{"name": "Kraft-Verket AS"}, http.StatusCreated, nil)

	for _, c := range []struct {
		name      string
		perms     []string
		want      gen.StaffCapabilities
		structure bool
	}{
		{"staff:access alone", nil, gen.StaffCapabilities{}, false},
		{"staff:view", []string{"staff:view"}, gen.StaffCapabilities{CanViewDirectory: true}, true},
		{"staff:approve", []string{"staff:approve"}, gen.StaffCapabilities{CanDecide: true}, false},
		{"staff:manage without staff:view", []string{"staff:manage"},
			gen.StaffCapabilities{CanEdit: true, CanSeePay: true, CanDecide: true}, true},
		{"staff:personal-data", []string{"staff:personal-data"},
			gen.StaffCapabilities{CanRevealIdentifiers: true, CanSeePay: true, CanSeeHealth: true}, false},
		{"all five", []string{"staff:view", "staff:approve", "staff:manage", "staff:personal-data"},
			gen.StaffCapabilities{CanViewDirectory: true, CanEdit: true, CanRevealIdentifiers: true, CanSeePay: true, CanSeeHealth: true, CanDecide: true}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			caller, _ := signIn(t, h, c.perms...)
			r := caller.Do(http.MethodGet, metaPath, nil)
			if r.Status != http.StatusOK {
				t.Fatalf("meta: status %d body %s", r.Status, r.Body)
			}
			var meta gen.StaffMeta
			r.JSON(&meta)
			if meta.Capabilities != c.want {
				t.Errorf("capabilities = %+v, want %+v", meta.Capabilities, c.want)
			}
			if meta.TimeZone != "Europe/Oslo" {
				t.Errorf("timeZone = %q, want the default Europe/Oslo", meta.TimeZone)
			}
			_, present := raw(t, r)["structure"]
			if present != c.structure {
				t.Errorf("structure present = %v, want %v", present, c.structure)
			}
			if c.structure && meta.Structure != nil && (len(meta.Structure.LegalEntities) != 1 || meta.Structure.LegalEntities[0].Name != "Kraft-Verket AS") {
				t.Errorf("structure = %+v, want the one legal entity", meta.Structure)
			}
		})
	}
}

// "Today" is the business time zone's calendar day, computed once per request
// in Go: at 22:30 UTC on a summer evening Oslo is already past midnight, so
// the module's today is a day ahead of UTC's.
func TestMeta_TodayIsTheBusinessZonesDay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	member, _ := signIn(t, h)

	if got := getMeta(t, member).Today.String(); got != today(t, h) {
		t.Errorf("today = %s, want %s", got, today(t, h))
	}
	// A session does not outlive weeks of idle clock, so the evening's caller
	// signs in afresh.
	setClock(t, h, time.Date(2026, time.September, 30, 22, 30, 0, 0, time.UTC))
	member, _ = signIn(t, h)
	if got := getMeta(t, member).Today.String(); got != "2026-10-01" {
		t.Errorf("at 22:30 UTC on 30 September today = %s, want Oslo's 2026-10-01", got)
	}
	if got := today(t, h); got != "2026-10-01" {
		t.Errorf("the harness's today = %s, want 2026-10-01", got)
	}
}
