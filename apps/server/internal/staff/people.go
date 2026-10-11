package staff

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is a person (design S2, S3, §3.1, §6 People): created by HR with
// an employee number typed or generated, read by everyone the person exists
// for — each reading the field groups theirs to read (shaping.go) — and
// replaced by HR. The history of every change is events.go's.

// The unique indexes a person's write can trip, each the rule it backs.
const (
	employeeNumberIndex = "ux_people_employee_number_upper"
	workEmailIndex      = "ux_people_work_email_lower"
)

// personWriteError is a person write's own database refusal: an employee
// number or a work email another person took between the checks and the
// write — a race the index settles — answered as the rule's code, as if the
// check had seen it. Anything else is the error it was.
func personWriteError(err error, title string) error {
	switch {
	case db.IsUniqueViolation(err, employeeNumberIndex):
		return refuseInvalid(title, fieldErrors{
			errs: fieldError("employeeNumber", "Another person already has this employee number, however it is cased"),
			code: codeEmployeeNumberTaken,
		})
	case db.IsUniqueViolation(err, workEmailIndex):
		return refuseInvalid(title, fieldErrors{
			errs: fieldError("workEmail", "Another person already has this work email"),
			code: codeWorkEmailTaken,
		})
	}
	return err
}

// bumpPersonRevision moves a person's revision on for a change to one of the
// lists their revision guards — next of kin, roles — and answers the new one.
func bumpPersonRevision(ctx context.Context, txq *store.Queries, id int64, now time.Time) (int32, error) {
	if err := txq.BumpPersonRevision(ctx, store.BumpPersonRevisionParams{ID: id, Now: now}); err != nil {
		return 0, fmt.Errorf("staff: move person %d's revision on: %w", id, err)
	}
	row, err := readPerson(ctx, txq, id)
	if err != nil {
		return 0, err
	}
	return row.Revision, nil
}

// refusedWithCode reports whether err is a refusal carrying code.
func refusedWithCode(err error, code string) bool {
	r, ok := asRefusal(err)
	return ok && r.problem.Code != nil && *r.problem.Code == code
}

// readPerson is one person on q, or the 404 refusal.
func readPerson(ctx context.Context, q *store.Queries, id int64) (store.GetPersonRow, error) {
	row, err := q.GetPerson(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, notFound()
	}
	if err != nil {
		return row, fmt.Errorf("staff: read person %d: %w", id, err)
	}
	return row, nil
}

// linkedUserID is the user id a person's self-service link names, as stored,
// nil when the person is linked to nobody.
func linkedUserID(ctx context.Context, q *store.Queries, personID int64) (*string, error) {
	id, err := q.PersonLinkedUser(ctx, personID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: read person %d's link: %w", personID, err)
	}
	return &id, nil
}

// linkedUser is the linked user as the directory names them, read through the
// checked accessor — so never inside a transaction — and nil without a link.
// A user the directory no longer knows is answered as "Unknown user", not
// active, rather than failing the person's read.
func (s *server) linkedUser(ctx context.Context, externalID *string) (*gen.StaffPersonUser, error) {
	if externalID == nil {
		return nil, nil
	}
	id, err := uuid.Parse(*externalID)
	if err != nil {
		return nil, fmt.Errorf("staff: a self-service link names %q, which is no user id: %w", *externalID, err)
	}
	user, err := s.usersUser(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("staff: read the linked user: %w", err)
	}
	if user == nil {
		return userResponse(id, unknownUserDisplay, false), nil
	}
	return userResponse(id, user.DisplayName, user.Active), nil
}

// respondPerson is the person as the caller may read them, after any
// transaction: the audience and the placement today resolved on the pool,
// and the linked user read from the directory when the audience reads the
// link.
func (s *server) respondPerson(ctx context.Context, caller *callerContext, row store.GetPersonRow, link *string) (gen.StaffPerson, error) {
	a, err := personAudience(ctx, store.New(s.deps.Pool), caller, row.ID)
	if err != nil {
		return gen.StaffPerson{}, err
	}
	placements, err := placementsOf(ctx, store.New(s.deps.Pool), []int64{row.ID}, caller.Today)
	if err != nil {
		return gen.StaffPerson{}, err
	}
	var user *gen.StaffPersonUser
	if a.show("user") {
		if user, err = s.linkedUser(ctx, link); err != nil {
			return gen.StaffPerson{}, err
		}
	}
	return personResponse(row, placements[row.ID], a, user), nil
}

// generateEmployeeNumber is the next number of the settings' sequence — the
// prefix and the next number, unpadded (plan reading 3) — skipping past every
// number a person already has however it is cased, so a number HR typed by
// hand never collides; the first free one is found in one statement. It holds
// the settings row for the rest of the transaction, so two creators take
// numbers one after the other. A number the skip carries past sixteen
// characters is refused employee_number_too_long: the settings' own check
// holds only for the number they name.
func generateEmployeeNumber(ctx context.Context, txq *store.Queries) (string, error) {
	row, err := txq.LockSettings(ctx)
	if err != nil {
		return "", fmt.Errorf("staff: lock the settings: %w", err)
	}
	n, err := txq.FirstFreeEmployeeNumber(ctx, store.FirstFreeEmployeeNumberParams{
		NextNumber: row.NextEmployeeNumber, Prefix: row.EmployeeNumberPrefix,
	})
	if err != nil {
		return "", fmt.Errorf("staff: find the next free employee number: %w", err)
	}
	number := row.EmployeeNumberPrefix + strconv.FormatInt(n, 10)
	if len(number) > employeeNumberMax {
		return "", refuseInvalid(invalidPersonTitle, fieldErrors{
			errs: fieldError("employeeNumber", fmt.Sprintf(
				"The next free employee number would be '%s', longer than %d characters; give one, or change the prefix or the next number in the settings",
				number, employeeNumberMax)),
			code: codeEmployeeNumberTooLong,
		})
	}
	if err := txq.AdvanceEmployeeNumber(ctx, n+1); err != nil {
		return "", fmt.Errorf("staff: advance the employee number sequence: %w", err)
	}
	return number, nil
}

// generationAttempts is how many times a create with a generated number is
// tried. The sequence's check cannot see a typed create that has not
// committed yet; when that create takes the same number, the generation's
// insert trips the unique index, and one more try — which now sees it — takes
// the next free number. A typed number that is taken is the caller's to
// change, and is never retried.
const generationAttempts = 2

// PostStaffPeople Add a person
// (POST /api/v1/staff/people)
func (s *server) PostStaffPeople(ctx context.Context, req gen.PostStaffPeopleRequestObject) (gen.PostStaffPeopleResponseObject, error) {
	var body gen.StaffPersonRequest
	if req.Body != nil {
		body = *req.Body
	}
	if f := s.unknownProperties(ctx, personRequestSchema); f.failed() {
		return gen.PostStaffPeople400ApplicationProblemPlusJSONResponse(invalid(invalidPersonTitle, f)), nil
	}
	parsed, f := parsePerson(body, true)
	if f.failed() {
		return gen.PostStaffPeople400ApplicationProblemPlusJSONResponse(invalid(invalidPersonTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}

	var created store.GetPersonRow
	for attempt := 1; ; attempt++ {
		err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
			number := ""
			if parsed.EmployeeNumber != nil {
				number = *parsed.EmployeeNumber
			} else {
				generated, err := generateEmployeeNumber(ctx, txq)
				if err != nil {
					return err
				}
				number = generated
			}
			row := parsed.applyTo(store.GetPersonRow{EmployeeNumber: number})
			id, err := txq.InsertPerson(ctx, store.InsertPersonParams{
				EmployeeNumber: row.EmployeeNumber, GivenName: row.GivenName, FamilyName: row.FamilyName,
				DisplayName: row.DisplayName, BirthDate: row.BirthDate, Gender: row.Gender,
				WorkEmail: row.WorkEmail, PhoneWork: row.PhoneWork, PhoneMobile: row.PhoneMobile,
				PhonePrivate: row.PhonePrivate, PrivateEmail: row.PrivateEmail, Street: row.Street,
				PostalCode: row.PostalCode, City: row.City, Country: row.Country, WorkCountry: row.WorkCountry,
				PreferredLanguage: row.PreferredLanguage, Notes: row.Notes, Now: s.deps.Clock(),
			})
			if err != nil {
				return personWriteError(err, invalidPersonTitle)
			}
			read, err := readPerson(ctx, txq, id)
			if err != nil {
				return err
			}
			created = read
			return s.recordChanges(ctx, txq, id, eventPersonCreated, who, diffPerson(nil, created))
		})
		if parsed.EmployeeNumber == nil && attempt < generationAttempts && refusedWithCode(err, codeEmployeeNumberTaken) {
			continue
		}
		break
	}
	if r, ok := asRefusal(err); ok {
		return gen.PostStaffPeople400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: add a person: %w", err)
	}
	out, err := s.respondPerson(ctx, caller, created, nil)
	if err != nil {
		return nil, err
	}
	return gen.PostStaffPeople201JSONResponse(out), nil
}

// GetStaffPeopleById Get a person
// (GET /api/v1/staff/people/{id})
//
// The person and their link are read in one snapshot; the linked user is
// read from the directory after it.
func (s *server) GetStaffPeopleById(ctx context.Context, req gen.GetStaffPeopleByIdRequestObject) (gen.GetStaffPeopleByIdResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var (
		row  store.GetPersonRow
		link *string
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		read, err := readPerson(ctx, q, req.Id)
		if err != nil {
			return err
		}
		a, err := personAudience(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		if !a.seesPerson() {
			return notFound()
		}
		row = read
		link, err = linkedUserID(ctx, q, req.Id)
		return err
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	out, err := s.respondPerson(ctx, caller, row, link)
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleById200JSONResponse(out), nil
}

// PutStaffPeopleById Change a person
// (PUT /api/v1/staff/people/{id})
//
// A full replace of the editable groups, guarded by If-Match under the
// person's lock. The employee number never changes; the display name stays
// when left out. A replace that changes nothing writes nothing — no
// revision, no event.
func (s *server) PutStaffPeopleById(ctx context.Context, req gen.PutStaffPeopleByIdRequestObject) (gen.PutStaffPeopleByIdResponseObject, error) {
	var body gen.StaffPersonRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffPeopleById400ApplicationProblemPlusJSONResponse(invalid(invalidPersonTitle, badIfMatch())), nil
	}
	if f := s.unknownProperties(ctx, personRequestSchema); f.failed() {
		return gen.PutStaffPeopleById400ApplicationProblemPlusJSONResponse(invalid(invalidPersonTitle, f)), nil
	}
	parsed, f := parsePerson(body, false)
	if f.failed() {
		return gen.PutStaffPeopleById400ApplicationProblemPlusJSONResponse(invalid(invalidPersonTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}

	var (
		saved store.GetPersonRow
		link  *string
	)
	err = s.withPersonLock(ctx, req.Id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := readPerson(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if err := checkRevision("person", current.Revision, revision); err != nil {
			return err
		}
		if parsed.EmployeeNumber != nil && *parsed.EmployeeNumber != current.EmployeeNumber {
			return refuseInvalid(invalidPersonTitle, fieldErrors{errs: fieldError("employeeNumber",
				fmt.Sprintf("An employee number never changes; leave it out or send '%s'", current.EmployeeNumber))})
		}
		if link, err = linkedUserID(ctx, txq, req.Id); err != nil {
			return err
		}
		next := parsed.applyTo(current)
		diff := diffPerson(&current, next)
		if len(diff) == 0 {
			saved = current
			return nil
		}
		if _, changes := diff["birthDate"]; changes {
			if err := s.checkBirthDateAgainstNationalID(ctx, txq, req.Id, next.BirthDate); err != nil {
				return err
			}
		}
		if err := txq.UpdatePerson(ctx, store.UpdatePersonParams{
			ID: req.Id, GivenName: next.GivenName, FamilyName: next.FamilyName, DisplayName: next.DisplayName,
			BirthDate: next.BirthDate, Gender: next.Gender, WorkEmail: next.WorkEmail, PhoneWork: next.PhoneWork,
			PhoneMobile: next.PhoneMobile, PhonePrivate: next.PhonePrivate, PrivateEmail: next.PrivateEmail,
			Street: next.Street, PostalCode: next.PostalCode, City: next.City, Country: next.Country,
			WorkCountry: next.WorkCountry, PreferredLanguage: next.PreferredLanguage, Notes: next.Notes,
			Now: s.deps.Clock(),
		}); err != nil {
			return personWriteError(err, invalidPersonTitle)
		}
		if saved, err = readPerson(ctx, txq, req.Id); err != nil {
			return err
		}
		return s.recordChanges(ctx, txq, req.Id, eventPersonUpdated, who, diff)
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PutStaffPeopleById404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffPeopleById404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffPeopleById409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffPeopleById400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("staff: change person %d: %w", req.Id, err)
	}
	out, err := s.respondPerson(ctx, caller, saved, link)
	if err != nil {
		return nil, err
	}
	return gen.PutStaffPeopleById200JSONResponse(out), nil
}
