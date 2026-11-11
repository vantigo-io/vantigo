package manufacturing

import "context"

// InOpenTx exposes inOpenTx to the external tests, whose contract-call hook
// uses it to tell a call made from inside one of this module's transactions
// from one made outside any.
var InOpenTx = inOpenTx

// LockVariantsCall is the name the hook hears the one in-transaction call by.
const LockVariantsCall = lockVariantsCall

// SetContractCallHook installs the hook every call out of this module is
// reported to (contractscalls.go), once, from TestMain.
func SetContractCallHook(hook func(ctx context.Context, method string)) {
	contractCallHook = hook
}
