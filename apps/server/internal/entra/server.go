package entra

import (
	"github.com/vantigo-io/vantigo/server/internal/entra/gen"
	"github.com/vantigo-io/vantigo/server/internal/entra/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, the module's contract
// operations: the connection's in connection.go, the secrets' in
// credentials.go, the events' and the accounts' in events.go.
type server struct {
	deps   module.Deps
	q      *store.Queries
	client *graphClient
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) *server {
	return &server{deps: d, q: store.New(d.Pool), client: newGraphClient(d)}
}
