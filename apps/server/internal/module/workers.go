package module

import "github.com/vantigo-io/vantigo/server/internal/worker"

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
// Deps.CalendarSources and Deps.IdentityProvisioners, which it resolves from
// the enabled modules the way
// Compose does — the same base Deps Compose
// resolves Directory from, before any Mount runs, with no per-module Doc or
// Catalog — since a background worker is a data-layer concern (Pool, Config,
// Clock, Mail, Secrets, ...), not a contract one.
func Workers(deps Deps, mods ...Module) []worker.Worker {
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
	var out []worker.Worker
	for _, mod := range enabled {
		if mod.Workers == nil {
			continue
		}
		out = append(out, mod.Workers(deps)...)
	}
	return out
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
