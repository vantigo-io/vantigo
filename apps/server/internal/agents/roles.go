package agents

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vantigo-io/vantigo/server/internal/agents/gen"
	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

const (
	maxRoleName         = 80
	maxRoleInstructions = 8000

	modeReview    = "review"
	modeAutoAbove = "auto_above"
	modeAuto      = "auto"
)

// modeRank orders the reply modes by how much they leave to the agent: raising
// it is the Owner's alone (V12).
func modeRank(mode string) int {
	switch mode {
	case modeAuto:
		return 2
	case modeAutoAbove:
		return 1
	}
	return 0
}

// uniqueViolation says err is PostgreSQL's 23505.
func uniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// roleSnapshot is what the audit records of a role.
func roleSnapshot(r store.AgentsRole) map[string]any {
	return map[string]any{
		"name": r.Name, "replyMode": r.ReplyMode, "threshold": r.Threshold, "capsToolCalls": r.CapsToolCalls,
		"capsTurns": r.CapsTurns, "maxRepliesPerDay": r.MaxRepliesPerDay, "handOffUserId": r.HandOffUserID,
	}
}

// loadRoleView reads one role as the contract answers it.
func (s *server) loadRoleView(ctx context.Context, role store.AgentsRole) (gen.AgentRole, error) {
	views, err := s.roleViews(ctx, store.New(s.deps.Pool), []store.AgentsRole{role})
	if err != nil {
		return gen.AgentRole{}, err
	}
	return views[role.AgentID][0], nil
}

// CreateAgentRole (POST /api/v1/agents/agents/{agentId}/roles)
//
// A new role is in Review with no channel; the template's allowlist reaches the
// gate through the agent's version, which moves on.
func (s *server) CreateAgentRole(ctx context.Context, req gen.CreateAgentRoleRequestObject) (gen.CreateAgentRoleResponseObject, error) {
	if req.Body == nil {
		return invalid("A body is required", nil), nil
	}
	b := *req.Body
	tpl, known := templates[string(b.Template)]
	name := strings.TrimSpace(b.Name)
	errs := map[string][]string{}
	if !known {
		errs["template"] = []string{"template is not one this build offers"}
	}
	if n := utf8.RuneCountInString(name); n < 1 || n > maxRoleName {
		errs["name"] = []string{fmt.Sprintf("name must be 1 to %d characters", maxRoleName)}
	}
	if utf8.RuneCountInString(b.Instructions) > maxRoleInstructions {
		errs["instructions"] = []string{fmt.Sprintf("instructions must be at most %d characters", maxRoleInstructions)}
	}
	if len(errs) > 0 {
		return invalid("The role is not valid", errs), nil
	}
	instructions := b.Instructions
	if strings.TrimSpace(instructions) == "" {
		instructions = tpl.DefaultInstructions
	}

	actor := callerID(ctx)
	var row store.AgentsRole
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		agent, err := q.LockAgent(ctx, req.AgentId)
		if err != nil {
			return err
		}
		if row, err = q.InsertRole(ctx, store.InsertRoleParams{
			ID: uuid.New(), AgentID: agent.ID, Template: tpl.Key, Name: name, Instructions: instructions, Now: s.deps.Clock(),
		}); err != nil {
			return fmt.Errorf("agents: add the role: %w", err)
		}
		if err := q.BumpAgentVersion(ctx, store.BumpAgentVersionParams{ID: agent.ID, Now: s.deps.Clock()}); err != nil {
			return fmt.Errorf("agents: move the agent's version on: %w", err)
		}
		return s.audit(ctx, q, actor, "role.created", row.ID.String(), nil, roleSnapshot(row))
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound(), nil
	}
	if err != nil {
		return nil, err
	}
	s.gates.invalidate(req.AgentId)
	view, err := s.loadRoleView(ctx, row)
	if err != nil {
		return nil, err
	}
	return gen.CreateAgentRole201JSONResponse(view), nil
}

// UpdateAgentRole (PUT /api/v1/agents/agents/{agentId}/roles/{roleId})
//
// The role is replaced. Raising the reply mode, or lowering the threshold of a
// role that is not in Review, leaves more to the agent and is the Owner's alone
// (V12); the opposite is anyone's with agents:manage.
func (s *server) UpdateAgentRole(ctx context.Context, req gen.UpdateAgentRoleRequestObject) (gen.UpdateAgentRoleResponseObject, error) {
	version, ok := parseVersion(req.Params.IfMatch)
	if !ok {
		return badIfMatch(), nil
	}
	if req.Body == nil {
		return invalid("A body is required", nil), nil
	}
	b := *req.Body
	name := strings.TrimSpace(b.Name)
	errs := map[string][]string{}
	if n := utf8.RuneCountInString(name); n < 1 || n > maxRoleName {
		errs["name"] = []string{fmt.Sprintf("name must be 1 to %d characters", maxRoleName)}
	}
	if utf8.RuneCountInString(b.Instructions) > maxRoleInstructions {
		errs["instructions"] = []string{fmt.Sprintf("instructions must be at most %d characters", maxRoleInstructions)}
	}
	if !oneOf(string(b.ReplyMode), modeReview, modeAutoAbove, modeAuto) {
		errs["replyMode"] = []string{"replyMode must be review, auto_above or auto"}
	}
	if b.Threshold < 50 || b.Threshold > 100 {
		errs["threshold"] = []string{"threshold must be from 50 to 100"}
	}
	if b.CapsToolCalls < 1 || b.CapsToolCalls > 60 {
		errs["capsToolCalls"] = []string{"capsToolCalls must be from 1 to 60"}
	}
	if b.CapsTurns < 1 || b.CapsTurns > 16 {
		errs["capsTurns"] = []string{"capsTurns must be from 1 to 16"}
	}
	if b.MaxRepliesPerDay < 1 || b.MaxRepliesPerDay > 50 {
		errs["maxRepliesPerDay"] = []string{"maxRepliesPerDay must be from 1 to 50"}
	}
	if b.HandOffUserId != nil {
		u, err := s.deps.Users.User(ctx, *b.HandOffUserId)
		if err != nil {
			return nil, fmt.Errorf("agents: read the hand-off person: %w", err)
		}
		if u == nil || !u.Active || u.Kind == contracts.UserKindAgent {
			errs["handOffUserId"] = []string{"handOffUserId must be an active person"}
		}
	}
	if len(errs) > 0 {
		return invalid("The role is not valid", errs), nil
	}

	actor := callerID(ctx)
	owner := s.isOwner(ctx)
	var saved store.AgentsRole
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		current, err := q.LockRole(ctx, store.LockRoleParams{ID: req.RoleId, AgentID: req.AgentId})
		if err != nil {
			return err
		}
		if current.Version != version {
			return errVersion
		}
		mode := string(b.ReplyMode)
		raises := modeRank(mode) > modeRank(current.ReplyMode) || (mode != modeReview && int16(b.Threshold) < current.Threshold)
		if raises && !owner {
			return errOwnerRequired
		}
		if saved, err = q.UpdateRole(ctx, store.UpdateRoleParams{
			ID: current.ID, Name: name, Instructions: b.Instructions, ReplyMode: mode, Threshold: int16(b.Threshold),
			CapsToolCalls: int16(b.CapsToolCalls), CapsTurns: int16(b.CapsTurns), MaxRepliesPerDay: int16(b.MaxRepliesPerDay),
			HandOffUserID: b.HandOffUserId, Now: s.deps.Clock(),
		}); err != nil {
			return fmt.Errorf("agents: change the role: %w", err)
		}
		action := "role.changed"
		if current.ReplyMode != saved.ReplyMode || current.Threshold != saved.Threshold {
			action = "role.mode.changed"
		}
		return s.audit(ctx, q, actor, action, current.ID.String(), roleSnapshot(current), roleSnapshot(saved))
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return notFound(), nil
	case errors.Is(err, errVersion):
		return versionConflict(), nil
	case errors.Is(err, errOwnerRequired):
		return ownerRequired(), nil
	case err != nil:
		return nil, err
	}
	view, err := s.loadRoleView(ctx, saved)
	if err != nil {
		return nil, err
	}
	return gen.UpdateAgentRole200JSONResponse(view), nil
}

// errOwnerRequired marks a change that needs the Owner.
var errOwnerRequired = errors.New("agents: only the Owner may do this")

// DeleteAgentRole (DELETE /api/v1/agents/agents/{agentId}/roles/{roleId})
func (s *server) DeleteAgentRole(ctx context.Context, req gen.DeleteAgentRoleRequestObject) (gen.DeleteAgentRoleResponseObject, error) {
	actor := callerID(ctx)
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.LockAgent(ctx, req.AgentId); err != nil {
			return err
		}
		current, err := q.LockRole(ctx, store.LockRoleParams{ID: req.RoleId, AgentID: req.AgentId})
		if err != nil {
			return err
		}
		if err := q.DeleteRole(ctx, current.ID); err != nil {
			return fmt.Errorf("agents: remove the role: %w", err)
		}
		if err := q.BumpAgentVersion(ctx, store.BumpAgentVersionParams{ID: req.AgentId, Now: s.deps.Clock()}); err != nil {
			return fmt.Errorf("agents: move the agent's version on: %w", err)
		}
		return s.audit(ctx, q, actor, "role.deleted", current.ID.String(), roleSnapshot(current), nil)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound(), nil
	}
	if err != nil {
		return nil, err
	}
	s.gates.invalidate(req.AgentId)
	return gen.DeleteAgentRole204Response{}, nil
}

// SetAgentRoleChannels (PUT /api/v1/agents/agents/{agentId}/roles/{roleId}/channels)
//
// A channel belongs to at most one role, so two agents never answer the same
// channel: a channel another role holds is 409 channel_bound. Binding one the
// role did not hold is the Owner's alone; unbinding is not. The channel's
// existence is Communications', which this module does not read: an id that
// names no channel never matches a conversation.
func (s *server) SetAgentRoleChannels(ctx context.Context, req gen.SetAgentRoleChannelsRequestObject) (gen.SetAgentRoleChannelsResponseObject, error) {
	if req.Body == nil {
		return invalid("A body is required", nil), nil
	}
	want := slices.Compact(slices.SortedFunc(slices.Values(req.Body.ChannelIds), func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) }))
	if len(want) > 50 {
		return invalid("The role is not valid", map[string][]string{"channelIds": {"a role answers at most 50 channels"}}), nil
	}
	actor := callerID(ctx)
	owner := s.isOwner(ctx)
	var role store.AgentsRole
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		if role, err = q.LockRole(ctx, store.LockRoleParams{ID: req.RoleId, AgentID: req.AgentId}); err != nil {
			return err
		}
		rows, err := q.ListChannelsOfRoles(ctx, []uuid.UUID{role.ID})
		if err != nil {
			return fmt.Errorf("agents: read the role's channels: %w", err)
		}
		had := make([]uuid.UUID, len(rows))
		for i, r := range rows {
			had[i] = r.ChannelID
		}
		if !owner && slices.ContainsFunc(want, func(c uuid.UUID) bool { return !slices.Contains(had, c) }) {
			return errOwnerRequired
		}
		if err := q.DeleteRoleChannels(ctx, role.ID); err != nil {
			return fmt.Errorf("agents: unbind the role's channels: %w", err)
		}
		for _, c := range want {
			if err := q.InsertRoleChannel(ctx, store.InsertRoleChannelParams{RoleID: role.ID, ChannelID: c}); err != nil {
				return err
			}
		}
		return s.audit(ctx, q, actor, "role.channels.changed", role.ID.String(), map[string]any{"channelIds": had}, map[string]any{"channelIds": want})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return notFound(), nil
	case errors.Is(err, errOwnerRequired):
		return ownerRequired(), nil
	case uniqueViolation(err):
		return refuse(409, codeChannelBound, "A channel is answered by another role"), nil
	case err != nil:
		return nil, err
	}
	view, err := s.loadRoleView(ctx, role)
	if err != nil {
		return nil, err
	}
	return gen.SetAgentRoleChannels200JSONResponse(view), nil
}
