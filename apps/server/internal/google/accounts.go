package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is the Accounts tab's operations (Google Workspace design G17,
// G20): the accounts and the groups the module keeps, read from its own
// rows; the confirmation of an adopted account or group; and the account
// rules, under If-Match.

// codeAdoptionConfirmed is the 409 of a confirmation with nothing to
// confirm.
const codeAdoptionConfirmed = "adoption_confirmed"

// actionSettingsChanged audits PUT /connection/settings.
const actionSettingsChanged = "google.settings.changed"

// The account rules' bounds.
const (
	maxOrgUnitPath      = 400
	minPassLifetimeDays = 1
	maxPassLifetimeDays = 30
)

// domainPattern is a domain name as an address's.
var domainPattern = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)+$`)

// accountBody is an account's row as the contract answers it; the password
// is never answered, only whether one waits.
func accountBody(a store.GoogleAccount, s *server) gen.GoogleAccount {
	return gen.GoogleAccount{
		UserId:              a.UserID,
		EmployeeNumber:      a.EmployeeNumber,
		PrimaryEmail:        a.PrimaryEmail,
		State:               gen.GoogleAccountState(a.State),
		CreatedByUs:         a.CreatedByUs,
		Licences:            orEmpty(a.LicencesAssigned),
		Reported:            orEmpty(a.Reported),
		PassWaiting:         livePass(a, s.deps.Clock()),
		LastError:           a.LastError,
		LastSyncedAt:        a.LastSyncedAt,
		AdoptionConfirmedAt: a.AdoptionConfirmedAt,
		CreatedAt:           a.CreatedAt,
	}
}

// GetGoogleAccounts answers every account the module keeps, by address,
// from its own rows. (GET /api/v1/google/accounts)
func (s *server) GetGoogleAccounts(ctx context.Context, _ gen.GetGoogleAccountsRequestObject) (gen.GetGoogleAccountsResponseObject, error) {
	rows, err := s.q.ListAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("google: list the accounts: %w", err)
	}
	out := make(gen.GetGoogleAccounts200JSONResponse, 0, len(rows))
	for _, a := range rows {
		out = append(out, accountBody(a, s))
	}
	return out, nil
}

// PostGoogleAccountsByUserIdConfirm confirms the adoption of a user the
// module found rather than made: from now on it writes to it as to one it
// made, and Staff's next cycle brings it to the person. Audited
// google.account.adoption_confirmed. 404 for an account the module does not
// keep, 409 adoption_confirmed for one confirmed already or one the module
// created. (POST /api/v1/google/accounts/{userId}/confirm)
func (s *server) PostGoogleAccountsByUserIdConfirm(ctx context.Context, req gen.PostGoogleAccountsByUserIdConfirmRequestObject) (gen.PostGoogleAccountsByUserIdConfirmResponseObject, error) {
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	if !validUserID(req.UserId) {
		return gen.PostGoogleAccountsByUserIdConfirm404Response{}, nil
	}
	now := s.deps.Clock()
	var (
		row   store.GoogleAccount
		found bool
	)
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		row, err = q.ConfirmAccountAdoption(ctx, store.ConfirmAccountAdoptionParams{Now: now, ConfirmedByUserID: actor.UserID, UserID: req.UserId})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			_, err := q.GetAccount(ctx, req.UserId)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				return nil
			case err != nil:
				return fmt.Errorf("google: read the account: %w", err)
			}
			found = true
			return refusal{problem(http.StatusConflict, codeAdoptionConfirmed, "Nothing to confirm",
				"The account's adoption is confirmed already, or Vantigo created the account.")}
		case err != nil:
			return fmt.Errorf("google: confirm the account's adoption: %w", err)
		}
		found = true
		return recordEvent(ctx, q, now, actor, actionAccountAdoptionConfirmed, row.UserID, nil)
	})
	var r refusal
	switch {
	case errors.As(err, &r):
		return gen.PostGoogleAccountsByUserIdConfirm409ApplicationProblemPlusJSONResponse(r.problem), nil
	case err != nil:
		return nil, err
	case !found:
		return gen.PostGoogleAccountsByUserIdConfirm404Response{}, nil
	}
	return gen.PostGoogleAccountsByUserIdConfirm200JSONResponse(accountBody(row, s)), nil
}

// groupBody is a group's row as the contract answers it.
func groupBody(g store.GoogleGroup) gen.GoogleGroup {
	return gen.GoogleGroup{
		GroupId: g.GroupID, StaffGroupId: g.StaffGroupID, Email: g.Email, CreatedByUs: g.CreatedByUs,
		AdoptionConfirmedAt: g.AdoptionConfirmedAt, CreatedAt: g.CreatedAt,
	}
}

// GetGoogleGroups answers every group the module keeps, by address, from
// its own rows. (GET /api/v1/google/groups)
func (s *server) GetGoogleGroups(ctx context.Context, _ gen.GetGoogleGroupsRequestObject) (gen.GetGoogleGroupsResponseObject, error) {
	rows, err := s.q.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("google: list the groups: %w", err)
	}
	out := make(gen.GetGoogleGroups200JSONResponse, 0, len(rows))
	for _, g := range rows {
		out = append(out, groupBody(g))
	}
	return out, nil
}

// PostGoogleGroupsByGroupIdConfirm confirms the adoption of a group the
// module found holding a Staff group's address rather than made. Idempotent:
// a group confirmed already, or one the module made, is answered as it is.
// Audited google.group.adoption_confirmed, once. 404 for a group the module
// does not keep. (POST /api/v1/google/groups/{groupId}/confirm)
func (s *server) PostGoogleGroupsByGroupIdConfirm(ctx context.Context, req gen.PostGoogleGroupsByGroupIdConfirmRequestObject) (gen.PostGoogleGroupsByGroupIdConfirmResponseObject, error) {
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	if !groupIDPattern.MatchString(req.GroupId) {
		return gen.PostGoogleGroupsByGroupIdConfirm404Response{}, nil
	}
	now := s.deps.Clock()
	var (
		row   store.GoogleGroup
		found bool
	)
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		before, err := q.GetGroup(ctx, req.GroupId)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("google: read the group: %w", err)
		}
		found = true
		if row, err = q.ConfirmGroupAdoption(ctx, store.ConfirmGroupAdoptionParams{Now: now, ConfirmedByUserID: *actor.UserID, GroupID: req.GroupId}); err != nil {
			return fmt.Errorf("google: confirm the group's adoption: %w", err)
		}
		if before.CreatedByUs || before.AdoptionConfirmedAt != nil {
			return nil
		}
		return recordEvent(ctx, q, now, actor, actionGroupAdoptionConfirmed, row.GroupID, nil)
	})
	switch {
	case err != nil:
		return nil, err
	case !found:
		return gen.PostGoogleGroupsByGroupIdConfirm404Response{}, nil
	}
	return gen.PostGoogleGroupsByGroupIdConfirm200JSONResponse(groupBody(row)), nil
}

// PutGoogleConnectionSettings sets the account rules (G17, G19) under
// If-Match; a field left out keeps its value. The address domain must be one
// of the verified domains once a test has read them (any domain before
// that), or empty for the primary one; the organisational unit a path from
// /; the password's lifetime 1 to 30 days. The same values again write
// nothing. Audited google.settings.changed with the names of what changed.
// (PUT /api/v1/google/connection/settings)
func (s *server) PutGoogleConnectionSettings(ctx context.Context, req gen.PutGoogleConnectionSettingsRequestObject) (gen.PutGoogleConnectionSettingsResponseObject, error) {
	version, ok := parseVersion(req.Params.IfMatch)
	if !ok {
		return gen.PutGoogleConnectionSettings400ApplicationProblemPlusJSONResponse(badIfMatch()), nil
	}
	var in gen.GoogleSettingsRequest
	if req.Body != nil {
		in = *req.Body
	}
	var f fieldErrors
	if in.AddressPattern != nil && !in.AddressPattern.Valid() {
		f.add("addressPattern", "The pattern is given.family, g.family or given.")
	}
	if in.PassDelivery != nil && !in.PassDelivery.Valid() {
		f.add("passDelivery", "Who hands the password over is hr, manager or both.")
	}
	if in.LeaverRemoveGroups != nil && !in.LeaverRemoveGroups.Valid() {
		f.add("leaverRemoveGroups", "The groups a leaver leaves are all, managed or none.")
	}
	if in.PassLifetimeDays != nil && (*in.PassLifetimeDays < minPassLifetimeDays || *in.PassLifetimeDays > maxPassLifetimeDays) {
		f.add("passLifetimeDays", fmt.Sprintf("The password's lifetime is %d to %d days.", minPassLifetimeDays, maxPassLifetimeDays))
	}
	var orgUnit string
	if in.OrgUnitPath != nil {
		orgUnit = strings.TrimSpace(*in.OrgUnitPath)
		if !strings.HasPrefix(orgUnit, "/") || utf8.RuneCountInString(orgUnit) > maxOrgUnitPath ||
			strings.ContainsFunc(orgUnit, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			f.add("orgUnitPath", fmt.Sprintf("The organisational unit is a path starting with /, at most %d characters, such as /Staff.", maxOrgUnitPath))
		}
	}
	var domain string
	if in.AddressDomain != nil {
		domain = strings.ToLower(strings.TrimSpace(*in.AddressDomain))
		if domain != "" && (len(domain) > 253 || !domainPattern.MatchString(domain)) {
			f.add("addressDomain", "The address domain is a domain, such as example.com.")
		}
	}
	if f.failed() {
		return gen.PutGoogleConnectionSettings400ApplicationProblemPlusJSONResponse(invalid("Invalid account rules", f)), nil
	}
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	var body gen.GoogleConnection
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		conn, err := q.LockConnection(ctx)
		if err != nil {
			return fmt.Errorf("google: lock the connection: %w", err)
		}
		if conn.Version != version {
			return refusal{versionMismatch(conn.Version, version)}
		}
		if domain != "" && len(conn.Domains) > 0 && !slices.Contains(conn.Domains, domain) {
			var f fieldErrors
			f.add("addressDomain", "The address domain is one of the organisation's verified domains.")
			return refusal{invalid("Invalid account rules", f)}
		}
		p := store.UpdateAccountRulesParams{
			AddressDomain: conn.AddressDomain, AddressPattern: conn.AddressPattern, OrgUnitPath: conn.OrgUnitPath,
			PassLifetimeDays: conn.PassLifetimeDays, PassDelivery: conn.PassDelivery,
			LeaverRemoveLicences: conn.LeaverRemoveLicences, LeaverRemoveGroups: conn.LeaverRemoveGroups, LeaverSignOut: conn.LeaverSignOut,
			Now: now, UpdatedByUserID: actor.UserID,
		}
		var changed []string
		mark := func(name string, differs bool) {
			if differs {
				changed = append(changed, name)
			}
		}
		if in.AddressDomain != nil {
			var d *string
			if domain != "" {
				d = &domain
			}
			mark("addressDomain", deref(conn.AddressDomain) != domain)
			p.AddressDomain = d
		}
		if in.AddressPattern != nil {
			mark("addressPattern", p.AddressPattern != string(*in.AddressPattern))
			p.AddressPattern = string(*in.AddressPattern)
		}
		if in.OrgUnitPath != nil {
			mark("orgUnitPath", p.OrgUnitPath != orgUnit)
			p.OrgUnitPath = orgUnit
		}
		if in.PassLifetimeDays != nil {
			mark("passLifetimeDays", p.PassLifetimeDays != *in.PassLifetimeDays)
			p.PassLifetimeDays = *in.PassLifetimeDays
		}
		if in.PassDelivery != nil {
			mark("passDelivery", p.PassDelivery != string(*in.PassDelivery))
			p.PassDelivery = string(*in.PassDelivery)
		}
		if in.LeaverRemoveLicences != nil {
			mark("leaverRemoveLicences", p.LeaverRemoveLicences != *in.LeaverRemoveLicences)
			p.LeaverRemoveLicences = *in.LeaverRemoveLicences
		}
		if in.LeaverRemoveGroups != nil {
			mark("leaverRemoveGroups", p.LeaverRemoveGroups != string(*in.LeaverRemoveGroups))
			p.LeaverRemoveGroups = string(*in.LeaverRemoveGroups)
		}
		if in.LeaverSignOut != nil {
			mark("leaverSignOut", p.LeaverSignOut != *in.LeaverSignOut)
			p.LeaverSignOut = *in.LeaverSignOut
		}
		if len(changed) > 0 {
			if conn, err = q.UpdateAccountRules(ctx, p); err != nil {
				return fmt.Errorf("google: set the account rules: %w", err)
			}
			if err := recordEvent(ctx, q, now, actor, actionSettingsChanged, "", changed); err != nil {
				return err
			}
		}
		body, err = readConnection(ctx, q, s.deps.Config, conn)
		return err
	})
	var r refusal
	switch {
	case errors.As(err, &r) && r.status() == http.StatusBadRequest:
		return gen.PutGoogleConnectionSettings400ApplicationProblemPlusJSONResponse(r.problem), nil
	case errors.As(err, &r):
		return gen.PutGoogleConnectionSettings412ApplicationProblemPlusJSONResponse(r.problem), nil
	case err != nil:
		return nil, err
	}
	return gen.PutGoogleConnectionSettings200JSONResponse(body), nil
}
