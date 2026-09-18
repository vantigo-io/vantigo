package timetracking

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/time/gen"
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
// rendered under, so an entry of a since-deleted account stays readable
// rather than blank.
const unknownUser = "Unknown user"

// unknownProject is the name a project the directory cannot resolve is
// rendered under. Projects cannot be deleted, so this only ever shows for a
// directory that has lost a row it once had; an entry still renders rather
// than failing the whole read.
const unknownProject = "Unknown project"

// callerID is the signed-in caller of one request. The router has already
// authenticated every operation (time:access), so a handler always has one.
func callerID(ctx context.Context) uuid.UUID {
	p, _ := contracts.PrincipalFrom(ctx)
	return p.UserID
}

// userEntries resolves ids through contracts.UserDirectory — the only way
// this module may read identity's users — in one call, answering an entry
// for every id: one the directory does not know gets unknownUser, inactive,
// so an entry survives the account it belongs to.
func (s *server) userEntries(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]contracts.UserEntry, error) {
	entries := make(map[uuid.UUID]contracts.UserEntry, len(ids))
	if len(ids) == 0 {
		return entries, nil
	}
	found, err := s.deps.Users.Users(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("time: resolve user display names: %w", err)
	}
	for _, e := range found {
		entries[e.ID] = e
	}
	for _, id := range ids {
		if _, ok := entries[id]; !ok {
			entries[id] = contracts.UserEntry{ID: id, DisplayName: unknownUser}
		}
	}
	return entries, nil
}

// has reports whether the caller holds one global permission key, evaluated
// the way the router evaluates an operation's rule. It fails closed.
func (s *server) has(ctx context.Context, key string) bool {
	return contracts.HasPermission(ctx, s.deps.Access, key)
}
