package communications

import (
	"bytes"
	"context"
	"crypto/sha256"
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

// TestDeterministicUploadID_UsesTheOneDeterministicGuidConvention is task
// 14's guard on unifying the module's two deterministic-id conventions.
//
// .NET derives the staged-upload id with the SAME helper as every other
// deterministic id in the module: `ObjectOwnershipLifecycle.DeterministicGuid(
// userId.Value, $"staged-upload:{key}")` (`EP/ConversationEndpoints.cs:132`,
// inventory `:1620`). Task 6 wrote a second convention instead, and it
// differed in TWO independent ways at once — the seed was rendered as a
// dashed UUID string rather than .NET's "N" format, and the digest was cut
// with the naive uuid.FromBytes byte order rather than the little-endian
// layout .NET's Guid(ReadOnlySpan<byte>) applies to the first eight bytes.
//
// The assertions are deliberately negative as well as positive: agreeing
// with deterministicGUID is the property, but on its own that reads as a
// restatement of a one-line function. Reproducing BOTH discarded conventions
// here and demanding they differ is what makes a silent regression to either
// one fail, and names which of the two came back.
func TestDeterministicUploadID_UsesTheOneDeterministicGuidConvention(t *testing.T) {
	user := uuid.MustParse("3f2504e0-4f89-11d3-9a0c-0305e82c3301")
	const key = "idem-key-42"

	got := deterministicUploadID(user, key)

	if want := deterministicGUID(user, "staged-upload:"+key); got != want {
		t.Errorf("deterministicUploadID = %s, want %s: the staged-upload id is DeterministicGuid(uploaderUserId, \"staged-upload:{key}\")",
			got, want)
	}

	// The dashed-seed convention: .NET interpolates {seed:N} (32 hex, no
	// dashes), so seeding with the dashed form hashes different bytes.
	dashedSeed := sha256.Sum256([]byte(user.String() + ":staged-upload:" + key))
	var dashed uuid.UUID
	copy(dashed[:], dashedSeed[:16])
	if got == dashed {
		t.Error("deterministicUploadID seeds with the dashed UUID string; .NET's {seed:N} is 32 hex with no dashes")
	}

	// The naive byte order: correct seed, but the digest copied straight
	// through instead of byte-swapped into .NET's Guid layout.
	correctSeed := sha256.Sum256([]byte(hexN(user) + ":staged-upload:" + key))
	var naive uuid.UUID
	copy(naive[:], correctSeed[:16])
	if got == naive {
		t.Error("deterministicUploadID uses the naive uuid.FromBytes byte order; the first eight bytes must be byte-swapped (see deterministicGUID)")
	}
}
