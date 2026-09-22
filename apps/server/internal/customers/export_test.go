package customers

import "time"

// SetRegistryHookTimeout moves the create/legal-identity-PUT registry hook's
// own deadline (registry.go's registryHookTimeout) for the length of one test
// and answers the function that puts the real one back. Proving that the hook
// is bounded at all otherwise means waiting out a real four-second attempt;
// the test that uses it does not run in parallel, because the deadline is the
// package's — expenses' SetExportMaxRows is the same seam for the same reason.
func SetRegistryHookTimeout(d time.Duration) func() {
	previous := registryHookTimeout
	registryHookTimeout = d
	return func() { registryHookTimeout = previous }
}
