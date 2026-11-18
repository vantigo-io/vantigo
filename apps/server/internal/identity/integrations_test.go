package identity_test

import (
	"context"
	"encoding/hex"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// The integrations service (API access design K8, K11-K13, K21, K22a): an
// Owner's screen for an external application's account and its API keys.

const integrationsPath = "/api/v1/identity/integrations"

// integrationDetail is IntegrationDetail as these tests read it.
type integrationDetail struct {
	Integration struct {
		ID       uuid.UUID `json:"id"`
		Name     string    `json:"name"`
		Status   string    `json:"status"`
		Version  string    `json:"version"`
		KeyCount int       `json:"keyCount"`
		Roles    []struct {
			ID   uuid.UUID `json:"id"`
			Name string    `json:"name"`
		} `json:"roles"`
		RateLimitPerMinute int        `json:"rateLimitPerMinute"`
		ContactUserID      *uuid.UUID `json:"contactUserId"`
		Description        *string    `json:"description"`
	} `json:"integration"`
	Keys                 []apiKeyBody `json:"keys"`
	EffectivePermissions []struct {
		Key      string `json:"key"`
		APINever bool   `json:"apiNever"`
	} `json:"effectivePermissions"`
}

type apiKeyBody struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Display     string    `json:"display"`
	Status      string    `json:"status"`
	ReadOnly    bool      `json:"readOnly"`
	ExpiresAt   time.Time `json:"expiresAt"`
	RolledFrom  *uuid.UUID
	Networks    []string `json:"allowedNetworks"`
	Permissions *[]struct {
		Key            string `json:"key"`
		GrantedByRoles bool   `json:"grantedByRoles"`
		InCatalog      bool   `json:"inCatalog"`
	} `json:"permissions"`
}

// createIntegration creates an integration named name holding roles as the
// Owner c, which must succeed.
func createIntegration(t testing.TB, c *client, name string, roles ...uuid.UUID) integrationDetail {
	t.Helper()
	if roles == nil {
		roles = []uuid.UUID{}
	}
	r := c.do(http.MethodPost, integrationsPath, map[string]any{"name": name, "roleIds": roles})
	if r.status != http.StatusCreated {
		t.Fatalf("create integration %s: status %d body %s", name, r.status, r.body)
	}
	var d integrationDetail
	r.json(&d)
	return d
}

// createKey makes a key for the integration with body as the Owner c, which
// must succeed, and answers it and its secret.
func createKey(t testing.TB, c *client, integration uuid.UUID, body map[string]any) (apiKeyBody, string) {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	if _, ok := body["name"]; !ok {
		body["name"] = "Production"
	}
	r := c.do(http.MethodPost, integrationsPath+"/"+integration.String()+"/keys", body)
	if r.status != http.StatusCreated {
		t.Fatalf("create key: status %d body %s", r.status, r.body)
	}
	if got := r.header("Cache-Control"); got != "no-store" {
		t.Errorf("a secret's Cache-Control = %q, want no-store", got)
	}
	var out struct {
		Key    apiKeyBody `json:"key"`
		Secret string     `json:"secret"`
	}
	r.json(&out)
	return out.Key, out.Secret
}

func getIntegration(t testing.TB, c *client, id uuid.UUID) (integrationDetail, *resp) {
	t.Helper()
	r := c.do(http.MethodGet, integrationsPath+"/"+id.String(), nil)
	var d integrationDetail
	if r.status == http.StatusOK {
		r.json(&d)
	}
	return d, r
}

// keyRequest is a request for rule-free Access.Check calls, presenting key.
func keyRequest(method, key string) *http.Request {
	r, _ := http.NewRequestWithContext(context.Background(), method, "http://identity.example.com/api/v1/x", nil)
	r.RemoteAddr = "192.0.2.10:1234"
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	return r
}

func TestIntegrations_CreateMakesAUserOfKindIntegrationWithOnlyItsRoles(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	role := h.insertRole(t, "Webshop", "customers:view")
	contact := h.createUser(t, owner, "contact@example.test", identity.RoleUser)

	r := owner.do(http.MethodPost, integrationsPath, map[string]any{
		"name": "  Webshop sync ", "description": "The web shop", "contactUserId": contact, "roleIds": []uuid.UUID{role},
	})
	if r.status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	var d integrationDetail
	r.json(&d)
	id := d.Integration.ID
	if d.Integration.Name != "Webshop sync" || d.Integration.Status != "active" || d.Integration.RateLimitPerMinute != 600 ||
		len(d.Integration.Roles) != 1 || d.Integration.Roles[0].ID != role || d.Integration.ContactUserID == nil || *d.Integration.ContactUserID != contact {
		t.Errorf("created %+v", d.Integration)
	}
	if len(d.EffectivePermissions) != 1 || d.EffectivePermissions[0].Key != "customers:view" || d.EffectivePermissions[0].APINever {
		t.Errorf("effective permissions %+v", d.EffectivePermissions)
	}

	var kind, email, display string
	if err := h.pool.QueryRow(context.Background(), `SELECT kind, email, display_name FROM identity.users WHERE id = $1`, id).Scan(&kind, &email, &display); err != nil {
		t.Fatal(err)
	}
	if kind != "integration" || email != "integration-"+id.String()+"@integrations.invalid" || display != "Webshop sync" {
		t.Errorf("user = %s %s %s", kind, email, display)
	}
	if got := h.roleNames(t, id); !slices.Equal(got, []string{"Webshop"}) {
		t.Errorf("roles %v, want Webshop alone: no built-in User", got)
	}
	events := h.auditEvents(t, "integration.created")
	if len(events) != 1 || events[0].Actor == nil || *events[0].Actor != ownerID || events[0].TargetUser == nil || *events[0].TargetUser != id ||
		!strings.Contains(events[0].After, `"Webshop"`) {
		t.Errorf("audit %+v", events)
	}

	// Listed, and the name is taken among those not archived, whatever its case.
	r = owner.do(http.MethodGet, integrationsPath, nil)
	if r.status != http.StatusOK || !strings.Contains(string(r.body), id.String()) {
		t.Errorf("list: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPost, integrationsPath, map[string]any{"name": "WEBSHOP SYNC"}); r.status != http.StatusConflict || r.code() != "integration_name_taken" {
		t.Errorf("a second Webshop sync: %d %s", r.status, r.body)
	}
	// Only custom roles; only a person as the contact; a name of 1-100.
	for name, body := range map[string]map[string]any{
		"the Owner role":     {"name": "A", "roleIds": []uuid.UUID{identity.RoleOwnerID}},
		"the User role":      {"name": "A", "roleIds": []uuid.UUID{identity.RoleUserID}},
		"an unknown role":    {"name": "A", "roleIds": []uuid.UUID{uuid.New()}},
		"another as contact": {"name": "A", "contactUserId": id},
		"no name":            {"name": " "},
		"a long name":        {"name": strings.Repeat("x", 101)},
	} {
		r := owner.do(http.MethodPost, integrationsPath, body)
		if r.status != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, r.status, r.body)
		}
	}

	// No one but an Owner reaches the screen.
	person := h.signIn(t, contact, false)
	if r := person.do(http.MethodGet, integrationsPath, nil); r.status != http.StatusForbidden {
		t.Errorf("a person's list: %d", r.status)
	}
}

func TestIntegrations_ChangeUnderIfMatchRolesDisableAndArchive(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	role := h.insertRole(t, "Webshop", "customers:view")
	other := h.insertRole(t, "Readers", "customers:create")
	d := createIntegration(t, owner, "Webshop sync", role)
	id := d.Integration.ID
	path := integrationsPath + "/" + id.String()

	body := map[string]any{"name": "Shop", "description": "Orders in", "rateLimitPerMinute": 1200}
	if r := owner.do(http.MethodPatch, path, body, header("If-Match", "not-a-version")); r.status != http.StatusBadRequest {
		t.Errorf("a bad If-Match: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPatch, path, body, header("If-Match", uuid.NewString())); r.status != http.StatusPreconditionFailed || r.code() != "version_mismatch" {
		t.Errorf("a stale If-Match: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPatch, path, map[string]any{"name": "Shop", "rateLimitPerMinute": 59}, header("If-Match", d.Integration.Version)); r.status != http.StatusBadRequest {
		t.Errorf("a rate limit below 60: %d %s", r.status, r.body)
	}
	r := owner.do(http.MethodPatch, path, body, header("If-Match", `"`+d.Integration.Version+`"`))
	if r.status != http.StatusOK {
		t.Fatalf("patch: %d %s", r.status, r.body)
	}
	r.json(&d)
	if d.Integration.Name != "Shop" || d.Integration.RateLimitPerMinute != 1200 || d.Integration.Description == nil || *d.Integration.Description != "Orders in" {
		t.Errorf("patched %+v", d.Integration)
	}
	if events := h.auditEvents(t, "integration.updated"); len(events) != 1 || !strings.Contains(events[0].Before, "Webshop sync") || !strings.Contains(events[0].After, `"Shop"`) {
		t.Errorf("audit %+v", events)
	}

	r = owner.do(http.MethodPut, path+"/roles", map[string]any{"roleIds": []uuid.UUID{other}})
	if r.status != http.StatusOK {
		t.Fatalf("roles: %d %s", r.status, r.body)
	}
	if got := h.roleNames(t, id); !slices.Equal(got, []string{"Readers"}) {
		t.Errorf("roles %v", got)
	}
	if r := owner.do(http.MethodPut, path+"/roles", map[string]any{"roleIds": []uuid.UUID{identity.RoleSystemAdminID}}); r.status != http.StatusBadRequest || r.code() != "invalid_roles" {
		t.Errorf("SystemAdmin: %d %s", r.status, r.body)
	}
	if events := h.auditEvents(t, "integration.roles_changed"); len(events) != 1 || !strings.Contains(events[0].Before, "Webshop") || !strings.Contains(events[0].After, "Readers") {
		t.Errorf("audit %+v", events)
	}

	_, secret := createKey(t, owner, id, nil)
	rule := permissionRule("customers:create")
	if _, err := h.access.Check(keyRequest(http.MethodGet, secret), rule); err != nil {
		t.Fatalf("the key: %v", err)
	}
	if r := owner.do(http.MethodPost, path+"/disable", nil); r.status != http.StatusOK || !strings.Contains(string(r.body), `"status":"disabled"`) {
		t.Errorf("disable: %d %s", r.status, r.body)
	}
	if _, err := h.access.Check(keyRequest(http.MethodGet, secret), rule); !isUnauthenticated(err) {
		t.Errorf("a disabled integration's key: %v", err)
	}
	if r := owner.do(http.MethodPost, path+"/enable", nil); r.status != http.StatusOK {
		t.Errorf("enable: %d %s", r.status, r.body)
	}
	if _, err := h.access.Check(keyRequest(http.MethodGet, secret), rule); err != nil {
		t.Errorf("an enabled integration's key: %v", err)
	}
	if len(h.auditEvents(t, "integration.disabled")) != 1 || len(h.auditEvents(t, "integration.enabled")) != 1 {
		t.Error("disable and enable are audited once each")
	}

	// Archiving revokes every key, frees the name, and is the last change.
	r = owner.do(http.MethodPost, path+"/archive", nil)
	if r.status != http.StatusOK {
		t.Fatalf("archive: %d %s", r.status, r.body)
	}
	r.json(&d)
	if d.Integration.Status != "archived" || d.Integration.KeyCount != 0 || len(d.Keys) != 1 || d.Keys[0].Status != "revoked" {
		t.Errorf("archived %+v", d)
	}
	if _, err := h.access.Check(keyRequest(http.MethodGet, secret), rule); !isUnauthenticated(err) {
		t.Errorf("an archived integration's key: %v", err)
	}
	if len(h.auditEvents(t, "integration.archived")) != 1 || len(h.auditEvents(t, "api_key.revoked")) != 1 {
		t.Error("archive audits itself and the key it revokes")
	}
	for _, call := range []struct {
		suffix string
		body   any
	}{{"/archive", nil}, {"/disable", nil}, {"/keys", map[string]any{"name": "x"}}} {
		if r := owner.do(http.MethodPost, path+call.suffix, call.body); r.status != http.StatusConflict || r.code() != "integration_archived" {
			t.Errorf("%s on an archived integration: %d %s", call.suffix, r.status, r.body)
		}
	}
	createIntegration(t, owner, "Shop")
	if _, r := getIntegration(t, owner, uuid.New()); r.status != http.StatusNotFound {
		t.Errorf("an unknown integration: %d", r.status)
	}
}

func TestIntegrations_KeysAreShownOnceRolledOnceAndRevoked(t *testing.T) {
	t.Parallel()
	h, owner, ownerID := rbacHarness(t)
	role := h.insertRole(t, "Webshop", "customers:view")
	id := createIntegration(t, owner, "Webshop sync", role).Integration.ID
	path := integrationsPath + "/" + id.String()

	key, secret := createKey(t, owner, id, map[string]any{
		"name": "Production", "expiresInDays": 30, "permissions": []string{"customers:view", "customers:create", "gone:key"},
		"readOnly": true, "allowedNetworks": []string{"192.0.2.0/24", "2001:db8::1"},
	})
	if len(secret) != 53 || !strings.HasPrefix(secret, "vtg_") || key.Display != secret[:8]+"…"+secret[49:] || !key.ReadOnly ||
		!key.ExpiresAt.Equal(h.now().AddDate(0, 0, 30)) || !slices.Equal(key.Networks, []string{"192.0.2.0/24", "2001:db8::1/128"}) {
		t.Errorf("key %+v secret %q", key, secret)
	}
	if key.Permissions == nil || len(*key.Permissions) != 3 {
		t.Fatalf("narrowing %+v", key.Permissions)
	}
	for _, p := range *key.Permissions {
		want := map[string][2]bool{"customers:view": {true, true}, "customers:create": {false, true}, "gone:key": {false, false}}[p.Key]
		if p.GrantedByRoles != want[0] || p.InCatalog != want[1] {
			t.Errorf("mark %+v", p)
		}
	}
	// Never again: not in the detail, not in the audit, which has no hash either.
	d, r := getIntegration(t, owner, id)
	if r.status != http.StatusOK || len(d.Keys) != 1 || strings.Contains(string(r.body), secret) || strings.Contains(string(r.body), secret[8:]) {
		t.Errorf("detail %d %s", r.status, r.body)
	}
	var hash []byte
	if err := h.pool.QueryRow(context.Background(), `SELECT token_hash FROM identity.api_keys WHERE id = $1`, key.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	created := h.auditEvents(t, "api_key.created")
	if len(created) != 1 || strings.Contains(created[0].After, "vtg_") || strings.Contains(created[0].After, hex.EncodeToString(hash)) ||
		!strings.Contains(created[0].After, "customers:create") {
		t.Errorf("audit %+v", created)
	}

	// The expiry and the networks are held to K12 and K14.
	for name, body := range map[string]map[string]any{
		"45 days":         {"name": "x", "expiresInDays": 45},
		"a date too far":  {"name": "x", "expiresAt": h.now().AddDate(0, 0, 366)},
		"a past date":     {"name": "x", "expiresAt": h.now().Add(-time.Hour)},
		"both":            {"name": "x", "expiresInDays": 30, "expiresAt": h.now().AddDate(0, 0, 10)},
		"21 networks":     {"name": "x", "allowedNetworks": slices.Repeat([]string{"192.0.2.1"}, 21)},
		"a bad network":   {"name": "x", "allowedNetworks": []string{"not-a-cidr"}},
		"a bad narrowing": {"name": "x", "permissions": []string{"Customers View"}},
		"no name":         {"name": ""},
	} {
		if r := owner.do(http.MethodPost, path+"/keys", body); r.status != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}

	// A roll: the same settings, a new secret, the old key working on for the
	// overlap; a second roll of the same key is refused.
	plain, plainSecret := createKey(t, owner, id, map[string]any{"name": "Plain"})
	rollPath := path + "/keys/" + plain.ID.String() + "/roll"
	r = owner.do(http.MethodPost, rollPath, map[string]any{"overlapHours": 2, "expiresInDays": 180})
	if r.status != http.StatusCreated || r.header("Cache-Control") != "no-store" {
		t.Fatalf("roll: %d %s", r.status, r.body)
	}
	var rolled struct {
		Key    apiKeyBody `json:"key"`
		Secret string     `json:"secret"`
	}
	r.json(&rolled)
	if rolled.Secret == plainSecret || rolled.Key.Name != "Plain" || !rolled.Key.ExpiresAt.Equal(h.now().AddDate(0, 0, 180)) {
		t.Errorf("rolled %+v", rolled.Key)
	}
	if r := owner.do(http.MethodPost, rollPath, nil); r.status != http.StatusConflict || r.code() != "api_key_already_rolled" {
		t.Errorf("a second roll: %d %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPost, rollPath, map[string]any{"overlapHours": 169}); r.status != http.StatusBadRequest {
		t.Errorf("an overlap past 7 days: %d %s", r.status, r.body)
	}
	if len(h.auditEvents(t, "api_key.rolled")) != 1 {
		t.Error("one roll audited")
	}
	rule := permissionRule("customers:view")
	if _, err := h.access.Check(keyRequest(http.MethodGet, plainSecret), rule); err != nil {
		t.Errorf("the old key within its overlap: %v", err)
	}
	h.advance(2*time.Hour + time.Second)
	owner = h.signIn(t, ownerID, false) // the Owner's session has gone idle meanwhile
	if _, err := h.access.Check(keyRequest(http.MethodGet, plainSecret), rule); !errorIsExpired(err) {
		t.Errorf("the old key past its overlap: %v", err)
	}
	if _, err := h.access.Check(keyRequest(http.MethodGet, rolled.Secret), rule); err != nil {
		t.Errorf("the new key: %v", err)
	}
	if r := owner.do(http.MethodPost, path+"/keys/"+plain.ID.String()+"/roll", nil); r.status != http.StatusConflict {
		t.Errorf("rolling an expired key: %d %s", r.status, r.body)
	}

	// Revoke: at once, and answered as it is the second time.
	revokePath := path + "/keys/" + rolled.Key.ID.String() + "/revoke"
	for range 2 {
		r := owner.do(http.MethodPost, revokePath, nil)
		if r.status != http.StatusOK || !strings.Contains(string(r.body), `"status":"revoked"`) {
			t.Errorf("revoke: %d %s", r.status, r.body)
		}
	}
	if _, err := h.access.Check(keyRequest(http.MethodGet, rolled.Secret), rule); !isUnauthenticated(err) || errorIsExpired(err) {
		t.Errorf("a revoked key: %v", err)
	}
	if n := len(h.auditEvents(t, "api_key.revoked")); n != 1 {
		t.Errorf("revocations audited %d times, want once", n)
	}
	if r := owner.do(http.MethodPost, path+"/keys/"+uuid.NewString()+"/revoke", nil); r.status != http.StatusNotFound {
		t.Errorf("an unknown key: %d", r.status)
	}
}

// TestIntegrations_ASecondRollLosesTheRace is K13's race: two rolls of one key
// at once, one of which is 201 and the other 409, never two new keys.
func TestIntegrations_ASecondRollLosesTheRace(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	id := createIntegration(t, owner, "Webshop sync").Integration.ID
	key, _ := createKey(t, owner, id, nil)
	rollPath := integrationsPath + "/" + id.String() + "/keys/" + key.ID.String() + "/roll"
	statuses := make(chan int, 2)
	for range 2 {
		go func() { statuses <- owner.do(http.MethodPost, rollPath, nil).status }()
	}
	got := []int{<-statuses, <-statuses}
	slices.Sort(got)
	if !slices.Equal(got, []int{http.StatusCreated, http.StatusConflict}) {
		t.Errorf("two rolls answered %v, want 201 and 409", got)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.api_keys WHERE rolled_from = $1`, key.ID); n != 1 {
		t.Errorf("%d keys rolled from it, want 1", n)
	}
}

// TestIntegrations_AStewardMayNotWidenARoleAnIntegrationHolds is K56: a
// delegate who stewards a role may edit it until an integration holds it,
// and then only an Owner may.
func TestIntegrations_AStewardMayNotWidenARoleAnIntegrationHolds(t *testing.T) {
	t.Parallel()
	h, owner, _ := rbacHarness(t)
	const email = "steward@example.test"
	stewardID := h.createUser(t, owner, email, identity.RoleUser)
	role := createRole(t, owner, "Webshop", "customers:view")
	delegate(t, h, owner, stewardID, []uuid.UUID{role.ID}, "customers:view", "customers:create")
	steward := h.login(t, email, userPassword)

	r := updateRole(steward, role.ID, role.Name, role.Version, "customers:view")
	if r.status != http.StatusOK {
		t.Fatalf("the steward's edit before: %d %s", r.status, r.body)
	}
	var edited createdRole
	r.json(&edited)

	createIntegration(t, owner, "Webshop sync", role.ID)
	wantFlat(t, "the steward widening it", updateRole(steward, role.ID, role.Name, edited.Version, "customers:view", "customers:create"),
		http.StatusForbidden, "role_edit_denied", "Only delegated stewarded custom roles within the selected delegation boundary may be changed.")
	if r := updateRole(owner, role.ID, role.Name, edited.Version, "customers:view", "customers:create"); r.status != http.StatusOK {
		t.Errorf("the Owner's edit: %d %s", r.status, r.body)
	}
}
