package storage_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// recordingStore is a minimal in-memory ObjectStore that records the exact
// key every call was made with, so the scope wrapper's key construction can
// be asserted without touching a filesystem.
type recordingStore struct {
	mu        sync.Mutex
	lastKey   string
	lastCall  string
	existsVal bool
	existsErr error
}

func (s *recordingStore) Put(_ context.Context, key string, r io.Reader, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastCall, s.lastKey = "Put", key
	_, _ = io.Copy(io.Discard, r)
	return nil
}

func (s *recordingStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastCall, s.lastKey = "Get", key
	return io.NopCloser(strings.NewReader("")), nil
}

func (s *recordingStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastCall, s.lastKey = "Exists", key
	return s.existsVal, s.existsErr
}

func (s *recordingStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastCall, s.lastKey = "Delete", key
	return nil
}

func (s *recordingStore) last() (call, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastCall, s.lastKey
}

// TestScope_CombinesKey proves the scope wrapper builds physical keys as
// "{scope}/{relative-key}" — with no tenant segment: .NET's physical key is
// tenants/{tenant-id}/{scope}/{key}, and dropping tenancy drops exactly that
// segment (docs/storage.md "Tenant isolation, module scopes, and
// downloads").
func TestScope_CombinesKey(t *testing.T) {
	inner := &recordingStore{}
	scoped, err := storage.NewScope(inner, "communications")
	if err != nil {
		t.Fatalf("NewScope() = %v", err)
	}

	ctx := context.Background()
	cases := []struct {
		method func() error
		want   string
	}{
		{func() error { return scoped.Put(ctx, "attachments/a.pdf", strings.NewReader("x"), "application/pdf") }, "communications/attachments/a.pdf"},
		{func() error { _, err := scoped.Get(ctx, "attachments/a.pdf"); return err }, "communications/attachments/a.pdf"},
		{func() error { _, err := scoped.Exists(ctx, "attachments/a.pdf"); return err }, "communications/attachments/a.pdf"},
		{func() error { return scoped.Delete(ctx, "attachments/a.pdf") }, "communications/attachments/a.pdf"},
	}
	for _, tc := range cases {
		if err := tc.method(); err != nil {
			t.Fatalf("call returned %v", err)
		}
		if _, key := inner.last(); key != tc.want {
			t.Errorf("physical key = %q, want %q", key, tc.want)
		}
	}
}

// TestScope_RejectsCallerSuppliedAbsoluteOrPrefixedKey proves a scoped store
// never reaches its inner store with a key the caller was never meant to
// construct: an absolute path, or a key that already repeats the scope name
// as its own prefix (docs/storage.md: "duplicate-prefix forms rejected").
func TestScope_RejectsCallerSuppliedAbsoluteOrPrefixedKey(t *testing.T) {
	inner := &recordingStore{}
	scoped, err := storage.NewScope(inner, "communications")
	if err != nil {
		t.Fatalf("NewScope() = %v", err)
	}

	for _, key := range []string{
		"/etc/passwd",
		"communications/attachments/a.pdf",
		"communications",
	} {
		t.Run(key, func(t *testing.T) {
			if err := scoped.Put(context.Background(), key, strings.NewReader("x"), "text/plain"); err == nil {
				t.Fatalf("Put(%q) = nil error, want a rejection", key)
			} else if !errors.Is(err, storage.ErrInvalidKey) {
				t.Errorf("Put(%q) = %v, want ErrInvalidKey", key, err)
			}
			if call, _ := inner.last(); call != "" {
				t.Errorf("inner store was called (%s) for a rejected key %q", call, key)
			}
		})
	}
}

// TestScope_RejectsUnsafeScopeName proves scope names are validated at
// construction, before any key is ever combined: canonical lowercase
// [a-z0-9-], no slashes or dots (docs/storage.md).
func TestScope_RejectsUnsafeScopeName(t *testing.T) {
	inner := &recordingStore{}
	for _, scope := range []string{"", "Communications", "com.munications", "communications/x", "-leading", "trailing-", "com munications"} {
		t.Run(scope, func(t *testing.T) {
			if _, err := storage.NewScope(inner, scope); err == nil {
				t.Fatalf("NewScope(%q) = nil error, want a rejection", scope)
			} else if !errors.Is(err, storage.ErrInvalidScope) {
				t.Errorf("NewScope(%q) = %v, want ErrInvalidScope", scope, err)
			}
		})
	}
}

// TestNew_FailsClosedWhenUnconfigured proves that with STORAGE_PROVIDER
// unset, the process still starts (New returns a usable store, not an
// error), and every operation on that store reports storage as not
// configured rather than panicking or silently doing nothing
// (docs/storage.md: "If Storage__Provider is omitted, the host starts with
// storage fail-closed; an operation reports that storage is not
// configured.").
func TestNew_FailsClosedWhenUnconfigured(t *testing.T) {
	cfg := &config.Config{}
	store, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("New() = %v, want the server to still start with storage unconfigured", err)
	}

	ctx := context.Background()
	if err := store.Put(ctx, "a", strings.NewReader("x"), "text/plain"); !errors.Is(err, storage.ErrNotConfigured) {
		t.Errorf("Put() = %v, want ErrNotConfigured", err)
	}
	if _, err := store.Get(ctx, "a"); !errors.Is(err, storage.ErrNotConfigured) {
		t.Errorf("Get() = %v, want ErrNotConfigured", err)
	}
	if _, err := store.Exists(ctx, "a"); !errors.Is(err, storage.ErrNotConfigured) {
		t.Errorf("Exists() = %v, want ErrNotConfigured", err)
	}
	if err := store.Delete(ctx, "a"); !errors.Is(err, storage.ErrNotConfigured) {
		t.Errorf("Delete() = %v, want ErrNotConfigured", err)
	}
}

// TestNew_SelectsFSDriver proves New wires STORAGE_PROVIDER=fs to a working
// fs driver rooted at STORAGE_FS_ROOT.
func TestNew_SelectsFSDriver(t *testing.T) {
	cfg := &config.Config{
		Env:             config.Production,
		StorageProvider: "fs",
		StorageFSRoot:   secureTempDir(t),
	}
	store, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	ctx := context.Background()
	if err := store.Put(ctx, "a.txt", strings.NewReader("hello"), "text/plain"); err != nil {
		t.Fatalf("Put() = %v", err)
	}
	rc, err := store.Get(ctx, "a.txt")
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll() = %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}
}

// TestNew_RejectsUnknownProvider is New's own defensive check: config.Load
// already restricts STORAGE_PROVIDER to "" or "fs", but New refuses
// anything else too, the same defensive duplication mail.New makes for its
// own driver switch.
func TestNew_RejectsUnknownProvider(t *testing.T) {
	cfg := &config.Config{StorageProvider: "s3"}
	if _, err := storage.New(cfg); err == nil {
		t.Fatal("New() = nil error, want an error for an unknown provider")
	}
}
