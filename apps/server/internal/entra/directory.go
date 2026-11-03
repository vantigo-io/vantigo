package entra

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The directory search (Entra phase 2, delivery B; the plan's readings 3 to
// 5), behind contracts.DirectorySearch since Google Workspace design G8:
// identity serves an Owner's two reads of the tenant's accounts, for
// connecting existing users to the workforce provider, and asks this
// module's provider for them while Entra is the configured OIDC provider.
// What the reads need of identity — the users not yet connected, with
// their addresses and employee numbers, and who already holds an account's
// keys — comes through contracts.SSOConnections (Deps.SSOConnections),
// which identity gates itself, with the request's context identity hands
// on. A user's address is compared here and goes nowhere else: not in an
// answer, a log, an event or an error. The Graph client logs a refused call
// by its path, never its query, so neither a search nor a filter of
// addresses reaches the log either.

// The reads' bounds (plan readings 3 and 4).
const (
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
	// the tenant's in filters and the holders — past which it fails
	// contracts.ErrDirectoryUnavailable.
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

// directorySearch is the module's contracts.DirectorySearch.
type directorySearch struct {
	deps   module.Deps
	q      *store.Queries
	client *graphClient
	// matches is the matches' read in flight, by caller (Matches).
	matches singleflight.Group
}

var _ contracts.DirectorySearch = (*directorySearch)(nil)

// newDirectorySearch is Module's DirectorySearch: the constructor Compose
// calls, after SSOConnections is resolved, with the dependencies it was
// given.
func newDirectorySearch(d module.Deps) contracts.DirectorySearch {
	return &directorySearch{deps: d, q: store.New(d.Pool), client: newGraphClient(d)}
}

// Key is "entra", the OIDC provider whose directory this is.
func (d *directorySearch) Key() string { return providerKey }

// DisplayName is "Microsoft Entra ID".
func (d *directorySearch) DisplayName() string { return providerName }

// unavailable is err as the contract answers a tenant that could not be
// asked: wrapped in contracts.ErrDirectoryUnavailable.
func unavailable(err error) error {
	return fmt.Errorf("entra: %w: %w", contracts.ErrDirectoryUnavailable, err)
}

// Users searches the tenant's accounts by display name, mail and principal
// name, at most limit (and never more than 25) by display name, each marked
// with who already holds it as identity knows (plan reading 3). search
// loses every " and \ — it is quoted into the search — and answers nothing
// when nothing is left once trimmed. A tenant that could not be asked —
// the secret refused, the tenant down, throttled twice — fails
// contracts.ErrDirectoryUnavailable.
func (d *directorySearch) Users(ctx context.Context, search string, limit int) ([]contracts.DirectoryUser, error) {
	q := strings.TrimSpace(strings.NewReplacer(`"`, "", `\`, "").Replace(search))
	if q == "" || limit < 1 {
		return nil, nil
	}
	cred, err := credentialFor(ctx, d.q, d.client)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, unavailable(err)
	}
	accounts, err := d.searchUsers(ctx, cred, q, min(limit, directorySearchTop))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, unavailable(err)
	}
	held, err := d.holders(ctx, accounts)
	if err != nil {
		return nil, err
	}
	out := make([]contracts.DirectoryUser, 0, len(accounts))
	for _, a := range accounts {
		if u, ok := directoryUser(a, held[a.ID]); ok {
			out = append(out, u)
		}
	}
	return out, nil
}

// searchUsers is the search of plan reading 3: q, already rid of " and \,
// quoted into a clause on each of the three properties, by display name, at
// most top — an advanced query, so with $count and
// ConsistencyLevel: eventual, and noWords: Graph's refusal may echo q, which
// may be an address, and is never logged.
func (d *directorySearch) searchUsers(ctx context.Context, cred graphCredential, q string, top int) ([]directoryAccount, error) {
	search := `"displayName:` + q + `" OR "mail:` + q + `" OR "userPrincipalName:` + q + `"`
	var page directoryPage
	err := d.client.get(ctx, cred, "/users?"+query(url.Values{
		"$search": {search}, "$select": {directorySelect}, "$orderby": {"displayName"},
		"$top": {strconv.Itoa(top)}, "$count": {"true"},
	}), &page, eventual, noWords)
	return page.Value, err
}

// findByKeys is every account whose property — mail, userPrincipalName or
// employeeId — is one of values, read in in filters of matchChunk values,
// each quoted as OData quotes, a ' doubled; an advanced query, so with
// $count and ConsistencyLevel: eventual, and noWords: Graph's refusal may
// echo the filter's addresses, and is never logged.
func (d *directorySearch) findByKeys(ctx context.Context, cred graphCredential, property string, values []string) ([]directoryAccount, error) {
	var out []directoryAccount
	for chunk := range slices.Chunk(values, matchChunk) {
		quoted := make([]string, len(chunk))
		for i, v := range chunk {
			quoted[i] = "'" + strings.ReplaceAll(v, "'", "''") + "'"
		}
		var page directoryPage
		err := d.client.get(ctx, cred, "/users?"+query(url.Values{
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
func (d *directorySearch) holders(ctx context.Context, accounts []directoryAccount) (map[string]*contracts.KeyHolder, error) {
	keys := make([]contracts.DirectoryKey, 0, len(accounts))
	for _, a := range accounts {
		keys = append(keys, contracts.DirectoryKey{ObjectID: a.ID, UserPrincipalName: a.UserPrincipalName})
	}
	found, err := d.keyHolders(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("entra: read who holds the accounts: %w", err)
	}
	out := make(map[string]*contracts.KeyHolder, len(found))
	for _, h := range found {
		out[h.ObjectID] = &h
	}
	return out, nil
}

// directoryUser is an account as the contract answers it, with its
// holder, its object id in lower-case dashed form; ok false for an id that
// is not a GUID, which Graph never answers.
func directoryUser(a directoryAccount, holder *contracts.KeyHolder) (contracts.DirectoryUser, bool) {
	id, ok := objectID(a.ID)
	if !ok {
		return contracts.DirectoryUser{}, false
	}
	return contracts.DirectoryUser{
		ObjectID:       id.String(),
		PrincipalName:  a.UserPrincipalName,
		DisplayName:    a.DisplayName,
		Mail:           nonEmpty(a.Mail),
		EmployeeNumber: nonEmpty(a.EmployeeID),
		Enabled:        a.AccountEnabled,
		Guest:          a.guest(),
		Holder:         holder,
	}, true
}

// nonEmpty is *s, "" when s is nil or blank.
func nonEmpty(s *string) string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return ""
	}
	return *s
}

// Matches proposes, for the users not yet connected, the one account in
// the tenant each matches exactly (plan reading 4, matchCandidates),
// leaving out an account held for another user. It reads at most 1000
// users, in identity's order, truncated saying when more exist.
// contracts.ErrSSONotConfigured while single sign-on is off;
// contracts.ErrDirectoryUnavailable when the tenant could not be asked or
// the read took longer than matchesTimeout. A caller's second request while
// their first still reads waits for it and shares its answer (d.matches,
// keyed by the caller: what identity answers depends on who asks), so the
// read is never run twice at once for one Owner.
func (d *directorySearch) Matches(ctx context.Context) (contracts.DirectoryMatches, error) {
	p, _ := contracts.PrincipalFrom(ctx)
	for {
		ch := d.matches.DoChan(p.UserID.String(), func() (any, error) {
			return d.readMatches(ctx)
		})
		select {
		case <-ctx.Done():
			return contracts.DirectoryMatches{}, ctx.Err()
		case res := <-ch:
			// The read shared was another request's, whose client left:
			// this one is still waiting, so it reads again.
			if res.Shared && errors.Is(res.Err, context.Canceled) && ctx.Err() == nil {
				continue
			}
			if res.Err != nil {
				return contracts.DirectoryMatches{}, res.Err
			}
			return res.Val.(contracts.DirectoryMatches), nil
		}
	}
}

// readMatches is Matches' read, bounded by matchesTimeout.
func (d *directorySearch) readMatches(ctx context.Context) (contracts.DirectoryMatches, error) {
	readCtx, cancel := context.WithTimeout(ctx, matchesTimeout)
	defer cancel()
	// fail is the answer to a read that failed: the request's own end as
	// it is, the timeout and a tenant that could not be asked as
	// contracts.ErrDirectoryUnavailable, anything else a fault.
	fail := func(err error, tenant bool) (contracts.DirectoryMatches, error) {
		switch {
		case ctx.Err() != nil:
			return contracts.DirectoryMatches{}, ctx.Err()
		case readCtx.Err() != nil:
			return contracts.DirectoryMatches{}, unavailable(fmt.Errorf("the matches took longer than %s: %w", matchesTimeout, readCtx.Err()))
		case tenant:
			return contracts.DirectoryMatches{}, unavailable(err)
		}
		return contracts.DirectoryMatches{}, err
	}
	candidates, err := d.unconnected(readCtx, matchCandidatesMax+1)
	if errors.Is(err, contracts.ErrSSONotConfigured) {
		return contracts.DirectoryMatches{}, err
	}
	if err != nil {
		return fail(fmt.Errorf("entra: read the users not yet connected: %w", err), false)
	}
	out := contracts.DirectoryMatches{Matches: []contracts.DirectoryMatch{}}
	if len(candidates) > matchCandidatesMax {
		candidates, out.Truncated = candidates[:matchCandidatesMax], true
	}
	out.Candidates = len(candidates)
	if len(candidates) == 0 {
		return out, nil
	}
	cred, err := credentialFor(readCtx, d.q, d.client)
	if err != nil {
		return fail(err, true)
	}
	accounts, err := d.findCandidates(readCtx, cred, candidates)
	if err != nil {
		return fail(err, true)
	}
	proposals, ambiguous := matchCandidates(candidates, accounts)
	out.Ambiguous = ambiguous
	proposed := make([]directoryAccount, 0, len(proposals))
	for _, p := range proposals {
		proposed = append(proposed, p.account)
	}
	held, err := d.holders(readCtx, proposed)
	if err != nil {
		return fail(err, false)
	}
	for _, p := range proposals {
		// An account held for somebody else — another user's announced link
		// or SCIM mapping, or an announced account, which has no user — is
		// left out; the candidate's own SCIM mapping does not block them.
		if h := held[p.account.ID]; h != nil && (h.UserID == nil || *h.UserID != p.userID) {
			continue
		}
		account, ok := directoryUser(p.account, nil)
		if !ok {
			continue
		}
		out.Matches = append(out.Matches, contracts.DirectoryMatch{UserID: p.userID, Account: account, MatchedBy: p.matchedBy})
	}
	return out, nil
}

// findCandidates is every account any of candidates' keys could match: by
// mail and by principal name for each address, by employeeId for each
// employee number, each value once (addresses lower-cased, numbers trimmed,
// both compared ignoring case) and each account once.
func (d *directorySearch) findCandidates(ctx context.Context, cred graphCredential, candidates []contracts.ConnectCandidate) ([]directoryAccount, error) {
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
		found, err := d.findByKeys(ctx, cred, read.property, read.values)
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
	matchedBy []string
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
// matchedBy (mail, principalName, employeeNumber, in that order), when its
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
	hits := make([]map[int][]string, len(candidates))
	// claimed[a] is how many candidates' keys matched account a.
	claimed := map[int]int{}
	for c, cand := range candidates {
		hits[c] = map[int][]string{}
		mark := func(index map[string][]int, key string, by string) {
			if key == "" {
				return
			}
			for _, a := range index[key] {
				hits[c][a] = append(hits[c][a], by)
			}
		}
		address := addressKey(cand.Email)
		mark(byMail, address, contracts.DirectoryMatchedByMail)
		mark(byUPN, address, contracts.DirectoryMatchedByPrincipalName)
		mark(byNumber, numberKey(cand.EmployeeNumber), contracts.DirectoryMatchedByEmployeeNumber)
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
