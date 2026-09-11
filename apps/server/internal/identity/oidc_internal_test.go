package identity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// The claim-policy fixtures: .NET's (TS/Endpoints/Auth/StaticOidcSecurityTests.cs).
const (
	policyTenantID = "00000000-0000-0000-0000-000000000000"
	policyClientID = "11111111-1111-1111-1111-111111111111"
	policyObjectID = "22222222-2222-2222-2222-222222222222"
	policyOtherID  = "33333333-3333-3333-3333-333333333333"
)

var (
	entraPolicyConfig = &config.OIDCConfig{
		Provider:     "entra",
		Authority:    "https://login.microsoftonline.com/" + policyTenantID + "/v2.0",
		ClientID:     policyClientID,
		ClientSecret: "secret",
		DisplayName:  "Entra",
	}
	googlePolicyConfig = &config.OIDCConfig{
		Provider:       "google",
		Authority:      "https://accounts.google.com",
		ClientID:       "client.apps.googleusercontent.com",
		ClientSecret:   "secret",
		AllowedDomains: []string{"example.com"},
		DisplayName:    "Google",
	}
)

// Ported from StaticOidcSecurityTests.EntraPolicyRequiresConfiguredTenantObjectAndAuthorizedParty.
func TestOIDCEntraPolicy_RequiresTheConfiguredTenantObjectAndAuthorizedParty(t *testing.T) {
	valid := oidcClaims{
		TenantID: policyTenantID, ObjectID: policyObjectID,
		Audience: audienceList{policyClientID, "another-audience"}, AuthorizedParty: policyClientID,
	}
	if err := checkClaimPolicy(entraPolicyConfig, valid); err != nil {
		t.Errorf("valid claims: %v", err)
	}
	if checkClaimPolicy(entraPolicyConfig, oidcClaims{TenantID: policyOtherID, ObjectID: policyObjectID}) == nil {
		t.Error("another tenant passed")
	}
	wrongAuthorizedParty := valid
	wrongAuthorizedParty.AuthorizedParty = policyOtherID
	if checkClaimPolicy(entraPolicyConfig, wrongAuthorizedParty) == nil {
		t.Error("another authorized party passed")
	}

	// Each rule on its own, beyond .NET's three cases.
	for name, tc := range map[string]struct {
		claims oidcClaims
		pass   bool
	}{
		"one audience needs no authorized party": {oidcClaims{TenantID: policyTenantID, ObjectID: policyObjectID, Audience: audienceList{policyClientID}}, true},
		"a braced tenant is the same GUID":       {oidcClaims{TenantID: "{" + policyTenantID + "}", ObjectID: policyObjectID}, true},
		"an unhyphenated tenant is the same":     {oidcClaims{TenantID: strings.ReplaceAll(policyTenantID, "-", ""), ObjectID: policyObjectID}, true},
		"no tenant":                              {oidcClaims{ObjectID: policyObjectID}, false},
		"a urn tenant is not a .NET GUID":        {oidcClaims{TenantID: "urn:uuid:" + policyTenantID, ObjectID: policyObjectID}, false},
		"an object id that is not a GUID":        {oidcClaims{TenantID: policyTenantID, ObjectID: "not-a-guid"}, false},
		"no object id":                           {oidcClaims{TenantID: policyTenantID}, false},
		"two audiences and no authorized party":  {oidcClaims{TenantID: policyTenantID, ObjectID: policyObjectID, Audience: audienceList{policyClientID, "other"}}, false},
	} {
		if err := checkClaimPolicy(entraPolicyConfig, tc.claims); (err == nil) != tc.pass {
			t.Errorf("%s: error %v, want pass %v", name, err, tc.pass)
		}
	}
}

// Ported from StaticOidcSecurityTests.GooglePolicyRequiresVerifiedAllowedHostedDomain.
func TestOIDCGooglePolicy_RequiresAVerifiedAllowedHostedDomain(t *testing.T) {
	valid := oidcClaims{Email: "person@example.com", EmailVerified: true, HostedDomain: "example.com"}
	if err := checkClaimPolicy(googlePolicyConfig, valid); err != nil {
		t.Errorf("valid claims: %v", err)
	}
	unverified := valid
	unverified.EmailVerified = false
	if checkClaimPolicy(googlePolicyConfig, unverified) == nil {
		t.Error("an unverified email passed")
	}
	mismatchedDomain := valid
	mismatchedDomain.HostedDomain = "other.example"
	if checkClaimPolicy(googlePolicyConfig, mismatchedDomain) == nil {
		t.Error("a hosted domain other than the email's passed")
	}

	// Each rule on its own, beyond .NET's three cases.
	for name, tc := range map[string]struct {
		claims oidcClaims
		pass   bool
	}{
		"hd is trimmed, dot-stripped and lowercased": {oidcClaims{Email: "person@Example.com", EmailVerified: true, HostedDomain: " EXAMPLE.com. "}, true},
		"a domain that is not allowed":               {oidcClaims{Email: "person@other.example", EmailVerified: true, HostedDomain: "other.example"}, false},
		"no hosted domain":                           {oidcClaims{Email: "person@example.com", EmailVerified: true}, false},
		"a display-name address":                     {oidcClaims{Email: "Person <person@example.com>", EmailVerified: true, HostedDomain: "example.com"}, false},
		"two at signs":                               {oidcClaims{Email: "a@b@example.com", EmailVerified: true, HostedDomain: "example.com"}, false},
		"a domain without a dot":                     {oidcClaims{Email: "person@localhost", EmailVerified: true, HostedDomain: "localhost"}, false},
		"a subdomain of an allowed domain":           {oidcClaims{Email: "person@eu.example.com", EmailVerified: true, HostedDomain: "eu.example.com"}, false},
	} {
		if err := checkClaimPolicy(googlePolicyConfig, tc.claims); (err == nil) != tc.pass {
			t.Errorf("%s: error %v, want pass %v", name, err, tc.pass)
		}
	}

	// An allowed domain config kept with a trailing dot still matches, as
	// .NET normalized it away.
	dotted := *googlePolicyConfig
	dotted.AllowedDomains = []string{"example.com."}
	if err := checkClaimPolicy(&dotted, valid); err != nil {
		t.Errorf("a dotted allowed domain: %v", err)
	}
	unknown := *googlePolicyConfig
	unknown.Provider = "okta"
	if checkClaimPolicy(&unknown, valid) == nil {
		t.Error("an unknown provider passed")
	}
}

// TestOIDCNormalizeIssuer pins TryNormalizeIssuer's rules
// (CFG/WorkforceOidcOptions.cs:160-170).
func TestOIDCNormalizeIssuer(t *testing.T) {
	for in, want := range map[string]string{
		"https://login.microsoftonline.com/Tenant/v2.0/":  "https://login.microsoftonline.com/Tenant/v2.0",
		"HTTPS://Login.MicrosoftOnline.COM/Tenant/v2.0":   "https://login.microsoftonline.com/Tenant/v2.0",
		"https://accounts.google.com":                     "https://accounts.google.com",
		"https://accounts.google.com:443":                 "https://accounts.google.com",
		"http://idp.example:80/realm":                     "http://idp.example/realm",
		"https://idp.example:8443/realm":                  "https://idp.example:8443/realm",
		"https://[::1]:8443/realm":                        "https://[::1]:8443/realm",
		"https://idp.example/" + strings.Repeat("a", 100): "https://idp.example/" + strings.Repeat("a", 100),
	} {
		if got, ok := normalizeIssuer(in); !ok || got != want {
			t.Errorf("normalizeIssuer(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{
		"", "   ", "accounts.google.com", "/relative", "ftp://idp.example", "https:///path", "https:opaque",
		"https://user:pw@idp.example", "https://idp.example/?x=1", "https://idp.example/?", "https://idp.example/#fragment",
		"https://idp.example/" + strings.Repeat("a", 2048),
	} {
		if got, ok := normalizeIssuer(in); ok {
			t.Errorf("normalizeIssuer(%q) = %q, want refused", in, got)
		}
	}
}

// TestOIDCReadExternalAccount pins ReadIdentity, ReadEmail and
// ReadDisplayName (EA/WorkforceOidcEndpoints.cs:253-321).
func TestOIDCReadExternalAccount(t *testing.T) {
	const issuer = "https://login.microsoftonline.com/" + policyTenantID + "/v2.0"
	base := oidcClaims{Issuer: issuer, Subject: "Subject-1", Email: "person@example.com", EmailVerified: true, Name: "Ada Lovelace"}
	a, ok := readExternalAccount(base, issuer)
	if !ok || a != (externalAccount{issuer: issuer, subject: "Subject-1", email: "person@example.com", emailVerified: true, displayName: "Ada Lovelace"}) {
		t.Errorf("readExternalAccount = %+v, %v", a, ok)
	}
	if a, ok := readExternalAccount(base, issuer+"x"); ok {
		t.Errorf("another issuer = %+v", a)
	}
	for _, subject := range []string{"", "a b", "tab\tbed", strings.Repeat("s", 513)} {
		c := base
		c.Subject = subject
		if _, ok := readExternalAccount(c, issuer); ok {
			t.Errorf("subject %q accepted", subject)
		}
	}
	long := base
	long.Subject = strings.Repeat("s", 512)
	if _, ok := readExternalAccount(long, issuer); !ok {
		t.Error("a 512-character subject was refused")
	}

	for in, want := range map[string]string{
		"person@example.com":               "person@example.com",
		"Person@Example.COM":               "Person@Example.COM",
		"":                                 "",
		" person@example.com":              "",
		"person@@example.com":              "",
		"@example.com":                     "",
		"person@":                          "",
		"person\x01@example.com":           "",
		strings.Repeat("a", 251) + "@b.cd": strings.Repeat("a", 251) + "@b.cd", // 256 characters
		strings.Repeat("a", 252) + "@b.cd": "",                                 // 257
	} {
		if got := readEmail(in); got != want {
			t.Errorf("readEmail(%q) = %q, want %q", in, got, want)
		}
	}

	for name, tc := range map[string]struct {
		claims oidcClaims
		want   string
	}{
		"name, whitespace collapsed":           {oidcClaims{Name: "  Ada    Lovelace ", GivenName: "X"}, "Ada Lovelace"},
		"a tab is a control character":         {oidcClaims{Name: "Ada\tLovelace", GivenName: "Ada"}, "Ada"},
		"given and family names":               {oidcClaims{Name: "bad\x07name", GivenName: "Ada", FamilyName: "Lovelace"}, "Ada Lovelace"},
		"given name alone":                     {oidcClaims{GivenName: "Ada"}, "Ada"},
		"preferred username":                   {oidcClaims{Name: strings.Repeat("n", 201), PreferredUsername: "ada@example.com"}, "ada@example.com"},
		"the subject when nothing else serves": {oidcClaims{Subject: "subject-7"}, "subject-7"},
		"the subject, cut to 200 characters":   {oidcClaims{Subject: strings.Repeat("s", 300)}, strings.Repeat("s", 200)},
	} {
		if got := readDisplayName(tc.claims); got != tc.want {
			t.Errorf("%s: readDisplayName = %q, want %q", name, got, tc.want)
		}
	}
}

// TestOIDCClaimDecoding pins how email_verified and aud decode: a boolean
// claim is true only for "true" in any case, as .NET compared it, and aud
// is one string or several.
func TestOIDCClaimDecoding(t *testing.T) {
	for raw, want := range map[string]bool{
		`{"email_verified":true}`:    true,
		`{"email_verified":"True"}`:  true,
		`{"email_verified":false}`:   false,
		`{"email_verified":"false"}`: false,
		`{"email_verified":1}`:       false,
		`{}`:                         false,
	} {
		var c oidcClaims
		if err := json.Unmarshal([]byte(raw), &c); err != nil || bool(c.EmailVerified) != want {
			t.Errorf("%s: email_verified = %v (%v), want %v", raw, c.EmailVerified, err, want)
		}
	}
	var one, many oidcClaims
	if err := json.Unmarshal([]byte(`{"aud":"a"}`), &one); err != nil || len(one.Audience) != 1 || one.Audience[0] != "a" {
		t.Errorf("aud string = %v (%v)", one.Audience, err)
	}
	if err := json.Unmarshal([]byte(`{"aud":["a","b"]}`), &many); err != nil || len(many.Audience) != 2 {
		t.Errorf("aud array = %v (%v)", many.Audience, err)
	}
}

// TestOIDCReadClientAssertion is ReadFresh's contract
// (EA/WorkforceOidcClientAssertion.cs:11-30): the file's trimmed content,
// read anew on every call, refused when empty or holding whitespace, with
// errors that never carry the content. The integration test proves the
// redemption uses it.
func TestOIDCReadClientAssertion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("  first-assertion\n")
	if got, err := readClientAssertion(path); err != nil || got != "first-assertion" {
		t.Errorf("first read = %q, %v", got, err)
	}
	write("second-assertion")
	if got, err := readClientAssertion(path); err != nil || got != "second-assertion" {
		t.Errorf("second read = %q, %v", got, err)
	}
	for _, content := range []string{"", " \n\t", "two words"} {
		write(content)
		trimmed := strings.TrimSpace(content)
		if _, err := readClientAssertion(path); err == nil || (trimmed != "" && strings.Contains(err.Error(), trimmed)) {
			t.Errorf("content %q: error %v", content, err)
		}
	}
	if _, err := readClientAssertion(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing file was read")
	}
}

// TestOIDCOpaqueEmail pins CreateOpaqueEmail's shape
// (CFG/WorkforceOidcOptions.cs:172-176): deterministic, "oidc-", 64 hex
// digits, sso.invalid, and the NUL separator keeps (issuer, subject) pairs
// apart that plain concatenation would merge.
func TestOIDCOpaqueEmail(t *testing.T) {
	shape := regexp.MustCompile(`^oidc-[0-9a-f]{64}@sso\.invalid$`)
	a := opaqueEmail("https://issuer.example", "subject")
	if !shape.MatchString(a) || a != opaqueEmail("https://issuer.example", "subject") {
		t.Errorf("opaqueEmail = %q", a)
	}
	if opaqueEmail("https://issuer.example", "Subject") == a {
		t.Error("the subject's case is ignored")
	}
	if opaqueEmail("ab", "c") == opaqueEmail("a", "bc") {
		t.Error("the separator does not separate")
	}
	if publicEmail(a) != nil {
		t.Error("the opaque address is shown")
	}
}
