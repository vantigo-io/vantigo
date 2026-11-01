package assistant

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// userExists asks identity (contracts.UserDirectory) whether the user is one.
func (s *server) userExists(ctx context.Context, id uuid.UUID) (bool, error) {
	entry, err := s.deps.Users.User(ctx, id)
	if err != nil {
		return false, fmt.Errorf("assistant: look up the user: %w", err)
	}
	return entry != nil, nil
}

// personResponse is a person's switch; a person with no row is off at version 0.
func personResponse(userID uuid.UUID, row *store.AssistantPersonSetting) gen.AssistantPerson {
	if row == nil {
		return gen.AssistantPerson{UserId: userID, Enabled: false, Version: 0}
	}
	return gen.AssistantPerson{UserId: userID, Enabled: row.Enabled, Version: row.Version}
}

// GetAssistantPerson (GET /api/v1/assistant/people/{userId})
func (s *server) GetAssistantPerson(ctx context.Context, req gen.GetAssistantPersonRequestObject) (gen.GetAssistantPersonResponseObject, error) {
	if ok, err := s.userExists(ctx, req.UserId); err != nil {
		return nil, err
	} else if !ok {
		return notFound(), nil
	}
	row, err := store.New(s.deps.Pool).GetPerson(ctx, req.UserId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetAssistantPerson200JSONResponse(personResponse(req.UserId, nil)), nil
	}
	if err != nil {
		return nil, fmt.Errorf("assistant: read the person's switch: %w", err)
	}
	return gen.GetAssistantPerson200JSONResponse(personResponse(req.UserId, &row)), nil
}

// PutAssistantPerson (PUT /api/v1/assistant/people/{userId})
func (s *server) PutAssistantPerson(ctx context.Context, req gen.PutAssistantPersonRequestObject) (gen.PutAssistantPersonResponseObject, error) {
	version, ok := parseVersion(req.Params.IfMatch)
	if !ok {
		return badIfMatch(), nil
	}
	if req.Body == nil {
		return invalid("A body is required", nil), nil
	}
	if ok, err := s.userExists(ctx, req.UserId); err != nil {
		return nil, err
	} else if !ok {
		return notFound(), nil
	}
	actor := callerID(ctx)
	var saved store.AssistantPersonSetting
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		now := s.deps.Clock()
		if err := q.EnsurePerson(ctx, store.EnsurePersonParams{UserID: req.UserId, Now: now}); err != nil {
			return fmt.Errorf("assistant: make the person's row: %w", err)
		}
		current, err := q.LockPerson(ctx, req.UserId)
		if err != nil {
			return fmt.Errorf("assistant: lock the person's row: %w", err)
		}
		if current.Version != version {
			return errVersion
		}
		if saved, err = q.SetPersonEnabled(ctx, store.SetPersonEnabledParams{UserID: req.UserId, Enabled: req.Body.Enabled, Now: now}); err != nil {
			return fmt.Errorf("assistant: change the person's switch: %w", err)
		}
		return s.audit(ctx, q, actor, "person.changed", req.UserId.String(),
			map[string]any{"enabled": current.Enabled}, map[string]any{"enabled": saved.Enabled})
	})
	if errors.Is(err, errVersion) {
		r := refuse(409, codeVersion, "The switch changed since you read it")
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PutAssistantPerson200JSONResponse(personResponse(req.UserId, &saved)), nil
}

// GetAssistantMe (GET /api/v1/assistant/me)
func (s *server) GetAssistantMe(ctx context.Context, _ gen.GetAssistantMeRequestObject) (gen.GetAssistantMeResponseObject, error) {
	if r, err := s.require(ctx); err != nil || r != nil {
		return orRefusal[gen.GetAssistantMeResponseObject](r, err)
	}
	row, err := store.New(s.deps.Pool).GetPerson(ctx, callerID(ctx))
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetAssistantMe200JSONResponse(gen.AssistantMe{}), nil
	}
	if err != nil {
		return nil, fmt.Errorf("assistant: read the caller's state: %w", err)
	}
	return gen.GetAssistantMe200JSONResponse(gen.AssistantMe{LastConversationId: row.LastConversationID}), nil
}

// PutAssistantMe (PUT /api/v1/assistant/me)
func (s *server) PutAssistantMe(ctx context.Context, req gen.PutAssistantMeRequestObject) (gen.PutAssistantMeResponseObject, error) {
	if r, err := s.require(ctx); err != nil || r != nil {
		return orRefusal[gen.PutAssistantMeResponseObject](r, err)
	}
	if req.Body == nil {
		return invalid("A body is required", nil), nil
	}
	user := callerID(ctx)
	q := store.New(s.deps.Pool)
	if id := req.Body.LastConversationId; id != nil {
		// Only the caller's own conversation: another's is a 404.
		if _, err := q.GetConversation(ctx, store.GetConversationParams{ID: *id, UserID: user}); errors.Is(err, pgx.ErrNoRows) {
			return notFound(), nil
		} else if err != nil {
			return nil, fmt.Errorf("assistant: look up the conversation: %w", err)
		}
	}
	if err := q.SetLastConversation(ctx, store.SetLastConversationParams{UserID: user, ConversationID: req.Body.LastConversationId, Now: s.deps.Clock()}); err != nil {
		return nil, fmt.Errorf("assistant: remember the conversation: %w", err)
	}
	return gen.PutAssistantMe200JSONResponse(gen.AssistantMe{LastConversationId: req.Body.LastConversationId}), nil
}

// orRefusal turns the pair require returns into a handler's answer: the
// refusal when there is one, the error otherwise. The zero T is never
// returned alongside neither.
func orRefusal[T any](r *refusal, err error) (T, error) {
	var zero T
	if err != nil {
		return zero, err
	}
	resp, _ := any(*r).(T)
	return resp, nil
}
