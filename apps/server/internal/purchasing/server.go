package purchasing

import (
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/purchasing/gen"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations in its own file.
type server struct {
	deps module.Deps
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) (*server, error) {
	return &server{deps: d}, nil
}
