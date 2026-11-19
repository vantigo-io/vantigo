package identity_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/identity"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// A same-origin APP_LOGO_URL path is read from the embedded files in process
// (B30.2): no connection is made, and the base path is not added twice.
func TestBrandImport_ASameOriginPathIsReadFromTheEmbeddedFiles(t *testing.T) {
	t.Parallel()
	logo := pngOf(t, 120, 40)
	for name, tc := range map[string]struct{ url, basePath string }{
		"at the root":               {"/logo.png", ""},
		"under a base path":         {"/logo.png", "/vantigo"},
		"already carrying the base": {"/vantigo/logo.png", "/vantigo"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, dials := logoImportHarness(t, tc.url, mapResolver{}, "127.0.0.1:9")
			h.access.UseBrandAssets(fstest.MapFS{"logo.png": {Data: logo}})
			owner, _ := h.bootstrapOwner(t)
			h.cfg.BasePath = tc.basePath // after the routes were mounted at the root
			if done, err := identity.RunBrandImportOnce(context.Background(), h.access, h.deps); err != nil || !done {
				t.Fatalf("done=%v err=%v", done, err)
			}
			b := getBrand(t, owner)
			if b.Published.Logos["mark_light"].Sha256 == "" || b.ImportNotice != nil {
				t.Errorf("brand = %+v notice = %v, want the logo imported", b.Published, b.ImportNotice)
			}
			if dials.Load() != 0 {
				t.Errorf("%d connections were made for a same-origin path", dials.Load())
			}
			if imported, _ := identity.BrandingImported(context.Background(), h.deps); !imported {
				t.Error("the import is not complete")
			}
		})
	}
}

func TestBrandImport_ASameOriginPathThatIsNotAFileFailsWithAReason(t *testing.T) {
	t.Parallel()
	for _, url := range []string{"/missing.png", "/", "/../etc/passwd", "/dir"} {
		t.Run(url, func(t *testing.T) {
			t.Parallel()
			h, dials := logoImportHarness(t, url, mapResolver{}, "127.0.0.1:9")
			h.access.UseBrandAssets(fstest.MapFS{"dir/a.png": {Data: []byte("x")}})
			owner, _ := h.bootstrapOwner(t)
			if done, err := identity.RunBrandImportOnce(context.Background(), h.access, h.deps); err != nil || !done {
				t.Fatalf("done=%v err=%v", done, err)
			}
			b := getBrand(t, owner)
			if b.ImportNotice == nil || !strings.Contains(*b.ImportNotice, "own files") || len(b.Published.Logos) != 0 || dials.Load() != 0 {
				t.Errorf("notice = %v logos = %v dials = %d", b.ImportNotice, b.Published.Logos, dials.Load())
			}
		})
	}
}

// A failed import leaves the environment honoured (B30): it is not complete,
// the notice stands, and an admin who publishes a logo of their own settles it.
func TestBrandImport_AFailedLogoLeavesTheEnvironmentHonouredUntilALogoIsPublished(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<svg xmlns='http://www.w3.org/2000/svg'/>"))
	}))
	t.Cleanup(srv.Close)
	h, _ := logoImportHarness(t, "http://logo.example.com/logo.svg", mapResolver{"logo.example.com": {"93.184.215.14"}}, srv.Listener.Addr().String())
	owner, _ := h.bootstrapOwner(t)
	if done, err := identity.RunBrandImportOnce(context.Background(), h.access, h.deps); err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if imported, _ := identity.BrandingImported(context.Background(), h.deps); imported {
		t.Fatal("a failed logo import is complete")
	}
	b := getBrand(t, owner)
	if b.ImportNotice == nil || !strings.Contains(*b.ImportNotice, "not a PNG or JPEG") {
		t.Fatalf("notice = %v", b.ImportNotice)
	}

	// The admin uploads a logo and publishes.
	var withLogo brandBody
	putLogo(t, owner, b, "mark_light", pngOf(t, 64, 32)).json(&withLogo)
	if pr := publish(owner, withLogo); pr.status != http.StatusOK {
		t.Fatalf("publish: %d %s", pr.status, pr.body)
	}
	if imported, _ := identity.BrandingImported(context.Background(), h.deps); !imported {
		t.Error("publishing a logo did not settle the import")
	}
	if got := getBrand(t, owner); got.ImportNotice != nil {
		t.Errorf("the notice outlived the logo: %v", *got.ImportNotice)
	}
}

// putLogo takes the brand's row lock as every brand write does: an admin's
// write that holds it is not overtaken with a stale copy.
func TestBrandImport_RecordingTheLogoWaitsForTheBrandRowLock(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngOf(t, 120, 40))
	}))
	t.Cleanup(srv.Close)
	h, _ := logoImportHarness(t, "http://logo.example.com/logo.png", mapResolver{"logo.example.com": {"93.184.215.14"}}, srv.Listener.Addr().String())
	h.bootstrapOwner(t)

	tx, err := h.deps.Pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(context.Background(), `SELECT 1 FROM identity.brands WHERE is_default FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := identity.RunBrandImportOnce(context.Background(), h.access, h.deps)
		finished <- err
	}()
	select {
	case err := <-finished:
		t.Fatalf("the import recorded its logo past a held row lock (err = %v)", err)
	case <-time.After(500 * time.Millisecond):
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the import did not finish after the lock was released")
	}
}

// hangingStore is an object store whose Put never answers until its context
// ends.
type hangingStore struct{ storage.ObjectStore }

func (hangingStore) Put(ctx context.Context, _ string, _ io.Reader, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

// The whole import, the store's Put included, runs under one deadline.
func TestBrandImport_AStuckObjectStoreEndsInATimeoutNotice(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngOf(t, 120, 40))
	}))
	t.Cleanup(srv.Close)
	h, _ := logoImportHarness(t, "http://logo.example.com/logo.png", mapResolver{"logo.example.com": {"93.184.215.14"}}, srv.Listener.Addr().String())
	owner, _ := h.bootstrapOwner(t)
	start := time.Now()
	done, err := identity.RunBrandImportOnceWith(context.Background(), h.access, h.deps, hangingStore{}, 400*time.Millisecond)
	if err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the import took %v with a stuck store", took)
	}
	b := getBrand(t, owner)
	if b.ImportNotice == nil || !strings.Contains(*b.ImportNotice, "in time") {
		t.Errorf("notice = %v, want a timeout", b.ImportNotice)
	}
}
