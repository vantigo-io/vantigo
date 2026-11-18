package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// This file is the integrations service (API access design K8, K11-K13,
// K21, K22a): an Owner's screen for the accounts of external applications and
// their API keys, and the only writer of an integration's roles (K3, K55).
// Every operation is policy:Owner+OwnerManagement, never open to an API key
// or the Assistant.

const (
	maxIntegrationNameLength        = 100
	maxIntegrationDescriptionLength = 1000
	maxAPIKeyNameLength             = 100
	maxAPIKeyNetworks               = 20
	minRateLimitPerMinute           = 60
	maxRateLimitPerMinute           = 6000
	defaultAPIKeyDays               = 90
	maxAPIKeyLifetime               = 365 * 24 * time.Hour
	defaultRollOverlap              = 24 * time.Hour
	maxRollOverlap                  = 7 * 24 * time.Hour
	// integrationEmailDomain is the RFC 2606 domain an integration's
	// address is under: it never routes (K1).
	integrationEmailDomain = "integrations.invalid"
	// secretCacheControl keeps a secret out of every cache (K11).
	secretCacheControl = "no-store"
)

// apiKeyDayChoices are the expiries a key may be given in days (K12).
var apiKeyDayChoices = []int{30, 90, 180, 365}

// The audit actions of K22a.
const (
	auditIntegrationCreated      = "integration.created"
	auditIntegrationUpdated      = "integration.updated"
	auditIntegrationRolesChanged = "integration.roles_changed"
	auditIntegrationDisabled     = "integration.disabled"
	auditIntegrationEnabled      = "integration.enabled"
	auditIntegrationArchived     = "integration.archived"
	auditAPIKeyCreated           = "api_key.created"
	auditAPIKeyRolled            = "api_key.rolled"
	auditAPIKeyRevoked           = "api_key.revoked"
	auditAPIKeyExpired           = "api_key.expired"
)

// The words of the integrations service's refusals.
const (
	integrationNameMessage        = "A name of 1 to 100 characters is required."
	integrationDescriptionMessage = "The description may be at most 1,000 characters."
	integrationContactMessage     = "The contact must be a person with an account."
	integrationRateLimitMessage   = "The rate limit is 60 to 6,000 requests a minute."
	integrationRolesMessage       = "Only custom roles may be given to an integration."
	apiKeyNameMessage             = "A name of 1 to 100 characters is required."
	apiKeyExpiryMessage           = "The expiry is 30, 90, 180 or 365 days, or a date at most 365 days ahead; give one or the other."
	apiKeyPermissionsMessage      = "Each permission is a key such as customers:view."
	apiKeyNetworksMessage         = "At most 20 networks, each a CIDR or an address."
	apiKeyOverlapMessage          = "The overlap is 0 to 168 hours."
	integrationIfMatchMessage     = "If-Match is the version the integration was read at."
)

var (
	integrationNotFound        = refuse(http.StatusNotFound, "not_found", "No such integration.", nil)
	apiKeyNotFound             = refuse(http.StatusNotFound, "not_found", "No such API key.", nil)
	integrationNameTaken       = refuse(http.StatusConflict, "integration_name_taken", "Another integration has this name.", map[string][]string{"name": {"Another integration has this name."}})
	integrationArchived        = refuse(http.StatusConflict, "integration_archived", "The integration is archived.", nil)
	apiKeyAlreadyRolled        = refuse(http.StatusConflict, "api_key_already_rolled", "This key has been rolled already.", nil)
	apiKeyNotLive              = refuse(http.StatusConflict, "api_key_revoked", "This key is revoked or has expired.", nil)
	integrationVersionMismatch = refuse(http.StatusPreconditionFailed, "version_mismatch", "The integration has been changed since it was read.", nil)
	invalidIntegrationRoles    = refuse(http.StatusBadRequest, "invalid_roles", integrationRolesMessage, map[string][]string{"roleIds": {integrationRolesMessage}})
)

// integrationAudit is an integration as the audit trail records it, before
// and after a change: never a key's hash or secret.
type integrationAudit struct {
	Name               string     `json:"name"`
	Description        *string    `json:"description"`
	ContactUserID      *uuid.UUID `json:"contactUserId"`
	RateLimitPerMinute int32      `json:"rateLimitPerMinute"`
	Roles              []string   `json:"roles"`
	Disabled           bool       `json:"disabled"`
	Archived           bool       `json:"archived"`
}

// apiKeyAudit is a key as the audit trail records it: its name, narrowing,
// read-only flag, networks and expiry, never its hash, its secret or its
// display.
type apiKeyAudit struct {
	KeyID           uuid.UUID  `json:"keyId"`
	Name            string     `json:"name"`
	Permissions     []string   `json:"permissions"`
	ReadOnly        bool       `json:"readOnly"`
	AllowedNetworks []string   `json:"allowedNetworks"`
	ExpiresAt       time.Time  `json:"expiresAt"`
	RolledFrom      *uuid.UUID `json:"rolledFrom,omitempty"`
	RevokedAt       *time.Time `json:"revokedAt,omitempty"`
}

func apiKeySnapshot(k store.IdentityApiKey) apiKeyAudit {
	return apiKeyAudit{
		KeyID: k.ID, Name: k.Name, Permissions: k.Permissions, ReadOnly: k.ReadOnly,
		AllowedNetworks: nonNil(networkStrings(k.AllowedNetworks)), ExpiresAt: k.ExpiresAt, RolledFrom: k.RolledFrom, RevokedAt: k.RevokedAt,
	}
}

// integrationSnapshot reads the integration's audit state on q.
func integrationSnapshot(ctx context.Context, q *store.Queries, row store.LockIntegrationRow) (integrationAudit, error) {
	roles, err := q.ListUserRoleAssignments(ctx, row.UserID)
	if err != nil {
		return integrationAudit{}, fmt.Errorf("identity: read the integration's roles: %w", err)
	}
	names := make([]string, 0, len(roles))
	for _, r := range orderAssignments(roles) {
		names = append(names, r.Name)
	}
	return integrationAudit{
		Name: row.Name, Description: row.Description, ContactUserID: row.ContactUserID,
		RateLimitPerMinute: row.RateLimitPerMinute, Roles: names, Disabled: row.IsDisabled, Archived: row.ArchivedAt != nil,
	}, nil
}

// integrationCaller is what every operation needs of its request: the Owner
// who makes it and the request itself, for the audit.
func integrationCaller(ctx context.Context) (contracts.Principal, *http.Request, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return contracts.Principal{}, nil, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return contracts.Principal{}, nil, err
	}
	return p, r, nil
}

// integrationInput is a create or update body once it is valid.
type integrationInput struct {
	name        string
	description *string
	contact     *uuid.UUID
	rateLimit   *int32
}

// readIntegrationInput holds a body to K8's rules, and answers 400 with the
// fields at fault. The contact must be a person (K25 mails them).
func (s *server) readIntegrationInput(ctx context.Context, name string, description *string, contact *uuid.UUID, rateLimit *int) (integrationInput, error) {
	fields := map[string][]string{}
	in := integrationInput{name: strings.TrimSpace(name), contact: contact}
	if n := len([]rune(in.name)); n == 0 || n > maxIntegrationNameLength {
		fields["name"] = []string{integrationNameMessage}
	}
	if description != nil {
		if d := strings.TrimSpace(*description); d != "" {
			if len([]rune(d)) > maxIntegrationDescriptionLength {
				fields["description"] = []string{integrationDescriptionMessage}
			}
			in.description = &d
		}
	}
	if rateLimit != nil {
		if *rateLimit < minRateLimitPerMinute || *rateLimit > maxRateLimitPerMinute {
			fields["rateLimitPerMinute"] = []string{integrationRateLimitMessage}
		} else {
			v := int32(*rateLimit)
			in.rateLimit = &v
		}
	}
	if contact != nil {
		u, err := s.q.GetUserByID(ctx, *contact)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return integrationInput{}, fmt.Errorf("identity: read the contact: %w", err)
		}
		if err != nil || u.Kind != contracts.UserKindPerson {
			fields["contactUserId"] = []string{integrationContactMessage}
		}
	}
	if len(fields) > 0 {
		return integrationInput{}, refuse(http.StatusBadRequest, "invalid_request", invalidRequestMessage, fields)
	}
	return in, nil
}

// replaceIntegrationRoles makes requested the integration's roles on q, under
// the licence that names it (K3, K55): only custom roles, so never Owner,
// SystemAdmin, User or any other built-in or system role (invalid_roles).
// Access-group memberships are never an integration's. The transaction holds
// each requested role's lock already (lockIntegrationRoles).
func replaceIntegrationRoles(ctx context.Context, q *store.Queries, id uuid.UUID, requested []uuid.UUID) error {
	requested = ordinalIDs(requested)
	if err := q.SetIntegrationRolesLicence(ctx, id.String()); err != nil {
		return fmt.Errorf("identity: licence the integration's roles: %w", err)
	}
	roles, err := q.AssignUserRoleLocks(ctx, requested)
	if err != nil {
		return fmt.Errorf("identity: read the roles: %w", err)
	}
	if len(roles) != len(requested) || slices.ContainsFunc(roles, func(r store.AssignUserRoleLocksRow) bool { return r.IsSystem || r.IsBuiltIn }) {
		return invalidIntegrationRoles
	}
	existing, err := q.ListUserRoleAssignments(ctx, id)
	if err != nil {
		return fmt.Errorf("identity: read the integration's roles: %w", err)
	}
	var drop []uuid.UUID
	for _, e := range existing {
		if !slices.Contains(requested, e.ID) {
			drop = append(drop, e.ID)
		}
	}
	if len(drop) > 0 {
		if err := q.RemoveUserRoles(ctx, store.RemoveUserRolesParams{UserID: id, RoleIds: drop}); err != nil {
			return fmt.Errorf("identity: take the integration's roles: %w", err)
		}
	}
	for _, roleID := range requested {
		if _, err := q.AssignUserRole(ctx, store.AssignUserRoleParams{UserID: id, RoleID: roleID}); err != nil {
			return fmt.Errorf("identity: give the integration a role: %w", err)
		}
	}
	return nil
}

// lockIntegrationRoles takes each role's lock, in ordinalIDs' order, before
// the transaction locks any users row (authorization.go's lock order): a
// steward's edit of a role (authorizeRoleEdit, K56) then either sees the
// integration holding it or is seen by the assignment, never neither.
func lockIntegrationRoles(ctx context.Context, q *store.Queries, roles []uuid.UUID) error {
	for _, roleID := range ordinalIDs(roles) {
		if err := lockRole(ctx, q, roleID); err != nil {
			return fmt.Errorf("identity: lock the role: %w", err)
		}
	}
	return nil
}

// integrationTx runs fn in one transaction on the integration id, locked:
// 404 when there is none, and 409 integration_archived for an archived one
// unless archivedOK. The locks of roles are taken first
// (lockIntegrationRoles).
func (s *server) integrationTx(ctx context.Context, id uuid.UUID, archivedOK bool, fn func(q *store.Queries, row store.LockIntegrationRow, now time.Time) error, roles ...uuid.UUID) error {
	return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := lockIntegrationRoles(ctx, q, roles); err != nil {
			return err
		}
		row, err := q.LockIntegration(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return integrationNotFound
		}
		if err != nil {
			return fmt.Errorf("identity: read the integration: %w", err)
		}
		if row.ArchivedAt != nil && !archivedOK {
			return integrationArchived
		}
		return fn(q, row, s.deps.Clock())
	})
}

// integrationDetail reads what GET /{id} answers, on q.
func (s *server) integrationDetail(ctx context.Context, q *store.Queries, id uuid.UUID, now time.Time) (gen.IntegrationDetail, error) {
	rows, err := q.ListIntegrations(ctx, store.ListIntegrationsParams{Now: now, ID: &id})
	if err != nil {
		return gen.IntegrationDetail{}, fmt.Errorf("identity: read the integration: %w", err)
	}
	if len(rows) == 0 {
		return gen.IntegrationDetail{}, integrationNotFound
	}
	keys, err := q.ListIntegrationAPIKeys(ctx, id)
	if err != nil {
		return gen.IntegrationDetail{}, fmt.Errorf("identity: read the integration's keys: %w", err)
	}
	held, err := q.EffectivePermissionsForUser(ctx, id)
	if err != nil {
		return gen.IntegrationDetail{}, fmt.Errorf("identity: read the integration's permissions: %w", err)
	}
	effective := make([]gen.IntegrationPermission, 0, len(held))
	for _, key := range held {
		effective = append(effective, gen.IntegrationPermission{Key: key, ApiNever: s.access.apiNever(key)})
	}
	out := gen.IntegrationDetail{Integration: integrationBody(rows[0]), Keys: make([]gen.ApiKey, 0, len(keys)), EffectivePermissions: effective}
	for _, k := range keys {
		out.Keys = append(out.Keys, s.apiKeyBody(k, held, now))
	}
	return out, nil
}

// answerDetail reads the detail after a write has committed.
func (s *server) answerDetail(ctx context.Context, id uuid.UUID) (gen.IntegrationDetail, error) {
	return s.integrationDetail(ctx, s.q, id, s.deps.Clock())
}

func integrationBody(row store.ListIntegrationsRow) gen.Integration {
	status := gen.IntegrationStatus("active")
	switch {
	case row.ArchivedAt != nil:
		status = "archived"
	case row.IsDisabled:
		status = "disabled"
	}
	roles := make([]gen.IntegrationRole, 0, len(row.RoleIds))
	for i, id := range row.RoleIds {
		roles = append(roles, gen.IntegrationRole{Id: id, Name: row.RoleNames[i], DisplayName: row.RoleDisplayNames[i]})
	}
	return gen.Integration{
		Id: row.UserID, Name: row.Name, Description: row.Description,
		ContactUserId: row.ContactUserID, ContactDisplayName: row.ContactDisplayName,
		RateLimitPerMinute: int(row.RateLimitPerMinute), Status: status, Roles: roles,
		KeyCount: int(row.LiveKeys), NearestExpiry: optionalTime(row.NearestExpiry), LastUsedAt: optionalTime(row.LastUsedAt),
		CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy, ArchivedAt: row.ArchivedAt, Version: row.Version.String(),
	}
}

// optionalTime is an aggregate's timestamptz, which is NULL over no rows.
func optionalTime(v any) *time.Time {
	if t, ok := v.(time.Time); ok {
		return &t
	}
	return nil
}

// apiKeyBody is a key as the screen shows it: never its secret, and each
// name of its narrowing marked with whether no key ever holds it (apiNever),
// whether it is granted (the integration's effective permissions hold it and
// it is not apiNever), and whether the catalog still has it.
func (s *server) apiKeyBody(k store.IdentityApiKey, held []string, now time.Time) gen.ApiKey {
	status := gen.ApiKeyStatus("active")
	switch {
	case k.RevokedAt != nil:
		status = "revoked"
	case !k.ExpiresAt.After(now):
		status = "expired"
	}
	var narrowing *[]gen.ApiKeyPermission
	if k.Permissions != nil {
		marks := make([]gen.ApiKeyPermission, 0, len(k.Permissions))
		for _, key := range k.Permissions {
			_, known := s.deps.Catalog[key]
			never := s.access.apiNever(key)
			marks = append(marks, gen.ApiKeyPermission{Key: key, GrantedByRoles: slices.Contains(held, key) && !never, InCatalog: known, ApiNever: never})
		}
		narrowing = &marks
	}
	return gen.ApiKey{
		Id: k.ID, Name: k.Name, Display: k.Display, Status: status, ReadOnly: k.ReadOnly,
		AllowedNetworks: nonNil(networkStrings(k.AllowedNetworks)), Permissions: narrowing,
		ExpiresAt: k.ExpiresAt, CreatedAt: k.CreatedAt, CreatedBy: k.CreatedBy, RevokedAt: k.RevokedAt,
		RolledFrom: k.RolledFrom, LastUsedAt: k.LastUsedAt, LastUsedIp: k.LastUsedIp,
	}
}

// keyBody reads the key's body after a write: the marks need the
// integration's permissions.
func (s *server) keyBody(ctx context.Context, k store.IdentityApiKey) (gen.ApiKey, error) {
	held, err := s.q.EffectivePermissionsForUser(ctx, k.IntegrationID)
	if err != nil {
		return gen.ApiKey{}, fmt.Errorf("identity: read the integration's permissions: %w", err)
	}
	return s.apiKeyBody(k, held, s.deps.Clock()), nil
}

// GetIdentityIntegrations lists every integration.
// (GET /api/v1/identity/integrations)
func (s *server) GetIdentityIntegrations(ctx context.Context, _ gen.GetIdentityIntegrationsRequestObject) (gen.GetIdentityIntegrationsResponseObject, error) {
	rows, err := s.q.ListIntegrations(ctx, store.ListIntegrationsParams{Now: s.deps.Clock()})
	if err != nil {
		return nil, fmt.Errorf("identity: list integrations: %w", err)
	}
	out := gen.IntegrationList{Integrations: make([]gen.Integration, 0, len(rows))}
	for _, row := range rows {
		out.Integrations = append(out.Integrations, integrationBody(row))
	}
	return gen.GetIdentityIntegrations200JSONResponse(out), nil
}

// PostIdentityIntegrations creates an integration: its user, of kind
// integration with an address that never routes and no built-in role, its
// row and its roles, audited integration.created (K1, K8).
// (POST /api/v1/identity/integrations)
func (s *server) PostIdentityIntegrations(ctx context.Context, req gen.PostIdentityIntegrationsRequestObject) (gen.PostIdentityIntegrationsResponseObject, error) {
	var body gen.IntegrationCreateRequest
	if req.Body != nil {
		body = *req.Body
	}
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	in, err := s.readIntegrationInput(ctx, body.Name, body.Description, body.ContactUserId, nil)
	if err != nil {
		return refusalOr[gen.PostIdentityIntegrationsResponseObject](err)
	}
	var roles []uuid.UUID
	if body.RoleIds != nil {
		roles = *body.RoleIds
	}
	id := uuid.New()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := lockIntegrationRoles(ctx, q, roles); err != nil {
			return err
		}
		now := s.deps.Clock()
		taken, err := q.IntegrationNameTaken(ctx, store.IntegrationNameTakenParams{Name: in.name, UserID: id})
		if err != nil || taken {
			return cmpOr(err, error(integrationNameTaken))
		}
		email := "integration-" + id.String() + "@" + integrationEmailDomain
		if err := q.InsertIntegrationUser(ctx, store.InsertIntegrationUserParams{
			ID: id, Email: email, NormalizedEmail: normalizeEmail(email), DisplayName: in.name, Version: uuid.New(), Now: now,
		}); err != nil {
			return fmt.Errorf("identity: create the integration's user: %w", err)
		}
		if err := q.InsertIntegration(ctx, store.InsertIntegrationParams{
			UserID: id, Name: in.name, Description: in.description, ContactUserID: in.contact, CreatedBy: &p.UserID, Now: now, Version: uuid.New(),
		}); err != nil {
			return fmt.Errorf("identity: create the integration: %w", err)
		}
		if err := replaceIntegrationRoles(ctx, q, id, roles); err != nil {
			return err
		}
		row, err := q.LockIntegration(ctx, id)
		if err != nil {
			return fmt.Errorf("identity: read the integration: %w", err)
		}
		after, err := integrationSnapshot(ctx, q, row)
		if err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &id, action: auditIntegrationCreated, before: struct{}{}, after: after, mfa: p.MFAVerified,
		})
	})
	if db.IsUniqueViolation(err, "ux_integrations_name") {
		return integrationNameTaken, nil
	}
	if err != nil {
		return refusalOr[gen.PostIdentityIntegrationsResponseObject](err)
	}
	detail, err := s.answerDetail(ctx, id)
	if err != nil {
		return nil, err
	}
	return gen.PostIdentityIntegrations201JSONResponse(detail), nil
}

// GetIdentityIntegrationsById reads one integration with its keys and its
// effective permissions.
// (GET /api/v1/identity/integrations/{id})
func (s *server) GetIdentityIntegrationsById(ctx context.Context, req gen.GetIdentityIntegrationsByIdRequestObject) (gen.GetIdentityIntegrationsByIdResponseObject, error) {
	detail, err := s.answerDetail(ctx, req.Id)
	if err != nil {
		return refusalOr[gen.GetIdentityIntegrationsByIdResponseObject](err)
	}
	return gen.GetIdentityIntegrationsById200JSONResponse(detail), nil
}

// parseIntegrationVersion reads the version an If-Match header carries, bare
// or quoted (and weakly tagged).
func parseIntegrationVersion(raw string) (uuid.UUID, bool) {
	v := strings.TrimPrefix(strings.TrimSpace(raw), "W/")
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	id, err := uuid.Parse(v)
	return id, err == nil
}

// PatchIdentityIntegrationsById changes an integration's name, description,
// contact and rate limit under If-Match, audited integration.updated.
// (PATCH /api/v1/identity/integrations/{id})
func (s *server) PatchIdentityIntegrationsById(ctx context.Context, req gen.PatchIdentityIntegrationsByIdRequestObject) (gen.PatchIdentityIntegrationsByIdResponseObject, error) {
	version, ok := parseIntegrationVersion(req.Params.IfMatch)
	if !ok {
		return refuse(http.StatusBadRequest, "invalid_request", integrationIfMatchMessage,
			map[string][]string{"If-Match": {integrationIfMatchMessage}}), nil
	}
	var body gen.IntegrationUpdateRequest
	if req.Body != nil {
		body = *req.Body
	}
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	in, err := s.readIntegrationInput(ctx, body.Name, body.Description, body.ContactUserId, body.RateLimitPerMinute)
	if err != nil {
		return refusalOr[gen.PatchIdentityIntegrationsByIdResponseObject](err)
	}
	err = s.integrationTx(ctx, req.Id, false, func(q *store.Queries, row store.LockIntegrationRow, now time.Time) error {
		if row.Version != version {
			return integrationVersionMismatch
		}
		taken, err := q.IntegrationNameTaken(ctx, store.IntegrationNameTakenParams{Name: in.name, UserID: req.Id})
		if err != nil || taken {
			return cmpOr(err, error(integrationNameTaken))
		}
		before, err := integrationSnapshot(ctx, q, row)
		if err != nil {
			return err
		}
		rateLimit := row.RateLimitPerMinute
		if in.rateLimit != nil {
			rateLimit = *in.rateLimit
		}
		if err := q.UpdateIntegration(ctx, store.UpdateIntegrationParams{
			UserID: req.Id, Name: in.name, Description: in.description, ContactUserID: in.contact,
			RateLimitPerMinute: rateLimit, Version: uuid.New(),
		}); err != nil {
			return fmt.Errorf("identity: change the integration: %w", err)
		}
		if err := q.SetIntegrationUser(ctx, store.SetIntegrationUserParams{
			ID: req.Id, DisplayName: in.name, IsDisabled: row.IsDisabled, Version: uuid.New(), Now: now,
		}); err != nil {
			return fmt.Errorf("identity: change the integration's user: %w", err)
		}
		after := before
		after.Name, after.Description, after.ContactUserID, after.RateLimitPerMinute = in.name, in.description, in.contact, rateLimit
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &req.Id, action: auditIntegrationUpdated, before: before, after: after, mfa: p.MFAVerified,
		})
	})
	if db.IsUniqueViolation(err, "ux_integrations_name") {
		return integrationNameTaken, nil
	}
	if err != nil {
		return refusalOr[gen.PatchIdentityIntegrationsByIdResponseObject](err)
	}
	detail, err := s.answerDetail(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.PatchIdentityIntegrationsById200JSONResponse(detail), nil
}

// PutIdentityIntegrationsByIdRoles replaces an integration's roles under its
// licence, audited integration.roles_changed.
// (PUT /api/v1/identity/integrations/{id}/roles)
func (s *server) PutIdentityIntegrationsByIdRoles(ctx context.Context, req gen.PutIdentityIntegrationsByIdRolesRequestObject) (gen.PutIdentityIntegrationsByIdRolesResponseObject, error) {
	var body gen.IntegrationRolesRequest
	if req.Body != nil {
		body = *req.Body
	}
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	err = s.integrationTx(ctx, req.Id, false, func(q *store.Queries, row store.LockIntegrationRow, now time.Time) error {
		before, err := integrationSnapshot(ctx, q, row)
		if err != nil {
			return err
		}
		if err := replaceIntegrationRoles(ctx, q, req.Id, body.RoleIds); err != nil {
			return err
		}
		if err := q.TouchIntegrationVersion(ctx, store.TouchIntegrationVersionParams{UserID: req.Id, Version: uuid.New()}); err != nil {
			return fmt.Errorf("identity: change the integration: %w", err)
		}
		after, err := integrationSnapshot(ctx, q, row)
		if err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &req.Id, action: auditIntegrationRolesChanged, before: before, after: after, mfa: p.MFAVerified,
		})
	}, body.RoleIds...)
	if err != nil {
		return refusalOr[gen.PutIdentityIntegrationsByIdRolesResponseObject](err)
	}
	detail, err := s.answerDetail(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.PutIdentityIntegrationsByIdRoles200JSONResponse(detail), nil
}

// setIntegrationDisabled disables or enables an integration, audited with
// action; an integration already so is answered as it is.
func (s *server) setIntegrationDisabled(ctx context.Context, id uuid.UUID, disabled bool, action string) (gen.IntegrationDetail, error) {
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return gen.IntegrationDetail{}, err
	}
	err = s.integrationTx(ctx, id, false, func(q *store.Queries, row store.LockIntegrationRow, now time.Time) error {
		if row.IsDisabled == disabled {
			return nil
		}
		before, err := integrationSnapshot(ctx, q, row)
		if err != nil {
			return err
		}
		if err := q.SetIntegrationUser(ctx, store.SetIntegrationUserParams{
			ID: id, DisplayName: row.Name, IsDisabled: disabled, Version: uuid.New(), Now: now,
		}); err != nil {
			return fmt.Errorf("identity: change the integration's user: %w", err)
		}
		if err := q.TouchIntegrationVersion(ctx, store.TouchIntegrationVersionParams{UserID: id, Version: uuid.New()}); err != nil {
			return fmt.Errorf("identity: change the integration: %w", err)
		}
		after := before
		after.Disabled = disabled
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &id, action: action, before: before, after: after, mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return gen.IntegrationDetail{}, err
	}
	return s.answerDetail(ctx, id)
}

// PostIdentityIntegrationsByIdDisable refuses every key of an integration
// until it is enabled again (K8).
// (POST /api/v1/identity/integrations/{id}/disable)
func (s *server) PostIdentityIntegrationsByIdDisable(ctx context.Context, req gen.PostIdentityIntegrationsByIdDisableRequestObject) (gen.PostIdentityIntegrationsByIdDisableResponseObject, error) {
	detail, err := s.setIntegrationDisabled(ctx, req.Id, true, auditIntegrationDisabled)
	if err != nil {
		return refusalOr[gen.PostIdentityIntegrationsByIdDisableResponseObject](err)
	}
	return gen.PostIdentityIntegrationsByIdDisable200JSONResponse(detail), nil
}

// PostIdentityIntegrationsByIdEnable enables a disabled integration.
// (POST /api/v1/identity/integrations/{id}/enable)
func (s *server) PostIdentityIntegrationsByIdEnable(ctx context.Context, req gen.PostIdentityIntegrationsByIdEnableRequestObject) (gen.PostIdentityIntegrationsByIdEnableResponseObject, error) {
	detail, err := s.setIntegrationDisabled(ctx, req.Id, false, auditIntegrationEnabled)
	if err != nil {
		return refusalOr[gen.PostIdentityIntegrationsByIdEnableResponseObject](err)
	}
	return gen.PostIdentityIntegrationsByIdEnable200JSONResponse(detail), nil
}

// PostIdentityIntegrationsByIdArchive revokes every key of an integration and
// archives it, in one transaction, audited api_key.revoked for each key and
// integration.archived (K8). It cannot be undone.
// (POST /api/v1/identity/integrations/{id}/archive)
func (s *server) PostIdentityIntegrationsByIdArchive(ctx context.Context, req gen.PostIdentityIntegrationsByIdArchiveRequestObject) (gen.PostIdentityIntegrationsByIdArchiveResponseObject, error) {
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	err = s.integrationTx(ctx, req.Id, false, func(q *store.Queries, row store.LockIntegrationRow, now time.Time) error {
		before, err := integrationSnapshot(ctx, q, row)
		if err != nil {
			return err
		}
		keys, err := q.ListIntegrationAPIKeys(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("identity: read the integration's keys: %w", err)
		}
		if _, err := q.RevokeIntegrationAPIKeys(ctx, store.RevokeIntegrationAPIKeysParams{IntegrationID: req.Id, Now: now, RevokedBy: &p.UserID}); err != nil {
			return fmt.Errorf("identity: revoke the integration's keys: %w", err)
		}
		for _, k := range keys {
			// An expired key is not revoked: its end is the expiry worker's
			// api_key.expired, never a second event.
			if k.RevokedAt != nil || !k.ExpiresAt.After(now) {
				continue
			}
			revoked := k
			revoked.RevokedAt = &now
			if err := writeAudit(ctx, q, r, now, auditEvent{
				actor: &p.UserID, targetUser: &req.Id, action: auditAPIKeyRevoked,
				before: apiKeySnapshot(k), after: apiKeySnapshot(revoked), mfa: p.MFAVerified,
			}); err != nil {
				return err
			}
		}
		if err := q.ArchiveIntegration(ctx, store.ArchiveIntegrationParams{UserID: req.Id, Now: now, ArchivedBy: &p.UserID, Version: uuid.New()}); err != nil {
			return fmt.Errorf("identity: archive the integration: %w", err)
		}
		after := before
		after.Archived = true
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &req.Id, action: auditIntegrationArchived, before: before, after: after, mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PostIdentityIntegrationsByIdArchiveResponseObject](err)
	}
	detail, err := s.answerDetail(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return gen.PostIdentityIntegrationsByIdArchive200JSONResponse(detail), nil
}

// keyInput is a key's settings once they are valid.
type keyInput struct {
	name        string
	permissions []string
	readOnly    bool
	networks    []netip.Prefix
	expiresAt   time.Time
}

// readExpiry is a key's expiry from a body (K12): one of apiKeyDayChoices
// days, or a date after now and at most 365 days ahead, or 90 days.
func readExpiry(days *int, at *time.Time, now time.Time, fields map[string][]string) time.Time {
	switch {
	case days != nil && at != nil:
		fields["expiresAt"] = []string{apiKeyExpiryMessage}
	case days != nil:
		if !slices.Contains(apiKeyDayChoices, *days) {
			fields["expiresInDays"] = []string{apiKeyExpiryMessage}
			return time.Time{}
		}
		return now.AddDate(0, 0, *days)
	case at != nil:
		if !at.After(now) || at.Sub(now) > maxAPIKeyLifetime {
			fields["expiresAt"] = []string{apiKeyExpiryMessage}
			return time.Time{}
		}
		return at.UTC()
	}
	return now.AddDate(0, 0, defaultAPIKeyDays)
}

// readKeyInput holds a key's body to K11's rules, and answers 400 with the
// fields at fault.
func readKeyInput(body gen.ApiKeyCreateRequest, now time.Time) (keyInput, error) {
	fields := map[string][]string{}
	in := keyInput{name: strings.TrimSpace(body.Name), readOnly: body.ReadOnly != nil && *body.ReadOnly}
	if n := len([]rune(in.name)); n == 0 || n > maxAPIKeyNameLength {
		fields["name"] = []string{apiKeyNameMessage}
	}
	in.expiresAt = readExpiry(body.ExpiresInDays, body.ExpiresAt, now, fields)
	if body.Permissions != nil {
		in.permissions = []string{}
		for _, key := range *body.Permissions {
			key = strings.TrimSpace(key)
			// Every entry has the form of a catalog key; one the catalog does
			// not know (yet, or any more) is kept and does nothing (K11).
			if !contracts.ValidPermissionKey(key) {
				fields["permissions"] = []string{apiKeyPermissionsMessage}
				break
			}
			in.permissions = append(in.permissions, key)
		}
		slices.Sort(in.permissions)
		in.permissions = slices.Compact(in.permissions)
	}
	networks, ok := parseNetworks(body.AllowedNetworks)
	if !ok || len(networks) > maxAPIKeyNetworks {
		fields["allowedNetworks"] = []string{apiKeyNetworksMessage}
	}
	in.networks = networks
	if len(fields) > 0 {
		return keyInput{}, refuse(http.StatusBadRequest, "invalid_request", invalidRequestMessage, fields)
	}
	return in, nil
}

// PostIdentityIntegrationsByIdKeys creates a key and answers its secret once,
// never cached, audited api_key.created without it (K11).
// (POST /api/v1/identity/integrations/{id}/keys)
func (s *server) PostIdentityIntegrationsByIdKeys(ctx context.Context, req gen.PostIdentityIntegrationsByIdKeysRequestObject) (gen.PostIdentityIntegrationsByIdKeysResponseObject, error) {
	var body gen.ApiKeyCreateRequest
	if req.Body != nil {
		body = *req.Body
	}
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	in, err := readKeyInput(body, s.deps.Clock())
	if err != nil {
		return refusalOr[gen.PostIdentityIntegrationsByIdKeysResponseObject](err)
	}
	secret, hash := newAPIKey()
	var key store.IdentityApiKey
	err = s.integrationTx(ctx, req.Id, false, func(q *store.Queries, _ store.LockIntegrationRow, now time.Time) error {
		var err error
		key, err = q.InsertAPIKey(ctx, store.InsertAPIKeyParams{
			ID: uuid.New(), IntegrationID: req.Id, Name: in.name, TokenHash: hash, Display: apiKeyDisplay(secret),
			Permissions: in.permissions, ReadOnly: in.readOnly, AllowedNetworks: nonNil(in.networks),
			ExpiresAt: in.expiresAt, CreatedBy: &p.UserID, Now: now,
		})
		if err != nil {
			return fmt.Errorf("identity: create the API key: %w", err)
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &req.Id, action: auditAPIKeyCreated, before: struct{}{}, after: apiKeySnapshot(key), mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PostIdentityIntegrationsByIdKeysResponseObject](err)
	}
	k, err := s.keyBody(ctx, key)
	if err != nil {
		return nil, err
	}
	noStore := secretCacheControl
	return gen.PostIdentityIntegrationsByIdKeys201JSONResponse{
		Body:    gen.ApiKeySecret{Key: k, Secret: secret},
		Headers: gen.PostIdentityIntegrationsByIdKeys201ResponseHeaders{CacheControl: &noStore},
	}, nil
}

// PostIdentityIntegrationsByIdKeysByKeyIdRoll issues a new key in place of
// one and answers its secret once (K13): the same name, narrowing, read-only
// flag and networks, a new expiry, and the old key's life cut to the overlap.
// The unique index on rolled_from makes a second roll 409
// api_key_already_rolled, however close the two come.
// (POST /api/v1/identity/integrations/{id}/keys/{keyId}/roll)
func (s *server) PostIdentityIntegrationsByIdKeysByKeyIdRoll(ctx context.Context, req gen.PostIdentityIntegrationsByIdKeysByKeyIdRollRequestObject) (gen.PostIdentityIntegrationsByIdKeysByKeyIdRollResponseObject, error) {
	var body gen.ApiKeyRollRequest
	if req.Body != nil {
		body = *req.Body
	}
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	fields := map[string][]string{}
	expiresAt := readExpiry(body.ExpiresInDays, body.ExpiresAt, now, fields)
	overlap := defaultRollOverlap
	if body.OverlapHours != nil {
		overlap = time.Duration(*body.OverlapHours) * time.Hour
		if overlap < 0 || overlap > maxRollOverlap {
			fields["overlapHours"] = []string{apiKeyOverlapMessage}
		}
	}
	if len(fields) > 0 {
		return refuse(http.StatusBadRequest, "invalid_request", invalidRequestMessage, fields), nil
	}
	secret, hash := newAPIKey()
	var key store.IdentityApiKey
	err = s.integrationTx(ctx, req.Id, false, func(q *store.Queries, _ store.LockIntegrationRow, now time.Time) error {
		old, err := q.LockAPIKey(ctx, store.LockAPIKeyParams{ID: req.KeyId, IntegrationID: req.Id})
		if errors.Is(err, pgx.ErrNoRows) {
			return apiKeyNotFound
		}
		if err != nil {
			return fmt.Errorf("identity: read the API key: %w", err)
		}
		if old.RevokedAt != nil || !old.ExpiresAt.After(now) {
			return apiKeyNotLive
		}
		key, err = q.InsertAPIKey(ctx, store.InsertAPIKeyParams{
			ID: uuid.New(), IntegrationID: req.Id, Name: old.Name, TokenHash: hash, Display: apiKeyDisplay(secret),
			Permissions: old.Permissions, ReadOnly: old.ReadOnly, AllowedNetworks: nonNil(old.AllowedNetworks),
			ExpiresAt: expiresAt, CreatedBy: &p.UserID, Now: now, RolledFrom: &old.ID,
		})
		if db.IsUniqueViolation(err, "ux_api_keys_rolled_from") {
			return apiKeyAlreadyRolled
		}
		if err != nil {
			return fmt.Errorf("identity: roll the API key: %w", err)
		}
		shortened, err := q.ShortenAPIKeyExpiry(ctx, store.ShortenAPIKeyExpiryParams{ID: old.ID, Until: now.Add(overlap)})
		if err != nil {
			return fmt.Errorf("identity: roll the API key: %w", err)
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &req.Id, action: auditAPIKeyRolled,
			before: apiKeySnapshot(old),
			after: struct {
				Old apiKeyAudit `json:"old"`
				New apiKeyAudit `json:"new"`
			}{apiKeySnapshot(shortened), apiKeySnapshot(key)},
			mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PostIdentityIntegrationsByIdKeysByKeyIdRollResponseObject](err)
	}
	k, err := s.keyBody(ctx, key)
	if err != nil {
		return nil, err
	}
	noStore := secretCacheControl
	return gen.PostIdentityIntegrationsByIdKeysByKeyIdRoll201JSONResponse{
		Body:    gen.ApiKeySecret{Key: k, Secret: secret},
		Headers: gen.PostIdentityIntegrationsByIdKeysByKeyIdRoll201ResponseHeaders{CacheControl: &noStore},
	}, nil
}

// PostIdentityIntegrationsByIdKeysByKeyIdRevoke revokes a key, audited
// api_key.revoked; one revoked already is answered as it is (K13).
// (POST /api/v1/identity/integrations/{id}/keys/{keyId}/revoke)
func (s *server) PostIdentityIntegrationsByIdKeysByKeyIdRevoke(ctx context.Context, req gen.PostIdentityIntegrationsByIdKeysByKeyIdRevokeRequestObject) (gen.PostIdentityIntegrationsByIdKeysByKeyIdRevokeResponseObject, error) {
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	var key store.IdentityApiKey
	err = s.integrationTx(ctx, req.Id, true, func(q *store.Queries, _ store.LockIntegrationRow, now time.Time) error {
		old, err := q.LockAPIKey(ctx, store.LockAPIKeyParams{ID: req.KeyId, IntegrationID: req.Id})
		if errors.Is(err, pgx.ErrNoRows) {
			return apiKeyNotFound
		}
		if err != nil {
			return fmt.Errorf("identity: read the API key: %w", err)
		}
		key = old
		if old.RevokedAt != nil {
			return nil
		}
		key, err = q.RevokeAPIKey(ctx, store.RevokeAPIKeyParams{ID: old.ID, Now: now, RevokedBy: &p.UserID})
		if err != nil {
			return fmt.Errorf("identity: revoke the API key: %w", err)
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &req.Id, action: auditAPIKeyRevoked,
			before: apiKeySnapshot(old), after: apiKeySnapshot(key), mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PostIdentityIntegrationsByIdKeysByKeyIdRevokeResponseObject](err)
	}
	k, err := s.keyBody(ctx, key)
	if err != nil {
		return nil, err
	}
	return gen.PostIdentityIntegrationsByIdKeysByKeyIdRevoke200JSONResponse(k), nil
}

func (r refusal) VisitGetIdentityIntegrationsByIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityIntegrationsResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPatchIdentityIntegrationsByIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPutIdentityIntegrationsByIdRolesResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityIntegrationsByIdDisableResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityIntegrationsByIdEnableResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityIntegrationsByIdArchiveResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityIntegrationsByIdKeysResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityIntegrationsByIdKeysByKeyIdRollResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityIntegrationsByIdKeysByKeyIdRevokeResponse(w http.ResponseWriter) error {
	return r.write(w)
}
