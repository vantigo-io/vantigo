package communications

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/testdb"
)

// TestReserveStorageKey_ErrorNeverCarriesTheStorageKey is fix round 2's
// item 2 teeth check. reserveStorageKey's own find-cleanup-record failure
// used to wrap the physical storage key into the returned error
// (`fmt.Errorf("... for %q: %w", key, err)`) — the only unhandled-error
// path this handler can take, which module.ResponseError hands to
// httpx.WriteError, whose default case logs the error's own text at Error
// level (problem.go's "request failed" log line). That text never reaches
// a response body — the contract's "no endpoint may expose a storage key"
// rule holds regardless — but a storage key sitting in server logs is
// exactly the kind of exposure nobody notices until the logs are exported
// somewhere.
//
// This is a white-box test, in the package itself rather than
// communications_test: the fix is about the *error value's own text*,
// which the HTTP surface never exposes either way, so the only way to
// prove it is to inspect the error — and, end to end, the log line
// httpx.WriteError would actually emit for it — directly.
//
// A second, independent *pgxpool.Pool against the same migrated database is
// closed immediately, before any use, so reserveStorageKey's
// FindLatestCleanupRecordByStorageKey query fails deterministically (a
// closed pool rejects every Acquire) without touching the shared pool
// testdb.Migrated registers its own t.Cleanup(pool.Close) for, and without
// the timing risk an already-canceled context passed to a fast local query
// would carry.
func TestReserveStorageKey_ErrorNeverCarriesTheStorageKey(t *testing.T) {
	_, databaseURL := testdb.Migrated(t)
	brokenPool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open a second pool: %v", err)
	}
	brokenPool.Close()

	clock := func() time.Time { return time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC) }
	s := &server{deps: module.Deps{Pool: brokenPool, Clock: clock}}

	uploadID := uuid.New()
	key := "staged-attachments/deadbeefdeadbeefdeadbeefdeadbeef/deadbeefdeadbeefdeadbeefdeadbeef/deadbeefdeadbeefdeadbeefdeadbeef"

	reserveErr := s.reserveStorageKey(context.Background(), uploadID, key, clock())
	if reserveErr == nil {
		t.Fatal("reserveStorageKey against a closed pool returned nil, want a query failure to wrap")
	}
	if strings.Contains(reserveErr.Error(), key) {
		t.Fatalf("reserveStorageKey's own error contains the storage key: %v", reserveErr)
	}
	if !strings.Contains(reserveErr.Error(), uploadID.String()) {
		t.Errorf("reserveStorageKey's own error = %v, want it to name the upload id (%s) instead", reserveErr, uploadID)
	}

	// End to end: the same log line httpx.WriteError actually emits for
	// this error, via the exact wrap the real handler applies
	// (attachments.go's own "reserve storage key for upload %s" call site).
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	handlerErr := fmt.Errorf("communications: reserve storage key for upload %s: %w", uploadID, reserveErr)
	rec := httptest.NewRecorder()
	httpx.WriteError(rec, httptest.NewRequest("POST", "/api/v1/communications/conversations/x/attachments", nil), handlerErr)

	if strings.Contains(buf.String(), key) {
		t.Errorf("the storage key reached the captured log output: %s", buf.String())
	}
	if !strings.Contains(buf.String(), uploadID.String()) {
		t.Errorf("the upload id did not reach the captured log output, want it in place of the key: %s", buf.String())
	}
}
