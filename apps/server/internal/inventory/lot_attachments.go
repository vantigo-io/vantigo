package inventory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// A lot's files (manufacturing phase 2 M54): an EN 10204 3.1 certificate, a
// supplier's analysis, a photo — Customers' attachments (internal/customers/
// attachments.go, objects.go, object_deletions.go) for lots, copied because
// no module imports another. The bytes live in the object store under the
// scope inventory-lots and the metadata in inventory.lot_attachments, and the
// two cannot be written in one transaction, so the store is reached only
// through the accessors below and never from inside a transaction of this
// module: the upload writes the object first and removes it after a refusal;
// a download reads the row on the pool, then the object; a delete removes the
// row and queues the object in one transaction, then removes it once that has
// committed — and the inventory.object-deletions worker retries what the
// store refused, until it confirms.

// lotStorageScope is this module's namespace in the object store.
const lotStorageScope = "inventory-lots"

// The bounds of M54.
const (
	maxLotAttachmentBytes        = 20 * 1024 * 1024
	maxLotAttachmentRequestBytes = maxLotAttachmentBytes + 64*1024
	maxLotAttachments            = 20
	lotAttachmentNameMaxLength   = 255
	lotAttachmentFormField       = "file"
	lotAttachmentFallbackName    = "attachment"
)

// The codes of M54.
const (
	codeAttachmentTooLarge   = "attachment_too_large"
	codeTooManyAttachments   = "too_many_attachments"
	codeAttachmentType       = "attachment_type"
	codeStorageNotConfigured = "storage_not_configured"
)

// The events of a lot's files (M64's list).
const (
	eventLotAttachmentAdded   = "lot_attachment_added"
	eventLotAttachmentRemoved = "lot_attachment_removed"
)

// objectCleanupTimeout bounds an object delete on a context detached from
// the request's: a compensation after a refusal, or the one attempt after a
// delete has committed.
const objectCleanupTimeout = 30 * time.Second

// lotAttachmentPolicy is the policy a served file carries itself: nothing
// may be fetched, and sandbox puts it in an opaque origin.
const lotAttachmentPolicy = "default-src 'none'; sandbox"

// moduleObjectStore is this module's object store: d.ObjectStore when a
// harness set one, otherwise the one Config selects, either way wrapped in
// the module's scope.
func moduleObjectStore(d module.Deps) (storage.ObjectStore, error) {
	base := d.ObjectStore
	if base == nil {
		cfg := d.Config
		if cfg == nil {
			cfg = &config.Config{}
		}
		var err error
		if base, err = storage.New(cfg); err != nil {
			return nil, fmt.Errorf("inventory: build the object store: %w", err)
		}
	}
	scoped, err := storage.NewScope(base, lotStorageScope)
	if err != nil {
		return nil, fmt.Errorf("inventory: scope the object store: %w", err)
	}
	return scoped, nil
}

// objectStore is the store an accessor calls, or the error it was built with.
func (s *server) objectStore() (storage.ObjectStore, error) {
	if s.objectsErr != nil {
		return nil, s.objectsErr
	}
	if s.objects == nil {
		return nil, storage.ErrNotConfigured
	}
	return s.objects, nil
}

func (s *server) objectPut(ctx context.Context, key string, r io.Reader, contentType string) error {
	st, err := s.objectStore()
	if err != nil {
		return err
	}
	return st.Put(ctx, key, r, contentType)
}

func (s *server) objectGet(ctx context.Context, key string) (io.ReadCloser, error) {
	st, err := s.objectStore()
	if err != nil {
		return nil, err
	}
	return st.Get(ctx, key)
}

func (s *server) objectDelete(ctx context.Context, key string) error {
	st, err := s.objectStore()
	if err != nil {
		return err
	}
	return st.Delete(ctx, key)
}

// logger is the Deps' logger, the default one when the Deps carry none.
func (s *server) logger() *slog.Logger {
	if s.deps.Logger != nil {
		return s.deps.Logger
	}
	return slog.Default()
}

// storageUnavailable is the 503 of a store that is not configured, or that
// failed: the request was right and the installation could not serve it.
func storageUnavailable(err error) gen.InventoryProblem {
	if errors.Is(err, storage.ErrNotConfigured) {
		return problem(http.StatusServiceUnavailable, codeStorageNotConfigured, "File storage is not configured",
			"This installation has no object store, so a lot's files cannot be kept. Nothing was changed.")
	}
	return problem(http.StatusServiceUnavailable, "", "File storage is unavailable",
		"The file store could not be reached. Nothing was changed; try again.")
}

// lotFileType is one of M54's three formats: what it is stored and served
// as, the content types a client may declare it with, and its extensions.
type lotFileType struct {
	ContentType string
	Declared    []string
	Extensions  []string
}

var lotFileTypes = []lotFileType{
	{ContentType: "application/pdf", Declared: []string{"application/pdf", "application/x-pdf"}, Extensions: []string{".pdf"}},
	{ContentType: "image/jpeg", Declared: []string{"image/jpeg", "image/jpg", "image/pjpeg"}, Extensions: []string{".jpg", ".jpeg", ".jpe"}},
	{ContentType: "image/png", Declared: []string{"image/png"}, Extensions: []string{".png"}},
}

// lotFileTypeOf is the type a file is stored under: the one its own bytes
// are, which must be one of the three and must not be contradicted by the
// type or the extension it was sent under. A declared type or an extension
// the three do not claim is not evidence either way.
func lotFileTypeOf(declared, fileName string, data []byte) (lotFileType, bool) {
	sniffed, _, _ := strings.Cut(http.DetectContentType(data), ";")
	var t lotFileType
	found := false
	for _, one := range lotFileTypes {
		if strings.EqualFold(one.ContentType, strings.TrimSpace(sniffed)) {
			t, found = one, true
		}
	}
	if !found {
		return lotFileType{}, false
	}
	if mediaType, _, err := mime.ParseMediaType(declared); err == nil && mediaType != "" {
		for _, other := range lotFileTypes {
			if other.ContentType != t.ContentType && slices.ContainsFunc(other.Declared, func(d string) bool { return strings.EqualFold(d, mediaType) }) {
				return lotFileType{}, false
			}
		}
	}
	ext := strings.ToLower(path.Ext(fileName))
	for _, other := range lotFileTypes {
		if other.ContentType != t.ContentType && slices.Contains(other.Extensions, ext) {
			return lotFileType{}, false
		}
	}
	return t, true
}

// lotFileName is the name a file keeps: the client's own without any path
// and control characters, capped at 255 characters, never part of the key.
func lotFileName(raw string) string {
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
		return lotAttachmentFallbackName
	}
	if runes := []rune(name); len(runes) > lotAttachmentNameMaxLength {
		name = string(runes[:lotAttachmentNameMaxLength])
	}
	return name
}

// rfc5987 percent-encodes every byte that is not an RFC 5987 attr-char, so a
// Norwegian file name arrives intact and a quote cannot break the header.
func rfc5987(value string) string {
	const attrChars = "!#$&+-.^_`|~"
	var b strings.Builder
	for _, c := range []byte(value) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte(attrChars, c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// lotFileForm is one upload's body as read.
type lotFileForm struct {
	data               []byte
	fileName, declared string
	files              int
	tooLarge           bool
	unreadable         bool
}

// readLotFileForm reads every part of mr, the file up to the bound and one
// byte more, so too large is told from just within without buffering more.
func readLotFileForm(mr *multipart.Reader) lotFileForm {
	var f lotFileForm
	if mr == nil {
		f.unreadable = true
		return f
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return f
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			f.tooLarge, f.unreadable = errors.As(err, &tooLarge), !errors.As(err, &tooLarge)
			return f
		}
		if part.FormName() != lotAttachmentFormField {
			_ = part.Close()
			continue
		}
		f.files++
		f.fileName, f.declared = part.FileName(), part.Header.Get("Content-Type")
		data, err := io.ReadAll(io.LimitReader(part, maxLotAttachmentBytes+1))
		_ = part.Close()
		if err != nil {
			f.unreadable = true
			return f
		}
		if len(data) > maxLotAttachmentBytes {
			f.tooLarge = true
			return f
		}
		f.data = data
	}
}

// lotAttachmentOf is one file as the contract answers it.
func lotAttachmentOf(a store.InventoryLotAttachment) gen.InventoryLotAttachment {
	out := gen.InventoryLotAttachment{Id: a.ID, FileName: a.FileName, ContentType: a.ContentType, SizeBytes: a.SizeBytes, UploadedAt: a.CreatedAt}
	if a.UploadedByDisplay != nil {
		out.UploadedBy = &gen.InventoryActor{DisplayName: *a.UploadedByDisplay, UserId: a.UploadedByUserID}
	}
	return out
}

// errTooManyAttachments aborts the upload's transaction on the cap.
var errTooManyAttachments = errors.New("inventory: too many attachments")

// PostInventoryLotAttachment Attach a file to a lot
// (POST /api/v1/inventory/lots/{id}/attachments)
//
// What was sent is judged first (400, 415), then the lot (404). The object
// is written before the transaction — a store that cannot write is the 503
// with nothing recorded — and whatever refuses after the write removes it.
// Under the lot's FOR NO KEY UPDATE the cap is counted, the row inserted, the
// revision moved on, the event recorded and the lot's topics told.
func (s *server) PostInventoryLotAttachment(ctx context.Context, req gen.PostInventoryLotAttachmentRequestObject) (gen.PostInventoryLotAttachmentResponseObject, error) {
	form := readLotFileForm(req.Body)
	switch {
	case form.tooLarge:
		return gen.PostInventoryLotAttachment400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, codeAttachmentTooLarge,
			"The file is too large", "A lot's file is at most 20 MB.")), nil
	case form.unreadable, form.files != 1, len(form.data) == 0:
		var f fieldErrors
		f.add(lotAttachmentFormField, "Send one file, in the multipart field file")
		return gen.PostInventoryLotAttachment400ApplicationProblemPlusJSONResponse(invalid("Invalid file", f)), nil
	}
	kind, ok := lotFileTypeOf(form.declared, form.fileName, form.data)
	if !ok {
		return gen.PostInventoryLotAttachment415ApplicationProblemPlusJSONResponse(problem(http.StatusUnsupportedMediaType, codeAttachmentType,
			"The file is not one a lot keeps", "A lot's file is a PDF, JPEG or PNG, and its contents must match the type and the name it is sent under.")), nil
	}
	if _, err := store.New(s.deps.Pool).GetLot(ctx, req.Id); noRows(err) {
		return gen.PostInventoryLotAttachment404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("inventory: read lot %d: %w", req.Id, err)
	}
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	key := "lots/" + uuid.New().String()
	if err := s.objectPut(ctx, key, bytes.NewReader(form.data), kind.ContentType); err != nil {
		s.logger().ErrorContext(ctx, "inventory: a lot's file could not be written to the store", "lot_id", req.Id, "error", err.Error())
		return gen.PostInventoryLotAttachment503ApplicationProblemPlusJSONResponse(storageUnavailable(err)), nil
	}
	var created store.InventoryLotAttachment
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if _, err := lockLotForChange(ctx, txq, req.Id); err != nil {
			return err
		}
		n, err := txq.CountLotAttachments(ctx, req.Id)
		if err != nil {
			return fmt.Errorf("inventory: count the files of lot %d: %w", req.Id, err)
		}
		if n >= maxLotAttachments {
			return errTooManyAttachments
		}
		now := s.deps.Clock()
		if created, err = txq.InsertLotAttachment(ctx, store.InsertLotAttachmentParams{
			LotID: req.Id, ObjectKey: key, FileName: lotFileName(form.fileName), ContentType: kind.ContentType, SizeBytes: int64(len(form.data)),
			UploadedByUserID: who.UserID, UploadedByDisplay: &who.Display, Now: now,
		}); err != nil {
			return fmt.Errorf("inventory: record a file of lot %d: %w", req.Id, err)
		}
		if err := txq.TouchLot(ctx, store.TouchLotParams{ID: req.Id, Now: now}); err != nil {
			return fmt.Errorf("inventory: touch lot %d: %w", req.Id, err)
		}
		if err := s.recordEvent(ctx, txq, who, event{EntityKind: entityLot, EntityID: req.Id, Kind: eventLotAttachmentAdded,
			Payload: map[string]any{"attachmentId": created.ID, "fileName": created.FileName, "contentType": created.ContentType, "sizeBytes": created.SizeBytes}}); err != nil {
			return err
		}
		return notifyLots(ctx, tx, []int64{req.Id})
	})
	if err != nil {
		s.removeObject(ctx, req.Id, key)
	}
	if errors.Is(err, errTooManyAttachments) {
		return gen.PostInventoryLotAttachment400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, codeTooManyAttachments,
			"Too many files", fmt.Sprintf("A lot carries at most %d files. Remove one first.", maxLotAttachments))), nil
	}
	if status, _, ok := answerOf(err); ok && status == http.StatusNotFound {
		return gen.PostInventoryLotAttachment404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return gen.PostInventoryLotAttachment201JSONResponse(lotAttachmentOf(created)), nil
}

// removeObject makes one attempt at removing an object nothing points at,
// on a context detached from the request's, and logs a failure naming the
// lot rather than the key.
func (s *server) removeObject(ctx context.Context, lotID int64, key string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), objectCleanupTimeout)
	defer cancel()
	if err := s.objectDelete(cleanup, key); err != nil {
		s.logger().ErrorContext(cleanup, "inventory: a lot's file object could not be removed from the store", "lot_id", lotID, "error", err.Error())
	}
}

// lotAttachmentDownload sets the headers every file read answers with: never
// cached, never sniffed, a policy of its own, and saved as an attachment
// under the name it was uploaded with — a browser's PDF viewer does not run
// under the sandbox policy, so the file is never shown in place.
type lotAttachmentDownload struct {
	body        gen.GetInventoryLotAttachmentResponseObject
	disposition string
}

func (r lotAttachmentDownload) VisitGetInventoryLotAttachmentResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", lotAttachmentPolicy)
	if r.disposition != "" {
		w.Header().Set("Content-Disposition", r.disposition)
	}
	return r.body.VisitGetInventoryLotAttachmentResponse(w)
}

// GetInventoryLotAttachment Download a lot's file
// (GET /api/v1/inventory/lots/{id}/attachments/{attachmentId})
//
// The row is read on the pool and the object after it, outside any
// transaction. A file of another lot is the 404 an unknown id is.
func (s *server) GetInventoryLotAttachment(ctx context.Context, req gen.GetInventoryLotAttachmentRequestObject) (gen.GetInventoryLotAttachmentResponseObject, error) {
	a, err := store.New(s.deps.Pool).LotAttachment(ctx, store.LotAttachmentParams{ID: req.AttachmentId, LotID: req.Id})
	if noRows(err) {
		return lotAttachmentDownload{body: gen.GetInventoryLotAttachment404Response{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inventory: read file %d of lot %d: %w", req.AttachmentId, req.Id, err)
	}
	body, err := s.objectGet(ctx, a.ObjectKey)
	if err != nil {
		s.logger().ErrorContext(ctx, "inventory: a lot's file could not be read from the store", "attachment_id", a.ID, "error", err.Error())
		return lotAttachmentDownload{body: gen.GetInventoryLotAttachment503ApplicationProblemPlusJSONResponse(storageUnavailable(err))}, nil
	}
	return lotAttachmentDownload{
		body:        gen.GetInventoryLotAttachment200AsteriskResponse{Body: body, ContentType: a.ContentType, ContentLength: a.SizeBytes},
		disposition: fmt.Sprintf("attachment; filename*=UTF-8''%s", rfc5987(a.FileName)),
	}, nil
}

// DeleteInventoryLotAttachment Remove a file from a lot
// (DELETE /api/v1/inventory/lots/{id}/attachments/{attachmentId})
//
// Under the lot's lock the row goes, its object is queued on the ledger, the
// event is recorded and the topics told, in one transaction; once that has
// committed one attempt removes the object, and a store that refuses leaves
// the row for the worker.
func (s *server) DeleteInventoryLotAttachment(ctx context.Context, req gen.DeleteInventoryLotAttachmentRequestObject) (gen.DeleteInventoryLotAttachmentResponseObject, error) {
	who, err := s.actorFor(ctx)
	if err != nil {
		return nil, err
	}
	var removed store.InventoryLotAttachment
	err = s.withLockedTx(ctx, func(ctx context.Context, tx pgx.Tx, txq *store.Queries) error {
		if _, err := lockLotForChange(ctx, txq, req.Id); err != nil {
			return err
		}
		var err error
		if removed, err = txq.DeleteLotAttachment(ctx, store.DeleteLotAttachmentParams{ID: req.AttachmentId, LotID: req.Id}); noRows(err) {
			return notFound()
		} else if err != nil {
			return fmt.Errorf("inventory: remove file %d of lot %d: %w", req.AttachmentId, req.Id, err)
		}
		now := s.deps.Clock()
		if err := txq.InsertObjectDeletion(ctx, store.InsertObjectDeletionParams{ObjectKey: removed.ObjectKey, Now: now}); err != nil {
			return fmt.Errorf("inventory: queue the object of file %d: %w", removed.ID, err)
		}
		if err := txq.TouchLot(ctx, store.TouchLotParams{ID: req.Id, Now: now}); err != nil {
			return fmt.Errorf("inventory: touch lot %d: %w", req.Id, err)
		}
		if err := s.recordEvent(ctx, txq, who, event{EntityKind: entityLot, EntityID: req.Id, Kind: eventLotAttachmentRemoved,
			Payload: map[string]any{"attachmentId": removed.ID, "fileName": removed.FileName}}); err != nil {
			return err
		}
		return notifyLots(ctx, tx, []int64{req.Id})
	})
	if status, _, ok := answerOf(err); ok && status == http.StatusNotFound {
		return gen.DeleteInventoryLotAttachment404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	s.finishObjectDeletion(ctx, removed.ObjectKey, 0, "lot_id", req.Id)
	return gen.DeleteInventoryLotAttachment204Response{}, nil
}

// The deletion ledger (M54): inventory.object_deletions, the objects whose
// rows a delete removed and that the store has not yet confirmed gone —
// Customers' ledger (object_deletions.go): rows claimed by a conditional
// update with a five-minute window, a hundred a pass, a backoff between
// attempts, a constant reason on the row and the store's own words in the
// log. Every statement takes a pooled connection for itself and gives it
// back, so none is held while the store is asked: the worker runs on a pool
// of one.
const (
	objectDeletionsWorkerName = "inventory.object-deletions"
	objectDeletionsPoll       = 5 * time.Minute
	ledgerBatch               = 100
	ledgerClaimWindow         = 5 * time.Minute
	maxRetryBackoff           = time.Hour

	objectDeleteRefused    = "the object store refused the delete"
	objectStoreUnavailable = "storage_unavailable: no object store is configured or it could not be built"
)

// retryBackoff is the wait before the next attempt after attempts failed
// ones: two seconds after the first, doubling, at most an hour.
func retryBackoff(attempts int32) time.Duration {
	if attempts < 1 {
		return time.Second
	}
	if attempts >= 12 {
		return maxRetryBackoff
	}
	return min(time.Duration(1<<attempts)*time.Second, maxRetryBackoff)
}

// finishObjectDeletion is one attempt at a ledger row's object, on a context
// detached from the caller's: the row is cleared when the store confirms (a
// key it no longer has is confirmed too), and the failure recorded on it —
// counted, its reason, the next attempt after the backoff — when it refuses.
// It answers whether the store confirmed.
func (s *server) finishObjectDeletion(ctx context.Context, key string, failedBefore int32, logAttrs ...any) bool {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), objectCleanupTimeout)
	defer cancel()
	q := store.New(s.deps.Pool)
	err := s.objectDelete(cleanup, key)
	if err == nil {
		if err := q.DeleteObjectDeletion(cleanup, key); err != nil {
			s.logger().ErrorContext(cleanup, "inventory: a removed object's ledger row could not be cleared; the next pass clears it",
				append(logAttrs, "error", err.Error())...)
		}
		return true
	}
	reason := objectDeleteRefused
	if s.objectsErr != nil || errors.Is(err, storage.ErrNotConfigured) {
		reason = objectStoreUnavailable
	}
	s.logger().ErrorContext(cleanup, "inventory: an object could not be removed from the store; the ledger retries it",
		append(logAttrs, "attempts", failedBefore+1, "error", err.Error())...)
	if err := q.RecordObjectDeletionFailure(cleanup, store.RecordObjectDeletionFailureParams{
		ObjectKey: key, LastError: &reason, NextAttemptAt: s.deps.Clock().Add(retryBackoff(failedBefore + 1)),
	}); err != nil {
		s.logger().ErrorContext(cleanup, "inventory: a failed object delete could not be recorded on its ledger row",
			append(logAttrs, "error", err.Error())...)
	}
	return false
}

// workers is the module's background work: the deletion ledger's worker.
// cmd/vantigo starts it through module.Workers in worker mode.
func workers(d module.Deps) []worker.Worker {
	return []worker.Worker{&objectDeletionsWorker{s: newServer(d)}}
}

// objectDeletionsWorker drains the deletion ledger.
type objectDeletionsWorker struct{ s *server }

var _ worker.Worker = (*objectDeletionsWorker)(nil)

func (*objectDeletionsWorker) Name() string            { return objectDeletionsWorkerName }
func (*objectDeletionsWorker) Interval() time.Duration { return objectDeletionsPoll }

func (w *objectDeletionsWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(objectDeletionsPoll)
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.s.logger().Error("inventory: the object deletions cycle failed", "worker", objectDeletionsWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is passes over the due rows nobody else has claimed, each claimed
// by a conditional update and given one attempt, until a pass finds less
// than a full batch. It answers the attempts the store confirmed. A row
// another pass won between the selection and the claim is left to it.
func (w *objectDeletionsWorker) RunCycle(ctx context.Context) (int, error) {
	s := w.s
	q := store.New(s.deps.Pool)
	succeeded := 0
	for {
		keys, err := q.DueObjectDeletions(ctx, store.DueObjectDeletionsParams{Now: s.deps.Clock(), RowLimit: ledgerBatch})
		if err != nil {
			return succeeded, fmt.Errorf("inventory: select the ledger's due rows: %w", err)
		}
		for _, key := range keys {
			if ctx.Err() != nil {
				return succeeded, nil
			}
			claimedAt := s.deps.Clock()
			failed, err := q.ClaimObjectDeletion(ctx, store.ClaimObjectDeletionParams{
				ClaimedUntil: claimedAt.Add(ledgerClaimWindow), ObjectKey: key, Now: claimedAt,
			})
			if noRows(err) {
				continue
			}
			if err != nil {
				return succeeded, fmt.Errorf("inventory: claim a ledger row: %w", err)
			}
			if s.finishObjectDeletion(ctx, key, failed, "worker", objectDeletionsWorkerName) {
				succeeded++
			}
		}
		if len(keys) < ledgerBatch {
			return succeeded, nil
		}
	}
}
