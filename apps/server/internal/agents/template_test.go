package agents

import (
	"context"
	"net/http"
	"slices"
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

// TestTemplates_EveryAllowlistedOperationExistsAndCustomerDataIsScoped is the
// contract test of V9b, for every template: an operation on a template's
// allowlist that returns a customer's data carries x-vantigo-customer-scope, and
// one that does not return it carries none, so a scope cannot be dropped or
// invented unnoticed. Customers', Orders' and Invoices' operations all return a
// customer's data.
func TestTemplates_EveryAllowlistedOperationExistsAndCustomerDataIsScoped(t *testing.T) {
	t.Parallel()
	ops := contractOperations(t)
	for _, key := range templateOrder {
		tpl := templates[key]
		seen := map[string]bool{}
		for _, a := range tpl.Allowlist {
			if seen[a.ID] {
				t.Errorf("%s: %s is on the allowlist twice", key, a.ID)
			}
			seen[a.ID] = true
			c, ok := ops[a.ID]
			if !ok {
				t.Errorf("%s: %s is on the allowlist and in no contract", key, a.ID)
				continue
			}
			_, scoped := c.op.Extensions[openapi.CustomerScopeExtension]
			switch {
			case a.CustomerData && !scoped:
				t.Errorf("%s: %s is marked as returning a customer's data and declares no %s", key, a.ID, openapi.CustomerScopeExtension)
			case !a.CustomerData && scoped:
				t.Errorf("%s: %s declares a customer scope and the allowlist does not mark it as customer data", key, a.ID)
			}
			if !a.CustomerData && (c.module == "customers" || c.module == "orders" || c.module == "invoices") {
				t.Errorf("%s: %s is a %s operation, which returns a customer's data, and is not marked", key, a.ID, c.module)
			}
			if a.OwnConversation && !strings.Contains(c.path, "/conversations/{id}") {
				t.Errorf("%s: %s is marked as naming the run's conversation and its path %s does not", key, a.ID, c.path)
			}
			if a.Safe && c.method == strings.ToLower(http.MethodGet) {
				t.Errorf("%s: %s is marked safe to automate and is a read", key, a.ID)
			}
			if mark, _ := c.op.Extensions[openapi.AssistantExtension].(string); mark == "never" {
				t.Errorf("%s: %s is on the allowlist and says never, so no agent could call it", key, a.ID)
			}
		}
	}
}

// TestTemplates_NeedsCoverWhatTheAllowlistedOperationsRequire holds each
// template's needs to the contract: each permission an allowlisted operation's
// access rule names is a need, at change for an operation that is not a read (the
// gate turns a confirm-mode operation into never when any permission it names is
// only read), and none is locked off.
func TestTemplates_NeedsCoverWhatTheAllowlistedOperationsRequire(t *testing.T) {
	t.Parallel()
	ops := contractOperations(t)
	for _, key := range templateOrder {
		tpl := templates[key]
		needs := map[string]string{}
		for _, n := range tpl.Needs {
			if isNever(n.Permission) {
				t.Errorf("%s needs %s, which no level can raise", key, n.Permission)
			}
			needs[n.Permission] = n.Level
		}
		required := map[string]string{}
		for _, a := range tpl.Allowlist {
			c := ops[a.ID]
			level := levelChange
			if c.method == strings.ToLower(http.MethodGet) || c.method == http.MethodGet {
				level = levelRead
			}
			keys := accessKeys(c.op)
			if len(keys) == 0 {
				t.Errorf("%s: %s names no permission, so the gate would refuse it", key, a.ID)
			}
			for _, k := range keys {
				if levelRank(level) > levelRank(required[k]) {
					required[k] = level
				}
			}
		}
		for k, level := range required {
			got, ok := needs[k]
			switch {
			case !ok:
				t.Errorf("%s: the allowlist needs %s at %s and the template does not list it", key, k, level)
			case levelRank(got) < levelRank(level):
				t.Errorf("%s: the allowlist needs %s at %s and the template lists %s", key, k, level, got)
			}
		}
		for k := range needs {
			if _, ok := required[k]; !ok {
				t.Errorf("%s needs %s and no allowlisted operation names it", key, k)
			}
		}
	}
}

// Every tool a template offers exists, a template that starts conversations or
// writes to a colleague has what the tool needs (a channel field, a recipient),
// and a run about a subject offers no tool that names a conversation.
func TestTemplates_ToolsAndFieldsAgree(t *testing.T) {
	t.Parallel()
	for _, key := range templateOrder {
		tpl := templates[key]
		rs := &runState{tpl: tpl}
		for _, tool := range tpl.Tools {
			if _, ok := rs.ownTool(tool); !ok {
				t.Errorf("%s offers %s, which is no tool", key, tool)
			}
			if !tpl.answersConversations() && (tool == toolReply || tool == toolNote || tool == toolTag) {
				t.Errorf("%s runs about a %s and offers %s, which names a conversation", key, tpl.Subject, tool)
			}
		}
		if tpl.offers(toolStartConversation) != tpl.uses(fieldStartMode) || tpl.offers(toolStartConversation) && !tpl.uses(fieldChannels) {
			t.Errorf("%s: start_conversation, the start mode and the channels do not go together", key)
		}
		if tpl.offers(toolStartConversation) && !slices.ContainsFunc(tpl.Allowlist, func(a allowedOperation) bool { return a.ID == opStart }) {
			t.Errorf("%s starts conversations and has not %s on its allowlist, so the gate would refuse", key, opStart)
		}
		if tpl.Recipient != tpl.uses(fieldRecipient) {
			t.Errorf("%s: the recipient and its field do not go together", key)
		}
		if tpl.uses(fieldSchedule) != (tpl.Subject == subjectSchedule) {
			t.Errorf("%s: the schedule and its subject do not go together", key)
		}
	}
	if !slices.Contains(reservedOperations, opStart) {
		t.Errorf("%s is not reserved, so the model could start a conversation past the rules", opStart)
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

// A run about a subject has no conversation: an operation that names one is refused for it, whatever the id.
func TestPin_RefusesAnOwnConversationOperationInARunAboutASubject(t *testing.T) {
	t.Parallel()
	rs := &runState{tpl: &frontDesk}
	if r := rs.pin(opGetConversation, map[string]any{"id": "x"}, nil); r == nil {
		t.Error("a run about a subject was allowed an operation on its conversation")
	}
}
