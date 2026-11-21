package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
)

// ObjectInfo is what a driver reports about one object when it is listed:
// its unscoped key, its size and, where the driver keeps one, its content
// type (the fs driver does not).
type ObjectInfo struct {
	Key         string
	Size        int64
	ContentType string
}

// Inventory is a driver that can list and size its objects, which the
// ObjectStore port deliberately cannot: modules never list a store. Only
// `vantigo storage copy` (Kubernetes readiness design KR15) uses it. FS
// and S3 both implement it.
type Inventory interface {
	ObjectStore
	// Walk calls fn for every object, in no particular order.
	Walk(ctx context.Context, fn func(ObjectInfo) error) error
	// Stat reports key's size, and whether it exists.
	Stat(ctx context.Context, key string) (ObjectInfo, bool, error)
}

// Pinger is a driver with a readiness check (KR14).
type Pinger interface {
	Ping(ctx context.Context) error
}

// Ping runs store's readiness check: a HeadBucket for s3, a stat of the
// root for fs. An unconfigured store has nothing to check and passes, since
// storage being unset is a supported configuration that fails closed per
// operation rather than at readiness.
func Ping(ctx context.Context, store ObjectStore) error {
	if p, ok := store.(Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

// CopyResult counts what Copy did.
type CopyResult struct {
	Copied  int
	Skipped int
	Bytes   int64
}

// Copy copies every object in src to dst under the same key, skipping
// those dst already holds with the same size unless all is set, so a second
// run copies what is new or has changed size since the first (KR15). An
// object rewritten in place at the same size is skipped too; all copies
// everything, for a final pass that must not miss one. An object that
// vanishes from src between the listing and the read is skipped: the
// source may be serving while it is copied. onCopy, when set, is told each copied key.
func Copy(ctx context.Context, src, dst Inventory, all bool, onCopy func(ObjectInfo)) (CopyResult, error) {
	var res CopyResult
	err := src.Walk(ctx, func(obj ObjectInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !all {
			existing, ok, err := dst.Stat(ctx, obj.Key)
			if err != nil {
				return err
			}
			if ok && existing.Size == obj.Size {
				res.Skipped++
				return nil
			}
		}
		r, err := src.Get(ctx, obj.Key)
		if errors.Is(err, ErrNotExist) {
			res.Skipped++
			return nil
		}
		if err != nil {
			return err
		}
		counted := &countingReader{r: r}
		err = dst.Put(ctx, obj.Key, counted, contentTypeOf(obj))
		_ = r.Close()
		if err != nil {
			return err
		}
		res.Copied++
		res.Bytes += counted.n
		if onCopy != nil {
			onCopy(obj)
		}
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("storage: copy: %w", err)
	}
	return res, nil
}

// contentTypeOf is the object's stored content type, else one guessed from
// its extension. Nothing reads the stored type back to serve a download —
// every module keeps its own in the database — so the guess only makes the
// bucket tidier to browse.
func contentTypeOf(obj ObjectInfo) string {
	if obj.ContentType != "" {
		return obj.ContentType
	}
	if t := mime.TypeByExtension(path.Ext(obj.Key)); t != "" {
		return t
	}
	return "application/octet-stream"
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
