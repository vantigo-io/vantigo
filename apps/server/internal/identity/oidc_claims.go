package identity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/mail"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// oidcClaims are the id_token claims workforce OIDC reads: the ones the
// claim policies decide on and the ones an account is made from. Claims
// arrive with MapInboundClaims=false semantics, under their JWT names
// (EA/AuthServiceCollectionExtensions.cs:168).
type oidcClaims struct {
	Issuer            string       `json:"iss"`
	Subject           string       `json:"sub"`
	Audience          audienceList `json:"aud,omitempty"`
	AuthorizedParty   string       `json:"azp,omitempty"`
	TenantID          string       `json:"tid,omitempty"`
	ObjectID          string       `json:"oid,omitempty"`
	Email             string       `json:"email,omitempty"`
	EmailVerified     claimBool    `json:"email_verified,omitempty"`
	HostedDomain      string       `json:"hd,omitempty"`
	Name              string       `json:"name,omitempty"`
	GivenName         string       `json:"given_name,omitempty"`
	FamilyName        string       `json:"family_name,omitempty"`
	PreferredUsername string       `json:"preferred_username,omitempty"`
}

// audienceList is aud, which a JWT carries as one string or an array.
type audienceList []string

func (a *audienceList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*a = audienceList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

// claimBool is a boolean claim as .NET read one: true only for a claim
// whose value is "true", ignoring case (EA/WorkforceOidcEndpoints.cs:273-276,
// EA/StaticOidcClaimValidation.cs:58). A JSON true is "true" to .NET, and so
// is the string "True"; anything else, a missing claim included, is false.
type claimBool bool

func (c *claimBool) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch v := v.(type) {
	case bool:
		*c = claimBool(v)
	case string:
		*c = claimBool(strings.EqualFold(v, "true"))
	default:
		*c = false
	}
	return nil
}

// maxIssuerLength is the longest normalized issuer .NET accepted
// (CFG/WorkforceOidcOptions.cs:169).
const maxIssuerLength = 2048

// normalizeIssuer is .NET's WorkforceOidcOptions.TryNormalizeIssuer
// (CFG/WorkforceOidcOptions.cs:160-170): an absolute http or https URL with
// a host and no user info, query or fragment, as scheme://host[:port]path
// with the scheme and host lowercased, a default port dropped, the path's
// case kept and a trailing "/" trimmed, at most 2048 characters. It is the
// oidc_links issuer and what a token's issuer is compared with the
// authority by.
func normalizeIssuer(v string) (string, bool) {
	if strings.TrimSpace(v) == "" {
		return "", false
	}
	u, err := url.Parse(v)
	if err != nil || !u.IsAbs() || u.Opaque != "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if (scheme != "http" && scheme != "https") || host == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return "", false
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // an IPv6 literal, bracketed as .NET's Uri.Host keeps it
	}
	authority := host
	if p := u.Port(); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil {
			return "", false
		}
		defaultPort := scheme == "https" && port == 443 || scheme == "http" && port == 80
		if !defaultPort {
			authority += ":" + strconv.Itoa(port)
		}
	}
	normalized := scheme + "://" + authority + strings.TrimRight(u.EscapedPath(), "/")
	if utf16Length(normalized) > maxIssuerLength {
		return "", false
	}
	return normalized, true
}

// checkClaimPolicy is .NET's StaticOidcClaimValidation.Validate
// (EA/StaticOidcClaimValidation.cs:20-27): the configured provider's claim
// policy. Its error is a reason to log, never shown to the browser.
func checkClaimPolicy(cfg *config.OIDCConfig, c oidcClaims) error {
	switch cfg.Provider {
	case "entra":
		return checkEntraClaims(cfg, c)
	case "google":
		return checkGoogleClaims(cfg, c)
	default:
		return errors.New("the configured OIDC provider is not supported")
	}
}

// checkEntraClaims is .NET's Entra policy (EA/StaticOidcClaimValidation.cs:29-49):
// tid is the configured tenant (both GUIDs), oid is a GUID, and a token for
// more than one audience names the client as its authorized party. The
// configured tenant is the one config pinned into the authority
// (CFG/WorkforceOidcOptions.cs:151-158). Entra has no domain restriction:
// OIDC_ALLOWED_DOMAINS is refused for it.
func checkEntraClaims(cfg *config.OIDCConfig, c oidcClaims) error {
	tenant, ok := parseGUID(c.TenantID)
	configured, configuredOK := parseGUID(entraTenant(cfg.Authority))
	if !ok || !configuredOK || tenant != configured {
		return errors.New("the Entra tenant claim does not match the configured tenant")
	}
	if _, ok := parseGUID(c.ObjectID); !ok {
		return errors.New("the Entra object-id claim is invalid")
	}
	if len(c.Audience) > 1 && c.AuthorizedParty != cfg.ClientID {
		return errors.New("the Entra authorized-party claim does not match the client")
	}
	return nil
}

// entraTenant is the tenant segment of an Entra authority,
// https://login.microsoftonline.com/<tenant>/v2.0, or "" for any other
// shape.
func entraTenant(authority string) string {
	rest, ok := strings.CutPrefix(authority, "https://login.microsoftonline.com/")
	if !ok {
		return ""
	}
	tenant, ok := strings.CutSuffix(rest, "/v2.0")
	if !ok || strings.Contains(tenant, "/") {
		return ""
	}
	return tenant
}

// parseGUID is .NET's Guid.TryParse for the forms a GUID claim is written
// in: 32 hex digits ("N"), hyphenated ("D"), and "D" in braces ("B") or
// parentheses ("P"), surrounding whitespace ignored. uuid.Parse alone would
// also take a "urn:uuid:" prefix, which .NET does not.
func parseGUID(s string) (uuid.UUID, bool) {
	s = strings.TrimSpace(s)
	if len(s) == 38 && (s[0] == '{' && s[37] == '}' || s[0] == '(' && s[37] == ')') {
		s = s[1:37]
	}
	if len(s) != 32 && len(s) != 36 {
		return uuid.Nil, false
	}
	u, err := uuid.Parse(s)
	return u, err == nil
}

// checkGoogleClaims is .NET's Google policy (EA/StaticOidcClaimValidation.cs:51-67):
// the email is verified and valid, the hosted domain (hd, trimmed,
// trailing dots dropped, lowercased) is present and is the email's domain,
// and it is one of the allowed domains, compared ordinally. It is the only
// domain restriction there is. The allowed domains are compared in .NET's
// normalized form, lowercased without trailing dots
// (CFG/WorkforceOidcOptions.cs:183-199), whatever config kept.
func checkGoogleClaims(cfg *config.OIDCConfig, c oidcClaims) error {
	if !c.EmailVerified {
		return errors.New("the Google email address is not verified")
	}
	domain, ok := googleEmailDomain(c.Email)
	if !ok {
		return errors.New("the Google email address is invalid")
	}
	hosted := strings.ToLower(strings.TrimRight(strings.TrimSpace(c.HostedDomain), "."))
	if hosted == "" || hosted != domain {
		return errors.New("the Google hosted domain does not match the email domain")
	}
	if !slices.ContainsFunc(cfg.AllowedDomains, func(d string) bool {
		return strings.ToLower(strings.TrimRight(strings.TrimSpace(d), ".")) == hosted
	}) {
		return errors.New("the Google hosted domain is not allowed")
	}
	return nil
}

// googleEmailDomain is .NET's IsValidEmail for the Google policy
// (EA/StaticOidcClaimValidation.cs:69-88): at most 256 characters, no
// whitespace or control character, exactly one "@", a well-formed address
// exactly as written, and a domain (trailing dots dropped, lowercased) with
// a dot in it. It returns that domain. net/mail stands in for .NET's
// MailAddress.
func googleEmailDomain(v string) (string, bool) {
	if strings.TrimSpace(v) == "" || hasWhitespace(v) || strings.ContainsFunc(v, unicode.IsControl) ||
		strings.Count(v, "@") != 1 || utf16Length(v) > 256 {
		return "", false
	}
	addr, err := mail.ParseAddress(v)
	if err != nil || addr.Name != "" || addr.Address != v {
		return "", false
	}
	at := strings.LastIndex(v, "@")
	domain := strings.ToLower(strings.TrimRight(v[at+1:], "."))
	return domain, at > 0 && domain != "" && strings.Contains(domain, ".")
}

// externalAccount is a validated federated identity as completion uses it
// (.NET's ExternalIdentity, EA/WorkforceOidcEndpoints.cs:386-391).
type externalAccount struct {
	issuer        string
	subject       string
	email         string // "" when the provider sent no usable one
	emailVerified bool
	displayName   string
}

// maxSubjectLength is the longest subject .NET linked
// (EA/WorkforceOidcEndpoints.cs:262), and the oidc_links check's bound.
const maxSubjectLength = 512

// readExternalAccount is .NET's ReadIdentity (EA/WorkforceOidcEndpoints.cs:253-278):
// the stamped issuer, normalized, must be the configured one, and the
// subject must be non-empty, without whitespace and at most 512
// characters; it is kept exactly, case included. The email and display
// name are read leniently: a claim that does not qualify is dropped, not
// fatal.
func readExternalAccount(c oidcClaims, configuredIssuer string) (externalAccount, bool) {
	issuer, ok := normalizeIssuer(c.Issuer)
	if !ok || issuer != configuredIssuer || c.Subject == "" || hasWhitespace(c.Subject) ||
		utf16Length(c.Subject) > maxSubjectLength {
		return externalAccount{}, false
	}
	return externalAccount{
		issuer:        issuer,
		subject:       c.Subject,
		email:         readEmail(c.Email),
		emailVerified: bool(c.EmailVerified),
		displayName:   readDisplayName(c),
	}, true
}

// readEmail is .NET's ReadEmail (EA/WorkforceOidcEndpoints.cs:280-290): the
// email claim when it is at most 256 characters with no whitespace or
// control character and exactly one "@" that is neither first nor last,
// else "".
func readEmail(v string) string {
	if v == "" || utf16Length(v) > 256 || hasWhitespace(v) || strings.ContainsFunc(v, unicode.IsControl) ||
		strings.Count(v, "@") != 1 {
		return ""
	}
	at := strings.IndexByte(v, '@')
	if at <= 0 || at >= len(v)-1 {
		return ""
	}
	return v
}

// maxOIDCDisplayNameLength is the longest display name an account gets
// (EA/WorkforceOidcEndpoints.cs:312-321).
const maxOIDCDisplayNameLength = 200

// readDisplayName is .NET's ReadDisplayName (EA/WorkforceOidcEndpoints.cs:292-310):
// name, else given_name and family_name joined, else preferred_username,
// each cleaned; else the subject, cut to 200 characters.
func readDisplayName(c oidcClaims) string {
	if name := cleanDisplayName(c.Name); name != "" {
		return name
	}
	var parts []string
	for _, part := range []string{cleanDisplayName(c.GivenName), cleanDisplayName(c.FamilyName)} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if combined := cleanDisplayName(strings.Join(parts, " ")); combined != "" {
		return combined
	}
	if username := cleanDisplayName(c.PreferredUsername); username != "" {
		return username
	}
	return truncateUTF16(c.Subject, maxOIDCDisplayNameLength)
}

// cleanDisplayName is .NET's CleanDisplayName (EA/WorkforceOidcEndpoints.cs:312-321):
// "" for a blank value or one with a control character; otherwise every
// run of whitespace collapsed to one space, and "" again when that is over
// 200 characters.
func cleanDisplayName(v string) string {
	if strings.TrimSpace(v) == "" || strings.ContainsFunc(v, unicode.IsControl) {
		return ""
	}
	normalized := strings.Join(strings.Fields(v), " ")
	if normalized == "" || utf16Length(normalized) > maxOIDCDisplayNameLength {
		return ""
	}
	return normalized
}

// truncateUTF16 is s cut to at most n UTF-16 units, as .NET's s[..n], but
// never inside a surrogate pair.
func truncateUTF16(s string, n int) string {
	units := 0
	for i, r := range s {
		units += utf16.RuneLen(r)
		if units > n {
			return s[:i]
		}
	}
	return s
}

// opaqueEmail is the reserved address an account whose provider sent no
// email gets, .NET's CreateOpaqueEmail (CFG/WorkforceOidcOptions.cs:172-176):
// "oidc-" and the hex SHA-256 of the issuer, a NUL and the subject, at
// sso.invalid. It is deterministic, never deliverable, and never used to
// link anything; responses show it as null (publicEmail).
func opaqueEmail(issuer, subject string) string {
	sum := sha256.Sum256(bytes.Join([][]byte{[]byte(issuer), []byte(subject)}, []byte{0}))
	return "oidc-" + hex.EncodeToString(sum[:]) + "@" + opaqueEmailDomain
}

// hasWhitespace reports whether s contains any whitespace character
// (.NET's char.IsWhiteSpace).
func hasWhitespace(s string) bool {
	return strings.ContainsFunc(s, unicode.IsSpace)
}
