package assistant

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

const (
	defaultPageSize   = 30
	maxPageSize       = 100
	maxTitleChars     = 200
	titleFromQuestion = 80
)

// conversationView is a conversation as the contract answers it.
func conversationView(id uuid.UUID, title, app, route string, pinned bool, archivedAt *time.Time, running bool, created, last time.Time) gen.AssistantConversation {
	return gen.AssistantConversation{
		Id: id, Title: title, OriginApp: app, OriginRoute: route, Pinned: pinned, Archived: archivedAt != nil,
		Running: running, CreatedAt: created, LastActivityAt: last,
	}
}

func fromListRow(r store.ListConversationsRow) gen.AssistantConversation {
	return conversationView(r.ID, r.Title, r.OriginApp, r.OriginRoute, r.Pinned, r.ArchivedAt, r.Running, r.CreatedAt, r.LastActivityAt)
}

func fromGetRow(r store.GetConversationRow) gen.AssistantConversation {
	return conversationView(r.ID, r.Title, r.OriginApp, r.OriginRoute, r.Pinned, r.ArchivedAt, r.Running, r.CreatedAt, r.LastActivityAt)
}

// cursor is the keyset position of the last conversation of a page.
type cursor struct {
	Pinned bool      `json:"p"`
	At     time.Time `json:"t"`
	ID     uuid.UUID `json:"i"`
}

func encodeCursor(c cursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(s string) (cursor, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, false
	}
	var c cursor
	if json.Unmarshal(raw, &c) != nil || c.ID == uuid.Nil {
		return cursor{}, false
	}
	return c, true
}

// ListAssistantConversations (GET /api/v1/assistant/conversations)
func (s *server) ListAssistantConversations(ctx context.Context, req gen.ListAssistantConversationsRequestObject) (gen.ListAssistantConversationsResponseObject, error) {
	if r, err := s.require(ctx); err != nil || r != nil {
		return orRefusal[gen.ListAssistantConversationsResponseObject](r, err)
	}
	p := store.ListConversationsParams{UserID: callerID(ctx), PageSize: defaultPageSize + 1}
	if req.Params.Archived != nil {
		p.Archived = *req.Params.Archived
	}
	if req.Params.Q != nil {
		p.Query = strings.TrimSpace(*req.Params.Q)
		if utf8.RuneCountInString(p.Query) > 200 {
			return invalid("The search is too long", map[string][]string{"q": {"q is at most 200 characters"}}), nil
		}
	}
	pageSize := int32(defaultPageSize)
	if req.Params.Limit != nil {
		pageSize = min(max(*req.Params.Limit, 1), maxPageSize)
	}
	p.PageSize = pageSize + 1
	if req.Params.Cursor != nil && *req.Params.Cursor != "" {
		c, ok := decodeCursor(*req.Params.Cursor)
		if !ok {
			return invalid("The cursor is not valid", map[string][]string{"cursor": {"cursor is not one this server issued"}}), nil
		}
		p.HasCursor, p.CursorPinned, p.CursorAt, p.CursorID = true, c.Pinned, c.At, c.ID
	}
	rows, err := store.New(s.deps.Pool).ListConversations(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("assistant: list the conversations: %w", err)
	}
	out := gen.AssistantConversationList{Items: []gen.AssistantConversation{}}
	if int32(len(rows)) > pageSize {
		last := rows[pageSize-1]
		next := encodeCursor(cursor{Pinned: last.Pinned, At: last.LastActivityAt, ID: last.ID})
		out.NextCursor = &next
		rows = rows[:pageSize]
	}
	for _, r := range rows {
		out.Items = append(out.Items, fromListRow(r))
	}
	return gen.ListAssistantConversations200JSONResponse(out), nil
}

// CreateAssistantConversation (POST /api/v1/assistant/conversations)
func (s *server) CreateAssistantConversation(ctx context.Context, req gen.CreateAssistantConversationRequestObject) (gen.CreateAssistantConversationResponseObject, error) {
	if r, err := s.require(ctx); err != nil || r != nil {
		return orRefusal[gen.CreateAssistantConversationResponseObject](r, err)
	}
	if req.Body == nil {
		return invalid("A body is required", nil), nil
	}
	origin := req.Body.Origin
	errs := map[string][]string{}
	if strings.TrimSpace(origin.App) == "" || len(origin.App) > 40 {
		errs["origin.app"] = []string{"origin.app is required, at most 40 characters"}
	}
	if !strings.HasPrefix(origin.Route, "/") || len(origin.Route) > 300 {
		errs["origin.route"] = []string{"origin.route is a route starting with /, at most 300 characters"}
	}
	if len(errs) > 0 {
		return invalid("The origin is not valid", errs), nil
	}
	user, id, now := callerID(ctx), uuid.New(), s.deps.Clock()
	var row store.AssistantConversation
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		row, err = q.CreateConversation(ctx, store.CreateConversationParams{ID: id, UserID: user, OriginApp: origin.App, OriginRoute: origin.Route, Now: now})
		if err != nil {
			return err
		}
		return q.SetLastConversation(ctx, store.SetLastConversationParams{UserID: user, ConversationID: &id, Now: now})
	})
	if err != nil {
		return nil, fmt.Errorf("assistant: start a conversation: %w", err)
	}
	s.countConversation(ctx, user)
	return gen.CreateAssistantConversation201JSONResponse(conversationView(row.ID, row.Title, row.OriginApp, row.OriginRoute, row.Pinned, row.ArchivedAt, false, row.CreatedAt, row.LastActivityAt)), nil
}

// GetAssistantConversation (GET /api/v1/assistant/conversations/{id})
func (s *server) GetAssistantConversation(ctx context.Context, req gen.GetAssistantConversationRequestObject) (gen.GetAssistantConversationResponseObject, error) {
	if r, err := s.require(ctx); err != nil || r != nil {
		return orRefusal[gen.GetAssistantConversationResponseObject](r, err)
	}
	user := callerID(ctx)
	q := store.New(s.deps.Pool)
	conv, err := q.GetConversation(ctx, store.GetConversationParams{ID: req.Id, UserID: user})
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("assistant: read the conversation: %w", err)
	}
	rows, err := q.ListMessages(ctx, store.ListMessagesParams{ConversationID: req.Id, UserID: user})
	if err != nil {
		return nil, fmt.Errorf("assistant: read the messages: %w", err)
	}
	out := gen.AssistantConversationDetail{Conversation: fromGetRow(conv), Messages: make([]gen.AssistantMessage, 0, len(rows)), Actions: []gen.AssistantAction{}, PendingActions: []gen.AssistantPendingAction{}}
	for _, m := range rows {
		out.Messages = append(out.Messages, messageView(m))
	}
	if run, err := q.GetLatestRun(ctx, store.GetLatestRunParams{ConversationID: req.Id, UserID: user}); err == nil {
		v := runView(run)
		if run.State == "running" || run.State == stateAwaiting {
			out.ActiveRun = &v
		} else {
			out.LastRun = &v
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("assistant: read the latest run: %w", err)
	}
	actions, err := q.ListActions(ctx, store.ListActionsParams{ConversationID: req.Id, UserID: user})
	if err != nil {
		return nil, fmt.Errorf("assistant: read the action log: %w", err)
	}
	for _, a := range actions {
		out.Actions = append(out.Actions, actionView(a))
	}
	pending, err := q.ListPendingActions(ctx, store.ListPendingActionsParams{ConversationID: req.Id, UserID: user})
	if err != nil {
		return nil, fmt.Errorf("assistant: read the pending actions: %w", err)
	}
	// A bulk job whose claim lapsed (its replica went away) is taken over by the
	// replica that serves the person looking at it.
	s.resumeLapsed(ctx, user)
	for _, p := range pending {
		out.PendingActions = append(out.PendingActions, s.cardOf(ctx, p))
	}
	return gen.GetAssistantConversation200JSONResponse(out), nil
}

// UpdateAssistantConversation (PATCH /api/v1/assistant/conversations/{id})
func (s *server) UpdateAssistantConversation(ctx context.Context, req gen.UpdateAssistantConversationRequestObject) (gen.UpdateAssistantConversationResponseObject, error) {
	if r, err := s.require(ctx); err != nil || r != nil {
		return orRefusal[gen.UpdateAssistantConversationResponseObject](r, err)
	}
	if req.Body == nil {
		return invalid("A body is required", nil), nil
	}
	p := store.UpdateConversationParams{ID: req.Id, UserID: callerID(ctx), Pinned: req.Body.Pinned, Archived: req.Body.Archived, Now: s.deps.Clock()}
	if req.Body.Title != nil {
		title := strings.TrimSpace(*req.Body.Title)
		if title == "" || utf8.RuneCountInString(title) > maxTitleChars {
			return invalid("The title is not valid", map[string][]string{"title": {fmt.Sprintf("title is from 1 to %d characters", maxTitleChars)}}), nil
		}
		p.Title = &title
	}
	row, err := store.New(s.deps.Pool).UpdateConversation(ctx, p)
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("assistant: change the conversation: %w", err)
	}
	running, err := s.isRunning(ctx, row.ID, row.UserID)
	if err != nil {
		return nil, err
	}
	return gen.UpdateAssistantConversation200JSONResponse(conversationView(row.ID, row.Title, row.OriginApp, row.OriginRoute, row.Pinned, row.ArchivedAt, running, row.CreatedAt, row.LastActivityAt)), nil
}

func (s *server) isRunning(ctx context.Context, conversationID, userID uuid.UUID) (bool, error) {
	_, err := store.New(s.deps.Pool).GetActiveRun(ctx, store.GetActiveRunParams{ConversationID: conversationID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("assistant: read the active run: %w", err)
	}
	return true, nil
}

// DeleteAssistantConversation (DELETE /api/v1/assistant/conversations/{id})
func (s *server) DeleteAssistantConversation(ctx context.Context, req gen.DeleteAssistantConversationRequestObject) (gen.DeleteAssistantConversationResponseObject, error) {
	if r, err := s.require(ctx); err != nil || r != nil {
		return orRefusal[gen.DeleteAssistantConversationResponseObject](r, err)
	}
	user := callerID(ctx)
	q := store.New(s.deps.Pool)
	if _, err := q.GetConversation(ctx, store.GetConversationParams{ID: req.Id, UserID: user}); errors.Is(err, pgx.ErrNoRows) {
		return notFound(), nil
	} else if err != nil {
		return nil, fmt.Errorf("assistant: read the conversation: %w", err)
	}
	// A run in progress is stopped first: its goroutine is told, and the rows it
	// would still write go with the conversation.
	s.runs.cancelConversation(req.Id)
	if _, err := q.DeleteConversation(ctx, store.DeleteConversationParams{ID: req.Id, UserID: user}); errors.Is(err, pgx.ErrNoRows) {
		return notFound(), nil
	} else if err != nil {
		return nil, fmt.Errorf("assistant: delete the conversation: %w", err)
	}
	if err := q.ClearLastConversation(ctx, store.ClearLastConversationParams{UserID: user, ConversationID: req.Id}); err != nil {
		return nil, fmt.Errorf("assistant: forget the conversation: %w", err)
	}
	return gen.DeleteAssistantConversation204Response{}, nil
}
