package knowledge

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/knowledge/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// RetentionResult is what one retention cycle did.
type RetentionResult = retentionResult

// RetentionCycle runs one cycle of the knowledge.retention worker.
func RetentionCycle(ctx context.Context, d module.Deps) (RetentionResult, error) {
	return (&retentionWorker{deps: d}).cycle(ctx)
}

// Recompute recomputes the materialised access of root's subtree under the
// tree's lock, in a transaction of its own, as every tree write does (K5).
func Recompute(ctx context.Context, pool *pgxpool.Pool, root uuid.UUID) error {
	return db.WithTx(ctx, pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := lockTree(ctx, q); err != nil {
			return err
		}
		return recompute(ctx, q, root)
	})
}

// KeyBetween is the fractional key between a and b.
func KeyBetween(a, b string) (string, error) { return keyBetween(a, b) }

// SplitQuery is the search's split of a query into its head and last word.
func SplitQuery(q string) (string, string) { return splitQuery(q) }
