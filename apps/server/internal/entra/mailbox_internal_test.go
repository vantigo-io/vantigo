package entra

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// TestMailboxStatusOf pins the mailbox provider's state and reason by the
// switch, the connection's status and the permission the last test saw
// (design O2, O4): available on and connected, the permission in the token
// or not (an Exchange RBAC grant never shows there); cleanup while a
// credential still works and the switch is off; unavailable otherwise, with
// the connection's reason.
func TestMailboxStatusOf(t *testing.T) {
	now := time.Date(2026, time.October, 27, 9, 0, 0, 0, time.UTC)
	tenant, client := uuid.New(), uuid.New()
	str := func(s string) *string { return &s }
	good := []store.EntraCredential{{ID: 1, ExpiresAt: now.AddDate(1, 0, 0), CreatedAt: now}}
	revoked := []store.EntraCredential{{ID: 1, ExpiresAt: now.AddDate(1, 0, 0), CreatedAt: now, RevokedAt: &now}}
	conn := func(on bool, status string, reason *string, perms ...string) store.EntraConnection {
		return store.EntraConnection{TenantID: &tenant, ClientID: &client, Status: status, StatusReason: reason,
			CalendarSync: on, PermissionsPresent: perms}
	}
	const cal = permissionCalendarsReadWrite
	for _, c := range []struct {
		name          string
		conn          store.EntraConnection
		creds         []store.EntraCredential
		state, reason string
	}{
		{"connected, on, permitted", conn(true, "connected", nil, cal), good, contracts.MailboxAvailable, ""},
		{"a secret expiring is still available", conn(true, "connected", nil, cal), []store.EntraCredential{{ID: 1, ExpiresAt: now.Add(time.Hour), CreatedAt: now}}, contracts.MailboxAvailable, ""},
		{"the switch off", conn(false, "connected", nil, cal), good, contracts.MailboxCleanup, reasonSwitchOff},
		{"the switch off, failing", conn(false, "failing", str("tenant_unreachable"), cal), good, contracts.MailboxCleanup, reasonSwitchOff},
		{"the permission not in the token", conn(true, "connected", nil, "User.ReadWrite.All"), good, contracts.MailboxAvailable, ""},
		{"the switch off, the secret refused", conn(false, "failing", str("credential_invalid"), cal), good, contracts.MailboxUnavailable, reasonSwitchOff},
		{"the permission missing, failing", conn(true, "failing", str("consent_missing")), good, contracts.MailboxUnavailable, "consent_missing"},
		{"failing", conn(true, "failing", str("tenant_unreachable"), cal), good, contracts.MailboxUnavailable, "tenant_unreachable"},
		{"failing with no reason", conn(true, "failing", nil, cal), good, contracts.MailboxUnavailable, "failing"},
		{"no secret", conn(true, "connected", nil, cal), revoked, contracts.MailboxUnavailable, "unconfigured"},
		{"no secret, the switch off", conn(false, "connected", nil, cal), nil, contracts.MailboxUnavailable, reasonSwitchOff},
		{"no tenant", store.EntraConnection{ClientID: &client, CalendarSync: true, Status: "connected", PermissionsPresent: []string{cal}}, good, contracts.MailboxUnavailable, "unconfigured"},
		{"every secret expired", conn(true, "connected", nil, cal), []store.EntraCredential{{ID: 1, ExpiresAt: now.Add(-time.Hour), CreatedAt: now}}, contracts.MailboxUnavailable, "secret_expired"},
	} {
		got := mailboxStatusOf(c.conn, c.creds, now)
		if got.State != c.state || got.Reason != c.reason {
			t.Errorf("%s: %s/%s, want %s/%s", c.name, got.State, got.Reason, c.state, c.reason)
		}
		if got.Reason != "" && !reasonPattern.MatchString(got.Reason) {
			t.Errorf("%s: reason %q outside the bound", c.name, got.Reason)
		}
	}
}

// TestMissingPermissions: Calendars.ReadWrite is asked for only while the
// switch is on and the token lacks it, after the eight the connection needs.
func TestMissingPermissions(t *testing.T) {
	all := append(slices.Clone(requiredPermissions), permissionCalendarsReadWrite)
	if got := missingPermissions(requiredPermissions, false); len(got) != 0 {
		t.Errorf("off, the eight present: %v, want none", got)
	}
	if got := missingPermissions(requiredPermissions, true); !slices.Equal(got, []string{permissionCalendarsReadWrite}) {
		t.Errorf("on, the calendar's absent: %v", got)
	}
	if got := missingPermissions(all, true); len(got) != 0 {
		t.Errorf("on, all present: %v, want none", got)
	}
	if got := missingPermissions(nil, false); !slices.Equal(got, requiredPermissions) {
		t.Errorf("off, none present: %v", got)
	}
	if got := missingPermissions(nil, true); !slices.Equal(got, all) {
		t.Errorf("on, none present: %v", got)
	}
}

// TestWindowsZoneOf: a golden zone, a territory's zone, an old spelling,
// UTC, and the fallback.
func TestWindowsZoneOf(t *testing.T) {
	for iana, want := range map[string]string{
		"Europe/Oslo":         "W. Europe Standard Time",
		"Europe/Berlin":       "W. Europe Standard Time",
		"Europe/Stockholm":    "W. Europe Standard Time",
		"Europe/Copenhagen":   "Romance Standard Time",
		"Europe/Helsinki":     "FLE Standard Time",
		"Europe/London":       "GMT Standard Time",
		"America/New_York":    "Eastern Standard Time",
		"America/Mexico_City": "Central Standard Time (Mexico)",
		"Asia/Kolkata":        "India Standard Time",
		"Asia/Calcutta":       "India Standard Time",
		"Etc/UTC":             "UTC",
		"UTC":                 "UTC",
		"":                    "UTC",
		"Mars/Olympus_Mons":   "UTC",
	} {
		if got := windowsZoneOf(iana); got != want {
			t.Errorf("windowsZoneOf(%q) = %q, want %q", iana, got, want)
		}
	}
	// Every Windows name the inversion answers is one of CLDR's.
	for iana, w := range ianaToWindows() {
		if _, ok := windowsZones[w]; !ok {
			t.Errorf("%s maps to %q, which is no Windows name of the table", iana, w)
		}
	}
}

// TestEventOf pins the event's JSON (design O4, O6): a timed event in UTC,
// an all-day one at midnight in the Windows zone with the end the day after
// the last, sensitivity, no reminder, the location cleared on an update,
// and the marker on create alone.
func TestEventOf(t *testing.T) {
	oslo, _ := time.LoadLocation("Europe/Oslo")
	timed := &contracts.MailboxEvent{
		Subject: "Standup", Body: "Kept by Vantigo", Location: "Rom 1",
		StartsAt: time.Date(2026, 11, 2, 9, 0, 0, 0, oslo), EndsAt: time.Date(2026, 11, 2, 9, 30, 0, 0, oslo),
		ShowAs: contracts.ShowAsTentative, Private: true,
	}
	create, err := eventOf(contracts.MailboxOp{Kind: contracts.MailboxOpCreate, Key: "appointment:42", Event: timed}, testPrefix)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(create)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	want := map[string]any{
		"subject":      "Standup",
		"body":         map[string]any{"contentType": "text", "content": "Kept by Vantigo"},
		"location":     map[string]any{"displayName": "Rom 1"},
		"isAllDay":     false,
		"start":        map[string]any{"dateTime": "2026-11-02T08:00:00", "timeZone": "UTC"},
		"end":          map[string]any{"dateTime": "2026-11-02T08:30:00", "timeZone": "UTC"},
		"showAs":       "tentative",
		"sensitivity":  "private",
		"isReminderOn": false,
		"singleValueExtendedProperties": []any{map[string]any{
			"id": "String {7b1c5e3a-6f0d-4d5e-9a40-3c7a5e2b9d11} Name VantigoKey", "value": testPrefix + "appointment:42",
		}},
	}
	wantRaw, _ := json.Marshal(want)
	if g, w := mustCanon(t, raw), mustCanon(t, wantRaw); string(g) != string(w) {
		t.Errorf("the create:\n got %s\nwant %s", g, w)
	}

	allDay := &contracts.MailboxEvent{
		Subject: "Ferie", AllDay: true, TimeZone: "Europe/Oslo", ShowAs: contracts.ShowAsOOF,
		StartDate: time.Date(2026, 12, 24, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 12, 26, 0, 0, 0, 0, time.UTC),
	}
	update, err := eventOf(contracts.MailboxOp{Kind: contracts.MailboxOpUpdate, EventID: "AAMk", Event: allDay}, testPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if update.Start != (graphDateTimeZone{DateTime: "2026-12-24T00:00:00", TimeZone: "W. Europe Standard Time"}) ||
		update.End != (graphDateTimeZone{DateTime: "2026-12-27T00:00:00", TimeZone: "W. Europe Standard Time"}) {
		t.Errorf("the all-day event: %+v – %+v", update.Start, update.End)
	}
	if !update.IsAllDay || update.Sensitivity != "normal" || update.ShowAs != "oof" || update.IsReminderOn {
		t.Errorf("the all-day event's fields: %+v", update)
	}
	if update.Location == nil || update.Location.DisplayName != "" {
		t.Errorf("an update's location: %+v, want cleared", update.Location)
	}
	if len(update.Properties) != 0 {
		t.Errorf("an update carries the marker: %+v", update.Properties)
	}
	raw, _ = json.Marshal(update)
	if strings.Contains(string(raw), "singleValueExtendedProperties") {
		t.Errorf("an update's JSON names the property: %s", raw)
	}
	noZone := *allDay
	noZone.TimeZone = ""
	if ev, _ := eventOf(contracts.MailboxOp{Kind: contracts.MailboxOpCreate, Key: "k", Event: &noZone}, testPrefix); ev.Start.TimeZone != "UTC" {
		t.Errorf("an all-day event with no zone: %+v, want UTC", ev.Start)
	}
	createNoLocation, _ := eventOf(contracts.MailboxOp{Kind: contracts.MailboxOpCreate, Key: "k", Event: allDay}, testPrefix)
	if createNoLocation.Location != nil {
		t.Errorf("a create with no location sends one: %+v", createNoLocation.Location)
	}

	for name, e := range map[string]*contracts.MailboxEvent{
		"an end before the start": {StartsAt: timed.EndsAt, EndsAt: timed.StartsAt},
		"an all-day end before":   {AllDay: true, StartDate: allDay.EndDate, EndDate: allDay.StartDate},
		"an unknown showAs":       {StartsAt: timed.StartsAt, EndsAt: timed.EndsAt, ShowAs: "away"},
	} {
		if _, err := eventOf(contracts.MailboxOp{Kind: contracts.MailboxOpCreate, Key: "k", Event: e}, testPrefix); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := eventOf(contracts.MailboxOp{Kind: contracts.MailboxOpCreate, Key: "k"}, testPrefix); err == nil {
		t.Error("a create with no event: no error")
	}
}

// testPrefix is the marker's prefix of an installation whose client id is
// 0f1e2d3c-4b5a-4968-8776-655443322110.
const testPrefix = "vantigo:0f1e2d3c-4b5a-4968-8776-655443322110:"

// TestValidID: an id Graph could have answered passes; nothing empty, too
// long, spaced, or a dot segment — bare, percent-encoded in either case, or
// encoded twice — reaches a $batch request's URL.
func TestValidID(t *testing.T) {
	for _, id := range []string{"AAMkAGI2TG93AAA=", "AAMk-_x.y", "a.b", "..."} {
		if !validID(id) {
			t.Errorf("validID(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", ".", "..", "%2e", "%2E", "%2e%2E", ".%2e", "%252e%252e", "a b", "a\x00", strings.Repeat("A", 513)} {
		if validID(id) {
			t.Errorf("validID(%q) = true, want false", id)
		}
	}
}

// TestBatchHeader: a $batch answer's header is read whatever its case,
// whether Graph sent a string or a number, and an answer with a header of
// another type still decodes.
func TestBatchHeader(t *testing.T) {
	var answer batchAnswer
	raw := `{"responses":[{"id":"0","status":429,"headers":{"retry-after":7,"X-Flag":true,"Content-Type":"application/json"}},
		{"id":"1","status":429,"headers":{"Retry-After":"3"}}]}`
	if err := json.Unmarshal([]byte(raw), &answer); err != nil {
		t.Fatal(err)
	}
	if got := batchHeader(answer.Responses[0].Headers, "Retry-After"); got != "7" {
		t.Errorf("a number: %q, want 7", got)
	}
	if got := batchHeader(answer.Responses[1].Headers, "Retry-After"); got != "3" {
		t.Errorf("a string: %q, want 3", got)
	}
	if got := batchHeader(answer.Responses[0].Headers, "X-Flag"); got != "" {
		t.Errorf("a boolean: %q, want none", got)
	}
}

// mustCanon is raw re-encoded through a map, its keys sorted, so two
// encodings compare by value.
func mustCanon(t *testing.T, raw []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(v)
	return out
}
