package config

import (
	"encoding/json"
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

// redacted is c with the application secret and any password in the
// database URLs replaced: the representation Format and MarshalJSON both
// print. A nested
// sub-configuration keeps its own field type, so printing or marshalling it
// still goes through its own Format or MarshalJSON.
func (c Config) redacted() redactedConfig {
	v := redactedConfig(c)
	v.DatabaseURL = redactDatabaseURL(v.DatabaseURL)
	v.MigrationsDatabaseURL = redactDatabaseURL(v.MigrationsDatabaseURL)
	if len(v.AppSecret) > 0 {
		v.AppSecret = []byte(redactedText)
	}
	v.CommunicationsAIAPIKey = redact(v.CommunicationsAIAPIKey)
	return v
}

// Format prints c for every verb with the application secret and any
// password in the database URLs replaced, and each sub-configuration
// redacted by its own Format. It
// has a value receiver, so a Config and a *Config are both covered.
func (c Config) Format(f fmt.State, verb rune) {
	_, _ = fmt.Fprintf(f, fmt.FormatString(f, verb), c.redacted())
}

// LogValue is c as Format prints it with %+v, so log/slog's text and JSON
// handlers never reflect into a secret field.
func (c Config) LogValue() slog.Value { return slog.StringValue(fmt.Sprintf("%+v", c)) }

// MarshalJSON encodes c with the same secrets redacted as Format. LogValue
// covers a Config logged on its own, but slog's JSON handler reaches a
// Config nested inside another value (module.Deps passed to slog.Any, say)
// through encoding/json directly, which consults neither LogValue nor
// Format; MarshalJSON is what encoding/json does call. Each
// sub-configuration's own MarshalJSON redacts it in turn.
func (c Config) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.redacted())
}

// Format prints m for every verb with the SMTP password replaced.
func (m MailConfig) Format(f fmt.State, verb rune) {
	_, _ = fmt.Fprintf(f, fmt.FormatString(f, verb), m.redacted())
}

// LogValue is m as Format prints it with %+v.
func (m MailConfig) LogValue() slog.Value { return slog.StringValue(fmt.Sprintf("%+v", m)) }

// MarshalJSON encodes m with the same password redacted as Format; see
// Config.MarshalJSON for why it exists alongside Format and LogValue.
func (m MailConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.redacted())
}

func (m MailConfig) redacted() redactedMailConfig {
	v := redactedMailConfig(m)
	v.Password = redact(v.Password)
	return v
}

// Format prints o for every verb with the client secret replaced.
func (o OIDCConfig) Format(f fmt.State, verb rune) {
	_, _ = fmt.Fprintf(f, fmt.FormatString(f, verb), o.redacted())
}

// LogValue is o as Format prints it with %+v.
func (o OIDCConfig) LogValue() slog.Value { return slog.StringValue(fmt.Sprintf("%+v", o)) }

// MarshalJSON encodes o with the same client secret redacted as Format; see
// Config.MarshalJSON for why it exists alongside Format and LogValue.
func (o OIDCConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.redacted())
}

func (o OIDCConfig) redacted() redactedOIDCConfig {
	v := redactedOIDCConfig(o)
	v.ClientSecret = redact(v.ClientSecret)
	return v
}

// Format prints s for every verb with both bearer tokens replaced.
func (s SCIMConfig) Format(f fmt.State, verb rune) {
	_, _ = fmt.Fprintf(f, fmt.FormatString(f, verb), s.redacted())
}

// LogValue is s as Format prints it with %+v.
func (s SCIMConfig) LogValue() slog.Value { return slog.StringValue(fmt.Sprintf("%+v", s)) }

// MarshalJSON encodes s with the same bearer tokens redacted as Format; see
// Config.MarshalJSON for why it exists alongside Format and LogValue.
func (s SCIMConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.redacted())
}

func (s SCIMConfig) redacted() redactedSCIMConfig {
	v := redactedSCIMConfig(s)
	v.Token = redact(v.Token)
	v.PreviousToken = redact(v.PreviousToken)
	return v
}

// redact is redactedText for a set value and "" for an unset one, so a
// formatted configuration still shows whether a secret is configured.
func redact(v string) string {
	if v == "" {
		return ""
	}
	return redactedText
}

// dsnPasswordPattern matches a password setting (password, sslpassword) in
// a keyword/value connection string, following libpq's keyword/value
// grammar: a value is either single-quoted, with \' and \\ escaped inside
// the quotes, or unquoted, where a backslash escapes the one character
// after it (typically a space, so the value can hold one without quoting).
// An empty value (password=, immediately followed by whitespace and the
// next setting) matches zero characters rather than swallowing what
// follows: the optional whitespace before a quote is only consumed as part
// of matching that quote, never on its own.
var dsnPasswordPattern = regexp.MustCompile(`(?i)\b(\w*password)\s*=(?:\s*'(?:[^'\\]|\\.)*'|(?:\\.|[^\s\\])*)`)

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
