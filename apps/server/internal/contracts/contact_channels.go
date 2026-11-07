package contracts

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ContactChannels is what Communications knows of a contact's messaging
// channels, as Customers' contact rows read it (Messenger design N22, delivery
// D): a read-only, in-process port, so that the customer page can show the
// Messenger mark of a contact and offer to write to it without importing
// Communications (barred by depguard) or reading its schema (barred by
// internal/db/schema_test.go). Communications is its sole provider
// (Module.ContactChannels); Compose wires it into every module's Deps before
// any Mount runs. It is nil when Communications is disabled, which a consumer
// reads as "there are no channels" and shows nothing for.
//
// It performs no authorization: it is consumer-gated. The consumer decides who
// may see a contact's channels (Customers asks for communications:conversations-view
// before it calls) and calls with the request's context, never inside a
// transaction of its own.
//
// It never carries the person's page-scoped id: that belongs to a Page, so it
// belongs to Communications and never leaves it. A participant is named by the
// id Communications gave it.
type ContactChannels interface {
	// ForContacts answers, for each contact id, one entry per active
	// Messenger channel: Linked for the channels on which a participant is
	// linked to the contact, and not Linked for those on which none is, so the
	// consumer can offer the contact's Messenger link (a contact cannot be
	// linked before it has written). A contact id is absent from the map when
	// there is no active Messenger channel at all; no ids answer an empty map.
	// Entries of a contact are ordered by the channel's name.
	ForContacts(ctx context.Context, contactIDs []int32) (map[int32][]ContactChannelEntry, error)
}

// ContactChannelEntry is one Messenger channel as a contact's row reads it.
type ContactChannelEntry struct {
	// ChannelID is the channel's id in Communications, and Type its type
	// ("messenger").
	ChannelID uuid.UUID
	Type      string
	// Name is the channel's display name, the Page's name.
	Name string
	// Linked is whether a participant of the channel is linked to the contact;
	// ParticipantID is that participant, nil when not Linked.
	Linked        bool
	ParticipantID *uuid.UUID
	// LastInboundAt is the instant of the participant's newest message,
	// nil when the participant has not written or is not Linked.
	LastInboundAt *time.Time
	// ReplyWindowClosesAt is when the reply window of the participant closes
	// (Messenger design N6), nil when it is closed or there is nothing to count
	// from. The window is the provider's, so Communications answers it:
	// a consumer never counts hours of its own.
	ReplyWindowClosesAt *time.Time
}
