package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestS3(t *testing.T, mutate func(*S3Options)) (*fakeS3, *S3) {
	t.Helper()
	fake, opts := newFakeS3(t, "tenant")
	if mutate != nil {
		mutate(&opts)
	}
	store, err := NewS3(opts)
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	return fake, store
}

func readAll(t *testing.T, store ObjectStore, key string) []byte {
	t.Helper()
	r, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	return b
}

// TestS3_RoundTrip covers the port: Put stores with its content type and
// overwrites, Get and Exists find it, a missing key is ErrNotExist and
// false, and Delete succeeds twice.
func TestS3_RoundTrip(t *testing.T) {
	ctx := context.Background()
	fake, store := newTestS3(t, nil)

	if err := store.Put(ctx, "invoices/a.pdf", strings.NewReader("first"), "application/pdf"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Put(ctx, "invoices/a.pdf", strings.NewReader("second"), "application/pdf"); err != nil {
		t.Fatalf("Put over: %v", err)
	}
	if got := readAll(t, store, "invoices/a.pdf"); string(got) != "second" {
		t.Errorf("Get = %q, want the overwrite", got)
	}
	if obj := fake.objects("tenant")["invoices/a.pdf"]; obj.contentType != "application/pdf" {
		t.Errorf("stored content type = %q", obj.contentType)
	}
	if ok, err := store.Exists(ctx, "invoices/a.pdf"); err != nil || !ok {
		t.Errorf("Exists = %v, %v", ok, err)
	}

	if _, err := store.Get(ctx, "invoices/missing.pdf"); !errors.Is(err, ErrNotExist) {
		t.Errorf("Get missing = %v, want ErrNotExist", err)
	}
	if ok, err := store.Exists(ctx, "invoices/missing.pdf"); err != nil || ok {
		t.Errorf("Exists missing = %v, %v", ok, err)
	}

	for range 2 {
		if err := store.Delete(ctx, "invoices/a.pdf"); err != nil {
			t.Errorf("Delete: %v", err)
		}
	}
	if ok, _ := store.Exists(ctx, "invoices/a.pdf"); ok {
		t.Error("Exists after Delete = true")
	}
}

// TestS3_Prefix puts the physical key under the prefix, through a scope:
// "{prefix/}{scope}/{key}".
func TestS3_Prefix(t *testing.T) {
	ctx := context.Background()
	fake, store := newTestS3(t, func(o *S3Options) { o.Prefix = "tenants/acme" })
	scoped, err := NewScope(store, "invoices")
	if err != nil {
		t.Fatal(err)
	}
	if err := scoped.Put(ctx, "2026/a.pdf", strings.NewReader("x"), "application/pdf"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, ok := fake.objects("tenant")["tenants/acme/invoices/2026/a.pdf"]; !ok {
		t.Errorf("objects = %v, want the prefixed physical key", fake.objects("tenant"))
	}
	if got := readAll(t, scoped, "2026/a.pdf"); string(got) != "x" {
		t.Errorf("Get = %q", got)
	}

	var keys []string
	if err := store.Walk(ctx, func(o ObjectInfo) error { keys = append(keys, o.Key); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "invoices/2026/a.pdf" {
		t.Errorf("Walk = %v, want keys relative to the prefix", keys)
	}
}

// TestS3_PutStreamsMultipart sends content of one part or less as a single
// PUT and anything longer as a multipart upload of 16 MiB parts, and never
// asks for a trailing checksum, which UpCloud rejects.
func TestS3_PutStreamsMultipart(t *testing.T) {
	ctx := context.Background()
	fake, store := newTestS3(t, nil)

	exact := bytes.Repeat([]byte("a"), s3PartSize)
	if err := store.Put(ctx, "exact", bytes.NewReader(exact), "application/octet-stream"); err != nil {
		t.Fatalf("Put exact: %v", err)
	}
	if fake.singlePuts != 1 || fake.multipartDone != 0 {
		t.Fatalf("a %d-byte object: %d single, %d multipart; want one single PUT", s3PartSize, fake.singlePuts, fake.multipartDone)
	}

	big := bytes.Repeat([]byte("0123456789abcdef"), (2*s3PartSize+100)/16)
	// io.MultiReader hides the length, as an upload stream does.
	if err := store.Put(ctx, "big", io.MultiReader(bytes.NewReader(big)), "application/zip"); err != nil {
		t.Fatalf("Put big: %v", err)
	}
	if fake.multipartDone != 1 {
		t.Fatalf("multipart uploads = %d, want 1", fake.multipartDone)
	}
	if len(fake.partSizes) != 3 || fake.partSizes[0] != s3PartSize || fake.partSizes[1] != s3PartSize {
		t.Errorf("part sizes = %v, want two of %d and the rest", fake.partSizes, s3PartSize)
	}
	if got := readAll(t, store, "big"); !bytes.Equal(got, big) {
		t.Errorf("big object came back with %d bytes, want %d", len(got), len(big))
	}
	if ct := fake.objects("tenant")["big"].contentType; ct != "application/zip" {
		t.Errorf("multipart content type = %q", ct)
	}

	for _, h := range fake.requestHeaders {
		for name := range h {
			if strings.HasPrefix(strings.ToLower(name), "x-amz-checksum") || strings.EqualFold(name, "x-amz-trailer") {
				t.Errorf("upload sent %s: %q", name, h.Get(name))
			}
		}
		if strings.Contains(h.Get("X-Amz-Content-Sha256"), "TRAILER") {
			t.Errorf("upload used a trailer payload: %q", h.Get("X-Amz-Content-Sha256"))
		}
	}
}

// TestS3_Ping is the readiness check: a present bucket passes, a missing
// one and refused credentials fail — after NewS3 succeeded, since neither
// stops the process.
func TestS3_Ping(t *testing.T) {
	ctx := context.Background()
	_, store := newTestS3(t, nil)
	if err := Ping(ctx, store); err != nil {
		t.Errorf("Ping = %v", err)
	}

	_, missing := newTestS3(t, func(o *S3Options) { o.Bucket = "other" })
	if err := missing.Ping(ctx); err == nil {
		t.Error("Ping on a missing bucket = nil")
	}
	// A HEAD's 404 cannot say which is missing: a missing bucket must not
	// read as an empty one.
	if ok, err := missing.Exists(ctx, "a"); ok || err == nil {
		t.Errorf("Exists in a missing bucket = %v, %v; want an error", ok, err)
	}
	if _, ok, err := missing.Stat(ctx, "a"); ok || err == nil {
		t.Errorf("Stat in a missing bucket = %v, %v; want an error", ok, err)
	}
	if _, err := missing.Get(ctx, "a"); err == nil || errors.Is(err, ErrNotExist) {
		t.Errorf("Get in a missing bucket = %v; want an error that is not ErrNotExist", err)
	}

	_, refused := newTestS3(t, func(o *S3Options) { o.AccessKeyID = "wrong-key" })
	if err := refused.Ping(ctx); err == nil {
		t.Error("Ping with refused credentials = nil")
	}
	if err := refused.Put(ctx, "a", strings.NewReader("x"), "text/plain"); err == nil {
		t.Error("Put with refused credentials = nil")
	}
}

func TestS3_RejectsInvalidKeysAndOptions(t *testing.T) {
	ctx := context.Background()
	fake, store := newTestS3(t, nil)
	for _, key := range []string{"", "../a", "/a", `a\b`, "a//b", "a%2fb"} {
		if err := store.Put(ctx, key, strings.NewReader("x"), "text/plain"); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Put(%q) = %v, want ErrInvalidKey", key, err)
		}
		if _, err := store.Get(ctx, key); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Get(%q) = %v, want ErrInvalidKey", key, err)
		}
	}
	if len(fake.requestHeaders) != 0 {
		t.Errorf("an invalid key reached the service")
	}
	if err := store.Put(ctx, "a", strings.NewReader("x"), ""); err == nil {
		t.Error("Put without a content type = nil")
	}

	_, opts := newFakeS3(t, "tenant")
	for name, mutate := range map[string]func(*S3Options){
		"endpoint": func(o *S3Options) { o.Endpoint = "" },
		"bucket":   func(o *S3Options) { o.Bucket = "" },
		"secret":   func(o *S3Options) { o.SecretAccessKey = "" },
		"prefix":   func(o *S3Options) { o.Prefix = "../up" },
	} {
		o := opts
		mutate(&o)
		if _, err := NewS3(o); err == nil {
			t.Errorf("NewS3 without a valid %s = nil error", name)
		}
	}
}

// TestCopy_FSToS3AndBack copies an fs root to the bucket, skips the fs
// driver's leftovers, copies nothing on a second run, then copies a changed
// object again, and copies the bucket back to an empty fs root.
func TestCopy_FSToS3AndBack(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fsStore, err := NewFS(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for key, body := range map[string]string{
		"invoices/2026/a.pdf": "invoice a",
		"communications/b":    "attachment b",
		"top.png":             "png",
	} {
		if err := fsStore.Put(ctx, key, strings.NewReader(body), "application/octet-stream"); err != nil {
			t.Fatal(err)
		}
	}
	// A write that never finished, as Put leaves it.
	if err := os.WriteFile(filepath.Join(root, "invoices", ".c.pdf.0123abcd.tmp"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	fake, s3Store := newTestS3(t, nil)
	res, err := Copy(ctx, fsStore, s3Store, false, nil)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if res.Copied != 3 || res.Skipped != 0 || res.Bytes != int64(len("invoice a")+len("attachment b")+len("png")) {
		t.Errorf("first copy = %+v", res)
	}
	objects := fake.objects("tenant")
	if len(objects) != 3 || string(objects["invoices/2026/a.pdf"].body) != "invoice a" {
		t.Errorf("bucket = %v", objects)
	}
	if objects["top.png"].contentType != "image/png" || objects["communications/b"].contentType != "application/octet-stream" {
		t.Errorf("content types = %q, %q", objects["top.png"].contentType, objects["communications/b"].contentType)
	}

	res, err = Copy(ctx, fsStore, s3Store, false, nil)
	if err != nil || res.Copied != 0 || res.Skipped != 3 {
		t.Errorf("second copy = %+v, %v; want everything skipped", res, err)
	}

	if err := fsStore.Put(ctx, "top.png", strings.NewReader("a larger png"), "image/png"); err != nil {
		t.Fatal(err)
	}
	var copied []string
	res, err = Copy(ctx, fsStore, s3Store, false, func(o ObjectInfo) { copied = append(copied, o.Key) })
	if err != nil || res.Copied != 1 || len(copied) != 1 || copied[0] != "top.png" {
		t.Errorf("copy after a change = %+v, %v, %v", res, copied, err)
	}

	// Rewritten in place at the same size: skipped, unless all.
	if err := fsStore.Put(ctx, "top.png", strings.NewReader("a larger PNG"), "image/png"); err != nil {
		t.Fatal(err)
	}
	if res, err := Copy(ctx, fsStore, s3Store, false, nil); err != nil || res.Copied != 0 {
		t.Errorf("same-size copy = %+v, %v; want it skipped", res, err)
	}
	if res, err := Copy(ctx, fsStore, s3Store, true, nil); err != nil || res.Copied != 3 || string(fake.objects("tenant")["top.png"].body) != "a larger PNG" {
		t.Errorf("copy --all = %+v, %v", res, err)
	}

	back, err := NewFS(t.TempDir(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	res, err = Copy(ctx, s3Store, back, false, nil)
	if err != nil || res.Copied != 3 {
		t.Fatalf("copy back = %+v, %v", res, err)
	}
	if got := readAll(t, back, "invoices/2026/a.pdf"); string(got) != "invoice a" {
		t.Errorf("copied back = %q", got)
	}
}

func TestFS_PingAndStat(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "objects")
	store, err := NewFS(root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := Ping(ctx, store); err != nil {
		t.Errorf("Ping = %v", err)
	}
	if err := store.Put(ctx, "a/b", strings.NewReader("abc"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if info, ok, err := store.Stat(ctx, "a/b"); err != nil || !ok || info.Size != 3 {
		t.Errorf("Stat = %+v, %v, %v", info, ok, err)
	}
	if _, ok, err := store.Stat(ctx, "a"); err != nil || ok {
		t.Errorf("Stat of a directory = %v, %v; want not an object", ok, err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := store.Ping(ctx); err == nil {
		t.Error("Ping after the root is gone = nil")
	}

	if err := Ping(ctx, unconfiguredStore{}); err != nil {
		t.Errorf("Ping on an unconfigured store = %v, want no check", err)
	}
}
