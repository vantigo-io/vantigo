package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// This file is a webhook endpoint's delivery log and its resend (outgoing
// webhooks design W22, W23, W35): an Owner reads an endpoint's deliveries,
// newest first by cursor, one with its body and attempts, and sends one that
// succeeded or is dead again. Every operation is scoped by the integration
// and the endpoint both, so a delivery of another endpoint is not found, and
// is policy:Owner+OwnerManagement, never open to a key or the Assistant
// (W34). An attempt's excerpt is the receiver's text, answered as JSON
// strings and shown by the screen as plain text only (S7).

const (
	// defaultWebhookLogPage and maxWebhookLogPage bound a page of the log.
	defaultWebhookLogPage = 50
	maxWebhookLogPage     = 100
	// webhookAttemptsShown is how many of a delivery's attempts its detail
	// answers, the latest.
	webhookAttemptsShown = 100
	// webhookResendsPerWindow resends of one delivery are allowed in each
	// webhookResendWindow (W35).
	webhookResendsPerWindow = 3
	webhookResendWindow     = time.Minute
	// webhookResendBefore is how old a delivery may be and still be resent:
	// the retention less the 3 days a run may stay pending, so a resend's
	// run is never pruned before it can finish (W22, W30).
	webhookResendBefore = webhookRetention - webhookPendingLimit
	// webhookMaxAttempts caps a delivery's attempts over its life, about 10
	// runs of 8, so resending one cannot become a stream of its own.
	webhookMaxAttempts = 10 * webhookRunAttempts
	auditWebhookResent = "webhook.resent"
)

var (
	webhookDeliveryNotFound = refuse(http.StatusNotFound, "not_found", "No such delivery of this webhook endpoint.", nil)
	// webhookNotResendable refuses a ping, a delivery still pending or
	// skipped, one queued 27 days ago or more, and one that has had 80
	// attempts (W22).
	webhookNotResendable = refuse(http.StatusConflict, "webhook_delivery_not_resendable",
		"Only an event that succeeded or is dead, queued less than 27 days ago and tried fewer than 80 times, is resent; send a test event again instead of resending one.", nil)
	webhookResendNotPermitted = refuse(http.StatusConflict, "webhook_event_not_permitted",
		"The integration's roles no longer let it receive this event.", nil)
	// webhookResendNotSubscribed refuses an event the endpoint no longer
	// lists: a resend goes to the endpoint as it is now.
	webhookResendNotSubscribed = refuse(http.StatusConflict, "webhook_event_not_subscribed",
		"The endpoint no longer lists this event.", nil)
)

// webhookResendLimited is the per-delivery limit's refusal (W35): three
// resends within a window of one minute that opens at the first, and when
// the window closes.
type webhookResendLimited struct{ retryAfter time.Duration }

func (webhookResendLimited) Error() string {
	return "identity: refused with 429 webhook_resend_limited"
}

// webhookLogCursor is a page's last row, the next page's bound.
type webhookLogCursor struct {
	At time.Time `json:"t"`
	ID uuid.UUID `json:"i"`
}

func formatWebhookLogCursor(d store.ListWebhookDeliveriesRow) string {
	raw, _ := json.Marshal(webhookLogCursor{At: d.CreatedAt, ID: d.ID}) // a time and a uuid: Marshal cannot fail
	return base64.RawURLEncoding.EncodeToString(raw)
}

func parseWebhookLogCursor(text string) (webhookLogCursor, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil {
		return webhookLogCursor{}, false
	}
	var c webhookLogCursor
	if err := json.Unmarshal(raw, &c); err != nil || c.At.IsZero() || c.ID == uuid.Nil {
		return webhookLogCursor{}, false
	}
	return c, true
}

// webhookResendable reports whether d's kind, state, age and attempts let
// it be resent at now (W22): an event, not a ping, that succeeded or is
// dead, queued less than 27 days ago, so its run ends before the 30 days'
// prune, and tried fewer than 80 times.
func webhookResendable(d store.ListWebhookDeliveriesRow, now time.Time) bool {
	return d.Kind == webhookKindEvent && (d.State == webhookSucceeded || d.State == webhookDead) &&
		now.Sub(d.CreatedAt) < webhookResendBefore && d.TotalAttempts < webhookMaxAttempts
}

// webhookLogRow is a whole delivery as the log reads its rows.
func webhookLogRow(d store.IdentityWebhookDelivery) store.ListWebhookDeliveriesRow {
	return store.ListWebhookDeliveriesRow{
		ID: d.ID, EventID: d.EventID, EventName: d.EventName, Kind: d.Kind, State: d.State, StateReason: d.StateReason,
		RunAttempts: d.RunAttempts, MaxRunAttempts: d.MaxRunAttempts, TotalAttempts: d.TotalAttempts,
		NextAttemptAt: d.NextAttemptAt, LastStatus: d.LastStatus, LastError: d.LastError,
		CreatedAt: d.CreatedAt, CompletedAt: d.CompletedAt,
	}
}

// webhookDeliveryBody is d as the log shows it, without its body.
func webhookDeliveryBody(d store.ListWebhookDeliveriesRow, now time.Time) gen.WebhookDelivery {
	out := gen.WebhookDelivery{
		Id: d.ID, MessageId: webhookMessageID(d.ID), EventId: d.EventID, EventName: d.EventName,
		Kind: gen.WebhookDeliveryKind(d.Kind), State: gen.WebhookDeliveryState(d.State),
		RunAttempts: int(d.RunAttempts), MaxRunAttempts: int(d.MaxRunAttempts), TotalAttempts: int(d.TotalAttempts),
		NextAttemptAt: d.NextAttemptAt, CreatedAt: d.CreatedAt, CompletedAt: d.CompletedAt,
		Resendable: webhookResendable(d, now),
	}
	if d.StateReason != nil {
		r := gen.WebhookDeliveryStateReason(*d.StateReason)
		out.StateReason = &r
	}
	if d.LastStatus != nil {
		s := int(*d.LastStatus)
		out.LastStatus = &s
	}
	if d.LastError != nil {
		e := gen.WebhookDeliveryLastError(*d.LastError)
		out.LastError = &e
	}
	return out
}

// webhookEndpointOf answers 404 unless the endpoint is one of the
// integration's, read without a lock.
func (s *server) webhookEndpointOf(ctx context.Context, integration, id uuid.UUID) error {
	if err := s.integrationExists(ctx, integration); err != nil {
		return err
	}
	if _, err := s.q.GetWebhookEndpoint(ctx, store.GetWebhookEndpointParams{ID: id, IntegrationID: integration}); errors.Is(err, pgx.ErrNoRows) {
		return webhookNotFound
	} else if err != nil {
		return fmt.Errorf("identity: read the webhook endpoint: %w", err)
	}
	return nil
}

// GetIdentityIntegrationsByIdWebhooksByWebhookIdDeliveries is an endpoint's
// delivery log, newest first, by cursor (W23).
// (GET /api/v1/identity/integrations/{id}/webhooks/{webhookId}/deliveries)
func (s *server) GetIdentityIntegrationsByIdWebhooksByWebhookIdDeliveries(ctx context.Context, req gen.GetIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesRequestObject) (gen.GetIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesResponseObject, error) {
	fields := map[string][]string{}
	params := store.ListWebhookDeliveriesParams{EndpointID: req.WebhookId}
	limit := defaultWebhookLogPage
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
		if limit < 1 || limit > maxWebhookLogPage {
			fields["limit"] = []string{"The limit is 1 to 100."}
		}
	}
	if req.Params.State != nil {
		state := string(*req.Params.State)
		switch state {
		case webhookPending, webhookSucceeded, webhookDead, webhookSkipped:
			params.State = &state
		default:
			fields["state"] = []string{"The state is pending, succeeded, dead or skipped."}
		}
	}
	if req.Params.Event != nil && *req.Params.Event != "" {
		params.EventName = req.Params.Event
	}
	if req.Params.Cursor != nil {
		c, ok := parseWebhookLogCursor(*req.Params.Cursor)
		if !ok {
			fields["cursor"] = []string{"The cursor is not one this log gave."}
		}
		params.AfterAt, params.AfterID = &c.At, &c.ID
	}
	if len(fields) > 0 {
		return refuse(http.StatusBadRequest, "invalid_request", invalidRequestMessage, fields), nil
	}
	if err := s.webhookEndpointOf(ctx, req.Id, req.WebhookId); err != nil {
		return refusalOr[gen.GetIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesResponseObject](err)
	}
	params.PageSize = int32(limit + 1) //nolint:gosec // at most 101
	rows, err := s.q.ListWebhookDeliveries(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("identity: list the webhook deliveries: %w", err)
	}
	now := s.deps.Clock()
	out := gen.WebhookDeliveryPage{Deliveries: make([]gen.WebhookDelivery, 0, min(len(rows), limit))}
	for i, d := range rows {
		if i == limit {
			next := formatWebhookLogCursor(rows[limit-1])
			out.NextCursor = &next
			break
		}
		out.Deliveries = append(out.Deliveries, webhookDeliveryBody(d, now))
	}
	return gen.GetIdentityIntegrationsByIdWebhooksByWebhookIdDeliveries200JSONResponse(out), nil
}

// GetIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryId is a
// delivery with its body and its latest attempts, newest first (W23).
// (GET /api/v1/identity/integrations/{id}/webhooks/{webhookId}/deliveries/{deliveryId})
func (s *server) GetIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryId(ctx context.Context, req gen.GetIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryIdRequestObject) (gen.GetIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryIdResponseObject, error) {
	if err := s.webhookEndpointOf(ctx, req.Id, req.WebhookId); err != nil {
		return refusalOr[gen.GetIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryIdResponseObject](err)
	}
	d, err := s.q.GetWebhookDelivery(ctx, store.GetWebhookDeliveryParams{ID: req.DeliveryId, EndpointID: req.WebhookId})
	if errors.Is(err, pgx.ErrNoRows) {
		return webhookDeliveryNotFound, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: read the webhook delivery: %w", err)
	}
	attempts, err := s.q.ListWebhookAttempts(ctx, store.ListWebhookAttemptsParams{DeliveryID: d.ID, PageSize: webhookAttemptsShown})
	if err != nil {
		return nil, fmt.Errorf("identity: list the webhook attempts: %w", err)
	}
	out := gen.WebhookDeliveryDetail{
		Delivery: webhookDeliveryBody(webhookLogRow(d), s.deps.Clock()), Body: d.Body,
		Attempts: make([]gen.WebhookDeliveryAttempt, 0, len(attempts)),
	}
	for _, a := range attempts {
		attempt := gen.WebhookDeliveryAttempt{
			At: a.At, DurationMs: int(a.DurationMs), Excerpt: a.Excerpt, SecretUsed: gen.WebhookDeliveryAttemptSecretUsed(a.SecretUsed),
		}
		if a.StatusCode != nil {
			code := int(*a.StatusCode)
			attempt.StatusCode = &code
		}
		if a.Error != nil {
			e := gen.WebhookDeliveryAttemptError(*a.Error)
			attempt.Error = &e
		}
		out.Attempts = append(out.Attempts, attempt)
	}
	return gen.GetIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryId200JSONResponse(out), nil
}

// PostIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryIdResend
// sends a delivery again (W22, W35), audited webhook.resent: an event that
// succeeded or is dead, queued less than 27 days ago and tried fewer than 80
// times, of an enabled endpoint that still lists the event, of an
// integration that is not archived and still holds the event's
// permissions, checked again here. It keeps the delivery's id, so its
// webhook-id, and its body; it goes to the endpoint's URL as it is now;
// each attempt signs afresh at its own time; the run is a new one of 8. A
// delivery is resent at most 3 times within a window of one minute that
// opens at the first, by a counter on its row (W35), since identity's
// limits are per address.
// (POST /api/v1/identity/integrations/{id}/webhooks/{webhookId}/deliveries/{deliveryId}/resend)
func (s *server) PostIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryIdResend(ctx context.Context, req gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryIdResendRequestObject) (gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryIdResendResponseObject, error) {
	p, r, err := integrationCaller(ctx)
	if err != nil {
		return nil, err
	}
	var resent store.IdentityWebhookDelivery
	err = s.webhookTx(ctx, req.Id, req.WebhookId, false, func(q *store.Queries, e store.IdentityWebhookEndpoint, now time.Time) error {
		d, err := q.LockWebhookDelivery(ctx, store.LockWebhookDeliveryParams{ID: req.DeliveryId, EndpointID: e.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return webhookDeliveryNotFound
		}
		if err != nil {
			return fmt.Errorf("identity: read the webhook delivery: %w", err)
		}
		if e.Status != webhookEnabled {
			return webhookEndpointDisabled
		}
		if !webhookResendable(webhookLogRow(d), now) {
			return webhookNotResendable
		}
		if !slices.Contains(e.Events, d.EventName) {
			return webhookResendNotSubscribed
		}
		entry, ok := contracts.LookupWebhookEvent(d.EventName)
		if !ok {
			return webhookResendNotPermitted
		}
		held, err := q.EffectivePermissionsForUser(ctx, e.IntegrationID)
		if err != nil {
			return fmt.Errorf("identity: read the integration's permissions: %w", err)
		}
		if !s.webhookEventPermitted(held, entry) {
			return webhookResendNotPermitted
		}
		count, window := int32(1), now
		if d.ResendWindowStart != nil && now.Sub(*d.ResendWindowStart) < webhookResendWindow {
			if d.ResendCount >= webhookResendsPerWindow {
				return webhookResendLimited{retryAfter: d.ResendWindowStart.Add(webhookResendWindow).Sub(now)}
			}
			count, window = d.ResendCount+1, *d.ResendWindowStart
		}
		resent, err = q.ResendWebhookDelivery(ctx, store.ResendWebhookDeliveryParams{
			ID: d.ID, MaxRunAttempts: webhookRunAttempts, Now: now, ResendCount: count, ResendWindowStart: window,
		})
		if err != nil {
			return fmt.Errorf("identity: resend the webhook delivery: %w", err)
		}
		snapshot, err := s.webhookSnapshot(e)
		if err != nil {
			return err
		}
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, targetUser: &req.Id, action: auditWebhookResent, before: snapshot,
			after: struct {
				webhookAudit
				DeliveryID uuid.UUID `json:"deliveryId"`
				EventName  string    `json:"eventName"`
			}{snapshot, d.ID, d.EventName}, mfa: p.MFAVerified,
		})
	})
	var limited webhookResendLimited
	if errors.As(err, &limited) {
		// Retry-After is in whole seconds, at least one, until the window closes.
		seconds := strconv.Itoa(max(1, int((limited.retryAfter+time.Second-1)/time.Second)))
		return gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryIdResend429JSONResponse{
			Body: authErrorBody("webhook_resend_limited",
				"A delivery is resent at most 3 times within a window of one minute that opens at the first.", nil),
			Headers: gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryIdResend429ResponseHeaders{RetryAfter: &seconds},
		}, nil
	}
	if err != nil {
		return refusalOr[gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryIdResendResponseObject](err)
	}
	return gen.PostIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryIdResend202JSONResponse(webhookDeliveryBody(webhookLogRow(resent), s.deps.Clock())), nil
}

func (r refusal) VisitGetIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitGetIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryIdResponse(w http.ResponseWriter) error {
	return r.write(w)
}

func (r refusal) VisitPostIdentityIntegrationsByIdWebhooksByWebhookIdDeliveriesByDeliveryIdResendResponse(w http.ResponseWriter) error {
	return r.write(w)
}
