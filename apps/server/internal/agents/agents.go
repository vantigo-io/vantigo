package agents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/agents/gen"
	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

const (
	maxAgentName  = 60
	maxAgentStyle = 1500

	stateActive  = "active"
	statePaused  = "paused"
	stateDeleted = "deleted"

	maxAvatarImageBytes = 5 * 1024 * 1024

	// minTokenBudget is the smallest daily token budget: below it a single run
	// would reach it.
	minTokenBudget = 1000
)

// persona is the part of an agent a request names (V10).
type persona struct {
	Name, Style, Languages, DisclosureWording, Model string
}

// validatePersona checks a request's persona and answers the field errors. The
// model must be one the installation allows; with no AI provider only the empty
// choice is.
func (s *server) validatePersona(p persona) map[string][]string {
	errs := map[string][]string{}
	if n := utf8.RuneCountInString(strings.TrimSpace(p.Name)); n < 1 || n > maxAgentName {
		errs["name"] = []string{fmt.Sprintf("name must be 1 to %d characters", maxAgentName)}
	}
	if utf8.RuneCountInString(p.Style) > maxAgentStyle {
		errs["style"] = []string{fmt.Sprintf("style must be at most %d characters", maxAgentStyle)}
	}
	if !oneOf(p.Languages, "customer", "nb", "en") {
		errs["languages"] = []string{"languages must be customer, nb or en"}
	}
	if !oneOf(p.DisclosureWording, "assistant", "talking", "compact") {
		errs["disclosureWording"] = []string{"disclosureWording must be assistant, talking or compact"}
	}
	if p.Model != "" && (s.deps.AI == nil || !slices.Contains(s.deps.AI.Models(), p.Model)) {
		errs["model"] = []string{"model must be one the installation allows"}
	}
	return errs
}

// loadAgent reads one agent, nil when there is none or it is deleted.
func (s *server) loadAgent(ctx context.Context, id uuid.UUID) (*store.AgentsAgent, error) {
	row, err := store.New(s.deps.Pool).GetAgent(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("agents: read the agent: %w", err)
	}
	return &row, nil
}

// agentView is one agent as the contract answers it.
func (s *server) agentView(ctx context.Context, row store.AgentsAgent) (gen.Agent, error) {
	views, err := s.agentViews(ctx, store.New(s.deps.Pool), []store.AgentsAgent{row})
	if err != nil {
		return gen.Agent{}, err
	}
	return views[0], nil
}

// agentSnapshot is what the audit records of an agent.
func agentSnapshot(a store.AgentsAgent, model string) map[string]any {
	return map[string]any{"name": a.Name, "style": a.Style, "languages": a.Languages, "disclosureWording": a.DisclosureWording, "state": a.State, "model": model, "dailyTokenBudget": a.DailyTokenBudget}
}

// ListAgents (GET /api/v1/agents/agents)
func (s *server) ListAgents(ctx context.Context, _ gen.ListAgentsRequestObject) (gen.ListAgentsResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.ListAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("agents: list the agents: %w", err)
	}
	views, err := s.agentViews(ctx, q, rows)
	if err != nil {
		return nil, err
	}
	return gen.ListAgents200JSONResponse(gen.AgentList{Agents: views}), nil
}

// CreateAgent (POST /api/v1/agents/agents)
//
// The company's name comes first (V11): the disclosure names it, so no agent
// exists before it is set. The user is made in identity before the row; when
// the row cannot be written the user is disabled again.
func (s *server) CreateAgent(ctx context.Context, req gen.CreateAgentRequestObject) (gen.CreateAgentResponseObject, error) {
	if req.Body == nil {
		return invalid("A body is required", nil), nil
	}
	b := *req.Body
	p := persona{Name: strings.TrimSpace(b.Name), Style: b.Style, Languages: string(b.Languages), DisclosureWording: string(b.DisclosureWording), Model: b.Model}
	if errs := s.validatePersona(p); len(errs) > 0 {
		return invalid("The agent is not valid", errs), nil
	}
	settings, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	if settings.CompanyName == "" {
		return refuse(http.StatusConflict, codeCompanyName, "Set the company's name in the settings first"), nil
	}

	userID, err := s.deps.AgentAccounts.Create(ctx, p.Name)
	if errors.Is(err, contracts.ErrInvalidAgentName) {
		return invalid("The agent is not valid", map[string][]string{"name": {"name must be 1 to 60 characters"}}), nil
	}
	if err != nil {
		return nil, fmt.Errorf("agents: make the agent's user: %w", err)
	}
	actor := callerID(ctx)
	id := uuid.New()
	// The steps in other modules come first and are undone when a later one
	// fails, so a failed create leaves no user and no model choice behind.
	undo := func() {
		if err := s.deps.AgentAccounts.Disable(ctx, userID); err != nil {
			s.warn("agents: disable the user of an agent that was not created", "user", userID, "err", err)
		}
		if p.Model != "" {
			if err := s.deps.AIModels.SetModelFor(ctx, aiTaskRun, id.String(), ""); err != nil {
				s.warn("agents: forget the model of an agent that was not created", "agent", id, "err", err)
			}
		}
	}
	if p.Model != "" {
		if err := s.deps.AIModels.SetModelFor(ctx, aiTaskRun, id.String(), p.Model); err != nil {
			undo()
			return nil, fmt.Errorf("agents: choose the agent's model: %w", err)
		}
	}
	var row store.AgentsAgent
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		row, err = q.InsertAgent(ctx, store.InsertAgentParams{
			ID: id, UserID: userID, Name: p.Name, Style: p.Style, Languages: p.Languages,
			DisclosureWording: p.DisclosureWording, Now: s.deps.Clock(), CreatedBy: actor,
		})
		if err != nil {
			return fmt.Errorf("agents: add the agent: %w", err)
		}
		return s.audit(ctx, q, actor, "agent.created", id.String(), nil, agentSnapshot(row, p.Model))
	})
	if err != nil {
		undo()
		return nil, err
	}
	view, err := s.agentView(ctx, row)
	if err != nil {
		return nil, err
	}
	return gen.CreateAgent201JSONResponse(view), nil
}

// GetAgent (GET /api/v1/agents/agents/{agentId})
func (s *server) GetAgent(ctx context.Context, req gen.GetAgentRequestObject) (gen.GetAgentResponseObject, error) {
	row, err := s.loadAgent(ctx, req.AgentId)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return notFound(), nil
	}
	view, err := s.agentView(ctx, *row)
	if err != nil {
		return nil, err
	}
	return gen.GetAgent200JSONResponse(view), nil
}

// UpdateAgent (PUT /api/v1/agents/agents/{agentId})
//
// The persona replaces the agent's. The version is judged under the row's lock.
// The user's display name and the model follow the row.
func (s *server) UpdateAgent(ctx context.Context, req gen.UpdateAgentRequestObject) (gen.UpdateAgentResponseObject, error) {
	version, ok := parseVersion(req.Params.IfMatch)
	if !ok {
		return badIfMatch(), nil
	}
	if req.Body == nil {
		return invalid("A body is required", nil), nil
	}
	b := *req.Body
	p := persona{Name: strings.TrimSpace(b.Name), Style: b.Style, Languages: string(b.Languages), DisclosureWording: string(b.DisclosureWording), Model: b.Model}
	errs := s.validatePersona(p)
	if b.DailyTokenBudget != nil && *b.DailyTokenBudget != 0 && *b.DailyTokenBudget < minTokenBudget {
		if errs == nil {
			errs = map[string][]string{}
		}
		errs["dailyTokenBudget"] = []string{fmt.Sprintf("dailyTokenBudget must be 0 or at least %d", minTokenBudget)}
	}
	if len(errs) > 0 {
		return invalid("The agent is not valid", errs), nil
	}
	actor := callerID(ctx)
	var before, saved store.AgentsAgent
	var beforeModel string
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		current, err := q.LockAgent(ctx, req.AgentId)
		if errors.Is(err, pgx.ErrNoRows) {
			return pgx.ErrNoRows
		}
		if err != nil {
			return fmt.Errorf("agents: lock the agent: %w", err)
		}
		if current.Version != version {
			return errVersion
		}
		before = current
		if beforeModel, err = s.modelFor(ctx, current.ID); err != nil {
			return err
		}
		budget := current.DailyTokenBudget // absent keeps it; 0 removes it
		if b.DailyTokenBudget != nil {
			budget = nil
			if *b.DailyTokenBudget > 0 {
				budget = b.DailyTokenBudget
			}
		}
		if saved, err = q.UpdateAgent(ctx, store.UpdateAgentParams{
			ID: current.ID, Name: p.Name, Style: p.Style, Languages: p.Languages, DisclosureWording: p.DisclosureWording,
			DailyTokenBudget: budget, Now: s.deps.Clock(),
		}); err != nil {
			return fmt.Errorf("agents: change the agent: %w", err)
		}
		if err := s.audit(ctx, q, actor, "agent.changed", current.ID.String(), agentSnapshot(current, beforeModel), agentSnapshot(saved, p.Model)); err != nil {
			return err
		}
		// The steps in other modules run before the commit, under the row's
		// lock and after the version check, so a refused or conflicting update
		// changes nothing of theirs. Both set the value the body names, so a
		// commit that fails after them is mended by sending the same body again.
		if before.Name != saved.Name {
			if err := s.deps.AgentAccounts.Rename(ctx, saved.UserID, saved.Name); err != nil {
				return fmt.Errorf("agents: rename the agent's user: %w", err)
			}
		}
		if s.deps.AIModels != nil && s.deps.AI != nil {
			if err := s.deps.AIModels.SetModelFor(ctx, aiTaskRun, saved.ID.String(), p.Model); err != nil {
				if before.Name != saved.Name {
					if rerr := s.deps.AgentAccounts.Rename(ctx, saved.UserID, before.Name); rerr != nil {
						s.warn("agents: restore the agent's user name", "user", saved.UserID, "err", rerr)
					}
				}
				return fmt.Errorf("agents: choose the agent's model: %w", err)
			}
		}
		return nil
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return notFound(), nil
	case errors.Is(err, errVersion):
		return versionConflict(), nil
	case err != nil:
		return nil, err
	}
	s.gates.invalidate(saved.ID)
	view, err := s.agentView(ctx, saved)
	if err != nil {
		return nil, err
	}
	return gen.UpdateAgent200JSONResponse(view), nil
}

// setState moves an agent between active and paused. Pausing a paused agent is
// not an error.
func (s *server) setState(ctx context.Context, id uuid.UUID, from, to string) (*store.AgentsAgent, error) {
	actor := callerID(ctx)
	var saved store.AgentsAgent
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		current, err := q.LockAgent(ctx, id)
		if err != nil {
			return err
		}
		if current.State != from {
			saved = current
			return nil
		}
		if saved, err = q.SetAgentState(ctx, store.SetAgentStateParams{ID: id, State: to, Now: s.deps.Clock()}); err != nil {
			return fmt.Errorf("agents: change the agent's state: %w", err)
		}
		return s.audit(ctx, q, actor, "agent."+to, id.String(), map[string]any{"state": from}, map[string]any{"state": to})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.gates.invalidate(id)
	return &saved, nil
}

// PauseAgent (POST /api/v1/agents/agents/{agentId}/pause)
//
// A paused agent starts no new run. Runs and items already queued stay queued.
func (s *server) PauseAgent(ctx context.Context, req gen.PauseAgentRequestObject) (gen.PauseAgentResponseObject, error) {
	row, err := s.setState(ctx, req.AgentId, stateActive, statePaused)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return notFound(), nil
	}
	view, err := s.agentView(ctx, *row)
	if err != nil {
		return nil, err
	}
	return gen.PauseAgent200JSONResponse(view), nil
}

// ResumeAgent (POST /api/v1/agents/agents/{agentId}/resume)
func (s *server) ResumeAgent(ctx context.Context, req gen.ResumeAgentRequestObject) (gen.ResumeAgentResponseObject, error) {
	row, err := s.setState(ctx, req.AgentId, statePaused, stateActive)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return notFound(), nil
	}
	view, err := s.agentView(ctx, *row)
	if err != nil {
		return nil, err
	}
	return gen.ResumeAgent200JSONResponse(view), nil
}

// DeleteAgent (DELETE /api/v1/agents/agents/{agentId})
//
// The agent is marked deleted, its channels are freed and its grants and user
// end (V26). The user is never deleted, so the audit's references stay true.
// The steps in identity come first and are idempotent, and the row is marked
// last, so a delete that fails half way leaves an agent that is still there and
// is deleted by sending it again.
func (s *server) DeleteAgent(ctx context.Context, req gen.DeleteAgentRequestObject) (gen.DeleteAgentResponseObject, error) {
	row, err := s.loadAgent(ctx, req.AgentId)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return notFound(), nil
	}
	if err := s.deps.AgentGrants.RevokeForUser(ctx, row.UserID); err != nil {
		return nil, fmt.Errorf("agents: end the agent's grants: %w", err)
	}
	if err := s.deps.AgentAccounts.Disable(ctx, row.UserID); err != nil {
		return nil, fmt.Errorf("agents: disable the agent's user: %w", err)
	}
	if s.deps.AIModels != nil && s.deps.AI != nil {
		if err := s.deps.AIModels.SetModelFor(ctx, aiTaskRun, row.ID.String(), ""); err != nil {
			return nil, fmt.Errorf("agents: forget the agent's model: %w", err)
		}
	}
	actor := callerID(ctx)
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		current, err := q.LockAgent(ctx, req.AgentId)
		if err != nil {
			return err
		}
		if _, err := q.SetAgentState(ctx, store.SetAgentStateParams{ID: current.ID, State: stateDeleted, Now: s.deps.Clock()}); err != nil {
			return fmt.Errorf("agents: delete the agent: %w", err)
		}
		withdrawn, err := q.WithdrawWaitingItemsOfAgent(ctx, store.WithdrawWaitingItemsOfAgentParams{AgentID: current.ID, Now: s.deps.Clock()})
		if err != nil {
			return fmt.Errorf("agents: withdraw what waited for the agent: %w", err)
		}
		if err := handOffWithdrawn(ctx, q, refsOf(withdrawn, func(r store.WithdrawWaitingItemsOfAgentRow) convRef {
			return convRef{r.AgentID, r.RoleID, r.ConversationID}
		}), "The agent was deleted, so what waited for its review was withdrawn.", s.deps.Clock()); err != nil {
			return err
		}
		if err := q.DeleteChannelsOfAgent(ctx, current.ID); err != nil {
			return fmt.Errorf("agents: free the agent's channels: %w", err)
		}
		return s.audit(ctx, q, actor, "agent.deleted", current.ID.String(), agentSnapshot(current, ""), map[string]any{"state": stateDeleted})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound(), nil
	}
	if err != nil {
		return nil, err
	}
	s.gates.invalidate(req.AgentId)
	return gen.DeleteAgent204Response{}, nil
}

// avatarHeaders is the private, unsniffable, inline headers an avatar answers
// with, as identity's own do.
type avatarHeaders struct {
	body gen.GetAgentAvatarResponseObject
}

func (r avatarHeaders) VisitGetAgentAvatarResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	return r.body.VisitGetAgentAvatarResponse(w)
}

// GetAgentAvatar (GET /api/v1/agents/agents/{agentId}/avatar)
func (s *server) GetAgentAvatar(ctx context.Context, req gen.GetAgentAvatarRequestObject) (gen.GetAgentAvatarResponseObject, error) {
	row, err := s.loadAgent(ctx, req.AgentId)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return gen.GetAgentAvatar404Response{}, nil
	}
	avatar, err := s.deps.Users.Avatar(ctx, row.UserID)
	if err != nil {
		return nil, fmt.Errorf("agents: read the agent's picture: %w", err)
	}
	if avatar == nil {
		return gen.GetAgentAvatar404Response{}, nil
	}
	length := int64(len(avatar.Data))
	if avatar.ContentType == "image/png" {
		return avatarHeaders{gen.GetAgentAvatar200ImagepngResponse{Body: bytes.NewReader(avatar.Data), ContentLength: length}}, nil
	}
	return avatarHeaders{gen.GetAgentAvatar200ImagejpegResponse{Body: bytes.NewReader(avatar.Data), ContentLength: length}}, nil
}

// avatarPart reads the multipart part named "avatar": its bytes and the type it
// declares. found is false for a missing, empty or oversized part and for a
// malformed body.
func avatarPart(mr *multipart.Reader) (data []byte, declared string, found bool) {
	for {
		part, err := mr.NextPart()
		if err != nil {
			return nil, "", false
		}
		if part.FormName() != "avatar" {
			_ = part.Close()
			continue
		}
		declared = part.Header.Get("Content-Type")
		data, err = io.ReadAll(io.LimitReader(part, maxAvatarImageBytes+1))
		_ = part.Close()
		return data, declared, err == nil && len(data) > 0 && len(data) <= maxAvatarImageBytes
	}
}

// PutAgentAvatar (PUT /api/v1/agents/agents/{agentId}/avatar)
func (s *server) PutAgentAvatar(ctx context.Context, req gen.PutAgentAvatarRequestObject) (gen.PutAgentAvatarResponseObject, error) {
	row, err := s.loadAgent(ctx, req.AgentId)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return notFound(), nil
	}
	bad := invalid("The picture must be a PNG or JPEG image", map[string][]string{"avatar": {"avatar must be a JPEG or PNG of at most 5 MB"}})
	if req.Body == nil {
		return bad, nil
	}
	data, declared, found := avatarPart(req.Body)
	if !found {
		return bad, nil
	}
	if err := s.deps.AgentAccounts.SetAvatar(ctx, row.UserID, declared, data); errors.Is(err, contracts.ErrInvalidAgentAvatar) {
		return bad, nil
	} else if err != nil {
		return nil, fmt.Errorf("agents: set the agent's picture: %w", err)
	}
	view, err := s.agentView(ctx, *row)
	if err != nil {
		return nil, err
	}
	return gen.PutAgentAvatar200JSONResponse(view), nil
}
