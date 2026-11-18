package apiref

import (
	"io/fs"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// Path is the page, and AssetPrefix where its files are served. The build
// refers to the files relative to the page (./api/reference/…), so a base
// path needs no rewriting.
const (
	Path        = "/api"
	AssetPrefix = "/api/reference/"
)

// ContentSecurityPolicy is the page's own policy (K27), replacing the
// application's: Scalar's bundle and the page's script come from 'self', and
// Scalar injects its styles inline. The bundle runs without 'unsafe-eval'
// (its schema library tests for eval and falls back), and nothing it loads
// may come from anywhere but this origin.
func ContentSecurityPolicy() string {
	return strings.Join([]string{
		"default-src 'none'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"connect-src 'self'",
		"img-src 'self' data:",
		"font-src 'self'",
		"frame-ancestors 'none'",
	}, "; ")
}

// Serves reports whether r is the reference's to answer rather than the
// API's: a GET or HEAD of Path whose Accept prefers HTML, or anything under
// AssetPrefix. Every other request to Path keeps the API's 404 problem
// (K26), so an API client still gets the API's own answer. path is the
// request's escaped path, the base path already stripped.
func Serves(r *http.Request, path string) bool {
	if strings.HasPrefix(path, AssetPrefix) {
		return true
	}
	return path == Path && (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		prefersHTML(r.Header.Values("Accept"))
}

// Handler answers what Serves admits: the page at Path, a file under
// AssetPrefix (cached as immutable, as the build names each by its content),
// and the 404 problem for any other.
func Handler(assets fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			httpx.WriteProblem(w, r, http.StatusMethodNotAllowed, "")
			return
		}
		path := r.URL.EscapedPath()
		name := "index.html"
		if path != Path {
			file, ok := strings.CutPrefix(path, AssetPrefix)
			if !ok || file == "" || strings.ContainsAny(file, "/\\%") || strings.HasPrefix(file, ".") || !isFile(assets, "api/reference/"+file) {
				httpx.NotFound(w, r)
				return
			}
			name = "api/reference/" + file
		}
		setPolicy(w.Header())
		// The page names its files by their content (the build's hashes), so a
		// file never changes under its name; the page itself always may.
		// Vary: Accept is the caller's (internal/server), which sends it on
		// every answer to /api, the API's 404 included.
		if name == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeFileFS(w, r, assets, name)
	})
}

// setPolicy swaps the application's policy for the page's, under whichever
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

// prefersHTML reports whether an Accept header names text/html, with a
// quality above zero and strictly higher than any JSON type it names: a tie
// is an API client's, and gets the API's answer. A wildcard
// counts for neither, so curl's */* and a client asking for JSON both get
// the API's answer, and a browser's navigation gets the page.
func prefersHTML(accept []string) bool {
	html, jsonQ := -1.0, -1.0
	for _, header := range accept {
		for _, part := range strings.Split(header, ",") {
			mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(part))
			if err != nil {
				continue
			}
			q := 1.0
			if v, ok := params["q"]; ok {
				if parsed, err := strconv.ParseFloat(v, 64); err == nil {
					q = parsed
				}
			}
			switch {
			case mediaType == "text/html":
				html = max(html, q)
			case mediaType == "application/json" || mediaType == "application/problem+json" || strings.HasSuffix(mediaType, "+json"):
				jsonQ = max(jsonQ, q)
			}
		}
	}
	return html > 0 && html > jsonQ
}

func isFile(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}
