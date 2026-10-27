package module

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// fakeWorker is a minimal worker.Worker: these tests only need a distinct,
// nameable value to inject through Module.Workers and assert on, never a
// real Run — the mechanics of running one belong to internal/worker's own
// tests.
type fakeWorker struct{ name string }

func (f fakeWorker) Name() string              { return f.name }
func (f fakeWorker) Interval() time.Duration   { return time.Minute }
func (f fakeWorker) Run(context.Context) error { return nil }

func workerNames(workers []worker.Worker) []string {
	names := make([]string, len(workers))
	for i, w := range workers {
		names[i] = w.Name()
	}
	return names
}

// TestWorkers_CollectsEveryEnabledModulesContribution proves Workers
// concatenates every enabled module's own workers, in mods order, each
// module's own slice kept in the order it returns them — not just the
// first module that declares any.
func TestWorkers_CollectsEveryEnabledModulesContribution(t *testing.T) {
	deps := Deps{Config: &config.Config{Modules: []string{"alpha", "beta"}}}
	got := Workers(deps,
		Module{Name: "identity"}, // always enabled; no Workers field
		Module{Name: "alpha", Workers: func(Deps) []worker.Worker {
			return []worker.Worker{fakeWorker{name: "alpha-1"}, fakeWorker{name: "alpha-2"}}
		}},
		Module{Name: "beta", Workers: func(Deps) []worker.Worker {
			return []worker.Worker{fakeWorker{name: "beta-1"}}
		}},
	)
	want := []string{"alpha-1", "alpha-2", "beta-1"}
	names := workerNames(got)
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("names = %v, want %v", names, want)
			break
		}
	}
}

// A module with a nil Workers field (every module today) contributes none:
// Workers never invents a zero-value worker or panics on the nil func.
func TestWorkers_ModuleWithoutWorkersContributesNone(t *testing.T) {
	deps := Deps{Config: &config.Config{Modules: []string{"alpha"}}}
	got := Workers(deps, Module{Name: "alpha"})
	if got != nil {
		t.Errorf("got = %v, want none from a module with a nil Workers field", got)
	}
}

// TestWorkers_DisabledModuleContributesNone is the rule the runner depends
// on for correctness, not just tidiness: a module MODULES has not enabled
// must not have its background job started at all, mirroring Compose's own
// enablement (enabledModules). Its Workers func must never even run.
func TestWorkers_DisabledModuleContributesNone(t *testing.T) {
	called := false
	deps := Deps{Config: &config.Config{Modules: []string{}}} // "alpha" not enabled
	got := Workers(deps, Module{Name: "alpha", Workers: func(Deps) []worker.Worker {
		called = true
		return []worker.Worker{fakeWorker{name: "alpha-1"}}
	}})
	if called {
		t.Error("a disabled module's Workers was called")
	}
	if got != nil {
		t.Errorf("got = %v, want none from a disabled module", got)
	}
}

// identity is always enabled regardless of MODULES, the same rule Compose
// applies (enabledModules): if it ever declared workers, they would always
// run.
func TestWorkers_IdentityIsAlwaysEnabled(t *testing.T) {
	deps := Deps{Config: &config.Config{Modules: nil}}
	got := Workers(deps, Module{Name: "identity", Workers: func(Deps) []worker.Worker {
		return []worker.Worker{fakeWorker{name: "identity-1"}}
	}})
	if names := workerNames(got); len(names) != 1 || names[0] != "identity-1" {
		t.Errorf("names = %v, want [identity-1]", names)
	}
}

// A nil Deps.Config enables everything, mirroring enabledModules' own rule
// for the tests that never exercise enablement.
func TestWorkers_NilConfigEnablesEverything(t *testing.T) {
	got := Workers(Deps{}, Module{Name: "alpha", Workers: func(Deps) []worker.Worker {
		return []worker.Worker{fakeWorker{name: "alpha-1"}}
	}})
	if names := workerNames(got); len(names) != 1 || names[0] != "alpha-1" {
		t.Errorf("names = %v, want [alpha-1]", names)
	}
}

// TestWorkers_NoModulesDeclareAnyReturnsNone proves the empty case does not
// panic and returns an empty (nil) slice rather than some non-nil zero
// value a caller would have to special-case.
func TestWorkers_NoModulesDeclareAnyReturnsNone(t *testing.T) {
	deps := Deps{Config: &config.Config{Modules: []string{"alpha", "beta"}}}
	got := Workers(deps, Module{Name: "alpha"}, Module{Name: "beta"})
	if got != nil {
		t.Errorf("got = %v, want none", got)
	}
}

// Worker mode never composes, and the anonymisation worker is the one caller
// of contracts.CustomerPersonalData's erase (customers GDPR design D2), so
// Workers collects the slot itself — from every module given, a disabled one
// included — before any module builds its workers.
func TestWorkers_HandTheWorkersEveryModulesCustomerPersonalData(t *testing.T) {
	beta := &fakePersonalData{name: "beta"}
	var seen []contracts.CustomerPersonalDataHolder
	Workers(Deps{Config: &config.Config{Modules: []string{"alpha"}}},
		Module{Name: "alpha", Workers: func(d Deps) []worker.Worker {
			seen = d.CustomerPersonalData
			return nil
		}},
		Module{Name: "beta", CustomerPersonalData: func(Deps) contracts.CustomerPersonalData { return beta }},
	)
	if want := []contracts.CustomerPersonalDataHolder{{Module: "beta", Data: beta}}; !slices.Equal(seen, want) {
		t.Errorf("alpha's workers saw Deps.CustomerPersonalData = %v, want beta's (disabled)", seen)
	}
}

// Worker mode never composes, yet the EHF workers build an invoices server
// from worker-mode Deps (rule 10): Workers collects the invoiced-work slot
// from every module given, a disabled one included, and partitions the
// customer personal data as Compose does, before any module builds its
// workers.
func TestWorkers_CarriesInvoicedWorkAndThePartition(t *testing.T) {
	var log []string
	var seen Deps
	mods := partitionModules(&log, func(string) func(Deps) (http.Handler, error) { return nil })
	mods[1].Workers = func(d Deps) []worker.Worker { // beta, enabled
		seen = d
		return nil
	}
	Workers(Deps{Config: &config.Config{Modules: []string{"beta"}}}, mods...)

	if len(seen.InvoicedWork) != 2 {
		t.Fatalf("beta's workers saw Deps.InvoicedWork = %v, want alpha's and gamma's (both disabled)", seen.InvoicedWork)
	}
	if a, g := seen.InvoicedWork[0].(*fakeInvoicedWork), seen.InvoicedWork[1].(*fakeInvoicedWork); a.name != "alpha" || g.name != "gamma" {
		t.Errorf("Deps.InvoicedWork = [%s %s], want [alpha gamma]", a.name, g.name)
	}
	var names []string
	for _, holder := range seen.CustomerPersonalData {
		names = append(names, holder.Module)
	}
	if want := []string{"beta", "delta", "alpha", "gamma"}; !slices.Equal(names, want) {
		t.Errorf("beta's workers saw Deps.CustomerPersonalData modules = %v, want %v", names, want)
	}
}

// Workers has no error to return, so a kind two modules claim — a bug in the
// binary's own module list, which Compose refuses at startup — panics rather
// than hand a worker an ambiguous slot.
func TestWorkers_PanicsOnAKindClaimedTwice(t *testing.T) {
	holder := func(Deps) contracts.InvoicedWorkHolder {
		return &fakeInvoicedWork{kinds: []contracts.WorkSourceKind{contracts.WorkSourceHours}}
	}
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), `"time.entry"`) {
			t.Errorf("recover() = %v, want a panic naming time.entry", r)
		}
	}()
	Workers(Deps{}, Module{Name: "alpha", InvoicedWork: holder}, Module{Name: "beta", InvoicedWork: holder})
}

// Worker mode never composes, yet a worker writes identity's accounts and
// reads its users (the staff-accounts worker): Workers resolves the Users
// and Accounts slots itself, from the enabled providers, Users first so the
// accounts provider may read it while it is built, before any module builds
// its workers.
func TestWorkers_ResolveTheUsersAndAccountsSlots(t *testing.T) {
	users := &fakeUserDirectory{}
	accounts := &fakeAccounts{name: "identity"}
	var sawUsers contracts.UserDirectory
	var seen Deps
	Workers(Deps{Config: &config.Config{Modules: []string{"alpha"}}},
		Module{Name: "identity", Users: func(Deps) contracts.UserDirectory { return users },
			Accounts: func(d Deps) contracts.AccountProvisioning {
				sawUsers = d.Users
				return accounts
			}},
		Module{Name: "alpha", Workers: func(d Deps) []worker.Worker {
			seen = d
			return nil
		}},
	)
	if seen.Users != users || seen.Accounts != accounts {
		t.Errorf("alpha's workers saw Users %v and Accounts %v, want identity's", seen.Users, seen.Accounts)
	}
	if sawUsers != users {
		t.Errorf("the accounts provider saw Deps.Users = %v, want the user directory resolved first", sawUsers)
	}
}

// A disabled module's provider fills no slot in worker mode either, and a
// value preset on Deps survives when no enabled module provides one.
func TestWorkers_LeaveAPresetSlotWhenNoEnabledModuleProvides(t *testing.T) {
	preset := &fakeAccounts{name: "preset"}
	var seen Deps
	Workers(Deps{Config: &config.Config{Modules: []string{"alpha"}}, Accounts: preset},
		Module{Name: "alpha", Workers: func(d Deps) []worker.Worker {
			seen = d
			return nil
		}},
		Module{Name: "beta", Accounts: func(Deps) contracts.AccountProvisioning { return &fakeAccounts{name: "beta"} }},
	)
	if seen.Accounts != preset {
		t.Errorf("Deps.Accounts = %v, want the preset (beta is disabled)", seen.Accounts)
	}
}

// Workers has no error to return, so two enabled modules providing one
// single-provider slot — a bug in the binary's module list, which Compose
// refuses at startup — panics naming both, as a kind claimed twice does.
func TestWorkers_PanicsOnTwoProvidersOfASlot(t *testing.T) {
	for _, tc := range []struct {
		name string
		mods []Module
		want string
	}{
		{"users", []Module{
			{Name: "alpha", Users: func(Deps) contracts.UserDirectory { return &fakeUserDirectory{} }},
			{Name: "beta", Users: func(Deps) contracts.UserDirectory { return &fakeUserDirectory{} }},
		}, "a user directory"},
		{"accounts", []Module{
			{Name: "alpha", Accounts: func(Deps) contracts.AccountProvisioning { return &fakeAccounts{} }},
			{Name: "beta", Accounts: func(Deps) contracts.AccountProvisioning { return &fakeAccounts{} }},
		}, "account provisioning"},
		{"workforce", []Module{
			{Name: "alpha", Workforce: func(Deps) contracts.Workforce { return &fakeWorkforce{} }},
			{Name: "beta", Workforce: func(Deps) contracts.Workforce { return &fakeWorkforce{} }},
		}, "a workforce directory"},
		{"mailbox calendar", []Module{
			{Name: "alpha", MailboxCalendar: func(Deps) contracts.MailboxCalendar { return &fakeMailboxCalendar{} }},
			{Name: "beta", MailboxCalendar: func(Deps) contracts.MailboxCalendar { return &fakeMailboxCalendar{} }},
		}, "a mailbox calendar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := fmt.Sprint(recover())
				if !strings.Contains(r, tc.want) || !strings.Contains(r, "alpha") || !strings.Contains(r, "beta") {
					t.Errorf("recover() = %v, want a panic naming %s, alpha and beta", r, tc.want)
				}
			}()
			Workers(Deps{}, tc.mods...)
		})
	}
}

// Worker mode never composes, yet energy's import worker resolves a file's
// organisation numbers through the customer directory (energy import design
// D4): Workers resolves the slot as Compose does — from the enabled module
// that declares one, never a disabled one — before any module builds its
// workers, and leaves the caller's own when none is enabled.
func TestWorkers_HandTheWorkersTheCustomerDirectory(t *testing.T) {
	enabled, disabled, preset := &fakeDirectory{}, &fakeDirectory{}, &fakeDirectory{}
	var seen contracts.CustomerDirectory
	reader := Module{Name: "reader", Workers: func(d Deps) []worker.Worker {
		seen = d.Directory
		return nil
	}}
	Workers(Deps{Config: &config.Config{Modules: []string{"reader", "owner"}}},
		reader, Module{Name: "owner", Directory: func(Deps) contracts.CustomerDirectory { return enabled }})
	if seen != enabled {
		t.Errorf("the workers saw Deps.Directory = %v, want the enabled owner's", seen)
	}
	Workers(Deps{Config: &config.Config{Modules: []string{"reader"}}, Directory: preset},
		reader, Module{Name: "owner", Directory: func(Deps) contracts.CustomerDirectory { return disabled }})
	if seen != preset {
		t.Errorf("the workers saw Deps.Directory = %v, want the caller's, the owner being disabled", seen)
	}
}

// Worker mode never composes, so Workers resolves the Workforce slot itself,
// as Compose does: from the enabled provider, after the business calendar so
// the provider may read it while it is built, and the caller's own when no
// enabled module provides one.
func TestWorkers_ResolveTheWorkforceSlot(t *testing.T) {
	var built *fakeWorkforce
	var seen contracts.Workforce
	reader := Module{Name: "reader", Workers: func(d Deps) []worker.Worker {
		seen = d.Workforce
		return nil
	}}
	owner := Module{Name: "owner", Calendar: func(Deps) contracts.BusinessCalendar { return fakeBusinessCalendar{} },
		Workforce: func(d Deps) contracts.Workforce {
			built = &fakeWorkforce{name: "owner", calendar: d.Calendar}
			return built
		}}
	Workers(Deps{Config: &config.Config{Modules: []string{"reader", "owner"}}}, reader, owner)
	if built == nil || seen != built {
		t.Errorf("the workers saw Deps.Workforce = %v, want the enabled owner's", seen)
	}
	if built != nil && built.calendar == nil {
		t.Errorf("the workforce provider was built without Deps.Calendar; want the business calendar resolved before it")
	}
	preset := &fakeWorkforce{name: "preset"}
	Workers(Deps{Config: &config.Config{Modules: []string{"reader"}}, Workforce: preset}, reader, owner)
	if seen != preset {
		t.Errorf("the workers saw Deps.Workforce = %v, want the caller's, the owner being disabled", seen)
	}
}

// Worker mode never composes, so Workers resolves the MailboxCalendar slot
// itself, as Compose does (calendar Outlook sync design O4): from the enabled
// provider, and the caller's own when no enabled module provides one.
func TestWorkers_ResolveTheMailboxCalendarSlot(t *testing.T) {
	built, preset := &fakeMailboxCalendar{key: "owner"}, &fakeMailboxCalendar{key: "preset"}
	var seen contracts.MailboxCalendar
	reader := Module{Name: "reader", Workers: func(d Deps) []worker.Worker {
		seen = d.MailboxCalendar
		return nil
	}}
	owner := Module{Name: "owner", MailboxCalendar: func(Deps) contracts.MailboxCalendar { return built }}
	Workers(Deps{Config: &config.Config{Modules: []string{"reader", "owner"}}, MailboxCalendar: preset}, reader, owner)
	if seen != built {
		t.Errorf("the workers saw Deps.MailboxCalendar = %v, want the enabled owner's", seen)
	}
	Workers(Deps{Config: &config.Config{Modules: []string{"reader"}}, MailboxCalendar: preset}, reader, owner)
	if seen != preset {
		t.Errorf("the workers saw Deps.MailboxCalendar = %v, want the caller's, the owner being disabled", seen)
	}
}

// Worker mode never composes, so Workers collects the calendar sources itself,
// as Compose does: from the enabled modules only (calendar design D1).
func TestWorkers_CollectCalendarSourcesFromEnabledModulesOnly(t *testing.T) {
	alpha := &fakeCalendarSource{name: "alpha"}
	var seen []contracts.CalendarSource
	Workers(Deps{Config: &config.Config{Modules: []string{"alpha"}}},
		Module{Name: "alpha", CalendarSource: func(Deps) contracts.CalendarSource { return alpha }, Workers: func(d Deps) []worker.Worker {
			seen = d.CalendarSources
			return nil
		}},
		Module{Name: "beta", CalendarSource: func(Deps) contracts.CalendarSource { return &fakeCalendarSource{name: "beta"} }},
	)
	if want := []contracts.CalendarSource{alpha}; !slices.Equal(seen, want) {
		t.Errorf("alpha's workers saw Deps.CalendarSources = %v, want alpha's alone (beta is disabled)", seen)
	}
}

// Worker mode never composes, so Workers collects the identity provisioners
// itself, as Compose does: from the enabled modules only (Entra design D6),
// after a preset list, which Staff's accounts worker then drives.
func TestWorkers_CollectIdentityProvisionersFromEnabledModulesOnly(t *testing.T) {
	alpha := &fakeIdentityProvisioner{key: "alpha-directory"}
	preset := &fakeIdentityProvisioner{key: "preset-directory"}
	var seen []contracts.IdentityProvisioner
	Workers(Deps{Config: &config.Config{Modules: []string{"alpha"}}, IdentityProvisioners: []contracts.IdentityProvisioner{preset}},
		Module{Name: "alpha", IdentityProvisioner: func(Deps) contracts.IdentityProvisioner { return alpha }, Workers: func(d Deps) []worker.Worker {
			seen = d.IdentityProvisioners
			return nil
		}},
		Module{Name: "beta", IdentityProvisioner: func(Deps) contracts.IdentityProvisioner { return &fakeIdentityProvisioner{key: "beta-directory"} }},
	)
	if want := []contracts.IdentityProvisioner{preset, alpha}; !slices.Equal(seen, want) {
		t.Errorf("alpha's workers saw Deps.IdentityProvisioners = %v, want the preset and alpha's alone (beta is disabled)", seen)
	}
}

// Workers has no error to return, so a key two providers claim — a bug in
// the binary's own module list, which Compose refuses at startup — panics
// rather than hand the accounts worker an ambiguous slot.
func TestWorkers_PanicsOnAnIdentityProviderKeyClaimedTwice(t *testing.T) {
	provider := func(Deps) contracts.IdentityProvisioner { return &fakeIdentityProvisioner{key: "entra"} }
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), `"entra"`) {
			t.Errorf("recover() = %v, want a panic naming entra", r)
		}
	}()
	Workers(Deps{}, Module{Name: "alpha", IdentityProvisioner: provider}, Module{Name: "beta", IdentityProvisioner: provider})
}

// Workers panics on a provider under a reserved key, as Compose refuses it.
func TestWorkers_PanicsOnAReservedIdentityProviderKey(t *testing.T) {
	provider := func(Deps) contracts.IdentityProvisioner { return &fakeIdentityProvisioner{key: "identity"} }
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "alpha") || !strings.Contains(fmt.Sprint(r), "reserved") {
			t.Errorf("recover() = %v, want a panic naming alpha and the reserved key", r)
		}
	}()
	Workers(Deps{}, Module{Name: "alpha", IdentityProvisioner: provider})
}

// Worker mode never composes, so Workers resolves the project directory, the
// three billable reads and consumption itself, as Compose does (energy
// consumption billing design D10): from the enabled providers, the billable
// ones built after the project directory and consumption after them, and the
// caller's own when no enabled module provides one. The invoices-billing
// worker drafts and issues through code that reads them.
func TestWorkers_ResolveTheProjectsBillableAndConsumptionSlots(t *testing.T) {
	var seen Deps
	reader := Module{Name: "reader", Workers: func(d Deps) []worker.Worker {
		seen = d
		return nil
	}}
	projects := &fakeProjectDirectory{}
	var hours *fakeBillableHours
	var consumption *fakeConsumption
	expenses, milestones := &fakeBillableExpenses{}, &fakeBillableMilestones{}
	owner := Module{Name: "owner",
		Projects: func(Deps) contracts.ProjectDirectory { return projects },
		BillableHours: func(d Deps) contracts.BillableHours {
			hours = &fakeBillableHours{projects: d.Projects}
			return hours
		},
		BillableExpenses:   func(Deps) contracts.BillableExpenses { return expenses },
		BillableMilestones: func(Deps) contracts.BillableMilestones { return milestones },
	}
	energy := Module{Name: "energy", Consumption: func(d Deps) contracts.ConsumptionProvider {
		consumption = &fakeConsumption{milestones: d.BillableMilestones}
		return consumption
	}}
	Workers(Deps{Config: &config.Config{Modules: []string{"reader", "owner", "energy"}}}, reader, owner, energy)
	if seen.Projects != projects {
		t.Errorf("the workers saw Deps.Projects = %v, want the enabled owner's", seen.Projects)
	}
	if hours == nil || seen.BillableHours != hours || seen.BillableExpenses != expenses || seen.BillableMilestones != milestones {
		t.Errorf("the workers saw the billable reads %v, %v, %v; want the enabled owner's", seen.BillableHours, seen.BillableExpenses, seen.BillableMilestones)
	}
	if hours != nil && hours.projects != projects {
		t.Errorf("billable hours were built without Deps.Projects; want the project directory resolved before them")
	}
	if consumption == nil || seen.Consumption != consumption {
		t.Errorf("the workers saw Deps.Consumption = %v, want the enabled energy module's", seen.Consumption)
	}
	if consumption != nil && consumption.milestones != milestones {
		t.Errorf("consumption was built before the billable reads; want it resolved last")
	}

	presetProjects, presetConsumption := &fakeProjectDirectory{}, &fakeConsumption{}
	Workers(Deps{Config: &config.Config{Modules: []string{"reader"}}, Projects: presetProjects, Consumption: presetConsumption},
		reader, owner, energy)
	if seen.Projects != presetProjects || seen.Consumption != presetConsumption || seen.BillableHours != nil {
		t.Errorf("the workers saw Projects %v, Consumption %v, BillableHours %v; want the caller's two and none, the providers being disabled",
			seen.Projects, seen.Consumption, seen.BillableHours)
	}
}

// Two enabled providers of one of the slots Workers learned for the billing
// worker are a bug in the binary's module list, which Compose refuses at
// startup; Workers has no error to return, so it panics, naming the slot.
func TestWorkers_PanicsOnTwoProvidersOfABillingSlot(t *testing.T) {
	cases := []struct {
		what    string
		declare func(*Module)
	}{
		{"a project directory", func(m *Module) {
			m.Projects = func(Deps) contracts.ProjectDirectory { return &fakeProjectDirectory{} }
		}},
		{"billable hours", func(m *Module) {
			m.BillableHours = func(Deps) contracts.BillableHours { return &fakeBillableHours{} }
		}},
		{"consumption", func(m *Module) {
			m.Consumption = func(Deps) contracts.ConsumptionProvider { return &fakeConsumption{} }
		}},
	}
	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			alpha, beta := Module{Name: "alpha"}, Module{Name: "beta"}
			tc.declare(&alpha)
			tc.declare(&beta)
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("Workers: want a panic when two modules declare %s", tc.what)
				}
				if msg := fmt.Sprint(r); !strings.Contains(msg, tc.what) {
					t.Errorf("panic %q does not say %q", msg, tc.what)
				}
			}()
			Workers(Deps{Config: &config.Config{Modules: []string{"alpha", "beta"}}}, alpha, beta)
		})
	}
}
