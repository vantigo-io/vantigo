package identity

import (
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, identity's contract
// operations. Each area implements its operations as methods in its own
// file, and unimplemented.go stubs the rest, so the build proves the
// interface is complete.
type server struct {
	access *Access
	deps   module.Deps
}

var _ gen.StrictServerInterface = (*server)(nil)

func newServer(a *Access, d module.Deps) *server {
	return &server{access: a, deps: d}
}
