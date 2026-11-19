package integrator_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/openapi/integrator"
)

// merging is a contract whose creating POSTs document a 409 and a 422 of their
// own, in every shape the composed contract has: a problem+json body, a JSON
// body, a shared component, and none.
const merging = `
openapi: 3.0.3
info: {title: Vantigo API, version: "1"}
paths:
  /api/v1/widgets:
    post:
      operationId: postWidgets
      x-vantigo-access: permission:widgets:edit
      responses:
        "201": {description: Created}
        "409":
          description: Conflict — widget_locked.
          content:
            application/problem+json:
              schema: {$ref: '#/components/schemas/Problem'}
        "422":
          description: Unprocessable — widget_invalid.
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Problem'}
  /api/v1/widgets/plain:
    post:
      operationId: postWidgetsPlain
      x-vantigo-access: permission:widgets:edit
      responses:
        "201": {description: Created}
  /api/v1/widgets/shared:
    post:
      operationId: postWidgetsShared
      x-vantigo-access: permission:widgets:edit
      responses:
        "201": {description: Created}
        "409": {$ref: '#/components/responses/Locked'}
  /api/v1/widgets/other:
    post:
      operationId: postWidgetsOther
      x-vantigo-access: permission:widgets:edit
      responses:
        "201": {description: Created}
        "409": {$ref: '#/components/responses/Locked'}
  /api/v1/widgets/auth:
    post:
      operationId: postWidgetsAuth
      x-vantigo-access: permission:widgets:edit
      responses:
        "201": {description: Created}
        "422":
          description: Unprocessable.
          content:
            application/json:
              schema: {$ref: '#/components/schemas/AuthErrorResponse'}
  /api/v1/widgets/comms:
    post:
      operationId: postWidgetsComms
      x-vantigo-access: permission:widgets:edit
      responses:
        "201": {description: Created}
        "409":
          description: Conflict — widget_taken.
          headers:
            retry-after: {schema: {type: integer}, description: Their own.}
          content:
            application/json:
              schema: {$ref: '#/components/schemas/CommsError'}
components:
  responses:
    Locked:
      description: Conflict — widget_locked.
      content:
        application/problem+json:
          schema: {$ref: '#/components/schemas/Problem'}
  schemas:
    Problem:
      type: object
      properties:
        code: {type: string}
    AuthErrorResponse:
      type: object
      properties:
        error: {type: object}
    CommsError:
      type: object
      properties:
        error:
          type: object
          properties:
            code: {type: string}
            message: {type: string}
`

func loadMerging(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData([]byte(merging))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// everyBut answers true for every operation but skip, recording what
// it was asked.
func everyBut(skip string, asked map[string]bool) integrator.Options {
	return integrator.Options{Idempotency: func(method string, op *openapi3.Operation, cat []contracts.Permission) bool {
		if _, ok := op.Extensions["x-vantigo-access"]; !ok || method != http.MethodPost || len(cat) != len(catalog) {
			panic("the predicate was not given the operation as the contract has it")
		}
		asked[op.OperationID] = true
		return op.OperationID != skip
	}}
}

func idempotencyParameter(op *openapi3.Operation) *openapi3.Parameter {
	for _, p := range op.Parameters {
		if p.Value != nil && p.Value.In == openapi3.ParameterInHeader && p.Value.Name == integrator.IdempotencyHeader {
			return p.Value
		}
	}
	return nil
}

// Build's Idempotency option (idempotency design I25): the header and the two
// answers on every operation the predicate admits, merged into what the
// operation documents and never replacing it; nothing on another, and nothing
// at all without the option.
func TestBuild_TheIdempotencyOption(t *testing.T) {
	contract := loadMerging(t)
	before, _ := json.Marshal(contract)
	asked := map[string]bool{}
	doc, err := integrator.Build(contract, catalog, nil, everyBut("postWidgetsOther", asked))
	if err != nil {
		t.Fatal(err)
	}
	if after, _ := json.Marshal(contract); !bytes.Equal(before, after) {
		t.Error("Build changed the contract it was given")
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Errorf("the document does not validate: %v", err)
	}
	if len(asked) != 6 {
		t.Errorf("the predicate was asked of %v, want every kept operation", asked)
	}
	op := func(path string) *openapi3.Operation { return doc.Paths.Value(path).Post }

	for _, path := range []string{"/api/v1/widgets", "/api/v1/widgets/plain", "/api/v1/widgets/shared", "/api/v1/widgets/auth"} {
		p := idempotencyParameter(op(path))
		if p == nil {
			t.Errorf("%s has no Idempotency-Key", path)
			continue
		}
		if p.Required || p.Schema == nil || p.Schema.Value.MinLength != 1 || p.Schema.Value.MaxLength == nil || *p.Schema.Value.MaxLength != 255 ||
			!strings.Contains(p.Description, integrator.RetryingURL) || !strings.Contains(p.Description, "one key per intended action") {
			t.Errorf("%s: the parameter %+v", path, p)
		}
	}
	if idempotencyParameter(op("/api/v1/widgets/other")) != nil {
		t.Error("postWidgetsOther, which the predicate leaves out, has the header")
	}

	// Merged: the operation's own text first, its own content kept, the common
	// error body beside its JSON schema.
	conflict := op("/api/v1/widgets").Responses.Value("409").Value
	if d := *conflict.Description; !strings.HasPrefix(d, "Conflict — widget_locked. With an Idempotency-Key, ") ||
		!strings.Contains(d, "idempotency_in_progress") || !strings.Contains(d, "idempotency_outcome_unknown") {
		t.Errorf("409: %q", d)
	}
	if s := conflict.Content["application/problem+json"]; s == nil || s.Schema.Ref != "#/components/schemas/Problem" {
		t.Errorf("409 lost its problem+json: %+v", conflict.Content)
	}
	if s := conflict.Content["application/json"]; s == nil || s.Schema.Ref != "#/components/schemas/AuthErrorResponse" {
		t.Errorf("409's JSON: %+v", conflict.Content["application/json"])
	}
	unprocessable := op("/api/v1/widgets").Responses.Value("422").Value
	if d := *unprocessable.Description; !strings.HasPrefix(d, "Unprocessable — widget_invalid. With an Idempotency-Key, ") ||
		!strings.Contains(d, "idempotency_key_reused") {
		t.Errorf("422: %q", d)
	}
	merged := unprocessable.Content["application/json"].Schema.Value
	if len(merged.OneOf) != 0 || len(merged.AnyOf) != 2 || merged.AnyOf[0].Ref != "#/components/schemas/Problem" || merged.AnyOf[1].Ref != "#/components/schemas/AuthErrorResponse" {
		t.Errorf("422's JSON schema: %+v", merged)
	}
	// A merged 409 declares Retry-After, and keeps a header of its own.
	if conflict.Headers["Retry-After"] == nil {
		t.Errorf("the merged 409 has no Retry-After: %+v", conflict.Headers)
	}
	comms := op("/api/v1/widgets/comms").Responses.Value("409").Value
	if s := comms.Content["application/json"].Schema; s.Ref != "#/components/schemas/CommsError" {
		t.Errorf("a schema that already takes the error body was changed: %+v", s)
	}
	if len(comms.Headers) != 1 || comms.Headers["retry-after"] == nil || comms.Headers["retry-after"].Value.Description != "Their own." {
		t.Errorf("postWidgetsComms's headers: %+v", comms.Headers)
	}
	// Already the common body: not offered twice.
	auth := op("/api/v1/widgets/auth").Responses.Value("422").Value
	if s := auth.Content["application/json"].Schema; s.Ref != "#/components/schemas/AuthErrorResponse" || !strings.HasPrefix(*auth.Description, "Unprocessable. With") {
		t.Errorf("postWidgetsAuth's 422: %q %+v", *auth.Description, s)
	}

	// None of its own: the answers of their own.
	plain := op("/api/v1/widgets/plain").Responses
	if plain.Value("409").Ref != "#/components/responses/IdempotencyConflict" || plain.Value("422").Ref != "#/components/responses/IdempotencyKeyReused" {
		t.Errorf("postWidgetsPlain: %+v %+v", plain.Value("409"), plain.Value("422"))
	}
	if r := doc.Components.Responses["IdempotencyConflict"]; r == nil || r.Value.Headers["Retry-After"] == nil {
		t.Error("IdempotencyConflict has no Retry-After")
	}

	// A shared component is copied, never changed: the operation the predicate
	// leaves out still refers to it as it was.
	shared := op("/api/v1/widgets/shared").Responses.Value("409")
	if shared.Ref != "" || !strings.Contains(*shared.Value.Description, "idempotency_outcome_unknown") {
		t.Errorf("postWidgetsShared's 409: %+v", shared)
	}
	other := op("/api/v1/widgets/other").Responses.Value("409")
	if other.Ref != "#/components/responses/Locked" || strings.Contains(*doc.Components.Responses["Locked"].Value.Description, "Idempotency") {
		t.Errorf("postWidgetsOther's 409, or the Locked component, changed: %+v", other)
	}

	// No option: no header, no answer, no component.
	plainDoc, err := integrator.Build(loadMerging(t), catalog, nil, integrator.Options{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(plainDoc)
	if bytes.Contains(body, []byte("Idempotency")) {
		t.Error("the document without the option mentions the Idempotency-Key")
	}
}
