package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the holiday account read from the database (design S9, §4)
// and HR's changes to it: the adjustments — a correction, or a carry-over
// from an earlier year — and the carry-over of a year's untaken days, at
// most one per person and year it comes from (plan pre-flight I8), judged
// under the person's lock and held by ux_holiday_adjustments_carry_over.
// holiday_account.go is the arithmetic.

// carryOverIndex is the unique index on a person's carry-over from a year.
const carryOverIndex = "ux_holiday_adjustments_carry_over"

// The bounds of an adjustment and a year (plan "Exact values").
const (
	adjustmentMaxTenths = 3660
	adjustmentNoteMax   = 500
	accountYearMin      = 2000
	accountYearMax      = 2100
)

// adjustmentReasons are an adjustment's reasons.
var adjustmentReasons = []string{reasonAgreedCarryOver, reasonForcedCarryOver, reasonCorrection}

// readAccountInput reads everything personID's account is derived from:
// their birth date, their employments with their schemes (the default for
// one naming none), their rows of the types drawing on the account, their
// adjustments, and — when automatic carry-over is on — the day it was.
func readAccountInput(ctx context.Context, q *store.Queries, settings store.StaffSetting, personID int64) (accountInput, error) {
	var in accountInput
	person, err := readPerson(ctx, q, personID)
	if err != nil {
		return in, err
	}
	in.BirthDate = civilPtr(person.BirthDate)
	schemes, err := q.ListHolidaySchemes(ctx)
	if err != nil {
		return in, fmt.Errorf("staff: read the holiday schemes: %w", err)
	}
	byID := map[int64]accountScheme{}
	var fallback accountScheme
	for _, s := range schemes {
		a := accountScheme{
			Entitlement: int(s.EntitlementVirkedager), ExtraFrom60: int(s.ExtraFrom60Virkedager),
			AfterSeptember: int(s.AfterSeptemberVirkedager), MaxCarryOver: int(s.MaxCarryOverVirkedager),
		}
		byID[s.ID] = a
		if s.IsDefault {
			fallback = a
		}
	}
	employments, err := q.ListPersonEmployments(ctx, personID)
	if err != nil {
		return in, fmt.Errorf("staff: read person %d's employments: %w", personID, err)
	}
	for _, e := range employments {
		scheme := fallback
		if e.HolidaySchemeID != nil {
			scheme = byID[*e.HolidaySchemeID]
		}
		in.Employments = append(in.Employments, accountEmployment{Start: civil(e.StartDate), End: civilPtr(e.EndDate), Scheme: scheme})
	}
	rows, err := q.HolidayAbsences(ctx, personID)
	if err != nil {
		return in, fmt.Errorf("staff: read person %d's holiday: %w", personID, err)
	}
	for _, r := range rows {
		in.Absences = append(in.Absences, accountAbsence{
			First: civil(r.FirstDay), Last: civil(r.LastDay), Status: r.Status, Share: shareOf(r.Kind, r.Percent, r.DegreePercent),
		})
	}
	adjustments, err := q.ListHolidayAdjustments(ctx, personID)
	if err != nil {
		return in, fmt.Errorf("staff: read person %d's holiday adjustments: %w", personID, err)
	}
	for _, a := range adjustments {
		in.Adjustments = append(in.Adjustments, accountAdjustment{
			Year: int(a.Year), Tenths: hundredthsOfNumeric(a.Days) / 10, Reason: a.Reason, FromYear: intOf(a.FromYear),
		})
	}
	if settings.AutoCarryOver && settings.AutoCarryOverSince.Valid {
		in.AutoCarryOverSince = civilPtr(settings.AutoCarryOverSince)
	}
	return in, nil
}

// intOf is a nullable smallint as an int.
func intOf(v *int16) *int {
	if v == nil {
		return nil
	}
	out := int(*v)
	return &out
}

// daysOf is an amount in the account's unit as the contract's number of
// days.
func daysOf(units int64) float64 { return float64(units) / dayUnit }

// holidayAccountResponse is one year's account as the contract answers it.
func holidayAccountResponse(personID int64, a holidayAccount) gen.StaffHolidayAccount {
	return gen.StaffHolidayAccount{
		PersonId: personID, Year: int32(a.Year), Entitlement: daysOf(a.Entitlement), CarriedIn: daysOf(a.CarriedIn),
		CarriedInDerived: daysOf(a.CarriedInDerived), Taken: daysOf(a.Taken), Pending: daysOf(a.Pending),
		Remaining: daysOf(a.Remaining), Overdrawn: a.Overdrawn,
	}
}

// parseAccountYear is the year a read asks for: this year when left out,
// else 2000 to 2100.
func parseAccountYear(year *int32, today time.Time) (int, fieldErrors) {
	var f fieldErrors
	if year == nil {
		return today.Year(), f
	}
	if *year < accountYearMin || *year > accountYearMax {
		f.add("year", fmt.Sprintf("A year is %d to %d", accountYearMin, accountYearMax))
	}
	return int(*year), f
}

// readAccount is personID's account for year, read in one snapshot.
func (s *server) readAccount(ctx context.Context, settings store.StaffSetting, personID int64, year int) (gen.StaffHolidayAccount, error) {
	var out gen.StaffHolidayAccount
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		in, err := readAccountInput(ctx, q, settings, personID)
		if err != nil {
			return err
		}
		out = holidayAccountResponse(personID, accountFor(in, year))
		return nil
	})
	return out, err
}

// GetStaffPeopleByIdHolidayAccount Get a person's holiday account
// (GET /api/v1/staff/people/{id}/holiday-account)
//
// The person, a manager of theirs today and HR read it — the readers of the
// absence detail group; to anyone else it is the 404 an unknown person is.
func (s *server) GetStaffPeopleByIdHolidayAccount(ctx context.Context, req gen.GetStaffPeopleByIdHolidayAccountRequestObject) (gen.GetStaffPeopleByIdHolidayAccountResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	year, f := parseAccountYear(req.Params.Year, caller.Today)
	if f.failed() {
		return gen.GetStaffPeopleByIdHolidayAccount400ApplicationProblemPlusJSONResponse(invalid(invalidAccountTitle, f)), nil
	}
	q := store.New(s.deps.Pool)
	if _, err := readPerson(ctx, q, req.Id); err != nil {
		if _, ok := asRefusal(err); ok {
			return gen.GetStaffPeopleByIdHolidayAccount404Response{}, nil
		}
		return nil, err
	}
	a, err := personAudience(ctx, q, caller, req.Id)
	if err != nil {
		return nil, err
	}
	if !a.reads(groupAbsenceDetail) {
		return gen.GetStaffPeopleByIdHolidayAccount404Response{}, nil
	}
	out, err := s.readAccount(ctx, caller.Settings, req.Id, year)
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdHolidayAccount404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdHolidayAccount200JSONResponse(out), nil
}

// adjustmentResponse is one adjustment as the contract answers it.
func adjustmentResponse(a store.StaffHolidayAdjustment) gen.StaffHolidayAdjustment {
	var from *int32
	if a.FromYear != nil {
		v := int32(*a.FromYear)
		from = &v
	}
	return gen.StaffHolidayAdjustment{
		Id: a.ID, PersonId: a.PersonID, Year: int32(a.Year), Days: float64(hundredthsOfNumeric(a.Days)) / 100,
		Reason: gen.StaffHolidayAdjustmentReason(a.Reason), FromYear: from, Note: a.Note, CreatedAt: a.CreatedAt,
	}
}

// adjustmentValues is an adjustment's fields as its events name them.
func adjustmentValues(a store.StaffHolidayAdjustment) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	put := func(name string, v any) {
		b, _ := json.Marshal(v)
		out[name] = b
	}
	put("year", a.Year)
	put("days", float64(hundredthsOfNumeric(a.Days))/100)
	put("reason", a.Reason)
	if a.FromYear != nil {
		put("fromYear", *a.FromYear)
	}
	if a.Note != nil {
		put("note", *a.Note)
	}
	return out
}

// recordAdjustment records kind about adjustment a, created (before nil) or
// deleted (after nil).
func (s *server) recordAdjustment(ctx context.Context, txq *store.Queries, kind string, who actor, before, after *store.StaffHolidayAdjustment) error {
	var b, a map[string]json.RawMessage
	var id, personID int64
	if before != nil {
		b, id, personID = adjustmentValues(*before), before.ID, before.PersonID
	}
	if after != nil {
		a, id, personID = adjustmentValues(*after), after.ID, after.PersonID
	}
	payload, classes := adjustmentEventPayload(id, b, a)
	return s.recordClassedEvent(ctx, txq, personID, kind, who, payload, classes)
}

// carryOverRecorded is the 409 of a second carry-over from one year.
func carryOverRecorded(fromYear int) error {
	return refuseConflict(conflict(codeCarryOverRecorded, "Carry-over recorded",
		fmt.Sprintf("A carry-over from %d is already recorded for this person. Delete it first to record another.", fromYear)))
}

// insertAdjustment writes one adjustment under the person's lock: a
// carry-over only when none from its year is recorded (asked, then held by
// the unique index, whose violation answers the same).
func (s *server) insertAdjustment(ctx context.Context, txq *store.Queries, params store.InsertHolidayAdjustmentParams, kind string, who actor) (store.StaffHolidayAdjustment, error) {
	if carryOverReason(params.Reason) && params.FromYear != nil {
		recorded, err := txq.CarryOverRecorded(ctx, store.CarryOverRecordedParams{PersonID: params.PersonID, FromYear: *params.FromYear})
		if err != nil {
			return store.StaffHolidayAdjustment{}, fmt.Errorf("staff: read person %d's carry-over: %w", params.PersonID, err)
		}
		if recorded {
			return store.StaffHolidayAdjustment{}, carryOverRecorded(int(*params.FromYear))
		}
	}
	row, err := txq.InsertHolidayAdjustment(ctx, params)
	if db.IsUniqueViolation(err, carryOverIndex) {
		// The index's backstop: reached only if a carry-over was written
		// past the person's lock. It answers the same code, in words of its
		// own, so a test tells it from the lock-time check.
		return row, refuseConflict(conflict(codeCarryOverRecorded, "Carry-over recorded",
			fmt.Sprintf("Another carry-over from %d was written at the same moment; the index refused this one.", *params.FromYear)))
	}
	if err != nil {
		return row, fmt.Errorf("staff: write person %d's holiday adjustment: %w", params.PersonID, err)
	}
	return row, s.recordAdjustment(ctx, txq, kind, who, nil, &row)
}

// adjustmentNote is a note an adjustment carries: at most 500 characters,
// required when need.
func adjustmentNote(raw *string, need bool, f *fieldErrors) *string {
	note, msg := optionalText("A note", raw, adjustmentNoteMax)
	f.add("note", msg)
	if need && msg == "" && (note == nil || strings.TrimSpace(*note) == "") {
		f.add("note", "A forced carry-over and a correction say why, in a note")
	}
	return note
}

// GetStaffPeopleByIdHolidayAdjustments List a person's holiday adjustments
// (GET /api/v1/staff/people/{id}/holiday-adjustments)
func (s *server) GetStaffPeopleByIdHolidayAdjustments(ctx context.Context, req gen.GetStaffPeopleByIdHolidayAdjustmentsRequestObject) (gen.GetStaffPeopleByIdHolidayAdjustmentsResponseObject, error) {
	var rows []store.StaffHolidayAdjustment
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := readPerson(ctx, q, req.Id); err != nil {
			return err
		}
		var err error
		if rows, err = q.ListHolidayAdjustments(ctx, req.Id); err != nil {
			return fmt.Errorf("staff: list person %d's holiday adjustments: %w", req.Id, err)
		}
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdHolidayAdjustments404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdHolidayAdjustments200JSONResponse(mapRows(rows, adjustmentResponse)), nil
}

// PostStaffPeopleByIdHolidayAdjustments Adjust a person's holiday account
// (POST /api/v1/staff/people/{id}/holiday-adjustments)
func (s *server) PostStaffPeopleByIdHolidayAdjustments(ctx context.Context, req gen.PostStaffPeopleByIdHolidayAdjustmentsRequestObject) (gen.PostStaffPeopleByIdHolidayAdjustmentsResponseObject, error) {
	var body gen.StaffHolidayAdjustmentRequest
	if req.Body != nil {
		body = *req.Body
	}
	var f fieldErrors
	if body.Year < accountYearMin || body.Year > accountYearMax {
		f.add("year", fmt.Sprintf("A year is %d to %d", accountYearMin, accountYearMax))
	}
	tenths, ok := tenthsOf(body.Days)
	if !ok || tenths == 0 || tenths < -adjustmentMaxTenths || tenths > adjustmentMaxTenths {
		f.add("days", "Days are -366 to 366 with at most one decimal, and not zero")
	}
	reason := string(body.Reason)
	if _, msg := parseEnum("A reason", &reason, adjustmentReasons); msg != "" {
		f.add("reason", msg)
	}
	var fromYear *int16
	switch {
	case carryOverReason(reason) && body.FromYear == nil:
		f.add("fromYear", "A carry-over names the year it comes from")
	case carryOverReason(reason) && *body.FromYear != body.Year-1:
		f.add("fromYear", "A carry-over comes from the year just before the one it changes")
	case carryOverReason(reason):
		v := int16(*body.FromYear)
		fromYear = &v
	case body.FromYear != nil:
		f.add("fromYear", "Only a carry-over names a year it comes from")
	}
	note := adjustmentNote(body.Note, reason != reasonAgreedCarryOver, &f)
	if f.failed() {
		return gen.PostStaffPeopleByIdHolidayAdjustments400ApplicationProblemPlusJSONResponse(invalid(invalidAdjustmentTitle, f)), nil
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var saved store.StaffHolidayAdjustment
	err = s.withPersonLock(ctx, req.Id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var err error
		saved, err = s.insertAdjustment(ctx, txq, store.InsertHolidayAdjustmentParams{
			PersonID: req.Id, Year: int16(body.Year), Days: numericOfTenths(tenths), Reason: reason, FromYear: fromYear,
			Note: note, ByUserID: caller.UserID, Now: s.deps.Clock(),
		}, eventHolidayAdjusted, who)
		return err
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPeopleByIdHolidayAdjustments404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		return gen.PostStaffPeopleByIdHolidayAdjustments409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PostStaffPeopleByIdHolidayAdjustments201JSONResponse(adjustmentResponse(saved)), nil
}

// DeleteStaffHolidayAdjustmentsById Delete a holiday adjustment
// (DELETE /api/v1/staff/holiday-adjustments/{id})
func (s *server) DeleteStaffHolidayAdjustmentsById(ctx context.Context, req gen.DeleteStaffHolidayAdjustmentsByIdRequestObject) (gen.DeleteStaffHolidayAdjustmentsByIdResponseObject, error) {
	current, err := store.New(s.deps.Pool).GetHolidayAdjustment(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteStaffHolidayAdjustmentsById404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("staff: read holiday adjustment %d: %w", req.Id, err)
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	err = s.withPersonLock(ctx, current.PersonID, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		row, err := txq.LockHolidayAdjustment(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock holiday adjustment %d: %w", req.Id, err)
		}
		if err := txq.DeleteHolidayAdjustment(ctx, row.ID); err != nil {
			return fmt.Errorf("staff: delete holiday adjustment %d: %w", row.ID, err)
		}
		return s.recordAdjustment(ctx, txq, eventHolidayAdjustmentDeleted, who, &row, nil)
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.DeleteStaffHolidayAdjustmentsById404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusConflict {
			return gen.DeleteStaffHolidayAdjustmentsById409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.DeleteStaffHolidayAdjustmentsById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.DeleteStaffHolidayAdjustmentsById204Response{}, nil
}

// PostStaffPeopleByIdCarryOver Carry holiday over
// (POST /api/v1/staff/people/{id}/carry-over)
//
// Under the person's lock: no carry-over from fromYear recorded yet; the days
// the request names, or fromYear's remaining rounded down to a tenth; an
// agreed one at most the scheme's cap (the default capped to it, more
// refused); a forced one uncapped, with its note.
func (s *server) PostStaffPeopleByIdCarryOver(ctx context.Context, req gen.PostStaffPeopleByIdCarryOverRequestObject) (gen.PostStaffPeopleByIdCarryOverResponseObject, error) {
	var body gen.StaffCarryOverRequest
	if req.Body != nil {
		body = *req.Body
	}
	var f fieldErrors
	if body.FromYear < accountYearMin || body.FromYear >= accountYearMax {
		f.add("fromYear", fmt.Sprintf("A year is %d to %d", accountYearMin, accountYearMax-1))
	}
	reason := map[gen.StaffCarryOverRequestKind]string{gen.StaffCarryOverKindAgreed: reasonAgreedCarryOver, gen.StaffCarryOverKindForced: reasonForcedCarryOver}[body.Kind]
	if reason == "" {
		f.add("kind", "A carry-over is agreed or forced")
	}
	var asked int64
	if body.Days != nil {
		t, ok := tenthsOf(*body.Days)
		if !ok || t <= 0 || t > adjustmentMaxTenths {
			f.add("days", "Days are above zero, at most 366, with at most one decimal")
		}
		asked = t
	}
	note := adjustmentNote(body.Note, reason == reasonForcedCarryOver, &f)
	if f.failed() {
		return gen.PostStaffPeopleByIdCarryOver400ApplicationProblemPlusJSONResponse(invalid(invalidCarryOverTitle, f)), nil
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	fromYear := int(body.FromYear)
	var saved store.StaffHolidayAdjustment
	err = s.withPersonLock(ctx, req.Id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		recorded, err := txq.CarryOverRecorded(ctx, store.CarryOverRecordedParams{PersonID: req.Id, FromYear: int16(fromYear)})
		if err != nil {
			return fmt.Errorf("staff: read person %d's carry-over: %w", req.Id, err)
		}
		if recorded {
			return carryOverRecorded(fromYear)
		}
		in, err := readAccountInput(ctx, txq, caller.Settings, req.Id)
		if err != nil {
			return err
		}
		tenths := asked
		limit := carryOverCap(in, fromYear) / (dayUnit / 10)
		if body.Days == nil {
			tenths = accountFor(in, fromYear).Remaining / (dayUnit / 10)
			if reason == reasonAgreedCarryOver && limit >= 0 {
				tenths = min(tenths, limit)
			}
		}
		var fe fieldErrors
		switch {
		case tenths <= 0:
			fe.add("days", fmt.Sprintf("Nothing of %d remains to carry over", fromYear))
		case reason == reasonAgreedCarryOver && limit >= 0 && tenths > limit:
			fe.add("days", fmt.Sprintf("An agreed carry-over is at most %s days under the person's holiday scheme; a forced one is not capped", formatTenths(limit)))
		}
		if fe.failed() {
			return refuseInvalid(invalidCarryOverTitle, fe)
		}
		from := int16(fromYear)
		saved, err = s.insertAdjustment(ctx, txq, store.InsertHolidayAdjustmentParams{
			PersonID: req.Id, Year: int16(fromYear + 1), Days: numericOfTenths(tenths), Reason: reason, FromYear: &from,
			Note: note, ByUserID: caller.UserID, Now: s.deps.Clock(),
		}, eventCarryOverRecorded, who)
		return err
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPeopleByIdCarryOver404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusConflict {
			return gen.PostStaffPeopleByIdCarryOver409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PostStaffPeopleByIdCarryOver400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PostStaffPeopleByIdCarryOver201JSONResponse(adjustmentResponse(saved)), nil
}

// formatTenths is tenths of a day as a number of days, one decimal at most.
func formatTenths(t int64) string {
	if t%10 == 0 {
		return fmt.Sprintf("%d", t/10)
	}
	return fmt.Sprintf("%d.%d", t/10, t%10)
}
