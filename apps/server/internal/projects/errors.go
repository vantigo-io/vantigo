package projects

import (
	"fmt"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
)

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
// that can only fail on its own — the code the unique index refused, and the
// status of the dedicated status operation.
func fieldError(field, message string) map[string][]string {
	return map[string][]string{field: {message}}
}

// forbidden is the access layer's own 403 body, answered by a handler that
// denies on something the router could not evaluate — whether the caller
// manages *this* project, which depends on a role the router never reads. A
// handler-level denial has to look exactly like a router-level one, or a
// client could tell the two apart and learn something about the project.
func forbidden() apicommon.AuthErrorResponse { return apicommon.ForbiddenBody() }

// revisionConflictTitle is the title of the only 409 this module answers:
// an edit carrying a revision the project has moved past.
const revisionConflictTitle = "Project revision conflict"

// revisionConflict is that 409's body. It names both revisions, so a client
// can tell "somebody else saved first" from "I sent the wrong number" — the
// same shape customers' timeline reports a stale entry revision with.
func revisionConflict(current, supplied int32) apicommon.ProblemDetails {
	return apicommon.ProblemStatus(revisionConflictTitle,
		fmt.Sprintf("The project has revision %d; the supplied revision was %d.", current, supplied),
		http.StatusConflict)
}
