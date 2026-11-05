package products

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is the object store, where a product's images keep their bytes
// (messenger design N13): the normalised file and its thumbnail, in the
// object store under the module's own scope, and everything else about them
// in products.product_images. The two cannot be written in one transaction,
// so the order is staff's photo's: the objects are written before the row's
// transaction and removed again when it fails, and an image's objects are
// removed once the transaction that deleted its row has committed. The store
// is never called inside a transaction: a slow store under a lock would
// starve the pool.

// storageScope is this module's namespace in the object store. Every key the
// module writes is relative to it, and the scoped store refuses to be handed
// its own prefix, so no key here can reach another module's objects.
const storageScope = "products-images"

// objectCleanupTimeout bounds the removal of an image's objects after its
// row's transaction has committed, on a context detached from the request's.
const objectCleanupTimeout = 10 * time.Second

// imageObjectKeys are where a new image's two files go: the product and a
// fresh random name, never derived from the uploaded file's name, so no
// caller chooses a key or reads anything out of one. They are relative to the
// module's scope.
func imageObjectKeys(productID int32) (key, thumbKey string) {
	name := uuid.New().String()
	return fmt.Sprintf("%d/%s", productID, name), fmt.Sprintf("%d/%s-thumb", productID, name)
}

// moduleObjectStore is this module's object store: d.ObjectStore when a
// harness set one, otherwise the module's own scoped wrapper over
// internal/storage.New — staff's and communications' construction. A nil
// d.Config is a bare Deps, which an empty config stands in for: the
// unconfigured, fail-closed store.
func moduleObjectStore(d module.Deps) (storage.ObjectStore, error) {
	if d.ObjectStore != nil {
		return d.ObjectStore, nil
	}
	cfg := d.Config
	if cfg == nil {
		cfg = &config.Config{}
	}
	base, err := storage.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("products: build the object store: %w", err)
	}
	scoped, err := storage.NewScope(base, storageScope)
	if err != nil {
		return nil, fmt.Errorf("products: scope the object store: %w", err)
	}
	return scoped, nil
}

// objects is the module's object store, or the error it could not be built
// with, which every call answers: a process that never touches an image still
// starts.
type objects struct {
	store storage.ObjectStore
	err   error
}

func newObjects(d module.Deps) objects {
	store, err := moduleObjectStore(d)
	return objects{store: store, err: err}
}

func (o objects) get(ctx context.Context, key string) (io.ReadCloser, error) {
	if o.err != nil {
		return nil, o.err
	}
	if o.store == nil {
		return nil, storage.ErrNotConfigured
	}
	return o.store.Get(ctx, key)
}

func (o objects) put(ctx context.Context, key string, r io.Reader, contentType string) error {
	if o.err != nil {
		return o.err
	}
	if o.store == nil {
		return storage.ErrNotConfigured
	}
	return o.store.Put(ctx, key, r, contentType)
}

func (o objects) delete(ctx context.Context, key string) error {
	if o.err != nil {
		return o.err
	}
	if o.store == nil {
		return storage.ErrNotConfigured
	}
	return o.store.Delete(ctx, key)
}

// remove makes one attempt at removing each key, on a context detached from
// the request's and bounded by objectCleanupTimeout, and logs a failure
// naming the product rather than the key — a key in a server log helps
// nobody. An object that stays behind is unreachable and harmless: no row
// names it.
func (s *server) removeObjects(ctx context.Context, productID int32, keys ...string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), objectCleanupTimeout)
	defer cancel()
	for _, key := range keys {
		if err := s.objects.delete(cleanup, key); err != nil {
			s.deps.Logger.ErrorContext(cleanup, "products: an image object could not be removed from the store",
				"product_id", productID, "error", err.Error())
		}
	}
}
