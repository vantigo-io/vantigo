package customers

// This module's own refusals are bare RFC 7807 problems and field-level
// validation text (apicommon.Problem, apicommon.ProblemStatus,
// apicommon.ValidationProblem), never identity's {code, message} bodies:
// .NET's Customers endpoints used TypedResults.Problem and
// TypedResults.ValidationProblem throughout and have no machine-readable code
// vocabulary at all, so callers key off the status and the problem's title
// and detail (inventory §1). Nothing here may grow a new error code of its
// own. The business-rule refusals that are not 400s go through
// ProblemStatus: AttachCustomerContactEndpoint's 409 "Contact already
// associated" (customers inventory §1.4) and BrregLookupEndpoint's 502
// "Lookup service unavailable" (§5).
//
// apicommon.ForbiddenBody is the one exception, and it is not a new code: it
// is the platform access layer's own {code,message} AuthErrorResponse shape
// (already every operation's 401/403 in the generated contract), reused for
// the one handler-level Access.Check this module makes beyond what
// module.Router's x-vantigo-access already enforced — legal-identity-manage
// on postCustomers/putCustomersById when the body carries an identity
// (inventory §1.1/§1.4). A caller denied there should see exactly what a
// router-level permission denial looks like, not a different shape for the
// same kind of refusal.
