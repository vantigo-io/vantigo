package identity

import (
	"net/url"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/mail"
)

// inviteURL is the link an invitation email carries: template with {token}
// replaced by the URL-escaped token (SV/InvitationTokenService.cs:46-54,
// :69-78). template is Config.InvitationAcceptURL, which config.Load has
// already resolved: INVITATION_ACCEPT_URL when set, else APP_URL + base path
// + "/invitations/accept?token={token}", the placeholder checked either way.
func inviteURL(template, token string) string {
	return strings.ReplaceAll(template, "{token}", escapeDataString(token))
}

// resetURL is the link a password-reset email carries: template
// (Config.PasswordResetURL: PASSWORD_RESET_URL, else APP_URL + base path +
// "/password-reset?email={email}&token={token}") with {email} and then
// {token} replaced by their URL-escaped values
// (SV/InvitationTokenService.cs:56-67).
func resetURL(template, email, token string) string {
	withEmail := strings.ReplaceAll(template, "{email}", escapeDataString(email))
	return strings.ReplaceAll(withEmail, "{token}", escapeDataString(token))
}

// escapeDataString is .NET's Uri.EscapeDataString: every byte outside RFC
// 3986's unreserved characters percent-encoded, a space as %20.
// url.QueryEscape differs only in writing a space as "+"; it has already
// encoded a literal "+" as %2B, so every "+" left stands for a space.
func escapeDataString(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// invitationMail and passwordResetMail are identity's two application
// mails: .NET's inline English plain-text templates, verbatim
// (EA/AuthAccountEndpoints.cs:1463-1466, and :1055-1058 and :1389-1392).
// The link is the whole secret, so neither is ever logged.
func invitationMail(to, link string) mail.Message {
	return mail.Message{
		To:       to,
		Subject:  "You are invited to Vantigo",
		TextBody: "Use this link to create your Vantigo account: " + link,
	}
}

func passwordResetMail(to, link string) mail.Message {
	return mail.Message{
		To:       to,
		Subject:  "Reset your Vantigo password",
		TextBody: "Use this link to reset your password: " + link,
	}
}
