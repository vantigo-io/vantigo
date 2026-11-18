package identity

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// The request log's buffered writer (API access design K23).

func logRows(t *testing.T, pool *pgxpool.Pool, key uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM identity.api_requests WHERE key_id = $1`, key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// eventually waits up to 5 seconds for key's rows to number want.
func eventually(t *testing.T, pool *pgxpool.Pool, key uuid.UUID, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if logRows(t, pool, key) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%d rows, want %d", logRows(t, pool, key), want)
}

func logRequest(key uuid.UUID) contracts.APIRequest {
	return contracts.APIRequest{At: time.Now(), KeyID: key, IntegrationID: uuid.New(), Method: "GET", OperationID: "getCustomers", Status: 200, Duration: 3 * time.Millisecond}
}

func TestAPIRequestLog_FlushesOnSizeOnTimeAndOnClose(t *testing.T) {
	pool, _ := testdb.Migrated(t)

	bySize := newAPIRequestLog(pool, nil, nil, time.Hour, 3, 100)
	go bySize.run()
	defer bySize.close(time.Second)
	key := uuid.New()
	for range 4 {
		bySize.record(logRequest(key))
	}
	eventually(t, pool, key, 3) // the fourth waits for its batch

	byTime := newAPIRequestLog(pool, nil, nil, 50*time.Millisecond, 500, 100)
	go byTime.run()
	defer byTime.close(time.Second)
	key = uuid.New()
	byTime.record(logRequest(key))
	eventually(t, pool, key, 1)

	onClose := newAPIRequestLog(pool, nil, nil, time.Hour, 500, 100)
	go onClose.run()
	key = uuid.New()
	onClose.record(logRequest(key))
	onClose.record(logRequest(key))
	onClose.close(5 * time.Second)
	if n := logRows(t, pool, key); n != 2 {
		t.Errorf("%d rows after Close, want 2", n)
	}
}

// A database that does not answer never holds up a request: Record returns
// at once, and once the queue is full it drops rows and counts them.
func TestAPIRequestLog_ASlowDatabaseNeverBlocksAndAFullQueueDrops(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	lock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, `LOCK TABLE identity.api_requests IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}

	reader := sdkmetric.NewManualReader()
	l := newAPIRequestLog(pool, nil, sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)), time.Hour, 1, 5)
	go l.run()
	key := uuid.New()
	start := time.Now()
	for range 50 {
		l.record(logRequest(key))
	}
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Errorf("50 Records took %v while the table was locked, want no wait", took)
	}
	// One row is being written, 5 wait in the queue, and the rest are dropped.
	if got := l.dropped.Load(); got < 40 || got > 45 {
		t.Errorf("%d dropped, want the rows past the queue", got)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	var counted int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "vantigo.identity.api_requests.dropped" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				counted += dp.Value
			}
		}
	}
	if counted != l.dropped.Load() {
		t.Errorf("the metric counted %d, the writer dropped %d", counted, l.dropped.Load())
	}

	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	l.close(5 * time.Second)
	if n := logRows(t, pool, key); int64(n)+l.dropped.Load() != 50 {
		t.Errorf("%d written and %d dropped, want 50 in all", n, l.dropped.Load())
	}
}

// Record keeps nothing before the log is started or after it is closed, and
// stamps a row with Access's clock when it carries no time.
func TestAccess_RecordOnlyWhileTheLogRuns(t *testing.T) {
	pool, _ := testdb.Migrated(t)
	at := time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)
	a := &Access{dbPool: pool, now: func() time.Time { return at }}
	key := uuid.New()
	req := logRequest(key)
	req.At = time.Time{}
	a.Record(context.Background(), req)

	closeLog := a.StartRequestLog()
	a.Record(context.Background(), req)
	a.FlushRequestLog()
	closeLog()
	a.Record(context.Background(), req)
	a.FlushRequestLog()

	if n := logRows(t, pool, key); n != 1 {
		t.Fatalf("%d rows, want the one recorded while the log ran", n)
	}
	var got time.Time
	if err := pool.QueryRow(context.Background(), `SELECT at FROM identity.api_requests WHERE key_id = $1`, key).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(at) {
		t.Errorf("at = %v, want Access's clock %v", got, at)
	}
}
