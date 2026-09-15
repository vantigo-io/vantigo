// Package apicommon is the API vocabulary every business module shares: the
// types common.yaml declares (generated into apicommon/gen and re-exported
// here, so a module imports one package) and the helpers that build them —
// RFC 7807 problems and validation problems, the access layer's forbidden
// body, pagination metadata, and the dashboard stats period.
//
// Modules may not import each other (depguard), which is why these live in
// the platform rather than in whichever module first needed them. Nothing
// here knows any module exists.
package apicommon

import (
	"math"
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/apicommon/gen"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// The common.yaml types, by their generated names. Aliases, not new types:
// a generated module response type wants exactly gen's type, and gets it.
type (
	AuthErrorResponse            = gen.AuthErrorResponse
	HttpValidationProblemDetails = gen.HttpValidationProblemDetails
	JsonElement                  = gen.JsonElement
	PaginationMetadata           = gen.PaginationMetadata
	ProblemDetails               = gen.ProblemDetails
)

// WriteDecodeError is the generated server's answer to a parameter or body
// it cannot decode: a bare 400 problem. It never echoes the decoder's
// error, which would leak the shape of the request it failed to parse.
func WriteDecodeError(w http.ResponseWriter, r *http.Request) {
	httpx.WriteProblem(w, r, http.StatusBadRequest, "")
}

// Problem builds a bare RFC 7807 ProblemDetails with status 400: title and
// detail text, no machine-readable code. The shape of every ad-hoc
// business-rule refusal in the modules that answer plain problems.
func Problem(title, detail string) ProblemDetails {
	return ProblemStatus(title, detail, http.StatusBadRequest)
}

// ProblemStatus is Problem with an explicit status.
func ProblemStatus(title, detail string, status int) ProblemDetails {
	s := int32(status)
	return ProblemDetails{Title: &title, Detail: &detail, Status: &s}
}

// ValidationProblem builds an RFC 7807 HttpValidationProblemDetails with
// status 400: a {field: [messages]} error map under one title, the shape
// of every field-level validation failure.
func ValidationProblem(title string, errs map[string][]string) HttpValidationProblemDetails {
	status := int32(http.StatusBadRequest)
	return HttpValidationProblemDetails{Title: &title, Status: &status, Errors: &errs}
}

// ForbiddenCode and ForbiddenMessage are the access layer's forbidden body:
// what every router-level permission denial answers, and what a handler
// answers when it denies on a permission the router could not evaluate
// (one conditional on the request body).
const (
	ForbiddenCode    = "forbidden"
	ForbiddenMessage = "You do not have permission to access this resource."
)

// ForbiddenBody is the AuthErrorResponse ForbiddenCode and ForbiddenMessage
// make, for a handler answering through a generated 403 response type.
func ForbiddenBody() AuthErrorResponse {
	var body AuthErrorResponse
	body.Error.Code = ForbiddenCode
	body.Error.Message = ForbiddenMessage
	return body
}

// Ptr returns a pointer to a copy of v, for the optional fields of a
// generated response type.
func Ptr[T any](v T) *T { return &v }

// TotalPages is the ceiling division of totalCount by pageSize; 0 for a
// non-positive page size.
func TotalPages(totalCount, pageSize int32) int32 {
	if pageSize <= 0 {
		return 0
	}
	return int32(math.Ceil(float64(totalCount) / float64(pageSize)))
}

// Pagination builds the PaginationMetadata of one page of a list: page and
// pageSize as requested, totalCount as counted, and the derived total,
// next and previous flags.
func Pagination(page, pageSize, totalCount int32) PaginationMetadata {
	totalPages := TotalPages(totalCount, pageSize)
	return PaginationMetadata{
		Page:            page,
		PageSize:        pageSize,
		TotalCount:      totalCount,
		TotalPages:      totalPages,
		HasNextPage:     page < totalPages,
		HasPreviousPage: page > 1 && totalCount > 0,
	}
}

// StatsDefaultPeriodDays is the length of a dashboard stats period when the
// request names no start.
const StatsDefaultPeriodDays = 30

// InvalidPeriodTitle and InvalidPeriodDetail are the 400 a dashboard stats
// endpoint answers a period whose start is after its end.
const (
	InvalidPeriodTitle  = "Invalid period"
	InvalidPeriodDetail = "The 'from' value must be earlier than or equal to the 'to' value."
)

// InvalidPeriod is the problem NormalizePeriod's false answers with.
func InvalidPeriod() ProblemDetails {
	return Problem(InvalidPeriodTitle, InvalidPeriodDetail)
}

// NormalizePeriod resolves a dashboard stats request's optional from and to
// into a period: to defaults to now, from defaults to StatsDefaultPeriodDays
// before to, and previousFrom is the start of the immediately preceding
// window of the same length as [periodFrom, periodTo). ok is false when from
// is after to (from == to is valid), the only way a stats handler answers
// 400.
func NormalizePeriod(from, to *time.Time, now time.Time) (periodFrom, periodTo, previousFrom time.Time, ok bool) {
	periodTo = now
	if to != nil {
		periodTo = *to
	}
	periodFrom = periodTo.AddDate(0, 0, -StatsDefaultPeriodDays)
	if from != nil {
		periodFrom = *from
	}
	if periodFrom.After(periodTo) {
		return time.Time{}, time.Time{}, time.Time{}, false
	}
	previousFrom = periodFrom.Add(-periodTo.Sub(periodFrom))
	return periodFrom, periodTo, previousFrom, true
}
