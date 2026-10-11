package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is an employment (design S2, §3.2, §6 Employments): one
// arbeidsforhold of a person, created by HR with its first terms row, read by
// everyone the person exists for — each reading the groups theirs to read —
// changed, ended and reopened by HR. Every write takes the person's row, then
// the employment's; one that can change who manages whom takes the manager
// graph's lock after both and judges the graph it leaves (org.go).

// employmentNumberIndex is the unique index that holds one employment per
// number, however it is cased.
const employmentNumberIndex = "ux_employments_number_upper"

// employmentNumberMax is the a-melding's arbeidsforholdID's width here.
const employmentNumberMax = 40

// ownEmploymentFields are an employment response's fields that are not data
// about the person: an event's payload leaves them out, and the derived dates
// with them, since they change with the terms, which have events of their own.
var ownEmploymentFields = []string{"id", "personId", "revision", "fteChangedAt", "salaryChangedAt"}

// parseEmploymentNumber is a typed employment number: 1 to 40 letters,
// digits or hyphens. Uniqueness, however it is cased, is the index's.
func parseEmploymentNumber(raw *string) (*string, string) {
	if raw == nil {
		return nil, ""
	}
	v := strings.TrimSpace(*raw)
	switch {
	case v == "":
		return nil, "An employment number cannot be blank; leave it out to have one generated"
	case utf8.RuneCountInString(v) > employmentNumberMax:
		return nil, fmt.Sprintf("An employment number cannot be longer than %d characters", employmentNumberMax)
	case !numberPattern.MatchString(v):
		return nil, "An employment number may hold only letters a–z, digits and hyphens"
	}
	return &v, ""
}

// employmentNumberTaken is the refusal of a number another employment has.
func employmentNumberTaken() error {
	return refuseInvalid(invalidEmploymentTitle, fieldErrors{
		errs: fieldError("employmentNumber", "Another employment already has this number, however it is cased"),
		code: codeEmploymentNumberTaken,
	})
}

// employmentWriteError is an employment write's own database refusal: a
// number another employment took between the check and the write is
// employment_number_taken, as if the check had seen it.
func employmentWriteError(err error) error {
	if db.IsUniqueViolation(err, employmentNumberIndex) {
		return employmentNumberTaken()
	}
	return err
}

// readEmployment is one employment on q, or the 404 refusal.
func readEmployment(ctx context.Context, q *store.Queries, id int64) (store.StaffEmployment, error) {
	row, err := q.GetEmployment(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, notFound()
	}
	if err != nil {
		return row, fmt.Errorf("staff: read employment %d: %w", id, err)
	}
	return row, nil
}

// lockEmployment takes an employment's row inside its person's lock, or the
// 404 refusal.
func lockEmployment(ctx context.Context, txq *store.Queries, id int64) (store.StaffEmployment, error) {
	row, err := txq.LockEmployment(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, notFound()
	}
	if err != nil {
		return row, fmt.Errorf("staff: lock employment %d: %w", id, err)
	}
	return row, nil
}

// chainOf is an employment's terms, oldest first.
func chainOf(ctx context.Context, q *store.Queries, employmentID int64) ([]store.StaffEmploymentTerm, error) {
	chain, err := q.ListTerms(ctx, employmentID)
	if err != nil {
		return nil, fmt.Errorf("staff: read employment %d's terms: %w", employmentID, err)
	}
	return chain, nil
}

// withEmploymentLock runs fn under the lock of the person an employment
// belongs to, then the employment's own row. The person is read first on the
// pool — an employment never changes person — and an employment that has
// gone by the time its row is locked is the 404.
func (s *server) withEmploymentLock(ctx context.Context, id int64, fn func(ctx context.Context, txq *store.Queries, e store.StaffEmployment) error) error {
	e, err := readEmployment(ctx, store.New(s.deps.Pool), id)
	if err != nil {
		return err
	}
	err = s.withPersonLock(ctx, e.PersonID, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		locked, err := lockEmployment(ctx, txq, id)
		if err != nil {
			return err
		}
		return fn(ctx, txq, locked)
	})
	if errors.Is(err, errPersonNotFound) {
		return notFound()
	}
	return err
}

// resolveUnit is the business unit an employment names: the default when
// it names none, else the one it names, which must exist and — unless the
// employment is already in it (current) — not be archived. The row is held
// against an archive until the transaction ends.
func resolveUnit(ctx context.Context, txq *store.Queries, id *int64, current int64) (int64, error) {
	if id == nil {
		unit, err := txq.DefaultBusinessUnit(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, refuseInvalid(invalidEmploymentTitle, fieldErrors{errs: fieldError("businessUnitId",
				"No business unit is the default yet; add one to the structure, or name one")})
		} else if err != nil {
			return 0, fmt.Errorf("staff: read the default business unit: %w", err)
		}
		return unit.ID, nil
	}
	unit, err := txq.ShareBusinessUnit(ctx, *id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, refuseInvalid(invalidEmploymentTitle, fieldErrors{errs: fieldError("businessUnitId", fmt.Sprintf("No business unit has id %d", *id))})
	} else if err != nil {
		return 0, fmt.Errorf("staff: read business unit %d: %w", *id, err)
	}
	if unit.ArchivedAt != nil && unit.ID != current {
		return 0, refuseConflict(structureArchived(fmt.Sprintf("Business unit %s is archived; an employment cannot be put in it.", unit.Name)))
	}
	return unit.ID, nil
}

// generateEmploymentNumber is the person's employee number, a hyphen and the
// first n from 1 that no employment has however it is cased — so a number
// typed by hand is skipped past. It runs under the person's lock, so two
// employments of one person are numbered one after the other.
func generateEmploymentNumber(ctx context.Context, txq *store.Queries, personID int64) (string, error) {
	person, err := readPerson(ctx, txq, personID)
	if err != nil {
		return "", err
	}
	n, err := txq.FirstFreeEmploymentNumber(ctx, person.EmployeeNumber)
	if err != nil {
		return "", fmt.Errorf("staff: find the next free employment number: %w", err)
	}
	return person.EmployeeNumber + "-" + strconv.FormatInt(n, 10), nil
}

// employmentValues is an employment's fields as an event's payload names
// them, nil for none.
func employmentValues(e *store.StaffEmployment) (map[string]json.RawMessage, error) {
	if e == nil {
		return nil, nil
	}
	return payloadValues(employmentResponse(*e, nil, time.Time{}, everything), ownEmploymentFields...)
}

// respondEmployment is an employment as the caller reads it, after any
// transaction: the audience resolved on the pool, the derived dates from the
// chain judged on today.
func (s *server) respondEmployment(ctx context.Context, caller *callerContext, e store.StaffEmployment) (gen.StaffEmployment, error) {
	q := store.New(s.deps.Pool)
	a, err := personAudience(ctx, q, caller, e.PersonID)
	if err != nil {
		return gen.StaffEmployment{}, err
	}
	chain, err := chainOf(ctx, q, e.ID)
	if err != nil {
		return gen.StaffEmployment{}, err
	}
	return employmentResponse(e, chain, caller.Today, a), nil
}

// GetStaffPeopleByIdEmployments List a person's employments
// (GET /api/v1/staff/people/{id}/employments)
func (s *server) GetStaffPeopleByIdEmployments(ctx context.Context, req gen.GetStaffPeopleByIdEmploymentsRequestObject) (gen.GetStaffPeopleByIdEmploymentsResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var out []gen.StaffEmployment
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := readPerson(ctx, q, req.Id); err != nil {
			return err
		}
		a, err := personAudience(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		if !a.seesPerson() {
			return notFound()
		}
		rows, err := q.ListPersonEmployments(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("staff: list person %d's employments: %w", req.Id, err)
		}
		ids := make([]int64, 0, len(rows))
		for _, e := range rows {
			ids = append(ids, e.ID)
		}
		terms, err := q.ListTermsOfEmployments(ctx, ids)
		if err != nil {
			return fmt.Errorf("staff: read person %d's terms: %w", req.Id, err)
		}
		chains := map[int64][]store.StaffEmploymentTerm{}
		for _, t := range terms {
			chains[t.EmploymentID] = append(chains[t.EmploymentID], t)
		}
		out = make([]gen.StaffEmployment, 0, len(rows))
		for _, e := range rows {
			out = append(out, employmentResponse(e, chains[e.ID], caller.Today, a))
		}
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdEmployments404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdEmployments200JSONResponse(out), nil
}

// PostStaffPeopleByIdEmployments Add an employment
// (POST /api/v1/staff/people/{id}/employments)
//
// The employment and its first terms row, dated the start, in one
// transaction under the person's lock; a first row that names a manager or a
// department takes the manager graph's lock and is judged on it from the
// start on.
func (s *server) PostStaffPeopleByIdEmployments(ctx context.Context, req gen.PostStaffPeopleByIdEmploymentsRequestObject) (gen.PostStaffPeopleByIdEmploymentsResponseObject, error) {
	var body gen.StaffEmploymentRequest
	if req.Body != nil {
		body = *req.Body
	}
	var f fieldErrors
	start := body.StartDate.Time
	if start.IsZero() {
		f.add("startDate", "An employment's start date is required")
	}
	number, msg := parseEmploymentNumber(body.EmploymentNumber)
	f.add("employmentNumber", msg)
	relationship := "ordinary"
	if v, msg := parseEnum("A relationship type", body.RelationshipType, relationshipTypes); msg != "" {
		f.add("relationshipType", msg)
	} else if v != nil {
		relationship = *v
	}
	if body.Terms.Correction != nil {
		f.add("terms.correction", "correction is for changing a terms row, not for a new one")
	}
	first, termsErrors := parseTerms(body.Terms, termsSource{given: func(string) bool { return true }}, start, "terms.")
	for field, messages := range termsErrors.errs {
		for _, m := range messages {
			f.add(field, m)
		}
	}
	if f.code == "" {
		f.code = termsErrors.code
	}
	if f.failed() {
		return gen.PostStaffPeopleByIdEmployments400ApplicationProblemPlusJSONResponse(invalid(invalidEmploymentTitle, f)), nil
	}
	if body.Terms.ValidFrom != nil && !civilDate(body.Terms.ValidFrom).Equal(start) {
		return gen.PostStaffPeopleByIdEmployments409ApplicationProblemPlusJSONResponse(conflict(codeTermsFirstRowStart, "The first row is dated the start",
			"An employment's first terms row takes effect on its start date; leave terms.validFrom out, or send the start.")), nil
	}
	first.ValidFrom = dayParam(start)
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}

	var created store.StaffEmployment
	err = s.withPersonLock(ctx, req.Id, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		edges := first.ManagerPersonID != nil || first.DepartmentID != nil
		if edges {
			if err := lockManagerGraph(ctx, txq); err != nil {
				return err
			}
		}
		unit, err := resolveUnit(ctx, txq, body.BusinessUnitId, 0)
		if err != nil {
			return err
		}
		if err := checkTermsReferences(ctx, txq, req.Id, first, nil, "terms."); err != nil {
			return err
		}
		employmentNumber := ""
		if number != nil {
			taken, err := txq.EmploymentNumberTaken(ctx, *number)
			if err != nil {
				return fmt.Errorf("staff: is employment number %s taken: %w", *number, err)
			}
			if taken {
				return employmentNumberTaken()
			}
			employmentNumber = *number
		} else if employmentNumber, err = generateEmploymentNumber(ctx, txq, req.Id); err != nil {
			return err
		}
		created, err = txq.InsertEmployment(ctx, store.InsertEmploymentParams{
			PersonID: req.Id, EmploymentNumber: employmentNumber, RelationshipType: relationship, BusinessUnitID: unit,
			StartDate: dayParam(start), ContractSignedAt: pgDate(civilDate(body.ContractSignedAt)), Now: s.deps.Clock(),
		})
		if err != nil {
			return employmentWriteError(fmt.Errorf("staff: add an employment to person %d: %w", req.Id, err))
		}
		first.EmploymentID = created.ID
		row, err := insertTerms(ctx, txq, first, s.deps.Clock())
		if err != nil {
			return err
		}
		if edges {
			if err := checkManagerCycle(ctx, txq, req.Id, start); err != nil {
				return err
			}
		}
		values, err := employmentValues(&created)
		if err != nil {
			return err
		}
		if err := s.recordRowChange(ctx, txq, req.Id, eventEmploymentCreated, who, created.ID, 0, diffFields(nil, values)); err != nil {
			return err
		}
		return s.recordTermsChange(ctx, txq, req.Id, eventTermsAdded, who, nil, &row)
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPeopleByIdEmployments404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PostStaffPeopleByIdEmployments404Response{}, nil
		case http.StatusConflict:
			return gen.PostStaffPeopleByIdEmployments409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PostStaffPeopleByIdEmployments400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, err
	}
	out, err := s.respondEmployment(ctx, caller, created)
	if err != nil {
		return nil, err
	}
	return gen.PostStaffPeopleByIdEmployments201JSONResponse(out), nil
}

// GetStaffEmploymentsById Get an employment
// (GET /api/v1/staff/employments/{id})
func (s *server) GetStaffEmploymentsById(ctx context.Context, req gen.GetStaffEmploymentsByIdRequestObject) (gen.GetStaffEmploymentsByIdResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var out gen.StaffEmployment
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		e, a, err := visibleEmployment(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		chain, err := chainOf(ctx, q, e.ID)
		if err != nil {
			return err
		}
		out = employmentResponse(e, chain, caller.Today, a)
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffEmploymentsById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffEmploymentsById200JSONResponse(out), nil
}

// visibleEmployment is an employment and the caller's audience on its
// person, or the 404 refusal when the caller may not see the person.
func visibleEmployment(ctx context.Context, q *store.Queries, caller *callerContext, id int64) (store.StaffEmployment, audience, error) {
	e, err := readEmployment(ctx, q, id)
	if err != nil {
		return e, audience{}, err
	}
	a, err := personAudience(ctx, q, caller, e.PersonID)
	if err != nil {
		return e, a, err
	}
	if !a.seesPerson() {
		return e, a, notFound()
	}
	return e, a, nil
}

// PutStaffEmploymentsById Change an employment
// (PUT /api/v1/staff/employments/{id})
//
// A new start moves the first terms row with it. An earlier start brings
// that row's edges into force on days they were not, so a start that moves
// takes the manager graph's lock and is judged on it.
func (s *server) PutStaffEmploymentsById(ctx context.Context, req gen.PutStaffEmploymentsByIdRequestObject) (gen.PutStaffEmploymentsByIdResponseObject, error) {
	var body gen.StaffEmploymentChangeRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffEmploymentsById400ApplicationProblemPlusJSONResponse(invalid(invalidEmploymentTitle, badIfMatch())), nil
	}
	var f fieldErrors
	start := body.StartDate.Time
	if start.IsZero() {
		f.add("startDate", "An employment's start date is required")
	}
	relationship, msg := parseEnum("A relationship type", &body.RelationshipType, relationshipTypes)
	f.add("relationshipType", msg)
	if f.failed() {
		return gen.PutStaffEmploymentsById400ApplicationProblemPlusJSONResponse(invalid(invalidEmploymentTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}

	var saved store.StaffEmployment
	err = s.withEmploymentLock(ctx, req.Id, func(ctx context.Context, txq *store.Queries, e store.StaffEmployment) error {
		if err := checkRevision("employment", e.Revision, revision); err != nil {
			return err
		}
		if body.EmploymentNumber != nil && strings.TrimSpace(*body.EmploymentNumber) != e.EmploymentNumber {
			return refuseInvalid(invalidEmploymentTitle, fieldErrors{errs: fieldError("employmentNumber",
				fmt.Sprintf("An employment number never changes; leave it out or send '%s'", e.EmploymentNumber))})
		}
		if e.EndDate.Valid && start.After(e.EndDate.Time) {
			return refuseInvalid(invalidEmploymentTitle, fieldErrors{errs: fieldError("startDate", "An employment starts on or before its end")})
		}
		chain, err := chainOf(ctx, txq, e.ID)
		if err != nil {
			return err
		}
		moves := !start.Equal(civil(e.StartDate))
		if moves && len(chain) > 1 && !civil(chain[1].ValidFrom).After(start) {
			return refuseConflict(conflict(codeTermsBeforeStart, "Terms before the start",
				fmt.Sprintf("A terms row takes effect on %s, which would lie before the new start.", civil(chain[1].ValidFrom).Format(time.DateOnly))))
		}
		if moves {
			if err := lockManagerGraph(ctx, txq); err != nil {
				return err
			}
		}
		unit, err := resolveUnit(ctx, txq, &body.BusinessUnitId, e.BusinessUnitID)
		if err != nil {
			return err
		}
		saved, err = txq.UpdateEmployment(ctx, store.UpdateEmploymentParams{
			ID: e.ID, RelationshipType: *relationship, BusinessUnitID: unit, StartDate: dayParam(start),
			ContractSignedAt: pgDate(civilDate(body.ContractSignedAt)), Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("staff: change employment %d: %w", e.ID, err)
		}
		if moves && len(chain) > 0 {
			if err := txq.MoveTermsValidFrom(ctx, store.MoveTermsValidFromParams{ID: chain[0].ID, ValidFrom: dayParam(start), Now: s.deps.Clock()}); err != nil {
				return fmt.Errorf("staff: move employment %d's first terms row: %w", e.ID, err)
			}
			if err := checkManagerCycle(ctx, txq, e.PersonID, earliest(start, civil(e.StartDate))); err != nil {
				return err
			}
		}
		before, err := employmentValues(&e)
		if err != nil {
			return err
		}
		after, err := employmentValues(&saved)
		if err != nil {
			return err
		}
		return s.recordRowChange(ctx, txq, e.PersonID, eventEmploymentUpdated, who, e.ID, 0, diffFields(before, after))
	})
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffEmploymentsById404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffEmploymentsById409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffEmploymentsById400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, err
	}
	out, err := s.respondEmployment(ctx, caller, saved)
	if err != nil {
		return nil, err
	}
	return gen.PutStaffEmploymentsById200JSONResponse(out), nil
}

// earliest is the earlier of two days.
func earliest(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// PostStaffEmploymentsByIdEnd End an employment
// (POST /api/v1/staff/employments/{id}/end)
//
// An end only takes edges out of the manager graph, which can close no loop,
// so it needs no graph lock.
func (s *server) PostStaffEmploymentsByIdEnd(ctx context.Context, req gen.PostStaffEmploymentsByIdEndRequestObject) (gen.PostStaffEmploymentsByIdEndResponseObject, error) {
	var body gen.StaffEmploymentEndRequest
	if req.Body != nil {
		body = *req.Body
	}
	var f fieldErrors
	if body.EndDate == nil {
		f.add("endDate", "The last day worked is required")
	}
	if body.EndReason == nil {
		f.addCoded("endReason", codeEndReasonRequired, "An ended employment states why, as one of the a-melding's six reasons: "+strings.Join(endReasons, ", "))
	}
	reason, msg := parseEnum("An end reason", body.EndReason, endReasons)
	f.add("endReason", msg)
	if f.failed() {
		return gen.PostStaffEmploymentsByIdEnd400ApplicationProblemPlusJSONResponse(invalid(invalidEndTitle, f)), nil
	}
	end := civilDate(body.EndDate).Time
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}

	var saved store.StaffEmployment
	err = s.withEmploymentLock(ctx, req.Id, func(ctx context.Context, txq *store.Queries, e store.StaffEmployment) error {
		if end.Before(civil(e.StartDate)) {
			return refuseInvalid(invalidEndTitle, fieldErrors{errs: fieldError("endDate", "An employment ends on or after its start")})
		}
		chain, err := chainOf(ctx, txq, e.ID)
		if err != nil {
			return err
		}
		if n := len(chain); n > 0 && civil(chain[n-1].ValidFrom).After(end) {
			return refuseConflict(conflict(codeTermsAfterEnd, "Terms after the end",
				fmt.Sprintf("A terms row takes effect on %s, after this end; delete it or end the employment later.", civil(chain[n-1].ValidFrom).Format(time.DateOnly))))
		}
		saved, err = txq.SetEmploymentEnd(ctx, store.SetEmploymentEndParams{ID: e.ID, EndDate: dayParam(end), EndReason: reason, Now: s.deps.Clock()})
		if err != nil {
			return fmt.Errorf("staff: end employment %d: %w", e.ID, err)
		}
		before, err := employmentValues(&e)
		if err != nil {
			return err
		}
		after, err := employmentValues(&saved)
		if err != nil {
			return err
		}
		return s.recordRowChange(ctx, txq, e.PersonID, eventEmploymentEnded, who, e.ID, 0, diffFields(before, after))
	})
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PostStaffEmploymentsByIdEnd404Response{}, nil
		case http.StatusConflict:
			return gen.PostStaffEmploymentsByIdEnd409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PostStaffEmploymentsByIdEnd400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, err
	}
	out, err := s.respondEmployment(ctx, caller, saved)
	if err != nil {
		return nil, err
	}
	return gen.PostStaffEmploymentsByIdEnd200JSONResponse(out), nil
}

// PostStaffEmploymentsByIdReopen Reopen an employment
// (POST /api/v1/staff/employments/{id}/reopen)
//
// Reopening brings the terms back into force after the old end, edges
// included, so it takes the manager graph's lock and is judged on it from
// the day after the old end.
func (s *server) PostStaffEmploymentsByIdReopen(ctx context.Context, req gen.PostStaffEmploymentsByIdReopenRequestObject) (gen.PostStaffEmploymentsByIdReopenResponseObject, error) {
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var saved store.StaffEmployment
	err = s.withEmploymentLock(ctx, req.Id, func(ctx context.Context, txq *store.Queries, e store.StaffEmployment) error {
		if !e.EndDate.Valid {
			saved = e
			return nil
		}
		if err := lockManagerGraph(ctx, txq); err != nil {
			return err
		}
		var err error
		saved, err = txq.SetEmploymentEnd(ctx, store.SetEmploymentEndParams{ID: e.ID, Now: s.deps.Clock()})
		if err != nil {
			return fmt.Errorf("staff: reopen employment %d: %w", e.ID, err)
		}
		if err := checkManagerCycle(ctx, txq, e.PersonID, e.EndDate.Time.AddDate(0, 0, 1)); err != nil {
			return err
		}
		before, err := employmentValues(&e)
		if err != nil {
			return err
		}
		after, err := employmentValues(&saved)
		if err != nil {
			return err
		}
		return s.recordRowChange(ctx, txq, e.PersonID, eventEmploymentReopened, who, e.ID, 0, diffFields(before, after))
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusNotFound {
			return gen.PostStaffEmploymentsByIdReopen404Response{}, nil
		}
		return gen.PostStaffEmploymentsByIdReopen409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	out, err := s.respondEmployment(ctx, caller, saved)
	if err != nil {
		return nil, err
	}
	return gen.PostStaffEmploymentsByIdReopen200JSONResponse(out), nil
}
