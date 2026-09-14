package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// GetIdentitySession describes the caller's session
// (EA/AuthEndpoints.cs:427-454): the user with their roles as the session
// check read them for this request, whether TOTP is enrolled, whether an
// Owner still has to enrol while owners are required to use MFA, whether
// this session's sign-in verified a second factor, and whether the user is
// a SystemAdmin.
func (s *server) GetIdentitySession(ctx context.Context, _ gen.GetIdentitySessionRequestObject) (gen.GetIdentitySessionResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	u, err := s.q.GetUserByID(ctx, p.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		// The session check joined the user a moment ago; it has gone since.
		return gen.GetIdentitySession401JSONResponse(authErrorBody("unauthenticated", "The session is not authenticated.", nil)), nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: session: %w", err)
	}
	return gen.GetIdentitySession200JSONResponse{
		User:                  authUser(u.ID, u.DisplayName, u.Email, p.Roles),
		TwoFactorEnabled:      u.TotpEnabled,
		MfaEnrollmentRequired: slices.Contains(p.Roles, RoleOwner) && s.deps.Config.OwnersRequireMFA && !u.TotpEnabled,
		MfaAuthenticated:      p.MFAVerified,
		IsSystemAdmin:         slices.Contains(p.Roles, RoleSystemAdmin),
	}, nil
}

// PostIdentityAccountSessionsRevoke ends every session of the caller's
// account, the caller's own included, and clears its cookie
// (EA/SessionEndpoints.cs:31-59). A pending two-factor sign-in the browser
// holds ends too, as .NET's SignOutAsync signed that scheme out as well.
func (s *server) PostIdentityAccountSessionsRevoke(ctx context.Context, _ gen.PostIdentityAccountSessionsRevokeRequestObject) (gen.PostIdentityAccountSessionsRevokeResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.revokeUserSessions(ctx, p, p.UserID); err != nil {
		return nil, err
	}
	cs := cookies{s.access.expiredSessionCookie()}
	ticket, err := s.discardLoginTicket(ctx, r)
	if err != nil {
		return nil, err
	}
	if ticket != nil {
		cs = append(cs, ticket)
	}
	return accountSessionsRevoked{
		cookies: cs,
		body:    gen.PostIdentityAccountSessionsRevoke200JSONResponse{UserId: p.UserID, Revoked: true},
	}, nil
}

// PostIdentitySystemUsersByUserIdSessionsRevoke ends every session of one
// account for a SystemAdmin (EA/SessionEndpoints.cs:61-97): 404
// user_not_found for an unknown account. The caller's own session survives
// unless the caller named their own account, which also clears their cookie.
func (s *server) PostIdentitySystemUsersByUserIdSessionsRevoke(ctx context.Context, req gen.PostIdentitySystemUsersByUserIdSessionsRevokeRequestObject) (gen.PostIdentitySystemUsersByUserIdSessionsRevokeResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	target := req.UserId
	switch _, err := s.q.GetUserByID(ctx, target); {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.PostIdentitySystemUsersByUserIdSessionsRevoke404JSONResponse(authErrorBody("user_not_found", "The account does not exist.", nil)), nil
	case err != nil:
		return nil, fmt.Errorf("identity: revoke sessions: %w", err)
	}
	if err := s.revokeUserSessions(ctx, p, target); err != nil {
		return nil, err
	}
	var cs cookies
	if target == p.UserID {
		cs = cookies{s.access.expiredSessionCookie()}
	}
	return systemSessionsRevoked{
		cookies: cs,
		body:    gen.PostIdentitySystemUsersByUserIdSessionsRevoke200JSONResponse{UserId: target, Revoked: true},
	}, nil
}

// revokeUserSessions ends every live session of target and records
// user.sessions-revoked by actor, in one transaction
// (EA/SessionEndpoints.cs:99-124). .NET revoked by rotating the security
// stamp (UpdateSecurityStampAsync, :109) for both endpoints (:47, :84),
// which also killed every reset link the target held, so this is a full
// rotateSecurityStamp that keeps no session: the self-revocation ends the
// caller's own too, as .NET then signed the caller out (:57).
func (s *server) revokeUserSessions(ctx context.Context, actor contracts.Principal, target uuid.UUID) error {
	r, err := requestFrom(ctx)
	if err != nil {
		return err
	}
	now := s.deps.Clock()
	return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := s.access.rotateSecurityStamp(ctx, q, target, uuid.Nil); err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor:      &actor.UserID,
			targetUser: &target,
			action:     "user.sessions-revoked",
			before:     sessionsRevokedAudit{SessionsRevoked: false},
			after:      sessionsRevokedAudit{SessionsRevoked: true, RevokedAt: &now},
			mfa:        actor.MFAVerified,
		})
	})
}

// sessionsRevokedAudit is .NET's user.sessions-revoked before/after shape
// (EA/SessionEndpoints.cs:119-121).
type sessionsRevokedAudit struct {
	SessionsRevoked bool       `json:"sessionsRevoked"`
	RevokedAt       *time.Time `json:"revokedAt,omitempty"`
}

// accountSessionsRevoked is the self-revocation's 200: the cleared cookie,
// then the generated body.
type accountSessionsRevoked struct {
	cookies
	body gen.PostIdentityAccountSessionsRevoke200JSONResponse
}

func (r accountSessionsRevoked) VisitPostIdentityAccountSessionsRevokeResponse(w http.ResponseWriter) error {
	r.set(w)
	return r.body.VisitPostIdentityAccountSessionsRevokeResponse(w)
}

// systemSessionsRevoked is the SystemAdmin revocation's 200: the cleared
// cookie when the caller revoked themselves, then the generated body.
type systemSessionsRevoked struct {
	cookies
	body gen.PostIdentitySystemUsersByUserIdSessionsRevoke200JSONResponse
}

func (r systemSessionsRevoked) VisitPostIdentitySystemUsersByUserIdSessionsRevokeResponse(w http.ResponseWriter) error {
	r.set(w)
	return r.body.VisitPostIdentitySystemUsersByUserIdSessionsRevokeResponse(w)
}
