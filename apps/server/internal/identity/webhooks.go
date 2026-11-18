package identity

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/netguard"
)

// This file is the webhook endpoints service (outgoing webhooks design W1-W3,
// W6-W11, W24, W25, W28, W31, W36): an Owner's endpoints of an integration,
// each an HTTPS URL that receives the catalogue events it lists, signed with
// a secret shown once. Every operation is policy:Owner+OwnerManagement, never
// open to an API key or the Assistant (W34). Fanning events out to the
// endpoints and sending them is the sender's (identity.webhook-sender).

const (
	maxWebhooksPerIntegration   = 5
	maxWebhooksPerWorkspace     = 50
	maxWebhookEvents            = 50
	maxWebhookDescriptionLength = 500
	defaultWebhookOverlap       = 24 * time.Hour
	minWebhookOverlap           = time.Hour
	maxWebhookOverlap           = 7 * 24 * time.Hour
	// webhookCreationLock is the key of the transaction advisory lock every
	// creation takes, so the per-workspace limit holds under concurrency
	// (W36). It is "WHOK".
	webhookCreationLock = 0x57484f4b
)

// The endpoint states and the reasons one is disabled or a delivery is
// settled (the migration's checks).
const (
	webhookEnabled              = "enabled"
	webhookDisabled             = "disabled"
	webhookReasonManual         = "manual"
	webhookReasonArchived       = "integration_archived"
	webhookReasonEndpointClosed = "endpoint_disabled"
)

// The audit actions of W24 this service writes; webhook.test_sent and
// webhook.resent are the sender's and the log's.
const (
	auditWebhookCreated      = "webhook.created"
	auditWebhookUpdated      = "webhook.updated"
	auditWebhookDeleted      = "webhook.deleted"
	auditWebhookEnabled      = "webhook.enabled"
	auditWebhookDisabled     = "webhook.disabled"
	auditWebhookSecretRolled = "webhook.secret_rolled"
)

// The words of the webhooks service's refusals.
const (
	webhookDescriptionMessage = "The description may be at most 500 characters."
	webhookEventsMessage      = "1 to 50 events of the catalogue are required."
	webhookOverlapMessage     = "The overlap is 1 to 168 hours."
	webhookIfMatchMessage     = "If-Match is the version the endpoint was read at."
)

var (
	webhookNotFound        = refuse(http.StatusNotFound, "not_found", "No such webhook endpoint.", nil)
	webhookLimitReached    = refuse(http.StatusConflict, "webhook_limit_reached", "An integration has at most 5 webhook endpoints, and the workspace 50.", nil)
	webhookVersionMismatch = refuse(http.StatusPreconditionFailed, "version_mismatch", "The webhook endpoint has been changed since it was read.", nil)
)

// webhookEventUnknown is the refusal of names no catalogue event has.
func webhookEventUnknown(names []string) refusal {
	return refuse(http.StatusUnprocessableEntity, "webhook_event_unknown", "No event of the catalogue has this name.",
		map[string][]string{"events": prefixed("Unknown event: ", names)})
}

// webhookEventNotPermitted is the refusal of events the integration's roles
// do not let it receive (W6, W7).
func webhookEventNotPermitted(names []string) refusal {
	return refuse(http.StatusUnprocessableEntity, "webhook_event_not_permitted", "The integration's roles do not let it receive this event.",
		map[string][]string{"events": prefixed("Not permitted: ", names)})
}

func prefixed(prefix string, names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = prefix + n
	}
	return out
}

// webhookAudit is an endpoint as the audit trail records it (W24, W28): its
// URL's scheme and host and a keyed digest of the whole, never its path or
// query, never a secret or its ciphertext, and not its description, which is
// an Owner's free text and may hold what the path held.
type webhookAudit struct {
	ID                      uuid.UUID  `json:"id"`
	IntegrationID           uuid.UUID  `json:"integrationId"`
	URL                     string     `json:"url"`
	URLDigest               string     `json:"urlDigest"`
	Events                  []string   `json:"events"`
	Status                  string     `json:"status"`
	DisabledReason          *string    `json:"disabledReason"`
	PreviousSecretExpiresAt *time.Time `json:"previousSecretExpiresAt,omitempty"`
}

func (s *server) webhookSnapshot(e store.IdentityWebhookEndpoint) (webhookAudit, error) {
	digest, err := webhookURLDigest(s.deps.Secrets, e.Url)
	if err != nil {
		return webhookAudit{}, err
	}
	return webhookAudit{
		ID: e.ID, IntegrationID: e.IntegrationID, URL: webhookURLOrigin(e.Url), URLDigest: digest,
		Events: nonNil(e.Events), Status: e.Status, DisabledReason: e.DisabledReason,
		PreviousSecretExpiresAt: e.PreviousSecretExpiresAt,
	}, nil
}

// webhookResolver is what a save resolves an endpoint's host with:
// Access.webhookResolver when a test set one, the system's otherwise.
func (s *server) webhookResolver() netguard.Resolver {
	if s.access.webhookResolver != nil {
		return s.access.webhookResolver
	}
	return net.DefaultResolver
}

// webhookInput is a create or update body once its form is valid.
type webhookInput struct {
	url         string
	description *string
	events      []string
}

// readWebhookInput holds a body to W1, W6 and W11 and resolves the host
// once, before any transaction (no connection is held while DNS answers):
// 400 with the fields at fault for the form, 422 webhook_event_unknown for a
// name the catalogue lacks, and 422 webhook_url_blocked or
// webhook_url_unresolvable for the host. Whether the integration may receive
// the events is checked in the transaction (webhookEventsPermitted).
func (s *server) readWebhookInput(ctx context.Context, rawURL string, description *string, events []string) (webhookInput, error) {
	fields := map[string][]string{}
	var in webhookInput
	u, ok := parseWebhookURL(rawURL)
	if ok {
		// The URL stored and dialled is the normalised one: its host in
		// ASCII, lowered, which is the form every check ran on (S1).
		in.url = u.String()
	} else {
		fields["url"] = []string{webhookURLMessage}
	}
	if description != nil {
		if d := strings.TrimSpace(*description); d != "" {
			if len([]rune(d)) > maxWebhookDescriptionLength {
				fields["description"] = []string{webhookDescriptionMessage}
			}
			in.description = &d
		}
	}
	for _, name := range events {
		in.events = append(in.events, strings.TrimSpace(name))
	}
	slices.Sort(in.events)
	in.events = slices.Compact(in.events)
	if len(in.events) == 0 || len(in.events) > maxWebhookEvents || slices.Contains(in.events, "") {
		fields["events"] = []string{webhookEventsMessage}
	}
	if len(fields) > 0 {
		return webhookInput{}, refuse(http.StatusBadRequest, "invalid_request", invalidRequestMessage, fields)
	}
	var unknown []string
	for _, name := range in.events {
		if _, ok := contracts.LookupWebhookEvent(name); !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		return webhookInput{}, webhookEventUnknown(unknown)
	}
	if err := checkWebhookHost(ctx, s.webhookResolver(), u); err != nil {
		return webhookInput{}, err
	}
	return in, nil
}

// webhookEventPermitted reports whether an integration holding held may
// receive e (W7): it holds every permission of e's GET, and none of them is
// one no API key ever holds.
func (s *server) webhookEventPermitted(held []string, e contracts.WebhookEvent) bool {
	for _, key := range e.Permissions {
		if !slices.Contains(held, key) || s.access.apiNever(key) {
			return false
		}
	}
	return true
}

// webhookEventsPermitted refuses, on q, the events the integration's roles
// do not let it receive (422 webhook_event_not_permitted).
//
// It reads the roles without locking them, so a role taken from the
// integration while the save commits can leave an endpoint listing an event
// it no longer may receive. That is accepted: the check here is the screen's
// advice, and the boundary is W7 and W33 — the fan-out and the sender's claim
// check the permission again, and record such an event skipped
// (permission_not_held) instead of sending it.
func (s *server) webhookEventsPermitted(ctx context.Context, q *store.Queries, integration uuid.UUID, events []string) error {
	held, err := q.EffectivePermissionsForUser(ctx, integration)
	if err != nil {
		return fmt.Errorf("identity: read the integration's permissions: %w", err)
	}
	var refused []string
	for _, name := range events {
		e, ok := contracts.LookupWebhookEvent(name)
		if !ok || !s.webhookEventPermitted(held, e) {
			refused = append(refused, name)
		}
	}
	if len(refused) > 0 {
		return webhookEventNotPermitted(refused)
	}
	return nil
}

// webhookBody is an endpoint as the screen shows it: never its secret, and
// the previous secret's expiry only while it still signs.
//
// The previous secret's columns are cleared once its overlap has ended by the
// next write to the endpoint (change, enable, disable, roll), not by a read,
// so a row can still hold an expired one. The sender (delivery B) must
// therefore never sign with the previous secret unless
// previous_secret_expires_at is after the attempt's own time, whatever the
// columns hold.
func webhookBody(e store.IdentityWebhookEndpoint, now time.Time) gen.WebhookEndpoint {
	var reason *gen.WebhookEndpointDisabledReason
	if e.DisabledReason != nil {
		r := gen.WebhookEndpointDisabledReason(*e.DisabledReason)
		reason = &r
	}
	previous := e.PreviousSecretExpiresAt
	if previous != nil && !previous.After(now) {
		previous = nil
	}
	return gen.WebhookEndpoint{
		Id: e.ID, IntegrationId: e.IntegrationID, Url: e.Url, Description: e.Description, Events: nonNil(e.Events),
		Status: gen.WebhookEndpointStatus(e.Status), DisabledReason: reason, DisabledAt: e.DisabledAt,
		FailingSince: e.FailingSince, LastSuccessAt: e.LastSuccessAt, PreviousSecretExpiresAt: previous,
		CreatedAt: e.CreatedAt, CreatedBy: e.CreatedBy, UpdatedAt: e.UpdatedAt, Version: e.Version.String(),
	}
}

// integrationExists answers 404 for an integration there is none of.
func (s *server) integrationExists(ctx context.Context, id uuid.UUID) error {
	if _, err := s.q.IntegrationArchivedAt(ctx, id); errors.Is(err, pgx.ErrNoRows) {
		return integrationNotFound
	} else if err != nil {
		return fmt.Errorf("identity: read the integration: %w", err)
	}
	return nil
}

// webhookTx runs fn on the endpoint id of the integration, both locked, the
// integration first (integrationTx): 404 when either is missing, 409
// integration_archived for an archived integration unless archivedOK.
func (s *server) webhookTx(ctx context.Context, integration, id uuid.UUID, archivedOK bool, fn func(q *store.Queries, e store.IdentityWebhookEndpoint, now time.Time) error) error {
	return s.integrationTx(ctx, integration, archivedOK, func(q *store.Queries, _ store.LockIntegrationRow, now time.Time) error {
		e, err := q.LockWebhookEndpoint(ctx, store.LockWebhookEndpointParams{ID: id, IntegrationID: integration})
		if errors.Is(err, pgx.ErrNoRows) {
			return webhookNotFound
		}
		if err != nil {
			return fmt.Errorf("identity: read the webhook endpoint: %w", err)
		}
		return fn(q, e, now)
	})
}

// GetIdentityIntegrationsByIdWebhooks lists an integration's endpoints.
// (GET /api/v1/identity/integrations/{id}/webhooks)
func (s *server) GetIdentityIntegrationsByIdWebhooks(ctx context.Context, req gen.GetIdentityIntegrationsByIdWebhooksRequestObject) (gen.GetIdentityIntegrationsByIdWebhooksResponseObject, error) {
	if err := s.integrationExists(ctx, req.Id); err != nil {
		return refusalOr[gen.GetIdentityIntegrationsByIdWebhooksResponseObject](err)
	}
	rows, err := s.q.ListWebhookEndpoints(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("identity: list the webhook endpoints: %w", err)
	}
	now := s.deps.Clock()
	out := gen.WebhookEndpointList{Endpoints: make([]gen.WebhookEndpoint, 0, len(rows))}
	for _, e := range rows {
		out.Endpoints = append(out.Endpoints, webhookBody(e, now))
	}
	return gen.GetIdentityIntegrationsByIdWebhooks200JSONResponse(out), nil
}

// PostIdentityIntegrationsByIdWebhooks creates an endpoint and answers its
// secret once, never cached, audited webhook.created without it (W1, W8).
// (POST /api/v1/identity/integrations/{id}/webhooks)
func (s *server) PostIdentityIntegrationsByIdWebhooks(ctx context.Context, req gen.PostIdentityIntegrationsByIdWebhooksRequestObject) (gen.PostIdentityIntegrationsByIdWebhooksResponseObject, error) {
	var body gen.WebhookEndpointCreateRequest
	if req.Body != nil {
		body = *req.Body
	}
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	in, err := s.readWebhookInput(ctx, body.Url, body.Description, body.Events)
	if err != nil {
		return refusalOr[gen.PostIdentityIntegrationsByIdWebhooksResponseObject](err)
	}
	id := uuid.New()
	secret := newWebhookSecret()
	sealed, err := sealWebhookSecret(s.deps.Secrets, id, secret)
	if err != nil {
		return nil, err
	}
	var created store.IdentityWebhookEndpoint
	err = s.integrationTx(ctx, req.Id, false, func(q *store.Queries, _ store.LockIntegrationRow, now time.Time) error {
		if err := s.webhookEventsPermitted(ctx, q, req.Id, in.events); err != nil {
			return err
		}
		if err := q.AcquireWebhookCreationLock(ctx, webhookCreationLock); err != nil {
			return fmt.Errorf("identity: lock webhook creation: %w", err)
		}
		counts, err := q.CountWebhookEndpoints(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("identity: count the webhook endpoints: %w", err)
		}
		if counts.IntegrationCount >= maxWebhooksPerIntegration || counts.WorkspaceCount >= maxWebhooksPerWorkspace {
			return webhookLimitReached
		}
		created, err = q.InsertWebhookEndpoint(ctx, store.InsertWebhookEndpointParams{
			ID: id, IntegrationID: req.Id, Url: in.url, Description: in.description, Events: in.events,
			SecretCiphertext: sealed, CreatedBy: &p.UserID, Now: now, Version: uuid.New(),
		})
		if err != nil {
			return fmt.Errorf("identity: create the webhook endpoint: %w", err)
		}
		after, err := s.webhookSnapshot(created)
		if err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &req.Id, action: auditWebhookCreated, before: struct{}{}, after: after, mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PostIdentityIntegrationsByIdWebhooksResponseObject](err)
	}
	noStore := secretCacheControl
	return gen.PostIdentityIntegrationsByIdWebhooks201JSONResponse{
		Body:    gen.WebhookEndpointSecret{Endpoint: webhookBody(created, s.deps.Clock()), Secret: secret},
		Headers: gen.PostIdentityIntegrationsByIdWebhooks201ResponseHeaders{CacheControl: &noStore},
	}, nil
}

// GetIdentityIntegrationsByIdWebhooksByWebhookId reads one endpoint.
// (GET /api/v1/identity/integrations/{id}/webhooks/{webhookId})
func (s *server) GetIdentityIntegrationsByIdWebhooksByWebhookId(ctx context.Context, req gen.GetIdentityIntegrationsByIdWebhooksByWebhookIdRequestObject) (gen.GetIdentityIntegrationsByIdWebhooksByWebhookIdResponseObject, error) {
	if err := s.integrationExists(ctx, req.Id); err != nil {
		return refusalOr[gen.GetIdentityIntegrationsByIdWebhooksByWebhookIdResponseObject](err)
	}
	e, err := s.q.GetWebhookEndpoint(ctx, store.GetWebhookEndpointParams{ID: req.WebhookId, IntegrationID: req.Id})
	if errors.Is(err, pgx.ErrNoRows) {
		return webhookNotFound, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: read the webhook endpoint: %w", err)
	}
	return gen.GetIdentityIntegrationsByIdWebhooksByWebhookId200JSONResponse(webhookBody(e, s.deps.Clock())), nil
}

// PatchIdentityIntegrationsByIdWebhooksByWebhookId replaces an endpoint's
// URL, description and events under If-Match, audited webhook.updated (W10).
// (PATCH /api/v1/identity/integrations/{id}/webhooks/{webhookId})
func (s *server) PatchIdentityIntegrationsByIdWebhooksByWebhookId(ctx context.Context, req gen.PatchIdentityIntegrationsByIdWebhooksByWebhookIdRequestObject) (gen.PatchIdentityIntegrationsByIdWebhooksByWebhookIdResponseObject, error) {
	version, ok := parseIntegrationVersion(req.Params.IfMatch)
	if !ok {
		return refuse(http.StatusBadRequest, "invalid_request", webhookIfMatchMessage,
			map[string][]string{"If-Match": {webhookIfMatchMessage}}), nil
	}
	var body gen.WebhookEndpointUpdateRequest
	if req.Body != nil {
		body = *req.Body
	}
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	in, err := s.readWebhookInput(ctx, body.Url, body.Description, body.Events)
	if err != nil {
		return refusalOr[gen.PatchIdentityIntegrationsByIdWebhooksByWebhookIdResponseObject](err)
	}
	var updated store.IdentityWebhookEndpoint
	err = s.webhookTx(ctx, req.Id, req.WebhookId, false, func(q *store.Queries, e store.IdentityWebhookEndpoint, now time.Time) error {
		if e.Version != version {
			return webhookVersionMismatch
		}
		if err := s.webhookEventsPermitted(ctx, q, req.Id, in.events); err != nil {
			return err
		}
		before, err := s.webhookSnapshot(e)
		if err != nil {
			return err
		}
		updated, err = q.UpdateWebhookEndpoint(ctx, store.UpdateWebhookEndpointParams{
			ID: e.ID, Url: in.url, Description: in.description, Events: in.events, Now: now, Version: uuid.New(),
		})
		if err != nil {
			return fmt.Errorf("identity: change the webhook endpoint: %w", err)
		}
		after, err := s.webhookSnapshot(updated)
		if err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &req.Id, action: auditWebhookUpdated, before: before, after: after, mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PatchIdentityIntegrationsByIdWebhooksByWebhookIdResponseObject](err)
	}
	return gen.PatchIdentityIntegrationsByIdWebhooksByWebhookId200JSONResponse(webhookBody(updated, s.deps.Clock())), nil
}

// DeleteIdentityIntegrationsByIdWebhooksByWebhookId deletes an endpoint with
// its deliveries, audited webhook.deleted; an archived integration's
// endpoints may be deleted too.
// (DELETE /api/v1/identity/integrations/{id}/webhooks/{webhookId})
func (s *server) DeleteIdentityIntegrationsByIdWebhooksByWebhookId(ctx context.Context, req gen.DeleteIdentityIntegrationsByIdWebhooksByWebhookIdRequestObject) (gen.DeleteIdentityIntegrationsByIdWebhooksByWebhookIdResponseObject, error) {
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	err = s.webhookTx(ctx, req.Id, req.WebhookId, true, func(q *store.Queries, e store.IdentityWebhookEndpoint, now time.Time) error {
		before, err := s.webhookSnapshot(e)
		if err != nil {
			return err
		}
		if err := q.DeleteWebhookEndpoint(ctx, e.ID); err != nil {
			return fmt.Errorf("identity: delete the webhook endpoint: %w", err)
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &req.Id, action: auditWebhookDeleted, before: before, after: struct{}{}, mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.DeleteIdentityIntegrationsByIdWebhooksByWebhookIdResponseObject](err)
	}
	return gen.DeleteIdentityIntegrationsByIdWebhooksByWebhookId204Response{}, nil
}

// disableWebhook disables e for reason on q and settles its pending
// deliveries as endpoint_disabled, audited webhook.disabled with actor: the
// endpoint's row is held already (W18, W31).
func (s *server) disableWebhook(ctx context.Context, q *store.Queries, r *http.Request, e store.IdentityWebhookEndpoint, reason, settled string, actor *uuid.UUID, mfa bool, now time.Time) (store.IdentityWebhookEndpoint, error) {
	before, err := s.webhookSnapshot(e)
	if err != nil {
		return e, err
	}
	disabled, err := q.DisableWebhookEndpoint(ctx, store.DisableWebhookEndpointParams{ID: e.ID, Reason: &reason, Now: now, Version: uuid.New()})
	if err != nil {
		return e, fmt.Errorf("identity: disable the webhook endpoint: %w", err)
	}
	if _, err := q.DeadLetterWebhookDeliveries(ctx, store.DeadLetterWebhookDeliveriesParams{EndpointIds: []uuid.UUID{e.ID}, Reason: &settled, Now: now}); err != nil {
		return e, fmt.Errorf("identity: settle the webhook endpoint's deliveries: %w", err)
	}
	after, err := s.webhookSnapshot(disabled)
	if err != nil {
		return e, err
	}
	return disabled, writeAudit(ctx, q, r, now, auditEvent{
		actor: actor, targetUser: &e.IntegrationID, action: auditWebhookDisabled, before: before, after: after, mfa: mfa,
	})
}

// PostIdentityIntegrationsByIdWebhooksByWebhookIdDisable disables an
// endpoint; one disabled already is answered as it is.
// (POST /api/v1/identity/integrations/{id}/webhooks/{webhookId}/disable)
func (s *server) PostIdentityIntegrationsByIdWebhooksByWebhookIdDisable(ctx context.Context, req gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdDisableRequestObject) (gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdDisableResponseObject, error) {
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	var out store.IdentityWebhookEndpoint
	err = s.webhookTx(ctx, req.Id, req.WebhookId, true, func(q *store.Queries, e store.IdentityWebhookEndpoint, now time.Time) error {
		out = e
		if e.Status == webhookDisabled {
			return nil
		}
		var err error
		out, err = s.disableWebhook(ctx, q, r, e, webhookReasonManual, webhookReasonEndpointClosed, &p.UserID, p.MFAVerified, now)
		return err
	})
	if err != nil {
		return refusalOr[gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdDisableResponseObject](err)
	}
	return gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdDisable200JSONResponse(webhookBody(out, s.deps.Clock())), nil
}

// PostIdentityIntegrationsByIdWebhooksByWebhookIdEnable enables a disabled
// endpoint, with no failure counted and its notice free to go again (W18,
// W32), audited webhook.enabled; one enabled already is answered as it is.
// (POST /api/v1/identity/integrations/{id}/webhooks/{webhookId}/enable)
func (s *server) PostIdentityIntegrationsByIdWebhooksByWebhookIdEnable(ctx context.Context, req gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdEnableRequestObject) (gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdEnableResponseObject, error) {
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	var out store.IdentityWebhookEndpoint
	err = s.webhookTx(ctx, req.Id, req.WebhookId, false, func(q *store.Queries, e store.IdentityWebhookEndpoint, now time.Time) error {
		out = e
		if e.Status == webhookEnabled {
			return nil
		}
		before, err := s.webhookSnapshot(e)
		if err != nil {
			return err
		}
		out, err = q.EnableWebhookEndpoint(ctx, store.EnableWebhookEndpointParams{ID: e.ID, Now: now, Version: uuid.New()})
		if err != nil {
			return fmt.Errorf("identity: enable the webhook endpoint: %w", err)
		}
		after, err := s.webhookSnapshot(out)
		if err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &req.Id, action: auditWebhookEnabled, before: before, after: after, mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdEnableResponseObject](err)
	}
	return gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdEnable200JSONResponse(webhookBody(out, s.deps.Clock())), nil
}

// PostIdentityIntegrationsByIdWebhooksByWebhookIdRollSecret gives an
// endpoint a new secret and answers it once, the one it replaces signing
// beside it for the overlap (W9), audited webhook.secret_rolled without
// either.
// (POST /api/v1/identity/integrations/{id}/webhooks/{webhookId}/roll-secret)
func (s *server) PostIdentityIntegrationsByIdWebhooksByWebhookIdRollSecret(ctx context.Context, req gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdRollSecretRequestObject) (gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdRollSecretResponseObject, error) {
	var body gen.WebhookSecretRollRequest
	if req.Body != nil {
		body = *req.Body
	}
	overlap := defaultWebhookOverlap
	if body.OverlapHours != nil {
		overlap = time.Duration(*body.OverlapHours) * time.Hour
		if overlap < minWebhookOverlap || overlap > maxWebhookOverlap {
			return refuse(http.StatusBadRequest, "invalid_request", invalidRequestMessage,
				map[string][]string{"overlapHours": {webhookOverlapMessage}}), nil
		}
	}
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	secret := newWebhookSecret()
	sealed, err := sealWebhookSecret(s.deps.Secrets, req.WebhookId, secret)
	if err != nil {
		return nil, err
	}
	var rolled store.IdentityWebhookEndpoint
	err = s.webhookTx(ctx, req.Id, req.WebhookId, false, func(q *store.Queries, e store.IdentityWebhookEndpoint, now time.Time) error {
		before, err := s.webhookSnapshot(e)
		if err != nil {
			return err
		}
		rolled, err = q.RollWebhookEndpointSecret(ctx, store.RollWebhookEndpointSecretParams{
			ID: e.ID, SecretCiphertext: sealed, PreviousUntil: now.Add(overlap), Now: now, Version: uuid.New(),
		})
		if err != nil {
			return fmt.Errorf("identity: roll the webhook secret: %w", err)
		}
		after, err := s.webhookSnapshot(rolled)
		if err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &req.Id, action: auditWebhookSecretRolled, before: before, after: after, mfa: p.MFAVerified,
		})
	})
	if err != nil {
		return refusalOr[gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdRollSecretResponseObject](err)
	}
	noStore := secretCacheControl
	return gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdRollSecret201JSONResponse{
		Body:    gen.WebhookEndpointSecret{Endpoint: webhookBody(rolled, s.deps.Clock()), Secret: secret},
		Headers: gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdRollSecret201ResponseHeaders{CacheControl: &noStore},
	}, nil
}

// GetIdentityWebhookEvents is the catalogue (W4) as one integration sees it:
// each event marked with whether its roles let it receive the event now.
// (GET /api/v1/identity/webhook-events)
func (s *server) GetIdentityWebhookEvents(ctx context.Context, req gen.GetIdentityWebhookEventsRequestObject) (gen.GetIdentityWebhookEventsResponseObject, error) {
	id := req.Params.IntegrationId
	if err := s.integrationExists(ctx, id); err != nil {
		return refusalOr[gen.GetIdentityWebhookEventsResponseObject](err)
	}
	held, err := s.q.EffectivePermissionsForUser(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("identity: read the integration's permissions: %w", err)
	}
	catalogue := contracts.WebhookEvents()
	out := gen.WebhookEventCatalogue{Events: make([]gen.WebhookEvent, 0, len(catalogue))}
	for _, e := range catalogue {
		out.Events = append(out.Events, gen.WebhookEvent{
			Name: e.Name(), Module: e.Module(), Resource: e.Resource, Path: e.Path,
			Permissions: e.Permissions, Permitted: s.webhookEventPermitted(held, e),
		})
	}
	return gen.GetIdentityWebhookEvents200JSONResponse(out), nil
}

// archiveIntegrationWebhooks disables every enabled endpoint of an
// integration being archived, on its archive's transaction, and settles
// their pending deliveries, audited webhook.disabled per endpoint (W2, W31,
// W32). It holds the endpoints' rows first, so a fan-out never inserts a
// delivery after their pending ones were settled.
func (s *server) archiveIntegrationWebhooks(ctx context.Context, q *store.Queries, r *http.Request, integration uuid.UUID, p contracts.Principal, now time.Time) error {
	endpoints, err := q.LockIntegrationWebhookEndpoints(ctx, integration)
	if err != nil {
		return fmt.Errorf("identity: read the integration's webhook endpoints: %w", err)
	}
	// A disabled endpoint has nothing pending: its disabling settled it.
	for _, e := range endpoints {
		if e.Status != webhookEnabled {
			continue
		}
		if _, err := s.disableWebhook(ctx, q, r, e, webhookReasonArchived, webhookReasonArchived, &p.UserID, p.MFAVerified, now); err != nil {
			return err
		}
	}
	return nil
}

func (r refusal) VisitGetIdentityIntegrationsByIdWebhooksResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityIntegrationsByIdWebhooksResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitGetIdentityIntegrationsByIdWebhooksByWebhookIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPatchIdentityIntegrationsByIdWebhooksByWebhookIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitDeleteIdentityIntegrationsByIdWebhooksByWebhookIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityIntegrationsByIdWebhooksByWebhookIdDisableResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityIntegrationsByIdWebhooksByWebhookIdEnableResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityIntegrationsByIdWebhooksByWebhookIdRollSecretResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitGetIdentityWebhookEventsResponse(w http.ResponseWriter) error {
	return r.write(w)
}
