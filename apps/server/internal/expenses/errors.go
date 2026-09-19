package expenses

import (
	"github.com/vantigo-io/vantigo/server/internal/apicommon"
)

// This module's refusals are bare RFC 7807 problems and field-level validation
// text (apicommon.ValidationProblem), never identity's {code, message} bodies:
// callers key off the status, the problem's title and the field errors. Nothing
// here may grow a machine-readable error code of its own.
//
// Two refusals carry no body at all, deliberately. Reading or changing
// something the caller may not see answers a bare 404, byte for byte the answer
// an unknown id gets, so neither its existence nor what it belongs to leaks;
// acting on something the caller may see but may not change answers the access
// layer's own 403 (apicommon.ForbiddenBody), so a handler-level denial looks
// exactly like a router-level one.

// The titles this module's validation problems carry, so a client can tell its
// field errors from the platform's bare decode failures.
const (
	invalidSettingsTitle = "Invalid expense settings"
	invalidRateTitle     = "Invalid rate"
	invalidCategoryTitle = "Invalid category"
)

// invalidSettings is the 400 body for a settings replace whose fields did not
// pass design §3.5.
func invalidSettings(errs map[string][]string) apicommon.HttpValidationProblemDetails {
	return apicommon.ValidationProblem(invalidSettingsTitle, errs)
}

// invalidRate is the 400 body for a rate whose fields did not pass design §3.4.
func invalidRate(errs map[string][]string) apicommon.HttpValidationProblemDetails {
	return apicommon.ValidationProblem(invalidRateTitle, errs)
}

// invalidCategory is the 400 body for a category whose fields did not pass
// design §3.3.
func invalidCategory(errs map[string][]string) apicommon.HttpValidationProblemDetails {
	return apicommon.ValidationProblem(invalidCategoryTitle, errs)
}

// fieldError is the one-field error map the bodies above take, for a rule that
// can only be decided on its own — a uniqueness the database reports after
// every other rule has passed.
func fieldError(field, message string) map[string][]string {
	return map[string][]string{field: {message}}
}
