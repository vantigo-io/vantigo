package customers_test

import (
	"fmt"
	"testing"

	"github.com/google/uuid"

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

// authenticatedClient signs in a caller holding every permission the
// customers CRUD/dashboard-stats (Task 6), contacts/association (Task 7) and
// timeline (Task 9) operations can exercise, plus customers:billing-manage
// for the billing profile (invoice-ready customer design D1, D4) — .NET's
// CustomersApiFactory.CreateAuthenticatedClient, which the ported tests
// assume throughout (TS/CustomersEndpointsTests.cs, TS/ContactsEndpointsTests.cs,
// TS/TimelineEndpointsTests.cs and friends). legal-identity-manage is
// included: this client is meant to represent a caller with no restrictions
// short of lookup-view, which every legal_identity_permission_test.go and
// brreg_test.go case signs in for separately (Task 8's own pattern, kept
// as-is here); the permission-gate tests that need a caller *without* some
// permission sign in separately with a narrower set too.
func authenticatedClient(t *testing.T, h *modtest.Harness) *modtest.Client {
	t.Helper()
	c, _ := authenticatedClientWithID(t, h)
	return c
}

// authenticatedClientWithID is authenticatedClient, but also returns the
// signed-in user's id — for a test that must name that specific user, such
// as asserting a timeline entry's actor against the caller who wrote it
// (customers foundation design D1).
func authenticatedClientWithID(t *testing.T, h *modtest.Harness) (*modtest.Client, uuid.UUID) {
	t.Helper()
	return h.SignInUser(t, "customers:view", "customers:create", "customers:update", "customers:delete",
		"customers:legal-identity-view", "customers:legal-identity-manage",
		"customers:contacts-view", "customers:contacts-manage",
		"customers:associations-view", "customers:associations-manage",
		"customers:timeline-view", "customers:timeline-manage",
		"customers:billing-manage")
}

// userDisplayName is the display name identity.users stores for userID — the
// value D1 says an actor's actorDisplay must equal, resolved from the same
// row actorFor itself reads from, never a literal the test invents.
func userDisplayName(t *testing.T, h *modtest.Harness, userID uuid.UUID) string {
	t.Helper()
	return modtest.One[string](t, h, `SELECT display_name FROM identity.users WHERE id = $1`, userID)
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
	h.Exec(t, `INSERT INTO customers.customers_contacts (customer_id, contact_id, title, email) VALUES ($1, $2, 'Primary', $3)`,
		customerID, contactID, email)
}

// setDisplayName, disableUser and forgetUser are the three states
// contracts.UserDirectory can report a customer's owner in (owner and tags
// design D1). There is no users fake to configure: modtest composes the real
// identity module, so Deps.Users reads identity.users and these three
// statements are how a test shapes what it answers — the same way projects'
// own people tests do it (internal/projects/people_test.go:96-109).
//
// setDisplayName exists because modtest seeds a user whose display_name is a
// generated email address: a test that asserts the owner's name must put a
// name there itself rather than hard-coding what modtest happens to generate.
func setDisplayName(t *testing.T, h *modtest.Harness, userID uuid.UUID, name string) {
	t.Helper()
	h.Exec(t, `UPDATE identity.users SET display_name = $2 WHERE id = $1`, userID, name)
}

// disableUser flips the flag the directory reports as Active: false. An owner
// disabled AFTER being assigned keeps the customer (design D1): nothing is
// silently revoked, so this is the fixture for "the name is still shown, with
// an inactive hint", not for a customer that lost its owner.
func disableUser(t *testing.T, h *modtest.Harness, userID uuid.UUID) {
	t.Helper()
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, userID)
}

// seedNamedUser writes one user directly, with the display name a test wants
// to search for. modtest's own SignInUser also mints a role and a session,
// which the twenty-one users of the assignable-users cap test have no use for —
// they exist only to be found by contracts.UserDirectory.SearchUsers. The
// column list is modtest.seedUser's own (modtest.go:582), version included: it
// is a uuid, not a counter.
func seedNamedUser(t *testing.T, h *modtest.Harness, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email := fmt.Sprintf("owner-candidate-%s@example.test", id)
	h.Exec(t, `INSERT INTO identity.users (id, email, normalized_email, display_name, version, created_at, updated_at)
	           VALUES ($1, $2, upper($2), $3, $4, $5, $5)`, id, email, name, uuid.New(), h.Now())
	return id
}

// forgetUser removes the account entirely, which is how a stored owner_user_id
// ends up naming a user the directory returns nothing for — the case that must
// read as "Unknown user", inactive, rather than as a 500 or a vanished owner
// (the actorFor precedent, actor.go). There is deliberately no foreign key
// from customers.customers to identity.users (migration 00024), which is what
// makes this state reachable at all.
func forgetUser(t *testing.T, h *modtest.Harness, userID uuid.UUID) {
	t.Helper()
	h.Exec(t, `DELETE FROM identity.users WHERE id = $1`, userID)
}

func ptr[T any](v T) *T { return &v }
