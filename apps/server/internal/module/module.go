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

	"github.com/vantigo-io/vantigo/server/internal/ai"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/operations"
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
	// SSOConnections is what connecting existing users to the workforce
	// provider reads of identity (see contracts.SSOConnections): the users
	// not yet connected and who holds a directory key, gated by identity
	// itself. Compose sets it, on every module's Deps copy, from whichever
	// enabled module declares Module.SSOConnections, after Users; Workers
	// does not, since no worker connects anyone. It is nil when no enabled
	// module provides one.
	SSOConnections contracts.SSOConnections
	// DirectorySearches are every enabled module's contracts.DirectorySearch
	// (Google Workspace design G8, module-boundaries rule 22): a directory's
	// accounts as identity offers them to an Owner connecting existing users
	// — a many-provider slot keyed by Key, the OIDC provider each serves;
	// identity asks the one whose Key is the configured provider. Compose
	// collects them from the enabled modules, after SSOConnections, which a
	// provider reads; Workers does not, since no worker searches a directory.
	// Compose appends to whatever the caller preset here (the seam
	// modtest.WithDirectorySearch fills) and refuses a Key two providers
	// claim or a reserved one. Only identity calls them, never inside a
	// transaction. nil when no enabled module provides one.
	DirectorySearches []contracts.DirectorySearch
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
	// SalesTax is Invoices' VAT codes, the mapping from Products' tax
	// categories to them, and the seller (contracts.SalesTax, orders design
	// O8). Compose sets it, on every module's Deps copy, from whichever
	// enabled module declares Module.SalesTax, beside Receivables; it is nil
	// when invoices is disabled.
	SalesTax contracts.SalesTax
	// SalesJournal is what Invoices has issued and been paid, in an order a
	// reader resumes from (contracts.SalesJournal, accounting design A12).
	// Compose and Workers set it, on every module's Deps copy, from whichever
	// enabled module declares Module.SalesJournal, right after SalesTax; it is
	// nil when invoices is disabled.
	SalesJournal contracts.SalesJournal
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
	// VariantReferenceHolders are every given module's
	// contracts.VariantReferenceHolder (inventory design I23), enabled or not,
	// in the order the modules were given — collected as
	// CustomerReferenceHolders is, for its reason: stock in a disabled
	// module's schema is still stock. Compose appends them to whatever the
	// caller preset here (modtest.WithVariantReferenceHolders). Only products
	// calls them, inside the transaction that deletes a variant or turns a
	// product into a service, after it has locked the variants. nil when no
	// module given holds variants.
	VariantReferenceHolders []contracts.VariantReferenceHolder
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
	// Consumption is Energy's settlements, what a customer owes for its
	// metering points (contracts.ConsumptionProvider, energy consumption
	// billing design D1). Compose sets it, on every module's Deps copy, from
	// whichever enabled module declares Module.Consumption, after the three
	// billable reads; it is nil when Energy is disabled.
	Consumption contracts.ConsumptionProvider
	// BillableOrders is Orders' read for invoicing: the order lines due to be
	// invoiced, and the allocations an invoice line is built from
	// (contracts.BillableOrders, orders design O37). Compose and Workers set
	// it, on every module's Deps copy, from whichever enabled module declares
	// Module.BillableOrders, after Consumption; it is nil when Orders is
	// disabled.
	BillableOrders contracts.BillableOrders
	// Stock is Inventory's read of on hand and available per variant and
	// site (contracts.Stock, orders design O45). Compose and Workers set it,
	// on every module's Deps copy, from whichever enabled module declares
	// Module.Stock, after the product catalog; it is nil when Inventory is
	// off, which a caller reads as "stock is not kept": it reserves and
	// issues nothing.
	Stock contracts.Stock
	// StockLedger is Inventory's write of reservations and issues on the
	// caller's transaction (contracts.StockLedger, module-boundaries rule
	// 24). Unlike Stock it is resolved from every module given, enabled or
	// not — the holders' reason: a reservation made while Inventory was on
	// must still be released after it is switched off — so it is nil only
	// when no module given provides one.
	StockLedger contracts.StockLedger
	// SalesOrderLines is Orders' read of its sales orders' goods lines for
	// Manufacturing (contracts.SalesOrderLines, manufacturing design M43a),
	// consumer-gated. Compose and Workers set it, on every module's Deps copy,
	// from whichever enabled module declares Module.SalesOrderLines, after the
	// stock slots; it is nil when Orders is off.
	SalesOrderLines contracts.SalesOrderLines
	// Production is Manufacturing's read of what is being made for sales
	// order lines (contracts.Production, manufacturing design M46a),
	// provider-gated on manufacturing:access. Compose and Workers set it the
	// same way, from Module.Production, beside SalesOrderLines; it is nil
	// when Manufacturing is off.
	Production contracts.Production
	// DeliveryTrace is Orders' read of its posted deliveries for Inventory's
	// trace (contracts.DeliveryTrace, manufacturing phase 2 M90),
	// provider-gated on orders:access. Compose and Workers set it the same
	// way, from Module.DeliveryTrace, beside SalesOrderLines; it is nil when
	// Orders is off.
	DeliveryTrace contracts.DeliveryTrace
	// KitStructure is Manufacturing's ungated read of its kit BoMs' components
	// (contracts.KitStructure, manufacturing phase 2 M145), which Orders
	// copies onto a kit line's child lines. Compose and Workers set it the
	// same way, from Module.KitStructure, beside SalesOrderLines; it is nil
	// when Manufacturing is off.
	KitStructure contracts.KitStructure
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
	// MailboxCalendars are every enabled module's contracts.MailboxCalendar
	// (calendar Outlook sync design O4, Google Workspace design G2), the one
	// sanctioned way the calendar writes a person's copies to their mailbox —
	// a many-provider slot keyed by Key, in the order the modules were given.
	// The calendar picks the one whose Key is the person's
	// DirectoryAccount.Provider (G3). Compose collects them from the enabled
	// modules, after the identity provisioners; Workers collects them too,
	// because the calendar's sync worker runs in worker mode. Both append to
	// whatever the caller preset here (the seam modtest.WithMailboxCalendar
	// fills) and refuse a Key two providers claim or a reserved one. Only the
	// calendar calls them, never inside a transaction. nil when no enabled
	// module provides one.
	MailboxCalendars []contracts.MailboxCalendar
	// ChannelMailboxes are every enabled module's contracts.ChannelMailbox
	// (Outlook connection design M11, rule 15, Google Workspace design G2),
	// the one sanctioned way Communications sends through a mailbox provider
	// — a many-provider slot keyed by Key, which a channel names in its
	// provider column. Compose collects them from the enabled modules, after
	// the mailbox calendars; Workers collects them too, because
	// Communications' outbox runs in worker mode. Both append to whatever the
	// caller preset here (the seam modtest.WithChannelMailbox fills) and
	// refuse a Key two providers claim or a reserved one. Only Communications
	// calls them, never inside a transaction. nil when no enabled module
	// provides one.
	ChannelMailboxes []contracts.ChannelMailbox
	// ChannelMessengers are every enabled module's contracts.ChannelMessenger
	// (Messenger design N2), the one sanctioned way Communications sends and
	// receives through a messaging provider — a many-provider slot keyed by
	// Key, which a channel of type messenger names in its provider column.
	// Compose collects them from the enabled modules, after the channel
	// mailboxes; Workers collects them too, because Communications' outbox and
	// its messenger sync run in worker mode. Both append to whatever the
	// caller preset here (the seam modtest.WithChannelMessenger fills) and
	// refuse a Key two providers claim, a reserved one, or one also filled in
	// ChannelMailboxes. Only Communications calls them, never inside a
	// transaction. nil when no enabled module provides one.
	ChannelMessengers []contracts.ChannelMessenger
	// PersonalMailboxes are every enabled module's contracts.PersonalMailbox
	// (Outlook connection design M20, Google Workspace design G2), through
	// which Communications sends from a person's own address — a
	// many-provider slot keyed by Key; Communications asks each for the
	// caller's status and records the one it sent through (G3). Compose
	// collects them from the enabled modules, after the channel mailboxes;
	// Workers collects them too, because Communications' outbox runs in
	// worker mode. Both append to whatever the caller preset here (the seam
	// modtest.WithPersonalMailbox fills) and refuse a Key two providers claim
	// or a reserved one. Only Communications calls them, never inside a
	// transaction. nil when no enabled module provides one.
	PersonalMailboxes []contracts.PersonalMailbox
	// OutboundMail is Communications' channels as other modules send through
	// them (see contracts.OutboundMail, integrations design T4, module-boundaries
	// rule 17): the one way Invoices and Calendar write to a customer, from a
	// channel an Owner chose. Compose sets it, on every module's Deps copy,
	// from whichever enabled module declares Module.OutboundMail, after the
	// channel and the personal mailboxes — so its provider may read
	// deps.ChannelMailboxes while it is built; Workers sets it too, the same
	// way, because Invoices' reminder worker sends in worker mode. Both keep a
	// preset value when no enabled module provides one (the seam
	// modtest.WithOutboundMail fills). Consumers call it outside any
	// transaction. nil when Communications is disabled, which a consumer
	// answers as "sending is disabled".
	OutboundMail contracts.OutboundMail
	// CalendarBusy is the calendar's busy time of people, provider-gated
	// (see contracts.CalendarBusy, calendar Outlook sync design O9.8).
	// Compose sets it, on every module's Deps copy, from whichever enabled
	// module declares Module.CalendarBusy, last of the single-provider
	// slots — after the mailbox calendar — so its provider may read the rest
	// of Deps while it is built. Workers does not: no worker reads it. A
	// preset value is kept when no enabled module provides one (the seam
	// modtest.WithCalendarBusy fills). Only Planning reads it, never inside a
	// transaction. nil when the calendar is disabled.
	CalendarBusy contracts.CalendarBusy
	// ContactChannels is what Communications knows of a contact's Messenger
	// channels, for Customers' contact rows (see contracts.ContactChannels,
	// Messenger design N22). Compose sets it, on every module's Deps copy, from
	// whichever enabled module declares Module.ContactChannels, after the
	// channel messengers its provider reads; Workers does not — no worker
	// reads it. A preset value is kept when no enabled module provides one
	// (the seam modtest.WithContactChannels fills). Consumer-gated, and never
	// called inside a transaction. nil when Communications is disabled.
	ContactChannels contracts.ContactChannels
	// AgentMemory is what the virtual employees remember about a customer, for
	// the customer page (see contracts.AgentMemory, virtual employees design
	// V27). Compose sets it, on every module's Deps copy, from whichever
	// enabled module declares Module.AgentMemory; Workers does not — no worker
	// reads it. A preset value is kept when no enabled module provides one (the
	// seam modtest.WithAgentMemory fills). Consumer-gated, and never called
	// inside a transaction. nil when Agents is disabled.
	AgentMemory contracts.AgentMemory
	// AgentColleagues is what the Assistant needs of the virtual employees to
	// let a person talk to one in the panel (see contracts.AgentColleagues,
	// virtual employees design V31). Compose sets it, on every module's Deps
	// copy, from whichever enabled module declares Module.AgentColleagues;
	// Workers does not — no worker reads it. A preset value is kept when no
	// enabled module provides one (the seam modtest.WithAgentColleagues fills).
	// Consumer-gated. nil when Agents is disabled.
	AgentColleagues contracts.AgentColleagues
	// AITasks are the AI tasks the enabled modules declare
	// (Module.AITasks, contracts.AITask), by key — composed the way Catalog is
	// composed from Permissions: a disabled module's are absent, and a key two
	// modules declare is refused. Compose sets it, on every module's Deps copy,
	// before any slot is resolved, so identity's AIModels provider may read it
	// while it is built. Workers does not: no worker performs a task yet.
	AITasks map[string]contracts.AITask
	// AIModels is the model chosen per AI task (contracts.AIModels, AI as a
	// platform provider design T12), the single-provider slot identity fills.
	// Compose sets it, on every module's Deps copy, from whichever enabled
	// module declares Module.AIModels, and hands it to the client below as its
	// resolver; a module does not call it, it calls AI. A preset value is kept
	// when no enabled module provides one (the seam modtest.WithAIModels
	// fills). nil when no enabled module provides one: every task then uses
	// the plan's default.
	AIModels contracts.AIModels
	// AssistantGrants issues and ends the assistant's grants
	// (contracts.AssistantGrants, the Assistant design S3). Compose resolves
	// it from the enabled modules (identity provides it), after the AI slot;
	// Workers does not, since a grant is issued while the person's own message
	// request is being served.
	AssistantGrants contracts.AssistantGrants
	// AgentAccounts makes and changes the users of kind agent
	// (contracts.AgentAccounts, virtual employees design V5) and AgentGrants
	// issues and ends the agents' grants (contracts.AgentGrants, design V6).
	// Compose resolves both from the enabled modules (identity provides them),
	// beside AssistantGrants; Workers does not resolve them.
	AgentAccounts contracts.AgentAccounts
	AgentGrants   contracts.AgentGrants
	// Operations is the dispatcher that calls the merged contract's operations
	// in-process, through the composed router, as the assistant principal
	// (the Assistant design S2, module boundaries rule 18). Compose sets it
	// only on the Deps of the module named "assistant"; it is nil for every
	// other module, and for the assistant until Compose has composed the
	// router, so it is for use while serving, never while mounting.
	Operations *operations.Dispatcher
	// AI is the platform's AI client (srv/ai): the one place the provider's
	// key lives, called with a task key and never with a model. Compose builds
	// it from Config (AI_PROVIDER and friends), HTTPTransport and AIModels, last
	// of the AI slots; a preset value is kept. nil is the unavailable state —
	// no provider is configured — which a caller answers as 503 ai_unavailable.
	AI *ai.Client
	// Live is the registry of live topics (live design L9): Compose fills it
	// from every enabled module's LiveTopics, after their Mount, and gives it
	// the resolved user directory. A caller that serves the live endpoint
	// presets it, so it can build its hub over what Compose filled; without
	// one Compose makes its own. A module never needs it to notify:
	// live.Notify is a free function.
	Live *live.Registry
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
	// (communications' outbox delivery worker and its OutboundMail, and
	// staff's notices) calls to deliver one message through a
	// *caller-supplied* SMTP configuration — communications passes each
	// channel's own stored credentials, staff the process's SMTP_* settings
	// (Config.Mail), the system mailbox's, for the tenant's own employees;
	// customers' mail (Invoices, Calendar) goes through OutboundMail and a
	// channel, never through here — which is why this is a
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
	// SSOConnections builds this module's contracts.SSOConnections
	// implementation, if it provides one. At most one enabled module may set
	// it; Compose calls it before any Mount runs, after Users, and puts the
	// result on every module's Deps.
	SSOConnections func(Deps) contracts.SSOConnections
	// DirectorySearch builds this module's contracts.DirectorySearch, if it
	// offers its directory's accounts to identity's connecting of existing
	// users (Google Workspace design G8). Any number of enabled modules may
	// set it, each under its own Key; Compose calls it after SSOConnections
	// is resolved — the provider may hold deps.SSOConnections — and puts the
	// list on every module's Deps as DirectorySearches. Workers does not. A
	// nil result contributes nothing.
	DirectorySearch func(Deps) contracts.DirectorySearch
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
	// SalesTax builds this module's contracts.SalesTax implementation, if it
	// provides one (invoices, orders design O8). At most one enabled module
	// may set it; Compose calls it before any Mount runs, right after
	// Receivables, and puts the result on every module's Deps, including the
	// provider's own.
	SalesTax func(Deps) contracts.SalesTax
	// SalesJournal builds this module's contracts.SalesJournal implementation,
	// if it provides one (invoices, accounting design A12). At most one
	// enabled module may set it; Compose and Workers call it right after
	// SalesTax and put the result on every module's Deps, including the
	// provider's own.
	SalesJournal func(Deps) contracts.SalesJournal
	// EventHandlers builds this module's handlers of the domain-events outbox
	// (events.Handler, orders design O2), if it has any — a many-provider
	// slot. Like CustomerReferences it is called for a module MODULES leaves
	// out too: a handler of a disabled module that needs its module on checks
	// the setting itself and does nothing. So it must need nothing of Deps a
	// disabled module would lack. A handler's Name is unique across the
	// composition.
	EventHandlers func(Deps) []events.Handler
	// Workers builds this module's background workers (worker.Worker), if
	// it has any. Unlike Directory, any number of enabled modules may set
	// it; Workers (workers.go) resolves it from deps the same way — before
	// any Mount runs — for every module MODULES enables, and concatenates
	// the results in mods order. A module with none leaves it nil.
	Workers func(Deps) []worker.Worker
	// APIRunners builds this module's runners (Runner), if it has any: work
	// that needs the operations dispatcher, which only the API process has
	// (virtual employees design V14; the Assistant's bulk host has no poller
	// either). ComposeAPI calls it for every enabled module, in mods order,
	// after the dispatcher is bound to the composed router — so Deps.Operations
	// of a module that receives it is ready to call — and the caller starts
	// the runners (StartRunners) on the process's context, in the API process
	// and never in worker mode. Unlike Workers, which Workers (workers.go)
	// collects without composing, this is not available to a worker process.
	APIRunners func(Deps) []Runner
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
	// VariantReferences builds this module's
	// contracts.VariantReferenceHolder, if it keeps stock or another fact of a
	// product variant that the variant's delete must not outrun (inventory
	// design I23). Like CustomerReferences, any number of modules may set it,
	// and it is called for a module MODULES leaves out too, so it must need
	// nothing of Deps a disabled module would lack.
	VariantReferences func(Deps) contracts.VariantReferenceHolder
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
	// Consumption builds this module's contracts.ConsumptionProvider, if it
	// provides one (energy). At most one enabled module may set it; Compose
	// calls it before any Mount runs — after it has resolved the billable
	// reads — and puts the result on every module's Deps, the same way it
	// resolves them. The provider is built from Deps.Pool and Deps.Clock and
	// never reads a directory while it serves.
	Consumption func(Deps) contracts.ConsumptionProvider
	// BillableOrders builds this module's contracts.BillableOrders, if it
	// provides one (orders). At most one enabled module may set it; Compose
	// and Workers call it before anything they build runs — after they have
	// resolved Consumption — and put the result on every module's Deps, the
	// same way. The provider is built from Deps.Pool and Deps.Clock and never
	// reads a directory while it serves.
	BillableOrders func(Deps) contracts.BillableOrders
	// Stock builds this module's contracts.Stock, if it provides one
	// (inventory). At most one enabled module may set it; Compose and
	// Workers call it before anything they build runs, after the product
	// catalog, and put the result on every module's Deps.
	Stock func(Deps) contracts.Stock
	// StockLedger builds this module's contracts.StockLedger, if it provides
	// one (inventory). At most one module given may set it, and — like
	// CustomerReferences — it is called for a module MODULES leaves out too,
	// so it must need nothing of Deps a disabled module would lack beyond the
	// pool, the clock and the product catalog (nil when Products is off, or in
	// worker mode, which then fails a Reserve or an Issue but never a
	// Release).
	StockLedger func(Deps) contracts.StockLedger
	// SalesOrderLines builds this module's contracts.SalesOrderLines, if it
	// provides one (orders). At most one enabled module may set it; Compose
	// and Workers call it after the stock slots and put the result on every
	// module's Deps. The provider is built from Deps.Pool and reads nothing
	// while it is built.
	SalesOrderLines func(Deps) contracts.SalesOrderLines
	// Production builds this module's contracts.Production, if it provides
	// one (manufacturing). At most one enabled module may set it; Compose and
	// Workers call it beside SalesOrderLines, from Deps.Pool and Deps.Access.
	Production func(Deps) contracts.Production
	// DeliveryTrace builds this module's contracts.DeliveryTrace, if it
	// provides one (orders). At most one enabled module may set it; Compose
	// and Workers call it beside SalesOrderLines, from Deps.Pool and
	// Deps.Access.
	DeliveryTrace func(Deps) contracts.DeliveryTrace
	// KitStructure builds this module's contracts.KitStructure, if it
	// provides one (manufacturing). At most one enabled module may set it;
	// Compose and Workers call it beside SalesOrderLines, from Deps.Pool.
	KitStructure func(Deps) contracts.KitStructure
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
	// MailboxCalendar builds this module's contracts.MailboxCalendar, if it
	// writes people's mailbox calendars for the calendar (calendar Outlook
	// sync design O4). Any number of enabled modules may set it, each under
	// its own Key (Google Workspace design G2); Compose and Workers call it
	// after the identity provisioners — it reads nothing while it is built —
	// and put the list on every module's Deps as MailboxCalendars. A nil
	// result contributes nothing.
	MailboxCalendar func(Deps) contracts.MailboxCalendar
	// ChannelMailbox builds this module's contracts.ChannelMailbox, if it
	// sends Communications' channel mail through a mailbox provider (Outlook
	// connection design M11). Any number of enabled modules may set it, each
	// under its own Key; Compose and Workers call it after the mailbox
	// calendars — it reads nothing while it is built — and put the list on
	// every module's Deps as ChannelMailboxes. A nil result contributes
	// nothing.
	ChannelMailbox func(Deps) contracts.ChannelMailbox
	// ChannelMessenger builds this module's contracts.ChannelMessenger, if it
	// sends and receives Communications' messenger channels through a
	// messaging provider (Messenger design N2). Any number of enabled modules
	// may set it, each under its own Key, which no ChannelMailbox may also
	// claim; Compose and Workers call it after the channel mailboxes — it
	// reads nothing while it is built — and put the list on every module's
	// Deps as ChannelMessengers. A nil result contributes nothing.
	ChannelMessenger func(Deps) contracts.ChannelMessenger
	// PersonalMailbox builds this module's contracts.PersonalMailbox, if it
	// sends Communications' personal mail through a person's connected
	// mailbox (Outlook connection design M20). Any number of enabled
	// modules may set it, each under its own Key; Compose and Workers call
	// it after the channel mailboxes — it reads nothing while it is built —
	// and put the list on every module's Deps as PersonalMailboxes. A nil
	// result contributes nothing.
	PersonalMailbox func(Deps) contracts.PersonalMailbox
	// OutboundMail builds this module's contracts.OutboundMail, if it sends
	// other modules' mail through its channels (integrations design T4:
	// Communications). At most one enabled module may set it; Compose and
	// Workers call it after the personal mailbox — its provider may read
	// deps.ChannelMailboxes while it is built, never while it serves outside
	// the provider's own calls — and put the result on every module's Deps.
	OutboundMail func(Deps) contracts.OutboundMail
	// AITasks are the AI jobs this module performs (contracts.AITask), declared
	// the way Permissions are: Compose validates each, refuses a key two
	// modules declare, and collects the enabled modules' into Deps.AITasks, the
	// list the AI models page shows an Owner. A task is declared by the delivery
	// that performs it.
	AITasks []contracts.AITask
	// AIModels builds this module's contracts.AIModels, if it keeps the
	// workspace's choice of model per AI task (identity). At most one enabled
	// module may set it; Compose calls it after the other slots, with
	// Deps.AITasks set, and gives the result to the AI client it builds. Workers
	// never calls it.
	AIModels func(Deps) contracts.AIModels
	// AssistantGrants provides contracts.AssistantGrants. At most one enabled
	// module may declare it; identity does.
	AssistantGrants func(Deps) contracts.AssistantGrants
	// AgentAccounts provides contracts.AgentAccounts and AgentGrants provides
	// contracts.AgentGrants. At most one enabled module may declare each;
	// identity does.
	AgentAccounts func(Deps) contracts.AgentAccounts
	AgentGrants   func(Deps) contracts.AgentGrants
	// LiveTopics builds the live topics this module serves (live design L9),
	// if it has any: each kind is <module>.<kind>, its prefix the module's
	// name. Compose calls it for every enabled module, right after its
	// Mount, with the same Deps, and refuses a kind outside the grammar, a
	// prefix that is not the module's name, or a kind two modules declare.
	// A disabled module's topics are unknown to the hub.
	LiveTopics func(Deps) []live.Topic
	// CalendarBusy builds this module's contracts.CalendarBusy, if it answers
	// people's busy time to other modules (calendar Outlook sync design
	// O9.8). At most one enabled module may set it; Compose calls it last of
	// the single-provider slots, after the mailbox calendar, and puts the
	// result on every module's Deps. Workers never calls it.
	CalendarBusy func(Deps) contracts.CalendarBusy
	// ContactChannels builds this module's contracts.ContactChannels, if it
	// answers a contact's messaging channels to other modules (Messenger design
	// N22: Communications). At most one enabled module may set it; Compose
	// calls it after the channel messengers it reads, and puts the result on
	// every module's Deps. Workers never calls it.
	ContactChannels func(Deps) contracts.ContactChannels
	// AgentMemory builds this module's contracts.AgentMemory, if it answers
	// what the agents remember about a customer to other modules (virtual
	// employees design V27: Agents). At most one enabled module may set it;
	// Compose calls it after ContactChannels and puts the result on every
	// module's Deps. Workers never calls it.
	AgentMemory func(Deps) contracts.AgentMemory
	// AgentColleagues builds this module's contracts.AgentColleagues, if it
	// answers which agents a person may talk to and what they may do to the
	// Assistant (virtual employees design V31: Agents). At most one enabled
	// module may set it; Compose calls it once the operations dispatcher exists
	// (the provider's Deps carries it when the provider is one of the modules that
	// receive it) and puts the result on every module's Deps. Workers never calls it.
	AgentColleagues func(Deps) contracts.AgentColleagues
}
