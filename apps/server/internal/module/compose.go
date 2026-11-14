package module

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/live"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
	"github.com/vantigo-io/vantigo/server/internal/operations"
)

// Compose mounts each module at /api/v1/<name>/, serves the combined
// contract of the modules at GET /api/openapi.json (RuleSession), and
// answers every other /api path with httpx.NotFound. It fails on a
// duplicate name, an invalid or duplicate permission, a Mount error, a path
// two modules both declare, a component two modules declare differently
// under the same name, two modules both declaring a customer directory, a
// user directory, a product catalog, a project directory, project actuals,
// project expenses, billable hours, billable expenses or billable milestones
// (naming both) — customer reference holders, customer personal-data providers
// and invoiced-work holders, the three many-provider contract slots, are
// collected from every module given instead, enabled or not, and calendar
// sources and identity provisioners, the fourth and the fifth, from the
// enabled modules only — a source kind two invoiced-work holders claim, a
// key two identity provisioners claim, an event handler name two handlers
// share (EventHandlers), a live topic kind that is malformed, not prefixed by
// its module's name or declared twice (LiveTopics), or a nil Deps.Config:
// enablement (which modules MODULES turns on) is meaningless without one, and
// every real caller already loads one before composing.
func Compose(deps Deps, mods ...Module) (http.Handler, error) {
	if deps.Config == nil {
		return nil, fmt.Errorf("module: Compose requires a non-nil Deps.Config to know which modules MODULES enables")
	}
	api, _, err := composeFrom(deps, embedded, mods...)
	return api, err
}

// ComposeAPI is Compose for the API process: it also answers the runners the
// enabled modules declare (Module.APIRunners), built after the operations
// dispatcher is bound, for the caller to start with StartRunners on the
// process's context. Compose, for a caller with no process of its own (a
// test of the router), drops them.
func ComposeAPI(deps Deps, mods ...Module) (http.Handler, []Runner, error) {
	if deps.Config == nil {
		return nil, nil, fmt.Errorf("module: ComposeAPI requires a non-nil Deps.Config to know which modules MODULES enables")
	}
	return composeFrom(deps, embedded, mods...)
}

// compose is Compose with the contract loader as a seam, so tests can
// compose in-memory contracts instead of the embedded specs. Nothing it
// loads is remembered: every call parses its contracts afresh.
func compose(deps Deps, load func(context.Context, string) (*openapi3.T, error), mods ...Module) (http.Handler, error) {
	api, _, err := composeFrom(deps, freshContracts(load), mods...)
	return api, err
}

// composeRunners is compose with the runners, for the tests of APIRunners.
func composeRunners(deps Deps, load func(context.Context, string) (*openapi3.T, error), mods ...Module) (http.Handler, []Runner, error) {
	return composeFrom(deps, freshContracts(load), mods...)
}

// contractSource is where composeFrom gets a module's own contract and the
// combined one from.
type contractSource interface {
	// doc is the contract handed to a module's Mount as Deps.Doc. It may be
	// shared between compositions, so nobody may change it.
	doc(ctx context.Context, name string) (*openapi3.T, error)
	// combined is the body of GET /api/openapi.json for these modules, in
	// this order.
	combined(ctx context.Context, order []string) ([]byte, error)
}

// freshContracts parses on every call and remembers nothing.
type freshContracts func(context.Context, string) (*openapi3.T, error)

func (load freshContracts) doc(ctx context.Context, name string) (*openapi3.T, error) {
	return load(ctx, name)
}

// combined gives mergeContract documents of its own: it internalises their
// references in place, so they cannot be the ones doc handed out.
func (load freshContracts) combined(ctx context.Context, order []string) ([]byte, error) {
	docs := make(map[string]*openapi3.T, len(order))
	for _, name := range order {
		doc, err := load(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("module: load %s contract: %w", name, err)
		}
		docs[name] = doc
	}
	contract, err := mergeContract(docs, order)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(contract)
	if err != nil {
		return nil, fmt.Errorf("module: marshal the combined contract: %w", err)
	}
	return body, nil
}

// embedded is the embedded specs, each parsed once per process and each
// combination of modules merged once per process.
//
// A server composes once, so this changes nothing for it. A test binary
// composes once per test — hundreds of times — and parsing seven modules'
// contracts twice over on every one of them was most of what the suite did:
// under the race detector on four cores it took the whole run from seventeen
// minutes to three. What makes sharing sound is that a contract is read-only
// once loaded (Deps.Doc says so) and that the race detector, which the same
// suite runs under, fails the run the moment anything writes to one.
var embedded = &rememberedContracts{load: openapi.Load}

type rememberedContracts struct {
	load   freshContracts
	docs   sync.Map // module name -> *openapi3.T
	bodies sync.Map // module names in order, NUL-joined -> []byte
}

func (c *rememberedContracts) doc(ctx context.Context, name string) (*openapi3.T, error) {
	if doc, ok := c.docs.Load(name); ok {
		return doc.(*openapi3.T), nil
	}
	doc, err := c.load(ctx, name)
	if err != nil {
		return nil, err
	}
	// Two first callers may both have parsed it; both get the one that won.
	kept, _ := c.docs.LoadOrStore(name, doc)
	return kept.(*openapi3.T), nil
}

func (c *rememberedContracts) combined(ctx context.Context, order []string) ([]byte, error) {
	// NUL cannot appear in a module name, so no two sets of modules share a key.
	key := strings.Join(order, "\x00")
	if body, ok := c.bodies.Load(key); ok {
		return body.([]byte), nil
	}
	// Merged from documents of its own (freshContracts.combined), never from
	// the shared ones doc hands out: merging rewrites what it is given.
	body, err := c.load.combined(ctx, order)
	if err != nil {
		return nil, err
	}
	kept, _ := c.bodies.LoadOrStore(key, body)
	return kept.([]byte), nil
}

func composeFrom(deps Deps, contractsFrom contractSource, mods ...Module) (http.Handler, []Runner, error) {
	ctx := context.Background()
	// Every module given, before enablement drops any: both customer slots
	// below — the reference holders and the personal-data providers — are
	// collected from all of them.
	given := mods
	mods = enabledModules(deps, mods)

	names := make(map[string]bool, len(mods))
	catalog := make(map[string]contracts.Permission)
	var aiTasks map[string]contracts.AITask
	for _, mod := range mods {
		if names[mod.Name] {
			return nil, nil, fmt.Errorf("module: duplicate module name %q", mod.Name)
		}
		names[mod.Name] = true

		for _, perm := range mod.Permissions {
			if err := contracts.ValidatePermission(mod.Name, perm); err != nil {
				return nil, nil, fmt.Errorf("module: %w", err)
			}
			if _, ok := catalog[perm.Key]; ok {
				return nil, nil, fmt.Errorf("module: duplicate permission %q", perm.Key)
			}
			catalog[perm.Key] = perm
		}

		// The AI tasks are composed beside the permissions: the same module
		// that declares what a person may do declares what it asks a model to
		// do, and a disabled module's vanish with its permissions.
		for _, task := range mod.AITasks {
			if err := contracts.ValidateAITask(mod.Name, task); err != nil {
				return nil, nil, fmt.Errorf("module: %w", err)
			}
			if _, ok := aiTasks[task.Key]; ok {
				return nil, nil, fmt.Errorf("module: duplicate AI task %q", task.Key)
			}
			if aiTasks == nil {
				aiTasks = make(map[string]contracts.AITask)
			}
			aiTasks[task.Key] = task
		}
	}
	deps.AITasks = aiTasks

	// The live topics' registry (live design L9) is on every module's Deps;
	// the topics themselves are collected after each Mount, below.
	if deps.Live == nil {
		deps.Live = live.NewRegistry()
	}

	// The customer directory, the user directory, the product catalog, the
	// project directory, project actuals, project expenses and customer
	// receivables are the sanctioned cross-module reads
	// (contracts.CustomerDirectory, UserDirectory, ProductCatalog,
	// ProjectDirectory, ProjectActuals, ProjectExpenses,
	// CustomerReceivables): at most one enabled module may declare each. Each is
	// resolved here, in this order, before any Mount runs, so its result can
	// be copied onto every module's Deps below — including its own
	// provider's, which may need it too — and so a later slot's provider
	// func may use an earlier one already set on deps (Projects, say, may
	// read deps.Directory). Each provider func runs on deps as Compose
	// itself received it plus whatever earlier slot just set, deliberately
	// narrower than the per-module copy Mount gets (no Doc, no per-module
	// Catalog reference beyond what is already built here): building a
	// directory is a data-layer concern (Pool, Config, Clock, Secrets, ...),
	// not a contract one, and no module's own Doc is loaded yet at this
	// point regardless.
	directoryProvider, err := soleProvider(mods, "a customer directory", func(m Module) bool { return m.Directory != nil })
	if err != nil {
		return nil, nil, err
	}
	if directoryProvider != nil {
		deps.Directory = directoryProvider.Directory(deps)
	}

	usersProvider, err := soleProvider(mods, "a user directory", func(m Module) bool { return m.Users != nil })
	if err != nil {
		return nil, nil, err
	}
	if usersProvider != nil {
		deps.Users = usersProvider.Users(deps)
	}
	// Presence names its members through the user directory.
	deps.Live.SetUsers(deps.Users)

	// The business calendar (contracts.BusinessCalendar) resolves right
	// after the user directory: identity provides both, and every module
	// judges its "today" by it.
	calendarProvider, err := soleProvider(mods, "a business calendar", func(m Module) bool { return m.Calendar != nil })
	if err != nil {
		return nil, nil, err
	}
	if calendarProvider != nil {
		deps.Calendar = calendarProvider.Calendar(deps)
	}

	// Account provisioning (contracts.AccountProvisioning) resolves right
	// after the user directory: it is identity's write port, staff →
	// identity, and its provider may read deps.Users while it is built.
	// Workers resolves both the same way, since worker mode never composes.
	accountsProvider, err := soleProvider(mods, "account provisioning", func(m Module) bool { return m.Accounts != nil })
	if err != nil {
		return nil, nil, err
	}
	if accountsProvider != nil {
		deps.Accounts = accountsProvider.Accounts(deps)
	}

	// What connecting users to the workforce provider reads of identity
	// (contracts.SSOConnections) resolves after the user directory, the
	// way Accounts does; worker mode has no use for it.
	ssoConnectionsProvider, err := soleProvider(mods, "SSO connections", func(m Module) bool { return m.SSOConnections != nil })
	if err != nil {
		return nil, nil, err
	}
	if ssoConnectionsProvider != nil {
		deps.SSOConnections = ssoConnectionsProvider.SSOConnections(deps)
	}

	// The product catalog, by the helper Workers uses too.
	if deps, err = withProducts(deps, mods); err != nil {
		return nil, nil, err
	}

	if deps, err = withProjects(deps, mods); err != nil {
		return nil, nil, err
	}

	// Actuals resolves after the project directory: the module that
	// owns logged work is built on the one that owns projects, so its
	// provider func may read deps.Projects — while it is built, never while
	// it serves (contracts.ProjectActuals).
	actualsProvider, err := soleProvider(mods, "project actuals", func(m Module) bool { return m.Actuals != nil })
	if err != nil {
		return nil, nil, err
	}
	if actualsProvider != nil {
		deps.Actuals = actualsProvider.Actuals(deps)
	}

	// Timesheets resolves right after actuals: the same module owns both,
	// and a consumer reads it with the request's context, never inside a
	// transaction (contracts.Timesheets).
	timesheetsProvider, err := soleProvider(mods, "timesheets", func(m Module) bool { return m.Timesheets != nil })
	if err != nil {
		return nil, nil, err
	}
	if timesheetsProvider != nil {
		deps.Timesheets = timesheetsProvider.Timesheets(deps)
	}

	// Project expenses resolves beside actuals, and for the same reason: the
	// module that owns expenses may read the one that owns projects while its
	// provider is built, never while it serves (contracts.ProjectExpenses).
	// The two are independent — an installation may have either, both or
	// neither.
	expensesProvider, err := soleProvider(mods, "project expenses", func(m Module) bool { return m.Expenses != nil })
	if err != nil {
		return nil, nil, err
	}
	if expensesProvider != nil {
		deps.Expenses = expensesProvider.Expenses(deps)
	}

	// Customer receivables (contracts.CustomerReceivables) resolves beside
	// expenses: the module that owns the sales documents reports what a
	// customer was invoiced and owes, and the module that owns customers
	// reads it. The provider needs nothing of Deps but the pool and the
	// clock; it never calls the customer directory while it serves.
	receivablesProvider, err := soleProvider(mods, "customer receivables", func(m Module) bool { return m.Receivables != nil })
	if err != nil {
		return nil, nil, err
	}
	if receivablesProvider != nil {
		deps.Receivables = receivablesProvider.Receivables(deps)
	}

	// Sales tax (contracts.SalesTax, orders design O8) resolves right after
	// receivables: the same module provides both, from its own tables only.
	if deps, err = withSalesTax(deps, mods); err != nil {
		return nil, nil, err
	}
	// The sales journal (contracts.SalesJournal, accounting design A12)
	// resolves right after sales tax: the same module provides it, from its
	// own tables only.
	if deps, err = withSalesJournal(deps, mods); err != nil {
		return nil, nil, err
	}

	// The three billable reads, consumption and billable orders resolve
	// beside actuals and expenses, after the project directory (withBillables
	// says why); Workers resolves them the same way.
	if deps, err = withBillables(deps, mods); err != nil {
		return nil, nil, err
	}

	// The workforce register (contracts.Workforce, staff competence design
	// S15) resolves after the business calendar, so its provider may read
	// deps.Calendar while it is built; Workers resolves it the same way.
	if deps, err = withWorkforce(deps, mods); err != nil {
		return nil, nil, err
	}

	// Customer reference holders are the first many-provider contract slot
	// (contracts.CustomerReferenceHolder, customers merge design D1): every
	// module given that declares one contributes it, in the order given, so
	// the merge that calls them does so in the same order on every run. A
	// module MODULES leaves out contributes its holder too, unlike every other
	// slot: every schema is migrated whatever MODULES says, so a module that
	// was on once and is off now still has rows naming customers, and a merge
	// that skipped them would leave them on the absorbed customer for good,
	// waiting for the module to come back. A holder needs only the caller's
	// transaction and its own schema, both there regardless. They are
	// resolved last, on deps as the single slots left it, and appended to
	// whatever the caller preset — onto a copy, so a harness's own slice is
	// never written through. The modules that provide invoiced work come last
	// (invoicedWorkLast): rule 10's lock order.
	var holders []contracts.CustomerReferenceHolder
	for _, mod := range invoicedWorkLast(given) {
		if mod.CustomerReferences == nil {
			continue
		}
		if holder := mod.CustomerReferences(deps); holder != nil {
			holders = append(holders, holder)
		}
	}
	if len(holders) > 0 {
		deps.CustomerReferenceHolders = append(slices.Clone(deps.CustomerReferenceHolders), holders...)
	}
	// The variant holders, collected from every module given as the customer
	// holders are (module-boundaries rule 8's reason: a disabled module's
	// schema still holds what it holds), appended onto a copy.
	var variantHolders []contracts.VariantReferenceHolder
	for _, mod := range given {
		if mod.VariantReferences == nil {
			continue
		}
		if holder := mod.VariantReferences(deps); holder != nil {
			variantHolders = append(variantHolders, holder)
		}
	}
	if len(variantHolders) > 0 {
		deps.VariantReferenceHolders = append(slices.Clone(deps.VariantReferenceHolders), variantHolders...)
	}

	// The stock read from the enabled modules, the stock ledger from every
	// module given (withStock says why), after the product catalog, by the
	// helper Workers uses too.
	if deps, err = withStock(deps, given, mods); err != nil {
		return nil, nil, err
	}

	// The outbox's event handlers (orders design O2), a many-provider slot
	// collected from every module given, for the holders' reason above. The
	// API runs none of them — the dispatcher, a worker, does — but a
	// duplicate name is refused here, at startup, all the same.
	if _, err := EventHandlers(deps, given...); err != nil {
		return nil, nil, err
	}

	// Customer personal data is the second many-provider slot (customers GDPR
	// design D2), collected from every module given, for the holders' reason
	// above, by the helper Workers uses too: the export reads it through a
	// Mount, the anonymisation worker through Workers.
	deps = withCustomerPersonalData(deps, given)

	// Invoiced work is the third many-provider slot (rule 10), collected from
	// every module given, for the holders' reason above, by the helper Workers
	// uses too: the issue reads it through a Mount, and a worker-mode invoices
	// server through Workers.
	if deps, err = withInvoicedWork(deps, given); err != nil {
		return nil, nil, err
	}

	// Calendar sources are the fourth many-provider slot (calendar design D1)
	// and, unlike the three above, are collected from the enabled modules
	// only, by the helper Workers uses too: a disabled module's gate could
	// only fail closed.
	deps = withCalendarSources(deps, mods)

	// Identity provisioners are the fifth many-provider slot (Entra design
	// D6), collected from the enabled modules only, as the calendar
	// sources, by the helper Workers uses too: Staff's accounts worker
	// drives them in worker mode as the API does.
	if deps, err = withIdentityProvisioners(deps, mods); err != nil {
		return nil, nil, err
	}

	// The mailbox calendars (contracts.MailboxCalendar, calendar Outlook
	// sync design O4), a many-provider slot keyed by Key (Google Workspace
	// design G2), collected from the enabled modules after the identity provisioners, by the
	// helper Workers uses too: the calendar's sync worker writes through
	// them in worker mode as the API does.
	if deps, err = withMailboxCalendars(deps, mods); err != nil {
		return nil, nil, err
	}

	// The channel mailboxes (contracts.ChannelMailbox, Outlook connection
	// design M11), keyed as the mailbox calendars, are collected after them, by the helper Workers
	// uses too: Communications' outbox sends through them in worker mode as
	// the API does.
	if deps, err = withChannelMailboxes(deps, mods); err != nil {
		return nil, nil, err
	}

	// The channel messengers (contracts.ChannelMessenger, Messenger design
	// N2), keyed as the channel mailboxes, are collected after them, by the
	// helper Workers uses too: Communications' outbox and messenger sync
	// run through them in worker mode as the API does. A key filled in both
	// slots is refused.
	if deps, err = withChannelMessengers(deps, mods); err != nil {
		return nil, nil, err
	}

	// The personal mailboxes (contracts.PersonalMailbox, Outlook connection
	// design M20), keyed as the mailbox calendars, are collected after the
	// channel mailboxes, by the helper Workers
	// uses too: Communications' outbox sends through them in worker mode as
	// the API does.
	if deps, err = withPersonalMailboxes(deps, mods); err != nil {
		return nil, nil, err
	}

	// The directory searches (contracts.DirectorySearch, Google Workspace
	// design G8), a many-provider slot keyed by Key, are collected from the
	// enabled modules after SSOConnections, which a provider holds. Workers
	// does not collect them: no worker searches a directory.
	if deps, err = withDirectorySearches(deps, mods); err != nil {
		return nil, nil, err
	}

	// The outbound mail (contracts.OutboundMail, integrations design T4)
	// resolves after the personal mailboxes, by the helper Workers uses too:
	// Invoices' reminder worker sends through it in worker mode as the API
	// does, and its provider, Communications, reads deps.ChannelMailboxes.
	if deps, err = withOutboundMail(deps, mods); err != nil {
		return nil, nil, err
	}

	// The model per AI task (contracts.AIModels) and the AI client built over it
	// resolve after every other slot, with Deps.AITasks already set: identity's
	// provider reads the tasks while it is built. Workers does not resolve
	// them, since no worker performs a task.
	if deps, err = withAI(deps, mods); err != nil {
		return nil, nil, err
	}

	// The assistant's grants (contracts.AssistantGrants, the Assistant design
	// S3) resolve after the AI slot. Workers does not resolve them.
	if deps, err = withAssistantGrants(deps, mods); err != nil {
		return nil, nil, err
	}

	// The agents' accounts and grants (contracts.AgentAccounts and
	// AgentGrants, virtual employees design V5 and V6) resolve beside them.
	// Workers does not resolve them.
	if deps, err = withAgentSlots(deps, mods); err != nil {
		return nil, nil, err
	}

	// The calendar's busy time (contracts.CalendarBusy, calendar Outlook sync
	// design O9.8) resolves last, after the mailbox calendar: its provider,
	// the calendar's server, reads the rest of Deps while it is built.
	// Workers does not resolve it — no worker reads it.
	busyProvider, err := soleProvider(mods, "calendar busy time", func(m Module) bool { return m.CalendarBusy != nil })
	if err != nil {
		return nil, nil, err
	}
	if busyProvider != nil {
		deps.CalendarBusy = busyProvider.CalendarBusy(deps)
	}

	// A contact's messaging channels (contracts.ContactChannels, Messenger
	// design N22) resolve after the channel messengers its provider,
	// Communications, reads. Workers does not resolve them — no worker reads
	// them.
	channelsProvider, err := soleProvider(mods, "contact channels", func(m Module) bool { return m.ContactChannels != nil })
	if err != nil {
		return nil, nil, err
	}
	if channelsProvider != nil {
		deps.ContactChannels = channelsProvider.ContactChannels(deps)
	}

	// What the virtual employees remember about a customer
	// (contracts.AgentMemory, virtual employees design V27) reads only its
	// provider's own schema. Workers does not resolve it — no worker reads it.
	memoryProvider, err := soleProvider(mods, "agent memory", func(m Module) bool { return m.AgentMemory != nil })
	if err != nil {
		return nil, nil, err
	}
	if memoryProvider != nil {
		deps.AgentMemory = memoryProvider.AgentMemory(deps)
	}

	// The operations dispatcher (module boundaries rule 18) exists before any
	// module mounts, so those modules can hold it, and is bound once the
	// router it dispatches through exists, below. Only the modules in
	// operationsModules (the assistant and the agents) are given it; no
	// other module's Deps carries it.
	var ops *operations.Dispatcher
	for name := range operationsModules {
		if names[name] {
			ops = operations.New()
		}
	}
	depsOf := make(map[string]Deps, len(mods))

	// The agents a person may talk to in the Assistant's panel
	// (contracts.AgentColleagues, virtual employees design V31) say what an agent
	// may do from the dispatcher's descriptions, so it resolves once the
	// dispatcher exists, and its provider is given the dispatcher when it is one
	// of the modules that receive it. Workers does not resolve it — no worker
	// reads it.
	colleaguesProvider, err := soleProvider(mods, "agent colleagues", func(m Module) bool { return m.AgentColleagues != nil })
	if err != nil {
		return nil, nil, err
	}
	if colleaguesProvider != nil {
		providerDeps := deps
		providerDeps.Operations = nil
		if operationsModules[colleaguesProvider.Name] {
			providerDeps.Operations = ops
		}
		deps.AgentColleagues = colleaguesProvider.AgentColleagues(providerDeps)
	}

	outer := http.NewServeMux()
	mounts := make([]moduleMount, 0, len(mods))
	order := make([]string, 0, len(mods))

	for _, mod := range mods {
		doc, err := contractsFrom.doc(ctx, mod.Name)
		if err != nil {
			return nil, nil, fmt.Errorf("module: load %s contract: %w", mod.Name, err)
		}

		modDeps := deps
		modDeps.Doc = doc
		modDeps.Catalog = catalog
		modDeps.Operations = nil
		if operationsModules[mod.Name] {
			modDeps.Operations = ops
		}
		depsOf[mod.Name] = modDeps
		handler, err := mod.Mount(modDeps)
		if err != nil {
			return nil, nil, fmt.Errorf("module: mount %s: %w", mod.Name, err)
		}
		if mod.LiveTopics != nil {
			if err := deps.Live.Register(mod.Name, mod.LiveTopics(modDeps)...); err != nil {
				return nil, nil, fmt.Errorf("module: %w", err)
			}
		}
		// A pattern ending in "/" matches the whole subtree and hands the
		// module's router the request path unchanged (no StripPrefix): the
		// module's own router matches full paths from its own Doc.
		outer.Handle("/api/v1/"+mod.Name+"/", handler)
		// The same subtree, matched again ahead of outer by dispatch below, so
		// http.ServeMux's path cleaning never folds a doubled slash inside a
		// module's subtree onto the real path (see moduleMount).
		mounts = append(mounts, moduleMount{prefix: "/api/v1/" + mod.Name + "/", handler: handler})
		// The subtree pattern alone does not cover the module's own root path,
		// so it is registered explicitly — whether or not the contract
		// declares it. A contract that declares "/api/v1/customers" (the
		// listing and its create) would otherwise have those requests
		// answered by http.ServeMux's automatic redirect to the
		// trailing-slash form: a path no contract declares, and one the
		// module's router would then answer 404. Registering the bare path
		// suppresses that redirect and delivers the request, path unchanged,
		// to the module.
		//
		// A module whose contract declares no bare root (energy) is
		// registered just the same, and for the same reason: the redirect is
		// wrong there too. It answered 307 with a text/html body and ran no
		// Access.Check at all, where every other undeclared API path answers
		// the 404 problem (internal/server's own /api contract). Handing the
		// path to the module instead lets its router answer that 404, as it
		// does for any other path its contract does not declare.
		outer.Handle("/api/v1/"+mod.Name, handler)
		mounts[len(mounts)-1].root = "/api/v1/" + mod.Name

		order = append(order, mod.Name)
	}

	body, err := contractsFrom.combined(ctx, order)
	if err != nil {
		return nil, nil, err
	}

	sessionRule := contracts.Rule{Kind: contracts.RuleSession}
	outer.HandleFunc("GET /api/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		if _, err := deps.Access.Check(r, sessionRule); err != nil {
			if errors.Is(err, contracts.ErrUnauthenticated) || errors.Is(err, contracts.ErrForbidden) {
				deps.Access.Reject(w, r, sessionRule, err)
				return
			}
			httpx.WriteError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})

	// Both the subtree and the bare "/api". Without the bare pattern,
	// http.ServeMux answers "/api" with its automatic redirect to "/api/" —
	// a 307 carrying a text/html body, and no Access.Check — where
	// internal/server's own contract (server.go's /api matcher) says every
	// unknown API path is the 404 problem.
	outer.HandleFunc("/api/", httpx.NotFound)
	outer.HandleFunc("/api", httpx.NotFound)

	api := dispatchModules(mounts, outer)
	if ops != nil {
		origin := ""
		if deps.Config != nil { // only a test composes without one
			origin = deps.Config.AppOrigin
		}
		if err := ops.Bind(body, catalog, deps.Access, api, operations.Options{Origin: origin, AgentGrants: deps.AgentGrants}); err != nil {
			return nil, nil, fmt.Errorf("module: bind the operations dispatcher: %w", err)
		}
	}
	// The API-process runners start from the dispatcher bound above, and only
	// the caller that owns the process context may start them (APIRunners).
	var runners []Runner
	for _, mod := range mods {
		if mod.APIRunners != nil {
			runners = append(runners, mod.APIRunners(depsOf[mod.Name])...)
		}
	}
	return api, runners, nil
}

// AssistantModule is the name of the module that receives Deps.Operations for
// the assistant principal.
const AssistantModule = "assistant"

// AgentsModule is the name of the other module that receives Deps.Operations,
// for the agent principal (virtual employees design V8).
const AgentsModule = "agents"

// operationsModules are the modules whose Deps carry the operations dispatcher
// (module boundaries rule 18): one per principal it calls as. Workers never
// carries it.
var operationsModules = map[string]bool{AssistantModule: true, AgentsModule: true}

// soleProvider returns the one module among mods for which declares reports
// true, nil when none does, and an error naming both when two do. what
// names the thing being provided ("a customer directory") for that error
// message, in the style Compose's own doc comment lists.
func soleProvider(mods []Module, what string, declares func(Module) bool) (*Module, error) {
	var provider *Module
	for i := range mods {
		if !declares(mods[i]) {
			continue
		}
		if provider != nil {
			return nil, fmt.Errorf("module: multiple modules declare %s: %q and %q", what, provider.Name, mods[i].Name)
		}
		provider = &mods[i]
	}
	return provider, nil
}

// moduleMount is one module's mounted handler and the paths that reach it:
// prefix is its subtree ("/api/v1/customers/"), root the bare path its own
// contract declares ("/api/v1/customers"), empty when it declares none.
type moduleMount struct {
	prefix  string
	root    string
	handler http.Handler
}

// dispatchModules matches each module's subtree itself, on the still-escaped
// path, before falling back to fallback (the http.ServeMux carrying
// /api/openapi.json, the /api catch-all, and the subtree patterns a module
// without its own root path still redirects through).
//
// The interception exists for one reason: http.ServeMux cleans the request
// path and redirects when the cleaned form differs, which folds
// "/api/v1/customers//contacts" onto the real "/api/v1/customers/contacts"
// and answers 307. .NET answered 404 there, and Router.matches deliberately
// refuses the empty segment a doubled slash produces so it falls through to
// the 404 problem — a guard the outer mux's redirect was reaching around
// before the module's router ever saw the path. Matching here delivers the
// path to the module verbatim, so the router's guard decides, as it does for
// a trailing slash.
func dispatchModules(mounts []moduleMount, fallback http.Handler) http.Handler {
	if len(mounts) == 0 {
		return fallback
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.EscapedPath()
		for _, m := range mounts {
			if strings.HasPrefix(path, m.prefix) || (m.root != "" && path == m.root) {
				m.handler.ServeHTTP(w, r)
				return
			}
		}
		fallback.ServeHTTP(w, r)
	})
}

// enabledModules keeps identity, always mounted, and any module deps.Config
// enables, dropping the rest before they contribute a route, a
// permission-catalog entry or a contract path: their paths fall through to
// the /api 404 problem. deps.Config is nil only in tests that do not
// exercise enablement; a nil Config enables everything it is given.
func enabledModules(deps Deps, mods []Module) []Module {
	if deps.Config == nil {
		return mods
	}
	out := make([]Module, 0, len(mods))
	for _, mod := range mods {
		if mod.Name == "identity" || slices.Contains(deps.Config.Modules, mod.Name) {
			out = append(out, mod)
		}
	}
	return out
}

// mergeContract builds the contract served at GET /api/openapi.json: every
// module's paths (a path two modules both declare fails, naming it and the
// second module), and their components merged (an identical component under
// the same name is kept once; a differing one fails, naming it), with
// common.yaml references internalised so none remain in the output. It
// mutates docs' entries in place (InternalizeRefs), so callers must pass it
// copies no one else holds onto.
func mergeContract(docs map[string]*openapi3.T, order []string) (*openapi3.T, error) {
	combined := &openapi3.T{
		OpenAPI: "3.0.3",
		Info:    &openapi3.Info{Title: "Vantigo API", Version: "1"},
		Servers: openapi3.Servers{{URL: "/"}},
		Paths:   openapi3.NewPaths(),
		Components: &openapi3.Components{
			Schemas:         openapi3.Schemas{},
			Parameters:      openapi3.ParametersMap{},
			Headers:         openapi3.Headers{},
			RequestBodies:   openapi3.RequestBodies{},
			Responses:       openapi3.ResponseBodies{},
			SecuritySchemes: openapi3.SecuritySchemes{},
			Examples:        openapi3.Examples{},
			Links:           openapi3.Links{},
			Callbacks:       openapi3.Callbacks{},
		},
	}

	for _, name := range order {
		doc := docs[name]
		for path, item := range doc.Paths.Map() {
			if combined.Paths.Find(path) != nil {
				return nil, fmt.Errorf("module: path %q from %s is already registered by an earlier module", path, name)
			}
			combined.Paths.Set(path, item)
		}
		if doc.Components == nil {
			continue
		}
		c := doc.Components
		if err := mergeComponent(combined.Components.Schemas, c.Schemas, "schema", name); err != nil {
			return nil, err
		}
		if err := mergeComponent(combined.Components.Parameters, c.Parameters, "parameter", name); err != nil {
			return nil, err
		}
		if err := mergeComponent(combined.Components.Headers, c.Headers, "header", name); err != nil {
			return nil, err
		}
		if err := mergeComponent(combined.Components.RequestBodies, c.RequestBodies, "requestBody", name); err != nil {
			return nil, err
		}
		if err := mergeComponent(combined.Components.Responses, c.Responses, "response", name); err != nil {
			return nil, err
		}
		if err := mergeComponent(combined.Components.SecuritySchemes, c.SecuritySchemes, "securityScheme", name); err != nil {
			return nil, err
		}
		if err := mergeComponent(combined.Components.Examples, c.Examples, "example", name); err != nil {
			return nil, err
		}
		if err := mergeComponent(combined.Components.Links, c.Links, "link", name); err != nil {
			return nil, err
		}
		if err := mergeComponent(combined.Components.Callbacks, c.Callbacks, "callback", name); err != nil {
			return nil, err
		}
	}

	combined.InternalizeRefs(context.Background(), nil)
	return combined, nil
}

// mergeComponent adds src's entries to dst (dst's map, named after kind, of
// the module named module). An entry whose name already exists in dst must
// be identical (compared as JSON, so unexported position metadata never
// causes a false conflict) or mergeContract fails, naming the collision.
func mergeComponent[V any](dst, src map[string]V, kind, module string) error {
	for name, value := range src {
		existing, ok := dst[name]
		if !ok {
			dst[name] = value
			continue
		}
		same, err := jsonEqual(existing, value)
		if err != nil {
			return fmt.Errorf("module: compare %s %q from %s: %w", kind, name, module, err)
		}
		if !same {
			return fmt.Errorf("module: %s %q from %s differs from an earlier module's", kind, name, module)
		}
	}
	return nil
}

func jsonEqual(a, b any) (bool, error) {
	ja, err := json.Marshal(a)
	if err != nil {
		return false, err
	}
	jb, err := json.Marshal(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ja, jb), nil
}

// DecodeError returns a RequestErrorHandlerFunc/ErrorHandlerFunc that writes
// body with status 400 (never err.Error()). write knows the failing
// operation's error body shape (AuthErrorResponse, the flat CodeMessageError,
// or a SCIM error body); DecodeError only discards the error oapi-codegen
// would otherwise echo.
func DecodeError(write func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, _ error) {
		write(w, r)
	}
}

// ResponseError returns a ResponseErrorHandlerFunc that sends err through
// httpx.WriteError, except ErrNotImplemented, which answers 501.
func ResponseError() func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		if errors.Is(err, ErrNotImplemented) {
			httpx.WriteProblem(w, r, http.StatusNotImplemented, "")
			return
		}
		httpx.WriteError(w, r, err)
	}
}

// withCustomerPersonalData is deps with every module's
// contracts.CustomerPersonalData appended to Deps.CustomerPersonalData, each
// under its module's name, in mods order with the invoiced-work providers
// last (invoicedWorkLast; customers GDPR design D2) — onto a copy of whatever
// the caller preset, so a harness's own slice is never written through. mods
// is every module given, enabled or not.
func withCustomerPersonalData(deps Deps, mods []Module) Deps {
	var holders []contracts.CustomerPersonalDataHolder
	for _, mod := range invoicedWorkLast(mods) {
		if mod.CustomerPersonalData == nil {
			continue
		}
		if data := mod.CustomerPersonalData(deps); data != nil {
			holders = append(holders, contracts.CustomerPersonalDataHolder{Module: mod.Name, Data: data})
		}
	}
	if len(holders) > 0 {
		deps.CustomerPersonalData = append(slices.Clone(deps.CustomerPersonalData), holders...)
	}
	return deps
}

// invoicedWorkLast is mods with every module that declares InvoicedWork moved
// after every module that does not, each side in the order given: a stable
// partition. It orders the customer slots (rule 10's lock order): an issue
// locks its invoices document before any source row, so a customers merge or
// anonymisation, calling the holders in this order, takes the invoices
// documents before a project's row too, and the two can never wait on each
// other.
func invoicedWorkLast(mods []Module) []Module {
	out := make([]Module, 0, len(mods))
	for _, mod := range mods {
		if mod.InvoicedWork == nil {
			out = append(out, mod)
		}
	}
	for _, mod := range mods {
		if mod.InvoicedWork != nil {
			out = append(out, mod)
		}
	}
	return out
}

// withInvoicedWork is deps with every module's contracts.InvoicedWorkHolder
// appended to Deps.InvoicedWork, in mods order, after whatever the caller
// preset, in a new slice, so a harness's own slice is never written through.
// mods is every module given, enabled or not. It refuses a kind two holders
// claim, naming the kind and both — a preset holder as "Deps.InvoicedWork",
// whose kinds are claimed first: the issue hands each kind's sources to one
// holder. A nil preset entry is dropped, so no consumer of the slot ever
// calls a nil holder.
func withInvoicedWork(deps Deps, mods []Module) (Deps, error) {
	var holders []contracts.InvoicedWorkHolder
	claimedBy := make(map[contracts.WorkSourceKind]string)
	for _, preset := range deps.InvoicedWork {
		if preset == nil {
			continue
		}
		holders = append(holders, preset)
		for _, kind := range preset.Kinds() {
			if _, ok := claimedBy[kind]; ok {
				return deps, fmt.Errorf("module: two modules both stamp %q: Deps.InvoicedWork, Deps.InvoicedWork", kind)
			}
			claimedBy[kind] = "Deps.InvoicedWork"
		}
	}
	for _, mod := range mods {
		if mod.InvoicedWork == nil {
			continue
		}
		holder := mod.InvoicedWork(deps)
		if holder == nil {
			continue
		}
		for _, kind := range holder.Kinds() {
			if other, ok := claimedBy[kind]; ok {
				return deps, fmt.Errorf("module: two modules both stamp %q: %s, %s", kind, other, mod.Name)
			}
			claimedBy[kind] = mod.Name
		}
		holders = append(holders, holder)
	}
	deps.InvoicedWork = holders
	return deps, nil
}

// withCalendarSources is deps with every module's contracts.CalendarSource
// appended to Deps.CalendarSources, in mods order, after whatever the caller
// preset, in a new slice, so a harness's own slice is never written through.
// mods is the enabled modules only (calendar design D1); a nil source is
// dropped, so the calendar never calls a nil provider.
func withCalendarSources(deps Deps, mods []Module) Deps {
	var sources []contracts.CalendarSource
	for _, mod := range mods {
		if mod.CalendarSource == nil {
			continue
		}
		if source := mod.CalendarSource(deps); source != nil {
			sources = append(sources, source)
		}
	}
	if len(sources) > 0 {
		deps.CalendarSources = append(slices.Clone(deps.CalendarSources), sources...)
	}
	return deps
}

// withIdentityProvisioners is deps with every module's
// contracts.IdentityProvisioner appended to Deps.IdentityProvisioners, in
// mods order, after whatever the caller preset, in a new slice, so a
// harness's own slice is never written through. mods is the enabled modules
// only (Entra design D6); a nil provider is dropped, preset or built, so
// Staff never calls a nil one. It refuses a Key two providers claim, naming
// the key and both — a preset provider as "Deps.IdentityProvisioners",
// whose keys are claimed first: Staff writes one provider's links under each
// key. It refuses a reserved Key too (reservedProviderKey), naming the
// provider's module.
func withIdentityProvisioners(deps Deps, mods []Module) (Deps, error) {
	var providers []contracts.IdentityProvisioner
	claimedBy := make(map[string]string)
	for _, preset := range deps.IdentityProvisioners {
		if preset == nil {
			continue
		}
		key := preset.Key()
		if reservedProviderKey(key) {
			return deps, fmt.Errorf("module: Deps.IdentityProvisioners provides an identity provider under the reserved key %q", key)
		}
		if _, ok := claimedBy[key]; ok {
			return deps, fmt.Errorf("module: two modules both provide the identity provider %q: Deps.IdentityProvisioners, Deps.IdentityProvisioners", key)
		}
		claimedBy[key] = "Deps.IdentityProvisioners"
		providers = append(providers, preset)
	}
	for _, mod := range mods {
		if mod.IdentityProvisioner == nil {
			continue
		}
		provider := mod.IdentityProvisioner(deps)
		if provider == nil {
			continue
		}
		key := provider.Key()
		if reservedProviderKey(key) {
			return deps, fmt.Errorf("module: %s provides an identity provider under the reserved key %q", mod.Name, key)
		}
		if other, ok := claimedBy[key]; ok {
			return deps, fmt.Errorf("module: two modules both provide the identity provider %q: %s, %s", key, other, mod.Name)
		}
		claimedBy[key] = mod.Name
		providers = append(providers, provider)
	}
	deps.IdentityProvisioners = providers
	return deps, nil
}

// reservedProviderKey is whether key is one no identity provider — nor any
// provider of a keyed many-provider slot (Google Workspace design G2) — may
// answer:
// none at all, "identity" — the provider Staff's own identity link names in
// staff.identity_links — and the two account kinds that name no provider,
// local and external (Entra design D5). A provider under any of them would
// have its links taken for the identity link, or a person's kind for a
// provider's.
func reservedProviderKey(key string) bool {
	switch key {
	case "", "identity", contracts.AccountKindLocal, contracts.AccountKindExternal:
		return true
	}
	return false
}

// withDirectorySearches collects contracts.DirectorySearch from the enabled
// modules into Deps.DirectorySearches, after whatever the caller preset (the
// seam modtest.WithDirectorySearch fills), refusing a duplicate or reserved
// Key (G8). Compose alone calls it, after SSOConnections is resolved.
func withDirectorySearches(deps Deps, enabled []Module) (Deps, error) {
	providers, err := collectKeyed(deps.DirectorySearches, enabled, "Deps.DirectorySearches", "a directory search",
		func(m Module) func(Deps) contracts.DirectorySearch { return m.DirectorySearch }, deps)
	if err != nil {
		return deps, err
	}
	deps.DirectorySearches = providers
	return deps, nil
}
