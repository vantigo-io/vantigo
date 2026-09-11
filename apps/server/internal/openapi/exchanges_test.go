package openapi

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

const corpusDir = "../../../../openapi/testdata/exchanges"

// TestRecordedExchangesMatchTheContract validates every exchange recorded
// from the .NET suites against the module that owns its path, and checks
// every module against the structural lint. Every operation must lint
// clean and every recorded exchange must validate.
func TestRecordedExchangesMatchTheContract(t *testing.T) {
	ctx := context.Background()
	failing := map[string][]string{} // operationId (or path) -> reasons

	for _, name := range Modules {
		doc, err := Load(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range Lint(doc) {
			failing[p.OperationID] = append(failing[p.OperationID], p.Message)
		}
		for _, ex := range readCorpus(t, filepath.Join(corpusDir, name+".jsonl")) {
			if id, err := Validate(ctx, doc, ex); err != nil {
				failing[id] = append(failing[id], ex.Method+" "+ex.Path+" "+err.Error())
			}
		}
	}

	var ids []string
	for id := range failing {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		t.Errorf("%s does not match the contract:\n  %s", id, strings.Join(failing[id], "\n  "))
	}
}

const validateSpec = `
openapi: 3.0.3
info: {title: t, version: "1"}
servers: [{url: /}]
paths:
  /api/v1/things:
    post:
      operationId: postThings
      x-vantigo-access: session
      parameters:
        - {in: header, name: Idempotency-Key, required: true, schema: {type: string}}
      requestBody:
        required: true
        content:
          application/json:
            schema: {type: object, required: [name], properties: {name: {type: string}}}
      responses:
        "201":
          description: created
          content:
            application/json:
              schema: {type: object, required: [id], properties: {id: {type: integer}}}
        "400":
          description: invalid
          content:
            application/problem+json:
              schema: {type: object, required: [title], properties: {title: {type: string}}}
  /api/v1/scim-things:
    put:
      operationId: putScimThings
      x-vantigo-access: scim
      requestBody:
        required: true
        content:
          application/scim+json:
            schema: {type: object, required: [userName], properties: {userName: {type: string}}}
      responses:
        "200":
          description: ok
          content:
            application/scim+json:
              schema: {type: object, required: [id], properties: {id: {type: string}}}
`

func TestValidate(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData([]byte(validateSpec))
	if err != nil {
		t.Fatal(err)
	}
	str := func(s string) *string { return &s }
	appJSON, problem, scimJSON := str("application/json"), str("application/problem+json"), str("application/scim+json")
	post := func(body string, status int, contentType *string, response string) Exchange {
		return Exchange{Method: "POST", Path: "/api/v1/things", RequestContentType: appJSON, RequestBody: str(body),
			Status: status, ResponseContentType: contentType, ResponseBody: str(response)}
	}
	put := func(body string, status int, response string) Exchange {
		return Exchange{Method: "PUT", Path: "/api/v1/scim-things", RequestContentType: scimJSON, RequestBody: str(body),
			Status: status, ResponseContentType: scimJSON, ResponseBody: str(response)}
	}
	cases := []struct {
		name string
		ex   Exchange
		ok   bool
	}{
		// postThings requires a header the recorder cannot carry (it keeps no
		// request headers), so its cases also prove Validate skips header parameters.
		{"valid exchange", post(`{"name":"a"}`, 201, appJSON, `{"id":1}`), true},
		{"undocumented status", post(`{"name":"a"}`, 409, problem, `{"title":"conflict"}`), false},
		{"response off contract", post(`{"name":"a"}`, 201, appJSON, `{"id":"x"}`), false},
		{"invalid request the server rejected", post(`{}`, 400, problem, `{"title":"bad"}`), true},
		{"invalid request the server accepted", post(`{}`, 201, appJSON, `{"id":1}`), false},
		{"rejection off contract", post(`{}`, 400, problem, `{}`), false},
		{"no matching operation", Exchange{Method: "GET", Path: "/api/v1/nothing", Status: 404}, false},
		{"scim+json request and response validate", put(`{"userName":"a"}`, 200, `{"id":"1"}`), true},
		{"scim+json response off schema", put(`{"userName":"a"}`, 200, `{"id":1}`), false},
	}
	for _, c := range cases {
		if _, err := Validate(context.Background(), doc, c.ex); (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok = %v", c.name, err, c.ok)
		}
	}
}

func readCorpus(t *testing.T, path string) []Exchange {
	t.Helper()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []Exchange
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<22)
	for scanner.Scan() {
		var ex Exchange
		if err := json.Unmarshal(scanner.Bytes(), &ex); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out = append(out, ex)
	}
	return out
}
