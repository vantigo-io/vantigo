package inventory

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/inventory/gen"
	"github.com/vantigo-io/vantigo/server/internal/inventory/store"
)

// The lengths the contract caps a request's text at; the server holds them
// too, since the router does not validate bodies.
const (
	maxNameLength       = 200
	maxNoteLength       = 1000
	maxSupplierName     = 200
	maxSupplierRef      = 100
	maxAddressLine      = 200
	maxPostalCode       = 20
	maxCity             = 100
	maxQueryTextLength  = 100
	defaultVariantLimit = 20
	maxVariantLimit     = 50
)

var countryPattern = regexp.MustCompile(`^[A-Z]{2}$`)

// parseIfMatch reads the revision an If-Match header carries: the bare number
// the response's revision gave, or that number quoted — and weakly tagged — as
// a client that treats the header as an ETag sends it back (Planning's rule,
// internal/planning/values.go). A revision is 1 or more; anything else is no
// revision at all, which the caller answers as a 400.
func parseIfMatch(raw string) (int32, bool) {
	v := strings.TrimSpace(raw)
	v = strings.TrimPrefix(v, "W/")
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil || n < 1 {
		return 0, false
	}
	return int32(n), true
}

// badIfMatch is the field error an If-Match that carries no revision answers.
func badIfMatch() fieldErrors {
	var f fieldErrors
	f.add("If-Match", "If-Match must carry the revision the row was read at, such as \"3\"")
	return f
}

// optionalText is a free-text field trimmed, nil when absent or blank; a
// field error on field when it is longer than max characters.
func optionalText(f *fieldErrors, field string, v *string, maxLength int) *string {
	if v == nil {
		return nil
	}
	t := strings.TrimSpace(*v)
	if t == "" {
		return nil
	}
	if utf8.RuneCountInString(t) > maxLength {
		f.add(field, "This is longer than "+strconv.Itoa(maxLength)+" characters")
		return nil
	}
	return &t
}

// address is a site's address as its five columns, every one optional.
type address struct {
	line1, line2, postalCode, city, country *string
}

// readAddress judges a site's address: each field trimmed, blank as absent,
// within its length, and the country an ISO 3166-1 alpha-2 code, upper-cased.
func readAddress(f *fieldErrors, a *gen.InventoryAddress) address {
	if a == nil {
		return address{}
	}
	out := address{
		line1:      optionalText(f, "address.line1", a.Line1, maxAddressLine),
		line2:      optionalText(f, "address.line2", a.Line2, maxAddressLine),
		postalCode: optionalText(f, "address.postalCode", a.PostalCode, maxPostalCode),
		city:       optionalText(f, "address.city", a.City, maxCity),
	}
	if a.Country != nil && strings.TrimSpace(*a.Country) != "" {
		c := strings.ToUpper(strings.TrimSpace(*a.Country))
		if !countryPattern.MatchString(c) {
			f.add("address.country", "The country is an ISO 3166-1 alpha-2 code, such as NO")
		} else {
			out.country = &c
		}
	}
	return out
}

// addressOf is a site's stored address as the contract's, nil when every
// field is empty.
func addressOf(line1, line2, postalCode, city, country *string) *gen.InventoryAddress {
	if line1 == nil && line2 == nil && postalCode == nil && city == nil && country == nil {
		return nil
	}
	return &gen.InventoryAddress{Line1: line1, Line2: line2, PostalCode: postalCode, City: city, Country: country}
}

// siteOf is a site as the contract answers it.
func siteOf(s store.InventorySite, rootID int64) gen.InventorySite {
	return gen.InventorySite{
		Id: s.ID, Code: s.Code, Name: s.Name, Kind: gen.InventorySiteKind(s.Kind), Status: gen.InventoryStatus(s.Status),
		Address:        addressOf(s.AddressLine1, s.AddressLine2, s.PostalCode, s.City, s.Country),
		RootLocationId: rootID, Revision: s.Revision, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
		FulfilsOrders: s.FulfilsOrders,
	}
}

// siteRowOf is a site read with its root as the contract answers it.
func siteRowOf(r store.GetSiteRow) gen.InventorySite {
	return siteOf(store.InventorySite{
		ID: r.ID, Code: r.Code, Name: r.Name, Kind: r.Kind, AddressLine1: r.AddressLine1, AddressLine2: r.AddressLine2,
		PostalCode: r.PostalCode, City: r.City, Country: r.Country, Status: r.Status, Revision: r.Revision,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, FulfilsOrders: r.FulfilsOrders,
	}, r.RootLocationID)
}

// siteRefOf is a site as other answers name it.
func siteRefOf(s store.InventorySite) gen.InventorySiteRef {
	return gen.InventorySiteRef{Id: s.ID, Code: s.Code, Name: s.Name, Kind: gen.InventorySiteKind(s.Kind), Status: gen.InventoryStatus(s.Status)}
}

// locationOf is a location as the contract answers it.
func locationOf(l store.InventoryLocation) gen.InventoryLocation {
	return gen.InventoryLocation{
		Id: l.ID, SiteId: l.SiteID, ParentId: l.ParentID, Kind: gen.InventoryLocationKind(l.Kind), Code: l.Code,
		Name: l.Name, SortOrder: l.SortOrder, Status: gen.InventoryStatus(l.Status), FullCode: l.FullCode,
		Depth: int32(l.Depth), Revision: l.Revision, CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt,
	}
}

// locationRefOf is a location as other answers name it.
func locationRefOf(l store.InventoryLocation) gen.InventoryLocationRef {
	return gen.InventoryLocationRef{
		Id: l.ID, SiteId: l.SiteID, FullCode: l.FullCode, Kind: gen.InventoryLocationKind(l.Kind),
		Name: l.Name, Status: gen.InventoryStatus(l.Status),
	}
}

// noRows reports whether err is a read that found nothing.
func noRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// ptr is a pointer to v.
func ptr[T any](v T) *T { return &v }
