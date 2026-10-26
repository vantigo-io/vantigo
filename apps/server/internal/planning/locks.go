package planning

import (
	"context"
	"fmt"
	"slices"

	"github.com/vantigo-io/vantigo/server/internal/planning/store"
)

// The module's locks (design S23). Every write of an allocation takes a
// transaction advisory lock on its person, so two planners booking one person
// queue and the second's overbooking is judged on the first's committed rows;
// a write of a request (delivery B) takes its project's — the old and the
// new project's when a PUT moves it. A write that needs both — a fill, the
// delete of a row that fills a request (which reopens it), a PUT that moves
// a row to another project — takes the project's first, then the person's;
// a PUT that moves a row to another
// person takes both persons' in ascending id. One order everywhere is what
// keeps two such writes from deadlocking: neither ever holds a lock the other
// takes before one it holds.
//
// The locks are named, each hashed to its key by
// pg_advisory_xact_lock(hashtext(name)). The names are the module's own, so
// they cannot collide with another module's locks by intent; hashtext's
// thirty-two bits make an accidental collision possible but harmless — two
// unrelated writes would queue, never deadlock, since every writer takes its
// locks in the one order.

// personLockName and projectLockName are the advisory locks' names.
func personLockName(personID int64) string   { return fmt.Sprintf("planning-person-%d", personID) }
func projectLockName(projectID int32) string { return fmt.Sprintf("planning-project-%d", projectID) }

// lockSet is what one write locks: its projects and its persons.
type lockSet struct {
	projects []int32
	persons  []int64
}

// withPersonLock runs fn in one locked transaction (withLockedTx) that has
// first taken personID's lock: every write of one allocation.
func (s *server) withPersonLock(ctx context.Context, personID int64, fn func(ctx context.Context, txq *store.Queries) error) error {
	return s.withLocks(ctx, lockSet{persons: []int64{personID}}, fn)
}

// withProjectLock runs fn in one locked transaction that has first taken
// projectID's lock: a request's create and withdrawal (delivery B).
func (s *server) withProjectLock(ctx context.Context, projectID int32, fn func(ctx context.Context, txq *store.Queries) error) error {
	return s.withLocks(ctx, lockSet{projects: []int32{projectID}}, fn)
}

// withLocks runs fn in one locked transaction that has first taken every lock
// of set in S23's order — the projects ascending, then the persons ascending,
// each once — for the write that moves a row to another project or person.
func (s *server) withLocks(ctx context.Context, set lockSet, fn func(ctx context.Context, txq *store.Queries) error) error {
	return s.withLockedTx(ctx, func(ctx context.Context, txq *store.Queries) error {
		for _, name := range set.names() {
			if err := advisoryLock(ctx, txq, name); err != nil {
				return err
			}
		}
		return fn(ctx, txq)
	})
}

// names is set's lock names in the order they are taken: projects before
// persons, each ascending, duplicates dropped.
func (set lockSet) names() []string {
	projects := slices.Compact(slices.Sorted(slices.Values(set.projects)))
	persons := slices.Compact(slices.Sorted(slices.Values(set.persons)))
	names := make([]string, 0, len(projects)+len(persons))
	for _, id := range projects {
		names = append(names, projectLockName(id))
	}
	for _, id := range persons {
		names = append(names, personLockName(id))
	}
	return names
}

// advisoryLock takes one of the module's named transaction advisory locks.
func advisoryLock(ctx context.Context, txq *store.Queries, name string) error {
	if err := txq.AdvisoryXactLock(ctx, name); err != nil {
		return fmt.Errorf("planning: take the %s lock: %w", name, err)
	}
	return nil
}
