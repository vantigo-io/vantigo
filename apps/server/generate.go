// Command go generate refreshes everything derived from the contract in
// ../../openapi. Run from apps/server:
//
//	go generate ./...
//
// Generated files are committed; CI regenerates them and fails on a diff.
package server

// The go:embed builtin cannot reach above the module root, so internal/openapi
// embeds a copy of the contract files. openapi/ stays the single source of
// truth; the drift test in internal/openapi fails if the copy is stale.
//go:generate sh -c "rm -f internal/openapi/specs/*.yaml && cp ../../openapi/*.yaml internal/openapi/specs/"
