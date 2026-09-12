package products

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"

	apicommon "github.com/vantigo-io/vantigo/server/internal/apicommon/gen"
	"github.com/vantigo-io/vantigo/server/internal/httpx"
)

// This module's refusals are bare RFC 7807 problems and field-level validation
// text, never identity's {code, message} bodies: .NET's Products endpoints
// used TypedResults.Problem and TypedResults.ValidationProblem throughout and
// have no machine-readable code vocabulary at all, so callers key off the
// status and the problem's title and detail (products inventory §1). Nothing
// here may grow an error code.
//
// forbiddenBody is the one exception, and it is not a new code: it is the
// platform access layer's own {code,message} AuthErrorResponse shape
// (already every operation's 401/403 in the generated contract), reused for
// the one handler-level Access.Check this module makes beyond what
// module.Router's x-vantigo-access already enforced — the conditional
// pricing-view+pricing-manage gate on postProducts/postProductsByIdVariants
// (server.go's hasPermission). A caller denied there should see exactly
// what a router-level permission denial looks like, not a different shape
// for the same kind of refusal.

// writeDecodeError is the generated server's answer to a parameter or body it
// cannot decode: a bare 400 problem. It never echoes the decoder's error,
// which would leak the shape of the request it failed to parse.
func writeDecodeError(w http.ResponseWriter, r *http.Request) {
	httpx.WriteProblem(w, r, http.StatusBadRequest, "")
}

// problem builds a bare RFC 7807 ProblemDetails, .NET's TypedResults.Problem
// (title/detail text, no machine-readable code): the ad-hoc business-rule
// 409s this module answers (duplicate SKU/barcode, SKU immutable, last
// variant, overlapping price), and GetProducts's query-parameter 400.
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
// title, every field-level product/variant/price validation failure's
// shape.
func validationProblem(title string, errs map[string][]string) apicommon.HttpValidationProblemDetails {
	status := int32(http.StatusBadRequest)
	return apicommon.HttpValidationProblemDetails{Title: &title, Status: &status, Errors: &errs}
}

// forbiddenCode and forbiddenMessage duplicate identity's access-layer
// forbidden body (internal/identity/errors.go's forbiddenMessage and
// Access.Reject, also duplicated in customers/errors.go): depguard forbids
// this module importing identity or customers, and the body is three
// lines. A caller denied by the conditional pricing gate sees exactly what
// module.Router's own permission denial would have shown had it been able
// to see the request body.
const (
	forbiddenCode    = "forbidden"
	forbiddenMessage = "You do not have permission to access this resource."
)

// forbiddenBody is the AuthErrorResponse forbiddenCode/forbiddenMessage
// make, for a handler answering through a generated 403 response type.
func forbiddenBody() apicommon.AuthErrorResponse {
	var body apicommon.AuthErrorResponse
	body.Error.Code = forbiddenCode
	body.Error.Message = forbiddenMessage
	return body
}

// isRestrictConflict reports whether err is a Postgres restrict_violation
// (23001) or foreign_key_violation (23503) — the two SQLSTATEs a delete
// blocked by category_id/tax_category_id/parent_id's Restrict foreign keys
// can raise (products inventory §3/§4/§7 oddity 7, corrected 2026-09-12): a
// literal `ON DELETE RESTRICT` raises 23001, confirmed against a real
// Postgres instance in internal/db/schema_test.go; 23503 is the FK default
// (NO ACTION) this schema's tables never use, but stays mapped too since a
// cascade or deferred path can still raise it (this is not this module's
// invention — the products inventory itself calls this out as a case a
// port "should handle both codes" for).
//
// .NET's global exception handler special-cases only unique_violation
// (23505) and exclusion_violation (23P01)
// (HOST/Diagnostics/VantigoExceptionHandler.cs:83-98) — neither 23001 nor
// 23503 is in its IsConstraintConflict list, so a Restrict-FK race falls
// through to a bare 500 in .NET. This module answers the documented 409
// instead: a deliberate divergence from .NET, not a bug fix disguised as a
// faithful port. categories.go's DeleteProductsCategoriesById and
// taxcategories.go's DeleteProductsTaxCategoriesById are the two callers.
func isRestrictConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23001" || pgErr.Code == "23503"
}
