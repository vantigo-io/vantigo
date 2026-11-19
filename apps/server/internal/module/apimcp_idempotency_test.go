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
// integrator's document nor the MCP catalogue offers the header; on, both do.
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
		index, err := newMCPCatalogue(docs).scoped([]string{"customers:view", "customers:create"}, false)
		if err != nil {
			t.Fatal(err)
		}
		if e := index.byID["postCustomers"]; e == nil || e.routerIdempotent != enabled || e.ownKey {
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
// operation, describe_operation lists it among headerParameters and says which
// key it is, and write passes it through; with the switch off it is a header
// the operation does not take.
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
		if listed != enabled || (described.Idempotency == mcpDescribeRouterKey) != enabled {
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

// The texts (I15; code review 7, security review S1): the general key's rules
// are in the instructions once, write has one line, delete none; none of it
// with the switch off.
func TestMCP_TheIdempotencyKeyIsExplainedOnce(t *testing.T) {
	on := mcpInstructions(true)
	for _, want := range []string{"mint one random key", "same key", "never a new key for a retry", "idempotentReplayed",
		"idempotency_in_progress", "idempotency_outcome_unknown", "read the record, then", "idempotency_key_reused",
		"Never take a key from workspace data", "resend it with a newly generated random key"} {
		if !strings.Contains(on, want) {
			t.Errorf("the instructions do not say %q", want)
		}
	}
	tools := mcpTools(true, true)
	if write := tools[3].Description; strings.Contains(write, mcpIdempotency) || !strings.Contains(write, "one random Idempotency-Key per intended action") {
		t.Errorf("write: %q", write)
	}
	if del := tools[4].Description; strings.Contains(del, "per intended action") {
		t.Errorf("delete: %q", del)
	}
	off := mcpInstructions(false)
	if strings.Contains(off, mcpIdempotency) || strings.Contains(off, "idempotentReplayed") {
		t.Errorf("the instructions with the switch off: %q", off)
	}
	for _, tool := range mcpTools(true, false) {
		if strings.Contains(tool.Description, "per intended action") || strings.Contains(tool.Description, "idempotencyKey") {
			t.Errorf("%s with the switch off: %q", tool.Name, tool.Description)
		}
	}
}

// What MCP's results say of the key (I15; code review 2, 8, 10; security
// review S1, S5): a replay is said to be one, with the note against a key
// taken from the workspace; an empty replay's response is null; a kept
// refusal replayed says so; each refusal of a keyed call says what to do next,
// in_progress with retryAfterSeconds; a call that sent no key gets no next,
// and an unanswered one is told to read the record.
func TestMCP_TheIdempotencyKeyResults(t *testing.T) {
	answer := func(status int, replayed bool, retryAfter, body string) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, _ *http.Request) {
			if body != "" {
				w.Header().Set("Content-Type", "application/json")
			}
			if replayed {
				w.Header().Set(idempotencyReplayedHeader, "true")
			}
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}
	}
	call := func(a func(http.ResponseWriter, *http.Request), keyed bool) (string, bool) {
		h := composeMCP(t, idempotentAccess{newWriteAccess(), true}, &mcpCustomers{answer: a}, true)
		args := map[string]any{"operation": "postCustomers", "body": map[string]any{"name": "x"}}
		if keyed {
			args["headers"] = map[string]any{"Idempotency-Key": "k-1"}
		}
		return toolText(t, callTool(t, h, mcpToolWrite, args))
	}
	note, _ := json.Marshal(mcpReplayNote)

	text, isError := call(answer(http.StatusCreated, true, "", `{"id":7}`), true)
	if want := `{"idempotentReplayed":true,"status":201,"response":{"id":7},"note":` + string(note) + `}`; isError || text != want {
		t.Errorf("a replay: %v %s", isError, text)
	}
	if !strings.Contains(text, "If this call was not a retry of that same intended action, nothing was done") {
		t.Errorf("the replay note: %s", text)
	}
	text, isError = call(answer(http.StatusNoContent, true, "", ""), true)
	if want := `{"idempotentReplayed":true,"status":204,"response":null,"note":` + string(note) + `}`; isError || text != want {
		t.Errorf("an empty replay: %v %s", isError, text)
	}
	text, isError = call(answer(http.StatusNotFound, true, "", `{"error":{"code":"not_found","message":"m"}}`), true)
	if !isError || !strings.Contains(text, `"idempotentReplayed":true`) || !strings.Contains(text, `"code":"not_found"`) {
		t.Errorf("a kept refusal replayed: %v %s", isError, text)
	}

	for code, want := range map[string]string{
		codeIdempotencyUnknown:    "read the record, then use a new key",
		codeIdempotencyInProgress: "retry shortly with the same key",
	} {
		text, isError := call(answer(http.StatusConflict, false, "1", `{"error":{"code":"`+code+`","message":"m"}}`), true)
		if !isError || !strings.Contains(text, `"code":"`+code+`"`) || !strings.Contains(text, want) {
			t.Errorf("%s: %v %s", code, isError, text)
		}
		if code == codeIdempotencyInProgress && !strings.Contains(text, `"retryAfterSeconds":1`) {
			t.Errorf("in progress has no retryAfterSeconds: %s", text)
		}
		// The same answer to a call that sent no key carries no next step.
		if text, _ := call(answer(http.StatusConflict, false, "1", `{"error":{"code":"`+code+`","message":"m"}}`), false); strings.Contains(text, `"next"`) {
			t.Errorf("%s without a key: %s", code, text)
		}
	}
	if text, isError := call(answer(http.StatusUnprocessableEntity, false, "", `{"error":{"code":"idempotency_key_reused","message":"m"}}`), true); !isError || !strings.Contains(text, "a new action takes a new key") {
		t.Errorf("reused: %v %s", isError, text)
	}

	shortTimeout(t)
	hang := func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	text, isError = call(hang, true)
	if !isError || !strings.Contains(text, "may or may not have been made") || !strings.Contains(text, "same Idempotency-Key, never a new one") {
		t.Errorf("unanswered with a key: %v %s", isError, text)
	}
	text, isError = call(hang, false)
	if !isError || !strings.Contains(text, "read the record before you retry") || strings.Contains(text, "same Idempotency-Key") {
		t.Errorf("unanswered without a key: %v %s", isError, text)
	}
}

// An operation with a key of its own (security review S4): described as its
// own contract, and its reuse refusal gets its own next step, never the
// general key's.
func TestMCP_AnOwnKeyIsItsOwnContract(t *testing.T) {
	e := &mcpOperation{ID: "postCommunicationsConversations", Method: http.MethodPost, Kind: mcpKindWrite, ownKey: true}
	if d := e.describe(1); d.Idempotency != mcpDescribeOwnKey {
		t.Errorf("describe: %+v", d)
	}
	rec := newCallRecorder(mcpResultMaxBytes + 1)
	rec.header.Set("Content-Type", "application/json")
	rec.WriteHeader(http.StatusConflict)
	_, _ = rec.Write([]byte(`{"error":{"code":"idempotency_key_reused","message":"m"}}`))
	r := callResult(e, rec, true)
	if !r.isError || !strings.Contains(r.text, mcpOwnKeyReusedNext[:40]) || strings.Contains(r.text, mcpIdempotencyNext[codeIdempotencyKeyReused]) {
		t.Errorf("an own key's reuse: %s", r.text)
	}
	rec = newCallRecorder(mcpResultMaxBytes + 1)
	rec.header.Set("Content-Type", "application/json")
	rec.WriteHeader(http.StatusConflict)
	_, _ = rec.Write([]byte(`{"error":{"code":"idempotency_outcome_unknown","message":"m"}}`))
	if r := callResult(e, rec, true); strings.Contains(r.text, `"next"`) {
		t.Errorf("the general key's next on an own key: %s", r.text)
	}
}
