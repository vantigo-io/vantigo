package identity

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// The API request log's buffered writer (API access design K23): Record
// queues a row and returns at once, and one goroutine writes the queue every
// 2 seconds or every 500 rows, and on Close, on a connection it takes from
// the pool for each write. A request never waits on the database for it.
// When the queue is full, because the database is slow or gone, rows are
// dropped and counted in vantigo.identity.api_requests.dropped. A crash may
// lose the last 2 seconds: the log is an operational record, not the audit
// trail.

const (
	apiRequestFlushEvery = 2 * time.Second
	apiRequestFlushRows  = 500
	// apiRequestQueue is how many rows wait for a write before Record drops
	// them: 20 writes' worth.
	apiRequestQueue = 20 * apiRequestFlushRows
	// apiRequestWriteTimeout bounds one write, apiRequestCloseTimeout the
	// last one, at shutdown.
	apiRequestWriteTimeout = 10 * time.Second
	apiRequestCloseTimeout = 5 * time.Second
	// apiRequestMeter scopes the writer's instrument.
	apiRequestMeter = "vantigo.identity"
)

// The reasons a dropped row is counted under.
const (
	apiRequestDroppedFull   = "queue_full"
	apiRequestDroppedFailed = "write_failed"
)

// apiRequestLog is the writer one API process runs.
type apiRequestLog struct {
	pool      *pgxpool.Pool
	log       *slog.Logger
	every     time.Duration
	batchRows int

	rows    chan store.InsertAPIRequestsParams
	flushes chan chan struct{}
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once

	dropped     atomic.Int64
	droppedRows metric.Int64Counter
}

func newAPIRequestLog(pool *pgxpool.Pool, log *slog.Logger, mp metric.MeterProvider, every time.Duration, batchRows, queue int) *apiRequestLog {
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	counter, err := mp.Meter(apiRequestMeter).Int64Counter("vantigo.identity.api_requests.dropped",
		metric.WithDescription("API request log rows dropped, by reason: the queue was full, or their write failed."))
	if err != nil {
		counter, _ = noop.NewMeterProvider().Meter(apiRequestMeter).Int64Counter("vantigo.identity.api_requests.dropped")
	}
	return &apiRequestLog{
		pool: pool, log: log, every: every, batchRows: batchRows,
		rows:        make(chan store.InsertAPIRequestsParams, queue),
		flushes:     make(chan chan struct{}),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		droppedRows: counter,
	}
}

// StartRequestLog starts the API request log's writer, which Record queues
// rows for, and answers the function that stops it: it writes what is
// queued, within a few seconds, and returns. Only the API process starts it,
// where identity.NewAccess is built (K35); until it is started, Record
// keeps nothing.
func (a *Access) StartRequestLog() (closeLog func()) {
	l := newAPIRequestLog(a.dbPool, a.log, nil, apiRequestFlushEvery, apiRequestFlushRows, apiRequestQueue)
	a.requests.Store(l)
	go l.run()
	return func() {
		a.requests.CompareAndSwap(l, nil)
		l.close(apiRequestCloseTimeout)
	}
}

// FlushRequestLog writes what the request log has queued and returns once
// it is written, for a caller that reads the log right after a request.
func (a *Access) FlushRequestLog() {
	if l := a.requests.Load(); l != nil {
		l.flush()
	}
}

// Record is contracts.APIRequestRecorder: it queues req for the request log,
// stamped now when its At is zero, and never waits.
func (a *Access) Record(_ context.Context, req contracts.APIRequest) {
	l := a.requests.Load()
	if l == nil {
		return
	}
	if req.At.IsZero() {
		req.At = a.now()
	}
	l.record(req)
}

var _ contracts.APIRequestRecorder = (*Access)(nil)

// record queues req, or drops and counts it when the queue is full.
func (l *apiRequestLog) record(req contracts.APIRequest) {
	row := store.InsertAPIRequestsParams{
		At:            req.At.UTC(),
		KeyID:         optionalID(req.KeyID),
		ConnectionID:  optionalID(req.ConnectionID),
		IntegrationID: req.IntegrationID,
		Method:        clip(req.Method, 10),
		OperationID:   clip(req.OperationID, 200),
		Status:        int16(req.Status),
		Reason:        optional(clip(req.Reason, 20)),
		DurationMs:    int32(min(req.Duration.Milliseconds(), 1<<31-1)),
		ClientIp:      optional(clip(req.ClientIP, 64)),
		TraceID:       optional(clip(req.TraceID, 64)),
	}
	select {
	case l.rows <- row:
	default:
		l.drop(1, apiRequestDroppedFull)
	}
}

func (l *apiRequestLog) drop(n int, reason string) {
	l.dropped.Add(int64(n))
	l.droppedRows.Add(context.Background(), int64(n), metric.WithAttributes(attribute.String("reason", reason)))
}

// run collects rows and writes them every l.every, every l.batchRows rows, on
// a flush and on stop.
func (l *apiRequestLog) run() {
	defer close(l.done)
	ticker := time.NewTicker(l.every)
	defer ticker.Stop()
	batch := make([]store.InsertAPIRequestsParams, 0, l.batchRows)
	write := func() {
		if len(batch) > 0 {
			l.write(batch)
			batch = batch[:0]
		}
	}
	drain := func() {
		for {
			select {
			case row := <-l.rows:
				batch = append(batch, row)
				if len(batch) >= l.batchRows {
					write()
				}
			default:
				write()
				return
			}
		}
	}
	for {
		select {
		case row := <-l.rows:
			batch = append(batch, row)
			if len(batch) >= l.batchRows {
				write()
			}
		case <-ticker.C:
			write()
		case reply := <-l.flushes:
			drain()
			close(reply)
		case <-l.stop:
			drain()
			return
		}
	}
}

// write copies batch into the log on a connection of its own. A write that
// fails drops its rows, counted, and is logged without them.
func (l *apiRequestLog) write(batch []store.InsertAPIRequestsParams) {
	ctx, cancel := context.WithTimeout(context.Background(), apiRequestWriteTimeout)
	defer cancel()
	conn, err := l.pool.Acquire(ctx)
	if err == nil {
		_, err = store.New(conn).InsertAPIRequests(ctx, batch)
		conn.Release()
	}
	if err != nil {
		l.drop(len(batch), apiRequestDroppedFailed)
		if l.log != nil {
			l.log.Warn("identity: could not write the API request log", "rows", len(batch), "error", err)
		}
	}
}

// flush asks run to write what is queued and waits until it has.
func (l *apiRequestLog) flush() {
	reply := make(chan struct{})
	select {
	case l.flushes <- reply:
		<-reply
	case <-l.done:
	}
}

// close stops run, which writes what is queued, and waits for it at most
// timeout.
func (l *apiRequestLog) close(timeout time.Duration) {
	l.once.Do(func() { close(l.stop) })
	select {
	case <-l.done:
	case <-time.After(timeout):
		if l.log != nil {
			l.log.Warn("identity: the API request log did not finish its last write in time", "timeout", timeout)
		}
	}
}

// clip cuts s to at most n bytes, the column's bound.
func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// optionalID is id as a nullable column: nil when zero, so a request a
// connection made keeps no key, and one a key made no connection (A18).
func optionalID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// optional is s as a nullable column: nil when empty.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
