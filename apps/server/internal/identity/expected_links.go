package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// The announced link (Entra phase 2 design L1–L4; delivery A). A user who
// already exists cannot be linked to the workforce OIDC provider by hand:
// identity.oidc_links is keyed on the token's sub, which the directory never
// returns, and a SCIM mapping is the provider's push. An Owner therefore
// announces the directory account the user holds — its object id, its user
// principal name, or both, under the configured issuer — and the user's
// next arrival fulfils it: the OIDC completion on the token's oid under its
// issuer, the SCIM create on userName against the principal name, then
// externalId against the object id. The arrival then links or maps the
// named user as a regular provisioning would, and the email claim is not
// consulted: an address never links an identity to an existing account (the
// takeover guard), and the Owner's word is the one authorised exception.
//
// An announced link outranks an announced account (expected_accounts.go):
// one names a user who exists, the other a user who does not, and an
// arrival never fulfils both. Every write runs under the owner lock in a
// serializable transaction, as every other writer of users does.

var (
	// ssoNotConfigured refuses announcing or disconnecting while OIDC is
	// off: there is no issuer to announce under.
	ssoNotConfigured = refuse(http.StatusConflict, "sso_not_configured", "Single sign-on is not configured.", nil)
	// accountLinked refuses announcing a user who is linked already.
	accountLinked = refuse(http.StatusConflict, "account_linked", "This user is already connected to the provider.", nil)
	// ssoLinkTaken refuses a directory account another user's announcement,
	// or an account Staff announced, already names.
	ssoLinkTaken = refuse(http.StatusConflict, "sso_link_taken", "Another user is already connected to this account.", nil)
)

// The link states OwnerUserResponse.ssoLink reports.
const (
	ssoLinkLinked   = gen.OwnerSsoLinkState("linked")
	ssoLinkExpected = gen.OwnerSsoLinkState("expected")
	ssoLinkNone     = gen.OwnerSsoLinkState("none")
)

// cleanExpectedLink is an OwnerSsoLinkRequest as identity stores it.
type cleanExpectedLink struct {
	object, userPrincipalName *string
}

// validExpectedLink trims body and judges it: the object id at most 200
// characters without a control character, normalised as SCIM's externalId is
// (a GUID in lower-case dashed form); the user principal name an address of
// at most 254 characters, normalised as emails are — and at least one of
// the two, the keys an arrival can carry. fields names what is wrong, per
// field, for 400 invalid_request.
func validExpectedLink(body gen.OwnerSsoLinkRequest) (cleanExpectedLink, map[string][]string) {
	var out cleanExpectedLink
	fields := map[string][]string{}
	if raw := strings.TrimSpace(deref(body.ObjectId)); raw != "" {
		object := normalizeExternalID(&raw)
		if utf16Length(*object) > maxExpectedObjectIDLength || strings.ContainsFunc(*object, unicode.IsControl) {
			fields["objectId"] = []string{"An object id of at most 200 characters is required."}
		} else {
			out.object = object
		}
	}
	if raw := strings.TrimSpace(deref(body.UserPrincipalName)); raw != "" {
		if !emailAddressValid(raw) || utf16Length(raw) > maxExpectedUPNLength || strings.ContainsFunc(raw, unicode.IsControl) {
			fields["userPrincipalName"] = []string{"A user principal name that is an address of at most 254 characters is required."}
		} else {
			upn := normalizeEmail(raw)
			out.userPrincipalName = &upn
		}
	}
	if len(fields) == 0 && out.object == nil && out.userPrincipalName == nil {
		fields["userPrincipalName"] = []string{"A user principal name or an object id is required."}
	}
	if len(fields) > 0 {
		return out, fields
	}
	return out, nil
}

// expectedLinkAudit is account.link-expected's after: the names of the keys
// the announcement carries, never their values (a principal name is an
// address).
type expectedLinkAudit struct {
	Keys []string `json:"keys"`
}

// unlinkedAudit is account.unlinked's before: how many links under the
// configured issuer went.
type unlinkedAudit struct {
	Links int64 `json:"links"`
}

// linkedFromProviderAudit is account.linked-from-provider's after: the key
// the arrival matched the announcement by.
type linkedFromProviderAudit struct {
	MatchedBy string `json:"matchedBy"`
}

// keysOf names the keys an announcement carries, for the audit.
func keysOf(object, upn *string) []string {
	keys := []string{}
	if object != nil {
		keys = append(keys, "objectId")
	}
	if upn != nil {
		keys = append(keys, "userPrincipalName")
	}
	return keys
}

// PostIdentityOwnerUsersByIdSsoLink announces the directory account another
// user holds, for an Owner (design L2). After validation (400
// invalid_request, per field) and the self check, one serializable
// transaction under the owner lock judges in order: an unknown user is 404;
// OIDC off is 409 sso_not_configured; a pending user 409 account_pending
// (their way in is the invitation, or the announced account that replaces
// them); a user linked under the issuer 409 account_linked; a key another
// user's announcement or an announced account names 409 sso_link_taken —
// also when only the unique indexes see a concurrent announcement. A
// disabled user, an Owner and a SystemAdmin may be announced. The same
// announcement again changes nothing; any change replaces it whole, audited
// account.link-expected with the names of its keys.
func (s *server) PostIdentityOwnerUsersByIdSsoLink(ctx context.Context, req gen.PostIdentityOwnerUsersByIdSsoLinkRequestObject) (gen.PostIdentityOwnerUsersByIdSsoLinkResponseObject, error) {
	var body gen.OwnerSsoLinkRequest
	if req.Body != nil {
		body = *req.Body
	}
	link, fields := validExpectedLink(body)
	if fields != nil {
		return refuse(http.StatusBadRequest, "invalid_request", userInvalidMessage, fields), nil
	}
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err := rejectSelf(p.UserID, req.Id); err != nil {
		return refusalOr[gen.PostIdentityOwnerUsersByIdSsoLinkResponseObject](err)
	}
	// Read before the transaction, which then holds its connection
	// without waiting for another.
	issuer, err := s.oidcIssuer(ctx)
	if err != nil {
		return nil, err
	}
	var out gen.OwnerUserResponse
	err = s.serializable(ctx, ssoLinkTaken, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := lockOwners(ctx, q); err != nil {
			return err
		}
		target, err := managedTarget(ctx, q, req.Id)
		if err != nil {
			return err
		}
		if issuer == nil {
			return ssoNotConfigured
		}
		if isPending(target) {
			return accountPending
		}
		linked, err := q.HasOidcLinkUnderIssuer(ctx, store.HasOidcLinkUnderIssuerParams{UserID: target.ID, Issuer: *issuer})
		if err != nil || linked {
			return cmpOr(err, error(accountLinked))
		}
		if _, err := q.GetExpectedLinkTaker(ctx, store.GetExpectedLinkTakerParams{
			UserID: target.ID, Issuer: *issuer, ObjectID: link.object, UserPrincipalName: link.userPrincipalName,
		}); err == nil {
			return ssoLinkTaken
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if named, err := q.ExpectedAccountNamesKey(ctx, store.ExpectedAccountNamesKeyParams{ObjectID: link.object, UserPrincipalName: link.userPrincipalName}); err != nil || named {
			return cmpOr(err, error(ssoLinkTaken))
		}
		now := s.deps.Clock()
		current, err := q.GetExpectedLinkByUser(ctx, target.ID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		case current.Issuer == *issuer && equalPtr(current.ObjectID, link.object) && equalPtr(current.UserPrincipalName, link.userPrincipalName):
			out, err = s.managedUser(ctx, q, target.ID, now, issuer)
			return err
		}
		if err := q.UpsertExpectedLink(ctx, store.UpsertExpectedLinkParams{
			UserID: target.ID, Issuer: *issuer, ObjectID: link.object, UserPrincipalName: link.userPrincipalName,
			Now: now, CreatedByUserID: p.UserID,
		}); err != nil {
			return err
		}
		if err := writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &target.ID, action: "account.link-expected",
			after: expectedLinkAudit{Keys: keysOf(link.object, link.userPrincipalName)}, mfa: p.MFAVerified,
		}); err != nil {
			return err
		}
		out, err = s.managedUser(ctx, q, target.ID, now, issuer)
		return err
	})
	if err != nil {
		return refusalOr[gen.PostIdentityOwnerUsersByIdSsoLinkResponseObject](err)
	}
	return gen.PostIdentityOwnerUsersByIdSsoLink200JSONResponse(out), nil
}

// DeleteIdentityOwnerUsersByIdSsoLink disconnects another user from the
// configured provider, for an Owner (design L2): in one serializable
// transaction under the owner lock, their announced link is withdrawn when
// there is one (audited account.link-unexpected) and every link of theirs
// under the issuer removed (audited account.unlinked with the count). A
// SCIM mapping is the provider's record and stays. Nothing to remove is 200
// with the user unchanged; an unknown user is 404; the caller's own account
// 400; OIDC off 409 sso_not_configured.
func (s *server) DeleteIdentityOwnerUsersByIdSsoLink(ctx context.Context, req gen.DeleteIdentityOwnerUsersByIdSsoLinkRequestObject) (gen.DeleteIdentityOwnerUsersByIdSsoLinkResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return nil, err
	}
	if err := rejectSelf(p.UserID, req.Id); err != nil {
		return refusalOr[gen.DeleteIdentityOwnerUsersByIdSsoLinkResponseObject](err)
	}
	issuer, err := s.oidcIssuer(ctx)
	if err != nil {
		return nil, err
	}
	var out gen.OwnerUserResponse
	err = s.serializable(ctx, userConflict, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := lockOwners(ctx, q); err != nil {
			return err
		}
		target, err := managedTarget(ctx, q, req.Id)
		if err != nil {
			return err
		}
		if issuer == nil {
			return ssoNotConfigured
		}
		now := s.deps.Clock()
		withdrawn, err := q.DeleteExpectedLink(ctx, target.ID)
		if err != nil {
			return err
		}
		if withdrawn > 0 {
			if err := writeAudit(ctx, q, r, now, auditEvent{
				actor: &p.UserID, targetUser: &target.ID, action: "account.link-unexpected", mfa: p.MFAVerified,
			}); err != nil {
				return err
			}
		}
		links, err := q.DeleteOidcLinksByUser(ctx, store.DeleteOidcLinksByUserParams{UserID: target.ID, Issuer: *issuer})
		if err != nil {
			return err
		}
		if links > 0 {
			if err := writeAudit(ctx, q, r, now, auditEvent{
				actor: &p.UserID, targetUser: &target.ID, action: "account.unlinked",
				before: unlinkedAudit{Links: links}, mfa: p.MFAVerified,
			}); err != nil {
				return err
			}
		}
		out, err = s.managedUser(ctx, q, target.ID, now, issuer)
		return err
	})
	if err != nil {
		return refusalOr[gen.DeleteIdentityOwnerUsersByIdSsoLinkResponseObject](err)
	}
	return gen.DeleteIdentityOwnerUsersByIdSsoLink200JSONResponse(out), nil
}

// ssoLinkOf is OwnerUserResponse.ssoLink for the listed user u under issuer:
// nil while OIDC is off; linked when a link exists under it; expected when
// the user's announcement is under it (one under another issuer fulfils
// nothing and reads as none); else none.
func ssoLinkOf(u store.ListManagedUsersRow, issuer *string) *gen.OwnerSsoLink {
	if issuer == nil {
		return nil
	}
	if u.SsoEnabled {
		return &gen.OwnerSsoLink{State: ssoLinkLinked}
	}
	if u.ExpectedIssuer != nil && *u.ExpectedIssuer == *issuer {
		// The principal name is stored in normalizeEmail's upper-case form
		// for the match; an address is shown lower-cased.
		var upn *string
		if u.ExpectedUpn != nil {
			lower := strings.ToLower(*u.ExpectedUpn)
			upn = &lower
		}
		return &gen.OwnerSsoLink{State: ssoLinkExpected, ObjectId: u.ExpectedObjectID, UserPrincipalName: upn, ExpectedAt: u.ExpectedAt}
	}
	return &gen.OwnerSsoLink{State: ssoLinkNone}
}

// expectedLinkMatch is the announced link an arrival fulfils and the key it
// matched by.
type expectedLinkMatch struct {
	link store.IdentityExpectedLink
	by   string
}

// firstExpectedLink is the announced link get found, or nil for none.
func firstExpectedLink(row store.IdentityExpectedLink, err error, by string) (*expectedLinkMatch, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &expectedLinkMatch{link: row, by: by}, nil
}

// matchedByPrincipalName is account.linked-from-provider's matchedBy for an
// OIDC sign-in matched on the token's principal name.
const matchedByPrincipalName = "principalName"

// oidcExpectedLink is the announced link a fulfils under a's issuer, read
// on q, by the first key that names one: the object id (the oid claim),
// then the principal name (readPrincipalName) normalised as emails are
// against the announced one; nil for none.
func oidcExpectedLink(ctx context.Context, q *store.Queries, a externalAccount) (*expectedLinkMatch, error) {
	if a.objectID != "" {
		row, err := q.GetExpectedLinkByObjectID(ctx, store.GetExpectedLinkByObjectIDParams{Issuer: a.issuer, ObjectID: &a.objectID})
		if m, err := firstExpectedLink(row, err, matchedByOID); m != nil || err != nil {
			return m, err
		}
	}
	if a.principalName == "" {
		return nil, nil
	}
	upn := normalizeEmail(a.principalName)
	row, err := q.GetExpectedLinkByUPN(ctx, store.GetExpectedLinkByUPNParams{Issuer: a.issuer, UserPrincipalName: &upn})
	return firstExpectedLink(row, err, matchedByPrincipalName)
}

// scimExpectedLink is the announced link the SCIM resource in matches, under
// issuer (the configured one, normalised), by the first key that names
// one: the userName normalised as emails are against the principal name,
// then the externalId (normalised already) against the object id. No
// configured issuer is no match: the announcement names one.
func scimExpectedLink(ctx context.Context, q *store.Queries, in scimUserInput, issuer *string) (*expectedLinkMatch, error) {
	if issuer == nil {
		return nil, nil
	}
	upn := normalizeEmail(*in.userName)
	row, err := q.GetExpectedLinkByUPN(ctx, store.GetExpectedLinkByUPNParams{Issuer: *issuer, UserPrincipalName: &upn})
	if m, err := firstExpectedLink(row, err, matchedByUserName); m != nil || err != nil {
		return m, err
	}
	if in.externalID == nil {
		return nil, nil
	}
	row, err = q.GetExpectedLinkByObjectID(ctx, store.GetExpectedLinkByObjectIDParams{Issuer: *issuer, ObjectID: in.externalID})
	return firstExpectedLink(row, err, matchedByExternalID)
}

// consumeExpectedLink deletes the announcement m fulfilled and audits the
// link of its user, account.linked-from-provider, on q.
func consumeExpectedLink(ctx context.Context, q *store.Queries, r *http.Request, m expectedLinkMatch, now time.Time) error {
	if _, err := q.DeleteExpectedLink(ctx, m.link.UserID); err != nil {
		return err
	}
	return writeAudit(ctx, q, r, now, auditEvent{
		targetUser: &m.link.UserID, action: "account.linked-from-provider",
		after: linkedFromProviderAudit{MatchedBy: m.by},
	})
}

// fulfilExpectedOIDCLink is OIDC's completion of a when an announced link
// names it under its issuer (oidcExpectedLink; design L3): in one
// serializable transaction under the owner lock, the named user — who must
// still exist and hold no link under the issuer, else nothing is written
// and fulfilled is false — is linked (issuer, sub), the announcement
// consumed and the link audited with the key matched by. The email claim is
// not consulted, and the user's address, name and roles are kept. The
// announcement is looked for outside the transaction first, so that a
// sign-in nobody announced never waits for the owner lock; the
// transaction reads it again.
func (s *server) fulfilExpectedOIDCLink(ctx context.Context, r *http.Request, a externalAccount) (fulfilled bool, err error) {
	if m, err := oidcExpectedLink(ctx, s.q, a); err != nil || m == nil {
		return false, err
	}
	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := lockOwners(ctx, q); err != nil {
			return err
		}
		m, err := oidcExpectedLink(ctx, q, a)
		if err != nil || m == nil {
			return err
		}
		u, err := lockUser(ctx, q, m.link.UserID)
		if err != nil {
			return err
		}
		if linked, err := q.HasOidcLinkUnderIssuer(ctx, store.HasOidcLinkUnderIssuerParams{UserID: u.ID, Issuer: a.issuer}); err != nil || linked {
			return err
		}
		if err := q.InsertOidcLink(ctx, store.InsertOidcLinkParams{Issuer: a.issuer, Subject: a.subject, UserID: u.ID, Now: now}); err != nil {
			return err
		}
		fulfilled = true
		return consumeExpectedLink(ctx, q, r, *m, now)
	})
	return fulfilled, err
}

// fulfilExpectedScimLink is SCIM's create of in when an announced link
// matches it (scimExpectedLink) and its user holds no SCIM mapping, on q,
// the create's transaction under the SCIM lock and the owner lock (design
// L3): the resource is mapped to that user as a create maps one —
// upstream_active, the profile and the ETag — and the user's address
// becomes the resource's work email when it carries one that no other user
// holds (another user's is DuplicateEmail; none keeps the address), since a
// mapped user's profile is the provider's from here on. The display name is
// kept. The announcement is consumed and the link audited. fulfilled is
// false, and nothing written, when no announcement may be fulfilled.
func fulfilExpectedScimLink(ctx context.Context, q *store.Queries, r *http.Request, in scimUserInput, issuer *string, now time.Time) (row scimUserRow, fulfilled bool, err error) {
	m, err := scimExpectedLink(ctx, q, in, issuer)
	if err != nil || m == nil {
		return scimUserRow{}, false, err
	}
	u, err := lockUser(ctx, q, m.link.UserID)
	if err != nil {
		return scimUserRow{}, false, err
	}
	if mapped, err := q.HasScimMapping(ctx, u.ID); err != nil || mapped {
		return scimUserRow{}, false, err
	}
	if taken, err := q.ScimUserNameTaken(ctx, store.ScimUserNameTakenParams{UserName: *in.userName, ResourceID: uuid.Nil}); err != nil || taken {
		return scimUserRow{}, false, cmpOr(err, error(scimUserConflict))
	}
	if email := in.profile.email; email != nil && !dotnetBlank(*email) && oneAtSign(*email) {
		holder, taken, err := emailTaken(ctx, q, *email)
		if err != nil {
			return scimUserRow{}, false, err
		}
		if taken && holder.ID != u.ID {
			return scimUserRow{}, false, scimInvalidValue("DuplicateEmail")
		}
		if !taken {
			if err := q.UpdateScimUserAccount(ctx, store.UpdateScimUserAccountParams{
				Email: *email, NormalizedEmail: normalizeEmail(*email), DisplayName: u.DisplayName,
				Version: uuid.New(), Now: now, ID: u.ID,
			}); err != nil {
				return scimUserRow{}, false, err
			}
		}
	}
	st := scimUserState{active: true}
	st.apply(in, false)
	resourceID := uuid.New()
	if err := insertScimMapping(ctx, q, resourceID, u.ID, st, now); err != nil {
		return scimUserRow{}, false, err
	}
	if row, err = q.GetScimUser(ctx, resourceID); err != nil {
		return scimUserRow{}, false, err
	}
	return row, true, consumeExpectedLink(ctx, q, r, *m, now)
}

func (r refusal) VisitPostIdentityOwnerUsersByIdSsoLinkResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitDeleteIdentityOwnerUsersByIdSsoLinkResponse(w http.ResponseWriter) error {
	return r.write(w)
}

// scimIssuer is the configured OIDC issuer for a SCIM arrival's announced
// link match: the authority normalised, or nil while OIDC is off.
func (s *server) scimIssuer(ctx context.Context) (*string, error) {
	issuer, err := s.oidcIssuer(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: SCIM announced link: %w", err)
	}
	return issuer, nil
}
