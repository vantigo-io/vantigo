package expenses

import (
	"fmt"
	"net/http"
	"strings"

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
	invalidEntryTitle    = "Invalid expense"
	invalidQueryTitle    = "Invalid query parameters"
)

// invalidEntry is the 400 body for an expense whose fields did not pass
// design §3.1 and §4.
func invalidEntry(errs map[string][]string) apicommon.HttpValidationProblemDetails {
	return apicommon.ValidationProblem(invalidEntryTitle, errs)
}

// invalidQuery is the 400 a list answers for query parameters it cannot use,
// in projects' and customers' own wording.
func invalidQuery(messages []string) apicommon.ProblemDetails {
	return apicommon.Problem(invalidQueryTitle, strings.Join(messages, " "))
}

// forbidden is the access layer's own 403 body, answered by a handler that
// denies on something the router could not evaluate — whether the caller owns
// *this* expense and it is still theirs to change.
func forbidden() apicommon.AuthErrorResponse { return apicommon.ForbiddenBody() }

// revisionConflictTitle is the title of the 409 an update carrying a revision
// that has moved on answers.
const revisionConflictTitle = "Expense revision conflict"

// revisionConflict is that 409's body. It names both revisions, so a client
// can tell "somebody else saved" from "I sent the wrong number".
func revisionConflict(current, supplied int32) apicommon.ProblemDetails {
	return apicommon.ProblemStatus(revisionConflictTitle,
		fmt.Sprintf("The expense has revision %d; the supplied revision was %d.", current, supplied),
		http.StatusConflict)
}

// projectsNotInstalled is the 404 body of an operation that only exists when
// this installation has the projects module (decision X2). It is a problem
// rather than a bare 404 because the path itself is real and a client that
// ignored GET /meta's projectsAvailable deserves to be told why.
func projectsNotInstalled() apicommon.ProblemDetails {
	return apicommon.ProblemStatus("Projects are not installed",
		"This installation does not have the projects module, so an expense cannot be booked on a project.",
		http.StatusNotFound)
}

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
