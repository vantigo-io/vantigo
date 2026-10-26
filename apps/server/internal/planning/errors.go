package planning

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/planning/gen"
)

// This module's refusals follow the codebase's rule (design §4.7): 404 for
// what does not exist (a bare body, byte for byte what an unknown id gets),
// 403 for what is not the caller's to do (the access layer's own body), 400
// naming the field, and 409 for a rule about the state of things — and every
// one is an RFC 9457 problem in one shape, gen.PlanningProblem, carrying the
// rule's code when there is one. A client keys off the code, never the
// words.

// The refusal codes of delivery A (§4.7).
const (
	codeOutsideEmployment     = "outside_employment"
	codeProjectClosed         = "project_closed"
	codeOverbooked            = "overbooked"
	codePeriodTooLong         = "period_too_long"
	codeRevisionConflict      = "revision_conflict"
	codeNoPerson              = "no_person"
	codeDependencyUnavailable = "dependency_unavailable"
)

// The refusal codes of delivery B (§4.7, S16).
const codeRequestNotOpen = "request_not_open"

// The warning codes a write answers beside its row (rule 6, §4.6). overbooked
// is both: a warning under the setting warn, a refusal under refuse (S7).
const (
	warnOverbooked          = codeOverbooked
	warnBookedWhileAway     = "booked_while_away"
	warnOutsideProjectDates = "outside_project_dates"
)

// The titles this module's validation problems carry, one per thing a request
// writes, so a client can tell a refusal of the settings from one of an
// allocation without reading the field errors.
const (
	invalidSettingsTitle   = "Invalid Planning settings"
	invalidAllocationTitle = "Invalid allocation"
	invalidRequestTitle    = "Invalid request"
)

// errDependencyUnavailable is every accessor's answer when the contract it
// reads is not composed (S25): config makes that impossible in a running
// installation, and a handler answers it as the 503 dependency_unavailable
// rather than panic on a nil interface.
var errDependencyUnavailable = errors.New("planning: a module this one reads is not composed")

// withFieldError adds one message to a map another rule may already have put
// something in. A nil map is the "nothing failed yet" case, so it is grown
// rather than written to.
func withFieldError(errs map[string][]string, field, message string) map[string][]string {
	if errs == nil {
		errs = map[string][]string{}
	}
	errs[field] = append(errs[field], message)
	return errs
}

// fieldError is the one-field error map, for a rule decided on its own.
func fieldError(field, message string) map[string][]string {
	return map[string][]string{field: {message}}
}

// fieldErrors is a validation pass's outcome: the field errors and, when one
// of them is a rule the design names, its code. The first coded rule to fail
// gives the code; the field errors carry every failure.
type fieldErrors struct {
	errs map[string][]string
	code string
}

// add records message on field; "" records nothing.
func (f *fieldErrors) add(field, message string) {
	if message != "" {
		f.errs = withFieldError(f.errs, field, message)
	}
}

// addCoded records message on field as the coded rule code (period_too_long
// on toDate, so far).
func (f *fieldErrors) addCoded(field, code, message string) {
	if message == "" {
		return
	}
	f.add(field, message)
	if f.code == "" {
		f.code = code
	}
}

// merge adds every failure of o to f, o's code if f has none.
func (f *fieldErrors) merge(o fieldErrors) {
	for field, messages := range o.errs {
		for _, m := range messages {
			f.add(field, m)
		}
	}
	if f.code == "" {
		f.code = o.code
	}
}

// failed reports whether any rule failed.
func (f *fieldErrors) failed() bool { return len(f.errs) > 0 }

// invalid is the 400 body for a request whose fields did not pass: title, the
// field errors and the code, when one of them carries one.
func invalid(title string, f fieldErrors) gen.PlanningProblem {
	status := int32(http.StatusBadRequest)
	p := gen.PlanningProblem{Title: &title, Status: &status, Errors: &f.errs}
	if f.code != "" {
		p.Code = &f.code
	}
	return p
}

// conflict is a 409 carrying the rule's code and a sentence for a person.
func conflict(code, title, detail string) gen.PlanningProblem {
	status := int32(http.StatusConflict)
	return gen.PlanningProblem{Code: &code, Title: &title, Detail: &detail, Status: &status}
}

// revisionConflict is the 409 a change carrying a stale If-Match answers. It
// names both revisions, so a client can tell "somebody else saved" from "I
// sent the wrong number".
func revisionConflict(what string, current, supplied int32) gen.PlanningProblem {
	return conflict(codeRevisionConflict, "Revision conflict",
		fmt.Sprintf("The %s has revision %d; the supplied revision was %d.", what, current, supplied))
}

// noPerson is the 404 My planning answers a caller Staff answers no person
// for (S19): a problem with its code rather than the bare 404 of an unknown
// id, since the caller is asking about themselves and there is nothing to
// hide from them.
func noPerson() gen.PlanningProblem {
	status := int32(http.StatusNotFound)
	code, title := codeNoPerson, "No person"
	detail := "Staff has no person linked to your user. Ask HR to link your user to your record."
	return gen.PlanningProblem{Code: &code, Title: &title, Detail: &detail, Status: &status}
}

// dependencyUnavailable is the 503 an operation answers when a contract it
// reads is not composed (S25, errDependencyUnavailable).
func dependencyUnavailable() gen.PlanningProblem {
	status := int32(http.StatusServiceUnavailable)
	code, title := codeDependencyUnavailable, "A module Planning reads is unavailable"
	detail := "Planning reads Projects, Staff and Customers, and one of them is not running in this installation. Nothing was changed."
	return gen.PlanningProblem{Code: &code, Title: &title, Detail: &detail, Status: &status}
}

// refusal is a write's answer that is not a success, returned from inside its
// transaction so everything the write did rolls back, and turned into the
// operation's own typed response by the handler.
type refusal struct {
	status  int
	problem gen.PlanningProblem
}

func (r *refusal) Error() string { return fmt.Sprintf("planning: refused with %d", r.status) }

// notFound is the bare 404 of an unknown id.
func notFound() error { return &refusal{status: http.StatusNotFound} }

// refuseConflict is a 409 carrying p.
func refuseConflict(p gen.PlanningProblem) error {
	return &refusal{status: http.StatusConflict, problem: p}
}

// refuseInvalid is a 400 carrying the field errors under title.
func refuseInvalid(title string, f fieldErrors) error {
	return &refusal{status: http.StatusBadRequest, problem: invalid(title, f)}
}

// asRefusal reports whether err is a refusal, and which.
func asRefusal(err error) (*refusal, bool) {
	var r *refusal
	ok := errors.As(err, &r)
	return r, ok
}

// checkRevision is the If-Match rule under the row's lock: the revision the
// caller read must be the row's.
func checkRevision(what string, current, supplied int32) error {
	if current != supplied {
		return refuseConflict(revisionConflict(what, current, supplied))
	}
	return nil
}

// The reads' refusals (design S8, S10, S12, §13 amendment 8): a window Staff
// refuses as outside its bound is a 400 on from or to carrying
// window_out_of_bounds, and every other fault of a window or a filter a plain
// field error under invalidQueryTitle.
const (
	codeWindowOutOfBounds = "window_out_of_bounds"
	invalidQueryTitle     = "Invalid query"
)
