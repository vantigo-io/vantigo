package communications

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	platformmail "github.com/vantigo-io/vantigo/server/internal/mail"
)

// threadWindow is how far back Graph's conversation threads a message (M15):
// an inbound message of the same Outlook conversation received in the last
// 90 days.
const threadWindow = 90 * 24 * time.Hour

// findThread is threading (M15; delivery D, reading 18), read before the
// import's transaction and always within the channel: the conversation of
// the message In-Reply-To names — a message of any direction, so a reply to
// one we sent threads onto its conversation —, else of the newest message
// the References name, newest reference first, else of the newest inbound
// message in Graph's conversation received in the last 90 days. uuid.Nil
// is a new conversation. Several messages with one id answer the newest's.
func findThread(ctx context.Context, q *store.Queries, channelID uuid.UUID, in platformmail.Inbound,
	item contracts.MailItem, now time.Time) (uuid.UUID, error) {
	ids := make([]string, 0, 1+len(in.References))
	if in.InReplyTo != "" {
		ids = append(ids, in.InReplyTo)
	}
	for i := len(in.References) - 1; i >= 0; i-- {
		ids = append(ids, in.References[i])
	}
	usable := ids[:0:0]
	for _, id := range ids {
		if rfcOf(id) != nil {
			usable = append(usable, id)
		}
	}
	if len(usable) > 0 {
		rows, err := q.FindConversationsByRfcMessageIDs(ctx, store.FindConversationsByRfcMessageIDsParams{
			ChannelID: channelID, Ids: usable,
		})
		if err != nil {
			return uuid.Nil, fmt.Errorf("communications: find the thread by Message-Id: %w", err)
		}
		newest := make(map[string]uuid.UUID, len(rows))
		for _, r := range rows {
			if _, ok := newest[r.RfcMessageID]; !ok {
				newest[r.RfcMessageID] = r.ConversationID
			}
		}
		for _, id := range usable {
			if conversation, ok := newest[id]; ok {
				return conversation, nil
			}
		}
	}

	if item.ConversationID == "" {
		return uuid.Nil, nil
	}
	conversation, err := q.FindConversationByGraphConversation(ctx, store.FindConversationByGraphConversationParams{
		ChannelID: channelID, GraphConversationID: item.ConversationID, Since: now.Add(-threadWindow),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("communications: find the thread by Graph's conversation: %w", err)
	}
	return conversation, nil
}
