package manufacturing

// A check's photos (manufacturing phase 2 M97): Inventory's lot files
// (internal/inventory/lot_attachments.go) for the station's camera, copied
// because no module imports another. The bytes live in the object store under
// the scope manufacturing-quality and the row in manufacturing.check_photos;
// the two cannot be written in one transaction, so the store is reached only
// through the accessors below and never from inside a transaction of this
// module: the upload writes the object first and removes it after a refusal,
// a read takes the row on the pool and the object after it. A photo is a
// quality record and nothing deletes one a result names; one no result names
// 24 hours after its upload is deleted with its object queued on
// manufacturing.object_deletions in one transaction, and the
// manufacturing.object-deletions worker removes the objects, retried until
// the store confirms.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/imagemeta"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/gen"
	"github.com/vantigo-io/vantigo/server/internal/manufacturing/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// qualityStorageScope is this module's namespace in the object store.
const qualityStorageScope = "manufacturing-quality"

// The bounds and codes of a photo (M97).
const (
	maxPhotoBytes        = 10 * 1024 * 1024
	maxPhotoRequestBytes = maxPhotoBytes + 64*1024
	photoFormField       = "file"
	photoTitle           = "The photo was not kept"
	photoPolicy          = "default-src 'none'; sandbox"
	photoCleanupTimeout  = 30 * time.Second
	stalePhotoAfter      = 24 * time.Hour
	codePhotoTooLarge    = "attachment_too_large"
	codePhotoType        = "attachment_type"
	codeStorageNotConfig = "storage_not_configured"
)

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
			return nil, fmt.Errorf("manufacturing: build the object store: %w", err)
		}
	}
	scoped, err := storage.NewScope(base, qualityStorageScope)
	if err != nil {
		return nil, fmt.Errorf("manufacturing: scope the object store: %w", err)
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
	if err := noteContractCall(ctx, "storage.Put"); err != nil {
		return err
	}
	st, err := s.objectStore()
	if err != nil {
		return err
	}
	return st.Put(ctx, key, r, contentType)
}

func (s *server) objectGet(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := noteContractCall(ctx, "storage.Get"); err != nil {
		return nil, err
	}
	st, err := s.objectStore()
	if err != nil {
		return nil, err
	}
	return st.Get(ctx, key)
}

func (s *server) objectDelete(ctx context.Context, key string) error {
	if err := noteContractCall(ctx, "storage.Delete"); err != nil {
		return err
	}
	st, err := s.objectStore()
	if err != nil {
		return err
	}
	return st.Delete(ctx, key)
}

// storageUnavailable is the 503 of a store that is not configured, or that
// failed.
func storageUnavailable(err error) gen.ManufacturingProblem {
	if errors.Is(err, storage.ErrNotConfigured) {
		return problem(http.StatusServiceUnavailable, codeStorageNotConfig, "File storage is not configured",
			"This installation has no object store, so a check's photo cannot be kept. Nothing was changed.")
	}
	return problem(http.StatusServiceUnavailable, "", "File storage is unavailable",
		"The file store could not be reached. Nothing was changed; try again.")
}

// photoForm is one upload's body as read.
type photoForm struct {
	data       []byte
	files      int
	tooLarge   bool
	unreadable bool
}

// readPhotoForm reads every part of mr, the file up to the bound and one
// byte more.
func readPhotoForm(mr *multipart.Reader) photoForm {
	var f photoForm
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
		if part.FormName() != photoFormField {
			_ = part.Close()
			continue
		}
		f.files++
		data, err := io.ReadAll(io.LimitReader(part, maxPhotoBytes+1))
		_ = part.Close()
		if err != nil {
			f.unreadable = true
			return f
		}
		if len(data) > maxPhotoBytes {
			f.tooLarge = true
			return f
		}
		f.data = data
	}
}

// UploadManufacturingCheckPhoto keeps a check's photo (M97): judged by its
// own bytes as a JPEG or PNG, its metadata stripped, written to the store
// before its row, and removed again when the row is refused.
func (s *server) UploadManufacturingCheckPhoto(ctx context.Context, req gen.UploadManufacturingCheckPhotoRequestObject) (gen.UploadManufacturingCheckPhotoResponseObject, error) {
	if err := s.requireShopFloor(ctx, "Taking a check's photo"); err != nil {
		_, p, _ := answerOf(err)
		return gen.UploadManufacturingCheckPhoto403ApplicationProblemPlusJSONResponse(p), nil
	}
	form := readPhotoForm(req.Body)
	switch {
	case form.tooLarge:
		return gen.UploadManufacturingCheckPhoto400ApplicationProblemPlusJSONResponse(problem(http.StatusBadRequest, codePhotoTooLarge,
			photoTitle, "A check's photo is at most 10 MB.")), nil
	case form.unreadable, form.files != 1, len(form.data) == 0:
		return gen.UploadManufacturingCheckPhoto400ApplicationProblemPlusJSONResponse(
			withField(problem(http.StatusBadRequest, "", photoTitle, ""), photoFormField, "Send one picture, in the multipart field file")), nil
	}
	sniffed, _, _ := strings.Cut(http.DetectContentType(form.data), ";")
	if sniffed != "image/jpeg" && sniffed != "image/png" {
		return gen.UploadManufacturingCheckPhoto415ApplicationProblemPlusJSONResponse(problem(http.StatusUnsupportedMediaType, codePhotoType,
			"The file is not a photo", "A check's photo is a JPEG or a PNG.")), nil
	}
	data, err := imagemeta.Strip(sniffed, form.data)
	if err != nil {
		return gen.UploadManufacturingCheckPhoto400ApplicationProblemPlusJSONResponse(
			withField(problem(http.StatusBadRequest, "", photoTitle, ""), photoFormField, "The picture cannot be read")), nil
	}
	w, err := store.New(s.deps.Pool).GetWorkOrder(ctx, req.Id)
	if noRows(err) {
		return gen.UploadManufacturingCheckPhoto404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	if w.Status == workOrderDone || w.Status == workOrderCancelled {
		_, p, _ := answerOf(checkNotOpen(w.Status))
		return gen.UploadManufacturingCheckPhoto409ApplicationProblemPlusJSONResponse(p), nil
	}
	who, err := s.actorOf(ctx)
	if err != nil {
		return nil, err
	}
	key := "checks/" + uuid.New().String()
	if err := s.objectPut(ctx, key, bytes.NewReader(data), sniffed); err != nil {
		s.logger().ErrorContext(ctx, "manufacturing: a check's photo could not be written to the store", "work_order_id", req.Id, "error", err.Error())
		return gen.UploadManufacturingCheckPhoto503ApplicationProblemPlusJSONResponse(storageUnavailable(err)), nil
	}
	var photo store.ManufacturingCheckPhoto
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		var err error
		photo, err = txq.InsertCheckPhoto(ctx, store.InsertCheckPhotoParams{WorkOrderID: w.ID, ObjectKey: key, ContentType: sniffed,
			SizeBytes: int64(len(data)), UploadedByUserID: who.id, UploadedByDisplay: &who.display, Now: s.deps.Clock()})
		return err
	})
	if err != nil {
		s.removeObject(ctx, key)
		return nil, err
	}
	return gen.UploadManufacturingCheckPhoto201JSONResponse(gen.ManufacturingCheckPhoto{Id: photo.ID, WorkOrderId: photo.WorkOrderID,
		ContentType: photo.ContentType, SizeBytes: photo.SizeBytes, UploadedAt: photo.CreatedAt}), nil
}

// removeObject makes one attempt at removing an object nothing points at.
func (s *server) removeObject(ctx context.Context, key string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), photoCleanupTimeout)
	defer cancel()
	if err := s.objectDelete(cleanup, key); err != nil {
		s.logger().ErrorContext(cleanup, "manufacturing: a photo's object could not be removed from the store", "error", err.Error())
	}
}

// photoDownload sets the headers a photo is served with.
type photoDownload struct {
	body gen.GetManufacturingCheckPhotoResponseObject
}

func (r photoDownload) VisitGetManufacturingCheckPhotoResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", photoPolicy)
	return r.body.VisitGetManufacturingCheckPhotoResponse(w)
}

// GetManufacturingCheckPhoto streams a check's photo (M97): the row on the
// pool, the object after it. A photo of another work order is a 404.
func (s *server) GetManufacturingCheckPhoto(ctx context.Context, req gen.GetManufacturingCheckPhotoRequestObject) (gen.GetManufacturingCheckPhotoResponseObject, error) {
	photo, err := store.New(s.deps.Pool).GetCheckPhoto(ctx, store.GetCheckPhotoParams{ID: req.PhotoId, WorkOrderID: req.Id})
	if noRows(err) {
		return photoDownload{body: gen.GetManufacturingCheckPhoto404Response{}}, nil
	}
	if err != nil {
		return nil, err
	}
	body, err := s.objectGet(ctx, photo.ObjectKey)
	if err != nil {
		s.logger().ErrorContext(ctx, "manufacturing: a check's photo could not be read from the store", "photo_id", photo.ID, "error", err.Error())
		return photoDownload{body: gen.GetManufacturingCheckPhoto503ApplicationProblemPlusJSONResponse(storageUnavailable(err))}, nil
	}
	return photoDownload{body: gen.GetManufacturingCheckPhoto200AsteriskResponse{Body: body, ContentType: photo.ContentType, ContentLength: photo.SizeBytes}}, nil
}

// The deletion ledger (M97): Inventory's (lot_attachments.go), rows claimed
// by a conditional update with a five-minute window, a hundred a pass, a
// backoff between attempts. Every statement takes a pooled connection for
// itself and gives it back, so none is held while the store is asked: the
// worker runs on a pool of one.
const (
	objectDeletionsWorkerName = "manufacturing.object-deletions"
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

// finishObjectDeletion is one attempt at a ledger row's object: the row is
// cleared when the store confirms, the failure recorded on it otherwise.
func (s *server) finishObjectDeletion(ctx context.Context, key string, failedBefore int32) bool {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), photoCleanupTimeout)
	defer cancel()
	q := store.New(s.deps.Pool)
	err := s.objectDelete(cleanup, key)
	if err == nil {
		if err := q.DeleteObjectDeletion(cleanup, key); err != nil {
			s.logger().ErrorContext(cleanup, "manufacturing: a removed object's ledger row could not be cleared; the next pass clears it", "error", err.Error())
		}
		return true
	}
	reason := objectDeleteRefused
	if s.objectsErr != nil || errors.Is(err, storage.ErrNotConfigured) {
		reason = objectStoreUnavailable
	}
	s.logger().ErrorContext(cleanup, "manufacturing: an object could not be removed from the store; the ledger retries it",
		"attempts", failedBefore+1, "error", err.Error())
	if err := q.RecordObjectDeletionFailure(cleanup, store.RecordObjectDeletionFailureParams{
		ObjectKey: key, LastError: &reason, NextAttemptAt: s.deps.Clock().Add(retryBackoff(failedBefore + 1)),
	}); err != nil {
		s.logger().ErrorContext(cleanup, "manufacturing: a failed object delete could not be recorded on its ledger row", "error", err.Error())
	}
	return false
}

// objectDeletionsWorker sweeps the photos no result names after a day and
// drains the deletion ledger.
type objectDeletionsWorker struct{ s *server }

var _ worker.Worker = (*objectDeletionsWorker)(nil)

func (*objectDeletionsWorker) Name() string            { return objectDeletionsWorkerName }
func (*objectDeletionsWorker) Interval() time.Duration { return objectDeletionsPoll }

func (w *objectDeletionsWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(objectDeletionsPoll)
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.s.logger().Error("manufacturing: the object deletions cycle failed", "worker", objectDeletionsWorkerName, "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle deletes the photos no result named within a day — each batch in
// one transaction that queues their objects — then gives each due ledger row
// one attempt, until a pass finds less than a full batch. It answers the
// objects the store confirmed gone.
func (w *objectDeletionsWorker) RunCycle(ctx context.Context) (int, error) {
	s := w.s
	for {
		swept := 0
		err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
			now := s.deps.Clock()
			stale, err := txq.StaleCheckPhotos(ctx, store.StaleCheckPhotosParams{Cut: now.Add(-stalePhotoAfter), RowLimit: ledgerBatch})
			if err != nil {
				return err
			}
			for _, p := range stale {
				deleted, err := txq.DeleteUnattachedCheckPhoto(ctx, p.ID)
				if err != nil {
					return err
				}
				if deleted == 0 {
					continue
				}
				if err := txq.InsertObjectDeletion(ctx, store.InsertObjectDeletionParams{ObjectKey: p.ObjectKey, Now: now}); err != nil {
					return err
				}
			}
			swept = len(stale)
			return nil
		})
		if err != nil {
			return 0, fmt.Errorf("manufacturing: sweep the unattached photos: %w", err)
		}
		if swept < ledgerBatch || ctx.Err() != nil {
			break
		}
	}
	q := store.New(s.deps.Pool)
	succeeded := 0
	for {
		keys, err := q.DueObjectDeletions(ctx, store.DueObjectDeletionsParams{Now: s.deps.Clock(), RowLimit: ledgerBatch})
		if err != nil {
			return succeeded, fmt.Errorf("manufacturing: select the ledger's due rows: %w", err)
		}
		for _, key := range keys {
			if ctx.Err() != nil {
				return succeeded, nil
			}
			claimedAt := s.deps.Clock()
			until := claimedAt.Add(ledgerClaimWindow)
			failed, err := q.ClaimObjectDeletion(ctx, store.ClaimObjectDeletionParams{ClaimedUntil: &until, ObjectKey: key, Now: claimedAt})
			if noRows(err) {
				continue
			}
			if err != nil {
				return succeeded, fmt.Errorf("manufacturing: claim a ledger row: %w", err)
			}
			if s.finishObjectDeletion(ctx, key, failed) {
				succeeded++
			}
		}
		if len(keys) < ledgerBatch {
			return succeeded, nil
		}
	}
}
