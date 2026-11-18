package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The webhooks' fan-out (outgoing webhooks design W7, W12, W14, W31): the
// outbox handler identity.webhooks takes every catalogue event and, on the
// outbox delivery's own transaction, queues one message per endpoint that
// lists it, its thin body fixed there. Nothing here leaves the process; the
// sender (identity.webhook-sender, webhooks_worker.go) sends what it queued.

const (
	webhooksHandlerName = "identity.webhooks"
	// webhookRunAttempts is a run's attempts for an event (W17); a ping has
	// one (W19).
	webhookRunAttempts = 8
	webhookPingName    = "webhook.ping"
)

// The delivery states and reasons the fan-out and the sender write (the
// migration's checks).
const (
	webhookPending           = "pending"
	webhookSucceeded         = "succeeded"
	webhookDead              = "dead"
	webhookSkipped           = "skipped"
	webhookKindEvent         = "event"
	webhookKindPing          = "ping"
	webhookReasonNotHeld     = "permission_not_held"
	webhookReasonExhausted   = "attempts_exhausted"
	webhookReasonGone        = "gone"
	webhookReasonFailing     = "failing"
	webhookReasonExpired     = "expired"
	webhookSecretUsedCurrent = "current"
	webhookSecretUsedBoth    = "both"
)

// webhookEventHandlers is identity's one outbox handler, identity.webhooks,
// taking every catalogue event at its version (W14). catalog is the composed
// permission catalog, which decides with the integration's roles whether it
// may receive an event (W7).
func webhookEventHandlers(d module.Deps) []events.Handler {
	catalogue := contracts.WebhookEvents()
	refs := make([]events.EventRef, 0, len(catalogue))
	for _, e := range catalogue {
		refs = append(refs, events.EventRef{Name: e.Name(), Version: e.Version})
	}
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	catalog := d.Catalog
	return []events.Handler{{
		Name:   webhooksHandlerName,
		Events: refs,
		Handle: func(ctx context.Context, tx pgx.Tx, ev events.Delivered) error {
			return fanOutWebhook(ctx, store.New(tx), catalog, ev, clock())
		},
	}}
}

// webhookPermitted reports whether an integration holding held may receive
// e under catalog (W7): it holds every permission of e's GET, and none of
// them is one no API key ever holds (apiNeverIn).
//
// An entry that names no permission is never permitted: the catalogue test
// holds every entry to at least one, and a slip there must fail closed.
func webhookPermitted(catalog map[string]contracts.Permission, held []string, e contracts.WebhookEvent) bool {
	if len(e.Permissions) == 0 {
		return false
	}
	for _, key := range e.Permissions {
		if !slices.Contains(held, key) || apiNeverIn(catalog, key) {
			return false
		}
	}
	return true
}

// fanOutWebhook queues ev for every endpoint that lists it, on q, the outbox
// delivery's transaction, at now (W14): the endpoints are held FOR SHARE in
// id order (W31), each integration's permission is read once, and each
// endpoint gets one row — pending, or skipped (permission_not_held) when the
// integration no longer may receive the event (W7), at most one such a day
// per endpoint and event (S5) — or none when an earlier fan-out of the same
// event already gave it one. An event whose
// payload lacks the catalogue's IDField is a bug and an error, so the outbox
// retries it and then holds it dead for the operator.
func fanOutWebhook(ctx context.Context, q *store.Queries, catalog map[string]contracts.Permission, ev events.Delivered, now time.Time) error {
	entry, ok := contracts.LookupWebhookEvent(ev.Name)
	if !ok || entry.Version != ev.Version {
		return nil
	}
	id, err := webhookResourceID(ev.Payload, entry.IDField)
	if err != nil {
		return fmt.Errorf("identity: webhook fan-out of %s event %d: %w", ev.Name, ev.ID, err)
	}
	endpoints, err := q.LockWebhookEndpointsForEvent(ctx, ev.Name)
	if err != nil {
		return fmt.Errorf("identity: read the webhook endpoints of %s: %w", ev.Name, err)
	}
	if len(endpoints) == 0 {
		return nil
	}
	body, err := webhookEventBody(ev, entry, id)
	if err != nil {
		return err
	}
	permitted := map[uuid.UUID]bool{}
	for _, e := range endpoints {
		ok, seen := permitted[e.IntegrationID]
		if !seen {
			held, err := q.EffectivePermissionsForUser(ctx, e.IntegrationID)
			if err != nil {
				return fmt.Errorf("identity: read the integration's permissions: %w", err)
			}
			ok = webhookPermitted(catalog, held, entry)
			permitted[e.IntegrationID] = ok
		}
		row := store.InsertWebhookDeliveryParams{
			ID: uuid.New(), EndpointID: e.ID, EventID: &ev.ID, EventName: ev.Name, Kind: webhookKindEvent, Body: body,
			State: webhookPending, MaxRunAttempts: webhookRunAttempts, NextAttemptAt: &now, CreatedAt: now,
		}
		if !ok {
			// One skipped row a day per endpoint and event shows the
			// cause; more would only grow the log (S5).
			recent, err := q.RecentSkippedWebhookDelivery(ctx, store.RecentSkippedWebhookDeliveryParams{
				EndpointID: e.ID, EventName: ev.Name, Since: now.Add(-webhookSkippedEvery),
			})
			if err != nil {
				return fmt.Errorf("identity: read the webhook endpoint's skipped deliveries: %w", err)
			}
			if recent {
				continue
			}
			reason := webhookReasonNotHeld
			row.State, row.StateReason, row.NextAttemptAt, row.CompletedAt = webhookSkipped, &reason, nil, &now
		}
		if _, err := q.InsertWebhookDelivery(ctx, row); err != nil {
			return fmt.Errorf("identity: queue the webhook delivery: %w", err)
		}
	}
	return nil
}

// errWebhookIDField is an event whose payload has no usable resource id.
var errWebhookIDField = errors.New("the payload has no resource id in its catalogue field")

// webhookResourceID is the resource id the payload holds in field: a string
// as it is, a number as its JSON literal. Anything else — absent, null,
// empty, an object — is errWebhookIDField.
func webhookResourceID(payload []byte, field string) (string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return "", fmt.Errorf("read the payload: %w", err)
	}
	raw, ok := fields[field]
	if !ok {
		return "", fmt.Errorf("%w (%s)", errWebhookIDField, field)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("%w (%s)", errWebhookIDField, field)
	}
	switch id := v.(type) {
	case string:
		if strings.TrimSpace(id) != "" {
			return id, nil
		}
	case json.Number:
		if _, err := strconv.ParseInt(id.String(), 10, 64); err == nil {
			return id.String(), nil
		}
	}
	return "", fmt.Errorf("%w (%s)", errWebhookIDField, field)
}

// webhookMessageBody is a message's body (W12), its fields in this order.
// Sequence is absent from a ping.
type webhookMessageBody struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Version   int    `json:"version"`
	Timestamp string `json:"timestamp"`
	Sequence  *int64 `json:"sequence,omitempty"`
	Data      any    `json:"data"`
}

type webhookResource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Href string `json:"href"`
}

// webhookEventBody is the thin body of ev, which names the resource id
// (W12): its type, id and path, and nothing else of the event.
func webhookEventBody(ev events.Delivered, e contracts.WebhookEvent, id string) (string, error) {
	sequence := ev.ID
	body, err := json.Marshal(webhookMessageBody{
		ID: "evt_" + strconv.FormatInt(ev.ID, 10), Type: ev.Name, Version: ev.Version,
		Timestamp: ev.OccurredAt.UTC().Format(time.RFC3339), Sequence: &sequence,
		Data: struct {
			Resource webhookResource `json:"resource"`
		}{webhookResource{Type: e.Resource, ID: id, Href: e.Href(id)}},
	})
	if err != nil {
		return "", fmt.Errorf("identity: write the webhook body: %w", err)
	}
	return string(body), nil
}

// queueWebhookPing queues a test event to the endpoint on q at now (W19,
// W37): a run of one attempt, no event, its body's id evt_ping_ and the
// delivery's id. It answers the delivery's id.
func queueWebhookPing(ctx context.Context, q *store.Queries, endpoint uuid.UUID, now time.Time) (uuid.UUID, error) {
	id := uuid.New()
	body, err := json.Marshal(webhookMessageBody{
		ID: "evt_ping_" + id.String(), Type: webhookPingName, Version: 1,
		Timestamp: now.UTC().Format(time.RFC3339), Data: struct{}{},
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("identity: write the webhook ping: %w", err)
	}
	if _, err := q.InsertWebhookDelivery(ctx, store.InsertWebhookDeliveryParams{
		ID: id, EndpointID: endpoint, EventName: webhookPingName, Kind: webhookKindPing, Body: string(body),
		State: webhookPending, MaxRunAttempts: 1, NextAttemptAt: &now, CreatedAt: now,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("identity: queue the webhook ping: %w", err)
	}
	return id, nil
}
