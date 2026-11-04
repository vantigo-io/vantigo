package google

import (
	"cmp"
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
	"github.com/vantigo-io/vantigo/server/internal/google/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's contracts.DirectorySearch (Google Workspace
// design G23; the plan, docs/superpowers/plans/2026-11-03-google-link-users.md):
// identity serves an Owner's two reads of the organisation's users, for
// connecting existing users to Google sign-in, and asks this provider for
// them while google is the configured OIDC provider. What the reads need of
// identity — the users not yet connected, with their addresses and employee
// numbers, and who already holds an account — comes through
// contracts.SSOConnections, with the request's context identity hands on,
// so identity's own gatekeeping applies. Both reads call the Directory as
// the admin to impersonate with the admin.directory.user scope (delivery
// B's), on no transaction. A user's address is compared here and goes
// nowhere else: not in an answer, a log, an event or an error. The client
// logs a call by its host and path, never its query, so neither a search
// nor an address reaches the log.
//
// The keys an announced link names are Google's: the Directory id as the
// object id, which identity compares with the ID token's sub (G7; U1), and
// the primary email as the principal name.

// The reads' bounds (the plan's decisions).
const (
	// searchTop is the most accounts one search answers, and the most each
	// of its two queries asks for.
	searchTop = 20
	// googleMatchCandidatesMax is the most users not yet connected the
	// matches read, in identity's order; more sets truncated.
	googleMatchCandidatesMax = 1000
	// matchPageSize is the page the matches read the users in, Google's
	// largest.
	matchPageSize = 500
	// matchPagesMax bounds the users read to 10,000; a page left after it
	// sets truncated, and the employee number is then not matched on.
	matchPagesMax = 20
	// googleMatchesTimeout bounds the matches' whole read, past which it
	// fails contracts.ErrDirectoryUnavailable.
	googleMatchesTimeout = 2 * time.Minute
)

// searchAccount is a user as the two reads ask for it (projection=basic).
type searchAccount struct {
	ID           string `json:"id"`
	PrimaryEmail string `json:"primaryEmail"`
	Name         struct {
		FullName string `json:"fullName"`
	} `json:"name"`
	Suspended          bool             `json:"suspended"`
	Aliases            []string         `json:"aliases"`
	NonEditableAliases []string         `json:"nonEditableAliases"`
	ExternalIDs        []map[string]any `json:"externalIds"`
}

// searchPage is one page of users.list.
type searchPage struct {
	Users         []searchAccount `json:"users"`
	NextPageToken string          `json:"nextPageToken"`
}

// employeeNumber is the account's first organization-typed external id,
// trimmed; "" when it has none.
func (a searchAccount) employeeNumber() string {
	for _, e := range a.ExternalIDs {
		if str(e["type"]) == externalIDOrganization {
			if v := strings.TrimSpace(str(e["value"])); v != "" {
				return v
			}
		}
	}
	return ""
}

// aliases are the account's addresses other than its primary email, as the
// matches compare them (addressKey): its aliases and its non-editable
// aliases (the same names on an alias domain). The addresses in emails[] are
// not the account's own — an administrator types them, on any domain — and
// match nobody.
func (a searchAccount) aliases() []string {
	primary := addressKey(a.PrimaryEmail)
	var out []string
	add := func(s string) {
		if k := addressKey(s); k != "" && k != primary && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	for _, s := range a.Aliases {
		add(s)
	}
	for _, s := range a.NonEditableAliases {
		add(s)
	}
	return out
}

// contractUser is the account as the contract answers it, with holder;
// ok false for an id that is not a Google user id, which Google never
// answers.
func (a searchAccount) contractUser(holder *contracts.KeyHolder) (contracts.DirectoryUser, bool) {
	if !validUserID(a.ID) {
		return contracts.DirectoryUser{}, false
	}
	name := strings.TrimSpace(a.Name.FullName)
	if name == "" {
		name = a.PrimaryEmail
	}
	return contracts.DirectoryUser{
		ObjectID:       a.ID,
		PrincipalName:  a.PrimaryEmail,
		DisplayName:    name,
		Mail:           a.PrimaryEmail,
		EmployeeNumber: a.employeeNumber(),
		Enabled:        !a.Suspended,
		Guest:          false,
		Holder:         holder,
	}, true
}

// directorySearch is the module's contracts.DirectorySearch.
type directorySearch struct {
	deps module.Deps
	keys *keyring
	// matches is the matches' read in flight, by caller (Matches).
	matches singleflight.Group
}

var _ contracts.DirectorySearch = (*directorySearch)(nil)

// newDirectorySearch is Module's DirectorySearch: the constructor Compose
// calls, after SSOConnections is resolved, with the dependencies it was
// given.
func newDirectorySearch(d module.Deps) contracts.DirectorySearch {
	return &directorySearch{deps: d, keys: newKeyring(store.New(d.Pool), newGoogleClient(d))}
}

// Key is "google", the OIDC provider whose directory this is.
func (d *directorySearch) Key() string { return providerKey }

// DisplayName is "Google Workspace".
func (d *directorySearch) DisplayName() string { return providerName }

// searchUnavailable is err as the contract answers an organisation that
// could not be asked: wrapped in contracts.ErrDirectoryUnavailable.
func searchUnavailable(err error) error {
	return fmt.Errorf("google: %w: %w", contracts.ErrDirectoryUnavailable, err)
}

// footing is a read's footing on the pool: the users' token source, as the
// admin to impersonate. A
// connection that names no admin or holds no live key cannot be asked:
// contracts.ErrDirectoryUnavailable.
func (d *directorySearch) footing(ctx context.Context) (tokenSource, error) {
	q := store.New(d.deps.Pool)
	conn, err := q.GetConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("google: read the connection: %w", err)
	}
	creds, err := q.ListCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("google: read the credentials: %w", err)
	}
	if !configured(conn, creds) {
		return nil, searchUnavailable(errNotConfigured)
	}
	return d.keys.tokenSource([]string{scopeUsers}, *conn.AdminSubject), nil
}

// failed is a call's error as the contract answers it: the request's own
// end as it is, anything from Google — a token refused, a refusal, a
// timeout, throttling — contracts.ErrDirectoryUnavailable.
func failed(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return searchUnavailable(err)
}

// searchTerm is search as it is quoted into a query: without ', ", \ and *,
// trimmed; "" when nothing is left.
func searchTerm(search string) string {
	return strings.TrimSpace(strings.NewReplacer(`'`, "", `"`, "", `\`, "", `*`, "").Replace(search))
}

// Users searches the organisation's users whose full name or primary email
// starts with search: two users.list queries, name:'<s>*' and
// email:'<s>*', 20 answers each, merged by id, ordered by full name, at
// most limit (and never more than 20), each marked with who already holds
// it as identity knows. Google documents no OR between clauses, so the two
// are always asked apart.
func (d *directorySearch) Users(ctx context.Context, search string, limit int) ([]contracts.DirectoryUser, error) {
	term := searchTerm(search)
	if term == "" || limit < 1 {
		return nil, nil
	}
	src, err := d.footing(ctx)
	if err != nil {
		return nil, err
	}
	var accounts []searchAccount
	seen := map[string]bool{}
	for _, field := range []string{"name", "email"} {
		v := url.Values{
			"customer": {"my_customer"}, "projection": {"basic"}, "viewType": {"admin_view"},
			"maxResults": {strconv.Itoa(searchTop)}, "query": {field + ":'" + term + "*'"},
		}
		var page searchPage
		if err := d.keys.c.get(ctx, src, directoryBase+"/users?"+v.Encode(), &page); err != nil {
			return nil, failed(ctx, err)
		}
		for _, a := range page.Users {
			if !seen[a.ID] {
				seen[a.ID] = true
				accounts = append(accounts, a)
			}
		}
	}
	slices.SortStableFunc(accounts, func(a, b searchAccount) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name.FullName), strings.ToLower(b.Name.FullName)), cmp.Compare(a.ID, b.ID))
	})
	accounts = accounts[:min(len(accounts), limit, searchTop)]
	held, err := d.holders(ctx, accounts)
	if err != nil {
		return nil, err
	}
	out := make([]contracts.DirectoryUser, 0, len(accounts))
	for _, a := range accounts {
		if u, ok := a.contractUser(held[a.ID]); ok {
			out = append(out, u)
		}
	}
	return out, nil
}

// holders is who holds each of accounts under the configured issuer, by the
// account's id (contracts.SSOConnections.KeyHolders); an account nobody
// holds is absent. Without the port — identity not composed — nobody holds
// anything.
func (d *directorySearch) holders(ctx context.Context, accounts []searchAccount) (map[string]*contracts.KeyHolder, error) {
	out := map[string]*contracts.KeyHolder{}
	if len(accounts) == 0 || d.deps.SSOConnections == nil {
		return out, nil
	}
	keys := make([]contracts.DirectoryKey, 0, len(accounts))
	for _, a := range accounts {
		keys = append(keys, contracts.DirectoryKey{ObjectID: a.ID, UserPrincipalName: a.PrimaryEmail})
	}
	found, err := d.deps.SSOConnections.KeyHolders(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("google: read who holds the accounts: %w", err)
	}
	for _, h := range found {
		out[h.ObjectID] = &h
	}
	return out, nil
}

// Matches proposes, for the users not yet connected, the one account in the
// organisation each matches exactly (matchAccounts), leaving out an account
// held for another user. It reads at most 1000 users, in identity's order,
// and at most 10,000 accounts, truncated saying when more exist.
// contracts.ErrSSONotConfigured while single sign-on is off;
// contracts.ErrDirectoryUnavailable when the organisation could not be
// asked or the read took longer than two minutes. A caller's second request
// while their first still reads waits for it and shares its answer, as
// Entra's does.
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

// readMatches is Matches' read, bounded by googleMatchesTimeout.
func (d *directorySearch) readMatches(ctx context.Context) (contracts.DirectoryMatches, error) {
	readCtx, cancel := context.WithTimeout(ctx, googleMatchesTimeout)
	defer cancel()
	fail := func(err error, google bool) (contracts.DirectoryMatches, error) {
		switch {
		case ctx.Err() != nil:
			return contracts.DirectoryMatches{}, ctx.Err()
		case readCtx.Err() != nil:
			return contracts.DirectoryMatches{}, searchUnavailable(fmt.Errorf("the matches took longer than %s: %w", googleMatchesTimeout, readCtx.Err()))
		case google:
			return contracts.DirectoryMatches{}, searchUnavailable(err)
		}
		return contracts.DirectoryMatches{}, err
	}
	if d.deps.SSOConnections == nil {
		return contracts.DirectoryMatches{}, contracts.ErrSSONotConfigured
	}
	candidates, err := d.deps.SSOConnections.Unconnected(readCtx, googleMatchCandidatesMax+1)
	if errors.Is(err, contracts.ErrSSONotConfigured) {
		return contracts.DirectoryMatches{}, err
	}
	if err != nil {
		return fail(fmt.Errorf("google: read the users not yet connected: %w", err), false)
	}
	out := contracts.DirectoryMatches{Matches: []contracts.DirectoryMatch{}}
	if len(candidates) > googleMatchCandidatesMax {
		candidates, out.Truncated = candidates[:googleMatchCandidatesMax], true
	}
	out.Candidates = len(candidates)
	if len(candidates) == 0 {
		return out, nil
	}
	src, err := d.footing(readCtx)
	if err != nil {
		if errors.Is(err, contracts.ErrDirectoryUnavailable) {
			return contracts.DirectoryMatches{}, err
		}
		return fail(err, false)
	}
	accounts, partial, err := d.readAccounts(readCtx, src)
	if err != nil {
		return fail(err, true)
	}
	if partial {
		out.Truncated = true
	}
	proposals, ambiguous := matchAccounts(candidates, accounts, !partial)
	out.Ambiguous = ambiguous
	proposed := make([]searchAccount, 0, len(proposals))
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
		// left out; the candidate's own mapping does not block them.
		if h := held[p.account.ID]; h != nil && (h.UserID == nil || *h.UserID != p.userID) {
			continue
		}
		account, ok := p.account.contractUser(nil)
		if !ok {
			continue
		}
		out.Matches = append(out.Matches, contracts.DirectoryMatch{UserID: p.userID, Account: account, MatchedBy: p.matchedBy})
	}
	return out, nil
}

// readAccounts is the organisation's users, read once in pages of
// matchPageSize, at most matchPagesMax of them; partial when a page was
// left unread.
func (d *directorySearch) readAccounts(ctx context.Context, src tokenSource) ([]searchAccount, bool, error) {
	var out []searchAccount
	token := ""
	for range matchPagesMax {
		v := url.Values{
			"customer": {"my_customer"}, "projection": {"basic"}, "viewType": {"admin_view"},
			"maxResults": {strconv.Itoa(matchPageSize)},
		}
		if token != "" {
			v.Set("pageToken", token)
		}
		var page searchPage
		if err := d.keys.c.get(ctx, src, directoryBase+"/users?"+v.Encode(), &page); err != nil {
			return nil, false, err
		}
		out = append(out, page.Users...)
		if token = page.NextPageToken; token == "" {
			return out, false, nil
		}
	}
	return out, true, nil
}

// accountProposal is a candidate and the one account matchAccounts proposes
// for them, with what matched.
type accountProposal struct {
	userID    uuid.UUID
	account   searchAccount
	matchedBy []string
}

// matchAccounts is the exact rule of Entra's matches over Google's keys. A
// key of a candidate matches an account when
//
//   - the candidate's address equals the account's primary email (trimmed,
//     ignoring case): mail and principalName, the primary email being both;
//   - it equals one of the account's aliases (searchAccount.aliases): mail;
//   - byNumber, and the candidate's employee number equals the account's
//     organization external id, trimmed and ignoring case: employeeNumber.
//
// A candidate whose keys matched no account is neither proposed nor
// ambiguous. A candidate is proposed, with every key that matched, when its
// keys matched exactly one account and no other candidate's key matched that
// account; it is ambiguous — left out and counted — when its keys matched
// two accounts or more, or one account another candidate's key matched too.
// An address identity made up (Email "") and an empty number match nothing.
// Proposals keep the candidates' order.
func matchAccounts(candidates []contracts.ConnectCandidate, accounts []searchAccount, byNumber bool) ([]accountProposal, int) {
	byPrimary, byAlias, byEmployee := map[string][]int{}, map[string][]int{}, map[string][]int{}
	for i, a := range accounts {
		if k := addressKey(a.PrimaryEmail); k != "" {
			byPrimary[k] = append(byPrimary[k], i)
		}
		for _, k := range a.aliases() {
			byAlias[k] = append(byAlias[k], i)
		}
		if byNumber {
			if k := numberKey(a.employeeNumber()); k != "" {
				byEmployee[k] = append(byEmployee[k], i)
			}
		}
	}

	// hits[c] is, per account candidate c's keys matched, what matched it.
	hits := make([]map[int][]string, len(candidates))
	// claimed[a] is how many candidates' keys matched account a.
	claimed := map[int]int{}
	for c, cand := range candidates {
		hits[c] = map[int][]string{}
		mark := func(index map[string][]int, key string, by ...string) {
			if key == "" {
				return
			}
			for _, a := range index[key] {
				for _, b := range by {
					if !slices.Contains(hits[c][a], b) {
						hits[c][a] = append(hits[c][a], b)
					}
				}
			}
		}
		address := addressKey(cand.Email)
		mark(byPrimary, address, contracts.DirectoryMatchedByMail, contracts.DirectoryMatchedByPrincipalName)
		mark(byAlias, address, contracts.DirectoryMatchedByMail)
		if byNumber {
			mark(byEmployee, numberKey(cand.EmployeeNumber), contracts.DirectoryMatchedByEmployeeNumber)
		}
		for a := range hits[c] {
			claimed[a]++
		}
	}

	order := []string{contracts.DirectoryMatchedByMail, contracts.DirectoryMatchedByPrincipalName, contracts.DirectoryMatchedByEmployeeNumber}
	var proposals []accountProposal
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
				slices.SortFunc(by, func(x, y string) int { return slices.Index(order, x) - slices.Index(order, y) })
				proposals = append(proposals, accountProposal{userID: cand.UserID, account: accounts[a], matchedBy: by})
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
