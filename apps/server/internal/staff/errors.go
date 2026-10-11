package staff

import (
	"fmt"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// This module's refusals follow the codebase's rule: 404 for what does not
// exist (a bare body, byte for byte what an unknown id gets, so nothing about
// a row the caller may not see leaks), 403 for what is not theirs to do (the
// access layer's own body), 400 naming the field, and 409 for a rule about the
// state of things — and every one is an RFC 9457 problem in one shape,
// gen.StaffProblem, carrying the rule's code when there is one (design §6's
// refusal codes). A client keys off the code, never the words. A 400 whose
// field errors are plain validation — a name too long, a code taken — carries
// no code; one that names a rule of the design (org_number_invalid,
// employee_number_too_long) carries it beside the field errors.

// The refusal codes delivery A1's foundation answers.
const (
	codeOrgNumberInvalid      = "org_number_invalid"
	codeEmployeeNumberTooLong = "employee_number_too_long"
	codeStructureArchived     = "structure_archived"
	codeStructureDefault      = "structure_default"
	codeDepartmentCycle       = "department_cycle"
	codeRevisionConflict      = "revision_conflict"
	codeEmployeeNumberTaken   = "employee_number_taken"
	codeWorkEmailTaken        = "work_email_taken"
	codePersonInUse           = "person_in_use"
)

// The titles this module's validation problems carry, one per thing a request
// writes, so a client can tell a refusal of the settings from one of a legal
// entity without reading the field errors.
const (
	invalidSettingsTitle     = "Invalid Staff settings"
	invalidLegalEntityTitle  = "Invalid legal entity"
	invalidBusinessUnitTitle = "Invalid business unit"
	invalidDepartmentTitle   = "Invalid department"
	invalidLocationTitle     = "Invalid location"
	invalidPersonTitle       = "Invalid person"
	invalidNextOfKinTitle    = "Invalid next of kin"
	invalidRolesTitle        = "Invalid roles"
	invalidPagingTitle       = "Invalid paging"
)

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

// fieldError is the one-field error map, for a rule decided on its own — a
// uniqueness the database reports after every other rule has passed.
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

// addCoded records message on field as the coded rule code.
func (f *fieldErrors) addCoded(field, code, message string) {
	if message == "" {
		return
	}
	f.add(field, message)
	if f.code == "" {
		f.code = code
	}
}

// failed reports whether any rule failed.
func (f *fieldErrors) failed() bool { return len(f.errs) > 0 }

// invalid is the 400 body for a request whose fields did not pass: title, the
// field errors and the code, when one of them carries one.
func invalid(title string, f fieldErrors) gen.StaffProblem {
	status := int32(http.StatusBadRequest)
	p := gen.StaffProblem{Title: &title, Status: &status, Errors: &f.errs}
	if f.code != "" {
		p.Code = &f.code
	}
	return p
}

// conflict is a 409 carrying the rule's code and a sentence for a person.
func conflict(code, title, detail string) gen.StaffProblem {
	status := int32(http.StatusConflict)
	return gen.StaffProblem{Code: &code, Title: &title, Detail: &detail, Status: &status}
}

// revisionConflict is the 409 a change carrying a stale If-Match answers. It
// names both revisions, so a client can tell "somebody else saved" from "I
// sent the wrong number".
func revisionConflict(what string, current, supplied int32) gen.StaffProblem {
	return conflict(codeRevisionConflict, "Revision conflict",
		fmt.Sprintf("The %s has revision %d; the supplied revision was %d.", what, current, supplied))
}

// structureArchived is the 409 a write naming an archived row answers.
func structureArchived(detail string) gen.StaffProblem {
	return conflict(codeStructureArchived, "Archived", detail)
}

// structureDefault is the 409 a write that would leave a structure table with
// no default answers: archiving the default, or taking the flag off it.
func structureDefault(what string) gen.StaffProblem {
	return conflict(codeStructureDefault, "The default "+what,
		fmt.Sprintf("This is the default %s, and there is always exactly one. Make another %s the default first.", what, what))
}
