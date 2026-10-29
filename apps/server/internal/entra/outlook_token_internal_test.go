package entra

import (
	"net/http"
	"testing"
)

// TestOutlookRefusalCode pins Microsoft's refusals of a refresh to the
// connection's error_code (Outlook connection design M5): by the AADSTS
// codes first, whatever the OAuth error says, then interaction_required and
// the rest of invalid_grant as sign_in_again; the client's own refusals are
// the credential's, never the person's; and a 5xx, a 429 or
// temporarily_unavailable is transient, no code at all.
func TestOutlookRefusalCode(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		error  string
		codes  []int
		code   string
		person bool
	}{
		{"code expired", 400, "invalid_grant", []int{70008}, "expired", true},
		{"refresh token past its lifetime", 400, "invalid_grant", []int{700082}, "expired", true},
		{"refresh token revoked by a password change", 400, "invalid_grant", []int{50173}, "expired", true},
		{"inactive too long", 400, "invalid_grant", []int{70043}, "sign_in_again", true},
		{"a sign-in frequency check", 400, "invalid_grant", []int{50132}, "sign_in_again", true},
		{"MFA required", 400, "interaction_required", []int{50076}, "mfa", true},
		{"MFA registration required", 400, "interaction_required", []int{50079}, "mfa", true},
		{"strong authentication", 400, "invalid_grant", []int{50078}, "mfa", true},
		{"a Conditional Access block", 400, "invalid_grant", []int{53003}, "blocked", true},
		{"a device not compliant", 400, "interaction_required", []int{53000}, "blocked", true},
		{"consent withdrawn", 400, "invalid_grant", []int{65001}, "consent_missing", true},
		{"interaction required, no code known", 400, "interaction_required", []int{9999999}, "sign_in_again", true},
		{"any other invalid_grant", 400, "invalid_grant", []int{9002313}, "sign_in_again", true},
		{"invalid_grant with no codes", 400, "invalid_grant", nil, "sign_in_again", true},
		{"another 4xx refusal", 400, "invalid_request", []int{900144}, "sign_in_again", true},
		{"the secret refused", 401, "invalid_client", []int{7000215}, "invalid_client", false},
		{"the application unknown", 400, "unauthorized_client", []int{700016}, "unauthorized_client", false},
		{"a server error", 500, "server_error", nil, "", false},
		{"a 503 with no body", 503, "", nil, "", false},
		{"throttled", 429, "", nil, "", false},
		{"temporarily unavailable", 400, "temporarily_unavailable", nil, "", false},
		{"a 200 without a token", 200, "", nil, "", false},
	} {
		code, person := outlookRefusalCode(c.status, outlookTokenAnswer{Error: c.error, ErrorCodes: c.codes})
		if code != c.code || person != c.person {
			t.Errorf("%s: (%q, %v), want (%q, %v)", c.name, code, person, c.code, c.person)
		}
	}
}

// TestClaimsChallenge reads a 401's WWW-Authenticate as a continuous access
// evaluation claims challenge only when it carries insufficient_claims and
// a claims value: an expired token's plain 401 is no challenge.
func TestClaimsChallenge(t *testing.T) {
	for _, c := range []struct {
		name   string
		header string
		want   bool
	}{
		{"a CAE challenge", `Bearer realm="", authorization_uri="https://login.microsoftonline.com/common/oauth2/authorize", error="insufficient_claims", claims="eyJhY2Nlc3NfdG9rZW4iOnt9fQ=="`, true},
		{"no claims value", `Bearer error="insufficient_claims"`, false},
		{"another error", `Bearer error="invalid_token", claims="eyJ9"`, false},
		{"a plain 401", `Bearer realm=""`, false},
		{"none", ``, false},
	} {
		h := http.Header{}
		if c.header != "" {
			h.Set("WWW-Authenticate", c.header)
		}
		if got := claimsChallenge(h); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// TestDelegatedScope is the scope a refresh asks: the row's Graph scopes by
// their resource, offline_access, and none of the OpenID scopes, which a
// refresh does not renew.
func TestDelegatedScope(t *testing.T) {
	got := delegatedScope([]string{"calendars.readwrite", "openid", "profile", "user.read", "offline_access", "email"})
	want := "https://graph.microsoft.com/calendars.readwrite https://graph.microsoft.com/user.read offline_access"
	if got != want {
		t.Errorf("scope = %q, want %q", got, want)
	}
}
