package contracts

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// Brands is what a module reads of Identity's brands (brands design B29): a
// read-only, in-process port, so that Customers can offer a brand picker and
// Invoices and Orders can print a document in a brand without importing
// identity (barred by depguard) or reading its PostgreSQL schema (barred by
// internal/db/schema_test.go). Identity is its sole provider (Module.Brands);
// Compose wires it into every module's Deps, and it is always present while
// identity is, which is mandatory.
//
// It performs no authorization: a consumer is gated on the permission to edit
// its own record. It never returns a draft brand, a brand's unpublished draft
// version or a campaign overlay (B24): what it answers is what a brand's
// published version says, so a document can never print a look nobody has
// published.
//
// A provider fails rather than under-reports: an error means "the brands could
// not be read", never "there are none". A consumer tests against
// modtest.FakeBrands.
type Brands interface {
	// List is every published, non-archived brand, the default first and then
	// by name: what a brand picker offers.
	List(ctx context.Context) ([]BrandEntry, error)

	// Get is one brand as its published version reads, archived ones included
	// (a customer may still point at one, and the caller decides what that
	// means from State). It is (nil, nil) for an id nobody holds and for a
	// brand that has never been published.
	Get(ctx context.Context, id uuid.UUID) (*BrandEntry, error)

	// LogoBytes is a logo of a published or archived brand's published version,
	// named by the sha256 Get's Logos gave, for a document to copy at the moment
	// it stops being editable (B27). ErrBrandLogoNotFound when no such
	// published logo exists, a draft's logo included.
	LogoBytes(ctx context.Context, sha256 string) (BrandLogo, error)
}

// Brand states as a consumer sees them: a draft is never returned.
const (
	BrandStatePublished = "published"
	BrandStateArchived  = "archived"
)

// BrandEntry is a brand's published version. Primary is its colour as
// "#rrggbb"; Title is the browser title and falls back to Name. Logos names
// the logo slots the brand holds by slot ("mark_light", "mark_dark", "icon",
// "wordmark"); a document is printed on white and uses "mark_light".
type BrandEntry struct {
	ID      uuid.UUID
	Name    string
	Title   string
	Primary string
	State   string
	Default bool
	Logos   map[string]BrandLogoRef
}

// BrandLogoRef names a stored logo by the hash of its bytes.
type BrandLogoRef struct {
	SHA256      string
	ContentType string
}

// BrandLogo is a logo's bytes with their content type, "image/png" or
// "image/jpeg".
type BrandLogo struct {
	Bytes       []byte
	ContentType string
}

// ErrBrandLogoNotFound is LogoBytes' answer for a hash no published or
// archived brand's published version names.
var ErrBrandLogoNotFound = errors.New("brands: no such published logo")
