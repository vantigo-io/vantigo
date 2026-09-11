package openapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
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

// knownServeMuxConflicts pins the operation pairs Go's stdlib http.ServeMux
// (1.22+) refuses to register together: it has no notion of route
// constraints — unlike the .NET host, which separates these with
// {id:int}/{id:guid} — and no literal-before-parameter precedence across
// differing segments, so it can't tell that an id will never literally equal
// "contacts" or "variants". These are genuine ambiguities from ServeMux's
// point of view, not a contract defect; sub-project 3 mounts the generated
// handlers on a precedence-aware router (via StdHTTPServerOptions.BaseRouter)
// and must route each pair correctly. A pair appearing or disappearing here
// means the contract's route shape changed and sub-project 3 needs to know.
var knownServeMuxConflicts = []string{
	"DELETE /api/v1/customers/contacts/{id} ⟷ DELETE /api/v1/customers/{id}/legal-identity",
	"GET /api/v1/customers/contacts/{id} ⟷ GET /api/v1/customers/{id}/contacts",
	"GET /api/v1/customers/contacts/{id} ⟷ GET /api/v1/customers/{id}/legal-identity",
	"GET /api/v1/customers/contacts/{id} ⟷ GET /api/v1/customers/{id}/timeline",
	"GET /api/v1/customers/contacts/{id}/customers ⟷ GET /api/v1/customers/{id}/timeline/{entryId}",
	"GET /api/v1/products/categories/{id} ⟷ GET /api/v1/products/{id}/variants",
	"GET /api/v1/products/tax-categories/{id} ⟷ GET /api/v1/products/{id}/variants",
	"PUT /api/v1/customers/contacts/{id} ⟷ PUT /api/v1/customers/{id}/legal-identity",
}

// TestServeMuxConflictsArePinned checks every pair of operations across every
// module against a real http.ServeMux and compares the set of pairs it
// refuses to mount together against knownServeMuxConflicts. See the comment
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

	want := append([]string(nil), knownServeMuxConflicts...)
	sort.Strings(want)

	if !slices.Equal(got, want) {
		t.Errorf("ServeMux conflicts drifted from knownServeMuxConflicts:\ngot:  %v\nwant: %v", got, want)
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
`))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range Lint(doc) {
		got[p.OperationID] = p.Message
	}
	for _, id := range []string{"good", "redirects", "emptyOk"} {
		if _, bad := got[id]; bad {
			t.Errorf("%s flagged: %v", id, got[id])
		}
	}
	for _, id := range []string{"noBody", "badAccess", "redirectsNowhere", ""} {
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
