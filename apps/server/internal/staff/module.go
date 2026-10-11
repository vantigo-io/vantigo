// Package staff is the staff module: the company's register of the people who
// work for it — their identity, contact, next of kin and bank, their
// employments as Norwegian arbeidsforhold with dated terms, the company's
// structure, a self-service link to their user, and the history of every
// change — serving openapi/staff.yaml under /api/v1/staff/.
//
// It depends on nobody but identity (design S1): it reads users through
// contracts.UserDirectory, which identity always provides, sends its one
// mail through the platform's SMTP seam, and keeps its documents' bytes in
// the platform's object store under its own scope. MODULES=staff alone is a valid
// installation. Nothing here imports another module — depguard enforces it,
// tests included — and no SQL of this module crosses a schema.
//
// Delivery A1 builds it in tasks on the names this foundation establishes: the
// whole of A1's schema (people and settings with every column the phase needs),
// the permissions, the settings, the structure and GET /meta come first; the
// people, the sealed identifiers, the self-service link, the employments and
// their terms, and the app follow.
package staff

import (
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// permissions is the module's permission catalog (design §5). Every operation
// requires staff:access — it is what puts the app in the switcher and what
// lets a person see their own record at all — and the other four sit above
// it.
//
// Two are sensitive. staff:manage is HR: every person's record, employments
// and pay, the structure and the settings. staff:personal-data reveals what
// nobody else may see — the national identity number and the bank account —
// and erases people. staff:approve is registered here although it grants
// nothing in delivery A1: it decides absence, which arrives with delivery B,
// and a catalog that grew a key in B would leave roles built before it short.
// All five are delegable, as every other module's are.
var permissions = []contracts.Permission{
	{
		Key: "staff:access", Display: "Use Staff",
		Description: "Use the Staff app: your own record, your own absence, your own documents and your own export.",
		Category:    "Staff", Sensitive: false, Delegable: true,
	},
	{
		Key: "staff:view", Display: "View the staff directory",
		Description: "See everyone's work details, employments and terms without pay, the company's structure, and the calendar with absence types hidden.",
		Category:    "Staff", Sensitive: false, Delegable: true,
	},
	{
		Key: "staff:approve", Display: "Decide absence",
		Description: "Decide anyone's absence requests, see absence types other than HR-only ones and the dates of open sick-leave cases, and mark their milestones.",
		Category:    "Staff", Sensitive: false, Delegable: true,
	},
	{
		Key: "staff:manage", Display: "Manage staff",
		Description: "Create and edit people (not their national identity number or bank account), employments and terms with pay, the company's structure, absence types and holiday schemes, the settings, documents other than health documents, the account flag, imports, exports and the payroll export.",
		Category:    "Staff", Sensitive: true, Delegable: true,
	},
	{
		Key: "staff:personal-data", Display: "Reveal and erase personal data",
		Description: "Reveal and change national identity numbers and bank accounts, read health documents and sick-leave notes, see HR-only absence types, export anyone's personal data, set legal holds, and erase and re-assess people.",
		Category:    "Staff", Sensitive: true, Delegable: true,
	},
}

// Module is staff as a platform module: its contract mounted under
// /api/v1/staff/ and its five permissions in the composed catalog. It provides
// no contract and fills no slot — nothing in another module reads the
// register yet (design reading 17 defers contracts.StaffDirectory to its first
// consumer).
func Module() module.Module {
	return module.Module{
		Name:        "staff",
		Permissions: permissions,
		Mount:       mount,
	}
}

// mount registers every contract operation on the platform router, which wraps
// each in its rate limit, access rule and request-body cap before the
// generated wrapper decodes it — every body capped at the platform default
// but the document upload's (objects.go's bodyLimits and limits). It fails
// when the contract does not close the request schemas whose unknown
// properties are refused (strictbody.go), or when the router reports a
// problem — an operation never registered, a rule that does not parse, or a
// permission missing from the catalog. An object store that cannot be built
// does not fail it: newServer keeps the error for the calls that need one.
func mount(d module.Deps) (http.Handler, error) {
	if err := checkClosedSchemas(d.Doc); err != nil {
		return nil, err
	}
	router := module.NewRouter(module.RouterOptions{
		Doc:        d.Doc,
		Access:     d.Access,
		Limiter:    d.Limiter,
		Limits:     limits,
		Catalog:    d.Catalog,
		BodyLimits: bodyLimits,
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
