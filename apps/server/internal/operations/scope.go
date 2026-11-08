package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// customerScopeExtension is the operation extension that declares an operation
// returns a customer's data and how a call is tied to one customer (virtual
// employees design V9b). (internal/openapi owns the name for the lint; this
// package does not import the loader.)
const customerScopeExtension = "x-vantigo-customer-scope"

// CustomerScope is an operation's x-vantigo-customer-scope: exactly one of
// Param and Resource.
//
//   - Param names a path or query parameter that carries the customer's id. For
//     an agent's run the dispatcher fills it with the run's customer, or refuses
//     a call that names another.
//   - Resource names the field of the JSON answer (a dotted path such as
//     "customer.customerId") that holds the customer's id. The dispatcher
//     refuses an answer whose field is not the run's customer.
//
// A scope with neither, or both, is malformed and refuses every call: the
// OpenAPI lint rejects it, so it only happens by hand.
type CustomerScope struct {
	Param    string
	Resource string
}

// parseCustomerScope reads the extension's value, nil for an operation without
// one. A value that is not an object with one string key is a malformed scope.
func parseCustomerScope(op map[string]any) *CustomerScope {
	raw, present := op[customerScopeExtension]
	if !present {
		return nil
	}
	scope := &CustomerScope{}
	obj, ok := raw.(map[string]any)
	if !ok {
		return scope
	}
	param, _ := obj["param"].(string)
	resource, _ := obj["resource"].(string)
	if len(obj) == 1 && (param == "") != (resource == "") {
		scope.Param, scope.Resource = param, resource
	}
	return scope
}

// valid says the scope names a parameter or a field, not both and not neither.
func (s *CustomerScope) valid() bool { return (s.Param == "") != (s.Resource == "") }

// scoped is req with the agent's customer scope applied, or a *Error refusing
// it. It changes nothing for an operation without a scope or for a call that is
// not an agent's: the assistant's reach is its person's, not a customer's.
func (e *entry) scoped(ctx context.Context, req CallRequest) (CallRequest, error) {
	if e.CustomerScope == nil {
		return req, nil
	}
	if grantKind(ctx) != contracts.GrantAgent {
		return req, nil
	}
	run, ok := AgentRunFrom(ctx)
	if !ok || run.CustomerID == nil {
		return req, &Error{Code: CodeCustomerScope, Message: e.ID + " returns a customer's data and this run has no customer"}
	}
	if !e.CustomerScope.valid() {
		return req, &Error{Code: CodeCustomerScope, Message: e.ID + " declares a customer scope that cannot be applied"}
	}
	if e.CustomerScope.Param == "" {
		return req, nil
	}
	want := strconv.FormatInt(int64(*run.CustomerID), 10)
	name := e.CustomerScope.Param
	for _, p := range e.params {
		if p.Name != name {
			continue
		}
		switch p.In {
		case "path":
			if raw, given := req.PathParams[name]; given {
				if got, ok := scalar(raw); !ok || got != want {
					return req, e.otherCustomer()
				}
				return req, nil
			}
			req.PathParams = withEntry(req.PathParams, name, want)
			return req, nil
		case "query":
			if raw, given := req.Query[name]; given && raw != nil {
				if got, ok := scalars(raw); !ok || len(got) != 1 || got[0] != want {
					return req, e.otherCustomer()
				}
				return req, nil
			}
			req.Query = withEntry(req.Query, name, want)
			return req, nil
		}
	}
	return req, &Error{Code: CodeCustomerScope, Message: e.ID + " declares a customer scope on a parameter it does not have"}
}

func (e *entry) otherCustomer() *Error {
	return &Error{Code: CodeCustomerScope, Message: e.ID + " may be called for this run's customer only"}
}

// withEntry is m with key set, as a copy: the caller's map is not changed.
func withEntry(m map[string]any, key string, value any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	out[key] = value
	return out
}

// checkResource refuses an answer that is not the run's customer's: the answer
// of a call that succeeded, for an operation scoped by Resource. A refusal drops
// the body, so another customer's data never reaches the agent.
func (e *entry) checkResource(ctx context.Context, res CallResult) error {
	if e.CustomerScope == nil || e.CustomerScope.Resource == "" || res.Status < 200 || res.Status > 299 {
		return nil
	}
	if grantKind(ctx) != contracts.GrantAgent {
		return nil
	}
	run, ok := AgentRunFrom(ctx)
	if !ok || run.CustomerID == nil {
		return &Error{Code: CodeCustomerScope, Message: e.ID + " returns a customer's data and this run has no customer"}
	}
	refuse := &Error{Code: CodeCustomerScope, Message: fmt.Sprintf("the answer of %s is not for this run's customer", e.ID)}
	if res.Truncated || len(bytes.TrimSpace(res.Body)) == 0 {
		return refuse
	}
	dec := json.NewDecoder(bytes.NewReader(res.Body))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return refuse
	}
	for _, key := range strings.Split(e.CustomerScope.Resource, ".") {
		obj, ok := value.(map[string]any)
		if !ok {
			return refuse
		}
		if value, ok = obj[key]; !ok {
			return refuse
		}
	}
	got, ok := value.(json.Number)
	if !ok || got.String() != strconv.FormatInt(int64(*run.CustomerID), 10) {
		return refuse
	}
	return nil
}
