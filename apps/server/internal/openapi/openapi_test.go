package openapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/oasdiff/yaml"
)

const repoContract = "../../../../openapi"

// TestEmbeddedSpecsMatchTheContract guards the go:generate copy against
// drifting from openapi/, the single source of truth.
func TestEmbeddedSpecsMatchTheContract(t *testing.T) {
	root, err := filepath.Glob(filepath.Join(repoContract, "*.yaml"))
	if err != nil || len(root) == 0 {
		t.Fatalf("no contract files in %s: %v", repoContract, err)
	}
	embedded, _ := fs.Glob(Files(), "*.yaml")
	if len(embedded) != len(root) {
		t.Fatalf("embedded %v, contract %v — run go generate ./... from apps/server", embedded, root)
	}
	for _, path := range root {
		want, _ := os.ReadFile(path)
		got, err := fs.ReadFile(Files(), filepath.Base(path))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s has drifted from its embedded copy — run go generate ./... from apps/server", filepath.Base(path))
		}
	}
}

func TestEveryModuleLoadsAndValidates(t *testing.T) {
	for _, name := range Modules {
		doc, err := Load(context.Background(), name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasPrefix(doc.OpenAPI, "3.0.") {
			t.Errorf("%s: openapi %q, want 3.0.x", name, doc.OpenAPI)
		}
		if err := doc.Validate(context.Background(), openapi3.EnableExamplesValidation()); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestLoadMatchesAnUncachedYAMLParse proves the JSON the parse cache holds
// yields exactly the document the embedded YAML yields: Load caches how the
// contract is parsed, never what it parses to. It is the guard on
// convertSpecsToJSON — a conversion that resolved a date example to a
// timestamp, say, would change the contract silently everywhere else.
func TestLoadMatchesAnUncachedYAMLParse(t *testing.T) {
	ctx := context.Background()
	files := Files()
	for _, name := range Modules {
		loader := openapi3.NewLoader()
		loader.Context = ctx
		loader.IsExternalRefsAllowed = true
		loader.ReadFromURIFunc = func(_ *openapi3.Loader, location *url.URL) ([]byte, error) {
			return fs.ReadFile(files, pathpkg.Clean(strings.TrimPrefix(location.Path, "/")))
		}
		data, err := fs.ReadFile(files, name+".yaml")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want, err := loader.LoadFromDataWithPath(data, &url.URL{Path: name + ".yaml"})
		if err != nil {
			t.Fatalf("%s: parse the embedded YAML: %v", name, err)
		}
		got, err := Load(ctx, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		wantJSON, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		gotJSON, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(wantJSON, gotJSON) {
			t.Errorf("%s: Load's document differs from parsing the embedded YAML directly", name)
		}
	}
}

// TestLoadReturnsIndependentDocuments proves the parse cache hands out no
// shared state: two Loads of one module are two documents, equal on the
// wire, and mutating one — as internal/module's mergeContract does through
// InternalizeRefs, which is why compose loads each contract twice — leaves
// the other byte for byte as it was.
func TestLoadReturnsIndependentDocuments(t *testing.T) {
	ctx := context.Background()
	for _, name := range Modules {
		first, err := Load(ctx, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		second, err := Load(ctx, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if first == second {
			t.Fatalf("%s: two Loads returned the same document", name)
		}
		firstJSON, err := json.Marshal(first)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		before, err := json.Marshal(second)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(firstJSON, before) {
			t.Errorf("%s: two Loads of one module differ", name)
		}

		first.InternalizeRefs(ctx, nil)
		mutated, err := json.Marshal(first)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if bytes.Contains(firstJSON, []byte("common.yaml#")) && bytes.Equal(firstJSON, mutated) {
			t.Fatalf("%s: sanity check failed — InternalizeRefs changed nothing, so this proves nothing", name)
		}
		after, err := json.Marshal(second)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(before, after) {
			t.Errorf("%s: mutating one loaded document changed another", name)
		}
	}
}

func TestOperationIDsAreUniqueAcrossModules(t *testing.T) {
	seen := map[string]string{}
	for _, name := range Modules {
		doc, err := Load(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range operations(doc) {
			if other, dup := seen[op.OperationID]; dup {
				t.Errorf("operationId %s in both %s and %s", op.OperationID, other, name)
			}
			seen[op.OperationID] = name
		}
	}
}

// KnownServeMuxConflicts pins the operation pairs Go's stdlib http.ServeMux
// (1.22+) refuses to register together: it has no notion of route
// constraints — unlike the .NET host, which separates these with
// {id:int}/{id:guid} — and no literal-before-parameter precedence across
// differing segments, so it can't tell that an id will never literally equal
// "contacts", "variants" or "tasks". These are genuine ambiguities from ServeMux's
// point of view, not a contract defect; sub-project 3 mounts the generated
// handlers on a precedence-aware router (via StdHTTPServerOptions.BaseRouter)
// and must route each pair correctly — see TestKnownServeMuxConflictsMountOnModuleRouter,
// which proves it does. A pair appearing or disappearing here means the
// contract's route shape changed and sub-project 3 needs to know.
//
// Exported so TestKnownServeMuxConflictsMountOnModuleRouter, in the
// black-box openapi_test package (module_router_test.go), can register the
// same pairs on module.NewRouter: module already imports this package in
// production code, so this white-box test file — package openapi — cannot
// import module itself without an import cycle.
var KnownServeMuxConflicts = []string{
	"DELETE /api/v1/customers/contacts/{id} ⟷ DELETE /api/v1/customers/{id}/legal-identity",
	"GET /api/v1/customers/contacts/{id} ⟷ GET /api/v1/customers/{id}/contacts",
	"GET /api/v1/customers/contacts/{id} ⟷ GET /api/v1/customers/{id}/legal-identity",
	"GET /api/v1/customers/contacts/{id} ⟷ GET /api/v1/customers/{id}/timeline",
	"GET /api/v1/customers/contacts/{id}/customers ⟷ GET /api/v1/customers/{id}/timeline/{entryId}",
	"GET /api/v1/products/categories/{id} ⟷ GET /api/v1/products/{id}/variants",
	"GET /api/v1/products/tax-categories/{id} ⟷ GET /api/v1/products/{id}/variants",
	"GET /api/v1/projects/milestones/{milestoneId} ⟷ GET /api/v1/projects/{id}/assignable-users",
	"GET /api/v1/projects/milestones/{milestoneId} ⟷ GET /api/v1/projects/{id}/billing-lines",
	"GET /api/v1/projects/milestones/{milestoneId} ⟷ GET /api/v1/projects/{id}/milestones",
	"GET /api/v1/projects/milestones/{milestoneId} ⟷ GET /api/v1/projects/{id}/roles",
	"GET /api/v1/projects/milestones/{milestoneId} ⟷ GET /api/v1/projects/{id}/tasks",
	"GET /api/v1/projects/milestones/{milestoneId} ⟷ GET /api/v1/projects/{id}/timeline",
	"GET /api/v1/projects/tasks/{taskId} ⟷ GET /api/v1/projects/{id}/assignable-users",
	"GET /api/v1/projects/tasks/{taskId} ⟷ GET /api/v1/projects/{id}/billing-lines",
	"GET /api/v1/projects/tasks/{taskId} ⟷ GET /api/v1/projects/{id}/milestones",
	"GET /api/v1/projects/tasks/{taskId} ⟷ GET /api/v1/projects/{id}/roles",
	"GET /api/v1/projects/tasks/{taskId} ⟷ GET /api/v1/projects/{id}/tasks",
	"GET /api/v1/projects/tasks/{taskId} ⟷ GET /api/v1/projects/{id}/timeline",
	"PUT /api/v1/customers/contacts/{id} ⟷ PUT /api/v1/customers/{id}/legal-identity",
	"PUT /api/v1/customers/contacts/{id} ⟷ PUT /api/v1/customers/{id}/type",
	"PUT /api/v1/projects/milestones/{milestoneId} ⟷ PUT /api/v1/projects/{id}/status",
	"PUT /api/v1/projects/milestones/{milestoneId}/position ⟷ PUT /api/v1/projects/{id}/billing-lines/{lineId}",
	"PUT /api/v1/projects/milestones/{milestoneId}/position ⟷ PUT /api/v1/projects/{id}/roles/{userId}",
	"PUT /api/v1/projects/tasks/{taskId} ⟷ PUT /api/v1/projects/{id}/status",
	"PUT /api/v1/projects/tasks/{taskId}/position ⟷ PUT /api/v1/projects/{id}/billing-lines/{lineId}",
	"PUT /api/v1/projects/tasks/{taskId}/position ⟷ PUT /api/v1/projects/{id}/roles/{userId}",
}

// TestServeMuxConflictsArePinned checks every pair of operations across every
// module against a real http.ServeMux and compares the set of pairs it
// refuses to mount together against KnownServeMuxConflicts. See the comment
// there for why these conflicts exist and who resolves them.
func TestServeMuxConflictsArePinned(t *testing.T) {
	var patterns []string
	for _, name := range Modules {
		doc, err := Load(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range operations(doc) {
			patterns = append(patterns, op.Method+" "+op.Path)
		}
	}

	var got []string
	for i := range patterns {
		for j := i + 1; j < len(patterns); j++ {
			if !serveMuxConflicts(patterns[i], patterns[j]) {
				continue
			}
			a, b := patterns[i], patterns[j]
			if a > b {
				a, b = b, a
			}
			got = append(got, a+" ⟷ "+b)
		}
	}
	sort.Strings(got)

	want := append([]string(nil), KnownServeMuxConflicts...)
	sort.Strings(want)

	if !slices.Equal(got, want) {
		t.Errorf("ServeMux conflicts drifted from KnownServeMuxConflicts:\ngot:  %v\nwant: %v", got, want)
	}
}

// serveMuxConflicts reports whether a fresh http.ServeMux refuses to
// register patterns a and b together.
func serveMuxConflicts(a, b string) (conflict bool) {
	defer func() {
		if recover() != nil {
			conflict = true
		}
	}()
	mux := http.NewServeMux()
	mux.HandleFunc(a, func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc(b, func(http.ResponseWriter, *http.Request) {})
	return false
}

func TestLintFlagsTheStructuralRules(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData([]byte(`
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /a:
    get:
      operationId: good
      x-vantigo-access: permission:customers:view
      responses: {"200": {description: ok, content: {application/json: {schema: {type: object}}}}}
    post:
      operationId: noBody
      x-vantigo-access: session
      responses: {"200": {description: ok}}
    put:
      operationId: badAccess
      x-vantigo-access: policy:Owner+Nobody
      responses: {"204": {description: ok}}
    delete:
      responses: {"204": {description: ok}}
    patch:
      operationId: redirects
      x-vantigo-access: anonymous
      responses: {"302": {description: found, headers: {Location: {schema: {type: string}}}}}
  /b:
    get:
      operationId: redirectsNowhere
      x-vantigo-access: anonymous
      responses: {"302": {description: found}}
    post:
      operationId: emptyOk
      x-vantigo-access: session
      responses: {"200": {description: ok, x-vantigo-empty-body: true}}
  /c:
    get:
      operationId: unsortedPermissions
      x-vantigo-access: permission:customers:view+customers:edit
      responses: {"204": {description: ok}}
    post:
      operationId: duplicatePolicy
      x-vantigo-access: policy:Owner+Owner
      responses: {"204": {description: ok}}
    put:
      operationId: sortedPolicies
      x-vantigo-access: policy:Owner+OwnerManagement
      responses: {"204": {description: ok}}
    patch:
      operationId: sortedPermissions
      x-vantigo-access: permission:communications:conversations-reply+communications:conversations-view
      responses: {"204": {description: ok}}
`))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range Lint(doc) {
		got[p.OperationID] = p.Message
	}
	for _, id := range []string{"good", "redirects", "emptyOk", "sortedPolicies", "sortedPermissions"} {
		if _, bad := got[id]; bad {
			t.Errorf("%s flagged: %v", id, got[id])
		}
	}
	for _, id := range []string{"noBody", "badAccess", "redirectsNowhere", "", "unsortedPermissions", "duplicatePolicy"} {
		if _, ok := got[id]; !ok {
			t.Errorf("operation %q not flagged (got %v)", id, got)
		}
	}
}

var accessRule = regexp.MustCompile(`^(anonymous|session|scim|permission:[a-z]+:[a-z-]+(\+[a-z]+:[a-z-]+)*|policy:(ActiveAccount|SystemAdmin|Owner|OwnerManagement|Business|AuthorizationManagement)(\+(ActiveAccount|SystemAdmin|Owner|OwnerManagement|Business|AuthorizationManagement))*)$`)

func TestAccessRuleIsTheContractGrammar(t *testing.T) {
	if accessRule.String() != AccessRule.String() {
		t.Fatalf("AccessRule drifted from the grammar in the plan's Global Constraints")
	}
}

// numericFormats are the schema formats .NET's web JSON defaults
// (NumberHandling = AllowReadingFromString) can leave untyped in the 3.0
// downgrade — see internal/openapi/cmd/contract/normalize.go.
var numericFormats = map[string]bool{"int32": true, "int64": true, "float": true, "double": true}

// TestNumericSchemasAreTyped guards against the contract regressing to
// untyped numbers (which oapi-codegen turns into interface{}): every schema
// with a numeric `format` must carry `type: integer` or `type: number`.
// `contract normalize` is what fixes a violation.
func TestNumericSchemasAreTyped(t *testing.T) {
	walkContract(t, func(file, path string, node map[string]any) {
		if _, hasType := node["type"]; hasType {
			return
		}
		if format, _ := node["format"].(string); numericFormats[format] {
			t.Errorf("%s: %s has format %q but no type — run contract normalize", file, path, format)
		}
	})
}

// TestNumericSchemasHaveNoPattern guards against the numeric-string
// `pattern` the 3.0 downgrade added beside `format` surviving on a schema that
// is typed integer or number: `pattern` only constrains strings, so on a
// number it is dead weight that reads as if the wire carried digit strings.
// `contract normalize` is what fixes a violation.
func TestNumericSchemasHaveNoPattern(t *testing.T) {
	walkContract(t, func(file, path string, node map[string]any) {
		if typ, _ := node["type"].(string); typ != "integer" && typ != "number" {
			return
		}
		if pattern, ok := node["pattern"].(string); ok {
			t.Errorf("%s: %s is a %s with pattern %q — run contract normalize", file, path, node["type"], pattern)
		}
	})
}

// TestNullableRefsUseAllOf guards against the contract regressing to the
// one-element-oneOf spelling of a nullable reference, which generates a
// json.RawMessage union wrapper instead of a pointer. `contract normalize`
// is what fixes a violation.
func TestNullableRefsUseAllOf(t *testing.T) {
	walkContract(t, func(file, path string, node map[string]any) {
		nullable, _ := node["nullable"].(bool)
		oneOf, ok := node["oneOf"].([]any)
		if !nullable || !ok || len(oneOf) != 1 {
			return
		}
		elem, ok := oneOf[0].(map[string]any)
		if !ok {
			return
		}
		if _, ok := elem["$ref"]; ok && len(elem) == 1 {
			t.Errorf("%s: %s is a nullable single-$ref oneOf — use allOf instead (run contract normalize)", file, path)
		}
	})
}

// walkContract decodes common.yaml and every module file from the embedded
// contract and calls visit on every JSON object node, including nested
// inline schemas (properties, items, allOf, request/response bodies, ...).
func walkContract(t *testing.T, visit func(file, path string, node map[string]any)) {
	t.Helper()
	for _, name := range append([]string{"common"}, Modules...) {
		data, err := fs.ReadFile(Files(), name+".yaml")
		if err != nil {
			t.Fatal(err)
		}
		j, err := yaml.YAMLToJSON(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc any
		if err := json.Unmarshal(j, &doc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var walk func(string, any)
		walk = func(path string, v any) {
			switch t := v.(type) {
			case map[string]any:
				visit(name+".yaml", path, t)
				for k, child := range t {
					walk(path+"/"+k, child)
				}
			case []any:
				for i, child := range t {
					walk(fmt.Sprintf("%s[%d]", path, i), child)
				}
			}
		}
		walk("", doc)
	}
}
