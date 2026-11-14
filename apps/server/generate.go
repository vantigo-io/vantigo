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

// The Assistant answers "how do I…" from the documentation, which the binary
// carries: the user guide and the administration guide, in both languages, are
// copied into the module and embedded. docs/src/content/docs stays the single
// source of truth; internal/assistant/docs_fresh_test.go fails if the copy is
// stale.
//go:generate sh -c "rm -rf internal/assistant/docs && mkdir -p internal/assistant/docs/en/user internal/assistant/docs/en/admin internal/assistant/docs/nb/user internal/assistant/docs/nb/admin && cp ../../docs/src/content/docs/en/user/*.md internal/assistant/docs/en/user/ && cp ../../docs/src/content/docs/en/admin/*.md internal/assistant/docs/en/admin/ && cp ../../docs/src/content/docs/nb/user/*.md internal/assistant/docs/nb/user/ && cp ../../docs/src/content/docs/nb/admin/*.md internal/assistant/docs/nb/admin/"

// The Assistant's tour targets: every module frontend registers the controls the
// Assistant can point at (apps/*/frontend/src/assist-targets.json), and
// tools/assist/gen-targets.ts merges them into the small registry the module
// embeds. internal/assistant/assist_test.go fails if the copy is stale.
//go:generate bun run ../../tools/assist/gen-targets.ts

// The contract's generated Go code. oapi-codegen is pinned here, in one place,
// and runs the same way locally and in CI's drift check.
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-common.yaml ../../openapi/common.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-energy.yaml ../../openapi/energy.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-identity.yaml ../../openapi/identity.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-customers.yaml ../../openapi/customers.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-products.yaml ../../openapi/products.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-projects.yaml ../../openapi/projects.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-communications.yaml ../../openapi/communications.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-time.yaml ../../openapi/time.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-expenses.yaml ../../openapi/expenses.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-invoices.yaml ../../openapi/invoices.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-staff.yaml ../../openapi/staff.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-planning.yaml ../../openapi/planning.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-inventory.yaml ../../openapi/inventory.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-orders.yaml ../../openapi/orders.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-manufacturing.yaml ../../openapi/manufacturing.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-accounting.yaml ../../openapi/accounting.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-knowledge.yaml ../../openapi/knowledge.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-calendar.yaml ../../openapi/calendar.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-entra.yaml ../../openapi/entra.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-google.yaml ../../openapi/google.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-meta.yaml ../../openapi/meta.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-assistant.yaml ../../openapi/assistant.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-agents.yaml ../../openapi/agents.yaml

// sqlc's typed query layer. Pinned in mise.toml ("aqua:sqlc-dev/sqlc"), so it
// is already on PATH by the time go generate runs.
//go:generate sqlc generate -f internal/identity/sqlc.yaml
//go:generate sqlc generate -f internal/customers/sqlc.yaml
//go:generate sqlc generate -f internal/products/sqlc.yaml
//go:generate sqlc generate -f internal/projects/sqlc.yaml
//go:generate sqlc generate -f internal/energy/sqlc.yaml
//go:generate sqlc generate -f internal/communications/sqlc.yaml
//go:generate sqlc generate -f internal/time/sqlc.yaml
//go:generate sqlc generate -f internal/expenses/sqlc.yaml
//go:generate sqlc generate -f internal/invoices/sqlc.yaml
//go:generate sqlc generate -f internal/staff/sqlc.yaml
//go:generate sqlc generate -f internal/planning/sqlc.yaml
//go:generate sqlc generate -f internal/inventory/sqlc.yaml
//go:generate sqlc generate -f internal/orders/sqlc.yaml
//go:generate sqlc generate -f internal/manufacturing/sqlc.yaml
//go:generate sqlc generate -f internal/accounting/sqlc.yaml
//go:generate sqlc generate -f internal/knowledge/sqlc.yaml
//go:generate sqlc generate -f internal/calendar/sqlc.yaml
//go:generate sqlc generate -f internal/entra/sqlc.yaml
//go:generate sqlc generate -f internal/google/sqlc.yaml
//go:generate sqlc generate -f internal/meta/sqlc.yaml
//go:generate sqlc generate -f internal/assistant/sqlc.yaml
//go:generate sqlc generate -f internal/agents/sqlc.yaml
