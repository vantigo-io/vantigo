package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// SCIM Users: an account and its scim_user_mappings row, correlated by
// externalId (SV/SCIM:111-360).
//
// No SCIM change ends a session by itself. .NET wrote SCIM changes to the
// user and mapping entities directly and never called
// UpdateSecurityStampAsync, and its one SaveChanges interceptor only drops
// a cache entry (SessionStateCache.cs:46-75), so no security stamp rotated
// and rotateSecurityStamp is not called here. A deactivated user who is not
// an Owner is refused at their very next request all the same, because
// session validation reads the mapping on every request while SCIM is
// configured (GetSessionByTokenHash), where .NET's did within 30 s.

// scimUserRow is a SCIM User as the store reads it: the mapping and its
// account.
type scimUserRow = store.GetScimUserRow

// scimStoredProfile is the name parts a mapping keeps (source_profile).
type scimStoredProfile struct {
	GivenName  *string `json:"givenName"`
	FamilyName *string `json:"familyName"`
}

// readStoredProfile is ReadProfile (SV/SCIM:1113-1118): an unreadable
// profile is an empty one.
func readStoredProfile(b []byte) scimStoredProfile {
	var p scimStoredProfile
	_ = json.Unmarshal(b, &p)
	return p
}

// scimUserState is what ApplyUserInput and ApplyUserAction change
// (SV/SCIM:914-940): the account's email and display name, and the
// mapping's userName, externalId, upstream state and name parts. email is
// nil when the User has none.
type scimUserState struct {
	email       *string
	displayName string
	userName    string
	externalID  string
	active      bool
	profile     scimStoredProfile
}

func scimStateOf(row scimUserRow) scimUserState {
	return scimUserState{
		email:       publicEmail(row.Email),
		displayName: row.DisplayName,
		userName:    row.UserName,
		externalID:  row.ExternalID,
		active:      row.UpstreamActive,
		profile:     readStoredProfile(row.SourceProfile),
	}
}

// apply is ApplyUserInput (SV/SCIM:928-940). A replacement also clears an
// email the resource leaves out, and derives the display name from the
// name parts when it names no display name but a part.
func (st *scimUserState) apply(in scimUserInput, replace bool) {
	st.userName = *in.userName
	if in.profile.email != nil || replace {
		st.email = in.profile.email
	}
	switch part := coalesce(in.profile.givenName, in.profile.familyName); {
	case in.displayName != nil:
		st.displayName = *in.displayName
	case replace && part != nil && !dotnetBlank(*part):
		st.displayName = scimDisplayName(in)
	}
	st.externalID = *in.externalID
	if in.active != nil {
		st.active = *in.active
	}
	if in.profile.givenName != nil {
		st.profile.GivenName = in.profile.givenName
	}
	if in.profile.familyName != nil {
		st.profile.FamilyName = in.profile.familyName
	}
}

// coalesce is C#'s a ?? b.
func coalesce(a, b *string) *string {
	if a != nil {
		return a
	}
	return b
}

// applyAction is ApplyUserAction (SV/SCIM:914-926). Removing active or the
// display name leaves them as they are.
func (st *scimUserState) applyAction(a scimUserAction) {
	switch a.field {
	case "userName":
		st.userName = *a.value
	case "externalId":
		st.externalID = *normalizeExternalID(a.value)
	case "active":
		if !a.remove {
			st.active = *a.active
		}
	case "displayName":
		if !a.remove {
			st.displayName = *a.value
		}
	case "email":
		st.email = a.value
	case "givenName":
		st.profile.GivenName = a.value
	case "familyName":
		st.profile.FamilyName = a.value
	}
}

// scimDisplayName is DisplayNameFor (SV/SCIM:1156-1164): the display name,
// else the name parts, else the userName, trimmed and cut to 200
// characters.
func scimDisplayName(in scimUserInput) string {
	value := ""
	if in.displayName != nil {
		value = *in.displayName
	}
	if dotnetBlank(value) {
		var parts []string
		for _, p := range []*string{in.profile.givenName, in.profile.familyName} {
			if p != nil && !dotnetBlank(*p) {
				parts = append(parts, *p)
			}
		}
		value = strings.Join(parts, " ")
	}
	if dotnetBlank(value) {
		value = *in.userName
	}
	return truncateUTF16(dotnetTrim(value), maxDisplayNameLength)
}

// scimStoredEmail is the address the account keeps for email. .NET stored
// no email for a User whose resource names none; identity.users requires
// one, so such an account gets a reserved address at sso.invalid, unique by
// the resource id, which no response shows (publicEmail) and no mail
// reaches.
func scimStoredEmail(email *string, resourceID uuid.UUID) string {
	if email == nil || dotnetBlank(*email) {
		return "scim-" + resourceID.String() + "@" + opaqueEmailDomain
	}
	return *email
}

// scimUserFacts is ScimUserAuditFacts (SV/SCIM:1206-1207), a User event's
// before and after.
type scimUserFacts struct {
	ConnectionID             uuid.UUID   `json:"ConnectionId"`
	ResourceID               string      `json:"ResourceId"`
	Active                   bool        `json:"Active"`
	UpstreamGroupResourceIDs []uuid.UUID `json:"UpstreamGroupResourceIds"`
}

// scimUserFactsOf is CaptureUserFactsAsync (SV/SCIM:987-997).
func scimUserFactsOf(ctx context.Context, q *store.Queries, row scimUserRow) (scimUserFacts, error) {
	groups, err := q.ScimUserGroupIDs(ctx, row.UserID)
	if err != nil {
		return scimUserFacts{}, fmt.Errorf("identity: SCIM user facts: %w", err)
	}
	return scimUserFacts{ConnectionID: scimConnectionID, ResourceID: row.ResourceID.String(), Active: row.UpstreamActive, UpstreamGroupResourceIDs: nonNil(groups)}, nil
}

// scimUser is ReadUserResourceAsync (SV/SCIM:586-610): the name when either
// part is set, the work email when there is one, and meta with the ETag.
func (s *server) scimUser(row scimUserRow) gen.ScimUser {
	profile := readStoredProfile(row.SourceProfile)
	u := gen.ScimUser{
		Schemas:     []string{scimUserSchema},
		Id:          row.ResourceID.String(),
		ExternalId:  row.ExternalID,
		UserName:    row.UserName,
		Active:      row.UpstreamActive,
		DisplayName: row.DisplayName,
		Meta: gen.ScimMeta{
			ResourceType: "User",
			Created:      row.CreatedAt.UTC(),
			LastModified: row.UpdatedAt.UTC(),
			Location:     s.scimUserLocation(row.ResourceID),
			Version:      row.Etag,
		},
	}
	if profile.GivenName != nil || profile.FamilyName != nil {
		u.Name = &gen.ScimName{GivenName: profile.GivenName, FamilyName: profile.FamilyName}
	}
	if email := publicEmail(row.Email); email != nil && !dotnetBlank(*email) {
		u.Emails = &[]gen.ScimEmail{{Value: *email, Type: "work", Primary: true}}
	}
	return u
}

// validateScimUser is ValidateUserInputAsync (SV/SCIM:677-696) for the User
// resource current (the nil uuid for a new one): safe values, then the
// userName and externalId no other mapping has.
func validateScimUser(ctx context.Context, q *store.Queries, in scimUserInput, current uuid.UUID) error {
	switch {
	case !isSafeValue(in.userName, 512) || !isSafeValue(in.externalID, 512):
		return scimInvalidValue("userName and externalId must be non-empty safe values.")
	case in.displayName != nil && !isSafeValue(in.displayName, maxDisplayNameLength):
		return scimInvalidValue("displayName is invalid.")
	case in.profile.givenName != nil && !isSafeValue(in.profile.givenName, 200) ||
		in.profile.familyName != nil && !isSafeValue(in.profile.familyName, 200):
		return scimInvalidValue("name values are invalid.")
	case in.profile.email != nil && !validMailAddress(*in.profile.email):
		return scimInvalidValue("The work email is invalid.")
	}
	taken, err := q.ScimUserNameTaken(ctx, store.ScimUserNameTakenParams{UserName: *in.userName, ResourceID: current})
	if err != nil {
		return err
	}
	if taken {
		return scimError{409, "uniqueness", "userName is already used by this connection."}
	}
	taken, err = q.ScimExternalIDTaken(ctx, store.ScimExternalIDTakenParams{ExternalID: *in.externalID, ResourceID: current})
	if err != nil {
		return err
	}
	if taken {
		return scimError{409, "uniqueness", "externalId is already used by this connection."}
	}
	return nil
}

// refuseScimOwner is IsScimControllableUserAsync's refusal
// (SV/ScimLifecycleService.cs:46-54, SV/SCIM:160-161): SCIM never changes an
// Owner, the installation's break-glass account.
func refuseScimOwner(ctx context.Context, q *store.Queries, userID uuid.UUID) error {
	owner, err := q.IsOwner(ctx, userID)
	if err != nil {
		return err
	}
	if owner {
		return scimProtected
	}
	return nil
}

// lockScimUser holds the User id names: 404 for an unknown one.
func lockScimUser(ctx context.Context, q *store.Queries, id string) (scimUserRow, error) {
	resourceID, ok := scimUserID(id)
	if !ok {
		return scimUserRow{}, scimNotFound
	}
	row, err := q.LockScimUser(ctx, resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return scimUserRow{}, scimNotFound
	}
	return scimUserRow(row), err
}

// saveScimUser writes after over before on q: the account when its email
// or display name changed, with a new version, and the mapping with a new
// ETag, as Touch did (SV/SCIM:965-971). A DELETE (deleted) also records
// that upstream no longer lists the user in any SCIM group, as
// DeleteUserAsync did (:346-352). An active:false does not: .NET's active
// action and input set only UpstreamActive (:920, :937), and session
// validation refuses the inactive user anyway, so a directory that
// disables and later re-enables a user leaves its groups as they were.
func saveScimUser(ctx context.Context, q *store.Queries, before scimUserRow, after scimUserState, now time.Time, deleted bool) (scimUserRow, error) {
	email := scimStoredEmail(after.email, before.ResourceID)
	if email != before.Email || after.displayName != before.DisplayName {
		if err := q.UpdateScimUserAccount(ctx, store.UpdateScimUserAccountParams{
			Email:           email,
			NormalizedEmail: normalizeEmail(email),
			DisplayName:     after.displayName,
			Version:         uuid.New(),
			Now:             now,
			ID:              before.UserID,
		}); err != nil {
			return scimUserRow{}, err
		}
	}
	profile, err := json.Marshal(after.profile)
	if err != nil {
		return scimUserRow{}, err
	}
	if err := q.UpdateScimMapping(ctx, store.UpdateScimMappingParams{
		ExternalID:     after.externalID,
		UserName:       after.userName,
		UpstreamActive: after.active,
		SourceProfile:  profile,
		Etag:           newScimETag(),
		Now:            now,
		ResourceID:     before.ResourceID,
	}); err != nil {
		return scimUserRow{}, err
	}
	if deleted {
		if err := q.MarkScimUserMembershipsAbsent(ctx, before.UserID); err != nil {
			return scimUserRow{}, err
		}
	}
	return q.GetScimUser(ctx, before.ResourceID)
}

// scimUserChange runs change on the User id names, in one SCIM transaction
// and .NET's order: 404, 412 for a stale If-Match, 400 mutability for an
// Owner (SV/SCIM:241-247, :286-292, :332-338). change returns the state to
// save, deleted says the change is a DELETE (saveScimUser), and the change
// is audited as action, with the User's facts before and after.
func (s *server) scimUserChange(ctx context.Context, id string, ifMatch *string, action string, deleted bool,
	change func(q *store.Queries, row scimUserRow) (scimUserState, error)) (scimUserRow, error) {
	_, r, err := scimRequest(ctx)
	if err != nil {
		return scimUserRow{}, err
	}
	var saved scimUserRow
	err = s.scimTx(ctx, func(q *store.Queries, now time.Time) error {
		row, err := lockScimUser(ctx, q, id)
		if err != nil {
			return err
		}
		if err := scimPrecondition(ifMatch, row.Etag); err != nil {
			return err
		}
		if err := refuseScimOwner(ctx, q, row.UserID); err != nil {
			return err
		}
		after, err := change(q, row)
		if err != nil {
			return err
		}
		before, err := scimUserFactsOf(ctx, q, row)
		if err != nil {
			return err
		}
		if saved, err = saveScimUser(ctx, q, row, after, now, deleted); err != nil {
			return err
		}
		facts, err := scimUserFactsOf(ctx, q, saved)
		if err != nil {
			return err
		}
		return writeScimAudit(ctx, q, r, now, action, &row.UserID, before, facts)
	})
	if db.IsUniqueViolation(err, "") {
		return scimUserRow{}, scimUserConflict
	}
	return saved, err
}

// PostIdentityScimV2Users is CreateUserAsync (SV/SCIM:111-182). The
// resource is read and validated first; an externalId the request leaves
// out is a new GUID. A userName or externalId another mapping has is 409
// uniqueness, before the transaction, as .NET checked it (:120-121). In the
// transaction the externalId is looked up again: a create that raced
// another for it replaces the User the other made (:156-162), unless it is
// an Owner; a new User is confirmed, not disabled, and has no role, group
// or password (:137-155). Either way it is audited scim.user.created, and
// the answer is 201 with the User, its Location and its ETag.
func (s *server) PostIdentityScimV2Users(ctx context.Context, _ gen.PostIdentityScimV2UsersRequestObject) (gen.PostIdentityScimV2UsersResponseObject, error) {
	in, r, err := scimRequest(ctx)
	if err != nil {
		return nil, err
	}
	input, err := readScimUser(in.body)
	if err != nil {
		return scimOr[gen.PostIdentityScimV2UsersResponseObject](err)
	}
	if input.externalID == nil {
		input.externalID = ptr(uuid.NewString())
	}
	if err := validateScimUser(ctx, s.q, input, uuid.Nil); err != nil {
		return scimOr[gen.PostIdentityScimV2UsersResponseObject](err)
	}
	var saved scimUserRow
	err = s.scimTx(ctx, func(q *store.Queries, now time.Time) error {
		existing, err := q.LockScimUserByExternalID(ctx, *input.externalID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			saved, err = createScimUser(ctx, q, input, now)
		case err == nil:
			saved, err = replaceRacedScimUser(ctx, q, scimUserRow(existing), input, now)
		}
		if err != nil {
			return err
		}
		facts, err := scimUserFactsOf(ctx, q, saved)
		if err != nil {
			return err
		}
		return writeScimAudit(ctx, q, r, now, "scim.user.created", &saved.UserID,
			scimCreatedBefore{ScimConnectionID: scimConnectionID, ResourceID: saved.ResourceID.String()}, facts)
	})
	if db.IsUniqueViolation(err, "") {
		err = scimUserConflict
	}
	if err != nil {
		return scimOr[gen.PostIdentityScimV2UsersResponseObject](err)
	}
	location := s.scimUserLocation(saved.ResourceID)
	return gen.PostIdentityScimV2Users201ApplicationScimPlusJSONResponse{
		Body:    s.scimUser(saved),
		Headers: gen.PostIdentityScimV2Users201ResponseHeaders{ETag: quotedETag(saved.Etag), Location: &location},
	}, nil
}

// createScimUser makes a new User (SV/SCIM:137-155). UserManager.CreateAsync
// validated the account first; of its rules, those for the email hold here,
// since identity keeps a unique email (RequireUniqueEmail,
// EA/AuthServiceCollectionExtensions.cs:28): an email is required and must
// hold one '@', neither first nor last (EmailAddressAttribute), or it is
// 400 invalidValue InvalidEmail, and another account's is DuplicateEmail
// (:145-147, :1204). Its user-name rules went with ASP.NET's user names
// (spec *Data model*); the userName is the mapping's, which no other
// mapping may have, rechecked here in case a create raced this one.
func createScimUser(ctx context.Context, q *store.Queries, in scimUserInput, now time.Time) (scimUserRow, error) {
	email := in.profile.email
	if email == nil || dotnetBlank(*email) || !oneAtSign(*email) {
		return scimUserRow{}, scimInvalidValue("InvalidEmail")
	}
	if _, taken, err := emailTaken(ctx, q, *email); err != nil || taken {
		return scimUserRow{}, cmpOr(err, error(scimInvalidValue("DuplicateEmail")))
	}
	if taken, err := q.ScimUserNameTaken(ctx, store.ScimUserNameTakenParams{UserName: *in.userName, ResourceID: uuid.Nil}); err != nil || taken {
		return scimUserRow{}, cmpOr(err, error(scimUserConflict))
	}
	st := scimUserState{displayName: scimDisplayName(in), active: true}
	st.apply(in, false)
	userID, resourceID := uuid.New(), uuid.New()
	if err := q.InsertUser(ctx, store.InsertUserParams{
		ID:              userID,
		Email:           *st.email,
		NormalizedEmail: normalizeEmail(*st.email),
		EmailConfirmed:  true,
		DisplayName:     st.displayName,
		Version:         uuid.New(),
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		return scimUserRow{}, err
	}
	profile, err := json.Marshal(st.profile)
	if err != nil {
		return scimUserRow{}, err
	}
	if err := q.InsertScimMapping(ctx, store.InsertScimMappingParams{
		ResourceID:     resourceID,
		UserID:         userID,
		ExternalID:     st.externalID,
		UserName:       st.userName,
		UpstreamActive: st.active,
		SourceProfile:  profile,
		Etag:           newScimETag(),
		Now:            now,
	}); err != nil {
		return scimUserRow{}, err
	}
	return q.GetScimUser(ctx, resourceID)
}

// oneAtSign is EmailAddressAttribute.IsValid: exactly one '@', neither the
// first character nor the last.
func oneAtSign(s string) bool {
	i := strings.IndexByte(s, '@')
	return i > 0 && i != len(s)-1 && i == strings.LastIndexByte(s, '@')
}

// replaceRacedScimUser is CreateUserAsync's replacement of the User another
// create made for the same externalId (SV/SCIM:156-164): refused for an
// Owner, otherwise replaced as PUT replaces. It gets a new ETag, as every
// change does here; .NET's left the one the other create gave it.
func replaceRacedScimUser(ctx context.Context, q *store.Queries, row scimUserRow, in scimUserInput, now time.Time) (scimUserRow, error) {
	if err := refuseScimOwner(ctx, q, row.UserID); err != nil {
		return scimUserRow{}, err
	}
	st := scimStateOf(row)
	st.apply(in, true)
	if taken, err := q.ScimUserNameTaken(ctx, store.ScimUserNameTakenParams{UserName: st.userName, ResourceID: row.ResourceID}); err != nil || taken {
		return scimUserRow{}, cmpOr(err, error(scimUserConflict))
	}
	return saveScimUser(ctx, q, row, st, now, false)
}

// GetIdentityScimV2UsersById is GetUserAsync (SV/SCIM:184-194): the User
// with its ETag, or 404.
func (s *server) GetIdentityScimV2UsersById(ctx context.Context, req gen.GetIdentityScimV2UsersByIdRequestObject) (gen.GetIdentityScimV2UsersByIdResponseObject, error) {
	id, ok := scimUserID(req.Id)
	if !ok {
		return scimNotFound, nil
	}
	row, err := s.q.GetScimUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return scimNotFound, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: read SCIM user: %w", err)
	}
	return gen.GetIdentityScimV2UsersById200ApplicationScimPlusJSONResponse{
		Body:    s.scimUser(row),
		Headers: gen.GetIdentityScimV2UsersById200ResponseHeaders{ETag: quotedETag(row.Etag)},
	}, nil
}

// GetIdentityScimV2Users is ListUsersAsync (SV/SCIM:196-232): paging, then
// the filter on userName and externalId, then a page of the Users it
// admits, by resource id.
func (s *server) GetIdentityScimV2Users(ctx context.Context, _ gen.GetIdentityScimV2UsersRequestObject) (gen.GetIdentityScimV2UsersResponseObject, error) {
	in, _, err := scimRequest(ctx)
	if err != nil {
		return nil, err
	}
	startIndex, count, err := scimPaging(in.query)
	if err != nil {
		return scimOr[gen.GetIdentityScimV2UsersResponseObject](err)
	}
	raw, _ := queryValue(in.query, "filter")
	clauses, err := scimFilter(raw, "userName", "externalId")
	if err != nil {
		return scimOr[gen.GetIdentityScimV2UsersResponseObject](err)
	}
	userNames, externalIDs := scimFilterValues(clauses, "userName")
	total, err := s.q.CountScimUsers(ctx, store.CountScimUsersParams{UserNames: userNames, ExternalIds: externalIDs})
	if err != nil {
		return nil, fmt.Errorf("identity: count SCIM users: %w", err)
	}
	rows, err := s.q.ListScimUsers(ctx, store.ListScimUsersParams{UserNames: userNames, ExternalIds: externalIDs, Skip: int64(startIndex - 1), Take: int64(count)})
	if err != nil {
		return nil, fmt.Errorf("identity: list SCIM users: %w", err)
	}
	resources := make([]gen.ScimUser, 0, len(rows))
	for _, row := range rows {
		resources = append(resources, s.scimUser(scimUserRow(row)))
	}
	return gen.GetIdentityScimV2Users200ApplicationScimPlusJSONResponse{
		Schemas:      []string{scimListSchema},
		TotalResults: int32(total),
		StartIndex:   int32(startIndex),
		ItemsPerPage: int32(len(resources)),
		Resources:    resources,
	}, nil
}

// PutIdentityScimV2UsersById is PutUserAsync (SV/SCIM:234-277): the
// resource is read first; then 404, 412, 400 mutability for an Owner, the
// externalId kept when the resource leaves it out, and validation. A
// changed externalId is 409 mutability: the correlation key is immutable
// (spec *SCIM 2.0*), where .NET let a PUT change it. The User is replaced
// and audited scim.user.replaced; active:false makes it upstream-inactive
// and leaves its group memberships alone.
func (s *server) PutIdentityScimV2UsersById(ctx context.Context, req gen.PutIdentityScimV2UsersByIdRequestObject) (gen.PutIdentityScimV2UsersByIdResponseObject, error) {
	in, _, err := scimRequest(ctx)
	if err != nil {
		return nil, err
	}
	input, err := readScimUser(in.body)
	if err != nil {
		return scimOr[gen.PutIdentityScimV2UsersByIdResponseObject](err)
	}
	saved, err := s.scimUserChange(ctx, req.Id, req.Params.IfMatch, "scim.user.replaced", false, func(q *store.Queries, row scimUserRow) (scimUserState, error) {
		if input.externalID == nil {
			input.externalID = &row.ExternalID
		}
		if err := validateScimUser(ctx, q, input, row.ResourceID); err != nil {
			return scimUserState{}, err
		}
		if *input.externalID != row.ExternalID {
			return scimUserState{}, scimExternalIDImmutable
		}
		st := scimStateOf(row)
		st.apply(input, true)
		return st, nil
	})
	if err != nil {
		return scimOr[gen.PutIdentityScimV2UsersByIdResponseObject](err)
	}
	return gen.PutIdentityScimV2UsersById200ApplicationScimPlusJSONResponse{
		Body:    s.scimUser(saved),
		Headers: gen.PutIdentityScimV2UsersById200ResponseHeaders{ETag: quotedETag(saved.Etag)},
	}, nil
}

// PatchIdentityScimV2UsersById is PatchUserAsync (SV/SCIM:279-325): the
// PatchOp is read first; then 404, 412, 400 mutability for an Owner, the
// operations validated (scimUserActions), and 409 mutability for an
// externalId other than the mapping's. .NET compared only the first
// externalId operation, so a second could change the key; every one is
// compared here. A userName another mapping has is 409 uniqueness, which
// .NET's unique user-name index answered. The actions are applied in order
// and audited scim.user.patched; setting active false makes the User
// upstream-inactive and leaves its group memberships alone.
func (s *server) PatchIdentityScimV2UsersById(ctx context.Context, req gen.PatchIdentityScimV2UsersByIdRequestObject) (gen.PatchIdentityScimV2UsersByIdResponseObject, error) {
	in, _, err := scimRequest(ctx)
	if err != nil {
		return nil, err
	}
	ops, err := readScimPatch(in.body)
	if err != nil {
		return scimOr[gen.PatchIdentityScimV2UsersByIdResponseObject](err)
	}
	saved, err := s.scimUserChange(ctx, req.Id, req.Params.IfMatch, "scim.user.patched", false, func(q *store.Queries, row scimUserRow) (scimUserState, error) {
		actions, err := scimUserActions(ops)
		if err != nil {
			return scimUserState{}, err
		}
		for _, a := range actions {
			if a.field == "externalId" && *normalizeExternalID(a.value) != row.ExternalID {
				return scimUserState{}, scimExternalIDImmutable
			}
		}
		st := scimStateOf(row)
		for _, a := range actions {
			st.applyAction(a)
		}
		if st.userName != row.UserName {
			taken, err := q.ScimUserNameTaken(ctx, store.ScimUserNameTakenParams{UserName: st.userName, ResourceID: row.ResourceID})
			if err != nil || taken {
				return scimUserState{}, cmpOr(err, error(scimUserConflict))
			}
		}
		return st, nil
	})
	if err != nil {
		return scimOr[gen.PatchIdentityScimV2UsersByIdResponseObject](err)
	}
	return gen.PatchIdentityScimV2UsersById200ApplicationScimPlusJSONResponse{
		Body:    s.scimUser(saved),
		Headers: gen.PatchIdentityScimV2UsersById200ResponseHeaders{ETag: quotedETag(saved.Etag)},
	}, nil
}

// DeleteIdentityScimV2UsersById is DeleteUserAsync (SV/SCIM:327-360), a soft
// delete: after 404, 412 and 400 mutability for an Owner, the User is no
// longer upstream-active and upstream no longer lists it in any SCIM group.
// The account and mapping stay, and a later GET shows active:false. Audited
// scim.user.deleted; 204 with the new ETag.
func (s *server) DeleteIdentityScimV2UsersById(ctx context.Context, req gen.DeleteIdentityScimV2UsersByIdRequestObject) (gen.DeleteIdentityScimV2UsersByIdResponseObject, error) {
	saved, err := s.scimUserChange(ctx, req.Id, req.Params.IfMatch, "scim.user.deleted", true, func(_ *store.Queries, row scimUserRow) (scimUserState, error) {
		st := scimStateOf(row)
		st.active = false
		return st, nil
	})
	if err != nil {
		return scimOr[gen.DeleteIdentityScimV2UsersByIdResponseObject](err)
	}
	return gen.DeleteIdentityScimV2UsersById204Response{Headers: gen.DeleteIdentityScimV2UsersById204ResponseHeaders{ETag: quotedETag(saved.Etag)}}, nil
}
