package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// TestNewRelyingParty_IsTheAppURL pins the relying party to .NET's
// IdentityPasskeyOptions (EA/AuthServiceCollectionExtensions.cs:41-62): the
// RP ID is APP_URL's host and the one origin APP_URL's, user verification
// and a resident key are required, five minutes, attestation "none", and
// the library's wall-clock expiry off. An IP host is no RP ID.
func TestNewRelyingParty_IsTheAppURL(t *testing.T) {
	t.Parallel()
	rp, err := newRelyingParty(&config.Config{AppOrigin: "https://vantigo.example.com:8443", AppHostname: "vantigo.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	c := rp.Config
	sel := c.AuthenticatorSelection
	if c.RPID != "vantigo.example.com" || c.RPDisplayName != "vantigo.example.com" ||
		!slices.Equal(c.RPOrigins, []string{"https://vantigo.example.com:8443"}) ||
		len(c.RPTopOrigins) != 0 || len(c.RPOpaqueOrigins) != 0 || c.RPAllowCrossOrigin ||
		c.AttestationPreference != protocol.PreferNoAttestation ||
		sel.ResidentKey != protocol.ResidentKeyRequirementRequired || sel.RequireResidentKey == nil || !*sel.RequireResidentKey ||
		sel.UserVerification != protocol.VerificationRequired || sel.AuthenticatorAttachment != "" ||
		c.Timeouts.Login.Timeout != 5*time.Minute || c.Timeouts.Registration.Timeout != 5*time.Minute ||
		c.Timeouts.Login.Enforce || c.Timeouts.Registration.Enforce {
		t.Errorf("relying party = %+v", c)
	}
	for _, host := range []string{"127.0.0.1", "10.0.0.5", "::1"} {
		if _, err := newRelyingParty(&config.Config{AppOrigin: "http://" + host, AppHostname: host}); err == nil {
			t.Errorf("host %s: a relying party, want an error", host)
		}
	}
}

// TestPasskeySignIn_WithoutARelyingPartyIsPasskeyConfiguration: an
// installation whose APP_URL host cannot be an RP ID (the internal server's
// configuration has none) still starts, and its passkey ceremonies answer
// 500 passkey_configuration, .NET's answer to options it could not generate.
func TestPasskeySignIn_WithoutARelyingPartyIsPasskeyConfiguration(t *testing.T) {
	t.Parallel()
	srv, pool := newInternalServer(t)
	if srv.relyingParty != nil {
		t.Fatal("a relying party without APP_URL")
	}
	ctx := contracts.WithRequest(context.Background(), httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
	email, credential := "someone@example.test", "{}"
	begin, err := srv.PostIdentityPasskeysLoginBegin(ctx, gen.PostIdentityPasskeysLoginBeginRequestObject{Body: &gen.PasskeyLoginBeginRequest{Email: &email}})
	wantConfiguration(t, "begin", begin, err)
	complete, err := srv.PostIdentityPasskeysLoginComplete(ctx, gen.PostIdentityPasskeysLoginCompleteRequestObject{
		Body: &gen.PasskeyCompleteRequest{CeremonyId: uuid.New(), CredentialJson: &credential},
	})
	wantConfiguration(t, "complete", complete, err)
	if n := countRows(t, pool, `SELECT count(*) FROM identity.passkey_ceremonies`); n != 0 {
		t.Errorf("%d ceremonies, want none", n)
	}
}

func wantConfiguration(t *testing.T, what string, answer any, err error) {
	t.Helper()
	r, ok := answer.(refusal)
	if err != nil || !ok || r.status != http.StatusInternalServerError || r.body.Error.Code != "passkey_configuration" {
		t.Errorf("%s = %v, %v; want 500 passkey_configuration", what, answer, err)
	}
}

// TestPasskeyLimitsKeepDotNetsPolicy pins the passkey operations' rate
// limits (EA/AccountSettingsEndpoints.cs:58-71): Mfa on enrolment and
// removal, PasskeyLogin on sign-in, none on the list.
func TestPasskeyLimitsKeepDotNetsPolicy(t *testing.T) {
	t.Parallel()
	want := map[string]ratelimit.Policy{
		"postIdentityAccountPasskeysBegin":            policyMfa,
		"postIdentityAccountPasskeysComplete":         policyMfa,
		"deleteIdentityAccountPasskeysByCredentialId": policyMfa,
		"postIdentityPasskeysLoginBegin":              policyPasskeyLogin,
		"postIdentityPasskeysLoginComplete":           policyPasskeyLogin,
	}
	for op, p := range want {
		if got, ok := limits[op]; !ok || got != p {
			t.Errorf("limits[%s] = %+v, want %s", op, got, p.Name)
		}
	}
	if p, ok := limits["getIdentityAccountPasskeys"]; ok {
		t.Errorf("the passkey list is limited by %s; .NET limited it by none", p.Name)
	}
}
