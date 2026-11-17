package invoices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // registers image/jpeg with image.DecodeConfig
	_ "image/png"  // registers image/png with image.DecodeConfig
	"io"
	"mime/multipart"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/imagemeta"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
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

// The logo's bounds (I1). The request — the file and its multipart framing —
// stays under the router's default body cap of 1 MiB, so the operation needs
// no BodyLimits entry (module.go).
const (
	maxLogoBytes       = 512 << 10
	maxLogoLongerSide  = 2000
	minLogoShorterSide = 16
	logoFormField      = "file"
	logoCacheControl   = "private, max-age=0, must-revalidate"
	invalidLogoTitle   = "Invalid logo"
	contentTypePNG     = "image/png"
	contentTypeJPEG    = "image/jpeg"
	logoKeyPrefix      = "branding/logo-"
	// logoContentID is the logo's Content-ID in a mail, which the HTML
	// body's <img src="cid:logo"> names.
	logoContentID = "logo"
)

// The logo's refusals, each a 400 carrying its code (I1).
const (
	codeLogoUnsupportedType = "logo_unsupported_type"
	codeLogoTooLarge        = "logo_too_large"
	codeLogoTooWide         = "logo_too_wide"
	codeLogoTooSmall        = "logo_too_small"
	codeLogoInvalid         = "logo_invalid"
)

// logoFormats is the format image.DecodeConfig names for each type a logo may
// be, keyed by the type its bytes sniff as, and the extension its key takes.
var logoFormats = map[string]struct{ format, extension string }{
	contentTypePNG:  {"png", "png"},
	contentTypeJPEG: {"jpeg", "jpg"},
}

// logoRefused is the 400 a logo is refused with: its code and a sentence.
func logoRefused(code, detail string) gen.InvoicesConflictProblem {
	c := conflict(code, invalidLogoTitle, detail)
	c.Status = ptr(int32(http.StatusBadRequest))
	return c
}

// readLogo reads the one file part of mr, at most maxLogoBytes+1 bytes of it
// — enough to tell "too large" from "just within" — skipping any other part.
// It answers the bytes, or the refusal: logo_too_large past the bound (the
// router's own cap included), logo_invalid for a body that is not a readable
// multipart form, no file part, an empty one, or two.
func readLogo(mr *multipart.Reader) ([]byte, *gen.InvoicesConflictProblem) {
	if mr == nil {
		return nil, ptr(logoRefused(codeLogoInvalid, "The request's body could not be read as a multipart form"))
	}
	fail := func(err error) ([]byte, *gen.InvoicesConflictProblem) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, ptr(logoRefused(codeLogoTooLarge, "A logo is at most 512 KiB"))
		}
		return nil, ptr(logoRefused(codeLogoInvalid, "The request's body could not be read as a multipart form"))
	}
	var data []byte
	files := 0
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fail(err)
		}
		if part.FormName() != logoFormField {
			_ = part.Close()
			continue
		}
		files++
		body, err := io.ReadAll(io.LimitReader(part, maxLogoBytes+1))
		_ = part.Close()
		if err != nil {
			return fail(err)
		}
		if len(body) > maxLogoBytes {
			return nil, ptr(logoRefused(codeLogoTooLarge, "A logo is at most 512 KiB"))
		}
		data = body
	}
	switch {
	case files > 1:
		return nil, ptr(logoRefused(codeLogoInvalid, "A request sets one logo; this one carried more than one file"))
	case files == 0 || len(data) == 0:
		return nil, ptr(logoRefused(codeLogoInvalid, "A logo file is required, as the multipart part named 'file'"))
	}
	return data, nil
}

// judgeLogo is the type data is stored as, or its refusal: a PNG or JPEG by
// its own bytes that image.DecodeConfig — which reads the header alone, never
// decompressing, so a decompression bomb costs nothing — decodes as that same
// format, at most maxLogoLongerSide on its longer side and at least
// minLogoShorterSide on its shorter.
func judgeLogo(data []byte) (string, *gen.InvoicesConflictProblem) {
	contentType := http.DetectContentType(data)
	want, ok := logoFormats[contentType]
	if !ok {
		return "", ptr(logoRefused(codeLogoUnsupportedType, "A logo is a PNG or a JPEG"))
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != want.format {
		return "", ptr(logoRefused(codeLogoUnsupportedType, "A logo is a PNG or a JPEG"))
	}
	longer, shorter := max(cfg.Width, cfg.Height), min(cfg.Width, cfg.Height)
	switch {
	case longer > maxLogoLongerSide:
		return "", ptr(logoRefused(codeLogoTooWide, fmt.Sprintf("A logo is at most %d pixels on its longer side", maxLogoLongerSide)))
	case shorter < minLogoShorterSide:
		return "", ptr(logoRefused(codeLogoTooSmall, fmt.Sprintf("A logo is at least %d pixels on its shorter side", minLogoShorterSide)))
	}
	return contentType, nil
}

// logoKey is where a logo's bytes are stored: named by their hash, so the
// same logo uploaded twice is one object and a key never holds two.
func logoKey(sha, contentType string) string {
	return logoKeyPrefix + sha + "." + logoFormats[contentType].extension
}

// settingsLogo is the settings' logo for the wire, nil when none is set.
func settingsLogo(row store.InvoicesSetting) *gen.InvoicesSettingsLogo {
	if row.LogoObjectKey == nil || row.LogoSha256 == nil || row.LogoContentType == nil {
		return nil
	}
	return &gen.InvoicesSettingsLogo{ContentType: gen.InvoicesSettingsLogoContentType(*row.LogoContentType), Sha256: *row.LogoSha256}
}

// PutInvoicesSettingsLogo Set the seller's logo
// (PUT /api/v1/invoices/settings/logo)
//
// The file is judged first — nothing is stored for a refused one — then its
// metadata stripped (imagemeta.Strip; one that cannot be walked is
// logo_unsupported_type), the stripped bytes hashed and put in the store
// outside any transaction (503 storage_unavailable with nothing recorded),
// and only then named on the settings under their lock. An object the row
// no longer names is kept: a document's snapshot may name it (I1).
func (s *server) PutInvoicesSettingsLogo(ctx context.Context, req gen.PutInvoicesSettingsLogoRequestObject) (gen.PutInvoicesSettingsLogoResponseObject, error) {
	data, refusal := readLogo(req.Body)
	if refusal != nil {
		return gen.PutInvoicesSettingsLogo400ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	contentType, refusal := judgeLogo(data)
	if refusal != nil {
		return gen.PutInvoicesSettingsLogo400ApplicationProblemPlusJSONResponse(*refusal), nil
	}
	stripped, err := imagemeta.Strip(contentType, data)
	if err != nil {
		return gen.PutInvoicesSettingsLogo400ApplicationProblemPlusJSONResponse(logoRefused(codeLogoUnsupportedType, "A logo is a PNG or a JPEG")), nil
	}
	sum := sha256.Sum256(stripped)
	sha := hex.EncodeToString(sum[:])
	key := logoKey(sha, contentType)
	if !s.storageConfigured {
		return gen.PutInvoicesSettingsLogo503ApplicationProblemPlusJSONResponse(storageUnavailable(
			"This installation has no object store, so a logo cannot be stored. Nothing was recorded.")), nil
	}
	if err := s.objectPut(ctx, key, bytes.NewReader(stripped), contentType); err != nil {
		s.deps.Logger.ErrorContext(ctx, "invoices: a logo could not be written to the store", "error", err.Error())
		return gen.PutInvoicesSettingsLogo503ApplicationProblemPlusJSONResponse(storageUnavailable(
			"The logo could not be stored. Nothing was recorded; try again.")), nil
	}

	var saved store.InvoicesSetting
	err = s.withLockedTx(ctx, func(ctx context.Context, _ pgx.Tx, txq *store.Queries) error {
		if _, err := txq.LockSettings(ctx); err != nil {
			return fmt.Errorf("invoices: lock the settings: %w", err)
		}
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

// GetInvoicesSettingsLogo Get the seller's logo
// (GET /api/v1/invoices/settings/logo)
//
// The logo the settings name, read from the store outside any transaction:
// none set, or an object the store no longer holds, is the bare 404; a store
// that cannot be read is 503.
func (s *server) GetInvoicesSettingsLogo(ctx context.Context, _ gen.GetInvoicesSettingsLogoRequestObject) (gen.GetInvoicesSettingsLogoResponseObject, error) {
	row, err := store.New(s.deps.Pool).GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the settings: %w", err)
	}
	if row.LogoObjectKey == nil || row.LogoSha256 == nil || row.LogoContentType == nil {
		return gen.GetInvoicesSettingsLogo404Response{}, nil
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
// answers 503; an object the store does not hold wraps storage.ErrNotExist.
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
		return nil, "", fmt.Errorf("invoices: logo %s is not in the store: %w", *key, err)
	}
	if err != nil {
		return nil, "", fmt.Errorf("%w: read logo %s: %w", errObjectStore, *key, err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, maxLogoBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("%w: read logo %s: %w", errObjectStore, *key, err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != *sha {
		return nil, "", fmt.Errorf("invoices: logo %s does not hash to its recorded SHA-256", *key)
	}
	contentType := http.DetectContentType(data)
	if _, ok := logoFormats[contentType]; !ok {
		return nil, "", fmt.Errorf("invoices: logo %s is %s, neither a PNG nor a JPEG", *key, contentType)
	}
	return data, contentType, nil
}

// documentLogo is an issued document's logo, from its snapshot.
func (s *server) documentLogo(ctx context.Context, inv store.InvoicesInvoice) ([]byte, string, error) {
	return s.logoOf(ctx, inv.SellerLogoObjectKey, inv.SellerLogoSha256)
}

// logoAttachment is the logo as a mail's inline part, named so the HTML
// body's cid:logo resolves.
func logoAttachment(data []byte, contentType string) mail.Attachment {
	name := "logo." + logoFormats[contentType].extension
	return mail.Attachment{FileName: name, ContentType: contentType, Content: data, ContentID: logoContentID, Inline: true}
}
