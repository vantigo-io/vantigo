package customers_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// newHarness is one customers installation for one test: internal/modtest's
// shared harness composing this module beside identity, with every exchange
// validated against customers.yaml through the package recorder.
func newHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
	t.Helper()
	return modtest.New(t, append([]modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(customers.Module()),
	}, opts...)...)
}

// insertCustomer creates a customer with the given name and status and returns
// its id. The customer number is the next one free, which is all the directory
// tests need of it.
func insertCustomer(t *testing.T, h *modtest.Harness, name, status string) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `
		INSERT INTO customers.customers (customer_number, name, status, created_at, updated_at)
		VALUES ((SELECT coalesce(max(customer_number), 1000) + 1 FROM customers.customers), $1, $2, $3, $3)
		RETURNING id`, name, status, h.Now())
}

// insertContact creates a contact and returns its id. email is the contact's
// canonical address; nil leaves it unset, as a contact reachable only through
// an association's own address is.
func insertContact(t *testing.T, h *modtest.Harness, firstName, lastName string, email *string) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `
		INSERT INTO customers.contacts (first_name, last_name, email, created_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id`, firstName, lastName, email, h.Now())
}

// associate links a contact to a customer, optionally with a customer-specific
// email that overrides the contact's canonical one for that relationship.
func associate(t *testing.T, h *modtest.Harness, customerID, contactID int32, email *string) {
	t.Helper()
	h.Exec(t, `INSERT INTO customers.customers_contacts (customer_id, contact_id, role, email) VALUES ($1, $2, 'Primary', $3)`,
		customerID, contactID, email)
}

func ptr[T any](v T) *T { return &v }
