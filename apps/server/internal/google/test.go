package google

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
)

// This file is Test connection (Google Workspace design G15): one fresh
// token per scope the module needs, asked as the admin to impersonate, one
// per scope it never uses but an over-wide admin is likely to delegate, then
// the organisation (customers.get) and its verified domains (domains.list).
// What it found is written to the connection beside its settings, leaving
// the version alone — unless the version moved on during the test (another
// admin named), when nothing is written.

// scopePrefix starts every scope but https://mail.google.com/.
const scopePrefix = "https://www.googleapis.com/auth/"

// maxOrganisationName is the most of Google's organizationName kept, in
// characters: organisation_name is varchar(256).
const maxOrganisationName = 256

// The scopes delivery A needs: the customer and its domains.
const (
	scopeCustomer = scopePrefix + "admin.directory.customer.readonly"
	scopeDomains  = scopePrefix + "admin.directory.domain.readonly"
)

// neededScopes are the scopes the admin is asked to delegate, in the order
// the page lists them, written in full (G15's table). Each delivery adds
// its rows here — A the customer's and the domains', B the accounts',
// groups', group settings' and licences' (accountScopes); F will add the two
// Gmail scopes while Mail channels is on.
func neededScopes() []string {
	return append([]string{scopeCustomer, scopeDomains}, accountScopes...)
}

// unexpectedProbes are the scopes the module never uses that the test asks a
// token for, to list any the admin delegated (G15): a token is the only way
// to learn what is delegated. The Gmail scopes are among them while Mail
// channels — delivery F's switch — does not exist or is off. No calendar
// scope is ever delegated (Q3).
var unexpectedProbes = []string{
	"https://mail.google.com/",
	scopePrefix + "gmail.readonly",
	scopePrefix + "gmail.modify",
	scopePrefix + "gmail.send",
	scopePrefix + "calendar",
	scopePrefix + "calendar.events",
	scopePrefix + "admin.directory.user.security",
	scopePrefix + "drive",
	scopePrefix + "cloud-platform",
}

// customerAnswer is what customers.get(my_customer) answers that the test
// reads.
type customerAnswer struct {
	ID             string `json:"id"`
	CustomerDomain string `json:"customerDomain"`
	PostalAddress  struct {
		OrganizationName string `json:"organizationName"`
	} `json:"postalAddress"`
}

// domainsAnswer is what domains.list answers that the test reads.
type domainsAnswer struct {
	Domains []struct {
		DomainName string `json:"domainName"`
		Verified   bool   `json:"verified"`
		IsPrimary  bool   `json:"isPrimary"`
	} `json:"domains"`
}

// testOutcome is what one test found.
type testOutcome struct {
	status, reason              string
	granted, missing, unexpect  []string
	customer                    *customerAnswer
	domains                     []string
	primaryDomain, organisation string
}

// probe asks a fresh token for scope as subject, as mode says: true when
// Google issued it, false when it refused the scope; any other failure is
// its error.
func (s *server) probe(ctx context.Context, scope, subject string, mode fetchMode) (bool, error) {
	_, _, err := s.keys.token(ctx, []string{scope}, subject, mode)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, errNotDelegated):
		return false, nil
	default:
		return false, err
	}
}

// failure is the status reason err reads as when the test cannot go on.
func failure(err error) string {
	switch {
	case errors.Is(err, errNoCredential), errors.Is(err, errCredentialInvalid), errors.Is(err, errNotConfigured):
		return reasonCredentialInvalid
	case errors.Is(err, errAdminRefused), errors.Is(err, errSubjectInvalid):
		return reasonAdminRefused
	default:
		return reasonGoogleUnreachable
	}
}

// run is the test against Google as subject. ctx ending is its error;
// everything Google answered is in the outcome.
func (s *server) run(ctx context.Context, subject string) (testOutcome, error) {
	out := testOutcome{status: statusConnected, granted: []string{}, missing: []string{}, unexpect: []string{}}
	for _, scope := range neededScopes() {
		ok, err := s.probe(ctx, scope, subject, tokenFresh)
		switch {
		case ctx.Err() != nil:
			return testOutcome{}, ctx.Err()
		case err != nil:
			out.status, out.reason = statusFailing, failure(err)
			return out, nil
		case ok:
			out.granted = append(out.granted, scope)
		default:
			out.missing = append(out.missing, scope)
		}
	}
	for _, scope := range unexpectedProbes {
		ok, err := s.probe(ctx, scope, subject, tokenProbe)
		switch {
		case ctx.Err() != nil:
			return testOutcome{}, ctx.Err()
		case err != nil:
			// The needed scopes' tokens came with this key and admin, so a
			// probe's failure is Google's passing trouble: not reported.
		case ok:
			out.unexpect = append(out.unexpect, scope)
		}
	}
	if len(out.granted) == 0 {
		out.status, out.reason = statusPendingDelegation, reasonNotDelegated
		return out, nil
	}
	if slices.Contains(out.granted, scopeCustomer) {
		var c customerAnswer
		if err := s.keys.c.get(ctx, s.keys.tokenSource([]string{scopeCustomer}, subject), directoryBase+"/customers/my_customer", &c); err != nil {
			if ctx.Err() != nil {
				return testOutcome{}, ctx.Err()
			}
			out.status, out.reason = statusFailing, failure(err)
			return out, nil
		}
		out.customer = &c
		out.organisation = strings.TrimSpace(c.PostalAddress.OrganizationName)
		if r := []rune(out.organisation); len(r) > maxOrganisationName {
			out.organisation = strings.TrimSpace(string(r[:maxOrganisationName]))
		}
	}
	if slices.Contains(out.granted, scopeDomains) {
		var d domainsAnswer
		if err := s.keys.c.get(ctx, s.keys.tokenSource([]string{scopeDomains}, subject), directoryBase+"/customer/my_customer/domains", &d); err != nil {
			if ctx.Err() != nil {
				return testOutcome{}, ctx.Err()
			}
			out.status, out.reason = statusFailing, failure(err)
			return out, nil
		}
		domains := []string{}
		for _, v := range d.Domains {
			name := strings.ToLower(strings.TrimSpace(v.DomainName))
			if name == "" || !v.Verified {
				continue
			}
			domains = append(domains, name)
			if v.IsPrimary {
				out.primaryDomain = name
			}
		}
		slices.Sort(domains)
		out.domains = slices.Compact(domains)
	}
	if out.primaryDomain == "" && out.customer != nil {
		out.primaryDomain = strings.ToLower(out.customer.CustomerDomain)
	}
	return out, nil
}

// PostGoogleConnectionTest tests the connection (G15) as the admin to
// impersonate with the newest live key, writes what it found and answers it,
// 200 whatever it found: pending_delegation/not_delegated when every needed
// scope was refused; connected, the refused ones listed missing, when some
// were; failing with credential_invalid (no key fetched a token),
// admin_refused (Google does not know the admin, or an Admin SDK call
// answered 403) or google_unreachable. A test whose connection changed
// version meanwhile is answered and neither written nor audited. Without a live key or an admin it is unconfigured and
// asks Google nothing. Audited google.connection.tested with the status,
// the reason and the scopes, as codes. (POST /api/v1/google/connection/test)
func (s *server) PostGoogleConnectionTest(ctx context.Context, _ gen.PostGoogleConnectionTestRequestObject) (gen.PostGoogleConnectionTestResponseObject, error) {
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := s.q.GetConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("google: read the connection: %w", err)
	}
	creds, err := s.q.ListCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("google: read the credentials: %w", err)
	}
	if !configured(conn, creds) {
		return gen.PostGoogleConnectionTest200JSONResponse(gen.GoogleConnectionTest{
			Status:     gen.GoogleConnectionTestStatus(statusUnconfigured),
			Granted:    []string{},
			Missing:    neededScopes(),
			Unexpected: []string{},
			Domains:    orEmpty(conn.Domains),
		}), nil
	}

	out, err := s.run(ctx, *conn.AdminSubject)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	write := store.SetConnectionTestParams{
		Status: out.status, ScopesGranted: out.granted, ScopesMissing: out.missing, ScopesUnexpected: out.unexpect, Now: now,
		Version: conn.Version,
	}
	if out.reason != "" {
		write.StatusReason = &out.reason
	}
	if out.customer != nil {
		write.CustomerID = &out.customer.ID
		if out.organisation != "" {
			write.OrganisationName = &out.organisation
		}
	}
	if out.primaryDomain != "" {
		write.PrimaryDomain = &out.primaryDomain
	}
	if out.domains != nil {
		write.Domains = out.domains
	}
	codes := []string{out.status}
	if out.reason != "" {
		codes = append(codes, out.reason)
	}
	for _, m := range out.missing {
		codes = append(codes, "missing:"+m)
	}
	for _, u := range out.unexpect {
		codes = append(codes, "unexpected:"+u)
	}
	// The test is written, and audited, only over the row it read: when the
	// admin changed meanwhile, it tested the one no longer named, and is
	// answered without being kept.
	updated := store.GoogleConnection{CustomerID: write.CustomerID, OrganisationName: write.OrganisationName, PrimaryDomain: write.PrimaryDomain, Domains: out.domains}
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		row, err := q.SetConnectionTest(ctx, write)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("google: record the test: %w", err)
		}
		updated = row
		return recordEvent(ctx, q, now, actor, actionConnectionTested, deref(row.CustomerID), codes)
	})
	if err != nil {
		return nil, err
	}
	answer := gen.GoogleConnectionTest{
		Status:     gen.GoogleConnectionTestStatus(out.status),
		Granted:    out.granted,
		Missing:    out.missing,
		Unexpected: out.unexpect,
		Domains:    orEmpty(updated.Domains),
	}
	if out.reason != "" {
		answer.Reason = &out.reason
	}
	if updated.CustomerID != nil {
		answer.Customer = &gen.GoogleCustomer{Id: *updated.CustomerID, OrganisationName: updated.OrganisationName, PrimaryDomain: updated.PrimaryDomain}
	}
	return gen.PostGoogleConnectionTest200JSONResponse(answer), nil
}
