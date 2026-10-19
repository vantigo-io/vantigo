package identity

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// ssoReads counts the queries that read identity.sso_settings.
type ssoReads struct{ n atomic.Int32 }

func (c *ssoReads) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "FROM identity.sso_settings") {
		c.n.Add(1)
	}
	return ctx
}

func (*ssoReads) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// The reader never holds its mutex across the database read. On a pool of
// one connection, held by a transaction: two stale gets wait for the
// connection with the mutex free, so the transaction's holder can still
// take it (evict) instead of deadlocking behind them; once the transaction
// commits both return, having read the row at most twice; and a get after
// that reads once more (the evict) and the next answers from the cache.
func TestSSOSettingsReader_FetchesWithoutItsLock(t *testing.T) {
	t.Parallel()
	_, databaseURL := testdb.Migrated(t)
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	reads := &ssoReads{}
	cfg.ConnConfig.Tracer = reads
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	r := newSSOSettingsReader(pool, func() time.Time { return internalNow }, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) }) // a no-op once committed

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := r.get(ctx)
			errs <- err
		})
	}
	time.Sleep(100 * time.Millisecond) // both are waiting for the connection

	evicted := make(chan struct{})
	go func() { r.evict(); close(evicted) }()
	select {
	case <-evicted:
	case <-time.After(2 * time.Second):
		t.Fatal("the reader's mutex is held while it waits for a connection")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("get: %v", err)
		}
	}
	if n := reads.n.Load(); n < 1 || n > 2 {
		t.Errorf("%d reads of the row for two concurrent gets, want one or two", n)
	}

	before := reads.n.Load()
	for range 2 {
		if _, err := r.get(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := reads.n.Load() - before; n != 1 {
		t.Errorf("%d reads for two gets after the evict, want 1 then the cache", n)
	}
}
