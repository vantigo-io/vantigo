package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the competence catalogue (staff competence design S1, S2,
// §3.1, §3.2): the categories — the five seeds and the company's own — and
// the competences, the Norwegian certificates seeded, added and changed by
// HR. The catalogue is about no person: every caller reads its visible rows
// (the coordinator's decision on delivery A's Task 3a — the person declaring
// a competence on My page needs it), and staff:manage alone reads the hidden
// and archived competences, when it asks. A seeded category never changes
// (seed_immutable) and a company one is archived only while no competence in
// use is filed under it (category_in_use). A seeded competence changes its
// names, its lead, its issuer hint and whether it is hidden, nothing else; a
// company competence's key and kind never change, and its hasLevel is fixed
// once a person's row names it (competence_in_use). Archiving a competence is
// never refused for the rows that name it (reading 12): history outlives the
// catalogue. Each competence write is an installation event —
// competence_created, competence_updated — as the absence types' are; a
// category's is none, as a document category's.

// The unique indexes on the catalogue's keys.
const (
	competenceCategoryKeyIndex = "ux_competence_categories_key"
	competenceKeyIndex         = "ux_competences_key"
)

// competenceCategoryResponse is one category in full.
func competenceCategoryResponse(c store.StaffCompetenceCategory) gen.StaffCompetenceCategory {
	return gen.StaffCompetenceCategory{
		Id:         c.ID,
		Key:        c.Key,
		Names:      gen.StaffCompetenceNames{En: c.NameEn, Nb: c.NameNb},
		Seeded:     c.Seeded,
		ArchivedAt: c.ArchivedAt,
		Sort:       c.Sort,
		Revision:   c.Revision,
	}
}

// competenceResponse is one competence in full.
func competenceResponse(c store.StaffCompetence) gen.StaffCompetence {
	return gen.StaffCompetence{
		Id:               c.ID,
		CategoryId:       c.CategoryID,
		Key:              c.Key,
		Names:            gen.StaffCompetenceNames{En: c.NameEn, Nb: c.NameNb},
		Kind:             gen.StaffCompetenceKind(c.Kind),
		HasLevel:         c.HasLevel,
		ValidityMonths:   int32Of(c.ValidityMonths),
		ReminderLeadDays: int32Of(c.ReminderLeadDays),
		IssuerHint:       c.IssuerHint,
		Basis:            c.Basis,
		Seeded:           c.Seeded,
		Hidden:           c.Hidden,
		ArchivedAt:       c.ArchivedAt,
		Sort:             c.Sort,
		Revision:         c.Revision,
	}
}

// GetStaffCompetenceCategories List competence categories
// (GET /api/v1/staff/competence-categories)
//
// Every caller reads every category, archived ones included, so a competence
// — and a person's row through it — can still name its own.
func (s *server) GetStaffCompetenceCategories(ctx context.Context, _ gen.GetStaffCompetenceCategoriesRequestObject) (gen.GetStaffCompetenceCategoriesResponseObject, error) {
	rows, err := store.New(s.deps.Pool).ListCompetenceCategories(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: list the competence categories: %w", err)
	}
	return gen.GetStaffCompetenceCategories200JSONResponse(mapRows(rows, competenceCategoryResponse)), nil
}

// PostStaffCompetenceCategories Add a competence category
// (POST /api/v1/staff/competence-categories)
func (s *server) PostStaffCompetenceCategories(ctx context.Context, req gen.PostStaffCompetenceCategoriesRequestObject) (gen.PostStaffCompetenceCategoriesResponseObject, error) {
	var body gen.StaffCompetenceCategoryRequest
	if req.Body != nil {
		body = *req.Body
	}
	var f fieldErrors
	if !competenceCategoryKeyPattern.MatchString(body.Key) {
		f.add("key", "A key is 1 to 40 characters of a–z, 0–9 and hyphens")
	}
	en, nb := parseCompetenceNames(body.Names, &f)
	if f.failed() {
		return gen.PostStaffCompetenceCategories400ApplicationProblemPlusJSONResponse(invalid(invalidCompetenceCategoryTitle, f)), nil
	}
	created, err := store.New(s.deps.Pool).InsertCompetenceCategory(ctx, store.InsertCompetenceCategoryParams{
		Key: body.Key, NameEn: en, NameNb: nb, Now: s.deps.Clock(),
	})
	if db.IsUniqueViolation(err, competenceCategoryKeyIndex) {
		return gen.PostStaffCompetenceCategories400ApplicationProblemPlusJSONResponse(invalid(invalidCompetenceCategoryTitle,
			fieldErrors{errs: fieldError("key", "Another category already has this key")})), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: add a competence category: %w", err)
	}
	return gen.PostStaffCompetenceCategories201JSONResponse(competenceCategoryResponse(created)), nil
}

// PutStaffCompetenceCategoriesById Change a competence category
// (PUT /api/v1/staff/competence-categories/{id})
//
// The category's row is taken FOR NO KEY UPDATE, which a competence write
// filing under it takes too: an archive and a competence filed under the
// category are judged one after the other.
func (s *server) PutStaffCompetenceCategoriesById(ctx context.Context, req gen.PutStaffCompetenceCategoriesByIdRequestObject) (gen.PutStaffCompetenceCategoriesByIdResponseObject, error) {
	var body gen.StaffCompetenceCategoryChangeRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffCompetenceCategoriesById400ApplicationProblemPlusJSONResponse(invalid(invalidCompetenceCategoryTitle, badIfMatch())), nil
	}
	var f fieldErrors
	en, nb := parseCompetenceNames(body.Names, &f)
	if f.failed() {
		return gen.PutStaffCompetenceCategoriesById400ApplicationProblemPlusJSONResponse(invalid(invalidCompetenceCategoryTitle, f)), nil
	}
	var saved store.StaffCompetenceCategory
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		row, err := txq.LockCompetenceCategory(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock competence category %d: %w", req.Id, err)
		}
		if err := checkRevision("competence category", row.Revision, revision); err != nil {
			return err
		}
		if row.Seeded {
			return refuseConflict(conflict(codeSeedImmutable, "Seeded category",
				fmt.Sprintf("The category %s is one every installation starts with, and it does not change.", row.NameEn)))
		}
		if body.Archived && row.ArchivedAt == nil {
			inUse, err := txq.CompetenceCategoryInUse(ctx, row.ID)
			if err != nil {
				return fmt.Errorf("staff: read whether competence category %d is in use: %w", row.ID, err)
			}
			if inUse {
				return refuseConflict(conflict(codeCategoryInUse, "Category in use",
					fmt.Sprintf("Competences in use are filed under %s. Move or archive them first.", row.NameEn)))
			}
		}
		saved, err = txq.UpdateCompetenceCategory(ctx, store.UpdateCompetenceCategoryParams{
			ID: row.ID, NameEn: en, NameNb: nb, Archived: body.Archived, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("staff: change competence category %d: %w", row.ID, err)
		}
		return nil
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusNotFound {
			return gen.PutStaffCompetenceCategoriesById404Response{}, nil
		}
		return gen.PutStaffCompetenceCategoriesById409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PutStaffCompetenceCategoriesById200JSONResponse(competenceCategoryResponse(saved)), nil
}

// GetStaffCompetences List competences
// (GET /api/v1/staff/competences)
//
// Every caller reads the competences neither hidden nor archived;
// includeHidden and includeArchived add the others for staff:manage, and are
// ignored for anyone else.
func (s *server) GetStaffCompetences(ctx context.Context, req gen.GetStaffCompetencesRequestObject) (gen.GetStaffCompetencesResponseObject, error) {
	manage := s.has(ctx, permManage)
	rows, err := store.New(s.deps.Pool).ListCompetences(ctx, store.ListCompetencesParams{
		IncludeHidden:   manage && req.Params.IncludeHidden != nil && *req.Params.IncludeHidden,
		IncludeArchived: manage && req.Params.IncludeArchived != nil && *req.Params.IncludeArchived,
		CategoryID:      req.Params.CategoryId,
	})
	if err != nil {
		return nil, fmt.Errorf("staff: list the competences: %w", err)
	}
	return gen.GetStaffCompetences200JSONResponse(mapRows(rows, competenceResponse)), nil
}

// lockFilingCategory is the category a competence is about to be filed
// under, held until the transaction ends: unknown, or archived when the
// competence is not already filed there, is a field error on categoryId.
func lockFilingCategory(ctx context.Context, txq *store.Queries, id int64, current int64) error {
	c, err := txq.LockCompetenceCategory(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return refuseInvalid(invalidCompetenceTitle, fieldErrors{errs: fieldError("categoryId", fmt.Sprintf("No competence category has id %d", id))})
	} else if err != nil {
		return fmt.Errorf("staff: lock competence category %d: %w", id, err)
	}
	if c.ArchivedAt != nil && c.ID != current {
		return refuseInvalid(invalidCompetenceTitle, fieldErrors{errs: fieldError("categoryId",
			fmt.Sprintf("The category %s is archived; no competence is filed under it", c.NameEn))})
	}
	return nil
}

// PostStaffCompetences Add a competence
// (POST /api/v1/staff/competences)
func (s *server) PostStaffCompetences(ctx context.Context, req gen.PostStaffCompetencesRequestObject) (gen.PostStaffCompetencesResponseObject, error) {
	var body gen.StaffCompetenceRequest
	if req.Body != nil {
		body = *req.Body
	}
	var f fieldErrors
	if !competenceKeyPattern.MatchString(body.Key) {
		f.add("key", "A key is 1 to 60 characters of a–z, 0–9 and hyphens")
	}
	en, nb := parseCompetenceNames(body.Names, &f)
	kind := string(body.Kind)
	if _, msg := parseEnum("A kind", &kind, competenceKinds); msg != "" {
		f.add("kind", msg)
	}
	spec := parseCompetenceSpec(kind, body.HasLevel, body.ValidityMonths, body.ReminderLeadDays, body.IssuerHint, &f)
	if f.failed() {
		return gen.PostStaffCompetences400ApplicationProblemPlusJSONResponse(invalid(invalidCompetenceTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var created store.StaffCompetence
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if err := lockFilingCategory(ctx, txq, body.CategoryId, 0); err != nil {
			return err
		}
		sort, err := txq.NextCompetenceSort(ctx)
		if err != nil {
			return fmt.Errorf("staff: read the next competence's place: %w", err)
		}
		created, err = txq.InsertCompetence(ctx, store.InsertCompetenceParams{
			CategoryID: body.CategoryId, Key: body.Key, NameEn: en, NameNb: nb, Kind: kind, HasLevel: spec.HasLevel,
			ValidityMonths: spec.ValidityMonths, ReminderLeadDays: spec.ReminderLeadDays, IssuerHint: spec.IssuerHint,
			Sort: sort, Now: s.deps.Clock(),
		})
		if db.IsUniqueViolation(err, competenceKeyIndex) {
			return refuseInvalid(invalidCompetenceTitle, fieldErrors{errs: fieldError("key", "Another competence already has this key")})
		}
		if err != nil {
			return fmt.Errorf("staff: add a competence: %w", err)
		}
		return s.recordCompetenceChange(ctx, txq, eventCompetenceCreated, who, nil, created)
	})
	if r, ok := asRefusal(err); ok {
		return gen.PostStaffCompetences400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PostStaffCompetences201JSONResponse(competenceResponse(created)), nil
}

// PutStaffCompetencesById Change a competence
// (PUT /api/v1/staff/competences/{id})
//
// The competence's row is taken FOR NO KEY UPDATE, which a person's write
// sharing it (ShareCompetence) waits for: a hide, an archive or a change of
// its level and a new row naming it are judged one after the other, and
// "no person's row names it" holds when the change commits.
func (s *server) PutStaffCompetencesById(ctx context.Context, req gen.PutStaffCompetencesByIdRequestObject) (gen.PutStaffCompetencesByIdResponseObject, error) {
	var body gen.StaffCompetenceChangeRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffCompetencesById400ApplicationProblemPlusJSONResponse(invalid(invalidCompetenceTitle, badIfMatch())), nil
	}
	if f := s.unknownProperties(ctx, competenceChangeSchema); f.failed() {
		return gen.PutStaffCompetencesById400ApplicationProblemPlusJSONResponse(invalid(invalidCompetenceTitle, f)), nil
	}
	var f fieldErrors
	en, nb := parseCompetenceNames(body.Names, &f)
	if f.failed() {
		return gen.PutStaffCompetencesById400ApplicationProblemPlusJSONResponse(invalid(invalidCompetenceTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var saved store.StaffCompetence
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		row, err := txq.LockCompetence(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock competence %d: %w", req.Id, err)
		}
		var f fieldErrors
		spec := parseCompetenceSpec(row.Kind, body.HasLevel, body.ValidityMonths, body.ReminderLeadDays, body.IssuerHint, &f)
		if f.failed() {
			return refuseInvalid(invalidCompetenceTitle, f)
		}
		if err := checkRevision("competence", row.Revision, revision); err != nil {
			return err
		}
		if row.Seeded && !seedCompetenceChangeAllowed(row, body, spec) {
			return refuseConflict(conflict(codeSeedImmutable, "Seeded competence",
				fmt.Sprintf("%s is one of the certificates every installation starts with: its names, its reminder lead, its issuer hint and whether it is hidden change, nothing else.", row.NameEn)))
		}
		if body.CategoryId != row.CategoryID {
			if err := lockFilingCategory(ctx, txq, body.CategoryId, row.CategoryID); err != nil {
				return err
			}
		} else if !body.Archived && row.ArchivedAt != nil {
			// A restore files the competence again: under an archived
			// category it would be offered from a category nobody sees.
			c, err := txq.LockCompetenceCategory(ctx, row.CategoryID)
			if err != nil {
				return fmt.Errorf("staff: lock competence category %d: %w", row.CategoryID, err)
			}
			if c.ArchivedAt != nil {
				return refuseInvalid(invalidCompetenceTitle, fieldErrors{errs: fieldError("archived",
					fmt.Sprintf("The category %s is archived; restore it first", c.NameEn))})
			}
		}
		if !row.Seeded && spec.HasLevel != row.HasLevel {
			inUse, err := txq.CompetenceInUse(ctx, row.ID)
			if err != nil {
				return fmt.Errorf("staff: read whether competence %d is in use: %w", row.ID, err)
			}
			if inUse {
				return refuseConflict(conflict(codeCompetenceInUse, "Competence in use",
					fmt.Sprintf("People's rows name %s, so whether it takes a level does not change now; add another competence instead.", row.NameEn)))
			}
		}
		saved, err = txq.UpdateCompetence(ctx, store.UpdateCompetenceParams{
			ID: row.ID, CategoryID: body.CategoryId, NameEn: en, NameNb: nb, HasLevel: spec.HasLevel,
			ValidityMonths: spec.ValidityMonths, ReminderLeadDays: spec.ReminderLeadDays, IssuerHint: spec.IssuerHint,
			Hidden: body.Hidden, Archived: body.Archived, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("staff: change competence %d: %w", row.ID, err)
		}
		return s.recordCompetenceChange(ctx, txq, eventCompetenceUpdated, who, &row, saved)
	})
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffCompetencesById404Response{}, nil
		case http.StatusConflict:
			return gen.PutStaffCompetencesById409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffCompetencesById400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.PutStaffCompetencesById200JSONResponse(competenceResponse(saved)), nil
}

// seedCompetenceChangeAllowed is whether a change of a seed keeps its
// category, its level, its validity and its being in the catalogue as they
// are (S1): its names, its lead, its issuer hint and hidden change.
func seedCompetenceChangeAllowed(row store.StaffCompetence, body gen.StaffCompetenceChangeRequest, spec competenceSpec) bool {
	return body.CategoryId == row.CategoryID && spec.HasLevel == row.HasLevel &&
		equalInt16(spec.ValidityMonths, row.ValidityMonths) && !body.Archived
}

// equalInt16 is whether two optional numbers are the same.
func equalInt16(a, b *int16) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

// recordCompetenceChange records a competence's create (before nil) or
// change as an installation event: the competence's id and the fields that
// changed.
func (s *server) recordCompetenceChange(ctx context.Context, txq *store.Queries, kind string, who actor, before *store.StaffCompetence, after store.StaffCompetence) error {
	var old map[string]json.RawMessage
	if before != nil {
		var err error
		if old, err = payloadValues(competenceResponse(*before), "id", "revision"); err != nil {
			return err
		}
	}
	values, err := payloadValues(competenceResponse(after), "id", "revision")
	if err != nil {
		return err
	}
	return s.recordInstallationChange(ctx, txq, kind, who, "competenceId", after.ID, diffFields(old, values))
}
