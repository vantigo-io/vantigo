package projects

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/projects/gen"
)

// server implements gen.StrictServerInterface, the module's contract
// operations. Each area implements its operations as methods in its own
// file.
type server struct {
	deps module.Deps
}

var _ gen.StrictServerInterface = (*server)(nil)

// newServer builds the module's operations over d.
func newServer(d module.Deps) *server { return &server{deps: d} }

// unknownUser is the display name a user the directory cannot resolve is
// recorded and rendered under. A timeline entry keeps the name it was
// written with forever, so a deleted account leaves a readable history
// rather than a blank one.
const unknownUser = "Unknown user"

// actor is the signed-in caller of one request, as the timeline records
// them: the id for machine use and the display name captured at the moment
// of the change.
type actor struct {
	UserID  uuid.UUID
	Display string
}

// callerAs resolves the request's principal into an actor, naming them
// through contracts.UserDirectory — the only way this module may read
// identity's users. A user the directory does not know is not an error: the
// change still happened, and unknownUser records that it did.
func (s *server) callerAs(ctx context.Context) (actor, error) {
	p, _ := contracts.PrincipalFrom(ctx)
	entry, err := s.deps.Users.User(ctx, p.UserID)
	if err != nil {
		return actor{}, fmt.Errorf("projects: resolve the caller: %w", err)
	}
	a := actor{UserID: p.UserID, Display: unknownUser}
	if entry != nil {
		a.Display = entry.DisplayName
	}
	return a, nil
}

// displayNames resolves ids to display names in one directory call, falling
// back to unknownUser for an id the directory does not know — an assignment
// of a since-deleted account stays readable (design §4.1).
func (s *server) displayNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	names := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return names, nil
	}
	entries, err := s.deps.Users.Users(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("projects: resolve user display names: %w", err)
	}
	for _, e := range entries {
		names[e.ID] = e.DisplayName
	}
	for _, id := range ids {
		if _, ok := names[id]; !ok {
			names[id] = unknownUser
		}
	}
	return names, nil
}

// billingLinesAvailable reports whether this installation has the products
// module enabled, and so whether a project can carry priced billing lines at
// all (D9/D10). Deps.Products is the optional contract: nil when products is
// disabled. It is answered on every project response so the frontend never
// has to ask separately.
func (s *server) billingLinesAvailable() bool { return s.deps.Products != nil }
