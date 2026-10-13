package staff

import (
	"fmt"
	"net/http"
	"time"

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

	codeNationalIDInvalid           = "national_id_invalid"
	codeNationalIDDuplicate         = "national_id_duplicate"
	codeNationalIDBirthDateMismatch = "national_id_birth_date_mismatch"
	codeBankAccountInvalid          = "bank_account_invalid"
	codeUserInactive                = "user_inactive"
	codeUserLinkedElsewhere         = "user_linked_elsewhere"
	codeNoPerson                    = "no_person"

	codeEmploymentNumberTaken   = "employment_number_taken"
	codeEndReasonRequired       = "end_reason_required"
	codeTermsRequiredFields     = "terms_required_fields"
	codeTemporaryGroundRequired = "temporary_ground_required"
	codeAppointmentFormMismatch = "appointment_form_mismatch"
	codeProbationTooLong        = "probation_too_long"
	codeFullTimeHoursMismatch   = "full_time_hours_mismatch"
	codeTermsValidFromTaken     = "terms_valid_from_taken"
	codeTermsBeforeStart        = "terms_before_start"
	codeTermsAfterEnd           = "terms_after_end"
	codeTermsFirstRowStart      = "terms_first_row_start"
	codeTermsNotLatest          = "terms_not_latest"
	codeSelfManager             = "self_manager"
	codeManagerCycle            = "manager_cycle"
)

// The refusal codes delivery A2's document archive answers.
const (
	codeDocumentTypeUnsupported = "document_type_unsupported"
	codeDocumentTooLarge        = "document_too_large"
	codeDocumentCountExceeded   = "document_count_exceeded"
	codeCategoryRestricted      = "category_restricted"
	codeCategoryInUse           = "category_in_use"
	codeSeedImmutable           = "seed_immutable"
	codePersonErased            = "person_erased"
	codeStorageUnavailable      = "storage_unavailable"
)

// The refusal codes of the erased state, the legal hold and the
// reassessment (delivery A2, Task 2).
const (
	codeLegalHold          = "legal_hold"
	codeReassessmentNotDue = "reassessment_not_due"
)

// The refusal codes of the register's import and export (delivery A2,
// Task 3): every one a 400, the file's own on "file".
const (
	codeImportSealedColumns = "import_sealed_columns"
	codeImportTooLarge      = "import_too_large"
	codeImportTooManyRows   = "import_too_many_rows"
	codeImportNotSemicolon  = "import_not_semicolon"
	codeExportTooMany       = "export_too_many"
)

// The refusal codes of the payroll export (delivery A2, Task 4): each a 400
// on the parameter it names.
const (
	codePayrollMonthInvalid      = "payroll_month_invalid"
	codePayrollFormatUnsupported = "payroll_format_unsupported"
)

// The refusal code of the retention run (delivery A2, Task 5): a 409.
const (
	codeRetentionBusy = "retention_busy"
)

// The refusal code of the thresholds (delivery A2, Task 6): a 404 naming the
// key that is none of the four.
const (
	codeThresholdUnknown = "threshold_unknown"
)

// The refusal code of the holiday schemes (delivery B, Task 2): a 409 on
// archiving the default scheme or one an employment names. A seed's change
// answers A2's seed_immutable.
const (
	codeHolidaySchemeInUse = "holiday_scheme_in_use"
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
	invalidListTitle         = "Invalid list filter"
	invalidNationalIDTitle   = "Invalid national identity number"
	invalidBankAccountTitle  = "Invalid bank account"
	invalidLinkTitle         = "Invalid user link"
	invalidEmploymentTitle   = "Invalid employment"
	invalidEndTitle          = "Invalid end of employment"
	invalidTermsTitle        = "Invalid terms"
	invalidDocumentTitle     = "Invalid document"
	invalidCategoryTitle     = "Invalid document category"
	invalidLegalHoldTitle    = "Invalid legal hold"
	invalidImportFileTitle   = "Invalid import file"
	invalidPayrollTitle      = "Invalid payroll export"
	invalidAbsenceTypeTitle  = "Invalid absence type"
	invalidSchemeTitle       = "Invalid holiday scheme"
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

// noPerson is the 404 My page answers a caller linked to no person: a
// problem with its code rather than the bare 404 of an unknown id, since the
// caller is asking about themselves and there is nothing to hide from them.
func noPerson() gen.StaffProblem {
	status := int32(http.StatusNotFound)
	code, title := codeNoPerson, "No person"
	detail := "You are linked to no person in Staff. Ask HR to link your user to your record."
	return gen.StaffProblem{Code: &code, Title: &title, Detail: &detail, Status: &status}
}

// thresholdUnknown is the 404 a threshold key that is none of the four
// answers: a problem with its code rather than the bare 404 of an unknown id,
// since the four keys are no secret.
func thresholdUnknown(key string) gen.StaffProblem {
	status := int32(http.StatusNotFound)
	code, title := codeThresholdUnknown, "Unknown threshold"
	detail := fmt.Sprintf("'%s' is no threshold; a threshold is 5, 30, 50 or otp.", key)
	return gen.StaffProblem{Code: &code, Title: &title, Detail: &detail, Status: &status}
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

// managerCycle is the 409 a write answers when it would close a loop in the
// manager graph on day: a person who, through their managers, would manage
// themselves. It names nobody — the loop runs through other people's rows.
func managerCycle(day time.Time) error {
	return refuseConflict(conflict(codeManagerCycle, "Manager cycle",
		fmt.Sprintf("This would make someone their own manager, through the people above them, from %s.", day.Format(time.DateOnly))))
}

// storageUnavailable is the 503 an upload or a download answers when the
// object store fails: the request was right and the installation could not
// serve it — worth retrying and worth an operator seeing, rather than a 404
// that would hide a fault as a missing document.
func storageUnavailable() gen.StaffProblem {
	status := int32(http.StatusServiceUnavailable)
	code, title := codeStorageUnavailable, "Document storage is unavailable"
	detail := "The document store could not be reached. Nothing was changed; try again."
	return gen.StaffProblem{Code: &code, Title: &title, Detail: &detail, Status: &status}
}

// categoryRestricted is the 403 HR without staff:personal-data answers for a
// document in a restricted category — a health document — or a move into or
// out of one. It is a problem with its code rather than the uniform 403: the
// caller may file documents, just not this kind.
func categoryRestricted() error {
	status := int32(http.StatusForbidden)
	code, title := codeCategoryRestricted, "Restricted category"
	detail := "Documents in this category are health documents, which only staff:personal-data files, moves or removes."
	return &refusal{status: http.StatusForbidden, problem: gen.StaffProblem{Code: &code, Title: &title, Detail: &detail, Status: &status}}
}

// retentionBusy is the 409 POST /retention/run answers while the retention
// worker's lease is held — by the worker on this replica or another, or by
// another run by hand: one erase cycle at a time, and this one ran nothing.
func retentionBusy() gen.StaffProblem {
	return conflict(codeRetentionBusy, "Retention is running",
		"The retention run is already running, here or on another server. Nothing was run; try again when it has finished.")
}
