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

// The contract's generated Go code. oapi-codegen is pinned here, in one place,
// and runs the same way locally and in CI's drift check.
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-common.yaml ../../openapi/common.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-energy.yaml ../../openapi/energy.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-identity.yaml ../../openapi/identity.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-customers.yaml ../../openapi/customers.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-products.yaml ../../openapi/products.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-communications.yaml ../../openapi/communications.yaml

// sqlc's typed query layer. Pinned in mise.toml ("aqua:sqlc-dev/sqlc"), so it
// is already on PATH by the time go generate runs.
//go:generate sqlc generate -f internal/identity/sqlc.yaml
