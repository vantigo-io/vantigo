// Package storage is the application object-storage port: an ObjectStore
// with Put, Get, Exists and Delete; a scope wrapper that isolates every
// caller under its own canonical "{scope}/" prefix; and an fs driver
// (fs.go) that stores objects on the local filesystem.
//
// This is a single-tenant port of Vantigo.Storage.Abstractions /
// Vantigo.Storage (packages/storage). .NET's physical key is
// tenants/{tenant-id}/{scope}/{key}; the tenant segment is dropped with
// tenancy, so the physical key here is just "{scope}/{key}"
// (docs/storage.md "Tenant isolation, module scopes, and downloads").
//
// New picks the driver from config.Config.StorageProvider: "fs" (fs.go)
// stores objects under StorageFSRoot. When StorageProvider is unset, New
// still returns a usable store — the process starts — but every operation
// on it reports ErrNotConfigured, mirroring .NET's host, which starts with
// storage fail-closed when Storage__Provider is omitted (docs/storage.md).
//
// There is deliberately no presigned-URL operation and no API here ever
// returns a physical filesystem path: downloads stream through an
// authorised application endpoint instead (docs/storage.md).
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// maxKeyBytes bounds a key (or a scope-combined key) in UTF-8 bytes,
// mirroring Vantigo.Storage.Validation.StorageKey.MaximumUtf8Bytes.
const maxKeyBytes = 1024

// ErrNotConfigured is returned by every ObjectStore operation when
// STORAGE_PROVIDER is unset: the process starts, but storage fails closed
// (docs/storage.md).
var ErrNotConfigured = errors.New("storage: not configured")

// ErrNotExist is the sentinel Get and Delete report for a key that has
// never been written, so callers can distinguish "does not exist" from any
// other failure with errors.Is. Deleting a missing key still succeeds — see
// Delete.
var ErrNotExist = errors.New("storage: key does not exist")

// ErrInvalidKey is wrapped by every rejection Put, Get, Exists and Delete
// make of a key that fails validation: traversal, URL-encoded, absolute,
// backslash, control-character and duplicate-prefix forms among them.
var ErrInvalidKey = errors.New("storage: invalid key")

// ErrInvalidScope is wrapped by every rejection of a scope name that is not
// a canonical lowercase [a-z0-9-] name.
var ErrInvalidScope = errors.New("storage: invalid scope")

// ObjectStore stores and retrieves opaque objects addressed by an
// application-level key. It never exposes a physical provider path: callers
// authorised to read an object get a stream, never a location.
type ObjectStore interface {
	// Put stores the content read from r under key, replacing any existing
	// object at that key. The caller retains ownership of r.
	Put(ctx context.Context, key string, r io.Reader, contentType string) error
	// Get returns the content stored at key. The caller must Close the
	// returned stream. Get returns an error wrapping ErrNotExist when key
	// has never been written.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Exists reports whether key has been written.
	Exists(ctx context.Context, key string) (bool, error)
	// Delete removes key. Deleting a key that does not exist succeeds.
	Delete(ctx context.Context, key string) error
}

// New builds the ObjectStore config.Config selects. StorageProvider "fs"
// selects NewFS, rooted at StorageFSRoot. An unset StorageProvider is not a
// construction error: New still returns a store, whose every operation
// reports ErrNotConfigured (docs/storage.md's fail-closed host).
func New(cfg *config.Config) (ObjectStore, error) {
	switch cfg.StorageProvider {
	case "":
		return unconfiguredStore{}, nil
	case "fs":
		return NewFS(cfg.StorageFSRoot, cfg.StorageFSAllowInsecureRoot, cfg.IsDevelopment())
	default:
		return nil, fmt.Errorf("storage: unknown provider %q", cfg.StorageProvider)
	}
}

// unconfiguredStore is the ObjectStore New returns when StorageProvider is
// unset: every operation fails closed with ErrNotConfigured, rather than
// panicking or silently succeeding as a no-op.
type unconfiguredStore struct{}

func (unconfiguredStore) Put(context.Context, string, io.Reader, string) error {
	return ErrNotConfigured
}

func (unconfiguredStore) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, ErrNotConfigured
}

func (unconfiguredStore) Exists(context.Context, string) (bool, error) {
	return false, ErrNotConfigured
}

func (unconfiguredStore) Delete(context.Context, string) error {
	return ErrNotConfigured
}

// scopeName is the canonical lowercase ASCII scope pattern: letters and
// digits, hyphens only between them, no slashes or dots
// (Vantigo.Storage.Validation.StorageKey.ScopeRegex).
var scopeName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

// maxScopeLength mirrors StorageKey.ValidateScope's own bound.
const maxScopeLength = 64

// validateScope reports whether scope is a safe, canonical storage scope
// name.
func validateScope(scope string) error {
	if scope == "" || len(scope) > maxScopeLength || !scopeName.MatchString(scope) {
		return fmt.Errorf("%w: %q is not a safe storage scope", ErrInvalidScope, scope)
	}
	return nil
}

// scopedStore prefixes every key it is given with its scope before
// delegating to inner, so a module using it can never reach outside its own
// namespace no matter what key it constructs.
type scopedStore struct {
	inner ObjectStore
	scope string
}

// NewScope wraps inner so every key passed to Put, Get, Exists and Delete
// is combined with scope as "{scope}/{key}" before reaching inner. scope
// must be a canonical lowercase [a-z0-9-] name; the relative keys callers
// pass must not repeat it (docs/storage.md).
func NewScope(inner ObjectStore, scope string) (ObjectStore, error) {
	if inner == nil {
		return nil, errors.New("storage: NewScope requires a non-nil store")
	}
	if err := validateScope(scope); err != nil {
		return nil, err
	}
	return &scopedStore{inner: inner, scope: scope}, nil
}

func (s *scopedStore) Put(ctx context.Context, key string, r io.Reader, contentType string) error {
	combined, err := combine(s.scope, key)
	if err != nil {
		return err
	}
	return s.inner.Put(ctx, combined, r, contentType)
}

func (s *scopedStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	combined, err := combine(s.scope, key)
	if err != nil {
		return nil, err
	}
	return s.inner.Get(ctx, combined)
}

func (s *scopedStore) Exists(ctx context.Context, key string) (bool, error) {
	combined, err := combine(s.scope, key)
	if err != nil {
		return false, err
	}
	return s.inner.Exists(ctx, combined)
}

func (s *scopedStore) Delete(ctx context.Context, key string) error {
	combined, err := combine(s.scope, key)
	if err != nil {
		return err
	}
	return s.inner.Delete(ctx, combined)
}

// combine validates scope and relativeKey and returns the physical key
// "{scope}/{relativeKey}". A relative key that equals the scope, or already
// starts with "{scope}/", is refused: a scoped store accepts only relative
// keys and must never be handed its own scope prefix
// (Vantigo.Storage.Validation.StorageKey.ValidateRelativeKey).
func combine(scope, relativeKey string) (string, error) {
	if err := validateScope(scope); err != nil {
		return "", err
	}
	if err := validateKey(relativeKey); err != nil {
		return "", err
	}
	if relativeKey == scope || strings.HasPrefix(relativeKey, scope+"/") {
		return "", fmt.Errorf("%w: a scoped store accepts relative keys and must not be given its own %q scope prefix", ErrInvalidKey, scope)
	}
	combined := scope + "/" + relativeKey
	if len(combined) > maxKeyBytes {
		return "", fmt.Errorf("%w: combined key is too long", ErrInvalidKey)
	}
	return combined, nil
}
