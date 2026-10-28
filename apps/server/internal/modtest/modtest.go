// Package modtest is the integration harness the business modules' test
// packages mount their module through: one migrated database per harness, the
// real server.New stack (forwarded-header trust, security headers, host
// filter, CrossOriginProtection, base path) over module.Compose with identity
// beside the module under test, a settable clock, and a client whose every
// exchange is validated against the module's own contract.
//
// It is an ordinary package rather than a _test one because three module test
// packages (customers, products, energy) import it, and a _test package cannot
// be imported. identity keeps its own harness_test.go, which this is modelled
// on and does not replace: identity's harness knows identity's endpoints,
// cookies and ceremonies, none of which belong here.
//
// A module's test package wires it up once and then writes one-line harness
// calls:
//
//	var recorder = contracttest.New(loadContract())
//
//	func newHarness(t *testing.T, opts ...modtest.Option) *modtest.Harness {
//		return modtest.New(t, append([]modtest.Option{
//			modtest.WithRecorder(recorder),
//			modtest.WithModule(customers.Module()),
//		}, opts...)...)
//	}
//
// The recorder belongs to the calling package, not to the harness: each
// module's coverage gate is over its own contract, and every harness a package
// builds must feed the one Recorder its TestMain turns into that gate.
//
// Every helper that can fail takes the calling test's t and reports to it,
// never to the test that built the harness, so parallel subtests can share one
// harness.
package modtest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/health"
	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/openapi/contracttest"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
	"github.com/vantigo-io/vantigo/server/internal/secrets"
	"github.com/vantigo-io/vantigo/server/internal/server"
	"github.com/vantigo-io/vantigo/server/internal/storage"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
	"github.com/vantigo-io/vantigo/server/internal/web"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// Start is where every harness clock begins. A module reads time only through
// Deps.Clock, so a window a test needs to cross is crossed with Advance, never
// by sleeping.
var Start = time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)

// harnessOrigin is the installation's APP_URL. It is a domain rather than the
// listener's 127.0.0.1 so the host filter and CrossOriginProtection see a real
// origin; httptest's client dials the listener whatever the host is.
const harnessOrigin = "http://modules.example.com"

// sessionCookieName is identity's session cookie (internal/identity/cookies.go).
// The constant is unexported there and only identity's own tests can reach the
// export_test.go alias, so it is repeated here. A drift is loud rather than
// silent: every signed-in request in every module would answer 401.
const sessionCookieName = "vantigo.session"

const indexHTML = `<!doctype html><html><head><title>Vantigo</title>` +
	`<script type="module" src="/assets/app-1.js"></script></head><body><div id="root"></div></body></html>`

// Harness is one installation for one test: its own migrated database, the
// real server stack over module.Compose(identity, the modules under test), a
// settable clock and a captured log. Nothing mutable is shared between
// harnesses except the caller's Recorder, which is safe for concurrent use, so
// tests using one can run in parallel.
type Harness struct {
	pool     *pgxpool.Pool
	deps     module.Deps
	recorder *contracttest.Recorder
	srv      *httptest.Server
	url      string // the installation's origin, APP_URL
	base     *url.URL
	log      *syncBuffer
	// mods is what Compose was given: identity (with WithAccounts' or
	// WithoutAccounts' change) and the modules under test, from which
	// Workers resolves the background workers as worker mode does.
	mods []module.Module

	mu    sync.Mutex
	clock time.Time

	clients atomic.Uint32
	users   atomic.Uint32
	roles   atomic.Uint32

	// notices is every Notify a composed module made (Notices).
	notices *noticeRecorder
}

// setup is what Options adjust: the environment the harness loads its
// configuration from, the modules it composes beside identity, and the
// Recorder its clients validate through.
type setup struct {
	env             map[string]string
	modules         []module.Module
	recorder        *contracttest.Recorder
	transport       http.RoundTripper
	backoff         func(int) time.Duration
	directory       contracts.CustomerDirectory
	products        contracts.ProductCatalog
	projects        contracts.ProjectDirectory
	actuals         contracts.ProjectActuals
	timesheets      contracts.Timesheets
	expenses        contracts.ProjectExpenses
	receivables     contracts.CustomerReceivables
	holders         []contracts.CustomerReferenceHolder
	personalData    []contracts.CustomerPersonalDataHolder
	invoicedWork    []contracts.InvoicedWorkHolder
	calendarSources []contracts.CalendarSource
	provisioners    []contracts.IdentityProvisioner
	workforce       contracts.Workforce
	mailboxCalendar contracts.MailboxCalendar
	calendarBusy    contracts.CalendarBusy
	hours           contracts.BillableHours
	billExpenses    contracts.BillableExpenses
	milestones      contracts.BillableMilestones
	consumption     contracts.ConsumptionProvider
	smtpVerify      func(ctx context.Context, cfg config.MailConfig) error
	smtpSend        func(ctx context.Context, cfg config.MailConfig, msg mail.Outbound) error
	objectStore     storage.ObjectStore
	peppolLookup    func(ctx context.Context, participant string) (peppol.Result, error)
	poolMaxConns    int32
	accounts        contracts.AccountProvisioning
	noAccounts      bool
	wrapUsers       func(contracts.UserDirectory) contracts.UserDirectory
}

// Option adjusts a harness before it is built.
type Option func(*setup)

// WithModule composes m beside identity. It may be given more than once, for a
// module under test that reads another through Deps.Directory.
func WithModule(m module.Module) Option {
	return func(s *setup) { s.modules = append(s.modules, m) }
}

// WithRecorder routes every client's exchanges through rec, the calling
// package's Recorder over that package's own contract.
func WithRecorder(rec *contracttest.Recorder) Option {
	return func(s *setup) { s.recorder = rec }
}

// WithTransport sets the RoundTripper Deps.HTTPTransport carries, for a
// module whose own outbound HTTP client (customers' Brreg lookup) must never
// touch the network in a test: rt stands in for the real transport, the same
// role .NET's StubBrregHandler played over CustomersApiFactory. Unset, a
// module falls back to its own production default.
func WithTransport(rt http.RoundTripper) Option {
	return func(s *setup) { s.transport = rt }
}

// WithBackoff sets the function Deps.HTTPBackoff carries, for a module whose
// own outbound HTTP client retries with a backoff: fn stands in for that
// client's real backoff, typically a function returning 0 so a test
// exercising several retries never actually sleeps. Unset, a module falls
// back to its own production default.
func WithBackoff(fn func(attempt int) time.Duration) Option {
	return func(s *setup) { s.backoff = fn }
}

// WithDirectory sets Deps.Directory directly to d, for a module under test
// that reads another module's data through contracts.CustomerDirectory
// (energy and time, so far) without composing that other module beside it —
// depguard forbids the module's own test package from importing the module
// that would otherwise provide one (customers), so the test builds its own
// fake directly against the contracts interface, the same seam .NET's
// FakeCustomerDirectory fills in EnergyApiFactory. module.Compose only ever
// overwrites Deps.Directory when one of the composed modules declares
// Module.Directory (none of their harnesses do), so a value set here
// survives Compose unchanged.
func WithDirectory(d contracts.CustomerDirectory) Option {
	return func(s *setup) { s.directory = d }
}

// WithProducts sets Deps.Products directly to p, for a module under test
// that reads the product catalog (contracts.ProductCatalog) without
// composing products beside it — depguard forbids the module's own test
// package from importing products directly, the same reason WithDirectory
// gives a module reading contracts.CustomerDirectory without composing
// customers. module.Compose only ever overwrites Deps.Products when one of
// the composed modules declares Module.Products, so a value set here
// survives Compose unchanged.
func WithProducts(p contracts.ProductCatalog) Option {
	return func(s *setup) { s.products = p }
}

// WithProjects sets Deps.Projects directly to p, for a module under test
// that reads projects through contracts.ProjectDirectory (time, so far)
// without composing projects beside it — depguard forbids the module's own
// test package from importing projects, the same reason WithDirectory and
// WithProducts give. module.Compose only ever overwrites Deps.Projects when
// one of the composed modules declares Module.Projects, so a value set here
// survives Compose unchanged.
//
// It also lists "projects" in the harness's MODULES (see modulesEnv): a
// module that requires projects is only valid configuration beside it, and
// the fake is what stands in for it here.
func WithProjects(p contracts.ProjectDirectory) Option {
	return func(s *setup) { s.projects = p }
}

// WithActuals sets Deps.Actuals directly to p, for a module under test that
// reads what has been logged against projects (contracts.ProjectActuals)
// without composing the module that owns the hours beside it — depguard
// forbids the module's own test package from importing time, the same reason
// WithProducts and WithProjects give. The fake decides what the module under
// test sees logged, which is the only way a test can ask for figures it
// chose. module.Compose only ever overwrites Deps.Actuals when one of the
// composed modules declares Module.Actuals, so a value set here survives
// Compose unchanged — exactly as WithProducts' catalog does, and for the same
// reason: a harness that stubs a contract does not compose the module that
// would provide it.
func WithActuals(p contracts.ProjectActuals) Option {
	return func(s *setup) { s.actuals = p }
}

// WithTimesheets sets Deps.Timesheets directly to p, for a module under test
// that reads a user's hours in a week (contracts.Timesheets) without
// composing time beside it, for WithActuals' reason. module.Compose only ever
// overwrites Deps.Timesheets when one of the composed modules declares
// Module.Timesheets, so a value set here survives Compose unchanged. Leaving
// it out is the installation without time: the slot stays nil, which a
// consumer reads as "time tracking is off".
func WithTimesheets(p contracts.Timesheets) Option {
	return func(s *setup) { s.timesheets = p }
}

// WithExpenses sets Deps.Expenses directly to p, for a module under test that
// reads what a project's expenses cost and bill (contracts.ProjectExpenses)
// without composing the module that owns the expenses beside it — depguard
// forbids the module's own test package from importing expenses, the same
// reason WithProducts, WithProjects and WithActuals give. The fake decides
// what the module under test sees recorded, which is the only way a test can
// ask for figures it chose. module.Compose only ever overwrites
// Deps.Expenses when one of the composed modules declares Module.Expenses, so
// a value set here survives Compose unchanged, exactly as WithActuals'
// provider does.
//
// Leaving it out is the installation without expenses: Deps.Expenses stays
// nil, which is the case a consumer must handle as "expense tracking is off"
// rather than as zeroes.
func WithExpenses(p contracts.ProjectExpenses) Option {
	return func(s *setup) { s.expenses = p }
}

// WithReceivables sets Deps.Receivables directly to p, for a module under
// test that reads what a customer was invoiced and owes
// (contracts.CustomerReceivables) without composing the module that owns the
// sales documents beside it — depguard forbids customers' own test package
// from importing invoices, the same reason WithExpenses gives. The fake
// decides what the module under test sees issued and open. module.Compose
// only ever overwrites Deps.Receivables when one of the composed modules
// declares Module.Receivables, so a value set here survives Compose
// unchanged.
//
// Leaving it out is the installation without invoices: Deps.Receivables stays
// nil, which is the case a consumer must handle as "invoicing is off" rather
// than as zeroes.
func WithReceivables(p contracts.CustomerReceivables) Option {
	return func(s *setup) { s.receivables = p }
}

// WithCustomerReferenceHolders adds holders to Deps.CustomerReferenceHolders,
// for the module that merges customers (customers merge design D1) to be
// tested against fakes that record what they were asked to re-point — or
// fail, to prove a merge rolls back — without composing projects, energy or
// communications beside it: depguard keeps those out of customers' tests.
// module.Compose appends the composed modules' own holders after these, so a
// value set here survives Compose; given more than once, the holders
// accumulate in order.
func WithCustomerReferenceHolders(holders ...contracts.CustomerReferenceHolder) Option {
	return func(s *setup) { s.holders = append(s.holders, holders...) }
}

// WithCustomerPersonalData adds holders to Deps.CustomerPersonalData, for the
// module that exports and anonymises a private person (customers GDPR design
// D2) to be tested against fakes that record what they were asked, answer a
// section, or fail — to prove an anonymisation rolls back — without composing
// communications, energy or projects beside it: depguard keeps those out of
// customers' tests. module.Compose and module.Workers append the given
// modules' own after these, so a value set here survives both; given more than
// once, the holders accumulate in order.
func WithCustomerPersonalData(holders ...contracts.CustomerPersonalDataHolder) Option {
	return func(s *setup) { s.personalData = append(s.personalData, holders...) }
}

// WithInvoicedWork adds holders to Deps.InvoicedWork, for the module that
// issues invoices (rule 10) to be tested against fakes that record what they
// were asked to stamp or release, and on which transaction — or refuse, to
// prove an issue rolls back — without composing time, expenses or projects
// beside it: depguard keeps those out of invoices' tests. module.Compose and
// module.Workers append the given modules' own after these, so a value set
// here survives both; given more than once, the holders accumulate in order.
func WithInvoicedWork(holders ...contracts.InvoicedWorkHolder) Option {
	return func(s *setup) { s.invoicedWork = append(s.invoicedWork, holders...) }
}

// WithCalendarSources adds sources to Deps.CalendarSources, for the calendar
// module to be tested against fake providers — kinds and items a test sets,
// or a failure — without composing the five modules that provide the slot:
// depguard keeps those out of calendar's tests. module.Compose appends the
// enabled modules' own after these (calendar design D1), so a value set here
// survives; given more than once, the sources accumulate in order.
func WithCalendarSources(sources ...contracts.CalendarSource) Option {
	return func(s *setup) { s.calendarSources = append(s.calendarSources, sources...) }
}

// WithIdentityProvisioners adds providers to Deps.IdentityProvisioners, for
// Staff to be tested against a fake external identity provider
// (FakeIdentityProvisioner) — its accounts, its status and its refusals set
// by the test — before any provider module exists. module.Compose and
// module.Workers append the enabled modules' own after these (Entra design
// D6), so a value set here survives both and reaches the accounts worker;
// given more than once, the providers accumulate in order, and two with one
// Key fail the composition.
func WithIdentityProvisioners(providers ...contracts.IdentityProvisioner) Option {
	return func(s *setup) { s.provisioners = append(s.provisioners, providers...) }
}

// WithWorkforce sets Deps.Workforce, for a module that reads who holds what
// (contracts.Workforce, staff competence design S15) to be tested against a
// fake register without composing Staff beside it. module.Compose and
// module.Workers keep a value set here when no enabled module provides the
// slot; a composed Staff's own replaces it.
//
// It also lists "staff" in the harness's MODULES (see modulesEnv), as
// WithProjects lists "projects": config.go rejects "planning" without
// "staff", and the fake is what stands in for it here.
func WithWorkforce(w contracts.Workforce) Option {
	return func(s *setup) { s.workforce = w }
}

// WithMailboxCalendar sets Deps.MailboxCalendar, for the calendar to be
// tested against a fake mailbox provider (FakeMailboxCalendar) — its
// mailboxes, its status and its refusals set by the test — without composing
// Entra beside it: depguard keeps it out of calendar's tests. module.Compose
// and module.Workers keep a value set here when no enabled module provides
// the slot (calendar Outlook sync design O4); a composed Entra's own
// replaces it.
func WithMailboxCalendar(m contracts.MailboxCalendar) Option {
	return func(s *setup) { s.mailboxCalendar = m }
}

// WithCalendarBusy sets Deps.CalendarBusy, for a module that reads the
// calendar's busy time (Planning's board) to be tested against a fake
// (FakeCalendarBusy) without composing the calendar beside it. module.Compose
// keeps a value set here when no enabled module provides the slot (calendar
// Outlook sync design O9.8); a composed calendar's own replaces it.
func WithCalendarBusy(b contracts.CalendarBusy) Option {
	return func(s *setup) { s.calendarBusy = b }
}

// WithBillableHours sets Deps.BillableHours directly to p, for a module under
// test that reads time's billable entries (contracts.BillableHours) without
// composing time beside it, for WithActuals' reason. module.Compose only ever
// overwrites Deps.BillableHours when one of the composed modules declares
// Module.BillableHours, so a value set here survives Compose unchanged.
// Leaving it out is the installation without time: the slot stays nil.
func WithBillableHours(p contracts.BillableHours) Option {
	return func(s *setup) { s.hours = p }
}

// WithBillableExpenses is WithBillableHours for expenses' billable lines
// (contracts.BillableExpenses).
func WithBillableExpenses(p contracts.BillableExpenses) Option {
	return func(s *setup) { s.billExpenses = p }
}

// WithBillableMilestones is WithBillableHours for projects' billing
// milestones (contracts.BillableMilestones).
func WithBillableMilestones(p contracts.BillableMilestones) Option {
	return func(s *setup) { s.milestones = p }
}

// WithConsumption is WithBillableHours for energy's settlements
// (contracts.ConsumptionProvider), which invoices builds lines from.
func WithConsumption(p contracts.ConsumptionProvider) Option {
	return func(s *setup) { s.consumption = p }
}

// WithSMTPVerify sets the function Deps.SMTPVerify carries, for a module
// whose own SMTP connectivity check (communications' channel verification)
// must succeed in a test without a live SMTP server or the production
// destination guard's network reach: fn stands in for mail.VerifyConnection
// itself, typically a function that records its arguments and returns nil.
// Unset, a module falls back to mail.VerifyConnection, the same guarded
// path production uses.
func WithSMTPVerify(fn func(ctx context.Context, cfg config.MailConfig) error) Option {
	return func(s *setup) { s.smtpVerify = fn }
}

// WithSMTPSend sets the function Deps.SMTPSend carries, for a module whose
// own outbound mail path (communications' outbox delivery worker) must send
// in a test without a live SMTP server: fn stands in for mail.SendOutbound
// itself, typically a function that records the envelope it was handed and
// returns nil — or returns an error, to drive the retry and terminal-failure
// paths deterministically. Unset, a module falls back to mail.SendOutbound,
// the same guarded path production uses.
func WithSMTPSend(fn func(ctx context.Context, cfg config.MailConfig, msg mail.Outbound) error) Option {
	return func(s *setup) { s.smtpSend = fn }
}

// WithObjectStore sets Deps.ObjectStore directly to store, for a module
// under test whose attachment paths (communications' staging and download)
// must exercise a storage failure deterministically — a fake whose Put or
// Get always errors — without depending on filesystem permission behaviour,
// which is unreliable when a test happens to run as root. Unset, a module
// falls back to building its own store from Config.StorageProvider, the
// same production path a harness configured with WithEnv("STORAGE_PROVIDER",
// "fs") and friends exercises for real.
func WithObjectStore(store storage.ObjectStore) Option {
	return func(s *setup) { s.objectStore = store }
}

// WithPeppolLookup sets the function Deps.PeppolLookup carries, for a
// module whose own Peppol capability check (customers' POST
// .../peppol-lookup) must exercise a lookup's outcomes — registered, not
// registered, upstream failure — deterministically, without a live Peppol
// network: fn stands in for a *peppol.Client's own Lookup, typically a
// function that records the participant it was asked about and returns a
// fixed peppol.Result or error. Unset, a module falls back to building its
// own *peppol.Client from configuration, the same production path a harness
// configured with PEPPOL_LOOKUP_ENABLED and friends (WithEnv) exercises for
// real — except that path still resolves real DNS and SMP, so a test that
// wants the disabled (503) path uses WithEnv alone and a test that wants a
// successful or failed lookup uses this instead.
func WithPeppolLookup(fn func(ctx context.Context, participant string) (peppol.Result, error)) Option {
	return func(s *setup) { s.peppolLookup = fn }
}

// WithAccounts makes p the installation's contracts.AccountProvisioning, for
// a module under test (staff) whose subject is what it does with identity's
// answers — a Staff-managed user, a mode mismatch, a provider that fails —
// rather than identity's own rules, which identity's tests prove. Identity
// is always composed and always provides Accounts, and Compose would
// overwrite a preset Deps.Accounts, so the option replaces identity's
// Module.Accounts in the composition instead: p is what Compose resolves the
// slot to, for every module, and identity's own provider is not built.
// Unset, the slot is identity's real provider.
func WithAccounts(p contracts.AccountProvisioning) Option {
	return func(s *setup) { s.accounts = p }
}

// WithoutAccounts composes identity without its Module.Accounts, so
// Deps.Accounts is nil for every module: an installation where no module
// provides account provisioning, which a consumer answers as "accounts are
// unavailable". It wins over WithAccounts.
func WithoutAccounts() Option {
	return func(s *setup) { s.noAccounts = true }
}

// WithUsers wraps identity's contracts.UserDirectory in wrap before Compose
// hands it to every module: a test whose subject is what a module does when
// the directory fails or answers something identity's tables cannot be made
// to (a lookup that errors), every other method still identity's own.
// Identity always provides Users and Compose would overwrite a preset
// Deps.Users, so the option wraps identity's Module.Users in the composition.
func WithUsers(wrap func(contracts.UserDirectory) contracts.UserDirectory) Option {
	return func(s *setup) { s.wrapUsers = wrap }
}

// WithPoolMaxConns builds the installation's pool at n connections on the
// harness's migrated database (testdb.Migrated's URL, db.WithMaxConns) in
// place of the shared default size, for a test that must starve when
// anything takes a connection it should not: the integration races run two
// writers on a pool of two, so a call that takes a second pool connection
// while its transaction holds locks waits for ever — to the test's deadline
// — instead of passing on a larger pool (invoices work design D1). Every raw
// lock-holder and probe of such a test opens its own pgx.Connect, outside
// the pool. The default pool testdb.Migrated opened is closed at once, so
// the installation holds no connection but the pool's.
func WithPoolMaxConns(n int32) Option {
	return func(s *setup) { s.poolMaxConns = n }
}

// WithEnv sets one additional environment variable a harness loads its
// configuration from, merged over the harness's own defaults (APP_ENV,
// DATABASE_URL, ...). For a setting modtest itself has no dedicated Option
// for — communications' STORAGE_PROVIDER/STORAGE_FS_ROOT, so far the only
// user.
func WithEnv(key, value string) Option {
	return func(s *setup) { s.env[key] = value }
}

// modulesEnv returns the MODULES value a harness composing mods should set,
// rather than leaving MODULES unset and falling back to config's own default
// (customers, products, energy): that default is a production choice —
// communications, for one, is deliberately not on it yet (config.go's
// defaultModules comment) — and a harness's job is to mount whatever module
// it was asked to test, not whatever happens to ship enabled today. Without
// this, a module absent from the production default would compose silently
// short of every route it declares, and every test built on this harness
// would see 404s that look like a routing bug rather than what they
// actually are: enabledModules filtering the module out.
//
// "customers" is always included alongside whatever mods names: config.go's
// modules() rejects "energy", "communications" or "projects" without it (all
// three read contracts.CustomerDirectory), so a harness testing any of them
// would otherwise fail configuration validation before Compose ever runs.
// Including it
// unconditionally costs nothing for a harness that did not ask for it and
// keeps this file from having to mirror config.go's dependency rule as new
// modules grow their own.
//
// "staff" is likewise included whenever "entra" is among mods: config.go
// rejects "entra" without "staff" (Staff's accounts worker is the provider's
// only caller), and entra's own tests drive the provider and its endpoints
// without Staff — listed as enabled, staff is mounted only when a harness
// composes it too.
//
// stubbed names the modules a harness stands in for with a fake contract
// rather than composing — "projects" when WithProjects was given, because
// config.go rejects "time" without "projects" and a time harness supplies a
// fake contracts.ProjectDirectory instead of the module, and "staff" when
// WithWorkforce was given, because it rejects "planning" without "staff". A
// stubbed name is listed as enabled but never mounted: enabledModules only
// ever filters the modules actually passed to Compose. The result is sorted
// and comma-joined, with no duplicates even when "customers" is itself one
// of mods, a name repeats across more than one WithModule call, or a stubbed
// name is also composed.
func modulesEnv(mods []module.Module, stubbed ...string) string {
	moduleSet := map[string]bool{"customers": true}
	for _, m := range mods {
		moduleSet[m.Name] = true
	}
	if moduleSet["entra"] {
		moduleSet["staff"] = true
	}
	for _, name := range stubbed {
		moduleSet[name] = true
	}
	names := make([]string, 0, len(moduleSet))
	for name := range moduleSet {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// New builds a harness for t. It fails t when no Recorder or no module was
// given: a harness without either would run tests that prove nothing about the
// module or its contract.
func New(t *testing.T, opts ...Option) *Harness {
	t.Helper()
	pool, databaseURL := testdb.Migrated(t)

	// httptest's peer is always 127.0.0.1, the one trusted proxy, so each
	// client's X-Forwarded-For address becomes its httpx.ClientIP and every
	// IP-keyed limit is per client.
	env := map[string]string{
		"APP_ENV":             "development",
		"DATABASE_URL":        databaseURL,
		"APP_URL":             harnessOrigin,
		"APP_SECRET":          "modtest-harness-app-secret-0123456789abcdef",
		"TRUSTED_PROXY_HOPS":  "1",
		"TRUSTED_PROXY_CIDRS": "127.0.0.1/32",
	}
	s := &setup{env: env}
	for _, o := range opts {
		o(s)
	}
	if s.recorder == nil {
		t.Fatal("modtest: no recorder; pass modtest.WithRecorder(rec) so every exchange is validated against the module's contract")
	}
	if len(s.modules) == 0 {
		t.Fatal("modtest: no module under test; pass modtest.WithModule(m)")
	}

	if s.poolMaxConns > 0 {
		pool.Close()
		sized, err := db.Open(context.Background(), databaseURL, db.WithMaxConns(s.poolMaxConns))
		if err != nil {
			t.Fatalf("modtest: open a pool of %d: %v", s.poolMaxConns, err)
		}
		t.Cleanup(sized.Close)
		pool = sized
	}

	var stubbed []string
	if s.projects != nil {
		stubbed = append(stubbed, "projects")
	}
	if s.workforce != nil {
		stubbed = append(stubbed, "staff")
	}
	env["MODULES"] = modulesEnv(s.modules, stubbed...)

	cfg, err := config.Load(env)
	if err != nil {
		t.Fatalf("modtest: %v", err)
	}
	box, err := secrets.New(cfg.AppSecret)
	if err != nil {
		t.Fatalf("modtest: %v", err)
	}

	h := &Harness{pool: pool, recorder: s.recorder, clock: Start, log: &syncBuffer{}, notices: &noticeRecorder{}}
	logger := slog.New(slog.NewJSONHandler(h.log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	// The limiter runs on the harness clock too, so a throttle window turns
	// over when a test advances the clock and never mid-test on the wall clock.
	h.deps = module.Deps{
		Config:                   cfg,
		Pool:                     pool,
		Logger:                   logger,
		Clock:                    h.Now,
		Mail:                     &mail.Fake{},
		Secrets:                  box,
		Limiter:                  ratelimit.NewWithClock(pool, h.Now),
		HTTPTransport:            s.transport,
		HTTPBackoff:              s.backoff,
		Directory:                s.directory,
		Products:                 s.products,
		Projects:                 s.projects,
		Actuals:                  s.actuals,
		Timesheets:               s.timesheets,
		Expenses:                 s.expenses,
		Receivables:              s.receivables,
		CustomerReferenceHolders: s.holders,
		CustomerPersonalData:     s.personalData,
		InvoicedWork:             s.invoicedWork,
		CalendarSources:          s.calendarSources,
		IdentityProvisioners:     s.provisioners,
		Workforce:                s.workforce,
		MailboxCalendar:          s.mailboxCalendar,
		CalendarBusy:             s.calendarBusy,
		BillableHours:            s.hours,
		BillableExpenses:         s.billExpenses,
		BillableMilestones:       s.milestones,
		Consumption:              s.consumption,
		SMTPVerify:               s.smtpVerify,
		SMTPSend:                 s.smtpSend,
		ObjectStore:              s.objectStore,
		PeppolLookup:             s.peppolLookup,
	}
	access := identity.NewAccess(h.deps)
	h.deps.Access = access

	// Identity is mounted beside the module under test so its access layer is
	// the real one: a signed-in principal, its roles and its permissions are
	// resolved from identity's tables on every request, as in production.
	identityModule := identity.Module(access)
	switch {
	case s.noAccounts:
		identityModule.Accounts = nil
	case s.accounts != nil:
		identityModule.Accounts = func(module.Deps) contracts.AccountProvisioning { return s.accounts }
	}
	if s.wrapUsers != nil && identityModule.Users != nil {
		users := identityModule.Users
		identityModule.Users = func(d module.Deps) contracts.UserDirectory { return s.wrapUsers(users(d)) }
	}
	// Every Notify is recorded on its way through, outermost, so Notices
	// sees what a module asked for even when WithUsers' wrapping refuses it.
	if identityModule.Users != nil {
		users := identityModule.Users
		identityModule.Users = func(d module.Deps) contracts.UserDirectory {
			return recordingUsers{UserDirectory: users(d), notices: h.notices}
		}
	}
	// The business calendar is preset on the harness's own Deps, so a test
	// that builds a worker from Deps() directly — the way the retention and
	// accounts tests build one on a pool of their own — has the calendar
	// identity would give it; Compose and Workers resolve their own copies
	// from the module regardless.
	if identityModule.Calendar != nil {
		h.deps.Calendar = identityModule.Calendar(h.deps)
	}
	// The user directory likewise, so a provider built from Deps() directly
	// — the calendar's CalendarBusy in its tests — reads users as composed.
	if identityModule.Users != nil {
		h.deps.Users = identityModule.Users(h.deps)
	}
	mods := append([]module.Module{identityModule}, s.modules...)
	h.mods = mods
	api, err := module.Compose(h.deps, mods...)
	if err != nil {
		t.Fatalf("modtest: compose: %v", err)
	}

	assets := fstest.MapFS{
		"index.html":      {Data: []byte(indexHTML)},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
	}
	index, err := web.NewIndex(assets, cfg.BasePath, cfg.Branding, cfg.Modules)
	if err != nil {
		t.Fatalf("modtest: %v", err)
	}
	srv := httptest.NewUnstartedServer(server.New(server.Options{
		Config: cfg,
		Logger: logger,
		Index:  index,
		Assets: assets,
		Health: health.Handler(logger, "test"),
		API:    api,
	}))
	srv.Start()
	t.Cleanup(srv.Close)
	h.srv = srv
	h.url = harnessOrigin
	h.base, err = url.Parse(harnessOrigin)
	if err != nil {
		t.Fatalf("modtest: %v", err)
	}
	return h
}

// Logs is everything the installation has written to Deps.Logger so far, as
// the JSON lines slog produced. It is how a test asserts a decision the
// response cannot show: a dependency that failed, was deliberately swallowed
// so the rest of the answer survived, and was supposed to say so in the log.
// A test that only wants "nothing was said about X" reads it the same way.
func (h *Harness) Logs() string { return string(h.log.Bytes()) }

// Notices is every contracts.UserDirectory.Notify call the composed modules
// have made so far, in call order, whether or not identity then mailed
// anyone: how a test asserts whom a module told and what it said, without the
// address, which identity alone holds. Identity still delivers each through
// Deps.Mail, as in production.
func (h *Harness) Notices() []Notice { return h.notices.all() }

// Now is the harness clock, the installation's Deps.Clock.
func (h *Harness) Now() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.clock
}

// Advance moves the harness clock forward by d.
func (h *Harness) Advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clock = h.clock.Add(d)
}

// Workers is the background workers of the composed modules, built from d
// as worker mode builds them (module.Workers): the single-provider slots —
// Users, and Accounts as WithAccounts or WithoutAccounts left identity's —
// resolved onto d, which Deps alone does not carry (Compose resolves them
// per module). A test passes Deps, or Deps with a pool of its own.
func (h *Harness) Workers(d module.Deps) []worker.Worker { return module.Workers(d, h.mods...) }

// Deps are the dependencies the installation was composed with, for a test
// that builds a module-level collaborator the way Compose does — a module's
// Directory, say.
func (h *Harness) Deps() module.Deps { return h.deps }

// DepsOn is Deps with pool in place of the installation's, and the
// single-provider slots identity fills rebuilt on that pool — the business
// calendar today — the way worker mode builds them on its own pool. A test
// that starves a worker on a pool of one uses this rather than replacing
// Deps().Pool by hand, or the calendar would keep reading through the
// harness's own pool and the starvation it is there to catch would never
// happen.
func (h *Harness) DepsOn(pool *pgxpool.Pool) module.Deps {
	d := h.deps
	d.Pool = pool
	if len(h.mods) > 0 && h.mods[0].Calendar != nil {
		d.Calendar = h.mods[0].Calendar(d)
	}
	return d
}

// Pool is the harness's own pool on the installation's database, for a fixture
// that Exec, Count and One cannot express.
func (h *Harness) Pool() *pgxpool.Pool { return h.pool }

// fixtureTimeout bounds every fixture query. Without it a pool with no free
// connection blocks in Acquire for as long as the test binary runs, which
// turns an exhausted pool into a hung package instead of a failed test.
const fixtureTimeout = 15 * time.Second

// Exec runs one fixture statement.
func (h *Harness) Exec(t testing.TB, sql string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fixtureTimeout)
	defer cancel()
	if _, err := h.pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("modtest: %s: %v", sql, err)
	}
}

// SetBusinessTimeZone sets the workspace's business time zone (time zones
// design D2) as an Owner's PUT /identity/owner/workspace would, for a test
// whose subject is what a module does under another zone: the singleton
// system settings row is created when there is none, as identity's own
// EnsureSystemSettings does, else replaced. Every module reads the zone
// through contracts.BusinessCalendar on the next request.
func (h *Harness) SetBusinessTimeZone(t testing.TB, zone string) {
	t.Helper()
	h.Exec(t, `INSERT INTO identity.system_settings (id, maintenance_enabled, business_time_zone, updated_at)
VALUES (1, false, $1, $2)
ON CONFLICT (id) DO UPDATE SET business_time_zone = EXCLUDED.business_time_zone, updated_at = EXCLUDED.updated_at`,
		zone, h.Now())
}

// Count runs a query that answers one integer.
func (h *Harness) Count(t testing.TB, sql string, args ...any) int {
	t.Helper()
	return One[int](t, h, sql, args...)
}

// One runs a query that answers one value of type T, such as the id an INSERT
// ... RETURNING hands back.
func One[T any](t testing.TB, h *Harness, sql string, args ...any) T {
	t.Helper()
	var v T
	ctx, cancel := context.WithTimeout(context.Background(), fixtureTimeout)
	defer cancel()
	if err := h.pool.QueryRow(ctx, sql, args...).Scan(&v); err != nil {
		t.Fatalf("modtest: %s: %v", sql, err)
	}
	return v
}

// SignIn seeds a user who holds exactly the given permission keys and returns a
// client carrying that user's session cookie. It delegates to SignInUser,
// discarding the seeded user's ID, for the common case where the caller only
// needs the client.
func (h *Harness) SignIn(t testing.TB, permissions ...string) *Client {
	t.Helper()
	c, _ := h.SignInUser(t, permissions...)
	return c
}

// SignInUser is SignIn, but also returns the seeded user's ID — for a test
// that needs to name that user itself, such as the subject of a
// Deps.Users/Deps.Projects lookup or a role assignment recorded elsewhere by
// user ID.
//
// The keys are granted through a role of the user's own, which is how a real
// installation grants them: the access layer resolves them from identity's
// tables on every request.
//
// The rows are written directly rather than through identity's endpoints. A
// module's tests are about that module, and driving sign-in over HTTP would
// cost a bootstrap, a user creation and a login round trip per test without
// proving anything identity's own tests do not already prove.
func (h *Harness) SignInUser(t testing.TB, permissions ...string) (*Client, uuid.UUID) {
	t.Helper()
	userID := h.seedUser(t)
	if len(permissions) > 0 {
		roleID := h.seedRole(t, permissions)
		h.Exec(t, `INSERT INTO identity.user_roles (user_id, role_id) VALUES ($1, $2)`, userID, roleID)
	}
	c := h.Client(t)
	c.SetCookie(sessionCookieName, h.session(t, userID))
	return c, userID
}

// SignInDisabled is SignIn for a caller whose account is disabled: it
// delegates to SignIn for the actual seeded user, role and session, then
// flips identity.users.is_disabled for the user that session belongs to —
// found by re-deriving the session's token hash from the client's own
// cookie jar (sessionCookie, the same base64url-then-SHA-256 shape session()
// itself uses), rather than duplicating seedUser/seedRole/session here.
// Session lookup itself already excludes a disabled user's row, so the
// returned client answers 401 rather than reaching any permission check.
func (h *Harness) SignInDisabled(t testing.TB, permissions ...string) *Client {
	t.Helper()
	c := h.SignIn(t, permissions...)
	raw, ok := c.sessionCookie()
	if !ok {
		t.Fatalf("modtest: SignInDisabled: SignIn did not plant a session cookie")
	}
	token, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("modtest: SignInDisabled: decode session token: %v", err)
	}
	hash := sha256.Sum256(token)
	h.Exec(t, `UPDATE identity.users SET is_disabled = true
	           WHERE id = (SELECT user_id FROM identity.sessions WHERE token_hash = $1)`, hash[:])
	return c
}

// seedUser creates a user with no password and no roles: it signs in through
// the session this harness plants, never through a sign-in endpoint.
func (h *Harness) seedUser(t testing.TB) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email := fmt.Sprintf("modtest-user-%d-%s@example.test", h.users.Add(1), id)
	h.Exec(t, `INSERT INTO identity.users (id, email, normalized_email, display_name, version, created_at, updated_at)
	           VALUES ($1, $2, upper($2), $2, $3, $4, $4)`, id, email, uuid.New(), h.Now())
	return id
}

// seedRole creates a custom role holding keys and returns its id. Each SignIn
// gets its own role, so one test's grants can never widen another's.
func (h *Harness) seedRole(t testing.TB, keys []string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	name := fmt.Sprintf("modtest-role-%d", h.roles.Add(1))
	h.Exec(t, `INSERT INTO identity.roles (id, name, normalized_name, display_name, version, created_at, updated_at)
	           VALUES ($1, $2, upper($2), $2, $3, $4, $4)`, id, name, uuid.New(), h.Now())
	for _, key := range keys {
		h.Exec(t, `INSERT INTO identity.role_permissions (role_id, permission_key) VALUES ($1, $2)`, id, key)
	}
	return id
}

// session starts a session for userID at the harness clock and returns its
// cookie token. The token is the base64url text of 32 random bytes and the row
// stores their SHA-256, exactly as identity mints and stores one
// (internal/identity/tokens.go).
func (h *Harness) session(t testing.TB, userID uuid.UUID) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("modtest: %v", err)
	}
	hash := sha256.Sum256(raw)
	now := h.Now()
	h.Exec(t, `INSERT INTO identity.sessions (id, user_id, token_hash, persistent, created_at, last_seen_at)
	           VALUES ($1, $2, $3, false, $4, $4)`, uuid.New(), userID, hash[:], now)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// syncBuffer is a bytes.Buffer safe for the concurrent writes of a server's log.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}
