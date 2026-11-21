package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

func storageTestConfig(t *testing.T, pairs ...string) *config.Config {
	t.Helper()
	env := map[string]string{
		"DATABASE_URL": "postgres://vantigo@localhost:5432/vantigo",
		"APP_URL":      "http://localhost:8080",
		"APP_SECRET":   testAppSecret,
		"SMTP_HOST":    "smtp.example.invalid",
		"SMTP_FROM":    "noreply@example.invalid",
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		env[pairs[i]] = pairs[i+1]
	}
	cfg, err := config.Load(env)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestStorageReadiness adds no check when storage is unset and a "storage"
// check that passes on a healthy fs root (KR14).
func TestStorageReadiness(t *testing.T) {
	checks, err := storageReadiness(storageTestConfig(t))
	if err != nil || len(checks) != 0 {
		t.Errorf("unset storage: %d checks, %v; want none", len(checks), err)
	}

	root := filepath.Join(t.TempDir(), "objects")
	checks, err = storageReadiness(storageTestConfig(t, "STORAGE_PROVIDER", "fs", "STORAGE_FS_ROOT", root))
	if err != nil || len(checks) != 1 || checks[0].Name != "storage" {
		t.Fatalf("fs storage: %+v, %v", checks, err)
	}
	if err := checks[0].Run(context.Background()); err != nil {
		t.Errorf("fs check = %v", err)
	}

	// The s3 check is built without contacting the bucket: an unreachable
	// one fails readiness, not startup.
	checks, err = storageReadiness(storageTestConfig(t,
		"STORAGE_PROVIDER", "s3", "STORAGE_S3_ENDPOINT", "https://127.0.0.1:1", "STORAGE_S3_REGION", "europe-1",
		"STORAGE_S3_BUCKET", "acme", "STORAGE_S3_ACCESS_KEY_ID", "id", "STORAGE_S3_SECRET_ACCESS_KEY", "secret"))
	if err != nil || len(checks) != 1 {
		t.Fatalf("s3 storage: %+v, %v", checks, err)
	}
	if err := checks[0].Run(context.Background()); err == nil {
		t.Error("s3 check against an unreachable endpoint = nil")
	}
}

func TestStorageCopy_Arguments(t *testing.T) {
	s3 := storageTestConfig(t,
		"STORAGE_PROVIDER", "s3", "STORAGE_S3_ENDPOINT", "https://127.0.0.1:1", "STORAGE_S3_REGION", "europe-1",
		"STORAGE_S3_BUCKET", "acme", "STORAGE_S3_ACCESS_KEY_ID", "id", "STORAGE_S3_SECRET_ACCESS_KEY", "secret")
	for _, args := range [][]string{
		nil,
		{"--fs-root", "relative"},
		{"--fs-root", "/data", "--to", "gcs"},
		{"--fs-root", "/data", "extra"},
		{"--unknown"},
	} {
		var stdout, stderr bytes.Buffer
		if code := storageCopy(context.Background(), s3, args, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), storageCopyUsage) {
			t.Errorf("storage copy %v = %d, %q; want 2 and the usage", args, code, stderr.String())
		}
	}

	var stdout, stderr bytes.Buffer
	if code := storageCopy(context.Background(), storageTestConfig(t), []string{"--fs-root", t.TempDir()}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "STORAGE_PROVIDER=s3") {
		t.Errorf("storage copy without s3 = %d, %q", code, stderr.String())
	}

	// An unreachable bucket is reported before anything is copied.
	stdout.Reset()
	stderr.Reset()
	if code := storageCopy(context.Background(), s3, []string{"--fs-root", t.TempDir()}, &stdout, &stderr); code != 1 || stdout.Len() != 0 {
		t.Errorf("storage copy to an unreachable bucket = %d, stdout %q", code, stdout.String())
	}
}
