package module

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// TestParseIdempotencyKey is I2: one value, trimmed, a surrounding pair of
// quotes removed, then 1 to 255 printable ASCII characters.
func TestParseIdempotencyKey(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		want   string
		ok     bool
	}{
		{"plain", []string{"8e03978e-40d5-43e8-bc93-6894a57f9324"}, "8e03978e-40d5-43e8-bc93-6894a57f9324", true},
		{"trimmed", []string{"  abc \t"}, "abc", true},
		{"the draft's String form", []string{`"abc"`}, "abc", true},
		{"spaces inside", []string{"a b"}, "a b", true},
		{"255 characters", []string{strings.Repeat("k", 255)}, strings.Repeat("k", 255), true},
		{"256 characters", []string{strings.Repeat("k", 256)}, "", false},
		{"empty", []string{""}, "", false},
		{"blank", []string{"   "}, "", false},
		{"empty quotes", []string{`""`}, "", false},
		{"one quote", []string{`"`}, `"`, true},
		{"a control character", []string{"a\x01b"}, "", false},
		{"DEL", []string{"a\x7fb"}, "", false},
		{"not ASCII", []string{"nøkkel"}, "", false},
		{"two headers", []string{"a", "b"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseIdempotencyKey(tc.values)
			if got != tc.want || ok != tc.ok {
				t.Errorf("parseIdempotencyKey(%q) = %q, %v; want %q, %v", tc.values, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func fingerprintOf(t *testing.T, method, target, contentType string, pathValues map[string]string, body string) []byte {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	var params []string
	for _, name := range []string{"id", "lineId"} {
		if v, ok := pathValues[name]; ok {
			req.SetPathValue(name, v)
			params = append(params, name)
		}
	}
	return idempotencyFingerprint(req, params, []byte(body))
}

// TestIdempotencyFingerprint is I24: the same request in another key order,
// spacing or query order is the same fingerprint; another value, path
// parameter, header or method is not.
func TestIdempotencyFingerprint(t *testing.T) {
	base := fingerprintOf(t, http.MethodPost, "/x?b=2&a=1&a=0", "application/json", map[string]string{"id": "7"}, `{"name":"A","lines":[{"q":1,"p":2.50}],"n":null}`)
	same := []struct {
		name string
		fp   []byte
	}{
		{"keys reordered and spaced", fingerprintOf(t, http.MethodPost, "/x?b=2&a=1&a=0", "application/json", map[string]string{"id": "7"}, " {\n \"n\": null, \"lines\": [ {\"p\":2.50, \"q\":1} ], \"name\": \"A\" }\n")},
		{"query names reordered", fingerprintOf(t, http.MethodPost, "/x?a=1&b=2&a=0", "application/json", map[string]string{"id": "7"}, `{"name":"A","lines":[{"q":1,"p":2.50}],"n":null}`)},
		{"utf-8 named as the charset", fingerprintOf(t, http.MethodPost, "/x?b=2&a=1&a=0", "application/json; charset=UTF-8", map[string]string{"id": "7"}, `{"name":"A","lines":[{"q":1,"p":2.50}],"n":null}`)},
		{"the media type's case", fingerprintOf(t, http.MethodPost, "/x?b=2&a=1&a=0", "Application/JSON", map[string]string{"id": "7"}, `{"name":"A","lines":[{"q":1,"p":2.50}],"n":null}`)},
		{"a unicode escape", fingerprintOf(t, http.MethodPost, "/x?b=2&a=1&a=0", "application/json", map[string]string{"id": "7"}, `{"name":"A","lines":[{"q":1,"p":2.50}],"n":null}`)},
	}
	for _, s := range same {
		if !bytes.Equal(s.fp, base) {
			t.Errorf("%s: another fingerprint", s.name)
		}
	}
	different := []struct {
		name string
		fp   []byte
	}{
		{"another value", fingerprintOf(t, http.MethodPost, "/x?b=2&a=1&a=0", "application/json", map[string]string{"id": "7"}, `{"name":"B","lines":[{"q":1,"p":2.50}],"n":null}`)},
		{"a number written otherwise", fingerprintOf(t, http.MethodPost, "/x?b=2&a=1&a=0", "application/json", map[string]string{"id": "7"}, `{"name":"A","lines":[{"q":1,"p":2.5}],"n":null}`)},
		{"array order", fingerprintOf(t, http.MethodPost, "/x?b=2&a=1&a=0", "application/json", map[string]string{"id": "7"}, `{"name":"A","lines":[{"p":2.50,"q":1}],"n":null,"x":[]}`)},
		{"values of one name reordered", fingerprintOf(t, http.MethodPost, "/x?b=2&a=0&a=1", "application/json", map[string]string{"id": "7"}, `{"name":"A","lines":[{"q":1,"p":2.50}],"n":null}`)},
		{"another path parameter", fingerprintOf(t, http.MethodPost, "/x?b=2&a=1&a=0", "application/json", map[string]string{"id": "8"}, `{"name":"A","lines":[{"q":1,"p":2.50}],"n":null}`)},
		{"another content type", fingerprintOf(t, http.MethodPost, "/x?b=2&a=1&a=0", "application/merge-patch+json", map[string]string{"id": "7"}, `{"name":"A","lines":[{"q":1,"p":2.50}],"n":null}`)},
		{"another method", fingerprintOf(t, http.MethodPut, "/x?b=2&a=1&a=0", "application/json", map[string]string{"id": "7"}, `{"name":"A","lines":[{"q":1,"p":2.50}],"n":null}`)},
	}
	for _, d := range different {
		if bytes.Equal(d.fp, base) {
			t.Errorf("%s: the same fingerprint", d.name)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	withMatch := httptest.NewRequest(http.MethodPost, "/x", nil)
	withMatch.Header.Set("If-Match", `"v1"`)
	if bytes.Equal(idempotencyFingerprint(req, nil, nil), idempotencyFingerprint(withMatch, nil, nil)) {
		t.Error("If-Match does not change the fingerprint")
	}
	if !bytes.Equal(fingerprintOf(t, http.MethodPost, "/x", "text/plain", nil, "a  b"), fingerprintOf(t, http.MethodPost, "/x", "text/plain", nil, "a  b")) ||
		bytes.Equal(fingerprintOf(t, http.MethodPost, "/x", "text/plain", nil, "a  b"), fingerprintOf(t, http.MethodPost, "/x", "text/plain", nil, "a b")) {
		t.Error("a body that is not JSON is not fingerprinted by its bytes")
	}
	if bytes.Equal(fingerprintOf(t, http.MethodPost, "/x", "", nil, `{"a":1} {"a":1}`), fingerprintOf(t, http.MethodPost, "/x", "", nil, `{"a":1}`)) {
		t.Error("two JSON values read as one")
	}
	// Each value is its own part: a value cannot be moved across a boundary.
	if bytes.Equal(fingerprintOf(t, http.MethodPost, "/x?a=1&a=2", "", nil, ""), fingerprintOf(t, http.MethodPost, "/x?a=1%002", "", nil, "")) {
		t.Error("two query values read as one with a NUL")
	}
	if bytes.Equal(fingerprintOf(t, http.MethodPost, "/x", "application/json; charset=latin1", nil, `{}`), fingerprintOf(t, http.MethodPost, "/x", "application/json", nil, `{}`)) {
		t.Error("another charset is the same request")
	}
	// Invalid UTF-8 is hashed as it is: two bodies the decoder would make one
	// stay two.
	if bytes.Equal(fingerprintOf(t, http.MethodPost, "/x", "application/json", nil, "{\"a\":\"\xff\"}"), fingerprintOf(t, http.MethodPost, "/x", "application/json", nil, "{\"a\":\"\xfe\"}")) {
		t.Error("two invalid UTF-8 bodies read as one")
	}
	// A body over 64 KiB is its bytes: reformatted, it is another request.
	large := `{"a":"` + strings.Repeat("x", 70<<10) + `","b":1}`
	reordered := `{"b":1,"a":"` + strings.Repeat("x", 70<<10) + `"}`
	if bytes.Equal(fingerprintOf(t, http.MethodPost, "/x", "application/json", nil, large), fingerprintOf(t, http.MethodPost, "/x", "application/json", nil, reordered)) {
		t.Error("a large body was read as JSON")
	}
}

// TestIdempotencyFingerprint_ALargeBodyIsNotDecoded is SEC 1: a 4 MiB JSON
// array is hashed as its bytes, without decoding it into a value per element.
func TestIdempotencyFingerprint_ALargeBodyIsNotDecoded(t *testing.T) {
	body := []byte("[" + strings.Repeat("1,", 2<<20) + "1]")
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("Content-Type", "application/json")
	allocs := testing.AllocsPerRun(5, func() { idempotencyFingerprint(req, nil, body) })
	if allocs > 50 {
		t.Errorf("%v allocations to fingerprint a 4 MiB array, want a handful", allocs)
	}
}

// TestIdempotencyScope is I3 and I28: an API key's scope is its integration
// across its keys; a person's includes the way in, a grant's its grant and
// customer, and a grant's principal with no grant on the context has none.
func TestIdempotencyScope(t *testing.T) {
	person, integration, grant, agent := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	customer := int32(42)
	ctx := context.Background()
	if s, ok := idempotencyScope(ctx, contracts.Principal{UserID: person}, "op", "k"); !ok || s.Principal != person || s.Via != "session" {
		t.Errorf("session: %+v", s)
	}
	a, _ := idempotencyScope(ctx, contracts.Principal{UserID: integration, Via: &contracts.Via{Kind: contracts.ViaAPIKey, KeyID: uuid.New()}}, "op", "k")
	b, _ := idempotencyScope(ctx, contracts.Principal{UserID: integration, Via: &contracts.Via{Kind: contracts.ViaAPIKey, KeyID: uuid.New()}}, "op", "k")
	if a != b || a.Via != contracts.ViaAPIKey {
		t.Errorf("two keys of one integration: %+v, %+v; want one scope", a, b)
	}
	assistantCtx := contracts.WithGrant(ctx, contracts.GrantRef{Kind: contracts.GrantAssistant, ID: grant})
	if s, ok := idempotencyScope(assistantCtx, contracts.Principal{UserID: person, Via: &contracts.Via{Kind: contracts.ViaAssistant}}, "op", "k"); !ok || s.Via != "assistant:"+grant.String() {
		t.Errorf("assistant: %+v", s)
	}
	agentCtx := contracts.WithGrant(ctx, contracts.GrantRef{Kind: contracts.GrantAgent, ID: grant})
	s, ok := idempotencyScope(agentCtx, contracts.Principal{UserID: agent, Via: &contracts.Via{Kind: contracts.ViaAgent, AgentID: agent, CustomerID: &customer}}, "op", "k")
	if !ok || s.Via != "agent:"+grant.String()+":customer:42" || s.CustomerID == nil || *s.CustomerID != customer {
		t.Errorf("agent: %+v", s)
	}
	for _, kind := range []string{contracts.ViaAssistant, contracts.ViaAgent} {
		if s, ok := idempotencyScope(ctx, contracts.Principal{UserID: agent, Via: &contracts.Via{Kind: kind, AgentID: agent}}, "op", "k"); ok {
			t.Errorf("%s with no grant: %+v, want no scope", kind, s)
		}
	}
}

// TestIdempotencyRecorder_Outcomes is I20 and I23: what is stored, released
// or left unknown, and that the client gets the real answer either way.
func TestIdempotencyRecorder_Outcomes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		write  func(w http.ResponseWriter)
		want   idempotencyOutcome
		stored int
	}{
		{"201 JSON", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Location", "/x/1")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1}`))
		}, outcomeDone, http.StatusCreated},
		{"204", func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }, outcomeDone, http.StatusNoContent},
		{"nothing written", func(http.ResponseWriter) {}, outcomeUnknown, 0},
		{"headers set, no status", func(w http.ResponseWriter) { w.Header().Set("Content-Type", "application/json") }, outcomeUnknown, 0},
		{"400 problem", func(w http.ResponseWriter) { writeJSON(w, http.StatusBadRequest, "application/problem+json") }, outcomeDone, http.StatusBadRequest},
		{"404", func(w http.ResponseWriter) { writeJSON(w, http.StatusNotFound, "application/json") }, outcomeDone, http.StatusNotFound},
		{"410", func(w http.ResponseWriter) { writeJSON(w, http.StatusGone, "application/json") }, outcomeDone, http.StatusGone},
		{"422", func(w http.ResponseWriter) { writeJSON(w, http.StatusUnprocessableEntity, "application/json") }, outcomeDone, http.StatusUnprocessableEntity},
		{"409", func(w http.ResponseWriter) { writeJSON(w, http.StatusConflict, "application/json") }, outcomeRelease, 0},
		{"500", func(w http.ResponseWriter) { writeJSON(w, http.StatusInternalServerError, "application/json") }, outcomeUnknown, 0},
		{"503", func(w http.ResponseWriter) { writeJSON(w, http.StatusServiceUnavailable, "application/json") }, outcomeUnknown, 0},
		{"408", func(w http.ResponseWriter) { writeJSON(w, http.StatusRequestTimeout, "application/json") }, outcomeUnknown, 0},
		{"429", func(w http.ResponseWriter) { writeJSON(w, http.StatusTooManyRequests, "application/json") }, outcomeUnknown, 0},
		{"403", func(w http.ResponseWriter) { writeJSON(w, http.StatusForbidden, "application/json") }, outcomeUnknown, 0},
		{"200 no-store", func(w http.ResponseWriter) {
			w.Header().Set("Cache-Control", "private, no-store")
			writeJSON(w, http.StatusOK, "application/json")
		}, outcomeUnknown, 0},
		{"200 Set-Cookie", func(w http.ResponseWriter) {
			w.Header().Add("Set-Cookie", "a=b")
			writeJSON(w, http.StatusOK, "application/json")
		}, outcomeUnknown, 0},
		{"200 not JSON", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/pdf")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("%PDF"))
		}, outcomeUnknown, 0},
		{"400 no-store", func(w http.ResponseWriter) {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusBadRequest, "application/problem+json")
		}, outcomeRelease, 0},
		{"over 256 KB", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`"` + strings.Repeat("a", idempotencyMaxBody) + `"`))
		}, outcomeUnknown, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := httptest.NewRecorder()
			rec := &idempotencyRecorder{w: client, header: http.Header{}}
			tc.write(rec)
			outcome, resp := rec.outcome()
			rec.finish()
			if outcome != tc.want {
				t.Errorf("outcome = %v, want %v", outcome, tc.want)
			}
			if outcome == outcomeDone && resp.Status != tc.stored {
				t.Errorf("stored status %d, want %d", resp.Status, tc.stored)
			}
			// The client always gets the handler's answer.
			direct := httptest.NewRecorder()
			tc.write(direct)
			if client.Code != direct.Code || !bytes.Equal(client.Body.Bytes(), direct.Body.Bytes()) || client.Header().Get("Content-Type") != direct.Header().Get("Content-Type") {
				t.Errorf("the client got %d %d bytes %v, want %d %d bytes %v", client.Code, client.Body.Len(), client.Header(), direct.Code, direct.Body.Len(), direct.Header())
			}
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"x","message":"y"}}`))
}

// TestIdempotentOperation is I19 on operations made to test each condition.
func TestIdempotentOperation(t *testing.T) {
	doc := loadDoc(t, idempotencyContract)
	catalog := []contracts.Permission{
		{Key: "x:write", Display: "d", Description: "d", Delegable: true},
		{Key: "x:secret", Display: "d", Description: "d", Delegable: false},
	}
	want := map[string]bool{
		"postCreate": true, "postEmpty": true,
		"getRead": false, "putReplace": false, "postPolicy": false, "postAnonymous": false, "postSession": false,
		"postNever": false, "postRead": false, "postAPINever": false, "postUnknownPermission": false,
		"postOwnKey": false, "postUpload": false, "postFile": false, "postNoStore": false,
	}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			w, ok := want[op.OperationID]
			if !ok {
				t.Fatalf("%s %s: no expectation", method, path)
			}
			if got := Idempotent(method, op, catalog); got != w {
				t.Errorf("%s (%s %s) = %v, want %v", op.OperationID, method, path, got, w)
			}
		}
	}
}

const idempotencyContract = `
openapi: 3.0.3
info: { title: Test, version: "1" }
paths:
  /a:
    get:
      operationId: getRead
      x-vantigo-access: permission:x:write
      responses: { "200": { description: ok, content: { application/json: { schema: { type: object } } } } }
    post:
      operationId: postCreate
      x-vantigo-access: permission:x:write
      requestBody: { content: { application/json: { schema: { type: object } } } }
      responses: { "201": { description: ok, content: { application/json: { schema: { type: object } } } } }
    put:
      operationId: putReplace
      x-vantigo-access: permission:x:write
      responses: { "204": { description: ok } }
  /b:
    post:
      operationId: postEmpty
      x-vantigo-access: permission:x:write
      responses: { "204": { description: ok } }
  /c:
    post:
      operationId: postPolicy
      x-vantigo-access: policy:Owner
      responses: { "204": { description: ok } }
  /d:
    post:
      operationId: postAnonymous
      x-vantigo-access: anonymous
      responses: { "204": { description: ok } }
  /e:
    post:
      operationId: postSession
      x-vantigo-access: session
      responses: { "204": { description: ok } }
  /f:
    post:
      operationId: postNever
      x-vantigo-access: permission:x:write
      x-vantigo-api: never
      responses: { "204": { description: ok } }
  /g:
    post:
      operationId: postRead
      x-vantigo-access: permission:x:write
      x-vantigo-api: read
      responses: { "200": { description: ok, content: { application/json: { schema: { type: object } } } } }
  /h:
    post:
      operationId: postAPINever
      x-vantigo-access: permission:x:secret
      responses: { "204": { description: ok } }
  /i:
    post:
      operationId: postUnknownPermission
      x-vantigo-access: permission:x:unknown
      responses: { "204": { description: ok } }
  /j:
    post:
      operationId: postOwnKey
      x-vantigo-access: permission:x:write
      parameters:
        - { in: header, name: Idempotency-Key, required: true, schema: { type: string } }
      responses: { "204": { description: ok } }
  /k:
    post:
      operationId: postUpload
      x-vantigo-access: permission:x:write
      requestBody: { content: { multipart/form-data: { schema: { type: object } } } }
      responses: { "204": { description: ok } }
  /l:
    post:
      operationId: postFile
      x-vantigo-access: permission:x:write
      responses: { "200": { description: ok, content: { application/pdf: { schema: { type: string, format: binary } } } } }
  /m:
    post:
      operationId: postNoStore
      x-vantigo-access: permission:x:write
      responses:
        "200":
          description: ok
          headers: { Cache-Control: { description: no-store — the value is never kept by a cache., schema: { type: string } } }
          content: { application/json: { schema: { type: object } } }
`
