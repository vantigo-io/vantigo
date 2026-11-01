package assistant

import (
	"encoding/json"

	"github.com/vantigo-io/vantigo/server/internal/assistant/gen"
	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
)

// messageView is a stored message as the contract answers it. A row whose JSON
// does not decode (it was written by this module, so it should) answers no
// parts rather than failing the whole conversation.
func messageView(m store.AssistantMessage) gen.AssistantMessage {
	out := gen.AssistantMessage{
		Id: m.ID, Role: gen.AssistantMessageRole(m.Role), RunId: m.RunID, Parts: []gen.AssistantPart{},
		Model: m.Model, InputTokens: m.InputTokens, OutputTokens: m.OutputTokens, CreatedAt: m.CreatedAt,
	}
	_ = json.Unmarshal(m.Content, &out.Parts)
	if out.Parts == nil {
		out.Parts = []gen.AssistantPart{}
	}
	if len(m.Context) > 0 {
		var c gen.AssistantMessageContext
		if json.Unmarshal(m.Context, &c) == nil {
			out.Context = &c
		}
	}
	return out
}

func runView(r store.AssistantRun) gen.AssistantRun {
	out := gen.AssistantRun{
		Id: r.ID, ConversationId: r.ConversationID, State: gen.AssistantRunState(r.State), ToolCalls: r.ToolCalls,
		StartedAt: r.StartedAt, EndedAt: r.EndedAt, ErrorCode: r.ErrorCode, StopRequested: r.StopRequested,
	}
	return out
}

func actionView(a store.AssistantAction) gen.AssistantAction {
	return gen.AssistantAction{
		Id: a.ID, RunId: a.RunID, OperationId: a.OperationID, Mode: a.Mode, HttpStatus: a.HttpStatus,
		DurationMs: a.DurationMs, CreatedAt: a.CreatedAt,
	}
}
