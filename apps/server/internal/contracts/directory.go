package contracts

import (
	"context"

	"github.com/google/uuid"
)

// CustomerEntry is a customer as another module may reference it: enough to
// name it in a UI or a document, never enough to manage it — that stays
// behind customers' own contract and permissions.
type CustomerEntry struct {
	ID       int32
	Name     string
	Archived bool
	// Group is the group the customer belongs to, nil when it belongs to none
	// (customer groups design D4). A customer belongs to at most one.
	Group *CustomerGroupEntry
	// MergedInto is the customer this one was merged into (customers merge
	// design D3), nil unless it was merged away — always the last survivor of
	// a chain, never a customer merged away itself. A merged-away customer is
	// archived, refuses every write the customers module takes, and still
	// resolves — a consumer holding a stale id learns where to look. Every
	// CustomerReferenceHolder re-points the references that existed when the
	// merge ran; one written for this id afterwards (or committed while the
	// merge ran) is what this field is the backstop for.
	MergedInto *int32
	// IndustryCode is the customer's NACE industry code as its registry record
	// (Brønnøysund) holds it, such as "24.420"; empty when the customer has no
	// registry record or the record no code. Energy reads it to suggest a
	// metering point's electricity-tax class (energy electricity-tax suggestion
	// design) — a hint, never a decision any module stores.
	IndustryCode string
}

// CustomerGroupEntry is the group a customer belongs to, as another module may
// reference it: an id and a name, which is what it takes to show the group and
// to look a group-specific decision up by. Products phase 4 (customer-group
// prices) is the intended reader; the id is stable and this module's own, so a
// consumer that resolves a price by group must never read a billing profile for
// it.
type CustomerGroupEntry struct {
	ID   uuid.UUID
	Name string
}

// ContactEntry is a contact as another module may reference it.
type ContactEntry struct {
	ID        int32
	FirstName string
	LastName  string
	Email     *string
}

// ContactMatch is one contact ContactsByEmail found, together with every
// customer it is currently linked to.
type ContactMatch struct {
	ContactID            int32
	CandidateCustomerIDs []int32
}

// CustomerAddressEntry is one resolved address, embedded in
// CustomerBillingProfile.InvoiceAddress and listed in its DeliveryAddresses:
// enough to print on a document, never the address's type — the profile has
// already picked which ones apply.
//
// ID is the address's own id in a DeliveryAddresses entry, so a document's
// chosen delivery address is found again on refresh (orders design O10), and
// 0 on the resolved InvoiceAddress, which a consumer never asks for again.
//
// Country is an ISO 3166-1 alpha-2 code in lower case, as customers stores
// it; a consumer compares it case-insensitively and prints it in whatever
// case its own format wants.
type CustomerAddressEntry struct {
	ID                                                     int32
	Label, Line1, Line2, PostalCode, City, Region, Country string
}

// CustomerBillingProfile is what an invoice needs to know about a customer —
// and, since customers bill-rate design D2, the default bill rate Time's rate
// chain prices hours with — already resolved: every rule that would otherwise
// have to be re-derived by whoever sends the invoice or prices the hours is
// applied once, here, by the module that owns the data (invoice-ready
// customer design D5).
type CustomerBillingProfile struct {
	ID             int32
	CustomerNumber int64
	Name           string
	Type           string // "business" | "person"
	Archived       bool
	// Status is the customer's own status — "active", "disabled" or
	// "archived" (an anonymised person is archived) — so one read answers every
	// gate an invoice keeps as well as its snapshot (invoices foundation design
	// D10). "disabled" means blocked for invoicing: Invoices refuses a new
	// invoice to it, never a credit note or a read.
	Status string
	// MergedInto is the customer this one was merged into, nil unless it was
	// merged away — the same answer CustomerEntry.MergedInto gives, so a
	// consumer holding a stale id learns where its references went.
	MergedInto *int32
	// LegalCountry is the legal identity's ISO 3166-1 alpha-2 code in lower
	// case, as customers stores it — compared case-insensitively by a
	// consumer — and "" when the customer has no legal identity.
	LegalCountry string
	LegalID      string
	LegalName    string
	// InvoiceAddress is the resolved invoice address (D3's rule: primary
	// invoice, else primary postal, else nil) — never a list, since a
	// consumer only ever needs the one address to print.
	InvoiceAddress *CustomerAddressEntry
	// InvoiceEmail is already resolved: the billing profile's own
	// invoiceEmail, else the customer's own contact-info email, else "".
	InvoiceEmail string
	// ReminderEmail is already resolved too: the billing profile's own
	// reminderEmail, else the resolved InvoiceEmail above.
	ReminderEmail string
	// PaymentTermsDays is already resolved: the billing profile's own
	// paymentTermsDays, else the customer's group's default (customer groups
	// design D4), else nil — so nil means neither the customer nor its group
	// decided, and a consumer never applies group logic itself.
	PaymentTermsDays *int32
	Currency         string
	Language         string
	InvoiceDelivery  string
	ReminderDelivery string
	// PeppolID is already resolved: the billing profile's own explicit
	// peppolId, else "0192:<legal id>" for a Norwegian business identity,
	// else "".
	PeppolID       string
	GLN            string
	BuyerReference string
	// DefaultBillRate is the customer's own default hourly bill rate
	// (customers bill-rate design D2), quoted in Currency — which is never ""
	// when this is set, since the billing profile refuses a rate without a
	// currency — and nil when the customer set none. Own value only: no group
	// tier, so nil means this customer decided nothing, and a consumer pricing
	// hours (Time's rate chain) falls through to its next step.
	DefaultBillRate *float64
	// DeliveryAddresses are the customer's delivery addresses, the primary
	// one first, then the others, each with its ID (orders design O10); empty
	// when the customer has none. Filled only when the caller asks
	// (BillingProfileOptions.WithDelivery), nil otherwise.
	DeliveryAddresses []CustomerAddressEntry
	// Contacts are the customer's contacts, archived ones left out (orders
	// design O10). Filled only when the caller asks
	// (BillingProfileOptions.WithContacts), nil otherwise.
	Contacts []CustomerContactEntry
}

// CustomerContactEntry is one contact of a customer, as
// CustomerBillingProfile.Contacts lists it: enough to name the buyer's
// contact on a document. Email and Phone are "" when the contact has none;
// Roles are the contact's roles on this customer, empty when it has none.
type CustomerContactEntry struct {
	ID                 int32
	Name, Email, Phone string
	Roles              []string
}

// BillingProfileOptions are what a BillingProfile caller asks for beyond the
// profile itself (orders design O10), so a caller that needs neither — every
// invoice — pays nothing for them.
type BillingProfileOptions struct {
	WithDelivery bool
	WithContacts bool
}

// CustomerDirectory is the one sanctioned way a module reads another
// module's data: a read-only, in-process port over customer data, so energy
// can name a customer on a supply period and communications can resolve a
// contact by email, without either importing the customers package (barred
// by depguard) or reading its PostgreSQL schema (barred by
// internal/db/schema_test.go). Whichever enabled module owns customer data
// implements it; Compose wires that implementation into every module's Deps
// before any Mount runs (see Module.Directory).
//
// Rules that hold across every method here, because none of them are
// obvious from the signatures alone:
//
//   - A missing row is (nil, nil), not an error — for Customer, Contact and
//     BillingProfile alike. A caller tells "does not exist" from "the
//     lookup failed" by checking err, never by treating a nil result as
//     failure. Customers is the batch exception: a missing id is simply
//     absent from the result slice, never an entry in it, and never an
//     error either — the same rule, restated for a slice instead of a
//     pointer; and so is CustomersByLegalID, whose unheld id is absent from
//     the map.
//   - Archived customers still resolve, from every one of Customer,
//     Customers, CustomersByLegalID and BillingProfile. A consumer (a supply
//     period, a past invoice) can hold a long-lived reference to a customer
//     that has since been archived, and must still be able to show its name
//     — so a past invoice can be shown and credited; Archived tells the
//     caller to decorate that reference, not that the lookup failed. Invoices
//     refuses a new invoice to an archived or disabled customer (the billing
//     profile's Status, invoices foundation design D10).
//   - More than one candidate from ContactsByEmail is ambiguous. A contact's
//     email is not unique across customers, and the directory does not
//     guess which customer the caller means: it returns every candidate,
//     and the caller — never the directory — decides how to resolve the
//     ambiguity, typically by asking a person.
//   - BillingProfile resolves every field it can, once, so no consumer ever
//     re-derives an invoice email, a reminder email, a payment term, a Peppol
//     id or "the" invoice address itself: a caller receiving an empty string or a nil
//     pointer back has learned that nothing was decided for that field, not
//     that the lookup failed, and is free to apply its own default the
//     billing profile's own GET endpoint would not presume to pick — or, for
//     the default bill rate, to fall through to its own next step (Time's
//     rate chain reads the profile too, customers bill-rate design D2).
type CustomerDirectory interface {
	// Customer looks up a customer by ID. It returns (nil, nil) if id does
	// not exist.
	Customer(ctx context.Context, id int32) (*CustomerEntry, error)
	// Customers looks up customers by ID, in one round trip. A nil or empty
	// ids is an empty, non-nil result with no query made; an id that does
	// not exist is simply absent from the result, not an error; a duplicate
	// id in ids is tolerated and never duplicates the customer in the
	// result. The result is ordered by ascending id, not by ids' own order,
	// so two callers asking for the same set always see it the same way.
	Customers(ctx context.Context, ids []int32) ([]CustomerEntry, error)
	// CustomersByLegalID finds the customers holding each organisation
	// number in ids, in one round trip, keyed by the id as given (energy
	// import design D4: an import resolves every number in its file once,
	// before the first row). The comparison is the duplicate-identity
	// guard's own — country case-insensitive, the id with its whitespace
	// removed — so "923 609 016" finds the customer stored as "923609016",
	// under the key "923 609 016". An id nobody holds is absent from the
	// map, not an error; a nil or empty ids is an empty, non-nil map with no
	// query made. More than one holder is ambiguous, and as with
	// ContactsByEmail the caller decides what that means. Archived and
	// merged-away customers are returned, with Archived and MergedInto set,
	// so the caller decides what a holder it cannot write to means. Each
	// id's customers are ordered by ascending id.
	CustomersByLegalID(ctx context.Context, country string, ids []string) (map[string][]CustomerEntry, error)
	// Contact looks up a contact by ID. It returns (nil, nil) if id does not
	// exist.
	Contact(ctx context.Context, id int32) (*ContactEntry, error)
	// ContactsByEmail finds every contact with the given email, each
	// together with the customers it is linked to. It returns an empty
	// slice, not an error, when no contact matches.
	ContactsByEmail(ctx context.Context, email string) ([]ContactMatch, error)
	// ContactsForCustomer lists the contacts associated with the customer
	// that have an address, ordered by last name, then first name: the
	// people a caller may invite on the customer's behalf (calendar
	// invitations design D2). Email is the contact's own address, never nil
	// or blank here — the one Contact answers, so what a caller picks is what
	// it later resolves. An unknown customer, or one with no such contact, is
	// an empty, non-nil result, not an error.
	ContactsForCustomer(ctx context.Context, customerID int32) ([]ContactEntry, error)
	// BillingProfile is what an invoice needs to know about a customer, and
	// the default bill rate Time's rate chain prices hours with, already
	// resolved (see the rules above). It returns (nil, nil) if id does not
	// exist. opts says whether DeliveryAddresses and Contacts are filled.
	// The profile, its invoice address, its delivery addresses and its
	// contacts are read in separate statements, not one snapshot, so a
	// concurrent edit can show in one part and not yet in another.
	BillingProfile(ctx context.Context, id int32, opts BillingProfileOptions) (*CustomerBillingProfile, error)
}
