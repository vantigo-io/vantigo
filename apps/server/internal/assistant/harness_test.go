package assistant_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/assistant"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The Owner's role: its id is fixed by identity's baseline.
const ownerRoleID = "00000000-0000-4000-8000-000000000002"

// newHarness is an installation with the assistant module and an AI provider
// replaced by model, a local one (http://ollama:11434/v1) so the data notice
// is not needed.
func newHarness(t *testing.T, model *modtest.FakeChatModel, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(assistant.Module()),
		modtest.WithEnv("AI_PROVIDER", "openai-compatible"),
		modtest.WithEnv("AI_BASE_URL", "http://ollama:11434/v1"),
		modtest.WithEnv("AI_MODELS", "test-model"),
		modtest.WithEnv("OWNERS_REQUIRE_MFA", "0"),
	}
	if model != nil {
		base = append(base, modtest.WithTransport(model))
	}
	return modtest.New(t, append(base, opts...)...)
}

// owner is a signed-in Owner.
func owner(t *testing.T, h *modtest.Harness) *modtest.Client {
	t.Helper()
	c, id := h.SignInUser(t)
	h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, id, ownerRoleID)
	return c
}

// person is a signed-in user with no permissions at all.
func person(t *testing.T, h *modtest.Harness) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return h.SignInUser(t)
}

type conversation struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	OriginApp      string `json:"originApp"`
	OriginRoute    string `json:"originRoute"`
	Pinned         bool   `json:"pinned"`
	Archived       bool   `json:"archived"`
	Running        bool   `json:"running"`
	LastActivityAt string `json:"lastActivityAt"`
}

func startConversation(t *testing.T, c *modtest.Client) conversation {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/assistant/conversations", map[string]any{"origin": map[string]any{"app": "customers", "route": "/customers"}})
	if r.Status != http.StatusCreated {
		t.Fatalf("create a conversation: %d %s", r.Status, r.Body)
	}
	var conv conversation
	r.JSON(&conv)
	return conv
}

// streamEvent is one server-sent event of a run.
type streamEvent struct {
	Name string
	Data json.RawMessage
}

// parseStream reads a text/event-stream body.
func parseStream(body []byte) []streamEvent {
	var out []streamEvent
	for _, frame := range bytes.Split(body, []byte("\n\n")) {
		var ev streamEvent
		for _, line := range strings.Split(string(frame), "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.Name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.Data = json.RawMessage(strings.TrimPrefix(line, "data: "))
			}
		}
		if ev.Name != "" {
			out = append(out, ev)
		}
	}
	return out
}

func names(events []streamEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Name
	}
	return out
}

// ask sends a message and answers the response, read to the end.
func ask(t *testing.T, c *modtest.Client, conversationID, text string, opts ...modtest.RequestOption) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPost, "/api/v1/assistant/conversations/"+conversationID+"/messages", map[string]any{"text": text}, opts...)
}

type message struct {
	ID    string `json:"id"`
	Role  string `json:"role"`
	RunID string `json:"runId"`
	Parts []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Name    string `json:"name"`
		Status  string `json:"status"`
		Content string `json:"content"`
		Route   string `json:"route"`
		Label   string `json:"label"`
	} `json:"parts"`
	Model        string `json:"model"`
	InputTokens  int    `json:"inputTokens"`
	OutputTokens int    `json:"outputTokens"`
}

type detail struct {
	Conversation conversation `json:"conversation"`
	Messages     []message    `json:"messages"`
	ActiveRun    *struct {
		ID    string `json:"id"`
		State string `json:"state"`
	} `json:"activeRun"`
	LastRun *struct {
		State     string `json:"state"`
		ErrorCode string `json:"errorCode"`
	} `json:"lastRun"`
	Actions []struct {
		OperationID string `json:"operationId"`
		Mode        string `json:"mode"`
		HTTPStatus  *int   `json:"httpStatus"`
	} `json:"actions"`
}

func getConversation(t *testing.T, c *modtest.Client, id string) detail {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/assistant/conversations/"+id, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get the conversation: %d %s", r.Status, r.Body)
	}
	var d detail
	r.JSON(&d)
	return d
}

func moduleForTests() module.Module { return assistant.Module() }
