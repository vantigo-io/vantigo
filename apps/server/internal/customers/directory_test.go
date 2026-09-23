package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
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

// --- Customers (batch) ------------------------------------------------------

// TestDirectory_Customers_ReturnsFoundOnesOnly proves the batch lookup
// answers with exactly the customers it found, silently dropping an id that
// does not exist rather than erroring or padding the result.
func TestDirectory_Customers_ReturnsFoundOnesOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	first := insertCustomer(t, h, "Nordvest Kraft AS", "active")
	second := insertCustomer(t, h, "Sørvest Energi AS", "active")

	got, err := newDirectory(t, h).Customers(context.Background(), []int32{first, 999_999, second})
	if err != nil {
		t.Fatalf("Customers: %v", err)
	}
	want := []contracts.CustomerEntry{
		{ID: first, Name: "Nordvest Kraft AS"},
		{ID: second, Name: "Sørvest Energi AS"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Customers = %+v, want %+v", got, want)
	}
}

// TestDirectory_Customers_ArchivedIncluded proves the batch lookup resolves
// an archived customer, flagged, exactly as the single lookup does.
func TestDirectory_Customers_ArchivedIncluded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := insertCustomer(t, h, "Fjordkraft Sør AS", "archived")

	got, err := newDirectory(t, h).Customers(context.Background(), []int32{id})
	if err != nil {
		t.Fatalf("Customers: %v", err)
	}
	if len(got) != 1 || !got[0].Archived || got[0].Name != "Fjordkraft Sør AS" {
		t.Errorf("Customers = %+v, want one archived Fjordkraft Sør AS", got)
	}
}

// TestDirectory_Customers_EmptyOrNilInputQueriesNothing proves both a nil and
// an empty ids answer an empty, non-nil slice without ever querying: the
// directory here is built on a nil pool (module.Deps{}, the same technique
// TestActualsForProjectsWithoutRequestsQueriesNothing and
// TestProjectExpensesWithoutProjectsQueriesNothing use), so any query at all
// would panic rather than answer — proving the guard runs before the query,
// not merely that the query happens to behave for an empty input.
func TestDirectory_Customers_EmptyOrNilInputQueriesNothing(t *testing.T) {
	t.Parallel()
	dir := customers.Module().Directory(module.Deps{})

	for name, ids := range map[string][]int32{"nil": nil, "empty": {}} {
		got, err := dir.Customers(context.Background(), ids)
		if err != nil {
			t.Errorf("Customers(%s) error = %v, want nil", name, err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("Customers(%s) = %#v, want a non-nil empty slice", name, got)
		}
	}
}

// TestDirectory_Customers_DuplicateIdsAreNotDuplicatedInTheResult proves a
// repeated id in the request comes back once, not once per repetition.
func TestDirectory_Customers_DuplicateIdsAreNotDuplicatedInTheResult(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := insertCustomer(t, h, "Nordvest Kraft AS", "active")

	got, err := newDirectory(t, h).Customers(context.Background(), []int32{id, id, id})
	if err != nil {
		t.Fatalf("Customers: %v", err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Errorf("Customers = %+v, want exactly one entry for %d", got, id)
	}
}

// TestDirectory_Customers_OrderedByAscendingID proves the result is ordered
// by id, not by the request's own order, so two callers asking for the same
// set always see it the same way.
func TestDirectory_Customers_OrderedByAscendingID(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	first := insertCustomer(t, h, "First AS", "active")
	second := insertCustomer(t, h, "Second AS", "active")
	third := insertCustomer(t, h, "Third AS", "active")

	got, err := newDirectory(t, h).Customers(context.Background(), []int32{third, first, second})
	if err != nil {
		t.Fatalf("Customers: %v", err)
	}
	if len(got) != 3 || got[0].ID != first || got[1].ID != second || got[2].ID != third {
		t.Errorf("Customers = %+v, want ascending order [%d %d %d]", got, first, second, third)
	}
}

// --- BillingProfile -----------------------------------------------------

// setCustomerType sets a customer's type column directly, bypassing the
// customer-type sub-resource's own write (customer_type.go), which this
// file's resolution tests have no business exercising.
func setCustomerType(t *testing.T, h *modtest.Harness, id int32, customerType string) {
	t.Helper()
	h.Exec(t, `UPDATE customers.customers SET type = $2 WHERE id = $1`, id, customerType)
}

// setLegalIdentity sets a customer's five legal-identity columns directly,
// all together — the same all-or-none invariant identityFromRow assumes.
func setLegalIdentity(t *testing.T, h *modtest.Harness, id int32, country, legalID, name string) {
	t.Helper()
	h.Exec(t, `UPDATE customers.customers SET legal_country = $2, legal_id = $3, legal_name = $4, legal_source = 'manual', legal_type = $5 WHERE id = $1`,
		id, country, legalID, name, "business")
}

// setBillingFields sets a customer's own invoiceEmail/reminderEmail/peppolId
// billing columns directly, bypassing PUT .../billing-profile: this file's
// resolution tests seed the stored values BillingProfile then resolves.
func setBillingFields(t *testing.T, h *modtest.Harness, id int32, invoiceEmail, reminderEmail, peppolID *string) {
	t.Helper()
	h.Exec(t, `UPDATE customers.customers SET invoice_email = $2, reminder_email = $3, peppol_id = $4 WHERE id = $1`,
		id, invoiceEmail, reminderEmail, peppolID)
}

// setContactEmail sets a customer's own contact-info email directly.
func setContactEmail(t *testing.T, h *modtest.Harness, id int32, email *string) {
	t.Helper()
	h.Exec(t, `UPDATE customers.customers SET email = $2 WHERE id = $1`, id, email)
}

// insertFullAddress inserts one address with every printable column set, so
// a BillingProfile test can prove InvoiceAddress carries every field across,
// not merely picks the right type — insertAddress (addresses_test.go) leaves
// most columns blank, which is enough for the address sub-resource's own
// tests but not for this one.
func insertFullAddress(t *testing.T, h *modtest.Harness, customerID int32, addrType string, isPrimary bool) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `
		INSERT INTO customers.customer_addresses (customer_id, type, label, line1, line2, postal_code, city, region, country, is_primary, created_at, updated_at)
		VALUES ($1, $2, 'HQ', 'Storgata 1', 'Suite 2', '0155', 'Oslo', 'Oslo', 'no', $3, $4, $4)
		RETURNING id`, customerID, addrType, isPrimary, h.Now())
}

// TestDirectory_BillingProfile_UnknownIdIsNilWithoutAnError proves a missing
// customer is (nil, nil), the same rule every other lookup here follows.
func TestDirectory_BillingProfile_UnknownIdIsNilWithoutAnError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	got, err := newDirectory(t, h).BillingProfile(context.Background(), 999_999)
	if got != nil || err != nil {
		t.Errorf("BillingProfile(unknown) = %+v, %v, want nil, nil", got, err)
	}
}

// TestDirectory_BillingProfile_ArchivedResolves proves an archived customer's
// billing profile still resolves, flagged — a past invoice can hold a
// reference to a customer archived since, and must still be able to invoice
// it again.
func TestDirectory_BillingProfile_ArchivedResolves(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := insertCustomer(t, h, "Nedlagt Handel AS", "archived")

	got, err := newDirectory(t, h).BillingProfile(context.Background(), id)
	if err != nil {
		t.Fatalf("BillingProfile: %v", err)
	}
	if got == nil || !got.Archived || got.Name != "Nedlagt Handel AS" {
		t.Errorf("BillingProfile = %+v, want an archived Nedlagt Handel AS", got)
	}
}

// TestDirectory_BillingProfile_InvoiceEmail_FallsBackToContactEmail proves
// the resolution order both ways: the billing profile's own invoiceEmail
// wins when set, and the customer's own contact-info email is used only
// when it is not.
func TestDirectory_BillingProfile_InvoiceEmail_FallsBackToContactEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	withBoth := insertCustomer(t, h, "Med Begge AS", "active")
	setBillingFields(t, h, withBoth, ptr("faktura@medbegge.example.test"), nil, nil)
	setContactEmail(t, h, withBoth, ptr("post@medbegge.example.test"))

	withOnlyContact := insertCustomer(t, h, "Kun Kontakt AS", "active")
	setContactEmail(t, h, withOnlyContact, ptr("post@kunkontakt.example.test"))

	withNeither := insertCustomer(t, h, "Uten Epost AS", "active")

	dir := newDirectory(t, h)

	got, err := dir.BillingProfile(context.Background(), withBoth)
	if err != nil {
		t.Fatalf("BillingProfile(withBoth): %v", err)
	}
	if got.InvoiceEmail != "faktura@medbegge.example.test" {
		t.Errorf("InvoiceEmail = %q, want the billing profile's own address, not the contact one", got.InvoiceEmail)
	}

	got, err = dir.BillingProfile(context.Background(), withOnlyContact)
	if err != nil {
		t.Fatalf("BillingProfile(withOnlyContact): %v", err)
	}
	if got.InvoiceEmail != "post@kunkontakt.example.test" {
		t.Errorf("InvoiceEmail = %q, want the contact-info email as fallback", got.InvoiceEmail)
	}

	got, err = dir.BillingProfile(context.Background(), withNeither)
	if err != nil {
		t.Fatalf("BillingProfile(withNeither): %v", err)
	}
	if got.InvoiceEmail != "" {
		t.Errorf("InvoiceEmail = %q, want empty when neither is set", got.InvoiceEmail)
	}
}

// TestDirectory_BillingProfile_ReminderEmail_FallsBackToResolvedInvoiceEmail
// proves the reminder rule both ways: the billing profile's own
// reminderEmail wins when set, and the *resolved* invoice email — itself
// possibly the contact-info fallback — is used when it is not.
func TestDirectory_BillingProfile_ReminderEmail_FallsBackToResolvedInvoiceEmail(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	withOwnReminder := insertCustomer(t, h, "Egen Purring AS", "active")
	setBillingFields(t, h, withOwnReminder, ptr("faktura@egenpurring.example.test"), ptr("purring@egenpurring.example.test"), nil)

	fallsBack := insertCustomer(t, h, "Faller Tilbake AS", "active")
	setContactEmail(t, h, fallsBack, ptr("post@fallertilbake.example.test"))

	dir := newDirectory(t, h)

	got, err := dir.BillingProfile(context.Background(), withOwnReminder)
	if err != nil {
		t.Fatalf("BillingProfile(withOwnReminder): %v", err)
	}
	if got.ReminderEmail != "purring@egenpurring.example.test" {
		t.Errorf("ReminderEmail = %q, want the billing profile's own reminder address", got.ReminderEmail)
	}

	got, err = dir.BillingProfile(context.Background(), fallsBack)
	if err != nil {
		t.Fatalf("BillingProfile(fallsBack): %v", err)
	}
	if got.ReminderEmail != "post@fallertilbake.example.test" {
		t.Errorf("ReminderEmail = %q, want the resolved invoice email (the contact-info fallback) as reminder", got.ReminderEmail)
	}
}

// TestDirectory_BillingProfile_PeppolID_ExplicitWinsOverDerived proves the
// explicit peppolId always wins, even for a Norwegian business identity that
// could otherwise derive one.
func TestDirectory_BillingProfile_PeppolID_ExplicitWinsOverDerived(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := insertCustomer(t, h, "Eksplisitt Peppol AS", "active")
	setCustomerType(t, h, id, "business")
	setLegalIdentity(t, h, id, "no", "923609016", "Eksplisitt Peppol AS")
	setBillingFields(t, h, id, nil, nil, ptr("9908:999999999"))

	got, err := newDirectory(t, h).BillingProfile(context.Background(), id)
	if err != nil {
		t.Fatalf("BillingProfile: %v", err)
	}
	if got.PeppolID != "9908:999999999" {
		t.Errorf("PeppolID = %q, want the explicit id, not a derived one", got.PeppolID)
	}
}

// TestDirectory_BillingProfile_PeppolID_DerivedForNorwegianBusinessIdentity
// proves the derivation rule both ways: a Norwegian business identity with
// no explicit peppolId derives "0192:<orgnr>", and each condition that rule
// needs — Norwegian country, business type, an identity at all — being
// false in turn leaves PeppolID empty instead.
func TestDirectory_BillingProfile_PeppolID_DerivedForNorwegianBusinessIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	dir := newDirectory(t, h)

	derived := insertCustomer(t, h, "Avledet Peppol AS", "active")
	setCustomerType(t, h, derived, "business")
	setLegalIdentity(t, h, derived, "no", "974760673", "Avledet Peppol AS")

	got, err := dir.BillingProfile(context.Background(), derived)
	if err != nil {
		t.Fatalf("BillingProfile(derived): %v", err)
	}
	if got.PeppolID != "0192:974760673" {
		t.Errorf("PeppolID = %q, want the derived 0192:974760673", got.PeppolID)
	}

	notNorwegian := insertCustomer(t, h, "Utenlandsk AS", "active")
	setCustomerType(t, h, notNorwegian, "business")
	setLegalIdentity(t, h, notNorwegian, "se", "5560360793", "Utenlandsk AS")
	got, err = dir.BillingProfile(context.Background(), notNorwegian)
	if err != nil {
		t.Fatalf("BillingProfile(notNorwegian): %v", err)
	}
	if got.PeppolID != "" {
		t.Errorf("PeppolID = %q, want empty for a non-Norwegian identity", got.PeppolID)
	}

	person := insertCustomer(t, h, "Ola Privatperson", "active")
	setCustomerType(t, h, person, "person")
	setLegalIdentity(t, h, person, "no", "01019012345", "Ola Privatperson")
	got, err = dir.BillingProfile(context.Background(), person)
	if err != nil {
		t.Fatalf("BillingProfile(person): %v", err)
	}
	if got.PeppolID != "" {
		t.Errorf("PeppolID = %q, want empty for a person, business type only", got.PeppolID)
	}

	noIdentity := insertCustomer(t, h, "Uten Identitet AS", "active")
	setCustomerType(t, h, noIdentity, "business")
	got, err = dir.BillingProfile(context.Background(), noIdentity)
	if err != nil {
		t.Fatalf("BillingProfile(noIdentity): %v", err)
	}
	if got.PeppolID != "" {
		t.Errorf("PeppolID = %q, want empty with no legal identity to derive from", got.PeppolID)
	}
}

// TestDirectory_BillingProfile_InvoiceAddress_PrefersPrimaryInvoiceOverPostal
// proves D3's resolution order both ways: a primary invoice address wins
// over a primary postal one when both exist, and the postal one is used
// when there is no invoice address at all.
func TestDirectory_BillingProfile_InvoiceAddress_PrefersPrimaryInvoiceOverPostal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	dir := newDirectory(t, h)

	both := insertCustomer(t, h, "Begge Adresser AS", "active")
	insertAddress(t, h, both, "postal", true)
	insertFullAddress(t, h, both, "invoice", true)

	got, err := dir.BillingProfile(context.Background(), both)
	if err != nil {
		t.Fatalf("BillingProfile(both): %v", err)
	}
	if got.InvoiceAddress == nil || got.InvoiceAddress.Line1 != "Storgata 1" || got.InvoiceAddress.City != "Oslo" {
		t.Errorf("InvoiceAddress = %+v, want the primary invoice address, not the postal one", got.InvoiceAddress)
	}

	postalOnly := insertCustomer(t, h, "Kun Postadresse AS", "active")
	insertFullAddress(t, h, postalOnly, "postal", true)
	got, err = dir.BillingProfile(context.Background(), postalOnly)
	if err != nil {
		t.Fatalf("BillingProfile(postalOnly): %v", err)
	}
	if got.InvoiceAddress == nil || got.InvoiceAddress.Line1 != "Storgata 1" {
		t.Errorf("InvoiceAddress = %+v, want the primary postal address as fallback", got.InvoiceAddress)
	}

	neither := insertCustomer(t, h, "Ingen Adresse AS", "active")
	insertAddress(t, h, neither, "delivery", true)
	got, err = dir.BillingProfile(context.Background(), neither)
	if err != nil {
		t.Fatalf("BillingProfile(neither): %v", err)
	}
	if got.InvoiceAddress != nil {
		t.Errorf("InvoiceAddress = %+v, want nil with no invoice or postal address", got.InvoiceAddress)
	}
}

// TestDirectory_BillingProfile_MalformedLegacyLegalId_DerivesNothing_AndEHFWarningFires
// pins final review fix I1: a row seeded before this module's own legal-id
// validation existed can hold a legal_id that is not a valid Norwegian
// organisation number (e.g. "NO 923 609 016 MVA", spaced and lettered rather
// than the nine bare digits validateLegalIdentity would store today).
// derivedPeppolID (billing_values.go), the one predicate both the directory
// and billingWarnings now share, must refuse to derive a Peppol recipient
// from it, and PUT .../billing-profile's own ehf_without_recipient warning
// must still fire — the two must never disagree about whether a recipient
// exists.
func TestDirectory_BillingProfile_MalformedLegacyLegalId_DerivesNothing_AndEHFWarningFires(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Legacy Malformed Id AS")
	setCustomerType(t, h, created.Id, "business")
	setLegalIdentity(t, h, created.Id, "no", "NO 923 609 016 MVA", "Legacy Malformed Id AS")

	got, err := newDirectory(t, h).BillingProfile(context.Background(), created.Id)
	if err != nil {
		t.Fatalf("BillingProfile: %v", err)
	}
	if got.PeppolID != "" {
		t.Errorf("PeppolID = %q, want empty: a malformed legacy legal id derives nothing", got.PeppolID)
	}

	r := putBillingProfile(t, c, created.Id, map[string]any{"invoiceDelivery": "ehf"})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var profile billingProfileJSON
	r.JSON(&profile)
	if !hasWarning(profile.Warnings, "ehf_without_recipient") {
		t.Errorf("warnings = %v, want ehf_without_recipient (the malformed legacy id gives no recipient either)", profile.Warnings)
	}
}

// TestDirectory_BillingProfile_NullLegalTypeLegacyRow_BothFunctionsAgree pins
// I1's other legacy shape: legal_country/legal_id set but legal_type left
// NULL (a row from before legal_type was populated consistently — identity.Type
// decodes as "" for it, identityFromRow, customers.go). derivedPeppolID reads
// the customer's own type, never identity.Type, so both the directory's
// derived PeppolID and PUT .../billing-profile's own ehf_without_recipient
// warning agree: before this fix, resolveBillingProfile already ignored
// identity.Type here but billingWarnings required it, so this exact row made
// the two disagree.
func TestDirectory_BillingProfile_NullLegalTypeLegacyRow_BothFunctionsAgree(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Legacy Null Type AS")
	setCustomerType(t, h, created.Id, "business")
	h.Exec(t, `UPDATE customers.customers SET legal_country = $2, legal_id = $3, legal_name = $4, legal_source = 'manual', legal_type = NULL WHERE id = $1`,
		created.Id, "no", "974760673", "Legacy Null Type AS")

	got, err := newDirectory(t, h).BillingProfile(context.Background(), created.Id)
	if err != nil {
		t.Fatalf("BillingProfile: %v", err)
	}
	if got.PeppolID != "0192:974760673" {
		t.Errorf("PeppolID = %q, want the derived 0192:974760673 (legal_type NULL must not block derivation)", got.PeppolID)
	}

	r := putBillingProfile(t, c, created.Id, map[string]any{"invoiceDelivery": "ehf"})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var profile billingProfileJSON
	r.JSON(&profile)
	if hasWarning(profile.Warnings, "ehf_without_recipient") {
		t.Errorf("warnings = %v, want no ehf_without_recipient (the directory can derive a recipient for this same row)", profile.Warnings)
	}
}

// TestDirectory_BillingProfileAndCustomersSeeTheGroup is design D4 through the
// real queries: the directory resolves an unset payment term from the group's
// default, and CustomerEntry names the group so Products phase 4 can resolve a
// group price without reading a billing profile for it.
func TestDirectory_BillingProfileAndCustomersSeeTheGroup(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail", "defaultPaymentTermsDays": 30})
	inherits := createCustomer(t, c, "Inherits AS")
	decides := createCustomer(t, c, "Decides AS")
	for _, id := range []int32{inherits.Id, decides.Id} {
		if r := putCustomerGroup(t, c, id, map[string]any{"groupId": retail.Id}); r.Status != http.StatusOK {
			t.Fatalf("group customer %d: status %d body %s", id, r.Status, r.Body)
		}
	}
	if r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/billing-profile", decides.Id),
		map[string]any{"paymentTermsDays": 14}); r.Status != http.StatusOK {
		t.Fatalf("set own terms: status %d body %s", r.Status, r.Body)
	}

	dir := newDirectory(t, h)
	profile, err := dir.BillingProfile(context.Background(), inherits.Id)
	if err != nil || profile == nil {
		t.Fatalf("BillingProfile(%d) = %v, %v", inherits.Id, profile, err)
	}
	if profile.PaymentTermsDays == nil || *profile.PaymentTermsDays != 30 {
		t.Errorf("PaymentTermsDays = %v, want 30 from the group", profile.PaymentTermsDays)
	}
	own, err := dir.BillingProfile(context.Background(), decides.Id)
	if err != nil || own == nil {
		t.Fatalf("BillingProfile(%d) = %v, %v", decides.Id, own, err)
	}
	if own.PaymentTermsDays == nil || *own.PaymentTermsDays != 14 {
		t.Errorf("PaymentTermsDays = %v, want 14: the customer's own value wins", own.PaymentTermsDays)
	}

	entry, err := dir.Customer(context.Background(), inherits.Id)
	if err != nil || entry == nil {
		t.Fatalf("Customer(%d) = %v, %v", inherits.Id, entry, err)
	}
	if entry.Group == nil || entry.Group.Name != "Retail" || entry.Group.ID.String() != retail.Id {
		t.Fatalf("Customer(…).Group = %+v, want Retail (%s)", entry.Group, retail.Id)
	}
	entries, err := dir.Customers(context.Background(), []int32{inherits.Id, decides.Id})
	if err != nil || len(entries) != 2 {
		t.Fatalf("Customers(…) = %v, %v", entries, err)
	}
	for _, e := range entries {
		if e.Group == nil || e.Group.ID != entry.Group.ID || e.Group.Name != "Retail" {
			t.Errorf("Customers(…) entry %d group = %+v, want the same group the single lookup answered", e.ID, e.Group)
		}
	}
}
