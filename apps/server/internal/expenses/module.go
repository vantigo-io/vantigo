// Package expenses is the expenses module: outlays and mileage with receipts,
// an approval flow, reimbursement and invoicing, admin-managed dated rates and
// categories, serving openapi/expenses.yaml under /api/v1/expenses/.
//
// It depends on nobody but identity (design decision X1). Projects is an
// *optional* read through contracts.ProjectDirectory: an installation may run
// MODULES=customers,expenses, or expenses alone, and then GET /meta answers
// projectsAvailable false and every project-shaped request field is refused on
// that field. Nothing here imports another module — depguard enforces it, tests
// included — and no SQL of this module crosses a schema.
//
// This delivery is the module's foundation: the whole schema (entries and
// attachments included, so no later delivery adds a migration), the settings,
// the dated rate table and the categories. Entries, receipts, the approval flow
// and the frontend come next, on the names established here.
package expenses

import (
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/expenses/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// permissions is the module's permission catalog (design §5). Every operation
// requires expenses:access — it is what puts the app in the switcher and what
// lets anyone record an expense at all — and the other three sit above it.
//
// Two are sensitive. expenses:manage reaches the installation's own money
// settings, records for other people, marks reimbursements paid and works past
// the period lock. expenses:view-all opens every colleague's outlays — what
// they bought, from whom and for how much — which is personal in a way a time
// entry is not; the sibling time module marks its own view-all sensitive for
// the same reason. All four are delegable, as every other module's are.
var permissions = []contracts.Permission{
	{
		Key: "expenses:access", Display: "Use Expenses",
		Description: "Use the Expenses app and record and submit your own expenses.",
		Category:    "Expenses", Sensitive: false, Delegable: true,
	},
	{
		Key: "expenses:approve", Display: "Approve expenses",
		Description: "Approve or reject anyone's expenses, including those with no project.",
		Category:    "Expenses", Sensitive: false, Delegable: true,
	},
	{
		Key: "expenses:view-all", Display: "View all expenses",
		Description: "See everyone's expenses.",
		Category:    "Expenses", Sensitive: true, Delegable: true,
	},
	{
		Key: "expenses:manage", Display: "Manage expenses",
		Description: "Change expense settings, rates and categories, record expenses for a colleague, mark expenses reimbursed, and work past the period lock.",
		Category:    "Expenses", Sensitive: true, Delegable: true,
	},
}

// limits maps each rate-limited operationId to its policy. It is empty and
// stays empty: no Expenses endpoint is rate limited, only identity's
// authentication ceremonies are.
var limits = map[string]ratelimit.Policy{}

// Module is expenses as a platform module: its contract mounted under
// /api/v1/expenses/ and its four permissions in the composed catalog. It
// provides no contract of its own and requires none — a nil Deps.Projects is a
// real installation, not a composition bug, which is why mount does not refuse
// one the way time's does.
func Module() module.Module {
	return module.Module{
		Name:        "expenses",
		Permissions: permissions,
		Mount:       mount,
	}
}

// mount registers every contract operation on the platform router, which wraps
// each in its access rule and request-body cap before the generated wrapper
// decodes it. It fails when the router reports a problem: an operation never
// registered, a rule that does not parse, or a permission missing from the
// catalog.
func mount(d module.Deps) (http.Handler, error) {
	router := module.NewRouter(module.RouterOptions{
		Doc:     d.Doc,
		Access:  d.Access,
		Limiter: d.Limiter,
		Limits:  limits,
		Catalog: d.Catalog,
	})
	strict := gen.NewStrictHandlerWithOptions(newServer(d), nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  module.DecodeError(apicommon.WriteDecodeError),
		ResponseErrorHandlerFunc: module.ResponseError(),
	})
	handler := gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseRouter:       router,
		ErrorHandlerFunc: module.DecodeError(apicommon.WriteDecodeError),
	})
	if err := router.Err(); err != nil {
		return nil, err
	}
	return handler, nil
}
