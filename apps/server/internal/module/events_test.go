package module

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/events"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

func eventHandlers(names ...string) func(Deps) []events.Handler {
	return func(Deps) []events.Handler {
		out := make([]events.Handler, 0, len(names))
		for _, name := range names {
			out = append(out, events.Handler{
				Name:   name,
				Events: []events.EventRef{{Name: "alpha.thing.happened", Version: 1}},
				Handle: func(context.Context, pgx.Tx, events.Delivered) error { return nil },
			})
		}
		return out
	}
}

// TestEventHandlers_CollectsEveryModuleGiven pins the slot: every module's
// handlers, a disabled module's too, in the order given (orders design O2).
func TestEventHandlers_CollectsEveryModuleGiven(t *testing.T) {
	deps := Deps{Config: &config.Config{Modules: []string{"alpha"}}}
	got, err := EventHandlers(deps,
		Module{Name: "alpha", EventHandlers: eventHandlers("alpha.one", "alpha.two")},
		Module{Name: "beta", EventHandlers: eventHandlers("beta.one")},
		Module{Name: "gamma"},
	)
	if err != nil {
		t.Fatalf("EventHandlers: %v", err)
	}
	var names []string
	for _, h := range got {
		names = append(names, h.Name)
	}
	if strings.Join(names, ",") != "alpha.one,alpha.two,beta.one" {
		t.Errorf("handlers = %v, want alpha's two and disabled beta's", names)
	}
}

// TestEventHandlers_RefusesADuplicateName pins the refusal, naming both
// modules, in EventHandlers and so in Compose.
func TestEventHandlers_RefusesADuplicateName(t *testing.T) {
	mods := []Module{
		{Name: "alpha", EventHandlers: eventHandlers("shared.handler"), Mount: staticHandler("alpha")},
		{Name: "beta", EventHandlers: eventHandlers("shared.handler"), Mount: staticHandler("beta")},
	}
	_, err := EventHandlers(Deps{}, mods...)
	if err == nil || !strings.Contains(err.Error(), `"shared.handler"`) || !strings.Contains(err.Error(), "alpha, beta") {
		t.Fatalf("EventHandlers = %v, want a refusal naming the handler and both modules", err)
	}
	// Compose refuses it too, beta being disabled: a disabled module's
	// handlers are composed all the same.
	_, err = compose(Deps{Access: &fakeAccess{}, Config: &config.Config{Modules: []string{"alpha"}}},
		fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}), mods...)
	if err == nil || !strings.Contains(err.Error(), `"shared.handler"`) {
		t.Fatalf("compose = %v, want the duplicate handler refused", err)
	}
	// One module twice over too.
	if _, err := EventHandlers(Deps{}, Module{Name: "alpha", EventHandlers: eventHandlers("a.x", "a.x")}); err == nil {
		t.Error("EventHandlers with one module's duplicate = nil, want an error")
	}
}

// TestEventHandlers_AndWorkersSeeTheEnabledModulesCatalog pins the catalog a
// worker-mode process hands its handlers and workers, which never compose:
// the enabled modules' permissions, unless the caller set one (outgoing
// webhooks design W7, W33).
func TestEventHandlers_AndWorkersSeeTheEnabledModulesCatalog(t *testing.T) {
	var seen []map[string]contracts.Permission
	capture := func(d Deps) []events.Handler {
		seen = append(seen, d.Catalog)
		return eventHandlers("alpha.one")(d)
	}
	mods := []Module{
		{Name: "alpha", Permissions: []contracts.Permission{{Key: "alpha:access", Delegable: true}}, EventHandlers: capture,
			Workers: func(d Deps) []worker.Worker { seen = append(seen, d.Catalog); return nil }},
		{Name: "beta", Permissions: []contracts.Permission{{Key: "beta:access"}}},
	}
	deps := Deps{Config: &config.Config{Modules: []string{"alpha"}}}
	if _, err := EventHandlers(deps, mods...); err != nil {
		t.Fatal(err)
	}
	Workers(deps, mods...)
	if len(seen) != 2 {
		t.Fatalf("seen %d catalogs", len(seen))
	}
	for _, c := range seen {
		if _, ok := c["alpha:access"]; !ok || len(c) != 1 {
			t.Errorf("catalog %v, want alpha's alone", c)
		}
	}
	seen = nil
	deps.Catalog = map[string]contracts.Permission{"x:y": {}}
	if _, err := EventHandlers(deps, mods...); err != nil {
		t.Fatal(err)
	}
	if _, ok := seen[0]["x:y"]; !ok || len(seen[0]) != 1 {
		t.Errorf("a preset catalog was replaced: %v", seen[0])
	}
}

// TestEventHandlers_RefusesAnIncompleteHandler pins the shape check.
func TestEventHandlers_RefusesAnIncompleteHandler(t *testing.T) {
	for _, h := range []events.Handler{
		{Events: []events.EventRef{{Name: "a.b.c", Version: 1}}, Handle: func(context.Context, pgx.Tx, events.Delivered) error { return nil }},
		{Name: "alpha.none", Handle: func(context.Context, pgx.Tx, events.Delivered) error { return nil }},
		{Name: "alpha.nohandle", Events: []events.EventRef{{Name: "a.b.c", Version: 1}}},
	} {
		_, err := EventHandlers(Deps{}, Module{Name: "alpha", EventHandlers: func(Deps) []events.Handler { return []events.Handler{h} }})
		if err == nil {
			t.Errorf("EventHandlers(%q) = nil, want an error", h.Name)
		}
	}
}
