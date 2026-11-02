package assistant

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/ai"
	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
)

// Simple compaction (the Assistant design S18). A provider whose history
// carries signed thinking cannot have a stored turn shortened in place: every
// thinking block after the change would be refused. When the conversation
// outgrows AI_CONTEXT_TOKENS the next request is instead started from a
// summary of everything before the person's new message, and replays nothing
// else from before it: no earlier turn, no earlier thinking block. The summary
// is kept on the conversation as a marker, so the turns after it are sent
// as they were stored, append-only, until the next compaction. The stored
// messages are never touched, and the person still sees all of them.

const (
	// codeAIRefused is the run's error code when the provider's safeguards
	// declined to answer.
	codeAIRefused = "ai_refused"

	// summaryAnswerTokens bounds a summary.
	summaryAnswerTokens = 2048
	// transcriptMaxChars bounds what the summariser is shown: the newest
	// part of the conversation when it is longer, after the earlier summary.
	transcriptMaxChars = 400_000
	// transcriptItemChars bounds one tool call's arguments or one result in
	// that transcript.
	transcriptItemChars = 600
)

const summarySystem = `You write the memory of a conversation between a person and the Assistant in Vantigo, a business application. The conversation is shown to you as a transcript. Your summary replaces it: the Assistant continues from your summary alone, and anything you leave out is forgotten.

Write the summary in the language the person uses, as plain notes. Keep:
- what the person asked for, and what is still open or unanswered;
- every decision the person made, and every change the Assistant made, proposed, was refused or was cancelled, with the record it concerned (name and id or route) and whether it was done;
- the records, ids, routes, names, numbers and dates the conversation is about, exactly as written;
- what the person prefers or told the Assistant to remember;
- what the Assistant found out that it would otherwise have to look up again.
Leave out greetings, pleasantries and tool mechanics. Never invent anything, and never state a change as made unless the transcript says it was. Write only the summary.`

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

// summaryMessage is the summary as the model reads it, at the head of the turns
// that follow it.
func summaryMessage(summary string) string {
	return "[Context from the application, not the person's words. The conversation so far was summarised to save room. The summary follows.]\n\n" + summary
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
		history = append([]ai.Message{{Role: ai.RoleUser, Text: summaryMessage(*state.Summary)}}, history...)
	}
	if contextSize(system, toolDefs, history) <= budget {
		return state
	}

	older := visible[:len(visible)-1]
	transcript := renderTranscript(state.Summary, modelMessages(older, actions, loc))
	resp, err := r.s.deps.AI.Chat(r.ctx, aiTaskChat, ai.ChatRequest{
		System:    summarySystem,
		Messages:  []ai.Message{{Role: ai.RoleUser, Text: "Summarise this conversation.\n\n<transcript>\n" + transcript + "\n</transcript>"}},
		MaxTokens: summaryAnswerTokens,
	}, nil)
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

// renderTranscript is the conversation as plain text for the summariser: the
// earlier summary, then each turn, tool calls and results cut short. When it
// is longer than the bound the oldest part is dropped.
func renderTranscript(earlier *string, msgs []ai.Message) string {
	var lines []string
	if earlier != nil {
		lines = append(lines, "Earlier summary: "+*earlier)
	}
	for _, m := range msgs {
		switch m.Role {
		case ai.RoleUser:
			lines = append(lines, "Person: "+m.Text)
		case ai.RoleAssistant:
			if m.Text != "" {
				lines = append(lines, "Assistant: "+m.Text)
			}
			for _, c := range m.ToolCalls {
				lines = append(lines, "Assistant called "+c.Name+" with "+clipItem(string(c.Arguments)))
			}
		case ai.RoleTool:
			lines = append(lines, "Result: "+clipItem(m.Text))
		}
	}
	total, from := 0, len(lines)
	for from > 0 && total+len(lines[from-1]) <= transcriptMaxChars {
		from--
		total += len(lines[from]) + 1
	}
	return strings.Join(lines[from:], "\n")
}

func clipItem(s string) string {
	if utf8.RuneCountInString(s) <= transcriptItemChars {
		return s
	}
	return string([]rune(s)[:transcriptItemChars]) + "…"
}
