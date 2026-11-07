package communications

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is POST /conversations for a Messenger participant (Messenger design
// N22): the one way the Page writes first, from a customer's contact. It replaces
// N7's blanket channel_cannot_start for a request that names a participantId.
//
// A participant whose reply window is open starts or reuses the conversation:
// the newest open one of the channel and participant, else a new one with no
// subject. A closed window answers 422 reply_window_closed. The first message
// then rides exactly the path a reply does (PostCommunicationsConversationsByIdReply),
// so the text limit, the window check at send time and the outbox are one code,
// and the Idempotency-Key is the reply's.

// createMessengerConversation is PostCommunicationsConversations for a request
// with participantId. The e-mail inputs (subject, recipients, to, cc, htmlBody,
// sendFromPersonal) are not used: a chat has none.
func (s *server) createMessengerConversation(ctx context.Context, key string, body gen.CreateConversationRequest) (gen.PostCommunicationsConversationsResponseObject, error) {
	reply := gen.ReplyRequest{TextBody: body.TextBody}
	if errs := validateReply(reply, false); len(errs) > 0 {
		return gen.PostCommunicationsConversations400JSONResponse(validationErrorBody(errs)), nil
	}
	if !validIdempotencyKey(key) {
		return gen.PostCommunicationsConversations400JSONResponse(flatErrorBody(
			"idempotency_key_required", "A valid Idempotency-Key header is required.")), nil
	}

	q := store.New(s.deps.Pool)
	participant, err := q.GetParticipantWithChannel(ctx, *body.ParticipantId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCommunicationsConversations422JSONResponse(flatErrorBody(
			"participant_invalid", "The selected participant does not exist.")), nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get participant: %w", err)
	}
	if participant.ChannelType != channelTypeMessenger {
		return gen.PostCommunicationsConversations422JSONResponse(flatErrorBody(
			"not_supported_on_channel", "A participant can only be written to first on a messenger channel.")), nil
	}
	if body.ChannelId != nil && *body.ChannelId != participant.ChannelID {
		return gen.PostCommunicationsConversations422JSONResponse(flatErrorBody(
			"participant_invalid", "The participant is not one of the channel's.")), nil
	}
	if !participant.ChannelIsActive {
		return gen.PostCommunicationsConversations422JSONResponse(flatErrorBody(
			"channel_invalid", "The selected channel does not exist or is inactive.")), nil
	}
	if body.CustomerId != nil {
		entry, derr := s.deps.Directory.Customer(ctx, *body.CustomerId)
		if derr != nil {
			return nil, fmt.Errorf("communications: look up customer: %w", derr)
		}
		if entry == nil {
			return gen.PostCommunicationsConversations422JSONResponse(flatErrorBody(
				"customer_invalid", "The selected customer does not exist.")), nil
		}
	}

	// An accepted request is answered again as the reply that carried it was
	// (its fingerprint is the reply's, over the conversation it made).
	if existing, err := q.GetIdempotencyRecordByKey(ctx, key); err == nil {
		fingerprint, ferr := replyFingerprint(existing.ConversationID, reply)
		if ferr != nil {
			return nil, ferr
		}
		if existing.PayloadFingerprint == fingerprint {
			return gen.PostCommunicationsConversations200JSONResponse(gen.ConversationMutationResponse{
				ConversationId: existing.ConversationID, MessageId: &existing.MessageID, Status: "queued", IdempotencyKey: &key,
			}), nil
		}
		return gen.PostCommunicationsConversations409JSONResponse(flatErrorBody(
			"idempotency_key_reused", "The Idempotency-Key was already used with a different payload.")), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("communications: get idempotency record: %w", err)
	}

	// The window counts from the participant's newest message of any of its
	// conversations: a new conversation has none of its own yet (N6, N22).
	now := s.deps.Clock()
	provider, err := s.channelMessenger(ctx, participant.ChannelProvider)
	if err != nil {
		return nil, err
	}
	if provider == nil {
		return gen.PostCommunicationsConversations422JSONResponse(flatErrorBody(
			"channel_invalid", "The selected channel does not exist or is inactive.")), nil
	}
	caps, err := messengerCapabilities(ctx, provider)
	if err != nil {
		return nil, err
	}
	last, err := q.ParticipantLatestInbound(ctx, &participant.ID)
	var lastInbound = &last
	if errors.Is(err, pgx.ErrNoRows) {
		lastInbound = nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: read the participant's newest message: %w", err)
	}
	if replyWindowOf(now, lastInbound, caps).state == windowClosed {
		return gen.PostCommunicationsConversations422JSONResponse(flatErrorBody(
			"reply_window_closed", "The customer must write again before you can answer here.")), nil
	}

	conversationID, created, err := s.openMessengerConversation(ctx, participant.ChannelID, participant.ID, body.CustomerId, now)
	if err != nil {
		return nil, err
	}

	resp, err := s.PostCommunicationsConversationsByIdReply(ctx, gen.PostCommunicationsConversationsByIdReplyRequestObject{
		Id: conversationID, Params: gen.PostCommunicationsConversationsByIdReplyParams{IdempotencyKey: key}, Body: &reply,
	})
	if err == nil {
		switch r := resp.(type) {
		case gen.PostCommunicationsConversationsByIdReply200JSONResponse:
			return gen.PostCommunicationsConversations200JSONResponse(r), nil
		case gen.PostCommunicationsConversationsByIdReply201JSONResponse:
			return gen.PostCommunicationsConversations201JSONResponse(r), nil
		}
	}

	// The message was not accepted: a conversation made for it is taken back.
	if created {
		s.discardEmptyConversation(ctx, conversationID)
	}
	if err != nil {
		return nil, err
	}
	switch r := resp.(type) {
	case gen.PostCommunicationsConversationsByIdReply400JSONResponse:
		return gen.PostCommunicationsConversations400JSONResponse(r), nil
	case gen.PostCommunicationsConversationsByIdReply409JSONResponse:
		return gen.PostCommunicationsConversations409JSONResponse(r), nil
	case gen.PostCommunicationsConversationsByIdReply422JSONResponse:
		return gen.PostCommunicationsConversations422JSONResponse(r), nil
	}
	return nil, fmt.Errorf("communications: the first message of a conversation was refused as %T", resp)
}

// openMessengerConversation is the newest open conversation of the channel with
// the participant, locked, else a new one with no subject (N3a), made in the same
// transaction. created says which.
func (s *server) openMessengerConversation(ctx context.Context, channelID, participantID uuid.UUID, customerID *int32, now time.Time) (uuid.UUID, bool, error) {
	var conversationID uuid.UUID
	created := false
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		open, err := txq.FindOpenMessengerConversation(ctx, store.FindOpenMessengerConversationParams{
			ChannelID: channelID, ParticipantID: participantID,
		})
		switch {
		case err == nil:
			conversationID = open.ID
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		conversationID, created = uuid.New(), true
		var source *string
		if customerID != nil {
			manual := "manual"
			source = &manual
		}
		if _, err := txq.InsertConversation(ctx, store.InsertConversationParams{
			ID: conversationID, ChannelID: channelID, CustomerID: customerID, CustomerAssociationSource: source,
			LastActivityAt: now, CreatedAt: now,
		}); err != nil {
			return err
		}
		return txq.InsertConversationParticipant(ctx, store.InsertConversationParticipantParams{
			ConversationID: conversationID, ParticipantID: participantID,
		})
	})
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("communications: open the conversation: %w", err)
	}
	return conversationID, created, nil
}

// discardEmptyConversation takes back a conversation that never got a message.
// A failure is only logged: an empty conversation is harmless, and the retention
// sweep takes it with its participant links.
func (s *server) discardEmptyConversation(ctx context.Context, id uuid.UUID) {
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.DeleteConversationParticipantLinks(ctx, id); err != nil {
			return err
		}
		return txq.DeleteConversationWithoutMessages(ctx, id)
	})
	if err != nil && s.deps.Logger != nil {
		s.deps.Logger.Warn("communications: a conversation started for a message that was refused could not be taken back",
			"conversation", id, "error", err)
	}
}
