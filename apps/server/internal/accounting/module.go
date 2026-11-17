// Package accounting is the Accounting module (nb Regnskap): the company's
// books as bokføringsloven asks for them — the chart of accounts with its
// SAF-T grouping codes, the tax codes, vouchers that are never edited and
// their postings, periods that lock, the opening balances, the party ledgers'
// open items and the specifications (accounting design,
// docs/superpowers/specs/2026-11-14-accounting-design.md, delivery A).
//
// Every voucher, whoever makes it, goes through one function, Post
// (posting.go): Invoices' sales post through it with a source key, read by
// the catch-up worker (catchup.go, sales.go; delivery B), and later
// deliveries post purchases, VAT terms and bank lines the same way. The module requires invoices
// (A1): Invoices' journal is what its sales post from (contracts.SalesJournal)
// and its seller is the company (contracts.SalesTax).
package accounting

import (
	"maps"
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// moduleName is the module's name, its schema and its sources' module.
const moduleName = "accounting"

// The permission keys (A1). An accountant is an ordinary user holding them.
// close is the owner's to give (not delegable): it moves the lock and
// changes the chart. export carries names, so it is sensitive.
// suppliers-manage is a buyer's (purchasing design A12): a supplier's name,
// addresses, contact and terms, not the fields of the books' own.
const (
	permRead            = "accounting:read"
	permPost            = "accounting:post"
	permClose           = "accounting:close"
	permExport          = "accounting:export"
	permSuppliersManage = "accounting:suppliers-manage"
)

var permissions = []contracts.Permission{
	{
		Key: permRead, Display: "Use Accounting",
		Description: "See the Accounting app: the chart, the tax codes, every voucher, the open items and every report.",
		Category:    "Accounting", Sensitive: false, Delegable: true,
	},
	{
		Key: permPost, Display: "Post vouchers",
		Description: "Post manual journals, reverse vouchers, attach files, keep the suppliers with their bank accounts, default accounts and tax codes, enter, post and pay supplier invoices, register payments on open items and act on the facts under Needs attention.",
		Category:    "Accounting", Sensitive: false, Delegable: true,
	},
	{
		Key: permClose, Display: "Manage the books",
		Description: "Set up the books (from a SAF-T file too), change the chart of accounts, the tax codes, the system roles and the posting rules, enter the opening balances and move the lock date.",
		Category:    "Accounting", Sensitive: false, Delegable: false,
	},
	{
		Key: permSuppliersManage, Display: "Keep the suppliers",
		Description: "Create and change suppliers — the name, the addresses, the contact and the terms — and look them up in Brønnøysundregisteret, also before the books are set up. A supplier's bank account, IBAN, BIC, default account, default tax code, person flag and active flag need Post vouchers; seeing Suppliers in Accounting needs Use Accounting.",
		Category:    "Accounting", Sensitive: false, Delegable: true,
	},
	{
		Key: permExport, Display: "Export the books",
		Description: "Download the specifications as files and export the books as SAF-T, which carry customers' and suppliers' names — a person's among them.",
		Category:    "Accounting", Sensitive: true, Delegable: true,
	},
}

// policyBrregLookup bounds the supplier lookup, the module's one call out of
// the installation: a lookup as one types, per client address.
var policyBrregLookup = ratelimit.Policy{
	Name:    "AccountingBrregLookup",
	Limit:   600,
	Window:  time.Hour,
	Message: "Too many lookups in Brønnøysundregisteret. Please wait a little.",
}

// policyFileUpload bounds the supplier invoices' file uploads, which write
// bytes outside the database, as Expenses' receipt uploads are bounded.
var policyFileUpload = ratelimit.Policy{
	Name:    "AccountingFileUpload",
	Limit:   600,
	Window:  time.Hour,
	Message: "Too many file uploads. Please wait a little before adding more.",
}

// limits are the module's rate limits beyond the platform's.
var limits = map[string]ratelimit.Policy{
	"lookupAccountingSupplierBrreg":       policyBrregLookup,
	"uploadAccountingSupplierInvoiceFile": policyFileUpload,
	"uploadAccountingSaftImport":          policyFileUpload,
}

// bodyLimits raises the router's request-body cap for an attachment: the file
// at most 20 MB and room for the multipart framing around it.
var bodyLimits = map[string]int64{
	"uploadAccountingAttachment":          maxAttachmentBytes + 64<<10,
	"uploadAccountingSupplierInvoiceFile": maxAttachmentBytes + 64<<10,
	// The opening balances may name a couple of thousand open documents.
	"postAccountingOpening": 4 << 20,
}

// Module is the Accounting module.
func Module() module.Module {
	return module.Module{
		Name:          moduleName,
		Permissions:   permissions,
		Mount:         mount,
		EventHandlers: eventHandlers,
		Workers:       workers,
		// The supplier register and the supplier invoices as other modules
		// read them (purchasing design P1, P6).
		SupplierDirectory: newSupplierDirectory,
		SupplierInvoices:  newSupplierInvoices,
	}
}

// mount builds the module's handler. Every operation but the meta and the
// setup answers 409 books_not_set_up until the books are set up (R12); the
// gate is a strict middleware, so no handler runs before it.
func mount(d module.Deps) (http.Handler, error) {
	srv := newServer(d)
	// A SAF-T file is as large as the configuration allows (A25), with room
	// for the multipart framing around it.
	limitsByOp := maps.Clone(bodyLimits)
	limitsByOp["uploadAccountingSaftImport"] = srv.saftImportMax() + 64<<10
	router := module.NewRouter(module.RouterOptions{
		Doc: d.Doc, Access: d.Access, Limiter: d.Limiter, Limits: limits, Catalog: d.Catalog,
		BodyLimits: limitsByOp,
	})
	strict := gen.NewStrictHandlerWithOptions(srv, []gen.StrictMiddlewareFunc{srv.setupGate}, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  module.DecodeError(apicommon.WriteDecodeError),
		ResponseErrorHandlerFunc: writeRefusal(),
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
