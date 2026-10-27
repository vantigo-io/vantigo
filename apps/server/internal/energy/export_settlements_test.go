package energy

import "context"

// SetInvoicedWorkAfterLock installs a hook both directions of the
// invoiced-work holder call with the settlements locked, and answers the
// function that takes it away. The hook is the package's, so the test that
// sets it does not run in parallel.
func SetInvoicedWorkAfterLock(hook func(ctx context.Context)) func() {
	invoicedWorkAfterLock = hook
	return func() { invoicedWorkAfterLock = nil }
}
