package projects

import "context"

// InLockedTx exposes inLockedTx to the external tests, whose contract-call
// hook uses it to tell a call made from inside a transaction holding the
// project's row lock from one made outside any.
var InLockedTx = inLockedTx

// SetContractCallHook installs the hook every cross-module call this module
// makes is reported to (contracts.go). It is only reachable from this
// package's own tests, and the harness installs it once, from TestMain, before
// any test runs: a package-level hook written while other tests are making
// requests would be a data race of the tests' own making.
func SetContractCallHook(hook func(ctx context.Context, method string)) {
	contractCallHook = hook
}

// SetInvoicedWorkAfterLock installs the seam the invoiced-work holder calls in
// both directions right after its locks (invoiced_work.go), with the context
// it runs under. A test that sets it does not run in parallel and puts nil
// back when it ends.
func SetInvoicedWorkAfterLock(hook func(ctx context.Context)) {
	invoicedWorkAfterLock = hook
}
