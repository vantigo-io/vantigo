package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// insecureUnixMode is the set of Unix permission bits that make a directory
// group- or world-writable.
const insecureUnixMode = 0o022

// rootMode is the mode a freshly created root, or its subdirectories, gets:
// the app identity only.
const rootMode = 0o700

// fileMode is the mode every object file gets: the app identity only,
// read/write, never executable.
const fileMode = 0o600

// FS is the ObjectStore driver backed by the local filesystem. Every key
// resolves to a path under root; Put writes through a temporary file and an
// atomic rename so a reader never observes a partial write; root and
// resolved paths are checked for symbolic-link components before every
// operation. This is defense in depth, not a claim of cross-platform
// TOCTOU-proof openat semantics — protect the root with deployment
// ownership and filesystem policy (docs/storage.md).
type FS struct {
	root string
}

// NewFS builds an FS store rooted at root: an absolute, writable directory
// that must exist or be creatable at initialisation, and must not itself be
// a symbolic link. Outside development the root must not be group- or
// world-writable; allowInsecureRoot relaxes only that one check, and only
// when isDevelopment is true. NewFS refuses allowInsecureRoot when
// isDevelopment is false even if the root's permissions are already secure
// — config.Load already rejects STORAGE_FS_ALLOW_INSECURE_ROOT outside
// development, but NewFS refuses the combination defensively too, the same
// duplication NewSMTP makes for its own TLS="none" check, since a caller
// could construct FS directly.
func NewFS(root string, allowInsecureRoot, isDevelopment bool) (*FS, error) {
	if allowInsecureRoot && !isDevelopment {
		return nil, errors.New("storage: STORAGE_FS_ALLOW_INSECURE_ROOT is only permitted when the environment is development")
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("storage: fs root is required")
	}
	if !filepath.IsAbs(root) {
		return nil, errors.New("storage: fs root must be an absolute path")
	}
	root = filepath.Clean(root)

	existed := false
	if info, err := os.Lstat(root); err == nil {
		existed = true
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, errors.New("storage: fs root must not be a symbolic link")
		}
		if !info.IsDir() {
			return nil, errors.New("storage: fs root exists and is not a directory")
		}
		if info.Mode().Perm()&insecureUnixMode != 0 && !allowInsecureRoot {
			return nil, errors.New("storage: fs root must not be group- or world-writable; set STORAGE_FS_ALLOW_INSECURE_ROOT=1 in development to relax this check")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("storage: statting fs root: %w", stripPath(err))
	}

	if err := os.MkdirAll(root, rootMode); err != nil {
		return nil, fmt.Errorf("storage: creating fs root: %w", stripPath(err))
	}
	if !existed {
		if err := os.Chmod(root, rootMode); err != nil {
			return nil, fmt.Errorf("storage: securing fs root: %w", stripPath(err))
		}
	}

	// Re-check after creation: MkdirAll on an already-existing path is a
	// no-op, but this also catches a root that a concurrent process
	// replaced with a symlink between the Lstat above and here — defense in
	// depth, not a TOCTOU-proof guarantee.
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("storage: statting fs root: %w", stripPath(err))
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, errors.New("storage: fs root must not be a symbolic link")
	}

	store := &FS{root: root}
	if err := store.probeWritable(); err != nil {
		return nil, err
	}
	return store, nil
}

// probeWritable creates and removes a throwaway file directly under root,
// so a root that exists but is not writable by this process fails at
// construction rather than lazily, on the first Put.
func (f *FS) probeWritable() error {
	tmp, err := os.CreateTemp(f.root, ".storage-writable-check-*.tmp")
	if err != nil {
		return fmt.Errorf("storage: fs root is not writable: %w", stripPath(err))
	}
	name := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(name)
	return nil
}

// Put stores the content read from r under key, replacing any existing
// object at that key. The write lands through a temporary file created in
// key's parent directory (so the final rename stays on one filesystem) and
// an atomic os.Rename over the target — a reader never observes a partial
// write.
func (f *FS) Put(ctx context.Context, key string, r io.Reader, contentType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("storage: put %q: content is required", key)
	}
	if strings.TrimSpace(contentType) == "" {
		return fmt.Errorf("storage: put %q: content type is required", key)
	}

	path, err := f.resolvePath(key)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, rootMode); err != nil {
		return fmt.Errorf("storage: put %q: %w", key, stripPath(err))
	}
	if err := f.ensureNoSymlink(dir); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("storage: put %q: %w", key, stripPath(err))
	}
	tmpName := tmp.Name()
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("storage: put %q: %w", key, stripPath(err))
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("storage: put %q: %w", key, stripPath(err))
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("storage: put %q: %w", key, stripPath(err))
	}
	if err := os.Chmod(tmpName, fileMode); err != nil {
		return fmt.Errorf("storage: put %q: %w", key, stripPath(err))
	}
	if err := f.ensureNoSymlink(tmpName); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("storage: put %q: %w", key, stripPath(err))
	}
	cleanupTemp = false
	return nil
}

// Get returns the content stored at key, or an error wrapping ErrNotExist
// when key has never been written. The caller must Close the returned
// stream.
func (f *FS) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := f.resolvePath(key)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("storage: get %q: %w", key, ErrNotExist)
		}
		return nil, fmt.Errorf("storage: get %q: %w", key, stripPath(err))
	}
	return file, nil
}

// Exists reports whether key has been written.
func (f *FS) Exists(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	path, err := f.resolvePath(key)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("storage: exists %q: %w", key, stripPath(err))
	}
	return info.Mode().IsRegular(), nil
}

// Delete removes key. Deleting a key that does not exist succeeds.
func (f *FS) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := f.resolvePath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("storage: delete %q: %w", key, stripPath(err))
	}
	return nil
}

// resolvePath validates key and returns its absolute path under root. The
// prefix check is defense in depth: a validated key (no "..", not absolute,
// no empty segment) cannot resolve outside root by construction, but the
// check stays in case that invariant is ever weakened.
func (f *FS) resolvePath(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	path := filepath.Join(f.root, filepath.FromSlash(key))
	prefix := f.root + string(filepath.Separator)
	if path == f.root || !strings.HasPrefix(path, prefix) {
		return "", fmt.Errorf("%w: key resolves outside the storage root", ErrInvalidKey)
	}
	if err := f.ensureNoSymlink(path); err != nil {
		return "", err
	}
	return path, nil
}

// ensureNoSymlink refuses path if root itself, or any path component
// between root and path, is a symbolic link — checked on every operation,
// not only at construction, since a root that was clean at startup can be
// altered afterwards. A component that does not exist yet is not an error:
// there is nothing there to be a symlink.
func (f *FS) ensureNoSymlink(path string) error {
	if err := lstatNotSymlink(f.root); err != nil {
		return err
	}
	rel, err := filepath.Rel(f.root, path)
	if err != nil || rel == "." {
		return nil
	}
	current := f.root
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, seg)
		if err := lstatNotSymlink(current); err != nil {
			return err
		}
	}
	return nil
}

func lstatNotSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("storage: checking path: %w", stripPath(err))
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%w: storage path must not contain a symbolic link", ErrInvalidKey)
	}
	return nil
}

// stripPath removes the physical path *fs.PathError and *os.LinkError
// otherwise carry in their own Error() text, so a wrapped OS error never
// leaks the storage root through an API's error message — no API here
// returns a physical filesystem path (docs/storage.md).
func stripPath(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return fmt.Errorf("%s: %w", pathErr.Op, pathErr.Err)
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return fmt.Errorf("%s: %w", linkErr.Op, linkErr.Err)
	}
	return err
}

// validateKey reports whether key is a safe object storage key: non-empty,
// at most maxKeyBytes, containing no backslash, percent (URL-encoded
// forms), colon, question mark, hash, "://", control character, or
// leading/absolute path form, and made of non-empty segments none of which
// is "." or ".." (traversal). Mirrors
// Vantigo.Storage.Validation.StorageKey.ValidateKeyCore. It says nothing
// about scope: the duplicate-prefix check lives in combine, since only a
// scoped store knows what its own prefix is.
func validateKey(key string) error {
	if key == "" || strings.TrimSpace(key) == "" {
		return fmt.Errorf("%w: key must not be empty", ErrInvalidKey)
	}
	if len(key) > maxKeyBytes {
		return fmt.Errorf("%w: key is too long", ErrInvalidKey)
	}
	if key[0] == '/' || key[0] == '\\' {
		return fmt.Errorf("%w: %q must not be an absolute path", ErrInvalidKey, key)
	}
	if filepath.IsAbs(key) {
		return fmt.Errorf("%w: %q must not be an absolute path", ErrInvalidKey, key)
	}
	if strings.ContainsAny(key, `\%:?#`) {
		return fmt.Errorf("%w: %q is not a safe object storage key", ErrInvalidKey, key)
	}
	if strings.Contains(key, "://") {
		return fmt.Errorf("%w: %q is not a safe object storage key", ErrInvalidKey, key)
	}
	for _, r := range key {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %q contains a control character", ErrInvalidKey, key)
		}
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w: %q is not a safe object storage key", ErrInvalidKey, key)
		}
	}
	return nil
}
