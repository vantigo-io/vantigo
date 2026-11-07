package customers

import (
	"context"
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
)

// conversationsView is Communications' key that the Messenger marks of a
// contact's row are shaped by (Messenger design N22): what Communications knows
// of a person's chat is read with it, so a caller without it sees the contacts
// as they were.
const conversationsView = "communications:conversations-view"

// The permissions the actions of the Messenger menu need in Communications:
// writing to the person, and getting a contact's link or unlinking. The page
// hides what the caller lacks (Communications refuses it all the same).
const (
	conversationsReply  = "communications:conversations-reply"
	conversationsManage = "communications:conversations-manage"
)

// contactChannelsOf is the Messenger channels of the contacts of a customer's
// list (contracts.ContactChannels, Messenger design N22), by contact id: one
// entry per active Messenger channel, linked or not, so that the row can offer a
// contact its Messenger link before it has written. The contract performs no
// authorization, so this is where it is decided: a caller who may not view
// conversations, a deployment without Communications (Deps.ContactChannels nil)
// and a customer with no contact all leave the map empty and the page as it was.
// It is read after the list's transaction closed, never inside it.
func (s *server) contactChannelsOf(ctx context.Context, rows []store.ListContactAssociationsForCustomerRow) (map[int32]*[]gen.CustomerContactChannel, error) {
	if s.deps.ContactChannels == nil || len(rows) == 0 || !s.hasPermission(ctx, conversationsView) {
		return nil, nil
	}
	ids := make([]int32, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	entries, err := s.deps.ContactChannels.ForContacts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("customers: read the contacts' channels: %w", err)
	}
	canWrite, canManage := s.hasPermission(ctx, conversationsReply), s.hasPermission(ctx, conversationsManage)
	out := make(map[int32]*[]gen.CustomerContactChannel, len(entries))
	for id, list := range entries {
		if len(list) == 0 {
			continue
		}
		channels := make([]gen.CustomerContactChannel, 0, len(list))
		for _, e := range list {
			c := gen.CustomerContactChannel{
				ChannelId: e.ChannelID, Type: e.Type, Name: e.Name, Linked: e.Linked, CanWrite: canWrite, CanManage: canManage,
				ParticipantId: e.ParticipantID, LastInboundAt: e.LastInboundAt, ReplyWindowClosesAt: e.ReplyWindowClosesAt,
			}
			channels = append(channels, c)
		}
		out[id] = &channels
	}
	return out, nil
}
