package products

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// This module's refusals are bare RFC 7807 problems and field-level validation
// text (apicommon.Problem, apicommon.ValidationProblem), never identity's
// {code, message} bodies: .NET's Products endpoints used TypedResults.Problem
// and TypedResults.ValidationProblem throughout and have no machine-readable
// code vocabulary at all, so callers key off the status and the problem's
// title and detail (products inventory §1). Nothing here may grow an error
// code.
//
// apicommon.ForbiddenBody is the one exception, and it is not a new code: it
// is the platform access layer's own {code,message} AuthErrorResponse shape
// (already every operation's 401/403 in the generated contract), reused for
// the one handler-level Access.Check this module makes beyond what
// module.Router's x-vantigo-access already enforced — the conditional
// pricing-view+pricing-manage gate on postProducts/postProductsByIdVariants
// (server.go's hasPermission). A caller denied there should see exactly
// what a router-level permission denial looks like, not a different shape
// for the same kind of refusal.

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
