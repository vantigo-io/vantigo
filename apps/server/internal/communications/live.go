package communications

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The live topics (live design L17) and their events. Events carry
// identifiers only: a screen that hears one refetches.
const (
	// liveInbox is the conversation list's topic: conversation.changed with
	// the conversation's id, or without one after a bulk change.
	liveInbox = "communications.inbox"
	// liveConversation is one conversation's topic, keyed by its id:
	// conversation.changed (status, assignment, tags, links), message.added
	// and message.changed (a delivery's outcome), each with the id of what
	// changed. Presence is allowed on it.
	liveConversation = "communications.conversation"

	// permConversationsView is what the list and a conversation's GET need.
	permConversationsView = "communications:conversations-view"

	eventConversationChanged = "conversation.changed"
	eventMessageAdded        = "message.added"
	eventMessageChanged      = "message.changed"
)

// liveTopics is the module's LiveTopics. The inbox is authorised by
// conversations-view, as the list is; a conversation by the rule GET
// /communications/conversations/{id} applies — conversations-view, and the
// conversation exists. A conversation that does not exist and one the person
// may not see are both ErrForbidden, so a subscription is no existence oracle.
func liveTopics(d module.Deps) []live.Topic {
	return []live.Topic{
		{Kind: liveInbox, Authorize: func(ctx context.Context, key string) error {
			if key != "" || !live.HasPermission(ctx, permConversationsView) {
				return live.ErrForbidden
			}
			return nil
		}},
		{Kind: liveConversation, Presence: true, Authorize: func(ctx context.Context, key string) error {
			id, err := uuid.Parse(key)
			if err != nil || !live.HasPermission(ctx, permConversationsView) {
				return live.ErrForbidden
			}
			if _, err := store.New(d.Pool).GetConversationByID(ctx, id); errors.Is(err, pgx.ErrNoRows) {
				return live.ErrForbidden
			} else if err != nil {
				return fmt.Errorf("communications: authorise a conversation's topic: %w", err)
			}
			return nil
		}},
	}
}

// notifyConversation tells the inbox that conversationID changed, and the
// conversation's own topic that kind happened to id — the message, for a
// message event, or "" for several; the conversation, for
// conversation.changed. It runs on q, the transaction that wrote (live design
// L6, L18), so it is heard only if that commits.
func notifyConversation(ctx context.Context, q live.Execer, conversationID uuid.UUID, kind, id string) error {
	if err := live.Notify(ctx, q, liveInbox, eventConversationChanged, conversationID.String()); err != nil {
		return err
	}
	return live.Notify(ctx, q, liveConversation+":"+conversationID.String(), kind, id)
}

// notifyConversationChanged is notifyConversation for a change to the
// conversation itself.
func notifyConversationChanged(ctx context.Context, q live.Execer, conversationID uuid.UUID) error {
	return notifyConversation(ctx, q, conversationID, eventConversationChanged, conversationID.String())
}

// notifyMessage is notifyConversation for one message: message.added or
// message.changed.
func notifyMessage(ctx context.Context, q live.Execer, conversationID uuid.UUID, kind string, messageID uuid.UUID) error {
	return notifyConversation(ctx, q, conversationID, kind, messageID.String())
}

// notifyInbox tells the inbox that conversations changed in bulk: one event
// without an id, never one per row (L6). A conversation open on a screen is
// refetched by the list's own refresh.
func notifyInbox(ctx context.Context, q live.Execer) error {
	return live.Notify(ctx, q, liveInbox, eventConversationChanged, "")
}

// notifyOnPool is notifyConversationChanged after a single write made on the
// pool, outside a transaction (live design L6). A failure to send is logged,
// not answered: the write is made, and the screens' fallback poll repairs a
// lost event.
func (s *server) notifyOnPool(ctx context.Context, conversationID uuid.UUID, what string) {
	if err := notifyConversationChanged(ctx, s.deps.Pool, conversationID); err != nil {
		s.deps.Logger.WarnContext(ctx, "communications: notify "+what, "error", err)
	}
}

// notifyMessageChanged is notifyMessage for a write that names only the
// message — the outbox's delivery outcomes — on its transaction: message.changed,
// in the conversation the message is in. A message that is gone notifies
// nothing.
func notifyMessageChanged(ctx context.Context, tx pgx.Tx, messageID uuid.UUID) error {
	conversationID, err := store.New(tx).GetMessageConversationID(ctx, messageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("communications: read a message's conversation: %w", err)
	}
	return notifyMessage(ctx, tx, conversationID, eventMessageChanged, messageID)
}
