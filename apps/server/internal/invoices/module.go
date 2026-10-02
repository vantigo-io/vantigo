// Package invoices is the invoices module: the sales document of Norwegian
// bookkeeping (invoices foundation design, phase 1A). A draft is issued into an
// immutable, numbered document with a buyer snapshot, a seller snapshot and
// VAT per rate, rendered to a PDF that is stored once, correctable only by a
// credit note in the same series, and listed in a journal that proves the
// series has no gaps. It serves openapi/invoices.yaml under /api/v1/invoices/.
//
// It requires customers (config refuses MODULES=invoices without it): the
// buyer, its billing profile and its address come from
// contracts.CustomerDirectory, read before any transaction takes a lock.
// Nothing here imports another module — depguard enforces it, tests included —
// and no SQL of this module crosses a schema.
package invoices

import (
	"errors"
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// permissions is the module's catalog (D1). Every operation requires
// invoices:access, through the grammar permission:invoices:access+invoices:<x>,
// and no built-in role holds any of them: Owner has the wildcard, and everyone
// else is granted invoicing deliberately. Issuing, the seller record and
// payments are sensitive — the first creates bookkeeping material that can
// never be taken back, the second decides what every document says the
// company is, and the third changes what the company says it is owed, a wrong
// registration corrected only by a removal that stays on record (payments and
// delivery design D1). Sending is under invoices:issue.
var permissions = []contracts.Permission{
	{
		Key: "invoices:access", Display: "Use Invoices",
		Description: "Use the Invoices app and read every invoice, credit note, PDF, payment and delivery, the journal, the CSV export and the stats.",
		Category:    "Invoices", Sensitive: false, Delegable: true,
	},
	{
		Key: "invoices:create", Display: "Create invoices",
		Description: "Create, edit and delete invoice drafts, and preview a draft as PDF.",
		Category:    "Invoices", Sensitive: false, Delegable: true,
	},
	{
		Key: "invoices:issue", Display: "Issue invoices",
		Description: "Issue a draft into a numbered document that can never be changed, create credit notes, and send an issued document by e-mail.",
		Category:    "Invoices", Sensitive: true, Delegable: true,
	},
	{
		Key: "invoices:manage", Display: "Manage invoicing",
		Description: "Change the seller record, the number series start, and the VAT codes and their rates.",
		Category:    "Invoices", Sensitive: true, Delegable: true,
	},
	{
		Key: "invoices:payments", Display: "Register payments",
		Description: "Register payments against issued invoices, and remove a registration with a reason.",
		Category:    "Invoices", Sensitive: true, Delegable: true,
	},
}

// limits maps each rate-limited operationId to its policy; the router hits
// it per client address before access is checked. A send takes an arbitrary
// recipient, which makes the endpoint an authenticated relay through the
// installation's SMTP server: 60 sends per client per 10 minutes (payments
// and delivery design D4).
var limits = map[string]ratelimit.Policy{
	"postInvoicesByIdSend": {Name: "invoices-send", Limit: 60, Window: 10 * time.Minute},
}

// Module is invoices as a platform module: its contract mounted under
// /api/v1/invoices/, its five permissions in the composed catalog, and the two
// slots every module holding customer ids fills (customer_slots.go): the merge
// holder and the personal-data provider.
func Module() module.Module {
	return module.Module{
		Name:                 "invoices",
		Permissions:          permissions,
		Mount:                mount,
		CustomerReferences:   newCustomerReferenceHolder,
		CustomerPersonalData: newCustomerPersonalData,
	}
}

// mount registers every contract operation on the platform router, which wraps
// each in its access rule and request-body cap before the generated wrapper
// decodes it. It fails when the router reports a problem, when the object
// store cannot be built, and — before any of that — when there is no customer
// directory: config already refuses "invoices" without "customers", so
// reaching here without one is a composition bug, and failing the mount says so
// where a nil directory would only panic on the first request.
func mount(d module.Deps) (http.Handler, error) {
	if d.Directory == nil {
		return nil, errors.New("invoices: no customer directory; the invoices module requires the customers module")
	}
	router := module.NewRouter(module.RouterOptions{
		Doc:     d.Doc,
		Access:  d.Access,
		Limiter: d.Limiter,
		Limits:  limits,
		Catalog: d.Catalog,
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
