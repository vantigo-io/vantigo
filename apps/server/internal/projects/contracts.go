package projects

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the whole of this module's reach into its neighbours. Every
// call into contracts.ProductCatalog (deps.Products), contracts.UserDirectory
// (deps.Users), contracts.CustomerDirectory (deps.Directory) and
// contracts.ProjectActuals (deps.Actuals) is made through one of the thin
// accessors below and through nowhere else, which is what makes "what does
// Projects ask of another module, and when" a question with one place to read
// the answer — and one place to check it from.
//
// What is checked is docs/projects.md's "Locking" rule: a cross-module call is
// always made *before* the project's row lock is taken, never from inside the
// transaction holding it (withProjectLock). These are in-process calls into
// another module that read through the same connection pool, so a transaction
// that holds row locks and then waits for one of them stalls every other
// writer of that project, and under enough load starves the pool outright —
// every connection held by a transaction waiting for one more.
//
// The rule used to be pinned for billing lines alone, by one probe test
// (lines_concurrency_test.go). noteContractCall pins it for the whole module,
// on every path, at a production cost of one nil comparison per call: nothing
// here changes what any of these calls do or when they are made.

// contractCallHook is handed the context and the name of every cross-module
// call this module makes. It is nil in production and installed once, before
// any test runs, by this package's own tests (SetContractCallHook in
// export_test.go); the harness's hook fails the test when the context it is
// given is one withProjectLock marked.
var contractCallHook func(ctx context.Context, method string)

// noteContractCall reports one call into another module's contract.
func noteContractCall(ctx context.Context, method string) {
	if contractCallHook != nil {
		contractCallHook(ctx, method)
	}
}

// productsVariant, productsVariants and productsListPrice are the product
// catalog. deps.Products is the one optional contract here (D10): every caller
// has already answered 409 for an installation without it, so none of these is
// reached with a nil catalog.
func (s *server) productsVariant(ctx context.Context, id int32) (*contracts.VariantEntry, error) {
	noteContractCall(ctx, "Products.Variant")
	return s.deps.Products.Variant(ctx, id)
}

func (s *server) productsVariants(ctx context.Context, ids []int32) ([]contracts.VariantEntry, error) {
	noteContractCall(ctx, "Products.Variants")
	return s.deps.Products.Variants(ctx, ids)
}

func (s *server) productsListPrice(ctx context.Context, variantID int32, currency string, at time.Time) (*contracts.Money, error) {
	noteContractCall(ctx, "Products.ListPrice")
	return s.deps.Products.ListPrice(ctx, variantID, currency, at)
}

// usersUser, usersUsers and usersSearchUsers are identity's user directory,
// the only way this module may read a user. It is always composed.
func (s *server) usersUser(ctx context.Context, id uuid.UUID) (*contracts.UserEntry, error) {
	noteContractCall(ctx, "Users.User")
	return s.deps.Users.User(ctx, id)
}

func (s *server) usersUsers(ctx context.Context, ids []uuid.UUID) ([]contracts.UserEntry, error) {
	noteContractCall(ctx, "Users.Users")
	return s.deps.Users.Users(ctx, ids)
}

func (s *server) usersSearchUsers(ctx context.Context, query string, limit int) ([]contracts.UserEntry, error) {
	noteContractCall(ctx, "Users.SearchUsers")
	return s.deps.Users.SearchUsers(ctx, query, limit)
}

// directoryCustomer is the customer directory. Customers is a required
// dependency of projects (config refuses to start without it), so this is
// never nil either.
func (s *server) directoryCustomer(ctx context.Context, id int32) (*contracts.CustomerEntry, error) {
	noteContractCall(ctx, "Directory.Customer")
	return s.deps.Directory.Customer(ctx, id)
}

// actualsFor and actualsForProjects are the hours another module has logged
// against projects. deps.Actuals is optional — an installation without time
// tracking has none — and every caller checks for nil before reaching these.
func (s *server) actualsFor(ctx context.Context, req contracts.ActualsRequest) (contracts.ProjectActualsEntry, error) {
	noteContractCall(ctx, "Actuals.Actuals")
	return s.deps.Actuals.Actuals(ctx, req)
}

func (s *server) actualsForProjects(ctx context.Context, reqs []contracts.ActualsRequest) (map[int32]contracts.ActualsTotals, error) {
	noteContractCall(ctx, "Actuals.ActualsForProjects")
	return s.deps.Actuals.ActualsForProjects(ctx, reqs)
}
