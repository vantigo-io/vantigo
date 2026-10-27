package calendar

import "context"

// InOpenTx exposes inOpenTx to the external tests, whose contract-call hook
// uses it to tell a call made from inside one of this module's transactions
// from one made outside any.
var InOpenTx = inOpenTx

// SetContractCallHook installs the hook every call out of this module is
// reported to (contractscalls.go). The harness installs it once, from
// TestMain, before any test runs: a package-level hook written while other
// tests are making requests would be a data race of the tests' own making.
func SetContractCallHook(hook func(ctx context.Context, method string)) {
	contractCallHook = hook
}

// SetMailboxBusyCap lowers the busy read's cap of rows a person holds for one
// test, and answers the function that puts it back. Only a test that does not
// run in parallel may call it: the cap is a package variable the worker reads.
func SetMailboxBusyCap(n int) (restore func()) {
	was := mailboxBusyCap
	mailboxBusyCap = n
	return func() { mailboxBusyCap = was }
}
