package agentloop

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/ai"
)

func TestCompactJSON_TruncatesListsAndKeepsTheCursor(t *testing.T) {
	t.Parallel()
	items := make([]any, 100)
	for i := range items {
		items[i] = map[string]any{"id": i, "name": strings.Repeat("name ", 20)}
	}
	got := CompactJSON(map[string]any{"items": items, "nextCursor": "abc", "total": 100}, 2000)
	raw, _ := json.Marshal(got)
	if len(raw) > 2000 {
		t.Fatalf("compacted to %d bytes, want at most 2000", len(raw))
	}
	m := got.(map[string]any)
	if m["nextCursor"] != "abc" || m["total"] != 100 {
		t.Errorf("the cursor and the count must survive: %s", raw)
	}
	if shown, _ := m["items_shown_of"].(string); !strings.HasSuffix(shown, " of 100") {
		t.Errorf("a truncated list must say how many there are: %s", raw)
	}
	if long := compactValue(strings.Repeat("x", 1000), 5, 0).(string); len([]rune(long)) > maxStringChars+1 {
		t.Errorf("a long string kept %d characters", len([]rune(long)))
	}
}

func TestFitContext_SummarisesOldToolResultsThenDropsOldTurns(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("r", 4000) // 1000 tokens
	history := []ai.Message{
		{Role: ai.RoleUser, Text: "first question"},
		{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{ID: "1", Name: "call_operation", Arguments: json.RawMessage(`{}`)}}},
		{Role: ai.RoleTool, ToolCallID: "1", Text: big},
		{Role: ai.RoleAssistant, Text: "first answer"},
		{Role: ai.RoleUser, Text: "second question"},
		{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{ID: "2", Name: "call_operation", Arguments: json.RawMessage(`{}`)}}},
		{Role: ai.RoleTool, ToolCallID: "2", Text: big},
	}
	// Under the budget nothing changes.
	if got := FitContext("sys", nil, history, 100000); got[2].Text != big || len(got) != len(history) {
		t.Error("a conversation under the budget must be left alone")
	}
	// Past it, the older result is summarised and the current one kept.
	got := FitContext("sys", nil, history, 1500)
	if got[2].Text == big || !strings.Contains(got[2].Text, "left out") || got[6].Text != big {
		t.Errorf("results = %q … %q, want the old one summarised and the latest kept", got[2].Text, got[6].Text[:10])
	}
	// Past that, earlier turns go, never the latest question.
	got = FitContext(strings.Repeat("s", 4000), nil, history, 1005)
	if got[0].Role != ai.RoleUser || got[0].Text != "second question" {
		t.Errorf("first message = %+v, want the second question after dropping the first turn", got[0])
	}
	if history[2].Text != big {
		t.Error("FitContext must not change its input")
	}
}
