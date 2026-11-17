package openapi

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

var updateAPIMarks = flag.Bool("update", false, "rewrite testdata/api-marks.txt from the contracts")

// apiMarksGolden pins every operation marked x-vantigo-api: never and every
// POST marked read (API access design K5, K6), so a change to what an API key
// may call is visible in review.
const apiMarksGolden = "testdata/api-marks.txt"

func TestLintChecksTheAPIExtension(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData([]byte(`
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /a:
    get:
      operationId: unmarkedPermission
      summary: S
      x-vantigo-access: permission:customers:view
      responses: {"204": {description: ok}}
    post:
      operationId: readSearch
      summary: S
      x-vantigo-access: permission:customers:view
      x-vantigo-api: read
      responses: {"204": {description: ok}}
    put:
      operationId: readOnPut
      summary: S
      x-vantigo-access: permission:customers:view
      x-vantigo-api: read
      responses: {"204": {description: ok}}
    delete:
      operationId: otherValue
      summary: S
      x-vantigo-access: permission:customers:view
      x-vantigo-api: sometimes
      responses: {"204": {description: ok}}
  /b:
    get:
      operationId: unmarkedAnonymous
      summary: S
      x-vantigo-access: anonymous
      responses: {"204": {description: ok}}
    post:
      operationId: neverAnonymous
      summary: S
      x-vantigo-access: anonymous
      x-vantigo-api: never
      responses: {"204": {description: ok}}
    put:
      operationId: allowedOnPolicy
      summary: S
      x-vantigo-access: policy:Owner
      x-vantigo-api: allowed
      responses: {"204": {description: ok}}
    patch:
      operationId: neverOnSession
      summary: S
      x-vantigo-access: session
      x-vantigo-api: never
      responses: {"204": {description: ok}}
    delete:
      operationId: allowedOnDelete
      summary: S
      x-vantigo-access: permission:customers:view
      x-vantigo-api: allowed
      responses: {"204": {description: ok}}
`))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range Lint(doc) {
		got[p.OperationID] = p.Message
	}
	for _, id := range []string{"unmarkedPermission", "readSearch", "neverAnonymous", "neverOnSession", "allowedOnDelete"} {
		if msg, bad := got[id]; bad {
			t.Errorf("%s flagged: %v", id, msg)
		}
	}
	for id, want := range map[string]string{
		"readOnPut":         "read is only for a POST",
		"otherValue":        "must be one of allowed, read, never",
		"unmarkedAnonymous": "is required on an anonymous operation",
		"allowedOnPolicy":   "a key meets only permission and anonymous rules",
	} {
		if msg := got[id]; !strings.Contains(msg, want) {
			t.Errorf("%s: %q, want %q", id, msg, want)
		}
	}
}

// TestAPIMarks holds the modules in APIMarkedModules to an explicit
// x-vantigo-api on every operation, and pins the list of operations no key may
// call and of POSTs a read-only key may (go test -run TestAPIMarks -update
// rewrites it).
func TestAPIMarks(t *testing.T) {
	var lines []string
	for _, name := range Modules {
		doc, err := Load(t.Context(), name)
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range operations(doc) {
			mode, present := op.Op.Extensions[APIExtension].(string)
			if !present && slices.Contains(APIMarkedModules, name) {
				t.Errorf("%s: %s %s (%s) has no %s; every operation of %s must say whether a key may call it", name, strings.ToUpper(op.Method), op.Path, op.OperationID, APIExtension, name)
			}
			if mode == "never" || mode == "read" {
				lines = append(lines, name+" "+mode+" "+op.OperationID)
			}
		}
	}
	slices.Sort(lines)
	got := strings.Join(lines, "\n") + "\n"
	path := filepath.FromSlash(apiMarksGolden)
	if *updateAPIMarks {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/openapi -run TestAPIMarks -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s is out of date: the operations an API key may not call, or may call read-only, changed. Review the change and run go test ./internal/openapi -run TestAPIMarks -update", apiMarksGolden)
	}
}
