package agents

// The hand-off (virtual employees design V13, V15): an agent that cannot or may
// not answer hands the conversation to the people. A hand-off is a marker that
// keeps the trigger from waking the agent again (agents.handed_off), a note on
// the conversation that says why, and the conversation assigned to the role's
// hand-off person, or the assignee cleared. The marker and the run's state are
// this module's own rows, written anywhere; the note and the assignee are
// Communications', reached through the dispatcher as the agent, so they are done
// only in the API process: a run handed off by a worker or by the trigger waits
// with no outcome until the runner completes it (finishHandOffs).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/agents/gen"
	"github.com/vantigo-io/vantigo/server/internal/agents/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// The reasons a run request is made for.
const (
	reasonMessage  = "message"
	reasonAssigned = "assigned"
	reasonRetry    = "retry"
)

// The states of a run (V14) and the outcomes it ends in.
const (
	runQueued        = "queued"
	runRunning       = "running"
	runDone          = "done"
	runWaitingReview = "waiting_review"
	runHandedOff     = "handed_off"
	runFailed        = "failed"
	runStopped       = "stopped"

	outcomeSent     = "sent"
	outcomeReview   = "review"
	outcomeHandedOf = "handed_off"
	outcomeNoReply  = "no_reply"
)

// The reasons a conversation is handed off, as the marker and the note name them.
const (
	handUnreadable  = "unreadable"
	handBudget      = "budget"
	handGuard       = "guard"
	handLapsed      = "lapsed"
	handStale       = "stale"
	handAI          = "ai_unavailable"
	handCap         = "cap"
	handAgent       = "hand_off"
	handDiscarded   = "discarded"
	handExpired     = "expired"
	handWithdrawn   = "withdrawn"
	handNoReply     = "no_reply"
	handUnknownSend = "send_unknown"
)

// markHandedOff records a hand-off that has no run in progress: a run that never
// started, a run row handed off whose note and assignee the API process will do,
// and the marker that keeps the trigger away. It also drops the conversation's
// waiting requests.
func markHandedOff(ctx context.Context, q *store.Queries, agentID, roleID, conversationID uuid.UUID, code, summary string, now time.Time) error {
	if err := q.InsertHandedOffRun(ctx, store.InsertHandedOffRunParams{
		ID: uuid.New(), AgentID: agentID, RoleID: roleID, ConversationID: conversationID, Reason: reasonMessage,
		ErrorCode: &code, Summary: &summary, Now: now,
	}); err != nil {
		return fmt.Errorf("agents: record a hand-off: %w", err)
	}
	return writeMarker(ctx, q, roleID, conversationID, code, now)
}

// handOffAtOnce is markHandedOff for the trigger.
func handOffAtOnce(ctx context.Context, q *store.Queries, role store.RoleOfChannelRow, conversationID uuid.UUID, code, summary string, now time.Time) error {
	return markHandedOff(ctx, q, role.AgentID, role.RoleID, conversationID, code, summary, now)
}

// writeMarker writes the hand-off marker and drops the requests that wait.
func writeMarker(ctx context.Context, q *store.Queries, roleID, conversationID uuid.UUID, code string, now time.Time) error {
	if err := q.UpsertHandedOff(ctx, store.UpsertHandedOffParams{ConversationID: conversationID, RoleID: roleID, At: now, Reason: code}); err != nil {
		return fmt.Errorf("agents: mark the conversation handed off: %w", err)
	}
	if err := q.DeleteUntakenRequestsOfConversation(ctx, conversationID); err != nil {
		return fmt.Errorf("agents: drop the waiting requests: %w", err)
	}
	return nil
}

// GetConversationHandOff (GET /api/v1/agents/conversations/{conversationId}/hand-off)
func (s *server) GetConversationHandOff(ctx context.Context, req gen.GetConversationHandOffRequestObject) (gen.GetConversationHandOffResponseObject, error) {
	row, err := store.New(s.deps.Pool).GetHandedOff(ctx, req.ConversationId)
	if isNoRows(err) {
		return gen.GetConversationHandOff200JSONResponse(gen.ConversationHandOff{}), nil
	}
	if err != nil {
		return nil, fmt.Errorf("agents: read the hand-off: %w", err)
	}
	return gen.GetConversationHandOff200JSONResponse(gen.ConversationHandOff{HandedOff: true, At: &row.At, Reason: &row.Reason, RoleId: &row.RoleID}), nil
}

// GiveBackConversation (DELETE /api/v1/agents/conversations/{conversationId}/hand-off)
//
// Clears the marker, so the agent answers the next message. A conversation that
// is not handed off is left as it is.
func (s *server) GiveBackConversation(ctx context.Context, req gen.GiveBackConversationRequestObject) (gen.GiveBackConversationResponseObject, error) {
	if _, err := store.New(s.deps.Pool).DeleteHandedOff(ctx, req.ConversationId); err != nil {
		return nil, fmt.Errorf("agents: give the conversation back: %w", err)
	}
	return gen.GiveBackConversation204Response{}, nil
}

// handOffNote is the internal note a hand-off writes: what the agent says, and
// why. It names the agent, so a colleague reading the thread knows who handed off.
func handOffNote(agentName, summary string, personHasIt bool) string {
	if personHasIt {
		return fmt.Sprintf("%s stopped working on this conversation. %s", agentName, summary)
	}
	return fmt.Sprintf("%s handed this conversation to a colleague. %s", agentName, summary)
}

// callAs calls an operation as the agent of grantID, confirmed: the module's own
// tools call the writes they are, and what the model asks for goes through the
// review.
func (s *server) callAs(ctx context.Context, grantID uuid.UUID, req operations.CallRequest) (operations.CallResult, error) {
	req.Confirmed = true
	return s.deps.Operations.Call(contracts.WithGrant(ctx, contracts.GrantRef{Kind: contracts.GrantAgent, ID: grantID}), req)
}

// handOffEffects does what a hand-off does in Communications: the note, and the
// assignee. The role's hand-off person is the assignee when they can still be
// handed a conversation; otherwise, or with none, the assignee is cleared when it
// is the agent. A step that fails is logged and does not stop the next, because
// the marker already keeps the agent away and a person finds the conversation in
// the Inbox.
func (s *server) handOffEffects(ctx context.Context, grantID uuid.UUID, agent store.AgentsAgent, role store.AgentsRole, conversationID uuid.UUID, assigned *uuid.UUID, summary string) {
	// A person who has the conversation keeps it: the agent never takes a conversation from
	// them, whatever the cause of the hand-off (a colleague took it, a reply was withdrawn).
	personHasIt := assigned != nil && *assigned != agent.UserID
	body, _ := json.Marshal(map[string]string{"textBody": handOffNote(agent.Name, summary, personHasIt)})
	if res, err := s.callAs(ctx, grantID, operations.CallRequest{
		OperationID: opNote, PathParams: map[string]any{"id": conversationID.String()}, Body: body,
	}); err != nil || res.Status >= 300 {
		s.warn("agents: the hand-off note was not written", "conversation", conversationID, "status", res.Status, "err", err)
	}

	if personHasIt {
		return
	}
	var target *uuid.UUID
	if role.HandOffUserID != nil {
		warnings, err := s.handOffWarnings(ctx, []store.AgentsRole{role})
		if err != nil {
			s.warn("agents: the hand-off person could not be checked", "err", err)
		} else if _, bad := warnings[*role.HandOffUserID]; !bad {
			target = role.HandOffUserID
		}
	}
	if target == nil && (assigned == nil || *assigned != agent.UserID) {
		return // already in the Inbox, unassigned or a person's
	}
	if target != nil && assigned != nil && *target == *assigned {
		return
	}
	payload := map[string]any{"assignedUserId": nil}
	if target != nil {
		payload["assignedUserId"] = target.String()
	}
	body, _ = json.Marshal(payload)
	if res, err := s.callAs(ctx, grantID, operations.CallRequest{
		OperationID: opPatch, PathParams: map[string]any{"id": conversationID.String()}, Body: body,
	}); err != nil || res.Status >= 300 {
		s.warn("agents: the conversation was not assigned in the hand-off", "conversation", conversationID, "status", res.Status, "err", err)
	}
}

// finishHandOffs completes the hand-offs the trigger, the budget and the worker
// left with no outcome: it issues a grant for the run, reads who has the
// conversation, does the effects and records the outcome.
func (s *server) finishHandOffs(ctx context.Context) error {
	q := store.New(s.deps.Pool)
	runs, err := q.ListRunsToHandOff(ctx, 20)
	if err != nil {
		return fmt.Errorf("agents: list the hand-offs to finish: %w", err)
	}
	for _, run := range runs {
		if err := s.finishHandOff(ctx, q, run); err != nil {
			s.warn("agents: a hand-off could not be finished", "run", run.ID, "err", err)
		}
	}
	return nil
}

func (s *server) finishHandOff(ctx context.Context, q *store.Queries, run store.AgentsRun) error {
	agent, err := q.GetAgentByID(ctx, run.AgentID)
	if err != nil {
		return err
	}
	role, err := q.GetRoleByID(ctx, run.RoleID)
	if isNoRows(err) {
		return q.SetRunOutcome(ctx, store.SetRunOutcomeParams{ID: run.ID, Outcome: ptr(outcomeHandedOf)}) // the role is gone
	}
	if err != nil {
		return err
	}
	grant, err := s.deps.AgentGrants.Issue(ctx, contracts.IssueAgentGrant{AgentUserID: agent.UserID, AgentID: agent.ID, RunID: run.ID})
	if errors.Is(err, contracts.ErrAgentInactive) || errors.Is(err, contracts.ErrNotAgent) {
		// A deleted agent cannot write a note or give up the assignee; the marker keeps
		// the trigger away, and that is all there is left to do.
		return q.SetRunOutcome(ctx, store.SetRunOutcomeParams{ID: run.ID, Outcome: ptr(outcomeHandedOf)})
	}
	if err != nil {
		return err
	}
	defer func() { _ = s.deps.AgentGrants.Revoke(context.WithoutCancel(ctx), grant) }()

	var assigned *uuid.UUID
	if res, err := s.callAs(ctx, grant, operations.CallRequest{OperationID: opGetConversation, PathParams: map[string]any{"id": run.ConversationID.String()}}); err == nil && res.Status == 200 {
		if c, err := parseConversation(res.Body); err == nil {
			assigned = c.AssignedUserID
			s.noteLinks(ctx, c)
		}
	}
	summary := ""
	if run.Summary != nil {
		summary = *run.Summary
	}
	s.handOffEffects(ctx, grant, agent, role, run.ConversationID, assigned, summary)
	return q.SetRunOutcome(ctx, store.SetRunOutcomeParams{ID: run.ID, Outcome: ptr(outcomeHandedOf)})
}

// ptr is a pointer to a copy of v.
func ptr[T any](v T) *T { return &v }
