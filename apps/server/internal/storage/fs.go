package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// FS is the ObjectStore driver backed by the local filesystem. Every
// filesystem operation is performed through root, an *os.Root opened once
// on the storage root at construction: os.Root resolves every name beneath
// it with openat-style containment at the moment of use, not a
// check-then-open pair, so a symlink planted inside the root after
// construction — even one timed to land between a validity check and the
// syscall that acts on it — still cannot make an operation land outside
// root. validateKey stays in front of every operation regardless: it is
// what gives a caller a clear, typed rejection for nonsense input and
// rejects it before any syscall runs; os.Root is what guarantees
// containment for whatever survives that check. The two are complementary,
// not redundant — see the "why not just os.Root" note on ensureSecureDirectory
// for the one thing os.Root does not cover.
type FS struct {
	root              *os.Root
	allowInsecureRoot bool
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
//
// The checks below, up to os.OpenRoot, run on the plain filesystem: os.Root
// can only be opened on a directory that already exists securely, so
// establishing that the directory exists, is not a symlink, and (if it
// already existed) is not insecurely permissioned has to happen first, with
// the ordinary os package. Every operation after that point — Put, Get,
// Exists, Delete, and the directory-permission walk in
// ensureSecureDirectory — goes through the opened Root instead.
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
	// replaced with a symlink between the Lstat above and here.
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("storage: statting fs root: %w", stripPath(err))
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, errors.New("storage: fs root must not be a symbolic link")
	}

	osRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("storage: opening fs root: %w", stripPath(err))
	}

	store := &FS{root: osRoot, allowInsecureRoot: allowInsecureRoot}
	if err := store.probeWritable(); err != nil {
		_ = osRoot.Close()
		return nil, err
	}
	return store, nil
}

// probeWritable creates and removes a throwaway file directly under root,
// so a root that exists but is not writable by this process fails at
// construction rather than lazily, on the first Put.
func (f *FS) probeWritable() error {
	name, file, err := f.createTemp(".", "storage-writable-check")
	if err != nil {
		return fmt.Errorf("storage: fs root is not writable: %w", stripPath(err))
	}
	_ = file.Close()
	_ = f.root.Remove(name)
	return nil
}

// createTemp creates a new, exclusively-owned file beneath dirRel (a "."
// meaning root itself), named after base with a random suffix, and returns
// its name (relative to root, suitable for a further Root call) and the
// open file. It is os.CreateTemp's collision-retry loop reimplemented
// against *os.Root, which has no CreateTemp of its own.
func (f *FS) createTemp(dirRel, base string) (string, *os.File, error) {
	for range 10000 {
		suffix, err := randomHex(12)
		if err != nil {
			return "", nil, fmt.Errorf("generating a temporary file name: %w", err)
		}
		name := "." + base + "." + suffix + ".tmp"
		if dirRel != "." {
			name = dirRel + "/" + name
		}
		file, err := f.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return name, file, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", nil, err
		}
	}
	return "", nil, errors.New("could not create a unique temporary file")
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Put stores the content read from r under key, replacing any existing
// object at that key. The write lands through a temporary file created in
// key's parent directory (so the final rename stays within one directory
// tree) and an atomic Root.Rename over the target — a reader never observes
// a partial write.
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
	if err := validateKey(key); err != nil {
		return err
	}

	rel := filepath.FromSlash(key)
	dirRel := filepath.Dir(rel) // "." when key has no "/"

	if dirRel != "." {
		if err := f.root.MkdirAll(dirRel, rootMode); err != nil {
			return fmt.Errorf("storage: put %q: %w", key, stripPath(err))
		}
	}
	if err := f.ensureSecureDirectory(dirRel); err != nil {
		return fmt.Errorf("storage: put %q: %w", key, err)
	}

	tmpName, tmp, err := f.createTemp(dirRel, filepath.Base(rel))
	if err != nil {
		return fmt.Errorf("storage: put %q: %w", key, stripPath(err))
	}
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = f.root.Remove(tmpName)
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
	if err := f.root.Rename(tmpName, rel); err != nil {
		return fmt.Errorf("storage: put %q: %w", key, stripPath(err))
	}
	cleanupTemp = false
	return nil
}

// Get returns the content stored at key, or an error wrapping ErrNotExist
// when key has never been written or does not name a regular file — a
// directory is not an object, the same distinction Exists makes with
// info.Mode().IsRegular(). The caller must Close the returned stream.
func (f *FS) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateKey(key); err != nil {
		return nil, err
	}
	rel := filepath.FromSlash(key)

	file, err := f.root.Open(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("storage: get %q: %w", key, ErrNotExist)
		}
		return nil, fmt.Errorf("storage: get %q: %w", key, stripPath(err))
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("storage: get %q: %w", key, stripPath(err))
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("storage: get %q: %w", key, ErrNotExist)
	}
	return file, nil
}

// Exists reports whether key names a regular file — a directory a Put
// created to hold a nested key does not count as an object that "exists".
func (f *FS) Exists(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := validateKey(key); err != nil {
		return false, err
	}
	rel := filepath.FromSlash(key)
	info, err := f.root.Stat(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("storage: exists %q: %w", key, stripPath(err))
	}
	return info.Mode().IsRegular(), nil
}

// Delete removes key. Deleting a key that does not exist succeeds. A key
// that resolves to a directory is also left alone and reported as success,
// mirroring .NET's own File.Exists-gated delete: a directory a Put created
// to hold nested keys is not an object this call is entitled to remove.
func (f *FS) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateKey(key); err != nil {
		return err
	}
	rel := filepath.FromSlash(key)
	info, err := f.root.Stat(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("storage: delete %q: %w", key, stripPath(err))
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	if err := f.root.Remove(rel); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("storage: delete %q: %w", key, stripPath(err))
	}
	return nil
}

// ensureSecureDirectory walks dirRel (relative to root; "." is a no-op,
// since NewFS already checked the root itself) one segment at a time and,
// for every segment that already exists, refuses a group- or
// world-writable directory unless allowInsecureRoot, then — when
// allowInsecureRoot is false — hardens it to rootMode. Ported from
// Vantigo.Storage's LocalFileObjectStore.EnsureSecureDirectoryPath
// (packages/storage/Vantigo.Storage/Providers/Local/LocalFileObjectStore.cs:183),
// called from Put the same way .NET calls it: after the directory is
// created, before the object is written into it.
//
// Why this still walks by hand even with os.Root in place: os.Root
// guarantees path *containment* (nothing escapes root), but says nothing
// about the *permission bits* of the directories a key descends through.
// A subdirectory that is itself group- or world-writable is fully "inside"
// the root and passes every os.Root call without complaint, yet still lets
// another local user tamper with objects beneath it — the same risk the
// root's own permission check exists to catch, just one or more levels
// deeper. NewFS's root check alone would miss it entirely.
func (f *FS) ensureSecureDirectory(dirRel string) error {
	if dirRel == "" || dirRel == "." {
		return nil
	}
	var current string
	for _, seg := range strings.Split(dirRel, string(filepath.Separator)) {
		if seg == "" || seg == "." {
			continue
		}
		if current == "" {
			current = seg
		} else {
			current += string(filepath.Separator) + seg
		}
		info, err := f.root.Lstat(current)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("checking directory: %w", stripPath(err))
		}
		if !info.IsDir() {
			continue
		}
		if info.Mode().Perm()&insecureUnixMode != 0 && !f.allowInsecureRoot {
			return errors.New("storage directory must not be group- or world-writable; set STORAGE_FS_ALLOW_INSECURE_ROOT=1 in development to relax this check")
		}
		if !f.allowInsecureRoot {
			if err := f.root.Chmod(current, rootMode); err != nil {
				return fmt.Errorf("securing directory: %w", stripPath(err))
			}
		}
	}
	return nil
}

// stripPath removes the physical path *fs.PathError and *os.LinkError
// otherwise carry in their own Error() text, so a wrapped OS error never
// leaks the storage root through an API's error message — no API here
// returns a physical filesystem path (docs/storage.md). This still matters
// with os.Root in place: the bootstrap checks in NewFS run before the Root
// exists and still use the plain os package on root's absolute path, and
// even os.Root's own *fs.PathError values (whose Path field is already
// only ever the relative key, never the physical root — verified against
// go1.27.0) are passed through stripPath too, for one consistent path
// regardless of which layer produced the error.
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

// maxSegmentBytes bounds a single "/"-delimited key segment, mirroring the
// common filesystem NAME_MAX (255 bytes on Linux): a longer segment is
// classified as ErrInvalidKey here, before any syscall runs, rather than
// surfacing as a raw "file name too long" once it reaches the filesystem.
const maxSegmentBytes = 255

// validateKey reports whether key is a safe object storage key: non-empty,
// at most maxKeyBytes overall and maxSegmentBytes per "/"-delimited
// segment, containing no backslash, percent (URL-encoded forms), colon,
// question mark, hash, "://", control character, or leading/absolute path
// form, and made of non-empty segments none of which is "." or ".."
// (traversal). Mirrors Vantigo.Storage.Validation.StorageKey.ValidateKeyCore.
// It says nothing about scope: the duplicate-prefix check lives in
// combine, since only a scoped store knows what its own prefix is.
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
		if len(seg) > maxSegmentBytes {
			return fmt.Errorf("%w: %q has a path segment longer than %d bytes", ErrInvalidKey, key, maxSegmentBytes)
		}
	}
	return nil
}
