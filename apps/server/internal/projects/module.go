// Package projects is the projects module: projects, the roles users hold on
// them, their billing lines, their timeline and the statistics over them,
// serving openapi/projects.yaml under /api/v1/projects/. A project is what
// later modules — time tracking first — attach work to; they read it through
// the contracts.ProjectDirectory this module publishes (directory.go).
package projects

import (
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// permissions is the module's permission catalog (design §5, §6). Every
// operation requires projects:access — it is what puts the app in the
// switcher, and without it a project member would never see the tile (D8) —
// and the other five sit above the per-project roles. The three that widen
// what a caller sees beyond their own projects' money and management are
// sensitive; all six are delegable, as every other module's are.
//
// projects:view-costs is the odd one out: it widens nothing on its own. It
// only ever adds the cost and margin block to a project whose money the
// caller can already see (design §2 E7), and it is in no role by default —
// the three built-in roles are seeded with no permission keys at all
// (migration 00002), so "no default role" is what every new permission
// starts as and nothing here has to opt out of one.
var permissions = []contracts.Permission{
	{Key: "projects:access", Display: "Use Projects", Description: "Use the Projects app and see the projects you hold a role in.", Category: "Projects", Sensitive: false, Delegable: true},
	{Key: "projects:create", Display: "Create projects", Description: "Create projects.", Category: "Projects", Sensitive: false, Delegable: true},
	{Key: "projects:view-all", Display: "View all projects", Description: "See every project, not only the ones you hold a role in.", Category: "Projects", Sensitive: false, Delegable: true},
	{Key: "projects:manage-all", Display: "Manage all projects", Description: "Manage every project, which also means seeing it and its financial fields.", Category: "Projects", Sensitive: true, Delegable: true},
	{Key: "projects:view-financials", Display: "View project financials", Description: "See fixed prices, budget amounts and line pricing on every project you can see.", Category: "Projects", Sensitive: true, Delegable: true},
	{Key: "projects:view-costs", Display: "View project costs", Description: "See what the work costs the company and the margin, on projects whose financials you can see. On a small project this can reveal a person's cost rate.", Category: "Projects", Sensitive: true, Delegable: true},
}

// limits maps each rate-limited operationId to its policy. It is empty and
// stays empty: no Projects endpoint is rate limited, only identity's
// authentication ceremonies are.
var limits = map[string]ratelimit.Policy{}

// Module is projects as a platform module: its contract mounted under
// /api/v1/projects/, its six permissions in the composed catalog, and the
// contracts.ProjectDirectory it publishes to the modules built on top of it
// — Time tracking first — and the contracts.CustomerReferenceHolder a
// customer merge re-points its projects through.
func Module() module.Module {
	return module.Module{
		Name:               "projects",
		Permissions:        permissions,
		Mount:              mount,
		Projects:           newDirectory,
		CustomerReferences: newCustomerReferenceHolder,
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
