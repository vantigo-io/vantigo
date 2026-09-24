package customers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// directory is this module's contracts.CustomerDirectory, the one sanctioned
// way another module reads customer data (.NET's ICustomerDirectory,
// SV/ApplicationServiceCollectionExtensions.cs:20-107). It is read-only and
// holds nothing but the queries: Compose builds it once, before any module
// mounts, and hands it to every module including this one.
type directory struct {
	q *store.Queries
}

var _ contracts.CustomerDirectory = (*directory)(nil)

// newDirectory is Module's Directory: the constructor Compose calls with the
// dependencies it was given.
func newDirectory(d module.Deps) contracts.CustomerDirectory {
	return &directory{q: store.New(d.Pool)}
}

// Customer looks up a customer by id, archived ones included: a supply period
// or a conversation can hold a reference to a customer that has since been
// archived, and must still be able to name it.
func (d *directory) Customer(ctx context.Context, id int32) (*contracts.CustomerEntry, error) {
	row, err := d.q.DirectoryCustomer(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: directory customer: %w", err)
	}
	return &contracts.CustomerEntry{ID: row.ID, Name: row.Name, Archived: row.Archived,
		Group: groupEntry(row.GroupID, row.GroupName), MergedInto: row.MergedIntoCustomerID}, nil
}

// Customers looks up customers by id in one round trip, archived ones
// included — the batch twin of Customer, for a caller naming a whole page
// of customers rather than one per row. A nil or empty ids is an empty,
// non-nil result with no query made; a duplicate id in ids is tolerated
// since the row itself only exists once (DirectoryCustomers, queries/
// customers.sql).
func (d *directory) Customers(ctx context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
	if len(ids) == 0 {
		return []contracts.CustomerEntry{}, nil
	}
	rows, err := d.q.DirectoryCustomers(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("customers: directory customers: %w", err)
	}
	entries := make([]contracts.CustomerEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, contracts.CustomerEntry{ID: row.ID, Name: row.Name, Archived: row.Archived,
			Group: groupEntry(row.GroupID, row.GroupName), MergedInto: row.MergedIntoCustomerID})
	}
	return entries, nil
}

// groupEntry is a directory row's group, nil when the customer belongs to none.
// Shared by Customer and Customers so the single lookup and the batch can never
// answer differently about the same customer.
func groupEntry(id *uuid.UUID, name *string) *contracts.CustomerGroupEntry {
	if id == nil {
		return nil
	}
	return &contracts.CustomerGroupEntry{ID: *id, Name: deref(name)}
}

// Contact looks up a contact by id.
func (d *directory) Contact(ctx context.Context, id int32) (*contracts.ContactEntry, error) {
	row, err := d.q.DirectoryContact(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: directory contact: %w", err)
	}
	return &contracts.ContactEntry{ID: row.ID, FirstName: row.FirstName, LastName: row.LastName, Email: row.Email}, nil
}

// ContactsByEmail finds every contact reachable at email, each with the
// customers that match makes candidates. The address is trimmed and
// lower-cased first, as .NET normalised it before comparing. More than one
// match is ambiguous, and resolving it is the caller's business, not the
// directory's.
func (d *directory) ContactsByEmail(ctx context.Context, email string) ([]contracts.ContactMatch, error) {
	rows, err := d.q.DirectoryContactsByEmail(ctx, normalizeEmail(email))
	if err != nil {
		return nil, fmt.Errorf("customers: directory contacts by email: %w", err)
	}
	matches := make([]contracts.ContactMatch, 0, len(rows))
	for _, row := range rows {
		matches = append(matches, contracts.ContactMatch{
			ContactID:            row.ContactID,
			CandidateCustomerIDs: row.CandidateCustomerIds,
		})
	}
	return matches, nil
}

// normalizeEmail is the comparison form of an address: trimmed and
// lower-cased, .NET's Trim().ToLowerInvariant().
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// BillingProfile is what an invoice needs to know about a customer, already
// resolved (contracts.CustomerDirectory's own doc comment): two queries —
// the customer row with billing, identity and contact columns
// (DirectoryBillingProfile), and the resolved invoice address
// (DirectoryInvoiceAddress) — then resolveBillingProfile, one pure function
// that applies every resolution rule. The first query also carries the
// customer's group's default payment term (customer groups design D4).
func (d *directory) BillingProfile(ctx context.Context, id int32) (*contracts.CustomerBillingProfile, error) {
	row, err := d.q.DirectoryBillingProfile(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: directory billing profile: %w", err)
	}

	var invoiceAddress *contracts.CustomerAddressEntry
	addr, err := d.q.DirectoryInvoiceAddress(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Neither a primary invoice nor a primary postal address: D3's
		// resolution rule found nothing, and invoiceAddress stays nil.
	case err != nil:
		return nil, fmt.Errorf("customers: directory invoice address: %w", err)
	default:
		invoiceAddress = &contracts.CustomerAddressEntry{
			Label: deref(addr.Label), Line1: addr.Line1, Line2: deref(addr.Line2),
			PostalCode: deref(addr.PostalCode), City: deref(addr.City), Region: deref(addr.Region), Country: addr.Country,
		}
	}

	defaultBillRate, err := floatPtrFromNumeric(row.DefaultBillRate)
	if err != nil {
		return nil, err
	}
	profile := billingProfileFromRow(row.InvoiceEmail, row.ReminderEmail, row.PaymentTermsDays,
		row.Currency, row.Language, row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference, defaultBillRate)
	identity := identityFromRow(row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType)

	return resolveBillingProfile(row.ID, row.CustomerNumber, row.Name, row.Type, row.Archived,
		identity, row.Email, profile, row.GroupDefaultPaymentTermsDays, invoiceAddress), nil
}

// resolveBillingProfile is contracts.CustomerDirectory.BillingProfile's one
// resolution function (invoice-ready customer design D5): every rule the
// interface's own doc comment promises, applied once here so no consumer
// ever re-derives an invoice email, a reminder email or a Peppol id for
// itself. It takes the same billingProfile and *legalIdentity value objects
// billing_profile.go itself builds from a customer row
// (billingProfileFromRow, identityFromRow) — which is what makes it
// unit-testable with plain structs, no database required by the test.
//
//   - InvoiceEmail: the billing profile's own invoiceEmail, else the
//     customer's own contact-info email, else "".
//   - ReminderEmail: the billing profile's own reminderEmail, else the
//     InvoiceEmail just resolved above — reminders fall back to where an
//     invoice would go, never straight to the contact-info email.
//   - PeppolID: the billing profile's own explicit peppolId, else
//     derivedPeppolID(identity, customerType) (billing_values.go, final
//     review fix I1 — the one predicate billingWarnings's ehf_without_
//     recipient check now shares, so the two can never disagree about
//     whether a recipient exists), else "".
//   - PaymentTermsDays: the billing profile's own paymentTermsDays, else the
//     customer's GROUP's default (customer groups design D4), else nil. The
//     first field here with a THREE-level chain, and the only one that inherits
//     from a group at all: currency, language and the delivery methods stay
//     "not decided here" until somebody asks for a default. nil out means
//     nobody decided, which is what every consumer already reads nil as — a
//     group with no default of its own and no group at all are the same answer.
//   - DefaultBillRate: the billing profile's own defaultBillRate, else nil
//     (customers bill-rate design D2) — no group tier, so it passes through
//     like Currency, the currency it is quoted in. Time's rate chain reads it.
func resolveBillingProfile(id int32, customerNumber int64, name, customerType string, archived bool,
	identity *legalIdentity, contactEmail *string, profile billingProfile, groupDefaultPaymentTermsDays *int32,
	invoiceAddress *contracts.CustomerAddressEntry,
) *contracts.CustomerBillingProfile {
	invoiceEmail := deref(profile.InvoiceEmail)
	if invoiceEmail == "" {
		invoiceEmail = deref(contactEmail)
	}
	reminderEmail := deref(profile.ReminderEmail)
	if reminderEmail == "" {
		reminderEmail = invoiceEmail
	}

	peppolID := deref(profile.PeppolID)
	if peppolID == "" {
		peppolID = derivedPeppolID(identity, customerType)
	}

	// A nil check, not a zero check: 0 days is "due on receipt", a decision the
	// group must not override.
	paymentTermsDays := profile.PaymentTermsDays
	if paymentTermsDays == nil {
		paymentTermsDays = groupDefaultPaymentTermsDays
	}

	var legalCountry, legalID, legalName string
	if identity != nil {
		legalCountry, legalID, legalName = identity.Country, identity.ID, identity.Name
	}

	return &contracts.CustomerBillingProfile{
		ID: id, CustomerNumber: customerNumber, Name: name, Type: customerType, Archived: archived,
		LegalCountry:     legalCountry,
		LegalID:          legalID,
		LegalName:        legalName,
		InvoiceAddress:   invoiceAddress,
		InvoiceEmail:     invoiceEmail,
		ReminderEmail:    reminderEmail,
		PaymentTermsDays: paymentTermsDays,
		Currency:         deref(profile.Currency),
		Language:         deref(profile.Language),
		InvoiceDelivery:  deref(profile.InvoiceDelivery),
		ReminderDelivery: deref(profile.ReminderDelivery),
		PeppolID:         peppolID,
		GLN:              deref(profile.Gln),
		BuyerReference:   deref(profile.BuyerReference),
		DefaultBillRate:  profile.DefaultBillRate,
	}
}
