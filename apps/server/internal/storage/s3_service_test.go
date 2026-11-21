package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// TestS3_RealService runs the driver against a real S3-compatible service
// when VANTIGO_TEST_S3_ENDPOINT names one (an http:// endpoint is fine;
// the dev compose's s3 profile serves one), with VANTIGO_TEST_S3_BUCKET,
// _ACCESS_KEY_ID and _SECRET_ACCESS_KEY. CI has none, so it skips there;
// the in-process fake covers the same calls.
func TestS3_RealService(t *testing.T) {
	endpoint := os.Getenv("VANTIGO_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("VANTIGO_TEST_S3_ENDPOINT is not set")
	}
	secure := strings.HasPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	region := os.Getenv("VANTIGO_TEST_S3_REGION")
	if region == "" {
		region = "us-east-1"
	}
	store, err := NewS3(S3Options{
		Endpoint: endpoint, Secure: secure, Region: region, Bucket: os.Getenv("VANTIGO_TEST_S3_BUCKET"),
		Prefix:      "test-" + time.Now().UTC().Format("20060102t150405"),
		AccessKeyID: os.Getenv("VANTIGO_TEST_S3_ACCESS_KEY_ID"), SecretAccessKey: os.Getenv("VANTIGO_TEST_S3_SECRET_ACCESS_KEY"),
		PathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	big := bytes.Repeat([]byte("0123456789abcdef"), (2*s3PartSize+12345)/16)
	if err := store.Put(ctx, "files/big.bin", io.MultiReader(bytes.NewReader(big)), "application/octet-stream"); err != nil {
		t.Fatalf("Put big: %v", err)
	}
	if err := store.Put(ctx, "files/small.txt", strings.NewReader("small"), "text/plain"); err != nil {
		t.Fatalf("Put small: %v", err)
	}
	if got := readAll(t, store, "files/big.bin"); !bytes.Equal(got, big) {
		t.Errorf("big came back with %d bytes, want %d", len(got), len(big))
	}
	if info, ok, err := store.Stat(ctx, "files/small.txt"); err != nil || !ok || info.Size != 5 {
		t.Errorf("Stat = %+v, %v, %v", info, ok, err)
	}
	var n int
	if err := store.Walk(ctx, func(ObjectInfo) error { n++; return nil }); err != nil || n != 2 {
		t.Errorf("Walk = %d objects, %v", n, err)
	}
	if _, err := store.Get(ctx, "files/missing"); !errors.Is(err, ErrNotExist) {
		t.Errorf("Get missing = %v", err)
	}
	if ok, err := store.Exists(ctx, "files/missing"); ok || err != nil {
		t.Errorf("Exists missing = %v, %v", ok, err)
	}
	for _, key := range []string{"files/big.bin", "files/small.txt", "files/missing"} {
		if err := store.Delete(ctx, key); err != nil {
			t.Errorf("Delete(%q) = %v", key, err)
		}
	}
	missing, _ := NewS3(S3Options{Endpoint: endpoint, Secure: secure, Region: region, Bucket: "no-such-bucket-vantigo", AccessKeyID: "x", SecretAccessKey: "y", PathStyle: true})
	if err := missing.Ping(ctx); err == nil {
		t.Error("Ping on a missing bucket with wrong credentials = nil")
	}
}
