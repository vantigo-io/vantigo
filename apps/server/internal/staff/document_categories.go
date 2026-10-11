package staff

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is the document categories (design S14, §3.6): the six seeds
// and the company's own, listed for every reader of the structure, added and
// changed by HR. A category's retention class is fixed when it is created,
// since the erase reads it from every document filed under it (S15), and
// restricted — a health category — is true exactly when the class is E,
// which only this file writes. A seed never changes (seed_immutable); a
// company category is renamed, or archived, which takes it out of the upload
// and keeps it on the documents already filed — and while any document is
// filed under it, it is not archived (category_in_use), so the archive never
// hides a category a document still names from the picker HR moves it with.

// categoryKeyIndex is the unique index on a category's key.
const categoryKeyIndex = "ux_document_categories_key"

// categoryKeyPattern is a company category's key: 1 to 40 of a–z, 0–9 and
// the hyphen, the alphabet of the seeds' own keys.
var categoryKeyPattern = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// categoryNameMaxLength is the names' columns' width.
const categoryNameMaxLength = 100

// categoryClasses are the classes a category may have: A (pay and contract
// documentation), D (personnel administration), E (health) — design S15's
// classes that hold documents.
var categoryClasses = []string{"A", "D", "E"}

// restrictedClass is the class a restricted category has, and only it.
const restrictedClass = "E"

// documentCategoryResponse is one category in full.
func documentCategoryResponse(c store.StaffDocumentCategory) gen.StaffDocumentCategory {
	return gen.StaffDocumentCategory{
		Id:         c.ID,
		Key:        c.Key,
		Names:      gen.StaffDocumentCategoryNames{En: c.NameEn, Nb: c.NameNb},
		Class:      gen.StaffDocumentClass(c.RetentionClass),
		Restricted: c.Restricted,
		Seeded:     c.Seeded,
		ArchivedAt: c.ArchivedAt,
		Sort:       c.Sort,
		Revision:   c.Revision,
	}
}

// parseCategoryNames is a category's two names, each 1 to 100 characters.
func parseCategoryNames(names gen.StaffDocumentCategoryNames, f *fieldErrors) (string, string) {
	en, msg := requiredText("An English name", names.En, categoryNameMaxLength)
	f.add("names.en", msg)
	nb, msg := requiredText("A Norwegian name", names.Nb, categoryNameMaxLength)
	f.add("names.nb", msg)
	return en, nb
}

// GetStaffDocumentCategories List document categories
// (GET /api/v1/staff/document-categories)
//
// The structure-GET precedent (readsStructure): the contract asks only
// staff:access, and the list is the directory's, HR's and
// staff:personal-data's; anyone else is the uniform 403. The person reading
// their own documents needs no list — each document carries its category.
func (s *server) GetStaffDocumentCategories(ctx context.Context, _ gen.GetStaffDocumentCategoriesRequestObject) (gen.GetStaffDocumentCategoriesResponseObject, error) {
	if !s.readsStructure(ctx) && !s.has(ctx, permPersonalData) {
		return gen.GetStaffDocumentCategories403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	rows, err := store.New(s.deps.Pool).ListDocumentCategories(ctx)
	if err != nil {
		return nil, fmt.Errorf("staff: list the document categories: %w", err)
	}
	return gen.GetStaffDocumentCategories200JSONResponse(mapRows(rows, documentCategoryResponse)), nil
}

// PostStaffDocumentCategories Add a document category
// (POST /api/v1/staff/document-categories)
func (s *server) PostStaffDocumentCategories(ctx context.Context, req gen.PostStaffDocumentCategoriesRequestObject) (gen.PostStaffDocumentCategoriesResponseObject, error) {
	var body gen.StaffDocumentCategoryRequest
	if req.Body != nil {
		body = *req.Body
	}
	var f fieldErrors
	if !categoryKeyPattern.MatchString(body.Key) {
		f.add("key", "A key is 1 to 40 characters of a–z, 0–9 and hyphens")
	}
	en, nb := parseCategoryNames(body.Names, &f)
	class := string(body.Class)
	if !slices.Contains(categoryClasses, class) {
		f.add("class", fmt.Sprintf("A category's class is A, D or E, but was '%s'", class))
	}
	if f.failed() {
		return gen.PostStaffDocumentCategories400ApplicationProblemPlusJSONResponse(invalid(invalidCategoryTitle, f)), nil
	}
	created, err := store.New(s.deps.Pool).InsertDocumentCategory(ctx, store.InsertDocumentCategoryParams{
		Key: body.Key, NameEn: en, NameNb: nb, RetentionClass: class, Restricted: class == restrictedClass, Now: s.deps.Clock(),
	})
	if db.IsUniqueViolation(err, categoryKeyIndex) {
		return gen.PostStaffDocumentCategories400ApplicationProblemPlusJSONResponse(invalid(invalidCategoryTitle,
			fieldErrors{errs: fieldError("key", "Another category already has this key")})), nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff: add a document category: %w", err)
	}
	return gen.PostStaffDocumentCategories201JSONResponse(documentCategoryResponse(created)), nil
}

// PutStaffDocumentCategoriesById Change a document category
// (PUT /api/v1/staff/document-categories/{id})
//
// The category's row is taken FOR NO KEY UPDATE, which an upload's FOR SHARE
// on it waits for and holds off: an archive and an upload into the category
// are judged one after the other, so "no document is filed under it" holds
// when the archive commits.
func (s *server) PutStaffDocumentCategoriesById(ctx context.Context, req gen.PutStaffDocumentCategoriesByIdRequestObject) (gen.PutStaffDocumentCategoriesByIdResponseObject, error) {
	var body gen.StaffDocumentCategoryChangeRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffDocumentCategoriesById400ApplicationProblemPlusJSONResponse(invalid(invalidCategoryTitle, badIfMatch())), nil
	}
	var f fieldErrors
	en, nb := parseCategoryNames(body.Names, &f)
	if f.failed() {
		return gen.PutStaffDocumentCategoriesById400ApplicationProblemPlusJSONResponse(invalid(invalidCategoryTitle, f)), nil
	}
	var saved store.StaffDocumentCategory
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		row, err := txq.LockDocumentCategory(ctx, req.Id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("staff: lock document category %d: %w", req.Id, err)
		}
		if err := checkRevision("document category", row.Revision, revision); err != nil {
			return err
		}
		if row.Seeded {
			return refuseConflict(conflict(codeSeedImmutable, "Seeded category",
				fmt.Sprintf("The category %s is one every installation starts with, and it does not change.", row.NameEn)))
		}
		if body.Archived && row.ArchivedAt == nil {
			inUse, err := txq.DocumentCategoryInUse(ctx, row.ID)
			if err != nil {
				return fmt.Errorf("staff: read whether document category %d is in use: %w", row.ID, err)
			}
			if inUse {
				return refuseConflict(conflict(codeCategoryInUse, "Category in use",
					fmt.Sprintf("Documents are filed under %s. Move them to another category first.", row.NameEn)))
			}
		}
		saved, err = txq.UpdateDocumentCategory(ctx, store.UpdateDocumentCategoryParams{
			ID: row.ID, NameEn: en, NameNb: nb, Archived: body.Archived, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("staff: change document category %d: %w", row.ID, err)
		}
		return nil
	})
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusNotFound {
			return gen.PutStaffDocumentCategoriesById404Response{}, nil
		}
		return gen.PutStaffDocumentCategoriesById409ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PutStaffDocumentCategoriesById200JSONResponse(documentCategoryResponse(saved)), nil
}
