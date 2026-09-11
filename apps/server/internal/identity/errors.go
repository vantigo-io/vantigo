package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	apicommon "github.com/vantigo-io/vantigo/server/internal/apicommon/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
)

// refusal is a client error answer that a handler, or a transaction on its
// behalf, decides on: an AuthErrorResponse with its status, the flat
// CodeMessageError /access/* answers with, or a bare 404. It is an error,
// so a transaction returns it to roll back and the handler hands it on
// (refusalOr). It implements the response interface of each operation that
// answers with one; those Visit methods sit beside the operations.
type refusal struct {
	status int
	body   *apicommon.AuthErrorResponse // nil for a flat body or a bare 404
	flat   *gen.CodeMessageError        // set only by refuseFlat
}

func refuse(status int, code, message string, fields map[string][]string) refusal {
	body := authErrorBody(code, message, fields)
	return refusal{status: status, body: &body}
}

// refuseFlat is a refusal with the flat CodeMessageError body
// {"code","message"} of the /access/* handlers
// (EA/AuthorizationManagementEndpoints.cs:759-760).
func refuseFlat(status int, code, message string) refusal {
	return refusal{status: status, flat: &gen.CodeMessageError{Code: code, Message: message}}
}

// notFound is the contract's bare 404, with no body, for an unknown user
// or invitation.
var notFound = refusal{status: http.StatusNotFound}

func (r refusal) Error() string {
	switch {
	case r.flat != nil:
		return fmt.Sprintf("identity: refused with %d %s", r.status, r.flat.Code)
	case r.body != nil:
		return fmt.Sprintf("identity: refused with %d %s", r.status, r.body.Error.Code)
	default:
		return fmt.Sprintf("identity: refused with %d", r.status)
	}
}

func (r refusal) write(w http.ResponseWriter) error {
	switch {
	case r.flat != nil:
		writeJSON(w, r.status, "application/json", r.flat)
	case r.body != nil:
		writeJSON(w, r.status, "application/json", r.body)
	default:
		w.WriteHeader(r.status)
	}
	return nil
}

// refusalOr is a handler's answer to err: the refusal in err's chain, or
// err itself, a server error. T is the operation's response interface; a
// refusal that does not implement it is a server error too, which the
// operation's tests catch.
func refusalOr[T any](err error) (T, error) {
	var zero T
	var r refusal
	if errors.As(err, &r) {
		if answer, ok := any(r).(T); ok {
			return answer, nil
		}
	}
	return zero, err
}

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
