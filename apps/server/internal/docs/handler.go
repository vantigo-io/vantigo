package docs

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// Prefix is the path the site is served under. The build bakes it into every
// link and asset URL (DOCS_BASE in scripts/spa-embed-overlay.sh), so the two
// must agree.
const Prefix = "/docs"

// ContentSecurityPolicy is the site's own policy, replacing the application's
// on every documentation response. Starlight renders its theme and
// navigation scripts inline and its search runs Pagefind's WebAssembly, which
// the application's hash-only script source does not allow. The pages are a
// build artefact that carries no user-provided content, so inline scripts
// here are the site's own.
func ContentSecurityPolicy() string {
	return strings.Join([]string{
		"default-src 'self'",
		"base-uri 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"form-action 'self'",
		"script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"worker-src 'self'",
	}, "; ")
}

// Handler serves the site for every path under Prefix (the application's base
// path already stripped). A directory answers its index.html; a path to a
// directory without the trailing slash redirects to the canonical one, as the
// relative URLs inside the pages expect; an unknown path answers the site's
// own 404 page with status 404. Astro's content-hashed files under _astro/
// are cached as immutable.
//
// basePath is the application's base path. The build knows only Prefix, so
// when the application runs under a base path the baked "/docs/" URLs in
// HTML, JavaScript and CSS are rewritten beneath it on the way out.
func Handler(assets fs.FS, basePath string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			httpx.WriteProblem(w, r, http.StatusMethodNotAllowed, "")
			return
		}
		setPolicy(w.Header())

		clean := path.Clean("/" + r.URL.Path)
		if clean != Prefix && !strings.HasPrefix(clean, Prefix+"/") {
			httpx.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(strings.TrimPrefix(clean, Prefix), "/")
		trailingSlash := strings.HasSuffix(r.URL.Path, "/")

		switch {
		case name == "":
			if !trailingSlash {
				http.Redirect(w, r, basePath+Prefix+"/", http.StatusMovedPermanently)
				return
			}
			name = "index.html"
		case isFile(assets, name):
			if strings.HasSuffix(name, "/index.html") || name == "index.html" {
				// The canonical address of a page is its directory.
				http.Redirect(w, r, basePath+Prefix+"/"+strings.TrimSuffix(name, "index.html"), http.StatusMovedPermanently)
				return
			}
		case isDir(assets, name):
			if !trailingSlash {
				http.Redirect(w, r, basePath+Prefix+"/"+name+"/", http.StatusMovedPermanently)
				return
			}
			name += "/index.html"
			if !isFile(assets, name) {
				notFound(w, r, assets, basePath)
				return
			}
		default:
			notFound(w, r, assets, basePath)
			return
		}

		if strings.HasPrefix(name, "_astro/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else if strings.HasSuffix(name, ".html") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		serve(w, r, assets, name, basePath, http.StatusOK)
	})
}

// setPolicy swaps the application's policy for the site's, under whichever
// header the application chose; a response with neither gets the enforcing one.
func setPolicy(h http.Header) {
	for _, key := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		if h.Get(key) != "" {
			h.Set(key, ContentSecurityPolicy())
			return
		}
	}
	h.Set("Content-Security-Policy", ContentSecurityPolicy())
}

func notFound(w http.ResponseWriter, r *http.Request, assets fs.FS, basePath string) {
	if !isFile(assets, "404.html") {
		httpx.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	serve(w, r, assets, "404.html", basePath, http.StatusNotFound)
}

// rewritten reports whether a file's text carries baked URLs to rewrite under
// a base path: the pages, the scripts that build URLs from the base, and the
// stylesheets' url() references.
func rewritten(name string) bool {
	return strings.HasSuffix(name, ".html") || strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".css")
}

func serve(w http.ResponseWriter, r *http.Request, assets fs.FS, name, basePath string, status int) {
	if basePath == "" && status == http.StatusOK {
		http.ServeFileFS(w, r, assets, name)
		return
	}
	body, err := fs.ReadFile(assets, name)
	if err != nil {
		httpx.NotFound(w, r)
		return
	}
	if basePath != "" && rewritten(name) {
		for _, lead := range []string{`"`, `'`, `(`, `=`} {
			body = bytes.ReplaceAll(body, []byte(lead+Prefix+"/"), []byte(lead+basePath+Prefix+"/"))
		}
	}
	if status != http.StatusOK {
		// ServeContent would answer 200; the 404 page must say 404.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
		return
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
}

func isFile(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

func isDir(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && info.IsDir()
}
