package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

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

// With rows of two customers present, an agent's list of sales orders or
// invoices holds only its run's customer's, and reading the other's by id is
// refused with its body dropped (virtual employees design V9b).
func TestAgentScope_ListsAndReadsHoldOnlyTheRunsCustomer(t *testing.T) {
	t.Parallel()
	o := newOrdersInstallation(t)
	other := invoiceNewBusiness(t, o.c, "Elv Sør", "Elv Sør AS", "974760673").Id
	own := o.customer

	order := func(customer int32) int64 {
		var got struct {
			Id int64 `json:"id"`
		}
		okJSON(t, o.c, http.MethodPost, ordersOrdersPath, map[string]any{
			"clientRequestId": uuid.New().String(), "customerId": customer,
			"lines": []any{orderLine(o.screw, 1, 0)},
		}, &got)
		return got.Id
	}
	ownOrder, otherOrder := order(own), order(other)
	ownDoc, otherDoc := newDraft(t, o.c, own), newDraft(t, o.c, other)

	ops := o.h.Operations(t)
	ops.SetGate(contracts.GrantAgent, func(_ context.Context, info operations.Info) operations.Mode { return info.Mode })
	agent, _ := o.h.AgentContext(t, &own, "orders:access", "invoices:access")

	var orders struct {
		Items []struct {
			Id         int64 `json:"id"`
			CustomerId int32 `json:"customerId"`
		} `json:"items"`
	}
	res, err := ops.Call(agent, operations.CallRequest{OperationID: "listSalesOrders"})
	if err != nil || res.Status != http.StatusOK {
		t.Fatalf("listSalesOrders: %v %d %s", err, res.Status, res.Body)
	}
	if err := json.Unmarshal(res.Body, &orders); err != nil || len(orders.Items) != 1 || orders.Items[0].Id != ownOrder || orders.Items[0].CustomerId != own {
		t.Errorf("the agent's orders = %s, want only order %d of customer %d", res.Body, ownOrder, own)
	}
	var invoices struct {
		Data []struct {
			Id         int64 `json:"id"`
			CustomerId int32 `json:"customerId"`
		} `json:"data"`
	}
	res, err = ops.Call(agent, operations.CallRequest{OperationID: "getInvoices"})
	if err != nil || res.Status != http.StatusOK {
		t.Fatalf("getInvoices: %v %d %s", err, res.Status, res.Body)
	}
	if err := json.Unmarshal(res.Body, &invoices); err != nil || len(invoices.Data) != 1 || invoices.Data[0].Id != ownDoc.ID || invoices.Data[0].CustomerId != own {
		t.Errorf("the agent's invoices = %s, want only invoice %d of customer %d", res.Body, ownDoc.ID, own)
	}

	for _, req := range []operations.CallRequest{
		{OperationID: "getSalesOrder", PathParams: map[string]any{"id": ownOrder}},
		{OperationID: "getInvoicesById", PathParams: map[string]any{"id": ownDoc.ID}},
	} {
		if res, err := ops.Call(agent, req); err != nil || res.Status != http.StatusOK {
			t.Errorf("%s of the run's customer: %v %d %s", req.OperationID, err, res.Status, res.Body)
		}
	}
	for _, req := range []operations.CallRequest{
		{OperationID: "getSalesOrder", PathParams: map[string]any{"id": otherOrder}},
		{OperationID: "getInvoicesById", PathParams: map[string]any{"id": otherDoc.ID}},
		{OperationID: "listSalesOrders", Query: map[string]any{"customerId": other}},
		{OperationID: "getInvoices", Query: map[string]any{"customerId": other}},
	} {
		res, err := ops.Call(agent, req)
		if !errors.Is(err, operations.ErrCustomerScope) || len(res.Body) != 0 {
			t.Errorf("%s for the other customer = %+v, %v, want customer_scope and no body", req.OperationID, res, err)
		}
	}
}
