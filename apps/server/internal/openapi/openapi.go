// Package openapi owns the embedded copy of the API contract (openapi/ at the
// repository root) and loads it for validation. The contract is the single
// source of truth for the Go server's routes and types and for the frontend's
// types; see docs/superpowers/specs/2026-09-10-api-contract-design.md.
package openapi

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/oasdiff/yaml"
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

// jsonSpecs is the embedded contract converted from YAML to JSON once per
// process. kin-openapi tries encoding/json before YAML (openapi3/marsh.go),
// and its YAML path is itself a YAML decode into a generic value followed by
// a re-marshal and that same JSON unmarshal — so handing the loader JSON
// skips two of the three steps and keeps the third. Profiled on this
// contract, that is a third of every Load: identity 62.5 ms → 43.5 ms,
// customers 18.4 → 12.0, communications 22.0 → 14.6.
//
// Only the parse is cached. Every Load still resolves references into a
// fresh *openapi3.T that its caller alone owns, because internal/module's
// compose merges one copy (mergeContract mutates it through InternalizeRefs)
// while the module that was mounted keeps the other. A cache that handed out
// one shared document would make those the same document; the loaded
// documents also carry unexported per-document state (T.visited, each Ref's
// refPath) that Validate and InternalizeRefs write, which a second holder
// would race on. TestLoadMatchesAnUncachedYAMLParse proves the cached parse
// yields the same document the YAML bytes do, and
// TestLoadReturnsIndependentDocuments proves two Loads share nothing.
var jsonSpecs = sync.OnceValues(convertSpecsToJSON)

func convertSpecsToJSON() (map[string][]byte, error) {
	files := Files()
	names, err := fs.Glob(files, "*.yaml")
	if err != nil {
		return nil, fmt.Errorf("openapi: list the embedded specs: %w", err)
	}
	out := make(map[string][]byte, len(names))
	for _, name := range names {
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("openapi: %w", err)
		}
		// The conversion kin-openapi's own YAML path performs, with the
		// DisableTimestamps it passes: without it a YAML 1.1 date scalar
		// (an example's "2026-09-12") would resolve to a timestamp and
		// re-marshal as an RFC 3339 string, changing the document.
		var generic any
		if _, err := yaml.Unmarshal(data, &generic, yaml.DecodeOpts{DisableTimestamps: true}); err != nil {
			return nil, fmt.Errorf("openapi: convert %s to JSON: %w", name, err)
		}
		encoded, err := json.Marshal(generic)
		if err != nil {
			return nil, fmt.Errorf("openapi: convert %s to JSON: %w", name, err)
		}
		out[name] = encoded
	}
	return out, nil
}

// Load loads <name>.yaml with its references into common.yaml resolved from
// the embedded files. Each call returns a new document the caller owns.
func Load(ctx context.Context, name string) (*openapi3.T, error) {
	files := Files()
	specs, err := jsonSpecs()
	if err != nil {
		return nil, err
	}
	// read serves the cached JSON, falling back to the embedded bytes so a
	// file the cache does not hold — a name no module has — still fails with
	// the fs error it failed with before the cache existed.
	read := func(file string) ([]byte, error) {
		if data, ok := specs[file]; ok {
			return data, nil
		}
		return fs.ReadFile(files, file)
	}

	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.IsExternalRefsAllowed = true
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, location *url.URL) ([]byte, error) {
		return read(path.Clean(strings.TrimPrefix(location.Path, "/")))
	}
	data, err := read(name + ".yaml")
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
