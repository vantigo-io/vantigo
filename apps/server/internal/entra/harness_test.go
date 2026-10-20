package entra_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/entra"
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
