package customers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/fileimport"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// This file is the import's endpoints (customers import wizard design D5,
// D7): the upload that parses the file once and keeps its rows, the import
// read with its sample and its suggestion, the mapping saved with its
// preview, the check and the run asked for and the worker nudged, the stop,
// the delete, the rows a page at a time and the failed rows as a file. Every
// one is admitted on customers:import and customers:view; the content —
// what the mapping may write — is judged when the mapping is saved
// (imports_mapping.go).

// importFormField is the multipart part the contract names for the file.
const importFormField = "file"

// The paging of the two lists: the imports as every list of this module
// pages, the rows wider, since an errors table is read in bigger pieces.
const (
	importsDefaultPageSize    = 25
	importsMaxPageSize        = 100
	importRowsDefaultPageSize = 50
	importRowsMaxPageSize     = 500
)

// The file-level 400: every refusal on "file", under one title.
const invalidImportFileTitle = "Invalid import file"

// importUploadsPerHour is how many uploads one client address may make — the
// attachment upload's figure, for the same reason: an office behind one
// address shares it.
const importUploadsPerHour = 600

// policyImportUpload is that bound as the platform states one.
var policyImportUpload = ratelimit.Policy{
	Name:    "CustomerImportUpload",
	Limit:   importUploadsPerHour,
	Window:  time.Hour,
	Message: "Too many import uploads. Please wait a little before uploading more.",
}

// The import's conflicts (design D7), CustomerConflictProblem codes in the
// module's established vocabulary.
const (
	importRunningCode         = "import_running"
	importNotCheckedCode      = "import_not_checked"
	importNothingToImportCode = "import_nothing_to_import"
	importFinishedCode        = "import_finished"
)

func importRunningConflict() gen.CustomerConflictProblem {
	return *mergeConflict("The import is running", importRunningCode, "The worker is checking or importing this file. Stop it first, or wait for it to finish.")
}

func importFinishedConflict() gen.CustomerConflictProblem {
	return *mergeConflict("The import has finished", importFinishedCode, "This import is done and takes no more changes. Start a new import to import again.")
}

func importNotCheckedConflict() gen.CustomerConflictProblem {
	return *mergeConflict("The import is not checked", importNotCheckedCode, "Check the file first; an import runs only after its check.")
}

func importNothingToImportConflict() gen.CustomerConflictProblem {
	return *mergeConflict("Nothing to import", importNothingToImportCode, "The check found no row that would be created or updated. Fix the mapping or the file and check again.")
}

func invalidImportFile(messages ...string) apicommon.HttpValidationProblemDetails {
	return apicommon.ValidationProblem(invalidImportFileTitle, map[string][]string{"file": messages})
}

// importFilePart reads the one multipart part named "file" — the attachment
// upload's receiptPart for a CSV — and the name it was sent under. A missing
// part, a second one, an empty one, one past maxImportUploadBytes and a read
// that fails (a malformed body, or the router's cap firing) are all ok=false.
func importFilePart(mr *multipart.Reader) (data []byte, fileName string, ok bool) {
	if mr == nil {
		return nil, "", false
	}
	found := false
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", false
		}
		if part.FormName() != importFormField {
			_ = part.Close()
			continue
		}
		if found {
			_ = part.Close()
			return nil, "", false
		}
		found = true
		fileName = attachmentFileName(part.FileName())
		data, err = io.ReadAll(io.LimitReader(part, maxImportUploadBytes+1))
		_ = part.Close()
		if err != nil {
			return nil, "", false
		}
	}
	return data, fileName, found && len(data) > 0 && len(data) <= maxImportUploadBytes
}

// importRowErrorsJSON is a row's errors as import_rows.errors holds them:
// the contract's own shape.
func importRowErrorsJSON(l importLayout, errs []importError) ([]byte, error) {
	out := make([]gen.CustomerImportRowError, 0, len(errs))
	for _, e := range errs {
		out = append(out, l.wire(e))
	}
	return json.Marshal(out)
}

// importRowErrorsFromJSON reads them back; an empty list for a row with none.
func importRowErrorsFromJSON(data []byte) ([]gen.CustomerImportRowError, error) {
	out := []gen.CustomerImportRowError{}
	if len(data) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("customers: read an import row's errors: %w", err)
	}
	return out, nil
}

// uploaderOf is the signed-in user an upload is attributed to — the id and
// the display name, resolved through actorFor before the transaction opens
// and kept on the import (design D1): the worker that writes the rows runs
// without a user directory and names the uploader on every event from these.
// nil, nil for a request with no user (none reaches this module today).
func (s *server) uploaderOf(ctx context.Context) (*uuid.UUID, *string, error) {
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, nil, err
	}
	if act.Kind != "user" || act.UserID == nil {
		return nil, nil, nil
	}
	name := act.Display
	return act.UserID, &name, nil
}

// PostCustomersImports Upload a file of customers
// (POST /api/v1/customers/imports)
//
// The file is read once (internal/fileimport) and its rows written with CopyFrom
// in the transaction that creates the import, so an import either has every
// row or does not exist.
func (s *server) PostCustomersImports(ctx context.Context, req gen.PostCustomersImportsRequestObject) (gen.PostCustomersImportsResponseObject, error) {
	data, fileName, ok := importFilePart(req.Body)
	if !ok {
		return gen.PostCustomersImports400ApplicationProblemPlusJSONResponse(invalidImportFile(invalidImportUploadMessage)), nil
	}
	file, refusal := fileimport.Read(data, importUploadLimits)
	if refusal != "" {
		return gen.PostCustomersImports400ApplicationProblemPlusJSONResponse(invalidImportFile(refusal)), nil
	}
	userID, userName, err := s.uploaderOf(ctx)
	if err != nil {
		return nil, err
	}
	now := s.deps.Clock()
	var id int32
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		id, err = store.New(tx).InsertImport(ctx, store.InsertImportParams{
			FileName: fileName, SizeBytes: int64(len(data)), Encoding: file.Encoding, Separator: string(file.Separator),
			Header: file.Header, RowCount: int32(len(file.Rows)), CreatedByUserID: userID, CreatedByName: userName, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		_, err = tx.CopyFrom(ctx, pgx.Identifier{"customers", "import_rows"}, []string{"import_id", "row_no", "cells"},
			pgx.CopyFromSlice(len(file.Rows), func(i int) ([]any, error) {
				return []any{id, int32(file.Rows[i].Row), file.Rows[i].Cells}, nil
			}))
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("customers: store an import: %w", err)
	}
	view, err := s.importByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return gen.PostCustomersImports201JSONResponse(*view), nil
}

// importByID is one import as the contract answers it, nil for an id nobody
// has.
func (s *server) importByID(ctx context.Context, id int32) (*gen.CustomerImport, error) {
	q := store.New(s.deps.Pool)
	imp, err := q.GetImport(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: read import %d: %w", id, err)
	}
	views, err := s.importViews(ctx, q, []store.CustomersImport{imp})
	if err != nil {
		return nil, err
	}
	return &views[0], nil
}

// importViews is imports as the contract answers them: each with its sample
// rows and their errors under its mapping, and its suggestion.
func (s *server) importViews(ctx context.Context, q *store.Queries, imps []store.CustomersImport) ([]gen.CustomerImport, error) {
	views := make([]gen.CustomerImport, 0, len(imps))
	for _, imp := range imps {
		view, err := s.importView(ctx, q, imp)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

// importView is one import on the wire (design D7).
func (s *server) importView(ctx context.Context, q *store.Queries, imp store.CustomersImport) (gen.CustomerImport, error) {
	mapping, err := importMappingFromJSON(imp.Mapping)
	if err != nil {
		return gen.CustomerImport{}, err
	}
	previous, err := q.LatestImportMappingForHeader(ctx, imp.Header)
	var previousMapping *gen.CustomerImportMapping
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return gen.CustomerImport{}, fmt.Errorf("customers: read the last mapping for a header: %w", err)
	default:
		if previousMapping, err = importMappingFromJSON(previous); err != nil {
			return gen.CustomerImport{}, err
		}
	}
	sample, err := s.importSample(ctx, q, imp, mapping)
	if err != nil {
		return gen.CustomerImport{}, err
	}
	view := gen.CustomerImport{
		Id: imp.ID, State: gen.CustomerImportState(imp.State), FileName: imp.FileName, SizeBytes: imp.SizeBytes,
		Encoding: imp.Encoding, Separator: imp.Separator, Rows: imp.RowCount, Header: imp.Header, Sample: sample,
		Mapping: mapping, Suggested: suggestMapping(imp.Header, previousMapping),
		Processed: imp.Processed, Created: imp.Created, Updated: imp.Updated, Failed: imp.Failed, Skipped: imp.Skipped,
		StopRequested: imp.StopRequested, Error: imp.Error, CreatedAt: imp.CreatedAt, CreatedByName: imp.CreatedByName,
		RequestedAt: imp.RequestedAt, StartedAt: imp.StartedAt, FinishedAt: imp.FinishedAt,
	}
	return view, nil
}

// importSample is the first rows of an import, each with the errors its cells
// get under mapping — planned, no database asked beyond the vocabularies —
// and none when there is no mapping yet.
func (s *server) importSample(ctx context.Context, q *store.Queries, imp store.CustomersImport, mapping *gen.CustomerImportMapping) ([]gen.CustomerImportSampleRow, error) {
	rows, err := q.ImportSampleRows(ctx, store.ImportSampleRowsParams{ImportID: imp.ID, Limit: importSampleRows})
	if err != nil {
		return nil, fmt.Errorf("customers: read an import's sample: %w", err)
	}
	var l importLayout
	var vocab importVocabulary
	if mapping != nil {
		l = layoutFor(*mapping, len(imp.Header))
		if vocab, err = importVocabularyFor(ctx, q, l); err != nil {
			return nil, err
		}
	}
	sample := make([]gen.CustomerImportSampleRow, 0, len(rows))
	for _, row := range rows {
		item := gen.CustomerImportSampleRow{Row: row.RowNo, Cells: row.Cells, Errors: []gen.CustomerImportRowError{}}
		if mapping != nil {
			_, errs := l.plan(fileimport.Record{Row: int(row.RowNo), Cells: row.Cells}, vocab, l.creates())
			for _, e := range errs {
				item.Errors = append(item.Errors, l.wire(e))
			}
		}
		sample = append(sample, item)
	}
	return sample, nil
}

// pageParams reads page and pageSize against a list's bounds, the follow-ups
// list's own wording.
func pageParams(page, pageSize *int32, defaultSize, maxSize int32) (int32, int32, string) {
	p, size := int32(1), defaultSize
	if page != nil {
		if *page < 1 {
			return 0, 0, fmt.Sprintf("'page' must be at least 1, but was %d.", *page)
		}
		p = *page
	}
	if pageSize != nil {
		if *pageSize < 1 || *pageSize > maxSize {
			return 0, 0, fmt.Sprintf("'pageSize' must be between 1 and %d, but was %d.", maxSize, *pageSize)
		}
		size = *pageSize
	}
	return p, size, ""
}

// GetCustomersImports List customer imports
// (GET /api/v1/customers/imports)
func (s *server) GetCustomersImports(ctx context.Context, req gen.GetCustomersImportsRequestObject) (gen.GetCustomersImportsResponseObject, error) {
	page, pageSize, msg := pageParams(req.Params.Page, req.Params.PageSize, importsDefaultPageSize, importsMaxPageSize)
	if msg != "" {
		return gen.GetCustomersImports400ApplicationProblemPlusJSONResponse(apicommon.Problem("Invalid query parameters", msg)), nil
	}
	q := store.New(s.deps.Pool)
	total, err := q.CountImports(ctx)
	if err != nil {
		return nil, fmt.Errorf("customers: count imports: %w", err)
	}
	imps, err := q.ListImports(ctx, store.ListImportsParams{Limit: pageSize, Offset: (page - 1) * pageSize})
	if err != nil {
		return nil, fmt.Errorf("customers: list imports: %w", err)
	}
	views, err := s.importViews(ctx, q, imps)
	if err != nil {
		return nil, err
	}
	return gen.GetCustomersImports200JSONResponse(gen.PaginatedResponseOfCustomerImport{
		Data: views, Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}), nil
}

// GetCustomersImportsByImportId Get a customer import
// (GET /api/v1/customers/imports/{importId})
func (s *server) GetCustomersImportsByImportId(ctx context.Context, req gen.GetCustomersImportsByImportIdRequestObject) (gen.GetCustomersImportsByImportIdResponseObject, error) {
	view, err := s.importByID(ctx, req.ImportId)
	if err != nil {
		return nil, err
	}
	if view == nil {
		return gen.GetCustomersImportsByImportId404Response{}, nil
	}
	return gen.GetCustomersImportsByImportId200JSONResponse(*view), nil
}

// PutCustomersImportsByImportIdMapping Save a customer import's mapping
// (PUT /api/v1/customers/imports/{importId}/mapping)
//
// The order of refusals: the import (404), its state (409), the mapping
// against the header and the caller (400) — and only then the save, in one
// transaction with the rows' results cleared, since the check they came from
// was about another mapping.
func (s *server) PutCustomersImportsByImportIdMapping(ctx context.Context, req gen.PutCustomersImportsByImportIdMappingRequestObject) (gen.PutCustomersImportsByImportIdMappingResponseObject, error) {
	q := store.New(s.deps.Pool)
	imp, err := q.GetImport(ctx, req.ImportId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersImportsByImportIdMapping404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: read import %d: %w", req.ImportId, err)
	}
	switch {
	case importStateIs(imp.State, gen.Checking, gen.Importing):
		return gen.PutCustomersImportsByImportIdMapping409ApplicationProblemPlusJSONResponse(importRunningConflict()), nil
	case importStateIs(imp.State, gen.Done):
		return gen.PutCustomersImportsByImportIdMapping409ApplicationProblemPlusJSONResponse(importFinishedConflict()), nil
	}
	if req.Body == nil {
		return gen.PutCustomersImportsByImportIdMapping400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid mapping", map[string][]string{"mapping": {"A mapping is required"}})), nil
	}
	if _, errs := s.validateImportMapping(ctx, *req.Body, imp.Header); errs != nil {
		return gen.PutCustomersImportsByImportIdMapping400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid mapping", errs)), nil
	}
	data, err := importMappingJSON(*req.Body)
	if err != nil {
		return nil, fmt.Errorf("customers: encode a mapping: %w", err)
	}
	now := s.deps.Clock()
	saved := false
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		n, err := txq.SetImportMapping(ctx, store.SetImportMappingParams{ID: imp.ID, Mapping: data, UpdatedAt: now})
		if err != nil || n == 0 {
			return err
		}
		saved = true
		return txq.ClearImportRowResults(ctx, imp.ID)
	})
	if err != nil {
		return nil, fmt.Errorf("customers: save a mapping: %w", err)
	}
	if !saved {
		// The worker took the import between the read and the write.
		return gen.PutCustomersImportsByImportIdMapping409ApplicationProblemPlusJSONResponse(importRunningConflict()), nil
	}
	view, err := s.importByID(ctx, imp.ID)
	if err != nil || view == nil {
		return nil, fmt.Errorf("customers: re-read import %d after its mapping: %w", imp.ID, err)
	}
	return gen.PutCustomersImportsByImportIdMapping200JSONResponse(*view), nil
}

// PostCustomersImportsByImportIdCheck Check a customer import
// (POST /api/v1/customers/imports/{importId}/check)
func (s *server) PostCustomersImportsByImportIdCheck(ctx context.Context, req gen.PostCustomersImportsByImportIdCheckRequestObject) (gen.PostCustomersImportsByImportIdCheckResponseObject, error) {
	q := store.New(s.deps.Pool)
	imp, err := q.GetImport(ctx, req.ImportId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCustomersImportsByImportIdCheck404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: read import %d: %w", req.ImportId, err)
	}
	switch {
	case importStateIs(imp.State, gen.Checking, gen.Importing):
		return gen.PostCustomersImportsByImportIdCheck409ApplicationProblemPlusJSONResponse(importRunningConflict()), nil
	case importStateIs(imp.State, gen.Done):
		return gen.PostCustomersImportsByImportIdCheck409ApplicationProblemPlusJSONResponse(importFinishedConflict()), nil
	case len(imp.Mapping) == 0:
		return gen.PostCustomersImportsByImportIdCheck400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid import",
			map[string][]string{"mapping": {"Map the file's columns before checking it"}})), nil
	}
	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.ClearImportRowResults(ctx, imp.ID); err != nil {
			return err
		}
		_, err := txq.RequestImportCheck(ctx, store.RequestImportCheckParams{ID: imp.ID, RequestedAt: ptrTime(now)})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("customers: queue a check: %w", err)
	}
	nudgeImportWorker()
	view, err := s.importByID(ctx, imp.ID)
	if err != nil || view == nil {
		return nil, fmt.Errorf("customers: re-read import %d after its check: %w", imp.ID, err)
	}
	return gen.PostCustomersImportsByImportIdCheck200JSONResponse(*view), nil
}

// PostCustomersImportsByImportIdRun Run a customer import
// (POST /api/v1/customers/imports/{importId}/run)
func (s *server) PostCustomersImportsByImportIdRun(ctx context.Context, req gen.PostCustomersImportsByImportIdRunRequestObject) (gen.PostCustomersImportsByImportIdRunResponseObject, error) {
	q := store.New(s.deps.Pool)
	imp, err := q.GetImport(ctx, req.ImportId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCustomersImportsByImportIdRun404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: read import %d: %w", req.ImportId, err)
	}
	switch {
	case importStateIs(imp.State, gen.Checking, gen.Importing):
		return gen.PostCustomersImportsByImportIdRun409ApplicationProblemPlusJSONResponse(importRunningConflict()), nil
	case importStateIs(imp.State, gen.Done):
		return gen.PostCustomersImportsByImportIdRun409ApplicationProblemPlusJSONResponse(importFinishedConflict()), nil
	case importStateIs(imp.State, gen.Draft):
		return gen.PostCustomersImportsByImportIdRun409ApplicationProblemPlusJSONResponse(importNotCheckedConflict()), nil
	case imp.Created+imp.Updated == 0:
		return gen.PostCustomersImportsByImportIdRun409ApplicationProblemPlusJSONResponse(importNothingToImportConflict()), nil
	}
	now := s.deps.Clock()
	queued := false
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		n, err := txq.RequestImportRun(ctx, store.RequestImportRunParams{ID: imp.ID, RequestedAt: ptrTime(now)})
		if err != nil || n == 0 {
			return err
		}
		queued = true
		return txq.ClearImportRowResults(ctx, imp.ID)
	})
	if err != nil {
		return nil, fmt.Errorf("customers: queue a run: %w", err)
	}
	if !queued {
		return gen.PostCustomersImportsByImportIdRun409ApplicationProblemPlusJSONResponse(importRunningConflict()), nil
	}
	nudgeImportWorker()
	view, err := s.importByID(ctx, imp.ID)
	if err != nil || view == nil {
		return nil, fmt.Errorf("customers: re-read import %d after its run: %w", imp.ID, err)
	}
	return gen.PostCustomersImportsByImportIdRun200JSONResponse(*view), nil
}

// PostCustomersImportsByImportIdStop Stop a customer import
// (POST /api/v1/customers/imports/{importId}/stop)
//
// A run the worker has not taken up goes straight back to where it was asked
// from; one under way is flagged, and the worker finishes it between chunks
// (import_worker.go).
func (s *server) PostCustomersImportsByImportIdStop(ctx context.Context, req gen.PostCustomersImportsByImportIdStopRequestObject) (gen.PostCustomersImportsByImportIdStopResponseObject, error) {
	q := store.New(s.deps.Pool)
	imp, err := q.GetImport(ctx, req.ImportId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostCustomersImportsByImportIdStop404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: read import %d: %w", req.ImportId, err)
	}
	if !importStateIs(imp.State, gen.Checking, gen.Importing) {
		return gen.PostCustomersImportsByImportIdStop409ApplicationProblemPlusJSONResponse(importFinishedConflict()), nil
	}
	now := s.deps.Clock()
	reverted := int64(0)
	if imp.StartedAt == nil {
		back := gen.Draft
		if importStateIs(imp.State, gen.Importing) {
			back = gen.Checked
		}
		reverted, err = q.RevertUnstartedImport(ctx, store.RevertUnstartedImportParams{ID: imp.ID, State: string(back), UpdatedAt: now, State_2: imp.State})
		if err != nil {
			return nil, fmt.Errorf("customers: revert an unstarted import: %w", err)
		}
	}
	if reverted == 0 {
		if _, err := q.RequestImportStop(ctx, store.RequestImportStopParams{ID: imp.ID, UpdatedAt: now}); err != nil {
			return nil, fmt.Errorf("customers: ask an import to stop: %w", err)
		}
	}
	view, err := s.importByID(ctx, imp.ID)
	if err != nil || view == nil {
		return nil, fmt.Errorf("customers: re-read import %d after its stop: %w", imp.ID, err)
	}
	return gen.PostCustomersImportsByImportIdStop200JSONResponse(*view), nil
}

// DeleteCustomersImportsByImportId Delete a customer import
// (DELETE /api/v1/customers/imports/{importId})
func (s *server) DeleteCustomersImportsByImportId(ctx context.Context, req gen.DeleteCustomersImportsByImportIdRequestObject) (gen.DeleteCustomersImportsByImportIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	n, err := q.DeleteImport(ctx, req.ImportId)
	if err != nil {
		return nil, fmt.Errorf("customers: delete import %d: %w", req.ImportId, err)
	}
	if n == 0 {
		if _, err := q.GetImport(ctx, req.ImportId); errors.Is(err, pgx.ErrNoRows) {
			return gen.DeleteCustomersImportsByImportId404Response{}, nil
		} else if err != nil {
			return nil, fmt.Errorf("customers: read import %d: %w", req.ImportId, err)
		}
		return gen.DeleteCustomersImportsByImportId409ApplicationProblemPlusJSONResponse(importRunningConflict()), nil
	}
	return gen.DeleteCustomersImportsByImportId204Response{}, nil
}

// GetCustomersImportsByImportIdRows List a customer import's rows
// (GET /api/v1/customers/imports/{importId}/rows)
func (s *server) GetCustomersImportsByImportIdRows(ctx context.Context, req gen.GetCustomersImportsByImportIdRowsRequestObject) (gen.GetCustomersImportsByImportIdRowsResponseObject, error) {
	page, pageSize, msg := pageParams(req.Params.Page, req.Params.PageSize, importRowsDefaultPageSize, importRowsMaxPageSize)
	if msg != "" {
		return gen.GetCustomersImportsByImportIdRows400ApplicationProblemPlusJSONResponse(apicommon.Problem("Invalid query parameters", msg)), nil
	}
	var outcome *string
	if req.Params.Outcome != nil {
		switch *req.Params.Outcome {
		case string(gen.Created), string(gen.Updated), string(gen.Failed), string(gen.Skipped):
			outcome = req.Params.Outcome
		default:
			return gen.GetCustomersImportsByImportIdRows400ApplicationProblemPlusJSONResponse(apicommon.Problem("Invalid query parameters",
				fmt.Sprintf("'outcome' must be one of 'created', 'updated', 'failed' or 'skipped', but was '%s'.", *req.Params.Outcome))), nil
		}
	}
	q := store.New(s.deps.Pool)
	if _, err := q.GetImport(ctx, req.ImportId); errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersImportsByImportIdRows404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: read import %d: %w", req.ImportId, err)
	}
	total, err := q.CountImportRowsPage(ctx, store.CountImportRowsPageParams{ImportID: req.ImportId, Outcome: outcome})
	if err != nil {
		return nil, fmt.Errorf("customers: count import rows: %w", err)
	}
	rows, err := q.ImportRowsPage(ctx, store.ImportRowsPageParams{ImportID: req.ImportId, Outcome: outcome, Limit: pageSize, Offset: (page - 1) * pageSize})
	if err != nil {
		return nil, fmt.Errorf("customers: list import rows: %w", err)
	}
	data := make([]gen.CustomerImportRow, 0, len(rows))
	for _, r := range rows {
		errs, err := importRowErrorsFromJSON(r.Errors)
		if err != nil {
			return nil, err
		}
		item := gen.CustomerImportRow{Row: r.RowNo, Cells: r.Cells, Errors: errs, CustomerId: r.CustomerID, CustomerNumber: r.CustomerNumber}
		if r.Outcome != nil {
			o := gen.CustomerImportRowOutcome(*r.Outcome)
			item.Outcome = &o
		}
		data = append(data, item)
	}
	return gen.GetCustomersImportsByImportIdRows200JSONResponse(gen.PaginatedResponseOfCustomerImportRow{
		Data: data, Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}), nil
}

// GetCustomersImportsByImportIdFailedRows Download an import's failed rows
// (GET /api/v1/customers/imports/{importId}/failed-rows)
//
// The file's own header, so the same mapping is suggested when it comes back,
// behind an error column holding every problem of the row — "<header>:
// message", the message alone for the row as a whole — each row's cells
// exactly as they were. A column with no header is named by its position.
func (s *server) GetCustomersImportsByImportIdFailedRows(ctx context.Context, req gen.GetCustomersImportsByImportIdFailedRowsRequestObject) (gen.GetCustomersImportsByImportIdFailedRowsResponseObject, error) {
	q := store.New(s.deps.Pool)
	imp, err := q.GetImport(ctx, req.ImportId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCustomersImportsByImportIdFailedRows404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: read import %d: %w", req.ImportId, err)
	}
	rows, err := q.FailedImportRows(ctx, imp.ID)
	if err != nil {
		return nil, fmt.Errorf("customers: read an import's failed rows: %w", err)
	}
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, append([]string{importErrorColumn}, imp.Header...))
	for _, r := range rows {
		errs, err := importRowErrorsFromJSON(r.Errors)
		if err != nil {
			return nil, err
		}
		problems := make([]string, 0, len(errs))
		for _, e := range errs {
			problems = append(problems, failedRowProblem(imp.Header, e))
		}
		writeCSVRow(&b, append([]string{strings.Join(problems, " | ")}, r.Cells...))
	}
	return csvDownload{body: b.Bytes(), fileName: fmt.Sprintf("customers-import-%d-failed-rows.csv", imp.ID)}, nil
}

// importErrorColumn is the failed-rows file's first column.
const importErrorColumn = "error"

// failedRowProblem is one error as the failed-rows file words it.
func failedRowProblem(header []string, e gen.CustomerImportRowError) string {
	if e.Column == nil {
		return e.Message
	}
	name := ""
	if int(*e.Column) < len(header) {
		name = header[*e.Column]
	}
	if name == "" {
		name = "Column " + strconv.Itoa(int(*e.Column)+1)
	}
	return name + ": " + e.Message
}

func (d csvDownload) VisitGetCustomersImportsByImportIdFailedRowsResponse(w http.ResponseWriter) error {
	return d.write(w)
}
