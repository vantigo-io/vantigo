// Package web serves the SPA: the Vite build compiled into the binary, its
// index document templated once at startup, and the fallback that hands every
// client-side route to that document.
package web

import (
	"embed"
	"io/fs"
)

// distFS is the frontend build. dist/index.html is a committed placeholder so
// the package builds and tests without a frontend build.
// scripts/spa-embed-overlay.sh replaces the directory with the Vite output
// before release builds; scripts/restore-embed-overlay.sh puts the placeholder
// back.
//
//go:embed all:dist
var distFS embed.FS

// Assets is the embedded frontend build rooted at its dist directory.
func Assets() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("web: the embedded dist directory is missing: " + err.Error())
	}
	return sub
}
