package invoices

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the whole of this module's reach outside its own schema: the
// customer directory (deps.Directory) and the object store (server.objects).
// Every call is made through one of the thin accessors below and through
// nowhere else, so "what does Invoices ask of its neighbours, and when" has one
// place to read the answer and one place to check it from.
//
// What is checked is the rule of D6 and D7: no directory call and no
// object-store call is ever made inside a transaction holding locks
// (withLockedTx). The directory reads through the same connection pool, and a
// slow store under a row lock is the same hazard by another route.
// noteContractCall pins the rule on every path, at a production cost of one nil
// comparison per call.

// contractCallHook is handed the context and the name of every call out of the
// module. It is nil in production and installed once, before any test runs, by
// this package's own tests (SetContractCallHook in export_test.go).
var contractCallHook func(ctx context.Context, method string)

// noteContractCall reports one call out of the module.
func noteContractCall(ctx context.Context, method string) {
	if contractCallHook != nil {
		contractCallHook(ctx, method)
	}
}

// customerProfile is the billing profile, the one read an invoice's gates and
// its buyer snapshot are made from (D4, D10).
func (s *server) customerProfile(ctx context.Context, id int32) (*contracts.CustomerBillingProfile, error) {
	noteContractCall(ctx, "Directory.BillingProfile")
	return s.deps.Directory.BillingProfile(ctx, id)
}

// customerEntries names a page of drafts' customers in one round trip. An
// empty batch asks nobody.
func (s *server) customerEntries(ctx context.Context, ids []int32) ([]contracts.CustomerEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	noteContractCall(ctx, "Directory.Customers")
	return s.deps.Directory.Customers(ctx, ids)
}
