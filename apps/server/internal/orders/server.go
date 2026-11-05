package orders

import (
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations in its own file: quotes.go,
// templates.go and settings.go (the quotes lane); orders.go and summary.go
// (the orders lane). The transaction helpers — a locked READ COMMITTED
// transaction for every write, a REPEATABLE READ snapshot for a read, and the
// marks the contract accessors refuse a call out of the module under — come
// with the first operation that writes, in Inventory's shape
// (internal/inventory/server.go).
type server struct {
	deps module.Deps
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) *server { return &server{deps: d} }
