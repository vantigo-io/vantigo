package communications

import (
	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations as methods in its own
// file; unimplemented.go holds the stubs of every area not yet built, so
// the build itself proves the interface is complete.
type server struct {
	deps module.Deps
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) *server {
	return &server{deps: d}
}

// ptr returns a pointer to a copy of v, for the optional fields of a
// generated response type (the same helper customers/server.go and
// identity/scim_input.go each carry — depguard forbids sharing it across
// modules for one line of code).
func ptr[T any](v T) *T { return &v }
