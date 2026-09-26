package invoices

import "context"

// InLockedTx exposes inLockedTx to the external tests, whose contract-call
// hook uses it to tell a call made from inside one of this module's locked
// transactions from one made outside any.
var InLockedTx = inLockedTx

// SetContractCallHook installs the hook every call out of this module is
// reported to (contractscalls.go). The harness installs it once, from
// TestMain, before any test runs: a package-level hook written while other
// tests are making requests would be a data race of the tests' own making.
func SetContractCallHook(hook func(ctx context.Context, method string)) {
	contractCallHook = hook
}

// SetIssueAfterAllocation installs a hook the issue calls inside its
// transaction right after the number is allocated, and answers the function
// that removes it. A test using it does not run in parallel: the hook is the
// package's.
func SetIssueAfterAllocation(hook func(ctx context.Context, invoiceID int64) error) func() {
	issueAfterAllocation = hook
	return func() { issueAfterAllocation = nil }
}

// SetAfterPDFRender installs a hook the store-once path calls with each
// rendered PDF, answering the bytes it stores, and answers the function that
// removes it. A test using it does not run in parallel: the hook is the
// package's.
func SetAfterPDFRender(hook func(ctx context.Context, body []byte) []byte) func() {
	afterPDFRender = hook
	return func() { afterPDFRender = nil }
}
