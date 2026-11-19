package orders

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/logoimage"
	"github.com/vantigo-io/vantigo/server/internal/orders/gen"
	"github.com/vantigo-io/vantigo/server/internal/orders/store"
)

// This file is a quote's and an order's brand (brands design B27, B28, B29,
// B29a; delivery E3). A draft carries brand_id, picked when it is made (the
// customer's default, then its group's, then the workspace default) or chosen
// for this one document; sending a quote and confirming an order snapshot the
// brand's id, name and logo, the logo copied into this module's own storage
// scope, so the document prints the same later whatever becomes of the brand.
// contracts.Brands answers published versions only, never a campaign overlay
// (B24), so no document ever prints one.
//
// The seller's legal identity stays what Invoices holds (B29a): a brand
// changes the logo, and its name is kept in the snapshot, never printed in
// place of the seller's. Orders printed no logo before; a document with no
// brand logo still prints none, since the seller's logo lives in Invoices'
// scope, which this module does not read.

// Where a document's brand came from.
const (
	brandChosen   = "chosen"
	brandCustomer = "customer"
	brandGroup    = "group"
	brandDefault  = "default"
)

// The logo slots a document prints, in order (B6, B27): the wordmark (wide,
// made for documents) when the brand has one, else the mark for light
// backgrounds, since a document is printed on white.
var documentLogoSlots = []string{"wordmark", "mark_light"}

// logoKeyPrefix is where a copied logo lives in this module's scope, named by
// the hash of its bytes: the same logo copied twice is one object, and a key
// never holds two.
const logoKeyPrefix = "branding/logo-"

// docLogo is a logo to print: its bytes and their type, image/png or
// image/jpeg. The zero value is no logo.
type docLogo struct {
	data        []byte
	contentType string
}

// brandPick is the brand a draft starts in: id nil for none (a deployment
// without brands, or no default brand published).
type brandPick struct {
	id      *uuid.UUID
	source  string
	skipped bool
}

// publishedBrands is every published, non-archived brand, the default first;
// none when this deployment has no brands provider.
func (s *server) publishedBrands(ctx context.Context) ([]contracts.BrandEntry, error) {
	if s.deps.Brands == nil {
		return nil, nil
	}
	if err := noteContractCall(ctx, "Brands.List"); err != nil {
		return nil, err
	}
	list, err := s.deps.Brands.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("orders: list the brands: %w", err)
	}
	return list, nil
}

// brandEntry is one brand's published version, nil when nobody holds it.
func (s *server) brandEntry(ctx context.Context, id uuid.UUID) (*contracts.BrandEntry, error) {
	if s.deps.Brands == nil {
		return nil, nil
	}
	if err := noteContractCall(ctx, "Brands.Get"); err != nil {
		return nil, err
	}
	e, err := s.deps.Brands.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("orders: read brand %s: %w", id, err)
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
// the directory and the brands, so never inside a transaction.
func (s *server) defaultBrand(ctx context.Context, customerID int32) (brandPick, error) {
	if s.deps.Brands == nil {
		return brandPick{}, nil
	}
	published, err := s.publishedBrands(ctx)
	if err != nil {
		return brandPick{}, err
	}
	var own, group *uuid.UUID
	if s.deps.Directory != nil {
		if err := noteContractCall(ctx, "Directory.Customer"); err != nil {
			return brandPick{}, err
		}
		c, err := s.deps.Directory.Customer(ctx, customerID)
		if err != nil {
			return brandPick{}, fmt.Errorf("orders: read customer %d's brands: %w", customerID, err)
		}
		if c != nil {
			own, group = c.BrandID, c.GroupBrandID
		}
	}
	return pickBrand(published, own, group), nil
}

// brandUnknown is the field error a refused brandId carries.
const brandUnknown = "Choose one of the published brands"

// chosenBrand reports whether id is a brand a person may choose: published, or
// the one the document already carries (current).
func (s *server) chosenBrand(ctx context.Context, id uuid.UUID, current *uuid.UUID) (bool, error) {
	if current != nil && *current == id {
		return true, nil
	}
	published, err := s.publishedBrands(ctx)
	if err != nil {
		return false, err
	}
	for _, b := range published {
		if b.ID == id {
			return true, nil
		}
	}
	return false, nil
}

// brandOfHeader settles a draft's brand on h, before any transaction: a create
// (base nil) takes the brand requested or the customer's default order; a save
// keeps the draft's own unless a brand is requested, or the customer changed
// and the brand was not chosen, when it is taken from the new customer's
// defaults again. A requested brand that is not published is a field error on
// brandId.
func (s *server) brandOfHeader(ctx context.Context, f *fieldErrors, h *header, requested *uuid.UUID, base *header) error {
	setPick := func(p brandPick) {
		h.BrandID, h.BrandSkipped = p.id, p.skipped
		h.BrandSource = nil
		if p.id != nil {
			src := p.source
			h.BrandSource = &src
		}
	}
	if requested != nil {
		if base != nil && base.BrandID != nil && *base.BrandID == *requested {
			return nil
		}
		var current *uuid.UUID
		if base != nil {
			current = base.BrandID
		}
		ok, err := s.chosenBrand(ctx, *requested, current)
		if err != nil {
			return err
		}
		if !ok {
			f.add("brandId", brandUnknown)
			return nil
		}
		id, src := *requested, brandChosen
		h.BrandID, h.BrandSource, h.BrandSkipped = &id, &src, false
		return nil
	}
	if base != nil && (base.CustomerID == h.CustomerID || (base.BrandSource != nil && *base.BrandSource == brandChosen)) {
		return nil
	}
	pick, err := s.defaultBrand(ctx, h.CustomerID)
	if err != nil {
		return err
	}
	setPick(pick)
	return nil
}

// brandSnap is the snapshot a send or a confirm writes. logo holds the bytes
// the key names, for the PDF printed at once.
type brandSnap struct {
	id     uuid.UUID
	source string
	name   string
	key    *string
	sha    *string
	logo   docLogo
}

// logoOf is the snapshot's logo to print, none for no snapshot.
func (b *brandSnap) logoOf() docLogo {
	if b == nil {
		return docLogo{}
	}
	return b.logo
}

// snapshotBrand is the brand a send or a confirm of a document in brandID
// prints, with its logo copied into this module's scope, read before any
// transaction. A brand archived or unknown by now is replaced by the
// customer's next in the order. nil for no brand; a logo it cannot copy is
// left out and logged — a send or a confirm never fails for a logo.
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
	snap.key, snap.sha, snap.logo = s.copyBrandLogo(ctx, entry)
	return snap, nil
}

// copyBrandLogo copies the brand's document logo into this module's scope
// (B27): the bytes the contract returns are judged and normalised as an upload
// is, hashed, and put under branding/logo-{sha256}. Never deleted. nil for a
// brand with no such logo, and for any failure, which is logged.
func (s *server) copyBrandLogo(ctx context.Context, entry *contracts.BrandEntry) (key, sha *string, logo docLogo) {
	var ref contracts.BrandLogoRef
	found := false
	for _, slot := range documentLogoSlots {
		if ref, found = entry.Logos[slot]; found {
			break
		}
	}
	if !found {
		return nil, nil, docLogo{}
	}
	fail := func(what string, err error) (*string, *string, docLogo) {
		s.deps.Logger.ErrorContext(ctx, "orders: a brand's logo is not copied; the document is made without a logo",
			"brand_id", entry.ID.String(), "reason", what, "error", fmt.Sprint(err))
		return nil, nil, docLogo{}
	}
	if err := noteContractCall(ctx, "Brands.LogoBytes"); err != nil {
		return fail("a call inside a transaction", err)
	}
	read, err := s.deps.Brands.LogoBytes(ctx, ref.SHA256)
	if err != nil {
		return fail("the logo could not be read", err)
	}
	info, refusal := logoimage.Judge(read.Bytes)
	if refusal != nil {
		return fail("the logo is refused", refusal)
	}
	stored, refusal := logoimage.Normalise(read.Bytes, info.ContentType)
	if refusal != nil {
		return fail("the logo is refused", refusal)
	}
	sum := sha256.Sum256(stored)
	h := hex.EncodeToString(sum[:])
	k := logoKeyPrefix + h + "." + logoimage.Extension(info.ContentType)
	if err := s.objectPut(ctx, k, bytes.NewReader(stored), info.ContentType); err != nil {
		return fail("the logo could not be stored", err)
	}
	return &k, &h, docLogo{data: stored, contentType: info.ContentType}
}

// logoOf is the logo a snapshot's key and hash name, read whole from this
// module's scope, no logo with no key. Its bytes must hash to sha, else the
// document is broken, never printed with another logo.
func (s *server) logoOf(ctx context.Context, key, sha *string) (docLogo, error) {
	if key == nil || sha == nil {
		return docLogo{}, nil
	}
	rc, err := s.objectGet(ctx, *key)
	if err != nil {
		return docLogo{}, fmt.Errorf("%w: read logo %s: %w", errDocumentBroken, *key, err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, logoimage.MaxStoredBytes+1))
	if err != nil {
		return docLogo{}, fmt.Errorf("%w: read logo %s: %w", errDocumentBroken, *key, err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != *sha {
		return docLogo{}, fmt.Errorf("%w: logo %s does not hash to its recorded SHA-256", errDocumentBroken, *key)
	}
	contentType := http.DetectContentType(data)
	if !logoimage.Supported(contentType) {
		return docLogo{}, fmt.Errorf("%w: logo %s is %s, neither a PNG nor a JPEG", errDocumentBroken, *key, contentType)
	}
	return docLogo{data: data, contentType: contentType}, nil
}

// currentLogo is a brand's document logo as it is now, for a draft's preview,
// which copies nothing. No logo for an unknown or archived brand, one with no
// such logo, and any failure.
func (s *server) currentLogo(ctx context.Context, id uuid.UUID) (docLogo, error) {
	entry, err := s.brandEntry(ctx, id)
	if err != nil || entry == nil || entry.State != contracts.BrandStatePublished {
		return docLogo{}, err
	}
	for _, slot := range documentLogoSlots {
		if ref, ok := entry.Logos[slot]; ok {
			if err := noteContractCall(ctx, "Brands.LogoBytes"); err != nil {
				return docLogo{}, err
			}
			read, err := s.deps.Brands.LogoBytes(ctx, ref.SHA256)
			if err != nil {
				return docLogo{}, nil
			}
			return docLogo{data: read.Bytes, contentType: read.ContentType}, nil
		}
	}
	return docLogo{}, nil
}

// quoteLogo is the logo a quote prints: its snapshot's once it was sent, a
// draft's brand as it is now.
func (s *server) quoteLogo(ctx context.Context, q store.OrdersQuote) (docLogo, error) {
	if q.BrandLogoObjectKey != nil {
		return s.logoOf(ctx, q.BrandLogoObjectKey, q.BrandLogoSha256)
	}
	if q.Status == quoteDraft && q.BrandID != nil {
		return s.currentLogo(ctx, *q.BrandID)
	}
	return docLogo{}, nil
}

// orderLogo is the logo an order prints: its snapshot's once it was confirmed,
// a draft's brand as it is now.
func (s *server) orderLogo(ctx context.Context, o store.OrdersOrder) (docLogo, error) {
	if o.BrandLogoObjectKey != nil {
		return s.logoOf(ctx, o.BrandLogoObjectKey, o.BrandLogoSha256)
	}
	if o.Status == orderDraft && o.BrandID != nil {
		return s.currentLogo(ctx, *o.BrandID)
	}
	return docLogo{}, nil
}

// snapshotOfQuote is the snapshot a sent quote holds, with its logo read: what
// an order made from the quote and confirmed at once carries, so the
// confirmation prints as the quote did. nil for a quote with no brand.
func (s *server) snapshotOfQuote(ctx context.Context, q store.OrdersQuote) (*brandSnap, error) {
	if q.BrandID == nil || q.BrandName == nil {
		return nil, nil
	}
	logo, err := s.logoOf(ctx, q.BrandLogoObjectKey, q.BrandLogoSha256)
	if err != nil {
		return nil, err
	}
	source := brandDefault
	if q.BrandSource != nil {
		source = *q.BrandSource
	}
	return &brandSnap{
		id: *q.BrandID, source: source, name: *q.BrandName, key: q.BrandLogoObjectKey, sha: q.BrandLogoSha256, logo: logo,
	}, nil
}

// brandOut is a document's brand for the wire, nil when it has none. A
// snapshot (a sent quote, a confirmed order) is printed as it is; a draft's
// name is read by fillBrand, and unavailable is true when the draft skipped a
// brand higher in the order.
func brandOut(id *uuid.UUID, source *string, skipped bool, name *string) *gen.OrdersBrand {
	if id == nil {
		return nil
	}
	src := brandDefault
	if source != nil {
		src = *source
	}
	return &gen.OrdersBrand{Id: *id, Source: gen.OrdersBrandSource(src), Name: name, Unavailable: skipped && name == nil}
}

// fillBrand names a draft's brand and marks it unavailable when it is archived
// or unknown now, after the transaction that read the draft. A snapshot, which
// carries its name, is left as it is.
func (s *server) fillBrand(ctx context.Context, b *gen.OrdersBrand) error {
	if b == nil || b.Name != nil || s.deps.Brands == nil {
		return nil
	}
	e, err := s.brandEntry(ctx, b.Id)
	if err != nil {
		return err
	}
	if e == nil || e.State != contracts.BrandStatePublished {
		b.Unavailable = true
	}
	if e != nil {
		b.Name = &e.Name
	}
	return nil
}

// GetOrdersBrands List the brands a quote or an order can be printed in
// (GET /api/v1/orders/brands)
//
// The editor's Brand select, fed by contracts.Brands, the workspace default
// first. A deployment without brands offers none.
func (s *server) GetOrdersBrands(ctx context.Context, _ gen.GetOrdersBrandsRequestObject) (gen.GetOrdersBrandsResponseObject, error) {
	out := []gen.OrdersBrandOption{}
	list, err := s.publishedBrands(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range list {
		out = append(out, gen.OrdersBrandOption{Id: b.ID, Name: b.Name, IsDefault: b.Default})
	}
	return gen.GetOrdersBrands200JSONResponse(out), nil
}
