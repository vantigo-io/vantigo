package communications

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// PostCommunicationsConversationsByIdMessagesByMessageIdRetry Re-queues a failed outbound message in the conversation so the outbox attempts delivery again (M20).
// (POST /api/v1/communications/conversations/{id}/messages/{messageId}/retry)
func (s *server) PostCommunicationsConversationsByIdMessagesByMessageIdRetry(ctx context.Context, req gen.PostCommunicationsConversationsByIdMessagesByMessageIdRetryRequestObject) (gen.PostCommunicationsConversationsByIdMessagesByMessageIdRetryResponseObject, error) {
	caller, err := callerUserID(ctx)
	if err != nil {
		return nil, err
	}

	q := store.New(s.deps.Pool)
	msg, err := q.GetConversationMessageForRetry(ctx, store.GetConversationMessageForRetryParams{
		ConversationID: req.Id,
		MessageID:      req.MessageId,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCommunicationsConversationsByIdMessagesByMessageIdRetry404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: get message for retry: %w", err)
	}

	if msg.Direction != "outbound" || msg.JobID == nil || msg.JobStatus == nil || *msg.JobStatus != "failed" {
		return gen.PostCommunicationsConversationsByIdMessagesByMessageIdRetry409JSONResponse(flatErrorBody(
			"message_not_failed", "The message is not an outbound message with a failed outbox job.")), nil
	}

	if !msg.ChannelIsActive {
		return gen.PostCommunicationsConversationsByIdMessagesByMessageIdRetry422JSONResponse(flatErrorBody(
			"channel_inactive", "The conversation channel is inactive.")), nil
	}

	if msg.SendAs == "personal" {
		if msg.AuthorUserID == nil || *msg.AuthorUserID != caller {
			var resp gen.PostCommunicationsConversationsByIdMessagesByMessageIdRetry403JSONResponse
			resp.Error.Code = "forbidden"
			resp.Error.Message = "Only the author of a message sent from their own address can retry it."
			return resp, nil
		}
		status, err := personalMailStatus(ctx, s.deps.PersonalMailbox)
		if err != nil {
			return nil, err
		}
		if !status.Available {
			return gen.PostCommunicationsConversationsByIdMessagesByMessageIdRetry422JSONResponse(flatErrorBody(
				"personal_sender_unavailable", "Your personal sender connection is unavailable. Please connect or reconnect Outlook before retrying.")), nil
		}
	}

	now := s.deps.Clock()
	if err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		resetDeliveries, err := txq.ResetFailedMessageDeliveries(ctx, msg.ID)
		if err != nil {
			return fmt.Errorf("communications: reset failed deliveries: %w", err)
		}
		rows, err := txq.RetryFailedOutboxJob(ctx, store.RetryFailedOutboxJobParams{
			ID:  *msg.JobID,
			Now: now,
		})
		if err != nil {
			return fmt.Errorf("communications: retry failed outbox job: %w", err)
		}
		if rows == 0 {
			// Already retried concurrently
			return nil
		}
		for _, deliveryID := range resetDeliveries {
			dID := deliveryID
			if err := txq.InsertMessageEvent(ctx, store.InsertMessageEventParams{
				ID:         uuid.New(),
				MessageID:  msg.ID,
				DeliveryID: &dID,
				EventType:  "queued",
				OccurredAt: now,
			}); err != nil {
				return fmt.Errorf("communications: insert retry delivery event: %w", err)
			}
		}
		if err := txq.InsertMessageEvent(ctx, store.InsertMessageEventParams{
			ID:         uuid.New(),
			MessageID:  msg.ID,
			DeliveryID: nil,
			EventType:  "message_queued",
			OccurredAt: now,
		}); err != nil {
			return fmt.Errorf("communications: insert retry message event: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return gen.PostCommunicationsConversationsByIdMessagesByMessageIdRetry200JSONResponse(gen.ConversationMutationResponse{
		ConversationId: req.Id,
		MessageId:      &req.MessageId,
		Status:         "queued",
	}), nil
}
