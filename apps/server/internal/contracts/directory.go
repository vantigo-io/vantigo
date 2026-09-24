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
// CustomerBillingProfile.InvoiceAddress: enough to print on a document,
// never the address's own id or type — the profile has already picked which
// one applies, and a consumer never needs to ask the directory for another.
type CustomerAddressEntry struct {
	Label, Line1, Line2, PostalCode, City, Region, Country string
}

// CustomerBillingProfile is what an invoice needs to know about a customer,
// already resolved: every rule that would otherwise have to be re-derived by
// whoever sends the invoice is applied once, here, by the module that owns
// the data (invoice-ready customer design D5).
type CustomerBillingProfile struct {
	ID             int32
	CustomerNumber int64
	Name           string
	Type           string // "business" | "person"
	Archived       bool
	LegalCountry   string // "" when the customer has no legal identity
	LegalID        string
	LegalName      string
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
//     pointer.
//   - Archived customers still resolve, from every one of Customer,
//     Customers and BillingProfile. A consumer (a supply period, a past
//     invoice) can hold a long-lived reference to a customer that has since
//     been archived, and must still be able to show its name or invoice it
//     again; Archived tells the caller to decorate that reference, not that
//     the lookup failed.
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
//     billing profile's own GET endpoint would not presume to pick.
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
	// Contact looks up a contact by ID. It returns (nil, nil) if id does not
	// exist.
	Contact(ctx context.Context, id int32) (*ContactEntry, error)
	// ContactsByEmail finds every contact with the given email, each
	// together with the customers it is linked to. It returns an empty
	// slice, not an error, when no contact matches.
	ContactsByEmail(ctx context.Context, email string) ([]ContactMatch, error)
	// BillingProfile is what an invoice needs to know about a customer,
	// already resolved (see the rules above). It returns (nil, nil) if id
	// does not exist.
	BillingProfile(ctx context.Context, id int32) (*CustomerBillingProfile, error)
}
