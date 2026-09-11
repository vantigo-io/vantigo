package identity

import (
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// linkConfig loads the configuration of a development installation served
// at APP_URL http://localhost:5173, with pairs applied over it. Go has no
// unconfigured installation (APP_URL is required), so .NET's "without
// configuration" is this: no INVITATION_ACCEPT_URL or PASSWORD_RESET_URL,
// and the local development origin .NET fell back to.
func linkConfig(t *testing.T, pairs ...string) *config.Config {
	t.Helper()
	cfg, err := config.Load(linkEnv(pairs...))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

func linkEnv(pairs ...string) map[string]string {
	env := map[string]string{
		"APP_ENV":      "development",
		"DATABASE_URL": "postgres://vantigo@localhost:5432/vantigo",
		"APP_URL":      "http://localhost:5173",
		"APP_SECRET":   "identity-links-test-app-secret-0123456789abcdef",
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		env[pairs[i]] = pairs[i+1]
	}
	return env
}

// Ported from InvitationTokenServiceUrlTests.InvitationUrl_WithoutConfiguration_UsesTheDevelopmentFallback.
func TestInviteURL_WithoutATemplateUsesTheLocalDevelopmentOrigin(t *testing.T) {
	cfg := linkConfig(t)
	if got, want := inviteURL(cfg.InvitationAcceptURL, "raw-token"), "http://localhost:5173/invitations/accept?token=raw-token"; got != want {
		t.Errorf("inviteURL = %q, want %q", got, want)
	}
}

// Ported from InvitationTokenServiceUrlTests.InvitationUrl_DerivesFromThePublicOriginAndBasePath.
func TestInviteURL_DerivesFromThePublicOriginAndBasePath(t *testing.T) {
	cfg := linkConfig(t, "APP_URL", "https://vantigo.example.com", "APP_BASE_PATH", "/customers")
	if got, want := inviteURL(cfg.InvitationAcceptURL, "raw-token"), "https://vantigo.example.com/customers/invitations/accept?token=raw-token"; got != want {
		t.Errorf("inviteURL = %q, want %q", got, want)
	}
}

// Ported from InvitationTokenServiceUrlTests.InvitationUrl_ExplicitTemplateWinsOverThePublicOrigin.
func TestInviteURL_ExplicitTemplateWinsOverThePublicOrigin(t *testing.T) {
	cfg := linkConfig(t,
		"APP_URL", "https://vantigo.example.com", "APP_BASE_PATH", "/customers",
		"INVITATION_ACCEPT_URL", "https://other.example.com/join?token={token}")
	if got, want := inviteURL(cfg.InvitationAcceptURL, "raw-token"), "https://other.example.com/join?token=raw-token"; got != want {
		t.Errorf("inviteURL = %q, want %q", got, want)
	}
}

// Ported from InvitationTokenServiceUrlTests.InvitationUrl_UrlEncodesTheToken.
func TestInviteURL_URLEncodesTheToken(t *testing.T) {
	cfg := linkConfig(t, "APP_URL", "https://vantigo.example.com")
	if got, want := inviteURL(cfg.InvitationAcceptURL, "a+b/c"), "https://vantigo.example.com/invitations/accept?token=a%2Bb%2Fc"; got != want {
		t.Errorf("inviteURL = %q, want %q", got, want)
	}
}

// Ported from InvitationTokenServiceUrlTests.PasswordResetUrl_DerivesFromThePublicOriginAndBasePath.
func TestResetURL_DerivesFromThePublicOriginAndBasePath(t *testing.T) {
	cfg := linkConfig(t, "APP_URL", "https://vantigo.example.com", "APP_BASE_PATH", "/customers")
	got := resetURL(cfg.PasswordResetURL, "user@example.test", "encoded-token")
	if want := "https://vantigo.example.com/customers/password-reset?email=user%40example.test&token=encoded-token"; got != want {
		t.Errorf("resetURL = %q, want %q", got, want)
	}
}

// Ported from InvitationTokenServiceUrlTests.PasswordResetUrl_ExplicitTemplateWinsOverThePublicOrigin.
// .NET asserted the prefix; this asserts the whole link.
func TestResetURL_ExplicitTemplateWinsOverThePublicOrigin(t *testing.T) {
	cfg := linkConfig(t,
		"APP_URL", "https://vantigo.example.com",
		"PASSWORD_RESET_URL", "https://other.example.com/reset?email={email}&token={token}")
	got := resetURL(cfg.PasswordResetURL, "user@example.test", "encoded-token")
	if want := "https://other.example.com/reset?email=user%40example.test&token=encoded-token"; got != want {
		t.Errorf("resetURL = %q, want %q", got, want)
	}
}

// Ported from InvitationTokenServiceUrlTests.PasswordResetUrl_WithoutConfiguration_UsesTheDevelopmentFallback.
func TestResetURL_WithoutATemplateUsesTheLocalDevelopmentOrigin(t *testing.T) {
	cfg := linkConfig(t)
	got := resetURL(cfg.PasswordResetURL, "user@example.test", "token")
	if want := "http://localhost:5173/password-reset?email=user%40example.test&token=token"; got != want {
		t.Errorf("resetURL = %q, want %q", got, want)
	}
}

// Ported from InvitationTokenServiceUrlTests.Urls_FailFastOnAnInvalidPublicOrigin.
// The links derive from APP_URL, which config.Load refuses unless it is an
// absolute http or https origin, so the installation never starts.
func TestLinks_FailFastOnAnInvalidPublicOrigin(t *testing.T) {
	_, err := config.Load(linkEnv("APP_URL", "vantigo.example.com"))
	if err == nil || !strings.Contains(err.Error(), "APP_URL") {
		t.Fatalf("config.Load with APP_URL vantigo.example.com: err = %v, want an APP_URL problem", err)
	}
}

// TestLinks_TemplatesMustCarryTheirPlaceholders pins what .NET checked at
// send time (SV/InvitationTokenService.cs:61-64, :71-74) to startup: a
// template without its placeholders is refused by config.Load.
func TestLinks_TemplatesMustCarryTheirPlaceholders(t *testing.T) {
	for _, c := range []struct{ field, value string }{
		{"INVITATION_ACCEPT_URL", "https://other.example.com/join"},
		{"PASSWORD_RESET_URL", "https://other.example.com/reset?token={token}"},
		{"PASSWORD_RESET_URL", "https://other.example.com/reset?email={email}"},
	} {
		if _, err := config.Load(linkEnv(c.field, c.value)); err == nil || !strings.Contains(err.Error(), c.field) {
			t.Errorf("%s=%s: err = %v, want a %s problem", c.field, c.value, err, c.field)
		}
	}
}

// TestEscapeDataString_IsUriEscapeDataString pins .NET's escaping: only
// RFC 3986's unreserved characters stay as they are, and a space is %20.
func TestEscapeDataString_IsUriEscapeDataString(t *testing.T) {
	for in, want := range map[string]string{
		"AZaz09-_.~":          "AZaz09-_.~",
		"a b":                 "a%20b",
		"a+b/c":               "a%2Bb%2Fc",
		"user@example.test":   "user%40example.test",
		"?&=#%:;,{}":          "%3F%26%3D%23%25%3A%3B%2C%7B%7D",
		"blåbær@example.test": "bl%C3%A5b%C3%A6r%40example.test",
	} {
		if got := escapeDataString(in); got != want {
			t.Errorf("escapeDataString(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestApplicationMailsAreDotNetsTemplates pins both mails to .NET's
// English plain-text templates (EA/AuthAccountEndpoints.cs:1463-1466,
// :1389-1392).
func TestApplicationMailsAreDotNetsTemplates(t *testing.T) {
	inv := invitationMail("invitee@example.test", "https://vantigo.example.com/invitations/accept?token=t")
	if inv.To != "invitee@example.test" || inv.Subject != "You are invited to Vantigo" ||
		inv.TextBody != "Use this link to create your Vantigo account: https://vantigo.example.com/invitations/accept?token=t" {
		t.Errorf("invitationMail = %+v", inv)
	}
	reset := passwordResetMail("user@example.test", "https://vantigo.example.com/password-reset?email=e&token=t")
	if reset.To != "user@example.test" || reset.Subject != "Reset your Vantigo password" ||
		reset.TextBody != "Use this link to reset your password: https://vantigo.example.com/password-reset?email=e&token=t" {
		t.Errorf("passwordResetMail = %+v", reset)
	}
}
