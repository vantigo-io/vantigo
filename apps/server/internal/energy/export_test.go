package energy

import "context"

// SetImportRunHook makes f run inside every import run the worker starts,
// before its first row (import_worker.go's importRunHookForTest); it answers
// the function that takes the hook away again. The hook is the package's, so
// the test that sets it does not run in parallel.
func SetImportRunHook(f func(ctx context.Context, importID int32) error) func() {
	previous := importRunHookForTest
	importRunHookForTest = f
	return func() { importRunHookForTest = previous }
}

// ImportLeaseKeyForTest is the import worker's advisory-lease key, exported so
// a test can take the same lock from a second connection and prove a cycle
// skips (energy import design D6).
const ImportLeaseKeyForTest = importLeaseKey

// SupplyPeriodLockClassForTest is the supply periods' advisory-lock class,
// exported so a test can hold a point's supply-period lock as an endpoint
// does and prove what an import row's own locks leave free.
const SupplyPeriodLockClassForTest = supplyPeriodLockClass
