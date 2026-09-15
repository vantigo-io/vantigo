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
		corpus := readCorpus(t, filepath.Join(corpusDir, name+".jsonl"))
		if len(corpus) == 0 {
			t.Errorf("%s: the recorded corpus is empty — it is frozen evidence from the retired .NET suites and cannot be re-cut; restore it from git history", name)
		}
		for _, ex := range corpus {
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
    get:
      operationId: getThings
      x-vantigo-access: session
      parameters:
        - {in: query, name: page, schema: {type: integer}}
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {type: array, items: {type: object}}
        "400":
          description: invalid
          content:
            application/problem+json:
              schema: {type: object, required: [title], properties: {title: {type: string}}}
  /api/v1/things/{id}/archive:
    post:
      operationId: postThingsByIdArchive
      x-vantigo-access: session
      parameters:
        - {in: path, name: id, required: true, schema: {type: integer}}
      responses:
        "204":
          description: archived
        "404":
          description: not found
  /api/v1/things/{id}/file:
    get:
      operationId: getThingsByIdFile
      x-vantigo-access: session
      parameters:
        - {in: path, name: id, required: true, schema: {type: integer}}
      responses:
        "200":
          description: the file
          content:
            application/pdf:
              schema: {type: string, format: binary}
            image/*:
              schema: {type: string, format: binary}
    put:
      operationId: putThingsByIdFile
      x-vantigo-access: session
      parameters:
        - {in: path, name: id, required: true, schema: {type: integer}}
      requestBody:
        required: true
        content:
          multipart/form-data:
            schema: {type: object, properties: {file: {type: string, format: binary}}}
      responses:
        "204":
          description: stored
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
	list := func(query string, status int, contentType *string, response string) Exchange {
		return Exchange{Method: "GET", Path: "/api/v1/things", Query: query, Status: status, ResponseContentType: contentType, ResponseBody: str(response)}
	}
	archive := func(body *string, status int, response *string) Exchange {
		ex := Exchange{Method: "POST", Path: "/api/v1/things/7/archive", RequestBody: body, Status: status, ResponseBody: response}
		if body != nil {
			ex.RequestContentType = appJSON
		}
		if response != nil {
			ex.ResponseContentType = appJSON
		}
		return ex
	}
	// download records a binary response: the recorder keeps no body for a
	// non-text content type, only the content type itself.
	download := func(contentType string) Exchange {
		return Exchange{Method: "GET", Path: "/api/v1/things/7/file", Status: 200, ResponseContentType: str(contentType)}
	}
	// upload records a binary request the same way.
	upload := func(contentType string, status int) Exchange {
		ex := Exchange{Method: "PUT", Path: "/api/v1/things/7/file", RequestContentType: str(contentType), Status: status}
		if status == 400 {
			ex.ResponseContentType, ex.ResponseBody = problem, str(`{"title":"bad"}`)
		}
		return ex
	}
	cases := []struct {
		name string
		ex   Exchange
		ok   bool
		// want is the failure key Validate returns, when the case pins it.
		want string
	}{
		// postThings requires a header the recorder cannot carry (it keeps no
		// request headers), so its cases also prove Validate skips header parameters.
		{"valid exchange", post(`{"name":"a"}`, 201, appJSON, `{"id":1}`), true, ""},
		{"undocumented status", post(`{"name":"a"}`, 409, problem, `{"title":"conflict"}`), false, ""},
		{"response off contract", post(`{"name":"a"}`, 201, appJSON, `{"id":"x"}`), false, ""},
		{"invalid request the server rejected", post(`{}`, 400, problem, `{"title":"bad"}`), true, ""},
		{"invalid request the server accepted", post(`{}`, 201, appJSON, `{"id":1}`), false, ""},
		{"rejection off contract", post(`{}`, 400, problem, `{}`), false, ""},
		{"no matching operation", Exchange{Method: "GET", Path: "/api/v1/nothing", Status: 404}, false, "GET /api/v1/nothing"},
		{"scim+json request and response validate", put(`{"userName":"a"}`, 200, `{"id":"1"}`), true, ""},
		{"scim+json response off schema", put(`{"userName":"a"}`, 200, `{"id":1}`), false, ""},

		{"declared query key", list("?page=2", 200, appJSON, `[]`), true, ""},
		{"no query string", list("", 200, appJSON, `[]`), true, ""},
		{"undeclared query key", list("?Page=2", 200, appJSON, `[]`), false, "getThings"},
		{"undeclared query key beside a declared one", list("?page=2&sort=name", 200, appJSON, `[]`), false, ""},
		{"undeclared query key the server rejected", list("?Page=x", 400, problem, `{"title":"bad"}`), false, ""},

		{"binary response of a documented type", download("application/pdf"), true, ""},
		{"binary response with parameters", download("application/pdf; charset=binary"), true, ""},
		{"binary response matching a type/* entry", download("image/png"), true, ""},
		{"binary response of an undocumented type", download("application/zip"), false, "getThingsByIdFile"},
		{"binary request of the documented type", upload("multipart/form-data; boundary=abc", 204), true, ""},
		{"binary request of an undocumented type the server accepted", upload("application/octet-stream", 204), false, ""},
		{"binary request of an undocumented type the server rejected", upload("application/octet-stream", 400), true, ""},

		{"no body where none is documented", archive(nil, 204, nil), true, ""},
		{"response body where none is documented", archive(nil, 404, str(`{"error":"gone"}`)), false, "postThingsByIdArchive"},
		{"request body to an operation without a requestBody", archive(str(`{"reason":"x"}`), 204, nil), false, "postThingsByIdArchive"},
		{"empty request body to an operation without a requestBody", archive(str(""), 204, nil), true, ""},
	}
	for _, c := range cases {
		id, err := Validate(context.Background(), doc, c.ex)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok = %v", c.name, err, c.ok)
		}
		if c.want != "" && id != c.want {
			t.Errorf("%s: key = %q, want %q", c.name, id, c.want)
		}
	}
}

// TestValidateNamesTheUndeclaredQueryKey checks that the error says which key
// the contract lacks, so a failing corpus line points at the parameter.
func TestValidateNamesTheUndeclaredQueryKey(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData([]byte(validateSpec))
	if err != nil {
		t.Fatal(err)
	}
	body := "[]"
	_, err = Validate(context.Background(), doc, Exchange{Method: "GET", Path: "/api/v1/things", Query: "?page=1&PageSize=5", Status: 200,
		ResponseContentType: &[]string{"application/json"}[0], ResponseBody: &body})
	if err == nil || !strings.Contains(err.Error(), `"PageSize"`) {
		t.Fatalf("err = %v, want it to name the undeclared key \"PageSize\"", err)
	}
}

func readCorpus(t *testing.T, path string) []Exchange {
	t.Helper()
	f, err := os.Open(path)
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
