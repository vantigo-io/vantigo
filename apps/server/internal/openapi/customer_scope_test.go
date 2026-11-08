package openapi

import (
	"context"
	"testing"
)

// customerDataOperations are the operations the front desk (virtual employees
// design V9b) reads a customer's data through, each with how a call is tied to
// one customer. An operation here that loses its x-vantigo-customer-scope, or
// has another, fails: the dispatcher enforces the extension for an agent's run,
// so dropping it silently would let a run read another customer's data. A new
// operation that returns customer data and may be allowlisted for an agent
// joins this table in the delivery that adds it, with its scope.
//
// Not marked, so not allowlistable: an operation whose answer names no
// customer and whose request carries none, such as an order's shipments or a
// delivery (Orders keeps the customer on the order, which is marked).
var customerDataOperations = map[string]string{
	"getCommunicationsConversations": "param:customerId",
	// getCommunicationsConversationsById is not here: an agent's run with no
	// customer, or on a conversation not linked yet, must read the conversation it
	// answers. The agents module pins that operation to the run's own conversation
	// instead (virtual employees design V9b; agents A2).
	"getCustomer":              "param:id",
	"getCustomersByIdContacts": "param:id",
	"listSalesOrders":          "param:customerId",
	"getSalesOrder":            "resource:customer.customerId",
	// The two quote reads are what the sales follow-up reads (virtual employees design V30):
	// the quote of the run's customer, and no other.
	"listQuotes":      "param:customerId",
	"getQuote":        "resource:customer.customerId",
	"getInvoices":     "param:customerId",
	"getInvoicesById": "resource:customerId",
}

func TestCustomerDataOperationsDeclareTheirCustomerScope(t *testing.T) {
	declared := map[string]string{}
	for _, name := range Modules {
		doc, err := Load(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range operations(doc) {
			value, present := op.Op.Extensions[CustomerScopeExtension]
			if !present {
				continue
			}
			if msg := customerScopeProblem(op, value); msg != "" {
				t.Errorf("%s: %s", op.OperationID, msg)
				continue
			}
			obj := value.(map[string]any)
			if param, ok := obj["param"].(string); ok {
				declared[op.OperationID] = "param:" + param
			} else {
				declared[op.OperationID] = "resource:" + obj["resource"].(string)
			}
		}
	}
	for id, want := range customerDataOperations {
		got, ok := declared[id]
		switch {
		case !ok:
			t.Errorf("%s returns a customer's data and declares no %s", id, CustomerScopeExtension)
		case got != want:
			t.Errorf("%s declares scope %q, want %q", id, got, want)
		}
	}
	for id := range declared {
		if _, ok := customerDataOperations[id]; !ok {
			t.Errorf("%s declares a customer scope this table does not list; add it with the reason it is allowlistable", id)
		}
	}
}
