package invoices

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices/gen"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
)

// This file is a document's brand (brands design B27, B28, B29, B29a;
// delivery E2). A draft carries brand_id, picked when it is made (the
// customer's default, then its group's, then the workspace default) or chosen
// for this one document; the issue snapshots the brand's id, name and logo,
// the logo copied into this module's own storage scope through the pipeline
// the seller's logo uses. contracts.Brands answers published versions only,
// never a campaign overlay (B24), so no document ever prints one.
//
// The seller's legal identity stays the settings' (B29a): a brand changes the
// logo, and its name is kept in the snapshot, never printed in place of the
// seller's.

// Where a document's brand came from.
const (
	brandChosen   = "chosen"
	brandCustomer = "customer"
	brandGroup    = "group"
	brandDefault  = "default"
)

// The logo slots a document prints, in order (B6, B27): the wordmark (wide,
// made for documents) when the brand has one, else the mark for light
// backgrounds, since a document is printed on white. A brand with neither
// prints the seller's logo from the settings.
var documentLogoSlots = []string{"wordmark", "mark_light"}

// brandPick is the brand a draft starts in: id nil for none (a deployment
// without brands, or no default brand published).
type brandPick struct {
	id      *uuid.UUID
	source  string
	skipped bool
}

// params are the pick as a draft's insert columns.
func (p brandPick) params() (*uuid.UUID, *string, bool) {
	if p.id == nil {
		return nil, nil, false
	}
	return p.id, &p.source, p.skipped
}

// publishedBrands is every published, non-archived brand, the default first;
// none when this deployment has no brands provider.
func (s *server) publishedBrands(ctx context.Context) ([]contracts.BrandEntry, error) {
	if s.deps.Brands == nil {
		return nil, nil
	}
	noteContractCall(ctx, "Brands.List")
	list, err := s.deps.Brands.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("invoices: list the brands: %w", err)
	}
	return list, nil
}

// brandEntry is one brand's published version, nil when nobody holds it.
func (s *server) brandEntry(ctx context.Context, id uuid.UUID) (*contracts.BrandEntry, error) {
	if s.deps.Brands == nil {
		return nil, nil
	}
	noteContractCall(ctx, "Brands.Get")
	e, err := s.deps.Brands.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("invoices: read brand %s: %w", id, err)
	}
	return e, nil
}

// pickBrand is B28's order for a customer: its own brand, its group's, the
// workspace default; one that is not in published is skipped, and skipped says
// so. No default in published is no brand.
func pickBrand(published []contracts.BrandEntry, own, group *uuid.UUID) brandPick {
	usable := func(id uuid.UUID) bool {
		for _, b := range published {
			if b.ID == id {
				return true
			}
		}
		return false
	}
	skipped := false
	for _, c := range []struct {
		id     *uuid.UUID
		source string
	}{{own, brandCustomer}, {group, brandGroup}} {
		if c.id == nil {
			continue
		}
		if usable(*c.id) {
			return brandPick{id: c.id, source: c.source, skipped: skipped}
		}
		skipped = true
	}
	for _, b := range published {
		if b.Default {
			id := b.ID
			return brandPick{id: &id, source: brandDefault, skipped: skipped}
		}
	}
	return brandPick{}
}

// defaultBrand is the brand a new document of a customer starts in. It reads
// the directory and the brands, so never under a lock.
func (s *server) defaultBrand(ctx context.Context, customerID int32) (brandPick, error) {
	if s.deps.Brands == nil {
		return brandPick{}, nil
	}
	published, err := s.publishedBrands(ctx)
	if err != nil {
		return brandPick{}, err
	}
	noteContractCall(ctx, "Directory.Customer")
	c, err := s.deps.Directory.Customer(ctx, customerID)
	if err != nil {
		return brandPick{}, fmt.Errorf("invoices: read customer %d's brands: %w", customerID, err)
	}
	var own, group *uuid.UUID
	if c != nil {
		own, group = c.BrandID, c.GroupBrandID
	}
	return pickBrand(published, own, group), nil
}

// chosenBrand is the pick for a brand a person chose: it must be published, or
// the one the document already carries (current).
func (s *server) chosenBrand(ctx context.Context, id uuid.UUID, current *uuid.UUID) (brandPick, bool, error) {
	if current != nil && *current == id {
		return brandPick{id: &id, source: brandChosen}, true, nil
	}
	published, err := s.publishedBrands(ctx)
	if err != nil {
		return brandPick{}, false, err
	}
	for _, b := range published {
		if b.ID == id {
			return brandPick{id: &id, source: brandChosen}, true, nil
		}
	}
	return brandPick{}, false, nil
}

// brandUnknown is the field error a refused brandId carries.
var brandUnknown = "Choose one of the published brands"

// brandSnap is the snapshot an issue writes.
type brandSnap struct {
	id     uuid.UUID
	source string
	name   string
	key    *string
	sha    *string
}

// snapshotBrand is the brand an issue of a draft in brandID prints, copied into
// this module's scope, read before the issue's lock. A brand archived or
// unknown by now is replaced by the customer's next in the order. nil for no
// brand; the logo it cannot copy is left out, logged, and the document prints
// the seller's logo — an issue never fails for a logo.
func (s *server) snapshotBrand(ctx context.Context, brandID *uuid.UUID, source *string, customerID int32) (*brandSnap, error) {
	if s.deps.Brands == nil {
		return nil, nil
	}
	var entry *contracts.BrandEntry
	src := brandDefault
	if source != nil {
		src = *source
	}
	if brandID != nil {
		e, err := s.brandEntry(ctx, *brandID)
		if err != nil {
			return nil, err
		}
		if e != nil && e.State == contracts.BrandStatePublished {
			entry = e
		}
	}
	if entry == nil {
		pick, err := s.defaultBrand(ctx, customerID)
		if err != nil || pick.id == nil {
			return nil, err
		}
		e, err := s.brandEntry(ctx, *pick.id)
		if err != nil || e == nil {
			return nil, err
		}
		entry, src = e, pick.source
	}
	snap := &brandSnap{id: entry.ID, source: src, name: entry.Name}
	snap.key, snap.sha = s.copyBrandLogo(ctx, entry)
	return snap, nil
}

// copyBrandLogo copies the brand's document logo into this module's scope
// (B27): the bytes the contract returns are judged and normalised as an upload
// is, hashed, and put under branding/logo-{sha256}, where the seller's logo
// lives. Never deleted, as that one is. nil for a brand with no such logo, and
// for any failure, which is logged.
func (s *server) copyBrandLogo(ctx context.Context, entry *contracts.BrandEntry) (key, sha *string) {
	var ref contracts.BrandLogoRef
	found := false
	for _, slot := range documentLogoSlots {
		if ref, found = entry.Logos[slot]; found {
			break
		}
	}
	if !found {
		return nil, nil
	}
	fail := func(what string, err error) (*string, *string) {
		s.deps.Logger.ErrorContext(ctx, "invoices: a brand's logo is not copied; the document is issued with the seller's logo",
			"brand_id", entry.ID.String(), "reason", what, "error", fmt.Sprint(err))
		return nil, nil
	}
	if !s.storageConfigured {
		return fail("no object store", nil)
	}
	noteContractCall(ctx, "Brands.LogoBytes")
	logo, err := s.deps.Brands.LogoBytes(ctx, ref.SHA256)
	if err != nil {
		return fail("the logo could not be read", err)
	}
	contentType, refusal := judgeLogo(logo.Bytes)
	if refusal != nil {
		return fail("the logo is refused", errors.New(*refusal.Detail))
	}
	stored, refusal := normaliseLogo(logo.Bytes, contentType)
	if refusal != nil {
		return fail("the logo is refused", errors.New(*refusal.Detail))
	}
	sum := sha256.Sum256(stored)
	h := hex.EncodeToString(sum[:])
	k := logoKey(h, contentType)
	if err := s.objectPut(ctx, k, bytes.NewReader(stored), contentType); err != nil {
		return fail("the logo could not be stored", err)
	}
	return &k, &h
}

// brandLogoBytes is a brand's document logo as it is now, for a draft's
// preview, which copies nothing. nil for none.
func (s *server) brandLogoBytes(ctx context.Context, id uuid.UUID) ([]byte, string, error) {
	entry, err := s.brandEntry(ctx, id)
	if err != nil || entry == nil || entry.State != contracts.BrandStatePublished {
		return nil, "", err
	}
	for _, slot := range documentLogoSlots {
		if ref, ok := entry.Logos[slot]; ok {
			noteContractCall(ctx, "Brands.LogoBytes")
			logo, err := s.deps.Brands.LogoBytes(ctx, ref.SHA256)
			if err != nil {
				return nil, "", nil
			}
			return logo.Bytes, logo.ContentType, nil
		}
	}
	return nil, "", nil
}

// applyBrandSnapshot writes an issue's brand columns: a credit note keeps its
// invoice's, which its draft copied; an invoice takes the snapshot.
func applyBrandSnapshot(p *store.IssueDocumentParams, locked store.InvoicesInvoice, snap *brandSnap) {
	if locked.Kind == kindCreditNote {
		p.BrandID, p.BrandSource, p.BrandName = locked.BrandID, locked.BrandSource, locked.BrandName
		p.BrandLogoObjectKey, p.BrandLogoSha256 = locked.BrandLogoObjectKey, locked.BrandLogoSha256
		return
	}
	if snap == nil {
		return
	}
	id := snap.id
	p.BrandID, p.BrandSource, p.BrandName = &id, &snap.source, &snap.name
	p.BrandLogoObjectKey, p.BrandLogoSha256 = snap.key, snap.sha
}

// brandResponse is a document's brand for the wire, nil when it has none. A
// snapshot (an issued document, a credit note) is printed as it is; a draft's
// is read now, and unavailable when it is archived or unknown or when the
// draft skipped one higher in the order.
func (s *server) brandResponse(ctx context.Context, inv store.InvoicesInvoice) (*gen.InvoicesBrand, error) {
	if inv.BrandID == nil {
		return nil, nil
	}
	source := brandDefault
	if inv.BrandSource != nil {
		source = *inv.BrandSource
	}
	out := &gen.InvoicesBrand{Id: *inv.BrandID, Source: gen.InvoicesBrandSource(source), Name: inv.BrandName}
	if inv.BrandName != nil {
		return out, nil
	}
	out.Unavailable = inv.BrandSkipped
	if s.deps.Brands == nil {
		return out, nil
	}
	e, err := s.brandEntry(ctx, *inv.BrandID)
	if err != nil {
		return nil, err
	}
	if e == nil || e.State != contracts.BrandStatePublished {
		out.Unavailable = true
	}
	if e != nil {
		out.Name = &e.Name
	}
	return out, nil
}

// GetInvoicesBrands List the brands a draft can be printed in
// (GET /api/v1/invoices/brands)
//
// The editor's Brand select, fed by contracts.Brands, the workspace default
// first. A deployment without brands offers none.
func (s *server) GetInvoicesBrands(ctx context.Context, _ gen.GetInvoicesBrandsRequestObject) (gen.GetInvoicesBrandsResponseObject, error) {
	out := []gen.InvoicesBrandOption{}
	list, err := s.publishedBrands(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range list {
		out = append(out, gen.InvoicesBrandOption{Id: b.ID, Name: b.Name, IsDefault: b.Default})
	}
	return gen.GetInvoicesBrands200JSONResponse(out), nil
}

// createBrand is the brand a new draft starts in: the one the request chose,
// else the customer's default order. refused is a chosen brand that is not
// published.
func (s *server) createBrand(ctx context.Context, customerID int32, requested *uuid.UUID) (brandPick, bool, error) {
	if requested != nil {
		pick, ok, err := s.chosenBrand(ctx, *requested, nil)
		return pick, !ok, err
	}
	pick, err := s.defaultBrand(ctx, customerID)
	return pick, false, err
}

// saveBrand is the brand a draft's replace sets, nil to leave it. The
// request's brandId wins (it must be published, or the one the draft carries);
// absent, the draft keeps its own unless its customer changed and the brand
// was not chosen, when it is taken from the new customer's defaults.
func (s *server) saveBrand(ctx context.Context, current store.InvoicesInvoice, customerID int32, requested *uuid.UUID) (*brandPick, bool, error) {
	if current.Kind != kindInvoice {
		return nil, false, nil
	}
	if requested != nil {
		if current.BrandID != nil && *current.BrandID == *requested {
			return nil, false, nil
		}
		pick, ok, err := s.chosenBrand(ctx, *requested, current.BrandID)
		if err != nil || !ok {
			return nil, !ok, err
		}
		return &pick, false, nil
	}
	if customerID == current.CustomerID || (current.BrandSource != nil && *current.BrandSource == brandChosen) {
		return nil, false, nil
	}
	pick, err := s.defaultBrand(ctx, customerID)
	if err != nil {
		return nil, false, err
	}
	return &pick, false, nil
}
