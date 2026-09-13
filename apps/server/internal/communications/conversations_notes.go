package communications

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
)

// PostCommunicationsConversationsByIdNotes Add an internal note
// (POST /api/v1/communications/conversations/{id}/notes)
//
// AddNote (inventory §1.2, §2, task 5 dispatch point 5): (1) ValidateNote ->
// 400; (2) conversation lookup -> 404 bare, after validation (validation
// wins over existence — the same ordering CreateConversation and StageAttachment
// use). The status string in the response is "created", not "queued" —
// AddNote is the one message-producing endpoint that does not go through
// the outbox at all (no delivery to queue for an internal note), and its
// own literal confirms it: `new ConversationMutationResponse(id, message.Id, "created", null)`.
func (s *server) PostCommunicationsConversationsByIdNotes(ctx context.Context, req gen.PostCommunicationsConversationsByIdNotesRequestObject) (gen.PostCommunicationsConversationsByIdNotesResponseObject, error) {
	caller, err := callerUserID(ctx)
	if err != nil {
		return nil, err
	}
	body := gen.NoteRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	if errs := validateNote(body); len(errs) > 0 {
		return gen.PostCommunicationsConversationsByIdNotes400JSONResponse(validationErrorBody(errs)), nil
	}

	now := s.deps.Clock()
	preview := preview(*body.TextBody)
	messageID := uuid.New()

	q := store.New(s.deps.Pool)
	if _, err := q.UpdateConversationActivity(ctx, store.UpdateConversationActivityParams{
		LastActivityAt: now, PreviewText: preview, ID: req.Id,
	}); errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCommunicationsConversationsByIdNotes404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("communications: update conversation activity: %w", err)
	}

	if _, err := q.InsertConversationMessage(ctx, store.InsertConversationMessageParams{
		ID: messageID, ConversationID: req.Id, Direction: "internal_note", AuthorUserID: &caller,
		TextBody: body.TextBody, OccurredAt: now, CreatedAt: now,
	}); err != nil {
		return nil, fmt.Errorf("communications: insert note message: %w", err)
	}

	return gen.PostCommunicationsConversationsByIdNotes201JSONResponse(gen.ConversationMutationResponse{
		ConversationId: req.Id, MessageId: &messageID, Status: "created", IdempotencyKey: nil,
	}), nil
}
