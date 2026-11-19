package module

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// EventHandlers collects the domain-events outbox's handlers (events.Handler,
// orders design O2) from every module given, enabled or not — a many-provider
// slot, collected as the customer reference holders are: a handler of a module
// MODULES leaves out checks the setting itself. They come in mods order, each
// module's own in the order it returns them. It refuses a handler with no
// name, no events or no Handle, and a name two handlers share, naming both
// modules. Compose calls it to refuse a bad composition at startup; the
// process that runs workers calls it to build the dispatcher,
// events.NewDispatcher, since worker mode never composes.
//
// A handler is built with deps holding the permission catalog of the
// enabled modules when the caller set none (withCatalog): worker mode never
// composes, and identity's webhooks fan-out decides by the catalog whether an
// integration may receive an event (outgoing webhooks design W7).
func EventHandlers(deps Deps, mods ...Module) ([]events.Handler, error) {
	deps = withCatalog(deps, enabledModules(deps, mods))
	var out []events.Handler
	claimedBy := make(map[string]string)
	for _, mod := range mods {
		if mod.EventHandlers == nil {
			continue
		}
		for _, h := range mod.EventHandlers(deps) {
			if h.Name == "" || len(h.Events) == 0 || h.Handle == nil {
				return nil, fmt.Errorf("module: %s declares an event handler %q without a name, events or Handle", mod.Name, h.Name)
			}
			if other, ok := claimedBy[h.Name]; ok {
				return nil, fmt.Errorf("module: two event handlers are both named %q: %s, %s", h.Name, other, mod.Name)
			}
			claimedBy[h.Name] = mod.Name
			out = append(out, h)
		}
	}
	return out, nil
}

// Workers returns every worker.Worker the enabled modules among mods
// contribute, in mods order, each module's own contribution concatenated in
// the order Module.Workers returns it. A module with a nil Workers field
// contributes none; a disabled module (one enabledModules drops, the same
// enablement Compose itself applies) contributes none either, whether or
// not it declares any.
//
// Resolving Workers does not require Compose: worker mode never mounts the
// HTTP API, so cmd/vantigo calls Workers directly instead of reaching it
// through Compose's return value. It resolves each module's contribution from
// deps as the caller passes it, plus Deps.CustomerPersonalData,
// Deps.InvoicedWork, Deps.Directory, Deps.Users, Deps.Accounts,
// Deps.Products, Deps.SalesTax, Deps.SalesJournal, Deps.ExpenseJournal, Deps.BankFeed, Deps.Workforce, Deps.Stock, Deps.StockLedger,
// Deps.SalesOrderLines, Deps.Production, Deps.DeliveryTrace, Deps.KitStructure, Deps.CalendarSources,
// Deps.IdentityProvisioners, Deps.MailboxCalendars, Deps.ChannelMailboxes and
// Deps.PersonalMailboxes, which it resolves from the enabled modules the way
// Compose does — the same base Deps Compose resolves Directory from, before
// any Mount runs, with no per-module Doc, and the enabled modules' permission
// catalog as Catalog unless the caller set one (withCatalog) — since a background worker is a data-layer concern (Pool, Config,
// Clock, Mail, Secrets, ...), not a contract one.
func Workers(deps Deps, mods ...Module) []worker.Worker {
	deps = WorkerDeps(deps, mods...)
	var out []worker.Worker
	for _, mod := range enabledModules(deps, mods) {
		if mod.Workers == nil {
			continue
		}
		out = append(out, mod.Workers(deps)...)
	}
	return out
}

// WorkerDeps is deps as Workers hands it to every module's workers: each
// slot Workers resolves, resolved from the enabled modules among mods as
// Compose resolves it. A program that drives a module's own entry points
// without composing — the demo seed — builds its Deps here, so they do not
// differ from a worker's in who holds what. Like Workers, it panics on a slot
// two modules claim: a bug in the binary's own module list.
func WorkerDeps(deps Deps, mods ...Module) Deps {
	// Every module given, enabled or not, before enablement drops any: the
	// anonymisation worker is where contracts.CustomerPersonalData's erase
	// runs (customers GDPR design D2), and worker mode never composes, so
	// nothing else would put the slot on its Deps.
	// The invoiced-work holders likewise (rule 10): the EHF workers build an
	// invoices server from these Deps, and a worker-mode server must never
	// read an empty slot as "nothing to stamp". Workers has no error to
	// return, so a kind two modules claim — a bug in the binary's own module
	// list, which Compose refuses at startup — panics.
	deps = withCustomerPersonalData(deps, mods)
	deps, err := withInvoicedWork(deps, mods)
	if err != nil {
		panic(err.Error())
	}
	enabled := enabledModules(deps, mods)
	// The permission catalog of the enabled modules, as Compose builds it,
	// when the caller set none: identity's webhook sender checks by it whether
	// an integration still may receive an event (outgoing webhooks design
	// W33), and worker mode never composes.
	deps = withCatalog(deps, enabled)
	// The customer directory, resolved as Compose resolves it — from the
	// enabled module that declares one — since a worker may read customers
	// too: energy's import worker resolves a file's organisation numbers
	// through it (energy import design D4), and worker mode never composes.
	// With no such module enabled the slot stays as the caller set it.
	provider, err := soleProvider(enabled, "a customer directory", func(m Module) bool { return m.Directory != nil })
	if err != nil {
		panic(err.Error())
	}
	if provider != nil {
		deps.Directory = provider.Directory(deps)
	}
	deps, err = withUsersAndAccounts(deps, enabled)
	if err != nil {
		panic(err.Error())
	}
	// The workforce register, resolved as Compose resolves it, after the
	// business calendar: a worker-mode Deps must not differ from a composed
	// one in who holds what.
	deps, err = withWorkforce(deps, enabled)
	if err != nil {
		panic(err.Error())
	}
	deps, err = withBrandTargets(deps, enabled)
	if err != nil {
		panic(err.Error())
	}
	// The product catalog, resolved as Compose resolves it, after the
	// accounts and before the stock: Manufacturing's make-to-order worker
	// locks the variants of the orders it makes through it, and Inventory's
	// stock ledger, built below, reserves through it.
	if deps, err = withProducts(deps, enabled); err != nil {
		panic(err.Error())
	}
	// The project directory, then the three billable reads and consumption,
	// resolved as Compose resolves them (energy consumption billing design
	// D10): the invoices-billing worker drafts and issues through the
	// wizard's and the issue's own code paths, which read them, and worker
	// mode never composes.
	if deps, err = withProjects(deps, enabled); err != nil {
		panic(err.Error())
	}
	if deps, err = withBillables(deps, enabled); err != nil {
		panic(err.Error())
	}
	// Sales tax, resolved as Compose resolves it: Orders' mail worker prints
	// the seller block it reads into every confirmation and shipping notice.
	if deps, err = withSalesTax(deps, enabled); err != nil {
		panic(err.Error())
	}
	// The sales journal, resolved as Compose resolves it, right after sales
	// tax: the ledger's catch-up worker reads it, and worker mode never
	// composes.
	if deps, err = withSalesJournal(deps, enabled); err != nil {
		panic(err.Error())
	}
	// The exchange rates, likewise, right after the sales journal.
	if deps, err = withExchangeRates(deps, enabled); err != nil {
		panic(err.Error())
	}
	// The expense journal, likewise: the ledger's catch-up reads it too.
	if deps, err = withExpenseJournal(deps, enabled); err != nil {
		panic(err.Error())
	}
	// The bank feed, likewise: the ledger's bank feed worker reads it.
	if deps, err = withBankFeed(deps, enabled); err != nil {
		panic(err.Error())
	}
	// The order backlog, as Compose resolves it: the invoices-billing worker
	// issues through the issue's own code path, which reads it.
	if deps, err = withOrderBacklog(deps, enabled); err != nil {
		panic(err.Error())
	}
	// The stock read and the stock ledger, resolved as Compose resolves
	// them: a worker that cancels an order must release its reservations
	// as the API does.
	if deps, err = withStock(deps, mods, enabled); err != nil {
		panic(err.Error())
	}
	// The calendar sources, from the enabled modules only, as Compose
	// collects them (calendar design D1): no worker reads them in phase 1, but
	// a worker-mode Deps must not differ from a composed one in what it says
	// the calendar may ask.
	deps = withCalendarSources(deps, enabled)
	// The identity provisioners, from the enabled modules only, as Compose
	// collects them (Entra design D6): Staff's accounts worker drives them,
	// and worker mode never composes. A key two providers claim — a bug in
	// the binary's own module list, which Compose refuses at startup —
	// panics, as a kind two invoiced-work holders claim does.
	deps, err = withIdentityProvisioners(deps, enabled)
	if err != nil {
		panic(err.Error())
	}
	// The mailbox calendars, resolved as Compose resolves them (calendar
	// Outlook sync design O4): the calendar's sync worker writes through
	// them, and worker mode never composes. A key two providers claim
	// panics, as one two identity provisioners claim does.
	deps, err = withMailboxCalendars(deps, enabled)
	if err != nil {
		panic(err.Error())
	}
	// The channel mailboxes, resolved as Compose resolves them (Outlook
	// connection design M11): Communications' outbox sends through them, and
	// worker mode never composes. A key two providers claim panics, as
	// for the mailbox calendars.
	deps, err = withChannelMailboxes(deps, enabled)
	if err != nil {
		panic(err.Error())
	}
	// The channel messengers, resolved as Compose resolves them (Messenger
	// design N2): Communications' outbox and messenger sync run through them,
	// and worker mode never composes. A key two providers claim, or one also
	// filled as a channel mailbox, panics, as for the channel mailboxes.
	deps, err = withChannelMessengers(deps, enabled)
	if err != nil {
		panic(err.Error())
	}
	// The personal mailboxes, resolved as Compose resolves them (Outlook
	// connection design M20): Communications' outbox sends through them, and
	// worker mode never composes. A key two providers claim panics, as
	// for the channel mailboxes.
	deps, err = withPersonalMailboxes(deps, enabled)
	if err != nil {
		panic(err.Error())
	}
	// The outbound mail slot, resolved as Compose resolves it (integrations
	// design T4): Invoices' reminder worker sends through it, and worker mode
	// never composes. Two providers panic, as two personal mailboxes do.
	deps, err = withOutboundMail(deps, enabled)
	if err != nil {
		panic(err.Error())
	}
	return deps
}

// withCatalog sets deps.Catalog to the permissions the enabled modules
// declare, keyed as Compose keys them, unless the caller set one. Compose
// refuses a duplicate key; a process that runs workers has composed, or is
// built from the same module list as one that did.
func withCatalog(deps Deps, enabled []Module) Deps {
	if deps.Catalog != nil {
		return deps
	}
	catalog := make(map[string]contracts.Permission)
	for _, mod := range enabled {
		for _, perm := range mod.Permissions {
			catalog[perm.Key] = perm
		}
	}
	deps.Catalog = catalog
	return deps
}

// withUsersAndAccounts resolves the single-provider slots a worker needs
// from the enabled modules, in Compose's order — Users, the Calendar, then Accounts, whose
// provider may read deps.Users while it is built — keeping a preset value
// when no enabled module provides one (the seam a test harness fills). The
// staff-accounts worker writes identity's accounts and reads its users, and
// worker mode never composes, so nothing else would set them.
func withUsersAndAccounts(deps Deps, enabled []Module) (Deps, error) {
	usersProvider, err := soleProvider(enabled, "a user directory", func(m Module) bool { return m.Users != nil })
	if err != nil {
		return deps, err
	}
	if usersProvider != nil {
		deps.Users = usersProvider.Users(deps)
	}
	calendarProvider, err := soleProvider(enabled, "a business calendar", func(m Module) bool { return m.Calendar != nil })
	if err != nil {
		return deps, err
	}
	if calendarProvider != nil {
		deps.Calendar = calendarProvider.Calendar(deps)
	}
	accountsProvider, err := soleProvider(enabled, "account provisioning", func(m Module) bool { return m.Accounts != nil })
	if err != nil {
		return deps, err
	}
	if accountsProvider != nil {
		deps.Accounts = accountsProvider.Accounts(deps)
	}
	return deps, nil
}

// withProducts resolves contracts.ProductCatalog from the enabled modules —
// at most one may declare it — keeping a preset value when none does (the
// seam modtest.WithProducts fills). Compose and Workers both call it, after
// account provisioning and before the stock slots, whose ledger reserves
// through it.
func withProducts(deps Deps, enabled []Module) (Deps, error) {
	provider, err := soleProvider(enabled, "a product catalog", func(m Module) bool { return m.Products != nil })
	if err != nil {
		return deps, err
	}
	if provider != nil {
		deps.Products = provider.Products(deps)
	}
	return deps, nil
}

// withSalesTax resolves contracts.SalesTax from the enabled modules — at
// most one may declare it — keeping a preset value when none does. Compose
// calls it right after the receivables, Workers after the billable reads;
// its provider reads its own tables only.
func withSalesTax(deps Deps, enabled []Module) (Deps, error) {
	provider, err := soleProvider(enabled, "sales tax", func(m Module) bool { return m.SalesTax != nil })
	if err != nil {
		return deps, err
	}
	if provider != nil {
		deps.SalesTax = provider.SalesTax(deps)
	}
	return deps, nil
}

// withSalesJournal resolves contracts.SalesJournal from the enabled modules —
// at most one may declare it — keeping a preset value when none does (the seam
// modtest.WithSalesJournal fills). Compose and Workers both call it right
// after withSalesTax: a worker reads the journal as a request does, and worker
// mode never composes.
func withSalesJournal(deps Deps, enabled []Module) (Deps, error) {
	provider, err := soleProvider(enabled, "a sales journal", func(m Module) bool { return m.SalesJournal != nil })
	if err != nil {
		return deps, err
	}
	if provider != nil {
		deps.SalesJournal = provider.SalesJournal(deps)
	}
	return deps, nil
}

// withExchangeRates resolves contracts.ExchangeRates from the enabled modules
// — at most one may declare it — keeping a preset value when none does.
// Compose and Workers both call it right after withSalesJournal.
func withExchangeRates(deps Deps, enabled []Module) (Deps, error) {
	provider, err := soleProvider(enabled, "exchange rates", func(m Module) bool { return m.ExchangeRates != nil })
	if err != nil {
		return deps, err
	}
	if provider != nil {
		deps.ExchangeRates = provider.ExchangeRates(deps)
	}
	return deps, nil
}

// withExpenseJournal resolves contracts.ExpenseJournal from the enabled
// modules — at most one may declare it — keeping a preset value when none
// does (the seam modtest.WithExpenseJournal fills). Compose and Workers both
// call it right after withSalesJournal.
func withExpenseJournal(deps Deps, enabled []Module) (Deps, error) {
	provider, err := soleProvider(enabled, "an expense journal", func(m Module) bool { return m.ExpenseJournal != nil })
	if err != nil {
		return deps, err
	}
	if provider != nil {
		deps.ExpenseJournal = provider.ExpenseJournal(deps)
	}
	return deps, nil
}

// withBankFeed resolves contracts.BankFeed from the enabled modules — at most
// one may declare it — keeping a preset value when none does (the seam
// modtest.WithBankFeed fills). The provider may answer nil: the feed is
// switched off on the instance. Compose and Workers both call it right after
// withExpenseJournal.
func withBankFeed(deps Deps, enabled []Module) (Deps, error) {
	provider, err := soleProvider(enabled, "a bank feed", func(m Module) bool { return m.BankFeed != nil })
	if err != nil {
		return deps, err
	}
	if provider != nil {
		deps.BankFeed = provider.BankFeed(deps)
	}
	return deps, nil
}

// withOrderBacklog resolves contracts.OrderBacklog from the enabled modules —
// at most one may declare it — keeping a preset value when none does. Compose
// calls it right after the receivables, Workers after the bank feed; its
// provider reads its own tables only.
func withOrderBacklog(deps Deps, enabled []Module) (Deps, error) {
	provider, err := soleProvider(enabled, "an order backlog", func(m Module) bool { return m.OrderBacklog != nil })
	if err != nil {
		return deps, err
	}
	if provider != nil {
		deps.OrderBacklog = provider.OrderBacklog(deps)
	}
	return deps, nil
}

// withWorkforce resolves contracts.Workforce from the enabled modules — at
// most one may declare it — keeping a preset value when none does (the seam
// modtest.WithWorkforce fills). Compose and Workers both call it, after the
// business calendar.
func withWorkforce(deps Deps, enabled []Module) (Deps, error) {
	provider, err := soleProvider(enabled, "a workforce directory", func(m Module) bool { return m.Workforce != nil })
	if err != nil {
		return deps, err
	}
	if provider != nil {
		deps.Workforce = provider.Workforce(deps)
	}
	return deps, nil
}

// withBrandTargets resolves contracts.BrandTargets from the enabled modules —
// at most one may declare it — keeping a preset value when none does (the seam
// modtest.WithBrandTargets fills). Compose and Workers both call it, after the
// business calendar, whose zone decides "today".
func withBrandTargets(deps Deps, enabled []Module) (Deps, error) {
	provider, err := soleProvider(enabled, "brand targets", func(m Module) bool { return m.BrandTargets != nil })
	if err != nil {
		return deps, err
	}
	if provider != nil {
		deps.BrandTargets = provider.BrandTargets(deps)
	}
	return deps, nil
}

// keyedProvider is a many-provider slot's provider: it answers its key with
// no context and no network (Google Workspace design G2).
type keyedProvider interface{ Key() string }

// collectKeyed is the providers of one many-provider slot keyed by Key: the
// caller's preset ones first, then every enabled module's in mods order, in a
// new slice so a harness's own slice is never written through. A nil
// provider, preset or built, is dropped. It refuses a Key two providers
// claim, naming the key and both — a preset provider as field — and a
// reserved Key (reservedProviderKey), naming the provider's module: a
// consumer picks a provider by the key a channel, a message or a person's
// account names, so two under one key would be one too many. what names the
// slot for the errors ("a mailbox calendar").
func collectKeyed[T keyedProvider](preset []T, mods []Module, field, what string, build func(Module) func(Deps) T, deps Deps) ([]T, error) {
	var providers []T
	claimedBy := make(map[string]string)
	claim := func(provider T, by string) error {
		key := provider.Key()
		if reservedProviderKey(key) {
			return fmt.Errorf("module: %s provides %s under the reserved key %q", by, what, key)
		}
		if other, ok := claimedBy[key]; ok {
			return fmt.Errorf("module: two modules both provide %s %q: %s, %s", what, key, other, by)
		}
		claimedBy[key] = by
		providers = append(providers, provider)
		return nil
	}
	for _, p := range preset {
		if isNilProvider(p) {
			continue
		}
		if err := claim(p, field); err != nil {
			return nil, err
		}
	}
	for _, mod := range mods {
		fn := build(mod)
		if fn == nil {
			continue
		}
		p := fn(deps)
		if isNilProvider(p) {
			continue
		}
		if err := claim(p, mod.Name); err != nil {
			return nil, err
		}
	}
	return providers, nil
}

// isNilProvider is whether p is a nil interface or holds a nil pointer.
func isNilProvider[T keyedProvider](p T) bool {
	v := reflect.ValueOf(p)
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func:
		return v.IsNil()
	}
	return false
}

// withMailboxCalendars collects contracts.MailboxCalendar from the enabled
// modules into Deps.MailboxCalendars, after whatever the caller preset (the
// seam modtest.WithMailboxCalendar fills), refusing a duplicate or reserved
// Key (Google Workspace design G2). Compose and Workers both call it, after
// the identity provisioners.
func withMailboxCalendars(deps Deps, enabled []Module) (Deps, error) {
	providers, err := collectKeyed(deps.MailboxCalendars, enabled, "Deps.MailboxCalendars", "a mailbox calendar",
		func(m Module) func(Deps) contracts.MailboxCalendar { return m.MailboxCalendar }, deps)
	if err != nil {
		return deps, err
	}
	deps.MailboxCalendars = providers
	return deps, nil
}

// withChannelMailboxes collects contracts.ChannelMailbox from the enabled
// modules into Deps.ChannelMailboxes, after whatever the caller preset (the
// seam modtest.WithChannelMailbox fills), refusing a duplicate or reserved
// Key (G2). Compose and Workers both call it, after the mailbox calendars.
func withChannelMailboxes(deps Deps, enabled []Module) (Deps, error) {
	providers, err := collectKeyed(deps.ChannelMailboxes, enabled, "Deps.ChannelMailboxes", "a channel mailbox",
		func(m Module) func(Deps) contracts.ChannelMailbox { return m.ChannelMailbox }, deps)
	if err != nil {
		return deps, err
	}
	deps.ChannelMailboxes = providers
	return deps, nil
}

// withChannelMessengers collects contracts.ChannelMessenger from the enabled
// modules into Deps.ChannelMessengers, after whatever the caller preset (the
// seam modtest.WithChannelMessenger fills), refusing a duplicate or reserved
// Key as the other slots do, and a Key that is also filled in
// Deps.ChannelMailboxes (Messenger design N1): a channel's provider names one
// kind of provider, so one key under both would be served by either. Compose
// and Workers both call it, after the channel mailboxes.
func withChannelMessengers(deps Deps, enabled []Module) (Deps, error) {
	providers, err := collectKeyed(deps.ChannelMessengers, enabled, "Deps.ChannelMessengers", "a channel messenger",
		func(m Module) func(Deps) contracts.ChannelMessenger { return m.ChannelMessenger }, deps)
	if err != nil {
		return deps, err
	}
	for _, messenger := range providers {
		for _, mailbox := range deps.ChannelMailboxes {
			if mailbox != nil && mailbox.Key() == messenger.Key() {
				return deps, fmt.Errorf("module: %q is provided both as a channel mailbox and as a channel messenger", messenger.Key())
			}
		}
	}
	deps.ChannelMessengers = providers
	return deps, nil
}

// withPersonalMailboxes collects contracts.PersonalMailbox from the enabled
// modules into Deps.PersonalMailboxes, after whatever the caller preset (the
// seam modtest.WithPersonalMailbox fills), refusing a duplicate or reserved
// Key (G2). Compose and Workers both call it, after the channel mailboxes.
func withPersonalMailboxes(deps Deps, enabled []Module) (Deps, error) {
	providers, err := collectKeyed(deps.PersonalMailboxes, enabled, "Deps.PersonalMailboxes", "a personal mailbox",
		func(m Module) func(Deps) contracts.PersonalMailbox { return m.PersonalMailbox }, deps)
	if err != nil {
		return deps, err
	}
	deps.PersonalMailboxes = providers
	return deps, nil
}

// withOutboundMail resolves contracts.OutboundMail from the enabled modules —
// at most one may declare it — keeping a preset value when none does (the
// seam modtest.WithOutboundMail fills). Compose and Workers both call it,
// after the personal mailboxes: its provider reads deps.ChannelMailboxes.
func withOutboundMail(deps Deps, enabled []Module) (Deps, error) {
	provider, err := soleProvider(enabled, "outbound mail", func(m Module) bool { return m.OutboundMail != nil })
	if err != nil {
		return deps, err
	}
	if provider != nil {
		deps.OutboundMail = provider.OutboundMail(deps)
	}
	return deps, nil
}

// withProjects resolves contracts.ProjectDirectory from the enabled modules —
// at most one may declare it — keeping a preset value when none does. Compose
// and Workers both call it, after the directory, the users, the calendar and
// account provisioning.
func withProjects(deps Deps, enabled []Module) (Deps, error) {
	provider, err := soleProvider(enabled, "a project directory", func(m Module) bool { return m.Projects != nil })
	if err != nil {
		return deps, err
	}
	if provider != nil {
		deps.Projects = provider.Projects(deps)
	}
	return deps, nil
}

// withBillables resolves the three billable reads (contracts.BillableHours,
// BillableExpenses, BillableMilestones, rule 10), then consumption
// (contracts.ConsumptionProvider, energy consumption billing design D1) and
// then billable orders (contracts.BillableOrders, orders design O37) from
// the enabled modules — at most one may declare each — keeping a preset value
// when none does. They come after the project directory: a provider may read
// deps.Projects while it is built, never while it serves. Consumption and
// billable orders come after the three: they are what Invoices builds a
// consumption invoice and an order invoice from, as the three are what it
// builds a work invoice from, and their providers never read the directory
// while they serve. Compose and Workers both call it.
func withBillables(deps Deps, enabled []Module) (Deps, error) {
	hours, err := soleProvider(enabled, "billable hours", func(m Module) bool { return m.BillableHours != nil })
	if err != nil {
		return deps, err
	}
	if hours != nil {
		deps.BillableHours = hours.BillableHours(deps)
	}
	expenses, err := soleProvider(enabled, "billable expenses", func(m Module) bool { return m.BillableExpenses != nil })
	if err != nil {
		return deps, err
	}
	if expenses != nil {
		deps.BillableExpenses = expenses.BillableExpenses(deps)
	}
	milestones, err := soleProvider(enabled, "billable milestones", func(m Module) bool { return m.BillableMilestones != nil })
	if err != nil {
		return deps, err
	}
	if milestones != nil {
		deps.BillableMilestones = milestones.BillableMilestones(deps)
	}
	consumption, err := soleProvider(enabled, "consumption", func(m Module) bool { return m.Consumption != nil })
	if err != nil {
		return deps, err
	}
	if consumption != nil {
		deps.Consumption = consumption.Consumption(deps)
	}
	orders, err := soleProvider(enabled, "billable orders", func(m Module) bool { return m.BillableOrders != nil })
	if err != nil {
		return deps, err
	}
	if orders != nil {
		deps.BillableOrders = orders.BillableOrders(deps)
	}
	return deps, nil
}

// withStock resolves contracts.Stock from the enabled modules and
// contracts.StockLedger from every module given, enabled or not — at most one
// may declare each — keeping a preset value when none does (orders design
// O45). The ledger is the holders' kind of slot (rule 24): a reservation made
// while Inventory was on is still released by a cancel, an edit or a manual
// marking after it is switched off, so a disabled Inventory still writes
// its own reservations. Compose and Workers both call it, after the product
// catalog; it ends with withProduction.
func withStock(deps Deps, given, enabled []Module) (Deps, error) {
	stock, err := soleProvider(enabled, "a stock read", func(m Module) bool { return m.Stock != nil })
	if err != nil {
		return deps, err
	}
	if stock != nil {
		deps.Stock = stock.Stock(deps)
	}
	ledger, err := soleProvider(given, "a stock ledger", func(m Module) bool { return m.StockLedger != nil })
	if err != nil {
		return deps, err
	}
	if ledger != nil {
		deps.StockLedger = ledger.StockLedger(deps)
	}
	if deps, err = withProduction(deps, enabled); err != nil {
		return deps, err
	}
	return withSuppliers(deps, given, enabled)
}

// withSuppliers resolves the supplier slots (purchasing design P1, P4, P6,
// P7, P10): contracts.SupplierDirectory, contracts.SupplierInvoices,
// contracts.SupplierInvoiceMatches and contracts.PurchaseSupply from the
// enabled modules — at most one may declare each — and then
// contracts.PurchaseDemand by its precedence (demandProvider), keeping a
// preset value when none does; and the supplier holders and personal data from every
// module given, enabled or not, appended onto a copy of what the caller
// preset, in module order. No provider reads another while it is built.
// withStock calls it last, for Compose and Workers both: Manufacturing's
// replenishment worker reads the open purchases in worker mode.
func withSuppliers(deps Deps, given, enabled []Module) (Deps, error) {
	directory, err := soleProvider(enabled, "a supplier directory", func(m Module) bool { return m.SupplierDirectory != nil })
	if err != nil {
		return deps, err
	}
	if directory != nil {
		deps.SupplierDirectory = directory.SupplierDirectory(deps)
	}
	invoices, err := soleProvider(enabled, "supplier invoices", func(m Module) bool { return m.SupplierInvoices != nil })
	if err != nil {
		return deps, err
	}
	if invoices != nil {
		deps.SupplierInvoices = invoices.SupplierInvoices(deps)
	}
	matches, err := soleProvider(enabled, "supplier invoice matches", func(m Module) bool { return m.SupplierInvoiceMatches != nil })
	if err != nil {
		return deps, err
	}
	if matches != nil {
		deps.SupplierInvoiceMatches = matches.SupplierInvoiceMatches(deps)
	}
	supply, err := soleProvider(enabled, "a purchase supply", func(m Module) bool { return m.PurchaseSupply != nil })
	if err != nil {
		return deps, err
	}
	if supply != nil {
		deps.PurchaseSupply = supply.PurchaseSupply(deps)
	}
	demand, err := demandProvider(enabled)
	if err != nil {
		return deps, err
	}
	if demand != nil {
		deps.PurchaseDemand = demand.PurchaseDemand(deps)
	}
	var holders []contracts.SupplierReferenceHolder
	var data []contracts.SupplierPersonalDataHolder
	for _, mod := range given {
		if mod.SupplierReferences != nil {
			if h := mod.SupplierReferences(deps); h != nil {
				holders = append(holders, h)
			}
		}
		if mod.SupplierPersonalData != nil {
			if d := mod.SupplierPersonalData(deps); d != nil {
				data = append(data, contracts.SupplierPersonalDataHolder{Module: mod.Name, Data: d})
			}
		}
	}
	if len(holders) > 0 {
		deps.SupplierReferenceHolders = append(slices.Clone(deps.SupplierReferenceHolders), holders...)
	}
	if len(data) > 0 {
		deps.SupplierPersonalData = append(slices.Clone(deps.SupplierPersonalData), data...)
	}
	return deps, nil
}

// demandProvider is the one enabled module whose contracts.PurchaseDemand
// Purchasing's proposals read (purchasing design A4): of the enabled modules
// declaring Module.PurchaseDemand, those no other of them supersedes; nil when
// none declares it, an error when two remain.
func demandProvider(enabled []Module) (*Module, error) {
	superseded := map[string]bool{}
	for _, m := range enabled {
		if m.PurchaseDemand != nil {
			for _, name := range m.PurchaseDemandSupersedes {
				superseded[name] = true
			}
		}
	}
	var remaining []Module
	for _, m := range enabled {
		if m.PurchaseDemand != nil && !superseded[m.Name] {
			remaining = append(remaining, m)
		}
	}
	return soleProvider(remaining, "a purchase demand", func(Module) bool { return true })
}

// withProduction resolves contracts.SalesOrderLines, contracts.Production,
// contracts.DeliveryTrace and contracts.KitStructure from the enabled modules — at most one may declare each — keeping a preset
// value when none does (manufacturing design M43, M46). Neither provider
// reads the other while it is built, so the two may come in either order;
// withStock calls it last, for Compose and Workers both.
func withProduction(deps Deps, enabled []Module) (Deps, error) {
	lines, err := soleProvider(enabled, "sales order lines", func(m Module) bool { return m.SalesOrderLines != nil })
	if err != nil {
		return deps, err
	}
	if lines != nil {
		deps.SalesOrderLines = lines.SalesOrderLines(deps)
	}
	production, err := soleProvider(enabled, "a production read", func(m Module) bool { return m.Production != nil })
	if err != nil {
		return deps, err
	}
	if production != nil {
		deps.Production = production.Production(deps)
	}
	trace, err := soleProvider(enabled, "a delivery trace", func(m Module) bool { return m.DeliveryTrace != nil })
	if err != nil {
		return deps, err
	}
	if trace != nil {
		deps.DeliveryTrace = trace.DeliveryTrace(deps)
	}
	kits, err := soleProvider(enabled, "a kit structure", func(m Module) bool { return m.KitStructure != nil })
	if err != nil {
		return deps, err
	}
	if kits != nil {
		deps.KitStructure = kits.KitStructure(deps)
	}
	return deps, nil
}
