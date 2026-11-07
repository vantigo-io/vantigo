package communications

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// contactChannels is this module's contracts.ContactChannels (Messenger design
// N22): for each contact, one entry per active Messenger channel, linked when a
// participant of the channel is linked to the contact. The customer page reads it
// to show the Messenger mark of a contact and to offer to write to it. It carries
// neither the participant's address (the page-scoped id) nor anything else of the
// person.
type contactChannels struct {
	deps module.Deps
	pool *pgxpool.Pool
}

var _ contracts.ContactChannels = (*contactChannels)(nil)

// newContactChannels is Module's ContactChannels.
func newContactChannels(d module.Deps) contracts.ContactChannels {
	return &contactChannels{deps: d, pool: d.Pool}
}

// ForContacts answers the entries of the given contacts, see the contract.
func (c *contactChannels) ForContacts(ctx context.Context, contactIDs []int32) (map[int32][]contracts.ContactChannelEntry, error) {
	out := map[int32][]contracts.ContactChannelEntry{}
	if len(contactIDs) == 0 {
		return out, nil
	}
	q := store.New(c.pool)
	channels, err := q.ListActiveMessengerChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("communications: list the messenger channels: %w", err)
	}
	if len(channels) == 0 {
		return out, nil
	}
	linked, err := q.ListMessengerParticipantsByContacts(ctx, contactIDs)
	if err != nil {
		return nil, fmt.Errorf("communications: list the contacts' participants: %w", err)
	}

	// The newest-spoken participant of each (contact, channel): two people may
	// have been linked to one contact on a Page, and the one who wrote last is
	// the one to write to.
	type key struct {
		contact int32
		channel string
	}
	type linkedParticipant struct {
		id   uuid.UUID
		last *time.Time
	}
	best := map[key]linkedParticipant{}
	for _, r := range linked {
		if r.ContactID == nil {
			continue
		}
		// max() of no row is NULL, which the driver hands over as nil.
		var last *time.Time
		if at, ok := r.LastInboundAt.(time.Time); ok {
			u := at.UTC()
			last = &u
		}
		k := key{*r.ContactID, r.ChannelID.String()}
		cur, ok := best[k]
		if !ok || (last != nil && (cur.last == nil || last.After(*cur.last))) {
			best[k] = linkedParticipant{id: r.ParticipantID, last: last}
		}
	}

	now := c.deps.Clock()
	caps := map[string]contracts.MessengerCapabilities{}
	for _, ch := range channels {
		if _, ok := caps[ch.Provider]; ok {
			continue
		}
		if m := channelMessengerFor(c.deps, ch.Provider); m != nil {
			caps[ch.Provider] = m.Capabilities()
		} else {
			caps[ch.Provider] = contracts.MessengerCapabilities{}
		}
	}

	for _, id := range contactIDs {
		if _, done := out[id]; done {
			continue
		}
		entries := make([]contracts.ContactChannelEntry, 0, len(channels))
		for _, ch := range channels {
			name := ch.Address
			if ch.DisplayName != nil && *ch.DisplayName != "" {
				name = *ch.DisplayName
			}
			entry := contracts.ContactChannelEntry{ChannelID: ch.ID, Type: channelTypeMessenger, Name: name}
			if r, ok := best[key{id, ch.ID.String()}]; ok {
				participant := r.id
				entry.Linked, entry.ParticipantID, entry.LastInboundAt = true, &participant, r.last
				w := replyWindowOf(now, r.last, caps[ch.Provider])
				if w.state != windowClosed {
					closes := w.closesAt.UTC()
					entry.ReplyWindowClosesAt = &closes
				}
			}
			entries = append(entries, entry)
		}
		out[id] = entries
	}
	return out, nil
}
