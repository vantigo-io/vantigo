package assistant

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/ai"
)

// Tool results are sized for small context windows (the Assistant design S7).
const (
	// toolResultMaxChars bounds one tool result as the model sees it: about
	// 1,500 tokens.
	toolResultMaxChars = 6000
	// maxStringChars bounds one string in a result.
	maxStringChars = 300
	// charsPerToken is the rough estimate behind AI_CONTEXT_TOKENS.
	charsPerToken = 4
)

// compactJSON shrinks a decoded JSON value to fit max characters once
// encoded: strings are cut, a list is truncated with a note of how many it
// has, and the keys that are not lists are kept. A list is cut to 20 items
// first, then to 10, 5 and 2 while the result is still too big. The note on a
// truncated list is a sibling key "<key>_shown_of", so a cursor beside it
// (nextCursor, cursor, more) survives to tell the model there is more.
func compactJSON(v any, max int) any {
	for _, limit := range []int{20, 10, 5, 2, 1} {
		out := compactValue(v, limit, 0)
		if enc, err := json.Marshal(out); err == nil && len(enc) <= max {
			return out
		}
	}
	enc, _ := json.Marshal(compactValue(v, 1, 0))
	return map[string]any{"truncated": true, "preview": string([]rune(string(enc))[:min(max, utf8.RuneCount(enc))])}
}

func compactValue(v any, listLimit, depth int) any {
	switch x := v.(type) {
	case string:
		if utf8.RuneCountInString(x) > maxStringChars {
			return string([]rune(x)[:maxStringChars]) + "…"
		}
		return x
	case []any:
		out := make([]any, 0, min(len(x), listLimit))
		for i, item := range x {
			if i >= listLimit {
				break
			}
			out = append(out, compactValue(item, listLimit, depth+1))
		}
		return out
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(x))
		for _, k := range keys {
			val := x[k]
			if list, ok := val.([]any); ok && len(list) > listLimit {
				out[k+"_shown_of"] = fmt.Sprintf("%d of %d", listLimit, len(list))
			}
			out[k] = compactValue(val, listLimit, depth+1)
		}
		return out
	}
	return v
}

// compactBody compacts a response body that is JSON, and keeps anything else
// as a bounded string.
func compactBody(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	if list, ok := v.([]any); ok {
		// A top-level list is wrapped, so its truncation can be said.
		v = map[string]any{"items": list}
	}
	return compactJSON(v, toolResultMaxChars)
}

// estimateTokens is the rough size of a string in tokens.
func estimateTokens(s string) int { return (len(s) + charsPerToken - 1) / charsPerToken }

// fitContext makes the conversation fit the context budget (S7). Older tool
// results are replaced by a one-line summary, oldest first, once the
// conversation passes the budget; if that is not enough, whole earlier turns
// are dropped, oldest first, keeping the latest user message and what follows.
// The system prompt and the tools' schemas count against the budget.
func fitContext(system string, tools []ai.Tool, messages []ai.Message, budgetTokens int) []ai.Message {
	size := func(ms []ai.Message) int {
		n := estimateTokens(system)
		for _, t := range tools {
			n += estimateTokens(t.Name) + estimateTokens(t.Description) + estimateTokens(string(t.Parameters))
		}
		for _, m := range ms {
			n += estimateTokens(m.Text) + 4
			for _, c := range m.ToolCalls {
				n += estimateTokens(string(c.Arguments)) + estimateTokens(c.Name)
			}
		}
		return n
	}
	out := append([]ai.Message(nil), messages...)
	if size(out) <= budgetTokens {
		return out
	}

	// The tool results of the latest turn (after the last user message) are
	// what the model is working from; older ones are summarised first.
	lastUser := -1
	for i, m := range out {
		if m.Role == ai.RoleUser {
			lastUser = i
		}
	}
	for i := range out {
		if size(out) <= budgetTokens {
			return out
		}
		if out[i].Role == ai.RoleTool && i < lastUser && len(out[i].Text) > 120 {
			out[i].Text = fmt.Sprintf("[an earlier result, %d characters, left out to save room]", len(out[i].Text))
		}
	}
	for i := range out {
		if size(out) <= budgetTokens {
			return out
		}
		if out[i].Role == ai.RoleTool && len(out[i].Text) > 120 {
			out[i].Text = fmt.Sprintf("[a result, %d characters, left out to save room]", len(out[i].Text))
		}
	}
	// Drop the oldest whole turns: from the first message up to the next user
	// message that is not the last.
	for size(out) > budgetTokens {
		next := -1
		for i := 1; i <= lastUser; i++ {
			if out[i].Role == ai.RoleUser {
				next = i
				break
			}
		}
		if next < 0 {
			break
		}
		out = out[next:]
		lastUser -= next
	}
	return out
}

// truncateTitle makes a conversation's title from its first question.
func truncateTitle(text string) string {
	t := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(t) <= titleFromQuestion {
		return t
	}
	cut := []rune(t)[:titleFromQuestion]
	i := len(cut)
	for i > titleFromQuestion/2 && cut[i-1] != ' ' {
		i--
	}
	return strings.TrimSpace(string(cut[:i])) + "…"
}
