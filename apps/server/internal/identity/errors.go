package identity

import (
	"encoding/json"
	"net/http"

	apicommon "github.com/vantigo-io/vantigo/server/internal/apicommon/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
)

// The fixed messages of the access layer's answers (.NET's cookie events,
// EA/AuthServiceCollectionExtensions.cs:114-125), of a body or parameter that
// does not decode, and of a SCIM request without a valid bearer token
// (SV/ScimProtocolService.cs:1202).
const (
	unauthenticatedMessage = "Authentication is required."
	forbiddenMessage       = "You do not have permission to access this resource."
	invalidRequestMessage  = "The request is invalid."
	scimUnauthorizedDetail = "A valid SCIM bearer token is required."
	scimErrorSchema        = "urn:ietf:params:scim:api:messages:2.0:Error"
)

// authError writes identity's usual error body, AuthErrorResponse:
// {"error":{"code","message","fields"}}, with fields omitted when empty.
func authError(w http.ResponseWriter, status int, code, message string, fields map[string][]string) {
	writeJSON(w, status, "application/json", authErrorBody(code, message, fields))
}

// authErrorBody is the AuthErrorResponse authError writes, for handlers
// that answer through a generated response type.
func authErrorBody(code, message string, fields map[string][]string) apicommon.AuthErrorResponse {
	var body apicommon.AuthErrorResponse
	body.Error.Code = code
	body.Error.Message = message
	if len(fields) > 0 {
		body.Error.Fields = &fields
	}
	return body
}

// codeMessage writes the flat CodeMessageError body {"code","message"} that
// /access/* and the maintenance 400 answer with.
func codeMessage(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, "application/json", gen.CodeMessageError{Code: code, Message: message})
}

// writeInvalidRequest is identity's answer to a parameter or body the
// generated server cannot decode. It never echoes the decoder's error.
func writeInvalidRequest(w http.ResponseWriter, _ *http.Request) {
	authError(w, http.StatusBadRequest, "invalid_request", invalidRequestMessage, nil)
}

// scimUnauthorized writes the SCIM 401 error body.
func scimUnauthorized(w http.ResponseWriter) {
	scimType := "invalidValue"
	writeJSON(w, http.StatusUnauthorized, "application/scim+json", gen.ScimError{
		Schemas:  []string{scimErrorSchema},
		Status:   "401",
		ScimType: &scimType,
		Detail:   scimUnauthorizedDetail,
	})
}

func writeJSON(w http.ResponseWriter, status int, contentType string, body any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body) // the bodies are plain structs of strings: Encode cannot fail
}
