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

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// The announced account (Entra design D4; delivery B plan, Task 3). Staff
// has an external provider create a person's account and announces it
// (Expect), so that the user the provider's SCIM push or the person's first
// sign-in creates is adopted as Staff-managed, numbered and not pending,
// with no Owner in between. An announcement is matched on arrival by three
// keys, in order: the employee number (the enterprise extension's
// employeeNumber, by contracts.NormalizeEmployeeNumber's key), the user
// principal name (SCIM's userName, normalised as emails are), and the
// provider's object id (SCIM's externalId, or OIDC's oid under the issuer the
// announcement names). An arrival consumes the announcement it fulfils;
// Unexpect withdraws one nobody fulfilled.
//
// Every write here runs under the owner lock: Expect in the provider's own
// serializable transaction (write), the SCIM create after the SCIM lock, the
// OIDC completion in its serializable transaction — a ReplaceUserID may name
// anyone's pending user, and the lock keeps one order with every other
// writer of users. The number only ever reaches identity.users from the
// announcement Staff made, never from a SCIM body.

// The bounds of an announcement's columns (identity.expected_accounts).
const (
	maxExpectedIssuerLength   = 200
	maxExpectedObjectIDLength = 200
	maxExpectedUPNLength      = 254
)

// The keys an adoption names as account.adopted-from-provider's matchedBy.
const (
	matchedByEmployeeNumber = "employeeNumber"
	matchedByUserName       = "userName"
	matchedByExternalID     = "externalId"
	matchedByOID            = "oid"
)

// cleanExpected is an ExpectedAccount as identity stores it.
type cleanExpected struct {
	number, displayName      string
	language, issuer, object *string
	userPrincipalName        *string
	replace                  *uuid.UUID
}

// validExpected trims s and judges it: the employee number in Staff's
// alphabet (normalizeEmployeeNumber), the display name 1 to 200 characters,
// the language en, nb or none, the issuer (normalizeIssuer's form) and the
// object id at most 200 characters, the user principal name an address
// (emailAddressValid) of at most 254 characters, stored normalised as
// emails are (normalizeEmail) — and at least one of the object id and the
// user principal name, the keys an arrival can carry. The object id is
// normalised as SCIM's externalId is (normalizeExternalID: a GUID in its
// lower-case dashed form), so the two compare equal.
func validExpected(s contracts.ExpectedAccount) (cleanExpected, error) {
	out := cleanExpected{displayName: strings.TrimSpace(s.DisplayName), replace: s.ReplaceUserID}
	number, ok := normalizeEmployeeNumber(s.EmployeeNumber)
	if !ok {
		return out, fmt.Errorf("%w: the employee number", errInvalidSpec)
	}
	out.number = number
	if n := utf16Length(out.displayName); n == 0 || n > maxDisplayNameLength || strings.ContainsFunc(out.displayName, unicode.IsControl) {
		return out, fmt.Errorf("%w: the display name", errInvalidSpec)
	}
	switch lang := strings.TrimSpace(s.PreferredLanguage); lang {
	case "":
	case "en", "nb":
		out.language = &lang
	default:
		return out, fmt.Errorf("%w: the language", errInvalidSpec)
	}
	if raw := strings.TrimSpace(s.Issuer); raw != "" {
		issuer, ok := normalizeIssuer(raw)
		if !ok || utf16Length(issuer) > maxExpectedIssuerLength {
			return out, fmt.Errorf("%w: the issuer", errInvalidSpec)
		}
		out.issuer = &issuer
	}
	if raw := strings.TrimSpace(s.ObjectID); raw != "" {
		object := normalizeExternalID(&raw)
		if utf16Length(*object) > maxExpectedObjectIDLength || strings.ContainsFunc(*object, unicode.IsControl) {
			return out, fmt.Errorf("%w: the object id", errInvalidSpec)
		}
		out.object = object
	}
	if raw := strings.TrimSpace(s.UserPrincipalName); raw != "" {
		if !emailAddressValid(raw) || utf16Length(raw) > maxExpectedUPNLength || strings.ContainsFunc(raw, unicode.IsControl) {
			return out, fmt.Errorf("%w: the user principal name", errInvalidSpec)
		}
		upn := normalizeEmail(raw)
		out.userPrincipalName = &upn
	}
	if out.object == nil && out.userPrincipalName == nil {
		return out, fmt.Errorf("%w: neither an object id nor a user principal name", errInvalidSpec)
	}
	if out.replace != nil && *out.replace == uuid.Nil {
		return out, fmt.Errorf("%w: the user to replace", errInvalidSpec)
	}
	return out, nil
}

// expectedAudit is account.expected's after: the number, the names of the
// match keys the announcement carries beside it (never their values: a
// user principal name is an address), and the pending user it replaces.
type expectedAudit struct {
	EmployeeNumber string     `json:"employeeNumber"`
	Keys           []string   `json:"keys"`
	ReplaceUserID  *uuid.UUID `json:"replaceUserId"`
}

// unexpectedAudit is account.unexpected's before: the number withdrawn.
type unexpectedAudit struct {
	EmployeeNumber string `json:"employeeNumber"`
}

// adoptedAudit is account.adopted-from-provider's after: the key the
// arrival matched by, the announcement's number, and the pending user the
// arrival completed rather than created.
type adoptedAudit struct {
	MatchedBy      string     `json:"matchedBy"`
	EmployeeNumber string     `json:"employeeNumber"`
	ReplacedUserID *uuid.UUID `json:"replacedUserId,omitempty"`
}

// Expect records or replaces the announcement for spec's employee number
// (Entra design D4), in write's transaction under the owner lock. spec is
// validated first (validExpected: errInvalidSpec). Then the user holding the
// number, by its key, decides:
//
//   - an unmanaged user is ErrEmployeeNumberTaken;
//   - a managed Owner or SystemAdmin is ErrAccountPrivileged;
//   - a managed user who is not pending has arrived already, or an Owner
//     adopted them: nothing is recorded and the answer is nil (plan reading
//     6), so the worker's next Prepare finds them;
//   - a managed, pending user without a credential is recorded only when
//     spec.ReplaceUserID names that very user, else errInvalidSpec.
//
// A ReplaceUserID naming anyone but such a holder — a user not managed, not
// pending, with a credential, or holding another number or none — is
// errInvalidSpec. Issuer "" is filled from the single sign-on row while OIDC
// is on: the configured authority normalised, the issuer every OIDC link and
// so the oid match compares by (plan reading 8). The same announcement again
// changes nothing; any change replaces it whole and is audited
// account.expected with the number, the names of its keys and the user it
// replaces — never an address.
func (p *provisioning) Expect(ctx context.Context, actor contracts.Actor, spec contracts.ExpectedAccount) error {
	return p.writeIn(ctx, actor, false, func(q *store.Queries, _ string, now time.Time) error {
		s, err := validExpected(spec)
		if err != nil {
			return err
		}
		key := contracts.NormalizeEmployeeNumber(s.number)
		holder, err := q.GetUserIDByEmployeeNumber(ctx, key)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if s.replace != nil {
				return fmt.Errorf("%w: the user to replace does not hold the number", errInvalidSpec)
			}
		case err != nil:
			return err
		default:
			u, err := lockUser(ctx, q, holder)
			if err != nil {
				return err
			}
			switch {
			case u.ManagedBy == nil:
				return contracts.ErrEmployeeNumberTaken
			case u.Privileged:
				return contracts.ErrAccountPrivileged
			case u.PendingSince == nil:
				return nil
			case s.replace == nil || *s.replace != u.ID || hasCredential(u):
				return fmt.Errorf("%w: a pending user holds the number and is not the one to replace", errInvalidSpec)
			}
		}
		if s.issuer == nil {
			settings, err := readSSOSettings(ctx, q, nil)
			if err != nil {
				return err
			}
			if settings.OIDC != nil {
				issuer, ok := normalizeIssuer(settings.OIDC.Authority)
				if !ok || utf16Length(issuer) > maxExpectedIssuerLength {
					return fmt.Errorf("identity: the OIDC authority cannot be an announcement's issuer")
				}
				s.issuer = &issuer
			}
		}
		current, err := q.GetExpectedAccountByNumber(ctx, key)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		case sameExpected(current, s):
			return nil
		}
		by, system := auditActor(actor)
		var bySystem *string
		if system != "" {
			bySystem = &system
		}
		if err := q.UpsertExpectedAccount(ctx, store.UpsertExpectedAccountParams{
			EmployeeNumberKey: key, EmployeeNumber: s.number, DisplayName: s.displayName, PreferredLanguage: s.language,
			Issuer: s.issuer, ObjectID: s.object, UserPrincipalName: s.userPrincipalName, ReplaceUserID: s.replace,
			Now: now, CreatedByUserID: by, CreatedBySystem: bySystem,
		}); err != nil {
			return err
		}
		keys := []string{}
		if s.object != nil {
			keys = append(keys, "objectId")
		}
		if s.userPrincipalName != nil {
			keys = append(keys, "userPrincipalName")
		}
		return writeAudit(ctx, q, nil, now, auditEvent{
			actor: by, system: system, targetUser: s.replace, action: "account.expected",
			after: expectedAudit{EmployeeNumber: s.number, Keys: keys, ReplaceUserID: s.replace},
		})
	})
}

// sameExpected reports whether the stored announcement row already says
// what s does.
func sameExpected(row store.IdentityExpectedAccount, s cleanExpected) bool {
	return row.EmployeeNumber == s.number && row.DisplayName == s.displayName &&
		equalPtr(row.PreferredLanguage, s.language) && equalPtr(row.Issuer, s.issuer) &&
		equalPtr(row.ObjectID, s.object) && equalPtr(row.UserPrincipalName, s.userPrincipalName) &&
		equalPtr(row.ReplaceUserID, s.replace)
}

// equalPtr reports whether a and b are both nil or point at equal values.
func equalPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// hasCredential reports whether u can sign in by themselves.
func hasCredential(u provisionedUser) bool {
	return u.HasPassword || u.HasPasskey || u.HasOidcLink
}

// Unexpect withdraws the announcement for the employee number, by its key,
// under the owner lock. Nothing to withdraw — never announced, or fulfilled
// already, which consumed it — is nil and writes nothing; a withdrawal is
// audited account.unexpected with the number as the announcement held it.
func (p *provisioning) Unexpect(ctx context.Context, actor contracts.Actor, employeeNumber string) error {
	return p.writeIn(ctx, actor, false, func(q *store.Queries, _ string, now time.Time) error {
		number, ok := normalizeEmployeeNumber(employeeNumber)
		if !ok {
			return fmt.Errorf("%w: the employee number", errInvalidSpec)
		}
		key := contracts.NormalizeEmployeeNumber(number)
		row, err := q.GetExpectedAccountByNumber(ctx, key)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := q.DeleteExpectedAccount(ctx, key); err != nil {
			return err
		}
		by, system := auditActor(actor)
		return writeAudit(ctx, q, nil, now, auditEvent{
			actor: by, system: system, targetUser: row.ReplaceUserID, action: "account.unexpected",
			before: unexpectedAudit{EmployeeNumber: row.EmployeeNumber},
		})
	})
}

// expectedMatch is the announcement an arrival fulfils and the key it
// matched by.
type expectedMatch struct {
	expected store.IdentityExpectedAccount
	by       string
}

// firstExpected is the announcement get finds, or nil for none.
func firstExpected(row store.IdentityExpectedAccount, err error, by string) (*expectedMatch, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &expectedMatch{expected: row, by: by}, nil
}

// scimExpected is the announcement the SCIM resource in matches, by the
// first key that names one: the enterprise employeeNumber's key, then the
// userName normalised as emails are against the user principal name, then
// the externalId (normalised already) against the object id.
func scimExpected(ctx context.Context, q *store.Queries, in scimUserInput) (*expectedMatch, error) {
	if e := in.enterprise; e != nil && e.EmployeeNumber != nil {
		if key := contracts.NormalizeEmployeeNumber(*e.EmployeeNumber); key != "" {
			row, err := q.GetExpectedAccountByNumber(ctx, key)
			if m, err := firstExpected(row, err, matchedByEmployeeNumber); m != nil || err != nil {
				return m, err
			}
		}
	}
	upn := normalizeEmail(*in.userName)
	row, err := q.GetExpectedAccountByUPN(ctx, &upn)
	if m, err := firstExpected(row, err, matchedByUserName); m != nil || err != nil {
		return m, err
	}
	if in.externalID == nil {
		return nil, nil
	}
	row, err = q.GetExpectedAccountByObjectID(ctx, in.externalID)
	return firstExpected(row, err, matchedByExternalID)
}

// adoptable reads who holds the announcement's number and judges whether
// the arrival may fulfil it: with no ReplaceUserID, when nobody holds the
// number; with one, when that user holds it and is still managed, not
// privileged, pending and without a credential — locked, then, and
// answered. Anything else (an Owner adopted someone with the number
// meanwhile, the pending user accepted their invitation) is no adoption:
// the arrival is what it is without an announcement.
func adoptable(ctx context.Context, q *store.Queries, e store.IdentityExpectedAccount) (ok bool, replaced *provisionedUser, err error) {
	holder, err := q.GetUserIDByEmployeeNumber(ctx, e.EmployeeNumberKey)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return e.ReplaceUserID == nil, nil, nil
	case err != nil:
		return false, nil, err
	case e.ReplaceUserID == nil || holder != *e.ReplaceUserID:
		return false, nil, nil
	}
	u, err := lockUser(ctx, q, holder)
	if errors.Is(err, contracts.ErrUserGone) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	if u.ManagedBy == nil || u.Privileged || u.PendingSince == nil || hasCredential(u) ||
		contracts.NormalizeEmployeeNumber(deref(u.EmployeeNumber)) != e.EmployeeNumberKey {
		return false, nil, nil
	}
	return true, &u, nil
}

// completeExpectedUser completes the pending user u an announcement names to
// replace, on q: their acceptable invitations revoked replaced, no longer
// pending, no invitation spell, and the address email the arrival brought.
func completeExpectedUser(ctx context.Context, q *store.Queries, u provisionedUser, email string, now time.Time) error {
	if err := q.RevokeUserInvitations(ctx, store.RevokeUserInvitationsParams{UserID: &u.ID, Now: now, RevokedReason: revokedReplaced}); err != nil {
		return err
	}
	return q.CompleteExpectedUser(ctx, store.CompleteExpectedUserParams{
		ID: u.ID, Email: email, NormalizedEmail: normalizeEmail(email), Version: uuid.New(), Now: now,
	})
}

// consumeExpected deletes the announcement m fulfilled and audits the
// adoption of userID, account.adopted-from-provider, on q.
func consumeExpected(ctx context.Context, q *store.Queries, r *http.Request, m expectedMatch, userID uuid.UUID, replaced *uuid.UUID, now time.Time) error {
	if _, err := q.DeleteExpectedAccount(ctx, m.expected.EmployeeNumberKey); err != nil {
		return err
	}
	return writeAudit(ctx, q, r, now, auditEvent{
		targetUser: &userID, action: "account.adopted-from-provider",
		after: adoptedAudit{MatchedBy: m.by, EmployeeNumber: m.expected.EmployeeNumber, ReplacedUserID: replaced},
	})
}

// adoptExpectedScimUser is SCIM's create of in when an announcement matches
// it (scimExpected) and the arrival may fulfil it (adoptable), on q, the
// create's transaction, which holds the SCIM lock and the owner lock: a new
// user as createScimUser makes one, Staff-managed with the announcement's
// number; or, for a ReplaceUserID, that pending user completed — the
// mapping attached to them, their address the resource's work email (by
// createScimUser's rules: InvalidEmail, DuplicateEmail when another user
// holds it), their display name and is_disabled as they were. The
// announcement is consumed and the adoption audited. adopted is false, and
// nothing written, when no announcement may be fulfilled.
func adoptExpectedScimUser(ctx context.Context, q *store.Queries, r *http.Request, in scimUserInput, now time.Time) (row scimUserRow, adopted bool, err error) {
	m, err := scimExpected(ctx, q, in)
	if err != nil || m == nil {
		return scimUserRow{}, false, err
	}
	ok, replaced, err := adoptable(ctx, q, m.expected)
	if err != nil || !ok {
		return scimUserRow{}, false, err
	}
	if replaced == nil {
		row, err = createScimUser(ctx, q, in, now, &m.expected)
		if err != nil {
			return scimUserRow{}, false, err
		}
		return row, true, consumeExpected(ctx, q, r, *m, row.UserID, nil, now)
	}
	email := in.profile.email
	if email == nil || dotnetBlank(*email) || !oneAtSign(*email) {
		return scimUserRow{}, false, scimInvalidValue("InvalidEmail")
	}
	if holder, taken, err := emailTaken(ctx, q, *email); err != nil || taken && holder.ID != replaced.ID {
		return scimUserRow{}, false, cmpOr(err, error(scimInvalidValue("DuplicateEmail")))
	}
	if taken, err := q.ScimUserNameTaken(ctx, store.ScimUserNameTakenParams{UserName: *in.userName, ResourceID: uuid.Nil}); err != nil || taken {
		return scimUserRow{}, false, cmpOr(err, error(scimUserConflict))
	}
	st := scimUserState{active: true}
	st.apply(in, false)
	if err := completeExpectedUser(ctx, q, *replaced, *email, now); err != nil {
		return scimUserRow{}, false, err
	}
	resourceID := uuid.New()
	if err := insertScimMapping(ctx, q, resourceID, replaced.ID, st, now); err != nil {
		return scimUserRow{}, false, err
	}
	if row, err = q.GetScimUser(ctx, resourceID); err != nil {
		return scimUserRow{}, false, err
	}
	return row, true, consumeExpected(ctx, q, r, *m, replaced.ID, &replaced.ID, now)
}

// oidcExpected is the announcement naming a's object id under a's issuer,
// read on q, or nil: none names the object id, or the newest that does was
// made for another issuer (or none) — an oid is the provider's, and only the
// issuer the announcement names may fulfil it.
func oidcExpected(ctx context.Context, q *store.Queries, a externalAccount) (*expectedMatch, error) {
	if a.objectID == "" {
		return nil, nil
	}
	row, err := q.GetExpectedAccountByObjectID(ctx, &a.objectID)
	m, err := firstExpected(row, err, matchedByOID)
	if err != nil || m == nil || m.expected.Issuer == nil || *m.expected.Issuer != a.issuer {
		return nil, err
	}
	return m, nil
}

// adoptExpectedOIDCAccount is OIDC's completion of a when an announcement
// names its object id under its issuer (oidcExpected) and the arrival may
// fulfil it (adoptable), on q, provisionOIDCAccount's serializable
// transaction under the owner lock: a new user as that makes one — the User
// role and the link — Staff-managed with the announcement's number, in its
// language; or, for a ReplaceUserID, that pending user completed: linked,
// their address the email claim's when there is one (another user holding
// it is errOIDCProvisioningLost, as in provisionOIDCAccount; the user being
// completed holding it is the completion), else kept. The announcement is
// consumed and the adoption audited with matchedBy oid. adopted is false,
// and nothing written, when no announcement may be fulfilled.
func adoptExpectedOIDCAccount(ctx context.Context, q *store.Queries, r *http.Request, a externalAccount, newID uuid.UUID, now time.Time) (userID uuid.UUID, adopted bool, err error) {
	m, err := oidcExpected(ctx, q, a)
	if err != nil || m == nil {
		return uuid.Nil, false, err
	}
	ok, replaced, err := adoptable(ctx, q, m.expected)
	if err != nil || !ok {
		return uuid.Nil, false, err
	}
	if replaced == nil {
		if err := insertOIDCUser(ctx, q, a, newID, m.expected.PreferredLanguage, now); err != nil {
			return uuid.Nil, false, err
		}
		if err := setStaffManaged(ctx, q, newID, &m.expected.EmployeeNumber, now); err != nil {
			return uuid.Nil, false, err
		}
		if err := q.InsertOidcLink(ctx, store.InsertOidcLinkParams{Issuer: a.issuer, Subject: a.subject, UserID: newID, Now: now}); err != nil {
			return uuid.Nil, false, err
		}
		return newID, true, consumeExpected(ctx, q, r, *m, newID, nil, now)
	}
	email := replaced.Email
	if a.email != "" {
		holder, taken, err := emailTaken(ctx, q, a.email)
		if err != nil {
			return uuid.Nil, false, err
		}
		if taken && holder.ID != replaced.ID {
			return uuid.Nil, false, errOIDCProvisioningLost
		}
		email = a.email
	}
	if err := completeExpectedUser(ctx, q, *replaced, email, now); err != nil {
		return uuid.Nil, false, err
	}
	if err := q.InsertOidcLink(ctx, store.InsertOidcLinkParams{Issuer: a.issuer, Subject: a.subject, UserID: replaced.ID, Now: now}); err != nil {
		return uuid.Nil, false, err
	}
	return replaced.ID, true, consumeExpected(ctx, q, r, *m, replaced.ID, &replaced.ID, now)
}
