package meta

import (
	"github.com/vantigo-io/vantigo/server/internal/meta/gen"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, the module's contract
// operations: the settings' in settings_ops.go, the Pages' in pages.go and the
// webhook's in webhook.go.
type server struct {
	core
	graph *graphClient
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) *server {
	return &server{core: newCore(d), graph: newGraphClient(d)}
}
