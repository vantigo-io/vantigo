package expenses

import "context"

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
