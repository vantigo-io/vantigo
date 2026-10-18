package energy

import (
	"bytes"
	"context"
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
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/energy/gen"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/fileimport"
	"github.com/vantigo-io/vantigo/server/internal/ratelimit"
)

// This file is the metering-point import's endpoints (energy import design
// D6, D7), the customers import's (internal/customers/imports.go) in energy's
// terms: the upload that parses the file once and keeps its rows, the import
// read with its sample and its suggestion, the mapping saved with its
// preview, the check, the run and the attach asked for and the worker nudged,
// the stop, the delete, the rows a page at a time and the failed rows as a
// file. Every one is admitted on energy:import and
// energy:metering-points-view, the attach on energy:supply-periods-manage
// too; what the mapping may write is judged when it is saved
// (imports_mapping.go).

// The upload's bounds (design D6): the customers import's. maxImport-
// UploadRequestBytes is the router's cap for the operation: the file plus room
// for the multipart framing.
const (
	maxImportUploadBytes        = 50 * 1024 * 1024
	maxImportUploadRequestBytes = maxImportUploadBytes + 64*1024
	maxImportUploadRows         = 100_000
	maxImportUploadColumns      = 200
	// importSampleRows is how many rows the import carries as its sample.
	importSampleRows = 20
)

// importUploadLimits are the bounds above as the reader takes them.
var importUploadLimits = fileimport.Limits{
	MaxBytes:   maxImportUploadBytes,
	MaxRows:    maxImportUploadRows,
	MaxColumns: maxImportUploadColumns,
}

// invalidImportUploadMessage is the refusal of an upload the reader never
// sees: no file part, two, an empty one or one past the size cap.
const invalidImportUploadMessage = "A metering-point import is one CSV file of at most 50 MB, sent as the multipart part named 'file'"

// importFormField is the multipart part the contract names for the file.
const importFormField = "file"

// The paging of the two lists: the imports as the metering-point list pages,
// the rows wider, since an errors table is read in bigger pieces.
const (
	importsDefaultPageSize    = 25
	importsMaxPageSize        = 100
	importRowsDefaultPageSize = 50
	importRowsMaxPageSize     = 500
)

// The file-level 400: every refusal on "file", under one title.
const invalidImportFileTitle = "Invalid import file"

// policyImportUpload is the upload's rate limit, the customers import's: six
// hundred an hour from one client address, since an office behind one address
// shares it.
var policyImportUpload = ratelimit.Policy{
	Name:    "EnergyImportUpload",
	Limit:   600,
	Window:  time.Hour,
	Message: "Too many import uploads. Please wait a little before uploading more.",
}

// The import's conflicts (design D7). Energy answers no codes anywhere else
// (errors.go); the import's are the customers import's vocabulary, which the
// shared import pages key off, plus the two the switch and the attach add.
const (
	importRunningCode            = "import_running"
	importNotCheckedCode         = "import_not_checked"
	importNothingToImportCode    = "import_nothing_to_import"
	importFinishedCode           = "import_finished"
	importSwitchNotConfirmedCode = "import_switch_not_confirmed"
	importNothingPendingCode     = "import_nothing_pending"
)

func importConflict(title, code, detail string) gen.EnergyImportConflictProblem {
	status := int32(http.StatusConflict)
	return gen.EnergyImportConflictProblem{Title: &title, Detail: &detail, Status: &status, Code: &code}
}

func importRunningConflict() gen.EnergyImportConflictProblem {
	return importConflict("The import is running", importRunningCode, "The worker is checking, importing or attaching this file. Stop it first, or wait for it to finish.")
}

func importFinishedConflict() gen.EnergyImportConflictProblem {
	return importConflict("The import has finished", importFinishedCode, "This import is done and takes no more changes. Start a new import to import again.")
}

func importNotCheckedConflict() gen.EnergyImportConflictProblem {
	return importConflict("The import is not checked", importNotCheckedCode, "Check the file first; an import runs only after its check.")
}

func importNothingToImportConflict() gen.EnergyImportConflictProblem {
	return importConflict("Nothing to import", importNothingToImportCode, "The check found no row that would be created or updated. Fix the mapping or the file and check again.")
}

func importNothingPendingConflict() gen.EnergyImportConflictProblem {
	return importConflict("Nothing to attach", importNothingPendingCode, "Only a finished import with pending metering points can attach them to their customers.")
}

// importSwitchNotConfirmedConflict is design D4's guard: the number of points
// that will switch customer, which confirmSwitches must repeat.
func importSwitchNotConfirmedConflict(switches int32) gen.EnergyImportConflictProblem {
	p := importConflict("Switches not confirmed", importSwitchNotConfirmedCode, fmt.Sprintf(
		"%d metering points will switch customer: each one's current supply period ends and the customer in the file takes over. Send confirmSwitches: %d to confirm.", switches, switches))
	p.Switches = &switches
	return p
}

func invalidImportFile(messages ...string) apicommon.HttpValidationProblemDetails {
	return apicommon.ValidationProblem(invalidImportFileTitle, map[string][]string{"file": messages})
}

// importFilePart reads the one multipart part named "file" and the name it was
// sent under, the customers import's rule: a missing part, a second one, an
// empty one, one past maxImportUploadBytes and a read that fails are all
// ok=false.
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
		fileName = importFileName(part.FileName())
		data, err = io.ReadAll(io.LimitReader(part, maxImportUploadBytes+1))
		_ = part.Close()
		if err != nil {
			return nil, "", false
		}
	}
	return data, fileName, found && len(data) > 0 && len(data) <= maxImportUploadBytes
}

// importFileName is the name a file was sent under as the import keeps it:
// its last path segment, control characters dropped, at most 255 characters,
// "import.csv" when nothing is left.
func importFileName(raw string) string {
	name := strings.TrimSpace(raw)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name))
	if name == "" || name == "." || name == ".." {
		return "import.csv"
	}
	if runes := []rune(name); len(runes) > 255 {
		name = string(runes[:255])
	}
	return name
}

// uploaderOf is the signed-in user an upload is attributed to — the id and
// the display name through the user directory, kept on the import. nil, nil
// for a request with no user.
func (s *server) uploaderOf(ctx context.Context) (*uuid.UUID, *string, error) {
	p, ok := contracts.PrincipalFrom(ctx)
	if !ok || p.SCIM || p.UserID == uuid.Nil || s.deps.Users == nil {
		return nil, nil, nil
	}
	user, err := s.deps.Users.User(ctx, p.UserID)
	if err != nil {
		return nil, nil, fmt.Errorf("energy: resolve the uploader: %w", err)
	}
	userID := p.UserID
	if user == nil {
		return &userID, nil, nil
	}
	name := user.DisplayName
	return &userID, &name, nil
}

// PostEnergyImports Upload a file of metering points
// (POST /api/v1/energy/imports)
//
// The file is read once (internal/fileimport) and its rows written with
// CopyFrom in the transaction that creates the import.
func (s *server) PostEnergyImports(ctx context.Context, req gen.PostEnergyImportsRequestObject) (gen.PostEnergyImportsResponseObject, error) {
	data, fileName, ok := importFilePart(req.Body)
	if !ok {
		return gen.PostEnergyImports400ApplicationProblemPlusJSONResponse(invalidImportFile(invalidImportUploadMessage)), nil
	}
	file, refusal := fileimport.Read(data, importUploadLimits)
	if refusal != "" {
		return gen.PostEnergyImports400ApplicationProblemPlusJSONResponse(invalidImportFile(refusal)), nil
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
		_, err = tx.CopyFrom(ctx, pgx.Identifier{"energy", "import_rows"}, []string{"import_id", "row_no", "cells"},
			pgx.CopyFromSlice(len(file.Rows), func(i int) ([]any, error) {
				return []any{id, int32(file.Rows[i].Row), file.Rows[i].Cells}, nil
			}))
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("energy: store an import: %w", err)
	}
	view, err := s.importByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return gen.PostEnergyImports201JSONResponse(*view), nil
}

// importByID is one import as the contract answers it, nil for an id nobody
// has.
func (s *server) importByID(ctx context.Context, id int32) (*gen.EnergyImport, error) {
	q := store.New(s.deps.Pool)
	imp, err := q.GetImport(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: read import %d: %w", id, err)
	}
	view, err := s.importView(ctx, q, imp)
	if err != nil {
		return nil, err
	}
	return &view, nil
}

// importView is one import on the wire (design D7).
func (s *server) importView(ctx context.Context, q *store.Queries, imp store.EnergyImport) (gen.EnergyImport, error) {
	mapping, err := importMappingFromJSON(imp.Mapping)
	if err != nil {
		return gen.EnergyImport{}, err
	}
	previous, err := q.LatestImportMappingForHeader(ctx, imp.Header)
	var previousMapping *gen.EnergyImportMapping
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return gen.EnergyImport{}, fmt.Errorf("energy: read the last mapping for a header: %w", err)
	default:
		if previousMapping, err = importMappingFromJSON(previous); err != nil {
			return gen.EnergyImport{}, err
		}
	}
	sample, err := importSample(ctx, q, imp, mapping)
	if err != nil {
		return gen.EnergyImport{}, err
	}
	pendingSwitches, err := q.CountPendingSwitches(ctx, imp.ID)
	if err != nil {
		return gen.EnergyImport{}, fmt.Errorf("energy: count import %d's pending switches: %w", imp.ID, err)
	}
	return gen.EnergyImport{
		Id: imp.ID, State: gen.EnergyImportState(imp.State), FileName: imp.FileName, SizeBytes: imp.SizeBytes,
		Encoding: imp.Encoding, Separator: imp.Separator, Rows: imp.RowCount, Header: imp.Header, Sample: sample,
		Mapping: mapping, Suggested: suggestMapping(imp.Header, previousMapping),
		Processed: imp.Processed, Created: imp.Created, Updated: imp.Updated, Failed: imp.Failed, Skipped: imp.Skipped,
		Switched: imp.Switched, Pending: imp.Pending, PendingSwitches: int32(pendingSwitches), Attached: imp.Attached,
		AttachStart: imp.AttachStart, StopRequested: imp.StopRequested, Error: imp.Error, CreatedAt: imp.CreatedAt,
		CreatedByName: imp.CreatedByName, RequestedAt: imp.RequestedAt, StartedAt: imp.StartedAt, FinishedAt: imp.FinishedAt,
	}, nil
}

// importSample is the first rows of an import, each with the errors its cells
// get under mapping — planned, no database asked — and none when there is no
// mapping yet or the row is a totals line.
func importSample(ctx context.Context, q *store.Queries, imp store.EnergyImport, mapping *gen.EnergyImportMapping) ([]gen.EnergyImportSampleRow, error) {
	rows, err := q.ImportSampleRows(ctx, store.ImportSampleRowsParams{ImportID: imp.ID, Limit: importSampleRows})
	if err != nil {
		return nil, fmt.Errorf("energy: read an import's sample: %w", err)
	}
	sample := make([]gen.EnergyImportSampleRow, 0, len(rows))
	for _, row := range rows {
		item := gen.EnergyImportSampleRow{Row: row.RowNo, Cells: row.Cells, Errors: []gen.EnergyImportRowError{}}
		if mapping != nil {
			l := layoutFor(*mapping, len(imp.Header))
			rec := fileimport.Record{Row: int(row.RowNo), Cells: row.Cells}
			if !l.isTotalsLine(rec) {
				_, errs := l.plan(rec, l.creates())
				for _, e := range errs {
					item.Errors = append(item.Errors, l.wire(e))
				}
			}
		}
		sample = append(sample, item)
	}
	return sample, nil
}

// importPageParams reads page and pageSize against a list's bounds.
func importPageParams(page, pageSize *int32, defaultSize, maxSize int32) (int32, int32, string) {
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

func importPagination(page, pageSize int32, total int64) gen.EnergyPaginationMetadata {
	return gen.EnergyPaginationMetadata{
		Page: page, PageSize: pageSize, TotalCount: int32(total), TotalPages: apicommon.TotalPages(int32(total), pageSize),
	}
}

// GetEnergyImports List metering-point imports
// (GET /api/v1/energy/imports)
func (s *server) GetEnergyImports(ctx context.Context, req gen.GetEnergyImportsRequestObject) (gen.GetEnergyImportsResponseObject, error) {
	page, pageSize, msg := importPageParams(req.Params.Page, req.Params.PageSize, importsDefaultPageSize, importsMaxPageSize)
	if msg != "" {
		return gen.GetEnergyImports400ApplicationProblemPlusJSONResponse(apicommon.Problem("Invalid query parameters", msg)), nil
	}
	q := store.New(s.deps.Pool)
	total, err := q.CountImports(ctx)
	if err != nil {
		return nil, fmt.Errorf("energy: count imports: %w", err)
	}
	imps, err := q.ListImports(ctx, store.ListImportsParams{Limit: pageSize, Offset: (page - 1) * pageSize})
	if err != nil {
		return nil, fmt.Errorf("energy: list imports: %w", err)
	}
	views := make([]gen.EnergyImport, 0, len(imps))
	for _, imp := range imps {
		view, err := s.importView(ctx, q, imp)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return gen.GetEnergyImports200JSONResponse(gen.PaginatedResponseOfEnergyImport{
		Data: views, Pagination: importPagination(page, pageSize, total),
	}), nil
}

// GetEnergyImportsByImportId Get a metering-point import
// (GET /api/v1/energy/imports/{importId})
func (s *server) GetEnergyImportsByImportId(ctx context.Context, req gen.GetEnergyImportsByImportIdRequestObject) (gen.GetEnergyImportsByImportIdResponseObject, error) {
	view, err := s.importByID(ctx, req.ImportId)
	if err != nil {
		return nil, err
	}
	if view == nil {
		return gen.GetEnergyImportsByImportId404Response{}, nil
	}
	return gen.GetEnergyImportsByImportId200JSONResponse(*view), nil
}

// PutEnergyImportsByImportIdMapping Save a metering-point import's mapping
// (PUT /api/v1/energy/imports/{importId}/mapping)
//
// The order of refusals: the import (404), its state (409), the mapping
// against the header and the caller (400) — and only then the save, in one
// transaction with the rows' results cleared.
func (s *server) PutEnergyImportsByImportIdMapping(ctx context.Context, req gen.PutEnergyImportsByImportIdMappingRequestObject) (gen.PutEnergyImportsByImportIdMappingResponseObject, error) {
	q := store.New(s.deps.Pool)
	imp, err := q.GetImport(ctx, req.ImportId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutEnergyImportsByImportIdMapping404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: read import %d: %w", req.ImportId, err)
	}
	switch {
	case importStateIs(imp.State, gen.Checking, gen.Importing, gen.Attaching):
		return gen.PutEnergyImportsByImportIdMapping409ApplicationProblemPlusJSONResponse(importRunningConflict()), nil
	case importStateIs(imp.State, gen.Done):
		return gen.PutEnergyImportsByImportIdMapping409ApplicationProblemPlusJSONResponse(importFinishedConflict()), nil
	}
	if req.Body == nil {
		return gen.PutEnergyImportsByImportIdMapping400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid mapping", map[string][]string{"mapping": {"A mapping is required"}})), nil
	}
	if _, errs := s.validateImportMapping(ctx, *req.Body, imp.Header); errs != nil {
		return gen.PutEnergyImportsByImportIdMapping400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid mapping", errs)), nil
	}
	data, err := importMappingJSON(*req.Body)
	if err != nil {
		return nil, fmt.Errorf("energy: encode a mapping: %w", err)
	}
	saved := false
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		n, err := txq.SetImportMapping(ctx, store.SetImportMappingParams{ID: imp.ID, Mapping: data, UpdatedAt: s.deps.Clock()})
		if err != nil || n == 0 {
			return err
		}
		saved = true
		return txq.ClearImportRowResults(ctx, imp.ID)
	})
	if err != nil {
		return nil, fmt.Errorf("energy: save a mapping: %w", err)
	}
	if !saved {
		// The worker took the import between the read and the write.
		return gen.PutEnergyImportsByImportIdMapping409ApplicationProblemPlusJSONResponse(importRunningConflict()), nil
	}
	view, err := s.importByID(ctx, imp.ID)
	if err != nil || view == nil {
		return nil, fmt.Errorf("energy: re-read import %d after its mapping: %w", imp.ID, err)
	}
	return gen.PutEnergyImportsByImportIdMapping200JSONResponse(*view), nil
}

// PostEnergyImportsByImportIdCheck Check a metering-point import
// (POST /api/v1/energy/imports/{importId}/check)
func (s *server) PostEnergyImportsByImportIdCheck(ctx context.Context, req gen.PostEnergyImportsByImportIdCheckRequestObject) (gen.PostEnergyImportsByImportIdCheckResponseObject, error) {
	q := store.New(s.deps.Pool)
	imp, err := q.GetImport(ctx, req.ImportId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostEnergyImportsByImportIdCheck404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: read import %d: %w", req.ImportId, err)
	}
	switch {
	case importStateIs(imp.State, gen.Checking, gen.Importing, gen.Attaching):
		return gen.PostEnergyImportsByImportIdCheck409ApplicationProblemPlusJSONResponse(importRunningConflict()), nil
	case importStateIs(imp.State, gen.Done):
		return gen.PostEnergyImportsByImportIdCheck409ApplicationProblemPlusJSONResponse(importFinishedConflict()), nil
	case len(imp.Mapping) == 0:
		return gen.PostEnergyImportsByImportIdCheck400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid import",
			map[string][]string{"mapping": {"Map the file's columns before checking it"}})), nil
	}
	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if err := txq.ClearImportRowResults(ctx, imp.ID); err != nil {
			return err
		}
		_, err := txq.RequestImportCheck(ctx, store.RequestImportCheckParams{ID: imp.ID, RequestedAt: &now})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("energy: queue a check: %w", err)
	}
	nudgeImportWorker()
	view, err := s.importByID(ctx, imp.ID)
	if err != nil || view == nil {
		return nil, fmt.Errorf("energy: re-read import %d after its check: %w", imp.ID, err)
	}
	return gen.PostEnergyImportsByImportIdCheck200JSONResponse(*view), nil
}

// PostEnergyImportsByImportIdRun Run a metering-point import
// (POST /api/v1/energy/imports/{importId}/run)
//
// After the state's conflicts, the switch guard (design D4): a check that
// counted switches runs only with confirmSwitches equal to that count, and
// one that counted none only without a count (or with 0). The queueing
// statement compares again, so a mapping saved in between cannot slip past.
func (s *server) PostEnergyImportsByImportIdRun(ctx context.Context, req gen.PostEnergyImportsByImportIdRunRequestObject) (gen.PostEnergyImportsByImportIdRunResponseObject, error) {
	q := store.New(s.deps.Pool)
	imp, err := q.GetImport(ctx, req.ImportId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostEnergyImportsByImportIdRun404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: read import %d: %w", req.ImportId, err)
	}
	switch {
	case importStateIs(imp.State, gen.Checking, gen.Importing, gen.Attaching):
		return gen.PostEnergyImportsByImportIdRun409ApplicationProblemPlusJSONResponse(importRunningConflict()), nil
	case importStateIs(imp.State, gen.Done):
		return gen.PostEnergyImportsByImportIdRun409ApplicationProblemPlusJSONResponse(importFinishedConflict()), nil
	case importStateIs(imp.State, gen.Draft):
		return gen.PostEnergyImportsByImportIdRun409ApplicationProblemPlusJSONResponse(importNotCheckedConflict()), nil
	case imp.Created+imp.Updated == 0:
		return gen.PostEnergyImportsByImportIdRun409ApplicationProblemPlusJSONResponse(importNothingToImportConflict()), nil
	}
	confirmed := int32(0)
	if req.Body != nil && req.Body.ConfirmSwitches != nil {
		confirmed = *req.Body.ConfirmSwitches
	}
	if confirmed != imp.Switched {
		return gen.PostEnergyImportsByImportIdRun409ApplicationProblemPlusJSONResponse(importSwitchNotConfirmedConflict(imp.Switched)), nil
	}
	now := s.deps.Clock()
	queued := false
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		n, err := txq.RequestImportRun(ctx, store.RequestImportRunParams{ID: imp.ID, RequestedAt: &now, ConfirmedSwitches: confirmed})
		if err != nil || n == 0 {
			return err
		}
		queued = true
		return txq.ClearImportRowResults(ctx, imp.ID)
	})
	if err != nil {
		return nil, fmt.Errorf("energy: queue a run: %w", err)
	}
	if !queued {
		return gen.PostEnergyImportsByImportIdRun409ApplicationProblemPlusJSONResponse(importRunningConflict()), nil
	}
	nudgeImportWorker()
	view, err := s.importByID(ctx, imp.ID)
	if err != nil || view == nil {
		return nil, fmt.Errorf("energy: re-read import %d after its run: %w", imp.ID, err)
	}
	return gen.PostEnergyImportsByImportIdRun200JSONResponse(*view), nil
}

// PostEnergyImportsByImportIdAttach Attach a metering-point import's pending points
// (POST /api/v1/energy/imports/{importId}/attach)
//
// The order of refusals: the import (404), its state (409 import_running, or
// import_nothing_pending for one not done or with nothing pending), the start
// (400), the switches counted now against confirmSwitches (409). The pending
// rows' switch_from is read again and the import queued in one transaction.
func (s *server) PostEnergyImportsByImportIdAttach(ctx context.Context, req gen.PostEnergyImportsByImportIdAttachRequestObject) (gen.PostEnergyImportsByImportIdAttachResponseObject, error) {
	q := store.New(s.deps.Pool)
	imp, err := q.GetImport(ctx, req.ImportId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostEnergyImportsByImportIdAttach404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: read import %d: %w", req.ImportId, err)
	}
	switch {
	case importStateIs(imp.State, gen.Checking, gen.Importing, gen.Attaching):
		return gen.PostEnergyImportsByImportIdAttach409ApplicationProblemPlusJSONResponse(importRunningConflict()), nil
	case !importStateIs(imp.State, gen.Done) || imp.Pending == 0:
		return gen.PostEnergyImportsByImportIdAttach409ApplicationProblemPlusJSONResponse(importNothingPendingConflict()), nil
	}
	body := gen.EnergyImportAttachRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	if msg := validateSupplyPeriodStart(body.Start); msg != "" {
		return gen.PostEnergyImportsByImportIdAttach400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			"Invalid attach", map[string][]string{"start": {msg}})), nil
	}
	switches, err := q.CountPendingSwitchesNow(ctx, imp.ID)
	if err != nil {
		return nil, fmt.Errorf("energy: count import %d's switches: %w", imp.ID, err)
	}
	confirmed := int32(0)
	if body.ConfirmSwitches != nil {
		confirmed = *body.ConfirmSwitches
	}
	if confirmed != int32(switches) {
		return gen.PostEnergyImportsByImportIdAttach409ApplicationProblemPlusJSONResponse(importSwitchNotConfirmedConflict(int32(switches))), nil
	}
	now := s.deps.Clock()
	start := body.Start.UTC()
	queued := false
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		n, err := txq.RequestImportAttach(ctx, store.RequestImportAttachParams{ID: imp.ID, RequestedAt: &now, AttachStart: &start, ConfirmedSwitches: confirmed})
		if err != nil || n == 0 {
			return err
		}
		queued = true
		return txq.PreparePendingRowsForAttach(ctx, imp.ID)
	})
	if err != nil {
		return nil, fmt.Errorf("energy: queue an attach: %w", err)
	}
	if !queued {
		return gen.PostEnergyImportsByImportIdAttach409ApplicationProblemPlusJSONResponse(importRunningConflict()), nil
	}
	nudgeImportWorker()
	view, err := s.importByID(ctx, imp.ID)
	if err != nil || view == nil {
		return nil, fmt.Errorf("energy: re-read import %d after its attach: %w", imp.ID, err)
	}
	return gen.PostEnergyImportsByImportIdAttach200JSONResponse(*view), nil
}

// PostEnergyImportsByImportIdStop Stop a metering-point import
// (POST /api/v1/energy/imports/{importId}/stop)
//
// A run the worker has not taken up goes straight back to where it was asked
// from; one under way is flagged, and the worker finishes it between chunks.
func (s *server) PostEnergyImportsByImportIdStop(ctx context.Context, req gen.PostEnergyImportsByImportIdStopRequestObject) (gen.PostEnergyImportsByImportIdStopResponseObject, error) {
	q := store.New(s.deps.Pool)
	imp, err := q.GetImport(ctx, req.ImportId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PostEnergyImportsByImportIdStop404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: read import %d: %w", req.ImportId, err)
	}
	var back gen.EnergyImportState
	switch {
	case importStateIs(imp.State, gen.Checking):
		back = gen.Draft
	case importStateIs(imp.State, gen.Importing):
		back = gen.Checked
	case importStateIs(imp.State, gen.Attaching):
		back = gen.Done
	default:
		return gen.PostEnergyImportsByImportIdStop409ApplicationProblemPlusJSONResponse(importFinishedConflict()), nil
	}
	now := s.deps.Clock()
	reverted := int64(0)
	if imp.StartedAt == nil {
		reverted, err = q.RevertUnstartedImport(ctx, store.RevertUnstartedImportParams{ID: imp.ID, State: string(back), UpdatedAt: now, State_2: imp.State})
		if err != nil {
			return nil, fmt.Errorf("energy: revert an unstarted import: %w", err)
		}
	}
	if reverted == 0 {
		if _, err := q.RequestImportStop(ctx, store.RequestImportStopParams{ID: imp.ID, UpdatedAt: now}); err != nil {
			return nil, fmt.Errorf("energy: ask an import to stop: %w", err)
		}
	}
	view, err := s.importByID(ctx, imp.ID)
	if err != nil || view == nil {
		return nil, fmt.Errorf("energy: re-read import %d after its stop: %w", imp.ID, err)
	}
	return gen.PostEnergyImportsByImportIdStop200JSONResponse(*view), nil
}

// DeleteEnergyImportsByImportId Delete a metering-point import
// (DELETE /api/v1/energy/imports/{importId})
func (s *server) DeleteEnergyImportsByImportId(ctx context.Context, req gen.DeleteEnergyImportsByImportIdRequestObject) (gen.DeleteEnergyImportsByImportIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	n, err := q.DeleteImport(ctx, req.ImportId)
	if err != nil {
		return nil, fmt.Errorf("energy: delete import %d: %w", req.ImportId, err)
	}
	if n == 0 {
		if _, err := q.GetImport(ctx, req.ImportId); errors.Is(err, pgx.ErrNoRows) {
			return gen.DeleteEnergyImportsByImportId404Response{}, nil
		} else if err != nil {
			return nil, fmt.Errorf("energy: read import %d: %w", req.ImportId, err)
		}
		return gen.DeleteEnergyImportsByImportId409ApplicationProblemPlusJSONResponse(importRunningConflict()), nil
	}
	return gen.DeleteEnergyImportsByImportId204Response{}, nil
}

// GetEnergyImportsByImportIdRows List a metering-point import's rows
// (GET /api/v1/energy/imports/{importId}/rows)
//
// Each row's customers are named through the customer directory, one batch
// read for the page; with no directory composed the names are absent.
func (s *server) GetEnergyImportsByImportIdRows(ctx context.Context, req gen.GetEnergyImportsByImportIdRowsRequestObject) (gen.GetEnergyImportsByImportIdRowsResponseObject, error) {
	invalid := func(msg string) (gen.GetEnergyImportsByImportIdRowsResponseObject, error) {
		return gen.GetEnergyImportsByImportIdRows400ApplicationProblemPlusJSONResponse(apicommon.Problem("Invalid query parameters", msg)), nil
	}
	page, pageSize, msg := importPageParams(req.Params.Page, req.Params.PageSize, importRowsDefaultPageSize, importRowsMaxPageSize)
	if msg != "" {
		return invalid(msg)
	}
	if o := req.Params.Outcome; o != nil {
		switch gen.EnergyImportRowOutcome(*o) {
		case gen.Created, gen.Updated, gen.Failed, gen.Skipped:
		default:
			return invalid(fmt.Sprintf("'outcome' must be one of 'created', 'updated', 'failed' or 'skipped', but was '%s'.", *o))
		}
	}
	if l := req.Params.Link; l != nil {
		switch gen.EnergyImportRowLink(*l) {
		case gen.None, gen.Same, gen.Assigned, gen.Switched, gen.Pending, gen.Attached:
		default:
			return invalid(fmt.Sprintf("'link' must be one of 'none', 'same', 'assigned', 'switched', 'pending' or 'attached', but was '%s'.", *l))
		}
	}
	q := store.New(s.deps.Pool)
	if _, err := q.GetImport(ctx, req.ImportId); errors.Is(err, pgx.ErrNoRows) {
		return gen.GetEnergyImportsByImportIdRows404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("energy: read import %d: %w", req.ImportId, err)
	}
	total, err := q.CountImportRowsPage(ctx, store.CountImportRowsPageParams{ImportID: req.ImportId, Outcome: req.Params.Outcome, Link: req.Params.Link})
	if err != nil {
		return nil, fmt.Errorf("energy: count import rows: %w", err)
	}
	rows, err := q.ImportRowsPage(ctx, store.ImportRowsPageParams{
		ImportID: req.ImportId, Outcome: req.Params.Outcome, Link: req.Params.Link, Limit: pageSize, Offset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("energy: list import rows: %w", err)
	}
	names, err := s.customerNames(ctx, rows)
	if err != nil {
		return nil, err
	}
	data := make([]gen.EnergyImportRow, 0, len(rows))
	for _, r := range rows {
		errs, err := importRowErrorsFromJSON(r.Errors)
		if err != nil {
			return nil, err
		}
		item := gen.EnergyImportRow{
			Row: r.RowNo, Cells: r.Cells, Errors: errs, MeteringPointId: r.MeteringPointID, Gsrn: r.Gsrn,
			CustomerId: r.CustomerID, SwitchFromCustomerId: r.SwitchFrom, SupplyStart: r.SupplyStart,
			CustomerName: nameOf(names, r.CustomerID), SwitchFromCustomerName: nameOf(names, r.SwitchFrom),
		}
		if r.Outcome != nil {
			o := gen.EnergyImportRowOutcome(*r.Outcome)
			item.Outcome = &o
		}
		if r.Link != nil {
			l := gen.EnergyImportRowLink(*r.Link)
			item.Link = &l
		}
		data = append(data, item)
	}
	return gen.GetEnergyImportsByImportIdRows200JSONResponse(gen.PaginatedResponseOfEnergyImportRow{
		Data: data, Pagination: importPagination(page, pageSize, total),
	}), nil
}

// customerNames is every customer a page of rows names, through the
// directory in one read; empty with no directory composed.
func (s *server) customerNames(ctx context.Context, rows []store.ImportRowsPageRow) (map[int32]string, error) {
	names := map[int32]string{}
	if s.deps.Directory == nil {
		return names, nil
	}
	var ids []int32
	for _, r := range rows {
		for _, id := range []*int32{r.CustomerID, r.SwitchFrom} {
			if id != nil {
				ids = append(ids, *id)
			}
		}
	}
	if len(ids) == 0 {
		return names, nil
	}
	entries, err := s.deps.Directory.Customers(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("energy: name the import rows' customers: %w", err)
	}
	for _, e := range entries {
		names[e.ID] = e.Name
	}
	return names, nil
}

func nameOf(names map[int32]string, id *int32) *string {
	if id == nil {
		return nil
	}
	if name, ok := names[*id]; ok {
		return &name
	}
	return nil
}

// GetEnergyImportsByImportIdFailedRows Download a metering-point import's failed rows
// (GET /api/v1/energy/imports/{importId}/failed-rows)
//
// The file's own header, so the same mapping is suggested when it comes back,
// behind an error column holding every problem of the row, each row's cells
// exactly as they were.
func (s *server) GetEnergyImportsByImportIdFailedRows(ctx context.Context, req gen.GetEnergyImportsByImportIdFailedRowsRequestObject) (gen.GetEnergyImportsByImportIdFailedRowsResponseObject, error) {
	q := store.New(s.deps.Pool)
	imp, err := q.GetImport(ctx, req.ImportId)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetEnergyImportsByImportIdFailedRows404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("energy: read import %d: %w", req.ImportId, err)
	}
	rows, err := q.FailedImportRows(ctx, imp.ID)
	if err != nil {
		return nil, fmt.Errorf("energy: read an import's failed rows: %w", err)
	}
	var b bytes.Buffer
	b.WriteString("\ufeff")
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
	return csvDownload{body: b.Bytes(), fileName: fmt.Sprintf("energy-import-%d-failed-rows.csv", imp.ID)}, nil
}

// importErrorColumn is the failed-rows file's first column.
const importErrorColumn = "error"

// failedRowProblem is one error as the failed-rows file words it.
func failedRowProblem(header []string, e gen.EnergyImportRowError) string {
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

// writeCSVRow writes one row the customers export's way: semicolons, CRLF,
// each cell guarded against a spreadsheet reading it as a formula and quoted
// when it holds a separator, a quote or a line end.
func writeCSVRow(b *bytes.Buffer, cells []string) {
	for i, cell := range cells {
		if i > 0 {
			b.WriteByte(';')
		}
		if strings.IndexAny(cell, "=+-@\t\r") == 0 {
			cell = "'" + cell
		}
		if strings.ContainsAny(cell, ";\"\r\n") {
			cell = `"` + strings.ReplaceAll(cell, `"`, `""`) + `"`
		}
		b.WriteString(cell)
	}
	b.WriteString("\r\n")
}

// csvDownload is a CSV answered as an attachment, kept out of every cache.
type csvDownload struct {
	body     []byte
	fileName string
}

func (d csvDownload) VisitGetEnergyImportsByImportIdFailedRowsResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.fileName))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(d.body)))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(d.body)
	return err
}
