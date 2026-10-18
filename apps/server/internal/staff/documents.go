package staff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is a person's document archive (design S14, §3.6, §5, §6
// Documents). Who reads a document is the person, staff:manage and
// staff:personal-data — a health document, one in a restricted category, the
// person and staff:personal-data alone — and to anyone else it is the bare
// 404 an unknown id is. Who writes one is staff:manage or staff:personal-data,
// a health document staff:personal-data alone (category_restricted); the
// person never does, and everyone else gets the uniform 403. The bytes are
// in the object store, reached only through objects.go's accessors and never
// inside a transaction: the upload writes the object first and removes it
// again on a refusal, the download reads the row in a snapshot and then the
// object, and the delete removes the row and queues the object in one
// transaction, then removes it. The locks are person → employment → document
// (§4 Locking).

// contractCategoryKey is the seeded category whose signed document records
// its employment's signing date (plan reading 5).
const contractCategoryKey = "contract"

// writesDocuments is whether the caller may file, change or remove any
// document at all: staff:manage or staff:personal-data. A health document
// needs the latter (categoryRestricted).
func (c *callerContext) writesDocuments() bool { return c.Manage || c.PersonalData }

// documentCategorySummary is a category as every document carries it.
func documentCategorySummary(c store.StaffDocumentCategory) gen.StaffDocumentCategorySummary {
	return gen.StaffDocumentCategorySummary{
		Id:         c.ID,
		Key:        c.Key,
		Names:      gen.StaffDocumentCategoryNames{En: c.NameEn, Nb: c.NameNb},
		Class:      gen.StaffDocumentClass(c.RetentionClass),
		Restricted: c.Restricted,
	}
}

// documentResponse is one document with its category. The object key is not
// on it, and there is no field on the generated type to put it in.
func documentResponse(d store.StaffDocument, c store.StaffDocumentCategory) gen.StaffDocument {
	return gen.StaffDocument{
		Id:           d.ID,
		PersonId:     d.PersonID,
		Category:     documentCategorySummary(c),
		FileName:     d.FileName,
		ContentType:  d.ContentType,
		SizeBytes:    d.SizeBytes,
		UploadedAt:   d.CreatedAt,
		Revision:     d.Revision,
		ValidTo:      dateOf(d.ValidTo),
		SignedAt:     dateOf(d.SignedAt),
		EmploymentId: d.EmploymentID,
		CaseId:       d.CaseID,
	}
}

// documentEventFields is what a document's events name: the category's key,
// the file name, the dates and the case — the document as a person would
// describe it.
type documentEventFields struct {
	Category string              `json:"category"`
	FileName string              `json:"fileName"`
	ValidTo  *openapi_types.Date `json:"validTo,omitempty"`
	SignedAt *openapi_types.Date `json:"signedAt,omitempty"`
	// CaseID is the sick-leave case a health document is filed under
	// (Task 5).
	CaseID *int64 `json:"caseId,omitempty"`
}

// documentValues is a document's fields as its events name them.
func documentValues(d store.StaffDocument, c store.StaffDocumentCategory) (map[string]json.RawMessage, error) {
	return payloadValues(documentEventFields{
		Category: c.Key, FileName: d.FileName, ValidTo: dateOf(d.ValidTo), SignedAt: dateOf(d.SignedAt), CaseID: d.CaseID,
	})
}

// equalID is whether two optional ids are the same.
func equalID(a, b *int64) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

// recordDocumentEvent records kind for the document's person with c as its
// fields and the document's id, its classes the categories' it was filed
// under before and after — which decide its group (shapePayload) and what
// the erase must reach. Nothing when c is empty.
func (s *server) recordDocumentEvent(ctx context.Context, txq *store.Queries, d store.StaffDocument, kind string, who actor,
	c changes, categories ...store.StaffDocumentCategory,
) error {
	if len(c) == 0 {
		return nil
	}
	var classes []string
	for _, category := range categories {
		if !slices.Contains(classes, category.RetentionClass) {
			classes = append(classes, category.RetentionClass)
		}
	}
	slices.Sort(classes)
	personID := d.PersonID
	return s.recordEvent(ctx, txq, &personID, kind, who, map[string]any{"documentId": d.ID, "fields": c}, classes)
}

// readDocument is one document with its category on q, or the 404 refusal.
func readDocument(ctx context.Context, q *store.Queries, id int64) (store.GetDocumentRow, error) {
	row, err := q.GetDocument(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, notFound()
	}
	if err != nil {
		return row, fmt.Errorf("staff: read document %d: %w", id, err)
	}
	return row, nil
}

// visibleDocument is one document the caller may read, judged in one
// snapshot: the row, its category and the caller's audience on its person.
// A document they may not read is the 404 an unknown id is.
func (s *server) visibleDocument(ctx context.Context, caller *callerContext, id int64) (store.GetDocumentRow, error) {
	var row store.GetDocumentRow
	err := s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		var err error
		if row, err = readDocument(ctx, q, id); err != nil {
			return err
		}
		a, err := personAudience(ctx, q, caller, row.StaffDocument.PersonID)
		if err != nil {
			return err
		}
		if !a.readsDocument(row.StaffDocumentCategory.Restricted) {
			return notFound()
		}
		return nil
	})
	return row, err
}

// lockWritableDocument takes a document's row under its person's lock for a
// change or a removal and judges it again as it is now, as visibleDocument
// does: gone, or moved since into a category the caller may not read, it is
// the 404. One the caller reads but may not write — a health document of
// their own, to HR without staff:personal-data — is category_restricted.
func lockWritableDocument(ctx context.Context, txq *store.Queries, caller *callerContext, id int64) (store.StaffDocument, store.StaffDocumentCategory, error) {
	d, err := txq.LockDocument(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, store.StaffDocumentCategory{}, notFound()
	} else if err != nil {
		return d, store.StaffDocumentCategory{}, fmt.Errorf("staff: lock document %d: %w", id, err)
	}
	c, err := txq.ShareDocumentCategory(ctx, d.CategoryID)
	if err != nil {
		return d, c, fmt.Errorf("staff: read document %d's category: %w", id, err)
	}
	a, err := personAudience(ctx, txq, caller, d.PersonID)
	if err != nil {
		return d, c, err
	}
	if !a.readsDocument(c.Restricted) {
		return d, c, notFound()
	}
	if c.Restricted && !caller.PersonalData {
		return d, c, categoryRestricted()
	}
	return d, c, nil
}

// fileableCategory is the category a document is about to be filed under,
// held against an archive until the transaction ends: unknown or archived is
// a field error on categoryId. A document's own category is never archived —
// a category documents are filed under is not (category_in_use) — so the
// rule holds for a change as for an upload.
func fileableCategory(ctx context.Context, txq *store.Queries, id int64) (store.StaffDocumentCategory, error) {
	c, err := txq.ShareDocumentCategory(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, refuseInvalid(invalidDocumentTitle, fieldErrors{errs: fieldError(documentCategoryField, fmt.Sprintf("No document category has id %d", id))})
	} else if err != nil {
		return c, fmt.Errorf("staff: read document category %d: %w", id, err)
	}
	if c.ArchivedAt != nil {
		return c, refuseInvalid(invalidDocumentTitle, fieldErrors{errs: fieldError(documentCategoryField,
			fmt.Sprintf("The category %s is archived; no new document is filed under it", c.NameEn))})
	}
	return c, nil
}

// GetStaffPeopleByIdDocuments List a person's documents
// (GET /api/v1/staff/people/{id}/documents)
func (s *server) GetStaffPeopleByIdDocuments(ctx context.Context, req gen.GetStaffPeopleByIdDocumentsRequestObject) (gen.GetStaffPeopleByIdDocumentsResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	var rows []store.ListPersonDocumentsRow
	err = s.withReadTx(ctx, func(ctx context.Context, q *store.Queries) error {
		if _, err := readPerson(ctx, q, req.Id); err != nil {
			return err
		}
		a, err := personAudience(ctx, q, caller, req.Id)
		if err != nil {
			return err
		}
		if !a.reads(groupDocuments) {
			return notFound()
		}
		rows, err = q.ListPersonDocuments(ctx, store.ListPersonDocumentsParams{PersonID: req.Id, WithRestricted: a.reads(groupHealth)})
		if err != nil {
			return fmt.Errorf("staff: list person %d's documents: %w", req.Id, err)
		}
		return nil
	})
	if _, ok := asRefusal(err); ok {
		return gen.GetStaffPeopleByIdDocuments404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.GetStaffPeopleByIdDocuments200JSONResponse(mapRows(rows, func(r store.ListPersonDocumentsRow) gen.StaffDocument {
		return documentResponse(r.StaffDocument, r.StaffDocumentCategory)
	})), nil
}

// GetStaffMeDocuments List my own documents
// (GET /api/v1/staff/me/documents)
//
// The caller's own archive as the person reads it: every document, health
// documents included.
func (s *server) GetStaffMeDocuments(ctx context.Context, _ gen.GetStaffMeDocumentsRequestObject) (gen.GetStaffMeDocumentsResponseObject, error) {
	q := store.New(s.deps.Pool)
	caller, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	if caller.Person == nil {
		return gen.GetStaffMeDocuments404ApplicationProblemPlusJSONResponse(noPerson()), nil
	}
	rows, err := q.ListPersonDocuments(ctx, store.ListPersonDocumentsParams{
		PersonID: *caller.Person, WithRestricted: selfAudience.reads(groupHealth),
	})
	if err != nil {
		return nil, fmt.Errorf("staff: list person %d's documents: %w", *caller.Person, err)
	}
	return gen.GetStaffMeDocuments200JSONResponse(mapRows(rows, func(r store.ListPersonDocumentsRow) gen.StaffDocument {
		return documentResponse(r.StaffDocument, r.StaffDocumentCategory)
	})), nil
}

// PostStaffPeopleByIdDocuments File a document
// (POST /api/v1/staff/people/{id}/documents)
//
// The refusals come in the order that tells a caller least: who may file at
// all (the uniform 403), whether the person exists (404), whether this
// category is theirs to file (category_restricted), then what was sent (400).
// The object is written before the transaction — a store that cannot write
// is 503 with nothing recorded — and under the person's lock the rest is
// judged as it is now: the person not erased (the read-only rule, judged at
// the lock), fewer than 200 documents, the category not archived. A signed
// contract covering exactly one employment with no signing date sets it, the
// employment locked before the document is written. Whatever refuses or fails
// after the write removes the object.
func (s *server) PostStaffPeopleByIdDocuments(ctx context.Context, req gen.PostStaffPeopleByIdDocumentsRequestObject) (gen.PostStaffPeopleByIdDocumentsResponseObject, error) {
	q := store.New(s.deps.Pool)
	caller, err := s.callerFor(ctx, q)
	if err != nil {
		return nil, err
	}
	if !caller.writesDocuments() {
		return gen.PostStaffPeopleByIdDocuments403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	if _, err := readPerson(ctx, q, req.Id); err != nil {
		if _, ok := asRefusal(err); ok {
			return gen.PostStaffPeopleByIdDocuments404Response{}, nil
		}
		return nil, err
	}
	form := readDocumentForm(req.Body)
	parsed, f := parseUpload(form)
	if parsed.categoryID != 0 {
		category, err := q.GetDocumentCategory(ctx, parsed.categoryID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			f.add(documentCategoryField, fmt.Sprintf("No document category has id %d", parsed.categoryID))
		case err != nil:
			return nil, fmt.Errorf("staff: read document category %d: %w", parsed.categoryID, err)
		case category.Restricted && !caller.PersonalData:
			r, _ := asRefusal(categoryRestricted())
			return gen.PostStaffPeopleByIdDocuments403ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if f.failed() {
		return gen.PostStaffPeopleByIdDocuments400ApplicationProblemPlusJSONResponse(invalid(invalidDocumentTitle, f)), nil
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}

	key := documentObjectKey(req.Id)
	if err := s.objectPut(ctx, key, bytes.NewReader(form.data), parsed.kind.ContentType); err != nil {
		s.deps.Logger.ErrorContext(ctx, "staff: a document could not be written to the store", "person_id", req.Id, "error", err.Error())
		return gen.PostStaffPeopleByIdDocuments503ApplicationProblemPlusJSONResponse(storageUnavailable()), nil
	}

	var (
		created  store.StaffDocument
		category store.StaffDocumentCategory
	)
	err = s.withPersonLock(ctx, req.Id, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		count, err := txq.CountPersonDocuments(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("staff: count person %d's documents: %w", req.Id, err)
		}
		if count >= maxDocumentsPerPerson {
			return refuseConflict(conflict(codeDocumentCountExceeded, "Too many documents",
				fmt.Sprintf("A person has at most %d documents, counted whatever you may read. Remove one first.", maxDocumentsPerPerson)))
		}
		if category, err = fileableCategory(ctx, txq, parsed.categoryID); err != nil {
			return err
		}
		employmentID, err := s.fileForEmployment(ctx, txq, req.Id, category, parsed.signedAt, caller, who)
		if err != nil {
			return err
		}
		created, err = txq.InsertDocument(ctx, store.InsertDocumentParams{
			PersonID: req.Id, CategoryID: category.ID, EmploymentID: employmentID, ObjectKey: key,
			FileName: documentFileName(form.fileName), ContentType: parsed.kind.ContentType, SizeBytes: int64(len(form.data)),
			ValidTo: pgDate(parsed.validTo), SignedAt: pgDate(parsed.signedAt), UploadedByUserID: caller.UserID, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("staff: file a document for person %d: %w", req.Id, err)
		}
		values, err := documentValues(created, category)
		if err != nil {
			return err
		}
		return s.recordDocumentEvent(ctx, txq, created, eventDocumentAdded, who, diffFields(nil, values), category)
	})
	if err != nil {
		s.removeObject(ctx, req.Id, key)
	}
	if errors.Is(err, errPersonNotFound) {
		return gen.PostStaffPeopleByIdDocuments404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		if r.status == http.StatusConflict {
			return gen.PostStaffPeopleByIdDocuments409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.PostStaffPeopleByIdDocuments400ApplicationProblemPlusJSONResponse(r.problem), nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PostStaffPeopleByIdDocuments201JSONResponse(documentResponse(created, category)), nil
}

// fileForEmployment is the employment a new document belongs to — the one
// covering its signing day, else today, when exactly one does — and, for a
// signed contract covering exactly one employment that records no signing
// date, records it there (plan reading 5): the employment locked first, its
// change an event of its own. Two covering employments set nothing, since
// which one the contract is for is HR's to say.
func (s *server) fileForEmployment(ctx context.Context, txq *store.Queries, personID int64, category store.StaffDocumentCategory,
	signedAt *openapi_types.Date, caller *callerContext, who actor,
) (*int64, error) {
	day := caller.Today
	if signedAt != nil {
		day = signedAt.Time
	}
	covering, err := txq.CoveringEmployments(ctx, store.CoveringEmploymentsParams{PersonID: personID, Day: dayParam(day)})
	if err != nil {
		return nil, fmt.Errorf("staff: read person %d's employments on %s: %w", personID, day.Format("2006-01-02"), err)
	}
	if len(covering) != 1 {
		return nil, nil
	}
	id := covering[0].ID
	if category.Key != contractCategoryKey || signedAt == nil || covering[0].ContractSignedAt.Valid {
		return &id, nil
	}
	e, err := lockEmployment(ctx, txq, id)
	if err != nil {
		return nil, err
	}
	if e.ContractSignedAt.Valid {
		return &id, nil
	}
	saved, err := txq.SetEmploymentContractSigned(ctx, store.SetEmploymentContractSignedParams{
		ID: id, ContractSignedAt: dayParam(signedAt.Time), Now: s.deps.Clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("staff: record employment %d's signing date: %w", id, err)
	}
	before, err := employmentValues(&e)
	if err != nil {
		return nil, err
	}
	after, err := employmentValues(&saved)
	if err != nil {
		return nil, err
	}
	return &id, s.recordRowChange(ctx, txq, personID, eventEmploymentUpdated, who, id, 0, diffFields(before, after))
}

// documentDownload sets the headers every document read answers with, the
// 404 and the 503 included: never cached, never sniffed, a policy of its own,
// and the name it was uploaded under.
type documentDownload struct {
	body        gen.GetStaffDocumentsByIdResponseObject
	disposition string
}

func (r documentDownload) VisitGetStaffDocumentsByIdResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", documentContentSecurityPolicy)
	if r.disposition != "" {
		w.Header().Set("Content-Disposition", r.disposition)
	}
	return r.body.VisitGetStaffDocumentsByIdResponse(w)
}

// GetStaffDocumentsById Download a document
// (GET /api/v1/staff/documents/{id})
//
// The row is read in a snapshot and the object after it, outside any
// transaction. A row whose object cannot be read is 503: a fault to be seen,
// not a document to be denied.
func (s *server) GetStaffDocumentsById(ctx context.Context, req gen.GetStaffDocumentsByIdRequestObject) (gen.GetStaffDocumentsByIdResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	row, err := s.visibleDocument(ctx, caller, req.Id)
	if _, ok := asRefusal(err); ok {
		return documentDownload{body: gen.GetStaffDocumentsById404Response{}}, nil
	}
	if err != nil {
		return nil, err
	}
	d := row.StaffDocument
	body, err := s.objectGet(ctx, d.ObjectKey)
	if err != nil {
		s.deps.Logger.ErrorContext(ctx, "staff: a document could not be read from the store", "document_id", d.ID, "error", err.Error())
		return documentDownload{body: gen.GetStaffDocumentsById503ApplicationProblemPlusJSONResponse(storageUnavailable())}, nil
	}
	return documentDownload{
		body:        gen.GetStaffDocumentsById200AsteriskResponse{Body: body, ContentType: d.ContentType, ContentLength: d.SizeBytes},
		disposition: contentDisposition(storedDocumentType(d.ContentType), d.FileName),
	}, nil
}

// PutStaffDocumentsById Change a document
// (PUT /api/v1/staff/documents/{id})
//
// A replace of the category and the dates; the employment is replaced only
// when the body names it — null clears it — since the upload derived it and
// a client that never shows it must not lose it by saving. A move into or
// out of a restricted category is staff:personal-data's; a document the
// caller may not read is the 404 before anything is judged.
func (s *server) PutStaffDocumentsById(ctx context.Context, req gen.PutStaffDocumentsByIdRequestObject) (gen.PutStaffDocumentsByIdResponseObject, error) {
	var body gen.StaffDocumentChangeRequest
	if req.Body != nil {
		body = *req.Body
	}
	revision, ok := parseIfMatch(req.Params.IfMatch)
	if !ok {
		return gen.PutStaffDocumentsById400ApplicationProblemPlusJSONResponse(invalid(invalidDocumentTitle, badIfMatch())), nil
	}
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if !caller.writesDocuments() {
		return gen.PutStaffDocumentsById403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	row, err := s.visibleDocument(ctx, caller, req.Id)
	if _, ok := asRefusal(err); ok {
		return gen.PutStaffDocumentsById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}

	var (
		saved store.StaffDocument
		to    store.StaffDocumentCategory
	)
	err = s.withPersonLock(ctx, row.StaffDocument.PersonID, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		d, from, err := lockWritableDocument(ctx, txq, caller, req.Id)
		if err != nil {
			return err
		}
		if err := checkRevision("document", d.Revision, revision); err != nil {
			return err
		}
		if to, err = fileableCategory(ctx, txq, body.CategoryId); err != nil {
			return err
		}
		if to.Restricted && !caller.PersonalData {
			return categoryRestricted()
		}
		employmentID := d.EmploymentID
		if bodyHas(ctx, "employmentId") {
			employmentID = body.EmploymentId
		}
		if body.EmploymentId != nil {
			has, err := txq.PersonHasEmployment(ctx, store.PersonHasEmploymentParams{ID: *body.EmploymentId, PersonID: d.PersonID})
			if err != nil {
				return fmt.Errorf("staff: read employment %d: %w", *body.EmploymentId, err)
			}
			if !has {
				return refuseInvalid(invalidDocumentTitle, fieldErrors{errs: fieldError("employmentId",
					fmt.Sprintf("Employment %d is not one of this person's", *body.EmploymentId))})
			}
		}
		// The case a health document is filed under (S15, Task 5): named
		// only on a document of a restricted category — which only
		// staff:personal-data reaches — and one of the person's own cases;
		// a document moved out of such a category leaves its case.
		caseID := d.CaseID
		if bodyHas(ctx, "caseId") {
			caseID = body.CaseId
		}
		if !to.Restricted {
			if body.CaseId != nil {
				return refuseInvalid(invalidDocumentTitle, fieldErrors{errs: fieldError("caseId",
					"Only a document of a restricted category is filed under a sick-leave case")})
			}
			caseID = nil
		}
		if caseID != nil && body.CaseId != nil {
			found, err := txq.PersonCaseExists(ctx, store.PersonCaseExistsParams{
				CaseID: *caseID, PersonID: d.PersonID, Zone: caller.Zone.String(),
				Cutoff: dayParam(caller.Today.AddDate(0, 0, -int(caller.Settings.HealthRetentionDays))),
			})
			if err != nil {
				return fmt.Errorf("staff: read case %d: %w", *caseID, err)
			}
			if !found {
				return refuseInvalid(invalidDocumentTitle, fieldErrors{errs: fieldError("caseId",
					fmt.Sprintf("Case %d is not one of this person's open or recent cases: an erased one, or one due for the erase, takes no document", *caseID))})
			}
		}
		saved, err = txq.UpdateDocument(ctx, store.UpdateDocumentParams{
			ID: d.ID, CategoryID: to.ID, EmploymentID: employmentID,
			ValidTo: pgDate(civilDate(body.ValidTo)), SignedAt: pgDate(civilDate(body.SignedAt)), Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("staff: change document %d: %w", d.ID, err)
		}
		if !equalID(caseID, d.CaseID) {
			if err := txq.SetDocumentCase(ctx, store.SetDocumentCaseParams{CaseID: caseID, ID: d.ID}); err != nil {
				return fmt.Errorf("staff: file document %d under its case: %w", d.ID, err)
			}
		}
		saved.CaseID = caseID
		before, err := documentValues(d, from)
		if err != nil {
			return err
		}
		after, err := documentValues(saved, to)
		if err != nil {
			return err
		}
		return s.recordDocumentEvent(ctx, txq, saved, eventDocumentUpdated, who, diffFields(before, after), from, to)
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.PutStaffDocumentsById404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusNotFound:
			return gen.PutStaffDocumentsById404Response{}, nil
		case http.StatusForbidden:
			return gen.PutStaffDocumentsById403ApplicationProblemPlusJSONResponse(r.problem), nil
		case http.StatusConflict:
			return gen.PutStaffDocumentsById409ApplicationProblemPlusJSONResponse(r.problem), nil
		default:
			return gen.PutStaffDocumentsById400ApplicationProblemPlusJSONResponse(r.problem), nil
		}
	}
	if err != nil {
		return nil, err
	}
	return gen.PutStaffDocumentsById200JSONResponse(documentResponse(saved, to)), nil
}

// DeleteStaffDocumentsById Delete a document
// (DELETE /api/v1/staff/documents/{id})
//
// The row goes, never marked (plan reading 21), and its object is queued in
// the ledger in the same transaction; once that has committed, one attempt
// removes the object through the accessor and, when the store confirms,
// clears the ledger row. A store that refuses leaves the row for the
// retention worker (delivery A2's ledger) — the document is gone either way,
// which is what was asked. Deleting by hand and erasing share this path
// (plan reading 22): the same ledger row, finished by the same attempt
// (finishObjectDeletion, object_deletions.go).
func (s *server) DeleteStaffDocumentsById(ctx context.Context, req gen.DeleteStaffDocumentsByIdRequestObject) (gen.DeleteStaffDocumentsByIdResponseObject, error) {
	caller, err := s.callerFor(ctx, store.New(s.deps.Pool))
	if err != nil {
		return nil, err
	}
	if !caller.writesDocuments() {
		return gen.DeleteStaffDocumentsById403JSONResponse(apicommon.ForbiddenBody()), nil
	}
	row, err := s.visibleDocument(ctx, caller, req.Id)
	if _, ok := asRefusal(err); ok {
		return gen.DeleteStaffDocumentsById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	personID := row.StaffDocument.PersonID
	var removed store.StaffDocument
	err = s.withPersonLock(ctx, personID, refuseErased, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		d, category, err := lockWritableDocument(ctx, txq, caller, req.Id)
		if err != nil {
			return err
		}
		if err := txq.DeleteDocument(ctx, d.ID); err != nil {
			return fmt.Errorf("staff: delete document %d: %w", d.ID, err)
		}
		if err := txq.InsertObjectDeletion(ctx, store.InsertObjectDeletionParams{ObjectKey: d.ObjectKey, Now: s.deps.Clock()}); err != nil {
			return fmt.Errorf("staff: queue document %d's object: %w", d.ID, err)
		}
		values, err := documentValues(d, category)
		if err != nil {
			return err
		}
		removed = d
		return s.recordDocumentEvent(ctx, txq, d, eventDocumentRemoved, who, diffFields(values, nil), category)
	})
	if errors.Is(err, errPersonNotFound) {
		return gen.DeleteStaffDocumentsById404Response{}, nil
	}
	if r, ok := asRefusal(err); ok {
		switch r.status {
		case http.StatusForbidden:
			return gen.DeleteStaffDocumentsById403ApplicationProblemPlusJSONResponse(r.problem), nil
		case http.StatusConflict:
			return gen.DeleteStaffDocumentsById409ApplicationProblemPlusJSONResponse(r.problem), nil
		}
		return gen.DeleteStaffDocumentsById404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	s.finishObjectDeletion(ctx, removed.ObjectKey, 0, "person_id", personID)
	return gen.DeleteStaffDocumentsById204Response{}, nil
}
