package identity

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
)

// This file is the Owner's directory search (Google Workspace design G8,
// module-boundaries rule 22): identity serves an Owner connecting existing
// users to the workforce provider the accounts of that provider's
// directory, through the contracts.DirectorySearch whose Key is the
// configured OIDC provider (Deps.DirectorySearches). The provider reads
// what it needs of identity back through contracts.SSOConnections with the
// request's context, gated there as it always was. No call to a provider is
// made inside a transaction (rule 10): these handlers open none.

// The search's bounds.
const (
	// directorySearchMin and directorySearchMax bound search, in
	// characters once trimmed and rid of " and \.
	directorySearchMin = 2
	directorySearchMax = 100
	// directorySearchLimit is the most accounts a search answers.
	directorySearchLimit = 25
)

// The directory's refusal codes.
const (
	codeNoDirectory         = "no_directory"
	codeProviderUnreachable = "provider_unreachable"
	codeInvalidRequest      = "invalid_request"
)

// The directory's refusals.
var (
	noDirectory = refuse(http.StatusConflict, codeNoDirectory,
		"No directory serves the configured single sign-on provider. Configure single sign-on with a provider whose module is turned on.", nil)
	providerUnreachable = refuse(http.StatusServiceUnavailable, codeProviderUnreachable,
		"The directory could not be asked. Check the provider's connection and try again in a minute.", nil)
)

// directoryFor is the DirectorySearch whose Key is the configured OIDC
// provider, read through the settings' cache, nil while OIDC is off or no
// enabled module serves that provider's directory.
func (s *server) directoryFor(ctx context.Context) (contracts.DirectorySearch, error) {
	settings, err := s.access.sso.get(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: read the single sign-on settings: %w", err)
	}
	if settings.OIDC == nil {
		return nil, nil
	}
	return s.directoryOf(settings.OIDC.Provider), nil
}

// directoryOf is the DirectorySearch under key, nil for none.
func (s *server) directoryOf(key string) contracts.DirectorySearch {
	if key == "" {
		return nil
	}
	for _, d := range s.deps.DirectorySearches {
		if d.Key() == key {
			return d
		}
	}
	return nil
}

// directoryBody is the Owner SSO settings' directory for the configured
// provider, nil while OIDC is off or none serves it.
func (s *server) directoryBody(enabled bool, provider *string) *gen.SsoDirectory {
	if !enabled || provider == nil {
		return nil
	}
	d := s.directoryOf(*provider)
	if d == nil {
		return nil
	}
	return &gen.SsoDirectory{Provider: d.Key(), DisplayName: d.DisplayName()}
}

// directoryRefusal is a provider's failure as identity answers it: the
// request's own end as it is, single sign-on turned off meanwhile as 409
// no_directory, a directory that could not be asked as 503
// provider_unreachable, anything else a fault.
func directoryRefusal(ctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, contracts.ErrSSONotConfigured):
		return noDirectory
	case errors.Is(err, contracts.ErrDirectoryUnavailable):
		return providerUnreachable
	}
	return fmt.Errorf("identity: the directory search: %w", err)
}

// GetIdentityOwnerSsoDirectoryUsers searches the configured provider's
// directory, at most 25 accounts, each with who holds it. search loses
// every " and \ and is then 2 to 100 characters once trimmed, else 400
// invalid_request on search. 409 no_directory while OIDC is off or no
// enabled module serves the provider's directory; 503
// provider_unreachable when it could not be asked.
// policy:Owner+OwnerManagement, as the router checks.
// (GET /api/v1/identity/owner/sso/directory/users)
func (s *server) GetIdentityOwnerSsoDirectoryUsers(ctx context.Context, req gen.GetIdentityOwnerSsoDirectoryUsersRequestObject) (gen.GetIdentityOwnerSsoDirectoryUsersResponseObject, error) {
	search := strings.TrimSpace(strings.NewReplacer(`"`, "", `\`, "").Replace(req.Params.Search))
	if n := utf8.RuneCountInString(search); n < directorySearchMin || n > directorySearchMax {
		return refuse(http.StatusBadRequest, codeInvalidRequest, invalidRequestMessage, map[string][]string{
			"search": {fmt.Sprintf("Enter %d to %d characters to search for.", directorySearchMin, directorySearchMax)},
		}), nil
	}
	dir, err := s.directoryFor(ctx)
	if err != nil {
		return nil, err
	}
	if dir == nil {
		return noDirectory, nil
	}
	users, err := dir.Users(ctx, search, directorySearchLimit)
	if err != nil {
		return refusalOr[gen.GetIdentityOwnerSsoDirectoryUsersResponseObject](directoryRefusal(ctx, err))
	}
	out := gen.SsoDirectoryUsers{Users: make([]gen.SsoDirectoryUser, 0, len(users))}
	for _, u := range users[:min(len(users), directorySearchLimit)] {
		out.Users = append(out.Users, directoryUserBody(u))
	}
	return gen.GetIdentityOwnerSsoDirectoryUsers200JSONResponse(out), nil
}

// GetIdentityOwnerSsoDirectoryMatches proposes, for the users not yet
// connected, the one account in the configured provider's directory each
// matches exactly, as the provider reads them. 409 no_directory while OIDC
// is off or no enabled module serves the provider's directory; 503
// provider_unreachable when it could not be asked.
// policy:Owner+OwnerManagement, as the router checks.
// (GET /api/v1/identity/owner/sso/directory/matches)
func (s *server) GetIdentityOwnerSsoDirectoryMatches(ctx context.Context, _ gen.GetIdentityOwnerSsoDirectoryMatchesRequestObject) (gen.GetIdentityOwnerSsoDirectoryMatchesResponseObject, error) {
	dir, err := s.directoryFor(ctx)
	if err != nil {
		return nil, err
	}
	if dir == nil {
		return noDirectory, nil
	}
	found, err := dir.Matches(ctx)
	if err != nil {
		return refusalOr[gen.GetIdentityOwnerSsoDirectoryMatchesResponseObject](directoryRefusal(ctx, err))
	}
	out := gen.SsoDirectoryMatches{
		Matches:    make([]gen.SsoDirectoryMatch, 0, len(found.Matches)),
		Candidates: clampInt32(found.Candidates),
		Ambiguous:  clampInt32(found.Ambiguous),
		Truncated:  found.Truncated,
	}
	for _, m := range found.Matches {
		by := make([]gen.SsoDirectoryMatchMatchedBy, 0, len(m.MatchedBy))
		for _, b := range m.MatchedBy {
			by = append(by, gen.SsoDirectoryMatchMatchedBy(b))
		}
		account := directoryUserBody(m.Account)
		account.Holder = nil
		out.Matches = append(out.Matches, gen.SsoDirectoryMatch{UserId: openapi_types.UUID(m.UserID), Account: account, MatchedBy: by})
	}
	return gen.GetIdentityOwnerSsoDirectoryMatches200JSONResponse(out), nil
}

// directoryUserBody is an account as the contract answers it: mail and
// the employee number absent when empty, guest only for a guest.
func directoryUserBody(u contracts.DirectoryUser) gen.SsoDirectoryUser {
	body := gen.SsoDirectoryUser{
		ObjectId:          u.ObjectID,
		UserPrincipalName: u.PrincipalName,
		DisplayName:       u.DisplayName,
		Enabled:           u.Enabled,
	}
	if u.Mail != "" {
		mail := u.Mail
		body.Mail = &mail
	}
	if u.EmployeeNumber != "" {
		number := u.EmployeeNumber
		body.EmployeeNumber = &number
	}
	if u.Guest {
		yes := true
		body.Guest = &yes
	}
	if h := u.Holder; h != nil {
		holder := &gen.SsoDirectoryHolder{Kind: gen.SsoDirectoryHolderKind(h.Kind)}
		if h.UserID != nil && *h.UserID != uuid.Nil {
			id := openapi_types.UUID(*h.UserID)
			holder.UserId = &id
		}
		body.Holder = holder
	}
	return body
}

// clampInt32 is n as an int32, at most math.MaxInt32.
func clampInt32(n int) int32 { return int32(min(max(n, 0), math.MaxInt32)) }

func (r refusal) VisitGetIdentityOwnerSsoDirectoryUsersResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitGetIdentityOwnerSsoDirectoryMatchesResponse(w http.ResponseWriter) error {
	return r.write(w)
}
