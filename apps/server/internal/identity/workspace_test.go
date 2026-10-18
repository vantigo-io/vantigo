package identity_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

const workspacePath = "/api/v1/identity/owner/workspace"

// workspaceBody decodes WorkspaceSettings.
type workspaceBody struct {
	BusinessTimeZone string `json:"businessTimeZone"`
}

// sessionZones is the three zone fields of AuthSessionResponse (time zones
// design D4).
type sessionZones struct {
	TimeZone         string `json:"timeZone"`
	TimeZoneSource   string `json:"timeZoneSource"`
	BusinessTimeZone string `json:"businessTimeZone"`
}

// fieldErrors is AuthErrorResponse's error.fields.
func fieldErrors(t testing.TB, r *resp) map[string][]string {
	t.Helper()
	var body struct {
		Error struct {
			Fields map[string][]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
	return body.Error.Fields
}

// A fresh installation runs on Europe/Oslo, with no settings row yet; the
// workspace's settings are an Owner's alone to read and change.
func TestWorkspace_TheZoneIsEuropeOsloUntilAnOwnerSetsIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	h.seedUser(t, "user@example.test", userPassword, identity.RoleUserID)
	user := h.login(t, "user@example.test", userPassword)

	r := owner.do(http.MethodGet, workspacePath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s", workspacePath, r.status, r.body)
	}
	var got workspaceBody
	r.json(&got)
	if got.BusinessTimeZone != "Europe/Oslo" {
		t.Errorf("businessTimeZone = %q, want the default Europe/Oslo", got.BusinessTimeZone)
	}
	if r := h.client(t).do(http.MethodGet, workspacePath, nil); r.status != http.StatusUnauthorized {
		t.Errorf("anonymous: status %d, want 401", r.status)
	}
	if r := user.do(http.MethodGet, workspacePath, nil); r.status != http.StatusForbidden {
		t.Errorf("a user: status %d, want 403", r.status)
	}
	if r := user.do(http.MethodPut, workspacePath, map[string]any{"businessTimeZone": "UTC"}); r.status != http.StatusForbidden {
		t.Errorf("a user changing it: status %d, want 403", r.status)
	}
}

// The zone is checked twice — by Go's tzdata and by the database's — and
// what is compared is what each means by the name: 'CET' is a zone with
// summer time to Go and a fixed +01:00 abbreviation to Postgres, so it is
// refused even though both "know" it, with a message naming a city zone to
// write instead. Nothing a refusal received is stored or audited.
func TestWorkspace_TheZoneIsCheckedByGoAndByTheDatabase(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)

	for _, zone := range []string{"", "  ", "Local", "Mars/Olympus_Mons", "+02:00", "CET", "EET", "MET", "WET", "cet"} {
		r := owner.do(http.MethodPut, workspacePath, map[string]any{"businessTimeZone": zone})
		if r.status != http.StatusBadRequest || r.code() != "invalid_request" {
			t.Errorf("zone %q: status %d code %q body %s, want 400 invalid_request", zone, r.status, r.code(), r.body)
			continue
		}
		if msgs := fieldErrors(t, r)["businessTimeZone"]; len(msgs) == 0 {
			t.Errorf("zone %q: fields %v, want one on businessTimeZone", zone, fieldErrors(t, r))
		}
	}
	var got workspaceBody
	owner.do(http.MethodGet, workspacePath, nil).json(&got)
	if got.BusinessTimeZone != "Europe/Oslo" {
		t.Errorf("after the refusals = %q, want the zone untouched", got.BusinessTimeZone)
	}
	if events := h.auditEvents(t, "workspace.business_time_zone_changed"); len(events) != 0 {
		t.Errorf("audit events after the refusals = %d, want none", len(events))
	}

	// City zones, and the fixed ones that mean the same to both, are
	// accepted — the check refuses disagreement, not abbreviation-shaped
	// names.
	for _, zone := range []string{"America/New_York", "UTC", "Asia/Kolkata", "Europe/Oslo"} {
		r := owner.do(http.MethodPut, workspacePath, map[string]any{"businessTimeZone": zone})
		if r.status != http.StatusOK {
			t.Fatalf("zone %q: status %d body %s, want 200", zone, r.status, r.body)
		}
		r.json(&got)
		if got.BusinessTimeZone != zone {
			t.Errorf("zone %q: answered %q", zone, got.BusinessTimeZone)
		}
	}
}

// A change is audited with the zone before and after, by the Owner who made
// it; the value already held changes nothing and audits nothing; and the
// session answers the workspace's zone as every signed-in user's effective
// zone until they choose their own.
func TestWorkspace_AChangeIsAuditedAndReachesTheSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)

	var zones sessionZones
	owner.do(http.MethodGet, sessionPath, nil).json(&zones)
	if zones != (sessionZones{TimeZone: "Europe/Oslo", TimeZoneSource: "business", BusinessTimeZone: "Europe/Oslo"}) {
		t.Errorf("a fresh session's zones = %+v, want Europe/Oslo from the business", zones)
	}

	if r := owner.do(http.MethodPut, workspacePath, map[string]any{"businessTimeZone": "America/New_York"}); r.status != http.StatusOK {
		t.Fatalf("PUT: status %d body %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPut, workspacePath, map[string]any{"businessTimeZone": "America/New_York"}); r.status != http.StatusOK {
		t.Fatalf("PUT the same again: status %d body %s", r.status, r.body)
	}
	events := h.auditEvents(t, "workspace.business_time_zone_changed")
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want exactly one for one change", len(events))
	}
	e := events[0]
	if e.Actor == nil || *e.Actor != ownerID || e.Before != `{"businessTimeZone":"Europe/Oslo"}` || e.After != `{"businessTimeZone":"America/New_York"}` {
		t.Errorf("audit event = %+v, want the Owner, Europe/Oslo before and America/New_York after", e)
	}

	owner.do(http.MethodGet, sessionPath, nil).json(&zones)
	if zones != (sessionZones{TimeZone: "America/New_York", TimeZoneSource: "business", BusinessTimeZone: "America/New_York"}) {
		t.Errorf("the session after the change = %+v, want America/New_York from the business", zones)
	}

	// A user who chose a zone of their own (delivery F gives it a setting;
	// the column is 00052's) sees theirs, and still knows the business's.
	h.exec(t, `UPDATE identity.users SET time_zone = 'Europe/Madrid' WHERE id = $1`, ownerID)
	owner.do(http.MethodGet, sessionPath, nil).json(&zones)
	if zones != (sessionZones{TimeZone: "Europe/Madrid", TimeZoneSource: "personal", BusinessTimeZone: "America/New_York"}) {
		t.Errorf("the session with a personal zone = %+v, want Europe/Madrid personal beside the business's", zones)
	}
}
