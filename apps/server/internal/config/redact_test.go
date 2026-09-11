package config

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The secrets TestFormat_NeverPrintsASecret configures, each distinctive so
// no other field can contain it by accident.
const (
	fmtAppSecret        = "app-secret-value-0123456789abcdefghij"
	fmtBootstrapSecret  = "bootstrap-secret-value"
	fmtAdminEmail       = "break-glass-admin@vantigo.example.com"
	fmtSMTPPassword     = "smtp-password-value"
	fmtOIDCClientSecret = "oidc-client-secret-value"
	fmtSCIMToken        = "scim-token-value"
	fmtSCIMPrevious     = "scim-previous-token-value"
	fmtDBPassword       = "db-password-value"
	fmtMigrationsPass   = "migrations password value"
)

// secretPatterns is every way fmt could render one of the secrets: the
// text itself, its hex (%x, %X), and for the byte-slice AppSecret its
// decimal bytes (%v, %d).
func secretPatterns() []string {
	var out []string
	for _, s := range []string{fmtAppSecret, fmtBootstrapSecret, fmtAdminEmail, fmtSMTPPassword, fmtOIDCClientSecret, fmtSCIMToken, fmtSCIMPrevious, fmtDBPassword, fmtMigrationsPass} {
		out = append(out, s, hex.EncodeToString([]byte(s)), strings.ToUpper(hex.EncodeToString([]byte(s))))
	}
	decimal := make([]string, 0, len(fmtAppSecret))
	for _, b := range []byte(fmtAppSecret) {
		decimal = append(decimal, strconv.Itoa(int(b)))
	}
	return append(out, strings.Join(decimal, " "))
}

func secretConfig(t *testing.T) *Config {
	t.Helper()
	return mustLoad(t, with(withEntra(validEnv()),
		"APP_SECRET", fmtAppSecret,
		"BOOTSTRAP_SECRET", fmtBootstrapSecret,
		"SYSTEM_ADMIN_EMAIL", fmtAdminEmail,
		"SMTP_USERNAME", "smtp-user",
		"SMTP_PASSWORD", fmtSMTPPassword,
		"OIDC_CLIENT_SECRET", fmtOIDCClientSecret,
		"SCIM_TOKEN", fmtSCIMToken,
		"SCIM_PREVIOUS_TOKEN", fmtSCIMPrevious,
		"SCIM_PREVIOUS_TOKEN_EXPIRES_AT", time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"DATABASE_URL", "postgres://vantigo:"+fmtDBPassword+"@db.internal:5432/vantigo?sslmode=verify-full",
		"MIGRATIONS_DATABASE_URL", "host=db.internal user=owner password='"+fmtMigrationsPass+"' dbname=vantigo sslmode=verify-full",
	))
}

// assertNoSecret fails when out contains any rendering of a configured
// secret.
func assertNoSecret(t *testing.T, what, out string) {
	t.Helper()
	for _, pattern := range secretPatterns() {
		if strings.Contains(out, pattern) {
			t.Errorf("%s prints a secret (%q):\n%s", what, pattern, out)
		}
	}
}

// TestFormat_NeverPrintsASecret formats a configuration carrying every
// secret, and each sub-configuration, as values and as pointers, under
// every verb: no secret appears in any form, while the non-secret settings
// still do and a set secret still shows as set.
func TestFormat_NeverPrintsASecret(t *testing.T) {
	cfg := secretConfig(t)
	values := map[string]any{
		"Config": *cfg, "*Config": cfg,
		"MailConfig": cfg.Mail, "*MailConfig": &cfg.Mail,
		"OIDCConfig": *cfg.OIDC, "*OIDCConfig": cfg.OIDC,
		"SCIMConfig": *cfg.SCIM, "*SCIMConfig": cfg.SCIM,
	}
	for name, v := range values {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%d", "%x", "%X", "%q", "%10v"} {
			assertNoSecret(t, name+" "+verb, fmt.Sprintf(verb, v))
		}
	}

	full := fmt.Sprintf("%+v", cfg)
	for _, want := range []string{"db.internal", "smtp.example.com", "smtp-user", validEntraClientID, "BootstrapSecret:" + redactedText, "Password:" + redactedText, "Token:" + redactedText} {
		if !strings.Contains(full, want) {
			t.Errorf("%%+v lost %q:\n%s", want, full)
		}
	}
}

// TestLogValue_NeverLogsASecret logs the same values through both of
// log/slog's handlers: no secret appears in either.
func TestLogValue_NeverLogsASecret(t *testing.T) {
	cfg := secretConfig(t)
	for name, newHandler := range map[string]func(*bytes.Buffer) slog.Handler{
		"text": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
		"json": func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
	} {
		var buf bytes.Buffer
		slog.New(newHandler(&buf)).Info("configuration",
			"cfg", *cfg, "ptr", cfg, "mail", cfg.Mail, "oidc", cfg.OIDC, "scim", cfg.SCIM,
			slog.Group("nested", "cfg", cfg))
		out := buf.String()
		assertNoSecret(t, name+" handler", out)
		if !strings.Contains(out, "db.internal") {
			t.Errorf("%s handler lost the non-secret settings:\n%s", name, out)
		}
	}
}

// redactDatabaseURL replaces every password a connection string can carry
// and leaves the rest readable.
func TestRedactDatabaseURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"postgres://vantigo@db:5432/vantigo?sslmode=verify-full", "postgres://vantigo@db:5432/vantigo?sslmode=verify-full"},
		{"postgres://vantigo:pw@db:5432/vantigo", "postgres://vantigo:redacted@db:5432/vantigo"},
		{"postgresql://db/vantigo?user=v&password=pw", "postgresql://db/vantigo?password=redacted&user=v"},
		{"postgres://db/v?sslpassword=kp", "postgres://db/v?sslpassword=redacted"},
		{"host=db password=pw dbname=v", "host=db password=redacted dbname=v"},
		{"host=db PASSWORD = 'p w\\' x' dbname=v", "host=db PASSWORD=redacted dbname=v"},
		{"host=db sslpassword=kp", "host=db sslpassword=redacted"},
		{"postgres://%zz:pw@db/v", redactedText},
	} {
		if got := redactDatabaseURL(tc.in); got != tc.want {
			t.Errorf("redactDatabaseURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
