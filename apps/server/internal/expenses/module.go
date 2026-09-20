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
	"time"

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

// receiptUploadsPerHour is how many receipt uploads one client address may
// make. Ten receipts per expense bounds an expense, and nothing bounds how
// many expenses a person may record — so without this, any holder of
// expenses:access is one loop away from filling the installation's disk with
// ten-megabyte files.
//
// It is keyed by client address, because that is what the platform's limiter
// keys on (module.Router passes httpx.ClientIP) and what identity's own
// policies are keyed on — which is why the number is six hundred rather than
// the hundred and twenty one person would ever need. An office behind one NAT
// is one client address: everybody in it shares the allowance, and a team
// catching up on a month of receipts after a trip must not run one another out
// of it. Six hundred ten-megabyte files an hour is still far below what filling
// a volume takes.
const receiptUploadsPerHour = 600

// policyReceiptUpload is that bound as the platform states one.
var policyReceiptUpload = ratelimit.Policy{
	Name:    "ExpensesReceiptUpload",
	Limit:   receiptUploadsPerHour,
	Window:  time.Hour,
	Message: "Too many receipt uploads. Please wait a little before adding more.",
}

// limits maps each rate-limited operationId to its policy. The receipt upload
// is the module's one rate-limited operation: it is the only one that writes
// bytes outside the database, and the only one a caller can use to consume an
// unbounded amount of anything.
var limits = map[string]ratelimit.Policy{
	"postExpensesEntriesByIdAttachments": policyReceiptUpload,
}

// Module is expenses as a platform module: its contract mounted under
// /api/v1/expenses/, its four permissions in the composed catalog, and the one
// contract it provides. It requires none — a nil Deps.Projects is a real
// installation, not a composition bug, which is why mount does not refuse one
// the way time's does.
//
// Expenses is contracts.ProjectExpenses (projectexpenses.go): what a project's
// expenses cost and bill, for a module that owns budgets to read. Providing it
// is not a dependency in either direction — an installation may run this module
// with no projects at all, and one running projects without this module simply
// finds Deps.Expenses nil.
func Module() module.Module {
	return module.Module{
		Name:        "expenses",
		Permissions: permissions,
		Mount:       mount,
		Expenses:    newProjectExpenses,
	}
}

// mount registers every contract operation on the platform router, which wraps
// each in its access rule and request-body cap before the generated wrapper
// decodes it. Every body is capped at the router's default
// (module.DefaultMaxBodyBytes) except the receipt upload, which gets
// maxReceiptRequestBytes (receiptBodyLimits). It fails when the router reports
// a problem — an operation never registered, a rule that does not parse, a
// permission missing from the catalog, or a BodyLimits entry naming no
// operation — or when the object store receipts go through cannot be built.
func mount(d module.Deps) (http.Handler, error) {
	router := module.NewRouter(module.RouterOptions{
		Doc:        d.Doc,
		Access:     d.Access,
		Limiter:    d.Limiter,
		Limits:     limits,
		Catalog:    d.Catalog,
		BodyLimits: receiptBodyLimits,
	})
	srv, err := newServer(d)
	if err != nil {
		return nil, err
	}
	strict := gen.NewStrictHandlerWithOptions(srv, nil, gen.StrictHTTPServerOptions{
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
