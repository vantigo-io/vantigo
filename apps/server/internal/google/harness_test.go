package google_test

import (
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/google"
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/googletest"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every google test against
// google.yaml and records which operations answered successfully;
// TestMain turns it into the coverage gate.
var recorder = contracttest.NewForModule("google")

// pendingOperations are the contract's operations no test covers yet: the
// calendar notifications, until delivery E's notifications land.
var pendingOperations = []string{"postGoogleCalendarNotifications"}

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder, pendingOperations...))
}

// The module's paths.
const (
	connectionPath  = "/api/v1/google/connection"
	credentialsPath = "/api/v1/google/connection/credentials"
	oauthClientPath = "/api/v1/google/connection/oauth-client"
	testPath        = "/api/v1/google/connection/test"
	eventsPath      = "/api/v1/google/events"
)

// The fake organisation.
const (
	adminAddress = "admin@nordlys.no"
	customerID   = "C03az79cb"
	saEmail      = "vantigo@nordlys-vantigo.iam.gserviceaccount.com"
	saClientID   = "102345678901234567890"
	saProject    = "nordlys-vantigo"
	webClientID  = "123456789012-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com"
	webSecret    = "GOCSPX-fake_client-secret_0123456789"
)

// everyScope is what the module asks the admin to delegate: delivery A's
// two scopes, then delivery B's four.
var everyScope = []string{
	googletest.ScopeCustomer, googletest.ScopeDomains,
	googletest.ScopeUsers, googletest.ScopeGroups, googletest.ScopeGroupsSettings, googletest.ScopeLicensing,
}

// The service accounts, made once: a 2048-bit key takes a while.
var (
	accountsOnce sync.Once
	saFirst      googletest.ServiceAccount
	saSecond     googletest.ServiceAccount
	saOther      googletest.ServiceAccount
)

// accounts are the test's service accounts: the organisation's, a second
// key of it, and another project's.
func accounts() (first, second, other googletest.ServiceAccount) {
	accountsOnce.Do(func() {
		saFirst = googletest.NewServiceAccount(saEmail, saClientID, saProject)
		saSecond = saFirst.Rotated()
		saOther = googletest.NewServiceAccount("other@elsewhere.iam.gserviceaccount.com", "109876543210987654321", "elsewhere")
	})
	return saFirst, saSecond, saOther
}

// harness is one google installation for one test, composed beside
// identity, with the fake Google as its transport and the waits zeroed.
type harness struct {
	*modtest.Harness
	g *googletest.FakeGoogle
}

// newHarness is a google installation over a fake organisation: one admin,
// both service account keys known to Google, nothing delegated yet.
func newHarness(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	first, second, _ := accounts()
	g := googletest.New()
	g.Admins = []string{adminAddress}
	g.CustomerID = customerID
	g.Organisation = "Nordlys AS"
	g.Domains = []googletest.Domain{
		{Name: "nordlys.no", Verified: true, IsPrimary: true},
		{Name: "Nordlys.example", Verified: true},
		{Name: "unverified.example", Verified: false},
	}
	g.AddKey(first)
	g.AddKey(second)
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(google.Module()),
		modtest.WithTransport(g),
		modtest.WithBackoff(func(int) time.Duration { return 0 }),
	}
	h := &harness{Harness: modtest.New(t, append(base, opts...)...), g: g}
	g.Configure(func(g *googletest.FakeGoogle) { g.Now = h.Now })
	return h
}

// manager is a caller holding google:manage, and nothing else.
func manager(t *testing.T, h *harness) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return h.SignInUser(t, "google:manage")
}

// owner is a signed-in Owner.
func owner(t *testing.T, h *harness) (*modtest.Client, uuid.UUID) {
	t.Helper()
	c, id := h.SignInUser(t)
	h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) SELECT $1, id FROM identity.roles WHERE name = 'Owner'`, id)
	return c, id
}

// ifMatch is the If-Match header carrying version.
func ifMatch(version int32) modtest.RequestOption {
	return modtest.Header("If-Match", strconv.Itoa(int(version)))
}

// getConnection is GET /connection, which must answer 200.
func getConnection(t *testing.T, c *modtest.Client) gen.GoogleConnection {
	t.Helper()
	r := c.Do(http.MethodGet, connectionPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /connection: status %d body %s", r.Status, r.Body)
	}
	var conn gen.GoogleConnection
	r.JSON(&conn)
	return conn
}

// setAdmin is PUT /connection naming admin, which must answer 200.
func setAdmin(t *testing.T, c *modtest.Client, admin string) gen.GoogleConnection {
	t.Helper()
	current := getConnection(t, c)
	r := c.Do(http.MethodPut, connectionPath, map[string]any{"adminSubject": admin}, ifMatch(current.Version))
	if r.Status != http.StatusOK {
		t.Fatalf("PUT /connection: status %d body %s", r.Status, r.Body)
	}
	var conn gen.GoogleConnection
	r.JSON(&conn)
	return conn
}

// uploadKey is POST /connection/credentials with a's key file, which must
// answer 201.
func uploadKey(t *testing.T, c *modtest.Client, a googletest.ServiceAccount) gen.GoogleCredential {
	t.Helper()
	r := c.Do(http.MethodPost, credentialsPath, map[string]any{"json": a.KeyFile()})
	if r.Status != http.StatusCreated {
		t.Fatalf("POST /connection/credentials: status %d body %s", r.Status, r.Body)
	}
	var cred gen.GoogleCredential
	r.JSON(&cred)
	return cred
}

// testConnection is POST /connection/test, which answers 200 whatever it
// found.
func testConnection(t *testing.T, c *modtest.Client) gen.GoogleConnectionTest {
	t.Helper()
	r := c.Do(http.MethodPost, testPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("POST /connection/test: status %d body %s", r.Status, r.Body)
	}
	var result gen.GoogleConnectionTest
	r.JSON(&result)
	return result
}

// configured is an Owner who uploaded the first key and named the admin.
func configured(t *testing.T, h *harness) (*modtest.Client, uuid.UUID) {
	t.Helper()
	first, _, _ := accounts()
	o, id := owner(t, h)
	uploadKey(t, o, first)
	setAdmin(t, o, adminAddress)
	return o, id
}

// problemOf is a refusal's GoogleProblem.
func problemOf(t *testing.T, r *modtest.Response) gen.GoogleProblem {
	t.Helper()
	var p gen.GoogleProblem
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
