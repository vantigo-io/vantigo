package contracts

import (
	"context"
	"time"
)

// VATCodeEntry is one of Invoices' VAT codes with the rate valid on a given
// day (orders design O8). RatePercent is exact decimal text ("25.00"), never
// a float. Category is the UNCL5305 category ("S", "Z", "E", "AE", "G", "O",
// "K") and SAFT the SAF-T standard tax code, as Invoices stores them.
type VATCodeEntry struct {
	ID          int32
	Code        string
	Name        string
	RatePercent string
	Category    string
	SAFT        string
}

// SellerProfile is the seller block Invoices prints on its documents, read
// from Invoices' settings as its issue snapshots it (orders design O8). A
// field Invoices has no value for is "". LogoStorageKey is the logo's key in
// storage, "" while Invoices keeps no logo.
type SellerProfile struct {
	LegalName            string
	OrganisationNumber   string
	VATRegistered        bool
	InForetaksregisteret bool
	AddressLine1         string
	AddressLine2         string
	PostalCode           string
	City                 string
	Country              string
	Email                string
	Phone                string
	BankAccount          string
	IBAN                 string
	BIC                  string
	FooterText           string
	LogoStorageKey       string
}

// SalesTax is the one sanctioned way a module reads Invoices' VAT codes, the
// mapping from Products' tax categories to them, and the seller (orders
// design O8): a single-provider, read-only, in-process port served by
// Invoices. Compose wires it into every module's Deps before any Mount runs
// (see Module.SalesTax); it is nil when invoices is disabled.
//
// Every method runs on the pool, holds no lock and is called before the
// caller opens a transaction (module-boundaries rule 8's discipline). at is a
// moment; the provider judges which day it falls on in the workspace's
// business time zone, as Invoices judges its own documents' days.
type SalesTax interface {
	// VATCodes is every active code with the rate valid at at. A code with
	// no rate period covering that day is left out.
	VATCodes(ctx context.Context, at time.Time) ([]VATCodeEntry, error)
	// ForTaxCategories maps each of Products' tax category ids to its VAT
	// code with the rate valid at at. An unmapped id — or one whose code is
	// inactive or has no rate that day — is absent from the map, not an
	// error: there is no default code.
	ForTaxCategories(ctx context.Context, ids []int32, at time.Time) (map[int32]VATCodeEntry, error)
	// Seller is the seller block Invoices prints.
	Seller(ctx context.Context) (SellerProfile, error)
}
