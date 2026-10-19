package identity_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// The personal time zone (time zones design D5): the profile sets it and
// clears it, the account answers the stored choice, and the session's
// effective zone and its source follow — the user's own while they have
// one, the workspace's when they have none.
func TestAccountProfile_TimeZoneIsSetClearedAndFollowedByTheSession(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	const email = "zone@example.test"
	id := h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)

	zones := func() sessionZones {
		t.Helper()
		var z sessionZones
		r := c.do(http.MethodGet, sessionPath, nil)
		if r.status != http.StatusOK {
			t.Fatalf("GET %s: status %d body %s", sessionPath, r.status, r.body)
		}
		r.json(&z)
		return z
	}
	business := sessionZones{TimeZone: "Europe/Oslo", TimeZoneSource: "business", BusinessTimeZone: "Europe/Oslo"}

	if a := getAccount(t, c); a.TimeZone != nil {
		t.Errorf("a new account's timeZone = %q, want null (follow the workspace)", *a.TimeZone)
	}
	if z := zones(); z != business {
		t.Errorf("a new session's zones = %+v, want %+v", z, business)
	}

	put := c.do(http.MethodPut, accountProfilePath, map[string]any{"displayName": "Zone", "timeZone": " America/New_York "})
	var putAccount account
	put.json(&putAccount)
	if put.status != http.StatusOK || putAccount.TimeZone == nil || *putAccount.TimeZone != "America/New_York" {
		t.Fatalf("PUT a zone: status %d body %s, want 200 and America/New_York trimmed", put.status, put.body)
	}
	if a := getAccount(t, c); a.TimeZone == nil || *a.TimeZone != "America/New_York" {
		t.Errorf("GET %s timeZone = %v, want America/New_York", accountPath, a.TimeZone)
	}
	if got := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND time_zone = 'America/New_York'`, id); got != 1 {
		t.Errorf("time_zone in the database is not America/New_York")
	}
	personal := sessionZones{TimeZone: "America/New_York", TimeZoneSource: "personal", BusinessTimeZone: "Europe/Oslo"}
	if z := zones(); z != personal {
		t.Errorf("zones after choosing one = %+v, want %+v", z, personal)
	}

	// Leaving it out keeps it, on PATCH and on PUT alike: a client that
	// predates the field (the language control, say) never clears it.
	if r := c.do(http.MethodPatch, accountProfilePath, map[string]any{"displayName": "Zone", "preferredLanguage": "nb"}); r.status != http.StatusOK {
		t.Fatalf("PATCH without timeZone: status %d body %s", r.status, r.body)
	}
	if r := c.do(http.MethodPut, accountPath, map[string]any{"displayName": "Zone"}); r.status != http.StatusOK {
		t.Fatalf("PUT without timeZone: status %d body %s", r.status, r.body)
	}
	if z := zones(); z != personal {
		t.Errorf("zones after edits that left timeZone out = %+v, want %+v", z, personal)
	}

	// The workspace's zone moving does not move a personal choice, and is
	// still answered beside it.
	if r := owner.do(http.MethodPut, workspacePath, map[string]any{"businessTimeZone": "Asia/Kolkata"}); r.status != http.StatusOK {
		t.Fatalf("PUT %s: status %d body %s", workspacePath, r.status, r.body)
	}
	if z := zones(); z != (sessionZones{TimeZone: "America/New_York", TimeZoneSource: "personal", BusinessTimeZone: "Asia/Kolkata"}) {
		t.Errorf("zones after the workspace moved = %+v, want the personal zone kept and Asia/Kolkata as the business", z)
	}

	// Null follows the workspace again, as does a blank name.
	patch := c.do(http.MethodPatch, accountProfilePath, map[string]any{"displayName": "Zone", "timeZone": nil})
	var cleared account
	patch.json(&cleared)
	if patch.status != http.StatusOK || cleared.TimeZone != nil {
		t.Fatalf("PATCH null: status %d body %s, want 200 and timeZone null", patch.status, patch.body)
	}
	if z := zones(); z != (sessionZones{TimeZone: "Asia/Kolkata", TimeZoneSource: "business", BusinessTimeZone: "Asia/Kolkata"}) {
		t.Errorf("zones after clearing = %+v, want the workspace's Asia/Kolkata", z)
	}
	if r := c.do(http.MethodPatch, accountProfilePath, map[string]any{"displayName": "Zone", "timeZone": "UTC"}); r.status != http.StatusOK {
		t.Fatalf("PATCH UTC: status %d body %s", r.status, r.body)
	}
	if r := c.do(http.MethodPut, accountProfilePath, map[string]any{"displayName": "Zone", "timeZone": "  "}); r.status != http.StatusOK {
		t.Fatalf("PUT a blank zone: status %d body %s", r.status, r.body)
	}
	if a := getAccount(t, c); a.TimeZone != nil {
		t.Errorf("timeZone after a blank name = %q, want null", *a.TimeZone)
	}
}

// A personal zone is checked as the workspace's is — Go must load it and
// the database must read it the same way — and refused on timeZone in the
// profile's own invalid_request shape, with nothing written.
func TestAccountProfile_TimeZoneIsCheckedByGoAndByTheDatabase(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const email = "badzone@example.test"
	h.seedUser(t, email, userPassword, identity.RoleUserID)
	c := h.login(t, email, userPassword)
	if r := c.do(http.MethodPut, accountProfilePath, map[string]any{"displayName": "Before", "timeZone": "Europe/Oslo"}); r.status != http.StatusOK {
		t.Fatalf("PUT Europe/Oslo: status %d body %s", r.status, r.body)
	}

	for _, zone := range []any{"CET", "cet", "Local", "Mars/Olympus_Mons", "+02:00", 42} {
		for _, method := range []string{http.MethodPut, http.MethodPatch} {
			var opts []reqOpt
			if _, isString := zone.(string); !isString {
				opts = append(opts, skipContract("a number where the contract names a string is the request under test"))
			}
			r := c.do(method, accountProfilePath, map[string]any{"displayName": "After", "timeZone": zone}, opts...)
			if r.status != http.StatusBadRequest || r.code() != "invalid_request" {
				t.Errorf("%s zone %v: status %d code %q body %s, want 400 invalid_request", method, zone, r.status, r.code(), r.body)
				continue
			}
			if msgs := fieldErrors(t, r)["timeZone"]; len(msgs) == 0 {
				t.Errorf("%s zone %v: fields %v, want one on timeZone", method, zone, fieldErrors(t, r))
			}
		}
	}
	// Every field's refusal comes in one answer.
	r := c.do(http.MethodPut, accountProfilePath, map[string]any{"displayName": " ", "timeZone": "CET"})
	if fields := fieldErrors(t, r); len(fields["displayName"]) == 0 || len(fields["timeZone"]) == 0 {
		t.Errorf("a blank name and CET: fields %v, want both refused", fields)
	}

	a := getAccount(t, c)
	if a.DisplayName != "Before" || a.TimeZone == nil || *a.TimeZone != "Europe/Oslo" {
		t.Errorf("after the refusals: displayName %q timeZone %v, want Before and Europe/Oslo untouched", a.DisplayName, a.TimeZone)
	}
}
