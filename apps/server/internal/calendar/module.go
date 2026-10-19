// Package calendar is the calendar module: one view of every time-bound thing
// the other modules know about one person, beside the appointments they
// schedule themselves, serving openapi/calendar.yaml under /api/v1/calendar/
// (docs/superpowers/specs/2026-10-19-calendar-design.md).
//
// It depends on nobody but identity. What it shows from the other modules it
// never copies: each enabled module projects its own rows at read time through
// contracts.CalendarSource (module.Deps.CalendarSources), under its own
// permission gate. The one thing the module owns is the appointment. Nothing
// here imports another module — depguard enforces it, tests included — and no
// SQL of this module crosses a schema.
//
// This is the module's skeleton: the contract, the schema, the queries and
// the permissions, with every operation registered and answering 501 until the
// handlers land.
package calendar

import (
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// permissions is the module's permission catalog (calendar design D3). Every
// operation requires calendar:access; a colleague's calendar requires
// calendar:view-colleagues beside it — the one thing not everyone should have
// by default. Neither is sensitive, and both are delegable, as every other
// module's are. There is no manage key: the module has no settings.
var permissions = []contracts.Permission{
	{
		Key: "calendar:access", Display: "Use Calendar",
		Description: "Use the Calendar app: your own calendar, your appointments and your answers to them, and the dashboard's agenda.",
		Category:    "Calendar", Sensitive: false, Delegable: true,
	},
	{
		Key: "calendar:view-colleagues", Display: "See colleagues' calendars",
		Description: "See a colleague's appointments as busy or free, with the title when it is shared.",
		Category:    "Calendar", Sensitive: false, Delegable: true,
	},
}

// Module is calendar as a platform module: its contract mounted under
// /api/v1/calendar/ and its two permissions in the composed catalog. It
// provides no contract to anyone; it reads Deps.CalendarSources, Deps.Users,
// Deps.Calendar and, when wired, Deps.Directory and Deps.Projects.
func Module() module.Module {
	return module.Module{
		Name:        "calendar",
		Permissions: permissions,
		Mount:       mount,
	}
}

// mount registers every contract operation on the platform router, which wraps
// each in its access rule and request-body cap before the generated wrapper
// decodes it. It fails when the router reports a problem — an operation never
// registered, a rule that does not parse, or a permission missing from the
// catalog.
func mount(d module.Deps) (http.Handler, error) {
	router := module.NewRouter(module.RouterOptions{
		Doc:     d.Doc,
		Access:  d.Access,
		Limiter: d.Limiter,
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
