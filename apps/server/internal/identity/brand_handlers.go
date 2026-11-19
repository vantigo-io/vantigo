package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// The brand operations of brands design B33 that B-A implements: the
// resolved brand, the public brand, the brands list, one brand, saving its
// draft, publishing it and discarding it. Logos are brand_logos.go, the
// stylesheet brand_css.go.

// Audit actions (B34).
const (
	auditBrandUpdated        = "brand.updated"
	auditBrandPublished      = "brand.published"
	auditBrandDraftDiscarded = "brand.draft_discarded"
)

// brandPublicTTL is how long an anonymous answer may be cached (B17).
const brandPublicTTL = 60 * time.Second

// loadBrand reads one brand, or reports that there is none.
func (s *server) loadBrand(ctx context.Context, q *store.Queries, id uuid.UUID) (brandRow, bool, error) {
	b, err := q.GetBrand(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return brandRow{}, false, nil
	}
	if err != nil {
		return brandRow{}, false, fmt.Errorf("identity: read a brand: %w", err)
	}
	row, err := loadBrandRow(b)
	return row, err == nil, err
}

// defaultBrand is the default brand. The first API mount creates it
// (brand_import.go); should it be missing all the same, it is created here, so
// resolution never has "no brand" (B5).
func (s *server) defaultBrand(ctx context.Context) (brandRow, error) {
	b, err := s.q.GetDefaultBrand(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := ensureDefaultBrand(ctx, s.deps, s.deps.Config.Branding); err != nil {
			return brandRow{}, err
		}
		b, err = s.q.GetDefaultBrand(ctx)
	}
	if err != nil {
		return brandRow{}, fmt.Errorf("identity: read the default brand: %w", err)
	}
	return loadBrandRow(b)
}

// holdsBrands reports whether the person holds identity:brands (an Owner does).
func (s *server) holdsBrands(ctx context.Context, userID uuid.UUID) (bool, error) {
	return s.access.UserHolds(ctx, userID, permBrands)
}

// brandJSONResponse answers a resolved brand with its validators.
type brandJSONResponse struct {
	payload      gen.ResolvedBrand
	etag         string
	cacheControl string
	public       bool
}

func (r brandJSONResponse) set(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", r.cacheControl)
	w.Header().Set("ETag", r.etag)
	w.Header().Add("Vary", "Cookie")
}

func (r brandJSONResponse) VisitGetIdentityBrandResponse(w http.ResponseWriter) error {
	r.set(w)
	return gen.GetIdentityBrand200JSONResponse(r.payload).VisitGetIdentityBrandResponse(w)
}

func (r brandJSONResponse) VisitGetIdentityBrandPublicResponse(w http.ResponseWriter) error {
	r.set(w)
	return gen.GetIdentityBrandPublic200JSONResponse(r.payload).VisitGetIdentityBrandPublicResponse(w)
}

// brandNotModified is a 304 with the validators of the payload it stands for.
type brandNotModified struct {
	etag         string
	cacheControl string
}

func (r brandNotModified) VisitGetIdentityBrandResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", r.cacheControl)
	w.Header().Set("ETag", r.etag)
	w.Header().Add("Vary", "Cookie")
	return gen.GetIdentityBrand304Response{}.VisitGetIdentityBrandResponse(w)
}

// GetIdentityBrand Get my brand (GET /api/v1/identity/brand)
func (s *server) GetIdentityBrand(ctx context.Context, req gen.GetIdentityBrandRequestObject) (gen.GetIdentityBrandResponseObject, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return nil, err
	}
	var payload gen.ResolvedBrand
	if req.Params.Preview != nil {
		ok, err := s.holdsBrands(ctx, p.UserID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return gen.GetIdentityBrand403JSONResponse(authErrorBody("forbidden", forbiddenMessage, nil)), nil
		}
		b, found, err := s.loadBrand(ctx, s.q, uuid.UUID(*req.Params.Preview))
		if err != nil {
			return nil, err
		}
		if !found {
			return gen.GetIdentityBrand404JSONResponse(authErrorBody("not_found", "There is no such brand.", nil)), nil
		}
		payload = s.resolvedBrand(b, b.editing(), b.draft != nil)
	} else {
		res, err := s.resolveUser(ctx, p.UserID)
		if err != nil {
			return nil, err
		}
		payload = s.resolvedFor(res)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("identity: marshal a brand: %w", err)
	}
	etag := etagOf(body)
	// Never stored by a shared cache: the answer is one person's brand, and a
	// CDN that ignores Vary: Cookie would serve it to the next.
	const cc = "private, no-store"
	if etagMatchesAny(req.Params.IfNoneMatch, etag) {
		return brandNotModified{etag: etag, cacheControl: cc}, nil
	}
	return brandJSONResponse{payload: payload, etag: etag, cacheControl: cc}, nil
}

// GetIdentityBrandPublic Get the public brand (GET /api/v1/identity/brand/public)
func (s *server) GetIdentityBrandPublic(ctx context.Context, _ gen.GetIdentityBrandPublicRequestObject) (gen.GetIdentityBrandPublicResponseObject, error) {
	b, v, err := s.resolve(ctx, uuid.Nil)
	if err != nil {
		return nil, err
	}
	payload := s.resolvedBrand(b, v, false)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("identity: marshal a brand: %w", err)
	}
	s.metrics.publicLookups.Add(ctx, 1)
	return brandJSONResponse{payload: payload, etag: etagOf(body), cacheControl: fmt.Sprintf("public, max-age=%d", int(brandPublicTTL.Seconds())), public: true}, nil
}

// GetIdentityBrands List the brands (GET /api/v1/identity/brands)
func (s *server) GetIdentityBrands(ctx context.Context, _ gen.GetIdentityBrandsRequestObject) (gen.GetIdentityBrandsResponseObject, error) {
	rows, err := s.q.ListBrands(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: list the brands: %w", err)
	}
	items := make([]gen.BrandSummary, 0, len(rows))
	for _, r := range rows {
		b, err := loadBrandRow(r)
		if err != nil {
			return nil, err
		}
		items = append(items, brandSummary(b))
	}
	return gen.GetIdentityBrands200JSONResponse{Items: items}, nil
}

// GetIdentityBrandsById Get a brand (GET /api/v1/identity/brands/{id})
func (s *server) GetIdentityBrandsById(ctx context.Context, req gen.GetIdentityBrandsByIdRequestObject) (gen.GetIdentityBrandsByIdResponseObject, error) {
	b, found, err := s.loadBrand(ctx, s.q, uuid.UUID(req.Id))
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.GetIdentityBrandsById404Response{}, nil
	}
	return gen.GetIdentityBrandsById200JSONResponse(s.brandResponse(b)), nil
}

// brandChange is what a write does to a locked brand: it answers the new
// draft, the new published version (publishing only), or a refusal.
type brandChange func(b brandRow) (draft *brandVersion, publish *brandVersion, refusal *brandRefusal, err error)

// brandOutcome is the result of changeBrand.
type brandOutcome struct {
	brand    brandRow
	found    bool
	refusal  *brandRefusal
	notFound bool
}

// changeBrand is every brand write: under the brand's row lock it compares
// If-Match (when ifMatch is not 0) with the revision, lets change decide,
// writes the draft or the publication, and audits it in the same transaction
// (B34). action names the audit event.
func (s *server) changeBrand(ctx context.Context, id uuid.UUID, ifMatch int32, action string, change brandChange, inTx ...func(ctx context.Context, tx pgx.Tx, q *store.Queries, now time.Time) error) (brandOutcome, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return brandOutcome{}, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return brandOutcome{}, err
	}
	now := s.deps.Clock()
	var out brandOutcome
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		raw, err := q.LockBrand(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			out.notFound = true
			return nil
		}
		if err != nil {
			return fmt.Errorf("identity: lock a brand: %w", err)
		}
		b, err := loadBrandRow(raw)
		if err != nil {
			return err
		}
		if ifMatch != 0 && b.Revision != ifMatch {
			out.refusal = staleRevision
			return nil
		}
		draft, publish, refusal, err := change(b)
		if err != nil {
			return err
		}
		if refusal != nil {
			out.refusal = refusal
			return nil
		}
		before := auditBrandState(b)
		userID := &p.UserID
		for _, f := range inTx {
			if err := f(ctx, tx, q, now); err != nil {
				return err
			}
		}
		switch {
		case publish != nil:
			err = q.PublishBrand(ctx, store.PublishBrandParams{
				Published: publish.marshal(), NameKey: nameKey(publish.Name), Now: now, UserID: userID, ID: id,
			})
			if err == nil && b.IsDefault {
				// An admin who publishes a logo of their own settles a logo
				// import that failed: the variables are ignored from the next
				// start (B30). A no-op when the import is already complete.
				if _, has := publish.Logos[logoSlotLight]; has {
					err = q.CompleteBrandImport(ctx, store.CompleteBrandImportParams{ID: id, Now: now})
				}
			}
		case draft != nil:
			err = q.SaveBrandDraft(ctx, store.SaveBrandDraftParams{
				Draft: draft.marshal(), NameKey: nameKey(draft.Name), Now: now, UserID: userID, ID: id,
			})
		default:
			key := nameKey(builtinBrandName)
			if b.pub != nil {
				key = nameKey(b.pub.Name)
			}
			err = q.DiscardBrandDraft(ctx, store.DiscardBrandDraftParams{NameKey: key, Now: now, UserID: userID, ID: id})
		}
		if err != nil {
			return fmt.Errorf("identity: write a brand: %w", err)
		}
		saved, err := q.GetBrand(ctx, id)
		if err != nil {
			return fmt.Errorf("identity: read the brand written: %w", err)
		}
		out.brand, err = loadBrandRow(saved)
		if err != nil {
			return err
		}
		out.found = true
		return writeAudit(ctx, q, r, now, auditEvent{
			actor: &p.UserID, action: action, before: before, after: auditBrandState(out.brand), mfa: p.MFAVerified,
		})
	})
	if db.IsUniqueViolation(err, "ux_brands_name_key") {
		return brandOutcome{refusal: nameTaken}, nil
	}
	return out, err
}

func nameKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// auditBrandState is what an audit row carries of a brand: names, versions and
// revisions, never a logo's bytes (B34).
func auditBrandState(b brandRow) map[string]any {
	m := map[string]any{"id": b.ID, "isDefault": b.IsDefault, "state": b.State, "publishedVersion": b.PublishedVersion, "revision": b.Revision}
	if b.pub != nil {
		m["published"] = map[string]any{"name": b.pub.Name, "title": b.pub.Title, "primary": b.pub.Primary, "radius": b.pub.Radius}
	}
	if b.draft != nil {
		m["draft"] = map[string]any{"name": b.draft.Name, "title": b.draft.Title, "primary": b.draft.Primary, "radius": b.draft.Radius, "logos": slotNames(b.draft.Logos)}
	}
	return m
}

func slotNames(logos map[string]brandLogo) []string {
	names := make([]string, 0, len(logos))
	for k := range logos {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

var errIfMatch = refuseBrand(http.StatusBadRequest, "invalid_request",
	"If-Match is the revision the brand was read at, such as 3.", map[string][]string{"If-Match": {"If-Match is the revision the brand was read at, such as 3."}})

// extraMembers refuses a body that carries members the contract does not list:
// tokens are never accepted (B37), shades and the derived set included.
func extraMembers(extra map[string]interface{}) *brandRefusal {
	if len(extra) == 0 {
		return nil
	}
	names := make([]string, 0, len(extra))
	for k := range extra {
		names = append(names, k)
	}
	sort.Strings(names)
	fields := map[string][]string{}
	for _, k := range names {
		fields[k] = []string{"A brand is saved from its inputs only; this member is not accepted. Colours are generated on the server."}
	}
	return refuseBrand(http.StatusBadRequest, codeBrandNotAccepted,
		"The request carries members a brand does not accept: "+strings.Join(names, ", ")+".", fields)
}

// PutIdentityBrandsById Save a brand's draft (PUT /api/v1/identity/brands/{id})
func (s *server) PutIdentityBrandsById(ctx context.Context, req gen.PutIdentityBrandsByIdRequestObject) (gen.PutIdentityBrandsByIdResponseObject, error) {
	reply := func(r *brandRefusal) (gen.PutIdentityBrandsByIdResponseObject, error) {
		switch r.status {
		case http.StatusBadRequest:
			return gen.PutIdentityBrandsById400JSONResponse(r.body()), nil
		case http.StatusConflict:
			return gen.PutIdentityBrandsById409JSONResponse(r.body()), nil
		default:
			return gen.PutIdentityBrandsById422JSONResponse(r.body()), nil
		}
	}
	rev, ok := parseRevision(req.Params.IfMatch)
	if !ok {
		return reply(errIfMatch)
	}
	in, refusal := brandInputOf(req.Body)
	if refusal != nil {
		return reply(refusal)
	}
	out, err := s.changeBrand(ctx, uuid.UUID(req.Id), rev, auditBrandUpdated, func(b brandRow) (*brandVersion, *brandVersion, *brandRefusal, error) {
		if b.State == "archived" {
			return nil, nil, errArchived, nil
		}
		v, refusal := newBrandVersion(in, b.editing().Logos)
		if refusal != nil {
			return nil, nil, refusal, nil
		}
		return &v, nil, nil, nil
	})
	switch {
	case err != nil:
		return nil, err
	case out.notFound:
		return gen.PutIdentityBrandsById404Response{}, nil
	case out.refusal != nil:
		return reply(out.refusal)
	}
	return gen.PutIdentityBrandsById200JSONResponse(s.brandResponse(out.brand)), nil
}

// PostIdentityBrandsByIdPublish Publish a brand's draft
// (POST /api/v1/identity/brands/{id}/publish)
func (s *server) PostIdentityBrandsByIdPublish(ctx context.Context, req gen.PostIdentityBrandsByIdPublishRequestObject) (gen.PostIdentityBrandsByIdPublishResponseObject, error) {
	reply := func(r *brandRefusal) (gen.PostIdentityBrandsByIdPublishResponseObject, error) {
		switch r.status {
		case http.StatusConflict:
			return gen.PostIdentityBrandsByIdPublish409JSONResponse(r.body()), nil
		default:
			return gen.PostIdentityBrandsByIdPublish422JSONResponse(r.body()), nil
		}
	}
	rev, ok := parseRevision(req.Params.IfMatch)
	if !ok {
		return gen.PostIdentityBrandsByIdPublish409JSONResponse(staleRevision.body()), nil
	}
	out, err := s.changeBrand(ctx, uuid.UUID(req.Id), rev, auditBrandPublished, func(b brandRow) (*brandVersion, *brandVersion, *brandRefusal, error) {
		if b.State == "archived" {
			return nil, nil, errArchived, nil
		}
		if b.draft == nil {
			return nil, nil, refuseBrand(http.StatusConflict, codeBrandNoDraft, "There is no pending draft to publish.", nil), nil
		}
		// B4: publish is refused while any colour pair is below AA.
		if failing := failingPairs(contrastChecks(*b.draft)); len(failing) > 0 {
			return nil, nil, refuseBrand(http.StatusUnprocessableEntity, codeBrandContrast,
				"Some colours are too close to their background to read: "+strings.Join(failing, ", ")+".",
				map[string][]string{"pairs": failing}), nil
		}
		// A logo the draft names must exist in the registry (B6).
		for slot, l := range b.draft.Logos {
			if _, err := s.q.GetBrandLogo(ctx, l.SHA256); err != nil {
				return nil, nil, nil, fmt.Errorf("identity: the draft's %s logo %s is not registered: %w", slot, l.SHA256, err)
			}
		}
		pub := *b.draft
		return nil, &pub, nil, nil
	}, func(ctx context.Context, tx pgx.Tx, _ *store.Queries, _ time.Time) error {
		return notifyBrands(ctx, tx, eventBrandChanged) // B18: heard only if the publish commits
	})
	switch {
	case err != nil:
		return nil, err
	case out.notFound:
		return gen.PostIdentityBrandsByIdPublish404Response{}, nil
	case out.refusal != nil:
		return reply(out.refusal)
	}
	s.metrics.publishes.Add(ctx, 1)
	s.deps.Logger.InfoContext(ctx, "identity: a brand was published", "brand_id", out.brand.ID, "version", out.brand.PublishedVersion)
	return gen.PostIdentityBrandsByIdPublish200JSONResponse(s.brandResponse(out.brand)), nil
}

// PostIdentityBrandsByIdDiscardDraft Discard a brand's draft
// (POST /api/v1/identity/brands/{id}/discard-draft)
func (s *server) PostIdentityBrandsByIdDiscardDraft(ctx context.Context, req gen.PostIdentityBrandsByIdDiscardDraftRequestObject) (gen.PostIdentityBrandsByIdDiscardDraftResponseObject, error) {
	rev, ok := parseRevision(req.Params.IfMatch)
	if !ok {
		return gen.PostIdentityBrandsByIdDiscardDraft409JSONResponse(staleRevision.body()), nil
	}
	out, err := s.changeBrand(ctx, uuid.UUID(req.Id), rev, auditBrandDraftDiscarded, func(b brandRow) (*brandVersion, *brandVersion, *brandRefusal, error) {
		if b.State == "archived" {
			return nil, nil, errArchived, nil
		}
		if b.draft == nil {
			return nil, nil, refuseBrand(http.StatusConflict, codeBrandNoDraft, "There is no pending draft to discard.", nil), nil
		}
		if b.pub == nil {
			return nil, nil, refuseBrand(http.StatusConflict, "never_published", "A brand that was never published has nothing to fall back to.", nil), nil
		}
		return nil, nil, nil, nil
	})
	switch {
	case err != nil:
		return nil, err
	case out.notFound:
		return gen.PostIdentityBrandsByIdDiscardDraft404Response{}, nil
	case out.refusal != nil:
		return gen.PostIdentityBrandsByIdDiscardDraft409JSONResponse(out.refusal.body()), nil
	}
	return gen.PostIdentityBrandsByIdDiscardDraft200JSONResponse(s.brandResponse(out.brand)), nil
}

// callerIsSession reports whether ctx's principal is a signed-in person.
func callerIsSession(ctx context.Context) (contracts.Principal, bool) {
	p, ok := contracts.PrincipalFrom(ctx)
	return p, ok && p.UserID != uuid.Nil && p.SessionID != uuid.Nil
}
