package expenses

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// cleanupStore is storage.ObjectStore for the one thing this file is about:
// which context a compensating delete arrives on. Delete records whether the
// context it was handed had already been cancelled, and whether it carries a
// deadline of its own.
type cleanupStore struct {
	mu           sync.Mutex
	deleted      []string
	sawCancelled bool
	sawDeadline  bool
}

var _ storage.ObjectStore = (*cleanupStore)(nil)

func (s *cleanupStore) Put(context.Context, string, io.Reader, string) error { return nil }

func (s *cleanupStore) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (s *cleanupStore) Exists(context.Context, string) (bool, error) { return true, nil }

func (s *cleanupStore) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		s.sawCancelled = true
	}
	if _, ok := ctx.Deadline(); ok {
		s.sawDeadline = true
	}
	s.deleted = append(s.deleted, key)
	return nil
}

// TestRemoveReceiptObject_OutlivesTheRequestItBelongsTo: every compensating
// and post-commit object delete runs after the decision it compensates for is
// already made — the row is gone, or was never written — so it must not be
// cancelled with the request. internal/storage's fs driver checks ctx.Err()
// before it touches the filesystem, so a client that closed the tab mid-upload
// would otherwise leave the bytes behind for good: there is no sweeper
// anywhere. The cleanup therefore runs on a context detached from the
// request's cancellation, with a bound of its own so it cannot hang a
// connection either.
func TestRemoveReceiptObject_OutlivesTheRequestItBelongsTo(t *testing.T) {
	t.Parallel()
	objects := &cleanupStore{}
	s := &server{
		deps:    module.Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
		objects: objects,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the client has gone: this is what the handler is left holding

	s.removeReceiptObject(ctx, logKeyEntryID, 42, "receipts/42/9c4f")

	if len(objects.deleted) != 1 || objects.deleted[0] != "receipts/42/9c4f" {
		t.Errorf("deleted = %v, want the one key, removed despite the cancelled request", objects.deleted)
	}
	if objects.sawCancelled {
		t.Error("the object store was handed an already-cancelled context; the bytes would have stayed")
	}
	if !objects.sawDeadline {
		t.Error("the cleanup context carries no deadline; a hung store would hold the request open")
	}
}

// TestReceiptUploadPolicy_IsRegisteredOnTheUpload pins the module's one rate
// limit: what it allows, over what window, and that it is registered against
// the upload and nothing else. The end-to-end test proves the limit bites;
// this one proves it is still the limit that was agreed, and that no later
// delivery quietly rate-limits a read.
func TestReceiptUploadPolicy_IsRegisteredOnTheUpload(t *testing.T) {
	t.Parallel()
	want := ratelimit.Policy{
		Name:    "ExpensesReceiptUpload",
		Limit:   600,
		Window:  time.Hour,
		Message: "Too many receipt uploads. Please wait a little before adding more.",
	}
	if policyReceiptUpload != want {
		t.Errorf("policyReceiptUpload = %+v, want %+v", policyReceiptUpload, want)
	}
	if len(limits) != 1 || limits["postExpensesEntriesByIdAttachments"] != policyReceiptUpload {
		t.Errorf("limits = %+v, want the receipt upload alone", limits)
	}
}
