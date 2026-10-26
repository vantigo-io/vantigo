// Package planning is the planning module (Norwegian Bemanning): who works on
// what and when — a person allocated to a project over a period, tentative or
// confirmed, in hours a week or a share of their position — the capacity of
// each person computed once from Staff's facts, and the views that read it:
// the board, a project's plan, a person's timeline, the unbooked list and My
// planning. It serves openapi/planning.yaml under /api/v1/planning/.
//
// It is the first module to consume two business modules at once (planning
// design S25): it reads projects through contracts.ProjectDirectory and
// people through contracts.Workforce — config refuses "planning" without
// "projects" or without "staff", so neither is nil in a running installation,
// and mount fails rather than panic if one is — customers' names through
// contracts.CustomerDirectory, which Projects requires, and users through
// contracts.UserDirectory, which identity always provides. It stores nothing
// of a person or a project but their ids (S1): every name is read through a
// contract on every response, before any transaction opens
// (contractscalls.go). Nothing here imports another module — depguard
// enforces it, tests included — and no SQL of this module crosses a schema.
//
// Delivery A: the schema, the contract, the permissions, the settings, the
// capacity calculation (capacity.go) and the window reader (window.go), the
// allocations and their writes (allocations*.go, notifications.go) and the
// reads, one file per screen.
package planning

import (
	"errors"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// permissions is the module's permission catalog (design §5). Every operation
// requires planning:access — it is what puts the app in the switcher and what
// lets a person see their own confirmed bookings — and the other two sit
// above it.
//
// Both are sensitive. planning:view reveals the whole project portfolio, bids
// included, which Projects otherwise reserves for projects:view-all (S9,
// reading 3), with every person's plan; planning:manage writes the plan and
// reads the planners' notes, the terms of a customer contract and the
// person's wishes. A planner also needs staff:view, without which
// the Workforce reads answer nobody (reading 14). All three are delegable, as
// every other module's are.
var permissions = []contracts.Permission{
	{
		Key: "planning:access", Display: "Use Planning",
		Description: "Use the Planning app: My planning, your own confirmed bookings and wishes, and your bookings on the Calendar.",
		Category:    "Planning", Sensitive: false, Delegable: true,
	},
	{
		Key: "planning:view", Display: "See the plan",
		Description: "See the board, every project's plan, every person's timeline with tentative bookings, the unbooked list, the events and the stats — and with them every project's code, name, customer, status and dates, bids included.",
		Category:    "Planning", Sensitive: true, Delegable: true,
	},
	{
		Key: "planning:manage", Display: "Plan",
		Description: "Create, change, confirm and delete allocations and their terms, create, change, fill and withdraw requests, read the planners' notes, the candidates and the wishes, and change the Planning settings.",
		Category:    "Planning", Sensitive: true, Delegable: true,
	},
}

// limits maps each rate-limited operationId to its policy. It is empty: no
// Planning endpoint is rate limited.
var limits = map[string]ratelimit.Policy{}

// Module is planning as a platform module: its contract mounted under
// /api/v1/planning/ and its three permissions in the composed catalog. It
// provides no contract and has no background worker (design S22: computed on
// read, no digest). From delivery C it fills the many-provider
// CalendarSources slot with the caller's own confirmed allocations (S24),
// when the calendar module is on (S25, calendar_source.go).
func Module() module.Module {
	return module.Module{
		Name:           "planning",
		Permissions:    permissions,
		Mount:          mount,
		CalendarSource: newCalendarSource,
	}
}

// mount registers every contract operation on the platform router, which wraps
// each in its access rule and request-body cap before the generated wrapper
// decodes it. It fails when there is no project directory or no workforce to
// read — config already refuses "planning" without "projects" or "staff", so
// reaching here without one is a composition bug, and failing the mount says
// so where a nil contract would only panic on the first request — when the
// contract does not close the request schemas whose unknown properties are
// refused (strictbody.go), or when the router reports a problem: an operation
// never registered, a rule that does not parse, or a permission missing from
// the catalog.
func mount(d module.Deps) (http.Handler, error) {
	if d.Projects == nil {
		return nil, errors.New("planning: no project directory; the planning module requires the projects module")
	}
	if d.Workforce == nil {
		return nil, errors.New("planning: no workforce; the planning module requires the staff module")
	}
	if err := checkClosedSchemas(d.Doc); err != nil {
		return nil, err
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
		// keepRawBody runs inside the router's access rule and body cap, and
		// before the generated decoder: a schema the contract closes
		// (additionalProperties false) is judged by the raw body
		// (strictbody.go).
		Middlewares: []gen.MiddlewareFunc{keepRawBody},
	})
	if err := router.Err(); err != nil {
		return nil, err
	}
	return handler, nil
}
