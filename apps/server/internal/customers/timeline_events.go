package customers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/customers/store"
)

// This file is the customer handlers' half of CustomerTimelineRecorder
// (SV/CustomerTimelineRecorder.cs): the three "generated" events a
// customer's own create/update/archive can emit. It never saves
// independently — every call happens inside the caller's transaction
// alongside the customer row mutation it accompanies, exactly as .NET's
// recorder stages the entry in the same DbContext SaveChanges the mutation
// is part of. Task 9 builds the manual-entry and revision-listing endpoints
// on the same two tables (customers_timeline_entries[_revisions]); nothing
// here is timeline-task scope, only the write path this module's own create/
// update/delete need for GetCustomer(s)'s timelineSummary and the
// "records a timeline event" behaviour customers inventory §2.4 documents
// for status changes.

// legalIdentitySnapshot is CustomerIdentitySnapshot
// (SV/CustomerTimelineRecorder.cs:167), the shape a generated event's
// payload embeds a legal identity as.
type legalIdentitySnapshot struct {
	Country string `json:"country"`
	Type    string `json:"type"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Source  string `json:"source"`
}

func identitySnapshot(identity *legalIdentity) *legalIdentitySnapshot {
	if identity == nil {
		return nil
	}
	return &legalIdentitySnapshot{Country: identity.Country, Type: identity.Type, ID: identity.ID, Name: identity.Name, Source: identity.Source}
}

// identityEqual reports whether a and b are the same legal identity (or
// both absent). UpdateCustomer uses it to decide whether the identity
// changed at all, the same comparison
// UpdateCustomerEndpoint.cs:93's `customer.Identity != customerIdentity`
// makes.
func identityEqual(a, b *legalIdentity) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// recordGeneratedEvent inserts one generated timeline entry and its single
// revision. now's UTC calendar date becomes the entry's occurred_on, and
// now itself its occurred_at, created_at and updated_at, as .NET's recorder
// stamps every field from one captured DateTimeOffset.UtcNow
// (SV/CustomerTimelineRecorder.cs:132-133).
func recordGeneratedEvent(ctx context.Context, q *store.Queries, customerID int32, now time.Time, eventType, summary string, payload any, payloadVersion int32) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("customers: encode timeline payload: %w", err)
	}
	utc := now.UTC()
	occurredOn := pgtype.Date{Time: time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
	return q.InsertGeneratedTimelineEvent(ctx, store.InsertGeneratedTimelineEventParams{
		CustomerID:     customerID,
		EventType:      eventType,
		OccurredOn:     occurredOn,
		Now:            now,
		Summary:        summary,
		PayloadJson:    payloadJSON,
		PayloadVersion: payloadVersion,
	})
}

// recordCustomerCreated is RecordCustomerCreated
// (SV/CustomerTimelineRecorder.cs:28-33).
func recordCustomerCreated(ctx context.Context, q *store.Queries, now time.Time, customerID int32, name string, identity *legalIdentity) error {
	payload := map[string]any{
		"customerId":    customerID,
		"customerName":  name,
		"legalIdentity": identitySnapshot(identity),
	}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.created", fmt.Sprintf("Customer created: %s", name), payload, 1)
}

// recordCustomerUpdated is RecordCustomerUpdated
// (SV/CustomerTimelineRecorder.cs:35-63): only called when the name or the
// legal identity actually changed (UpdateCustomerEndpoint.cs:93,97-100), and
// its payload is the only one in the module carrying PayloadVersion 2
// (inventory §2.4).
func recordCustomerUpdated(ctx context.Context, q *store.Queries, now time.Time, customerID int32, beforeName string, beforeIdentity *legalIdentity, afterName string, afterIdentity *legalIdentity) error {
	identityChanged := !identityEqual(beforeIdentity, afterIdentity)
	changeNote := ""
	switch {
	case !identityChanged:
	case beforeIdentity == nil:
		changeNote = "legal identity added"
	case afterIdentity == nil:
		changeNote = "legal identity removed"
	default:
		changeNote = "legal identity updated"
	}
	summary := fmt.Sprintf("Customer updated: %s", afterName)
	if changeNote != "" {
		summary = fmt.Sprintf("Customer updated: %s — %s", afterName, changeNote)
	}

	before := map[string]any{"customerName": beforeName}
	after := map[string]any{"customerName": afterName}
	changes := map[string]any{}
	if identityChanged {
		before["legalIdentity"] = identitySnapshot(beforeIdentity)
		after["legalIdentity"] = identitySnapshot(afterIdentity)
		changes["legalIdentity"] = map[string]any{"before": identitySnapshot(beforeIdentity), "after": identitySnapshot(afterIdentity)}
	}
	if beforeName != afterName {
		changes["customerName"] = map[string]any{"before": beforeName, "after": afterName}
	}
	payload := map[string]any{"customerId": customerID, "before": before, "after": after, "changes": changes}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.updated", summary, payload, 2)
}

// recordCustomerStatusChanged is RecordCustomerStatusChanged
// (SV/CustomerTimelineRecorder.cs:65-76): called from both PutCustomersById
// (an explicit status change) and DeleteCustomersById (the archive
// transition).
func recordCustomerStatusChanged(ctx context.Context, q *store.Queries, now time.Time, customerID int32, previousStatus, currentStatus string) error {
	summary := fmt.Sprintf("Customer status changed: %s → %s", previousStatus, currentStatus)
	payload := map[string]any{
		"customerId": customerID,
		"before":     map[string]any{"status": previousStatus},
		"after":      map[string]any{"status": currentStatus},
		"changes":    map[string]any{"status": map[string]any{"before": previousStatus, "after": currentStatus}},
	}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.status_changed", summary, payload, 1)
}
