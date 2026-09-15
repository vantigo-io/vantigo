package energy

import (
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
)

// This module's refusals are bare RFC 7807 problems and field-level
// validation text (apicommon.Problem, apicommon.ValidationProblem), never
// identity's {code, message} bodies: .NET's Energy endpoints used
// TypedResults.Problem and TypedResults.ValidationProblem throughout and have
// no machine-readable code vocabulary at all, so callers key off the status
// and the problem's title and detail (energy inventory §1). Nothing here may
// grow an error code.

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
