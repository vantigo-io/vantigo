package module

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

const alphaContract = `
openapi: 3.0.3
info: { title: Alpha, version: "1" }
paths:
  /api/v1/alpha/x:
    get:
      operationId: getAlphaX
      x-vantigo-access: anonymous
      responses:
        "204": { description: ok }
components:
  schemas:
    Shared:
      type: object
      properties:
        id: { type: string }
`

// alphaRootContract declares the module's own root path, "/api/v1/alpha", as
// the real customers contract declares "/api/v1/customers" for the customer
// listing and its create, alongside a path below it.
const alphaRootContract = `
openapi: 3.0.3
info: { title: Alpha, version: "1" }
paths:
  /api/v1/alpha:
    get:
      operationId: getAlpha
      x-vantigo-access: anonymous
      responses:
        "204": { description: ok }
  /api/v1/alpha/x:
    get:
      operationId: getAlphaX
      x-vantigo-access: anonymous
      responses:
        "204": { description: ok }
`

const betaContract = `
openapi: 3.0.3
info: { title: Beta, version: "1" }
paths:
  /api/v1/beta/y:
    get:
      operationId: getBetaY
      x-vantigo-access: session
      responses:
        "204": { description: ok }
components:
  schemas:
    Shared:
      type: object
      properties:
        id: { type: string }
`

// betaConflictContract's Shared schema differs from alphaContract's (an
// integer id, not a string), so merging alpha with this instead of beta must
// fail.
const betaConflictContract = `
openapi: 3.0.3
info: { title: Beta, version: "1" }
paths:
  /api/v1/beta/y:
    get:
      operationId: getBetaY
      x-vantigo-access: session
      responses:
        "204": { description: ok }
components:
  schemas:
    Shared:
      type: object
      properties:
        id: { type: integer }
`

// identityContract is a fixture standing in for the real identity module: a
// module named "identity", the one name enabledModules always mounts
// regardless of Deps.Config.Modules.
const identityContract = `
openapi: 3.0.3
info: { title: Identity, version: "1" }
paths:
  /api/v1/identity/z:
    get:
      operationId: getIdentityZ
      x-vantigo-access: anonymous
      responses:
        "204": { description: ok }
`

// gammaDuplicatePathContract declares the exact same path alphaContract
// does, under a different module name — a path two modules both declare.
const gammaDuplicatePathContract = `
openapi: 3.0.3
info: { title: Gamma, version: "1" }
paths:
  /api/v1/alpha/x:
    get:
      operationId: getGammaDuplicate
      x-vantigo-access: anonymous
      responses:
        "204": { description: ok }
`

// fakeLoad is a compose loader over in-memory fixtures, the seam decision 3
// asks for so Compose is unit-testable without the embedded specs.
func fakeLoad(docs map[string]string) func(context.Context, string) (*openapi3.T, error) {
	return func(_ context.Context, name string) (*openapi3.T, error) {
		yaml, ok := docs[name]
		if !ok {
			return nil, fmt.Errorf("fakeLoad: no fixture for %q", name)
		}
		return openapi3.NewLoader().LoadFromData([]byte(yaml))
	}
}

// staticHandler is a Module.Mount that answers every request with body and
// the path it was actually called with, so a test can tell Compose forwarded
// the full, unstripped path.
func staticHandler(body string) func(Deps) (http.Handler, error) {
	return func(Deps) (http.Handler, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, "%s:%s", body, r.URL.Path)
		}), nil
	}
}

func TestCompose_RoutesToModulesAndAnswers404Otherwise(t *testing.T) {
	handler, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}),
		Module{Name: "alpha", Mount: staticHandler("alpha")},
		Module{Name: "beta", Mount: staticHandler("beta")},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alpha/x", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if want := "alpha:/api/v1/alpha/x"; rec.Body.String() != want {
		t.Errorf("body = %q, want %q (the full path, unstripped)", rec.Body.String(), want)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/other", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
}

// A module whose contract declares its own root path receives requests for
// it, path unchanged. The subtree pattern alone does not cover that path:
// http.ServeMux would answer it with a redirect to the trailing-slash form,
// which no contract declares and the module's own router would then answer
// 404, leaving the customer listing unreachable.
func TestCompose_RoutesTheModulesOwnRootPath(t *testing.T) {
	handler, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaRootContract, "beta": betaContract}),
		Module{Name: "alpha", Mount: staticHandler("alpha")},
		Module{Name: "beta", Mount: staticHandler("beta")},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alpha", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if want := "alpha:/api/v1/alpha"; rec.Body.String() != want {
		t.Errorf("root path: body = %q, want %q (the full path, unstripped, and no redirect)", rec.Body.String(), want)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/alpha/x", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if want := "alpha:/api/v1/alpha/x"; rec.Body.String() != want {
		t.Errorf("subtree: body = %q, want %q", rec.Body.String(), want)
	}

	// beta's contract declares no root path of its own, so nothing is
	// registered for it and http.ServeMux's subtree redirect still applies,
	// exactly as before.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/beta", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently && rec.Code != http.StatusTemporaryRedirect {
		t.Errorf("a module without a root path: status = %d, want the unchanged ServeMux redirect", rec.Code)
	}
}

// A module cfg.Modules does not enable contributes no route, no
// permission-catalog entry and no contract path: its paths fall through to
// the /api 404 problem, exactly as if it had never been passed to Compose.
func TestCompose_DisabledModuleContributesNothing(t *testing.T) {
	alphaPerm := contracts.Permission{Key: "alpha:manage", Display: "d", Description: "d", Category: "c"}
	betaPerm := contracts.Permission{Key: "beta:manage", Display: "d", Description: "d", Category: "c"}
	var gotCatalog map[string]contracts.Permission

	handler, err := compose(
		Deps{Access: &fakeAccess{}, Config: &config.Config{Modules: []string{"alpha"}}},
		fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}),
		Module{Name: "alpha", Permissions: []contracts.Permission{alphaPerm}, Mount: func(d Deps) (http.Handler, error) {
			gotCatalog = d.Catalog
			return staticHandler("alpha")(d)
		}},
		Module{Name: "beta", Permissions: []contracts.Permission{betaPerm}, Mount: staticHandler("beta")},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alpha/x", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if want := "alpha:/api/v1/alpha/x"; rec.Body.String() != want {
		t.Errorf("enabled module: body = %q, want %q", rec.Body.String(), want)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/beta/y", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("disabled module: status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("disabled module: Content-Type = %q, want application/problem+json", ct)
	}

	if _, ok := gotCatalog["alpha:manage"]; !ok {
		t.Error("catalog is missing the enabled module's permission")
	}
	if _, ok := gotCatalog["beta:manage"]; ok {
		t.Error("catalog holds the disabled module's permission")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("openapi.json: status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var doc struct {
		Paths map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("openapi.json: invalid JSON: %v", err)
	}
	if _, ok := doc.Paths["/api/v1/alpha/x"]; !ok {
		t.Error("combined contract is missing the enabled module's path")
	}
	if _, ok := doc.Paths["/api/v1/beta/y"]; ok {
		t.Error("combined contract holds the disabled module's path")
	}
}

// Identity is always mounted, even against the production shape of
// Deps.Config, where Modules never contains "identity" itself (config.Load
// never puts it there). A module Config.Modules does not enable still gets
// nothing, alongside it.
func TestCompose_IdentityMountsRegardlessOfConfigModules(t *testing.T) {
	handler, err := compose(
		Deps{Access: &fakeAccess{}, Config: &config.Config{Modules: []string{"alpha"}}},
		fakeLoad(map[string]string{"identity": identityContract, "beta": betaContract}),
		Module{Name: "identity", Mount: staticHandler("identity")},
		Module{Name: "beta", Mount: staticHandler("beta")},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/identity/z", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if want := "identity:/api/v1/identity/z"; rec.Body.String() != want {
		t.Errorf("identity: body = %q, want %q", rec.Body.String(), want)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/beta/y", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("beta (disabled, not in Config.Modules): status = %d, want 404", rec.Code)
	}
}

// The exported Compose fails closed without a Config: enablement is
// meaningless without one, and every real caller (cmd/vantigo, the identity
// test harness) already loads a real one before composing.
func TestCompose_RequiresAConfig(t *testing.T) {
	_, err := Compose(Deps{Access: &fakeAccess{}}, Module{Name: "identity", Mount: staticHandler("identity")})
	if err == nil {
		t.Fatal("Compose: want an error when Deps.Config is nil")
	}
	if !strings.Contains(err.Error(), "Config") {
		t.Errorf("error %q does not name the missing config", err)
	}
}

func TestCompose_OpenAPIJSONRequiresASessionAndListsEveryPath(t *testing.T) {
	access := &fakeAccess{check: func(*http.Request, contracts.Rule) (contracts.Principal, error) {
		return contracts.Principal{}, contracts.ErrUnauthenticated
	}}
	handler, err := compose(Deps{Access: access}, fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}),
		Module{Name: "alpha", Mount: staticHandler("alpha")},
		Module{Name: "beta", Mount: staticHandler("beta")},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without a session: status = %d, want 401", rec.Code)
	}
	if len(access.rejected) != 1 {
		t.Fatalf("Reject was called %d times, want 1", len(access.rejected))
	}
	if access.rejected[0].rule.Kind != contracts.RuleSession {
		t.Errorf("Reject's rule = %+v, want RuleSession", access.rejected[0].rule)
	}

	access.check = func(*http.Request, contracts.Rule) (contracts.Principal, error) {
		return contracts.Principal{}, nil
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with a session: status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var doc struct {
		Paths      map[string]any `json:"paths"`
		Components struct {
			Schemas map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, want := range []string{"/api/v1/alpha/x", "/api/v1/beta/y"} {
		if _, ok := doc.Paths[want]; !ok {
			t.Errorf("combined contract missing path %q", want)
		}
	}
	// alpha and beta declare an identical Shared schema: it is kept once.
	if _, ok := doc.Components.Schemas["Shared"]; !ok {
		t.Error("combined contract missing the Shared schema")
	}
	if len(doc.Components.Schemas) != 1 {
		t.Errorf("Schemas = %v, want exactly one (the shared component kept once)", doc.Components.Schemas)
	}
}

func TestCompose_DifferingComponentUnderTheSameNameFails(t *testing.T) {
	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaConflictContract}),
		Module{Name: "alpha", Mount: staticHandler("alpha")},
		Module{Name: "beta", Mount: staticHandler("beta")},
	)
	if err == nil {
		t.Fatal("compose: want an error for the differing Shared component")
	}
	if !strings.Contains(err.Error(), "Shared") {
		t.Errorf("error %q does not name the differing component", err)
	}
}

func TestCompose_DuplicatePathAcrossModulesFails(t *testing.T) {
	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract, "gamma": gammaDuplicatePathContract}),
		Module{Name: "alpha", Mount: staticHandler("alpha")},
		Module{Name: "gamma", Mount: staticHandler("gamma")},
	)
	if err == nil {
		t.Fatal("compose: want an error for the path both modules declare")
	}
	if !strings.Contains(err.Error(), "/api/v1/alpha/x") {
		t.Errorf("error %q does not name the duplicate path", err)
	}
}

func TestCompose_DuplicateModuleNameFails(t *testing.T) {
	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract}),
		Module{Name: "alpha", Mount: staticHandler("alpha")},
		Module{Name: "alpha", Mount: staticHandler("alpha")},
	)
	if err == nil {
		t.Fatal("compose: want an error for the duplicate module name")
	}
}

func TestCompose_DuplicatePermissionFails(t *testing.T) {
	perm := contracts.Permission{Key: "alpha:manage", Display: "d", Description: "d", Category: "c"}
	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract}),
		Module{Name: "alpha", Permissions: []contracts.Permission{perm, perm}, Mount: staticHandler("alpha")},
	)
	if err == nil {
		t.Fatal("compose: want an error for the duplicate permission")
	}
}

func TestCompose_InvalidPermissionFails(t *testing.T) {
	// The key's module prefix ("beta") does not match the owning module ("alpha").
	perm := contracts.Permission{Key: "beta:manage", Display: "d", Description: "d", Category: "c"}
	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract}),
		Module{Name: "alpha", Permissions: []contracts.Permission{perm}, Mount: staticHandler("alpha")},
	)
	if err == nil {
		t.Fatal("compose: want an error for the invalid permission")
	}
}

func TestCompose_MountErrorFails(t *testing.T) {
	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract}),
		Module{Name: "alpha", Mount: func(Deps) (http.Handler, error) { return nil, errors.New("boom") }},
	)
	if err == nil {
		t.Fatal("compose: want an error when Mount fails")
	}
}

func TestCompose_PassesTheComposedCatalogAndTheModulesOwnDocToMount(t *testing.T) {
	perm := contracts.Permission{Key: "alpha:manage", Display: "d", Description: "d", Category: "c"}
	var gotCatalog map[string]contracts.Permission
	var gotDoc *openapi3.T
	mount := func(d Deps) (http.Handler, error) {
		gotCatalog, gotDoc = d.Catalog, d.Doc
		return http.NotFoundHandler(), nil
	}

	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract}),
		Module{Name: "alpha", Permissions: []contracts.Permission{perm}, Mount: mount},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if _, ok := gotCatalog["alpha:manage"]; !ok {
		t.Error("Mount's Deps.Catalog is missing the module's own permission")
	}
	if gotDoc == nil {
		t.Error("Mount's Deps.Doc is nil")
	}
}

// Decision 5: merge the six real contracts (identity plus the four business
// modules, each loaded by openapi.Load, common.yaml pulled in by reference)
// without error, and check that no common.yaml# reference survives, the
// path count matches the sum of the modules' own, and the served bytes
// reload and validate as a standalone OpenAPI document.
func TestCompose_MergesTheSixRealContracts(t *testing.T) {
	ctx := context.Background()

	var wantPaths int
	for _, name := range openapi.Modules {
		doc, err := openapi.Load(ctx, name)
		if err != nil {
			t.Fatalf("Load(%s): %v", name, err)
		}
		wantPaths += len(doc.Paths.Map())
	}

	access := &fakeAccess{check: func(*http.Request, contracts.Rule) (contracts.Principal, error) {
		return contracts.Principal{}, nil
	}}
	mods := make([]Module, len(openapi.Modules))
	for i, name := range openapi.Modules {
		mods[i] = Module{Name: name, Mount: func(Deps) (http.Handler, error) { return http.NotFoundHandler(), nil }}
	}

	// Every business module contract here must actually mount, so all of
	// them are enabled; identity mounts regardless.
	handler, err := Compose(Deps{Access: access, Config: &config.Config{Modules: []string{"customers", "products", "energy", "communications"}}}, mods...)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "common.yaml#") {
		t.Error("the combined contract still references common.yaml#")
	}

	var doc struct {
		Paths map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(doc.Paths) != wantPaths {
		t.Errorf("combined contract has %d paths, want %d (the sum of every module's own)", len(doc.Paths), wantPaths)
	}

	reloaded, err := openapi3.NewLoader().LoadFromData(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("reload the served bytes: %v", err)
	}
	if err := reloaded.Validate(ctx); err != nil {
		t.Errorf("the combined contract does not validate on its own: %v", err)
	}
}

// Decision 3/5: mergeContract's InternalizeRefs must not reach back into a
// module's own Doc (the *openapi3.T a module's Mount received and may keep):
// Compose merges independent copies.
func TestCompose_DoesNotMutateModulesDocs(t *testing.T) {
	ctx := context.Background()

	var identityDoc *openapi3.T
	mods := []Module{
		{Name: "identity", Mount: func(d Deps) (http.Handler, error) {
			identityDoc = d.Doc
			return http.NotFoundHandler(), nil
		}},
		{Name: "customers", Mount: func(Deps) (http.Handler, error) { return http.NotFoundHandler(), nil }},
	}
	if _, err := Compose(Deps{Access: &fakeAccess{}, Config: &config.Config{Modules: []string{"customers"}}}, mods...); err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if identityDoc == nil {
		t.Fatal("identity's Mount never ran")
	}

	got, err := json.Marshal(identityDoc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(got), "common.yaml#") {
		t.Fatal("sanity check failed: identity's own Doc has no common.yaml# ref to begin with")
	}

	fresh, err := openapi.Load(ctx, "identity")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want, err := json.Marshal(fresh)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != string(want) {
		t.Error("Compose mutated the module's own Doc: its marshalled JSON changed after Compose ran")
	}
}

// fakeDirectory is a minimal contracts.CustomerDirectory: these tests only
// need a distinct, comparable value to inject through Deps and assert on,
// never its actual lookup behaviour.
type fakeDirectory struct{}

func (*fakeDirectory) Customer(context.Context, int32) (*contracts.CustomerEntry, error) {
	return nil, nil
}

func (*fakeDirectory) Contact(context.Context, int32) (*contracts.ContactEntry, error) {
	return nil, nil
}

func (*fakeDirectory) ContactsByEmail(context.Context, string) ([]contracts.ContactMatch, error) {
	return nil, nil
}

// Decision 3: the module that declares Directory has it resolved before any
// Mount runs, and the result reaches every module's Deps — including a
// module that does not provide one itself, and the provider's own Mount.
func TestCompose_DirectoryProviderReachesEveryModulesMount(t *testing.T) {
	fake := &fakeDirectory{}
	var gotInAlpha, gotInBeta contracts.CustomerDirectory

	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}),
		Module{Name: "alpha", Mount: func(d Deps) (http.Handler, error) {
			gotInAlpha = d.Directory
			return staticHandler("alpha")(d)
		}},
		Module{
			Name:      "beta",
			Directory: func(Deps) contracts.CustomerDirectory { return fake },
			Mount: func(d Deps) (http.Handler, error) {
				gotInBeta = d.Directory
				return staticHandler("beta")(d)
			},
		},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if gotInAlpha != fake {
		t.Errorf("alpha's Deps.Directory = %v, want the directory beta provides", gotInAlpha)
	}
	if gotInBeta != fake {
		t.Errorf("beta's own Deps.Directory = %v, want the directory it provides", gotInBeta)
	}
}

// With no module declaring Directory, Deps.Directory is nil in every Mount:
// Compose never invents a directory, and a consumer sees a plain nil rather
// than some zero-value stand-in.
func TestCompose_NoDirectoryProviderLeavesItNil(t *testing.T) {
	var got contracts.CustomerDirectory = &fakeDirectory{} // starts non-nil so a no-op would be caught

	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract}),
		Module{Name: "alpha", Mount: func(d Deps) (http.Handler, error) {
			got = d.Directory
			return staticHandler("alpha")(d)
		}},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got != nil {
		t.Errorf("Deps.Directory = %v, want nil with no provider", got)
	}
}

// Two modules both declaring Directory is a compose error naming both, in
// the style of the duplicate-name and duplicate-permission errors above.
func TestCompose_TwoDirectoryProvidersFails(t *testing.T) {
	_, err := compose(Deps{Access: &fakeAccess{}}, fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}),
		Module{Name: "alpha", Directory: func(Deps) contracts.CustomerDirectory { return &fakeDirectory{} }, Mount: staticHandler("alpha")},
		Module{Name: "beta", Directory: func(Deps) contracts.CustomerDirectory { return &fakeDirectory{} }, Mount: staticHandler("beta")},
	)
	if err == nil {
		t.Fatal("compose: want an error when two modules declare a customer directory")
	}
	if !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "beta") {
		t.Errorf("error %q does not name both modules", err)
	}
}

// enabledModules filters before the directory scan runs, so a disabled
// module's Directory never counts toward the two-provider check and is
// never resolved: two modules declare Directory, only one (alpha) is
// enabled, Compose succeeds, and the enabled provider's own directory is
// what its Mount sees.
func TestCompose_DisabledDirectoryProviderDoesNotCount(t *testing.T) {
	enabled := &fakeDirectory{}
	var gotInAlpha contracts.CustomerDirectory

	_, err := compose(
		Deps{Access: &fakeAccess{}, Config: &config.Config{Modules: []string{"alpha"}}},
		fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}),
		Module{
			Name:      "alpha",
			Directory: func(Deps) contracts.CustomerDirectory { return enabled },
			Mount: func(d Deps) (http.Handler, error) {
				gotInAlpha = d.Directory
				return staticHandler("alpha")(d)
			},
		},
		Module{Name: "beta", Directory: func(Deps) contracts.CustomerDirectory { return &fakeDirectory{} }, Mount: staticHandler("beta")},
	)
	if err != nil {
		t.Fatalf("compose: %v, want no error: beta's Directory is disabled and must not count toward the two-provider check", err)
	}
	if gotInAlpha != enabled {
		t.Errorf("alpha's Deps.Directory = %v, want its own directory", gotInAlpha)
	}
}

func TestDecodeError(t *testing.T) {
	write := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_request","message":"The request is invalid."}`))
	}
	handler := DecodeError(write)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	handler(rec, req, errors.New("column \"foo\" does not exist"))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "does not exist") {
		t.Errorf("body echoes the error: %q", rec.Body.String())
	}
}

func TestResponseError(t *testing.T) {
	t.Run("ErrNotImplemented", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ResponseError()(rec, httptest.NewRequest(http.MethodGet, "/", nil), ErrNotImplemented)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("status = %d, want 501", rec.Code)
		}
	})

	t.Run("wrapped ErrNotImplemented", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ResponseError()(rec, httptest.NewRequest(http.MethodGet, "/", nil), fmt.Errorf("handler getX: %w", ErrNotImplemented))
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("status = %d, want 501", rec.Code)
		}
	})

	t.Run("any other error is a 500 that never echoes it", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ResponseError()(rec, httptest.NewRequest(http.MethodGet, "/", nil), errors.New("column \"foo\" does not exist"))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "does not exist") {
			t.Errorf("body echoes the error: %q", rec.Body.String())
		}
	})
}
