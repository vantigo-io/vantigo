package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/identity/gen"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
)

// The life of a brand beyond its draft (brands design B21, B33): create,
// duplicate, archive, restore, make default and delete. Each is a write under
// the brand's row lock, audited in the same transaction (B34), and each that
// changes what people see tells the open sessions (B18).

// Audit actions (B34).
const (
	auditBrandCreated        = "brand.created"
	auditBrandArchived       = "brand.archived"
	auditBrandRestored       = "brand.restored"
	auditBrandDeleted        = "brand.deleted"
	auditBrandDefaultChanged = "brand.default_changed"
)

const (
	codeBrandInUse         = "brand_in_use"
	codeBrandNameTaken     = "brand_name_taken"
	codeBrandNotPublished  = "brand_not_published"
	codeBrandNotArchived   = "not_archived"
	codeBrandAlreadyStored = "already_archived"
)

var nameTaken = refuseBrand(http.StatusConflict, codeBrandNameTaken,
	"Another brand already has that name.", map[string][]string{"name": {"Another brand already has that name."}})

// brandCall is who is writing, and when.
type brandCall struct {
	p   contracts.Principal
	r   *http.Request
	now time.Time
	tx  pgx.Tx // the transaction of the write, for live.Notify
}

func (s *server) brandCall(ctx context.Context) (brandCall, error) {
	p, err := callerFrom(ctx)
	if err != nil {
		return brandCall{}, err
	}
	r, err := requestFrom(ctx)
	if err != nil {
		return brandCall{}, err
	}
	return brandCall{p: p, r: r, now: s.deps.Clock()}, nil
}

// brandWrite is the change a lifecycle operation makes to a locked brand: it
// answers a refusal, or writes through q.
type brandWrite func(ctx context.Context, q *store.Queries, b brandRow, c brandCall) (*brandRefusal, error)

// lifecycle runs write on brand id under its row lock, after comparing If-Match
// with the revision, then reads the brand back and audits action.
func (s *server) lifecycle(ctx context.Context, id uuid.UUID, ifMatch int32, action string, write brandWrite) (brandOutcome, error) {
	c, err := s.brandCall(ctx)
	if err != nil {
		return brandOutcome{}, err
	}
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
		before := auditBrandState(b)
		c.tx = tx
		refusal, err := write(ctx, q, b, c)
		if err != nil {
			return err
		}
		if refusal != nil {
			out.refusal = refusal
			return nil
		}
		saved, err := q.GetBrand(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			out.found = true // deleted by write
			return writeAudit(ctx, q, c.r, c.now, auditEvent{
				actor: &c.p.UserID, action: action, before: before, after: struct{}{}, mfa: c.p.MFAVerified,
			})
		}
		if err != nil {
			return fmt.Errorf("identity: read the brand written: %w", err)
		}
		out.brand, err = loadBrandRow(saved)
		if err != nil {
			return err
		}
		out.found = true
		return writeAudit(ctx, q, c.r, c.now, auditEvent{
			actor: &c.p.UserID, action: action, before: before, after: auditBrandState(out.brand), mfa: c.p.MFAVerified,
		})
	})
	if db.IsUniqueViolation(err, "ux_brands_name_key") {
		return brandOutcome{refusal: nameTaken}, nil
	}
	return out, err
}

// removeRulesOf drops a brand's rules (it is archived or deleted), moving the
// list's revision and auditing the change when there were any.
func removeRulesOf(ctx context.Context, q *store.Queries, brandID uuid.UUID, c brandCall) error {
	n, err := q.DeleteBrandRulesOfBrand(ctx, brandID)
	if err != nil {
		return fmt.Errorf("identity: remove a brand's rules: %w", err)
	}
	if n == 0 {
		return nil
	}
	if _, err := q.BumpBrandRulesRevision(ctx); err != nil {
		return fmt.Errorf("identity: move the brand rules' revision: %w", err)
	}
	return writeAudit(ctx, q, c.r, c.now, auditEvent{
		actor: &c.p.UserID, action: auditBrandRulesChanged, mfa: c.p.MFAVerified,
		before: map[string]any{"removedForBrand": brandID, "rules": n}, after: map[string]any{"removedForBrand": brandID},
	})
}

// brandInputOf reads the inputs of a create or an update request: inputs
// only (B37), a member the contract does not list refused.
func brandInputOf(body *gen.BrandUpdateRequest) (brandInput, *brandRefusal) {
	if body == nil {
		return brandInput{}, refuseBrand(http.StatusBadRequest, codeBrandNotAccepted, "The request needs a body.", nil)
	}
	if refusal := extraMembers(body.AdditionalProperties); refusal != nil {
		return brandInput{}, refusal
	}
	in := brandInput{Name: body.Name, Primary: body.Primary, Radius: string(body.Radius)}
	if body.Title != nil {
		in.Title = *body.Title
	}
	if body.Support != nil {
		in.Support = brandSupport{Email: deref(body.Support.Email), Phone: deref(body.Support.Phone), URL: deref(body.Support.Url)}
	}
	return in, nil
}

// PostIdentityBrands Create a brand (POST /api/v1/identity/brands)
func (s *server) PostIdentityBrands(ctx context.Context, req gen.PostIdentityBrandsRequestObject) (gen.PostIdentityBrandsResponseObject, error) {
	reply := func(r *brandRefusal) (gen.PostIdentityBrandsResponseObject, error) {
		switch r.status {
		case http.StatusBadRequest:
			return gen.PostIdentityBrands400JSONResponse(r.body()), nil
		case http.StatusConflict:
			return gen.PostIdentityBrands409JSONResponse(r.body()), nil
		default:
			return gen.PostIdentityBrands422JSONResponse(r.body()), nil
		}
	}
	in, refusal := brandInputOf(req.Body)
	if refusal != nil {
		return reply(refusal)
	}
	v, refusal := newBrandVersion(in, nil)
	if refusal != nil {
		return reply(refusal)
	}
	out, err := s.createDraftBrand(ctx, v)
	switch {
	case err != nil:
		return nil, err
	case out.refusal != nil:
		return reply(out.refusal)
	}
	return gen.PostIdentityBrands201JSONResponse(s.brandResponse(out.brand)), nil
}

// createDraftBrand inserts a brand that was never published, with v as its
// draft, and audits brand.created.
func (s *server) createDraftBrand(ctx context.Context, v brandVersion) (brandOutcome, error) {
	c, err := s.brandCall(ctx)
	if err != nil {
		return brandOutcome{}, err
	}
	var out brandOutcome
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := store.New(tx)
		id, err := q.InsertDraftBrand(ctx, store.InsertDraftBrandParams{
			Draft: v.marshal(), NameKey: nameKey(v.Name), Now: c.now, UserID: &c.p.UserID,
		})
		if err != nil {
			return err
		}
		saved, err := q.GetBrand(ctx, id)
		if err != nil {
			return fmt.Errorf("identity: read the brand created: %w", err)
		}
		out.brand, err = loadBrandRow(saved)
		if err != nil {
			return err
		}
		out.found = true
		return writeAudit(ctx, q, c.r, c.now, auditEvent{
			actor: &c.p.UserID, action: auditBrandCreated, before: struct{}{}, after: auditBrandState(out.brand), mfa: c.p.MFAVerified,
		})
	})
	if db.IsUniqueViolation(err, "ux_brands_name_key") {
		return brandOutcome{refusal: nameTaken}, nil
	}
	return out, err
}

// PostIdentityBrandsByIdDuplicate Duplicate a brand
// (POST /api/v1/identity/brands/{id}/duplicate)
func (s *server) PostIdentityBrandsByIdDuplicate(ctx context.Context, req gen.PostIdentityBrandsByIdDuplicateRequestObject) (gen.PostIdentityBrandsByIdDuplicateResponseObject, error) {
	rev, ok := parseRevision(req.Params.IfMatch)
	if !ok {
		return gen.PostIdentityBrandsByIdDuplicate409JSONResponse(staleRevision.body()), nil
	}
	src, found, err := s.loadBrand(ctx, s.q, uuid.UUID(req.Id))
	if err != nil {
		return nil, err
	}
	if !found {
		return gen.PostIdentityBrandsByIdDuplicate404Response{}, nil
	}
	if src.Revision != rev {
		return gen.PostIdentityBrandsByIdDuplicate409JSONResponse(staleRevision.body()), nil
	}
	v := src.editing()
	name, err := s.freeCopyName(ctx, v.Name)
	if err != nil {
		return nil, err
	}
	v.Name = name
	out, err := s.createDraftBrand(ctx, v)
	switch {
	case err != nil:
		return nil, err
	case out.refusal != nil:
		return gen.PostIdentityBrandsByIdDuplicate409JSONResponse(out.refusal.body()), nil
	}
	return gen.PostIdentityBrandsByIdDuplicate201JSONResponse(s.brandResponse(out.brand)), nil
}

// freeCopyName is "<name> (copy)", or "(copy 2)" and so on, the first that no
// brand has, within the name's length.
func (s *server) freeCopyName(ctx context.Context, name string) (string, error) {
	for n := 1; n < 100; n++ {
		suffix := " (copy)"
		if n > 1 {
			suffix = fmt.Sprintf(" (copy %d)", n)
		}
		base := name
		for utf8.RuneCountInString(base+suffix) > brandNameMax {
			base = strings.TrimSpace(string([]rune(base)[:utf8.RuneCountInString(base)-1]))
		}
		taken, err := s.q.BrandNameKeyTaken(ctx, nameKey(base+suffix))
		if err != nil {
			return "", fmt.Errorf("identity: check a brand name: %w", err)
		}
		if !taken {
			return base + suffix, nil
		}
	}
	return "", errors.New("identity: no free copy name for a brand")
}

// PostIdentityBrandsByIdArchive Archive a brand
// (POST /api/v1/identity/brands/{id}/archive)
func (s *server) PostIdentityBrandsByIdArchive(ctx context.Context, req gen.PostIdentityBrandsByIdArchiveRequestObject) (gen.PostIdentityBrandsByIdArchiveResponseObject, error) {
	rev, ok := parseRevision(req.Params.IfMatch)
	if !ok {
		return gen.PostIdentityBrandsByIdArchive409JSONResponse(staleRevision.body()), nil
	}
	out, err := s.lifecycle(ctx, uuid.UUID(req.Id), rev, auditBrandArchived, func(ctx context.Context, q *store.Queries, b brandRow, c brandCall) (*brandRefusal, error) {
		switch {
		case b.IsDefault:
			return refuseBrand(http.StatusConflict, codeBrandInUse, "The default brand cannot be archived; make another brand the default first.", nil), nil
		case b.State == "archived":
			return refuseBrand(http.StatusConflict, codeBrandAlreadyStored, "The brand is already archived.", nil), nil
		}
		if err := q.SetBrandState(ctx, store.SetBrandStateParams{State: "archived", Now: c.now, UserID: &c.p.UserID, ID: b.ID}); err != nil {
			return nil, fmt.Errorf("identity: archive a brand: %w", err)
		}
		// An archived brand cannot be assigned or chosen (B21).
		if err := removeRulesOf(ctx, q, b.ID, c); err != nil {
			return nil, err
		}
		if _, err := q.ClearUserPreferredBrandOf(ctx, &b.ID); err != nil {
			return nil, fmt.Errorf("identity: clear the choice of an archived brand: %w", err)
		}
		return nil, notifyBrands(ctx, c.tx, eventBrandChanged)
	})
	switch {
	case err != nil:
		return nil, err
	case out.notFound:
		return gen.PostIdentityBrandsByIdArchive404Response{}, nil
	case out.refusal != nil:
		return gen.PostIdentityBrandsByIdArchive409JSONResponse(out.refusal.body()), nil
	}
	return gen.PostIdentityBrandsByIdArchive200JSONResponse(s.brandResponse(out.brand)), nil
}

// PostIdentityBrandsByIdRestore Restore a brand
// (POST /api/v1/identity/brands/{id}/restore)
func (s *server) PostIdentityBrandsByIdRestore(ctx context.Context, req gen.PostIdentityBrandsByIdRestoreRequestObject) (gen.PostIdentityBrandsByIdRestoreResponseObject, error) {
	rev, ok := parseRevision(req.Params.IfMatch)
	if !ok {
		return gen.PostIdentityBrandsByIdRestore409JSONResponse(staleRevision.body()), nil
	}
	out, err := s.lifecycle(ctx, uuid.UUID(req.Id), rev, auditBrandRestored, func(ctx context.Context, q *store.Queries, b brandRow, c brandCall) (*brandRefusal, error) {
		if b.State != "archived" {
			return refuseBrand(http.StatusConflict, codeBrandNotArchived, "The brand is not archived.", nil), nil
		}
		state := "draft"
		if b.pub != nil {
			state = "published"
		}
		if err := q.SetBrandState(ctx, store.SetBrandStateParams{State: state, Now: c.now, UserID: &c.p.UserID, ID: b.ID}); err != nil {
			return nil, fmt.Errorf("identity: restore a brand: %w", err)
		}
		return nil, notifyBrands(ctx, c.tx, eventBrandChanged)
	})
	switch {
	case err != nil:
		return nil, err
	case out.notFound:
		return gen.PostIdentityBrandsByIdRestore404Response{}, nil
	case out.refusal != nil:
		return gen.PostIdentityBrandsByIdRestore409JSONResponse(out.refusal.body()), nil
	}
	return gen.PostIdentityBrandsByIdRestore200JSONResponse(s.brandResponse(out.brand)), nil
}

// PostIdentityBrandsByIdMakeDefault Make a brand the default
// (POST /api/v1/identity/brands/{id}/make-default)
func (s *server) PostIdentityBrandsByIdMakeDefault(ctx context.Context, req gen.PostIdentityBrandsByIdMakeDefaultRequestObject) (gen.PostIdentityBrandsByIdMakeDefaultResponseObject, error) {
	rev, ok := parseRevision(req.Params.IfMatch)
	if !ok {
		return gen.PostIdentityBrandsByIdMakeDefault409JSONResponse(staleRevision.body()), nil
	}
	out, err := s.lifecycle(ctx, uuid.UUID(req.Id), rev, auditBrandDefaultChanged, func(ctx context.Context, q *store.Queries, b brandRow, c brandCall) (*brandRefusal, error) {
		if b.State != "published" {
			return refuseBrand(http.StatusConflict, codeBrandNotPublished, "Only a published brand can be the default.", nil), nil
		}
		if b.IsDefault {
			return nil, nil
		}
		// The unique index allows one default: clear it before setting the next.
		if err := q.ClearDefaultBrand(ctx, store.ClearDefaultBrandParams{Now: c.now, UserID: &c.p.UserID}); err != nil {
			return nil, fmt.Errorf("identity: clear the default brand: %w", err)
		}
		if err := q.SetDefaultBrand(ctx, store.SetDefaultBrandParams{Now: c.now, UserID: &c.p.UserID, ID: b.ID}); err != nil {
			return nil, fmt.Errorf("identity: set the default brand: %w", err)
		}
		return nil, notifyBrands(ctx, c.tx, eventBrandChanged)
	})
	switch {
	case err != nil:
		return nil, err
	case out.notFound:
		return gen.PostIdentityBrandsByIdMakeDefault404Response{}, nil
	case out.refusal != nil:
		return gen.PostIdentityBrandsByIdMakeDefault409JSONResponse(out.refusal.body()), nil
	}
	return gen.PostIdentityBrandsByIdMakeDefault200JSONResponse(s.brandResponse(out.brand)), nil
}

// DeleteIdentityBrandsById Delete a brand (DELETE /api/v1/identity/brands/{id})
func (s *server) DeleteIdentityBrandsById(ctx context.Context, req gen.DeleteIdentityBrandsByIdRequestObject) (gen.DeleteIdentityBrandsByIdResponseObject, error) {
	rev, ok := parseRevision(req.Params.IfMatch)
	if !ok {
		return gen.DeleteIdentityBrandsById409JSONResponse(staleRevision.body()), nil
	}
	out, err := s.lifecycle(ctx, uuid.UUID(req.Id), rev, auditBrandDeleted, func(ctx context.Context, q *store.Queries, b brandRow, c brandCall) (*brandRefusal, error) {
		// A published brand is archived, not deleted, because documents may name
		// it (B21); the default is never deleted.
		if b.IsDefault || b.pub != nil || b.PublishedVersion > 0 {
			return refuseBrand(http.StatusConflict, codeBrandInUse,
				"Only a brand that was never published can be deleted; archive this one instead.", nil), nil
		}
		if err := removeRulesOf(ctx, q, b.ID, c); err != nil {
			return nil, err
		}
		if err := q.DeleteBrand(ctx, b.ID); err != nil {
			return nil, fmt.Errorf("identity: delete a brand: %w", err)
		}
		return nil, notifyBrands(ctx, c.tx, eventBrandChanged)
	})
	switch {
	case err != nil:
		return nil, err
	case out.notFound:
		return gen.DeleteIdentityBrandsById404Response{}, nil
	case out.refusal != nil:
		return gen.DeleteIdentityBrandsById409JSONResponse(out.refusal.body()), nil
	}
	return gen.DeleteIdentityBrandsById204Response{}, nil
}
