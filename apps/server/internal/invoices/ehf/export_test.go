package ehf

// The package's internals the external tests reach.

// EASCodeCount is how many codes the vendored EAS list holds.
func EASCodeCount() int { return len(easSchemes) }

// KnownUnitCode is the unit-code invariant's table.
func KnownUnitCode(code string) bool { return knownUnitCode(code) }
