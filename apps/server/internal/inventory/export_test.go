package inventory

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

// ErrDependencyUnavailable is what an accessor answers when the catalog is not
// there; a fake catalog answers it to reach the 503s.
var ErrDependencyUnavailable = errDependencyUnavailable

// SetTransferNoteHook installs the hook a transfer note's lines are reported
// to as they are printed (transfer_note.go); nil removes it.
func SetTransferNoteHook(hook func(rows [][]string)) {
	if hook == nil {
		noteRowsBuilt.Store(nil)
		return
	}
	noteRowsBuilt.Store(&hook)
}
