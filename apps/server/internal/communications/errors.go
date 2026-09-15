package communications

import (
	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
)

// Communications is the one module in this project with two coexisting
// error vocabularies, both observable (communications inventory §3, design
// doc §3): every module error other than stats is
// {"error":{"code","message","fields"?}} on application/json
// (CommunicationErrorResponse, EP/CommunicationEndpointHelpers.cs:31-32);
// the three stats endpoints alone answer RFC 7807 ProblemDetails on
// application/problem+json, built with apicommon.Problem — nothing outside
// stats.go may call it. There is no HttpValidationProblemDetails anywhere,
// and every 404 is bare-bodied. Nothing here may invent a third shape.

// validationErrorBody is CommunicationEndpointHelpers.ValidationError
// (EP/CommunicationEndpointHelpers.cs:29, inventory §3): code is always
// "invalid_request" and message always "The request is invalid." — the
// per-field detail lives entirely in fields, never in message.
func validationErrorBody(fields map[string][]string) gen.CommunicationErrorResponse {
	var resp gen.CommunicationErrorResponse
	resp.Error.Code = "invalid_request"
	resp.Error.Message = "The request is invalid."
	resp.Error.Fields = &fields
	return resp
}

// flatErrorBody is CommunicationEndpointHelpers.Error (:31-32) for a
// fields-less refusal: a fixed machine-readable code and a human message,
// no per-field detail (inventory §3.2's flat-error table — channel_exists,
// destination_rejected, verification_failed and the rest).
func flatErrorBody(code, message string) gen.CommunicationErrorResponse {
	var resp gen.CommunicationErrorResponse
	resp.Error.Code = code
	resp.Error.Message = message
	return resp
}

// fieldsErrorBody is CommunicationEndpointHelpers.Error's other overload
// (:31-32), the one call site outside ValidationError that still carries a
// `fields` dictionary alongside a fixed code: Reply's own
// recipient_suppressed (`EP/ConversationEndpoints.cs:304`,
// `fields["recipients"] = suppressed`). Unlike validationErrorBody, code and
// message are whatever the caller passes, not the fixed invalid_request
// pair.
func fieldsErrorBody(code, message string, fields map[string][]string) gen.CommunicationErrorResponse {
	var resp gen.CommunicationErrorResponse
	resp.Error.Code = code
	resp.Error.Message = message
	resp.Error.Fields = &fields
	return resp
}
