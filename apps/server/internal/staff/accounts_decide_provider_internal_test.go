package staff

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// TestDecideProvider is D7's column row by row: a person of a provider's
// kind, the provider connected unless the row says otherwise, today
// 2026-10-18, an employment from 2026-10-01, createBeforeDays 14.
func TestDecideProvider(t *testing.T) {
	ext := "obj-1"
	span := func(start, end string) decideEmployment {
		e := decideEmployment{Start: day(t, start)}
		if end != "" {
			d := day(t, end)
			e.End = &d
		}
		return e
	}
	account := func(state string) *contracts.ExternalAccount {
		return &contracts.ExternalAccount{ExternalID: ext, State: state}
	}
	link := func(state string, announced bool) *providerLinkFacts {
		return &providerLinkFacts{State: state, ExternalID: &ext, Announced: announced}
	}
	withAccount := func(state string) func(*providerDecision) {
		return func(in *providerDecision) { in.Link, in.Account = link(state, true), account(state) }
	}
	employed := func(spans ...decideEmployment) func(*providerDecision) {
		return func(in *providerDecision) { in.Person.Employments = spans }
	}
	all := func(fns ...func(*providerDecision)) func(*providerDecision) {
		return func(in *providerDecision) {
			for _, fn := range fns {
				fn(in)
			}
		}
	}
	days := func(n int) *int { return &n }
	step := func(kind accountStepKind) accountStep { return accountStep{Kind: kind} }
	report := func(reason, state string) accountStep {
		return accountStep{Kind: accountStepReport, Reason: reason, State: state}
	}
	none := step(accountStepNone)

	for _, c := range []struct {
		name string
		edit func(*providerDecision)
		want accountStep
	}{
		{"provider unconfigured, no link", func(in *providerDecision) { in.Status = contracts.ProviderUnconfigured }, none},
		{"provider unconfigured, a link", all(withAccount(providerStateDisabled), func(in *providerDecision) { in.Status = contracts.ProviderUnconfigured }),
			report(reasonProviderUnavailable, "")},
		{"provider failing, a link", all(withAccount(providerStateEnabled), func(in *providerDecision) { in.Status = contracts.ProviderFailing }),
			report(reasonProviderUnavailable, "")},
		{"provider failing, a leaver enabled: still deactivated", all(employed(span("2026-01-01", "2026-10-10")), withAccount(providerStateEnabled),
			func(in *providerDecision) { in.Status = contracts.ProviderFailing }), step(accountStepDeactivate)},
		{"flag off, no link", func(in *providerDecision) { in.Person.Flag = false }, none},
		{"flag off, an account, employed", all(withAccount(providerStateEnabled), func(in *providerDecision) { in.Person.Flag = false }), none},
		{"flag on, no employment, no link", employed(), none},
		{"flag on, no employment, a link", all(employed(), withAccount(providerStateDisabled)), report(reasonNoEmployment, "")},
		{"before the window", employed(span("2026-11-15", "")), none},
		{"the window, no link", employed(span("2026-10-25", "")), step(accountStepPrepare)},
		{"the window, prepared, not announced", all(employed(span("2026-10-25", "")), func(in *providerDecision) {
			in.Link, in.Account = link(providerStateDisabled, false), account(providerStateDisabled)
		}), step(accountStepAnnounce)},
		{"the window, prepared, announced", all(employed(span("2026-10-25", "")), withAccount(providerStateDisabled)), none},
		{"the window, not announced, the user arrived", all(employed(span("2026-10-25", "")), func(in *providerDecision) {
			in.Link, in.Account, in.Arrived = link(providerStateDisabled, false), account(providerStateDisabled), true
		}), none},
		{"due, no link", nil, step(accountStepPrepare)},
		{"due, a link with no account yet (blocked)", func(in *providerDecision) { in.Link = &providerLinkFacts{State: providerStateBlocked} },
			step(accountStepPrepare)},
		{"due, disabled", withAccount(providerStateDisabled), step(accountStepActivate)},
		{"due, disabled, enabled today by the worker", all(withAccount(providerStateDisabled), func(in *providerDecision) { in.Link.EnabledToday = true }), none},
		{"due, enabled", withAccount(providerStateEnabled), none},
		{"due, enabled, the spec changed", all(withAccount(providerStateEnabled), func(in *providerDecision) { in.SpecChanged = true }), step(accountStepUpdate)},
		// Run now asks a new pass only on the activation that enables the
		// account (review M2): never over an account already enabled.
		{"due, enabled, Run now: no new pass", all(withAccount(providerStateEnabled), func(in *providerDecision) { in.Fresh = true }), none},
		{"due, enabled, the spec changed, Run now: the update alone", all(withAccount(providerStateEnabled),
			func(in *providerDecision) { in.Fresh, in.SpecChanged = true, true }), step(accountStepUpdate)},
		{"due, disabled, Run now: the enable with a new pass", all(withAccount(providerStateDisabled), func(in *providerDecision) { in.Fresh = true }),
			accountStep{Kind: accountStepActivate, Fresh: true}},
		{"due, the account gone", func(in *providerDecision) { in.Link = link(providerStateEnabled, true) }, report(reasonUserGone, providerStateFailed)},
		{"due, the account gone, failed already: never re-created", func(in *providerDecision) { in.Link = link(providerStateFailed, true) },
			report(reasonUserGone, providerStateFailed)},
		{"due, the link deleted (a rehire after the delete)", func(in *providerDecision) { in.Link = link(providerStateDeleted, true) }, step(accountStepPrepare)},
		{"leaver, enabled", all(employed(span("2026-01-01", "2026-10-10")), withAccount(providerStateEnabled)), step(accountStepDeactivate)},
		{"leaver, enabled, flag off", all(employed(span("2026-01-01", "2026-10-10")), withAccount(providerStateEnabled),
			func(in *providerDecision) { in.Person.Flag = false }), step(accountStepDeactivate)},
		{"leaver, disabled, no delete setting", all(employed(span("2026-01-01", "2026-10-10")), withAccount(providerStateDisabled)), none},
		{"leaver, disabled, before the delete", all(employed(span("2026-01-01", "2026-10-10")), withAccount(providerStateDisabled),
			func(in *providerDecision) { in.DeleteAfterDays = days(30) }), none},
		{"leaver, disabled, the delete due", all(employed(span("2026-01-01", "2026-09-10")), withAccount(providerStateDisabled),
			func(in *providerDecision) { in.DeleteAfterDays = days(30) }), step(accountStepDelete)},
		{"leaver, disabled, the delete due, a legal hold", all(employed(span("2026-01-01", "2026-09-10")), withAccount(providerStateDisabled),
			func(in *providerDecision) { in.DeleteAfterDays, in.LegalHold = days(30), true }), none},
		{"leaver, deleted", all(employed(span("2026-01-01", "2026-09-10")), withAccount(providerStateDeleted),
			func(in *providerDecision) { in.DeleteAfterDays = days(30) }), none},
		{"between employments, before the later's window, enabled", all(employed(span("2026-01-01", "2026-10-10"), span("2026-12-01", "")),
			withAccount(providerStateEnabled)), step(accountStepDeactivate)},
		{"between employments, disabled: never deleted", all(employed(span("2025-01-01", "2025-10-10"), span("2026-12-01", "")),
			withAccount(providerStateDisabled), func(in *providerDecision) { in.DeleteAfterDays = days(30) }), none},
		{"rehire: disabled, a new employment due", all(employed(span("2025-01-01", "2025-06-30"), span("2026-10-01", "")),
			withAccount(providerStateDisabled)), step(accountStepActivate)},
		{"Disable now, enabled", all(withAccount(providerStateEnabled), func(in *providerDecision) { in.DisableRequested, in.Person.Flag = true, false }),
			step(accountStepDeactivate)},
		{"Disable now, disabled", all(withAccount(providerStateDisabled), func(in *providerDecision) { in.DisableRequested, in.Person.Flag = true, false }), none},
		{"Disable now, an erased person enabled", all(withAccount(providerStateEnabled), func(in *providerDecision) {
			in.DisableRequested, in.Person.Flag, in.Person.Erased = true, false, true
		}), step(accountStepDeactivate)},
		{"erased", all(withAccount(providerStateEnabled), func(in *providerDecision) { in.Person.Erased = true }), none},
	} {
		in := providerDecision{
			Person: accountPersonFacts{
				Kind: contracts.AccountKindEntra, Flag: true, EmployeeNumber: "A-1",
				Employments: []decideEmployment{span("2026-10-01", "")},
			},
			Status: contracts.ProviderConnected, CreateBeforeDays: 14, Today: day(t, "2026-10-18"),
		}
		if c.edit != nil {
			c.edit(&in)
		}
		if got := decideProvider(in); got != c.want {
			t.Errorf("%s: decideProvider = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestDecideProvider_Purity pins that the provider column is pure as decide
// is: accounts_decide_provider.go imports the time package and the
// contracts only, never touches the clock, declares no method and names no
// provider interface.
func TestDecideProvider_Purity(t *testing.T) {
	const name = "accounts_decide_provider.go"
	allowed := map[string]bool{"time": true, "github.com/vantigo-io/vantigo/server/internal/contracts": true}
	clock := map[string]bool{"Now": true, "Since": true, "Until": true, "After": true, "AfterFunc": true, "Tick": true, "NewTicker": true, "NewTimer": true, "Sleep": true, "Local": true}
	providers := map[string]bool{"AccountProvisioning": true, "UserDirectory": true, "IdentityProvisioner": true}
	f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	timeName, contractsName := "", ""
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if !allowed[path] {
			t.Errorf("%s imports %q; the provider column imports only %v", name, path, allowed)
		}
		local := lastElem(path)
		if imp.Name != nil {
			local = imp.Name.Name
		}
		switch path {
		case "time":
			timeName = local
		case "github.com/vantigo-io/vantigo/server/internal/contracts":
			contractsName = local
		}
	}
	found := false
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			if fn.Recv != nil {
				t.Errorf("%s declares the method %s; the provider column is functions of their inputs", name, fn.Name.Name)
			}
			found = found || fn.Name.Name == "decideProvider"
		}
	}
	if !found {
		t.Errorf("%s declares no decideProvider", name)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if timeName != "" && pkg.Name == timeName && clock[sel.Sel.Name] {
			t.Errorf("%s uses time.%s; today is an input", name, sel.Sel.Name)
		}
		if contractsName != "" && pkg.Name == contractsName && providers[sel.Sel.Name] {
			t.Errorf("%s names contracts.%s; decideProvider calls no provider", name, sel.Sel.Name)
		}
		return true
	})
}

// TestDecideProvider_TheDeleteDay pins the delete's day: the setting's days
// after the last end, judged on civil days.
func TestDecideProvider_TheDeleteDay(t *testing.T) {
	end := day(t, "2026-09-18")
	employments := []decideEmployment{{Start: day(t, "2026-01-01"), End: &end}}
	thirty := 30
	for _, c := range []struct {
		today string
		want  bool
	}{{"2026-10-17", false}, {"2026-10-18", true}, {"2026-10-19", true}} {
		if got := providerDeleteDue(employments, day(t, c.today), &thirty); got != c.want {
			t.Errorf("on %s: delete due = %v, want %v", c.today, got, c.want)
		}
	}
	if providerDeleteDue(employments, day(t, "2030-01-01"), nil) {
		t.Error("no setting: delete due, want never")
	}
}
