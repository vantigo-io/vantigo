package entra_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// The connection (plan task 3, connection.go, events.go): its read, the
// Owner's registration and test, entra:manage's account rules, the
// tenant's licences, the events and the accounts.

func TestConnection_AFreshInstallation(t *testing.T) {
	t.Parallel()
	h, g := newTenantHarness(t)
	m, _ := manager(t, h)
	conn := getConnection(t, m)
	if conn.Status != gen.EntraConnectionStatusUnconfigured || conn.StatusReason != nil || conn.TenantId != nil || conn.ClientId != nil ||
		conn.Version != 1 || len(conn.Credentials) != 0 || conn.Accounts != (gen.EntraAccountCounts{}) || conn.LastCheckedAt != nil {
		t.Errorf("a fresh connection: %+v", conn)
	}
	if !slices.Equal(conn.PermissionsMissing, everyPermission) || len(conn.PermissionsPresent) != 0 || len(conn.VerifiedDomains) != 0 {
		t.Errorf("before a test every permission is missing: %v / %v", conn.PermissionsMissing, conn.PermissionsPresent)
	}
	s := conn.Settings
	if s.UpnPattern != "given.family" || s.DefaultUsageLocation != "NO" || !s.LeaverStripLicences || s.LeaverRemoveGroups != "all" ||
		!s.LeaverRevokeSessions || !s.LeaverWriteLeaveDate || !s.WriteHireDate || s.PassLifetimeMinutes != 480 || !s.PassUsableOnce ||
		s.PassDelivery != "hr" || s.UpnDomain != nil {
		t.Errorf("the default settings: %+v", s)
	}
	if n := len(g.Calls()); n != 0 {
		t.Errorf("GET /connection made %d calls to the tenant", n)
	}
}

func TestConnection_RegistrationValidatesAndHoldsTheVersion(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	o, ownerID := owner(t, h)
	body := map[string]any{"tenantId": tenantID, "clientId": clientID}

	r := o.Do(http.MethodPut, connectionPath, map[string]any{"tenantId": "contoso.onmicrosoft.com", "clientId": "{" + clientID + "}"}, ifMatch(1))
	if p := problemOf(t, r); r.Status != http.StatusBadRequest || p.Errors == nil || len((*p.Errors)["tenantId"]) == 0 || len((*p.Errors)["clientId"]) == 0 {
		t.Errorf("ids that are not GUIDs: %d %s, want field errors on both", r.Status, r.Body)
	}
	if r := o.Do(http.MethodPut, connectionPath, body, modtest.Header("If-Match", "latest")); r.Status != http.StatusBadRequest {
		t.Errorf("an If-Match that is not a version: %d %s, want 400", r.Status, r.Body)
	}
	if r := o.Do(http.MethodPut, connectionPath, body, ifMatch(7)); r.Status != http.StatusPreconditionFailed || codeOf(t, r) != "version_mismatch" {
		t.Errorf("a stale version: %d %s, want 412 version_mismatch", r.Status, r.Body)
	}

	r = o.Do(http.MethodPut, connectionPath, map[string]any{"tenantId": strings.ToUpper(tenantID), "clientId": clientID}, modtest.Header("If-Match", `"1"`))
	if r.Status != http.StatusOK {
		t.Fatalf("register: %d %s", r.Status, r.Body)
	}
	var conn gen.EntraConnection
	r.JSON(&conn)
	if conn.Version != 2 || conn.TenantId == nil || conn.TenantId.String() != tenantID || conn.ClientId.String() != clientID || conn.Status != gen.EntraConnectionStatusUnconfigured {
		t.Errorf("registered: %+v", conn)
	}
	codes := modtest.One[[]string](t, h.Harness, `SELECT codes FROM entra.events WHERE action = 'entra.connection.changed' AND actor_user_id = $1`, ownerID)
	if !slices.Equal(codes, []string{"tenant_before:none", "tenant_after:" + tenantID, "client_before:none", "client_after:" + clientID}) {
		t.Errorf("the event's codes = %v", codes)
	}

	// The same pair again writes nothing.
	r = o.Do(http.MethodPut, connectionPath, body, ifMatch(2))
	r.JSON(&conn)
	if r.Status != http.StatusOK || conn.Version != 2 {
		t.Errorf("the same pair: %d version %d, want 200 at version 2", r.Status, conn.Version)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.connection.changed'`); n != 1 {
		t.Errorf("%d change events, want one", n)
	}
}

func TestConnection_AChangeForgetsTheTestAndNeedsConfirmationWhileAccountsExist(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	o := connected(t, h)
	before := getConnection(t, o)
	if before.Status != gen.EntraConnectionStatusConnected || len(before.PermissionsPresent) != 8 || before.LastCheckedAt == nil {
		t.Fatalf("connected: %+v", before)
	}
	addAccount(t, h, "E-1", "kari.nordmann@nordlys.no")
	otherClient := "11111111-2222-4333-8444-555555555555"

	r := o.Do(http.MethodPut, connectionPath, map[string]any{"tenantId": tenantID, "clientId": otherClient}, ifMatch(before.Version))
	if r.Status != http.StatusConflict || codeOf(t, r) != "connection_in_use" {
		t.Fatalf("a change while accounts exist: %d %s, want 409 connection_in_use", r.Status, r.Body)
	}
	if after := getConnection(t, o); after.ClientId.String() != clientID || after.Version != before.Version {
		t.Errorf("the refused change wrote: %+v", after)
	}

	r = o.Do(http.MethodPut, connectionPath, map[string]any{"tenantId": tenantID, "clientId": otherClient, "confirmChange": true}, ifMatch(before.Version))
	if r.Status != http.StatusOK {
		t.Fatalf("a confirmed change: %d %s", r.Status, r.Body)
	}
	var after gen.EntraConnection
	r.JSON(&after)
	if after.ClientId.String() != otherClient || after.Version != before.Version+1 || after.Status != gen.EntraConnectionStatusUnconfigured ||
		len(after.PermissionsPresent) != 0 || len(after.VerifiedDomains) != 0 || after.OrganisationName != nil || after.LastCheckedAt != nil {
		t.Errorf("after the change: %+v, want unconfigured with the test forgotten", after)
	}
	if after.Accounts.Count != 1 || len(after.Credentials) != 1 {
		t.Errorf("the change deleted accounts or secrets: %+v", after)
	}
}

// TestConnection_AConfirmedTenantChangeForgetsThePasses is review L2: the
// 409 says the old tenant's accounts are no longer disabled or deleted by
// Vantigo, and a confirmed change of the tenant forgets every pass not yet
// revealed in its own transaction; a change of the client alone keeps them.
func TestConnection_AConfirmedTenantChangeForgetsThePasses(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	o := connected(t, h)
	id := addAccount(t, h, "E-1", "kari.nordmann@nordlys.no")
	h.Exec(t, `UPDATE entra.accounts SET pass_sealed = '\x01', pass_expires_at = $2 WHERE object_id = $1`, id, h.Now().Add(time.Hour))
	passes := func() int {
		return h.Count(t, `SELECT count(*) FROM entra.accounts WHERE pass_sealed IS NOT NULL OR pass_expires_at IS NOT NULL`)
	}
	otherTenant := "99999999-2222-4333-8444-555555555555"
	otherClient := "11111111-2222-4333-8444-555555555555"

	before := getConnection(t, o)
	r := o.Do(http.MethodPut, connectionPath, map[string]any{"tenantId": otherTenant, "clientId": clientID}, ifMatch(before.Version))
	if r.Status != http.StatusConflict || codeOf(t, r) != "connection_in_use" {
		t.Fatalf("a change while accounts exist: %d %s", r.Status, r.Body)
	}
	if p := problemOf(t, r); p.Detail == nil || !strings.Contains(*p.Detail, "will no longer be disabled or deleted by Vantigo") {
		t.Errorf("the 409's detail does not say what confirming costs: %+v", p.Detail)
	}

	r = o.Do(http.MethodPut, connectionPath, map[string]any{"tenantId": tenantID, "clientId": otherClient, "confirmChange": true}, ifMatch(before.Version))
	if r.Status != http.StatusOK {
		t.Fatalf("a confirmed change of the client: %d %s", r.Status, r.Body)
	}
	if n := passes(); n != 1 {
		t.Errorf("a change of the client alone forgot the pass: %d left", n)
	}
	r = o.Do(http.MethodPut, connectionPath, map[string]any{"tenantId": otherTenant, "clientId": otherClient, "confirmChange": true}, ifMatch(before.Version+1))
	if r.Status != http.StatusOK {
		t.Fatalf("a confirmed change of the tenant: %d %s", r.Status, r.Body)
	}
	if n := passes(); n != 0 {
		t.Errorf("a change of the tenant left %d passes", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.accounts`); n != 1 {
		t.Errorf("the change deleted the account's row: %d left", n)
	}
}

func TestConnection_TheTestReportsTheMissingPermissionsAndWritesTheDomains(t *testing.T) {
	t.Parallel()
	h, g := newTenantHarness(t)
	o, ownerID := owner(t, h)
	register(t, o)
	addSecret(t, o, secretValue, "Vantigo", h.Now().AddDate(1, 0, 0))
	g.Configure(func(g *graphtest.FakeGraph) {
		g.Roles = []string{"User.ReadWrite.All", "Organization.Read.All", "Directory.Read.All"}
	})

	result := testConnection(t, o)
	if result.Status != gen.EntraConnectionTestStatusConnected || result.Reason != nil || result.RoleKnown {
		t.Errorf("the test: %+v, want connected with roleKnown false", result)
	}
	wantMissing := []string{"User-LifeCycleInfo.ReadWrite.All", "User.RevokeSessions.All", "UserAuthMethod-TAP.ReadWrite.All",
		"LicenseAssignment.ReadWrite.All", "Group.ReadWrite.All", "Domain.Read.All"}
	if !slices.Equal(result.PermissionsMissing, wantMissing) {
		t.Errorf("missing = %v, want %v", result.PermissionsMissing, wantMissing)
	}
	if !slices.Equal(result.PermissionsPresent, []string{"Directory.Read.All", "Organization.Read.All", "User.ReadWrite.All"}) {
		t.Errorf("present = %v", result.PermissionsPresent)
	}
	if !slices.Equal(result.VerifiedDomains, []string{"nordlys.no", "nordlys.onmicrosoft.com"}) || result.OrganisationName == nil || *result.OrganisationName != "Nordlys AS" {
		t.Errorf("the organisation: %v %v", result.OrganisationName, result.VerifiedDomains)
	}

	conn := getConnection(t, o)
	if conn.Status != gen.EntraConnectionStatusConnected || !slices.Equal(conn.VerifiedDomains, result.VerifiedDomains) ||
		!slices.Equal(conn.PermissionsMissing, wantMissing) || conn.LastCheckedAt == nil || !conn.LastCheckedAt.Equal(h.Now()) || conn.Version != 2 {
		t.Errorf("the connection after the test: %+v", conn)
	}
	codes := modtest.One[[]string](t, h.Harness, `SELECT codes FROM entra.events WHERE action = 'entra.connection.tested' AND actor_user_id = $1`, ownerID)
	// Directory.Read.All is none of the module's: a warning after the
	// missing ones (design M19), the status connected all the same.
	if len(codes) != 2+len(wantMissing) || codes[0] != "connected" || codes[1] != "missing:User-LifeCycleInfo.ReadWrite.All" ||
		codes[len(codes)-1] != "unexpected:Directory.Read.All" {
		t.Errorf("the event's codes = %v", codes)
	}
	if !slices.Equal(result.PermissionsUnexpected, []string{"Directory.Read.All"}) {
		t.Errorf("unexpected = %v", result.PermissionsUnexpected)
	}

	// Consent granted since: the next test takes a fresh token and reads it.
	g.Configure(func(g *graphtest.FakeGraph) { g.Roles = slices.Clone(everyPermission) })
	if result := testConnection(t, o); len(result.PermissionsMissing) != 0 {
		t.Errorf("after consent: missing %v, want none", result.PermissionsMissing)
	}
}

func TestConnection_TheTestsFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		edit   func(g *graphtest.FakeGraph)
		reason string
	}{
		{"no consent at all", func(g *graphtest.FakeGraph) { g.Roles = nil }, "consent_missing"},
		{"the organisation refused", func(g *graphtest.FakeGraph) { g.FailNext(http.StatusForbidden) }, "consent_missing"},
		{"the tenant down", func(g *graphtest.FakeGraph) {
			g.FailNext(http.StatusServiceUnavailable)
			g.FailNext(http.StatusServiceUnavailable)
		}, "tenant_unreachable"},
		{"throttled twice", func(g *graphtest.FakeGraph) { g.Throttle(2) }, "tenant_unreachable"},
		{"a tenant that does not exist", func(g *graphtest.FakeGraph) { g.TenantID = "99999999-9999-4999-8999-999999999999" }, "tenant_unreachable"},
		{"an unknown client", func(g *graphtest.FakeGraph) { g.Secrets = map[string][]string{} }, "credential_invalid"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h, g := newTenantHarness(t)
			o, _ := owner(t, h)
			register(t, o)
			addSecret(t, o, secretValue, "Vantigo", h.Now().AddDate(1, 0, 0))
			c.edit(g) // nothing is in flight; FailNext and Throttle take the fake's lock themselves
			result := testConnection(t, o)
			if result.Status != gen.EntraConnectionTestStatusFailing || result.Reason == nil || *result.Reason != c.reason || result.RoleKnown {
				t.Errorf("the test: %+v, want failing/%s", result, c.reason)
			}
			conn := getConnection(t, o)
			if conn.Status != gen.EntraConnectionStatusFailing || conn.StatusReason == nil || *conn.StatusReason != c.reason || conn.LastCheckedAt == nil {
				t.Errorf("the connection: %s/%v, want failing/%s", conn.Status, conn.StatusReason, c.reason)
			}
		})
	}
}

func TestConnection_TheTestBeforeRegistration(t *testing.T) {
	t.Parallel()
	h, g := newTenantHarness(t)
	o, _ := owner(t, h)
	result := testConnection(t, o)
	if result.Status != gen.EntraConnectionTestStatusUnconfigured || result.Reason != nil || !slices.Equal(result.PermissionsMissing, everyPermission) {
		t.Errorf("a test with nothing registered: %+v", result)
	}
	if len(g.Calls()) != 0 || h.Count(t, `SELECT count(*) FROM entra.events`) != 0 {
		t.Error("a test with nothing registered reached the tenant or wrote an event")
	}
}

func TestSettings_ByTable(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		tested bool
		body   map[string]any
		field  string // the refused field, "" when it passes
		check  func(s gen.EntraSettings) bool
	}{
		{"any domain before a test", false, map[string]any{"upnDomain": " Example.COM "}, "", func(s gen.EntraSettings) bool { return *s.UpnDomain == "example.com" }},
		{"not a domain", false, map[string]any{"upnDomain": "not a domain"}, "upnDomain", nil},
		{"a bare label", false, map[string]any{"upnDomain": "localhost"}, "upnDomain", nil},
		{"a verified domain after a test", true, map[string]any{"upnDomain": "NORDLYS.no"}, "", func(s gen.EntraSettings) bool { return *s.UpnDomain == "nordlys.no" }},
		{"an unverified domain after a test", true, map[string]any{"upnDomain": "nordlys.example"}, "upnDomain", nil},
		{"a pattern", false, map[string]any{"upnPattern": "g.family"}, "", func(s gen.EntraSettings) bool { return s.UpnPattern == "g.family" }},
		{"an unknown pattern", false, map[string]any{"upnPattern": "family.given"}, "upnPattern", nil},
		{"a usage location", false, map[string]any{"defaultUsageLocation": "se"}, "", func(s gen.EntraSettings) bool { return s.DefaultUsageLocation == "SE" }},
		{"an unassigned country", false, map[string]any{"defaultUsageLocation": "XX"}, "defaultUsageLocation", nil},
		{"three letters", false, map[string]any{"defaultUsageLocation": "NOR"}, "defaultUsageLocation", nil},
		{"groups: managed", false, map[string]any{"leaverRemoveGroups": "managed"}, "", func(s gen.EntraSettings) bool { return s.LeaverRemoveGroups == "managed" }},
		{"groups: some", false, map[string]any{"leaverRemoveGroups": "some"}, "leaverRemoveGroups", nil},
		{"the shortest pass", false, map[string]any{"passLifetimeMinutes": 10}, "", func(s gen.EntraSettings) bool { return s.PassLifetimeMinutes == 10 }},
		{"the longest pass", false, map[string]any{"passLifetimeMinutes": 43200}, "", func(s gen.EntraSettings) bool { return s.PassLifetimeMinutes == 43200 }},
		{"a pass too short", false, map[string]any{"passLifetimeMinutes": 9}, "passLifetimeMinutes", nil},
		{"a pass too long", false, map[string]any{"passLifetimeMinutes": 43201}, "passLifetimeMinutes", nil},
		{"delivery: both", false, map[string]any{"passDelivery": "both"}, "", func(s gen.EntraSettings) bool { return s.PassDelivery == "both" }},
		{"delivery: post", false, map[string]any{"passDelivery": "post"}, "passDelivery", nil},
		{"the switches", false, map[string]any{"leaverStripLicences": false, "leaverRevokeSessions": false, "leaverWriteLeaveDate": false, "writeHireDate": false, "passUsableOnce": false},
			"", func(s gen.EntraSettings) bool {
				return !s.LeaverStripLicences && !s.LeaverRevokeSessions && !s.LeaverWriteLeaveDate && !s.WriteHireDate && !s.PassUsableOnce
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h, _ := newTenantHarness(t)
			if c.tested {
				connected(t, h)
			}
			m, managerID := manager(t, h)
			conn := getConnection(t, m)
			r := m.Do(http.MethodPut, settingsPath, c.body, ifMatch(conn.Version))
			if c.field != "" {
				if p := problemOf(t, r); r.Status != http.StatusBadRequest || p.Errors == nil || len((*p.Errors)[c.field]) == 0 || len(*p.Errors) != 1 {
					t.Errorf("%v: %d %s, want one field error on %s", c.body, r.Status, r.Body, c.field)
				}
				if after := getConnection(t, m); after.Version != conn.Version || after.Settings != conn.Settings {
					t.Errorf("a refused PUT wrote: %+v", after.Settings)
				}
				return
			}
			if r.Status != http.StatusOK {
				t.Fatalf("%v: %d %s", c.body, r.Status, r.Body)
			}
			var after gen.EntraConnection
			r.JSON(&after)
			if after.Version != conn.Version+1 || !c.check(after.Settings) {
				t.Errorf("%v: version %d, settings %+v", c.body, after.Version, after.Settings)
			}
			codes := modtest.One[[]string](t, h.Harness, `SELECT codes FROM entra.events WHERE action = 'entra.settings.changed' AND actor_user_id = $1`, managerID)
			for field := range c.body {
				if !slices.Contains(codes, field) {
					t.Errorf("the event's codes %v do not name %s", codes, field)
				}
			}
		})
	}
}

func TestSettings_AFieldLeftOutIsKeptAndTheVersionHolds(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	m, _ := manager(t, h)
	r := m.Do(http.MethodPut, settingsPath, map[string]any{"upnPattern": "given", "passLifetimeMinutes": 60, "upnDomain": "example.com"}, ifMatch(1))
	if r.Status != http.StatusOK {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	r = m.Do(http.MethodPut, settingsPath, map[string]any{"passDelivery": "manager"}, ifMatch(2))
	var conn gen.EntraConnection
	r.JSON(&conn)
	s := conn.Settings
	if r.Status != http.StatusOK || s.UpnPattern != "given" || s.PassLifetimeMinutes != 60 || *s.UpnDomain != "example.com" || s.PassDelivery != "manager" || conn.Version != 3 {
		t.Errorf("after two PUTs: %d %+v version %d", r.Status, s, conn.Version)
	}
	if r := m.Do(http.MethodPut, settingsPath, map[string]any{"passDelivery": "hr"}, ifMatch(2)); r.Status != http.StatusPreconditionFailed || codeOf(t, r) != "version_mismatch" {
		t.Errorf("a stale version: %d %s, want 412 version_mismatch", r.Status, r.Body)
	}
	if r := m.Do(http.MethodPut, settingsPath, map[string]any{}, modtest.Header("If-Match", "-1")); r.Status != http.StatusBadRequest {
		t.Errorf("a negative If-Match: %d, want 400", r.Status)
	}
}

func TestSkus_TheTenantsLicences(t *testing.T) {
	t.Parallel()
	h, g := newTenantHarness(t)
	m, _ := manager(t, h)
	if r := m.Do(http.MethodGet, skusPath, nil); r.Status != http.StatusServiceUnavailable || codeOf(t, r) != "credential_invalid" {
		t.Errorf("before registration: %d %s, want 503 credential_invalid", r.Status, r.Body)
	}
	connected(t, h)

	r := m.Do(http.MethodGet, skusPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /skus: %d %s", r.Status, r.Body)
	}
	var skus []gen.EntraSku
	r.JSON(&skus)
	if len(skus) != 2 || skus[0].SkuPartNumber != "ENTERPRISEPACK" || skus[0].SkuId.String() != skuE3 || skus[0].Enabled != 25 || skus[0].Consumed != 20 ||
		skus[1].SkuPartNumber != "STANDARDPACK" {
		t.Errorf("the licences: %+v", skus)
	}

	g.FailNext(http.StatusInternalServerError)
	g.FailNext(http.StatusInternalServerError)
	if r := m.Do(http.MethodGet, skusPath, nil); r.Status != http.StatusServiceUnavailable || codeOf(t, r) != "tenant_unreachable" {
		t.Errorf("the tenant down: %d %s, want 503 tenant_unreachable", r.Status, r.Body)
	}
	if strings.Contains(string(r.Body), "fake was told") {
		t.Error("Graph's words reached the answer")
	}
}

// TestSkus_RateLimitedInABucketOfItsOwn is review L8: GET /connection/skus
// reaches the tenant on every request, so it is limited to 30 a minute per
// client address — entra-skus, not the Owner operations' bucket.
func TestSkus_RateLimitedInABucketOfItsOwn(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	o := connected(t, h)
	m, _ := manager(t, h)
	for i := 1; i <= 30; i++ {
		if r := m.Do(http.MethodGet, skusPath, nil); r.Status != http.StatusOK {
			t.Fatalf("GET /skus %d: %d %s", i, r.Status, r.Body)
		}
	}
	if r := m.Do(http.MethodGet, skusPath, nil); r.Status != http.StatusTooManyRequests {
		t.Errorf("GET /skus 31: %d, want 429", r.Status)
	}
	if result := testConnection(t, o); result.Status != gen.EntraConnectionTestStatusConnected {
		t.Errorf("the Owner's test after the licences' limit: %+v, want its own bucket", result)
	}
}

func TestEvents_TheNewestFirstWithinTheLimit(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	o, ownerID := owner(t, h)
	register(t, o)
	h.Advance(time.Second)
	addSecret(t, o, secretValue, "Vantigo", h.Now().AddDate(1, 0, 0))
	h.Advance(time.Second)
	testConnection(t, o)
	h.Exec(t, `INSERT INTO entra.events (at, actor_system, action, object_id, upn, codes)
	           VALUES ($1, 'system:staff-accounts', 'entra.user.prepared', gen_random_uuid(), 'kari.nordmann@nordlys.no', '{created}')`, h.Now().Add(time.Second))

	m, _ := manager(t, h)
	r := m.Do(http.MethodGet, eventsPath, nil)
	var events []gen.EntraEvent
	r.JSON(&events)
	if r.Status != http.StatusOK || len(events) != 4 {
		t.Fatalf("GET /events: %d %s", r.Status, r.Body)
	}
	actions := []string{events[0].Action, events[1].Action, events[2].Action, events[3].Action}
	if !slices.Equal(actions, []string{"entra.user.prepared", "entra.connection.tested", "entra.credential.added", "entra.connection.changed"}) {
		t.Errorf("the order: %v", actions)
	}
	if events[0].Actor != "system:staff-accounts" || events[0].ObjectId == nil || *events[0].Upn != "kari.nordmann@nordlys.no" ||
		events[1].Actor != ownerID.String() || events[1].ObjectId != nil || events[1].Upn != nil {
		t.Errorf("the actors and the subjects: %+v", events[:2])
	}

	r = m.Do(http.MethodGet, eventsPath+"?limit=2", nil)
	r.JSON(&events)
	if r.Status != http.StatusOK || len(events) != 2 || events[0].Action != "entra.user.prepared" {
		t.Errorf("limit=2: %d %s", r.Status, r.Body)
	}
	for _, bad := range []string{"0", "201", "-3"} {
		if r := m.Do(http.MethodGet, eventsPath+"?limit="+bad, nil); r.Status != http.StatusBadRequest {
			t.Errorf("limit=%s: %d, want 400", bad, r.Status)
		}
	}
	if r := m.Do(http.MethodGet, eventsPath+"?limit=200", nil); r.Status != http.StatusOK {
		t.Errorf("limit=200: %d", r.Status)
	}
}

func TestAccounts_TheRowsWithoutThePass(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	m, _ := manager(t, h)
	if r := m.Do(http.MethodGet, accountsPath, nil); r.Status != http.StatusOK || strings.TrimSpace(string(r.Body)) != "[]" {
		t.Errorf("no accounts: %d %s, want []", r.Status, r.Body)
	}
	waiting := addAccount(t, h, "E-2", "ola.nordmann@nordlys.no")
	expired := addAccount(t, h, "E-1", "kari.nordmann@nordlys.no")
	h.Exec(t, `UPDATE entra.accounts SET pass_sealed = '\x0102'::bytea, pass_expires_at = $2, state = 'enabled', reported = '{hire_date_not_written}' WHERE object_id = $1`, waiting, h.Now().Add(time.Hour))
	h.Exec(t, `UPDATE entra.accounts SET pass_sealed = '\x0304'::bytea, pass_expires_at = $2 WHERE object_id = $1`, expired, h.Now().Add(-time.Minute))

	r := m.Do(http.MethodGet, accountsPath, nil)
	var accounts []gen.EntraAccount
	r.JSON(&accounts)
	if r.Status != http.StatusOK || len(accounts) != 2 {
		t.Fatalf("GET /accounts: %d %s", r.Status, r.Body)
	}
	kari, ola := accounts[0], accounts[1]
	if kari.UserPrincipalName != "kari.nordmann@nordlys.no" || kari.HasPass || kari.State != "disabled" || len(kari.Reported) != 0 || !kari.CreatedByUs {
		t.Errorf("an expired pass: %+v", kari)
	}
	if ola.EmployeeNumber != "E-2" || !ola.HasPass || ola.State != "enabled" || !slices.Equal(ola.Reported, []string{"hire_date_not_written"}) {
		t.Errorf("a pass waiting: %+v", ola)
	}
	for _, leak := range []string{"sealed", "passExpires", "AQI", "\\u0001"} {
		if strings.Contains(string(r.Body), leak) {
			t.Errorf("the answer carries %q: %s", leak, r.Body)
		}
	}
}

// insertOutlookRow puts a person's connection in state for oid, as a
// complete would have.
func insertOutlookRow(t *testing.T, h *harness, oid uuid.UUID, state string) {
	t.Helper()
	h.Exec(t, `INSERT INTO entra.outlook_connections (object_id, user_id, tenant_id, scopes, refresh_sealed, state, connected_at, changed_at, created_at, updated_at)
	           VALUES ($1, $2, $3, '{calendars.readwrite}', '\x01', $4, $5, $5, $5, $5)`, oid, uuid.New(), tenantID, state, h.Now())
}

// outlookConnect's five statuses (Outlook connection design M2), in their
// order — off, unavailable, consent_missing, ready, untested — and its
// counts of the people connected and waiting to reconnect.
func TestConnection_OutlookConnectsStatusAndCounts(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	o := connected(t, h)
	status := func(want gen.EntraOutlookConnectStatus) gen.EntraOutlookConnect {
		t.Helper()
		got := getConnection(t, o).OutlookConnect
		if got.Status != want {
			t.Errorf("status = %s, want %s (%+v)", got.Status, want, got)
		}
		return got
	}
	status(gen.EntraOutlookConnectStatusOff)

	on := setOutlookConnect(t, o, true)
	if on.OutlookConnect.Status != gen.EntraOutlookConnectStatusUntested {
		t.Errorf("turned on: %s, want untested", on.OutlookConnect.Status)
	}
	if got := status(gen.EntraOutlookConnectStatusUntested); got.Connected != 0 || got.Reconnect != 0 {
		t.Errorf("counts = %d/%d, want none", got.Connected, got.Reconnect)
	}

	// A connection made before the switch was last turned on is no proof.
	h.Exec(t, `UPDATE entra.connection SET outlook_connect_at = $1`, h.Now().Add(time.Hour))
	insertOutlookRow(t, h, uuid.New(), "active")
	status(gen.EntraOutlookConnectStatusUntested)
	h.Exec(t, `UPDATE entra.connection SET outlook_connect_at = $1`, h.Now())
	insertOutlookRow(t, h, uuid.New(), "active")
	insertOutlookRow(t, h, uuid.New(), "reconnect")
	insertOutlookRow(t, h, uuid.New(), "closing")
	if got := status(gen.EntraOutlookConnectStatusReady); got.Connected != 2 || got.Reconnect != 1 {
		t.Errorf("counts = %d/%d, want 2 connected and 1 to reconnect", got.Connected, got.Reconnect)
	}

	h.Exec(t, `UPDATE entra.connection SET outlook_last_error = 'consent_missing', outlook_last_error_at = $1`, h.Now())
	status(gen.EntraOutlookConnectStatusConsentMissing)

	h.Exec(t, `UPDATE entra.connection SET status = 'failing', status_reason = 'tenant_unreachable'`)
	if got := status(gen.EntraOutlookConnectStatusUnavailable); got.Reason == nil || *got.Reason != "tenant_unreachable" {
		t.Errorf("reason = %v, want the connection's", got.Reason)
	}
}

// Turning the switch on records when; turning it on again does not move
// it; turning it off closes every live connection, its token kept for the
// removals, one entra.outlook.closed event each under the caller.
func TestSettings_TheOutlookSwitchOnAndOff(t *testing.T) {
	t.Parallel()
	h, _ := newTenantHarness(t)
	o, ownerID := owner(t, h)
	register(t, o)
	addSecret(t, o, secretValue, "Vantigo", h.Now().AddDate(1, 0, 0))
	testConnection(t, o)

	setOutlookConnect(t, o, true)
	turnedOn := h.Now()
	if got := modtest.One[time.Time](t, h.Harness, `SELECT outlook_connect_at FROM entra.connection`); !got.Equal(turnedOn) {
		t.Errorf("outlook_connect_at = %v, want %v", got, turnedOn)
	}
	h.Advance(time.Hour)
	setOutlookConnect(t, o, true)
	if got := modtest.One[time.Time](t, h.Harness, `SELECT outlook_connect_at FROM entra.connection`); !got.Equal(turnedOn) {
		t.Errorf("on again moved outlook_connect_at to %v", got)
	}

	live := []uuid.UUID{addAccount(t, h, "E-1", "kari@nordlys.no"), addAccount(t, h, "E-2", "ola@nordlys.no")}
	insertOutlookRow(t, h, live[0], "active")
	insertOutlookRow(t, h, live[1], "reconnect")
	closing := uuid.New()
	insertOutlookRow(t, h, closing, "closing")

	setOutlookConnect(t, o, false)
	if n := h.Count(t, `SELECT count(*) FROM entra.outlook_connections WHERE state = 'closing' AND refresh_sealed IS NOT NULL AND revision = 2 AND changed_at = $1`, h.Now()); n != 2 {
		t.Errorf("%d connections closed with their tokens, want 2", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.outlook_connections WHERE object_id = $1 AND revision = 1`, closing); n != 1 {
		t.Error("a connection closing already was written again")
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.outlook.closed' AND codes = '{switch_off}' AND actor_user_id = $1
	    AND ((object_id = $2 AND upn = 'kari@nordlys.no') OR (object_id = $3 AND upn = 'ola@nordlys.no'))`, ownerID, live[0], live[1]); n != 2 {
		t.Errorf("%d closed events, want one per connection", n)
	}

	h.Advance(time.Hour)
	setOutlookConnect(t, o, true)
	if got := modtest.One[time.Time](t, h.Harness, `SELECT outlook_connect_at FROM entra.connection`); !got.Equal(h.Now()) {
		t.Errorf("on after off: outlook_connect_at = %v, want %v", got, h.Now())
	}
	if n := h.Count(t, `SELECT count(*) FROM entra.events WHERE action = 'entra.outlook.closed'`); n != 2 {
		t.Errorf("turning it on closed something: %d events", n)
	}
}

// permissionsUnexpected (design M19): the token's roles beyond the eight
// and Calendars.ReadWrite, a warning in GET /connection and the test's
// answer and an unexpected: code in the test's event — never a status.
func TestConnection_PermissionsUnexpected(t *testing.T) {
	t.Parallel()
	h, g := newTenantHarness(t)
	o := connected(t, h)
	if got := getConnection(t, o).PermissionsUnexpected; len(got) != 0 {
		t.Errorf("the eight alone: %v, want none", got)
	}

	g.Configure(func(g *graphtest.FakeGraph) { g.Roles = append(g.Roles, "Calendars.ReadWrite") })
	if got := testConnection(t, o).PermissionsUnexpected; len(got) != 0 {
		t.Errorf("with Calendars.ReadWrite: %v, want none", got)
	}

	g.Configure(func(g *graphtest.FakeGraph) { g.Roles = append(g.Roles, "Mail.Send", "Directory.ReadWrite.All") })
	result := testConnection(t, o)
	want := []string{"Directory.ReadWrite.All", "Mail.Send"}
	if result.Status != gen.EntraConnectionTestStatusConnected || !slices.Equal(result.PermissionsUnexpected, want) {
		t.Errorf("the test: %s %v, want connected warning of %v", result.Status, result.PermissionsUnexpected, want)
	}
	conn := getConnection(t, o)
	if conn.Status != gen.EntraConnectionStatusConnected || !slices.Equal(conn.PermissionsUnexpected, want) {
		t.Errorf("GET /connection: %s %v", conn.Status, conn.PermissionsUnexpected)
	}
	codes := modtest.One[[]string](t, h.Harness, `SELECT codes FROM entra.events WHERE action = 'entra.connection.tested' ORDER BY id DESC LIMIT 1`)
	if !slices.Equal(codes, []string{"connected", "unexpected:Directory.ReadWrite.All", "unexpected:Mail.Send"}) {
		t.Errorf("the test's codes = %v", codes)
	}
}
