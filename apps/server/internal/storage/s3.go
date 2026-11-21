package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// s3PartSize is the multipart part size, and the size below which Put sends
// one PUT instead (Kubernetes readiness design KR13).
const s3PartSize = 16 << 20

// S3Options are the settings NewS3 needs: config.Config's STORAGE_S3_*
// values (KR11), already validated by config.Load.
type S3Options struct {
	// Endpoint is the service's host, with a port when it is not the
	// scheme's default: no scheme, no path.
	Endpoint string
	// Secure selects https. config.Load allows http only in development.
	Secure          bool
	Region          string
	Bucket          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	// PathStyle addresses the bucket as https://endpoint/bucket/key rather
	// than as a subdomain; UpCloud and MinIO want it.
	PathStyle bool
	// Transport replaces the HTTP transport; nil keeps minio-go's own.
	// Tests point it at an in-process fake.
	Transport http.RoundTripper
}

// S3 is the ObjectStore driver for any S3-compatible service (KR11–KR13),
// built on minio-go (KR12). The physical key is "{prefix/}{scope}/{key}",
// the fs driver's relative path, so `vantigo storage copy` maps one to the
// other without renaming.
//
// The client never sends trailing checksums (minio-go adds them only with
// Options.TrailingHeaders, left off), which several S3-compatible services,
// UpCloud's among them, reject.
type S3 struct {
	client *minio.Client
	bucket string
	prefix string // "" or "{prefix}/"
}

// NewS3 builds an S3 store. It contacts nothing: a missing bucket or
// refused credentials show in Ping, the readiness check (KR14), and in the
// first operation, never as a startup failure.
func NewS3(opts S3Options) (*S3, error) {
	switch {
	case strings.TrimSpace(opts.Endpoint) == "":
		return nil, errors.New("storage: s3 endpoint is required")
	case strings.TrimSpace(opts.Bucket) == "":
		return nil, errors.New("storage: s3 bucket is required")
	case opts.AccessKeyID == "" || opts.SecretAccessKey == "":
		return nil, errors.New("storage: s3 credentials are required")
	}
	prefix := ""
	if opts.Prefix != "" {
		if err := validateKey(opts.Prefix); err != nil {
			return nil, fmt.Errorf("storage: s3 prefix: %w", err)
		}
		prefix = opts.Prefix + "/"
	}
	lookup := minio.BucketLookupDNS
	if opts.PathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(opts.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(opts.AccessKeyID, opts.SecretAccessKey, ""),
		Secure:       opts.Secure,
		Region:       opts.Region,
		BucketLookup: lookup,
		Transport:    opts.Transport,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: s3 client: %w", err)
	}
	return &S3{client: client, bucket: opts.Bucket, prefix: prefix}, nil
}

func (s *S3) physical(key string) string { return s.prefix + key }

// Put stores r under key, replacing any object there. Content that fits in
// one part goes as a single PUT; anything larger streams as a multipart
// upload of s3PartSize parts, so memory stays bounded by one part whatever
// the object's size (KR13).
func (s *S3) Put(ctx context.Context, key string, r io.Reader, contentType string) error {
	if r == nil {
		return fmt.Errorf("storage: put %q: content is required", key)
	}
	if strings.TrimSpace(contentType) == "" {
		return fmt.Errorf("storage: put %q: content type is required", key)
	}
	if err := validateKey(key); err != nil {
		return err
	}
	opts := minio.PutObjectOptions{ContentType: contentType, PartSize: s3PartSize}

	// Read up to one byte past a part, into a buffer that grows only as far
	// as the content does, so a small attachment costs its own size: what
	// fits in a part goes as one PUT of known length, anything longer
	// streams as multipart of unknown length.
	var head bytes.Buffer
	n, err := io.CopyN(&head, r, s3PartSize+1)
	switch {
	case errors.Is(err, io.EOF):
		_, err = s.client.PutObject(ctx, s.bucket, s.physical(key), &head, n, opts)
	case err != nil:
		return fmt.Errorf("storage: put %q: %w", key, err)
	default:
		_, err = s.client.PutObject(ctx, s.bucket, s.physical(key), io.MultiReader(&head, r), -1, opts)
	}
	if err != nil {
		return fmt.Errorf("storage: put %q: %w", key, err)
	}
	return nil
}

// Get returns the content at key, or an error wrapping ErrNotExist when
// there is none. minio-go's GetObject is lazy; Stat sends the request, so a
// missing key is reported here rather than on the caller's first Read.
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, s.physical(key), minio.GetObjectOptions{})
	if err != nil {
		return nil, s.wrap("get", key, err)
	}
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		if isNotFound(err) {
			if berr := s.confirmBucket(ctx, "get", key); berr != nil {
				return nil, berr
			}
		}
		return nil, s.wrap("get", key, err)
	}
	return obj, nil
}

// Exists is a HEAD on the key.
func (s *S3) Exists(ctx context.Context, key string) (bool, error) {
	if err := validateKey(key); err != nil {
		return false, err
	}
	_, err := s.client.StatObject(ctx, s.bucket, s.physical(key), minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return false, s.confirmBucket(ctx, "exists", key)
		}
		return false, s.wrap("exists", key, err)
	}
	return true, nil
}

// Delete removes key; a missing key succeeds, as S3 itself answers.
func (s *S3) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	err := s.client.RemoveObject(ctx, s.bucket, s.physical(key), minio.RemoveObjectOptions{})
	if err != nil && !isNotFound(err) {
		return s.wrap("delete", key, err)
	}
	return nil
}

// Ping is the readiness check (KR14): a HeadBucket. A missing bucket or
// refused credentials fail it.
func (s *S3) Ping(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("storage: s3 bucket %q: %w", s.bucket, err)
	}
	if !ok {
		return fmt.Errorf("storage: s3 bucket %q does not exist", s.bucket)
	}
	return nil
}

// Walk calls fn for every object under the store's prefix, with its key
// relative to the prefix. It is what `vantigo storage copy` reads.
func (s *S3) Walk(ctx context.Context, fn func(ObjectInfo) error) error {
	for obj := range s.client.ListObjectsIter(ctx, s.bucket, minio.ListObjectsOptions{Prefix: s.prefix, Recursive: true}) {
		if obj.Err != nil {
			return fmt.Errorf("storage: s3 list: %w", obj.Err)
		}
		key := strings.TrimPrefix(obj.Key, s.prefix)
		if key == "" || strings.HasSuffix(key, "/") {
			continue
		}
		if err := fn(ObjectInfo{Key: key, Size: obj.Size, ContentType: obj.ContentType}); err != nil {
			return err
		}
	}
	return nil
}

// Stat reports key's size, and whether it exists.
func (s *S3) Stat(ctx context.Context, key string) (ObjectInfo, bool, error) {
	if err := validateKey(key); err != nil {
		return ObjectInfo{}, false, err
	}
	info, err := s.client.StatObject(ctx, s.bucket, s.physical(key), minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return ObjectInfo{}, false, s.confirmBucket(ctx, "stat", key)
		}
		return ObjectInfo{}, false, s.wrap("stat", key, err)
	}
	return ObjectInfo{Key: key, Size: info.Size, ContentType: info.ContentType}, true, nil
}

// confirmBucket follows a HEAD's 404, which has no body and so cannot say
// whether the key or the bucket is missing (minio-go reports both as
// NoSuchKey): it is nil when the bucket exists, so the answer really is "no
// such key", and an error otherwise, so a deleted or misnamed bucket never
// reads as an empty one.
func (s *S3) confirmBucket(ctx context.Context, op, key string) error {
	if err := s.Ping(ctx); err != nil {
		return fmt.Errorf("storage: %s %q: %w", op, key, err)
	}
	return nil
}

func (s *S3) wrap(op, key string, err error) error {
	if isNotFound(err) {
		return fmt.Errorf("storage: %s %q: %w", op, key, ErrNotExist)
	}
	return fmt.Errorf("storage: %s %q: %w", op, key, err)
}

// isNotFound reports a missing key: NoSuchKey from a GET, or the bare 404 a
// HEAD answers with, which minio-go also calls NoSuchKey whether the key or
// the bucket is missing; Exists and Stat tell the two apart with
// confirmBucket.
func isNotFound(err error) bool {
	resp := minio.ToErrorResponse(err)
	return resp.Code == minio.NoSuchKey || (resp.StatusCode == http.StatusNotFound && resp.Code != minio.NoSuchBucket)
}
