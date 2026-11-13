package manufacturing

import (
	"context"

	"github.com/vantigo-io/vantigo/server/internal/module"
)

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

// ErasureCycle runs one cycle of the erasure sweep: the notes it blanked.
func ErasureCycle(ctx context.Context, d module.Deps) (int64, error) {
	return (&erasureWorker{deps: d}).RunCycle(ctx)
}

// MtoCycle runs one cycle of the make-to-order worker: the requests it
// finished.
func MtoCycle(ctx context.Context, d module.Deps) (int, error) {
	return (&mtoWorker{s: newServer(d)}).RunCycle(ctx)
}

// ReplenishmentCycle runs one cycle of the replenishment worker: the runs it
// finished.
func ReplenishmentCycle(ctx context.Context, d module.Deps) (int, error) {
	return (&replenishmentWorker{s: newServer(d)}).RunCycle(ctx)
}
