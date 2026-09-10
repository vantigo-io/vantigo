package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// Handler serves real files from assets and the templated index for every
// other GET/HEAD, so the client-side router (TanStack Router) resolves deep
// links. /index.html is answered with the templated document, never the raw
// file. Vite's content-hashed bundles under assets/ are cached as immutable;
// a missing one is a 404, not HTML where the browser expects JavaScript.
func Handler(assets fs.FS, index *Index) http.Handler {
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			httpx.WriteProblem(w, r, http.StatusMethodNotAllowed, "")
			return
		}

		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" && name != "index.html" && isFile(assets, name) {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			httpx.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(index.HTML)
		}
	})
}

func isFile(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}
