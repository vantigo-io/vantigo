package identity

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/identity/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// brandsContract is this module's contracts.Brands (brands design B29): the
// published brands another module may list and print a document with. It
// authorizes nothing — the consumer is gated on the permission to edit its own
// record — and it only ever reads a brand's PUBLISHED version: a draft brand is
// invisible, a published brand's pending draft is never read, and no campaign
// overlay exists in it (B24), so a document can never print a look nobody has
// published.
type brandsContract struct {
	q    *store.Queries
	deps module.Deps
}

var _ contracts.Brands = (*brandsContract)(nil)

// newBrandsContract is Module's Brands; Compose and Workers build it beside the
// brand targets. The object store is opened when a logo is first read, so a
// storage that is not configured fails that read and not the boot.
func newBrandsContract(d module.Deps) contracts.Brands {
	return &brandsContract{q: store.New(d.Pool), deps: d}
}

// documentable is a brand a consumer may see: one with a published version,
// published or archived.
func documentable(b brandRow) bool {
	return b.pub != nil && (b.State == contracts.BrandStatePublished || b.State == contracts.BrandStateArchived)
}

func brandEntry(b brandRow) contracts.BrandEntry {
	v := *b.pub
	logos := make(map[string]contracts.BrandLogoRef, len(v.Logos))
	for slot, l := range v.Logos {
		logos[slot] = contracts.BrandLogoRef{SHA256: l.SHA256, ContentType: l.ContentType}
	}
	title := v.Title
	if title == "" {
		title = v.Name
	}
	return contracts.BrandEntry{ID: b.ID, Name: v.Name, Title: title, Primary: v.Primary, State: b.State, Default: b.IsDefault, Logos: logos}
}

// List is the published, non-archived brands, the default first and then by
// name (ListBrands' order, which is the lower-cased name).
func (c *brandsContract) List(ctx context.Context) ([]contracts.BrandEntry, error) {
	rows, err := c.q.ListBrands(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: list the brands: %w", err)
	}
	out := make([]contracts.BrandEntry, 0, len(rows))
	for _, r := range rows {
		b, err := loadBrandRow(r)
		if err != nil {
			return nil, err
		}
		if documentable(b) && b.State == contracts.BrandStatePublished {
			out = append(out, brandEntry(b))
		}
	}
	return out, nil
}

// Get is one brand's published version, archived ones included; (nil, nil) for
// an unknown id and for a brand never published.
func (c *brandsContract) Get(ctx context.Context, id uuid.UUID) (*contracts.BrandEntry, error) {
	row, err := c.q.GetBrand(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: read a brand: %w", err)
	}
	b, err := loadBrandRow(row)
	if err != nil {
		return nil, err
	}
	if !documentable(b) {
		return nil, nil
	}
	e := brandEntry(b)
	return &e, nil
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// LogoBytes is a logo some published or archived brand's PUBLISHED version
// names. A logo only a draft names is refused as not found, so the contract
// cannot be a way to read an unpublished look.
func (c *brandsContract) LogoBytes(ctx context.Context, sha string) (contracts.BrandLogo, error) {
	sha = strings.ToLower(sha)
	if !sha256Hex.MatchString(sha) {
		return contracts.BrandLogo{}, contracts.ErrBrandLogoNotFound
	}
	rows, err := c.q.ListBrands(ctx)
	if err != nil {
		return contracts.BrandLogo{}, fmt.Errorf("identity: list the brands: %w", err)
	}
	named := false
	for _, r := range rows {
		b, err := loadBrandRow(r)
		if err != nil {
			return contracts.BrandLogo{}, err
		}
		if documentable(b) && slices.ContainsFunc(mapValues(b.pub.Logos), func(l brandLogo) bool { return l.SHA256 == sha }) {
			named = true
			break
		}
	}
	if !named {
		return contracts.BrandLogo{}, contracts.ErrBrandLogoNotFound
	}
	logo, err := c.q.GetBrandLogo(ctx, sha)
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.BrandLogo{}, contracts.ErrBrandLogoNotFound
	}
	if err != nil {
		return contracts.BrandLogo{}, fmt.Errorf("identity: read a brand logo: %w", err)
	}
	objects, err := newBrandObjects(c.deps)
	if err != nil {
		return contracts.BrandLogo{}, err
	}
	data, contentType, err := readBrandObject(ctx, objects, logo)
	if err != nil {
		return contracts.BrandLogo{}, fmt.Errorf("identity: read a brand logo: %w", err)
	}
	return contracts.BrandLogo{Bytes: data, ContentType: contentType}, nil
}

func mapValues(m map[string]brandLogo) []brandLogo {
	out := make([]brandLogo, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
