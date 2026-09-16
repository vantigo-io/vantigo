package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

func testHandler(t *testing.T) (http.Handler, *Index) {
	t.Helper()
	assets := fstest.MapFS{
		"index.html":             {Data: []byte(builtIndexHTML)},
		"favicon.png":            {Data: []byte("png")},
		"assets/index-abc123.js": {Data: []byte("console.log(1)")},
	}
	idx, err := NewIndex(assets, "", config.Branding{}, nil)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	return Handler(assets, idx), idx
}

func get(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHandler_ServesTheTemplatedIndexForClientRoutes(t *testing.T) {
	h, idx := testHandler(t)
	for _, path := range []string{"/", "/customers/123", "/settings/roles", "/index.html"} {
		rec := get(h, http.MethodGet, path)
		if rec.Code != http.StatusOK || rec.Body.String() != string(idx.HTML) {
			t.Errorf("%s: status %d, templated index served = %v", path, rec.Code, rec.Body.String() == string(idx.HTML))
		}
		if rec.Header().Get("Content-Type") != "text/html; charset=utf-8" || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: Content-Type %q, Cache-Control %q", path, rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"))
		}
	}
}

func TestHandler_ServesHashedAssetsAsImmutable(t *testing.T) {
	h, _ := testHandler(t)
	rec := get(h, http.MethodGet, "/assets/index-abc123.js")

	if rec.Code != http.StatusOK || rec.Body.String() != "console.log(1)" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Errorf("Content-Type = %q", rec.Header().Get("Content-Type"))
	}
}

func TestHandler_ServesOtherFilesWithoutImmutableCaching(t *testing.T) {
	h, _ := testHandler(t)
	rec := get(h, http.MethodGet, "/favicon.png")
	if rec.Code != http.StatusOK || rec.Body.String() != "png" || rec.Header().Get("Cache-Control") != "" {
		t.Errorf("status %d body %q Cache-Control %q", rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
	}
}

func TestHandler_UnknownHashedAssetIs404NotTheIndex(t *testing.T) {
	h, _ := testHandler(t)
	if rec := get(h, http.MethodGet, "/assets/index-stale.js"); rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Errorf("status %d, want 404; Content-Type %q, want application/problem+json", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestHandler_HEADHasNoBody(t *testing.T) {
	h, _ := testHandler(t)
	rec := get(h, http.MethodHead, "/customers")
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("status %d body length %d", rec.Code, rec.Body.Len())
	}
}

func TestHandler_RejectsOtherMethods(t *testing.T) {
	h, _ := testHandler(t)
	rec := get(h, http.MethodPost, "/customers")
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("status %d Allow %q", rec.Code, rec.Header().Get("Allow"))
	}
}
