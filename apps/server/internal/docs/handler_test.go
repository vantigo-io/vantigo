package docs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const page = `<!doctype html><html><head><link rel="stylesheet" href="/docs/_astro/a.css"><meta http-equiv="refresh" content="0; url=/docs/en/"></head><body><a href="/docs/en/user/">Guide</a><script>import("/docs/pagefind/pagefind.js")</script></body></html>`

func site() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte(page)},
		"404.html":             {Data: []byte(`<p>There is nothing at this address. <a href="/docs/en/">Start</a></p>`)},
		"en/index.html":        {Data: []byte("<h1>English</h1>")},
		"en/user/index.html":   {Data: []byte("<h1>User guide</h1>")},
		"_astro/a.css":         {Data: []byte(`body{background:url(/docs/_astro/bg.png)}`)},
		"_astro/page.js":       {Data: []byte(`const base="/docs/";fetch('/docs/pagefind/pagefind-entry.json')`)},
		"pagefind/pagefind.js": {Data: []byte("export const search = 1")},
		"favicon.png":          {Data: []byte("png")},
	}
}

func request(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestHandler_ServesPagesByDirectory(t *testing.T) {
	h := Handler(site(), "")
	for target, want := range map[string]string{
		"/docs/":         `href="/docs/en/user/"`,
		"/docs/en/":      "<h1>English</h1>",
		"/docs/en/user/": "<h1>User guide</h1>",
	} {
		rec := request(h, http.MethodGet, target)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: status %d body %q", target, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: Content-Type %q", target, ct)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s: Cache-Control %q", target, cc)
		}
	}
}

func TestHandler_RedirectsToTheCanonicalDirectory(t *testing.T) {
	h := Handler(site(), "")
	for target, want := range map[string]string{
		"/docs":               "/docs/",
		"/docs/en":            "/docs/en/",
		"/docs/en/index.html": "/docs/en/",
		"/docs/index.html":    "/docs/",
	} {
		rec := request(h, http.MethodGet, target)
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != want {
			t.Errorf("%s: status %d Location %q, want 301 %q", target, rec.Code, rec.Header().Get("Location"), want)
		}
	}
}

func TestHandler_RedirectsUnderTheBasePath(t *testing.T) {
	h := Handler(site(), "/vantigo")
	rec := request(h, http.MethodGet, "/docs/en")
	if rec.Header().Get("Location") != "/vantigo/docs/en/" {
		t.Errorf("Location = %q", rec.Header().Get("Location"))
	}
}

func TestHandler_ServesHashedAssetsAsImmutable(t *testing.T) {
	h := Handler(site(), "")
	rec := request(h, http.MethodGet, "/docs/_astro/page.js")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("status %d Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if rec := request(h, http.MethodGet, "/docs/favicon.png"); rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "" {
		t.Errorf("favicon: status %d Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

func TestHandler_AnswersTheSitesOwn404Page(t *testing.T) {
	h := Handler(site(), "")
	for _, target := range []string{"/docs/nope/", "/docs/en/missing/", "/docs/nope.png"} {
		rec := request(h, http.MethodGet, target)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "nothing at this address") {
			t.Errorf("%s: status %d body %q", target, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: Content-Type %q", target, ct)
		}
	}
	// HEAD carries the status without a body.
	if rec := request(h, http.MethodHead, "/docs/nope/"); rec.Code != http.StatusNotFound || rec.Body.Len() != 0 {
		t.Errorf("HEAD: status %d body %d bytes", rec.Code, rec.Body.Len())
	}
}

func TestHandler_FallsBackToAProblemWithoutA404Page(t *testing.T) {
	assets := site()
	delete(assets, "404.html")
	rec := request(Handler(assets, ""), http.MethodGet, "/docs/nope/")
	if rec.Code != http.StatusNotFound || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/problem+json") {
		t.Fatalf("status %d Content-Type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestHandler_RewritesBakedURLsUnderTheBasePath(t *testing.T) {
	h := Handler(site(), "/vantigo")
	cases := map[string][]string{
		"/docs/":               {`href="/vantigo/docs/_astro/a.css"`, `url=/vantigo/docs/en/`, `href="/vantigo/docs/en/user/"`, `import("/vantigo/docs/pagefind/pagefind.js")`},
		"/docs/_astro/a.css":   {`url(/vantigo/docs/_astro/bg.png)`},
		"/docs/_astro/page.js": {`const base="/vantigo/docs/"`, `fetch('/vantigo/docs/pagefind/pagefind-entry.json')`},
		"/docs/nope/":          {`href="/vantigo/docs/en/"`},
	}
	for target, wants := range cases {
		body := request(h, http.MethodGet, target).Body.String()
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Errorf("%s: body lacks %q: %s", target, want, body)
			}
		}
		if strings.Contains(body, `"/docs/`) || strings.Contains(body, `(/docs/`) {
			t.Errorf("%s: an unrewritten /docs/ URL remains: %s", target, body)
		}
	}
	// Binary and non-text files pass through untouched.
	if body := request(h, http.MethodGet, "/docs/favicon.png").Body.String(); body != "png" {
		t.Errorf("favicon body = %q", body)
	}
}

func TestHandler_ReplacesTheContentSecurityPolicy(t *testing.T) {
	h := Handler(site(), "")
	for _, header := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		rec := httptest.NewRecorder()
		rec.Header().Set(header, "default-src 'none'")
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs/en/", nil))
		if got := rec.Header().Get(header); got != ContentSecurityPolicy() {
			t.Errorf("%s = %q", header, got)
		}
		other := "Content-Security-Policy-Report-Only"
		if header == other {
			other = "Content-Security-Policy"
		}
		if rec.Header().Get(other) != "" {
			t.Errorf("%s set alongside %s", other, header)
		}
	}
	if got := request(h, http.MethodGet, "/docs/en/").Header().Get("Content-Security-Policy"); got != ContentSecurityPolicy() {
		t.Errorf("without a policy from the application: %q", got)
	}
	if p := ContentSecurityPolicy(); !strings.Contains(p, "'wasm-unsafe-eval'") || !strings.Contains(p, "frame-ancestors 'none'") {
		t.Errorf("policy = %q", p)
	}
}

func TestHandler_RefusesOtherMethodsAndForeignPaths(t *testing.T) {
	h := Handler(site(), "")
	if rec := request(h, http.MethodPost, "/docs/en/"); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST: status %d Allow %q", rec.Code, rec.Header().Get("Allow"))
	}
	if rec := request(h, http.MethodGet, "/documents/"); rec.Code != http.StatusNotFound {
		t.Errorf("/documents/: status %d", rec.Code)
	}
	// Path traversal cannot escape the site.
	if rec := request(h, http.MethodGet, "/docs/../index.html"); rec.Code != http.StatusNotFound && rec.Code != http.StatusMovedPermanently {
		t.Errorf("traversal: status %d", rec.Code)
	}
}

// The embedded build is the placeholder in a test build and the real site
// after scripts/spa-embed-overlay.sh; either way the root page is there.
func TestAssets_ServesTheEmbeddedBuild(t *testing.T) {
	rec := request(Handler(Assets(), ""), http.MethodGet, "/docs/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>Vantigo documentation</title>") {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
}
