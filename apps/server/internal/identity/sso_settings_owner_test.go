package identity_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/identity"
)

// The Owner's single sign-on operations' paths.
const (
	ssoPath             = "/api/v1/identity/owner/sso"
	ssoOIDCPath         = ssoPath + "/oidc"
	ssoScimPath         = ssoPath + "/scim"
	ssoScimTokenPath    = ssoScimPath + "/token"
	ssoScimPreviousPath = ssoScimPath + "/previous"
	ssoChangedAction    = "sso-settings.changed"
)

// ssoBody decodes SsoSettings.
type ssoBody struct {
	Version                 int32      `json:"version"`
	ImportedFromEnvironment bool       `json:"importedFromEnvironment"`
	LastOidcSignInAt        *time.Time `json:"lastOidcSignInAt"`
	LastScimRequestAt       *time.Time `json:"lastScimRequestAt"`
	Oidc                    struct {
		Enabled           bool       `json:"enabled"`
		Provider          *string    `json:"provider"`
		Authority         *string    `json:"authority"`
		ClientID          *string    `json:"clientId"`
		ClientSecretSetAt *time.Time `json:"clientSecretSetAt"`
		WorkloadTokenFile *string    `json:"workloadTokenFile"`
		AllowedDomains    []string   `json:"allowedDomains"`
		DisplayName       *string    `json:"displayName"`
		CallbackURL       string     `json:"callbackUrl"`
	} `json:"oidc"`
	Scim struct {
		Enabled           bool       `json:"enabled"`
		TokenSetAt        *time.Time `json:"tokenSetAt"`
		PreviousExpiresAt *time.Time `json:"previousExpiresAt"`
		EndpointURL       string     `json:"endpointUrl"`
	} `json:"scim"`
}

// scimTokenBody decodes SsoScimToken.
type scimTokenBody struct {
	Token             string     `json:"token"`
	EndpointURL       string     `json:"endpointUrl"`
	PreviousExpiresAt *time.Time `json:"previousExpiresAt"`
	Settings          ssoBody    `json:"settings"`
}

// getSSO is GET /owner/sso as c, which must answer 200.
func getSSO(t testing.TB, c *client) ssoBody {
	t.Helper()
	r := c.do(http.MethodGet, ssoPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s", ssoPath, r.status, r.body)
	}
	var b ssoBody
	r.json(&b)
	return b
}

// putOIDC is PUT /owner/sso/oidc as c under If-Match version.
func putOIDC(c *client, version int32, body map[string]any) *resp {
	return c.do(http.MethodPut, ssoOIDCPath, body, header("If-Match", strconv.Itoa(int(version))))
}

// mustPutOIDC is putOIDC, which must answer 200 with the version moved on.
func mustPutOIDC(t testing.TB, c *client, version int32, body map[string]any) ssoBody {
	t.Helper()
	r := putOIDC(c, version, body)
	if r.status != http.StatusOK {
		t.Fatalf("PUT %s %v: status %d body %s", ssoOIDCPath, body, r.status, r.body)
	}
	var b ssoBody
	r.json(&b)
	if b.Version != version+1 {
		t.Errorf("PUT answered version %d, want %d", b.Version, version+1)
	}
	return b
}

// issueScimToken is POST /owner/sso/scim/token as c with body, which must
// answer 200.
func issueScimToken(t testing.TB, c *client, body any) scimTokenBody {
	t.Helper()
	r := c.do(http.MethodPost, ssoScimTokenPath, body)
	if r.status != http.StatusOK {
		t.Fatalf("POST %s: status %d body %s", ssoScimTokenPath, r.status, r.body)
	}
	if r.header("Cache-Control") != "no-store" || r.header("Pragma") != "no-cache" {
		t.Errorf("POST %s: Cache-Control %q Pragma %q, want no-store and no-cache", ssoScimTokenPath, r.header("Cache-Control"), r.header("Pragma"))
	}
	var b scimTokenBody
	r.json(&b)
	return b
}

// ssoRequest is a PUT body for provider over the fake's values, with extra
// set over them.
func ssoRequest(provider string, extra map[string]any) map[string]any {
	body := map[string]any{"provider": "entra", "authority": fakeEntraAuthority, "clientId": fakeEntraClientID}
	if provider == "google" {
		body = map[string]any{"provider": "google", "authority": fakeGoogleAuthority, "clientId": fakeGoogleClientID,
			"allowedDomains": []string{"example.com"}}
	}
	for k, v := range extra {
		if v == nil {
			delete(body, k)
			continue
		}
		body[k] = v
	}
	return body
}

// sealedClientSecret is the row's sealed client secret, nil when none.
func (h *harness) sealedClientSecret(t testing.TB) []byte {
	t.Helper()
	var sealed []byte
	if err := h.pool.QueryRow(context.Background(), `SELECT oidc_client_secret_sealed FROM identity.sso_settings WHERE id = 1`).Scan(&sealed); err != nil {
		t.Fatalf("harness: the sealed client secret: %v", err)
	}
	return sealed
}

// tokenFile is a readable workload identity token file, by absolute path.
func tokenFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "azure-identity-token")
	if err := os.WriteFile(path, []byte("eyJ.fake.assertion"), 0o600); err != nil {
		t.Fatalf("write the token file: %v", err)
	}
	return path
}

// digestForms are the ways a token's SHA-256 could leak into text: hex,
// and base64 with and without padding, standard and URL-safe.
func digestForms(token string) []string {
	sum := sha256.Sum256([]byte(token))
	return []string{
		hex.EncodeToString(sum[:]),
		strings.ToUpper(hex.EncodeToString(sum[:])),
		base64.StdEncoding.EncodeToString(sum[:]),
		base64.RawStdEncoding.EncodeToString(sum[:]),
		base64.URLEncoding.EncodeToString(sum[:]),
		base64.RawURLEncoding.EncodeToString(sum[:]),
	}
}

// A fresh installation answers the settings never written — version 0,
// both off — with the callback and the SCIM endpoint to register; one that
// imported its environment answers what it imported with the client secret
// and the SCIM token withheld.
func TestSsoSettings_GetAnswersTheRowWithTheSecretsWithheld(t *testing.T) {
	t.Parallel()
	fresh := newHarness(t)
	owner, _ := fresh.bootstrapOwner(t)
	got := getSSO(t, owner)
	if got.Version != 0 || got.Oidc.Enabled || got.Scim.Enabled || got.ImportedFromEnvironment {
		t.Errorf("a fresh installation = %+v, want version 0 with both off", got)
	}
	if got.Oidc.CallbackURL != harnessOrigin+oidcCallbackPath || got.Scim.EndpointURL != harnessOrigin+scimPath {
		t.Errorf("callback %q endpoint %q", got.Oidc.CallbackURL, got.Scim.EndpointURL)
	}
	if got.Oidc.AllowedDomains == nil || len(got.Oidc.AllowedDomains) != 0 || got.Oidc.Provider != nil {
		t.Errorf("an OIDC block that is off = %+v", got.Oidc)
	}

	f := newFakeOIDC(t)
	h := newHarness(t, append(f.options(), withEnv("SCIM_TOKEN", scimToken))...)
	owner, _ = h.bootstrapOwner(t)
	r := owner.do(http.MethodGet, ssoPath, nil)
	if r.status != http.StatusOK {
		t.Fatalf("GET: status %d body %s", r.status, r.body)
	}
	r.json(&got)
	if got.Version != 1 || !got.ImportedFromEnvironment || !got.Oidc.Enabled || !got.Scim.Enabled {
		t.Errorf("the imported settings = %s", r.body)
	}
	if deref(got.Oidc.Provider) != "entra" || deref(got.Oidc.Authority) != fakeEntraAuthority || deref(got.Oidc.ClientID) != fakeEntraClientID ||
		got.Oidc.ClientSecretSetAt == nil || got.Oidc.WorkloadTokenFile != nil || deref(got.Oidc.DisplayName) != "Workforce SSO" {
		t.Errorf("the imported OIDC block = %s", r.body)
	}
	if got.Scim.TokenSetAt == nil || got.Scim.PreviousExpiresAt != nil {
		t.Errorf("the imported SCIM block = %s", r.body)
	}
	for _, secret := range append([]string{fakeClientSecret, scimToken, base64.StdEncoding.EncodeToString(h.sealedClientSecret(t))}, digestForms(scimToken)...) {
		if strings.Contains(string(r.body), secret) {
			t.Errorf("GET carries %q: %s", secret, r.body)
		}
	}
}

// Under a base path, the callback and the SCIM endpoint carry it.
func TestSsoSettings_TheURLsCarryTheBasePath(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("APP_BASE_PATH", "/vantigo"))
	owner := h.signIn(t, h.insertUser(t, ownerEmail, identity.RoleOwnerID), true)
	r := owner.do(http.MethodGet, "/vantigo"+ssoPath, nil, skipContract("the contract's paths carry no base path"))
	if r.status != http.StatusOK {
		t.Fatalf("GET: status %d body %s", r.status, r.body)
	}
	var got ssoBody
	r.json(&got)
	if got.Oidc.CallbackURL != harnessOrigin+"/vantigo"+oidcCallbackPath || got.Scim.EndpointURL != harnessOrigin+"/vantigo"+scimPath {
		t.Errorf("callback %q endpoint %q, want both under /vantigo", got.Oidc.CallbackURL, got.Scim.EndpointURL)
	}
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// Every rule config held the OIDC_* variables to is the PUT's, each
// refusal a 400 with a field error under the request's own field name, and
// nothing a refusal received is written or audited.
func TestSsoSettings_PutHoldsEveryRule(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	file := tokenFile(t)
	unreadable := filepath.Join(t.TempDir(), "unreadable")
	if err := os.WriteFile(unreadable, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	oversized := filepath.Join(t.TempDir(), "oversized")
	if err := os.WriteFile(oversized, []byte(strings.Repeat("a", 16*1024+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := map[string]any{"clientSecret": "a-client-secret"}
	with := func(base map[string]any, extra map[string]any) map[string]any {
		for k, v := range base {
			if _, ok := extra[k]; !ok {
				extra[k] = v
			}
		}
		return extra
	}

	for _, tc := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"an unknown provider", with(secret, map[string]any{"provider": "okta", "authority": fakeEntraAuthority, "clientId": fakeEntraClientID}), "provider"},
		{"no provider", with(secret, map[string]any{"provider": "", "authority": fakeEntraAuthority, "clientId": fakeEntraClientID}), "provider"},
		{"entra, a common authority", ssoRequest("entra", with(secret, map[string]any{"authority": "https://login.microsoftonline.com/common/v2.0"})), "authority"},
		{"entra, a trailing slash", ssoRequest("entra", with(secret, map[string]any{"authority": fakeEntraAuthority + "/"})), "authority"},
		{"entra, the v1 endpoint", ssoRequest("entra", with(secret, map[string]any{"authority": "https://login.microsoftonline.com/" + fakeTenantID})), "authority"},
		{"entra, over http", ssoRequest("entra", with(secret, map[string]any{"authority": "http://login.microsoftonline.com/" + fakeTenantID + "/v2.0"})), "authority"},
		{"entra, Google's authority", ssoRequest("entra", with(secret, map[string]any{"authority": fakeGoogleAuthority})), "authority"},
		{"google, a trailing slash", ssoRequest("google", with(secret, map[string]any{"authority": fakeGoogleAuthority + "/"})), "authority"},
		{"google, Entra's authority", ssoRequest("google", with(secret, map[string]any{"authority": fakeEntraAuthority})), "authority"},
		{"entra, a client id that is no GUID", ssoRequest("entra", with(secret, map[string]any{"clientId": "vantigo"})), "clientId"},
		{"entra, a Google client id", ssoRequest("entra", with(secret, map[string]any{"clientId": fakeGoogleClientID})), "clientId"},
		{"google, a GUID client id", ssoRequest("google", with(secret, map[string]any{"clientId": fakeEntraClientID})), "clientId"},
		{"entra with allowed domains", ssoRequest("entra", with(secret, map[string]any{"allowedDomains": []string{"example.com"}})), "allowedDomains"},
		{"google without allowed domains", ssoRequest("google", with(secret, map[string]any{"allowedDomains": nil})), "allowedDomains"},
		{"google with blank domains only", ssoRequest("google", with(secret, map[string]any{"allowedDomains": []string{" ", ""}})), "allowedDomains"},
		{"a URL for a domain", ssoRequest("google", with(secret, map[string]any{"allowedDomains": []string{"https://example.com"}})), "allowedDomains"},
		{"an address for a domain", ssoRequest("google", with(secret, map[string]any{"allowedDomains": []string{"person@example.com"}})), "allowedDomains"},
		{"a port on a domain", ssoRequest("google", with(secret, map[string]any{"allowedDomains": []string{"example.com:443"}})), "allowedDomains"},
		{"a single label", ssoRequest("google", with(secret, map[string]any{"allowedDomains": []string{"localhost"}})), "allowedDomains"},
		{"a domain of dots", ssoRequest("google", with(secret, map[string]any{"allowedDomains": []string{"example.com", "..."}})), "allowedDomains"},
		{"a secret and a token file", ssoRequest("entra", map[string]any{"clientSecret": "s", "workloadTokenFile": file}), "clientSecret"},
		{"no credential at all", ssoRequest("entra", nil), "clientSecret"},
		{"empty credentials", ssoRequest("entra", map[string]any{"clientSecret": "", "workloadTokenFile": ""}), "clientSecret"},
		{"google with a token file", ssoRequest("google", map[string]any{"workloadTokenFile": file}), "workloadTokenFile"},
		{"a relative token file", ssoRequest("entra", map[string]any{"workloadTokenFile": "azure-identity-token"}), "workloadTokenFile"},
		{"a missing token file", ssoRequest("entra", map[string]any{"workloadTokenFile": filepath.Join(t.TempDir(), "missing")}), "workloadTokenFile"},
		{"a directory for a token file", ssoRequest("entra", map[string]any{"workloadTokenFile": t.TempDir()}), "workloadTokenFile"},
		{"a FIFO for a token file", ssoRequest("entra", map[string]any{"workloadTokenFile": fifo}), "workloadTokenFile"},
		{"a device for a token file", ssoRequest("entra", map[string]any{"workloadTokenFile": "/dev/zero"}), "workloadTokenFile"},
		{"a token file of 16 KiB and a byte", ssoRequest("entra", map[string]any{"workloadTokenFile": oversized}), "workloadTokenFile"},
		{"a display name of 101 characters", ssoRequest("entra", with(secret, map[string]any{"displayName": strings.Repeat("å", 101)})), "displayName"},
	} {
		// A client of its own per case, each with its own address, so the
		// table stays under the operations' rate limit.
		r := putOIDC(h.signIn(t, ownerID, false), 0, tc.body)
		if r.status != http.StatusBadRequest || r.code() != "invalid_request" {
			t.Errorf("%s: status %d code %q body %s, want 400 invalid_request", tc.name, r.status, r.code(), r.body)
			continue
		}
		if msgs := fieldErrors(t, r)[tc.field]; len(msgs) == 0 {
			t.Errorf("%s: fields %v, want one on %s", tc.name, fieldErrors(t, r), tc.field)
		}
	}
	if os.Geteuid() != 0 { // root reads a file whatever its mode
		r := putOIDC(owner, 0, ssoRequest("entra", map[string]any{"workloadTokenFile": unreadable}))
		if r.status != http.StatusBadRequest || len(fieldErrors(t, r)["workloadTokenFile"]) == 0 {
			t.Errorf("an unreadable token file: status %d body %s, want 400 on workloadTokenFile", r.status, r.body)
		}
	}
	if got := getSSO(t, owner); got.Version != 0 || got.Oidc.Enabled {
		t.Errorf("after the refusals = %+v, want the settings untouched", got)
	}
	if events := h.auditEvents(t, ssoChangedAction); len(events) != 0 {
		t.Errorf("%d audit events after the refusals, want none", len(events))
	}

	// What passes: Google's domains normalised — trimmed, lower-cased,
	// trailing dots stripped, deduplicated and in ordinal order — and an
	// empty display name defaulted; a display name of 100 characters;
	// Entra on a workload identity token file.
	got := mustPutOIDC(t, owner, 0, ssoRequest("google", with(secret, map[string]any{
		"allowedDomains": []string{" Example.COM. ", "example.com", "", "b.example.org"}, "displayName": "  ",
	})))
	if !slices.Equal(got.Oidc.AllowedDomains, []string{"b.example.org", "example.com"}) || deref(got.Oidc.DisplayName) != "Workforce SSO" {
		t.Errorf("google = allowed %v display %q", got.Oidc.AllowedDomains, deref(got.Oidc.DisplayName))
	}
	name := strings.Repeat("å", 100)
	got = mustPutOIDC(t, owner, got.Version, ssoRequest("google", with(secret, map[string]any{"displayName": name})))
	if deref(got.Oidc.DisplayName) != name {
		t.Errorf("a display name of 100 characters = %q", deref(got.Oidc.DisplayName))
	}
	got = mustPutOIDC(t, owner, got.Version, ssoRequest("entra", map[string]any{"workloadTokenFile": file}))
	if deref(got.Oidc.WorkloadTokenFile) != file || got.Oidc.ClientSecretSetAt != nil || len(got.Oidc.AllowedDomains) != 0 {
		t.Errorf("entra on a token file = %+v", got.Oidc)
	}
	if h.sealedClientSecret(t) != nil {
		t.Error("a token file left the sealed secret in the row")
	}
}

// The credential rules: an absent secret keeps the sealed one for the same
// application, which the provider then still accepts at sign-in; a new
// client id or provider needs its own credential; a token file clears the
// secret and a secret clears the token file.
func TestSsoSettings_TheCredentialRules(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	owner, _ := h.bootstrapOwner(t)
	before := getSSO(t, owner)
	sealed := h.sealedClientSecret(t)

	h.advance(time.Minute)
	got := mustPutOIDC(t, owner, before.Version, ssoRequest("entra", map[string]any{"displayName": "Company SSO"}))
	if got.ImportedFromEnvironment || deref(got.Oidc.DisplayName) != "Company SSO" ||
		got.Oidc.ClientSecretSetAt == nil || !got.Oidc.ClientSecretSetAt.Equal(*before.Oidc.ClientSecretSetAt) {
		t.Errorf("a PUT without the secret = %+v, want the secret kept as it was set", got)
	}
	if !slices.Equal(h.sealedClientSecret(t), sealed) {
		t.Error("a PUT without the secret changed the sealed bytes")
	}
	// The relying party is rebuilt for the new version, with the kept
	// secret, which the fake's token endpoint checks.
	assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), "/")

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"a new client id", ssoRequest("entra", map[string]any{"clientId": "44444444-4444-4444-4444-444444444444"})},
		{"a new provider", ssoRequest("google", nil)},
	} {
		r := putOIDC(owner, got.Version, tc.body)
		if r.status != http.StatusBadRequest || len(fieldErrors(t, r)["clientSecret"]) == 0 {
			t.Errorf("%s without a credential: status %d body %s, want 400 on clientSecret", tc.name, r.status, r.body)
		}
	}

	file := tokenFile(t)
	got = mustPutOIDC(t, owner, got.Version, ssoRequest("entra", map[string]any{"workloadTokenFile": file}))
	if got.Oidc.ClientSecretSetAt != nil || deref(got.Oidc.WorkloadTokenFile) != file || h.sealedClientSecret(t) != nil {
		t.Errorf("a token file = %+v, want the secret cleared", got.Oidc)
	}
	// With the token file and no secret, an absent credential is no
	// credential: the token file is a field the screen sends back.
	if r := putOIDC(owner, got.Version, ssoRequest("entra", nil)); r.status != http.StatusBadRequest || len(fieldErrors(t, r)["clientSecret"]) == 0 {
		t.Errorf("no credential after a token file: status %d body %s", r.status, r.body)
	}

	h.advance(time.Minute)
	got = mustPutOIDC(t, owner, got.Version, ssoRequest("entra", map[string]any{"clientSecret": fakeClientSecret}))
	if got.Oidc.WorkloadTokenFile != nil || got.Oidc.ClientSecretSetAt == nil || !got.Oidc.ClientSecretSetAt.Equal(h.now()) {
		t.Errorf("a secret = %+v, want the token file cleared and the secret set now", got.Oidc)
	}
	if slices.Equal(h.sealedClientSecret(t), sealed) {
		t.Error("a new secret was not sealed afresh")
	}
	assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), "/")
}

// If-Match carries the version the settings were read at, bare or quoted;
// a version that has moved on is 412 and one that is not a number is 400.
func TestSsoSettings_PutIsUnderIfMatch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, _ := h.bootstrapOwner(t)
	body := ssoRequest("entra", map[string]any{"clientSecret": "s"})

	r := owner.do(http.MethodPut, ssoOIDCPath, body, header("If-Match", `"0"`))
	if r.status != http.StatusOK {
		t.Fatalf("a quoted If-Match: status %d body %s", r.status, r.body)
	}
	if r := owner.do(http.MethodPut, ssoOIDCPath, body, header("If-Match", `W/"1"`)); r.status != http.StatusOK {
		t.Fatalf("a weak If-Match: status %d body %s", r.status, r.body)
	}
	r = putOIDC(owner, 1, body)
	if r.status != http.StatusPreconditionFailed || r.code() != "version_mismatch" {
		t.Errorf("a stale If-Match: status %d code %q, want 412 version_mismatch", r.status, r.code())
	}
	for _, v := range []string{"latest", "-1", `"x"`, "1.5"} {
		r := owner.do(http.MethodPut, ssoOIDCPath, body, header("If-Match", v))
		if r.status != http.StatusBadRequest || len(fieldErrors(t, r)["If-Match"]) == 0 {
			t.Errorf("If-Match %q: status %d body %s, want 400 on If-Match", v, r.status, r.body)
		}
	}
	if got := getSSO(t, owner); got.Version != 2 {
		t.Errorf("version = %d, want 2", got.Version)
	}
}

// The links are keyed on the issuer, so an authority that moves it while
// people are linked under the old one, and turning OIDC off, need the
// Owner's confirmation.
func TestSsoSettings_LinkedPeopleNeedAConfirmation(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	owner, _ := h.bootstrapOwner(t)
	got := getSSO(t, owner)

	// Without links, the authority changes freely.
	other := "https://login.microsoftonline.com/" + fakeOtherTenantID + "/v2.0"
	got = mustPutOIDC(t, owner, got.Version, ssoRequest("entra", map[string]any{"authority": other, "clientSecret": fakeClientSecret}))
	got = mustPutOIDC(t, owner, got.Version, ssoRequest("entra", map[string]any{"clientSecret": fakeClientSecret}))
	assertOIDCRedirect(t, h.oidcSignIn(t, h.client(t), f), "/")

	change := ssoRequest("entra", map[string]any{"authority": other, "clientSecret": fakeClientSecret})
	r := putOIDC(owner, got.Version, change)
	if r.status != http.StatusConflict || r.code() != "oidc_links_exist" {
		t.Fatalf("an authority change with a link: status %d code %q body %s, want 409 oidc_links_exist", r.status, r.code(), r.body)
	}
	change["confirmIssuerChange"] = false
	if r := putOIDC(owner, got.Version, change); r.status != http.StatusConflict {
		t.Errorf("confirmIssuerChange false: status %d, want 409", r.status)
	}
	// A change that keeps the issuer needs no confirmation.
	same := ssoRequest("entra", map[string]any{"displayName": "Renamed"})
	got = mustPutOIDC(t, owner, got.Version, same)
	change["confirmIssuerChange"] = true
	got = mustPutOIDC(t, owner, got.Version, change)
	if deref(got.Oidc.Authority) != other {
		t.Errorf("authority = %q, want %q", deref(got.Oidc.Authority), other)
	}
	// Back under the linked issuer, turning OIDC off needs confirm.
	got = mustPutOIDC(t, owner, got.Version, ssoRequest("entra", map[string]any{"clientSecret": fakeClientSecret}))

	for _, path := range []string{ssoOIDCPath, ssoOIDCPath + "?confirm=false"} {
		if r := owner.do(http.MethodDelete, path, nil); r.status != http.StatusConflict || r.code() != "oidc_links_exist" {
			t.Errorf("DELETE %s: status %d code %q, want 409 oidc_links_exist", path, r.status, r.code())
		}
	}
	if r := owner.do(http.MethodDelete, ssoOIDCPath+"?confirm=maybe", nil); r.status != http.StatusBadRequest {
		t.Errorf("confirm=maybe: status %d, want 400", r.status)
	}
	r = owner.do(http.MethodDelete, ssoOIDCPath+"?confirm=true", nil)
	if r.status != http.StatusOK {
		t.Fatalf("DELETE confirmed: status %d body %s", r.status, r.body)
	}
	var off ssoBody
	r.json(&off)
	if off.Version != got.Version+1 || off.Oidc.Enabled || off.Oidc.Provider != nil || off.Oidc.ClientSecretSetAt != nil || h.sealedClientSecret(t) != nil {
		t.Errorf("after DELETE = %s, want the block and the secret cleared under a new version", r.body)
	}
	// Off already, a DELETE writes nothing.
	r = owner.do(http.MethodDelete, ssoOIDCPath, nil)
	var again ssoBody
	r.json(&again)
	if r.status != http.StatusOK || again.Version != off.Version {
		t.Errorf("a second DELETE: status %d version %d, want 200 at %d", r.status, again.Version, off.Version)
	}
	if r := h.client(t).do(http.MethodGet, oidcChallengePath, nil); strings.HasPrefix(r.header("Location"), "https://login.microsoftonline.com/") {
		t.Errorf("the challenge still redirects to the provider after DELETE: %s", r.header("Location"))
	}
}

// The SCIM token is generated, answered once, and verifies a SCIM request
// by its digest; a second issue keeps the first accepted for 24 hours by
// default, until the overlap is ended or the clock passes it; DELETE turns
// SCIM off at once. No token is in a GET, an audit row or the log.
func TestSsoSettings_TheScimTokenAndItsRotation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner, ownerID := h.bootstrapOwner(t)
	admitted := func(token string) bool {
		t.Helper()
		r := h.scim(t, token).do(http.MethodGet, "/ServiceProviderConfig", nil)
		return r.status == http.StatusOK
	}

	first := issueScimToken(t, owner, nil)
	if len(first.Token) != 43 || first.EndpointURL != harnessOrigin+scimPath || first.PreviousExpiresAt != nil {
		t.Errorf("the first token = %+v", first)
	}
	if s := first.Settings; s.Version != 1 || !s.Scim.Enabled || s.Scim.TokenSetAt == nil || !s.Scim.TokenSetAt.Equal(h.now()) {
		t.Errorf("the first token's settings = %+v", s)
	}
	if _, err := base64.RawURLEncoding.DecodeString(first.Token); err != nil {
		t.Errorf("the token %q is not base64url: %v", first.Token, err)
	}
	if !admitted(first.Token) {
		t.Fatal("the issued token is refused")
	}
	if admitted(first.Token + "x") {
		t.Error("a longer token is admitted")
	}

	// A second issue: the first is the previous token for 24 hours.
	second := issueScimToken(t, owner, nil)
	if second.Token == first.Token || second.PreviousExpiresAt == nil || !second.PreviousExpiresAt.Equal(h.now().Add(24*time.Hour)) ||
		second.Settings.Version != 2 || second.Settings.Scim.PreviousExpiresAt == nil {
		t.Errorf("the second token = %+v", second)
	}
	if !admitted(first.Token) || !admitted(second.Token) {
		t.Error("both tokens should be admitted during the overlap")
	}
	h.advance(24*time.Hour - time.Second)
	if !admitted(first.Token) {
		t.Error("the previous token is refused before its expiry")
	}
	h.advance(time.Second)
	if admitted(first.Token) || !admitted(second.Token) {
		t.Error("at its expiry the previous token should be refused and the current one admitted")
	}
	owner = h.signIn(t, ownerID, false) // the first session has idled out over the day
	if got := getSSO(t, owner); got.Scim.PreviousExpiresAt != nil {
		t.Errorf("an expired overlap is answered: %v", got.Scim.PreviousExpiresAt)
	}

	// previousExpiresAt is in the future and at most 24 hours ahead.
	for _, at := range []time.Time{h.now(), h.now().Add(-time.Minute), h.now().Add(24*time.Hour + time.Second)} {
		r := owner.do(http.MethodPost, ssoScimTokenPath, map[string]any{"previousExpiresAt": at})
		if r.status != http.StatusBadRequest || len(fieldErrors(t, r)["previousExpiresAt"]) == 0 {
			t.Errorf("previousExpiresAt %v: status %d body %s, want 400 on previousExpiresAt", at, r.status, r.body)
		}
	}
	if !admitted(second.Token) {
		t.Error("a refused issue changed the token")
	}
	third := issueScimToken(t, owner, map[string]any{"previousExpiresAt": h.now().Add(time.Hour)})
	if third.PreviousExpiresAt == nil || !third.PreviousExpiresAt.Equal(h.now().Add(time.Hour)) || third.Settings.Version != 3 {
		t.Errorf("the third token = %+v", third)
	}
	if !admitted(second.Token) || !admitted(third.Token) || admitted(first.Token) {
		t.Error("the third issue: want the second and third admitted and the first refused")
	}

	// Ending the overlap refuses the previous token at once.
	r := owner.do(http.MethodDelete, ssoScimPreviousPath, nil)
	var ended ssoBody
	r.json(&ended)
	if r.status != http.StatusOK || ended.Version != 4 || ended.Scim.PreviousExpiresAt != nil || !ended.Scim.Enabled {
		t.Errorf("DELETE previous: status %d body %s", r.status, r.body)
	}
	if admitted(second.Token) || !admitted(third.Token) {
		t.Error("after ending the overlap: want the previous refused and the current admitted")
	}
	r = owner.do(http.MethodDelete, ssoScimPreviousPath, nil)
	r.json(&ended)
	if r.status != http.StatusOK || ended.Version != 4 {
		t.Errorf("a second DELETE previous: status %d version %d, want 200 at 4", r.status, ended.Version)
	}

	// Turning SCIM off refuses every token at once.
	r = owner.do(http.MethodDelete, ssoScimPath, nil)
	var off ssoBody
	r.json(&off)
	if r.status != http.StatusOK || off.Version != 5 || off.Scim.Enabled || off.Scim.TokenSetAt != nil {
		t.Errorf("DELETE scim: status %d body %s", r.status, r.body)
	}
	if admitted(third.Token) {
		t.Error("a token is admitted with SCIM off")
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sso_settings WHERE scim_token_sha256 IS NOT NULL OR scim_previous_token_sha256 IS NOT NULL OR scim_previous_expires_at IS NOT NULL`); n != 0 {
		t.Error("DELETE scim left a digest in the row")
	}

	// No token, and no digest of one, in a GET, an audit row or the log.
	got := owner.do(http.MethodGet, ssoPath, nil)
	events := h.auditEvents(t, ssoChangedAction)
	if len(events) != 5 {
		t.Errorf("%d audit events, want one per write (5)", len(events))
	}
	var leaks []string
	for _, token := range []string{first.Token, second.Token, third.Token} {
		leaks = append(append(leaks, token), digestForms(token)...)
	}
	logged := string(h.log.Bytes())
	for _, leak := range leaks {
		if strings.Contains(string(got.body), leak) {
			t.Errorf("GET carries %q", leak)
		}
		if strings.Contains(logged, leak) {
			t.Errorf("the log carries %q", leak)
		}
		for i, e := range events {
			if strings.Contains(e.Before, leak) || strings.Contains(e.After, leak) {
				t.Errorf("audit event %d carries %q", i, leak)
			}
		}
	}
}

// Every write is audited sso-settings.changed by the Owner, with before and
// after as GET answers them — neither the client secret, its sealed bytes,
// nor a token or its digest — and re-records the authority mode the row
// derives in its transaction.
func TestSsoSettings_EveryWriteIsAuditedWithoutASecret(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, append(f.options(), withEnv("SCIM_TOKEN", scimToken))...)
	owner, ownerID := h.bootstrapOwner(t)
	got := getSSO(t, owner)
	importedSealed := h.sealedClientSecret(t)

	const newSecret = "a-brand-new-client-secret-91c2"
	got = mustPutOIDC(t, owner, got.Version, ssoRequest("entra", map[string]any{"clientSecret": newSecret}))
	newSealed := h.sealedClientSecret(t)
	token := issueScimToken(t, owner, nil)
	for _, path := range []string{ssoScimPreviousPath, ssoScimPath, ssoOIDCPath} {
		if r := owner.do(http.MethodDelete, path, nil); r.status != http.StatusOK {
			t.Fatalf("DELETE %s: status %d body %s", path, r.status, r.body)
		}
	}
	if final := getSSO(t, owner); final.Version != got.Version+4 {
		t.Errorf("version = %d, want %d: every write moves it on", final.Version, got.Version+4)
	}

	events := h.auditEvents(t, ssoChangedAction)
	if len(events) != 5 {
		t.Fatalf("%d audit events, want 5", len(events))
	}
	leaks := []string{fakeClientSecret, newSecret, scimToken, token.Token,
		base64.StdEncoding.EncodeToString(importedSealed), base64.StdEncoding.EncodeToString(newSealed),
		hex.EncodeToString(importedSealed), hex.EncodeToString(newSealed), "\\x" + hex.EncodeToString(newSealed)}
	leaks = append(append(leaks, digestForms(scimToken)...), digestForms(token.Token)...)
	for i, e := range events {
		if e.Actor == nil || *e.Actor != ownerID || !strings.Contains(e.Details, ssoChangedAction) {
			t.Errorf("audit event %d: actor %v details %s", i, e.Actor, e.Details)
		}
		if !strings.Contains(e.Before, `"version"`) || !strings.Contains(e.After, `"version"`) {
			t.Errorf("audit event %d: before %s after %s, want the settings as GET answers them", i, e.Before, e.After)
		}
		for _, leak := range leaks {
			if strings.Contains(e.Before, leak) || strings.Contains(e.After, leak) {
				t.Errorf("audit event %d carries %q", i, leak)
			}
		}
		for _, word := range []string{"clientSecret\"", "sealed", "sha256", "Sha256"} {
			if strings.Contains(e.Before, word) || strings.Contains(e.After, word) {
				t.Errorf("audit event %d carries the field %q", i, word)
			}
		}
	}

	// Both off: the mode the row derives is local, recorded and audited
	// by the write that turned the last one off.
	if n := h.count(t, `SELECT count(*) FROM identity.system_settings WHERE authority_mode = 'local'`); n != 1 {
		t.Error("the authority mode was not recorded local")
	}
	modes := h.auditEvents(t, "authority-mode.changed")
	if len(modes) != 1 || !strings.Contains(modes[0].After, `"local"`) {
		t.Errorf("authority-mode.changed = %+v, want one to local", modes)
	}
}

// Every operation is the Owner's, and under OWNERS_REQUIRE_MFA an Owner's
// session must have verified a second factor: a user and an Owner without
// MFA are 403 on every one of them.
func TestSsoSettings_OnlyAnOwnerWithMFA(t *testing.T) {
	t.Parallel()
	h := newHarness(t, withEnv("OWNERS_REQUIRE_MFA", "1"))
	_, ownerID := h.bootstrapOwner(t)
	user := h.signIn(t, h.insertUser(t, "user@example.test", identity.RoleUserID), true)
	ownerWithoutMFA := h.signIn(t, ownerID, false)
	owner := h.signIn(t, ownerID, true)

	operations := []struct {
		method, path string
		body         any
		opts         []reqOpt
	}{
		{http.MethodGet, ssoPath, nil, nil},
		{http.MethodPut, ssoOIDCPath, ssoRequest("entra", map[string]any{"clientSecret": "s"}), []reqOpt{header("If-Match", "0")}},
		{http.MethodDelete, ssoOIDCPath, nil, nil},
		{http.MethodPost, ssoScimTokenPath, nil, nil},
		{http.MethodDelete, ssoScimPreviousPath, nil, nil},
		{http.MethodDelete, ssoScimPath, nil, nil},
	}
	for _, op := range operations {
		for name, c := range map[string]*client{"a user": user, "an Owner without MFA": ownerWithoutMFA} {
			if r := c.do(op.method, op.path, op.body, op.opts...); r.status != http.StatusForbidden {
				t.Errorf("%s %s as %s: status %d, want 403", op.method, op.path, name, r.status)
			}
		}
		if r := h.client(t).do(op.method, op.path, op.body, op.opts...); r.status != http.StatusUnauthorized {
			t.Errorf("%s %s anonymously: status %d, want 401", op.method, op.path, r.status)
		}
	}
	if events := h.auditEvents(t, ssoChangedAction); len(events) != 0 {
		t.Errorf("%d audit events after the refusals, want none", len(events))
	}
	for _, op := range operations {
		if r := owner.do(op.method, op.path, op.body, op.opts...); r.status != http.StatusOK {
			t.Errorf("%s %s as an Owner with MFA: status %d body %s, want 200", op.method, op.path, r.status, r.body)
		}
	}
	// PUT, DELETE oidc, POST token and DELETE scim wrote; DELETE previous
	// found no previous token and wrote nothing.
	events := h.auditEvents(t, ssoChangedAction)
	if len(events) != 4 {
		t.Errorf("%d audit events, want 4", len(events))
	}
	for _, e := range events {
		if !e.MFA {
			t.Error("an Owner's audited write is not marked MFA")
		}
	}
}

// A token file that has grown past maxAssertionBytes since it was saved
// fails the sign-in as a remote failure, and nothing is sent to the
// provider; the read is bounded, so a 1 MiB file is not read whole.
func TestSsoSettings_AnOversizedTokenFileFailsTheSignIn(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	f.update(func(f *fakeOIDC) { f.secret = "" })
	path := tokenFile(t)
	h := newHarness(t, append(f.options(), withEnv("OIDC_WORKLOAD_IDENTITY_TOKEN_FILE", path))...)
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 1<<20)), 0o600); err != nil {
		t.Fatal(err)
	}
	assertOIDCRedirect(t, h.oidcCallback(t, h.client(t), f), oidcErrorLocation("oidc_remote_failure"))
	if n := len(f.redemptionForms()); n != 0 {
		t.Errorf("%d redemptions, want none: nothing is sent without an assertion", n)
	}
}

// The import never stops the API over a value longer than the row holds: a
// display name is imported cut to 100 characters, with a warning naming the
// variable.
func TestSsoSettings_TheImportCutsALongDisplayName(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, append(f.options(), withEnv("OIDC_DISPLAY_NAME", strings.Repeat("å", 150)))...)
	owner, _ := h.bootstrapOwner(t)
	got := getSSO(t, owner)
	if got.Version != 1 || !got.Oidc.Enabled || deref(got.Oidc.DisplayName) != strings.Repeat("å", 100) {
		t.Errorf("the import = version %d enabled %v display %q, want the provider on, its name cut to 100", got.Version, got.Oidc.Enabled, deref(got.Oidc.DisplayName))
	}
	warned := false
	for _, rec := range h.logRecords(t) {
		if rec["level"] == "WARN" && rec["variable"] == "OIDC_DISPLAY_NAME" {
			warned = true
		}
		if msg, _ := rec["msg"].(string); strings.Contains(msg, strings.Repeat("å", 101)) {
			t.Errorf("a log line carries the whole name: %s", msg)
		}
	}
	if !warned {
		t.Error("no warning names OIDC_DISPLAY_NAME")
	}
}
