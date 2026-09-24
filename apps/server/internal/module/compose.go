package module

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// Compose mounts each module at /api/v1/<name>/, serves the combined
// contract of the modules at GET /api/openapi.json (RuleSession), and
// answers every other /api path with httpx.NotFound. It fails on a
// duplicate name, an invalid or duplicate permission, a Mount error, a path
// two modules both declare, a component two modules declare differently
// under the same name, two modules both declaring a customer directory, a
// user directory, a product catalog, a project directory, project actuals or
// project expenses (naming both) — customer reference holders and customer
// personal-data providers, the two many-provider contract slots, are collected
// from every module given instead, enabled or not — or a nil Deps.Config:
// enablement (which modules MODULES turns on) is meaningless without one, and
// every real caller already loads one before composing.
func Compose(deps Deps, mods ...Module) (http.Handler, error) {
	if deps.Config == nil {
		return nil, fmt.Errorf("module: Compose requires a non-nil Deps.Config to know which modules MODULES enables")
	}
	return composeFrom(deps, embedded, mods...)
}

// compose is Compose with the contract loader as a seam, so tests can
// compose in-memory contracts instead of the embedded specs. Nothing it
// loads is remembered: every call parses its contracts afresh.
func compose(deps Deps, load func(context.Context, string) (*openapi3.T, error), mods ...Module) (http.Handler, error) {
	return composeFrom(deps, freshContracts(load), mods...)
}

// contractSource is where composeFrom gets a module's own contract and the
// combined one from.
type contractSource interface {
	// doc is the contract handed to a module's Mount as Deps.Doc. It may be
	// shared between compositions, so nobody may change it.
	doc(ctx context.Context, name string) (*openapi3.T, error)
	// combined is the body of GET /api/openapi.json for these modules, in
	// this order.
	combined(ctx context.Context, order []string) ([]byte, error)
}

// freshContracts parses on every call and remembers nothing.
type freshContracts func(context.Context, string) (*openapi3.T, error)

func (load freshContracts) doc(ctx context.Context, name string) (*openapi3.T, error) {
	return load(ctx, name)
}

// combined gives mergeContract documents of its own: it internalises their
// references in place, so they cannot be the ones doc handed out.
func (load freshContracts) combined(ctx context.Context, order []string) ([]byte, error) {
	docs := make(map[string]*openapi3.T, len(order))
	for _, name := range order {
		doc, err := load(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("module: load %s contract: %w", name, err)
		}
		docs[name] = doc
	}
	contract, err := mergeContract(docs, order)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(contract)
	if err != nil {
		return nil, fmt.Errorf("module: marshal the combined contract: %w", err)
	}
	return body, nil
}

// embedded is the embedded specs, each parsed once per process and each
// combination of modules merged once per process.
//
// A server composes once, so this changes nothing for it. A test binary
// composes once per test — hundreds of times — and parsing seven modules'
// contracts twice over on every one of them was most of what the suite did:
// under the race detector on four cores it took the whole run from seventeen
// minutes to three. What makes sharing sound is that a contract is read-only
// once loaded (Deps.Doc says so) and that the race detector, which the same
// suite runs under, fails the run the moment anything writes to one.
var embedded = &rememberedContracts{load: openapi.Load}

type rememberedContracts struct {
	load   freshContracts
	docs   sync.Map // module name -> *openapi3.T
	bodies sync.Map // module names in order, NUL-joined -> []byte
}

func (c *rememberedContracts) doc(ctx context.Context, name string) (*openapi3.T, error) {
	if doc, ok := c.docs.Load(name); ok {
		return doc.(*openapi3.T), nil
	}
	doc, err := c.load(ctx, name)
	if err != nil {
		return nil, err
	}
	// Two first callers may both have parsed it; both get the one that won.
	kept, _ := c.docs.LoadOrStore(name, doc)
	return kept.(*openapi3.T), nil
}

func (c *rememberedContracts) combined(ctx context.Context, order []string) ([]byte, error) {
	// NUL cannot appear in a module name, so no two sets of modules share a key.
	key := strings.Join(order, "\x00")
	if body, ok := c.bodies.Load(key); ok {
		return body.([]byte), nil
	}
	// Merged from documents of its own (freshContracts.combined), never from
	// the shared ones doc hands out: merging rewrites what it is given.
	body, err := c.load.combined(ctx, order)
	if err != nil {
		return nil, err
	}
	kept, _ := c.bodies.LoadOrStore(key, body)
	return kept.([]byte), nil
}

func composeFrom(deps Deps, contractsFrom contractSource, mods ...Module) (http.Handler, error) {
	ctx := context.Background()
	// Every module given, before enablement drops any: both customer slots
	// below — the reference holders and the personal-data providers — are
	// collected from all of them.
	given := mods
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

	// The customer directory, the user directory, the product catalog, the
	// project directory, project actuals and project expenses are the
	// sanctioned cross-module reads (contracts.CustomerDirectory,
	// UserDirectory, ProductCatalog, ProjectDirectory, ProjectActuals,
	// ProjectExpenses): at most one enabled module may declare each. Each is
	// resolved here, in this order, before any Mount runs, so its result can
	// be copied onto every module's Deps below — including its own
	// provider's, which may need it too — and so a later slot's provider
	// func may use an earlier one already set on deps (Projects, say, may
	// read deps.Directory). Each provider func runs on deps as Compose
	// itself received it plus whatever earlier slot just set, deliberately
	// narrower than the per-module copy Mount gets (no Doc, no per-module
	// Catalog reference beyond what is already built here): building a
	// directory is a data-layer concern (Pool, Config, Clock, Secrets, ...),
	// not a contract one, and no module's own Doc is loaded yet at this
	// point regardless.
	directoryProvider, err := soleProvider(mods, "a customer directory", func(m Module) bool { return m.Directory != nil })
	if err != nil {
		return nil, err
	}
	if directoryProvider != nil {
		deps.Directory = directoryProvider.Directory(deps)
	}

	usersProvider, err := soleProvider(mods, "a user directory", func(m Module) bool { return m.Users != nil })
	if err != nil {
		return nil, err
	}
	if usersProvider != nil {
		deps.Users = usersProvider.Users(deps)
	}

	productsProvider, err := soleProvider(mods, "a product catalog", func(m Module) bool { return m.Products != nil })
	if err != nil {
		return nil, err
	}
	if productsProvider != nil {
		deps.Products = productsProvider.Products(deps)
	}

	projectsProvider, err := soleProvider(mods, "a project directory", func(m Module) bool { return m.Projects != nil })
	if err != nil {
		return nil, err
	}
	if projectsProvider != nil {
		deps.Projects = projectsProvider.Projects(deps)
	}

	// Actuals resolves after the project directory: the module that
	// owns logged work is built on the one that owns projects, so its
	// provider func may read deps.Projects — while it is built, never while
	// it serves (contracts.ProjectActuals).
	actualsProvider, err := soleProvider(mods, "project actuals", func(m Module) bool { return m.Actuals != nil })
	if err != nil {
		return nil, err
	}
	if actualsProvider != nil {
		deps.Actuals = actualsProvider.Actuals(deps)
	}

	// Project expenses resolves beside actuals, and for the same reason: the
	// module that owns expenses may read the one that owns projects while its
	// provider is built, never while it serves (contracts.ProjectExpenses).
	// The two are independent — an installation may have either, both or
	// neither.
	expensesProvider, err := soleProvider(mods, "project expenses", func(m Module) bool { return m.Expenses != nil })
	if err != nil {
		return nil, err
	}
	if expensesProvider != nil {
		deps.Expenses = expensesProvider.Expenses(deps)
	}

	// Customer reference holders are the one many-provider contract slot
	// (contracts.CustomerReferenceHolder, customers merge design D1): every
	// module given that declares one contributes it, in the order given, so
	// the merge that calls them does so in the same order on every run. A
	// module MODULES leaves out contributes its holder too, unlike every other
	// slot: every schema is migrated whatever MODULES says, so a module that
	// was on once and is off now still has rows naming customers, and a merge
	// that skipped them would leave them on the absorbed customer for good,
	// waiting for the module to come back. A holder needs only the caller's
	// transaction and its own schema, both there regardless. They are
	// resolved last, on deps as the single slots left it, and appended to
	// whatever the caller preset — onto a copy, so a harness's own slice is
	// never written through.
	var holders []contracts.CustomerReferenceHolder
	for _, mod := range given {
		if mod.CustomerReferences == nil {
			continue
		}
		if holder := mod.CustomerReferences(deps); holder != nil {
			holders = append(holders, holder)
		}
	}
	if len(holders) > 0 {
		deps.CustomerReferenceHolders = append(slices.Clone(deps.CustomerReferenceHolders), holders...)
	}

	// Customer personal data is the second many-provider slot (customers GDPR
	// design D2), collected from every module given, for the holders' reason
	// above, by the helper Workers uses too: the export reads it through a
	// Mount, the anonymisation worker through Workers.
	deps = withCustomerPersonalData(deps, given)

	outer := http.NewServeMux()
	mounts := make([]moduleMount, 0, len(mods))
	order := make([]string, 0, len(mods))

	for _, mod := range mods {
		doc, err := contractsFrom.doc(ctx, mod.Name)
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
		// The same subtree, matched again ahead of outer by dispatch below, so
		// http.ServeMux's path cleaning never folds a doubled slash inside a
		// module's subtree onto the real path (see moduleMount).
		mounts = append(mounts, moduleMount{prefix: "/api/v1/" + mod.Name + "/", handler: handler})
		// The subtree pattern alone does not cover the module's own root path,
		// so it is registered explicitly — whether or not the contract
		// declares it. A contract that declares "/api/v1/customers" (the
		// listing and its create) would otherwise have those requests
		// answered by http.ServeMux's automatic redirect to the
		// trailing-slash form: a path no contract declares, and one the
		// module's router would then answer 404. Registering the bare path
		// suppresses that redirect and delivers the request, path unchanged,
		// to the module.
		//
		// A module whose contract declares no bare root (energy) is
		// registered just the same, and for the same reason: the redirect is
		// wrong there too. It answered 307 with a text/html body and ran no
		// Access.Check at all, where every other undeclared API path answers
		// the 404 problem (internal/server's own /api contract). Handing the
		// path to the module instead lets its router answer that 404, as it
		// does for any other path its contract does not declare.
		outer.Handle("/api/v1/"+mod.Name, handler)
		mounts[len(mounts)-1].root = "/api/v1/" + mod.Name

		order = append(order, mod.Name)
	}

	body, err := contractsFrom.combined(ctx, order)
	if err != nil {
		return nil, err
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

	// Both the subtree and the bare "/api". Without the bare pattern,
	// http.ServeMux answers "/api" with its automatic redirect to "/api/" —
	// a 307 carrying a text/html body, and no Access.Check — where
	// internal/server's own contract (server.go's /api matcher) says every
	// unknown API path is the 404 problem.
	outer.HandleFunc("/api/", httpx.NotFound)
	outer.HandleFunc("/api", httpx.NotFound)

	return dispatchModules(mounts, outer), nil
}

// soleProvider returns the one module among mods for which declares reports
// true, nil when none does, and an error naming both when two do. what
// names the thing being provided ("a customer directory") for that error
// message, in the style Compose's own doc comment lists.
func soleProvider(mods []Module, what string, declares func(Module) bool) (*Module, error) {
	var provider *Module
	for i := range mods {
		if !declares(mods[i]) {
			continue
		}
		if provider != nil {
			return nil, fmt.Errorf("module: multiple modules declare %s: %q and %q", what, provider.Name, mods[i].Name)
		}
		provider = &mods[i]
	}
	return provider, nil
}

// moduleMount is one module's mounted handler and the paths that reach it:
// prefix is its subtree ("/api/v1/customers/"), root the bare path its own
// contract declares ("/api/v1/customers"), empty when it declares none.
type moduleMount struct {
	prefix  string
	root    string
	handler http.Handler
}

// dispatchModules matches each module's subtree itself, on the still-escaped
// path, before falling back to fallback (the http.ServeMux carrying
// /api/openapi.json, the /api catch-all, and the subtree patterns a module
// without its own root path still redirects through).
//
// The interception exists for one reason: http.ServeMux cleans the request
// path and redirects when the cleaned form differs, which folds
// "/api/v1/customers//contacts" onto the real "/api/v1/customers/contacts"
// and answers 307. .NET answered 404 there, and Router.matches deliberately
// refuses the empty segment a doubled slash produces so it falls through to
// the 404 problem — a guard the outer mux's redirect was reaching around
// before the module's router ever saw the path. Matching here delivers the
// path to the module verbatim, so the router's guard decides, as it does for
// a trailing slash.
func dispatchModules(mounts []moduleMount, fallback http.Handler) http.Handler {
	if len(mounts) == 0 {
		return fallback
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.EscapedPath()
		for _, m := range mounts {
			if strings.HasPrefix(path, m.prefix) || (m.root != "" && path == m.root) {
				m.handler.ServeHTTP(w, r)
				return
			}
		}
		fallback.ServeHTTP(w, r)
	})
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

// withCustomerPersonalData is deps with every module's
// contracts.CustomerPersonalData appended to Deps.CustomerPersonalData, each
// under its module's name, in mods order (customers GDPR design D2) — onto a
// copy of whatever the caller preset, so a harness's own slice is never
// written through. mods is every module given, enabled or not.
func withCustomerPersonalData(deps Deps, mods []Module) Deps {
	var holders []contracts.CustomerPersonalDataHolder
	for _, mod := range mods {
		if mod.CustomerPersonalData == nil {
			continue
		}
		if data := mod.CustomerPersonalData(deps); data != nil {
			holders = append(holders, contracts.CustomerPersonalDataHolder{Module: mod.Name, Data: data})
		}
	}
	if len(holders) > 0 {
		deps.CustomerPersonalData = append(slices.Clone(deps.CustomerPersonalData), holders...)
	}
	return deps
}
