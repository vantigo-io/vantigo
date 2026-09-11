package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// passwordResetLifetime is how long a password-reset token stays valid:
// the lifespan .NET gave its DataProtection tokens
// (EA/AuthServiceCollectionExtensions.cs:68-71).
const passwordResetLifetime = 24 * time.Hour

// Password reset's messages (EA/AuthAccountEndpoints.cs:1405-1452).
const (
	resetInvalidMessage      = "The password reset request is invalid."
	resetNotCompletedMessage = "The password reset could not be completed."
)

// invalidResetToken is the reset's one answer for every token it will not
// honour: no such account, a token that is not theirs, expired or spent.
var invalidResetToken = refuse(http.StatusBadRequest, "invalid_reset_token", "The password reset token is invalid or expired.", nil)

// sendPasswordReset mints a password-reset token for u, valid for
// passwordResetLifetime and stored only as its hash, and emails u its link
// (EA/AuthAccountEndpoints.cs:1042-1064). Expired tokens are purged on the
// way. A failed send is swallowed, so no answer tells whether the account
// exists, and logged at WARN with neither the address, nor the link, nor
// the mail driver's error (which can name the recipient). A database
// failure is an error.
func (s *server) sendPasswordReset(ctx context.Context, u store.IdentityUser) error {
	now := s.deps.Clock()
	if err := s.q.DeleteExpiredPasswordResetTokens(ctx, now); err != nil {
		return fmt.Errorf("identity: password reset token: %w", err)
	}
	token, hash := newToken()
	if err := s.q.InsertPasswordResetToken(ctx, store.InsertPasswordResetTokenParams{
		TokenHash: hash,
		UserID:    u.ID,
		ExpiresAt: now.Add(passwordResetLifetime),
	}); err != nil {
		return fmt.Errorf("identity: password reset token: %w", err)
	}
	if err := s.deps.Mail.Send(ctx, passwordResetMail(u.Email, resetURL(s.deps.Config.PasswordResetURL, u.Email, token))); err != nil {
		s.deps.Logger.WarnContext(ctx, "password reset email could not be sent", "user_id", u.ID)
	}
	return nil
}

// PostIdentityPasswordRecoveryRequest starts a password recovery
// (EA/AuthAccountEndpoints.cs:1370-1403): the account with that email is
// sent a reset link when it has a password and a confirmed email. The
// answer is always {accepted:true}, for an unknown email and a failed send
// alike, so it never tells a caller which emails have accounts.
func (s *server) PostIdentityPasswordRecoveryRequest(ctx context.Context, req gen.PostIdentityPasswordRecoveryRequestRequestObject) (gen.PostIdentityPasswordRecoveryRequestResponseObject, error) {
	if req.Body != nil && !blank(req.Body.Email) {
		u, err := s.q.GetUserByNormalizedEmail(ctx, normalizeEmail(*req.Body.Email))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return nil, fmt.Errorf("identity: password recovery: %w", err)
		case u.PasswordHash != nil && u.EmailConfirmed:
			if err := s.sendPasswordReset(ctx, u); err != nil {
				return nil, err
			}
		}
	}
	return gen.PostIdentityPasswordRecoveryRequest200JSONResponse{Accepted: true}, nil
}

// PostIdentityPasswordRecoveryReset sets a new password with a reset token
// (EA/AuthAccountEndpoints.cs:1405-1452). The token must be one issued to
// the account with that email and unexpired, else 400 invalid_reset_token.
// A blank new password is then 400 invalid_request, and one the policy
// fails is 400 invalid_reset_token with the policy's problems, as .NET
// answered. A success spends the token, and with it every other reset
// token of the account, and ends every session of the account, all in the
// transaction that stores the password; of two resets racing with one
// token, only one gets that far.
func (s *server) PostIdentityPasswordRecoveryReset(ctx context.Context, req gen.PostIdentityPasswordRecoveryResetRequestObject) (gen.PostIdentityPasswordRecoveryResetResponseObject, error) {
	var body gen.PasswordResetRequest
	if req.Body != nil {
		body = *req.Body
	}
	if blank(body.Email) || blank(body.Token) {
		return invalidResetToken, nil
	}
	u, err := s.q.GetUserByNormalizedEmail(ctx, normalizeEmail(*body.Email))
	if errors.Is(err, pgx.ErrNoRows) {
		return invalidResetToken, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: password reset: %w", err)
	}
	tokenHash, ok := parseToken(*body.Token)
	if !ok {
		return invalidResetToken, nil
	}
	valid, err := s.q.PasswordResetTokenValid(ctx, store.PasswordResetTokenValidParams{TokenHash: tokenHash, UserID: u.ID, Now: s.deps.Clock()})
	if err != nil {
		return nil, fmt.Errorf("identity: password reset: %w", err)
	}
	if !valid {
		return invalidResetToken, nil
	}

	newPassword := deref(body.NewPassword)
	if strings.TrimSpace(newPassword) == "" {
		return refuse(http.StatusBadRequest, "invalid_request", resetInvalidMessage,
			map[string][]string{"newPassword": {"A new password is required."}}), nil
	}
	if problems := validatePassword(newPassword, s.deps.Config.IsDevelopment()); problems != nil {
		return refuse(http.StatusBadRequest, "invalid_reset_token", resetNotCompletedMessage, problems), nil
	}
	if err := s.setPassword(ctx, u.ID, newPassword, tokenHash); err != nil {
		return refusalOr[gen.PostIdentityPasswordRecoveryResetResponseObject](err)
	}
	return gen.PostIdentityPasswordRecoveryReset200JSONResponse{Success: true}, nil
}

func (r refusal) VisitPostIdentityPasswordRecoveryResetResponse(w http.ResponseWriter) error {
	return r.write(w)
}
