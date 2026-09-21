package customers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/customers/store"
)

// This file is CustomerTimelineRecorder's contact-association half
// (SV/CustomerTimelineRecorder.cs:87-124): the four generated events Attach,
// Update and Detach on a customer-contact association, and DeleteContact's
// cascade, record. It shares recordGeneratedEvent with timeline_events.go's
// customer-scoped events (create/update/status-changed); nothing here is
// Task 9's timeline-read/manual-entry scope, only the write path this
// module's own association operations need.

// contactDisplayName joins first, middle and last name with spaces, skipping
// a blank middle name (CustomerTimelineRecorder.cs:101-104).
func contactDisplayName(firstName string, middleName *string, lastName string) string {
	parts := make([]string, 0, 3)
	parts = append(parts, firstName)
	if middleName != nil && strings.TrimSpace(*middleName) != "" {
		parts = append(parts, *middleName)
	}
	parts = append(parts, lastName)
	return strings.Join(parts, " ")
}

// recordContactEvent is AddContact (SV/CustomerTimelineRecorder.cs:99-124):
// the shared shape of every contact-association generated event — summary
// "{action}: {displayName} (#{contactId})" and a payload of the contact's
// name parts plus the association's role/phone/email.
func recordContactEvent(ctx context.Context, q *store.Queries, now time.Time, customerID int32, eventType, action string, contact store.CustomersContact, role string, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	displayName := contactDisplayName(contact.FirstName, contact.MiddleName, contact.LastName)
	summary := fmt.Sprintf("%s: %s (#%d)", action, displayName, contact.ID)
	payload := map[string]any{
		"customerId":  customerID,
		"contactId":   contact.ID,
		"displayName": displayName,
		"firstName":   contact.FirstName,
		"middleName":  contact.MiddleName,
		"lastName":    contact.LastName,
		"role":        role,
		"phone":       phone,
		"email":       email,
	}
	return recordGeneratedEvent(ctx, q, customerID, now, eventType, summary, payload, 1, actorKind, actorDisplay, actorUserID)
}

// recordContactAttached is RecordContactAttached (SV/CustomerTimelineRecorder.cs:87-88).
func recordContactAttached(ctx context.Context, q *store.Queries, now time.Time, customerID int32, contact store.CustomersContact, role string, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	return recordContactEvent(ctx, q, now, customerID, "customer.contact_attached", "Contact linked", contact, role, phone, email, actorKind, actorDisplay, actorUserID)
}

// recordContactRelationshipUpdated is RecordContactRelationshipUpdated
// (SV/CustomerTimelineRecorder.cs:90-91): only called when role, phone or
// email actually changed.
func recordContactRelationshipUpdated(ctx context.Context, q *store.Queries, now time.Time, customerID int32, contact store.CustomersContact, role string, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	return recordContactEvent(ctx, q, now, customerID, "customer.contact_relationship_updated", "Contact relationship updated", contact, role, phone, email, actorKind, actorDisplay, actorUserID)
}

// recordContactDetached is RecordContactDetached (SV/CustomerTimelineRecorder.cs:93-94).
func recordContactDetached(ctx context.Context, q *store.Queries, now time.Time, customerID int32, contact store.CustomersContact, role string, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	return recordContactEvent(ctx, q, now, customerID, "customer.contact_detached", "Contact unlinked", contact, role, phone, email, actorKind, actorDisplay, actorUserID)
}

// recordContactRemoved is RecordContactRemoved (SV/CustomerTimelineRecorder.cs:96-97),
// called once per association DeleteContact cascades over, before the
// contact row itself is deleted.
func recordContactRemoved(ctx context.Context, q *store.Queries, now time.Time, customerID int32, contact store.CustomersContact, role string, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	return recordContactEvent(ctx, q, now, customerID, "customer.contact_removed", "Contact removed", contact, role, phone, email, actorKind, actorDisplay, actorUserID)
}
