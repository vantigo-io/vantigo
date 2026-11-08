package agents

// The agent's usage, runs and audit (virtual employees design V24, V25, V20): the
// Owner's view of what an agent costs, of what it did on each conversation, and of
// who changed its settings. A run's transcript copies the customer's messages, so
// the runs need agents:activity and what the agent read, like a review item.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/agents/gen"
	"github.com/vantigo-io/vantigo/server/internal/agents/store"
)

const (
	defaultUsageDays = 30
	maxUsageDays     = 90
)

// GetAgentUsage (GET /api/v1/agents/agents/{agentId}/usage)
func (s *server) GetAgentUsage(ctx context.Context, req gen.GetAgentUsageRequestObject) (gen.GetAgentUsageResponseObject, error) {
	q := store.New(s.deps.Pool)
	agent, err := q.GetAgentByID(ctx, req.AgentId)
	if isNoRows(err) {
		return notFound(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("agents: read the agent: %w", err)
	}
	days := int32(defaultUsageDays)
	if req.Params.Days != nil && *req.Params.Days >= 1 {
		days = min(*req.Params.Days, maxUsageDays)
	}
	today := s.day(s.deps.Clock())
	from := today.AddDate(0, 0, -int(days-1))
	rows, err := q.ListUsageDays(ctx, store.ListUsageDaysParams{AgentID: agent.ID, FromDay: from, ToDay: today})
	if err != nil {
		return nil, fmt.Errorf("agents: read the usage: %w", err)
	}
	byDay := map[string]store.AgentsUsageDay{}
	for _, r := range rows {
		byDay[r.Day.Format(time.DateOnly)] = r
	}
	out := gen.AgentUsage{AgentId: agent.ID, DailyTokenBudget: agent.DailyTokenBudget, Days: make([]gen.AgentUsageDay, 0, days)}
	for i := range int(days) {
		d := from.AddDate(0, 0, i)
		u := byDay[d.Format(time.DateOnly)]
		out.Days = append(out.Days, gen.AgentUsageDay{
			Day: openapi_types.Date{Time: d}, Runs: int32(u.Runs), InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
			Sent: u.Sent, Held: u.Held, Discarded: u.Discarded,
		})
	}
	return gen.GetAgentUsage200JSONResponse(out), nil
}

var runStates = []string{runQueued, runRunning, runDone, runWaitingReview, runHandedOff, runFailed, runStopped}

func runView(r store.AgentsRun) gen.AgentRun {
	return gen.AgentRun{
		Id: r.ID, AgentId: r.AgentID, RoleId: r.RoleID, ConversationId: r.ConversationID, Reason: r.Reason, Attempt: int32(r.Attempt),
		State: gen.AgentRunState(r.State), Outcome: r.Outcome, ErrorCode: r.ErrorCode, Summary: r.Summary,
		ToolCalls: r.ToolCalls, ModelTurns: r.ModelTurns, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
		StartedAt: r.StartedAt, EndedAt: r.EndedAt,
	}
}

// ListAgentRuns (GET /api/v1/agents/agents/{agentId}/runs)
func (s *server) ListAgentRuns(ctx context.Context, req gen.ListAgentRunsRequestObject) (gen.ListAgentRunsResponseObject, error) {
	var state *string
	if req.Params.State != nil {
		st := string(*req.Params.State)
		if !slices.Contains(runStates, st) {
			return invalid("The filter is not valid", map[string][]string{"state": {"state is not a run's state"}}), nil
		}
		state = &st
	}
	q := store.New(s.deps.Pool)
	if _, err := q.GetAgentByID(ctx, req.AgentId); isNoRows(err) {
		return notFound(), nil
	} else if err != nil {
		return nil, fmt.Errorf("agents: read the agent: %w", err)
	}
	visible := s.visibleTemplates(ctx)
	if len(visible) == 0 {
		return notPermitted(), nil
	}
	limit, offset := page(req.Params.Limit, req.Params.Offset)
	rows, err := q.ListRunsOfAgent(ctx, store.ListRunsOfAgentParams{AgentID: req.AgentId, State: state, Templates: visible, N: limit, Off: offset})
	if err != nil {
		return nil, fmt.Errorf("agents: list the runs: %w", err)
	}
	total, err := q.CountRunsOfAgent(ctx, store.CountRunsOfAgentParams{AgentID: req.AgentId, State: state, Templates: visible})
	if err != nil {
		return nil, fmt.Errorf("agents: count the runs: %w", err)
	}
	out := gen.AgentRunList{Runs: make([]gen.AgentRun, 0, len(rows)), Total: total, Limit: limit, Offset: offset}
	for _, r := range rows {
		out.Runs = append(out.Runs, runView(r))
	}
	return gen.ListAgentRuns200JSONResponse(out), nil
}

// GetAgentRun (GET /api/v1/agents/runs/{runId})
func (s *server) GetAgentRun(ctx context.Context, req gen.GetAgentRunRequestObject) (gen.GetAgentRunResponseObject, error) {
	q := store.New(s.deps.Pool)
	run, err := q.GetRun(ctx, req.RunId)
	if isNoRows(err) {
		return notFound(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("agents: read the run: %w", err)
	}
	agent, err := q.GetAgentByID(ctx, run.AgentID)
	if err != nil {
		return nil, fmt.Errorf("agents: read the run's agent: %w", err)
	}
	template, roleName := templateFrontDesk, (*string)(nil)
	if role, err := q.GetRoleByID(ctx, run.RoleID); err == nil {
		template, roleName = role.Template, &role.Name
	} else if !isNoRows(err) {
		return nil, fmt.Errorf("agents: read the run's role: %w", err)
	}
	if !s.maySee(ctx, template) {
		return notPermitted(), nil
	}

	out := gen.AgentRunDetail{Run: runView(run), Actions: []gen.AgentRunAction{}, Items: []gen.ReviewItem{}, Transcript: []gen.AgentRunMessage{}}
	if len(run.Verdict) > 0 {
		var v map[string]any
		if json.Unmarshal(run.Verdict, &v) == nil {
			out.Verdict = &v
		}
	}
	actions, err := q.ListActionsOfRun(ctx, run.ID)
	if err != nil {
		return nil, fmt.Errorf("agents: read the run's actions: %w", err)
	}
	for _, a := range actions {
		out.Actions = append(out.Actions, gen.AgentRunAction{
			Id: a.ID, OperationId: a.OperationID, Ref: a.Ref, Scoped: a.Scoped, Mode: a.Mode, Status: a.Status,
			HttpStatus: a.HttpStatus, DurationMs: a.DurationMs, ApprovedByUserId: a.ApprovedBy, CreatedAt: a.CreatedAt,
		})
	}
	items, err := q.ListReviewItemsOfRun(ctx, run.ID)
	if err != nil {
		return nil, fmt.Errorf("agents: read the run's items: %w", err)
	}
	names := itemRow{AgentName: agent.Name, RoleName: roleName, RoleTemplate: template}
	for _, it := range items {
		out.Items = append(out.Items, itemView(withNames(names, it)))
	}
	msgs, err := q.ListRunMessages(ctx, run.ID)
	if err != nil {
		return nil, fmt.Errorf("agents: read the run's transcript: %w", err)
	}
	for _, m := range msgs {
		tm := gen.AgentRunMessage{Id: m.ID, Role: m.Role, Text: m.Text, ToolCallId: m.ToolCallID, CreatedAt: m.CreatedAt}
		if len(m.ToolCalls) > 0 {
			var calls []map[string]any
			if json.Unmarshal(m.ToolCalls, &calls) == nil {
				tm.ToolCalls = &calls
			}
		}
		out.Transcript = append(out.Transcript, tm)
	}
	return gen.GetAgentRun200JSONResponse(out), nil
}

// ListAgentsAudit (GET /api/v1/agents/audit)
func (s *server) ListAgentsAudit(ctx context.Context, req gen.ListAgentsAuditRequestObject) (gen.ListAgentsAuditResponseObject, error) {
	q := store.New(s.deps.Pool)
	limit, offset := page(req.Params.Limit, req.Params.Offset)
	rows, err := q.ListEvents(ctx, store.ListEventsParams{N: limit, Off: offset})
	if err != nil {
		return nil, fmt.Errorf("agents: list the audit: %w", err)
	}
	total, err := q.CountEvents(ctx)
	if err != nil {
		return nil, fmt.Errorf("agents: count the audit: %w", err)
	}
	out := gen.AgentAuditList{Events: make([]gen.AgentAuditEvent, 0, len(rows)), Total: total, Limit: limit, Offset: offset}
	for _, e := range rows {
		out.Events = append(out.Events, gen.AgentAuditEvent{
			Id: e.ID, At: e.At, ActorUserId: e.ActorUserID, Action: e.Action, Target: e.Target, Before: objectOf(e.Before), After: objectOf(e.After),
		})
	}
	return gen.ListAgentsAudit200JSONResponse(out), nil
}
