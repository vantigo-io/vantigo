package integration_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// An agent's call is tied to its run's customer by the dispatcher
// (virtual employees design V9b), against the real Customers contract and
// router: the customer's own id is filled in, another's is refused before the
// handler runs, and a run with no customer reads none.
func TestAgentScope_CustomersReadsAreTiedToTheRunsCustomer(t *testing.T) {
	t.Parallel()
	h := newInstallation(t, modCustomers)
	admin, _ := h.SignInUser(t, "customers:view", "customers:create")
	type created struct {
		Id int32 `json:"id"`
	}
	var own, other created
	okJSON(t, admin, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Acme AS"}, &own)
	okJSON(t, admin, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Beta AS"}, &other)

	ops := h.Operations(t)
	// The agents module's gate is not here yet; this one passes the contract's
	// mode, so what is under test is the scope and the router.
	ops.SetGate(contracts.GrantAgent, func(_ context.Context, info operations.Info) operations.Mode { return info.Mode })
	perms := []string{"customers:view", "customers:associations-view", "customers:contacts-view"}
	agent, _ := h.AgentContext(t, &own.Id, perms...)

	res, err := ops.Call(agent, operations.CallRequest{OperationID: "getCustomer"})
	if err != nil || res.Status != http.StatusOK || !strings.Contains(string(res.Body), `"name":"Acme AS"`) {
		t.Fatalf("getCustomer with the parameter left out: %v %d %s, want the run's customer", err, res.Status, res.Body)
	}
	res, err = ops.Call(agent, operations.CallRequest{OperationID: "getCustomersByIdContacts"})
	if err != nil || res.Status != http.StatusOK {
		t.Errorf("the run's customer's contacts: %v %d %s", err, res.Status, res.Body)
	}
	_, err = ops.Call(agent, operations.CallRequest{OperationID: "getCustomer", PathParams: map[string]any{"id": other.Id}})
	if !errors.Is(err, operations.ErrCustomerScope) {
		t.Errorf("getCustomer for another customer = %v, want customer_scope", err)
	}

	none, _ := h.AgentContext(t, nil, perms...)
	_, err = ops.Call(none, operations.CallRequest{OperationID: "getCustomer", PathParams: map[string]any{"id": own.Id}})
	if !errors.Is(err, operations.ErrCustomerScope) {
		t.Errorf("getCustomer in a run with no customer = %v, want customer_scope", err)
	}

	// The agent's own principal is what the handler checked: without the
	// permission the router refuses, scope or not.
	bare, _ := h.AgentContext(t, &own.Id)
	res, err = ops.Call(bare, operations.CallRequest{OperationID: "getCustomer"})
	if err != nil || res.Status != http.StatusForbidden {
		t.Errorf("an agent without customers:view: %v %d, want the router's 403", err, res.Status)
	}
}
