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

// generatedProducerAPI and generatedProducerBrreg are the two producers a
// generated entry can name: what this module's own endpoints did to a
// customer, and what Enhetsregisteret says about it (Brreg in full design
// D4). The distinction is worth a column because a registry.change event
// describes the world changing, not a person acting — the actor on it is
// whoever clicked Refresh (or the system, once delivery B's worker does the
// clicking), which is a different claim from "this user edited the
// customer".
const (
	generatedProducerAPI   = "customers.api"
	generatedProducerBrreg = "customers.brreg"
)

// recordGeneratedEvent inserts one generated timeline entry and its single
// revision, produced by this module's own API (generatedProducerAPI) —
// every event in this file but the two registry ones at the bottom, which
// call recordGeneratedEventFrom with their own producer.
func recordGeneratedEvent(ctx context.Context, q *store.Queries, customerID int32, now time.Time, eventType, summary string, payload any, payloadVersion int32, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	return recordGeneratedEventFrom(ctx, q, customerID, now, generatedProducerAPI, eventType, summary, payload, payloadVersion, actorKind, actorDisplay, actorUserID)
}

// recordGeneratedEventFrom is recordGeneratedEvent for a caller that names
// its own producer. now's UTC calendar date becomes the entry's occurred_on,
// and now itself its occurred_at, created_at and updated_at, as .NET's
// recorder stamps every field from one captured DateTimeOffset.UtcNow
// (SV/CustomerTimelineRecorder.cs:132-133). actorKind/actorDisplay/actorUserID
// are the acting user's resolved actor (server.actorFor, customers
// foundation design D1) — every caller resolves it once, before opening the
// transaction this function runs inside, and threads it down to here rather
// than this function resolving it itself.
func recordGeneratedEventFrom(ctx context.Context, q *store.Queries, customerID int32, now time.Time, producer, eventType, summary string, payload any, payloadVersion int32, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("customers: encode timeline payload: %w", err)
	}
	utc := now.UTC()
	occurredOn := pgtype.Date{Time: time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
	return q.InsertGeneratedTimelineEvent(ctx, store.InsertGeneratedTimelineEventParams{
		CustomerID:     customerID,
		Producer:       producer,
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

// billingProfileChanges is recordCustomerBillingProfileUpdated's changes
// map, shaped like contactInfoChanges: only the fields that actually moved,
// each keyed to its own before/after pair (invoice-ready customer design D4).
func billingProfileChanges(before, after billingProfile) map[string]any {
	changes := map[string]any{}
	if !stringPtrEqual(before.InvoiceEmail, after.InvoiceEmail) {
		changes["invoiceEmail"] = map[string]any{"before": before.InvoiceEmail, "after": after.InvoiceEmail}
	}
	if !stringPtrEqual(before.ReminderEmail, after.ReminderEmail) {
		changes["reminderEmail"] = map[string]any{"before": before.ReminderEmail, "after": after.ReminderEmail}
	}
	if !int32PtrEqual(before.PaymentTermsDays, after.PaymentTermsDays) {
		changes["paymentTermsDays"] = map[string]any{"before": before.PaymentTermsDays, "after": after.PaymentTermsDays}
	}
	if !stringPtrEqual(before.Currency, after.Currency) {
		changes["currency"] = map[string]any{"before": before.Currency, "after": after.Currency}
	}
	if !stringPtrEqual(before.Language, after.Language) {
		changes["language"] = map[string]any{"before": before.Language, "after": after.Language}
	}
	if !stringPtrEqual(before.InvoiceDelivery, after.InvoiceDelivery) {
		changes["invoiceDelivery"] = map[string]any{"before": before.InvoiceDelivery, "after": after.InvoiceDelivery}
	}
	if !stringPtrEqual(before.ReminderDelivery, after.ReminderDelivery) {
		changes["reminderDelivery"] = map[string]any{"before": before.ReminderDelivery, "after": after.ReminderDelivery}
	}
	if !stringPtrEqual(before.PeppolID, after.PeppolID) {
		changes["peppolId"] = map[string]any{"before": before.PeppolID, "after": after.PeppolID}
	}
	if !stringPtrEqual(before.Gln, after.Gln) {
		changes["gln"] = map[string]any{"before": before.Gln, "after": after.Gln}
	}
	if !stringPtrEqual(before.BuyerReference, after.BuyerReference) {
		changes["buyerReference"] = map[string]any{"before": before.BuyerReference, "after": after.BuyerReference}
	}
	return changes
}

// recordCustomerBillingProfileUpdated is PutCustomersByIdBillingProfile's
// own generated event (invoice-ready customer design D1, D4), shaped like
// recordCustomerContactInfoUpdated: no .NET ancestor, since a billing
// profile is new to this port. Only called once the handler has already
// confirmed something changed (billing_profile.go's no-op rule), so changes
// is never empty here.
func recordCustomerBillingProfileUpdated(ctx context.Context, q *store.Queries, now time.Time, customerID int32, before, after billingProfile, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	payload := map[string]any{
		"customerId": customerID,
		"before":     before,
		"after":      after,
		"changes":    billingProfileChanges(before, after),
	}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.billing_profile_updated", "Customer billing profile updated", payload, 1, actorKind, actorDisplay, actorUserID)
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

// addressSummary is "Address added/updated/removed: <display>", truncated
// to customers_timeline_entries.summary's varchar(500) the same way
// truncateUTF16 already protects the manual-entry summary (timeline.go): a
// valid address (line1 and line2 each up to their own 255-character limit,
// plus label/postalCode/city/country) can produce a display well past 500
// characters on its own — untruncated, InsertGeneratedTimelineEvent's insert
// would fail with a database-level "value too long" error on an otherwise
// perfectly valid request. The payload's own "display" field (below) is
// never truncated — only the stored summary column is.
func addressSummary(verb, display string) string {
	return truncateUTF16(fmt.Sprintf("Address %s: %s", verb, display), 500)
}

// peppolLookupSummary is recordCustomerPeppolLookup's fixed-literal summary
// per status (can-this-customer-receive-EHF design D3's controller ruling):
// three constants, never built from interpolated data, so they stay far
// below customers_timeline_entries.summary's 500-unit limit without
// truncateUTF16's help (addressSummary, above, needs it; this never will).
func peppolLookupSummary(status string, canReceiveInvoice bool) string {
	switch {
	case status == peppolStatusRegistered && canReceiveInvoice:
		return "Peppol lookup: can receive EHF invoices"
	case status == peppolStatusRegistered:
		return "Peppol lookup: registered, but not for invoices"
	default:
		return "Peppol lookup: not registered"
	}
}

// recordCustomerPeppolLookup is POST .../peppol-lookup's own generated event
// (can-this-customer-receive-EHF design D3): no .NET ancestor, since Peppol
// lookup is new to this port. Only called once the handler has already
// confirmed the status or either capability changed from the stored answer
// (peppol_lookup.go's own no-op rule — a first lookup counts as changed).
// The payload deliberately omits the participant id: the timeline is
// readable with customers:timeline-view alone, which does not imply
// customers:legal-identity-view, and a derived participant id is that
// permission's to show (server.go's legalIdentityView, peppol_lookup.go's
// own withholding rule).
func recordCustomerPeppolLookup(ctx context.Context, q *store.Queries, now time.Time, customerID int32, status string, canReceiveInvoice, canReceiveCreditNote bool, smpHost, previousStatus *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	payload := map[string]any{
		"status": status, "canReceiveInvoice": canReceiveInvoice, "canReceiveCreditNote": canReceiveCreditNote,
		"smpHost": smpHost, "previousStatus": previousStatus,
	}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.peppol_lookup", peppolLookupSummary(status, canReceiveInvoice), payload, 1, actorKind, actorDisplay, actorUserID)
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
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.address_added", addressSummary("added", display), payload, 1, actorKind, actorDisplay, actorUserID)
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
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.address_updated", addressSummary("updated", display), payload, 1, actorKind, actorDisplay, actorUserID)
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
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.address_removed", addressSummary("removed", display), payload, 1, actorKind, actorDisplay, actorUserID)
}

// registryChangePayload is the payload both registry events carry: the list
// of differences, each with the field's name and whichever of from/to was
// not empty — the same objects the refresh response returns, so a card
// showing "3 changes" and the timeline entry behind it never disagree. The
// keys are omitted rather than sent null for an empty side, the wire
// convention the whole module follows.
//
// The values stay in the payload, readable with timeline-view alone (fix
// round 2, I4's stated decision): the registry record is open data (NLOD 2.0)
// about a public entity, the customer.created event already carries the
// identity snapshot, and the Peppol event's omission of the participant id is
// about a *derived* capability signal rather than a public fact.
func registryChangePayload(changes []registryChange) map[string]any {
	encoded := make([]map[string]any, 0, len(changes))
	for _, c := range changes {
		change := map[string]any{"field": c.Field}
		if c.From != "" {
			change["from"] = c.From
		}
		if c.To != "" {
			change["to"] = c.To
		}
		encoded = append(encoded, change)
	}
	return map[string]any{"changes": encoded}
}

// recordRegistryChange is a refresh's own generated event (Brreg in full
// design D4): the registry.change type, which has existed as a manual entry
// type since the port (timeline.go's manualTimelineEventTypes) and now has a
// producer as well — customers.brreg, since what it describes is the world
// changing rather than this module's API being called. Only called once the
// handler has confirmed something actually differed (registry.go's own rule,
// and diffRegistryRecords's first-fetch rule before it), so changes is never
// empty here.
func recordRegistryChange(ctx context.Context, q *store.Queries, now time.Time, customerID int32, changes []registryChange, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	return recordGeneratedEventFrom(ctx, q, customerID, now, generatedProducerBrreg, "registry.change",
		registryChangeSummary(changes), registryChangePayload(changes), 1, actorKind, actorDisplay, actorUserID)
}

// recordRegistryRemoved is the same event for the one difference a removal
// makes (design D2): the entity is gone from open data, its stored record
// deleted in the same transaction, and this entry is all that is left of it.
// Its summary is a fixed literal, not built from the changes, because there
// is only ever the one change and "Registry record updated: removedFromOpenData"
// would read like a field edit rather than a disappearance.
func recordRegistryRemoved(ctx context.Context, q *store.Queries, now time.Time, customerID int32, changes []registryChange, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	return recordGeneratedEventFrom(ctx, q, customerID, now, generatedProducerBrreg, "registry.change",
		"Registry record removed from open data", registryChangePayload(changes), 1, actorKind, actorDisplay, actorUserID)
}
