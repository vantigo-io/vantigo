package assistant

import (
	"fmt"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/agentloop"
	"github.com/vantigo-io/vantigo/server/internal/ai"
	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
)

// Simple compaction (the Assistant design S18) is agentloop's: the summary, the
// request it is made with and the summary message. What stays here is where the
// summary is kept: a marker on the conversation, over the stored messages.

// afterSummary is the messages a request still carries: those after the ones a
// summary stands for.
func afterSummary(rows []store.AssistantMessage, state store.AssistantConversationState) []store.AssistantMessage {
	if state.SummaryThroughSeq == nil {
		return rows
	}
	for i, row := range rows {
		if row.Seq != nil && *row.Seq > *state.SummaryThroughSeq {
			return rows[i:]
		}
	}
	return nil
}

// compact summarises the conversation when it has outgrown the budget and the
// person's new message opens a turn, and answers the state with the new marker.
// It never runs in the middle of a tool round: the newest stored message must
// be the person's own, so every tool call before it has its result. A summary
// that cannot be made leaves the state as it was, and the request goes on
// uncompacted.
func (r *runner) compact(q *store.Queries, state store.AssistantConversationState, rows []store.AssistantMessage, actions []store.AssistantPendingAction, system string, loc *time.Location, budget int) store.AssistantConversationState {
	visible := afterSummary(rows, state)
	if len(visible) < 2 || visible[len(visible)-1].Role != "user" {
		return state
	}
	history := modelMessages(visible, actions, loc)
	if state.Summary != nil {
		history = append([]ai.Message{{Role: ai.RoleUser, Text: agentloop.SummaryMessage(*state.Summary)}}, history...)
	}
	if agentloop.ContextSize(system, toolDefs, history) <= budget {
		return state
	}

	older := visible[:len(visible)-1]
	resp, err := agentloop.Summarise(r.ctx, r.s.deps.AI, aiTaskChat, state.Summary, modelMessages(older, actions, loc))
	if err != nil {
		r.s.deps.Logger.Warn("assistant: the conversation could not be summarised", "run", r.runID, "error_type", fmt.Sprintf("%T", err))
		return state
	}
	r.s.countTokens(r.persist(), r.userID, resp.InputTokens, resp.OutputTokens)
	summary := strings.TrimSpace(resp.Message.Text)
	if summary == "" {
		return state
	}
	seq := older[len(older)-1].Seq
	if seq == nil {
		return state
	}
	through := *seq
	if err := q.SetConversationSummary(r.persist(), store.SetConversationSummaryParams{
		ConversationID: r.convID, Summary: &summary, ThroughSeq: &through, Now: r.s.deps.Clock(),
	}); err != nil {
		r.s.deps.Logger.Error("assistant: store the summary", "run", r.runID, "error", err)
		return state
	}
	state.Summary, state.SummaryThroughSeq = &summary, &through
	r.s.deps.Logger.Info("assistant: a conversation was summarised", "run", r.runID, "through", through)
	return state
}
