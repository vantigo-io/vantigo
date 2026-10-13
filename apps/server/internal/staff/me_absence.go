package staff

import (
	"context"
	"errors"
	"net/http"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is My page's absence (design §6 Me, S17): the caller's own
// absences and holiday account, through their self-service link, read as the
// person reads themselves (selfAudience) — and their own request or
// registration, POST /absences for their own person. A caller linked to
// nobody gets no_person.

// GetStaffMeAbsences List my own absences
// (GET /api/v1/staff/me/absences)
func (s *server) GetStaffMeAbsences(ctx context.Context, req gen.GetStaffMeAbsencesRequestObject) (gen.GetStaffMeAbsencesResponseObject, error) {
	prm := req.Params
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if caller.Person == nil {
		return gen.GetStaffMeAbsences404ApplicationProblemPlusJSONResponse(noPerson()), nil
	}
	filter, f := parseAbsenceList(caller.Person, prm.From, prm.To, prm.Status, prm.Page, prm.PageSize)
	if f.failed() {
		return gen.GetStaffMeAbsences400ApplicationProblemPlusJSONResponse(invalid(invalidListTitle, f)), nil
	}
	var out gen.PaginatedResponseOfStaffAbsence
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		out, err = s.listAbsences(ctx, q, filter, false, []int64{*caller.Person}, nil, func(int64) audience { return selfAudience })
		return err
	})
	if err != nil {
		return nil, err
	}
	return gen.GetStaffMeAbsences200JSONResponse(out), nil
}

// PostStaffMeAbsences Request or register my own absence
// (POST /api/v1/staff/me/absences)
func (s *server) PostStaffMeAbsences(ctx context.Context, req gen.PostStaffMeAbsencesRequestObject) (gen.PostStaffMeAbsencesResponseObject, error) {
	var body gen.StaffAbsenceChangeRequest
	if req.Body != nil {
		body = *req.Body
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if caller.Person == nil {
		return gen.PostStaffMeAbsences404ApplicationProblemPlusJSONResponse(noPerson()), nil
	}
	if f := s.unknownProperties(ctx, absenceChangeSchema); f.failed() {
		return gen.PostStaffMeAbsences400ApplicationProblemPlusJSONResponse(invalid(invalidAbsenceTitle, f)), nil
	}
	p, f := parseAbsence(fieldsOfChange(body))
	if f.failed() {
		return gen.PostStaffMeAbsences400ApplicationProblemPlusJSONResponse(invalid(invalidAbsenceTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	w, err := s.createAbsence(ctx, caller, who, *caller.Person, p)
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffMeAbsences404ApplicationProblemPlusJSONResponse(noPerson()), nil
	}
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusConflict {
			return gen.PostStaffMeAbsences409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PostStaffMeAbsences400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	w.audience = selfAudience
	return gen.PostStaffMeAbsences201JSONResponse(respondWrite(w, *caller.Person)), nil
}

// GetStaffMeHolidayAccount Get my own holiday account
// (GET /api/v1/staff/me/holiday-account)
func (s *server) GetStaffMeHolidayAccount(ctx context.Context, req gen.GetStaffMeHolidayAccountRequestObject) (gen.GetStaffMeHolidayAccountResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if caller.Person == nil {
		return gen.GetStaffMeHolidayAccount404ApplicationProblemPlusJSONResponse(noPerson()), nil
	}
	year, f := parseAccountYear(req.Params.Year, caller.Today)
	if f.failed() {
		return gen.GetStaffMeHolidayAccount400ApplicationProblemPlusJSONResponse(invalid(invalidAccountTitle, f)), nil
	}
	out, err := s.readAccount(ctx, caller.Settings, *caller.Person, year)
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffMeHolidayAccount404ApplicationProblemPlusJSONResponse(noPerson()), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffMeHolidayAccount200JSONResponse(out), nil
}
