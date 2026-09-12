package customers

import (
	"math"
	"net/http"

	apicommon "github.com/vantigo-io/vantigo/server/internal/apicommon/gen"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// This module's refusals are bare RFC 7807 problems and field-level validation
// text, never identity's {code, message} bodies: .NET's Customers endpoints
// used TypedResults.Problem and TypedResults.ValidationProblem throughout and
// have no machine-readable code vocabulary at all, so callers key off the
// status and the problem's title and detail (inventory §1). Nothing here may
// grow an error code.

// writeDecodeError is the generated server's answer to a parameter or body it
// cannot decode: a bare 400 problem. It never echoes the decoder's error,
// which would leak the shape of the request it failed to parse.
func writeDecodeError(w http.ResponseWriter, r *http.Request) {
	httpx.WriteProblem(w, r, http.StatusBadRequest, "")
}

// problem builds a bare RFC 7807 ProblemDetails, .NET's TypedResults.Problem
// (title/detail text, no machine-readable code): the ad-hoc business-rule
// 400s this module answers (GetCustomers's query-parameter errors, the
// dashboard stats endpoints' invalid period/metric).
func problem(title, detail string) apicommon.ProblemDetails {
	status := int32(http.StatusBadRequest)
	return apicommon.ProblemDetails{Title: &title, Detail: &detail, Status: &status}
}

// validationProblem builds an RFC 7807 HttpValidationProblemDetails, .NET's
// TypedResults.ValidationProblem: a {field: [messages]} error map under one
// title, every field-level customer/identity validation failure's shape.
func validationProblem(title string, errs map[string][]string) apicommon.HttpValidationProblemDetails {
	status := int32(http.StatusBadRequest)
	return apicommon.HttpValidationProblemDetails{Title: &title, Status: &status, Errors: &errs}
}

// paginationMetadata is PaginationMetadata.Create
// (Endpoints/Dtos/PaginationMetadata.cs:18-31).
func paginationMetadata(page, pageSize, totalCount int32) apicommon.PaginationMetadata {
	var totalPages int32
	if pageSize > 0 {
		totalPages = int32(math.Ceil(float64(totalCount) / float64(pageSize)))
	}
	return apicommon.PaginationMetadata{
		Page:            page,
		PageSize:        pageSize,
		TotalCount:      totalCount,
		TotalPages:      totalPages,
		HasNextPage:     page < totalPages,
		HasPreviousPage: page > 1 && totalCount > 0,
	}
}
