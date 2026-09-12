package contracts

import "context"

// CustomerEntry is a customer as another module may reference it: enough to
// name it in a UI or a document, never enough to manage it — that stays
// behind customers' own contract and permissions.
type CustomerEntry struct {
	ID       int32
	Name     string
	Archived bool
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

// CustomerDirectory is the one sanctioned way a module reads another
// module's data: a read-only, in-process port over customer data, so energy
// can name a customer on a supply period and communications can resolve a
// contact by email, without either importing the customers package (barred
// by depguard) or reading its PostgreSQL schema (barred by
// internal/db/schema_test.go). Whichever enabled module owns customer data
// implements it; Compose wires that implementation into every module's Deps
// before any Mount runs (see Module.Directory).
//
// Three rules hold for every method here, because none of them are obvious
// from the signatures alone:
//
//   - A missing row is (nil, nil), not an error. A caller tells "does not
//     exist" from "the lookup failed" by checking err, never by treating a
//     nil result as failure.
//   - Archived customers still resolve. A consumer (a supply period, a past
//     invoice) can hold a long-lived reference to a customer that has since
//     been archived, and must still be able to show its name; Archived
//     tells the caller to decorate that reference, not that the lookup
//     failed.
//   - More than one candidate from ContactsByEmail is ambiguous. A contact's
//     email is not unique across customers, and the directory does not
//     guess which customer the caller means: it returns every candidate,
//     and the caller — never the directory — decides how to resolve the
//     ambiguity, typically by asking a person.
type CustomerDirectory interface {
	// Customer looks up a customer by ID. It returns (nil, nil) if id does
	// not exist.
	Customer(ctx context.Context, id int32) (*CustomerEntry, error)
	// Contact looks up a contact by ID. It returns (nil, nil) if id does not
	// exist.
	Contact(ctx context.Context, id int32) (*ContactEntry, error)
	// ContactsByEmail finds every contact with the given email, each
	// together with the customers it is linked to. It returns an empty
	// slice, not an error, when no contact matches.
	ContactsByEmail(ctx context.Context, email string) ([]ContactMatch, error)
}
