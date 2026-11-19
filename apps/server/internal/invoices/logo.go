package invoices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/logoimage"
	"github.com/vantigo-io/vantigo/server/internal/mail"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is the seller's logo (invoices phase 6 design I1-I3): GET, PUT
// and DELETE /settings/logo, and the read of a document's snapshotted logo
// that its PDF, its reminder letters and its mails print. The bytes live in
// the object store under the module's scope, content-addressed as
// branding/logo-{sha256}.{png|jpg}; the settings row names the key, the hash
// and the type. The object is written before the settings lock is taken, and
// never deleted: removing the logo clears the row, and a document's snapshot
// may name any object a logo ever had (I1). The upload's shape follows
// Staff's person photo (staff/person_photo.go): one multipart part, file,
// read no further than the bound allows, its own bytes deciding its type.

// The logo's bounds and refusals are the shared logoimage package's (brands
// design B6); this file keeps the seller's logo's storage, keys and snapshot
// rules. The request — the file and its multipart framing — stays under the
// router's default body cap of 1 MiB, so the operation needs no BodyLimits
// entry (module.go).
const (
	logoCacheControl = "private, max-age=0, must-revalidate"
	invalidLogoTitle = "Invalid logo"
	contentTypePNG   = logoimage.ContentTypePNG
	contentTypeJPEG  = logoimage.ContentTypeJPEG
	logoKeyPrefix    = "branding/logo-"
	// logoContentID is the logo's Content-ID in a mail, which the HTML
	// body's <img src="cid:logo"> names.
	logoContentID = "logo"
)

// The logo's refusals, each a 400 carrying its code (I1). They are written out
// here, not aliased, because the frontend's catalog test reads this package's
// code… constants as source; logoimage's own are pinned equal by a test.
const (
	codeLogoUnsupportedType = "logo_unsupported_type"
	codeLogoTooLarge        = "logo_too_large"
	codeLogoTooWide         = "logo_too_wide"
	codeLogoTooSmall        = "logo_too_small"
	codeLogoInvalid         = "logo_invalid"
)

// logoRefused is the 400 a logo is refused with: its code and a sentence.
func logoRefused(code, detail string) gen.InvoicesConflictProblem {
	c := conflict(code, invalidLogoTitle, detail)
	c.Status = ptr(int32(http.StatusBadRequest))
	return c
}

// refusedLogo is a shared refusal as this module's 400, nil for none.
func refusedLogo(r *logoimage.Refusal) *gen.InvoicesConflictProblem {
	if r == nil {
		return nil
	}
	return ptr(logoRefused(r.Code, r.Detail))
}

// readLogo reads the one file part of mr (logoimage.Read): the bytes, or the
// refusal.
func readLogo(mr *multipart.Reader) ([]byte, *gen.InvoicesConflictProblem) {
	data, r := logoimage.Read(mr)
	return data, refusedLogo(r)
}

// judgeLogo is the type data is stored as, or its refusal (logoimage.Judge).
func judgeLogo(data []byte) (string, *gen.InvoicesConflictProblem) {
	info, r := logoimage.Judge(data)
	return info.ContentType, refusedLogo(r)
}

// normaliseLogo is a judged logo as it is stored (logoimage.Normalise):
// encoded again without metadata and known to load in the PDF renderer.
func normaliseLogo(data []byte, contentType string) ([]byte, *gen.InvoicesConflictProblem) {
	out, r := logoimage.Normalise(data, contentType)
	return out, refusedLogo(r)
}

// logoKey is where a logo's bytes are stored: named by their hash, so the
// same logo uploaded twice is one object and a key never holds two.
func logoKey(sha, contentType string) string {
	return logoKeyPrefix + sha + "." + logoimage.Extension(contentType)
}

// settingsLogo is the settings' logo for the wire, nil when none is set.
func settingsLogo(row store.InvoicesSetting) *gen.InvoicesSettingsLogo {
	if row.LogoObjectKey == nil || row.LogoSha256 == nil || row.LogoContentType == nil {
		return nil
	}
	return &gen.InvoicesSettingsLogo{ContentType: gen.InvoicesSettingsLogoContentType(*row.LogoContentType), Sha256: *row.LogoSha256}
}

// withLogoState marks the settings' logo missing when the store no longer
// holds its object (review of delivery A): the page then asks for it again. A
// store that cannot be asked leaves the answer as it is, logged.
func (s *server) withLogoState(ctx context.Context, resp *gen.InvoicesSettingsResponse, row store.InvoicesSetting) {
	if resp.Logo == nil || row.LogoObjectKey == nil {
		return
	}
	exists, err := s.objectExists(ctx, *row.LogoObjectKey)
	switch {
	case err != nil:
		s.deps.Logger.WarnContext(ctx, "invoices: whether the logo is in the store could not be read", "error", err.Error())
	case !exists:
		resp.Logo.Missing = ptr(true)
	}
}

// goneLogo is the settings' logo key when an issue must not snapshot it, ""
// otherwise: its object is not in the store, or the store cannot say. Read on
// the pool before the issue's transaction, and never a refusal — an issue
// does not fail for a logo; it is issued without one, and the operator told.
func (s *server) goneLogo(ctx context.Context, q *store.Queries) string {
	settings, err := q.GetSettings(ctx)
	if err != nil || settings.LogoObjectKey == nil {
		return ""
	}
	key := *settings.LogoObjectKey
	exists, err := s.objectExists(ctx, key)
	switch {
	case err != nil:
		s.deps.Logger.ErrorContext(ctx, "invoices: the logo could not be checked in the store; the document is issued without it",
			"logo_key", key, "error", err.Error())
	case !exists:
		s.deps.Logger.ErrorContext(ctx, "invoices: the logo the settings name is not in the store; the document is issued without it — upload it again",
			"logo_key", key)
	default:
		return ""
	}
	return key
}

// PutInvoicesSettingsLogo Set the seller's logo
// (PUT /api/v1/invoices/settings/logo)
//
// The file is judged first — nothing is stored for a refused one — then
// normalised (normaliseLogo), the normalised bytes hashed and put in the
// store outside any transaction (503 storage_unavailable with nothing
// recorded), and only then named on the settings under their lock, the
// revision left as it is. An object the row no longer names is kept: a
// document's snapshot may name it (I1).
func (s *server) PutInvoicesSettingsLogo(ctx context.Context, req gen.PutInvoicesSettingsLogoRequestObject) (gen.PutInvoicesSettingsLogoResponseObject, error) {
	data, refusal := readLogo(req.Body)
	if refusal != nil {
		return gen.PutInvoicesSettingsLogo400ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	contentType, refusal := judgeLogo(data)
	if refusal != nil {
		return gen.PutInvoicesSettingsLogo400ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	stored, refusal := normaliseLogo(data, contentType)
	if refusal != nil {
		return gen.PutInvoicesSettingsLogo400ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	sum := sha256.Sum256(stored)
	sha := hex.EncodeToString(sum[:])
	key := logoKey(sha, contentType)
	if !s.storageConfigured {
		return gen.PutInvoicesSettingsLogo503ApplicationProblemPlusJSONResponse(storageUnavailable(
			"This installation has no object store, so a logo cannot be stored. Nothing was recorded.")), nil
	}
	if err := s.objectPut(ctx, key, bytes.NewReader(stored), contentType); err != nil {
		s.deps.Logger.ErrorContext(ctx, "invoices: a logo could not be written to the store", "error", err.Error())
		return gen.PutInvoicesSettingsLogo503ApplicationProblemPlusJSONResponse(storageUnavailable(
			"The logo could not be stored. Nothing was recorded; try again.")), nil
	}

	var saved store.InvoicesSetting
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if _, err := txq.LockSettings(ctx); err != nil {
			return fmt.Errorf("invoices: lock the settings: %w", err)
		}
		var err error
		saved, err = txq.SetSettingsLogo(ctx, store.SetSettingsLogoParams{
			LogoObjectKey: &key, LogoSha256: &sha, LogoContentType: &contentType, Now: s.deps.Clock(),
		})
		if err != nil {
			return fmt.Errorf("invoices: set the logo: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	resp, err := s.settingsAnswer(ctx, saved)
	if err != nil {
		return nil, err
	}
	return gen.PutInvoicesSettingsLogo200JSONResponse(resp), nil
}

// DeleteInvoicesSettingsLogo Remove the seller's logo
// (DELETE /api/v1/invoices/settings/logo)
//
// Under the settings lock: no logo set is the 404, else the three columns
// are cleared. The object stays in the store (I1).
func (s *server) DeleteInvoicesSettingsLogo(ctx context.Context, _ gen.DeleteInvoicesSettingsLogoRequestObject) (gen.DeleteInvoicesSettingsLogoResponseObject, error) {
	none := false
	err := s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		current, err := txq.LockSettings(ctx)
		if err != nil {
			return fmt.Errorf("invoices: lock the settings: %w", err)
		}
		if current.LogoObjectKey == nil {
			none = true
			return nil
		}
		if _, err := txq.ClearSettingsLogo(ctx, s.deps.Clock()); err != nil {
			return fmt.Errorf("invoices: remove the logo: %w", err)
		}
		return nil
	})
	switch {
	case err != nil:
		return nil, err
	case none:
		return gen.DeleteInvoicesSettingsLogo404Response{}, nil
	}
	return gen.DeleteInvoicesSettingsLogo204Response{}, nil
}

// logoDownload sets the headers a served logo carries before the generated
// response writes its type, its length and the bytes: its hash as the ETag,
// revalidated on every use, never sniffed.
type logoDownload struct {
	body gen.GetInvoicesSettingsLogoResponseObject
	sha  string
}

func (r logoDownload) VisitGetInvoicesSettingsLogoResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", logoCacheControl)
	w.Header().Set("ETag", `"`+r.sha+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	return r.body.VisitGetInvoicesSettingsLogoResponse(w)
}

// logoNotModified is the 304 a client holding the current logo is answered,
// with the same ETag and caching as the logo itself.
type logoNotModified struct{ sha string }

func (r logoNotModified) VisitGetInvoicesSettingsLogoResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", logoCacheControl)
	w.Header().Set("ETag", `"`+r.sha+`"`)
	return gen.GetInvoicesSettingsLogo304Response{}.VisitGetInvoicesSettingsLogoResponse(w)
}

// etagMatches reports whether an If-None-Match header names sha: "*", or one
// of its comma-separated tags, weak or strong, quoted or bare.
func etagMatches(header *string, sha string) bool {
	if header == nil {
		return false
	}
	for _, tag := range strings.Split(*header, ",") {
		tag = strings.Trim(strings.TrimPrefix(strings.TrimSpace(tag), "W/"), `"`)
		if tag == "*" || tag == sha {
			return true
		}
	}
	return false
}

// GetInvoicesSettingsLogo Get the seller's logo
// (GET /api/v1/invoices/settings/logo)
//
// The logo the settings name, read from the store outside any transaction:
// none set, or an object the store no longer holds, is the bare 404; a store
// that cannot be read is 503.
func (s *server) GetInvoicesSettingsLogo(ctx context.Context, req gen.GetInvoicesSettingsLogoRequestObject) (gen.GetInvoicesSettingsLogoResponseObject, error) {
	row, err := store.New(s.deps.Pool).GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	if row.LogoObjectKey == nil || row.LogoSha256 == nil || row.LogoContentType == nil {
		return gen.GetInvoicesSettingsLogo404Response{}, nil
	}
	if etagMatches(req.Params.IfNoneMatch, *row.LogoSha256) {
		return logoNotModified{sha: *row.LogoSha256}, nil
	}
	data, contentType, err := s.logoOf(ctx, row.LogoObjectKey, row.LogoSha256)
	switch {
	case errors.Is(err, storage.ErrNotExist):
		return gen.GetInvoicesSettingsLogo404Response{}, nil
	case errors.Is(err, errObjectStore):
		s.deps.Logger.ErrorContext(ctx, "invoices: the logo could not be read from the store", "error", err.Error())
		return gen.GetInvoicesSettingsLogo503ApplicationProblemPlusJSONResponse(storageUnavailable(
			"The logo could not be read from the store; try again.")), nil
	case err != nil:
		return nil, err
	}
	length := int64(len(data))
	if contentType == contentTypeJPEG {
		return logoDownload{sha: *row.LogoSha256, body: gen.GetInvoicesSettingsLogo200ImagejpegResponse{Body: bytes.NewReader(data), ContentLength: length}}, nil
	}
	return logoDownload{sha: *row.LogoSha256, body: gen.GetInvoicesSettingsLogo200ImagepngResponse{Body: bytes.NewReader(data), ContentLength: length}}, nil
}

// logoOf is the logo a key and its hash name — a document's snapshot or the
// settings — read whole from the store, nil with no key. Its bytes must hash
// to sha, else it is an error, never a document printed with another logo
// (I2). A store that cannot be read wraps errObjectStore, so a download
// answers 503; an object the store does not hold wraps storage.ErrNotExist
// and errObjectStore both — retryable, since uploading the same file again
// puts the object back under its content's key.
// It answers the bytes and the type they sniff as, PNG or JPEG.
func (s *server) logoOf(ctx context.Context, key, sha *string) ([]byte, string, error) {
	if key == nil || sha == nil {
		return nil, "", nil
	}
	if !s.storageConfigured {
		return nil, "", fmt.Errorf("%w: no object store for logo %s", errObjectStore, *key)
	}
	rc, err := s.objectGet(ctx, *key)
	if errors.Is(err, storage.ErrNotExist) {
		return nil, "", fmt.Errorf("%w: logo %s is not in the store: %w", errObjectStore, *key, err)
	}
	if err != nil {
		return nil, "", fmt.Errorf("%w: read logo %s: %w", errObjectStore, *key, err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, logoimage.MaxStoredBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("%w: read logo %s: %w", errObjectStore, *key, err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != *sha {
		return nil, "", fmt.Errorf("invoices: logo %s does not hash to its recorded SHA-256", *key)
	}
	contentType := http.DetectContentType(data)
	if !logoimage.Supported(contentType) {
		return nil, "", fmt.Errorf("invoices: logo %s is %s, neither a PNG nor a JPEG", *key, contentType)
	}
	return data, contentType, nil
}

// documentLogo is an issued document's logo, from its snapshot: its brand's
// when the issue copied one (brands design B27), else the seller's.
func (s *server) documentLogo(ctx context.Context, inv store.InvoicesInvoice) ([]byte, string, error) {
	if inv.BrandLogoObjectKey != nil {
		return s.logoOf(ctx, inv.BrandLogoObjectKey, inv.BrandLogoSha256)
	}
	return s.logoOf(ctx, inv.SellerLogoObjectKey, inv.SellerLogoSha256)
}

// logoAttachment is the logo as a mail's inline part, named so the HTML
// body's cid:logo resolves.
func logoAttachment(data []byte, contentType string) mail.Attachment {
	name := "logo." + logoimage.Extension(contentType)
	return mail.Attachment{FileName: name, ContentType: contentType, Content: data, ContentID: logoContentID, Inline: true}
}
