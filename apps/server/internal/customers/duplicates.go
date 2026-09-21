package customers

import (
	"context"
	"errors"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
)

// This file is customers foundation design D6: a legal identity another
// customer already has is a conflict, unless the caller says otherwise
// (allowDuplicateIdentity). It is written wherever a legal identity is
// written — PostCustomers, PutCustomersById (customers.go) and
// PutCustomersByIdLegalIdentity (legal_identity.go) — never a fourth place,
// since customer_type.go's SetCustomerType only ever clears an identity, it
// never sets one.

// errDuplicateIdentity is the sentinel a write's transaction returns to
// abort itself once duplicateIdentityProblem finds a holder: db.WithTx
// rolls the transaction back on any error, so a refused create burns no
// customer number (NextCounterValue is never reached) and no timeline event
// is recorded either way. The caller (customers.go, legal_identity.go)
// checks errors.Is(err, errDuplicateIdentity) and answers 409 with the
// gen.CustomerConflictProblem it captured from duplicateIdentityProblem
// before returning this sentinel — the transaction is gone by the time the
// handler sees the error, so the body has to travel out some other way than
// the error itself.
var errDuplicateIdentity = errors.New("customers: legal identity already in use")

// duplicateIdentityTitle/Code/Detail are the fixed conflict body D6
// specifies verbatim; unlike customerRevisionConflict's detail (which names
// the two revisions in question), there is nothing request-specific to
// report beyond who already holds the identity.
const (
	duplicateIdentityTitle  = "Duplicate legal identity"
	duplicateIdentityCode   = "duplicate_legal_identity"
	duplicateIdentityDetail = "Another customer already has this legal identity."
)

// identityCountryAndIDEqual is the narrower comparison the duplicate
// check's "identity unchanged" exemption uses (customers foundation design
// D6, controller ruling) — a name, source or type change alone never
// triggers the check, only a change of country or id does. identityEqual
// (timeline_events.go), by contrast, compares all five fields: it decides
// whether a customer.updated event is worth recording, an unrelated
// question. Both nil counts as equal; exactly one nil never does.
func identityCountryAndIDEqual(a, b *legalIdentity) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Country == b.Country && a.ID == b.ID
}

// duplicateIdentityProblem reports whether another customer already holds
// identity, other than excludeID — 0 on create, where there is no existing
// customer to exclude, and the row being updated's own id on the two PUTs,
// so a customer is never reported as conflicting with itself. q is always
// the write's own transaction-scoped *store.Queries (db.WithTx's txq): the
// check must see, and answer inside, the same transaction the write commits
// or rolls back with, never a snapshot taken outside it. nil, nil means no
// conflict — an archived holder still conflicts, whatever the caller's own
// view of the row would be.
//
// nameHolders decides whether the conflict body carries duplicates at all:
// none of the three write paths implies customers:view (POST /customers needs
// create + legal-identity-manage; the legal-identity PUT needs
// legal-identity-manage + legal-identity-view), so a caller who cannot read a
// customer must not learn another customer's id, number, name and status from
// a refusal. Such a caller still gets the full title/code/detail/status — that
// the identity is taken is about their own request — just nobody's name with
// it; duplicates is optional in the contract for exactly this. The caller
// resolves nameHolders *before* opening the transaction, because it is an
// access check (a session lookup and a permission query), and this module
// makes no access or directory call while holding a database lock — the same
// discipline actorFor is held to (actor.go).
func (s *server) duplicateIdentityProblem(ctx context.Context, q *store.Queries, identity legalIdentity, excludeID int32, nameHolders bool) (*gen.CustomerConflictProblem, error) {
	rows, err := q.CustomersByLegalIdentity(ctx, store.CustomersByLegalIdentityParams{
		Country:   identity.Country,
		LegalID:   identity.ID,
		ExcludeID: excludeID,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	title, code, detail := duplicateIdentityTitle, duplicateIdentityCode, duplicateIdentityDetail
	status := int32(http.StatusConflict)
	problem := &gen.CustomerConflictProblem{Title: &title, Code: &code, Detail: &detail, Status: &status}
	if !nameHolders {
		return problem, nil
	}

	duplicates := make([]gen.CustomerConflictDuplicate, 0, len(rows))
	for _, r := range rows {
		duplicates = append(duplicates, gen.CustomerConflictDuplicate{
			Id: r.ID, CustomerNumber: r.CustomerNumber, Name: r.Name, Status: r.Status,
		})
	}
	problem.Duplicates = &duplicates
	return problem, nil
}
