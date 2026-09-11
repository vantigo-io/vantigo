package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
)

// redactedText stands in for a secret wherever a configuration is
// formatted, and redactedURLPart for one inside a connection URL, where
// brackets would be percent-encoded.
const (
	redactedText    = "[redacted]"
	redactedURLPart = "redacted"
)

// The redacted* types are Config and its sub-configurations without their
// methods. Format prints a copy of the value through one of them, secrets
// replaced, so fmt's reflection never reaches an original secret field and
// never recurses back into Format. A nested sub-configuration still has
// its own Format, which fmt calls for the field.
type (
	redactedConfig     Config
	redactedMailConfig MailConfig
	redactedOIDCConfig OIDCConfig
	redactedSCIMConfig SCIMConfig
)

// Format prints c for every verb with the application and bootstrap
// secrets, the SYSTEM_ADMIN_EMAIL address and any password in the database
// URLs replaced, and each sub-configuration redacted by its own Format. It
// has a value receiver, so a Config and a *Config are both covered.
func (c Config) Format(f fmt.State, verb rune) {
	v := redactedConfig(c)
	v.DatabaseURL = redactDatabaseURL(v.DatabaseURL)
	v.MigrationsDatabaseURL = redactDatabaseURL(v.MigrationsDatabaseURL)
	if len(v.AppSecret) > 0 {
		v.AppSecret = []byte(redactedText)
	}
	v.BootstrapSecret = redact(v.BootstrapSecret)
	v.SystemAdminEmail = redact(v.SystemAdminEmail)
	_, _ = fmt.Fprintf(f, fmt.FormatString(f, verb), v)
}

// LogValue is c as Format prints it with %+v, so log/slog's text and JSON
// handlers never reflect into a secret field.
func (c Config) LogValue() slog.Value { return slog.StringValue(fmt.Sprintf("%+v", c)) }

// Format prints m for every verb with the SMTP password replaced.
func (m MailConfig) Format(f fmt.State, verb rune) {
	v := redactedMailConfig(m)
	v.Password = redact(v.Password)
	_, _ = fmt.Fprintf(f, fmt.FormatString(f, verb), v)
}

// LogValue is m as Format prints it with %+v.
func (m MailConfig) LogValue() slog.Value { return slog.StringValue(fmt.Sprintf("%+v", m)) }

// Format prints o for every verb with the client secret replaced.
func (o OIDCConfig) Format(f fmt.State, verb rune) {
	v := redactedOIDCConfig(o)
	v.ClientSecret = redact(v.ClientSecret)
	_, _ = fmt.Fprintf(f, fmt.FormatString(f, verb), v)
}

// LogValue is o as Format prints it with %+v.
func (o OIDCConfig) LogValue() slog.Value { return slog.StringValue(fmt.Sprintf("%+v", o)) }

// Format prints s for every verb with both bearer tokens replaced.
func (s SCIMConfig) Format(f fmt.State, verb rune) {
	v := redactedSCIMConfig(s)
	v.Token = redact(v.Token)
	v.PreviousToken = redact(v.PreviousToken)
	_, _ = fmt.Fprintf(f, fmt.FormatString(f, verb), v)
}

// LogValue is s as Format prints it with %+v.
func (s SCIMConfig) LogValue() slog.Value { return slog.StringValue(fmt.Sprintf("%+v", s)) }

// redact is redactedText for a set value and "" for an unset one, so a
// formatted configuration still shows whether a secret is configured.
func redact(v string) string {
	if v == "" {
		return ""
	}
	return redactedText
}

// dsnPasswordPattern matches a password setting (password, sslpassword) in
// a keyword/value connection string, quoted or not.
var dsnPasswordPattern = regexp.MustCompile(`(?i)\b(\w*password)\s*=\s*('(?:[^'\\]|\\.)*'|\S*)`)

// redactDatabaseURL is v with every password in it replaced: the URL
// form's user password and any *password query parameter, or the
// keyword/value form's *password settings. A URL that does not parse is
// replaced whole, since where its password sits cannot be known.
func redactDatabaseURL(v string) string {
	if v == "" {
		return ""
	}
	if !strings.Contains(v, "://") {
		return dsnPasswordPattern.ReplaceAllString(v, "${1}="+redactedURLPart)
	}
	u, err := url.Parse(v)
	if err != nil {
		return redactedText
	}
	if _, ok := u.User.Password(); ok {
		u.User = url.UserPassword(u.User.Username(), redactedURLPart)
	}
	q := u.Query()
	changed := false
	for k := range q {
		if strings.Contains(strings.ToLower(k), "password") {
			q[k] = []string{redactedURLPart}
			changed = true
		}
	}
	if changed {
		u.RawQuery = q.Encode()
	}
	return u.String()
}
