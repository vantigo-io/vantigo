package staff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is a person's competences (staff competence design S3–S6, §4,
// §5): one current row per competence, declared by the person or added by a
// verifier — a manager of theirs today, staff:approve — or HR, verified,
// renewed as a new row that supersedes the old, removed, and carrying its
// evidence in the person's archive. Everyone who sees the person reads the
// Competence group; the person, their manager, the Approver and HR read the
// detail too (shaping.go). The person writes their own declared rows and
// renews their own; a verifier and HR write any current row; nobody writes a
// superseded one (superseded), and nobody verifies their own (own_record).
//
// Every write follows the module's person-scoped shape: the caller and the
// actor read before any transaction; the row and the caller's audience
// judged in a snapshot (a row whose person the caller may not see is the
// 404 an unknown id is); then, under the person's lock, the competence
// shared and the row locked — person → competence → person competence (§4
// Locking) — and everything judged again as it is now. The verifier's name
// is read from the user directory after the transaction.

// personCompetenceCurrentIndex is the partial unique index on a person's
// current row of a competence (S3).
const personCompetenceCurrentIndex = "ux_person_competences_current"

// certificateCategoryKey is the seeded document category a competence's
// evidence is filed under (S6).
const certificateCategoryKey = "certificate"

// competenceFieldGroup is a field's group on a person's competence row and
// in its events (§5): the competence, its level and validity and its state
// the Competence group's, the rest the detail's; a name it does not know
// nobody's.
func competenceFieldGroup(name string) fieldGroup {
	switch name {
	case "competence", "competenceKey", "level", "validFrom", "validTo", "verified", "superseded",
		"expiring", "expired", "declaredAt", "revision", "chain":
		return groupCompetence
	case "issuedOn", "issuer", "reference", "evidence", "documentId", "source", "verifiedAt", "verifiedBy",
		"verifiedBasis", "basis", "verifiedNote":
		return groupCompetenceDetail
	}
	return groupNobody
}

// competenceExpiry is whether a row is expiring and whether it has expired
// on today (S7): a current row's end within its competence's lead of today
// and not passed, or passed. A superseded row is neither, and a competence
// without a lead never expiring.
func competenceExpiry(r store.StaffPersonCompetence, lead *int16, today time.Time) (expiring, expired bool) {
	if isSuperseded(r) || !r.ValidTo.Valid {
		return false, false
	}
	end := r.ValidTo.Time
	if end.Before(today) {
		return false, true
	}
	return lead != nil && !end.After(today.AddDate(0, 0, int(*lead))), false
}

// personCompetenceResponse is one row as the audience reads it: the
// Competence group to everyone who sees the person, the detail only to its
// readers — absent otherwise. names is the verifiers' names by user id.
func personCompetenceResponse(row store.GetPersonCompetenceRow, a audience, today time.Time, names map[uuid.UUID]string) gen.StaffPersonCompetence {
	r := row.StaffPersonCompetence
	expiring, expired := competenceExpiry(r, row.CompetenceReminderLeadDays, today)
	out := gen.StaffPersonCompetence{
		Id:       r.ID,
		PersonId: r.PersonID,
		Competence: gen.StaffCompetenceSummary{
			Id:         r.CompetenceID,
			Key:        row.CompetenceKey,
			Names:      gen.StaffCompetenceNames{En: row.CompetenceNameEn, Nb: row.CompetenceNameNb},
			Kind:       gen.StaffCompetenceKind(row.CompetenceKind),
			HasLevel:   row.CompetenceHasLevel,
			CategoryId: row.CompetenceCategoryID,
			Archived:   row.CompetenceArchivedAt != nil,
		},
		Level:      int32Of(r.Level),
		ValidFrom:  openapi_types.Date{Time: civil(r.ValidFrom)},
		ValidTo:    dateOf(r.ValidTo),
		Verified:   isVerified(r),
		Superseded: isSuperseded(r),
		Expiring:   expiring,
		Expired:    expired,
		Revision:   r.Revision,
		DeclaredAt: r.CreatedAt,
	}
	if !a.reads(groupCompetenceDetail) {
		return out
	}
	source := r.Source
	out.IssuedOn = dateOf(r.IssuedOn)
	out.Issuer = r.Issuer
	out.Reference = r.Reference
	out.Source = &source
	out.VerifiedAt = r.VerifiedAt
	out.VerifiedBasis = r.VerifiedBasis
	out.VerifiedNote = r.VerifiedNote
	if r.VerifiedByUserID != nil {
		name, ok := names[*r.VerifiedByUserID]
		if !ok {
			name = unknownUserDisplay
		}
		out.VerifiedBy = &name
	}
	if r.EvidenceDocumentID != nil && row.EvidenceFileName != nil && row.EvidenceContentType != nil && row.EvidenceSizeBytes != nil {
		out.Evidence = &gen.StaffCompetenceEvidence{
			DocumentId: *r.EvidenceDocumentID, FileName: *row.EvidenceFileName,
			ContentType: *row.EvidenceContentType, SizeBytes: *row.EvidenceSizeBytes,
		}
	}
	return out
}

// verifierNames is the names of the users who verified rows, as the user
// directory names them — read outside any transaction, and only for an
// audience that reads the detail. A user the directory no longer knows is
// left out, and named "Unknown user" by personCompetenceResponse.
func (s *server) verifierNames(ctx context.Context, a audience, rows []store.StaffPersonCompetence) (map[uuid.UUID]string, error) {
	if !a.reads(groupCompetenceDetail) {
		return nil, nil
	}
	var ids []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, r := range rows {
		if r.VerifiedByUserID != nil && !seen[*r.VerifiedByUserID] {
			seen[*r.VerifiedByUserID] = true
			ids = append(ids, *r.VerifiedByUserID)
		}
	}
	users, err := s.usersUsers(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("staff: read the verifiers' names: %w", err)
	}
	out := make(map[uuid.UUID]string, len(users))
	for _, u := range users {
		out[u.ID] = u.DisplayName
	}
	return out, nil
}

// personCompetencesResponse is rows as the audience reads them, the
// verifiers' names read after the transaction that read the rows.
func (s *server) personCompetencesResponse(ctx context.Context, rows []store.GetPersonCompetenceRow, a audience, today time.Time) ([]gen.StaffPersonCompetence, error) {
	names, err := s.verifierNames(ctx, a, mapRows(rows, func(r store.GetPersonCompetenceRow) store.StaffPersonCompetence {
		return r.StaffPersonCompetence
	}))
	if err != nil {
		return nil, err
	}
	return mapRows(rows, func(r store.GetPersonCompetenceRow) gen.StaffPersonCompetence {
		return personCompetenceResponse(r, a, today, names)
	}), nil
}

// onePersonCompetence is one row as the audience reads it, after the
// transaction that wrote it.
func (s *server) onePersonCompetence(ctx context.Context, row store.GetPersonCompetenceRow, a audience, today time.Time) (gen.StaffPersonCompetence, error) {
	out, err := s.personCompetencesResponse(ctx, []store.GetPersonCompetenceRow{row}, a, today)
	if err != nil {
		return gen.StaffPersonCompetence{}, err
	}
	return out[0], nil
}

// readCompetenceRow is one row joined with its competence and evidence on q,
// or the 404 refusal.
func readCompetenceRow(ctx context.Context, q *store.Queries, id int64) (store.GetPersonCompetenceRow, error) {
	row, err := q.GetPersonCompetence(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, notFound()
	}
	if err != nil {
		return row, fmt.Errorf("staff: read competence row %d: %w", id, err)
	}
	return row, nil
}

// competenceRowValues is what a row's change event names of it: the level,
// the dates, the issuer and the reference, as the contract names them.
type competenceRowValues struct {
	Level     *int16              `json:"level,omitempty"`
	IssuedOn  *openapi_types.Date `json:"issuedOn,omitempty"`
	ValidFrom openapi_types.Date  `json:"validFrom"`
	ValidTo   *openapi_types.Date `json:"validTo,omitempty"`
	Issuer    *string             `json:"issuer,omitempty"`
	Reference *string             `json:"reference,omitempty"`
}

// rowValues is r's fields as its change event names them.
func rowValues(r store.StaffPersonCompetence) (map[string]json.RawMessage, error) {
	return payloadValues(competenceRowValues{
		Level: r.Level, IssuedOn: dateOf(r.IssuedOn), ValidFrom: openapi_types.Date{Time: civil(r.ValidFrom)},
		ValidTo: dateOf(r.ValidTo), Issuer: r.Issuer, Reference: r.Reference,
	})
}

// competenceClass is every competence event field's class: D, the row's
// (S17).
func competenceClass(string) string { return "D" }

// recordCompetenceEvent records kind for the row's person, naming the row
// (competenceId) and, beside c's fields, the competence's key. Each field is
// classed D in the payload's own map, which the erase blanks it by.
func (s *server) recordCompetenceEvent(ctx context.Context, txq *store.Queries, personID, rowID int64, kind string, who actor, key string, c changes) error {
	c["competenceKey"] = fieldChange{To: key}
	payload, classes := classedPayload("competenceId", rowID, c, competenceClass)
	return s.recordClassedEvent(ctx, txq, personID, kind, who, payload, classes)
}

// visibleCompetenceRow is one row the caller may see and their audience on
// its person, judged in one snapshot: an unknown row, or one whose person
// they may not see, is the 404 refusal.
func (s *server) visibleCompetenceRow(ctx context.Context, caller *callerContext, id int64) (store.GetPersonCompetenceRow, audience, error) {
	var (
		row store.GetPersonCompetenceRow
		a   audience
	)
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if row, err = readCompetenceRow(ctx, q, id); err != nil {
			return err
		}
		if a, err = personAudience(ctx, q, caller, row.StaffPersonCompetence.PersonID); err != nil {
			return err
		}
		if !a.reads(groupCompetence) {
			return notFound()
		}
		return nil
	})
	return row, a, err
}

// lockedCompetenceRow is what a row's write judges under its person's lock:
// the caller's audience now, the competence shared and the row locked.
type lockedCompetenceRow struct {
	a          audience
	competence store.StaffCompetence
	row        store.StaffPersonCompetence
}

// withCompetenceRow runs fn under the row's person's lock with the row as it
// is now — person → competence → person competence — after judging the
// audience again: the person no longer seen, or the row gone, is the 404.
func (s *server) withCompetenceRow(ctx context.Context, caller *callerContext, seen store.GetPersonCompetenceRow,
	fn func(ctx context.Context, txq *store.Queries, l lockedCompetenceRow) error,
) error {
	personID := seen.StaffPersonCompetence.PersonID
	return s.withPersonLock(ctx, personID, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var (
			l   lockedCompetenceRow
			err error
		)
		if l.a, err = personAudience(ctx, txq, caller, personID); err != nil {
			return err
		}
		if !l.a.reads(groupCompetence) {
			return notFound()
		}
		if l.competence, err = txq.ShareCompetence(ctx, seen.StaffPersonCompetence.CompetenceID); err != nil {
			return fmt.Errorf("staff: read competence %d: %w", seen.StaffPersonCompetence.CompetenceID, err)
		}
		l.row, err = txq.LockPersonCompetence(ctx, seen.StaffPersonCompetence.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock competence row %d: %w", seen.StaffPersonCompetence.ID, err)
		}
		return fn(ctx, txq, l)
	})
}

// GetStaffPeopleByIdCompetences List a person's competences
// (GET /api/v1/staff/people/{id}/competences)
func (s *server) GetStaffPeopleByIdCompetences(ctx context.Context, req gen.GetStaffPeopleByIdCompetencesRequestObject) (gen.GetStaffPeopleByIdCompetencesResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	history := req.Params.History != nil && *req.Params.History
	var (
		a    audience
		rows []store.GetPersonCompetenceRow
	)
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := readPerson(ctx, q, req.Id); err != nil {
			return err
		}
		var err error
		if a, err = personAudience(ctx, q, caller, req.Id); err != nil {
			return err
		}
		if !a.reads(groupCompetence) {
			return notFound()
		}
		if history {
			list, err := q.ListPersonCompetenceHistory(ctx, req.Id)
			if err != nil {
				return fmt.Errorf("staff: list person %d's competence history: %w", req.Id, err)
			}
			rows = mapRows(list, func(r store.ListPersonCompetenceHistoryRow) store.GetPersonCompetenceRow {
				return store.GetPersonCompetenceRow(r)
			})
			return nil
		}
		list, err := q.ListPersonCompetences(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("staff: list person %d's competences: %w", req.Id, err)
		}
		rows = mapRows(list, func(r store.ListPersonCompetencesRow) store.GetPersonCompetenceRow {
			return store.GetPersonCompetenceRow(r)
		})
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdCompetences404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	out, err := s.personCompetencesResponse(ctx, rows, a, caller.Today)
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdCompetences200JSONResponse(out), nil
}

// PostStaffPeopleByIdCompetences Declare or add a competence
// (POST /api/v1/staff/people/{id}/competences)
//
// The person declares (source self, declared); a manager of theirs today or
// staff:approve adds (source manager), staff:manage too (source hr), the two
// verified at once with basis observed and the caller as the verifier
// (reading 5). The refusals come in the order that tells a caller least: the
// person they may not see (404), the uniform 403, the fields (400), then the
// state of things (409).
func (s *server) PostStaffPeopleByIdCompetences(ctx context.Context, req gen.PostStaffPeopleByIdCompetencesRequestObject) (gen.PostStaffPeopleByIdCompetencesResponseObject, error) {
	var body gen.StaffPersonCompetenceRequest
	if req.Body != nil {
		body = *req.Body
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := readPerson(ctx, q, req.Id); err != nil {
			return err
		}
		a, err := personAudience(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		if !a.reads(groupCompetence) {
			return notFound()
		}
		if competenceRoleOf(a) == roleNone {
			return forbidden()
		}
		return nil
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusForbidden {
			return gen.PostStaffPeopleByIdCompetences403JSONResponse(apicommon.ForbiddenBody()), nil
		}
		return gen.PostStaffPeopleByIdCompetences404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	var f fieldErrors
	fields := parseRowFields(body.IssuedOn, body.ValidFrom, body.ValidTo, body.Issuer, body.Reference, nil, caller.Today, &f)
	if f.failed() {
		return gen.PostStaffPeopleByIdCompetences400ApplicationProblemPlusJSONResponse(invalid(invalidPersonCompetenceTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var (
		a       audience
		created store.GetPersonCompetenceRow
	)
	err = s.withPersonLock(ctx, req.Id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var err error
		if a, err = personAudience(ctx, txq, caller, req.Id); err != nil {
			return err
		}
		role := competenceRoleOf(a)
		if !a.reads(groupCompetence) || role == roleNone {
			return forbidden()
		}
		c, err := txq.ShareCompetence(ctx, body.CompetenceId)
		if errors.Is(err, pgx.ErrNoRows) {
			return unavailableCompetence(body.CompetenceId)
		} else if err != nil {
			return fmt.Errorf("staff: read competence %d: %w", body.CompetenceId, err)
		}
		if !competenceAvailable(c) {
			return unavailableCompetence(body.CompetenceId)
		}
		var f fieldErrors
		level := parseLevel(body.Level, c.HasLevel, &f)
		if f.failed() {
			return refuseInvalid(invalidPersonCompetenceTitle, f)
		}
		if _, err := txq.CurrentPersonCompetenceOf(ctx, store.CurrentPersonCompetenceOfParams{PersonID: req.Id, CompetenceID: c.ID}); err == nil {
			return competenceHeld()
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("staff: read person %d's row of competence %d: %w", req.Id, c.ID, err)
		}
		params := store.InsertPersonCompetenceParams{
			PersonID: req.Id, CompetenceID: c.ID, Level: level, IssuedOn: pgTime(fields.IssuedOn),
			ValidFrom: dayParam(fields.ValidFrom), ValidTo: pgTime(fields.ValidTo), Issuer: fields.Issuer,
			Reference: fields.Reference, Source: role.source(), Now: s.deps.Clock(),
		}
		if role.verifies() {
			params.VerifiedAt, params.VerifiedByUserID, params.VerifiedBasis = verifiedNow(s.deps.Clock(), caller.UserID)
		}
		row, err := txq.InsertPersonCompetence(ctx, params)
		if db.IsUniqueViolation(err, personCompetenceCurrentIndex) {
			return competenceHeld()
		}
		if err != nil {
			return fmt.Errorf("staff: add competence %d for person %d: %w", c.ID, req.Id, err)
		}
		if created, err = readCompetenceRow(ctx, txq, row.ID); err != nil {
			return err
		}
		c2 := changes{"source": fieldChange{To: row.Source}}
		if row.Level != nil {
			c2["level"] = fieldChange{To: *row.Level}
		}
		return s.recordCompetenceEvent(ctx, txq, req.Id, row.ID, eventCompetenceDeclared, who, c.Key, c2)
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPeopleByIdCompetences404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PostStaffPeopleByIdCompetences404Response{}, nil
		case http.StatusForbidden:
			return gen.PostStaffPeopleByIdCompetences403JSONResponse(apicommon.ForbiddenBody()), nil
		case http.StatusConflict:
			return gen.PostStaffPeopleByIdCompetences409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PostStaffPeopleByIdCompetences400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("staff: add a competence for person %d: %w", req.Id, err)
	}
	out, err := s.onePersonCompetence(ctx, created, a, caller.Today)
	if err != nil {
		return nil, err
	}
	return gen.PostStaffPeopleByIdCompetences201JSONResponse(out), nil
}

// verifiedNow is a verification stamped now by user, on the basis observed:
// a verifier's or HR's own entry (reading 5).
func verifiedNow(now time.Time, user uuid.UUID) (*time.Time, *uuid.UUID, *string) {
	basis := basisObserved
	return &now, &user, &basis
}

// pgTime is an optional calendar day as a nullable date column.
func pgTime(d *time.Time) pgtype.Date {
	if d == nil {
		return pgtype.Date{}
	}
	return dayParam(*d)
}

// PutStaffPersonCompetencesById Change a competence row
// (PUT /api/v1/staff/person-competences/{id})
//
// The person changes their own declared row (a verified one is verified_row:
// they renew it instead); a verifier and HR change any current row; a
// superseded row is nobody's to change.
func (s *server) PutStaffPersonCompetencesById(ctx context.Context, req gen.PutStaffPersonCompetencesByIdRequestObject) (gen.PutStaffPersonCompetencesByIdResponseObject, error) {
	var body gen.StaffPersonCompetenceChangeRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffPersonCompetencesById400ApplicationProblemPlusJSONResponse(invalid(invalidPersonCompetenceTitle, badIfMatch())), nil
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	seen, _, err := s.visibleCompetenceRow(ctx, caller, req.Id)
	if _, ok := asRefusal(err); ok {
		return gen.PutStaffPersonCompetencesById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var (
		a     audience
		saved store.GetPersonCompetenceRow
	)
	err = s.withCompetenceRow(ctx, caller, seen, func(ctx context.Context, txq *store.Queries, l lockedCompetenceRow) error {
		a = l.a
		role := competenceRoleOf(a)
		switch {
		case role == roleNone:
			return forbidden()
		case isSuperseded(l.row):
			return supersededRow()
		case role == roleSelf && isVerified(l.row):
			return verifiedRow()
		}
		if err := checkRevision("competence row", l.row.Revision, revision); err != nil {
			return err
		}
		var f fieldErrors
		level := parseLevel(body.Level, l.competence.HasLevel, &f)
		from := civil(l.row.ValidFrom)
		fields := parseRowFields(body.IssuedOn, body.ValidFrom, body.ValidTo, body.Issuer, body.Reference, &from, caller.Today, &f)
		if f.failed() {
			return refuseInvalid(invalidPersonCompetenceTitle, f)
		}
		row, err := txq.UpdatePersonCompetence(ctx, store.UpdatePersonCompetenceParams{
			ID: l.row.ID, Level: level, IssuedOn: pgTime(fields.IssuedOn), ValidFrom: dayParam(fields.ValidFrom),
			ValidTo: pgTime(fields.ValidTo), Issuer: fields.Issuer, Reference: fields.Reference, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("staff: change competence row %d: %w", l.row.ID, err)
		}
		if saved, err = readCompetenceRow(ctx, txq, row.ID); err != nil {
			return err
		}
		before, err := rowValues(l.row)
		if err != nil {
			return err
		}
		after, err := rowValues(row)
		if err != nil {
			return err
		}
		c := diffFields(before, after)
		if len(c) == 0 {
			// A change that changed nothing has nothing to say.
			return nil
		}
		return s.recordCompetenceEvent(ctx, txq, row.PersonID, row.ID, eventCompetenceChanged, who, l.competence.Key, c)
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PutStaffPersonCompetencesById404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffPersonCompetencesById404Response{}, nil
		case http.StatusForbidden:
			return gen.PutStaffPersonCompetencesById403JSONResponse(apicommon.ForbiddenBody()), nil
		case http.StatusConflict:
			return gen.PutStaffPersonCompetencesById409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffPersonCompetencesById400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("staff: change competence row %d: %w", req.Id, err)
	}
	out, err := s.onePersonCompetence(ctx, saved, a, caller.Today)
	if err != nil {
		return nil, err
	}
	return gen.PutStaffPersonCompetencesById200JSONResponse(out), nil
}

// DeleteStaffPersonCompetencesById Remove a competence row
// (DELETE /api/v1/staff/person-competences/{id})
//
// The person removes their own declared row, and with it the evidence they
// uploaded for it (reading 4): its document deleted and its object queued in
// the ledger in the same transaction, then removed once that has committed,
// as a document's delete is. A verifier and HR remove any current row; chain
// (staff:manage) removes every row the person has had for the competence. A
// superseded row goes only with its chain.
func (s *server) DeleteStaffPersonCompetencesById(ctx context.Context, req gen.DeleteStaffPersonCompetencesByIdRequestObject) (gen.DeleteStaffPersonCompetencesByIdResponseObject, error) {
	chain := req.Params.Chain != nil && *req.Params.Chain
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	seen, _, err := s.visibleCompetenceRow(ctx, caller, req.Id)
	if _, ok := asRefusal(err); ok {
		return gen.DeleteStaffPersonCompetencesById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var queued *string
	err = s.withCompetenceRow(ctx, caller, seen, func(ctx context.Context, txq *store.Queries, l lockedCompetenceRow) error {
		role := competenceRoleOf(l.a)
		switch {
		case role == roleNone, chain && !l.a.Manage:
			return forbidden()
		case isSuperseded(l.row):
			return supersededRow()
		case role == roleSelf && isVerified(l.row):
			return verifiedRow()
		}
		if chain {
			if _, err := txq.DeletePersonCompetenceChain(ctx, store.DeletePersonCompetenceChainParams{
				PersonID: l.row.PersonID, CompetenceID: l.row.CompetenceID,
			}); err != nil {
				return fmt.Errorf("staff: remove person %d's rows of competence %d: %w", l.row.PersonID, l.row.CompetenceID, err)
			}
		} else if _, err := txq.DeletePersonCompetence(ctx, l.row.ID); err != nil {
			return fmt.Errorf("staff: remove competence row %d: %w", l.row.ID, err)
		}
		if role == roleSelf && l.row.EvidenceDocumentID != nil {
			key, err := s.removeOwnEvidence(ctx, txq, *l.row.EvidenceDocumentID, caller.UserID, who)
			if err != nil {
				return err
			}
			queued = key
		}
		return s.recordCompetenceEvent(ctx, txq, l.row.PersonID, l.row.ID, eventCompetenceRemoved, who, l.competence.Key,
			changes{"chain": fieldChange{To: chain}})
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.DeleteStaffPersonCompetencesById404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusForbidden:
			return gen.DeleteStaffPersonCompetencesById403JSONResponse(apicommon.ForbiddenBody()), nil
		case http.StatusConflict:
			return gen.DeleteStaffPersonCompetencesById409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.DeleteStaffPersonCompetencesById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: remove competence row %d: %w", req.Id, err)
	}
	if queued != nil {
		s.finishObjectDeletion(ctx, *queued, 0, "person_id", seen.StaffPersonCompetence.PersonID)
	}
	return gen.DeleteStaffPersonCompetencesById204Response{}, nil
}

// removeOwnEvidence deletes the evidence document of a row its person
// removes, when that person uploaded it (reading 4) — HR's uploads are HR's
// — and queues its object, answering the key queued; nil when the document
// is gone already or is someone else's upload.
func (s *server) removeOwnEvidence(ctx context.Context, txq *store.Queries, documentID int64, user uuid.UUID, who actor) (*string, error) {
	d, err := txq.LockDocument(ctx, documentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("staff: lock document %d: %w", documentID, err)
	}
	if d.UploadedByUserID != user {
		return nil, nil
	}
	category, err := txq.GetDocumentCategory(ctx, d.CategoryID)
	if err != nil {
		return nil, fmt.Errorf("staff: read document %d's category: %w", d.ID, err)
	}
	if err := txq.DeleteDocument(ctx, d.ID); err != nil {
		return nil, fmt.Errorf("staff: delete document %d: %w", d.ID, err)
	}
	if err := txq.InsertObjectDeletion(ctx, store.InsertObjectDeletionParams{ObjectKey: d.ObjectKey, Now: s.deps.Clock()}); err != nil {
		return nil, fmt.Errorf("staff: queue document %d's object: %w", d.ID, err)
	}
	values, err := documentValues(d, category)
	if err != nil {
		return nil, err
	}
	if err := s.recordDocumentEvent(ctx, txq, d, eventDocumentRemoved, who, diffFields(values, nil), category); err != nil {
		return nil, err
	}
	return &d.ObjectKey, nil
}

// PostStaffPersonCompetencesByIdVerify Verify a competence row
// (POST /api/v1/staff/person-competences/{id}/verify)
//
// A manager of the person today, staff:approve or staff:manage verify — the
// person never (own_record), judged first; the level the verifier agrees to
// becomes the row's. A verified row verified again moves the stamp. The note
// is the row's, never the event's.
func (s *server) PostStaffPersonCompetencesByIdVerify(ctx context.Context, req gen.PostStaffPersonCompetencesByIdVerifyRequestObject) (gen.PostStaffPersonCompetencesByIdVerifyResponseObject, error) {
	var body gen.StaffCompetenceVerifyRequest
	if req.Body != nil {
		body = *req.Body
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	seen, a, err := s.visibleCompetenceRow(ctx, caller, req.Id)
	if _, ok := asRefusal(err); ok {
		return gen.PostStaffPersonCompetencesByIdVerify404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := verifierRefusal(a); err != nil {
		return verifyRefused(err), nil
	}
	var f fieldErrors
	basis, msg := parseEnum("A basis", &body.Basis, verificationBases)
	f.add("basis", msg)
	note, msg := optionalText("A note", body.Note, competenceNoteMaxLength)
	f.add("note", msg)
	if f.failed() {
		return gen.PostStaffPersonCompetencesByIdVerify400ApplicationProblemPlusJSONResponse(invalid(invalidVerificationTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var saved store.GetPersonCompetenceRow
	err = s.withCompetenceRow(ctx, caller, seen, func(ctx context.Context, txq *store.Queries, l lockedCompetenceRow) error {
		a = l.a
		if err := verifierRefusal(a); err != nil {
			return err
		}
		if isSuperseded(l.row) {
			return supersededRow()
		}
		var f fieldErrors
		level := parseLevel(body.Level, l.competence.HasLevel, &f)
		if f.failed() {
			return refuseInvalid(invalidVerificationTitle, f)
		}
		now := s.deps.Clock()
		row, err := txq.VerifyPersonCompetence(ctx, store.VerifyPersonCompetenceParams{
			ID: l.row.ID, Level: level, VerifiedAt: now, VerifiedByUserID: caller.UserID, VerifiedBasis: *basis,
			VerifiedNote: note, Now: now,
		})
		if err != nil {
			return fmt.Errorf("staff: verify competence row %d: %w", l.row.ID, err)
		}
		if saved, err = readCompetenceRow(ctx, txq, row.ID); err != nil {
			return err
		}
		c := changes{"basis": fieldChange{To: *basis}}
		if l.competence.HasLevel {
			// Both sides, changed or not (design §9: the event carries the
			// level declared and the level verified).
			var from any
			if l.row.Level != nil {
				from = *l.row.Level
			}
			c["level"] = fieldChange{From: from, To: *row.Level}
		}
		return s.recordCompetenceEvent(ctx, txq, row.PersonID, row.ID, eventCompetenceVerified, who, l.competence.Key, c)
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPersonCompetencesByIdVerify404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PostStaffPersonCompetencesByIdVerify404Response{}, nil
		case http.StatusForbidden:
			return verifyRefused(err), nil
		case http.StatusConflict:
			return gen.PostStaffPersonCompetencesByIdVerify409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PostStaffPersonCompetencesByIdVerify400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("staff: verify competence row %d: %w", req.Id, err)
	}
	out, err := s.onePersonCompetence(ctx, saved, a, caller.Today)
	if err != nil {
		return nil, err
	}
	return gen.PostStaffPersonCompetencesByIdVerify200JSONResponse(out), nil
}

// verifierRefusal is the verifier's rule (§4) as a refusal: the person on
// their own row own_record, anyone neither a manager of theirs today,
// staff:approve nor staff:manage the uniform 403, nil for a verifier.
func verifierRefusal(a audience) error {
	switch {
	case a.Self:
		return ownRecord()
	case !a.Manager && !a.Approver && !a.Manage:
		return forbidden()
	}
	return nil
}

// verifyRefused is a 403 refusal as the verify operation answers it: with
// its problem for own_record, the access layer's body otherwise.
func verifyRefused(err error) gen.PostStaffPersonCompetencesByIdVerifyResponseObject {
	if r, ok := asRefusal(err); ok && r.problem.Code != nil {
		return gen.PostStaffPersonCompetencesByIdVerify403ApplicationProblemPlusJSONResponse(r.problem)
	}
	return gen.PostStaffPersonCompetencesByIdVerify403JSONResponse(apicommon.ForbiddenBody())
}

// PostStaffPersonCompetencesByIdRenew Renew a competence row
// (POST /api/v1/staff/person-competences/{id}/renew)
//
// A new current row with the new dates, issuer and reference and the old
// row's level; the old row, kept as it was, superseded by it in the same
// transaction. The person's renewal is declared; a verifier's or HR's is
// verified at once. validTo left out is the issue date plus the
// competence's validity, when both are known.
func (s *server) PostStaffPersonCompetencesByIdRenew(ctx context.Context, req gen.PostStaffPersonCompetencesByIdRenewRequestObject) (gen.PostStaffPersonCompetencesByIdRenewResponseObject, error) {
	var body gen.StaffCompetenceRenewRequest
	if req.Body != nil {
		body = *req.Body
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	seen, a, err := s.visibleCompetenceRow(ctx, caller, req.Id)
	if _, ok := asRefusal(err); ok {
		return gen.PostStaffPersonCompetencesByIdRenew404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	if competenceRoleOf(a) == roleNone {
		return gen.PostStaffPersonCompetencesByIdRenew403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var created store.GetPersonCompetenceRow
	err = s.withCompetenceRow(ctx, caller, seen, func(ctx context.Context, txq *store.Queries, l lockedCompetenceRow) error {
		a = l.a
		role := competenceRoleOf(a)
		if role == roleNone {
			return forbidden()
		}
		if isSuperseded(l.row) {
			return supersededRow()
		}
		validTo := body.ValidTo
		if validTo == nil && body.IssuedOn != nil && l.competence.ValidityMonths != nil {
			validTo = &openapi_types.Date{Time: addMonths(civilDate(body.IssuedOn).Time, int(*l.competence.ValidityMonths))}
		}
		var f fieldErrors
		fields := parseRowFields(body.IssuedOn, body.ValidFrom, validTo, body.Issuer, body.Reference, nil, caller.Today, &f)
		if f.failed() {
			return refuseInvalid(invalidPersonCompetenceTitle, f)
		}
		// The old row stops being current before the new one is written —
		// at most one current row per competence (S3) — pointing at itself
		// for the moment, then at the row that renews it.
		now := s.deps.Clock()
		if err := txq.SupersedePersonCompetence(ctx, store.SupersedePersonCompetenceParams{ID: l.row.ID, SupersededByID: l.row.ID, Now: now}); err != nil {
			return fmt.Errorf("staff: supersede competence row %d: %w", l.row.ID, err)
		}
		params := store.InsertPersonCompetenceParams{
			PersonID: l.row.PersonID, CompetenceID: l.row.CompetenceID, Level: l.row.Level, IssuedOn: pgTime(fields.IssuedOn),
			ValidFrom: dayParam(fields.ValidFrom), ValidTo: pgTime(fields.ValidTo), Issuer: fields.Issuer,
			Reference: fields.Reference, Source: role.source(), Now: now,
		}
		if role.verifies() {
			params.VerifiedAt, params.VerifiedByUserID, params.VerifiedBasis = verifiedNow(now, caller.UserID)
		}
		row, err := txq.InsertPersonCompetence(ctx, params)
		if err != nil {
			return fmt.Errorf("staff: renew competence row %d: %w", l.row.ID, err)
		}
		if err := txq.SupersedePersonCompetence(ctx, store.SupersedePersonCompetenceParams{ID: l.row.ID, SupersededByID: row.ID, Now: now}); err != nil {
			return fmt.Errorf("staff: supersede competence row %d: %w", l.row.ID, err)
		}
		if created, err = readCompetenceRow(ctx, txq, row.ID); err != nil {
			return err
		}
		c := changes{}
		if fc, ok := dateChange(l.row.ValidTo, row.ValidTo); ok {
			c["validTo"] = fc
		}
		return s.recordCompetenceEvent(ctx, txq, row.PersonID, row.ID, eventCompetenceRenewed, who, l.competence.Key, c)
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPersonCompetencesByIdRenew404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PostStaffPersonCompetencesByIdRenew404Response{}, nil
		case http.StatusForbidden:
			return gen.PostStaffPersonCompetencesByIdRenew403JSONResponse(apicommon.ForbiddenBody()), nil
		case http.StatusConflict:
			return gen.PostStaffPersonCompetencesByIdRenew409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PostStaffPersonCompetencesByIdRenew400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("staff: renew competence row %d: %w", req.Id, err)
	}
	out, err := s.onePersonCompetence(ctx, created, a, caller.Today)
	if err != nil {
		return nil, err
	}
	return gen.PostStaffPersonCompetencesByIdRenew201JSONResponse(out), nil
}

// dateChange is a date's change from before to after as an event names it,
// each side absent when empty; false when neither side has a date.
func dateChange(before, after pgtype.Date) (fieldChange, bool) {
	var c fieldChange
	if before.Valid {
		c.From = openapi_types.Date{Time: before.Time}
	}
	if after.Valid {
		c.To = openapi_types.Date{Time: after.Time}
	}
	return c, before.Valid || after.Valid
}

// PostStaffPersonCompetencesByIdEvidence Add a competence row's evidence
// (POST /api/v1/staff/person-competences/{id}/evidence)
//
// The first write the person makes into their own archive (S6): the row's
// person on their own current row, and staff:manage or staff:personal-data
// as any document. The file is judged as the documents' upload judges one,
// written to the store before the transaction — a store that cannot write is
// 503 with nothing recorded — and filed under the seeded certificate
// category, with no employment, under the person's lock; whatever refuses or
// fails after the write removes the object. A second upload replaces the
// row's reference and leaves the earlier document in the archive.
func (s *server) PostStaffPersonCompetencesByIdEvidence(ctx context.Context, req gen.PostStaffPersonCompetencesByIdEvidenceRequestObject) (gen.PostStaffPersonCompetencesByIdEvidenceResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	seen, a, err := s.visibleCompetenceRow(ctx, caller, req.Id)
	if _, ok := asRefusal(err); ok {
		return gen.PostStaffPersonCompetencesByIdEvidence404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !filesEvidence(caller, a) {
		return gen.PostStaffPersonCompetencesByIdEvidence403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	form := readDocumentForm(req.Body)
	parsed, f := parseUpload(form)
	// The evidence's form is the file alone: the category is the
	// certificate one, and no date is read.
	for _, field := range []string{documentCategoryField, documentValidToField, documentSignedAtField} {
		delete(f.errs, field)
	}
	if f.failed() {
		return gen.PostStaffPersonCompetencesByIdEvidence400ApplicationProblemPlusJSONResponse(invalid(invalidDocumentTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	personID := seen.StaffPersonCompetence.PersonID
	key := documentObjectKey(personID)
	if err := s.objectPut(ctx, key, bytes.NewReader(form.data), parsed.kind.ContentType); err != nil {
		s.deps.Logger.ErrorContext(ctx, "staff: a competence's evidence could not be written to the store", "person_id", personID, "error", err.Error())
		return gen.PostStaffPersonCompetencesByIdEvidence503ApplicationProblemPlusJSONResponse(storageUnavailable()), nil
	}
	var saved store.GetPersonCompetenceRow
	err = s.withCompetenceRow(ctx, caller, seen, func(ctx context.Context, txq *store.Queries, l lockedCompetenceRow) error {
		a = l.a
		if !filesEvidence(caller, a) {
			return forbidden()
		}
		if isSuperseded(l.row) {
			return supersededRow()
		}
		count, err := txq.CountPersonDocuments(ctx, personID)
		if err != nil {
			return fmt.Errorf("staff: count person %d's documents: %w", personID, err)
		}
		if count >= maxDocumentsPerPerson {
			return refuseConflict(conflict(codeDocumentCountExceeded, "Too many documents",
				fmt.Sprintf("A person has at most %d documents, counted whatever you may read. Remove one first.", maxDocumentsPerPerson)))
		}
		category, err := certificateCategory(ctx, txq)
		if err != nil {
			return err
		}
		document, err := txq.InsertDocument(ctx, store.InsertDocumentParams{
			PersonID: personID, CategoryID: category.ID, ObjectKey: key, FileName: documentFileName(form.fileName),
			ContentType: parsed.kind.ContentType, SizeBytes: int64(len(form.data)), UploadedByUserID: caller.UserID, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("staff: file person %d's evidence: %w", personID, err)
		}
		values, err := documentValues(document, category)
		if err != nil {
			return err
		}
		if err := s.recordDocumentEvent(ctx, txq, document, eventDocumentAdded, who, diffFields(nil, values), category); err != nil {
			return err
		}
		row, err := txq.SetPersonCompetenceEvidence(ctx, store.SetPersonCompetenceEvidenceParams{
			ID: l.row.ID, EvidenceDocumentID: document.ID, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("staff: name competence row %d's evidence: %w", l.row.ID, err)
		}
		if saved, err = readCompetenceRow(ctx, txq, row.ID); err != nil {
			return err
		}
		return s.recordCompetenceEvent(ctx, txq, personID, row.ID, eventCompetenceEvidenceAdded, who, l.competence.Key,
			changes{"documentId": fieldChange{To: document.ID}})
	})
	if err != nil {
		s.removeObject(ctx, personID, key)
	}
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPersonCompetencesByIdEvidence404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PostStaffPersonCompetencesByIdEvidence404Response{}, nil
		case http.StatusForbidden:
			return gen.PostStaffPersonCompetencesByIdEvidence403JSONResponse(apicommon.ForbiddenBody()), nil
		case http.StatusConflict:
			return gen.PostStaffPersonCompetencesByIdEvidence409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PostStaffPersonCompetencesByIdEvidence400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("staff: file competence row %d's evidence: %w", req.Id, err)
	}
	out, err := s.onePersonCompetence(ctx, saved, a, caller.Today)
	if err != nil {
		return nil, err
	}
	return gen.PostStaffPersonCompetencesByIdEvidence200JSONResponse(out), nil
}

// filesEvidence is whether the caller files a row's evidence (S6): the row's
// person, or anyone who files documents.
func filesEvidence(caller *callerContext, a audience) bool {
	return a.Self || caller.writesDocuments()
}

// certificateCategory is the seeded category evidence is filed under,
// shared against an archive until the transaction ends.
func certificateCategory(ctx context.Context, txq *store.Queries) (store.StaffDocumentCategory, error) {
	categories, err := txq.ListDocumentCategories(ctx)
	if err != nil {
		return store.StaffDocumentCategory{}, fmt.Errorf("staff: list the document categories: %w", err)
	}
	for _, c := range categories {
		if c.Seeded && c.Key == certificateCategoryKey {
			shared, err := txq.ShareDocumentCategory(ctx, c.ID)
			if err != nil {
				return shared, fmt.Errorf("staff: read document category %d: %w", c.ID, err)
			}
			return shared, nil
		}
	}
	return store.StaffDocumentCategory{}, fmt.Errorf("staff: the seeded %s document category is missing", certificateCategoryKey)
}
