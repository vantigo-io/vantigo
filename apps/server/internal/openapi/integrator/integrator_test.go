package integrator_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/openapi/integrator"
)

var update = flag.Bool("update", false, "rewrite the golden documents under testdata")

// catalog is the small contract's: widgets:owner is the Owner tier, so no key
// holds it, and identity:manage is never a key's whatever it says.
var catalog = []contracts.Permission{
	{Key: "widgets:view", Delegable: true},
	{Key: "widgets:edit", Delegable: true},
	{Key: "widgets:owner", Delegable: false},
	{Key: "identity:manage", Delegable: true},
}

func load(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile(filepath.Join("testdata", "contract.yaml"))
	if err != nil {
		t.Fatalf("load the contract: %v", err)
	}
	return doc
}

// TestBuildGolden pins the document for no filter, a narrowed key and a
// read-only key: which operations stay, security, the added answers, the
// permission in each description and the stripped extensions.
func TestBuildGolden(t *testing.T) {
	cases := []struct {
		name       string
		filter     *integrator.Filter
		keepsDraft bool
	}{
		{"unfiltered", nil, true},
		{"narrowed", &integrator.Filter{Permissions: []string{"widgets:view"}}, false},
		{"read-only", &integrator.Filter{Permissions: []string{"widgets:view", "widgets:edit"}, ReadOnly: true}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			contract := load(t)
			before, _ := json.Marshal(contract)
			doc, err := integrator.Build(contract, catalog, c.filter, integrator.Options{})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if after, _ := json.Marshal(contract); !bytes.Equal(before, after) {
				t.Error("Build changed the contract it was given")
			}
			if err := doc.Validate(context.Background()); err != nil {
				t.Errorf("the document does not validate: %v", err)
			}
			got, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			if strings.Contains(string(got), "x-vantigo-") {
				t.Error("the document still carries an x-vantigo-* extension")
			}
			schemas := doc.Components.Schemas
			if schemas["Secret"] != nil {
				t.Error("the document keeps Secret, which only a never operation uses")
			}
			if _, kept := schemas["WidgetDraft"]; kept != c.keepsDraft || (schemas["WidgetName"] != nil) != c.keepsDraft {
				t.Errorf("WidgetDraft and WidgetName kept = %v, want %v: they belong to postWidgets alone", kept, c.keepsDraft)
			}
			if schemas["Widget"] == nil || schemas["AuthErrorResponse"] == nil {
				t.Error("the document lost a schema a kept operation uses")
			}
			golden := filepath.Join("testdata", c.name+".json.golden")
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test ./internal/openapi/integrator -update)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s is out of date; review the change and run go test ./internal/openapi/integrator -update", golden)
			}
		})
	}
}

func TestBuildRefusesAnInvalidRule(t *testing.T) {
	contract := load(t)
	contract.Paths.Value("/api/v1/widgets").Get.Extensions["x-vantigo-access"] = "permission:"
	if _, err := integrator.Build(contract, catalog, nil, integrator.Options{}); err == nil {
		t.Error("Build accepted an invalid access rule")
	}
}
