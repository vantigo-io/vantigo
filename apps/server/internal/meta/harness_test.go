package meta_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/meta"
	"github.com/vantigo-io/vantigo/server/internal/meta/gen"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
)

// recorder validates every exchange of every meta test against meta.yaml and
// records which operations answered successfully; TestMain turns it into the
// coverage gate.
var recorder = contracttest.NewForModule("meta")

// pendingOperations are the contract's operations no test covers yet: none.
var pendingOperations []string

func TestMain(m *testing.M) {
	os.Exit(contracttest.RequireCoverage(m, recorder, pendingOperations...))
}

// The module's paths.
const (
	settingsPath    = "/api/v1/meta/settings"
	verifyTokenPath = "/api/v1/meta/settings/verify-token"
	pagesPath       = "/api/v1/meta/pages"
	webhookPath     = "/api/v1/meta/webhook"
)

// The fake app and the instance's public host.
const (
	publicBase = "https://vantigo.example.test"
	appID      = "1234567890123456"
	appSecret  = "0123456789abcdef0123456789abcdef"
)

// harness is one meta installation for one test, composed beside identity, with
// PUBLIC_BASE_URL set (a test that wants none overrides it with an empty
// value) and the waits zeroed. Pass modtest.WithTransport for a fake Graph API.
type harness struct {
	*modtest.Harness
}

// newHarness is a meta installation with nothing configured.
func newHarness(t *testing.T, opts ...modtest.Option) *harness {
	t.Helper()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(meta.Module()),
		modtest.WithEnv("PUBLIC_BASE_URL", publicBase),
		modtest.WithBackoff(func(int) time.Duration { return 0 }),
	}
	return &harness{Harness: modtest.New(t, append(base, opts...)...)}
}

// owner is a signed-in Owner.
func owner(t *testing.T, h *harness) (*modtest.Client, uuid.UUID) {
	t.Helper()
	c, id := h.SignInUser(t)
	h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) SELECT $1, id FROM identity.roles WHERE name = 'Owner'`, id)
	return c, id
}

// getSettings is GET /settings, which must answer 200.
func getSettings(t *testing.T, c *modtest.Client) gen.MetaSettings {
	t.Helper()
	r := c.Do(http.MethodGet, settingsPath, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /settings: status %d body %s", r.Status, r.Body)
	}
	var s gen.MetaSettings
	r.JSON(&s)
	return s
}

// configure is PUT /settings with the fake app's id and secret, which must
// answer 200.
func configure(t *testing.T, c *modtest.Client) gen.MetaSettings {
	t.Helper()
	r := c.Do(http.MethodPut, settingsPath, map[string]any{"appId": appID, "appSecret": appSecret})
	if r.Status != http.StatusOK {
		t.Fatalf("PUT /settings: status %d body %s", r.Status, r.Body)
	}
	var s gen.MetaSettings
	r.JSON(&s)
	return s
}

// signature is the X-Hub-Signature-256 header value Meta sends for body under
// secret: "sha256=" and the hex HMAC-SHA256 of the bytes exactly as sent.
func signature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// problemCode is a refusal's code, "" without one.
func problemCode(t *testing.T, r *modtest.Response) string {
	t.Helper()
	var p gen.MetaProblem
	r.JSON(&p)
	if p.Code == nil {
		return ""
	}
	return *p.Code
}
