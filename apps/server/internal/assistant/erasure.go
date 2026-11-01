package assistant

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/assistant/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// Erasure (the Assistant design S9): when identity erases a person — deletes
// the user, which is what erasing a Staff person's account ends in — the
// person's conversations, messages, runs, actions and switch go with them.
// Grants cascade in identity already.
//
// No module is told that a user was erased, and no module may reach into
// identity's schema, so the module asks the way every module asks about a
// user: contracts.UserDirectory, which answers a user that no longer exists by
// leaving it out. The retention worker's cycle sweeps the users this module
// holds anything of, a chunk at a time, and deletes the rows of those the
// directory no longer knows. A disabled user is still known and is kept: their
// conversations go with the retention, not the switch.
//
// The seam costs one directory read per cycle per 200 users, and it means the
// data of an erased person can outlive the erasure by up to a day. That is the
// retention's own scale, and it keeps the module from learning identity's
// internals. A customer's GDPR erase does not search conversation text:
// retention is the answer (the reference says so).

// sweepErased deletes what the module holds of users identity has erased, and
// answers how many it found.
func sweepErased(ctx context.Context, d module.Deps) (int, error) {
	if d.Users == nil {
		return 0, nil
	}
	q := store.New(d.Pool)
	after := uuid.Nil
	erased := 0
	for {
		ids, err := q.ListHeldUserIDs(ctx, store.ListHeldUserIDsParams{After: after, BatchSize: erasureChunk})
		if err != nil {
			return erased, fmt.Errorf("assistant: list the users held: %w", err)
		}
		if len(ids) == 0 {
			return erased, nil
		}
		known, err := d.Users.Users(ctx, ids)
		if err != nil {
			return erased, fmt.Errorf("assistant: ask which users still exist: %w", err)
		}
		exists := make(map[uuid.UUID]bool, len(known))
		for _, u := range known {
			exists[u.ID] = true
		}
		var gone []uuid.UUID
		for _, id := range ids {
			if !exists[id] {
				gone = append(gone, id)
			}
		}
		if len(gone) > 0 {
			if err := q.DeleteUserData(ctx, gone); err != nil {
				return erased, fmt.Errorf("assistant: delete an erased user's data: %w", err)
			}
			erased += len(gone)
		}
		after = ids[len(ids)-1]
		if len(ids) < erasureChunk || ctx.Err() != nil {
			return erased, nil
		}
	}
}
