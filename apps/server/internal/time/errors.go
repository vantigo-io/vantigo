package timetracking

import (
	"fmt"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
)

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

// invalidEntry is the 400 body for a create or an update whose fields did
// not pass design §4.2.
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

// invalidWeekTitle is the title of the 400 a week operation answers when its
// weekStart is not a Monday.
const invalidWeekTitle = "Invalid week"

// invalidWeek is that 400's body.
func invalidWeek(errs map[string][]string) apicommon.HttpValidationProblemDetails {
	return apicommon.ValidationProblem(invalidWeekTitle, errs)
}

// invalidSubmissionTitle is the title of the 400 a submit answers when
// something in it may not be submitted: a week holding drafts before the
// lock, or ids naming entries that are not the caller's submittable drafts.
const invalidSubmissionTitle = "Invalid submission"

// invalidSubmission is that 400's body.
func invalidSubmission(errs map[string][]string) apicommon.HttpValidationProblemDetails {
	return apicommon.ValidationProblem(invalidSubmissionTitle, errs)
}

// invalidQueryTitle is the title of the 400 a list answers for query
// parameters it cannot use, projects' and customers' wording.
const invalidQueryTitle = "Invalid query parameters"

// revisionConflictTitle is the title of the 409 an update carrying a
// revision that has moved on answers.
const revisionConflictTitle = "Time entry revision conflict"

// revisionConflict is that 409's body. It names both revisions, so a client
// can tell "somebody else saved" from "I sent the wrong number".
func revisionConflict(current, supplied int32) apicommon.ProblemDetails {
	return apicommon.ProblemStatus(revisionConflictTitle,
		fmt.Sprintf("The entry has revision %d; the supplied revision was %d.", current, supplied),
		http.StatusConflict)
}
