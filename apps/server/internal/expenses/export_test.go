package expenses

import (
	"context"
	"math/big"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/expenses/store"
)

// OwesEmployee is owesEmployee asked about a row of this kind and payer with a
// gross above zero, so the test holding it against expenses.owes_employee
// compares the rule and not the arithmetic.
func OwesEmployee(kind string, paidBy *string) bool {
	return owesEmployee(store.ExpensesEntry{
		Kind: kind, PaidBy: paidBy,
		GrossAmount: pgtype.Numeric{Int: big.NewInt(100), Valid: true},
	})
}

// InLockedTx exposes inLockedTx to the external tests, whose contract-call
// hook uses it to tell a call made from inside one of this module's locked
// transactions from one made outside any.
var InLockedTx = inLockedTx

// SetContractCallHook installs the hook every cross-module call this module
// makes is reported to (contractscalls.go). It is only reachable from this
// package's own tests, and the harness installs it once, from TestMain, before
// any test runs: a package-level hook written while other tests are making
// requests would be a data race of the tests' own making.
func SetContractCallHook(hook func(ctx context.Context, method string)) {
	contractCallHook = hook
}

// ReceiptUploadsPerHour exposes the upload rate limit's own number to the
// external tests, so the test that proves the limit bites cannot drift from
// the policy the module registers.
const ReceiptUploadsPerHour = receiptUploadsPerHour

// SetExportMaxRows moves the payroll export's row cap for the length of one
// test and answers the function that puts the real one back. Proving the cap
// bites otherwise means recording five thousand expenses; the test that uses
// it does not run in parallel, because the cap is the package's.
func SetExportMaxRows(n int) func() {
	previous := exportMaxRows
	exportMaxRows = n
	return func() { exportMaxRows = previous }
}

// BusinessDay exposes businessDay to the external tests: the property that Go
// and Postgres name the same day from one instant is asserted against the very
// function the handlers derive every claim-shaped date with.
var BusinessDay = businessDay
