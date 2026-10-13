package staff

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the decisions on absence (design S12, plan Task 4): approve
// and reject a request, cancel a decided one, and HR's correction of an
// approved or registered one. Who decides is deciders.go's, judged before
// the person's lock — it reads the directory, which no transaction may —
// and the row is judged under it: a request decided, cancelled or changed
// meanwhile is absence_not_requested, so of two deciders at once the second
// is told the first won. A decision's status never crosses the line between
// a request and a registration (absence_status_change, Task 3). The mails
// go after commit (notifications.go).

// decisionSchema is the contract's name for a decision's body.
const decisionSchema = "StaffAbsenceDecisionRequest"

// invalidDecisionTitle is the title of a decision's 400.
const invalidDecisionTitle = "The decision is not valid"

// maxDecisionNote is a decision note's length in characters.
const maxDecisionNote = 500

// refuseForbidden is a 403 carrying a problem: who the caller is to the
// person refuses them, which a code says.
func refuseForbidden(code, title, detail string) error {
	p := conflict(code, title, detail)
	status := int32(http.StatusForbidden)
	p.Status = &status
	return &refusal{status: http.StatusForbidden, problem: p}
}

// refusal is d's answer to a decision: nil when the caller decides,
// own_request on their own, decision_not_yours otherwise.
func (d decider) refusal() error {
	switch {
	case d.decides:
		return nil
	case d.own:
		return refuseForbidden(codeOwnRequest, "Your own absence",
			"Nobody decides their own absence: your manager, or someone who decides absence for everyone, does.")
	}
	return refuseForbidden(codeDecisionNotYours, "Not yours to decide",
		"Only the person's manager today, staff:approve or staff:manage decides this absence.")
}

// notCancellable is the 409 of a cancel or a correction of a row that is not
// one the caller may cancel or correct.
func notCancellable(detail string) error {
	return refuseConflict(conflict(codeAbsenceNotCancellable, "Not cancellable", detail))
}

// parseDecisionNote reads a decision's note: trimmed, at most 500
// characters, and required on a rejection.
func parseDecisionNote(body *gen.StaffAbsenceDecisionRequest, required bool) (*string, fieldErrors) {
	var f fieldErrors
	var note *string
	if body != nil && body.Note != nil {
		if v := strings.TrimSpace(*body.Note); v != "" {
			note = &v
		}
	}
	switch {
	case note == nil && required:
		f.add("note", "A rejection needs its reason")
	case note != nil && utf8.RuneCountInString(*note) > maxDecisionNote:
		f.add("note", "At most 500 characters")
	}
	return note, f
}

// deciderOn reads absence id as the caller may see it — notFound otherwise —
// and what the caller is to its person's requests.
func (s *server) deciderOn(ctx context.Context, caller *callerContext, id int64) (store.GetAbsenceRow, decider, error) {
	row, _, err := readAbsenceFor(ctx, store.New(s.deps.Pool), caller, id)
	if err != nil {
		return row, decider{}, err
	}
	d, err := s.judgeDecider(ctx, caller, row.StaffAbsence.PersonID)
	return row, d, err
}

// decide records status — approved or rejected — on request id of
// personID's under the person's lock, with the caller as decider and note.
// An approval judges the row's overlap again (reading 17) and answers the
// accounts after it.
func (s *server) decide(ctx context.Context, caller *callerContext, who actor, personID, id int64, d decider, status string, note *string) (absenceWrite, error) {
	var w absenceWrite
	err := s.withPersonLock(ctx, personID, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		before, t, err := lockAbsence(ctx, txq, id, personID)
		if err != nil {
			return err
		}
		if before.Status != absenceRequested {
			return refuseConflict(conflict(codeAbsenceNotRequested, "No longer a request",
				"This absence is no longer a request: it was decided, cancelled or removed meanwhile."))
		}
		if status == absenceApproved {
			p := parsedAbsence{TypeID: t.ID, First: civil(before.FirstDay), Last: civil(before.LastDay),
				Percent: before.Percent, Hours: before.Hours, Degree: before.DegreePercent}
			if err := judgeOverlap(ctx, txq, personID, before.ID, p, t); err != nil {
				return err
			}
		}
		self := d.selfApproves && status == absenceApproved
		userID := caller.UserID
		row, err := txq.DecideAbsence(ctx, store.DecideAbsenceParams{
			ID: before.ID, Status: status, DecidedByUserID: &userID, DecisionNote: note, SelfApproved: self, Now: s.deps.Clock(),
		})
		if err != nil {
			return err
		}
		kind := eventAbsenceRejected
		switch {
		case self:
			kind = eventAbsenceSelfApproved
		case status == absenceApproved:
			kind = eventAbsenceApproved
		}
		if err := s.recordAbsence(ctx, txq, personID, kind, who, row.ID, &before, &row, t); err != nil {
			return err
		}
		w = absenceWrite{row: row, typ: t}
		if status == absenceApproved {
			if w.accounts, err = s.accountsAfter(ctx, txq, caller.Settings, t, row); err != nil {
				return err
			}
		}
		w.audience, err = personAudience(ctx, txq, caller, personID)
		return err
	})
	return w, err
}

// decision is the approval's and the rejection's shared path: the body, the
// caller's right to decide, the write and the mail. It answers the row, or
// the refusal's status and problem (0 for a bare 404).
func (s *server) decision(ctx context.Context, id int64, body *gen.StaffAbsenceDecisionRequest, status string) (gen.StaffAbsence, *refusal, error) {
	if f := s.unknownProperties(ctx, decisionSchema); f.failed() {
		return gen.StaffAbsence{}, &refusal{status: http.StatusBadRequest, problem: invalid(invalidDecisionTitle, f)}, nil
	}
	note, f := parseDecisionNote(body, status == absenceRejected)
	if f.failed() {
		return gen.StaffAbsence{}, &refusal{status: http.StatusBadRequest, problem: invalid(invalidDecisionTitle, f)}, nil
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return gen.StaffAbsence{}, nil, err
	}
	current, d, err := s.deciderOn(ctx, caller, id)
	if err == nil {
		err = d.refusal()
	}
	if r, ok := asRefusal(err); ok {
		return gen.StaffAbsence{}, r, nil
	}
	if err != nil {
		return gen.StaffAbsence{}, nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return gen.StaffAbsence{}, nil, err
	}
	personID := current.StaffAbsence.PersonID
	w, err := s.decide(ctx, caller, who, personID, id, d, status, note)
	if errors.Is(err, errPersonNotFound) {
		return gen.StaffAbsence{}, &refusal{status: http.StatusNotFound}, nil
	}
	if r, ok := asRefusal(err); ok {
		return gen.StaffAbsence{}, r, nil
	}
	if err != nil {
		return gen.StaffAbsence{}, nil, err
	}
	if !w.row.SelfApproved {
		s.notifyAbsence(ctx, caller.Today, noticeOf(status, w.row, w.typ))
	}
	return s.respondWrite(w, personID), nil, nil
}

// PostStaffAbsencesByIdApprove Approve an absence request
// (POST /api/v1/staff/absences/{id}/approve)
func (s *server) PostStaffAbsencesByIdApprove(ctx context.Context, req gen.PostStaffAbsencesByIdApproveRequestObject) (gen.PostStaffAbsencesByIdApproveResponseObject, error) {
	out, r, err := s.decision(ctx, req.Id, req.Body, absenceApproved)
	if err != nil {
		return nil, err
	}
	if r != nil {
		switch r.status {
		case http.StatusBadRequest:
			return gen.PostStaffAbsencesByIdApprove400ApplicationProblemPlusJSONResponse(r.problem), nil
		case http.StatusForbidden:
			return gen.PostStaffAbsencesByIdApprove403ApplicationProblemPlusJSONResponse(r.problem), nil
		case http.StatusConflict:
			return gen.PostStaffAbsencesByIdApprove409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PostStaffAbsencesByIdApprove404Response{}, nil
	}
	return gen.PostStaffAbsencesByIdApprove200JSONResponse(out), nil
}

// PostStaffAbsencesByIdReject Reject an absence request
// (POST /api/v1/staff/absences/{id}/reject)
func (s *server) PostStaffAbsencesByIdReject(ctx context.Context, req gen.PostStaffAbsencesByIdRejectRequestObject) (gen.PostStaffAbsencesByIdRejectResponseObject, error) {
	out, r, err := s.decision(ctx, req.Id, req.Body, absenceRejected)
	if err != nil {
		return nil, err
	}
	if r != nil {
		switch r.status {
		case http.StatusBadRequest:
			return gen.PostStaffAbsencesByIdReject400ApplicationProblemPlusJSONResponse(r.problem), nil
		case http.StatusForbidden:
			return gen.PostStaffAbsencesByIdReject403ApplicationProblemPlusJSONResponse(r.problem), nil
		case http.StatusConflict:
			return gen.PostStaffAbsencesByIdReject409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PostStaffAbsencesByIdReject404Response{}, nil
	}
	return gen.PostStaffAbsencesByIdReject200JSONResponse(out), nil
}

// PostStaffAbsencesByIdCancel Cancel an absence
// (POST /api/v1/staff/absences/{id}/cancel)
//
// A request is cancelled by its person or by HR (Task 3); an approved row by
// a decider of the person today — never one's own but the self-approver's —
// and, once its first day has come, by staff:manage alone (a decided absence
// in the past is HR's, S12). Anything else is absence_not_cancellable.
func (s *server) PostStaffAbsencesByIdCancel(ctx context.Context, req gen.PostStaffAbsencesByIdCancelRequestObject) (gen.PostStaffAbsencesByIdCancelResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	current, d, err := s.deciderOn(ctx, caller, req.Id)
	if _, ok := asRefusal(err); ok {
		return gen.PostStaffAbsencesByIdCancel404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	personID := current.StaffAbsence.PersonID
	self := caller.Person != nil && *caller.Person == personID
	if current.StaffAbsence.Status == absenceRequested && !self && !caller.Manage {
		return gen.PostStaffAbsencesByIdCancel403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var w absenceWrite
	decided := false
	err = s.withPersonLock(ctx, personID, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		before, t, err := lockAbsence(ctx, txq, req.Id, personID)
		if err != nil {
			return err
		}
		switch before.Status {
		case absenceRequested:
			if !self && !caller.Manage {
				return notCancellable("Only its person or HR cancels a request.")
			}
		case absenceApproved:
			if self && !caller.Approve && !caller.Manage {
				return notCancellable("A decided absence is cancelled by whoever decides it: ask your manager.")
			}
			if err := d.refusal(); err != nil {
				return err
			}
			if civil(before.FirstDay).Before(caller.Today) && !caller.Manage {
				return notCancellable("This absence has begun: only HR cancels or corrects it now.")
			}
			decided = true
		default:
			return notCancellable("Only a request, or an approved absence by its decider, is cancelled; this absence is neither.")
		}
		row, err := txq.SetAbsenceStatus(ctx, store.SetAbsenceStatusParams{ID: before.ID, Status: absenceCancelled, Now: s.deps.Clock()})
		if err != nil {
			return err
		}
		if err := s.recordAbsence(ctx, txq, personID, eventAbsenceCancelled, who, row.ID, &before, &row, t); err != nil {
			return err
		}
		w = absenceWrite{row: row, typ: t}
		w.audience, err = personAudience(ctx, txq, caller, personID)
		return err
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffAbsencesByIdCancel404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusConflict:
			return gen.PostStaffAbsencesByIdCancel409ApplicationProblemPlusJSONResponse(r.problem), nil
		case http.StatusForbidden:
			return gen.PostStaffAbsencesByIdCancel403ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PostStaffAbsencesByIdCancel404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	if decided && !self {
		s.notifyAbsence(ctx, caller.Today, noticeOf(noticeCancelled, w.row, w.typ))
	}
	return gen.PostStaffAbsencesByIdCancel200JSONResponse(s.respondWrite(w, personID)), nil
}

// PostStaffAbsencesByIdCorrect Correct a decided or registered absence
// (POST /api/v1/staff/absences/{id}/correct)
//
// HR's (the router holds staff:manage): an approved or registered row's
// type, days, percent, hours, degree and note, judged as a new row is — a
// request, a rejected or a cancelled row is absence_not_cancellable
// (pre-flight M7) — keeping its status, so never across the line between a
// request and a registration; never HR's own row but the self-approver's.
func (s *server) PostStaffAbsencesByIdCorrect(ctx context.Context, req gen.PostStaffAbsencesByIdCorrectRequestObject) (gen.PostStaffAbsencesByIdCorrectResponseObject, error) {
	var body gen.StaffAbsenceChangeRequest
	if req.Body != nil {
		body = *req.Body
	}
	match, ok := parseAbsenceIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PostStaffAbsencesByIdCorrect400ApplicationProblemPlusJSONResponse(invalid(invalidAbsenceTitle, badIfMatch())), nil
	}
	if f := s.unknownProperties(ctx, absenceChangeSchema); f.failed() {
		return gen.PostStaffAbsencesByIdCorrect400ApplicationProblemPlusJSONResponse(invalid(invalidAbsenceTitle, f)), nil
	}
	p, f := parseAbsence(fieldsOfChange(body))
	if f.failed() {
		return gen.PostStaffAbsencesByIdCorrect400ApplicationProblemPlusJSONResponse(invalid(invalidAbsenceTitle, f)), nil
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	current, d, err := s.deciderOn(ctx, caller, req.Id)
	if err == nil {
		err = d.refusal()
	}
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusForbidden {
			return gen.PostStaffAbsencesByIdCorrect403ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PostStaffAbsencesByIdCorrect404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	personID := current.StaffAbsence.PersonID
	var w absenceWrite
	err = s.withPersonLock(ctx, personID, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		before, beforeType, err := lockAbsence(ctx, txq, req.Id, personID)
		if err != nil {
			return err
		}
		if err := s.checkAbsenceIfMatch(before, match, writerAudience(caller, personID)); err != nil {
			return err
		}
		if before.Status != absenceApproved && before.Status != absenceRegistered {
			return notCancellable("Only an approved or a registered absence is corrected; a request is changed, and a rejected or cancelled one stays as it was.")
		}
		p, t, err := judgeAbsence(ctx, txq, personID, before.ID, p)
		if err != nil {
			return err
		}
		if (statusFor(t) == absenceRegistered) != (before.Status == absenceRegistered) {
			return refuseConflict(conflict(codeAbsenceStatusChange, "Another kind of absence",
				"A correction keeps a decided absence decided and a registration a registration: a type that needs a decision and one that is a notification are not swapped."))
		}
		row, err := txq.UpdateAbsence(ctx, store.UpdateAbsenceParams{
			ID: before.ID, TypeID: t.ID, FirstDay: dayParam(p.First), LastDay: dayParam(p.Last), Hours: p.Hours,
			Percent: p.Percent, DegreePercent: p.Degree, Note: p.Note, Status: before.Status, Now: s.deps.Clock(),
		})
		if err != nil {
			return err
		}
		if err := s.recordAbsence(ctx, txq, personID, eventAbsenceCorrected, who, row.ID, &before, &row, beforeType, t); err != nil {
			return err
		}
		w = absenceWrite{row: row, typ: t}
		if w.accounts, err = s.accountsAfter(ctx, txq, caller.Settings, t, row); err != nil {
			return err
		}
		w.audience, err = personAudience(ctx, txq, caller, personID)
		return err
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffAbsencesByIdCorrect404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PostStaffAbsencesByIdCorrect404Response{}, nil
		case http.StatusConflict:
			return gen.PostStaffAbsencesByIdCorrect409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PostStaffAbsencesByIdCorrect400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	if !d.own {
		s.notifyAbsence(ctx, caller.Today, noticeOf(noticeCorrected, w.row, w.typ))
	}
	return gen.PostStaffAbsencesByIdCorrect200JSONResponse(s.respondWrite(w, personID)), nil
}
