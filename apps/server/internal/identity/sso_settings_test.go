package identity_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
)

// The single sign-on settings (Entra design D1): the environment imported
// once into identity.sso_settings, and every reader reading the row
// through its 20-second cache.

const (
	importedClientSecret = "imported-oidc-client-secret-51c2"
	importedScimToken    = "imported-scim-token-0e9d7a"
)

// importedNames are the variables newImportHarness sets, as the import
// names them.
var importedNames = []string{"OIDC_AUTHORITY", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "OIDC_PROVIDER", "SCIM_TOKEN"}

// newImportHarness is a fresh installation whose environment still carries
// an Entra provider and a SCIM token.
func newImportHarness(t *testing.T) *harness {
	t.Helper()
	return newHarness(t,
		withEnv("OIDC_PROVIDER", "entra"),
		withEnv("OIDC_AUTHORITY", fakeEntraAuthority),
		withEnv("OIDC_CLIENT_ID", fakeEntraClientID),
		withEnv("OIDC_CLIENT_SECRET", importedClientSecret),
		withEnv("SCIM_TOKEN", importedScimToken),
	)
}

// ssoRow is the parts of the row the import tests read.
type ssoRow struct {
	version              int32
	imported, oidc, scim bool
	sealed, digest       []byte
	updatedBy            *string
}

func (h *harness) ssoRow(t testing.TB) ssoRow {
	t.Helper()
	var r ssoRow
	if err := h.pool.QueryRow(context.Background(), `
		SELECT version, imported_from_environment, oidc_enabled, scim_enabled,
		       coalesce(oidc_client_secret_sealed, ''), coalesce(scim_token_sha256, ''), updated_by_user_id::text
		FROM identity.sso_settings WHERE id = 1`).
		Scan(&r.version, &r.imported, &r.oidc, &r.scim, &r.sealed, &r.digest, &r.updatedBy); err != nil {
		t.Fatalf("the single sign-on row: %v", err)
	}
	return r
}

// warnings is every WARN record the installation logged whose message
// contains part.
func (h *harness) warnings(t testing.TB, part string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, rec := range h.logRecords(t) {
		if msg, _ := rec["msg"].(string); rec["level"] == "WARN" && strings.Contains(msg, part) {
			out = append(out, rec)
		}
	}
	return out
}

// loggedNames is a log record's variables attribute.
func loggedNames(rec map[string]any) []string {
	raw, _ := rec["variables"].([]any)
	names := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		names = append(names, s)
	}
	return names
}

// mountAgain mounts identity once more over the harness's pool, as a
// second API process — or the same one restarted — does.
func (h *harness) mountAgain(t testing.TB) {
	t.Helper()
	if _, err := module.Compose(h.deps, identity.Module(identity.NewAccess(h.deps))); err != nil {
		t.Fatalf("mount again: %v", err)
	}
}

// A fresh installation whose environment carries OIDC_* and SCIM_* imports
// them once at the mount: version 1, imported from the environment, no
// user behind it, the client secret sealed under its purpose (never the
// clear bytes), the token's SHA-256 only; one audit row naming the
// variables and nothing of their values, and a warning naming them.
// Mounting again imports nothing and warns that the variables are ignored.
func TestSSOSettings_TheEnvironmentIsImportedOnce(t *testing.T) {
	t.Parallel()
	h := newImportHarness(t)

	row := h.ssoRow(t)
	if row.version != 1 || !row.imported || !row.oidc || !row.scim || row.updatedBy != nil {
		t.Errorf("row = %+v, want version 1, imported, OIDC and SCIM on, no user", row)
	}
	if len(row.sealed) == 0 || bytes.Contains(row.sealed, []byte(importedClientSecret)) {
		t.Errorf("the client secret is not sealed: %x", row.sealed)
	}
	box, err := secrets.New(h.cfg.AppSecret)
	if err != nil {
		t.Fatal(err)
	}
	if opened, err := box.Open("identity/oidc-client-secret", row.sealed); err != nil || string(opened) != importedClientSecret {
		t.Errorf("the sealed secret opens to %q, %v", opened, err)
	}
	if digest := sha256.Sum256([]byte(importedScimToken)); !bytes.Equal(row.digest, digest[:]) {
		t.Errorf("the token digest = %x, want %x", row.digest, digest)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sso_settings WHERE scim_token_sha256 = convert_to($1, 'UTF8')`, importedScimToken); n != 0 {
		t.Error("the row holds the clear token")
	}

	audits := h.auditEvents(t, "sso-settings.imported")
	if len(audits) != 1 {
		t.Fatalf("%d sso-settings.imported audits, want 1", len(audits))
	}
	a := audits[0]
	if a.Actor != nil || a.Details != `{"action":"sso-settings.imported"}` ||
		a.After != `{"variables":["OIDC_AUTHORITY","OIDC_CLIENT_ID","OIDC_CLIENT_SECRET","OIDC_PROVIDER","SCIM_TOKEN"]}` {
		t.Errorf("audit = %+v", a)
	}
	for _, secret := range []string{importedClientSecret, importedScimToken} {
		if strings.Contains(a.Details+a.Before+a.After, secret) {
			t.Errorf("the audit carries %q", secret)
		}
		if bytes.Contains(h.log.Bytes(), []byte(secret)) {
			t.Errorf("the log carries %q", secret)
		}
	}
	warned := h.warnings(t, "imported from the environment")
	if len(warned) != 1 || !slices.Equal(loggedNames(warned[0]), importedNames) {
		t.Errorf("import warnings = %v, want one naming %v", warned, importedNames)
	}
	if n := len(h.warnings(t, "variables are ignored")); n != 0 {
		t.Errorf("%d ignored warnings at the importing mount, want none", n)
	}

	h.mountAgain(t)
	if again := h.ssoRow(t); again.version != 1 || !bytes.Equal(again.sealed, row.sealed) {
		t.Errorf("the second mount rewrote the row: %+v", again)
	}
	if n := len(h.auditEvents(t, "sso-settings.imported")); n != 1 {
		t.Errorf("%d imports audited after the second mount, want 1", n)
	}
	if n := len(h.warnings(t, "imported from the environment")); n != 1 {
		t.Errorf("%d import warnings after the second mount, want 1", n)
	}
	ignored := h.warnings(t, "variables are ignored")
	if len(ignored) != 1 || !slices.Equal(loggedNames(ignored[0]), importedNames) {
		t.Errorf("ignored warnings = %v, want one naming %v", ignored, importedNames)
	}
}

// An installation without OIDC_* or SCIM_* imports nothing: the row exists,
// never written, everything off, and nothing is audited or warned.
func TestSSOSettings_NothingToImport(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if row := h.ssoRow(t); row.version != 0 || row.imported || row.oidc || row.scim {
		t.Errorf("row = %+v, want version 0 and everything off", row)
	}
	if n := len(h.auditEvents(t, "sso-settings.imported")); n != 0 {
		t.Errorf("%d imports audited, want none", n)
	}
	if n := len(h.warnings(t, "OIDC and SCIM")); n != 0 {
		t.Errorf("%d warnings, want none", n)
	}
}

// The imported token authenticates a SCIM request; a previous token,
// imported with its expiry, is accepted until then and not from then on.
func TestSSOSettings_TheImportedTokensAuthenticate(t *testing.T) {
	t.Parallel()
	expires := time.Now().Add(time.Hour).Truncate(time.Second)
	h := newScimHarness(t, withPreviousScimToken(expires))
	h.toWallClock()
	current, previous := h.scim(t, scimToken), h.scim(t, scimPreviousToken)
	if r := current.do(http.MethodGet, "/ServiceProviderConfig", nil); r.status != http.StatusOK {
		t.Fatalf("the current token: status %d, want 200", r.status)
	}
	if r := previous.do(http.MethodGet, "/ServiceProviderConfig", nil); r.status != http.StatusOK {
		t.Fatalf("the previous token before its expiry: status %d, want 200", r.status)
	}
	h.advance(expires.Sub(h.now()))
	wantScimError(t, "the previous token at its expiry", previous.do(http.MethodGet, "/ServiceProviderConfig", nil), http.StatusUnauthorized, "invalidValue", scimUnauthorizedText)
	if r := current.do(http.MethodGet, "/ServiceProviderConfig", nil); r.status != http.StatusOK {
		t.Errorf("the current token after the overlap: status %d, want 200", r.status)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.sso_settings WHERE scim_previous_expires_at = $1`, expires); n != 1 {
		t.Error("the previous token's expiry was not imported as it was")
	}
}

// scimAdmits reports whether a admits a SCIM request bearing token.
func scimAdmits(t testing.TB, a *identity.Access, token string) bool {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/identity/scim/v2/Users", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	p, err := a.Check(r, contracts.Rule{Kind: contracts.RuleSCIM})
	if errors.Is(err, contracts.ErrUnauthenticated) {
		return false
	}
	if err != nil {
		t.Fatalf("SCIM check: %v", err)
	}
	return p.SCIM
}

// Two Access instances over one pool — two replicas — each read the row
// through their own cache: a change written behind them is in effect on
// both once 20 seconds have passed since they last read it, not before.
func TestSSOSettings_TwoReplicasSeeAChangeWithinTheTTL(t *testing.T) {
	t.Parallel()
	h := newScimHarness(t)
	first, second := h.access, identity.NewAccess(h.deps)
	if !scimAdmits(t, first, scimToken) || !scimAdmits(t, second, scimToken) {
		t.Fatal("the imported token is not admitted")
	}

	h.exec(t, `UPDATE identity.sso_settings SET scim_enabled = false, version = version + 1`)
	h.advance(ssoSettingsCacheTTLForTest - time.Second)
	if !scimAdmits(t, first, scimToken) || !scimAdmits(t, second, scimToken) {
		t.Error("a replica read the row again within the TTL")
	}
	h.advance(time.Second)
	if scimAdmits(t, first, scimToken) || scimAdmits(t, second, scimToken) {
		t.Error("a replica still admits the token 20 s after SCIM was turned off")
	}
	if r := h.scim(t, scimToken).do(http.MethodGet, "/ServiceProviderConfig", nil); r.status != http.StatusUnauthorized {
		t.Errorf("the installation's SCIM endpoint answers %d with SCIM off, want 401", r.status)
	}
}

// ssoSettingsCacheTTLForTest is the readers' TTL, ssoSettingsCacheTTL.
const ssoSettingsCacheTTLForTest = 20 * time.Second

// The authority mode follows the row, not a process's configuration: in a
// process whose Config carries neither OIDC nor SCIM, a row with SCIM on
// makes the provider act federated — Mode answers it, and a write
// succeeds as a federated one, adopting the managed user by number — and
// never ErrModeMismatch, though the mount recorded local.
func TestSSOSettings_TheModeFollowsTheRowNotTheConfig(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if h.cfg.OIDC != nil || h.cfg.SCIM != nil {
		t.Fatal("the harness configures OIDC or SCIM")
	}
	owner, _ := h.bootstrapOwner(t)
	employeeRole(t, h)
	id := h.createUser(t, owner, "row@example.test", identity.RoleUser)
	mustManage(t, owner, id, "R-1")

	h.exec(t, `UPDATE identity.sso_settings SET scim_enabled = true, scim_token_sha256 = $1, version = version + 1`,
		func() []byte { d := sha256.Sum256([]byte("row-token")); return d[:] }())
	p := h.accounts(t)
	ctx := context.Background()
	if mode, err := p.Mode(ctx); err != nil || mode != contracts.AccountModeFederated {
		t.Fatalf("Mode = %q, %v, want federated", mode, err)
	}
	ref, err := p.Prepare(ctx, systemActor, spec("ignored@example.test", "R-1"))
	if err != nil || ref.UserID != id || ref.Created {
		t.Fatalf("Prepare = %+v, %v, want the managed user adopted", ref, err)
	}
	_, err = p.Prepare(ctx, systemActor, spec("nobody@example.test", "R-2"))
	wantErr(t, "a number nobody holds", err, contracts.ErrAccountNotFound)
	if _, err := p.Update(ctx, systemActor, id, spec("other@example.test", "R-1")); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if n := h.count(t, `SELECT count(*) FROM identity.users WHERE id = $1 AND email = 'row@example.test'`, id); n != 1 {
		t.Error("a federated Update wrote the address")
	}
}

// A change of the OIDC block rebuilds the relying party once the server's
// reader sees the new version: the sign-in page's display name follows
// it, and a sign-in runs against the provider on the rebuilt party, which
// discovers anew.
func TestSSOSettings_TheRelyingPartyIsRebuiltOnANewVersion(t *testing.T) {
	t.Parallel()
	f := newFakeOIDC(t)
	h := newHarness(t, f.options()...)
	h.bootstrapOwner(t)
	providers := func() string {
		r := h.client(t).do(http.MethodGet, "/api/v1/identity/providers", nil)
		if r.status != http.StatusOK {
			t.Fatalf("providers: status %d", r.status)
		}
		return strings.TrimSpace(string(r.body))
	}
	if got := providers(); got != `{"oidc":{"displayName":"Workforce SSO"}}` {
		t.Errorf("providers = %s", got)
	}
	if r := h.oidcSignIn(t, h.client(t), f); r.status != http.StatusFound {
		t.Fatalf("the first sign-in: status %d", r.status)
	}
	discovered := f.discoveries()

	h.exec(t, `UPDATE identity.sso_settings SET oidc_display_name = 'Contoso', version = version + 1`)
	if got := providers(); got != `{"oidc":{"displayName":"Workforce SSO"}}` {
		t.Errorf("providers within the TTL = %s, want the party it had", got)
	}
	h.advance(ssoSettingsCacheTTLForTest)
	if got := providers(); got != `{"oidc":{"displayName":"Contoso"}}` {
		t.Errorf("providers after the TTL = %s, want the rebuilt party's", got)
	}
	if r := h.oidcSignIn(t, h.client(t), f); r.status != http.StatusFound || r.header("Location") != "/" {
		t.Errorf("a sign-in on the rebuilt party: status %d Location %q", r.status, r.header("Location"))
	}
	if f.discoveries() != discovered+1 {
		t.Errorf("discoveries %d → %d, want the rebuilt party to discover once", discovered, f.discoveries())
	}

	h.exec(t, `UPDATE identity.sso_settings SET oidc_enabled = false, version = version + 1`)
	h.advance(ssoSettingsCacheTTLForTest)
	if got := providers(); got != `{"oidc":null}` {
		t.Errorf("providers with OIDC off = %s", got)
	}
	if r := h.client(t).do(http.MethodGet, oidcChallengePath, nil); r.status != http.StatusNotFound {
		t.Errorf("challenge with OIDC off: status %d, want 404", r.status)
	}
}
