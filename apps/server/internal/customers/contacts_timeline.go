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

// recordContactEvent is AddContact (SV/CustomerTimelineRecorder.cs:99-124),
// widened by typed contact roles design D4: the payload gains `title` (the
// same value `role` carries, under the name the field now has) and `roles`,
// and `role` stays exactly where it was because the recorded corpus and every
// timeline entry already written speak it.
func recordContactEvent(ctx context.Context, q *store.Queries, now time.Time, customerID int32, eventType, action string, contact store.CustomersContact, title *string, roles []contactRole, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	displayName := contactDisplayName(contact.FirstName, contact.MiddleName, contact.LastName)
	summary := fmt.Sprintf("%s: %s (#%d)", action, displayName, contact.ID)
	// Never nil: a payload that says "roles": null cannot be told apart from
	// one written before roles existed, while "roles": [] says the association
	// holds none, which is a fact.
	if roles == nil {
		roles = []contactRole{}
	}
	payload := map[string]any{
		"customerId":  customerID,
		"contactId":   contact.ID,
		"displayName": displayName,
		"firstName":   contact.FirstName,
		"middleName":  contact.MiddleName,
		"lastName":    contact.LastName,
		"role":        deref(title),
		"title":       title,
		"roles":       roles,
		"phone":       phone,
		"email":       email,
	}
	return recordGeneratedEvent(ctx, q, customerID, now, eventType, truncateUTF16(summary, 500), payload, 1, actorKind, actorDisplay, actorUserID)
}

// recordContactAttached is RecordContactAttached (SV/CustomerTimelineRecorder.cs:87-88).
func recordContactAttached(ctx context.Context, q *store.Queries, now time.Time, customerID int32, contact store.CustomersContact, title *string, roles []contactRole, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	return recordContactEvent(ctx, q, now, customerID, "customer.contact_attached", "Contact linked", contact, title, roles, phone, email, actorKind, actorDisplay, actorUserID)
}

// recordContactRelationshipUpdated is RecordContactRelationshipUpdated
// (SV/CustomerTimelineRecorder.cs:90-91): only called when the title, the
// phone, the email, the role set or a primary flag actually changed (design
// D4), and its action says which of those it was — see
// relationshipUpdateAction.
func recordContactRelationshipUpdated(ctx context.Context, q *store.Queries, now time.Time, customerID int32, contact store.CustomersContact, action string, title *string, roles []contactRole, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	return recordContactEvent(ctx, q, now, customerID, "customer.contact_relationship_updated", action, contact, title, roles, phone, email, actorKind, actorDisplay, actorUserID)
}

// recordContactDetached is RecordContactDetached (SV/CustomerTimelineRecorder.cs:93-94).
func recordContactDetached(ctx context.Context, q *store.Queries, now time.Time, customerID int32, contact store.CustomersContact, title *string, roles []contactRole, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	return recordContactEvent(ctx, q, now, customerID, "customer.contact_detached", "Contact unlinked", contact, title, roles, phone, email, actorKind, actorDisplay, actorUserID)
}

// recordContactRemoved is RecordContactRemoved (SV/CustomerTimelineRecorder.cs:96-97),
// called once per association DeleteContact cascades over, before the contact
// row itself is deleted.
func recordContactRemoved(ctx context.Context, q *store.Queries, now time.Time, customerID int32, contact store.CustomersContact, title *string, roles []contactRole, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	return recordContactEvent(ctx, q, now, customerID, "customer.contact_removed", "Contact removed", contact, title, roles, phone, email, actorKind, actorDisplay, actorUserID)
}

// relationshipUpdateActionDefault is what an update that moved only the title,
// the phone or the email has always said.
const relationshipUpdateActionDefault = "Contact relationship updated"

// relationshipUpdateAction is design D4's "its summary names what changed".
// Becoming a role's primary is the one change worth saying out loud on a
// timeline — it is the answer to "who gets the invoice" moving — so it wins
// over the plainer wordings, and a write that made this contact primary for
// more than one role names the first in the design's fixed order rather than
// listing them: the summary is a varchar(500) one-liner in a feed, and the
// payload carries the whole set for anyone who needs it.
func relationshipUpdateAction(before, after []contactRole) string {
	wasPrimary := make(map[string]bool, len(before))
	for _, r := range before {
		wasPrimary[r.Role] = r.Primary
	}
	for _, r := range after { // after is in contactRoleOrder
		if r.Primary && !wasPrimary[r.Role] {
			return fmt.Sprintf("Now the primary %s contact", contactRoleSummaryLabel(r.Role))
		}
	}
	if rolesChanged(before, after) {
		return "Roles updated"
	}
	return relationshipUpdateActionDefault
}

// contactRoleSummaryLabel is a role inside an English sentence, which is not
// the same as the code: "decision_maker" reads as a column name in a feed.
// Only the summary uses it — the payload and the API always carry the code —
// and the frontend never reads it, because the frontend has its own catalogs.
func contactRoleSummaryLabel(role string) string {
	if role == contactRoleDecisionMaker {
		return "decision-maker"
	}
	return role
}

// recordContactPromoted is design D4's promotion event: a contact that became
// a role's primary because SOMEBODY ELSE gave it up, was detached, or was
// deleted. It is a customer.contact_relationship_updated — the relationship
// did change, and inventing a type for it would be a type no timeline filter
// knows — recorded against the promoted contact, with the acting user who
// caused it rather than a system actor: a person did this, indirectly, and the
// timeline's job is to say who.
func recordContactPromoted(ctx context.Context, q *store.Queries, now time.Time, customerID int32, contact store.CustomersContact, role string, title *string, roles []contactRole, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	action := fmt.Sprintf("Now the primary %s contact", contactRoleSummaryLabel(role))
	return recordContactEvent(ctx, q, now, customerID, "customer.contact_relationship_updated", action, contact, title, roles, phone, email, actorKind, actorDisplay, actorUserID)
}
