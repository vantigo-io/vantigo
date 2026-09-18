// Package timetracking is the time module: time entries with snapshotted
// rates, person rate cards, weekly submission, per-entry approval and a
// period lock, serving openapi/time.yaml under /api/v1/time/.
//
// The package lives in internal/time — the directory is the module's name,
// which is also its URL prefix, its PostgreSQL schema and its MODULES entry,
// and internal/db's schema test maps a directory to its schema by name — but
// its package clause is timetracking, so that nothing in it shadows the
// standard library's time package. Importers write
//
//	timetracking "github.com/vantigo-io/vantigo/server/internal/time"
//
// and call timetracking.Module().
//
// Time reads projects only through contracts.ProjectDirectory (required:
// config refuses "time" without "projects"), names people through
// contracts.UserDirectory, and prices billing lines through
// contracts.ProductCatalog when products is enabled (D4). It provides no
// contract of its own yet.
package timetracking

import (
	"errors"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
	"github.com/vantigo-io/vantigo/server/internal/time/gen"
)

// permissions is the module's permission catalog (design §6). Every
// operation requires time:access — it is what puts the app in the switcher
// and what lets a project member log time at all — and the other three sit
// above what a project role grants. The two that reach beyond the caller's
// own projects' people — everyone's entries and cost rates, and the rate
// cards and lock — are sensitive; all four are delegable, as every other
// module's are.
var permissions = []contracts.Permission{
	{Key: "time:access", Display: "Use Time", Description: "Use the Time app, log time on the projects you work on and see your own entries.", Category: "Time", Sensitive: false, Delegable: true},
	{Key: "time:approve", Display: "Approve time", Description: "Approve and reject anyone's submitted time entries, on every project.", Category: "Time", Sensitive: false, Delegable: true},
	{Key: "time:view-all", Display: "View all time", Description: "See everyone's time entries, the people overview and cost rates.", Category: "Time", Sensitive: true, Delegable: true},
	{Key: "time:manage", Display: "Manage time", Description: "Manage person rate cards and the period lock, unapprove entries and edit entries past the lock.", Category: "Time", Sensitive: true, Delegable: true},
}

// limits maps each rate-limited operationId to its policy. It is empty and
// stays empty: no Time endpoint is rate limited, only identity's
// authentication ceremonies are.
var limits = map[string]ratelimit.Policy{}

// Module is time as a platform module: its contract mounted under
// /api/v1/time/ and its four permissions in the composed catalog.
func Module() module.Module {
	return module.Module{
		Name:        "time",
		Permissions: permissions,
		Mount:       mount,
	}
}

// mount registers every contract operation on the platform router, which wraps
// each in its access rule and request-body cap before the generated wrapper
// decodes it. It fails when the router reports a problem: an operation never
// registered, a rule that does not parse, or a permission missing from the
// catalog — and, before any of that, when there is no project directory to
// read: config already refuses "time" without "projects", so reaching here
// without one is a composition bug, and failing the mount says so where a
// nil directory would only panic on the first request.
func mount(d module.Deps) (http.Handler, error) {
	if d.Projects == nil {
		return nil, errors.New("time: no project directory; the time module requires the projects module")
	}
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
