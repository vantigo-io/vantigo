package agents

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// contractOp is an operation of the merged contract with where it comes from.
type contractOp struct {
	module, method, path string
	op                   *openapi3.Operation
}

func contractOperations(t *testing.T) map[string]contractOp {
	t.Helper()
	out := map[string]contractOp{}
	for _, name := range openapi.Modules {
		doc, err := openapi.Load(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		for path, item := range doc.Paths.Map() {
			for method, op := range item.Operations() {
				out[op.OperationID] = contractOp{module: name, method: method, path: path, op: op}
			}
		}
	}
	return out
}

// accessKeys are the permission keys of an operation's x-vantigo-access.
func accessKeys(op *openapi3.Operation) []string {
	rule, _ := op.Extensions["x-vantigo-access"].(string)
	list, ok := strings.CutPrefix(rule, "permission:")
	if !ok {
		return nil
	}
	return strings.Split(list, "+")
}

// TestFrontDesk_EveryAllowlistedOperationExistsAndCustomerDataIsScoped is the
// contract test of V9b: an operation on a template's allowlist that returns a
// customer's data carries x-vantigo-customer-scope, and one that does not
// return it carries none, so a scope cannot be dropped or invented unnoticed.
// Customers', Orders' and Invoices' operations all return a customer's data.
func TestFrontDesk_EveryAllowlistedOperationExistsAndCustomerDataIsScoped(t *testing.T) {
	t.Parallel()
	ops := contractOperations(t)
	seen := map[string]bool{}
	for _, a := range frontDesk.Allowlist {
		if seen[a.ID] {
			t.Errorf("%s is on the allowlist twice", a.ID)
		}
		seen[a.ID] = true
		c, ok := ops[a.ID]
		if !ok {
			t.Errorf("%s is on the allowlist and in no contract", a.ID)
			continue
		}
		_, scoped := c.op.Extensions[openapi.CustomerScopeExtension]
		switch {
		case a.CustomerData && !scoped:
			t.Errorf("%s is marked as returning a customer's data and declares no %s", a.ID, openapi.CustomerScopeExtension)
		case !a.CustomerData && scoped:
			t.Errorf("%s declares a customer scope and the allowlist does not mark it as customer data", a.ID)
		}
		if !a.CustomerData && (c.module == "customers" || c.module == "orders" || c.module == "invoices") {
			t.Errorf("%s is a %s operation, which returns a customer's data, and is not marked", a.ID, c.module)
		}
		if a.OwnConversation && !strings.Contains(c.path, "/conversations/{id}") {
			t.Errorf("%s is marked as naming the run's conversation and its path %s does not", a.ID, c.path)
		}
		if mark, _ := c.op.Extensions[openapi.AssistantExtension].(string); mark == "never" {
			t.Errorf("%s is on the allowlist and says never, so no agent could call it", a.ID)
		}
	}
}

// TestFrontDesk_NeedsCoverWhatTheAllowlistedOperationsRequire holds the needs to
// the contract: each permission an allowlisted operation's access rule names
// is a need, at change for an operation that is not a read (the gate turns a
// confirm-mode operation into never when any permission it names is only read),
// and none is locked off.
func TestFrontDesk_NeedsCoverWhatTheAllowlistedOperationsRequire(t *testing.T) {
	t.Parallel()
	ops := contractOperations(t)
	needs := map[string]string{}
	for _, n := range frontDesk.Needs {
		if isNever(n.Permission) {
			t.Errorf("the front desk needs %s, which no level can raise", n.Permission)
		}
		needs[n.Permission] = n.Level
	}
	required := map[string]string{}
	for _, a := range frontDesk.Allowlist {
		c := ops[a.ID]
		level := levelChange
		if c.method == strings.ToLower(http.MethodGet) || c.method == http.MethodGet {
			level = levelRead
		}
		keys := accessKeys(c.op)
		if len(keys) == 0 {
			t.Errorf("%s names no permission, so the gate would refuse it", a.ID)
		}
		for _, key := range keys {
			if levelRank(level) > levelRank(required[key]) {
				required[key] = level
			}
		}
	}
	for key, level := range required {
		got, ok := needs[key]
		switch {
		case !ok:
			t.Errorf("the allowlist needs %s at %s and the template does not list it", key, level)
		case levelRank(got) < levelRank(level):
			t.Errorf("the allowlist needs %s at %s and the template lists %s", key, level, got)
		}
	}
	for key := range needs {
		if _, ok := required[key]; !ok {
			t.Errorf("the template needs %s and no allowlisted operation names it", key)
		}
	}
}

func TestAgentNever_IsSortedAndOnlyWhatIsLocked(t *testing.T) {
	t.Parallel()
	for i, key := range agentNever {
		if i > 0 && agentNever[i-1] >= key {
			t.Errorf("agentNever is not sorted and unique at %s", key)
		}
	}
	for _, key := range []string{"identity:manage", "invoices:issue", "invoices:payments", "time:approve", "expenses:approve", "staff:approve", "staff:personal-data", "agents:manage"} {
		if !isNever(key) {
			t.Errorf("%s is not locked off", key)
		}
	}
	if isNever("communications:conversations-reply") {
		t.Errorf("the front desk's reply permission is locked off")
	}
}
