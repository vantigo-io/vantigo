package staff

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// keepRawBody keeps a copy of every body but a multipart or an octet-stream
// one, which it passes to the handler untouched and unread, so an upload is
// never held in memory by it.
func TestKeepRawBody_KeepsAllButUploads(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, contentType, body string
		kept                    bool
	}{
		{"json", "application/json", `{"givenName":"Kari"}`, true},
		{"json with a charset", "application/json; charset=utf-8", `{"givenName":"Kari"}`, true},
		{"multipart", "multipart/form-data; boundary=x", "--x\r\n\r\nfile\r\n--x--", false},
		{"octet-stream", "application/octet-stream", "\x00\x01", false},
		// The generated decoder parses JSON whatever the Content-Type, so
		// every other body is captured — fetch sends a string as text/plain.
		{"plain text", "text/plain;charset=UTF-8", `{"givenName":"Kari"}`, true},
		{"no content type", "", `{"givenName":"Kari"}`, true},
	} {
		var sawKept bool
		var sawBody string
		var sawSame bool
		original := io.NopCloser(strings.NewReader(c.body))
		handler := keepRawBody(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			_, sawKept = r.Context().Value(rawBodyKey{}).([]byte)
			sawSame = r.Body == original
			b, _ := io.ReadAll(r.Body)
			sawBody = string(b)
		}))
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Body = original
		if c.contentType != "" {
			req.Header.Set("Content-Type", c.contentType)
		}
		handler.ServeHTTP(httptest.NewRecorder(), req)
		if sawKept != c.kept || sawBody != c.body {
			t.Errorf("%s: kept %v body %q, want kept %v and the body intact", c.name, sawKept, sawBody, c.kept)
		}
		if !c.kept && !sawSame {
			t.Errorf("%s: the handler got another reader, want the request's own body untouched", c.name)
		}
	}
}

// The mount fails unless the contract closes StaffPersonRequest,
// StaffMeRequest and StaffAbsenceTypeChangeRequest (additionalProperties false): the unknown-property refusal reads that, and
// a contract that lost it — or the schema — would silently accept anything.
func TestCheckClosedSchemas_FailsTheMountOnAnOpenOrMissingSchema(t *testing.T) {
	t.Parallel()
	doc, err := openapi.Load(context.Background(), "staff")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkClosedSchemas(doc); err != nil {
		t.Errorf("the real contract: %v, want nil", err)
	}
	if !slices.Equal(closedSchemas, []string{personRequestSchema, meRequestSchema, absenceTypeChangeSchema, absenceChangeSchema, absenceRequestSchema}) {
		t.Errorf("closed schemas = %v, want StaffPersonRequest, StaffMeRequest, StaffAbsenceTypeChangeRequest, StaffAbsenceChangeRequest and StaffAbsenceRequest", closedSchemas)
	}
	open := true
	for _, name := range closedSchemas {
		opened, _ := openapi.Load(context.Background(), "staff")
		opened.Components.Schemas[name].Value.AdditionalProperties = openapi3.AdditionalProperties{Has: &open}
		if err := checkClosedSchemas(opened); err == nil {
			t.Errorf("an open %s passed, want an error", name)
		}
		missing, _ := openapi.Load(context.Background(), "staff")
		delete(missing.Components.Schemas, name)
		if err := checkClosedSchemas(missing); err == nil {
			t.Errorf("a contract without %s passed, want an error", name)
		}
	}
	if err := checkClosedSchemas(nil); err == nil {
		t.Error("no contract passed, want an error")
	}
}
