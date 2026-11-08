package agents

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents/gen"
	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/ai"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// permViewConversations is what a hand-off person needs to be handed a
// conversation.
const permViewConversations = "communications:conversations-view"

// modelFor is the model the agent works with, one AI_MODELS allows; empty when
// the installation has no AI provider or no model slot.
func (s *server) modelFor(ctx context.Context, agentID uuid.UUID) (string, error) {
	if s.deps.AI == nil || s.deps.AIModels == nil {
		return "", nil
	}
	model, err := s.deps.AIModels.ModelFor(ctx, ai.TaskFor(aiTaskRun, agentID.String()))
	if err != nil {
		return "", fmt.Errorf("agents: read the agent's model: %w", err)
	}
	return model, nil
}

// handOffWarnings answers, for each person a role hands off to, why the
// hand-off would go to the Inbox instead (V12). A person who is fine is absent.
func (s *server) handOffWarnings(ctx context.Context, roles []store.AgentsRole) (map[uuid.UUID]gen.AgentRoleHandOffWarning, error) {
	var ids []uuid.UUID
	for _, r := range roles {
		if r.HandOffUserID != nil && !slices.Contains(ids, *r.HandOffUserID) {
			ids = append(ids, *r.HandOffUserID)
		}
	}
	out := map[uuid.UUID]gen.AgentRoleHandOffWarning{}
	if len(ids) == 0 {
		return out, nil
	}
	users, err := s.deps.Users.Users(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("agents: read the hand-off people: %w", err)
	}
	holders, err := s.deps.Users.PermissionHolders(ctx, permViewConversations)
	if err != nil {
		return nil, fmt.Errorf("agents: read who may be handed a conversation: %w", err)
	}
	holds := map[uuid.UUID]bool{}
	for _, h := range holders {
		holds[h.ID] = true
	}
	found := map[uuid.UUID]contracts.UserEntry{}
	for _, u := range users {
		found[u.ID] = u
	}
	for _, id := range ids {
		u, ok := found[id]
		switch {
		case !ok || !u.Active:
			out[id] = "disabled"
		case u.Kind == contracts.UserKindAgent:
			out[id] = "not_a_person"
		case !holds[id]:
			out[id] = "no_permission"
		}
	}
	return out, nil
}

// roleViews are the roles as the contract answers them, with their channels and
// hand-off warnings.
func (s *server) roleViews(ctx context.Context, q *store.Queries, roles []store.AgentsRole) (map[uuid.UUID][]gen.AgentRole, error) {
	out := map[uuid.UUID][]gen.AgentRole{}
	if len(roles) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, len(roles))
	for i, r := range roles {
		ids[i] = r.ID
	}
	rows, err := q.ListChannelsOfRoles(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("agents: read the roles' channels: %w", err)
	}
	channels := map[uuid.UUID][]uuid.UUID{}
	for _, c := range rows {
		channels[c.RoleID] = append(channels[c.RoleID], c.ChannelID)
	}
	warnings, err := s.handOffWarnings(ctx, roles)
	if err != nil {
		return nil, err
	}
	for _, r := range roles {
		complaintTags := r.ComplaintTagIds
		if complaintTags == nil {
			complaintTags = []uuid.UUID{}
		}
		v := gen.AgentRole{
			Id: r.ID, AgentId: r.AgentID, Template: gen.AgentRoleTemplate(r.Template), Name: r.Name,
			Instructions: r.Instructions, ReplyMode: gen.AgentRoleReplyMode(r.ReplyMode), Threshold: int32(r.Threshold),
			CapsToolCalls: int32(r.CapsToolCalls), CapsTurns: int32(r.CapsTurns), MaxRepliesPerDay: int32(r.MaxRepliesPerDay),
			HandOffUserId: r.HandOffUserID, HandOffWarning: "none", ChannelIds: channels[r.ID], ComplaintTagIds: &complaintTags,
			Version: r.Version, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}
		if v.ChannelIds == nil {
			v.ChannelIds = []uuid.UUID{}
		}
		if r.HandOffUserID != nil {
			if w, ok := warnings[*r.HandOffUserID]; ok {
				v.HandOffWarning = w
			}
		}
		out[r.AgentID] = append(out[r.AgentID], v)
	}
	return out, nil
}

// agentViews are the agents as the contract answers them, with their roles,
// models and whether they have a picture.
func (s *server) agentViews(ctx context.Context, q *store.Queries, rows []store.AgentsAgent) ([]gen.Agent, error) {
	ids := make([]uuid.UUID, len(rows))
	for i, a := range rows {
		ids[i] = a.ID
	}
	roles, err := q.ListRolesOfAgents(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("agents: read the roles: %w", err)
	}
	roleViews, err := s.roleViews(ctx, q, roles)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Agent, 0, len(rows))
	for _, a := range rows {
		model, err := s.modelFor(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		avatar, err := s.deps.Users.AvatarInfo(ctx, a.UserID)
		if err != nil {
			return nil, fmt.Errorf("agents: read the agent's picture: %w", err)
		}
		v := gen.Agent{
			Id: a.ID, UserId: a.UserID, Name: a.Name, Style: a.Style,
			Languages: gen.AgentLanguages(a.Languages), DisclosureWording: gen.AgentDisclosureWording(a.DisclosureWording),
			State: gen.AgentState(a.State), DailyTokenBudget: a.DailyTokenBudget, Model: model, HasAvatar: avatar != nil,
			Roles: roleViews[a.ID], Version: a.Version, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
		}
		if v.Roles == nil {
			v.Roles = []gen.AgentRole{}
		}
		out = append(out, v)
	}
	return out, nil
}
