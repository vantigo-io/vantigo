package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://vantigo:secret@db.internal:5432/vantigo?sslmode=verify-full",
		"APP_URL":      "https://vantigo.example.com",
	}
}

// with returns a copy of env with the key/value pairs applied.
func with(env map[string]string, pairs ...string) map[string]string {
	out := make(map[string]string, len(env)+len(pairs)/2)
	for k, v := range env {
		out[k] = v
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		out[pairs[i]] = pairs[i+1]
	}
	return out
}

func mustLoad(t *testing.T, env map[string]string) *Config {
	t.Helper()
	cfg, err := Load(env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func loadError(t *testing.T, env map[string]string) string {
	t.Helper()
	_, err := Load(env)
	if err == nil {
		t.Fatal("Load succeeded, want an error")
	}
	return err.Error()
}

func TestLoad_MinimalProductionConfigGetsDefaults(t *testing.T) {
	cfg := mustLoad(t, validEnv())

	if cfg.Env != Production || cfg.IsDevelopment() {
		t.Errorf("Env = %q, want production", cfg.Env)
	}
	if !cfg.EnforcesTransportSecurity() {
		t.Error("EnforcesTransportSecurity = false, want true in production")
	}
	if cfg.MigrationsDatabaseURL != cfg.DatabaseURL {
		t.Errorf("MigrationsDatabaseURL = %q, want DATABASE_URL", cfg.MigrationsDatabaseURL)
	}
	if cfg.AppOrigin != "https://vantigo.example.com" || cfg.AppHostname != "vantigo.example.com" {
		t.Errorf("AppOrigin/AppHostname = %q/%q", cfg.AppOrigin, cfg.AppHostname)
	}
	if cfg.BasePath != "" || cfg.Port != 8080 || cfg.TrustedProxyHops != 0 {
		t.Errorf("BasePath/Port/TrustedProxyHops = %q/%d/%d", cfg.BasePath, cfg.Port, cfg.TrustedProxyHops)
	}
	if cfg.ShutdownTimeout != 30*time.Second || cfg.LogLevel != slog.LevelInfo {
		t.Errorf("ShutdownTimeout/LogLevel = %v/%v", cfg.ShutdownTimeout, cfg.LogLevel)
	}
	if cfg.AllowInsecureTransport || cfg.CSPReportOnly {
		t.Error("flags default to on, want off")
	}
}

func TestLoad_ReportsEveryProblemAtOnce(t *testing.T) {
	msg := loadError(t, map[string]string{
		"APP_URL":            "not a url",
		"PORT":               "0",
		"SHUTDOWN_TIMEOUT":   "soon",
		"TRUSTED_PROXY_HOPS": "-1",
	})

	if !strings.HasPrefix(msg, "invalid configuration:\n  ") {
		t.Errorf("error does not start with the heading: %q", msg)
	}
	for _, want := range []string{
		"DATABASE_URL: is required",
		"APP_URL: must be an absolute http or https URL",
		"PORT: must be an integer from 1 to 65535",
		"SHUTDOWN_TIMEOUT: must be a positive duration",
		"TRUSTED_PROXY_HOPS: must be an integer from 0 to 10",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error is missing %q:\n%s", want, msg)
		}
	}
}

func TestLoad_AppURL(t *testing.T) {
	accepted := []struct{ raw, origin, hostname string }{
		{"https://Vantigo.Example.com/", "https://vantigo.example.com", "vantigo.example.com"},
		{"https://vantigo.example.com:8443", "https://vantigo.example.com:8443", "vantigo.example.com"},
		{"https://[2001:db8::1]:8443", "https://[2001:db8::1]:8443", "2001:db8::1"},
	}
	for _, tc := range accepted {
		t.Run(tc.raw, func(t *testing.T) {
			cfg := mustLoad(t, with(validEnv(), "APP_URL", tc.raw))
			if cfg.AppOrigin != tc.origin || cfg.AppHostname != tc.hostname {
				t.Errorf("got %q/%q, want %q/%q", cfg.AppOrigin, cfg.AppHostname, tc.origin, tc.hostname)
			}
		})
	}

	rejected := []struct{ raw, want string }{
		{"ftp://vantigo.example.com", "APP_URL: must be an absolute http or https URL"},
		{"vantigo.example.com", "APP_URL: must be an absolute http or https URL"},
		{"https://vantigo.example.com/app", "APP_URL: must be an origin only"},
		{"https://user:pw@vantigo.example.com", "APP_URL: must be an origin only"},
		{"https://vantigo.example.com/?x=1", "APP_URL: must be an origin only"},
	}
	for _, tc := range rejected {
		t.Run(tc.raw, func(t *testing.T) {
			if msg := loadError(t, with(validEnv(), "APP_URL", tc.raw)); !strings.Contains(msg, tc.want) {
				t.Errorf("error %q does not contain %q", msg, tc.want)
			}
		})
	}
}

// unverified is the rejection every connection string that would not
// authenticate the database server gets.
const unverified = "DATABASE_URL: must require certificate-verified TLS outside development (sslmode=verify-full, or verify-ca when the server certificate does not name the host); this connection string would not authenticate the server"

func TestLoad_TransportRules(t *testing.T) {
	// pgconn reads PGSSLMODE from the process environment; a developer's
	// shell must not change what these cases mean.
	t.Setenv("PGSSLMODE", "")

	tests := []struct {
		name    string
		env     map[string]string
		wantErr string // empty: must load
	}{
		{"production requires https", with(validEnv(), "APP_URL", "http://vantigo.example.com"), "APP_URL: must use https"},
		{"sslmode=require does not authenticate the server", with(validEnv(), "DATABASE_URL", "postgres://v:s@db/v?sslmode=require"), unverified},
		{"keyword sslmode=require does not authenticate the server", with(validEnv(), "DATABASE_URL", "host=db user=v dbname=v sslmode=require"), unverified},
		{"no sslmode means libpq's prefer", with(validEnv(), "DATABASE_URL", "postgres://v:s@db/v"), unverified},
		{"sslmode=prefer falls back to plaintext", with(validEnv(), "DATABASE_URL", "postgres://v:s@db/v?sslmode=prefer"), unverified},
		{"sslmode=allow tries plaintext first", with(validEnv(), "DATABASE_URL", "postgres://v:s@db/v?sslmode=allow"), unverified},
		{"keyword form with verify-ca is accepted", with(validEnv(), "DATABASE_URL", "host=db user=v dbname=v sslmode=verify-ca"), ""},
		{"keyword form with verify-full is accepted", with(validEnv(), "DATABASE_URL", "host=db user=v dbname=v sslmode=verify-full"), ""},
		{"URL form with verify-ca is accepted", with(validEnv(), "DATABASE_URL", "postgres://v:s@db/v?sslmode=verify-ca"), ""},
		{"URL form with verify-full is accepted", with(validEnv(), "DATABASE_URL", "postgres://v:s@db/v?sslmode=verify-full"), ""},
		// pgx takes the last sslmode; judging the first would fail open.
		{"a later duplicate sslmode wins", with(validEnv(), "DATABASE_URL", "host=db user=v dbname=v sslmode=verify-full sslmode=disable"), unverified},
		// A quoted value is one token to pgx, not to a whitespace split.
		{"an sslmode inside a quoted value is not a setting", with(validEnv(), "DATABASE_URL", "host=db user=v dbname=v password='x sslmode=verify-full' sslmode=disable"), unverified},
		// Every host pgx may fall back to must be verified too.
		{"an unverified fallback host is rejected", with(validEnv(), "DATABASE_URL", "host=db,/var/run/postgresql user=v dbname=v sslmode=verify-full"), unverified},
		{"an insecure migrations URL is reported by name", with(validEnv(), "MIGRATIONS_DATABASE_URL", "postgres://o:s@db/v?sslmode=disable"), "MIGRATIONS_DATABASE_URL: must require certificate-verified TLS"},
		{"ALLOW_INSECURE_TRANSPORT accepts plaintext", with(validEnv(), "APP_URL", "http://localhost:8080", "DATABASE_URL", "postgres://v:s@db/v?sslmode=disable", "ALLOW_INSECURE_TRANSPORT", "1"), ""},
		{"development accepts plaintext", with(validEnv(), "APP_ENV", "development", "APP_URL", "http://localhost:8080", "DATABASE_URL", "postgres://v:s@db/v?sslmode=disable"), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(tc.env)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("Load: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// pgconn reads PGSSLMODE from the process environment, not from the map
// Load is given; in production the two are the same environment.
func TestLoad_PGSSLMODEFromTheProcessEnvironment(t *testing.T) {
	t.Setenv("PGSSLMODE", "verify-full")
	mustLoad(t, with(validEnv(), "DATABASE_URL", "postgres://v:s@db/v"))

	t.Setenv("PGSSLMODE", "disable")
	if msg := loadError(t, with(validEnv(), "DATABASE_URL", "postgres://v:s@db/v")); !strings.Contains(msg, unverified) {
		t.Errorf("error = %q, want %q", msg, unverified)
	}
}

func TestLoad_UnverifiedDatabaseURLNeverEchoesTheSecret(t *testing.T) {
	t.Setenv("PGSSLMODE", "")
	msg := loadError(t, with(validEnv(), "DATABASE_URL", "host=db user=v dbname=v password='hunter2 sslmode=verify-full' sslmode=disable"))

	if !strings.Contains(msg, unverified) {
		t.Errorf("error = %q, want %q", msg, unverified)
	}
	if strings.Contains(msg, "hunter2") {
		t.Errorf("error leaks the password: %q", msg)
	}
}

func TestLoad_InvalidDatabaseURLNeverEchoesTheSecret(t *testing.T) {
	msg := loadError(t, with(validEnv(), "DATABASE_URL", "postgres://vantigo:hunter2@db/v?sslmode=bogus"))

	if !strings.Contains(msg, "DATABASE_URL: is not a valid PostgreSQL connection string") {
		t.Errorf("error = %q", msg)
	}
	if strings.Contains(msg, "hunter2") {
		t.Errorf("error leaks the password: %q", msg)
	}
}

func TestNormalizeBasePath(t *testing.T) {
	for raw, want := range map[string]string{
		"":             "",
		"/":            "",
		"vantigo":      "/vantigo",
		"/vantigo/":    "/vantigo",
		"  /a/b/  ":    "/a/b",
		"/erp/vantigo": "/erp/vantigo",
	} {
		if got := NormalizeBasePath(raw); got != want {
			t.Errorf("NormalizeBasePath(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestLoad_BasePath(t *testing.T) {
	if got := mustLoad(t, with(validEnv(), "APP_BASE_PATH", "vantigo/")).BasePath; got != "/vantigo" {
		t.Errorf("BasePath = %q, want /vantigo", got)
	}
	for _, raw := range []string{"/a b", "/../x", "/a//b", "/a?b", "/."} {
		if msg := loadError(t, with(validEnv(), "APP_BASE_PATH", raw)); !strings.Contains(msg, "APP_BASE_PATH: must be a path like /vantigo") {
			t.Errorf("APP_BASE_PATH=%q: error %q", raw, msg)
		}
	}
}

func TestLoad_FlagsAreStrict(t *testing.T) {
	if msg := loadError(t, with(validEnv(), "ALLOW_INSECURE_TRANSPORT", "true")); !strings.Contains(msg, `ALLOW_INSECURE_TRANSPORT: must be "0" or "1"`) {
		t.Errorf("error = %q", msg)
	}
	if !mustLoad(t, with(validEnv(), "CSP_REPORT_ONLY", "1")).CSPReportOnly {
		t.Error("CSP_REPORT_ONLY=1 did not enable report-only mode")
	}
}

func TestLoad_Branding(t *testing.T) {
	cfg := mustLoad(t, with(validEnv(),
		"APP_TITLE", "  Acme ERP  ",
		"APP_LOGO_URL", "/logo.svg",
		"APP_SUPPORT_EMAIL", "help@acme.test",
		"APP_SUPPORT_PHONE", " +47 123 45 678 ",
		"APP_SUPPORT_URL", "https://support.acme.test",
	))
	want := Branding{Title: "Acme ERP", LogoURL: "/logo.svg", SupportEmail: "help@acme.test", SupportPhone: "+47 123 45 678", SupportURL: "https://support.acme.test"}
	if cfg.Branding != want {
		t.Errorf("Branding = %+v, want %+v", cfg.Branding, want)
	}

	msg := loadError(t, with(validEnv(),
		"APP_LOGO_URL", "javascript:alert(1)",
		"APP_SUPPORT_URL", "//evil.example",
		"APP_SUPPORT_EMAIL", "Help <help@acme.test>",
	))
	for _, want := range []string{
		"APP_LOGO_URL: must be an absolute http or https URL or a path starting with /",
		"APP_SUPPORT_URL: must be an absolute http or https URL or a path starting with /",
		"APP_SUPPORT_EMAIL: must be a plain email address",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error is missing %q:\n%s", want, msg)
		}
	}
}

func TestLoad_EnvAndLogLevel(t *testing.T) {
	if got := mustLoad(t, with(validEnv(), "LOG_LEVEL", "debug")).LogLevel; got != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", got)
	}
	msg := loadError(t, with(validEnv(), "LOG_LEVEL", "verbose", "APP_ENV", "staging"))
	for _, want := range []string{
		`LOG_LEVEL: must be one of "debug", "info", "warn", "error"`,
		`APP_ENV: must be "production" or "development"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error is missing %q:\n%s", want, msg)
		}
	}
}
