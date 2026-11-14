package accounting

// A voucher's attachments (A4): the documentation a voucher rests on, kept in
// the object store under the module's own scope, added after posting too, and
// never removed — bokføringsloven keeps them as long as the voucher.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/accounting/gen"
	"github.com/vantigo-io/vantigo/server/internal/accounting/store"
	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

const (
	// maxAttachmentBytes is an attachment's most: 20 MB.
	maxAttachmentBytes = 20 << 20
	// attachmentFormField is the multipart field the file is sent in.
	attachmentFormField = "file"
	// storageScope is the module's prefix in the object store.
	storageScope = "accounting"
)

// moduleObjectStore is the deployment's object store under the module's
// scope; an installation without one answers ErrNotConfigured on use.
func moduleObjectStore(d module.Deps) (storage.ObjectStore, error) {
	base := d.ObjectStore
	if base == nil {
		cfg := d.Config
		if cfg == nil {
			cfg = &config.Config{}
		}
		var err error
		if base, err = storage.New(cfg); err != nil {
			return nil, fmt.Errorf("accounting: build the object store: %w", err)
		}
	}
	scoped, err := storage.NewScope(base, storageScope)
	if err != nil {
		return nil, fmt.Errorf("accounting: scope the object store: %w", err)
	}
	return scoped, nil
}

func (s *server) objectStore() (storage.ObjectStore, error) {
	if s.objectsErr != nil {
		return nil, s.objectsErr
	}
	if s.objects == nil {
		return nil, storage.ErrNotConfigured
	}
	return s.objects, nil
}

// storageRefusal is the 503 of a store that is not configured or failed.
func storageRefusal(err error) error {
	if errors.Is(err, storage.ErrNotConfigured) {
		return refuse(http.StatusServiceUnavailable, codeStorageNotConfigured, "File storage is not configured",
			"This installation has no object store, so an attachment cannot be kept. Nothing was changed.")
	}
	return refuse(http.StatusServiceUnavailable, "", "File storage is unavailable",
		"The file store could not be reached. Nothing was changed; try again.")
}

func attachmentView(a store.AccountingAttachment) gen.AccountingAttachment {
	return gen.AccountingAttachment{Id: a.ID, FileName: a.FileName, ContentType: a.ContentType, SizeBytes: a.SizeBytes,
		Sha256: a.Sha256, UploadedAt: a.UploadedAt}
}

// uploadedFile is the one file of an upload's body.
type uploadedFile struct {
	name, contentType string
	data              []byte
}

// readUpload reads the multipart body's file field, at most the bound.
func readUpload(mr *multipart.Reader) (uploadedFile, error) {
	const title = "The file cannot be attached"
	if mr == nil {
		return uploadedFile{}, invalidField(title, attachmentFormField, "", "Send one file in the multipart field file.")
	}
	var out uploadedFile
	files := 0
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return uploadedFile{}, invalidField(title, attachmentFormField, codeAttachmentTooLarge, "An attachment is at most 20 MB.")
			}
			return uploadedFile{}, invalidField(title, attachmentFormField, "", "The body is not a readable multipart form.")
		}
		if part.FormName() != attachmentFormField {
			_ = part.Close()
			continue
		}
		files++
		data, err := io.ReadAll(io.LimitReader(part, maxAttachmentBytes+1))
		_ = part.Close()
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return uploadedFile{}, invalidField(title, attachmentFormField, codeAttachmentTooLarge, "An attachment is at most 20 MB.")
			}
			return uploadedFile{}, invalidField(title, attachmentFormField, "", "The file could not be read.")
		}
		if len(data) > maxAttachmentBytes {
			return uploadedFile{}, invalidField(title, attachmentFormField, codeAttachmentTooLarge, "An attachment is at most 20 MB.")
		}
		out = uploadedFile{name: part.FileName(), contentType: part.Header.Get("Content-Type"), data: data}
	}
	if files != 1 || len(out.data) == 0 {
		return uploadedFile{}, invalidField(title, attachmentFormField, "", "Send one file, not empty, in the multipart field file.")
	}
	return out, nil
}

// cleanFileName is a file's name as it is kept: its base, valid UTF-8, at
// most 255 characters, "attachment" when there is none.
func cleanFileName(name string) string {
	name = strings.TrimSpace(path.Base(strings.ReplaceAll(name, "\\", "/")))
	if !utf8.ValidString(name) || name == "" || name == "." || name == "/" {
		return "attachment"
	}
	if r := []rune(name); len(r) > 255 {
		name = string(r[:255])
	}
	return name
}

// UploadAccountingAttachment keeps a file on a voucher: the object first, the
// row after it; the object is removed again if the row is refused.
func (s *server) UploadAccountingAttachment(ctx context.Context, req gen.UploadAccountingAttachmentRequestObject) (gen.UploadAccountingAttachmentResponseObject, error) {
	file, err := readUpload(req.Body)
	if err != nil {
		return nil, err
	}
	q := store.New(s.deps.Pool)
	exists, err := q.VoucherExists(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, notFound()
	}
	contentType := file.contentType
	if _, _, err := mime.ParseMediaType(contentType); err != nil || contentType == "" || contentType == "application/octet-stream" {
		contentType, _, _ = strings.Cut(http.DetectContentType(file.data), ";")
	}
	if len(contentType) > 100 {
		contentType = "application/octet-stream"
	}
	sum := sha256.Sum256(file.data)
	st, err := s.objectStore()
	if err != nil {
		return nil, storageRefusal(err)
	}
	key := fmt.Sprintf("vouchers/%d/%s", req.Id, uuid.New().String())
	if err := st.Put(ctx, key, bytes.NewReader(file.data), contentType); err != nil {
		s.logger().ErrorContext(ctx, "accounting: an attachment could not be written to the store", "voucher_id", req.Id, "error", err.Error())
		return nil, storageRefusal(err)
	}
	var row store.AccountingAttachment
	err = s.withTx(ctx, func(_ pgx.Tx, q *store.Queries) error {
		var err error
		row, err = q.InsertAttachment(ctx, store.InsertAttachmentParams{VoucherID: req.Id, ObjectKey: key,
			FileName: cleanFileName(file.name), ContentType: contentType, SizeBytes: int64(len(file.data)),
			Sha256: hex.EncodeToString(sum[:]), Now: s.now(), UserID: callerID(ctx)})
		return err
	})
	if err != nil {
		if derr := st.Delete(context.WithoutCancel(ctx), key); derr != nil {
			s.logger().ErrorContext(ctx, "accounting: an orphaned attachment object could not be removed", "error", derr.Error())
		}
		return nil, err
	}
	return gen.UploadAccountingAttachment201JSONResponse(attachmentView(row)), nil
}

// attachmentDownload sets the headers an attachment is served with: never
// cached, never sniffed, a download under its name.
type attachmentDownload struct {
	name string
	body gen.GetAccountingAttachmentResponseObject
}

func (r attachmentDownload) VisitGetAccountingAttachmentResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": r.name}))
	return r.body.VisitGetAccountingAttachmentResponse(w)
}

// GetAccountingAttachment streams one of a voucher's attachments.
func (s *server) GetAccountingAttachment(ctx context.Context, req gen.GetAccountingAttachmentRequestObject) (gen.GetAccountingAttachmentResponseObject, error) {
	a, err := store.New(s.deps.Pool).GetAttachment(ctx, store.GetAttachmentParams{ID: req.AttachmentId, VoucherID: req.Id})
	if noRows(err) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	st, err := s.objectStore()
	if err != nil {
		return nil, storageRefusal(err)
	}
	body, err := st.Get(ctx, a.ObjectKey)
	if err != nil {
		s.logger().ErrorContext(ctx, "accounting: an attachment could not be read from the store", "attachment_id", a.ID, "error", err.Error())
		return nil, storageRefusal(err)
	}
	return attachmentDownload{name: a.FileName, body: gen.GetAccountingAttachment200AsteriskResponse{Body: body,
		ContentType: a.ContentType, ContentLength: a.SizeBytes}}, nil
}
