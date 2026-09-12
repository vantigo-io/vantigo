package module

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// Compose mounts each module at /api/v1/<name>/, serves the combined
// contract of the modules at GET /api/openapi.json (RuleSession), and
// answers every other /api path with httpx.NotFound. It fails on a
// duplicate name, an invalid or duplicate permission, a Mount error, a path
// two modules both declare, or a component two modules declare differently
// under the same name.
func Compose(deps Deps, mods ...Module) (http.Handler, error) {
	return compose(deps, openapi.Load, mods...)
}

// compose is Compose with the contract loader as a seam, so tests can
// compose in-memory contracts instead of the embedded specs.
func compose(deps Deps, load func(context.Context, string) (*openapi3.T, error), mods ...Module) (http.Handler, error) {
	ctx := context.Background()
	mods = enabledModules(deps, mods)

	names := make(map[string]bool, len(mods))
	catalog := make(map[string]contracts.Permission)
	for _, mod := range mods {
		if names[mod.Name] {
			return nil, fmt.Errorf("module: duplicate module name %q", mod.Name)
		}
		names[mod.Name] = true

		for _, perm := range mod.Permissions {
			if err := contracts.ValidatePermission(mod.Name, perm); err != nil {
				return nil, fmt.Errorf("module: %w", err)
			}
			if _, ok := catalog[perm.Key]; ok {
				return nil, fmt.Errorf("module: duplicate permission %q", perm.Key)
			}
			catalog[perm.Key] = perm
		}
	}

	outer := http.NewServeMux()
	docs := make(map[string]*openapi3.T, len(mods))
	order := make([]string, 0, len(mods))

	for _, mod := range mods {
		doc, err := load(ctx, mod.Name)
		if err != nil {
			return nil, fmt.Errorf("module: load %s contract: %w", mod.Name, err)
		}

		modDeps := deps
		modDeps.Doc = doc
		modDeps.Catalog = catalog
		handler, err := mod.Mount(modDeps)
		if err != nil {
			return nil, fmt.Errorf("module: mount %s: %w", mod.Name, err)
		}
		// A pattern ending in "/" matches the whole subtree and hands the
		// module's router the request path unchanged (no StripPrefix): the
		// module's own router matches full paths from its own Doc.
		outer.Handle("/api/v1/"+mod.Name+"/", handler)

		// mergeContract (via InternalizeRefs) mutates the *openapi3.T it
		// merges in place. doc was just handed to Mount as modDeps.Doc and a
		// module may keep it, so mergeContract gets its own independent copy
		// — a second, separate call to load — rather than doc itself, which
		// stays exactly as Mount received it.
		mergeDoc, err := load(ctx, mod.Name)
		if err != nil {
			return nil, fmt.Errorf("module: load %s contract: %w", mod.Name, err)
		}
		docs[mod.Name] = mergeDoc
		order = append(order, mod.Name)
	}

	contract, err := mergeContract(docs, order)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(contract)
	if err != nil {
		return nil, fmt.Errorf("module: marshal the combined contract: %w", err)
	}

	sessionRule := contracts.Rule{Kind: contracts.RuleSession}
	outer.HandleFunc("GET /api/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		if _, err := deps.Access.Check(r, sessionRule); err != nil {
			if errors.Is(err, contracts.ErrUnauthenticated) || errors.Is(err, contracts.ErrForbidden) {
				deps.Access.Reject(w, r, sessionRule, err)
				return
			}
			httpx.WriteError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})

	outer.HandleFunc("/api/", httpx.NotFound)

	return outer, nil
}

// enabledModules keeps identity, always mounted, and any module deps.Config
// enables, dropping the rest before they contribute a route, a
// permission-catalog entry or a contract path: their paths fall through to
// the /api 404 problem. deps.Config is nil only in tests that do not
// exercise enablement; a nil Config enables everything it is given.
func enabledModules(deps Deps, mods []Module) []Module {
	if deps.Config == nil {
		return mods
	}
	out := make([]Module, 0, len(mods))
	for _, mod := range mods {
		if mod.Name == "identity" || slices.Contains(deps.Config.Modules, mod.Name) {
			out = append(out, mod)
		}
	}
	return out
}

// mergeContract builds the contract served at GET /api/openapi.json: every
// module's paths (a path two modules both declare fails, naming it and the
// second module), and their components merged (an identical component under
// the same name is kept once; a differing one fails, naming it), with
// common.yaml references internalised so none remain in the output. It
// mutates docs' entries in place (InternalizeRefs), so callers must pass it
// copies no one else holds onto.
func mergeContract(docs map[string]*openapi3.T, order []string) (*openapi3.T, error) {
	combined := &openapi3.T{
		OpenAPI: "3.0.3",
		Info:    &openapi3.Info{Title: "Vantigo API", Version: "1"},
		Servers: openapi3.Servers{{URL: "/"}},
		Paths:   openapi3.NewPaths(),
		Components: &openapi3.Components{
			Schemas:         openapi3.Schemas{},
			Parameters:      openapi3.ParametersMap{},
			Headers:         openapi3.Headers{},
			RequestBodies:   openapi3.RequestBodies{},
			Responses:       openapi3.ResponseBodies{},
			SecuritySchemes: openapi3.SecuritySchemes{},
			Examples:        openapi3.Examples{},
			Links:           openapi3.Links{},
			Callbacks:       openapi3.Callbacks{},
		},
	}

	for _, name := range order {
		doc := docs[name]
		for path, item := range doc.Paths.Map() {
			if combined.Paths.Find(path) != nil {
				return nil, fmt.Errorf("module: path %q from %s is already registered by an earlier module", path, name)
			}
			combined.Paths.Set(path, item)
		}
		if doc.Components == nil {
			continue
		}
		c := doc.Components
		if err := mergeComponent(combined.Components.Schemas, c.Schemas, "schema", name); err != nil {
			return nil, err
		}
		if err := mergeComponent(combined.Components.Parameters, c.Parameters, "parameter", name); err != nil {
			return nil, err
		}
		if err := mergeComponent(combined.Components.Headers, c.Headers, "header", name); err != nil {
			return nil, err
		}
		if err := mergeComponent(combined.Components.RequestBodies, c.RequestBodies, "requestBody", name); err != nil {
			return nil, err
		}
		if err := mergeComponent(combined.Components.Responses, c.Responses, "response", name); err != nil {
			return nil, err
		}
		if err := mergeComponent(combined.Components.SecuritySchemes, c.SecuritySchemes, "securityScheme", name); err != nil {
			return nil, err
		}
		if err := mergeComponent(combined.Components.Examples, c.Examples, "example", name); err != nil {
			return nil, err
		}
		if err := mergeComponent(combined.Components.Links, c.Links, "link", name); err != nil {
			return nil, err
		}
		if err := mergeComponent(combined.Components.Callbacks, c.Callbacks, "callback", name); err != nil {
			return nil, err
		}
	}

	combined.InternalizeRefs(context.Background(), nil)
	return combined, nil
}

// mergeComponent adds src's entries to dst (dst's map, named after kind, of
// the module named module). An entry whose name already exists in dst must
// be identical (compared as JSON, so unexported position metadata never
// causes a false conflict) or mergeContract fails, naming the collision.
func mergeComponent[V any](dst, src map[string]V, kind, module string) error {
	for name, value := range src {
		existing, ok := dst[name]
		if !ok {
			dst[name] = value
			continue
		}
		same, err := jsonEqual(existing, value)
		if err != nil {
			return fmt.Errorf("module: compare %s %q from %s: %w", kind, name, module, err)
		}
		if !same {
			return fmt.Errorf("module: %s %q from %s differs from an earlier module's", kind, name, module)
		}
	}
	return nil
}

func jsonEqual(a, b any) (bool, error) {
	ja, err := json.Marshal(a)
	if err != nil {
		return false, err
	}
	jb, err := json.Marshal(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ja, jb), nil
}

// DecodeError returns a RequestErrorHandlerFunc/ErrorHandlerFunc that writes
// body with status 400 (never err.Error()). write knows the failing
// operation's error body shape (AuthErrorResponse, the flat CodeMessageError,
// or a SCIM error body); DecodeError only discards the error oapi-codegen
// would otherwise echo.
func DecodeError(write func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, _ error) {
		write(w, r)
	}
}

// ResponseError returns a ResponseErrorHandlerFunc that sends err through
// httpx.WriteError, except ErrNotImplemented, which answers 501.
func ResponseError() func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		if errors.Is(err, ErrNotImplemented) {
			httpx.WriteProblem(w, r, http.StatusNotImplemented, "")
			return
		}
		httpx.WriteError(w, r, err)
	}
}
