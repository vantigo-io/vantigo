// Package config loads and validates the process configuration from
// environment variables. It is parsed once at startup and reports every
// problem at once, so a misconfigured container fails its first boot with the
// complete list instead of one restart per mistake.
//
// Add new settings here, never by reading os.Getenv at a call site.
package config

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgconn"
)

// Env is the deployment environment. Only development relaxes anything.
type Env string

const (
	Production  Env = "production"
	Development Env = "development"
)

// Branding is the whitelabeling the SPA index document is templated with.
type Branding struct {
	Title        string
	LogoURL      string
	SupportEmail string
	SupportPhone string
	SupportURL   string
}

// SessionConfig bounds the application cookie session. The idle timeout is
// what the cookie itself enforces; the absolute lifetime is enforced on every
// validation so an actively used session cannot renew indefinitely.
// Privileged sessions (Owner, SystemAdmin) get the tighter pair of bounds.
type SessionConfig struct {
	Idle               time.Duration
	PrivilegedIdle     time.Duration
	Absolute           time.Duration
	PrivilegedAbsolute time.Duration
}

// MailConfig is where application mail (invitations, password resets) is
// sent. Driver "log" writes mail to the log instead of sending it, and is
// rejected outside development because those mails carry bearer links.
type MailConfig struct {
	Driver   string // "smtp" | "log"
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLS      string // "implicit" | "starttls" | "none"
}

// OIDCConfig is the one startup-bound workforce OpenID Connect provider. A
// nil *OIDCConfig on Config means OIDC is disabled.
type OIDCConfig struct {
	Provider          string // "entra" | "google"
	Authority         string
	ClientID          string
	ClientSecret      string
	WorkloadTokenFile string
	AllowedDomains    []string
	DisplayName       string
}

// SCIMConfig is the static bearer credential for the SCIM endpoint. A nil
// *SCIMConfig on Config means SCIM is disabled. PreviousToken lets a token
// rotation overlap briefly instead of breaking every in-flight client at once.
type SCIMConfig struct {
	Token                  string
	PreviousToken          string
	PreviousTokenExpiresAt time.Time
}

// Config is the validated process configuration. Load populates every field;
// there is no partial state.
type Config struct {
	Env Env
	// DatabaseURL is the runtime connection (the least-privilege role).
	DatabaseURL string
	// MigrationsDatabaseURL is the connection `migrate` uses: the owner role
	// where a deployment separates the two, otherwise DatabaseURL.
	MigrationsDatabaseURL string
	// AppOrigin is APP_URL as scheme://host[:port], lower-case, no trailing slash.
	AppOrigin string
	// AppHostname is AppOrigin's host without port or IPv6 brackets.
	AppHostname string
	// BasePath is "" at the domain root, otherwise "/prefix" with no trailing slash.
	BasePath               string
	Port                   int
	TrustedProxyHops       int
	AllowInsecureTransport bool
	CSPReportOnly          bool
	ShutdownTimeout        time.Duration
	LogLevel               slog.Level
	Branding               Branding

	// AppSecret is the process-wide key material (CSRF tokens, cookie
	// signing, encryption of TOTP secrets). At least 32 bytes.
	AppSecret []byte
	// BootstrapSecret authenticates the one-time bootstrap endpoint that
	// creates the first Owner. Required outside development; never
	// generated or logged there, unlike development's own fallback.
	BootstrapSecret string
	// SystemAdminEmail, when it matches a bootstrapped Owner's email
	// case-insensitively, also grants that account SystemAdmin.
	SystemAdminEmail string
	Sessions         SessionConfig

	OwnersRequireMFA         bool
	OwnersAllowInsecureNoMFA bool
	MFAIssuer                string

	InvitationLifetime  time.Duration
	InvitationAcceptURL string
	PasswordResetURL    string

	Mail MailConfig
	OIDC *OIDCConfig
	SCIM *SCIMConfig

	TrustedProxyCIDRs []netip.Prefix
}

// IsDevelopment reports whether APP_ENV=development.
func (c *Config) IsDevelopment() bool { return c.Env == Development }

// EnforcesTransportSecurity reports whether the fail-closed transport rules
// apply: always outside development, unless the operator knowingly set
// ALLOW_INSECURE_TRANSPORT=1.
func (c *Config) EnforcesTransportSecurity() bool {
	return c.Env == Production && !c.AllowInsecureTransport
}

// FromOS loads configuration from the process environment.
func FromOS() (*Config, error) {
	env := make(map[string]string)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return Load(env)
}

// Load parses and validates configuration from a plain map, the shape both a
// real environment and a test fixture share. It reports every problem in one
// error.
func Load(env map[string]string) (*Config, error) {
	var p problems
	c := &Config{}

	switch env["APP_ENV"] {
	case "", string(Production):
		c.Env = Production
	case string(Development):
		c.Env = Development
	default:
		p.add("APP_ENV", `must be "production" or "development"`)
		c.Env = Production
	}

	c.AllowInsecureTransport = flag(&p, env, "ALLOW_INSECURE_TRANSPORT")
	c.CSPReportOnly = flag(&p, env, "CSP_REPORT_ONLY")

	var dbConn, migrationsConn *pgconn.Config
	c.DatabaseURL, dbConn = databaseURL(&p, env, "DATABASE_URL", true)
	c.MigrationsDatabaseURL, migrationsConn = databaseURL(&p, env, "MIGRATIONS_DATABASE_URL", false)
	if c.MigrationsDatabaseURL == "" {
		c.MigrationsDatabaseURL = c.DatabaseURL
	}

	c.AppOrigin, c.AppHostname = appOrigin(&p, env)
	c.BasePath = basePath(&p, env)
	c.Port = integer(&p, env, "PORT", 8080, 1, 65535)
	c.TrustedProxyHops = integer(&p, env, "TRUSTED_PROXY_HOPS", 0, 0, 10)
	c.ShutdownTimeout = duration(&p, env, "SHUTDOWN_TIMEOUT", 30*time.Second)
	c.LogLevel = logLevel(&p, env)
	c.Branding = branding(&p, env)

	c.AppSecret = appSecret(&p, env)
	c.BootstrapSecret = bootstrapSecret(&p, env, c)
	c.SystemAdminEmail = strings.TrimSpace(env["SYSTEM_ADMIN_EMAIL"])
	c.Sessions = sessions(&p, env)

	c.OwnersRequireMFA = ownersRequireMFA(&p, env, c)
	c.OwnersAllowInsecureNoMFA = flag(&p, env, "OWNERS_ALLOW_INSECURE_NO_MFA")
	c.MFAIssuer = env["MFA_ISSUER"]
	if c.MFAIssuer == "" {
		c.MFAIssuer = "Vantigo"
	}
	if !c.IsDevelopment() && !c.OwnersRequireMFA && !c.OwnersAllowInsecureNoMFA {
		p.add("OWNERS_REQUIRE_MFA", "must not be 0 outside development unless OWNERS_ALLOW_INSECURE_NO_MFA=1 (accepts that a compromised Owner or SystemAdmin password alone reaches every identity and tenant control-plane endpoint)")
	}

	c.InvitationLifetime = boundedDuration(&p, env, "INVITATION_LIFETIME", 168*time.Hour, 24*time.Hour, 720*time.Hour)
	c.InvitationAcceptURL = templatedURL(&p, env, "INVITATION_ACCEPT_URL", c.AppOrigin+c.BasePath+"/invitations/accept?token={token}", "{token}")
	c.PasswordResetURL = templatedURL(&p, env, "PASSWORD_RESET_URL", c.AppOrigin+c.BasePath+"/password-reset?token={token}&email={email}", "{token}", "{email}")

	c.Mail = mailConfig(&p, env, c)
	c.OIDC = oidc(&p, env)
	c.SCIM = scim(&p, env)

	c.TrustedProxyCIDRs = prefixes(&p, env, "TRUSTED_PROXY_CIDRS")

	if c.EnforcesTransportSecurity() {
		if c.AppOrigin != "" && !strings.HasPrefix(c.AppOrigin, "https://") {
			p.add("APP_URL", "must use https outside development; set ALLOW_INSECURE_TRANSPORT=1 to knowingly accept plaintext (local and evaluation use only)")
		}
		if dbConn != nil {
			requireVerifiedTLS(&p, "DATABASE_URL", dbConn)
		}
		if migrationsConn != nil {
			requireVerifiedTLS(&p, "MIGRATIONS_DATABASE_URL", migrationsConn)
		}
	}

	if len(p) > 0 {
		return nil, fmt.Errorf("invalid configuration:\n  %s", strings.Join(p, "\n  "))
	}
	return c, nil
}

// NormalizeBasePath returns raw as "/prefix" (leading slash, no trailing
// slash), or "" when it is empty or "/" — serve at the domain root. Mirrors
// the .NET AppBasePathOptions.Normalized.
func NormalizeBasePath(raw string) string {
	v := strings.Trim(strings.TrimSpace(raw), "/")
	if v == "" {
		return ""
	}
	return "/" + v
}

// problems accumulates every validation failure instead of stopping at the first.
type problems []string

func (p *problems) add(field, format string, args ...any) {
	*p = append(*p, field+": "+fmt.Sprintf(format, args...))
}

// databaseURL returns the connection string together with pgx's own parse of
// it, so the transport check judges exactly what pgx will connect with. Like
// pgx, the parse reads PG* variables (PGSSLMODE, a service file) from the
// process environment, which in production is the env Load was given.
func databaseURL(p *problems, env map[string]string, field string, required bool) (string, *pgconn.Config) {
	v := env[field]
	if v == "" {
		if required {
			p.add(field, "is required")
		}
		return "", nil
	}
	// The parser's own error is not echoed: it can quote the connection string.
	cfg, err := pgconn.ParseConfig(v)
	if err != nil {
		p.add(field, "is not a valid PostgreSQL connection string")
		return "", nil
	}
	return v, cfg
}

// requireVerifiedTLS rejects a connection that would not authenticate the
// server, judged on pgx's parsed configuration rather than a re-parse of the
// string (pgx takes the last of duplicate settings and honours quoting).
// Every fallback must pass too: libpq's default, "prefer", and "allow" add a
// plaintext fallback, and neither checks a certificate even over TLS. The
// message never quotes the connection string: it can carry a password.
func requireVerifiedTLS(p *problems, field string, cfg *pgconn.Config) {
	verified := verifiesServer(cfg.TLSConfig)
	for _, fb := range cfg.Fallbacks {
		verified = verified && verifiesServer(fb.TLSConfig)
	}
	if !verified {
		p.add(field, "must require certificate-verified TLS outside development (sslmode=verify-full, or verify-ca when the server certificate does not name the host); this connection string would not authenticate the server. Set ALLOW_INSECURE_TRANSPORT=1 to knowingly accept an unauthenticated database connection (local and evaluation use only)")
	}
}

// verifiesServer reports whether a pgx TLS configuration authenticates the
// server: verify-full keeps Go's own verification, verify-ca skips it but
// installs a chain verifier, and require (or no TLS at all) does neither.
func verifiesServer(c *tls.Config) bool {
	return c != nil && (!c.InsecureSkipVerify || c.VerifyPeerCertificate != nil)
}

func appOrigin(p *problems, env map[string]string) (origin, hostname string) {
	v := env["APP_URL"]
	if v == "" {
		p.add("APP_URL", "is required")
		return "", ""
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		p.add("APP_URL", "must be an absolute http or https URL")
		return "", ""
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		p.add("APP_URL", "must be an origin only (scheme, host and optional port); put a path prefix in APP_BASE_PATH")
		return "", ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), strings.ToLower(u.Hostname())
}

func basePath(p *problems, env map[string]string) string {
	v := NormalizeBasePath(env["APP_BASE_PATH"])
	if v == "" {
		return ""
	}
	for _, seg := range strings.Split(v[1:], "/") {
		if seg == "" || seg == "." || seg == ".." || strings.IndexFunc(seg, invalidPathRune) >= 0 {
			p.add("APP_BASE_PATH", "must be a path like /vantigo made of letters, digits, '.', '_', '~' and '-'")
			return ""
		}
	}
	return v
}

func invalidPathRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	case r == '.' || r == '_' || r == '~' || r == '-':
		return false
	}
	return true
}

func integer(p *problems, env map[string]string, field string, def, min, max int) int {
	v := env[field]
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		p.add(field, "must be an integer from %d to %d", min, max)
		return def
	}
	return n
}

func duration(p *problems, env map[string]string, field string, def time.Duration) time.Duration {
	v := env[field]
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		p.add(field, "must be a positive duration such as 30s")
		return def
	}
	return d
}

// flag reads a strict "0"/"1" switch. Anything else is rejected rather than
// guessed at: the fail-safe default stays off.
func flag(p *problems, env map[string]string, field string) bool {
	switch env[field] {
	case "", "0":
		return false
	case "1":
		return true
	default:
		p.add(field, `must be "0" or "1"`)
		return false
	}
}

func logLevel(p *problems, env map[string]string) slog.Level {
	switch strings.ToLower(env["LOG_LEVEL"]) {
	case "", "info":
		return slog.LevelInfo
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		p.add("LOG_LEVEL", `must be one of "debug", "info", "warn", "error"`)
		return slog.LevelInfo
	}
}

func branding(p *problems, env map[string]string) Branding {
	b := Branding{
		Title:        strings.TrimSpace(env["APP_TITLE"]),
		SupportPhone: strings.TrimSpace(env["APP_SUPPORT_PHONE"]),
		LogoURL:      linkField(p, env, "APP_LOGO_URL"),
		SupportURL:   linkField(p, env, "APP_SUPPORT_URL"),
	}
	if v := strings.TrimSpace(env["APP_SUPPORT_EMAIL"]); v != "" {
		if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
			p.add("APP_SUPPORT_EMAIL", "must be a plain email address such as support@example.com")
		} else {
			b.SupportEmail = v
		}
	}
	return b
}

// linkField accepts an absolute http(s) URL or a same-origin path. Anything
// else (javascript:, protocol-relative //host) would end up in an href or src
// the SPA renders.
func linkField(p *problems, env map[string]string, field string) string {
	v := strings.TrimSpace(env[field])
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") {
		return v
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		p.add(field, "must be an absolute http or https URL or a path starting with /")
		return ""
	}
	return v
}

// appSecret reads the process-wide key material. Its value is never included
// in a problem message: only its length is ever reported.
func appSecret(p *problems, env map[string]string) []byte {
	v := env["APP_SECRET"]
	if len(v) < 32 {
		p.add("APP_SECRET", "must be at least 32 bytes")
		return nil
	}
	return []byte(v)
}

// bootstrapSecret is required outside development. A development-only
// fallback (generated and logged) is the caller's concern, not config's.
func bootstrapSecret(p *problems, env map[string]string, c *Config) string {
	v := env["BOOTSTRAP_SECRET"]
	if v == "" && !c.IsDevelopment() {
		p.add("BOOTSTRAP_SECRET", "is required outside development")
	}
	return v
}

// sessions parses the four session bounds and rejects a privileged bound
// looser than its standard counterpart, which would defeat the point of
// having a tighter pair for Owner and SystemAdmin sessions.
func sessions(p *problems, env map[string]string) SessionConfig {
	s := SessionConfig{
		Idle:               duration(p, env, "SESSION_IDLE_TIMEOUT", 8*time.Hour),
		PrivilegedIdle:     duration(p, env, "SESSION_PRIVILEGED_IDLE_TIMEOUT", 2*time.Hour),
		Absolute:           duration(p, env, "SESSION_ABSOLUTE_LIFETIME", 24*time.Hour),
		PrivilegedAbsolute: duration(p, env, "SESSION_PRIVILEGED_ABSOLUTE_LIFETIME", 8*time.Hour),
	}
	if s.PrivilegedIdle > s.Idle {
		p.add("SESSION_PRIVILEGED_IDLE_TIMEOUT", "must not exceed SESSION_IDLE_TIMEOUT")
	}
	if s.PrivilegedAbsolute > s.Absolute {
		p.add("SESSION_PRIVILEGED_ABSOLUTE_LIFETIME", "must not exceed SESSION_ABSOLUTE_LIFETIME")
	}
	return s
}

// ownersRequireMFA defaults to on in production and off in development: a
// compromised Owner or SystemAdmin password alone must not reach the control
// plane unless the operator has knowingly accepted that with
// OWNERS_ALLOW_INSECURE_NO_MFA (checked once both flags are parsed).
func ownersRequireMFA(p *problems, env map[string]string, c *Config) bool {
	def := !c.IsDevelopment()
	switch env["OWNERS_REQUIRE_MFA"] {
	case "":
		return def
	case "0":
		return false
	case "1":
		return true
	default:
		p.add("OWNERS_REQUIRE_MFA", `must be "0" or "1"`)
		return def
	}
}

// boundedDuration is duration plus a [min,max] range. A syntactically valid
// duration outside the range is its own problem, reported once: duration
// already reports an unparsable or non-positive value.
func boundedDuration(p *problems, env map[string]string, field string, def, min, max time.Duration) time.Duration {
	d := duration(p, env, field, def)
	if d < min || d > max {
		p.add(field, "must be from %s to %s", min, max)
		return def
	}
	return d
}

// templatedURL falls back to def (typically derived from APP_URL) when unset,
// mirroring InvitationTokenService's same-origin default. Whichever URL is
// effective, every placeholder (such as "{token}") must appear in it
// literally, so the caller can substitute it safely.
func templatedURL(p *problems, env map[string]string, field, def string, placeholders ...string) string {
	v := env[field]
	if v == "" {
		v = def
	}
	for _, ph := range placeholders {
		if !strings.Contains(v, ph) {
			p.add(field, "must contain the %s placeholder", ph)
			return def
		}
	}
	return v
}

// mailConfig resolves the application mail driver. The "log" driver is a
// development convenience: invitation and password-reset mails carry bearer
// links, so writing them to the log anywhere else is a leak, not a feature.
func mailConfig(p *problems, env map[string]string, c *Config) MailConfig {
	var m MailConfig
	switch env["MAIL_DRIVER"] {
	case "":
		if c.IsDevelopment() {
			m.Driver = "log"
		} else {
			m.Driver = "smtp"
		}
	case "smtp":
		m.Driver = "smtp"
	case "log":
		if !c.IsDevelopment() {
			p.add("MAIL_DRIVER", `must not be "log" outside development`)
		}
		m.Driver = "log"
	default:
		p.add("MAIL_DRIVER", `must be "smtp" or "log"`)
		m.Driver = "smtp"
	}

	m.Username = env["SMTP_USERNAME"]
	m.Password = env["SMTP_PASSWORD"]
	m.Port = integer(p, env, "SMTP_PORT", 587, 1, 65535)

	if m.Driver == "smtp" {
		if m.Host = env["SMTP_HOST"]; m.Host == "" {
			p.add("SMTP_HOST", "is required")
		}
		if v := env["SMTP_FROM"]; v == "" {
			p.add("SMTP_FROM", "is required")
		} else if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
			p.add("SMTP_FROM", "must be a plain email address such as noreply@example.com")
		} else {
			m.From = v
		}
	}

	switch env["SMTP_TLS"] {
	case "":
		m.TLS = "starttls"
	case "implicit", "starttls":
		m.TLS = env["SMTP_TLS"]
	case "none":
		m.TLS = "none"
		if c.EnforcesTransportSecurity() {
			p.add("SMTP_TLS", `"none" requires ALLOW_INSECURE_TRANSPORT=1 outside development`)
		}
	default:
		p.add("SMTP_TLS", `must be "implicit", "starttls" or "none"`)
		m.TLS = "starttls"
	}

	return m
}

// entraAuthorityPattern and guidPattern mirror
// WorkforceOidcOptions.TryValidateEntraAuthority and its ClientId check.
var (
	entraAuthorityPattern = regexp.MustCompile(`^https://login\.microsoftonline\.com/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})/v2\.0$`)
	guidPattern           = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// oidc resolves the one workforce OpenID Connect provider. OIDC_PROVIDER is
// the on/off switch: empty disables it (a nil *OIDCConfig), set enables it
// and every other OIDC_* setting is then validated for that provider.
// Mirrors WorkforceOidcOptions.FromAuthenticationOptions.
func oidc(p *problems, env map[string]string) *OIDCConfig {
	provider := env["OIDC_PROVIDER"]
	if provider == "" {
		return nil
	}
	if provider != "entra" && provider != "google" {
		p.add("OIDC_PROVIDER", `must be "entra" or "google"`)
		return nil
	}
	o := &OIDCConfig{Provider: provider}

	o.Authority = env["OIDC_AUTHORITY"]
	switch provider {
	case "entra":
		if !entraAuthorityPattern.MatchString(o.Authority) {
			p.add("OIDC_AUTHORITY", "must be exactly https://login.microsoftonline.com/<tenant-guid>/v2.0")
		}
	case "google":
		if o.Authority != "https://accounts.google.com" {
			p.add("OIDC_AUTHORITY", "must be exactly https://accounts.google.com")
		}
	}

	o.ClientID = env["OIDC_CLIENT_ID"]
	switch provider {
	case "entra":
		if !guidPattern.MatchString(o.ClientID) {
			p.add("OIDC_CLIENT_ID", "must be a GUID for the entra provider")
		}
	case "google":
		if !strings.HasSuffix(o.ClientID, ".apps.googleusercontent.com") {
			p.add("OIDC_CLIENT_ID", "must end with .apps.googleusercontent.com for the google provider")
		}
	}

	secret := env["OIDC_CLIENT_SECRET"]
	tokenFile := env["OIDC_WORKLOAD_IDENTITY_TOKEN_FILE"]
	if tokenFile == "" {
		tokenFile = env["AZURE_FEDERATED_TOKEN_FILE"]
	}
	switch {
	case secret != "" && tokenFile != "":
		p.add("OIDC_CLIENT_SECRET", "and OIDC_WORKLOAD_IDENTITY_TOKEN_FILE are mutually exclusive")
	case secret == "" && tokenFile == "":
		p.add("OIDC_CLIENT_SECRET", "or OIDC_WORKLOAD_IDENTITY_TOKEN_FILE is required")
	case secret != "":
		o.ClientSecret = secret
	case provider != "entra":
		p.add("OIDC_WORKLOAD_IDENTITY_TOKEN_FILE", "is supported only for the entra provider")
	case !filepath.IsAbs(tokenFile):
		p.add("OIDC_WORKLOAD_IDENTITY_TOKEN_FILE", "must be an absolute path")
	default:
		o.WorkloadTokenFile = tokenFile
	}

	o.AllowedDomains = allowedDomains(env["OIDC_ALLOWED_DOMAINS"])
	switch provider {
	case "entra":
		if len(o.AllowedDomains) > 0 {
			p.add("OIDC_ALLOWED_DOMAINS", "is only valid for the google provider")
		}
	case "google":
		if len(o.AllowedDomains) == 0 {
			p.add("OIDC_ALLOWED_DOMAINS", "must list at least one domain for the google provider")
		}
	}

	if o.DisplayName = env["OIDC_DISPLAY_NAME"]; o.DisplayName == "" {
		o.DisplayName = "Workforce SSO"
	}

	return o
}

// allowedDomains splits a comma list of bare DNS names, trimming and
// lower-casing each one.
func allowedDomains(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, raw := range strings.Split(v, ",") {
		if d := strings.ToLower(strings.TrimSpace(raw)); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// scim resolves the static SCIM bearer credential. SCIM_TOKEN is the on/off
// switch: empty disables it (a nil *SCIMConfig). Mirrors
// StaticScimOptions.FromAuthenticationOptions, minus the file-based secret
// sources (not needed by this deployment shape).
func scim(p *problems, env map[string]string) *SCIMConfig {
	token := env["SCIM_TOKEN"]
	if token == "" {
		return nil
	}
	if containsWhitespace(token) {
		p.add("SCIM_TOKEN", "must not contain whitespace")
		return nil
	}
	s := &SCIMConfig{Token: token}

	previous := env["SCIM_PREVIOUS_TOKEN"]
	expiresRaw := env["SCIM_PREVIOUS_TOKEN_EXPIRES_AT"]
	if previous == "" {
		if expiresRaw != "" {
			p.add("SCIM_PREVIOUS_TOKEN_EXPIRES_AT", "requires SCIM_PREVIOUS_TOKEN")
		}
		return s
	}

	switch {
	case containsWhitespace(previous):
		p.add("SCIM_PREVIOUS_TOKEN", "must not contain whitespace")
	case previous == token:
		p.add("SCIM_PREVIOUS_TOKEN", "must differ from SCIM_TOKEN")
	default:
		s.PreviousToken = previous
	}

	switch expiresRaw {
	case "":
		p.add("SCIM_PREVIOUS_TOKEN_EXPIRES_AT", "is required when SCIM_PREVIOUS_TOKEN is set")
	default:
		t, err := time.Parse(time.RFC3339, expiresRaw)
		switch {
		case err != nil:
			p.add("SCIM_PREVIOUS_TOKEN_EXPIRES_AT", "must be an RFC 3339 timestamp")
		case !t.After(time.Now()):
			p.add("SCIM_PREVIOUS_TOKEN_EXPIRES_AT", "must be in the future")
		case t.After(time.Now().Add(24 * time.Hour)):
			p.add("SCIM_PREVIOUS_TOKEN_EXPIRES_AT", "must be at most 24h from now")
		default:
			s.PreviousTokenExpiresAt = t
		}
	}

	return s
}

func containsWhitespace(v string) bool {
	return strings.IndexFunc(v, unicode.IsSpace) >= 0
}

// prefixes parses a comma list of CIDR prefixes. Empty means none.
func prefixes(p *problems, env map[string]string, field string) []netip.Prefix {
	v := env[field]
	if v == "" {
		return nil
	}
	var out []netip.Prefix
	for _, raw := range strings.Split(v, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		pfx, err := netip.ParsePrefix(raw)
		if err != nil {
			p.add(field, "must be a comma list of CIDR prefixes")
			return nil
		}
		out = append(out, pfx)
	}
	return out
}
