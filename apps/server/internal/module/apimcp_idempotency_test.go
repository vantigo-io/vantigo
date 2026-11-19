package module

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The Idempotency-Key on the surface (idempotency design I15, I25): the
// integrator's document and the MCP catalogue offer it exactly when the
// router honours it, and MCP explains it.

// idempotentAccess is an mcpFakeAccess that is a contracts.IdempotencyStore,
// switched on or off. Its store is never reached here: these compositions
// mount a module's handler without the router.
type idempotentAccess struct {
	*mcpFakeAccess
	enabled bool
}

func (a idempotentAccess) IdempotencyEnabled() bool { return a.enabled }

var errNoStore = errors.New("module test: no idempotency store")

func (idempotentAccess) ClaimIdempotencyKey(context.Context, contracts.IdempotencyKey, []byte) (contracts.IdempotencyClaim, error) {
	return contracts.IdempotencyClaim{}, errNoStore
}

func (idempotentAccess) CompleteIdempotencyKey(context.Context, contracts.IdempotencyKey, uuid.UUID, contracts.IdempotencyResponse) error {
	return errNoStore
}

func (idempotentAccess) MarkIdempotencyKeyUnknown(context.Context, contracts.IdempotencyKey, uuid.UUID) error {
	return errNoStore
}

func (idempotentAccess) ReleaseIdempotencyKey(context.Context, contracts.IdempotencyKey, uuid.UUID) error {
	return errNoStore
}

func customersCatalog() map[string]contracts.Permission {
	catalog := map[string]contracts.Permission{}
	for _, m := range customersModules() {
		for _, p := range m.Permissions {
			catalog[p.Key] = p
		}
	}
	return catalog
}

// documentTakesKey reports whether operationID of the integrator's document
// body has the Idempotency-Key header.
func documentTakesKey(t *testing.T, body []byte, operationID string) bool {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string           `json:"operationId"`
			Parameters  []map[string]any `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	for _, item := range doc.Paths {
		for _, op := range item {
			if op.OperationID != operationID {
				continue
			}
			for _, p := range op.Parameters {
				if p["$ref"] == "#/components/parameters/IdempotencyKey" {
					return true
				}
			}
			return false
		}
	}
	t.Fatalf("%s is not in the document", operationID)
	return false
}

// The kill switch (I9, I25): with IDEMPOTENCY_ENABLED off, neither the
// integrator's document nor the MCP catalogue offers the header; on, both do;
// and the option is part of every cache key.
func TestIdempotency_TheSurfaceFollowsTheSwitch(t *testing.T) {
	full := mustCustomersContract(t)
	for _, enabled := range []bool{true, false} {
		docs := newAPIDocuments(idempotentAccess{newWriteAccess(), enabled}, nil, full, customersCatalog(), "")
		body, err := docs.integrator()
		if err != nil {
			t.Fatal(err)
		}
		if got := documentTakesKey(t, body, "postCustomers"); got != enabled {
			t.Errorf("enabled %v: the document offers the key on postCustomers: %v", enabled, got)
		}
		if documentTakesKey(t, body, "getCustomers") {
			t.Errorf("enabled %v: a GET offers the key", enabled)
		}
		scoped, err := docs.scoped([]string{"customers:view", "customers:create"}, false)
		if err != nil {
			t.Fatal(err)
		}
		if got := documentTakesKey(t, scoped, "postCustomers"); got != enabled {
			t.Errorf("enabled %v: a key's document offers it: %v", enabled, got)
		}
		key, _ := docs.scopeKey([]string{"customers:view"}, false)
		if strings.HasSuffix(key, " idempotency") != enabled {
			t.Errorf("enabled %v: the cache key %q", enabled, key)
		}
		index, err := newMCPCatalogue(docs).scoped([]string{"customers:view", "customers:create"}, false)
		if err != nil {
			t.Fatal(err)
		}
		if e := index.byID["postCustomers"]; e == nil || e.idempotencyKey != enabled {
			t.Errorf("enabled %v: the catalogue's postCustomers %+v", enabled, e)
		}
	}
	// An Access that is no store: nothing honours the header, nothing offers it.
	body, err := newAPIDocuments(newWriteAccess(), nil, full, customersCatalog(), "").integrator()
	if err != nil {
		t.Fatal(err)
	}
	if documentTakesKey(t, body, "postCustomers") {
		t.Error("a composition without the store offers the key")
	}
}

// MCP lists the header where the router honours it (I15): search marks the
// operation, describe_operation lists it among headerParameters, and write
// passes it through; with the switch off it is a header the operation does
// not take.
func TestMCP_TheIdempotencyKeyIsOffered(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		customers := &mcpCustomers{answer: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":7}`))
		}}
		h := composeMCP(t, idempotentAccess{newWriteAccess(), enabled}, customers, true)

		text, _ := toolText(t, callTool(t, h, mcpToolDescribe, map[string]any{"operation": "postCustomers"}))
		var described mcpDescription
		if err := json.Unmarshal([]byte(text), &described); err != nil {
			t.Fatal(err)
		}
		listed := false
		for _, p := range described.Headers {
			if p.Name == IdempotencyHeader && !p.Required && strings.Contains(p.Description, "one key per intended action") {
				listed = true
			}
		}
		if listed != enabled {
			t.Errorf("enabled %v: describe_operation lists the key: %v (%s)", enabled, listed, text)
		}

		text, _ = toolText(t, callTool(t, h, mcpToolSearch, map[string]any{"query": "create customer"}))
		if !strings.Contains(text, `"operation":"postCustomers"`) || strings.Contains(text, `"idempotencyKey":true`) != enabled {
			t.Errorf("enabled %v: search: %s", enabled, text)
		}

		text, isError := toolText(t, callTool(t, h, mcpToolWrite, map[string]any{
			"operation": "postCustomers", "body": map[string]any{"name": "x"}, "headers": map[string]any{"idempotency-key": "k-1"},
		}))
		if enabled {
			if isError || text != `{"id":7}` || len(customers.requests) != 1 || customers.requests[0].Header.Get(IdempotencyHeader) != "k-1" {
				t.Errorf("enabled: write %v %s", isError, text)
			}
		} else if !isError || !strings.Contains(text, "takes no header") || len(customers.requests) != 0 {
			t.Errorf("disabled: write %v %s", isError, text)
		}
	}
}

// What MCP says of the key (I15): write, delete and the instructions say one
// key per intended action, reused for every retry; a replay is said to be one;
// each refusal says what to do next; an unanswered write that took a key says
// to retry with it.
func TestMCP_TheIdempotencyKeyIsExplained(t *testing.T) {
	for _, text := range []string{mcpInstructions, mcpTools(true)[3].Description, mcpTools(true)[4].Description} {
		for _, want := range []string{"mint one key", "same key", "never a new key for a retry", "idempotentReplayed",
			"idempotency_in_progress", "idempotency_outcome_unknown", "read the record, then", "idempotency_key_reused"} {
			if !strings.Contains(text, want) {
				t.Errorf("%q does not say %q", text, want)
			}
		}
	}

	answer := func(status int, header, body string) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if header != "" {
				w.Header().Set(idempotencyReplayedHeader, header)
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}
	}
	call := func(h http.Handler) (string, bool) {
		return toolText(t, callTool(t, h, mcpToolWrite, map[string]any{
			"operation": "postCustomers", "body": map[string]any{"name": "x"}, "headers": map[string]any{"Idempotency-Key": "k-1"},
		}))
	}

	customers := &mcpCustomers{answer: answer(http.StatusCreated, "true", `{"id":7}`)}
	text, isError := call(composeMCP(t, idempotentAccess{newWriteAccess(), true}, customers, true))
	if isError || text != `{"idempotentReplayed":true,"status":201,"response":{"id":7}}` {
		t.Errorf("a replay: %v %s", isError, text)
	}

	for code, want := range map[string]string{
		codeIdempotencyUnknown:    "read the record, then use a new key",
		codeIdempotencyInProgress: "retry shortly with the same key",
	} {
		customers := &mcpCustomers{answer: answer(http.StatusConflict, "", `{"error":{"code":"`+code+`","message":"m"}}`)}
		text, isError := call(composeMCP(t, idempotentAccess{newWriteAccess(), true}, customers, true))
		if !isError || !strings.Contains(text, `"code":"`+code+`"`) || !strings.Contains(text, want) {
			t.Errorf("%s: %v %s", code, isError, text)
		}
	}
	customers = &mcpCustomers{answer: answer(http.StatusUnprocessableEntity, "", `{"error":{"code":"idempotency_key_reused","message":"m"}}`)}
	if text, isError := call(composeMCP(t, idempotentAccess{newWriteAccess(), true}, customers, true)); !isError || !strings.Contains(text, "a new action takes a new key") {
		t.Errorf("reused: %v %s", isError, text)
	}

	shortTimeout(t)
	customers = &mcpCustomers{answer: func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }}
	text, isError = call(composeMCP(t, idempotentAccess{newWriteAccess(), true}, customers, true))
	if !isError || !strings.Contains(text, "may or may not have been made") || !strings.Contains(text, "same Idempotency-Key, never a new one") {
		t.Errorf("unanswered: %v %s", isError, text)
	}
}
