package live

import (
	"context"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// accessContextKey carries the Access a Topic's Authorize asks through.
type accessContextKey struct{}

func withAccess(ctx context.Context, a contracts.Access) context.Context {
	return context.WithValue(ctx, accessContextKey{}, a)
}

// HasPermission reports whether the person a Topic's Authorize is asked for
// holds every key: contracts.HasPermissions through the hub's Access, with the
// context's contracts.AsUser. During the 60-second re-check that Access
// caches UserHolds per user and keys for the cycle (L11), so many
// subscriptions on one permission cost one read. Outside an Authorize call it
// is false: it fails closed.
func HasPermission(ctx context.Context, keys ...string) bool {
	a, ok := ctx.Value(accessContextKey{}).(contracts.Access)
	if !ok {
		return false
	}
	return contracts.HasPermissions(ctx, a, keys...)
}

// stationContextKey carries the station a station socket's Authorize is asked
// for.
type stationContextKey struct{}

// StationID is the shared shop-floor station a Topic's Authorize is asked for
// (manufacturing phase 2 §17), and false for a person's socket. A topic marked
// Station decides from it which keys the station may hear.
func StationID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(stationContextKey{}).(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// cycleAccess is an Access whose UserHolds answers are remembered for its
// lifetime: one subscription, or one re-check of a connection.
type cycleAccess struct {
	contracts.Access
	mu    sync.Mutex
	held  map[string]bool
	reads int // UserHolds calls that reached the inner Access, for a test
}

func newCycleAccess(a contracts.Access) *cycleAccess {
	return &cycleAccess{Access: a, held: map[string]bool{}}
}

func (c *cycleAccess) UserHolds(ctx context.Context, userID uuid.UUID, keys ...string) (bool, error) {
	key := userID.String() + "\x00" + strings.Join(keys, "\x00")
	c.mu.Lock()
	if held, ok := c.held[key]; ok {
		c.mu.Unlock()
		return held, nil
	}
	c.mu.Unlock()
	held, err := c.Access.UserHolds(ctx, userID, keys...)
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	c.held[key] = held
	c.reads++
	c.mu.Unlock()
	return held, nil
}
