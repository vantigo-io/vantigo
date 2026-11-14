// Package accounting is the Accounting module (nb Regnskap): the company's
// books as bokføringsloven asks for them — the chart of accounts with its
// SAF-T grouping codes, the tax codes, vouchers that are never edited and
// their postings, periods that lock, the opening balances, the party ledgers'
// open items and the specifications (accounting design,
// docs/superpowers/specs/2026-11-14-accounting-design.md, delivery A).
//
// Every voucher, whoever makes it, goes through one function, Post
// (posting.go): later deliveries post Invoices' sales, purchases, VAT terms
// and bank lines through it with a source key. The module requires invoices
// (A1): Invoices' journal is what its sales post from (contracts.SalesJournal)
// and its seller is the company (contracts.SalesTax).
package accounting

import (
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// moduleName is the module's name, its schema and its sources' module.
const moduleName = "accounting"

// The four permission keys (A1). An accountant is an ordinary user holding
// them. close is the owner's to give (not delegable): it moves the lock and
// changes the chart. export carries names, so it is sensitive.
const (
	permRead   = "accounting:read"
	permPost   = "accounting:post"
	permClose  = "accounting:close"
	permExport = "accounting:export"
)

var permissions = []contracts.Permission{
	{
		Key: permRead, Display: "Use Accounting",
		Description: "See the Accounting app: the chart, the tax codes, every voucher, the open items and every report.",
		Category:    "Accounting", Sensitive: false, Delegable: true,
	},
	{
		Key: permPost, Display: "Post vouchers",
		Description: "Post manual journals, reverse vouchers, attach files and register payments on open items.",
		Category:    "Accounting", Sensitive: false, Delegable: true,
	},
	{
		Key: permClose, Display: "Manage the books",
		Description: "Set up the books, change the chart of accounts, the tax codes and the system roles, enter the opening balances and move the lock date.",
		Category:    "Accounting", Sensitive: false, Delegable: false,
	},
	{
		Key: permExport, Display: "Export the books",
		Description: "Download the specifications as files, which carry customers' and suppliers' names.",
		Category:    "Accounting", Sensitive: true, Delegable: true,
	},
}

// limits are the module's rate limits; none beyond the platform's.
var limits = map[string]ratelimit.Policy{}

// bodyLimits raises the router's request-body cap for an attachment: the file
// at most 20 MB and room for the multipart framing around it.
var bodyLimits = map[string]int64{
	"uploadAccountingAttachment": maxAttachmentBytes + 64<<10,
	// The opening balances may name a couple of thousand open documents.
	"postAccountingOpening": 4 << 20,
}

// Module is the Accounting module.
func Module() module.Module {
	return module.Module{
		Name:        moduleName,
		Permissions: permissions,
		Mount:       mount,
	}
}

// mount builds the module's handler. Every operation but the meta and the
// setup answers 409 books_not_set_up until the books are set up (R12); the
// gate is a strict middleware, so no handler runs before it.
func mount(d module.Deps) (http.Handler, error) {
	router := module.NewRouter(module.RouterOptions{
		Doc: d.Doc, Access: d.Access, Limiter: d.Limiter, Limits: limits, Catalog: d.Catalog,
		BodyLimits: bodyLimits,
	})
	srv := newServer(d)
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
