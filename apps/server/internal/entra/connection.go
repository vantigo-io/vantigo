package entra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
)

// This file is the connection (Entra design D11, D15): its read, the
// Owner's registration of the tenant and the client (step 1) and the test
// (step 3), entra:manage's account rules (step 4) and the tenant's licences.
// The two PUTs run under the row's lock and If-Match on its version, and move
// it on; what a test finds is written beside it and leaves the version
// alone, so an open form is not refused for it.

// requiredPermissions are the application permissions the module needs
// (research §3.6), in the wizard's order: what the connection test compares
// the token's roles with.
var requiredPermissions = []string{
	"User.ReadWrite.All",
	"User-LifeCycleInfo.ReadWrite.All",
	"User.RevokeSessions.All",
	"UserAuthMethod-TAP.ReadWrite.All",
	"LicenseAssignment.ReadWrite.All",
	"Group.ReadWrite.All",
	"Domain.Read.All",
	"Organization.Read.All",
}

// The account rules' bounds.
const (
	minPassLifetimeMinutes = 10
	maxPassLifetimeMinutes = 43200
	maxDomainLength        = 253
)

var (
	// guidPattern is a GUID as the portal's Overview shows it.
	guidPattern = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)
	// domainPattern is a lower-case DNS name of two labels or more.
	domainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// ifMatchMessage is the field error of an If-Match that is not a version.
const ifMatchMessage = "If-Match is the version the connection was read at, such as 3."

// parseVersion reads an If-Match version, bare (3), quoted ("3") or weak
// (W/"3").
func parseVersion(raw string) (int32, bool) {
	v := strings.TrimPrefix(strings.TrimSpace(raw), "W/")
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil || n < 0 {
		return 0, false
	}
	return int32(n), true
}

// badIfMatch is the 400 of an If-Match that is not a version.
func badIfMatch() gen.EntraProblem {
	var f fieldErrors
	f.add("If-Match", ifMatchMessage)
	return invalid("Invalid If-Match", f)
}

// missingPermissions are requiredPermissions less present, in their order.
func missingPermissions(present []string) []string {
	out := []string{}
	for _, p := range requiredPermissions {
		if !slices.Contains(present, p) {
			out = append(out, p)
		}
	}
	return out
}

// orEmpty is s, or an empty slice for nil, so an array is never null.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// uuidPtr is u as the contract's UUID, nil for nil.
func uuidPtr(u *uuid.UUID) *openapi_types.UUID {
	if u == nil {
		return nil
	}
	v := openapi_types.UUID(*u)
	return &v
}

// connectionBody is the connection as GET /connection answers it at now.
func connectionBody(conn store.EntraConnection, creds []store.EntraCredential, counts store.CountAccountsRow, now time.Time) gen.EntraConnection {
	status, reason := statusOf(conn, creds, now)
	body := gen.EntraConnection{
		Status:             gen.EntraConnectionStatus(status),
		TenantId:           uuidPtr(conn.TenantID),
		ClientId:           uuidPtr(conn.ClientID),
		OrganisationName:   conn.OrganisationName,
		VerifiedDomains:    orEmpty(conn.VerifiedDomains),
		PermissionsPresent: orEmpty(conn.PermissionsPresent),
		PermissionsMissing: missingPermissions(conn.PermissionsPresent),
		LastCheckedAt:      conn.LastCheckedAt,
		Settings: gen.EntraSettings{
			UpnDomain:            conn.UpnDomain,
			UpnPattern:           gen.EntraSettingsUpnPattern(conn.UpnPattern),
			DefaultUsageLocation: conn.DefaultUsageLocation,
			LeaverStripLicences:  conn.LeaverStripLicences,
			LeaverRemoveGroups:   gen.EntraSettingsLeaverRemoveGroups(conn.LeaverRemoveGroups),
			LeaverRevokeSessions: conn.LeaverRevokeSessions,
			LeaverWriteLeaveDate: conn.LeaverWriteLeaveDate,
			WriteHireDate:        conn.WriteHireDate,
			PassLifetimeMinutes:  conn.PassLifetimeMinutes,
			PassUsableOnce:       conn.PassUsableOnce,
			PassDelivery:         gen.EntraSettingsPassDelivery(conn.PassDelivery),
		},
		Credentials: credentialsBody(creds, now),
		Accounts:    gen.EntraAccountCounts{Count: counts.Total, Enabled: counts.Enabled, Disabled: counts.Disabled, Deleted: counts.Deleted},
		Version:     conn.Version,
	}
	if reason != "" {
		body.StatusReason = &reason
	}
	return body
}

// readConnection is the connection's body read through q at now.
func readConnection(ctx context.Context, q *store.Queries, conn store.EntraConnection, now time.Time) (gen.EntraConnection, error) {
	creds, err := q.ListCredentials(ctx)
	if err != nil {
		return gen.EntraConnection{}, fmt.Errorf("entra: read the secrets: %w", err)
	}
	counts, err := q.CountAccounts(ctx)
	if err != nil {
		return gen.EntraConnection{}, fmt.Errorf("entra: count the accounts: %w", err)
	}
	return connectionBody(conn, creds, counts, now), nil
}

// GetEntraConnection answers the connection from the module's rows; nothing
// reaches the tenant. (GET /api/v1/entra/connection)
func (s *server) GetEntraConnection(ctx context.Context, _ gen.GetEntraConnectionRequestObject) (gen.GetEntraConnectionResponseObject, error) {
	conn, err := s.q.GetConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("entra: read the connection: %w", err)
	}
	body, err := readConnection(ctx, s.q, conn, s.deps.Clock())
	if err != nil {
		return nil, err
	}
	return gen.GetEntraConnection200JSONResponse(body), nil
}

// PutEntraConnection sets the tenant and the client (step 1), both GUIDs,
// under If-Match. A change sends the status back to unconfigured and
// forgets what the last test found; while the module keeps accounts it
// needs confirmChange, and deletes nothing in the tenant either way. The
// same pair again writes nothing. Audited entra.connection.changed with the
// ids before and after. (PUT /api/v1/entra/connection)
func (s *server) PutEntraConnection(ctx context.Context, req gen.PutEntraConnectionRequestObject) (gen.PutEntraConnectionResponseObject, error) {
	version, ok := parseVersion(req.Params.IfMatch)
	if !ok {
		return gen.PutEntraConnection400ApplicationProblemPlusJSONResponse(badIfMatch()), nil
	}
	var in gen.EntraConnectionRequest
	if req.Body != nil {
		in = *req.Body
	}
	var f fieldErrors
	tenant, err := parseGUID(in.TenantId)
	if err != nil {
		f.add("tenantId", "The tenant id is a GUID, as the app registration's Overview shows it.")
	}
	client, err := parseGUID(in.ClientId)
	if err != nil {
		f.add("clientId", "The client id is a GUID, as the app registration's Overview shows it.")
	}
	if f.failed() {
		return gen.PutEntraConnection400ApplicationProblemPlusJSONResponse(invalid("Invalid connection", f)), nil
	}
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	confirmed := in.ConfirmChange != nil && *in.ConfirmChange
	now := s.deps.Clock()
	var body gen.EntraConnection
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		conn, err := q.LockConnection(ctx)
		if err != nil {
			return fmt.Errorf("entra: lock the connection: %w", err)
		}
		if conn.Version != version {
			return refusal{versionMismatch(conn.Version, version)}
		}
		same := conn.TenantID != nil && *conn.TenantID == tenant && conn.ClientID != nil && *conn.ClientID == client
		if !same {
			if !confirmed {
				counts, err := q.CountAccounts(ctx)
				if err != nil {
					return fmt.Errorf("entra: count the accounts: %w", err)
				}
				if counts.Total > 0 {
					return refusal{connectionInUse(counts.Total)}
				}
			}
			updated, err := q.UpdateConnectionRegistration(ctx, store.UpdateConnectionRegistrationParams{
				TenantID: &tenant, ClientID: &client, Now: now, UpdatedByUserID: actor.UserID,
			})
			if err != nil {
				return fmt.Errorf("entra: set the tenant and the client: %w", err)
			}
			codes := []string{
				"tenant_before:" + idOrNone(conn.TenantID), "tenant_after:" + tenant.String(),
				"client_before:" + idOrNone(conn.ClientID), "client_after:" + client.String(),
			}
			if err := recordEvent(ctx, q, now, actor, actionConnectionChanged, nil, "", codes); err != nil {
				return err
			}
			conn = updated
		}
		body, err = readConnection(ctx, q, conn, now)
		return err
	})
	var r refusal
	switch {
	case errors.As(err, &r) && r.status() == http.StatusConflict:
		return gen.PutEntraConnection409ApplicationProblemPlusJSONResponse(r.problem), nil
	case errors.As(err, &r):
		return gen.PutEntraConnection412ApplicationProblemPlusJSONResponse(r.problem), nil
	case err != nil:
		return nil, err
	}
	return gen.PutEntraConnection200JSONResponse(body), nil
}

// parseGUID reads a GUID in the portal's form, answered lower-case.
func parseGUID(s string) (uuid.UUID, error) {
	s = strings.TrimSpace(s)
	if !guidPattern.MatchString(s) {
		return uuid.Nil, errors.New("not a GUID")
	}
	return uuid.Parse(s)
}

// idOrNone is an id's text, "none" for nil.
func idOrNone(id *uuid.UUID) string {
	if id == nil {
		return "none"
	}
	return id.String()
}

// mergeSettings is the settings request merged over the row: a field left
// out keeps its value, and every rule that fails is a field error under
// its own name.
func mergeSettings(in gen.EntraSettingsRequest, conn store.EntraConnection) (store.UpdateConnectionSettingsParams, []string, fieldErrors) {
	p := store.UpdateConnectionSettingsParams{
		UpnDomain:            conn.UpnDomain,
		UpnPattern:           conn.UpnPattern,
		DefaultUsageLocation: conn.DefaultUsageLocation,
		LeaverStripLicences:  conn.LeaverStripLicences,
		LeaverRemoveGroups:   conn.LeaverRemoveGroups,
		LeaverRevokeSessions: conn.LeaverRevokeSessions,
		LeaverWriteLeaveDate: conn.LeaverWriteLeaveDate,
		WriteHireDate:        conn.WriteHireDate,
		PassLifetimeMinutes:  conn.PassLifetimeMinutes,
		PassUsableOnce:       conn.PassUsableOnce,
		PassDelivery:         conn.PassDelivery,
	}
	var f fieldErrors
	var changed []string
	mark := func(field string, differs bool) {
		if differs {
			changed = append(changed, field)
		}
	}
	if in.UpnDomain != nil {
		domain := strings.ToLower(strings.TrimSpace(*in.UpnDomain))
		switch {
		case len(conn.VerifiedDomains) > 0:
			i := slices.IndexFunc(conn.VerifiedDomains, func(d string) bool { return strings.EqualFold(d, domain) })
			if i < 0 {
				f.add("upnDomain", "The UPN domain is one of the tenant's verified domains: "+strings.Join(conn.VerifiedDomains, ", ")+".")
			}
		case len(domain) > maxDomainLength || !domainPattern.MatchString(domain):
			f.add("upnDomain", "The UPN domain is a domain name, such as example.com.")
		}
		mark("upnDomain", p.UpnDomain == nil || *p.UpnDomain != domain)
		p.UpnDomain = &domain
	}
	if in.UpnPattern != nil {
		if !in.UpnPattern.Valid() {
			f.add("upnPattern", "The UPN pattern is given.family, g.family or given.")
		}
		mark("upnPattern", p.UpnPattern != string(*in.UpnPattern))
		p.UpnPattern = string(*in.UpnPattern)
	}
	if in.DefaultUsageLocation != nil {
		country := strings.ToUpper(strings.TrimSpace(*in.DefaultUsageLocation))
		if !validCountry(country) {
			f.add("defaultUsageLocation", "The usage location is a country's two letters, ISO 3166-1 alpha-2, such as NO.")
		}
		mark("defaultUsageLocation", p.DefaultUsageLocation != country)
		p.DefaultUsageLocation = country
	}
	if in.LeaverRemoveGroups != nil {
		if !in.LeaverRemoveGroups.Valid() {
			f.add("leaverRemoveGroups", "A leaver's groups are all, managed or none.")
		}
		mark("leaverRemoveGroups", p.LeaverRemoveGroups != string(*in.LeaverRemoveGroups))
		p.LeaverRemoveGroups = string(*in.LeaverRemoveGroups)
	}
	if in.PassLifetimeMinutes != nil {
		if n := *in.PassLifetimeMinutes; n < minPassLifetimeMinutes || n > maxPassLifetimeMinutes {
			f.add("passLifetimeMinutes", fmt.Sprintf("The pass's lifetime is %d to %d minutes.", minPassLifetimeMinutes, maxPassLifetimeMinutes))
		}
		mark("passLifetimeMinutes", p.PassLifetimeMinutes != *in.PassLifetimeMinutes)
		p.PassLifetimeMinutes = *in.PassLifetimeMinutes
	}
	if in.PassDelivery != nil {
		if !in.PassDelivery.Valid() {
			f.add("passDelivery", "The pass is delivered by hr, manager or both.")
		}
		mark("passDelivery", p.PassDelivery != string(*in.PassDelivery))
		p.PassDelivery = string(*in.PassDelivery)
	}
	for _, b := range []struct {
		field string
		in    *bool
		to    *bool
	}{
		{"leaverStripLicences", in.LeaverStripLicences, &p.LeaverStripLicences},
		{"leaverRevokeSessions", in.LeaverRevokeSessions, &p.LeaverRevokeSessions},
		{"leaverWriteLeaveDate", in.LeaverWriteLeaveDate, &p.LeaverWriteLeaveDate},
		{"writeHireDate", in.WriteHireDate, &p.WriteHireDate},
		{"passUsableOnce", in.PassUsableOnce, &p.PassUsableOnce},
	} {
		if b.in != nil {
			mark(b.field, *b.to != *b.in)
			*b.to = *b.in
		}
	}
	return p, changed, f
}

// PutEntraConnectionSettings sets the account rules (step 4) under
// If-Match: every field optional and kept when left out, each refusal a
// field error. Audited entra.settings.changed with the names of the fields
// that changed. (PUT /api/v1/entra/connection/settings)
func (s *server) PutEntraConnectionSettings(ctx context.Context, req gen.PutEntraConnectionSettingsRequestObject) (gen.PutEntraConnectionSettingsResponseObject, error) {
	version, ok := parseVersion(req.Params.IfMatch)
	if !ok {
		return gen.PutEntraConnectionSettings400ApplicationProblemPlusJSONResponse(badIfMatch()), nil
	}
	var in gen.EntraSettingsRequest
	if req.Body != nil {
		in = *req.Body
	}
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	var body gen.EntraConnection
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		conn, err := q.LockConnection(ctx)
		if err != nil {
			return fmt.Errorf("entra: lock the connection: %w", err)
		}
		if conn.Version != version {
			return refusal{versionMismatch(conn.Version, version)}
		}
		p, changed, f := mergeSettings(in, conn)
		if f.failed() {
			return refusal{invalid("Invalid account rules", f)}
		}
		p.Now, p.UpdatedByUserID = now, actor.UserID
		updated, err := q.UpdateConnectionSettings(ctx, p)
		if err != nil {
			return fmt.Errorf("entra: set the account rules: %w", err)
		}
		if err := recordEvent(ctx, q, now, actor, actionSettingsChanged, nil, "", changed); err != nil {
			return err
		}
		body, err = readConnection(ctx, q, updated, now)
		return err
	})
	var r refusal
	switch {
	case errors.As(err, &r) && r.status() == http.StatusBadRequest:
		return gen.PutEntraConnectionSettings400ApplicationProblemPlusJSONResponse(r.problem), nil
	case errors.As(err, &r):
		return gen.PutEntraConnectionSettings412ApplicationProblemPlusJSONResponse(r.problem), nil
	case err != nil:
		return nil, err
	}
	return gen.PutEntraConnectionSettings200JSONResponse(body), nil
}

// organisation is what the connection test reads of GET /organization.
type organisation struct {
	Value []struct {
		DisplayName     string `json:"displayName"`
		VerifiedDomains []struct {
			Name       string `json:"name"`
			IsVerified *bool  `json:"isVerified"`
		} `json:"verifiedDomains"`
	} `json:"value"`
}

// PostEntraConnectionTest tests the connection (step 3): a fresh token
// with the newest valid secret, GET /organization for the name and the
// verified domains, and the token's roles compared with
// requiredPermissions. The status is connected when the token came, it
// carried roles and the organisation answered; else failing with
// credential_invalid (no secret fetched a token), tenant_unreachable (the
// tenant did not answer) or consent_missing (no roles at all, or the
// organisation refused for want of a permission). What it found is written
// to the connection and audited entra.connection.tested; the answer is 200
// whatever it found. Whether the application holds the User Administrator
// role cannot be read without a further permission: roleKnown is false.
// (POST /api/v1/entra/connection/test)
func (s *server) PostEntraConnectionTest(ctx context.Context, _ gen.PostEntraConnectionTestRequestObject) (gen.PostEntraConnectionTestResponseObject, error) {
	actor, err := requestActor(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := s.q.GetConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("entra: read the connection: %w", err)
	}
	if conn.TenantID == nil || conn.ClientID == nil {
		return gen.PostEntraConnectionTest200JSONResponse(gen.EntraConnectionTest{
			Status:             gen.EntraConnectionTestStatus(contracts.ProviderUnconfigured),
			VerifiedDomains:    orEmpty(conn.VerifiedDomains),
			PermissionsPresent: []string{},
			PermissionsMissing: missingPermissions(nil),
		}), nil
	}

	status, reason := contracts.ProviderConnected, ""
	write := store.SetConnectionStatusParams{PermissionsPresent: []string{}}
	cred, err := pickCredential(ctx, s.q, s.client, true)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		status, reason = contracts.ProviderFailing, reasonFor(err)
	} else {
		write.PermissionsPresent = orEmpty(cred.roles)
		var org organisation
		orgErr := s.client.get(ctx, cred, "/organization?%24select=displayName%2CverifiedDomains", &org)
		switch {
		case len(cred.roles) == 0:
			status, reason = contracts.ProviderFailing, reasonConsentMissing
		case isStatus(orgErr, http.StatusUnauthorized) || isStatus(orgErr, http.StatusForbidden):
			status, reason = contracts.ProviderFailing, reasonConsentMissing
		case orgErr != nil:
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			status, reason = contracts.ProviderFailing, reasonTenantUnreachable
		}
		if orgErr == nil && len(org.Value) > 0 {
			name := org.Value[0].DisplayName
			write.OrganisationName = &name
			domains := []string{}
			for _, d := range org.Value[0].VerifiedDomains {
				if d.Name != "" && (d.IsVerified == nil || *d.IsVerified) {
					domains = append(domains, strings.ToLower(d.Name))
				}
			}
			slices.Sort(domains)
			write.VerifiedDomains = slices.Compact(domains)
		}
	}

	now := s.deps.Clock()
	write.Status, write.Now, write.LastCheckedAt = status, now, &now
	if reason != "" {
		write.StatusReason = &reason
	}
	updated, err := s.q.SetConnectionStatus(ctx, write)
	if err != nil {
		return nil, fmt.Errorf("entra: record the test: %w", err)
	}
	missing := missingPermissions(updated.PermissionsPresent)
	codes := []string{status}
	if reason != "" {
		codes = append(codes, reason)
	}
	for _, m := range missing {
		codes = append(codes, "missing:"+m)
	}
	if err := recordEvent(ctx, s.q, now, actor, actionConnectionTested, nil, "", codes); err != nil {
		return nil, err
	}
	answer := gen.EntraConnectionTest{
		Status:             gen.EntraConnectionTestStatus(status),
		OrganisationName:   updated.OrganisationName,
		VerifiedDomains:    orEmpty(updated.VerifiedDomains),
		PermissionsPresent: orEmpty(updated.PermissionsPresent),
		PermissionsMissing: missing,
		RoleKnown:          false,
	}
	if reason != "" {
		answer.Reason = &reason
	}
	return gen.PostEntraConnectionTest200JSONResponse(answer), nil
}

// subscribedSkus is what GET /subscribedSkus answers of each licence.
type subscribedSkus struct {
	Value []struct {
		SkuID         string `json:"skuId"`
		SkuPartNumber string `json:"skuPartNumber"`
		ConsumedUnits int32  `json:"consumedUnits"`
		PrepaidUnits  struct {
			Enabled int32 `json:"enabled"`
		} `json:"prepaidUnits"`
	} `json:"value"`
}

// GetEntraConnectionSkus answers the tenant's licences through the
// connection, by part number: 503 credential_invalid when no secret fetched
// a token, tenant_unreachable when the tenant did not answer.
// (GET /api/v1/entra/connection/skus)
func (s *server) GetEntraConnectionSkus(ctx context.Context, _ gen.GetEntraConnectionSkusRequestObject) (gen.GetEntraConnectionSkusResponseObject, error) {
	cred, err := credentialFor(ctx, s.q, s.client)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return gen.GetEntraConnectionSkus503ApplicationProblemPlusJSONResponse(tenantUnavailable(reasonFor(err))), nil
	}
	var skus subscribedSkus
	if err := s.client.get(ctx, cred, "/subscribedSkus", &skus); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return gen.GetEntraConnectionSkus503ApplicationProblemPlusJSONResponse(tenantUnavailable(codeTenantUnreachable)), nil
	}
	out := make(gen.GetEntraConnectionSkus200JSONResponse, 0, len(skus.Value))
	for _, v := range skus.Value {
		id, err := uuid.Parse(v.SkuID)
		if err != nil || v.SkuPartNumber == "" {
			continue
		}
		out = append(out, gen.EntraSku{
			SkuId: openapi_types.UUID(id), SkuPartNumber: v.SkuPartNumber,
			Enabled: v.PrepaidUnits.Enabled, Consumed: v.ConsumedUnits,
		})
	}
	slices.SortFunc(out, func(a, b gen.EntraSku) int { return strings.Compare(a.SkuPartNumber, b.SkuPartNumber) })
	return out, nil
}
