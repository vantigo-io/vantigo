package integration_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// MCP's write with an Idempotency-Key end to end (idempotency design I15, §7
// B): describe_operation lists the header the router honours, a write with a
// key creates once, its retry with the same key is the first answer replayed
// and said to be one, logged idempotent_replay, and the same key with another
// body is refused with what to do next.
func TestMCP_AWriteWithAKeyIsReplayed(t *testing.T) {
	h := mcpHarness(t)
	integration := h.Integration(t, "Agent", "customers:view", "customers:create")
	setMCPAccess(t, h, integration, "read_write")
	key, _ := h.APIKeyFor(t, integration, modtest.APIKey{})
	c := h.APIKeyClient(t, key)

	_, text, isError := mcpTool(t, c, "describe_operation", map[string]any{"operation": "postCustomers"})
	if isError || !strings.Contains(text, `"headerParameters":[{"name":"Idempotency-Key"`) {
		t.Fatalf("describe: %v %s", isError, text)
	}

	write := func(name string) (string, bool) {
		_, text, isError := mcpTool(t, c, "write", map[string]any{
			"operation": "postCustomers", "body": map[string]any{"name": name},
			"headers": map[string]any{"Idempotency-Key": "mcp-retry-1"},
		})
		return text, isError
	}
	first, isError := write("Fjordkraft AS")
	if isError || !strings.Contains(first, `"customerNumber"`) {
		t.Fatalf("the first write: %v %s", isError, first)
	}
	again, isError := write("Fjordkraft AS")
	var replay struct {
		Replayed bool            `json:"idempotentReplayed"`
		Status   int             `json:"status"`
		Response json.RawMessage `json:"response"`
	}
	if isError || json.Unmarshal([]byte(again), &replay) != nil || !replay.Replayed || replay.Status != 201 || string(replay.Response) != first {
		t.Fatalf("the retry: %v %s, want the first answer %s replayed", isError, again, first)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE name = 'Fjordkraft AS'`); n != 1 {
		t.Errorf("%d customers created, want 1", n)
	}
	replays := 0
	for _, row := range mcpLog(t, h, integration) {
		if row.operationID == "postCustomers" && row.reason == "idempotent_replay" {
			replays++
		}
	}
	if replays != 1 {
		t.Errorf("%d replays logged, want 1", replays)
	}

	text, isError = write("Another AS")
	if !isError || !strings.Contains(text, `"code":"idempotency_key_reused"`) || !strings.Contains(text, "a new action takes a new key") {
		t.Errorf("the key with another body: %v %s", isError, text)
	}
}

// With IDEMPOTENCY_ENABLED off nothing honours the header, so MCP does not
// offer it: describe_operation leaves it out and write refuses it (I9, I25).
func TestMCP_TheKeyIsNotOfferedWithTheSwitchOff(t *testing.T) {
	h := mcpHarness(t, modtest.WithEnv("IDEMPOTENCY_ENABLED", "0"))
	integration := h.Integration(t, "Agent", "customers:view", "customers:create")
	setMCPAccess(t, h, integration, "read_write")
	key, _ := h.APIKeyFor(t, integration, modtest.APIKey{})
	c := h.APIKeyClient(t, key)

	if _, text, isError := mcpTool(t, c, "describe_operation", map[string]any{"operation": "postCustomers"}); isError || strings.Contains(text, "Idempotency-Key") {
		t.Errorf("describe: %v %s", isError, text)
	}
	_, text, isError := mcpTool(t, c, "write", map[string]any{
		"operation": "postCustomers", "body": map[string]any{"name": "Fjordkraft AS"},
		"headers": map[string]any{"Idempotency-Key": "k"},
	})
	if !isError || !strings.Contains(text, "takes no header") {
		t.Errorf("write: %v %s", isError, text)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers`); n != 0 {
		t.Errorf("%d customers created", n)
	}
}
