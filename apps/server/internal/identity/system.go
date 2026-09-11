package identity

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// maxMaintenanceMessageLength is PutMaintenance's bound on the maintenance
// message, in UTF-16 units, as .NET counted it
// (EA/SystemMaintenanceEndpoints.cs:59-60).
const maxMaintenanceMessageLength = 500

// The two kinds of heartbeat SystemStatus reports, .NET's
// OperationalEventKinds (SV/OperationalEventService.cs:8-12).
const (
	operationalEventStaticOIDCSignIn = "static-oidc.sign-in-succeeded"
	operationalEventScimRequest      = "scim.authenticated-request"
)

// maintenanceCache is the 20-second in-process cache GetStatus reads
// through (EA/SystemMaintenanceEndpoints.cs:16-17,33-47): a mutex, the
// cached value and when it was fetched, taken from Deps.Clock rather than
// the wall clock so a test can move it with h.advance. It lives on *server,
// not a package global, so parallel harnesses (each its own *server) never
// share one. PutIdentitySystemMaintenance evicts it on every write (:80).
type maintenanceCache struct {
	mu        sync.Mutex
	status    gen.SystemMaintenanceStatus
	fetchedAt time.Time
	fresh     bool
}

// maintenanceCacheTTL is StatusCacheDuration (:17).
const maintenanceCacheTTL = 20 * time.Second

// get returns the cached status when it was fetched within maintenanceCacheTTL
// of now, else calls fetch, caches what it returns, and returns that.
func (c *maintenanceCache) get(now time.Time, fetch func() (gen.SystemMaintenanceStatus, error)) (gen.SystemMaintenanceStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fresh && now.Sub(c.fetchedAt) < maintenanceCacheTTL {
		return c.status, nil
	}
	status, err := fetch()
	if err != nil {
		return gen.SystemMaintenanceStatus{}, err
	}
	c.status, c.fetchedAt, c.fresh = status, now, true
	return status, nil
}

// evict forces the next get to fetch fresh, as PUT /system/maintenance does
// (:80).
func (c *maintenanceCache) evict() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fresh = false
}

// GetIdentitySystemStatus is GetStatus (EA/SystemMaintenanceEndpoints.cs:33-47):
// the anonymous, cached maintenance status. Maintenance is advisory only —
// nothing here or elsewhere in the backend blocks on it, as in .NET; only
// the SPA reads this to warn a non-SystemAdmin (spec *Maintenance mode*).
func (s *server) GetIdentitySystemStatus(ctx context.Context, _ gen.GetIdentitySystemStatusRequestObject) (gen.GetIdentitySystemStatusResponseObject, error) {
	status, err := s.maintenanceStatus(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetIdentitySystemStatus200JSONResponse(status), nil
}

// maintenanceStatus is the cached read behind both GetIdentitySystemStatus
// and PutIdentitySystemMaintenance's response: the system_settings singleton
// row, defaulting to {false, nil} when no row exists yet
// (EA/SystemMaintenanceEndpoints.cs:84-87).
func (s *server) maintenanceStatus(ctx context.Context) (gen.SystemMaintenanceStatus, error) {
	return s.maintenance.get(s.deps.Clock(), func() (gen.SystemMaintenanceStatus, error) {
		row, err := s.q.GetSystemSettings(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.SystemMaintenanceStatus{Maintenance: false, Message: nil}, nil
		}
		if err != nil {
			return gen.SystemMaintenanceStatus{}, fmt.Errorf("identity: read maintenance status: %w", err)
		}
		return gen.SystemMaintenanceStatus{Maintenance: row.MaintenanceEnabled, Message: row.MaintenanceMessage}, nil
	})
}

// PutIdentitySystemMaintenance is PutMaintenance (EA/SystemMaintenanceEndpoints.cs:49-82):
// SystemAdmin only, by the router's rule. A nil body is treated as the
// contract's optional empty one, as every other optional-body operation in
// this package does (bootstrap.go, invitations.go, signin.go): enabled
// defaults to false and message to nil, rather than .NET's separate
// "a request is required" refusal, which no test exercises here.
func (s *server) PutIdentitySystemMaintenance(ctx context.Context, req gen.PutIdentitySystemMaintenanceRequestObject) (gen.PutIdentitySystemMaintenanceResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	var body gen.SystemMaintenanceRequest
	if req.Body != nil {
		body = *req.Body
	}
	// A flat 400, over the CodeMessageError body every /access/* handler
	// uses too (EA/SystemMaintenanceEndpoints.cs:59-60).
	if body.Message != nil && utf16Length(*body.Message) > maxMaintenanceMessageLength {
		return gen.PutIdentitySystemMaintenance400JSONResponse{
			Code:    "invalid_message",
			Message: "The maintenance message must be at most 500 characters.",
		}, nil
	}

	enabled := body.Enabled != nil && *body.Enabled
	now := s.deps.Clock()
	if err := s.q.UpsertSystemSettings(ctx, store.UpsertSystemSettingsParams{
		MaintenanceEnabled: enabled,
		MaintenanceMessage: body.Message,
		Now:                now,
		UpdatedByUserID:    &p.UserID,
	}); err != nil {
		return nil, fmt.Errorf("identity: update maintenance status: %w", err)
	}
	s.maintenance.evict()

	return gen.PutIdentitySystemMaintenance200JSONResponse{Maintenance: enabled, Message: body.Message}, nil
}

// GetIdentityOwnerSystemStatus is SystemStatus (EA/AuthEndpoints.cs:106-153):
// the Owner's dashboard of aggregate, non-sensitive counts, which SSO and
// provisioning features are configured, and their last heartbeat. No
// account, invitation, or secret value is ever included.
func (s *server) GetIdentityOwnerSystemStatus(ctx context.Context, _ gen.GetIdentityOwnerSystemStatusRequestObject) (gen.GetIdentityOwnerSystemStatusResponseObject, error) {
	now := s.deps.Clock()
	cfg := s.deps.Config

	counts, err := s.q.GetOwnerSystemStatusCounts(ctx, store.GetOwnerSystemStatusCountsParams{
		Now:         now,
		ScimEnabled: cfg.SCIM != nil,
	})
	if err != nil {
		return nil, fmt.Errorf("identity: owner system status counts: %w", err)
	}
	pending, err := s.q.CountPendingInvitations(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("identity: owner system status pending invitations: %w", err)
	}
	events, err := s.q.GetLastOperationalEvents(ctx, store.GetLastOperationalEventsParams{
		OidcKind: operationalEventStaticOIDCSignIn,
		ScimKind: operationalEventScimRequest,
	})
	if err != nil {
		return nil, fmt.Errorf("identity: owner system status operational events: %w", err)
	}

	var provider *string
	oidcEnabled := cfg.OIDC != nil
	if oidcEnabled {
		p := cfg.OIDC.Provider
		provider = &p
	}

	return gen.GetIdentityOwnerSystemStatus200JSONResponse{
		Total:                             int32(counts.Total),
		Active:                            int32(counts.Active),
		Disabled:                          int32(counts.Disabled),
		PendingInvitations:                int32(pending),
		StaticOidcEnabled:                 oidcEnabled,
		StaticOidcProvider:                provider,
		StaticScimEnabled:                 cfg.SCIM != nil,
		LastStaticOidcSignInAtUtc:         events.LastOidcSignInAt,
		LastAuthenticatedScimRequestAtUtc: events.LastScimRequestAt,
	}, nil
}

// recordOperationalEvent is OperationalEventService.RecordAsync
// (SV/OperationalEventService.cs:14-30): a best-effort last-seen heartbeat
// for kind (one of the operationalEvent… constants), upserted on the pool
// outside any caller's transaction, exactly as .NET ran it on a fresh scope
// and connection so a concurrent write never poisons the caller's (:20-22).
// It runs on a context that survives the caller's own cancellation
// (context.WithoutCancel), as .NET's own CancellationToken.None did, and
// its error is logged and swallowed: operational status is a heartbeat, not
// an audit log, and must never change the caller's outcome
// (EA/WorkforceOidcEndpoints.cs:344-353, SV/ScimProtocolService.cs:574-581).
// Task 18 calls it on every successful workforce OIDC sign-in and Task 19 on
// every authenticated SCIM request.
func (s *server) recordOperationalEvent(ctx context.Context, kind string) {
	if err := s.q.RecordOperationalEvent(context.WithoutCancel(ctx), store.RecordOperationalEventParams{
		Kind: kind,
		Now:  s.deps.Clock(),
	}); err != nil {
		s.deps.Logger.ErrorContext(ctx, "identity: operational event not recorded", "kind", kind, "error", err.Error())
	}
}
