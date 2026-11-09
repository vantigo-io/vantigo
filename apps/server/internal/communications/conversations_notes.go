package communications

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/events"
)

// maxMentions is how many users one note may mention.
const maxMentions = 10

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
//
// A note may mention users picked with the @ picker (virtual employees design
// V32): their ids are stored with the note and published as one event in the
// note's transaction, and a person mentioned is mailed once it is committed. A
// note written through an agent grant mentions no one, so that the agents
// cannot wake each other.
func (s *server) PostCommunicationsConversationsByIdNotes(ctx context.Context, req gen.PostCommunicationsConversationsByIdNotesRequestObject) (gen.PostCommunicationsConversationsByIdNotesResponseObject, error) {
	caller, err := callerUserID(ctx)
	if err != nil {
		return nil, err
	}
	body := gen.NoteRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	errs := validateNote(body)
	byAgent := false
	if p, ok := contracts.PrincipalFrom(ctx); ok && p.Via != nil && p.Via.Kind == contracts.ViaAgent {
		byAgent = true
	}
	var mentioned []contracts.UserEntry
	if len(errs) == 0 && !byAgent {
		var merr error
		if mentioned, merr = s.resolveMentions(ctx, body.Mentions, caller, errs); merr != nil {
			return nil, merr
		}
	}
	if len(errs) > 0 {
		return gen.PostCommunicationsConversationsByIdNotes400JSONResponse(validationErrorBody(errs)), nil
	}

	now := s.deps.Clock()
	preview := preview(*body.TextBody)
	messageID := uuid.New()

	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := txq.UpdateConversationActivity(ctx, store.UpdateConversationActivityParams{
			LastActivityAt: now, PreviewText: preview, ID: req.Id,
		}); err != nil {
			return err
		}
		if _, err := txq.InsertConversationMessage(ctx, store.InsertConversationMessageParams{
			ID: messageID, ConversationID: req.Id, Direction: "internal_note", AuthorUserID: &caller,
			TextBody: body.TextBody, OccurredAt: now, CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("communications: insert note message: %w", err)
		}
		if err := notifyMessage(ctx, tx, req.Id, eventMessageAdded, messageID); err != nil {
			return err
		}
		if len(mentioned) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, len(mentioned))
		for i, u := range mentioned {
			ids[i] = u.ID
			if err := txq.InsertNoteMention(ctx, store.InsertNoteMentionParams{MessageID: messageID, UserID: u.ID}); err != nil {
				return fmt.Errorf("communications: store a mention: %w", err)
			}
		}
		channelID, err := txq.GetConversationChannel(ctx, req.Id)
		if err != nil {
			return err
		}
		return events.Publish(ctx, tx, moduleName, contracts.ConversationNoteMentioned{
			ConversationID: req.Id, MessageID: messageID, ChannelID: channelID, ByUserID: caller, MentionedUserIDs: ids,
		}, now)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCommunicationsConversationsByIdNotes404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("communications: add a note: %w", err)
	}
	s.notifyMentioned(ctx, caller, req.Id, mentioned)

	return gen.PostCommunicationsConversationsByIdNotes201JSONResponse(gen.ConversationMutationResponse{
		ConversationId: req.Id, MessageId: &messageID, Status: "created", IdempotencyKey: nil,
	}), nil
}

// resolveMentions is the users a note mentions: each id once, the author left
// out, every one a known, active user. An id that is not is a validation error
// on mentions.
func (s *server) resolveMentions(ctx context.Context, raw *[]uuid.UUID, author uuid.UUID, errs map[string][]string) ([]contracts.UserEntry, error) {
	if raw == nil || len(*raw) == 0 {
		return nil, nil
	}
	var ids []uuid.UUID
	for _, id := range *raw {
		if id != author && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) > maxMentions {
		errs["mentions"] = []string{fmt.Sprintf("At most %d users.", maxMentions)}
		return nil, nil
	}
	if len(ids) == 0 {
		return nil, nil
	}
	found, err := s.deps.Users.Users(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("communications: look up the mentioned users: %w", err)
	}
	byID := make(map[uuid.UUID]contracts.UserEntry, len(found))
	for _, u := range found {
		byID[u.ID] = u
	}
	out := make([]contracts.UserEntry, 0, len(ids))
	for _, id := range ids {
		u, ok := byID[id]
		if !ok || !u.Active {
			errs["mentions"] = []string{"Every mentioned user must be a known, active user."}
			return nil, nil
		}
		out = append(out, u)
	}
	return out, nil
}

// notifyMentioned mails each person a note mentioned that a colleague did. The
// mail names the author and links the conversation; it holds nothing of the
// note, because it leaves the application and the person may not read the
// conversation. An agent is woken by the agents module, not mailed. A failure is
// logged and never fails the note, which is written.
func (s *server) notifyMentioned(ctx context.Context, author, conversationID uuid.UUID, mentioned []contracts.UserEntry) {
	if len(mentioned) == 0 {
		return
	}
	by := "A colleague"
	if u, err := s.deps.Users.User(ctx, author); err == nil && u != nil && u.Kind == contracts.UserKindPerson {
		by = u.DisplayName
	}
	link := ""
	if cfg := s.deps.Config; cfg != nil && cfg.AppOrigin != "" {
		link = cfg.AppOrigin + cfg.BasePath + "/communications/inbox?conversationId=" + conversationID.String()
	}
	open := func(prefix string) string {
		if link == "" {
			return ""
		}
		return "\n\n" + prefix + " " + link
	}
	notice := contracts.Notice{Texts: map[string]contracts.NoticeText{
		"en": {Subject: by + " mentioned you in a note", Body: by + " mentioned you in an internal note on a conversation." + open("Open it in Vantigo:") + "\n"},
		"nb": {Subject: by + " nevnte deg i et notat", Body: by + " nevnte deg i et internt notat i en samtale." + open("Åpne den i Vantigo:") + "\n"},
	}}
	for _, u := range mentioned {
		if u.Kind == contracts.UserKindAgent {
			continue
		}
		if err := s.deps.Users.Notify(context.WithoutCancel(ctx), u.ID, notice); err != nil {
			s.deps.Logger.WarnContext(ctx, "communications: notify a mentioned person", "error", err)
		}
	}
}
