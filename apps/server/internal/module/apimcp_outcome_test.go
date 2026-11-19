package module

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// What a write's result says when its outcome is in doubt (S1, S2), the
// per-key bound on calls in flight (S7) and a header given twice (S8).

func shortTimeout(t *testing.T) {
	t.Helper()
	old := mcpCallTimeout
	mcpCallTimeout = 50 * time.Millisecond
	t.Cleanup(func() { mcpCallTimeout = old })
}

// A write that answers after its deadline answered: its result is what it
// wrote, never a failure (S1).
func TestMCP_AWriteAnsweredPastItsDeadlineSucceeds(t *testing.T) {
	shortTimeout(t)
	customers := &mcpCustomers{answer: func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":7}`))
	}}
	h := composeMCP(t, newWriteAccess(), customers, true)
	text, isError := toolText(t, callTool(t, h, mcpToolWrite, map[string]any{"operation": "postCustomers", "body": map[string]any{"name": "x"}}))
	if isError || text != `{"id":7}` {
		t.Errorf("%v %s", isError, text)
	}
}

// A write that never answers may or may not have been made, and says so; a
// read that never answers only timed out (S1).
func TestMCP_AnUnansweredWriteMayHaveBeenMade(t *testing.T) {
	shortTimeout(t)
	customers := &mcpCustomers{answer: func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }}
	h := composeMCP(t, newWriteAccess(), customers, true)
	text, isError := toolText(t, callTool(t, h, mcpToolWrite, map[string]any{"operation": "postCustomers", "body": map[string]any{"name": "x"}}))
	// postCustomers takes no Idempotency-Key here (the fake Access is no
	// IdempotencyStore): read the record first.
	if !isError || !strings.Contains(text, "may or may not have been made") || !strings.Contains(text, "read the record") ||
		strings.Contains(text, "Idempotency-Key") {
		t.Errorf("write: %v %s", isError, text)
	}
	text, isError = toolText(t, callTool(t, h, mcpToolRead, map[string]any{"operation": "getCustomers"}))
	if !isError || !strings.Contains(text, "did not answer within") || strings.Contains(text, "may or may not") {
		t.Errorf("read: %v %s", isError, text)
	}
}

// A write that succeeded with an answer over the cap is a success that says it
// was cut, with its status, length, Location and id; a read over the cap still
// asks for less (S2).
func TestMCP_AnAppliedWriteOverTheCapIsNoError(t *testing.T) {
	big := `{"id":42,"lines":"` + strings.Repeat("a", 200_000) + `"}`
	customers := &mcpCustomers{answer: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", "/api/v1/customers/42")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(big))
	}}
	h := composeMCP(t, newWriteAccess(), customers, true)
	text, isError := toolText(t, callTool(t, h, mcpToolWrite, map[string]any{"operation": "postCustomers", "body": map[string]any{"name": "x"}}))
	var got map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil || isError {
		t.Fatalf("%v %v %.200s", err, isError, text)
	}
	if got["status"] != float64(201) || got["truncated"] != true || got["length"] != float64(len(big)) ||
		got["location"] != "/api/v1/customers/42" || got["id"] != float64(42) {
		t.Errorf("%s", text)
	}
	if text, isError := toolText(t, callTool(t, h, mcpToolRead, map[string]any{"operation": "getCustomers"})); !isError || !strings.Contains(text, "larger than") {
		t.Errorf("read over the cap: %v %.200s", isError, text)
	}
}

// topLevelID reads the object's own id, never a nested one, from a body cut
// anywhere after it.
func TestMCP_TopLevelID(t *testing.T) {
	for raw, want := range map[string]any{
		`{"id":"a1","x":[1,2`:                    "a1",
		`{"customer":{"id":3},"id":9,"rest":"…`:  float64(9),
		`{"customer":{"id":3},"rest":"never end`: nil,
		`[{"id":1}]`:                             nil,
	} {
		got, ok := topLevelID([]byte(raw))
		if (want == nil) == ok || got != want {
			t.Errorf("%s: %v %v, want %v", raw, got, ok, want)
		}
	}
}

// One key has at most mcpInFlight calls looping back at once; one more is told
// to retry, and the module never sees it (S7).
func TestMCP_AKeysCallsInFlightAreBounded(t *testing.T) {
	release := make(chan struct{})
	var started sync.WaitGroup
	started.Add(mcpInFlight)
	customers := &mcpCustomers{answer: func(w http.ResponseWriter, _ *http.Request) {
		started.Done()
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}}
	access := newMCPAccess()
	access.principal = &contracts.Principal{UserID: uuid.New(), Via: &contracts.Via{Kind: contracts.ViaAPIKey, KeyID: uuid.New()}}
	h := composeMCP(t, access, customers, true)
	var done sync.WaitGroup
	for range mcpInFlight {
		done.Add(1)
		go func() {
			defer done.Done()
			callTool(t, h, mcpToolRead, map[string]any{"operation": "getCustomers"})
		}()
	}
	started.Wait()
	text, isError := toolText(t, callTool(t, h, mcpToolRead, map[string]any{"operation": "getCustomers"}))
	if !isError || !strings.Contains(text, "retry shortly") {
		t.Errorf("one call too many: %v %s", isError, text)
	}
	close(release)
	done.Wait()
	customers.mu.Lock()
	n := len(customers.requests)
	customers.answer = nil
	customers.mu.Unlock()
	if n != mcpInFlight {
		t.Errorf("%d calls reached the module, want %d", n, mcpInFlight)
	}
	if _, isError := toolText(t, callTool(t, h, mcpToolRead, map[string]any{"operation": "getCustomers"})); isError {
		t.Error("a call after the others finished was refused")
	}
}

// A header given twice in different cases is refused (S8).
func TestMCP_AHeaderGivenTwiceIsRefused(t *testing.T) {
	e := &mcpOperation{ID: "putThing", Method: http.MethodPut, Path: "/api/v1/things", params: []*openapi3.Parameter{
		{Name: "If-Match", In: openapiInHeader},
	}}
	if _, _, _, refusal := buildCall(e, mcpCallArguments{Operation: e.ID, Headers: map[string]string{"If-Match": "1", "if-match": "2"}}); refusal == nil {
		t.Error("two spellings of If-Match were taken")
	}
}
