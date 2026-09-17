package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// ownerInvitationsPath is the owner invitations collection, the Location of
// a created invitation.
const ownerInvitationsPath = "/api/v1/identity/owner/invitations"

// Invitation messages (EA/AuthAccountEndpoints.cs:1076-1368).
const (
	invitationRequestInvalidMessage = "The invitation request is invalid."
	invitationNotCreatedMessage     = "The invitation account could not be created."
)

var (
	// accountConflict answers an invitation change that lost a race on
	// every attempt: .NET's owner-management filter
	// (EA/AuthAccountEndpoints.cs:28-39).
	accountConflict = refuse(http.StatusConflict, "account_conflict", "The account change conflicts with another account change.", nil)
	// invitationInvalid is acceptance's one answer for a token it cannot
	// accept: missing, unknown, revoked, accepted, expired, or lost to a
	// concurrent acceptance.
	invitationInvalid = refuse(http.StatusBadRequest, "invitation_invalid", "The invitation is invalid or no longer available.", nil)
)

// PostIdentityOwnerInvitations invites an email to create an account with a
// role, for an Owner (EA/AuthAccountEndpoints.cs:1076-1146). After
// validation (400 invalid_request), one serializable transaction: an email
// some account already has is 409 account_exists; otherwise the invitation
// replaces the email's pending ones (issueInvitation). Only after commit is
// the link emailed, and a failed send revokes the invitation and answers
// 500 (deliverInvitation). 201 with the invitation and its Location.
func (s *server) PostIdentityOwnerInvitations(ctx context.Context, req gen.PostIdentityOwnerInvitationsRequestObject) (gen.PostIdentityOwnerInvitationsResponseObject, error) {
	var body gen.InvitationRequest
	if req.Body != nil {
		body = *req.Body
	}
	if fields := validateInvitationRequest(body); len(fields) > 0 {
		return refuse(http.StatusBadRequest, "invalid_request", invitationRequestInvalidMessage, fields), nil
	}
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}

	email := strings.TrimSpace(*body.Email)
	var displayName *string
	if !blank(body.DisplayName) {
		v := strings.TrimSpace(*body.DisplayName)
		displayName = &v
	}
	var inv store.IdentityInvitation
	var token string
	err = s.serializable(ctx, accountConflict, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, taken, err := emailTaken(ctx, q, email); err != nil || taken {
			return cmpOr(err, error(accountExists))
		}
		var err error
		inv, token, err = s.issueInvitation(ctx, q, store.InsertInvitationParams{
			Email:           email,
			NormalizedEmail: normalizeEmail(email),
			Role:            *body.Role,
			DisplayName:     displayName,
			InvitedByUserID: &p.UserID,
		}, s.deps.Clock())
		return err
	})
	if err != nil {
		return refusalOr[gen.PostIdentityOwnerInvitationsResponseObject](err)
	}
	if err := s.deliverInvitation(ctx, inv, token); err != nil {
		return nil, err
	}
	return invitationCreated{
		location: s.deps.Config.BasePath + ownerInvitationsPath + "/" + inv.ID.String(),
		body:     gen.PostIdentityOwnerInvitations201JSONResponse(invitationResponse(inv)),
	}, nil
}

// issueInvitation revokes the pending invitations for inv's email and
// stores inv as its one active invitation, with a fresh id and token, made
// at now and expiring INVITATION_LIFETIME later
// (EA/AuthAccountEndpoints.cs:1076-1146, :1203-1265). Only the token's hash
// is stored; the token itself goes back to the caller for the email alone.
func (s *server) issueInvitation(ctx context.Context, q *store.Queries, inv store.InsertInvitationParams, now time.Time) (store.IdentityInvitation, string, error) {
	if err := q.RevokeActiveInvitationsForEmail(ctx, store.RevokeActiveInvitationsForEmailParams{NormalizedEmail: inv.NormalizedEmail, Now: now}); err != nil {
		return store.IdentityInvitation{}, "", err
	}
	token, hash := newToken()
	inv.ID, inv.TokenHash, inv.CreatedAt, inv.ExpiresAt = uuid.New(), hash, now, now.Add(s.deps.Config.InvitationLifetime)
	row, err := q.InsertInvitation(ctx, inv)
	return row, token, err
}

// deliverInvitation emails inv's link once its transaction has committed
// (EA/AuthAccountEndpoints.cs:1133-1143, :1454-1467). When the send fails,
// nobody holds the token, so the invitation is revoked, even if the caller
// has gone, and the failure is the answer: a 500. The mail driver's error
// stays out of it, because it can name the recipient (go-mail lists the
// affected recipients).
func (s *server) deliverInvitation(ctx context.Context, inv store.IdentityInvitation, token string) error {
	if err := s.deps.Mail.Send(ctx, invitationMail(inv.Email, inviteURL(s.deps.Config.InvitationAcceptURL, token))); err == nil {
		return nil
	}
	if _, err := s.q.RevokeInvitation(context.WithoutCancel(ctx), store.RevokeInvitationParams{ID: inv.ID, Now: s.deps.Clock()}); err != nil {
		return fmt.Errorf("identity: invitation %s could not be sent, nor revoked: %w", inv.ID, err)
	}
	return fmt.Errorf("identity: invitation %s could not be sent and was revoked", inv.ID)
}

// GetIdentityOwnerInvitations lists every invitation, newest first, for an
// Owner (EA/AuthAccountEndpoints.cs:1148-1174).
func (s *server) GetIdentityOwnerInvitations(ctx context.Context, _ gen.GetIdentityOwnerInvitationsRequestObject) (gen.GetIdentityOwnerInvitationsResponseObject, error) {
	rows, err := s.q.ListInvitations(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: list invitations: %w", err)
	}
	out := make(gen.GetIdentityOwnerInvitations200JSONResponse, len(rows))
	for i, inv := range rows {
		out[i] = invitationResponse(inv)
	}
	return out, nil
}

// PostIdentityOwnerInvitationsByIdRevoke revokes a pending invitation for
// an Owner (EA/AuthAccountEndpoints.cs:1176-1201). It is idempotent: an
// accepted or already revoked invitation is answered as it is. 404 for an
// unknown one.
func (s *server) PostIdentityOwnerInvitationsByIdRevoke(ctx context.Context, req gen.PostIdentityOwnerInvitationsByIdRevokeRequestObject) (gen.PostIdentityOwnerInvitationsByIdRevokeResponseObject, error) {
	inv, err := s.q.RevokeInvitation(ctx, store.RevokeInvitationParams{ID: req.Id, Now: s.deps.Clock()})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostIdentityOwnerInvitationsByIdRevoke404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: revoke invitation: %w", err)
	}
	return gen.PostIdentityOwnerInvitationsByIdRevoke200JSONResponse(invitationResponse(inv)), nil
}

// PostIdentityOwnerInvitationsByIdResend sends an invitation again for an
// Owner (EA/AuthAccountEndpoints.cs:1203-1265): 404 for an unknown one, 409
// invitation_not_active for an accepted one. Otherwise a new invitation
// with a new token replaces every pending one for the email, the resent
// one included, and is delivered as a new one is. 200 with the new
// invitation.
func (s *server) PostIdentityOwnerInvitationsByIdResend(ctx context.Context, req gen.PostIdentityOwnerInvitationsByIdResendRequestObject) (gen.PostIdentityOwnerInvitationsByIdResendResponseObject, error) {
	var inv store.IdentityInvitation
	var token string
	err := s.serializable(ctx, accountConflict, func(tx pgx.Tx) error {
		q := store.New(tx)
		orig, err := q.GetInvitation(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound
		}
		if err != nil {
			return err
		}
		if orig.AcceptedAt != nil {
			return refuse(http.StatusConflict, "invitation_not_active", "The invitation is no longer active.", nil)
		}
		inv, token, err = s.issueInvitation(ctx, q, store.InsertInvitationParams{
			Email:           orig.Email,
			NormalizedEmail: orig.NormalizedEmail,
			Role:            orig.Role,
			DisplayName:     orig.DisplayName,
			InvitedByUserID: orig.InvitedByUserID,
		}, s.deps.Clock())
		return err
	})
	if err != nil {
		return refusalOr[gen.PostIdentityOwnerInvitationsByIdResendResponseObject](err)
	}
	if err := s.deliverInvitation(ctx, inv, token); err != nil {
		return nil, err
	}
	return gen.PostIdentityOwnerInvitationsByIdResend200JSONResponse(invitationResponse(inv)), nil
}

// GetIdentityInvitationsValidate tells the invitee's browser whether a
// token can still be accepted, and for which email and role until when
// (EA/AuthAccountEndpoints.cs:1267-1276). Anything else answers
// {valid:false}.
func (s *server) GetIdentityInvitationsValidate(ctx context.Context, req gen.GetIdentityInvitationsValidateRequestObject) (gen.GetIdentityInvitationsValidateResponseObject, error) {
	hash, ok := parseToken(deref(req.Params.Token))
	if !ok {
		return gen.GetIdentityInvitationsValidate200JSONResponse{Valid: false}, nil
	}
	inv, err := s.q.GetActiveInvitationByTokenHash(ctx, store.GetActiveInvitationByTokenHashParams{TokenHash: hash, Now: s.deps.Clock()})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetIdentityInvitationsValidate200JSONResponse{Valid: false}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: validate invitation: %w", err)
	}
	return gen.GetIdentityInvitationsValidate200JSONResponse{Valid: true, Email: &inv.Email, Role: &inv.Role, ExpiresAt: &inv.ExpiresAt}, nil
}

// invitationAcceptedAudit is .NET's invitation.accepted-with-role before
// shape (EA/AuthAccountEndpoints.cs:1278-1368).
type invitationAcceptedAudit struct {
	InvitationID uuid.UUID `json:"InvitationId"`
	Roles        []string  `json:"Roles"`
}

// PostIdentityInvitationsAccept creates the invited account and signs it in
// (EA/AuthAccountEndpoints.cs:1278-1368). A missing token or password is
// 400 invitation_invalid. Then one serializable transaction: the
// invitation is locked FOR UPDATE, and one that is unknown, revoked,
// accepted or expired is 400 invitation_invalid, as is a race lost to a
// concurrent acceptance; an Owner invitation takes the owner lock. The
// account is created as ASP.NET's CreateAsync would (400
// identity_validation_failed for the password policy, an invalid email or
// an existing account), with a confirmed email, the invitation's role, and
// the display name from the request, else the invitation, else the email.
// The invitation is marked accepted, invitation.accepted-with-role is
// audited, and a new session starts: 201 with Location /session.
func (s *server) PostIdentityInvitationsAccept(ctx context.Context, req gen.PostIdentityInvitationsAcceptRequestObject) (gen.PostIdentityInvitationsAcceptResponseObject, error) {
	var body gen.InvitationAcceptanceRequest
	if req.Body != nil {
		body = *req.Body
	}
	if blank(body.Token) || blank(body.Password) {
		return invitationInvalid, nil
	}
	if !blank(body.DisplayName) && utf16Length(strings.TrimSpace(*body.DisplayName)) > maxDisplayNameLength {
		return refuse(http.StatusBadRequest, "invalid_request", invalidRequestMessage, map[string][]string{
			"displayName": {fmt.Sprintf("Display name must be at most %d characters.", maxDisplayNameLength)},
		}), nil
	}
	tokenHash, ok := parseToken(*body.Token)
	if !ok {
		return invitationInvalid, nil
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}

	password := *body.Password
	userID := uuid.New()
	var user gen.AuthUserResponse
	var session string
	err = s.serializable(ctx, invitationInvalid, func(tx pgx.Tx) error {
		q := store.New(tx)
		inv, err := q.LockInvitationByTokenHash(ctx, tokenHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return invitationInvalid
		}
		if err != nil {
			return err
		}
		now := s.deps.Clock()
		if inv.RevokedAt != nil || inv.AcceptedAt != nil || !inv.ExpiresAt.After(now) {
			return invitationInvalid
		}
		if inv.Role == RoleOwner {
			if err := lockOwners(ctx, q); err != nil {
				return err
			}
		}
		displayName := inv.Email
		if inv.DisplayName != nil {
			displayName = *inv.DisplayName
		}
		if !blank(body.DisplayName) {
			displayName = strings.TrimSpace(*body.DisplayName)
		}
		if err := s.checkNewAccount(ctx, q, inv.Email, password); err != nil {
			return err
		}
		hash, err := hashPassword(password)
		if err != nil {
			return err
		}
		if err := q.InsertUser(ctx, store.InsertUserParams{
			ID:              userID,
			Email:           inv.Email,
			NormalizedEmail: normalizeEmail(inv.Email),
			EmailConfirmed:  true,
			DisplayName:     displayName,
			PasswordHash:    &hash,
			Version:         uuid.New(),
			CreatedAt:       now,
			UpdatedAt:       now,
		}); err != nil {
			return err
		}
		if _, err := q.AssignUserRole(ctx, store.AssignUserRoleParams{UserID: userID, RoleID: builtInRoleID(inv.Role)}); err != nil {
			return err
		}
		if err := q.MarkInvitationAccepted(ctx, store.MarkInvitationAcceptedParams{ID: inv.ID, Now: now}); err != nil {
			return err
		}
		after, err := captureUser(ctx, q, userID)
		if err != nil {
			return err
		}
		if err := writeAudit(ctx, q, r, now, auditEvent{
			targetUser: &userID,
			action:     "invitation.accepted-with-role",
			before:     invitationAcceptedAudit{InvitationID: inv.ID, Roles: []string{}},
			after:      after,
		}); err != nil {
			return err
		}
		user = authUser(userID, displayName, inv.Email, orderRoles(after.Roles))
		session, err = s.access.createSession(ctx, tx, userID, false, false, r)
		return err
	})
	if err != nil {
		return refusalOr[gen.PostIdentityInvitationsAcceptResponseObject](err)
	}
	return invitationAccepted{
		cookies:  cookies{s.access.newSessionCookie(session, false)},
		location: s.deps.Config.BasePath + sessionLocation,
		body: gen.AuthSuccessResponse{
			User:                  &user,
			RequiresTwoFactor:     false,
			TwoFactorEnabled:      false,
			MfaEnrollmentRequired: s.mfaEnrollmentRequired(user.Roles, false), // a new account has no TOTP
		},
	}, nil
}

// checkNewAccount refuses an invited account ASP.NET's CreateAsync would
// not create: a password the policy fails, then an invalid email, then an
// email some account already has (the same checks and codes bootstrap
// makes). It returns nil for an account it may create.
func (s *server) checkNewAccount(ctx context.Context, q *store.Queries, email, password string) error {
	if problems := validatePassword(password, s.deps.Config.IsDevelopment()); problems != nil {
		return refuse(http.StatusBadRequest, "identity_validation_failed", invitationNotCreatedMessage, problems)
	}
	if !emailAddressValid(email) {
		return refuse(http.StatusBadRequest, "identity_validation_failed", invitationNotCreatedMessage,
			map[string][]string{"InvalidEmail": {fmt.Sprintf("Email '%s' is invalid.", email)}})
	}
	_, taken, err := emailTaken(ctx, q, email)
	if err != nil {
		return err
	}
	if taken {
		return refuse(http.StatusBadRequest, "identity_validation_failed", invitationNotCreatedMessage, map[string][]string{
			"DuplicateUserName": {fmt.Sprintf("Username '%s' is already taken.", email)},
			"DuplicateEmail":    {fmt.Sprintf("Email '%s' is already taken.", email)},
		})
	}
	return nil
}

// validateInvitationRequest is .NET's ValidateInvitationRequest
// (EA/AuthAccountEndpoints.cs:1480-1488), plus the display-name bound .NET
// left to a database error. It returns the problems, an empty map when
// there are none.
func validateInvitationRequest(b gen.InvitationRequest) map[string][]string {
	fields := map[string][]string{}
	if blank(b.Email) || !strings.Contains(*b.Email, "@") {
		fields["email"] = []string{"A valid email is required."}
	}
	if !isManagedRole(deref(b.Role)) {
		fields["role"] = []string{"Role must be User or Owner."}
	}
	if !blank(b.DisplayName) && utf16Length(strings.TrimSpace(*b.DisplayName)) > maxDisplayNameLength {
		fields["displayName"] = []string{fmt.Sprintf("Display name must be at most %d characters.", maxDisplayNameLength)}
	}
	return fields
}

// invitationResponse is an invitation as the owner endpoints show one
// (EA/AuthAccountEndpoints.cs:1492-1494).
func invitationResponse(inv store.IdentityInvitation) gen.InvitationResponse {
	return gen.InvitationResponse{
		Id:          inv.ID,
		Email:       inv.Email,
		Role:        inv.Role,
		DisplayName: inv.DisplayName,
		CreatedAt:   inv.CreatedAt,
		ExpiresAt:   inv.ExpiresAt,
		RevokedAt:   inv.RevokedAt,
		AcceptedAt:  inv.AcceptedAt,
	}
}

// invitationCreated is the invitation's 201: its Location, then the
// generated body.
type invitationCreated struct {
	location string
	body     gen.PostIdentityOwnerInvitations201JSONResponse
}

func (r invitationCreated) VisitPostIdentityOwnerInvitationsResponse(w http.ResponseWriter) error {
	w.Header().Set("Location", r.location)
	return r.body.VisitPostIdentityOwnerInvitationsResponse(w)
}

// invitationAccepted is acceptance's 201: the new session's cookie and the
// Location of GET /session, then the body.
type invitationAccepted struct {
	cookies
	location string
	body     gen.AuthSuccessResponse
}

func (r invitationAccepted) VisitPostIdentityInvitationsAcceptResponse(w http.ResponseWriter) error {
	r.set(w)
	w.Header().Set("Location", r.location)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	return json.NewEncoder(w).Encode(r.body)
}

// The refusals the invitation endpoints answer with.

func (r refusal) VisitPostIdentityOwnerInvitationsResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityOwnerInvitationsByIdResendResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityInvitationsAcceptResponse(w http.ResponseWriter) error {
	return r.write(w)
}
