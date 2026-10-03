// Package docs serves the documentation site from inside the binary: the
// Starlight build under docs/ at the repository root, compiled in and served
// under /docs, so every installation carries the guide for exactly the
// version it runs, offline and without depending on the public site.
package docs

import (
	"embed"
	"io/fs"
)

// distFS is the documentation build. dist/index.html is a committed
// placeholder so the package builds and tests without a site build.
// scripts/spa-embed-overlay.sh replaces the directory with the Astro output
// (built with DOCS_BASE=/docs) before release builds;
// scripts/restore-embed-overlay.sh puts the placeholder back.
//
//go:embed all:dist
var distFS embed.FS

// Assets is the embedded documentation build rooted at its dist directory.
func Assets() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("docs: the embedded dist directory is missing: " + err.Error())
	}
	return sub
}
