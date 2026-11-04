package google

import (
	"github.com/vantigo-io/vantigo/server/internal/google/gen"
	"github.com/vantigo-io/vantigo/server/internal/google/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// server implements gen.StrictServerInterface, the module's contract
// operations: the connection's in connection.go, the credentials' in
// credentials.go, the test in test.go and the events' in events.go.
type server struct {
	deps module.Deps
	q    *store.Queries
	keys *keyring
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) *server {
	q := store.New(d.Pool)
	return &server{deps: d, q: q, keys: newKeyring(q, newGoogleClient(d))}
}
