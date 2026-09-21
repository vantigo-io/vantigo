package customers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
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
// (SV/CustomerTimelineRecorder.cs:132-133). actorKind/actorDisplay/actorUserID
// are the acting user's resolved actor (server.actorFor, customers
// foundation design D1) — every caller resolves it once, before opening the
// transaction this function runs inside, and threads it down to here rather
// than this function resolving it itself.
func recordGeneratedEvent(ctx context.Context, q *store.Queries, customerID int32, now time.Time, eventType, summary string, payload any, payloadVersion int32, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
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
		ActorKind:      actorKind,
		ActorDisplay:   actorDisplay,
		ActorUserID:    actorUserID,
	})
}

// recordCustomerCreated is RecordCustomerCreated
// (SV/CustomerTimelineRecorder.cs:28-33).
func recordCustomerCreated(ctx context.Context, q *store.Queries, now time.Time, customerID int32, name string, identity *legalIdentity, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	payload := map[string]any{
		"customerId":    customerID,
		"customerName":  name,
		"legalIdentity": identitySnapshot(identity),
	}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.created", fmt.Sprintf("Customer created: %s", name), payload, 1, actorKind, actorDisplay, actorUserID)
}

// recordCustomerUpdated is RecordCustomerUpdated
// (SV/CustomerTimelineRecorder.cs:35-63): only called when the name or the
// legal identity actually changed (UpdateCustomerEndpoint.cs:93,97-100), and
// its payload is the only one in the module carrying PayloadVersion 2
// (inventory §2.4).
func recordCustomerUpdated(ctx context.Context, q *store.Queries, now time.Time, customerID int32, beforeName string, beforeIdentity *legalIdentity, afterName string, afterIdentity *legalIdentity, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
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
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.updated", summary, payload, 2, actorKind, actorDisplay, actorUserID)
}

// recordCustomerTypeChanged is the customer.type_changed event
// PutCustomersByIdType writes, shaped like recordCustomerStatusChanged's:
// no .NET ancestor, since the customer type is new to this port
// (00007_customers_type.sql).
func recordCustomerTypeChanged(ctx context.Context, q *store.Queries, now time.Time, customerID int32, previousType, currentType string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	summary := fmt.Sprintf("Customer type changed: %s → %s", previousType, currentType)
	payload := map[string]any{
		"customerId": customerID,
		"before":     map[string]any{"type": previousType},
		"after":      map[string]any{"type": currentType},
		"changes":    map[string]any{"type": map[string]any{"before": previousType, "after": currentType}},
	}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.type_changed", summary, payload, 1, actorKind, actorDisplay, actorUserID)
}

// recordCustomerStatusChanged is RecordCustomerStatusChanged
// (SV/CustomerTimelineRecorder.cs:65-76): called from both PutCustomersById
// (an explicit status change) and DeleteCustomersById (the archive
// transition).
func recordCustomerStatusChanged(ctx context.Context, q *store.Queries, now time.Time, customerID int32, previousStatus, currentStatus string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	summary := fmt.Sprintf("Customer status changed: %s → %s", previousStatus, currentStatus)
	payload := map[string]any{
		"customerId": customerID,
		"before":     map[string]any{"status": previousStatus},
		"after":      map[string]any{"status": currentStatus},
		"changes":    map[string]any{"status": map[string]any{"before": previousStatus, "after": currentStatus}},
	}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.status_changed", summary, payload, 1, actorKind, actorDisplay, actorUserID)
}

// contactInfoChanges is recordCustomerContactInfoUpdated's changes map,
// shaped like recordCustomerUpdated's: only the fields that actually moved,
// each keyed to its own before/after pair (customers foundation design D2).
func contactInfoChanges(before, after contactInfo) map[string]any {
	changes := map[string]any{}
	if !stringPtrEqual(before.Email, after.Email) {
		changes["email"] = map[string]any{"before": before.Email, "after": after.Email}
	}
	if !stringPtrEqual(before.Phone, after.Phone) {
		changes["phone"] = map[string]any{"before": before.Phone, "after": after.Phone}
	}
	if !stringPtrEqual(before.Website, after.Website) {
		changes["website"] = map[string]any{"before": before.Website, "after": after.Website}
	}
	return changes
}

// recordCustomerContactInfoUpdated is PutCustomersByIdContactInfo's own
// generated event (invoice-ready customer design D2), shaped like
// recordCustomerStatusChanged/recordCustomerTypeChanged: no .NET ancestor,
// since contact info is new to this port. Only called once the handler has
// already confirmed something changed (contact_info.go's no-op rule), so
// changes is never empty here.
func recordCustomerContactInfoUpdated(ctx context.Context, q *store.Queries, now time.Time, customerID int32, before, after contactInfo, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	payload := map[string]any{
		"customerId": customerID,
		"before":     before,
		"after":      after,
		"changes":    contactInfoChanges(before, after),
	}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.contact_info_updated", "Customer contact info updated", payload, 1, actorKind, actorDisplay, actorUserID)
}

// addressChanges is recordCustomerAddressUpdated's changes map, shaped like
// contactInfoChanges: only the fields that actually moved, each keyed to its
// own before/after pair (invoice-ready customer design D3).
func addressChanges(before, after addressSnapshot) map[string]any {
	changes := map[string]any{}
	if before.Type != after.Type {
		changes["type"] = map[string]any{"before": before.Type, "after": after.Type}
	}
	if !stringPtrEqual(before.Label, after.Label) {
		changes["label"] = map[string]any{"before": before.Label, "after": after.Label}
	}
	if before.Line1 != after.Line1 {
		changes["line1"] = map[string]any{"before": before.Line1, "after": after.Line1}
	}
	if !stringPtrEqual(before.Line2, after.Line2) {
		changes["line2"] = map[string]any{"before": before.Line2, "after": after.Line2}
	}
	if !stringPtrEqual(before.PostalCode, after.PostalCode) {
		changes["postalCode"] = map[string]any{"before": before.PostalCode, "after": after.PostalCode}
	}
	if !stringPtrEqual(before.City, after.City) {
		changes["city"] = map[string]any{"before": before.City, "after": after.City}
	}
	if !stringPtrEqual(before.Region, after.Region) {
		changes["region"] = map[string]any{"before": before.Region, "after": after.Region}
	}
	if before.Country != after.Country {
		changes["country"] = map[string]any{"before": before.Country, "after": after.Country}
	}
	if before.IsPrimary != after.IsPrimary {
		changes["isPrimary"] = map[string]any{"before": before.IsPrimary, "after": after.IsPrimary}
	}
	return changes
}

// recordCustomerAddressAdded is PostCustomersByIdAddresses's own generated
// event (invoice-ready customer design D3): no .NET ancestor. The payload
// always carries addressId, type, label and a one-line display rendering
// (addresses.go's addressDisplay) — the controller ruling's own list of what
// every one of the three address events must never omit.
func recordCustomerAddressAdded(ctx context.Context, q *store.Queries, now time.Time, customerID, addressID int32, addr addressSnapshot, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	display := addressDisplay(addr)
	payload := map[string]any{
		"customerId": customerID, "addressId": addressID,
		"type": addr.Type, "label": addr.Label, "display": display,
	}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.address_added", fmt.Sprintf("Address added: %s", display), payload, 1, actorKind, actorDisplay, actorUserID)
}

// recordCustomerAddressUpdated is PutCustomersByIdAddressesByAddressId's own
// generated event (invoice-ready customer design D3): the controller
// ruling's "one event, not two" case — a PUT that makes this address primary
// and so demotes a different one in the same transaction records only this
// one, naming only the address the request itself was about. type/label/
// display reflect the address's state after the write, as
// recordCustomerContactInfoUpdated's before/after/changes shape does.
func recordCustomerAddressUpdated(ctx context.Context, q *store.Queries, now time.Time, customerID, addressID int32, before, after addressSnapshot, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	display := addressDisplay(after)
	payload := map[string]any{
		"customerId": customerID, "addressId": addressID,
		"type": after.Type, "label": after.Label, "display": display,
		"before": before, "after": after, "changes": addressChanges(before, after),
	}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.address_updated", fmt.Sprintf("Address updated: %s", display), payload, 1, actorKind, actorDisplay, actorUserID)
}

// recordCustomerAddressRemoved is DeleteCustomersByIdAddressesByAddressId's
// own generated event (invoice-ready customer design D3): addr is the
// address's state just before the delete — the only state left to describe,
// since the row is gone once this runs. Promoting a different address of the
// same type to primary in the same transaction (the delete-the-primary
// case) records no event of its own, the same "one event, not two" rule
// recordCustomerAddressUpdated's doc comment describes.
func recordCustomerAddressRemoved(ctx context.Context, q *store.Queries, now time.Time, customerID, addressID int32, addr addressSnapshot, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	display := addressDisplay(addr)
	payload := map[string]any{
		"customerId": customerID, "addressId": addressID,
		"type": addr.Type, "label": addr.Label, "display": display,
	}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.address_removed", fmt.Sprintf("Address removed: %s", display), payload, 1, actorKind, actorDisplay, actorUserID)
}
