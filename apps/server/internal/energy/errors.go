package energy

import (
	"net/http"

	apicommon "github.com/vantigo-io/vantigo/server/internal/apicommon/gen"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// This module's refusals are bare RFC 7807 problems and field-level
// validation text, never identity's {code, message} bodies: .NET's Energy
// endpoints used TypedResults.Problem and TypedResults.ValidationProblem
// throughout and have no machine-readable code vocabulary at all, so callers
// key off the status and the problem's title and detail (energy inventory
// §1). Nothing here may grow an error code.

// writeDecodeError is the generated server's answer to a parameter or body it
// cannot decode: a bare 400 problem. It never echoes the decoder's error,
// which would leak the shape of the request it failed to parse.
func writeDecodeError(w http.ResponseWriter, r *http.Request) {
	httpx.WriteProblem(w, r, http.StatusBadRequest, "")
}

// problem builds a bare RFC 7807 ProblemDetails, .NET's TypedResults.Problem
// (title/detail text, no machine-readable code): GetMeteringPoints's
// query-parameter 400, the duplicate-GSRN and overlapping-supply-period
// 409s, and EndSupplyPeriod's "cancelled period" 400 (which walks the
// HttpValidationProblemDetails wire type with no "errors" field set, see
// validationProblemNoErrors).
func problem(title, detail string) apicommon.ProblemDetails {
	return problemStatus(title, detail, http.StatusBadRequest)
}

// problemStatus is problem with an explicit status.
func problemStatus(title, detail string, status int) apicommon.ProblemDetails {
	s := int32(status)
	return apicommon.ProblemDetails{Title: &title, Detail: &detail, Status: &s}
}

// validationProblem builds an RFC 7807 HttpValidationProblemDetails, .NET's
// TypedResults.ValidationProblem: a {field: [messages]} error map under one
// title, every field-level metering-point/meter/supply-period validation
// failure's shape.
func validationProblem(title string, errs map[string][]string) apicommon.HttpValidationProblemDetails {
	status := int32(http.StatusBadRequest)
	return apicommon.HttpValidationProblemDetails{Title: &title, Status: &status, Errors: &errs}
}

// validationProblemNoErrors builds an HttpValidationProblemDetails body with
// no "errors" field: the shape a plain ProblemDetails takes when the
// contract's declared 400 wire type for an operation has no room to offer
// one. EndSupplyPeriodEndpoint's "cancelled period" branch returns .NET's
// plain TypedResults.Problem (title "Invalid supply period", detail "A
// cancelled period cannot be ended."), while the same operation's other 400
// (a bad end date) returns a real ValidationProblem with an "end" field
// error — two different 400 bodies from one operation (energy inventory
// §1.1 line 41, §8 oddity 2). The contract only documents the
// HttpValidationProblemDetails shape for this operation's 400, so this
// walks that same Go type with Errors left nil, producing the same wire
// bytes a plain ProblemDetails would.
func validationProblemNoErrors(title, detail string) apicommon.HttpValidationProblemDetails {
	status := int32(http.StatusBadRequest)
	return apicommon.HttpValidationProblemDetails{Title: &title, Detail: &detail, Status: &status}
}
