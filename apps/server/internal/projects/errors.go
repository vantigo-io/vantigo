package projects

import "github.com/vantigo-io/vantigo/server/internal/apicommon"

// This module's refusals are bare RFC 7807 problems and field-level
// validation text (apicommon.Problem, apicommon.ProblemStatus,
// apicommon.ValidationProblem), never identity's {code, message} bodies:
// callers key off the status and the problem's title and detail. Nothing
// here may grow a machine-readable error code of its own.
//
// Two refusals carry no body at all, deliberately. Reading a project the
// caller holds no role in answers a bare 404, byte for byte the answer an
// unknown id gets, so project codes and existence do not leak (D7); acting
// on a project the caller can see but may not manage answers the access
// layer's own 403 (apicommon.ForbiddenBody), so a handler-level denial looks
// exactly like a router-level one.

// invalidProjectTitle is the title every create/update validation problem
// carries, so a client can tell this module's field errors from the
// platform's bare decode failures.
const invalidProjectTitle = "Invalid project"

// invalidProject is the 400 body for a create or update whose fields did not
// pass design §4.1.
func invalidProject(errs map[string][]string) apicommon.HttpValidationProblemDetails {
	return apicommon.ValidationProblem(invalidProjectTitle, errs)
}

// fieldError is the one-field error map invalidProject takes, for a rule
// that can only fail on its own — the code the unique index refused, so far.
func fieldError(field, message string) map[string][]string {
	return map[string][]string{field: {message}}
}
