// Package config loads and validates the process configuration from
// environment variables. It is parsed once at startup and reports every
// problem at once, so a misconfigured container fails its first boot with the
// complete list instead of one restart per mistake.
//
// Add new settings here, never by reading os.Getenv at a call site.
package config

import (
	"fmt"
	"log/slog"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

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

	c.DatabaseURL = databaseURL(&p, env, "DATABASE_URL", true)
	c.MigrationsDatabaseURL = databaseURL(&p, env, "MIGRATIONS_DATABASE_URL", false)
	migrationsURLSet := c.MigrationsDatabaseURL != ""
	if !migrationsURLSet {
		c.MigrationsDatabaseURL = c.DatabaseURL
	}

	c.AppOrigin, c.AppHostname = appOrigin(&p, env)
	c.BasePath = basePath(&p, env)
	c.Port = integer(&p, env, "PORT", 8080, 1, 65535)
	c.TrustedProxyHops = integer(&p, env, "TRUSTED_PROXY_HOPS", 0, 0, 10)
	c.ShutdownTimeout = duration(&p, env, "SHUTDOWN_TIMEOUT", 30*time.Second)
	c.LogLevel = logLevel(&p, env)
	c.Branding = branding(&p, env)

	if c.EnforcesTransportSecurity() {
		if c.AppOrigin != "" && !strings.HasPrefix(c.AppOrigin, "https://") {
			p.add("APP_URL", "must use https outside development; set ALLOW_INSECURE_TRANSPORT=1 to knowingly accept plaintext (local and evaluation use only)")
		}
		if c.DatabaseURL != "" {
			requireVerifiedTLS(&p, env, "DATABASE_URL", c.DatabaseURL)
		}
		if migrationsURLSet {
			requireVerifiedTLS(&p, env, "MIGRATIONS_DATABASE_URL", c.MigrationsDatabaseURL)
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

func databaseURL(p *problems, env map[string]string, field string, required bool) string {
	v := env[field]
	if v == "" {
		if required {
			p.add(field, "is required")
		}
		return ""
	}
	// The parser's own error is not echoed: it can quote the connection string.
	if _, err := pgconn.ParseConfig(v); err != nil {
		p.add(field, "is not a valid PostgreSQL connection string")
		return ""
	}
	return v
}

// requireVerifiedTLS rejects any sslmode that does not authenticate the
// server. libpq's default, "prefer", silently falls back to plaintext and
// never checks a certificate even when TLS is used.
func requireVerifiedTLS(p *problems, env map[string]string, field, dsn string) {
	switch mode := sslMode(dsn, env["PGSSLMODE"]); mode {
	case "verify-full", "verify-ca":
	default:
		p.add(field, "must require certificate-verified TLS outside development (sslmode=verify-full, or verify-ca when the server certificate does not name the host); %q does not authenticate the server. Set ALLOW_INSECURE_TRANSPORT=1 to knowingly accept an unauthenticated database connection (local and evaluation use only)", mode)
	}
}

// sslMode extracts the sslmode a connection string requests, in URL
// (postgres://…?sslmode=…) or keyword/value (host=… sslmode=…) form, falling
// back to PGSSLMODE and then libpq's default. Anything unrecognised reads as
// the default, which the caller rejects: the parser fails closed.
func sslMode(dsn, pgsslmode string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		if u, err := url.Parse(dsn); err == nil {
			if m := u.Query().Get("sslmode"); m != "" {
				return strings.ToLower(m)
			}
		}
	} else {
		for _, field := range strings.Fields(dsn) {
			if k, v, ok := strings.Cut(field, "="); ok && k == "sslmode" {
				return strings.ToLower(strings.Trim(v, "'"))
			}
		}
	}
	if pgsslmode != "" {
		return strings.ToLower(pgsslmode)
	}
	return "prefer"
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
