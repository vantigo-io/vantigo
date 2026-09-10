// Package openapi owns the embedded copy of the API contract (openapi/ at the
// repository root) and loads it for validation. The contract is the single
// source of truth for the Go server's routes and types and for the frontend's
// types; see docs/superpowers/specs/2026-09-10-api-contract-design.md.
package openapi

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// Modules are the contract files that carry paths; common.yaml only holds
// shared components.
var Modules = []string{"identity", "customers", "products", "energy", "communications"}

//go:embed specs/*.yaml
var specs embed.FS

// Files is the embedded contract, rooted at the spec files.
func Files() fs.FS {
	sub, err := fs.Sub(specs, "specs")
	if err != nil {
		panic("openapi: embedded specs missing: " + err.Error())
	}
	return sub
}

// Load loads <name>.yaml with its references into common.yaml resolved from
// the embedded files.
func Load(ctx context.Context, name string) (*openapi3.T, error) {
	files := Files()
	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.IsExternalRefsAllowed = true
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, location *url.URL) ([]byte, error) {
		return fs.ReadFile(files, path.Clean(strings.TrimPrefix(location.Path, "/")))
	}
	data, err := fs.ReadFile(files, name+".yaml")
	if err != nil {
		return nil, fmt.Errorf("openapi: %w", err)
	}
	doc, err := loader.LoadFromDataWithPath(data, &url.URL{Path: name + ".yaml"})
	if err != nil {
		return nil, fmt.Errorf("openapi: load %s: %w", name, err)
	}
	return doc, nil
}

// operation is one method on one path.
type operation struct {
	Path, Method, OperationID string
	Op                        *openapi3.Operation
}

func operations(doc *openapi3.T) []operation {
	var out []operation
	for p, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			out = append(out, operation{Path: p, Method: method, OperationID: op.OperationID, Op: op})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}
