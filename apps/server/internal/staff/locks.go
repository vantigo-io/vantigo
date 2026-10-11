package staff

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// The module's locks (design §4, Locking). Every mutation of a person's rows
// takes the person's row first (withPersonLock), then their employments;
// nothing locks two people. A write that changes an edge of the manager
// graph — a terms row naming, clearing or changing a manager or a department,
// a department's manager or parent — takes the manager-graph advisory lock
// after the person's row, so the cycle checks across people are judged one at
// a time. The structure tables' defaults are held by an advisory lock of their
// own per table, so two writes that both find "no default yet" or both move it
// queue rather than race to the partial unique index.

// errPersonNotFound is withPersonLock's answer for an id no person has. The
// caller answers it as its 404.
var errPersonNotFound = errors.New("staff: no such person")

// The advisory locks' names, each hashed to the lock's key by
// pg_advisory_xact_lock(hashtext(name)). The names are the module's own, so
// they cannot collide with another module's locks by intent; hashtext's
// thirty-two bits make an accidental collision possible but harmless — two
// unrelated writes would queue, never deadlock, since each takes one.
const (
	managerGraphLock        = "staff-manager-graph"
	legalEntityDefaultLock  = "staff-legal-entities-default"
	businessUnitDefaultLock = "staff-business-units-default"
)

// withPersonLock runs fn in one locked transaction (withLockedTx) that has
// first taken personID's row. The row is held FOR NO KEY UPDATE: every writer
// of the person queues behind it exactly as FOR UPDATE would make it, but a
// foreign-key check that names the person — another person's terms naming
// them as manager, a department naming them as its head — takes only a KEY
// SHARE lock, which this one does not block. FOR UPDATE would, and would let
// such a write, already holding its own person's lock and the manager graph's,
// wait on this one while this one waits on the graph: the cross-person
// deadlock the rule "nothing locks two people" exists to rule out. A person
// who does not exist is errPersonNotFound, and fn never runs.
//
// The lock is also where the read-only rule is judged (erased.go): rule says
// whether the write is one an erased person refuses, and on a person the
// retention run has erased such a write is the 409 person_erased before fn
// runs — judged on the row the lock now holds, so no erase can slip in
// between the check and the write.
func (s *server) withPersonLock(ctx context.Context, personID int64, rule erasedRule, fn func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error) error {
	return s.withLockedTx(ctx, lockingPerson(personID, rule, fn))
}

// withPersonLockOn is withPersonLock on conn — the retention worker's lease
// connection (design §4 Locking, the plan's first worker discipline): the
// erase cycle holds that one connection for its lease and runs every
// person's transaction on it, so it never waits on the pool for a second
// while holding the first (the deadlock customers met at startup, PR #124).
// The transaction is marked locked as withPersonLock's is, so no call out of
// the module is made from inside it either.
func (s *server) withPersonLockOn(ctx context.Context, conn *pgxpool.Conn, personID int64, rule erasedRule, fn func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error) error {
	return withLockedTxOn(ctx, conn, lockingPerson(personID, rule, fn))
}

// lockingPerson is fn behind the person's lock and the read-only rule: the
// person's row taken FOR NO KEY UPDATE, a person who does not exist
// errPersonNotFound, an erased one person_erased when rule refuses it.
func lockingPerson(personID int64, rule erasedRule, fn func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error) func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
	return func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		row, err := txq.LockPerson(ctx, personID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errPersonNotFound
		} else if err != nil {
			return fmt.Errorf("staff: lock person %d: %w", personID, err)
		}
		if rule == refuseErased && row.ErasedAt != nil {
			return personErased()
		}
		return fn(ctx, tx, txq)
	}
}

// lockManagerGraph takes the manager-graph advisory lock for the rest of the
// transaction. A caller that also mutates a person takes it after the
// person's row, never before.
func lockManagerGraph(ctx context.Context, txq *store.Queries) error {
	return advisoryLock(ctx, txq, managerGraphLock)
}

// advisoryLock takes one of the module's named transaction advisory locks.
func advisoryLock(ctx context.Context, txq *store.Queries, name string) error {
	if err := txq.AdvisoryXactLock(ctx, name); err != nil {
		return fmt.Errorf("staff: take the %s lock: %w", name, err)
	}
	return nil
}
