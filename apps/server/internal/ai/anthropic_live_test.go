package ai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/ai"
	"github.com/vantigo-io/vantigo/server/internal/config"
)

// TestAnthropicLive_ATwoTurnToolConversation runs against the real API, and is
// skipped without ANTHROPIC_API_KEY. It asserts that the answer streams, that
// the model calls the tool, and that the second request — the first turn's
// thinking block replayed verbatim — comes back with no input transformation:
// the append-only history is intact.
func TestAnthropicLive_ATwoTurnToolConversation(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY is not set")
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	cfg := &config.Config{AIProvider: "anthropic", AIAPIKey: key, AIModels: []string{"claude-haiku-5-5"}, AIEffort: "low", AIFallbacks: true, AITimeout: 90 * time.Second}
	c := ai.New(cfg, http.DefaultTransport, nil, ai.WithLogger(logger))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	req := ai.ChatRequest{
		System:    "You are a terse assistant. When asked about the weather, you must call the get_weather tool, then answer in one sentence.",
		Messages:  []ai.Message{{Role: ai.RoleUser, Text: "What is the weather in Oslo?"}},
		MaxTokens: 1024,
		Tools: []ai.Tool{{
			Name: "get_weather", Description: "The current weather in a city.",
			Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
		}},
	}
	var streamed strings.Builder
	first, err := c.Chat(ctx, "live", req, func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if len(first.Message.ToolCalls) != 1 || first.Message.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("tool calls = %+v, want one get_weather", first.Message.ToolCalls)
	}
	if len(first.Message.Provider) == 0 {
		t.Fatal("no provider blocks to replay")
	}

	req.Messages = append(req.Messages, first.Message, ai.Message{Role: ai.RoleTool, ToolCallID: first.Message.ToolCalls[0].ID, Text: `{"temperature_c":4,"sky":"clear"}`})
	var answer strings.Builder
	second, err := c.Chat(ctx, "live", req, func(s string) { answer.WriteString(s) })
	if err != nil {
		t.Fatalf("second turn: %v", err)
	}
	if answer.Len() == 0 || answer.String() != second.Message.Text {
		t.Errorf("streamed %q, assembled %q: want the stream to carry the whole answer", answer.String(), second.Message.Text)
	}
	if logs.Len() != 0 {
		t.Errorf("the provider changed the request: %s", logs.String())
	}
}
