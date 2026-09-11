package identity

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestErrorBodies pins identity's error envelopes byte for byte, so a
// handler can rely on the shape each writer produces.
func TestErrorBodies(t *testing.T) {
	cases := []struct {
		name        string
		write       func(http.ResponseWriter)
		status      int
		contentType string
		body        string
	}{
		{"authError", func(w http.ResponseWriter) { authError(w, http.StatusConflict, "account_exists", "Taken.", nil) },
			http.StatusConflict, "application/json", `{"error":{"code":"account_exists","message":"Taken."}}`},
		{"authError with fields", func(w http.ResponseWriter) {
			authError(w, http.StatusBadRequest, "invalid_request", "Bad.", map[string][]string{"email": {"Required."}})
		}, http.StatusBadRequest, "application/json", `{"error":{"code":"invalid_request","fields":{"email":["Required."]},"message":"Bad."}}`},
		{"codeMessage", func(w http.ResponseWriter) { codeMessage(w, http.StatusConflict, "role_exists", "Exists.") },
			http.StatusConflict, "application/json", `{"code":"role_exists","message":"Exists."}`},
		{"writeInvalidRequest", func(w http.ResponseWriter) { writeInvalidRequest(w, httptest.NewRequest(http.MethodGet, "/", nil)) },
			http.StatusBadRequest, "application/json", `{"error":{"code":"invalid_request","message":"The request is invalid."}}`},
		{"scimUnauthorized", scimUnauthorized, http.StatusUnauthorized, "application/scim+json",
			`{"schemas":["urn:ietf:params:scim:api:messages:2.0:Error"],"status":"401","scimType":"invalidValue","detail":"A valid SCIM bearer token is required."}`},
		{"scimError with no scimType", func(w http.ResponseWriter) { _ = scimNotFound.write(w) }, http.StatusNotFound, "application/scim+json",
			`{"schemas":["urn:ietf:params:scim:api:messages:2.0:Error"],"status":"404","scimType":null,"detail":"The requested resource was not found."}`},
		{"writeDecodeError outside SCIM", func(w http.ResponseWriter) {
			writeDecodeError(w, httptest.NewRequest(http.MethodPost, "/api/v1/identity/login", nil))
		},
			http.StatusBadRequest, "application/json", `{"error":{"code":"invalid_request","message":"The request is invalid."}}`},
		{"writeDecodeError under SCIM", func(w http.ResponseWriter) {
			writeDecodeError(w, httptest.NewRequest(http.MethodGet, "/api/v1/identity/scim/v2/Users?startIndex=x", nil))
		}, http.StatusBadRequest, "application/scim+json",
			`{"schemas":["urn:ietf:params:scim:api:messages:2.0:Error"],"status":"400","scimType":"invalidSyntax","detail":"The request is invalid."}`},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		c.write(rec)
		if rec.Code != c.status || rec.Header().Get("Content-Type") != c.contentType || strings.TrimSpace(rec.Body.String()) != c.body {
			t.Errorf("%s: %d %q %s\nwant %d %q %s", c.name, rec.Code, rec.Header().Get("Content-Type"), rec.Body, c.status, c.contentType, c.body)
		}
	}
}
