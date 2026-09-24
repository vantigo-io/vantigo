package contracts

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// CustomerReferenceHolder is a module that stores customer ids in its own
// schema, and the one sanctioned cross-module WRITE (customers merge design
// D1). Every other contract here is a read. This one exists because merging
// two customers must move every reference to the absorbed one, and every
// module shares one database and one pool, so the move can be a single
// transaction with no event bus: the customers module opens it, locks both
// customer rows, moves its own tables, and hands the same transaction to each
// holder in turn. A holder runs its own SQL, on its own schema, from its own
// package — depguard and internal/db/schema_test.go hold exactly as they do
// for a read — and an error anywhere rolls back every module's part together.
//
// tx is a platform type, not a store type, so rule 3 of
// docs/module-boundaries.md (contracts carry no store types) still holds: the
// holder builds its own store over it.
//
// RepointCustomer moves every reference from `from` to `into` inside tx and
// reports what it moved, kind by kind, for the merge's answer and its timeline
// event. The rules a holder keeps, none of which the signature shows:
//
//   - It never begins, commits or rolls back a transaction, and never touches
//     a pool: tx is the caller's, and so is the decision.
//   - It never reads a directory or any other contract. The caller holds two
//     customer rows locked, and no in-process lookup happens under a lock in
//     this codebase (the customers module's actor.go); a holder needs none —
//     both ids are known to exist.
//   - It tolerates a reference that already points at `into`: a junction row
//     that exists for both customers is kept once, never a unique violation.
//   - It reports every kind it handles, a zero count included, so the answer
//     says what was looked at as well as what moved. A kind is
//     "<module>.<what>" in the API's camelCase: "projects.projects",
//     "energy.supplyPeriods".
type CustomerReferenceHolder interface {
	RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]RepointedReferences, error)
}

// RepointedReferences is one kind of reference a holder re-pointed, and how
// many rows of it.
type RepointedReferences struct {
	Kind  string
	Count int64
}
