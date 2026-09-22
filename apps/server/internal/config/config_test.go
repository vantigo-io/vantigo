package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// testAppSecret is a fixture value only: 32 bytes, never a real secret.
var testAppSecret = strings.Repeat("s", 32)

func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://vantigo:secret@db.internal:5432/vantigo?sslmode=verify-full",
		"APP_URL":      "https://vantigo.example.com",
		"APP_SECRET":   testAppSecret,
		"SMTP_HOST":    "smtp.example.com",
		"SMTP_FROM":    "noreply@vantigo.example.com",
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

// TestLoad_CommunicationsRetention pins the three retention settings
// (communications inventory §12.1): .NET's defaults of 365 days, 100 messages
// per batch and a 60-minute poll, and the bounds this file enforces where .NET
// silently clamps — a mistyped window is a startup error, not a retention
// policy nobody chose.
func TestLoad_CommunicationsRetention(t *testing.T) {
	cfg := mustLoad(t, validEnv())
	if cfg.CommunicationsRetentionDays != 365 {
		t.Errorf("CommunicationsRetentionDays = %d, want 365", cfg.CommunicationsRetentionDays)
	}
	if cfg.CommunicationsRetentionBatchSize != 100 {
		t.Errorf("CommunicationsRetentionBatchSize = %d, want 100", cfg.CommunicationsRetentionBatchSize)
	}
	if cfg.CommunicationsRetentionPoll != time.Hour {
		t.Errorf("CommunicationsRetentionPoll = %v, want 1h", cfg.CommunicationsRetentionPoll)
	}

	tuned := mustLoad(t, with(validEnv(),
		"COMMUNICATIONS_RETENTION_DAYS", "30",
		"COMMUNICATIONS_RETENTION_BATCH_SIZE", "1000",
		"COMMUNICATIONS_RETENTION_POLL", "5m"))
	if tuned.CommunicationsRetentionDays != 30 {
		t.Errorf("CommunicationsRetentionDays = %d, want 30", tuned.CommunicationsRetentionDays)
	}
	if tuned.CommunicationsRetentionBatchSize != 1000 {
		t.Errorf("CommunicationsRetentionBatchSize = %d, want 1000", tuned.CommunicationsRetentionBatchSize)
	}
	if tuned.CommunicationsRetentionPoll != 5*time.Minute {
		t.Errorf("CommunicationsRetentionPoll = %v, want 5m", tuned.CommunicationsRetentionPoll)
	}

	for _, bad := range [][2]string{
		{"COMMUNICATIONS_RETENTION_DAYS", "0"},
		{"COMMUNICATIONS_RETENTION_BATCH_SIZE", "1001"},
		{"COMMUNICATIONS_RETENTION_POLL", "0s"},
	} {
		if msg := loadError(t, with(validEnv(), bad[0], bad[1])); !strings.Contains(msg, bad[0]) {
			t.Errorf("%s=%q: error %q does not name the field", bad[0], bad[1], msg)
		}
	}
}

func TestLoad_MinimalProductionConfigGetsDefaults(t *testing.T) {
	cfg := mustLoad(t, validEnv())

	if cfg.Env != Production || cfg.IsDevelopment() {
		t.Errorf("Env = %q, want production", cfg.Env)
	}
	if !cfg.UsesHTTPS() {
		t.Error("UsesHTTPS = false, want true for an https APP_URL")
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
	if cfg.CSPReportOnly {
		t.Error("CSP_REPORT_ONLY defaults to on, want off")
	}
	if !cfg.WorkersInProcess {
		t.Error("WorkersInProcess defaults to off, want on")
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

// Transport — http or https, TLS or plaintext to PostgreSQL and SMTP — is
// the operator's choice per deployment. Load accepts every combination in
// every environment; only the cookie Secure attribute follows from it.
func TestLoad_TransportIsTheOperatorsChoice(t *testing.T) {
	// pgconn reads PGSSLMODE from the process environment; a developer's
	// shell must not change what these cases mean.
	t.Setenv("PGSSLMODE", "")

	tests := []struct {
		name    string
		env     map[string]string
		wantErr string // empty: must load
	}{
		{"production accepts a plaintext http APP_URL", with(validEnv(), "APP_URL", "http://vantigo.example.com"), ""},
		{"production accepts SMTP_TLS=none", with(validEnv(), "SMTP_TLS", "none"), ""},
		// A same-host or private-network PostgreSQL has no certificate
		// authority to verify against; the choice is made on the connection
		// string alone.
		{"production accepts sslmode=disable", with(validEnv(), "DATABASE_URL", "postgres://v:s@db/v?sslmode=disable"), ""},
		{"production accepts sslmode=require", with(validEnv(), "DATABASE_URL", "postgres://v:s@db/v?sslmode=require"), ""},
		{"production accepts no sslmode at all", with(validEnv(), "DATABASE_URL", "postgres://v:s@db/v"), ""},
		{"production accepts sslmode=verify-full", with(validEnv(), "DATABASE_URL", "postgres://v:s@db/v?sslmode=verify-full"), ""},
		{"production accepts a plaintext migrations URL", with(validEnv(), "MIGRATIONS_DATABASE_URL", "postgres://o:s@db/v?sslmode=disable"), ""},
		// A Unix domain socket is the same-host transport; pgx (like libpq)
		// ignores every ssl setting on it, so it must load in production.
		{"production accepts a Unix socket in keyword form", with(validEnv(), "DATABASE_URL", "host=/var/run/postgresql user=v dbname=v"), ""},
		{"production accepts a Unix socket in URL form", with(validEnv(), "DATABASE_URL", "postgres://v:s@/v?host=/var/run/postgresql"), ""},
		{"production accepts a Unix socket with sslmode=disable", with(validEnv(), "DATABASE_URL", "postgres://v:s@/v?host=/var/run/postgresql&sslmode=disable"), ""},
		{"production accepts a TCP host with a Unix socket fallback", with(validEnv(), "DATABASE_URL", "host=db,/var/run/postgresql user=v dbname=v sslmode=verify-full"), ""},
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

// The connection string is still parsed the way pgx will parse it, so a
// malformed one fails configuration rather than the first connection.
func TestLoad_MalformedDatabaseURLIsRejectedByName(t *testing.T) {
	for _, field := range []string{"DATABASE_URL", "MIGRATIONS_DATABASE_URL"} {
		msg := loadError(t, with(validEnv(), field, "host=db user=v dbname=v sslmode=bogus"))
		if want := field + ": is not a valid PostgreSQL connection string"; !strings.Contains(msg, want) {
			t.Errorf("error = %q, want it to contain %q", msg, want)
		}
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
	if msg := loadError(t, with(validEnv(), "CSP_REPORT_ONLY", "true")); !strings.Contains(msg, `CSP_REPORT_ONLY: must be "0" or "1"`) {
		t.Errorf("error = %q", msg)
	}
	if !mustLoad(t, with(validEnv(), "CSP_REPORT_ONLY", "1")).CSPReportOnly {
		t.Error("CSP_REPORT_ONLY=1 did not enable report-only mode")
	}
}

// TestLoad_WorkersInProcess proves the opposite default from flag's
// off-by-default switches (design §3.2: single-container deployments run
// api and worker together unless split out), and that it stays a strict
// "0"/"1" switch like every other one.
func TestLoad_WorkersInProcess(t *testing.T) {
	if !mustLoad(t, validEnv()).WorkersInProcess {
		t.Error("WORKERS_IN_PROCESS unset did not default to on")
	}
	if mustLoad(t, with(validEnv(), "WORKERS_IN_PROCESS", "0")).WorkersInProcess {
		t.Error("WORKERS_IN_PROCESS=0 did not disable it")
	}
	if !mustLoad(t, with(validEnv(), "WORKERS_IN_PROCESS", "1")).WorkersInProcess {
		t.Error("WORKERS_IN_PROCESS=1 did not enable it")
	}
	if msg := loadError(t, with(validEnv(), "WORKERS_IN_PROCESS", "true")); !strings.Contains(msg, `WORKERS_IN_PROCESS: must be "0" or "1"`) {
		t.Errorf("error = %q", msg)
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

func TestLoad_IdentityDefaults(t *testing.T) {
	cfg := mustLoad(t, validEnv())

	if len(cfg.AppSecret) != 32 || string(cfg.AppSecret) != testAppSecret {
		t.Errorf("AppSecret = %q, want %q", cfg.AppSecret, testAppSecret)
	}
	wantSessions := SessionConfig{Idle: 8 * time.Hour, PrivilegedIdle: 2 * time.Hour, Absolute: 24 * time.Hour, PrivilegedAbsolute: 8 * time.Hour}
	if cfg.Sessions != wantSessions {
		t.Errorf("Sessions = %+v, want %+v", cfg.Sessions, wantSessions)
	}
	if !cfg.OwnersRequireMFA || cfg.OwnersAllowInsecureNoMFA {
		t.Errorf("OwnersRequireMFA/OwnersAllowInsecureNoMFA = %v/%v, want true/false in production", cfg.OwnersRequireMFA, cfg.OwnersAllowInsecureNoMFA)
	}
	if cfg.MFAIssuer != "Vantigo" {
		t.Errorf("MFAIssuer = %q, want Vantigo", cfg.MFAIssuer)
	}
	if cfg.InvitationLifetime != 168*time.Hour {
		t.Errorf("InvitationLifetime = %v, want 168h", cfg.InvitationLifetime)
	}
	if want := "https://vantigo.example.com/invitations/accept?token={token}"; cfg.InvitationAcceptURL != want {
		t.Errorf("InvitationAcceptURL = %q, want the same-origin default %q", cfg.InvitationAcceptURL, want)
	}
	if want := "https://vantigo.example.com/password-reset?email={email}&token={token}"; cfg.PasswordResetURL != want {
		t.Errorf("PasswordResetURL = %q, want the same-origin default %q", cfg.PasswordResetURL, want)
	}
	wantMail := MailConfig{Driver: "smtp", Host: "smtp.example.com", Port: 587, From: "noreply@vantigo.example.com", TLS: "starttls"}
	if cfg.Mail != wantMail {
		t.Errorf("Mail = %+v, want %+v", cfg.Mail, wantMail)
	}
	if cfg.OIDC != nil {
		t.Errorf("OIDC = %+v, want nil (disabled)", cfg.OIDC)
	}
	if cfg.SCIM != nil {
		t.Errorf("SCIM = %+v, want nil (disabled)", cfg.SCIM)
	}
	if cfg.TrustedProxyCIDRs != nil {
		t.Errorf("TrustedProxyCIDRs = %v, want none", cfg.TrustedProxyCIDRs)
	}

	dev := mustLoad(t, with(validEnv(), "APP_ENV", "development"))
	if dev.OwnersRequireMFA {
		t.Error("OwnersRequireMFA = true, want false in development")
	}
	if dev.Mail.Driver != "log" {
		t.Errorf("Mail.Driver = %q, want log in development", dev.Mail.Driver)
	}
}

func TestLoad_AppSecret(t *testing.T) {
	msg := loadError(t, with(validEnv(), "APP_SECRET", ""))
	if !strings.Contains(msg, "APP_SECRET: must be at least 32 bytes") {
		t.Errorf("error = %q", msg)
	}

	short := "top-secret-but-short"
	msg = loadError(t, with(validEnv(), "APP_SECRET", short))
	if !strings.Contains(msg, "APP_SECRET: must be at least 32 bytes") {
		t.Errorf("error = %q", msg)
	}
	if strings.Contains(msg, short) {
		t.Errorf("error leaks the secret value: %q", msg)
	}

	cfg := mustLoad(t, with(validEnv(), "APP_SECRET", testAppSecret+"x"))
	if string(cfg.AppSecret) != testAppSecret+"x" {
		t.Errorf("AppSecret = %q", cfg.AppSecret)
	}
}

func TestLoad_Sessions(t *testing.T) {
	cfg := mustLoad(t, with(validEnv(),
		"SESSION_IDLE_TIMEOUT", "10h",
		"SESSION_PRIVILEGED_IDLE_TIMEOUT", "1h",
		"SESSION_ABSOLUTE_LIFETIME", "48h",
		"SESSION_PRIVILEGED_ABSOLUTE_LIFETIME", "4h",
	))
	want := SessionConfig{Idle: 10 * time.Hour, PrivilegedIdle: time.Hour, Absolute: 48 * time.Hour, PrivilegedAbsolute: 4 * time.Hour}
	if cfg.Sessions != want {
		t.Errorf("Sessions = %+v, want %+v", cfg.Sessions, want)
	}

	for _, field := range []string{"SESSION_IDLE_TIMEOUT", "SESSION_PRIVILEGED_IDLE_TIMEOUT", "SESSION_ABSOLUTE_LIFETIME", "SESSION_PRIVILEGED_ABSOLUTE_LIFETIME"} {
		for _, bad := range []string{"0", "-1h"} {
			if msg := loadError(t, with(validEnv(), field, bad)); !strings.Contains(msg, field+": must be a positive duration") {
				t.Errorf("%s=%q: error %q", field, bad, msg)
			}
		}
	}

	if msg := loadError(t, with(validEnv(), "SESSION_PRIVILEGED_IDLE_TIMEOUT", "9h")); !strings.Contains(msg, "SESSION_PRIVILEGED_IDLE_TIMEOUT: must not exceed SESSION_IDLE_TIMEOUT") {
		t.Errorf("error = %q", msg)
	}
	if msg := loadError(t, with(validEnv(), "SESSION_PRIVILEGED_ABSOLUTE_LIFETIME", "25h")); !strings.Contains(msg, "SESSION_PRIVILEGED_ABSOLUTE_LIFETIME: must not exceed SESSION_ABSOLUTE_LIFETIME") {
		t.Errorf("error = %q", msg)
	}
}

func TestLoad_OwnersRequireMFA(t *testing.T) {
	if msg := loadError(t, with(validEnv(), "OWNERS_REQUIRE_MFA", "0")); !strings.Contains(msg, "OWNERS_REQUIRE_MFA: must not be 0 outside development") {
		t.Errorf("error = %q", msg)
	}
	if cfg := mustLoad(t, with(validEnv(), "OWNERS_REQUIRE_MFA", "0", "OWNERS_ALLOW_INSECURE_NO_MFA", "1")); cfg.OwnersRequireMFA {
		t.Error("OwnersRequireMFA = true, want false")
	}
	if cfg := mustLoad(t, with(validEnv(), "APP_ENV", "development", "OWNERS_REQUIRE_MFA", "0")); cfg.OwnersRequireMFA {
		t.Error("OwnersRequireMFA = true, want false in development")
	}
	if msg := loadError(t, with(validEnv(), "OWNERS_REQUIRE_MFA", "yes")); !strings.Contains(msg, `OWNERS_REQUIRE_MFA: must be "0" or "1"`) {
		t.Errorf("error = %q", msg)
	}
}

func TestLoad_MFAIssuer(t *testing.T) {
	if cfg := mustLoad(t, with(validEnv(), "MFA_ISSUER", "Acme Corp")); cfg.MFAIssuer != "Acme Corp" {
		t.Errorf("MFAIssuer = %q", cfg.MFAIssuer)
	}
}

func TestLoad_InvitationLifetime(t *testing.T) {
	if cfg := mustLoad(t, with(validEnv(), "INVITATION_LIFETIME", "72h")); cfg.InvitationLifetime != 72*time.Hour {
		t.Errorf("InvitationLifetime = %v", cfg.InvitationLifetime)
	}
	for _, bad := range []string{"1h", "800h"} {
		if msg := loadError(t, with(validEnv(), "INVITATION_LIFETIME", bad)); !strings.Contains(msg, "INVITATION_LIFETIME: must be from 24h") {
			t.Errorf("INVITATION_LIFETIME=%q: error %q", bad, msg)
		}
	}
}

func TestLoad_InvitationAcceptURL(t *testing.T) {
	if cfg := mustLoad(t, with(validEnv(), "INVITATION_ACCEPT_URL", "")); cfg.InvitationAcceptURL == "" || !strings.Contains(cfg.InvitationAcceptURL, "{token}") {
		t.Errorf("InvitationAcceptURL = %q, want the APP_URL-derived default when unset", cfg.InvitationAcceptURL)
	}
	if cfg := mustLoad(t, with(validEnv(), "INVITATION_ACCEPT_URL", "https://accept.example.com/i?token={token}")); cfg.InvitationAcceptURL != "https://accept.example.com/i?token={token}" {
		t.Errorf("InvitationAcceptURL = %q", cfg.InvitationAcceptURL)
	}
	if msg := loadError(t, with(validEnv(), "INVITATION_ACCEPT_URL", "https://vantigo.example.com/invitations/accept")); !strings.Contains(msg, "INVITATION_ACCEPT_URL: must contain the {token} placeholder") {
		t.Errorf("error = %q", msg)
	}
}

func TestLoad_PasswordResetURL(t *testing.T) {
	if cfg := mustLoad(t, with(validEnv(), "PASSWORD_RESET_URL", "")); cfg.PasswordResetURL == "" || !strings.Contains(cfg.PasswordResetURL, "{token}") || !strings.Contains(cfg.PasswordResetURL, "{email}") {
		t.Errorf("PasswordResetURL = %q, want the APP_URL-derived default when unset", cfg.PasswordResetURL)
	}
	if msg := loadError(t, with(validEnv(), "PASSWORD_RESET_URL", "https://vantigo.example.com/password-reset?email={email}")); !strings.Contains(msg, "PASSWORD_RESET_URL: must contain the {token} placeholder") {
		t.Errorf("error = %q", msg)
	}
	if msg := loadError(t, with(validEnv(), "PASSWORD_RESET_URL", "https://vantigo.example.com/password-reset?token={token}")); !strings.Contains(msg, "PASSWORD_RESET_URL: must contain the {email} placeholder") {
		t.Errorf("error = %q", msg)
	}
}

func TestLoad_MailDriver(t *testing.T) {
	if cfg := mustLoad(t, validEnv()); cfg.Mail.Driver != "smtp" {
		t.Errorf("Mail.Driver = %q, want smtp in production", cfg.Mail.Driver)
	}
	if cfg := mustLoad(t, with(validEnv(), "APP_ENV", "development")); cfg.Mail.Driver != "log" {
		t.Errorf("Mail.Driver = %q, want log in development", cfg.Mail.Driver)
	}
	if msg := loadError(t, with(validEnv(), "MAIL_DRIVER", "log")); !strings.Contains(msg, `MAIL_DRIVER: must not be "log" outside development`) {
		t.Errorf("error = %q", msg)
	}
	if cfg := mustLoad(t, with(validEnv(), "APP_ENV", "development", "MAIL_DRIVER", "log")); cfg.Mail.Driver != "log" {
		t.Errorf("Mail.Driver = %q", cfg.Mail.Driver)
	}
	if msg := loadError(t, with(validEnv(), "MAIL_DRIVER", "sendgrid")); !strings.Contains(msg, `MAIL_DRIVER: must be "smtp" or "log"`) {
		t.Errorf("error = %q", msg)
	}
}

func TestLoad_SMTP(t *testing.T) {
	msg := loadError(t, with(validEnv(), "SMTP_HOST", "", "SMTP_FROM", ""))
	for _, want := range []string{"SMTP_HOST: is required", "SMTP_FROM: is required"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error is missing %q:\n%s", want, msg)
		}
	}
	if msg := loadError(t, with(validEnv(), "SMTP_FROM", "Team <team@example.com>")); !strings.Contains(msg, "SMTP_FROM: must be a plain email address") {
		t.Errorf("error = %q", msg)
	}

	if cfg := mustLoad(t, validEnv()); cfg.Mail.Port != 587 {
		t.Errorf("Mail.Port = %d, want 587", cfg.Mail.Port)
	}
	if cfg := mustLoad(t, with(validEnv(), "SMTP_PORT", "465")); cfg.Mail.Port != 465 {
		t.Errorf("Mail.Port = %d", cfg.Mail.Port)
	}

	if cfg := mustLoad(t, validEnv()); cfg.Mail.TLS != "starttls" {
		t.Errorf("Mail.TLS = %q, want starttls", cfg.Mail.TLS)
	}
	for _, mode := range []string{"implicit", "starttls", "none"} {
		if cfg := mustLoad(t, with(validEnv(), "SMTP_TLS", mode)); cfg.Mail.TLS != mode {
			t.Errorf("Mail.TLS = %q, want %q", cfg.Mail.TLS, mode)
		}
	}
	if msg := loadError(t, with(validEnv(), "SMTP_TLS", "ssl")); !strings.Contains(msg, `SMTP_TLS: must be "implicit", "starttls" or "none"`) {
		t.Errorf("error = %q", msg)
	}
}

const (
	validEntraAuthority = "https://login.microsoftonline.com/11111111-2222-3333-4444-555555555555/v2.0"
	validEntraClientID  = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	validGoogleClientID = "12345-abc.apps.googleusercontent.com"
)

func withEntra(env map[string]string) map[string]string {
	return with(env,
		"OIDC_PROVIDER", "entra",
		"OIDC_AUTHORITY", validEntraAuthority,
		"OIDC_CLIENT_ID", validEntraClientID,
		"OIDC_CLIENT_SECRET", "entra-client-secret",
	)
}

func withGoogle(env map[string]string) map[string]string {
	return with(env,
		"OIDC_PROVIDER", "google",
		"OIDC_AUTHORITY", "https://accounts.google.com",
		"OIDC_CLIENT_ID", validGoogleClientID,
		"OIDC_CLIENT_SECRET", "google-client-secret",
		"OIDC_ALLOWED_DOMAINS", "example.com",
	)
}

func TestLoad_OIDC_DisabledByDefault(t *testing.T) {
	if cfg := mustLoad(t, validEnv()); cfg.OIDC != nil {
		t.Errorf("OIDC = %+v, want nil", cfg.OIDC)
	}
}

func TestLoad_OIDC_Entra(t *testing.T) {
	cfg := mustLoad(t, withEntra(validEnv()))
	want := &OIDCConfig{
		Provider: "entra", Authority: validEntraAuthority, ClientID: validEntraClientID,
		ClientSecret: "entra-client-secret", DisplayName: "Workforce SSO",
	}
	if cfg.OIDC.Provider != want.Provider || cfg.OIDC.Authority != want.Authority || cfg.OIDC.ClientID != want.ClientID ||
		cfg.OIDC.ClientSecret != want.ClientSecret || cfg.OIDC.DisplayName != want.DisplayName || len(cfg.OIDC.AllowedDomains) != 0 {
		t.Errorf("OIDC = %+v, want %+v", cfg.OIDC, want)
	}

	if msg := loadError(t, with(withEntra(validEnv()), "OIDC_AUTHORITY", "https://login.microsoftonline.com/not-a-guid/v2.0")); !strings.Contains(msg, "OIDC_AUTHORITY: must be exactly https://login.microsoftonline.com/<tenant-guid>/v2.0") {
		t.Errorf("error = %q", msg)
	}
	if msg := loadError(t, with(withEntra(validEnv()), "OIDC_CLIENT_ID", "not-a-guid")); !strings.Contains(msg, "OIDC_CLIENT_ID: must be a GUID") {
		t.Errorf("error = %q", msg)
	}
	if msg := loadError(t, with(withEntra(validEnv()), "OIDC_ALLOWED_DOMAINS", "example.com")); !strings.Contains(msg, "OIDC_ALLOWED_DOMAINS: is only valid for the google provider") {
		t.Errorf("error = %q", msg)
	}
}

func TestLoad_OIDC_Google(t *testing.T) {
	cfg := mustLoad(t, withGoogle(validEnv()))
	want := &OIDCConfig{
		Provider: "google", Authority: "https://accounts.google.com", ClientID: validGoogleClientID,
		ClientSecret: "google-client-secret", AllowedDomains: []string{"example.com"}, DisplayName: "Workforce SSO",
	}
	if cfg.OIDC.Provider != want.Provider || cfg.OIDC.Authority != want.Authority || cfg.OIDC.ClientID != want.ClientID ||
		cfg.OIDC.ClientSecret != want.ClientSecret || cfg.OIDC.DisplayName != want.DisplayName ||
		strings.Join(cfg.OIDC.AllowedDomains, ",") != strings.Join(want.AllowedDomains, ",") {
		t.Errorf("OIDC = %+v, want %+v", cfg.OIDC, want)
	}

	if msg := loadError(t, with(withGoogle(validEnv()), "OIDC_AUTHORITY", "https://accounts.google.example")); !strings.Contains(msg, "OIDC_AUTHORITY: must be exactly https://accounts.google.com") {
		t.Errorf("error = %q", msg)
	}
	if msg := loadError(t, with(withGoogle(validEnv()), "OIDC_CLIENT_ID", "12345.apps.example.com")); !strings.Contains(msg, "OIDC_CLIENT_ID: must end with .apps.googleusercontent.com") {
		t.Errorf("error = %q", msg)
	}
	if msg := loadError(t, with(withGoogle(validEnv()), "OIDC_ALLOWED_DOMAINS", "")); !strings.Contains(msg, "OIDC_ALLOWED_DOMAINS: must list at least one domain") {
		t.Errorf("error = %q", msg)
	}
}

// OIDC_ALLOWED_DOMAINS is normalised and validated as .NET's
// WorkforceOidcOptions.NormalizeDomains did
// (packages/configuration/Vantigo.Configuration/WorkforceOidcOptions.cs:183-198):
// trimmed, trailing dots stripped, lower-cased, distinct and ordered; any
// entry that is not a bare DNS name is a problem naming the variable,
// never the value.
func TestLoad_OIDC_AllowedDomainsAreBareDNSNames(t *testing.T) {
	cfg := mustLoad(t, with(withGoogle(validEnv()), "OIDC_ALLOWED_DOMAINS", " Sub.Example.ORG. , example.com.., EXAMPLE.com,, "))
	if got := strings.Join(cfg.OIDC.AllowedDomains, ","); got != "example.com,sub.example.org" {
		t.Errorf("AllowedDomains = %q, want example.com,sub.example.org", got)
	}

	for _, junk := range []string{
		"https://example.com", "example.com/path", "admin@example.com", "example.com:443", "exa mple.com",
		"localhost", "-example.com", "example-.com", "*.example.com", "example.c", "example.123", ".",
		"ex_ample.com", "exämple.com", strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 60) + ".com",
	} {
		t.Run(junk, func(t *testing.T) {
			msg := loadError(t, with(withGoogle(validEnv()), "OIDC_ALLOWED_DOMAINS", "example.com,"+junk))
			if !strings.Contains(msg, "OIDC_ALLOWED_DOMAINS: must contain bare DNS names") {
				t.Errorf("error = %q, want the bare DNS name problem", msg)
			}
			if len(junk) > 3 && strings.Contains(strings.ReplaceAll(msg, "such as example.com", ""), junk) {
				t.Errorf("error echoes the value: %q", msg)
			}
		})
	}
}

func TestLoad_OIDC_DisplayName(t *testing.T) {
	if cfg := mustLoad(t, withEntra(validEnv())); cfg.OIDC.DisplayName != "Workforce SSO" {
		t.Errorf("DisplayName = %q, want Workforce SSO", cfg.OIDC.DisplayName)
	}
	if cfg := mustLoad(t, withEntra(with(validEnv(), "OIDC_DISPLAY_NAME", "Acme SSO"))); cfg.OIDC.DisplayName != "Acme SSO" {
		t.Errorf("DisplayName = %q", cfg.OIDC.DisplayName)
	}
}

func TestLoad_OIDC_ClientAuthentication(t *testing.T) {
	if msg := loadError(t, with(withEntra(validEnv()), "OIDC_CLIENT_SECRET", "")); !strings.Contains(msg, "OIDC_CLIENT_SECRET: or OIDC_WORKLOAD_IDENTITY_TOKEN_FILE is required") {
		t.Errorf("error = %q", msg)
	}
	if msg := loadError(t, with(withEntra(validEnv()), "OIDC_WORKLOAD_IDENTITY_TOKEN_FILE", "/var/run/token")); !strings.Contains(msg, "OIDC_CLIENT_SECRET: and OIDC_WORKLOAD_IDENTITY_TOKEN_FILE are mutually exclusive") {
		t.Errorf("error = %q", msg)
	}

	tokenFile := filepath.Join(t.TempDir(), "federated-token")
	if err := os.WriteFile(tokenFile, []byte("federated-token-contents"), 0o600); err != nil {
		t.Fatal(err)
	}

	env := with(withEntra(validEnv()), "OIDC_CLIENT_SECRET", "", "OIDC_WORKLOAD_IDENTITY_TOKEN_FILE", tokenFile)
	if cfg := mustLoad(t, env); cfg.OIDC.WorkloadTokenFile != tokenFile || cfg.OIDC.ClientSecret != "" {
		t.Errorf("OIDC = %+v", cfg.OIDC)
	}

	relative := with(withEntra(validEnv()), "OIDC_CLIENT_SECRET", "", "OIDC_WORKLOAD_IDENTITY_TOKEN_FILE", "relative/token")
	if msg := loadError(t, relative); !strings.Contains(msg, "OIDC_WORKLOAD_IDENTITY_TOKEN_FILE: must be an absolute path") {
		t.Errorf("error = %q", msg)
	}

	missing := with(withEntra(validEnv()), "OIDC_CLIENT_SECRET", "", "OIDC_WORKLOAD_IDENTITY_TOKEN_FILE", filepath.Join(t.TempDir(), "does-not-exist"))
	if msg := loadError(t, missing); !strings.Contains(msg, "OIDC_WORKLOAD_IDENTITY_TOKEN_FILE: must be a readable file") {
		t.Errorf("error = %q", msg)
	}

	googleWorkload := with(withGoogle(validEnv()), "OIDC_CLIENT_SECRET", "", "OIDC_WORKLOAD_IDENTITY_TOKEN_FILE", tokenFile)
	if msg := loadError(t, googleWorkload); !strings.Contains(msg, "OIDC_WORKLOAD_IDENTITY_TOKEN_FILE: is supported only for the entra provider") {
		t.Errorf("error = %q", msg)
	}

	fallback := with(withEntra(validEnv()), "OIDC_CLIENT_SECRET", "", "AZURE_FEDERATED_TOKEN_FILE", tokenFile)
	if cfg := mustLoad(t, fallback); cfg.OIDC.WorkloadTokenFile != tokenFile {
		t.Errorf("WorkloadTokenFile = %q, want the AZURE_FEDERATED_TOKEN_FILE fallback", cfg.OIDC.WorkloadTokenFile)
	}
}

func TestLoad_OIDC_LeftoverSettingsWhileDisabled(t *testing.T) {
	if cfg := mustLoad(t, validEnv()); cfg.OIDC != nil {
		t.Errorf("OIDC = %+v, want nil", cfg.OIDC)
	}

	for _, field := range []string{
		"OIDC_AUTHORITY", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET",
		"OIDC_WORKLOAD_IDENTITY_TOKEN_FILE", "OIDC_ALLOWED_DOMAINS", "OIDC_DISPLAY_NAME",
	} {
		t.Run(field, func(t *testing.T) {
			msg := loadError(t, with(validEnv(), field, "leftover-value"))
			want := "OIDC_PROVIDER: is required because " + field + " is set"
			if !strings.Contains(msg, want) {
				t.Errorf("error = %q, want it to contain %q", msg, want)
			}
			if strings.Contains(msg, "leftover-value") {
				t.Errorf("error echoes the value, not just the field name: %q", msg)
			}
		})
	}

	// Several leftovers are listed in one grammatical sentence.
	for _, tc := range []struct {
		pairs []string
		want  string
	}{
		{[]string{"OIDC_AUTHORITY", "a", "OIDC_CLIENT_ID", "b"}, "OIDC_PROVIDER: is required because OIDC_AUTHORITY and OIDC_CLIENT_ID are set"},
		{[]string{"OIDC_AUTHORITY", "a", "OIDC_CLIENT_ID", "b", "OIDC_DISPLAY_NAME", "c"}, "OIDC_PROVIDER: is required because OIDC_AUTHORITY, OIDC_CLIENT_ID and OIDC_DISPLAY_NAME are set"},
	} {
		if msg := loadError(t, with(validEnv(), tc.pairs...)); !strings.Contains(msg, tc.want) {
			t.Errorf("error = %q, want it to contain %q", msg, tc.want)
		}
	}

	// AZURE_FEDERATED_TOKEN_FILE alone is a platform variable other software
	// may set; it is not evidence of an abandoned OIDC configuration.
	if cfg := mustLoad(t, with(validEnv(), "AZURE_FEDERATED_TOKEN_FILE", "/var/run/secrets/azure-token")); cfg.OIDC != nil {
		t.Errorf("OIDC = %+v, want nil: AZURE_FEDERATED_TOKEN_FILE alone is not a leftover", cfg.OIDC)
	}
}

func TestLoad_OIDC_UnknownProvider(t *testing.T) {
	if msg := loadError(t, with(validEnv(), "OIDC_PROVIDER", "okta")); !strings.Contains(msg, `OIDC_PROVIDER: must be "entra" or "google"`) {
		t.Errorf("error = %q", msg)
	}
}

func TestLoad_SCIM_DisabledByDefault(t *testing.T) {
	if cfg := mustLoad(t, validEnv()); cfg.SCIM != nil {
		t.Errorf("SCIM = %+v, want nil", cfg.SCIM)
	}
}

func TestLoad_SCIM(t *testing.T) {
	cfg := mustLoad(t, with(validEnv(), "SCIM_TOKEN", "scim-bearer-token"))
	if cfg.SCIM == nil || cfg.SCIM.Token != "scim-bearer-token" {
		t.Errorf("SCIM = %+v", cfg.SCIM)
	}

	if msg := loadError(t, with(validEnv(), "SCIM_TOKEN", "has a space")); !strings.Contains(msg, "SCIM_TOKEN: must not contain whitespace") {
		t.Errorf("error = %q", msg)
	}

	if msg := loadError(t, with(validEnv(), "SCIM_TOKEN", "current-token", "SCIM_PREVIOUS_TOKEN", "previous-token")); !strings.Contains(msg, "SCIM_PREVIOUS_TOKEN_EXPIRES_AT: is required when SCIM_PREVIOUS_TOKEN is set") {
		t.Errorf("error = %q", msg)
	}
	if msg := loadError(t, with(validEnv(), "SCIM_TOKEN", "current-token", "SCIM_PREVIOUS_TOKEN_EXPIRES_AT", time.Now().Add(time.Hour).Format(time.RFC3339))); !strings.Contains(msg, "SCIM_PREVIOUS_TOKEN_EXPIRES_AT: requires SCIM_PREVIOUS_TOKEN") {
		t.Errorf("error = %q", msg)
	}
	if msg := loadError(t, with(validEnv(), "SCIM_TOKEN", "current-token", "SCIM_PREVIOUS_TOKEN", "current-token", "SCIM_PREVIOUS_TOKEN_EXPIRES_AT", time.Now().Add(time.Hour).Format(time.RFC3339))); !strings.Contains(msg, "SCIM_PREVIOUS_TOKEN: must differ from SCIM_TOKEN") {
		t.Errorf("error = %q", msg)
	}
	if msg := loadError(t, with(validEnv(), "SCIM_TOKEN", "current-token", "SCIM_PREVIOUS_TOKEN", "previous-token", "SCIM_PREVIOUS_TOKEN_EXPIRES_AT", "not-a-timestamp")); !strings.Contains(msg, "SCIM_PREVIOUS_TOKEN_EXPIRES_AT: must be an RFC 3339 timestamp") {
		t.Errorf("error = %q", msg)
	}
	if msg := loadError(t, with(validEnv(), "SCIM_TOKEN", "current-token", "SCIM_PREVIOUS_TOKEN", "previous-token", "SCIM_PREVIOUS_TOKEN_EXPIRES_AT", time.Now().Add(-time.Hour).Format(time.RFC3339))); !strings.Contains(msg, "SCIM_PREVIOUS_TOKEN_EXPIRES_AT: must be in the future") {
		t.Errorf("error = %q", msg)
	}
	if msg := loadError(t, with(validEnv(), "SCIM_TOKEN", "current-token", "SCIM_PREVIOUS_TOKEN", "previous-token", "SCIM_PREVIOUS_TOKEN_EXPIRES_AT", time.Now().Add(25*time.Hour).Format(time.RFC3339))); !strings.Contains(msg, "SCIM_PREVIOUS_TOKEN_EXPIRES_AT: must be at most 24h from now") {
		t.Errorf("error = %q", msg)
	}

	expires := time.Now().Add(12 * time.Hour).Truncate(time.Second)
	valid := mustLoad(t, with(validEnv(), "SCIM_TOKEN", "current-token", "SCIM_PREVIOUS_TOKEN", "previous-token", "SCIM_PREVIOUS_TOKEN_EXPIRES_AT", expires.Format(time.RFC3339)))
	if valid.SCIM.PreviousToken != "previous-token" || !valid.SCIM.PreviousTokenExpiresAt.Equal(expires) {
		t.Errorf("SCIM = %+v, want previous token %q expiring %v", valid.SCIM, "previous-token", expires)
	}
}

func TestLoad_TrustedProxyCIDRs(t *testing.T) {
	if cfg := mustLoad(t, validEnv()); cfg.TrustedProxyCIDRs != nil {
		t.Errorf("TrustedProxyCIDRs = %v, want none", cfg.TrustedProxyCIDRs)
	}
	cfg := mustLoad(t, with(validEnv(), "TRUSTED_PROXY_CIDRS", "10.0.0.0/8, 192.168.0.0/16"))
	if len(cfg.TrustedProxyCIDRs) != 2 || cfg.TrustedProxyCIDRs[0].String() != "10.0.0.0/8" || cfg.TrustedProxyCIDRs[1].String() != "192.168.0.0/16" {
		t.Errorf("TrustedProxyCIDRs = %v", cfg.TrustedProxyCIDRs)
	}
	if msg := loadError(t, with(validEnv(), "TRUSTED_PROXY_CIDRS", "10.0.0.0/8, not-a-cidr")); !strings.Contains(msg, "TRUSTED_PROXY_CIDRS: must be a comma list of CIDR prefixes") {
		t.Errorf("error = %q", msg)
	}
}

// Outside development, forwarded headers are honoured only from a peer
// inside TRUSTED_PROXY_CIDRS, so hops without a list is a problem naming
// both variables. Development keeps the hops-only behaviour (server.New
// warns about it), and a list that fails to parse is reported once, as
// itself.
func TestLoad_TrustedProxyHopsRequireCIDRsOutsideDevelopment(t *testing.T) {
	const want = "TRUSTED_PROXY_HOPS: requires TRUSTED_PROXY_CIDRS outside development"
	for _, cidrs := range []string{"", " , "} {
		if msg := loadError(t, with(validEnv(), "TRUSTED_PROXY_HOPS", "1", "TRUSTED_PROXY_CIDRS", cidrs)); !strings.Contains(msg, want) {
			t.Errorf("hops without CIDRs (%q): error %q, want %q", cidrs, msg, want)
		}
	}
	cfg := mustLoad(t, with(validEnv(), "TRUSTED_PROXY_HOPS", "2", "TRUSTED_PROXY_CIDRS", "10.0.0.0/8"))
	if cfg.TrustedProxyHops != 2 || len(cfg.TrustedProxyCIDRs) != 1 {
		t.Errorf("hops with CIDRs: hops %d CIDRs %v", cfg.TrustedProxyHops, cfg.TrustedProxyCIDRs)
	}
	if cfg := mustLoad(t, with(validEnv(), "APP_ENV", "development", "TRUSTED_PROXY_HOPS", "1")); cfg.TrustedProxyHops != 1 || cfg.TrustedProxyCIDRs != nil {
		t.Errorf("development hops without CIDRs: hops %d CIDRs %v", cfg.TrustedProxyHops, cfg.TrustedProxyCIDRs)
	}
	msg := loadError(t, with(validEnv(), "TRUSTED_PROXY_HOPS", "1", "TRUSTED_PROXY_CIDRS", "not-a-cidr"))
	if !strings.Contains(msg, "TRUSTED_PROXY_CIDRS: must be a comma list") || strings.Contains(msg, want) {
		t.Errorf("an unparsable list: error %q, want only the parse problem", msg)
	}
}

func TestLoad_Modules(t *testing.T) {
	if cfg := mustLoad(t, validEnv()); !slices.Equal(cfg.Modules, []string{"customers", "products", "energy", "communications", "projects", "time", "expenses"}) {
		t.Errorf("Modules = %v, want the default customers,products,energy,communications,projects,time,expenses when MODULES is unset", cfg.Modules)
	}

	cfg := mustLoad(t, with(validEnv(), "MODULES", " Customers ,, ENERGY,products "))
	if !slices.Equal(cfg.Modules, []string{"customers", "energy", "products"}) {
		t.Errorf("Modules = %v, want trimmed, lower-cased entries with empties dropped", cfg.Modules)
	}

	if msg := loadError(t, with(validEnv(), "MODULES", "customers,widgets")); !strings.Contains(msg, `"widgets" is not a known module`) || !strings.Contains(msg, "customers, products, energy, communications, projects, time, expenses") {
		t.Errorf("error = %q, want it to name the bad value and the known set", msg)
	}

	if msg := loadError(t, with(validEnv(), "MODULES", "energy")); !strings.Contains(msg, "MODULES: energy requires customers") {
		t.Errorf("error = %q, want energy without customers named", msg)
	}

	if msg := loadError(t, with(validEnv(), "MODULES", "communications")); !strings.Contains(msg, "MODULES: communications requires customers") {
		t.Errorf("error = %q, want communications without customers named", msg)
	}

	if msg := loadError(t, with(validEnv(), "MODULES", "projects")); !strings.Contains(msg, "MODULES: projects requires customers") {
		t.Errorf("error = %q, want projects without customers named", msg)
	}

	if msg := loadError(t, with(validEnv(), "MODULES", "customers,time")); !strings.Contains(msg, "MODULES: time requires projects") {
		t.Errorf("error = %q, want time without projects named", msg)
	}

	cfg = mustLoad(t, with(validEnv(), "MODULES", "customers,energy,communications,projects,time"))
	if !slices.Equal(cfg.Modules, []string{"customers", "energy", "communications", "projects", "time"}) {
		t.Errorf("Modules = %v, want exactly customers,energy,communications,projects,time", cfg.Modules)
	}

	// expenses depends on nobody (decision X1): it loads beside customers
	// alone, and — since it does not read the customer directory either — on
	// its own. Both are configurations an installation may actually run, so
	// both are pinned here; a "expenses requires ..." rule added to modules()
	// would fail one of them.
	cfg = mustLoad(t, with(validEnv(), "MODULES", "customers,expenses"))
	if !slices.Equal(cfg.Modules, []string{"customers", "expenses"}) {
		t.Errorf("Modules = %v, want exactly customers,expenses", cfg.Modules)
	}
	cfg = mustLoad(t, with(validEnv(), "MODULES", "expenses"))
	if !slices.Equal(cfg.Modules, []string{"expenses"}) {
		t.Errorf("Modules = %v, want exactly expenses", cfg.Modules)
	}
}

func TestLoad_BrregBaseURL(t *testing.T) {
	if cfg := mustLoad(t, validEnv()); cfg.BrregBaseURL != "https://data.brreg.no" {
		t.Errorf("BrregBaseURL = %q, want the data.brreg.no default when unset", cfg.BrregBaseURL)
	}
	if cfg := mustLoad(t, with(validEnv(), "BRREG_BASE_URL", "https://brreg.example.test/")); cfg.BrregBaseURL != "https://brreg.example.test" {
		t.Errorf("BrregBaseURL = %q, want the trailing slash trimmed", cfg.BrregBaseURL)
	}
	if msg := loadError(t, with(validEnv(), "BRREG_BASE_URL", "not-a-url")); !strings.Contains(msg, "BRREG_BASE_URL: must be an absolute http or https URL") {
		t.Errorf("error = %q", msg)
	}
	if msg := loadError(t, with(validEnv(), "BRREG_BASE_URL", "ftp://brreg.example.test")); !strings.Contains(msg, "BRREG_BASE_URL: must be an absolute http or https URL") {
		t.Errorf("error = %q, want a non-http(s) scheme rejected", msg)
	}
}

func TestLoad_BrregTimeout(t *testing.T) {
	if cfg := mustLoad(t, validEnv()); cfg.BrregTimeout != 15*time.Second {
		t.Errorf("BrregTimeout = %v, want 15s by default", cfg.BrregTimeout)
	}
	if cfg := mustLoad(t, with(validEnv(), "BRREG_TIMEOUT", "5s")); cfg.BrregTimeout != 5*time.Second {
		t.Errorf("BrregTimeout = %v", cfg.BrregTimeout)
	}
	if msg := loadError(t, with(validEnv(), "BRREG_TIMEOUT", "not-a-duration")); !strings.Contains(msg, "BRREG_TIMEOUT: must be a positive duration") {
		t.Errorf("error = %q", msg)
	}
}

func TestLoad_PeppolLookupEnabled(t *testing.T) {
	if cfg := mustLoad(t, validEnv()); !cfg.PeppolLookupEnabled {
		t.Errorf("PeppolLookupEnabled = %v, want true by default", cfg.PeppolLookupEnabled)
	}
	if cfg := mustLoad(t, with(validEnv(), "PEPPOL_LOOKUP_ENABLED", "0")); cfg.PeppolLookupEnabled {
		t.Errorf("PeppolLookupEnabled = %v, want false", cfg.PeppolLookupEnabled)
	}
	if cfg := mustLoad(t, with(validEnv(), "PEPPOL_LOOKUP_ENABLED", "1")); !cfg.PeppolLookupEnabled {
		t.Errorf("PeppolLookupEnabled = %v, want true", cfg.PeppolLookupEnabled)
	}
	if msg := loadError(t, with(validEnv(), "PEPPOL_LOOKUP_ENABLED", "yes")); !strings.Contains(msg, `PEPPOL_LOOKUP_ENABLED: must be "0" or "1"`) {
		t.Errorf("error = %q", msg)
	}
}

func TestLoad_PeppolSMLZone(t *testing.T) {
	if cfg := mustLoad(t, validEnv()); cfg.PeppolSMLZone != "participant.sml.prod.tech.peppol.org" {
		t.Errorf("PeppolSMLZone = %q, want the production zone by default", cfg.PeppolSMLZone)
	}
	if cfg := mustLoad(t, with(validEnv(), "PEPPOL_SML_ZONE", "participant.sml.test.tech.peppol.org")); cfg.PeppolSMLZone != "participant.sml.test.tech.peppol.org" {
		t.Errorf("PeppolSMLZone = %q, want the test network's own zone", cfg.PeppolSMLZone)
	}
	for _, bad := range []string{
		"https://participant.sml.prod.tech.peppol.org",
		"participant.sml.prod.tech.peppol.org/path",
		"has a space",
		"participant.sml.prod.tech.peppol.org:8080", // a caller pasted host:port, not a zone
		".participant.sml.prod.tech.peppol.org",     // leading dot: an empty label
		"participant.sml.prod.tech.peppol.org.",     // trailing dot: an empty label
		"participant..sml.prod.tech.peppol.org",     // consecutive dots: an empty label
	} {
		if msg := loadError(t, with(validEnv(), "PEPPOL_SML_ZONE", bad)); !strings.Contains(msg, "PEPPOL_SML_ZONE: must be a plain hostname: no scheme, port, path, whitespace or empty label") {
			t.Errorf("PEPPOL_SML_ZONE=%q: error = %q", bad, msg)
		}
	}
}

func TestLoad_PeppolDNSServer(t *testing.T) {
	if cfg := mustLoad(t, validEnv()); cfg.PeppolDNSServer != "" {
		t.Errorf("PeppolDNSServer = %q, want empty by default (the server's own resolvers)", cfg.PeppolDNSServer)
	}
	if cfg := mustLoad(t, with(validEnv(), "PEPPOL_DNS_SERVER", "127.0.0.1:53")); cfg.PeppolDNSServer != "127.0.0.1:53" {
		t.Errorf("PeppolDNSServer = %q, want 127.0.0.1:53", cfg.PeppolDNSServer)
	}
	if cfg := mustLoad(t, with(validEnv(), "PEPPOL_DNS_SERVER", "[::1]:53")); cfg.PeppolDNSServer != "[::1]:53" {
		t.Errorf("PeppolDNSServer = %q, want [::1]:53 (a bracketed IPv6 literal)", cfg.PeppolDNSServer)
	}
	for _, bad := range []string{"127.0.0.1", "127.0.0.1:", "127.0.0.1:dns", "not-host-port-at-all"} {
		if msg := loadError(t, with(validEnv(), "PEPPOL_DNS_SERVER", bad)); !strings.Contains(msg, "PEPPOL_DNS_SERVER:") {
			t.Errorf("PEPPOL_DNS_SERVER=%q: error = %q, want a PEPPOL_DNS_SERVER problem", bad, msg)
		}
	}
}

func TestLoad_PeppolTimeout(t *testing.T) {
	if cfg := mustLoad(t, validEnv()); cfg.PeppolTimeout != 10*time.Second {
		t.Errorf("PeppolTimeout = %v, want 10s by default", cfg.PeppolTimeout)
	}
	if cfg := mustLoad(t, with(validEnv(), "PEPPOL_TIMEOUT", "3s")); cfg.PeppolTimeout != 3*time.Second {
		t.Errorf("PeppolTimeout = %v, want 3s", cfg.PeppolTimeout)
	}
	if msg := loadError(t, with(validEnv(), "PEPPOL_TIMEOUT", "0s")); !strings.Contains(msg, "PEPPOL_TIMEOUT: must be a positive duration") {
		t.Errorf("error = %q, want a positive-duration problem for 0s", msg)
	}
	if msg := loadError(t, with(validEnv(), "PEPPOL_TIMEOUT", "not-a-duration")); !strings.Contains(msg, "PEPPOL_TIMEOUT: must be a positive duration") {
		t.Errorf("error = %q", msg)
	}
}

func TestLoad_ObjectStorageUnconfigured(t *testing.T) {
	cfg := mustLoad(t, validEnv())
	if cfg.StorageProvider != "" || cfg.StorageFSRoot != "" || cfg.StorageFSAllowInsecureRoot {
		t.Errorf("StorageProvider/StorageFSRoot/StorageFSAllowInsecureRoot = %q/%q/%v, want all zero when STORAGE_PROVIDER is unset",
			cfg.StorageProvider, cfg.StorageFSRoot, cfg.StorageFSAllowInsecureRoot)
	}
}

func TestLoad_StorageProvider(t *testing.T) {
	cfg := mustLoad(t, with(validEnv(), "STORAGE_PROVIDER", "fs", "STORAGE_FS_ROOT", "/var/lib/vantigo/objects"))
	if cfg.StorageProvider != "fs" || cfg.StorageFSRoot != "/var/lib/vantigo/objects" {
		t.Errorf("StorageProvider/StorageFSRoot = %q/%q", cfg.StorageProvider, cfg.StorageFSRoot)
	}

	if msg := loadError(t, with(validEnv(), "STORAGE_PROVIDER", "s3")); !strings.Contains(msg, `STORAGE_PROVIDER: must be "fs" if set`) {
		t.Errorf("error = %q, want an unknown provider rejected", msg)
	}
}

func TestLoad_StorageFSRoot(t *testing.T) {
	if msg := loadError(t, with(validEnv(), "STORAGE_PROVIDER", "fs")); !strings.Contains(msg, `STORAGE_FS_ROOT: is required when STORAGE_PROVIDER is "fs"`) {
		t.Errorf("error = %q, want a missing root rejected", msg)
	}
	if msg := loadError(t, with(validEnv(), "STORAGE_PROVIDER", "fs", "STORAGE_FS_ROOT", "relative/path")); !strings.Contains(msg, "STORAGE_FS_ROOT: must be an absolute path") {
		t.Errorf("error = %q, want a relative root rejected", msg)
	}
	if msg := loadError(t, with(validEnv(), "STORAGE_FS_ROOT", "/var/lib/vantigo/objects")); !strings.Contains(msg, `STORAGE_FS_ROOT: is only valid when STORAGE_PROVIDER is "fs"`) {
		t.Errorf("error = %q, want a root without a provider rejected", msg)
	}
}

func TestLoad_StorageFSAllowInsecureRoot(t *testing.T) {
	env := with(validEnv(), "STORAGE_PROVIDER", "fs", "STORAGE_FS_ROOT", "/var/lib/vantigo/objects")

	if cfg := mustLoad(t, env); cfg.StorageFSAllowInsecureRoot {
		t.Error("StorageFSAllowInsecureRoot = true, want false by default")
	}

	if msg := loadError(t, with(env, "STORAGE_FS_ALLOW_INSECURE_ROOT", "1")); !strings.Contains(msg, "STORAGE_FS_ALLOW_INSECURE_ROOT: must not be 1 outside development") {
		t.Errorf("error = %q, want the escape hatch rejected outside development", msg)
	}

	devEnv := with(env, "APP_ENV", "development", "STORAGE_FS_ALLOW_INSECURE_ROOT", "1")
	if cfg := mustLoad(t, devEnv); !cfg.StorageFSAllowInsecureRoot {
		t.Error("StorageFSAllowInsecureRoot = false, want true when accepted in development")
	}
}

// TestLoad_CommunicationsAIDefaults pins CommunicationsAiOptions' defaults
// (communications inventory §17.1): off, "openai", "gpt-4o-mini", no key —
// the state in which the two AI operations answer 503 ai_unavailable. An
// unconfigured AI feature is never a load error, exactly as .NET simply
// declines to register a chat client for it.
func TestLoad_CommunicationsAIDefaults(t *testing.T) {
	cfg := mustLoad(t, validEnv())
	if cfg.CommunicationsAIEnabled {
		t.Error("CommunicationsAIEnabled = true, want false by default")
	}
	if cfg.CommunicationsAIProvider != "openai" {
		t.Errorf("CommunicationsAIProvider = %q, want %q", cfg.CommunicationsAIProvider, "openai")
	}
	if cfg.CommunicationsAIModel != "gpt-4o-mini" {
		t.Errorf("CommunicationsAIModel = %q, want %q", cfg.CommunicationsAIModel, "gpt-4o-mini")
	}
	if cfg.CommunicationsAIAPIKey != "" {
		t.Errorf("CommunicationsAIAPIKey = %q, want empty", cfg.CommunicationsAIAPIKey)
	}
}

// TestLoad_CommunicationsAIConfigured pins that each value is read and
// trimmed, that a blank provider or model falls back to its default rather
// than becoming empty, and that COMMUNICATIONS_AI_ENABLED is this file's
// strict "0"/"1" flag.
func TestLoad_CommunicationsAIConfigured(t *testing.T) {
	cfg := mustLoad(t, with(validEnv(),
		"COMMUNICATIONS_AI_ENABLED", "1",
		"COMMUNICATIONS_AI_PROVIDER", "  openai  ",
		"COMMUNICATIONS_AI_MODEL", "  gpt-4.1-mini  ",
		"COMMUNICATIONS_AI_API_KEY", "  sk-test-key  ",
	))
	if !cfg.CommunicationsAIEnabled {
		t.Error("CommunicationsAIEnabled = false, want true")
	}
	if cfg.CommunicationsAIProvider != "openai" {
		t.Errorf("CommunicationsAIProvider = %q, want it trimmed", cfg.CommunicationsAIProvider)
	}
	if cfg.CommunicationsAIModel != "gpt-4.1-mini" {
		t.Errorf("CommunicationsAIModel = %q, want it trimmed", cfg.CommunicationsAIModel)
	}
	if cfg.CommunicationsAIAPIKey != "sk-test-key" {
		t.Errorf("CommunicationsAIAPIKey = %q, want it trimmed", cfg.CommunicationsAIAPIKey)
	}

	blank := mustLoad(t, with(validEnv(), "COMMUNICATIONS_AI_PROVIDER", "   ", "COMMUNICATIONS_AI_MODEL", "   "))
	if blank.CommunicationsAIProvider != "openai" || blank.CommunicationsAIModel != "gpt-4o-mini" {
		t.Errorf("provider/model = %q/%q, want the defaults for a blank value",
			blank.CommunicationsAIProvider, blank.CommunicationsAIModel)
	}

	if msg := loadError(t, with(validEnv(), "COMMUNICATIONS_AI_ENABLED", "true")); !strings.Contains(msg, "COMMUNICATIONS_AI_ENABLED") {
		t.Errorf("error = %q, want a non-0/1 flag value rejected", msg)
	}
}

func TestLoad_BootstrapOwnerEmail(t *testing.T) {
	if cfg := mustLoad(t, validEnv()); cfg.BootstrapOwnerEmail != "" {
		t.Errorf("BootstrapOwnerEmail = %q, want empty by default", cfg.BootstrapOwnerEmail)
	}
	cfg := mustLoad(t, with(validEnv(), "BOOTSTRAP_OWNER_EMAIL", "  owner@customer.example  "))
	if cfg.BootstrapOwnerEmail != "owner@customer.example" {
		t.Errorf("BootstrapOwnerEmail = %q, want it trimmed", cfg.BootstrapOwnerEmail)
	}
	for _, bad := range []string{"not-an-address", "Owner <owner@customer.example>"} {
		if msg := loadError(t, with(validEnv(), "BOOTSTRAP_OWNER_EMAIL", bad)); !strings.Contains(msg, "BOOTSTRAP_OWNER_EMAIL: must be a plain email address") {
			t.Errorf("%q: error = %q", bad, msg)
		}
	}
	if cfg := mustLoad(t, with(validEnv(), "BOOTSTRAP_OWNER_EMAIL", "   ")); cfg.BootstrapOwnerEmail != "" {
		t.Errorf("BootstrapOwnerEmail = %q, want a whitespace-only value to load as disabled", cfg.BootstrapOwnerEmail)
	}
}

func TestLoad_Management_DisabledByDefault(t *testing.T) {
	if cfg := mustLoad(t, validEnv()); cfg.Management != nil {
		t.Errorf("Management = %+v, want nil", cfg.Management)
	}
}

func TestLoad_Management(t *testing.T) {
	token := strings.Repeat("t", 32)
	cfg := mustLoad(t, with(validEnv(), "MANAGEMENT_PORT", "9090", "MANAGEMENT_TOKEN", token))
	if cfg.Management == nil || cfg.Management.Port != 9090 || cfg.Management.Token != token {
		t.Errorf("Management = %+v", cfg.Management)
	}

	cases := []struct {
		pairs []string
		want  string
	}{
		{[]string{"MANAGEMENT_PORT", "9090"}, "MANAGEMENT_TOKEN: is required when MANAGEMENT_PORT is set"},
		{[]string{"MANAGEMENT_TOKEN", token}, "MANAGEMENT_PORT: is required when MANAGEMENT_TOKEN is set"},
		{[]string{"MANAGEMENT_PORT", "0", "MANAGEMENT_TOKEN", token}, "MANAGEMENT_PORT: must be an integer from 1 to 65535"},
		{[]string{"MANAGEMENT_PORT", "8080", "MANAGEMENT_TOKEN", token}, "MANAGEMENT_PORT: must differ from PORT"},
		{[]string{"MANAGEMENT_PORT", "9090", "MANAGEMENT_TOKEN", "short"}, "MANAGEMENT_TOKEN: must be at least 32 characters"},
		{[]string{"MANAGEMENT_PORT", "9090", "MANAGEMENT_TOKEN", strings.Repeat("t", 31) + " "}, "MANAGEMENT_TOKEN: must not contain whitespace"},
		{[]string{"MANAGEMENT_PORT", "9090", "MANAGEMENT_TOKEN", "   "}, "MANAGEMENT_TOKEN: must not contain whitespace"},
	}
	for _, tc := range cases {
		msg := loadError(t, with(validEnv(), tc.pairs...))
		if !strings.Contains(msg, tc.want) {
			t.Errorf("%v: error = %q, want %q", tc.pairs[:1], msg, tc.want)
		}
		if strings.Contains(msg, token) {
			t.Errorf("the error echoes the token: %q", msg)
		}
	}
}
