package entra_test

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/graphtest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// harness is one entra installation for one test: internal/modtest's shared
// harness composing this module beside identity, every exchange validated
// against entra.yaml through the package recorder. Staff is listed as
// enabled (MODULES refuses entra without it) but not composed: the module's
// own tests drive its endpoints and its provider directly. A test that wants
// the fake Graph passes modtest.WithTransport(graph) among opts.
type harness struct {
	*modtest.Harness
}

// newHarness is an entra installation.
func newHarness(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(entra.Module()),
	}
	return &harness{Harness: modtest.New(t, append(base, opts...)...)}
}

// The module's paths, so a typo is one compile error rather than a 404 a test
// then explains to itself.
const (
	connectionPath  = "/api/v1/entra/connection"
	credentialsPath = "/api/v1/entra/connection/credentials"
	testPath        = "/api/v1/entra/connection/test"
	settingsPath    = "/api/v1/entra/connection/settings"
	skusPath        = "/api/v1/entra/connection/skus"
	eventsPath      = "/api/v1/entra/events"
	accountsPath    = "/api/v1/entra/accounts"
	groupsPath      = "/api/v1/entra/groups"
)

// manager is a caller holding entra:manage, and nothing else.
func manager(t *testing.T, h *harness) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return h.SignInUser(t, "entra:manage")
}

// owner is a signed-in Owner: a user holding identity's built-in Owner role
// and no role of their own, who holds every key by the wildcard and meets
// the Owner policy the connection's writes are under.
func owner(t *testing.T, h *harness) (*modtest.Client, uuid.UUID) {
	t.Helper()
	c, id := h.SignInUser(t)
	h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) SELECT $1, id FROM identity.roles WHERE name = 'Owner'`, id)
	return c, id
}

// The fake tenant's registration: the ids the portal shows and the one
// secret the tenant accepts.
const (
	tenantID    = "6d5f3f0e-1d2c-4b3a-9f8e-7a6b5c4d3e2f"
	clientID    = "0f1e2d3c-4b5a-4968-8776-655443322110"
	secretValue = "Qx8~fake.client-secret_value-0123456789"
	skuE3       = "6fd2c87f-b296-42f0-b197-1e91e994b900"
	skuE1       = "18181a46-0d4e-45cd-891e-60aabd171b4e"
)

// everyPermission is the eight application permissions the module needs.
var everyPermission = []string{
	"User.ReadWrite.All", "User-LifeCycleInfo.ReadWrite.All", "User.RevokeSessions.All",
	"UserAuthMethod-TAP.ReadWrite.All", "LicenseAssignment.ReadWrite.All", "Group.ReadWrite.All",
	"Domain.Read.All", "Organization.Read.All",
}

// newTenantHarness is an entra installation whose transport is a fake
// tenant: the test client registered with secretValue, every permission
// consented, two verified domains and an unverified one, two licences — on
// the harness's clock, with Graph's waits zeroed.
func newTenantHarness(t *testing.T, opts ...modtest.Option) (*harness, *graphtest.FakeGraph) {
	t.Helper()
	g := graphtest.New()
	g.TenantID = tenantID
	g.Secrets[clientID] = []string{secretValue}
	g.Roles = slices.Clone(everyPermission)
	g.Organisation = "Nordlys AS"
	g.Domains = []graphtest.Domain{
		{Name: "nordlys.no", IsVerified: true, IsDefault: true},
		{Name: "Nordlys.onmicrosoft.com", IsVerified: true},
		{Name: "nordlys.example", IsVerified: false},
	}
	g.Skus = []graphtest.Sku{
		{SkuID: skuE3, PartNumber: "ENTERPRISEPACK", Enabled: 25, Consumed: 20},
		{SkuID: skuE1, PartNumber: "STANDARDPACK", Enabled: 10, Consumed: 0},
	}
	base := []modtest.Option{modtest.WithTransport(g), modtest.WithBackoff(func(int) time.Duration { return 0 })}
	h := newHarness(t, append(base, opts...)...)
	g.Configure(func(g *graphtest.FakeGraph) { g.Now = h.Now })
	return h, g
}

// ifMatch is the If-Match header carrying version.
func ifMatch(version int32) modtest.RequestOption {
	return modtest.Header("If-Match", strconv.Itoa(int(version)))
}

// getConnection is GET /connection, which must answer 200.
func getConnection(t *testing.T, c *modtest.Client) gen.EntraConnection {
	t.Helper()
	r := c.Do(http.MethodGet, connectionPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /connection: status %d body %s", r.Status, r.Body)
	}
	var conn gen.EntraConnection
	r.JSON(&conn)
	return conn
}

// register is step 1 by an Owner: the test tenant and client, which must
// answer 200.
func register(t *testing.T, c *modtest.Client) gen.EntraConnection {
	t.Helper()
	current := getConnection(t, c)
	r := c.Do(http.MethodPut, connectionPath, map[string]any{"tenantId": tenantID, "clientId": clientID}, ifMatch(current.Version))
	if r.Status != http.StatusOK {
		t.Fatalf("PUT /connection: status %d body %s", r.Status, r.Body)
	}
	var conn gen.EntraConnection
	r.JSON(&conn)
	return conn
}

// addSecret is step 2 by an Owner, which must answer 201.
func addSecret(t *testing.T, c *modtest.Client, value, label string, expires time.Time) gen.EntraCredential {
	t.Helper()
	r := c.Do(http.MethodPost, credentialsPath, map[string]any{
		"kind": "client_secret", "label": label, "secret": value, "expiresAt": expires.UTC().Format(time.RFC3339),
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("POST /connection/credentials: status %d body %s", r.Status, r.Body)
	}
	var cred gen.EntraCredential
	r.JSON(&cred)
	return cred
}

// testConnection is step 3 by an Owner, which answers 200 whatever it
// found.
func testConnection(t *testing.T, c *modtest.Client) gen.EntraConnectionTest {
	t.Helper()
	r := c.Do(http.MethodPost, testPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("POST /connection/test: status %d body %s", r.Status, r.Body)
	}
	var result gen.EntraConnectionTest
	r.JSON(&result)
	return result
}

// connected is a tenant registered, given the good secret for a year and
// tested, by the Owner it answers.
func connected(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	o, _ := owner(t, h)
	register(t, o)
	addSecret(t, o, secretValue, "Vantigo", h.Now().AddDate(1, 0, 0))
	if result := testConnection(t, o); result.Status != gen.EntraConnectionTestStatusConnected {
		t.Fatalf("the connection test: %+v, want connected", result)
	}
	return o
}

// problemOf is a refusal's EntraProblem.
func problemOf(t *testing.T, r *modtest.Response) gen.EntraProblem {
	t.Helper()
	var p gen.EntraProblem
	r.JSON(&p)
	return p
}

// codeOf is a refusal's code, "" without one.
func codeOf(t *testing.T, r *modtest.Response) string {
	t.Helper()
	if p := problemOf(t, r); p.Code != nil {
		return *p.Code
	}
	return ""
}

// provider is the module's provisioner built on the harness's Deps, as
// Compose builds it.
func provider(h *harness) contracts.IdentityProvisioner {
	return entra.Module().IdentityProvisioner(h.Deps())
}

// addAccount puts an account in entra.accounts, as a Prepare would.
func addAccount(t *testing.T, h *harness, number, upn string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.Exec(t, `INSERT INTO entra.accounts (object_id, employee_number, user_principal_name, state, created_by_us, created_at, updated_at)
	           VALUES ($1, $2, $3, 'disabled', true, $4, $4)`, id, number, upn, h.Now())
	return id
}

// ownerWithMFA is owner whose session verified a second factor, as an
// installation with OWNERS_REQUIRE_MFA asks of the Owner policy.
func ownerWithMFA(t *testing.T, h *harness) *modtest.Client {
	t.Helper()
	c, id := owner(t, h)
	h.Exec(t, `UPDATE identity.sessions SET mfa_verified_at = $2 WHERE user_id = $1`, id, h.Now())
	return c
}
