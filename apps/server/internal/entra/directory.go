package entra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
)

// The directory picker and the exact matches (Entra phase 2, delivery B;
// the plan's readings 3 to 5): an Owner's two reads of the tenant's
// accounts, for connecting existing users to the workforce provider. What
// they need of identity — the users not yet connected, with their addresses
// and employee numbers, and who already holds an account's keys — comes
// through contracts.SSOConnections (Deps.SSOConnections), which identity
// gates itself. A user's address is compared here and goes nowhere else:
// not on the wire, not in a log, an event or an error. The Graph client
// logs a refused call by its path, never its query, so neither a search nor
// a filter of addresses reaches the log either.

// The directory's own refusal codes, besides errors.go's.
const (
	// codeInvalidRequest is a field that did not pass (400), the search's
	// q.
	codeInvalidRequest = "invalid_request"
	// codeThrottled is a read the tenant throttled twice in a row (503).
	codeThrottled = "throttled"
	// codeSSONotConfigured is the matches while single sign-on is off:
	// there is no issuer to connect anybody under (409).
	codeSSONotConfigured = "sso_not_configured"
)

// The reads' bounds (plan readings 3 and 4).
const (
	// directorySearchMin and directorySearchMax bound q, in characters once
	// trimmed.
	directorySearchMin = 2
	directorySearchMax = 100
	// directorySearchTop is the most hits a search answers.
	directorySearchTop = 25
	// matchCandidatesMax is the most users the matches read, in identity's
	// order; more sets truncated.
	matchCandidatesMax = 1000
	// matchChunk is the most values one in filter lists, as Graph allows.
	matchChunk = 15
	// matchTop is the page asked for an in filter's answer: Graph's largest,
	// far above what fifteen exact values can match, so one page is all of
	// it.
	matchTop = 999
	// directorySelect is what both reads ask of an account.
	directorySelect = "id,displayName,mail,userPrincipalName,employeeId,accountEnabled,userType"
	// matchesTimeout bounds the matches' whole read — identity's candidates,
	// the tenant's in filters and the holders — past which it answers 503
	// tenant_unreachable.
	matchesTimeout = 2 * time.Minute
)

// guestUserType is the userType Graph answers for a guest of the tenant,
// an account invited from another organisation.
const guestUserType = "Guest"

// directoryAccount is an account as the two reads ask for it
// (directorySelect).
type directoryAccount struct {
	ID                string  `json:"id"`
	DisplayName       string  `json:"displayName"`
	Mail              *string `json:"mail"`
	UserPrincipalName string  `json:"userPrincipalName"`
	EmployeeID        *string `json:"employeeId"`
	AccountEnabled    bool    `json:"accountEnabled"`
	UserType          string  `json:"userType"`
}

// guest reports whether the account is a guest of the tenant.
func (a directoryAccount) guest() bool { return strings.EqualFold(a.UserType, guestUserType) }

// directoryPage is a list of accounts as Graph answers it.
type directoryPage struct {
	Value []directoryAccount `json:"value"`
}

// GetEntraDirectoryUsers searches the tenant's accounts by display name,
// mail and principal name, at most 25 by display name, each marked with who
// already holds it as identity knows (plan reading 3). q loses every " and
// \ — it is quoted into the search — and is then 2 to 100 characters once
// trimmed, else 400 invalid_request on q. 503 credential_invalid,
// tenant_unreachable or throttled when the tenant could not be asked.
// policy:Owner+OwnerManagement, as the router checks.
// (GET /api/v1/entra/directory/users)
func (s *server) GetEntraDirectoryUsers(ctx context.Context, req gen.GetEntraDirectoryUsersRequestObject) (gen.GetEntraDirectoryUsersResponseObject, error) {
	q := strings.TrimSpace(strings.NewReplacer(`"`, "", `\`, "").Replace(req.Params.Q))
	if n := utf8.RuneCountInString(q); n < directorySearchMin || n > directorySearchMax {
		var f fieldErrors
		f.add("q", fmt.Sprintf("Enter %d to %d characters to search for.", directorySearchMin, directorySearchMax))
		p := invalid("Invalid search", f)
		code := codeInvalidRequest
		p.Code = &code
		return gen.GetEntraDirectoryUsers400ApplicationProblemPlusJSONResponse(p), nil
	}
	cred, err := credentialFor(ctx, s.q, s.client)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return gen.GetEntraDirectoryUsers503ApplicationProblemPlusJSONResponse(directoryUnavailable(err)), nil
	}
	accounts, err := s.searchUsers(ctx, cred, q)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return gen.GetEntraDirectoryUsers503ApplicationProblemPlusJSONResponse(directoryUnavailable(err)), nil
	}
	held, err := s.holders(ctx, accounts)
	if err != nil {
		return nil, err
	}
	out := gen.EntraDirectoryUsers{Users: make([]gen.EntraDirectoryUser, 0, len(accounts))}
	for _, a := range accounts {
		if u, ok := directoryUserBody(a, held[a.ID]); ok {
			out.Users = append(out.Users, u)
		}
	}
	return gen.GetEntraDirectoryUsers200JSONResponse(out), nil
}

// searchUsers is the search of plan reading 3: q, already rid of " and \,
// quoted into a clause on each of the three properties, by display name, at
// most directorySearchTop — an advanced query, so with $count and
// ConsistencyLevel: eventual, and noWords: Graph's refusal may echo q, which
// may be an address, and is never logged.
func (s *server) searchUsers(ctx context.Context, cred graphCredential, q string) ([]directoryAccount, error) {
	search := `"displayName:` + q + `" OR "mail:` + q + `" OR "userPrincipalName:` + q + `"`
	var page directoryPage
	err := s.client.get(ctx, cred, "/users?"+query(url.Values{
		"$search": {search}, "$select": {directorySelect}, "$orderby": {"displayName"},
		"$top": {strconv.Itoa(directorySearchTop)}, "$count": {"true"},
	}), &page, eventual, noWords)
	return page.Value, err
}

// findByKeys is every account whose property — mail, userPrincipalName or
// employeeId — is one of values, read in in filters of matchChunk values,
// each quoted as OData quotes, a ' doubled; an advanced query, so with
// $count and ConsistencyLevel: eventual, and noWords: Graph's refusal may
// echo the filter's addresses, and is never logged.
func (s *server) findByKeys(ctx context.Context, cred graphCredential, property string, values []string) ([]directoryAccount, error) {
	var out []directoryAccount
	for chunk := range slices.Chunk(values, matchChunk) {
		quoted := make([]string, len(chunk))
		for i, v := range chunk {
			quoted[i] = "'" + strings.ReplaceAll(v, "'", "''") + "'"
		}
		var page directoryPage
		err := s.client.get(ctx, cred, "/users?"+query(url.Values{
			"$filter": {property + " in (" + strings.Join(quoted, ", ") + ")"}, "$select": {directorySelect},
			"$top": {strconv.Itoa(matchTop)}, "$count": {"true"},
		}), &page, eventual, noWords)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Value...)
	}
	return out, nil
}

// holders is who holds each of accounts under the configured issuer, by
// the account's id, as identity answers it (contracts.SSOConnections's
// KeyHolders, through keyHolders); an account nobody holds is absent.
// Without the port — identity not composed — nobody holds anything.
func (s *server) holders(ctx context.Context, accounts []directoryAccount) (map[string]*gen.EntraDirectoryHolder, error) {
	keys := make([]contracts.DirectoryKey, 0, len(accounts))
	for _, a := range accounts {
		keys = append(keys, contracts.DirectoryKey{ObjectID: a.ID, UserPrincipalName: a.UserPrincipalName})
	}
	found, err := s.keyHolders(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("entra: read who holds the accounts: %w", err)
	}
	out := make(map[string]*gen.EntraDirectoryHolder, len(found))
	for _, h := range found {
		holder := &gen.EntraDirectoryHolder{Kind: gen.EntraDirectoryHolderKind(h.Kind)}
		if h.UserID != nil {
			id := openapi_types.UUID(*h.UserID)
			holder.UserId = &id
		}
		out[h.ObjectID] = holder
	}
	return out, nil
}

// directoryUserBody is an account as the contract answers it, with its
// holder, guest set only for a guest; ok false for an id that is not a
// GUID, which Graph never answers.
func directoryUserBody(a directoryAccount, holder *gen.EntraDirectoryHolder) (gen.EntraDirectoryUser, bool) {
	id, ok := objectID(a.ID)
	if !ok {
		return gen.EntraDirectoryUser{}, false
	}
	var guest *bool
	if a.guest() {
		yes := true
		guest = &yes
	}
	return gen.EntraDirectoryUser{
		Guest:             guest,
		ObjectId:          openapi_types.UUID(id),
		DisplayName:       a.DisplayName,
		Mail:              nonEmpty(a.Mail),
		UserPrincipalName: a.UserPrincipalName,
		EmployeeId:        nonEmpty(a.EmployeeID),
		AccountEnabled:    a.AccountEnabled,
		Holder:            holder,
	}, true
}

// nonEmpty is s, nil when it is nil or blank.
func nonEmpty(s *string) *string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	v := *s
	return &v
}

// GetEntraDirectoryMatches proposes, for the users not yet connected, the
// one account in the tenant each matches exactly (plan reading 4,
// matchCandidates), leaving out an account held for another user. It reads
// at most 1000 users, in identity's order, truncated saying when more
// exist. 409 sso_not_configured while single sign-on is off; 503
// credential_invalid, tenant_unreachable or throttled when the tenant could
// not be asked, tenant_unreachable too when the read took longer than
// matchesTimeout. A caller's second request while their first still reads
// waits for it and shares its answer (s.matches, keyed by the caller: what
// identity answers depends on who asks), so the read is never run twice at
// once for one Owner. policy:Owner+OwnerManagement, as the router checks.
// (GET /api/v1/entra/directory/matches)
func (s *server) GetEntraDirectoryMatches(ctx context.Context, _ gen.GetEntraDirectoryMatchesRequestObject) (gen.GetEntraDirectoryMatchesResponseObject, error) {
	p, _ := contracts.PrincipalFrom(ctx)
	for {
		ch := s.matches.DoChan(p.UserID.String(), func() (any, error) {
			return s.readMatches(ctx)
		})
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case res := <-ch:
			// The read shared was another request's, whose client left:
			// this one is still waiting, so it reads again.
			if res.Shared && errors.Is(res.Err, context.Canceled) && ctx.Err() == nil {
				continue
			}
			if res.Err != nil {
				return nil, res.Err
			}
			return res.Val.(gen.GetEntraDirectoryMatchesResponseObject), nil
		}
	}
}

// readMatches is GetEntraDirectoryMatches' read, bounded by matchesTimeout.
func (s *server) readMatches(ctx context.Context) (gen.GetEntraDirectoryMatchesResponseObject, error) {
	readCtx, cancel := context.WithTimeout(ctx, matchesTimeout)
	defer cancel()
	// unavailable is the answer to a read that failed: the request's own
	// end as it is, the timeout as 503 tenant_unreachable, a tenant that
	// could not be asked as its 503, anything else a fault.
	unavailable := func(err error, tenant bool) (gen.GetEntraDirectoryMatchesResponseObject, error) {
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case readCtx.Err() != nil:
			return gen.GetEntraDirectoryMatches503ApplicationProblemPlusJSONResponse(tenantUnavailable(reasonTenantUnreachable)), nil
		case tenant:
			return gen.GetEntraDirectoryMatches503ApplicationProblemPlusJSONResponse(directoryUnavailable(err)), nil
		}
		return nil, err
	}
	candidates, err := s.unconnected(readCtx, matchCandidatesMax+1)
	if errors.Is(err, contracts.ErrSSONotConfigured) {
		return gen.GetEntraDirectoryMatches409ApplicationProblemPlusJSONResponse(ssoNotConfigured()), nil
	}
	if err != nil {
		return unavailable(fmt.Errorf("entra: read the users not yet connected: %w", err), false)
	}
	out := gen.EntraDirectoryMatches{Matches: []gen.EntraDirectoryMatch{}}
	if len(candidates) > matchCandidatesMax {
		candidates, out.Truncated = candidates[:matchCandidatesMax], true
	}
	out.Candidates = int32(len(candidates))
	if len(candidates) == 0 {
		return gen.GetEntraDirectoryMatches200JSONResponse(out), nil
	}
	cred, err := credentialFor(readCtx, s.q, s.client)
	if err != nil {
		return unavailable(err, true)
	}
	accounts, err := s.findCandidates(readCtx, cred, candidates)
	if err != nil {
		return unavailable(err, true)
	}
	proposals, ambiguous := matchCandidates(candidates, accounts)
	out.Ambiguous = int32(ambiguous)
	proposed := make([]directoryAccount, 0, len(proposals))
	for _, p := range proposals {
		proposed = append(proposed, p.account)
	}
	held, err := s.holders(readCtx, proposed)
	if err != nil {
		return unavailable(err, false)
	}
	for _, p := range proposals {
		// An account held for somebody else — another user's announced link
		// or SCIM mapping, or an announced account, which has no user — is
		// left out; the candidate's own SCIM mapping does not block them.
		if h := held[p.account.ID]; h != nil && (h.UserId == nil || uuid.UUID(*h.UserId) != p.userID) {
			continue
		}
		account, ok := directoryUserBody(p.account, nil)
		if !ok {
			continue
		}
		out.Matches = append(out.Matches, gen.EntraDirectoryMatch{UserId: openapi_types.UUID(p.userID), Account: account, MatchedBy: p.matchedBy})
	}
	return gen.GetEntraDirectoryMatches200JSONResponse(out), nil
}

// findCandidates is every account any of candidates' keys could match: by
// mail and by principal name for each address, by employeeId for each
// employee number, each value once (addresses lower-cased, numbers trimmed,
// both compared ignoring case) and each account once.
func (s *server) findCandidates(ctx context.Context, cred graphCredential, candidates []contracts.ConnectCandidate) ([]directoryAccount, error) {
	var addresses, numbers []string
	seenAddress, seenNumber := map[string]bool{}, map[string]bool{}
	for _, c := range candidates {
		if a := addressKey(c.Email); a != "" && !seenAddress[a] {
			seenAddress[a] = true
			addresses = append(addresses, a)
		}
		if n := strings.TrimSpace(c.EmployeeNumber); n != "" && !seenNumber[strings.ToLower(n)] {
			seenNumber[strings.ToLower(n)] = true
			numbers = append(numbers, n)
		}
	}
	var out []directoryAccount
	seen := map[string]bool{}
	for _, read := range []struct {
		property string
		values   []string
	}{{"mail", addresses}, {"userPrincipalName", addresses}, {"employeeId", numbers}} {
		found, err := s.findByKeys(ctx, cred, read.property, read.values)
		if err != nil {
			return nil, err
		}
		for _, a := range found {
			if !seen[a.ID] {
				seen[a.ID] = true
				out = append(out, a)
			}
		}
	}
	return out, nil
}

// proposal is a candidate and the one account matchCandidates proposes for
// them, with what matched.
type proposal struct {
	userID    uuid.UUID
	account   directoryAccount
	matchedBy []gen.EntraDirectoryMatchMatchedBy
}

// matchCandidates is plan reading 4's rule, exact only. A key of a
// candidate matches an account when
//
//   - mail: the candidate's address equals the account's mail, ignoring case
//     and surrounding space;
//   - userPrincipalName: the candidate's address equals the account's
//     principal name, the same way;
//   - employeeId: the candidate's employee number equals the account's
//     employeeId, trimmed and ignoring case.
//
// A guest account matches nobody: it is left out before matching and
// counted nowhere, so it neither is proposed nor makes a candidate
// ambiguous. An address identity made up (Email "") and an empty number
// match nothing.
// A candidate whose keys matched no account is neither proposed nor
// ambiguous. A candidate is proposed, with every key that matched in
// matchedBy (mail, userPrincipalName, employeeId, in that order), when its
// keys matched exactly one account and no other candidate's key matched that
// account. A candidate is ambiguous — left out and counted — when its keys
// matched two accounts or more, or when they matched one account that a key
// of another candidate matched too (whether or not that other candidate
// matched further accounts). No candidate is ever proposed by a guess
// between two. Proposals keep the candidates' order. Whether somebody
// already holds a proposed account is the caller's to check, after this.
func matchCandidates(candidates []contracts.ConnectCandidate, accounts []directoryAccount) ([]proposal, int) {
	byMail, byUPN, byNumber := map[string][]int{}, map[string][]int{}, map[string][]int{}
	for i, a := range accounts {
		if a.guest() {
			continue
		}
		if a.Mail != nil {
			if k := addressKey(*a.Mail); k != "" {
				byMail[k] = append(byMail[k], i)
			}
		}
		if k := addressKey(a.UserPrincipalName); k != "" {
			byUPN[k] = append(byUPN[k], i)
		}
		if a.EmployeeID != nil {
			if k := numberKey(*a.EmployeeID); k != "" {
				byNumber[k] = append(byNumber[k], i)
			}
		}
	}

	// hits[c] is, per account candidate c's keys matched, what matched it.
	hits := make([]map[int][]gen.EntraDirectoryMatchMatchedBy, len(candidates))
	// claimed[a] is how many candidates' keys matched account a.
	claimed := map[int]int{}
	for c, cand := range candidates {
		hits[c] = map[int][]gen.EntraDirectoryMatchMatchedBy{}
		mark := func(index map[string][]int, key string, by gen.EntraDirectoryMatchMatchedBy) {
			if key == "" {
				return
			}
			for _, a := range index[key] {
				hits[c][a] = append(hits[c][a], by)
			}
		}
		address := addressKey(cand.Email)
		mark(byMail, address, gen.EntraDirectoryMatchMatchedByMail)
		mark(byUPN, address, gen.EntraDirectoryMatchMatchedByUserPrincipalName)
		mark(byNumber, numberKey(cand.EmployeeNumber), gen.EntraDirectoryMatchMatchedByEmployeeId)
		for a := range hits[c] {
			claimed[a]++
		}
	}

	var proposals []proposal
	ambiguous := 0
	for c, cand := range candidates {
		switch len(hits[c]) {
		case 0:
			continue
		case 1:
			for a, by := range hits[c] {
				if claimed[a] > 1 {
					ambiguous++
					continue
				}
				proposals = append(proposals, proposal{userID: cand.UserID, account: accounts[a], matchedBy: by})
			}
		default:
			ambiguous++
		}
	}
	return proposals, ambiguous
}

// addressKey is an address as the matches compare it: trimmed and
// lower-cased.
func addressKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// numberKey is an employee number as the matches compare it: trimmed and
// lower-cased.
func numberKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// directoryUnavailable is the 503 of a directory read the tenant did not
// answer: throttled when Graph throttled it twice in a row, else
// credential_invalid or tenant_unreachable as the licences' read answers
// them (reasonFor).
func directoryUnavailable(err error) gen.EntraProblem {
	if errors.Is(err, errThrottled) {
		return problem(http.StatusServiceUnavailable, codeThrottled, "Tenant throttled",
			"Microsoft Entra ID asked Vantigo to slow down. Try again in a minute.")
	}
	return tenantUnavailable(reasonFor(err))
}

// ssoNotConfigured is the 409 the matches answer while single sign-on is
// off: there is no issuer to connect anybody under.
func ssoNotConfigured() gen.EntraProblem {
	return problem(http.StatusConflict, codeSSONotConfigured, "Single sign-on not configured",
		"Single sign-on is not configured, so there is nothing to connect users under. Configure it first.")
}
