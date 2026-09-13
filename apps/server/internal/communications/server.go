package communications

import (
	"fmt"

	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// storageScope is this module's IStorageScope name
// (IS/CommunicationsStorageScope.cs:5-8, docs/storage.md "communications"
// example, inventory §16.1): every key attachments.go passes to store is
// combined as "communications/{key}" before reaching the configured
// backend.
const storageScope = "communications"

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations as methods in its own
// file; unimplemented.go holds the stubs of every area not yet built, so
// the build itself proves the interface is complete.
type server struct {
	deps  module.Deps
	store storage.ObjectStore
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d. store is d.ObjectStore
// when a test harness set one (see module.Deps.ObjectStore); otherwise it is
// this module's own scoped wrapper over internal/storage.New(d.Config) — the
// production path, which fails only when Config.StorageProvider is "fs" and
// the configured root cannot actually be opened (an unset provider is not an
// error here: the process starts and every storage operation fails closed
// with storage.ErrNotConfigured instead, docs/storage.md). d.Config is nil
// in the handful of white-box tests that build a bare module.Deps to probe
// routing rather than storage (module_internal_test.go); an empty
// *config.Config stands in for it, the same StorageProvider="" unconfigured
// shape a real deployment with no STORAGE_PROVIDER gets.
func newServer(d module.Deps) (*server, error) {
	store := d.ObjectStore
	if store == nil {
		cfg := d.Config
		if cfg == nil {
			cfg = &config.Config{}
		}
		base, err := storage.New(cfg)
		if err != nil {
			return nil, fmt.Errorf("communications: build object store: %w", err)
		}
		scoped, err := storage.NewScope(base, storageScope)
		if err != nil {
			return nil, fmt.Errorf("communications: scope object store: %w", err)
		}
		store = scoped
	}
	return &server{deps: d, store: store}, nil
}

// ptr returns a pointer to a copy of v, for the optional fields of a
// generated response type (the same helper customers/server.go and
// identity/scim_input.go each carry — depguard forbids sharing it across
// modules for one line of code).
func ptr[T any](v T) *T { return &v }
