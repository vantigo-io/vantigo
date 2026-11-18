// Package apiref serves the interactive API reference at /api (API access
// design K26, K27): a static page from apps/apiref/frontend, compiled in, that
// loads /api/openapi.json with the key typed into it, or with the session,
// and renders it with Scalar. It names nothing about the installation.
package apiref

import (
	"embed"
	"io/fs"
)

// distFS is the page's build. dist/index.html is a committed placeholder so
// the package builds and tests without a frontend build.
// scripts/spa-embed-overlay.sh replaces the directory with the Vite output of
// apps/apiref/frontend before release builds; scripts/restore-embed-overlay.sh
// puts the placeholder back.
//
//go:embed all:dist
var distFS embed.FS

// Assets is the embedded build rooted at its dist directory: index.html, and
// the scripts and styles under api/reference/.
func Assets() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("apiref: the embedded dist directory is missing: " + err.Error())
	}
	return sub
}
