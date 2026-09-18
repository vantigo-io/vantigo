package timetracking

import "github.com/vantigo-io/vantigo/server/internal/apicommon"

// This module's refusals are bare RFC 7807 problems and field-level
// validation text (apicommon.ValidationProblem), never identity's {code,
// message} bodies: callers key off the status and the problem's title and
// the field errors. Nothing here may grow a machine-readable error code of
// its own.
//
// Two refusals carry no body at all, deliberately. Reading an entry the
// caller may not see answers a bare 404, byte for byte the answer an unknown
// id gets, so neither the entry's existence nor the project it is on leaks;
// acting on an entry the caller may see but may not change answers the access
// layer's own 403 (apicommon.ForbiddenBody), so a handler-level denial looks
// exactly like a router-level one.

// invalidEntryTitle is the title every time-entry validation problem carries,
// so a client can tell this module's field errors from the platform's bare
// decode failures.
const invalidEntryTitle = "Invalid time entry"

// invalidEntry is the 400 body for a create whose fields did not pass design
// §4.2.
func invalidEntry(errs map[string][]string) apicommon.HttpValidationProblemDetails {
	return apicommon.ValidationProblem(invalidEntryTitle, errs)
}

// fieldError is the one-field error map invalidEntry takes, for a rule that
// can only be decided on its own — the day cap, which is checked inside the
// transaction after every other rule has passed.
func fieldError(field, message string) map[string][]string {
	return map[string][]string{field: {message}}
}

// forbidden is the access layer's own 403 body, answered by a handler that
// denies on something the router could not evaluate — whether the caller
// owns *this* entry and it is still theirs to change.
func forbidden() apicommon.AuthErrorResponse { return apicommon.ForbiddenBody() }
