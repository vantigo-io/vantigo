package openapi

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
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
`))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range Lint(doc) {
		got[p.OperationID] = p.Message
	}
	for _, id := range []string{"good", "redirects"} {
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
