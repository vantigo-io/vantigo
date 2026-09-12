package customers_test

import (
	"context"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// newDirectory builds the module's customer directory over the harness's
// dependencies, exactly as module.Compose builds it before any module mounts.
func newDirectory(t *testing.T, h *modtest.Harness) contracts.CustomerDirectory {
	t.Helper()
	d := customers.Module().Directory
	if d == nil {
		t.Fatal("the module declares no customer directory")
	}
	return d(h.Deps())
}

// TestDirectory_ResolvesACustomer proves a customer another module references
// resolves to its name, the only thing the directory ever exposes of it.
func TestDirectory_ResolvesACustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := insertCustomer(t, h, "Nordvest Kraft AS", "active")

	got, err := newDirectory(t, h).Customer(context.Background(), id)
	if err != nil {
		t.Fatalf("Customer: %v", err)
	}
	if got == nil || *got != (contracts.CustomerEntry{ID: id, Name: "Nordvest Kraft AS", Archived: false}) {
		t.Errorf("Customer = %+v, want {%d Nordvest Kraft AS false}", got, id)
	}
}

// TestDirectory_ArchivedCustomerStillResolves proves archival does not hide a
// customer from the modules holding historical references to it: it resolves,
// flagged, rather than reading as deleted (inventory §6).
func TestDirectory_ArchivedCustomerStillResolves(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := insertCustomer(t, h, "Fjordkraft Sør AS", "archived")

	got, err := newDirectory(t, h).Customer(context.Background(), id)
	if err != nil {
		t.Fatalf("Customer: %v", err)
	}
	if got == nil || !got.Archived || got.Name != "Fjordkraft Sør AS" {
		t.Errorf("Customer = %+v, want the customer with Archived true", got)
	}
}

// TestDirectory_UnknownIdsAreNilWithoutAnError proves a missing row is
// (nil, nil) for both lookups: a caller tells "does not exist" from "the
// lookup failed" by checking err, never by reading nil as failure.
func TestDirectory_UnknownIdsAreNilWithoutAnError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	dir := newDirectory(t, h)
	ctx := context.Background()

	customer, err := dir.Customer(ctx, 999_999)
	if customer != nil || err != nil {
		t.Errorf("Customer(unknown) = %+v, %v, want nil, nil", customer, err)
	}
	contact, err := dir.Contact(ctx, 999_999)
	if contact != nil || err != nil {
		t.Errorf("Contact(unknown) = %+v, %v, want nil, nil", contact, err)
	}
}

// TestDirectory_ResolvesAContact proves a contact resolves with the four
// fields the directory publishes, its canonical email included.
func TestDirectory_ResolvesAContact(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := insertContact(t, h, "Ola", "Nordmann", ptr("ola@example.test"))

	got, err := newDirectory(t, h).Contact(context.Background(), id)
	if err != nil {
		t.Fatalf("Contact: %v", err)
	}
	if got == nil || got.ID != id || got.FirstName != "Ola" || got.LastName != "Nordmann" ||
		got.Email == nil || *got.Email != "ola@example.test" {
		t.Errorf("Contact = %+v, want Ola Nordmann <ola@example.test>", got)
	}
}

// TestDirectory_ContactsByEmailMatchesTheCanonicalEmail proves a contact
// matched on its own address offers every customer it is linked to, and that
// the address is compared trimmed and case-folded, as .NET normalised it
// before comparing.
func TestDirectory_ContactsByEmailMatchesTheCanonicalEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	contactID := insertContact(t, h, "Kari", "Nordmann", ptr("kari@example.test"))
	first := insertCustomer(t, h, "First AS", "active")
	second := insertCustomer(t, h, "Second AS", "active")
	associate(t, h, first, contactID, nil)
	associate(t, h, second, contactID, nil)

	got, err := newDirectory(t, h).ContactsByEmail(context.Background(), "  KARI@Example.TEST ")
	if err != nil {
		t.Fatalf("ContactsByEmail: %v", err)
	}
	if len(got) != 1 || got[0].ContactID != contactID || !slices.Equal(got[0].CandidateCustomerIDs, []int32{first, second}) {
		t.Errorf("ContactsByEmail = %+v, want one match for contact %d with customers %v", got, contactID, []int32{first, second})
	}
}

// TestDirectory_ContactsByEmailMatchesACustomerSpecificEmail proves an address
// that belongs to one relationship rather than to the contact resolves, and
// offers only the customer whose association carries it — never the contact's
// other customers (TS/CustomerDirectoryEmailResolutionTests.cs:40-55).
func TestDirectory_ContactsByEmailMatchesACustomerSpecificEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	contactID := insertContact(t, h, "Per", "Hansen", nil)
	billing := insertCustomer(t, h, "Billing AS", "active")
	unrelated := insertCustomer(t, h, "Unrelated AS", "active")
	associate(t, h, billing, contactID, ptr("per@billing.example.test"))
	associate(t, h, unrelated, contactID, nil)

	got, err := newDirectory(t, h).ContactsByEmail(context.Background(), "PER@Billing.example.test")
	if err != nil {
		t.Fatalf("ContactsByEmail: %v", err)
	}
	if len(got) != 1 || got[0].ContactID != contactID || !slices.Equal(got[0].CandidateCustomerIDs, []int32{billing}) {
		t.Errorf("ContactsByEmail = %+v, want one match for contact %d with only customer %d", got, contactID, unrelated)
	}
}

// TestDirectory_ContactsByEmailIsAmbiguousForTwoContacts proves the directory
// never chooses for the caller: two contacts sharing an address come back as
// two matches, each with its own candidates, and deciding between them is the
// caller's business (contracts.CustomerDirectory).
func TestDirectory_ContactsByEmailIsAmbiguousForTwoContacts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const shared = "shared@example.test"
	firstContact := insertContact(t, h, "Ada", "Berg", ptr(shared))
	secondContact := insertContact(t, h, "Bo", "Dahl", ptr(shared))
	firstCustomer := insertCustomer(t, h, "Ada AS", "active")
	secondCustomer := insertCustomer(t, h, "Bo AS", "active")
	associate(t, h, firstCustomer, firstContact, nil)
	associate(t, h, secondCustomer, secondContact, nil)

	got, err := newDirectory(t, h).ContactsByEmail(context.Background(), shared)
	if err != nil {
		t.Fatalf("ContactsByEmail: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ContactsByEmail = %+v, want two matches", got)
	}
	if got[0].ContactID != firstContact || !slices.Equal(got[0].CandidateCustomerIDs, []int32{firstCustomer}) ||
		got[1].ContactID != secondContact || !slices.Equal(got[1].CandidateCustomerIDs, []int32{secondCustomer}) {
		t.Errorf("ContactsByEmail = %+v, want contacts %d and %d with customers %d and %d",
			got, firstContact, secondContact, firstCustomer, secondCustomer)
	}
}

// TestDirectory_ContactsByEmailWithoutAMatchIsEmpty proves an address nobody
// holds is an empty result, not an error.
func TestDirectory_ContactsByEmailWithoutAMatchIsEmpty(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	insertContact(t, h, "Ola", "Nordmann", ptr("ola@example.test"))

	got, err := newDirectory(t, h).ContactsByEmail(context.Background(), "nobody@example.test")
	if err != nil || len(got) != 0 {
		t.Errorf("ContactsByEmail = %+v, %v, want an empty result and no error", got, err)
	}
}
