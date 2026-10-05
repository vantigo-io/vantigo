package timetracking

import "context"

// InLockedTx exposes inLockedTx to the external tests, whose fake
// directories record any call made from inside a withLockedTx transaction.
var InLockedTx = inLockedTx

// SetInvoicedWorkAfterLock installs a hook both directions of the
// invoiced-work holder call right after they have locked the entries, with
// the holder's context, and answers the function that removes it. A test
// using it does not run in parallel: the hook is the package's.
func SetInvoicedWorkAfterLock(hook func(ctx context.Context)) func() {
	invoicedWorkAfterLock = hook
	return func() { invoicedWorkAfterLock = nil }
}
