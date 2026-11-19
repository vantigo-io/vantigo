package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/logoimage"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// Brand logos (brands design B6, B15): the same validation as the seller's
// logo on invoices (internal/logoimage), stored content-addressed in
// identity's scope of the object store as brands/logo-{sha256}.{png|jpg} and
// never deleted, because a document may snapshot one. A logo PUT writes the
// draft; the published version's logos are served by the anonymous route, the
// draft's only to a holder of identity:brands.

// brandStorageScope is identity's namespace in the object store.
const brandStorageScope = "identity"

const (
	brandLogoKeyPrefix = "brands/logo-"
	codeLogoNotSquare  = "logo_not_square"
	codeStorageOff     = "storage_unavailable"

	// brandAssetCacheControl is for a file named by the hash of its bytes: it
	// can never change.
	brandAssetCacheControl = "public, max-age=31536000, immutable"
	brandLogoCacheControl  = "private, max-age=0, must-revalidate"
)

// brandObjects is the object store brand logos live in, scoped to identity.
type brandObjects struct {
	store      storage.ObjectStore
	configured bool
}

// newBrandObjects builds the store a brand's logos use from d: the harness's
// ObjectStore when one is set, else the configured provider.
func newBrandObjects(d module.Deps) (brandObjects, error) {
	cfg := d.Config
	if cfg == nil {
		cfg = &config.Config{}
	}
	if d.ObjectStore != nil {
		return brandObjects{store: d.ObjectStore, configured: true}, nil
	}
	base, err := storage.New(cfg)
	if err != nil {
		return brandObjects{}, fmt.Errorf("identity: build the object store: %w", err)
	}
	scoped, err := storage.NewScope(base, brandStorageScope)
	if err != nil {
		return brandObjects{}, fmt.Errorf("identity: scope the object store: %w", err)
	}
	return brandObjects{store: scoped, configured: cfg.StorageProvider != ""}, nil
}

func brandLogoKey(sha, contentType string) string {
	return brandLogoKeyPrefix + sha + "." + logoimage.Extension(contentType)
}

// refusedLogo is a shared refusal as this module's 400.
func logoRefusal(r *logoimage.Refusal) *brandRefusal {
	return refuseBrand(http.StatusBadRequest, r.Code, r.Detail, nil)
}

var storageOffRefusal = refuseBrand(http.StatusServiceUnavailable, codeStorageOff,
	"This installation has no object store, so a logo cannot be stored. Nothing was recorded.", nil)

// storeLogo judges, normalises and stores data (B6), and registers the asset.
// It writes the object before any transaction. The slot decides one more rule:
// the icon is square within 5 %.
func (s *server) storeLogo(ctx context.Context, slot string, data []byte) (brandLogo, *brandRefusal, error) {
	return storeBrandLogo(ctx, s.brandObjects, slot, data)
}

func storeBrandLogo(ctx context.Context, objects brandObjects, slot string, data []byte) (brandLogo, *brandRefusal, error) {
	info, refusal := logoimage.Judge(data)
	if refusal != nil {
		return brandLogo{}, logoRefusal(refusal), nil
	}
	if slot == "icon" && !info.IsSquare() {
		return brandLogo{}, refuseBrand(http.StatusBadRequest, codeLogoNotSquare, "The icon must be square, within 5 %.", nil), nil
	}
	stored, refusal := logoimage.Normalise(data, info.ContentType)
	if refusal != nil {
		return brandLogo{}, logoRefusal(refusal), nil
	}
	if !objects.configured {
		return brandLogo{}, storageOffRefusal, nil
	}
	sum := sha256.Sum256(stored)
	sha := hex.EncodeToString(sum[:])
	key := brandLogoKey(sha, info.ContentType)
	if err := objects.store.Put(ctx, key, bytes.NewReader(stored), info.ContentType); err != nil {
		return brandLogo{}, refuseBrand(http.StatusServiceUnavailable, codeStorageOff, "The logo could not be stored. Nothing was recorded; try again.", nil),
			fmt.Errorf("identity: write a brand logo: %w", err)
	}
	return brandLogo{SHA256: sha, ContentType: info.ContentType}, nil, nil
}

// slotOf validates the slot path parameter.
func slotOf(v string) (string, bool) { return v, validSlot(v) }

// registerLogo records the asset in the registry, in the changing transaction.
func registerLogo(ctx context.Context, q *store.Queries, l brandLogo, now time.Time) error {
	return q.InsertBrandLogo(ctx, store.InsertBrandLogoParams{
		Sha256: l.SHA256, ContentType: l.ContentType, ObjectKey: brandLogoKey(l.SHA256, l.ContentType), Now: now,
	})
}

// PutIdentityBrandsByIdLogosBySlot Set a brand logo in the draft
// (PUT /api/v1/identity/brands/{id}/logos/{slot})
func (s *server) PutIdentityBrandsByIdLogosBySlot(ctx context.Context, req gen.PutIdentityBrandsByIdLogosBySlotRequestObject) (gen.PutIdentityBrandsByIdLogosBySlotResponseObject, error) {
	reply := func(r *brandRefusal) (gen.PutIdentityBrandsByIdLogosBySlotResponseObject, error) {
		if r.status == http.StatusServiceUnavailable {
			return gen.PutIdentityBrandsByIdLogosBySlot503JSONResponse(r.body()), nil
		}
		return gen.PutIdentityBrandsByIdLogosBySlot400JSONResponse(r.body()), nil
	}
	slot, ok := slotOf(string(req.Slot))
	if !ok {
		return gen.PutIdentityBrandsByIdLogosBySlot404Response{}, nil
	}
	if _, found, err := s.loadBrand(ctx, s.q, uuid.UUID(req.Id)); err != nil {
		return nil, err
	} else if !found {
		return gen.PutIdentityBrandsByIdLogosBySlot404Response{}, nil
	}
	data, refusal := logoimage.Read(req.Body)
	if refusal != nil {
		return reply(logoRefusal(refusal))
	}
	logo, bad, err := s.storeLogo(ctx, slot, data)
	if bad != nil {
		if err != nil {
			s.deps.Logger.ErrorContext(ctx, "identity: a brand logo could not be written to the store", "error", err.Error())
		}
		return reply(bad)
	}
	if err != nil {
		return nil, err
	}
	out, err := s.changeBrandWithLogo(ctx, uuid.UUID(req.Id), "brand.logo_set", slot, &logo)
	switch {
	case err != nil:
		return nil, err
	case out.notFound:
		return gen.PutIdentityBrandsByIdLogosBySlot404Response{}, nil
	case out.refusal != nil:
		return reply(out.refusal)
	}
	return gen.PutIdentityBrandsByIdLogosBySlot200JSONResponse(s.brandResponse(out.brand)), nil
}

// DeleteIdentityBrandsByIdLogosBySlot Remove a brand logo from the draft
// (DELETE /api/v1/identity/brands/{id}/logos/{slot})
func (s *server) DeleteIdentityBrandsByIdLogosBySlot(ctx context.Context, req gen.DeleteIdentityBrandsByIdLogosBySlotRequestObject) (gen.DeleteIdentityBrandsByIdLogosBySlotResponseObject, error) {
	slot, ok := slotOf(string(req.Slot))
	if !ok {
		return gen.DeleteIdentityBrandsByIdLogosBySlot404Response{}, nil
	}
	out, err := s.changeBrandWithLogo(ctx, uuid.UUID(req.Id), "brand.logo_removed", slot, nil)
	switch {
	case err != nil:
		return nil, err
	case out.notFound, out.refusal != nil: // an archived brand's logos are not edited
		return gen.DeleteIdentityBrandsByIdLogosBySlot404Response{}, nil
	}
	return gen.DeleteIdentityBrandsByIdLogosBySlot200JSONResponse(s.brandResponse(out.brand)), nil
}

// changeBrandWithLogo puts logo (or none) in slot of the brand's draft, the
// asset registered in the same transaction. The published version is never
// touched (B6).
func (s *server) changeBrandWithLogo(ctx context.Context, id uuid.UUID, action, slot string, logo *brandLogo) (brandOutcome, error) {
	return s.changeBrand(ctx, id, 0, action, func(b brandRow) (*brandVersion, *brandVersion, *brandRefusal, error) {
		if b.State == "archived" {
			return nil, nil, errArchived, nil
		}
		v := b.editing()
		logos := make(map[string]brandLogo, len(v.Logos)+1)
		for k, l := range v.Logos {
			logos[k] = l
		}
		if logo == nil {
			delete(logos, slot)
		} else {
			logos[slot] = *logo
		}
		v.Logos = logos
		return &v, nil, nil, nil
	}, func(ctx context.Context, _ pgx.Tx, q *store.Queries, now time.Time) error {
		if logo == nil {
			return nil
		}
		return registerLogo(ctx, q, *logo, now)
	})
}

// readLogoObject reads a registered logo from the store whole, verifying that
// its bytes hash to its name.
func (s *server) readLogoObject(ctx context.Context, sha string) ([]byte, string, error) {
	row, err := s.q.GetBrandLogo(ctx, sha)
	if err != nil {
		return nil, "", err
	}
	return readBrandObject(ctx, s.brandObjects, row)
}

func readBrandObject(ctx context.Context, objects brandObjects, row store.IdentityBrandLogo) ([]byte, string, error) {
	if !objects.configured {
		return nil, "", errBrandStorageOff
	}
	rc, err := objects.store.Get(ctx, row.ObjectKey)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, logoimage.MaxStoredBytes+1))
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != row.Sha256 {
		return nil, "", fmt.Errorf("identity: brand logo %s does not hash to its name", row.ObjectKey)
	}
	return data, row.ContentType, nil
}

var errBrandStorageOff = errors.New("identity: no object store for brand logos")

// logoDownload sets a served logo's validators before the generated response
// writes its type, length and bytes; never sniffed.
type logoDownload struct {
	sha          string
	cacheControl string
	visit        func(w http.ResponseWriter) error
}

func (l logoDownload) set(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", l.cacheControl)
	w.Header().Set("ETag", `"`+l.sha+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

type brandLogoResponse struct{ logoDownload }

func (r brandLogoResponse) VisitGetIdentityBrandsByIdLogosBySlotResponse(w http.ResponseWriter) error {
	r.set(w)
	return r.visit(w)
}

type brandAssetResponse struct{ logoDownload }

func (r brandAssetResponse) VisitGetIdentityBrandAssetsByFileResponse(w http.ResponseWriter) error {
	r.set(w)
	return r.visit(w)
}

type brandAssetNotModified struct{ logoDownload }

func (r brandAssetNotModified) VisitGetIdentityBrandAssetsByFileResponse(w http.ResponseWriter) error {
	r.set(w)
	return gen.GetIdentityBrandAssetsByFile304Response{}.VisitGetIdentityBrandAssetsByFileResponse(w)
}

// GetIdentityBrandsByIdLogosBySlot Get a brand logo
// (GET /api/v1/identity/brands/{id}/logos/{slot})
func (s *server) GetIdentityBrandsByIdLogosBySlot(ctx context.Context, req gen.GetIdentityBrandsByIdLogosBySlotRequestObject) (gen.GetIdentityBrandsByIdLogosBySlotResponseObject, error) {
	slot, ok := slotOf(string(req.Slot))
	if !ok {
		return gen.GetIdentityBrandsByIdLogosBySlot404Response{}, nil
	}
	b, found, err := s.loadBrand(ctx, s.q, uuid.UUID(req.Id))
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.GetIdentityBrandsByIdLogosBySlot404Response{}, nil
	}
	v := b.pub
	if req.Params.Draft != nil && *req.Params.Draft {
		e := b.editing()
		v = &e
	}
	if v == nil {
		return gen.GetIdentityBrandsByIdLogosBySlot404Response{}, nil
	}
	l, has := v.Logos[slot]
	if !has {
		return gen.GetIdentityBrandsByIdLogosBySlot404Response{}, nil
	}
	data, contentType, err := s.readLogoObject(ctx, l.SHA256)
	switch {
	case errors.Is(err, errBrandStorageOff), errors.Is(err, storage.ErrNotExist), errors.Is(err, pgx.ErrNoRows):
		if errors.Is(err, errBrandStorageOff) {
			return gen.GetIdentityBrandsByIdLogosBySlot503JSONResponse(storageOffRefusal.body()), nil
		}
		return gen.GetIdentityBrandsByIdLogosBySlot404Response{}, nil
	case err != nil:
		s.deps.Logger.ErrorContext(ctx, "identity: a brand logo could not be read from the store", "error", err.Error())
		return gen.GetIdentityBrandsByIdLogosBySlot503JSONResponse(authErrorBody(codeStorageOff, "The logo could not be read from the store; try again.", nil)), nil
	}
	length := int64(len(data))
	dl := logoDownload{sha: l.SHA256, cacheControl: brandLogoCacheControl}
	if contentType == logoimage.ContentTypeJPEG {
		dl.visit = gen.GetIdentityBrandsByIdLogosBySlot200ImagejpegResponse{Body: bytes.NewReader(data), ContentLength: length}.VisitGetIdentityBrandsByIdLogosBySlotResponse
	} else {
		dl.visit = gen.GetIdentityBrandsByIdLogosBySlot200ImagepngResponse{Body: bytes.NewReader(data), ContentLength: length}.VisitGetIdentityBrandsByIdLogosBySlotResponse
	}
	return brandLogoResponse{dl}, nil
}

var assetFile = regexp.MustCompile(`^([0-9a-f]{64})\.(png|jpg)$`)

// GetIdentityBrandAssetsByFile Get a published brand logo
// (GET /api/v1/identity/brand-assets/{file})
//
// Anonymous and content-addressed (B15): it answers only for an asset a
// published brand names, so it is not a way to read an unpublished draft's
// logo; anything else is the same bare 404.
func (s *server) GetIdentityBrandAssetsByFile(ctx context.Context, req gen.GetIdentityBrandAssetsByFileRequestObject) (gen.GetIdentityBrandAssetsByFileResponseObject, error) {
	m := assetFile.FindStringSubmatch(strings.ToLower(req.File))
	if m == nil {
		return gen.GetIdentityBrandAssetsByFile404Response{}, nil
	}
	sha, ext := m[1], m[2]
	published, err := s.q.BrandLogoIsPublished(ctx, sha)
	if err != nil {
		return nil, fmt.Errorf("identity: is a brand logo published: %w", err)
	}
	if !published {
		// A campaign's logo is served while the campaign is in force (B22).
		if published, err = s.campaignLogoInForce(ctx, sha); err != nil {
			return nil, err
		}
	}
	if !published {
		return gen.GetIdentityBrandAssetsByFile404Response{}, nil
	}
	row, err := s.q.GetBrandLogo(ctx, sha)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetIdentityBrandAssetsByFile404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: read a brand logo's record: %w", err)
	}
	if (ext == "jpg") != (row.ContentType == logoimage.ContentTypeJPEG) {
		return gen.GetIdentityBrandAssetsByFile404Response{}, nil
	}
	// The name is the hash of the bytes, so a client that holds the ETag holds
	// the asset: answer before the object store is read.
	if etagMatchesAny(req.Params.IfNoneMatch, `"`+sha+`"`) {
		return brandAssetNotModified{logoDownload{sha: sha, cacheControl: brandAssetCacheControl}}, nil
	}
	data, contentType, err := readBrandObject(ctx, s.brandObjects, row)
	if err != nil {
		if !errors.Is(err, storage.ErrNotExist) && !errors.Is(err, errBrandStorageOff) {
			s.deps.Logger.ErrorContext(ctx, "identity: a brand asset could not be read from the store", "error", err.Error())
		}
		return gen.GetIdentityBrandAssetsByFile404Response{}, nil
	}
	length := int64(len(data))
	dl := logoDownload{sha: sha, cacheControl: brandAssetCacheControl}
	if contentType == logoimage.ContentTypeJPEG {
		dl.visit = gen.GetIdentityBrandAssetsByFile200ImagejpegResponse{Body: bytes.NewReader(data), ContentLength: length}.VisitGetIdentityBrandAssetsByFileResponse
	} else {
		dl.visit = gen.GetIdentityBrandAssetsByFile200ImagepngResponse{Body: bytes.NewReader(data), ContentLength: length}.VisitGetIdentityBrandAssetsByFileResponse
	}
	return brandAssetResponse{dl}, nil
}
