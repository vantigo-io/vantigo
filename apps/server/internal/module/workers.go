package module

import "github.com/vantigo-io/vantigo/server/internal/worker"

// Workers returns every worker.Worker the enabled modules among mods
// contribute, in mods order, each module's own contribution concatenated in
// the order Module.Workers returns it. A module with a nil Workers field
// contributes none; a disabled module (one enabledModules drops, the same
// enablement Compose itself applies) contributes none either, whether or
// not it declares any.
//
// Unlike Directory, resolving Workers does not require Compose: worker mode
// never mounts the HTTP API, so cmd/vantigo calls Workers directly instead
// of reaching it through Compose's return value. It resolves each module's
// contribution from deps as the caller passes it, plus Deps.CustomerPersonalData,
// which it collects the way Compose does — the same base Deps Compose
// resolves Directory from, before any Mount runs, with no per-module Doc or
// Catalog — since a background worker is a data-layer concern (Pool, Config,
// Clock, Mail, Secrets, ...), not a contract one.
func Workers(deps Deps, mods ...Module) []worker.Worker {
	// Every module given, enabled or not, before enablement drops any: the
	// anonymisation worker is where contracts.CustomerPersonalData's erase
	// runs (customers GDPR design D2), and worker mode never composes, so
	// nothing else would put the slot on its Deps.
	deps = withCustomerPersonalData(deps, mods)
	var out []worker.Worker
	for _, mod := range enabledModules(deps, mods) {
		if mod.Workers == nil {
			continue
		}
		out = append(out, mod.Workers(deps)...)
	}
	return out
}
