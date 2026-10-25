// Package module is the platform every business module (identity, customers,
// products, energy, communications) mounts through: a router that enforces
// each operation's x-vantigo-access rule and rate limit at runtime, a
// Compose that assembles the modules under /api, and a Workers (workers.go)
// that collects the background workers the enabled modules contribute for
// cmd/vantigo's worker runner to start.
package module

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
	"github.com/vantigo-io/vantigo/server/internal/storage"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// Deps are the dependencies every module's Mount can use.
type Deps struct {
	Config  *config.Config
	Pool    *pgxpool.Pool
	Logger  *slog.Logger
	Clock   func() time.Time
	Mail    mail.Sender
	Secrets *secrets.Box
	Limiter *ratelimit.Limiter
	Access  contracts.Access
	Catalog map[string]contracts.Permission // the composed catalog
	// Doc is this module's own contract. Compose sets it, on a copy of Deps,
	// before calling Mount, by loading the module's own specs/<name>.yaml.
	//
	// It is read-only: the embedded specs are parsed once per process and the
	// same document is handed to every composition (see embedded in
	// compose.go), so a module that wrote to it would be writing to every
	// other composition's contract too.
	Doc *openapi3.T
	// Directory is the customer directory, the one sanctioned way a module
	// reads another's data (see contracts.CustomerDirectory). Compose sets
	// it, on every module's Deps copy, from whichever enabled module
	// declares Module.Directory; it is nil when no enabled module provides
	// one.
	Directory contracts.CustomerDirectory
	// Users is the user directory, the one sanctioned way a module reads
	// identity's user data (see contracts.UserDirectory). Compose sets it,
	// on every module's Deps copy, from whichever enabled module declares
	// Module.Users; unlike Directory, it is always set once composed —
	// identity, the one module always mounted, is the one that provides it.
	Users contracts.UserDirectory
	// Calendar is the business calendar, the one sanctioned way a module
	// reads the workspace's business time zone and derives a day from an
	// instant (see contracts.BusinessCalendar). Compose sets it, on every
	// module's Deps copy, from whichever enabled module declares
	// Module.Calendar — identity, always mounted — so, like Users, it is
	// always set once composed; Workers sets it too, since Staff's workers
	// judge "today" by it.
	Calendar contracts.BusinessCalendar
	// Accounts is account provisioning, the one sanctioned way a module
	// writes identity's users (see contracts.AccountProvisioning): staff →
	// identity, never called inside a caller's transaction. Compose sets it,
	// on every module's Deps copy, from whichever enabled module declares
	// Module.Accounts, after Users; Workers sets it too, because worker mode
	// never composes and the accounts worker writes through it. It is nil
	// when no enabled module provides one, which a caller answers as
	// "accounts are unavailable".
	Accounts contracts.AccountProvisioning
	// Products is the product catalog, the one sanctioned way a module
	// reads products' catalog data (see contracts.ProductCatalog). Compose
	// sets it, on every module's Deps copy, from whichever enabled module
	// declares Module.Products; it is nil when no enabled module provides
	// one (products disabled).
	Products contracts.ProductCatalog
	// Projects is the project directory, the one sanctioned way a module
	// reads projects' data (see contracts.ProjectDirectory). Compose sets
	// it, on every module's Deps copy, from whichever enabled module
	// declares Module.Projects; it is nil when projects is disabled.
	Projects contracts.ProjectDirectory
	// Actuals is what has been logged against projects, the one sanctioned
	// way a module reads another's record of actual work (see
	// contracts.ProjectActuals). Compose sets it, on every module's Deps
	// copy, from whichever enabled module declares Module.Actuals, after
	// Projects — so the provider may read deps.Projects while it is built,
	// though never while it serves; it is nil when time is disabled, which a
	// caller reads as "time tracking is off".
	Actuals contracts.ProjectActuals
	// Timesheets is the hours logged per identity user and week, the one
	// sanctioned way a module reads another's weekly total of logged work
	// (see contracts.Timesheets). It performs no authorization: the consumer
	// decides who may see whose hours. Compose sets it, on every module's
	// Deps copy, from whichever enabled module declares Module.Timesheets;
	// it is nil when time is disabled, which a caller reads as "time
	// tracking is off".
	Timesheets contracts.Timesheets
	// Expenses is what a project's expenses cost and bill, the one sanctioned
	// way a module reads another's record of recorded costs (see
	// contracts.ProjectExpenses). Compose sets it, on every module's Deps
	// copy, from whichever enabled module declares Module.Expenses, after
	// Projects — so the provider may read deps.Projects while it is built,
	// though never while it serves; it is nil when expenses is disabled,
	// which a caller reads as "expense tracking is off".
	Expenses contracts.ProjectExpenses
	// Receivables is what a customer has been invoiced and owes, the one
	// sanctioned way a module reads another's record of sales documents and
	// payments (see contracts.CustomerReceivables). Compose sets it, on every
	// module's Deps copy, from whichever enabled module declares
	// Module.Receivables; it is nil when invoices is disabled, which a caller
	// reads as "invoicing is off".
	Receivables contracts.CustomerReceivables
	// CustomerReferenceHolders are every given module's
	// contracts.CustomerReferenceHolder (customers merge design D1), enabled
	// or not, in the order the modules were given to Compose — the one
	// many-provider contract slot. Compose collects them before any Mount runs, after the six
	// single-provider slots, and appends them to whatever the caller preset
	// here (the seam modtest.WithCustomerReferenceHolders fills), so every
	// module's Deps carries the same list. Only the module that merges
	// customers calls them, and only inside its merge transaction. nil when no
	// module given holds customer ids.
	CustomerReferenceHolders []contracts.CustomerReferenceHolder
	// CustomerPersonalData is every given module's
	// contracts.CustomerPersonalData (customers GDPR design D2), enabled or
	// not, each under its module's name, in the order the modules were given —
	// the second many-provider contract slot, collected the holders' way and
	// for their reason. Compose collects it before any Mount runs, for the
	// export; Workers (workers.go) collects it too, for the anonymisation
	// worker, because worker mode never composes. Both append to whatever the
	// caller preset here (the seam modtest.WithCustomerPersonalData fills). nil
	// when no module given holds anything about a person.
	CustomerPersonalData []contracts.CustomerPersonalDataHolder
	// BillableHours, BillableExpenses and BillableMilestones are the
	// line-level reads an invoice is built from (contracts.BillableHours and
	// its two siblings, rule 10). Compose sets each, on every module's Deps
	// copy, from whichever enabled module declares it, after Projects — so a
	// provider may read deps.Projects while it is built, never while it
	// serves; each is nil when its module is disabled.
	BillableHours      contracts.BillableHours
	BillableExpenses   contracts.BillableExpenses
	BillableMilestones contracts.BillableMilestones
	// Workforce is Staff's register of competences, the one sanctioned way a
	// module reads who holds what (see contracts.Workforce, staff competence
	// design S15). Compose sets it, on every module's Deps copy, from
	// whichever enabled module declares Module.Workforce, after the business
	// calendar — so its provider may read deps.Calendar while it is built;
	// Workers sets it too, the same way. It is nil when no enabled module
	// provides one (staff disabled).
	Workforce contracts.Workforce
	// InvoicedWork is every given module's contracts.InvoicedWorkHolder
	// (rule 10), enabled or not, in the order the modules were given — the
	// third many-provider contract slot, collected the customer slots' way
	// and for their reason: a disabled module's rows are still stamped and
	// released. Compose collects it before any Mount runs; Workers collects it
	// too, because worker mode never composes and the EHF workers build an
	// invoices server from worker-mode Deps. Both append to whatever the caller
	// preset here (the seam modtest.WithInvoicedWork fills) and refuse a kind
	// two modules claim. Only the invoices issue calls them, inside its
	// transaction. nil when no module given provides invoiced work.
	InvoicedWork []contracts.InvoicedWorkHolder
	// CalendarSources are every enabled module's contracts.CalendarSource
	// (calendar design D1), in the order the modules were given — the fourth
	// many-provider slot, and the first collected from enabled modules only,
	// the way Workers is: a disabled module's permissions are not in the
	// catalog, so its gate could only fail closed. Compose collects them
	// before any Mount runs, after every single-provider slot, so a provider
	// may read deps.Calendar while it is built; Workers collects them too.
	// Both append to whatever the caller preset here. Only the calendar module
	// calls them, never inside a transaction. nil when no enabled module
	// projects anything onto the calendar. Not to be confused with Calendar,
	// the business calendar.
	CalendarSources []contracts.CalendarSource
	// IdentityProvisioners are every enabled module's
	// contracts.IdentityProvisioner (Entra design D6), in the order the
	// modules were given — the fifth many-provider slot, collected from
	// enabled modules only as CalendarSources is, for its reason: a
	// disabled provider module has no connection to drive. Compose collects
	// them before any Mount runs, after every single-provider slot and the
	// calendar sources, so a provider may read deps.Users and deps.Accounts
	// while it is built; Workers collects them too, because Staff's accounts
	// worker drives them and worker mode never composes. Both append to
	// whatever the caller preset here (the seam
	// modtest.WithIdentityProvisioners fills) and refuse a Key two providers
	// claim. Only Staff calls them, never inside a transaction. nil when no
	// enabled module provides one.
	IdentityProvisioners []contracts.IdentityProvisioner
	// HTTPTransport is the RoundTripper a module's own outbound HTTP client
	// (customers' Brreg lookup, so far the only one) dials through. nil in
	// production, meaning http.DefaultTransport; a test harness sets it to a
	// fake so no test ever touches the network, the same shape as Clock
	// makes time controllable.
	HTTPTransport http.RoundTripper
	// HTTPBackoff is the delay a module's own outbound HTTP client waits
	// before retry attempt n (1-indexed). nil in production, meaning that
	// client's own real backoff; a test harness sets it (typically to a
	// function returning 0) so a retry loop's tests never actually sleep.
	HTTPBackoff func(attempt int) time.Duration
	// SMTPVerify is the function a module's own SMTP connectivity check
	// (communications' channel verification, so far the only one) calls to
	// connect, authenticate and disconnect against a caller-supplied SMTP
	// configuration — no message sent. nil in production, meaning
	// mail.VerifyConnection itself, which dials through internal/mail's
	// real DNS-rebinding guard; a test harness sets it to a fake so a
	// verify-succeeds test can exercise a handler's success path without a
	// live SMTP server or the guard's network reach, the same shape
	// HTTPTransport gives an outbound HTTP client.
	SMTPVerify func(ctx context.Context, cfg config.MailConfig) error
	// SMTPSend is the function a module's own outbound mail path
	// (communications' outbox delivery worker, and invoices' send) calls to
	// deliver one message through a *caller-supplied* SMTP configuration —
	// communications passes each channel's own stored credentials, invoices
	// the process's SMTP_* settings (Config.Mail) — which is why this is a
	// function taking a config rather than another mail.Sender. nil in
	// production, meaning mail.SendOutbound
	// itself, which dials through internal/mail's real DNS-rebinding guard;
	// a test harness sets it to a fake that records the envelope, the same
	// seam SMTPVerify gives the verify-only path.
	SMTPSend func(ctx context.Context, cfg config.MailConfig, msg mail.Outbound) error
	// ObjectStore is the unscoped object store a module's own attachment
	// paths (communications' staging and download, so far the only ones)
	// put and get bytes through. nil in production, meaning that module
	// builds its own from Config.StorageProvider via internal/storage.New,
	// wrapped in its own storage.NewScope; a test harness sets it to a fake
	// so a storage-failure path (a 503) can be exercised deterministically,
	// the same seam HTTPTransport and SMTPVerify give an outbound
	// dependency a module cannot let a test touch for real.
	ObjectStore storage.ObjectStore
	// PeppolLookup is the function a module's own Peppol capability check
	// (customers' POST .../peppol-lookup, so far the only one) calls to ask
	// the Peppol network whether a participant identifier can receive an
	// EHF invoice. nil in production, meaning the module builds its own
	// *peppol.Client from Config (PeppolSMLZone, PeppolDNSServer,
	// PeppolTimeout) the same way newServer builds the Brreg client from
	// Config and HTTPTransport — but only when Config.PeppolLookupEnabled;
	// disabled, this seam is never even consulted, so a test that disables
	// the feature and still sets a fake here can assert the fake was never
	// called. A test harness sets it to a fake so a lookup's three outcomes
	// (registered, not registered, upstream failure) are exercised
	// deterministically, without a live Peppol network or the real client's
	// own DNS/SMP round trip — the same shape SMTPVerify gives an outbound
	// dependency a module cannot let a test touch for real.
	PeppolLookup func(ctx context.Context, participant string) (peppol.Result, error)
}

// Module is one business module: its name (the path segment it mounts under,
// /api/v1/<name>/), the permissions it contributes to the composed catalog,
// how to build its handler, and, optionally, the customer directory it
// provides to every other module.
type Module struct {
	Name        string
	Permissions []contracts.Permission
	Mount       func(Deps) (http.Handler, error)
	// Directory builds this module's contracts.CustomerDirectory
	// implementation, if it provides one. At most one enabled module may
	// set it; Compose calls it before any Mount runs and puts the result on
	// every module's Deps, including the provider's own.
	Directory func(Deps) contracts.CustomerDirectory
	// Users builds this module's contracts.UserDirectory implementation, if
	// it provides one. At most one enabled module may set it; Compose calls
	// it before any Mount runs and puts the result on every module's Deps,
	// including the provider's own, the same way it resolves Directory.
	Users func(Deps) contracts.UserDirectory
	// Calendar builds this module's contracts.BusinessCalendar
	// implementation, if it provides one. Compose resolves it right after
	// Users and puts the result on every module's Deps; Workers does the
	// same in worker mode.
	Calendar func(Deps) contracts.BusinessCalendar
	// Accounts builds this module's contracts.AccountProvisioning
	// implementation, if it provides one. At most one enabled module may set
	// it; Compose and Workers call it before anything they build runs —
	// after they have resolved Users, so a provider built here may read
	// deps.Users — and put the result on every module's Deps.
	//
	// The provider it builds must be stateless — its pool, its configuration
	// and its clock, nothing it remembers between calls: api mode with
	// WORKERS_IN_PROCESS builds it twice, once for Compose and once for
	// Workers, and the two must answer alike, as must one per process across
	// replicas.
	Accounts func(Deps) contracts.AccountProvisioning
	// Products builds this module's contracts.ProductCatalog
	// implementation, if it provides one. At most one enabled module may
	// set it; Compose calls it before any Mount runs and puts the result on
	// every module's Deps, including the provider's own, the same way it
	// resolves Directory.
	Products func(Deps) contracts.ProductCatalog
	// Projects builds this module's contracts.ProjectDirectory
	// implementation, if it provides one. At most one enabled module may
	// set it; Compose calls it before any Mount runs and puts the result on
	// every module's Deps, including the provider's own, the same way it
	// resolves Directory.
	Projects func(Deps) contracts.ProjectDirectory
	// Actuals builds this module's contracts.ProjectActuals implementation,
	// if it provides one. At most one enabled module may set it; Compose
	// calls it before any Mount runs — after it has resolved Projects, so a
	// provider built here may read deps.Projects — and puts the result on
	// every module's Deps, including the provider's own, the same way it
	// resolves Directory.
	Actuals func(Deps) contracts.ProjectActuals
	// Timesheets builds this module's contracts.Timesheets implementation,
	// if it provides one (time). At most one enabled module may set it;
	// Compose calls it before any Mount runs — after it has resolved
	// Actuals — and puts the result on every module's Deps, including the
	// provider's own, the same way it resolves Actuals.
	Timesheets func(Deps) contracts.Timesheets
	// Expenses builds this module's contracts.ProjectExpenses
	// implementation, if it provides one. At most one enabled module may set
	// it; Compose calls it before any Mount runs — after it has resolved
	// Projects, so a provider built here may read deps.Projects — and puts
	// the result on every module's Deps, including the provider's own, the
	// same way it resolves Actuals.
	Expenses func(Deps) contracts.ProjectExpenses
	// Receivables builds this module's contracts.CustomerReceivables
	// implementation, if it provides one (invoices). At most one enabled
	// module may set it; Compose calls it before any Mount runs — after it
	// has resolved Directory — and puts the result on every module's Deps,
	// including the provider's own, the same way it resolves Expenses.
	Receivables func(Deps) contracts.CustomerReceivables
	// Workers builds this module's background workers (worker.Worker), if
	// it has any. Unlike Directory, any number of enabled modules may set
	// it; Workers (workers.go) resolves it from deps the same way — before
	// any Mount runs — for every module MODULES enables, and concatenates
	// the results in mods order. A module with none leaves it nil.
	Workers func(Deps) []worker.Worker
	// CustomerReferences builds this module's
	// contracts.CustomerReferenceHolder, if it stores customer ids in its own
	// schema (customers merge design D1). Unlike Directory, and like Workers,
	// any number of modules may set it: Compose calls every one, in mods
	// order, before any Mount runs, and puts the list on every module's Deps
	// as CustomerReferenceHolders. Unlike Workers, it is called for a module
	// MODULES leaves out too — its schema is migrated regardless and may still
	// name customers — so it must need nothing of Deps a disabled module
	// would lack. A module holding no customer ids leaves it nil — time and
	// expenses reach a customer only through a project.
	CustomerReferences func(Deps) contracts.CustomerReferenceHolder
	// CustomerPersonalData builds this module's contracts.CustomerPersonalData,
	// if it holds anything about a customer as a person (customers GDPR design
	// D2). Like CustomerReferences, any number of modules may set it, and it is
	// called for a module MODULES leaves out too, so it must need nothing of
	// Deps a disabled module would lack. Compose and Workers both call it,
	// before anything they build runs, and put the list on Deps as
	// CustomerPersonalData, each under this module's Name. A module holding
	// nothing about a person leaves it nil — time and expenses reach a customer
	// only through a project, and products not at all.
	CustomerPersonalData func(Deps) contracts.CustomerPersonalData
	// BillableHours, BillableExpenses and BillableMilestones build this
	// module's line-level billable reads (contracts.BillableHours and its
	// siblings), if it provides one: time, expenses and projects respectively.
	// At most one enabled module may set each; Compose calls them before any
	// Mount runs — after it has resolved Projects — and puts the results on
	// every module's Deps, the same way it resolves Actuals.
	BillableHours      func(Deps) contracts.BillableHours
	BillableExpenses   func(Deps) contracts.BillableExpenses
	BillableMilestones func(Deps) contracts.BillableMilestones
	// Workforce builds this module's contracts.Workforce implementation, if
	// it provides one (staff). At most one enabled module may set it; Compose
	// and Workers call it before anything they build runs — after they have
	// resolved the business calendar — and put the result on every module's
	// Deps, including the provider's own, the same way they resolve Calendar.
	Workforce func(Deps) contracts.Workforce
	// InvoicedWork builds this module's contracts.InvoicedWorkHolder, if an
	// invoice is built from its rows (rule 10). Like CustomerReferences, any
	// number of modules may set it, and it is called for a module MODULES
	// leaves out too, so it must need nothing of Deps but the logger. Compose
	// and Workers both call it, before anything they build runs, and put the
	// list on Deps as InvoicedWork; no two holders may claim one kind. A module
	// that declares it has its CustomerReferences and CustomerPersonalData
	// placed after every other module's (see invoicedWorkLast in compose.go).
	InvoicedWork func(Deps) contracts.InvoicedWorkHolder
	// CalendarSource builds this module's contracts.CalendarSource, if it has
	// time-bound rows about a person to project onto the calendar (calendar
	// design D1). Like Workers, any number of enabled modules may set it, and
	// a module MODULES leaves out contributes nothing: Compose and Workers
	// call it for the enabled modules only, in mods order, after every
	// single-provider slot is resolved, and put the list on Deps as
	// CalendarSources. A nil result contributes nothing.
	CalendarSource func(Deps) contracts.CalendarSource
	// IdentityProvisioner builds this module's contracts.IdentityProvisioner,
	// if it drives an external identity provider's directory for Staff
	// (Entra design D6). Like CalendarSource, any number of enabled modules
	// may set it, and a module MODULES leaves out contributes nothing:
	// Compose and Workers call it for the enabled modules only, in mods
	// order, after the calendar sources, and put the list on Deps as
	// IdentityProvisioners; no two providers may answer one Key. A nil
	// result contributes nothing.
	IdentityProvisioner func(Deps) contracts.IdentityProvisioner
}
