package calendar

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
)

// This module's refusals (calendar design D3) are RFC 9457 problems in one
// shape, gen.CalendarProblem: field errors on the body (400), keyed by the
// request's camelCase field — recurrence.rule for a nested one, attendees[2]
// for an element of a list; a bare 404 for what the caller may not see, byte
// for byte what an unknown id gets, so neither an appointment's existence nor
// whose it is leaks; a 409 carrying one of the codes below; and a 503 when a
// calendar source could not be read or, on a resend, mail cannot be sent.
// The uniform 403 is the access layer's own. A client keys off the status
// and the code, never the words.

// The refusal codes: three 409s and a 503.
const (
	// codeRevisionMismatch is a PUT whose If-Match revision has moved on.
	codeRevisionMismatch = "revision_mismatch"
	// codeOccurrenceNotInSeries is an occurrence the series' rule never
	// produces.
	codeOccurrenceNotInSeries = "occurrence_not_in_series"
	// codeAppointmentCancelled is any write to a cancelled appointment
	// (calendar invitations design D6): nothing of it changes.
	codeAppointmentCancelled = "appointment_cancelled"
	// codeInvitationsUnavailable is a resend when the mail driver is not
	// smtp (calendar invitations design D3), a 503.
	codeInvitationsUnavailable = "invitations_unavailable"
)

// The titles this module's validation problems carry, one per thing a request
// writes, so a client can tell a refused occurrence from a refused
// appointment without reading the field errors.
const (
	invalidAppointmentTitle = "Invalid appointment"
	invalidOccurrenceTitle  = "Invalid occurrence"
	invalidResponseTitle    = "Invalid response"
	invalidWindowTitle      = "Invalid window"
	invalidQueryTitle       = "Invalid query parameters"
)

// fieldErrors is a validation pass's outcome: every failure, by field.
type fieldErrors struct {
	errs map[string][]string
}

// add records message on field; "" records nothing.
func (f *fieldErrors) add(field, message string) {
	if message == "" {
		return
	}
	if f.errs == nil {
		f.errs = map[string][]string{}
	}
	f.errs[field] = append(f.errs[field], message)
}

// failed reports whether any rule failed.
func (f *fieldErrors) failed() bool { return len(f.errs) > 0 }

// invalid is the 400 body for a request whose fields did not pass.
func invalid(title string, f fieldErrors) gen.CalendarProblem {
	status := int32(http.StatusBadRequest)
	errs := f.errs
	return gen.CalendarProblem{Title: &title, Status: &status, Errors: &errs}
}

// badIfMatch is the field error of an If-Match that names no revision.
func badIfMatch() fieldErrors {
	var f fieldErrors
	f.add("If-Match", "If-Match is the revision the appointment was read at, such as 3")
	return f
}

// conflict is a 409 carrying the rule's code and a sentence for a person.
func conflict(code, title, detail string) gen.CalendarProblem {
	status := int32(http.StatusConflict)
	return gen.CalendarProblem{Code: &code, Title: &title, Detail: &detail, Status: &status}
}

// revisionMismatch is the 409 a PUT carrying a stale If-Match answers. It
// names both revisions, so a client can tell "somebody else saved" from "I
// sent the wrong number".
func revisionMismatch(current, supplied int32) gen.CalendarProblem {
	return conflict(codeRevisionMismatch, "Revision mismatch",
		fmt.Sprintf("The appointment has revision %d; the supplied revision was %d.", current, supplied))
}

// occurrenceNotInSeries is the 409 an occurrence's PUT answers for a day the
// series' rule never produces.
func occurrenceNotInSeries(date time.Time) gen.CalendarProblem {
	return conflict(codeOccurrenceNotInSeries, "Not an occurrence",
		fmt.Sprintf("The series has no occurrence on %s.", date.Format(time.DateOnly)))
}

// appointmentCancelled is the 409 every write to a cancelled appointment
// answers.
func appointmentCancelled(at time.Time) gen.CalendarProblem {
	return conflict(codeAppointmentCancelled, "Appointment cancelled",
		fmt.Sprintf("The appointment was cancelled on %s, and nothing of it changes.", at.UTC().Format(time.DateOnly)))
}

// errCancelled is the refusal of a write to the cancelled appointment a.
func errCancelled(a store.CalendarAppointment) error {
	return refuse(http.StatusConflict, appointmentCancelled(*a.CancelledAt))
}

// invitationsUnavailable is the 503 a resend answers when the mail driver is
// not smtp: no invitation can be sent at all.
func invitationsUnavailable() gen.CalendarProblem {
	status := int32(http.StatusServiceUnavailable)
	code := codeInvitationsUnavailable
	title := "Invitations unavailable"
	detail := "This installation does not send mail (the mail driver is not smtp), so no invitation can be sent."
	return gen.CalendarProblem{Code: &code, Title: &title, Detail: &detail, Status: &status}
}

// sourceUnavailable is the 503 GET /items answers when a calendar source
// fails: absent must never also mean "could not be read" (calendar design
// D1). It names no module — which one failed is the log's, not the caller's.
func sourceUnavailable() gen.CalendarProblem {
	status := int32(http.StatusServiceUnavailable)
	title := "Calendar source unavailable"
	detail := "Part of the calendar could not be read. Nothing is shown rather than a calendar with something missing; try again."
	return gen.CalendarProblem{Title: &title, Detail: &detail, Status: &status}
}

// refusal is a handler's answer decided inside a transaction — a 404, a 400
// or a 409 — carried out of it as an error so the transaction rolls back, and
// turned into the operation's response by the handler.
type refusal struct {
	status  int
	problem gen.CalendarProblem
}

func (r *refusal) Error() string { return fmt.Sprintf("calendar: refused with %d", r.status) }

// errNotFound is the bare 404 of an unknown id, or of one the caller may not
// see or change.
var errNotFound = &refusal{status: http.StatusNotFound}

// refuse is a refusal of status carrying p.
func refuse(status int, p gen.CalendarProblem) error {
	return &refusal{status: status, problem: p}
}

// asRefusal reports whether err is a refusal, and which.
func asRefusal(err error) (*refusal, bool) {
	var r *refusal
	ok := errors.As(err, &r)
	return r, ok
}
