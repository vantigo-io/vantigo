package storage_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// TestNewFS_RootMustBeAbsolute proves a relative root is refused rather than
// silently resolved against the process's working directory.
func TestNewFS_RootMustBeAbsolute(t *testing.T) {
	if _, err := storage.NewFS("relative/path", false, false); err == nil {
		t.Fatal("NewFS() = nil error, want a relative root rejected")
	}
}

// TestNewFS_CreatesRootIfMissing proves the root is created at
// initialisation when it does not yet exist, restrictively permissioned.
func TestNewFS_CreatesRootIfMissing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "objects")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("test setup: %q already exists", root)
	}

	store, err := storage.NewFS(root, false, false)
	if err != nil {
		t.Fatalf("NewFS() = %v", err)
	}
	if store == nil {
		t.Fatal("NewFS() store = nil")
	}

	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("root was not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("root is not a directory")
	}
	if runtimeIsUnix() && info.Mode().Perm()&0o077 != 0 {
		t.Errorf("root mode = %v, want no group/other bits set on a freshly created root", info.Mode().Perm())
	}
}

// TestNewFS_RefusesSymlinkRoot proves a root that is itself a symbolic link
// is refused outright, never followed.
func TestNewFS_RefusesSymlinkRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	if _, err := storage.NewFS(link, false, false); err == nil {
		t.Fatal("NewFS() = nil error, want a symlinked root rejected")
	}
}

// TestNewFS_RefusesGroupOrWorldWritableRootOutsideDevelopment proves a
// pre-existing root that is group- or world-writable is refused outside
// development, and accepted only with both the escape hatch and a genuine
// development environment.
func TestNewFS_RefusesGroupOrWorldWritableRootOutsideDevelopment(t *testing.T) {
	skipUnlessUnix(t)

	for _, mode := range []os.FileMode{0o770, 0o707, 0o777} {
		t.Run(mode.String(), func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, mode); err != nil {
				t.Fatal(err)
			}

			if _, err := storage.NewFS(root, false, false); err == nil {
				t.Fatalf("NewFS(allowInsecureRoot=false, isDevelopment=false) = nil error for mode %v, want a rejection", mode)
			}

			if _, err := storage.NewFS(root, true, true); err != nil {
				t.Fatalf("NewFS(allowInsecureRoot=true, isDevelopment=true) = %v, want the development escape hatch to accept mode %v", err, mode)
			}
		})
	}
}

// TestNewFS_RefusesEscapeHatchOutsideDevelopment proves
// STORAGE_FS_ALLOW_INSECURE_ROOT is refused outside development regardless
// of the root's actual permissions — config.Load already rejects this
// combination, but NewFS refuses it too, defensively, the same way NewSMTP
// refuses TLS="none" without insecure transport even though config already
// guarantees the combination cannot occur outside development.
func TestNewFS_RefusesEscapeHatchOutsideDevelopment(t *testing.T) {
	root := secureTempDir(t) // secure, so the failure below is only ever about the flag itself.

	if _, err := storage.NewFS(root, true, false); err == nil {
		t.Fatal("NewFS(allowInsecureRoot=true, isDevelopment=false) = nil error, want a rejection even for an already-secure root")
	}
}

// TestFS_PutIsAtomic proves a write lands through a temporary file under the
// root followed by an atomic replace: the target key is invisible to Get
// while the write is still in flight, and no temporary file is left behind
// once it completes.
func TestFS_PutIsAtomic(t *testing.T) {
	dir := secureTempDir(t)
	store, err := storage.NewFS(dir, false, false)
	if err != nil {
		t.Fatalf("NewFS() = %v", err)
	}

	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- store.Put(context.Background(), "reports/a.csv", pr, "text/csv")
	}()

	if _, err := pw.Write([]byte("partial-chunk-1,")); err != nil {
		t.Fatalf("pw.Write() = %v", err)
	}

	parent := filepath.Join(dir, "reports")
	deadline := time.Now().Add(2 * time.Second)
	sawTemp := false
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(parent)
		for _, e := range entries {
			if e.Name() != "a.csv" {
				sawTemp = true
			}
		}
		if sawTemp {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !sawTemp {
		t.Fatal("no temporary file appeared under the target's parent directory while the write was in flight")
	}

	if _, err := store.Get(context.Background(), "reports/a.csv"); !errors.Is(err, storage.ErrNotExist) {
		t.Fatalf("Get() during an in-flight Put = %v, want ErrNotExist: the write must not be visible until it completes", err)
	}

	if _, err := pw.Write([]byte("partial-chunk-2")); err != nil {
		t.Fatalf("pw.Write() = %v", err)
	}
	if err := pw.Close(); err != nil {
		t.Fatalf("pw.Close() = %v", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("Put() = %v", err)
	}

	rc, err := store.Get(context.Background(), "reports/a.csv")
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll() = %v", err)
	}
	if string(got) != "partial-chunk-1,partial-chunk-2" {
		t.Errorf("content = %q", got)
	}

	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("ReadDir() = %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.csv" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("parent directory entries = %v, want exactly [a.csv], no leftover temporary file", names)
	}
}

// TestFS_PutOverwritesExistingKeyAtomically proves a second Put to the same
// key replaces its content wholesale via the same temp-then-rename path,
// rather than truncating and rewriting the existing file in place (which
// would let a concurrent reader observe a half-old-half-new file).
func TestFS_PutOverwritesExistingKeyAtomically(t *testing.T) {
	dir := secureTempDir(t)
	store, err := storage.NewFS(dir, false, false)
	if err != nil {
		t.Fatalf("NewFS() = %v", err)
	}
	ctx := context.Background()
	if err := store.Put(ctx, "k", strings.NewReader("first-version-long-content"), "text/plain"); err != nil {
		t.Fatalf("Put() = %v", err)
	}
	if err := store.Put(ctx, "k", strings.NewReader("v2"), "text/plain"); err != nil {
		t.Fatalf("Put() = %v", err)
	}
	rc, err := store.Get(ctx, "k")
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != "v2" {
		t.Errorf("content = %q, want %q (no trailing bytes from the first, longer write)", got, "v2")
	}
}

// TestFS_GetMissingKeyReturnsSentinel proves a missing key is a typed error
// callers can test for, not a bare nil stream or an opaque error.
func TestFS_GetMissingKeyReturnsSentinel(t *testing.T) {
	store, err := storage.NewFS(secureTempDir(t), false, false)
	if err != nil {
		t.Fatalf("NewFS() = %v", err)
	}
	rc, err := store.Get(context.Background(), "never/written.txt")
	if rc != nil {
		t.Error("Get() returned a non-nil stream for a missing key")
	}
	if !errors.Is(err, storage.ErrNotExist) {
		t.Fatalf("Get() = %v, want ErrNotExist", err)
	}
}

// TestFS_DeleteMissingKeySucceeds proves deleting a key that was never
// written is not an error, matching the port's documented contract.
func TestFS_DeleteMissingKeySucceeds(t *testing.T) {
	store, err := storage.NewFS(secureTempDir(t), false, false)
	if err != nil {
		t.Fatalf("NewFS() = %v", err)
	}
	if err := store.Delete(context.Background(), "never/written.txt"); err != nil {
		t.Fatalf("Delete() = %v, want success for a missing key", err)
	}
}

// TestFS_ExistsAndDeleteRoundTrip is a basic Put/Exists/Delete/Exists cycle.
func TestFS_ExistsAndDeleteRoundTrip(t *testing.T) {
	store, err := storage.NewFS(secureTempDir(t), false, false)
	if err != nil {
		t.Fatalf("NewFS() = %v", err)
	}
	ctx := context.Background()

	if ok, err := store.Exists(ctx, "k"); err != nil || ok {
		t.Fatalf("Exists() before Put = %v/%v, want false/nil", ok, err)
	}
	if err := store.Put(ctx, "k", strings.NewReader("v"), "text/plain"); err != nil {
		t.Fatalf("Put() = %v", err)
	}
	if ok, err := store.Exists(ctx, "k"); err != nil || !ok {
		t.Fatalf("Exists() after Put = %v/%v, want true/nil", ok, err)
	}
	if err := store.Delete(ctx, "k"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if ok, err := store.Exists(ctx, "k"); err != nil || ok {
		t.Fatalf("Exists() after Delete = %v/%v, want false/nil", ok, err)
	}
}

// TestFS_ExistsIsFalseForADirectoryKey proves Exists reports an object, not
// merely a filesystem entry: a key one segment shorter than a written key
// resolves to a directory Put created to hold it, and must not read as
// "exists".
func TestFS_ExistsIsFalseForADirectoryKey(t *testing.T) {
	store, err := storage.NewFS(secureTempDir(t), false, false)
	if err != nil {
		t.Fatalf("NewFS() = %v", err)
	}
	ctx := context.Background()
	if err := store.Put(ctx, "a/b/c.txt", strings.NewReader("v"), "text/plain"); err != nil {
		t.Fatalf("Put() = %v", err)
	}
	if ok, err := store.Exists(ctx, "a/b"); err != nil || ok {
		t.Fatalf("Exists(%q) = %v/%v, want false/nil for a directory, not an object", "a/b", ok, err)
	}
}

// TestFS_RefusesSymlinkedIntermediateComponent proves the symlink check
// walks every path segment between root and the resolved key, not only
// root itself: a symlink planted one level inside root, pointing outside
// it, must still be refused when a key resolves through it.
func TestFS_RefusesSymlinkedIntermediateComponent(t *testing.T) {
	root := secureTempDir(t)
	outside := t.TempDir()

	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	store, err := storage.NewFS(root, false, false)
	if err != nil {
		t.Fatalf("NewFS() = %v", err)
	}
	ctx := context.Background()

	if err := store.Put(ctx, "escape/payload.txt", strings.NewReader("x"), "text/plain"); err == nil {
		t.Fatal("Put() through a symlinked intermediate component = nil error, want a rejection")
	}
	if _, err := os.Stat(filepath.Join(outside, "payload.txt")); !os.IsNotExist(err) {
		t.Fatal("Put() wrote outside the storage root through the symlink")
	}
	if _, err := store.Get(ctx, "escape/payload.txt"); err == nil {
		t.Fatal("Get() through a symlinked intermediate component = nil error, want a rejection")
	}
}

// TestFS_RejectsUnsafeKeys proves every listed key form is rejected before
// any filesystem operation happens, for every method on the store.
func TestFS_RejectsUnsafeKeys(t *testing.T) {
	store, err := storage.NewFS(secureTempDir(t), false, false)
	if err != nil {
		t.Fatalf("NewFS() = %v", err)
	}
	ctx := context.Background()

	cases := []struct {
		name string
		key  string
	}{
		{"traversal", "../outside"},
		{"traversal nested", "a/../../outside"},
		{"traversal dot-segment", "a/./b"},
		{"url-encoded percent", "a%2e%2e/b"},
		{"absolute unix", "/etc/passwd"},
		{"backslash", `a\b`},
		{"windows drive", `C:\Windows`},
		{"control character", "a\x00b"},
		{"control newline", "a\nb"},
		{"empty", ""},
		{"empty segment", "a//b"},
		{"trailing slash empty segment", "a/"},
		{"colon", "a:b"},
		{"question mark", "a?b"},
		{"hash", "a#b"},
		{"scheme-like", "file://a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.Put(ctx, tc.key, strings.NewReader("x"), "text/plain"); !errors.Is(err, storage.ErrInvalidKey) {
				t.Errorf("Put(%q) = %v, want ErrInvalidKey", tc.key, err)
			}
			if _, err := store.Get(ctx, tc.key); !errors.Is(err, storage.ErrInvalidKey) {
				t.Errorf("Get(%q) = %v, want ErrInvalidKey", tc.key, err)
			}
			if _, err := store.Exists(ctx, tc.key); !errors.Is(err, storage.ErrInvalidKey) {
				t.Errorf("Exists(%q) = %v, want ErrInvalidKey", tc.key, err)
			}
			if err := store.Delete(ctx, tc.key); !errors.Is(err, storage.ErrInvalidKey) {
				t.Errorf("Delete(%q) = %v, want ErrInvalidKey", tc.key, err)
			}
		})
	}
}

// TestFS_ErrorsNeverRevealThePhysicalPath proves no API ever returns a
// physical filesystem path: not for a missing key, and not for an
// underlying OS error either — a raw *fs.PathError, left unwrapped, would
// otherwise carry the absolute root in its own Error() text.
func TestFS_ErrorsNeverRevealThePhysicalPath(t *testing.T) {
	skipUnlessUnix(t)
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}

	dir := secureTempDir(t)
	store, err := storage.NewFS(dir, false, false)
	if err != nil {
		t.Fatalf("NewFS() = %v", err)
	}
	ctx := context.Background()

	if err := store.Put(ctx, "notes/x.txt", strings.NewReader("secret"), "text/plain"); err != nil {
		t.Fatalf("Put() = %v", err)
	}
	physical := filepath.Join(dir, "notes", "x.txt")
	if err := os.Chmod(physical, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(physical, 0o600) })

	if _, err := store.Get(ctx, "notes/x.txt"); err == nil {
		t.Fatal("Get() = nil error, want a permission error")
	} else if strings.Contains(err.Error(), dir) {
		t.Errorf("Get() error leaks the physical root %q: %v", dir, err)
	}

	if _, err := store.Get(ctx, "missing/key.txt"); !errors.Is(err, storage.ErrNotExist) {
		t.Fatalf("Get() = %v, want ErrNotExist", err)
	} else if strings.Contains(err.Error(), dir) {
		t.Errorf("Get() error leaks the physical root %q: %v", dir, err)
	}

	if err := store.Put(ctx, "/etc/passwd", strings.NewReader("x"), "text/plain"); err == nil {
		t.Fatal("Put() = nil error, want a rejection")
	} else if strings.Contains(err.Error(), dir) {
		t.Errorf("Put() error leaks the physical root %q: %v", dir, err)
	}
}

// secureTempDir returns a fresh temporary directory forced to mode 0700.
// testing.T.TempDir() itself is created by applying the process umask to a
// permissive requested mode, so under a permissive local umask (0002, vs.
// CI's 0022) it comes back group-writable — precisely the condition NewFS
// is meant to reject. Tests that are not themselves exercising that
// rejection need a directory that starts out secure regardless of umask.
func secureTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	return dir
}

func skipUnlessUnix(t *testing.T) {
	t.Helper()
	if !runtimeIsUnix() {
		t.Skip("unix-only permission semantics")
	}
}

func runtimeIsUnix() bool {
	return os.PathSeparator == '/'
}
