package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/health"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// storageReadiness builds the object store once, for the readiness check
// named "storage" (Kubernetes readiness design KR14): a HeadBucket for s3,
// a stat of the root for fs, and no check at all when storage is unset. The
// modules still build their own stores; this one only answers the probe. A
// store that cannot be built is a startup error, as it is in every module
// that calls storage.New; a bucket that is missing or refuses the
// credentials is not — it fails readiness, and the process keeps running.
func storageReadiness(cfg *config.Config) ([]health.Check, error) {
	if cfg.StorageProvider == "" {
		return nil, nil
	}
	store, err := storage.New(cfg)
	if err != nil {
		return nil, err
	}
	return []health.Check{{Name: "storage", Run: func(ctx context.Context) error { return storage.Ping(ctx, store) }}}, nil
}

// storageCopyUsage is printed for a malformed `vantigo storage copy`.
const storageCopyUsage = "usage: vantigo storage copy --fs-root <absolute path> [--to s3|fs] [--all]"

// storageCopy is `vantigo storage copy` (KR15): it copies every object
// between the fs root named by --fs-root and the s3 bucket the environment
// configures (STORAGE_PROVIDER=s3), to s3 by default or to fs with
// --to fs. Objects already at the destination with the same size are
// skipped unless --all, so it is safe to run again; the fs driver's unfinished-write
// leftovers are never copied. The documented cutover runs it once while
// the installation serves, again once writes have stopped, and then
// switches STORAGE_PROVIDER.
func storageCopy(ctx context.Context, cfg *config.Config, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("storage copy", flag.ContinueOnError)
	flags.SetOutput(stderr)
	fsRoot := flags.String("fs-root", "", "the fs driver's root directory (STORAGE_FS_ROOT of the fs installation)")
	to := flags.String("to", "s3", `the destination: "s3" or "fs"`)
	all := flags.Bool("all", false, "copy every object, including those already there with the same size")
	if err := flags.Parse(args); err != nil || flags.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, storageCopyUsage)
		return 2
	}
	if *fsRoot == "" || !filepath.IsAbs(*fsRoot) || (*to != "s3" && *to != "fs") {
		_, _ = fmt.Fprintln(stderr, storageCopyUsage)
		return 2
	}
	if cfg.StorageProvider != "s3" {
		_, _ = fmt.Fprintln(stderr, `storage copy needs the bucket's settings: set STORAGE_PROVIDER=s3 and the STORAGE_S3_* variables`)
		return 2
	}

	fsStore, err := storage.NewFS(*fsRoot, cfg.IsDevelopment() && cfg.StorageFSAllowInsecureRoot, cfg.IsDevelopment())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	s3Store, err := storage.NewS3(storage.S3OptionsFrom(cfg))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if err := s3Store.Ping(ctx); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	var src, dst storage.Inventory = fsStore, s3Store
	if *to == "fs" {
		src, dst = s3Store, fsStore
	}
	_, _ = fmt.Fprintf(stdout, "copying objects to %s\n", *to)
	res, err := storage.Copy(ctx, src, dst, *all, nil)
	_, _ = fmt.Fprintf(stdout, "copied %d objects (%d bytes), skipped %d already there\n", res.Copied, res.Bytes, res.Skipped)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			_, _ = fmt.Fprintln(stderr, "interrupted; run the command again to finish")
		} else {
			_, _ = fmt.Fprintln(stderr, err)
		}
		return 1
	}
	return 0
}
