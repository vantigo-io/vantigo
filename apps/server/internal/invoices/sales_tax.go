package invoices

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/holidays"
	"github.com/vantigo-io/vantigo/server/internal/invoices/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// This file is the module's contracts.SalesTax (orders design O8): its VAT
// codes with the rate valid on a day, the mapping from Products' tax
// categories to them (O7, invoices.tax_category_vat_codes), and the seller
// block its documents print.
//
// It serves from this module's own tables and nothing else, on the pool, and
// holds no lock and opens no transaction, as customerReceivables does. A
// moment becomes a day in the workspace's business time zone, read through
// the business calendar, as the module judges its own documents' days.
//
// The seller is read from invoices.settings, the row the issue snapshots onto
// a document (sellerSnapshot in issue.go) and so the one its PDF prints.
// Invoices keeps no phone and no logo, so SellerProfile.Phone and
// LogoStorageKey are "".

// salesTax is this module's contracts.SalesTax.
type salesTax struct {
	q        *store.Queries
	calendar contracts.BusinessCalendar
}

var _ contracts.SalesTax = (*salesTax)(nil)

// newSalesTax is Module's SalesTax: the constructor Compose calls with the
// dependencies it was given. It takes the pool and the business calendar and
// nothing else.
func newSalesTax(d module.Deps) contracts.SalesTax {
	return &salesTax{q: store.New(d.Pool), calendar: d.Calendar}
}

// day is at's business day: the date it falls on in the workspace's business
// time zone, UTC when no calendar is composed.
func (t *salesTax) day(ctx context.Context, at time.Time) (pgtype.Date, error) {
	loc := time.UTC
	if t.calendar != nil {
		noteContractCall(ctx, "Calendar.Zone")
		zone, err := t.calendar.Zone(ctx)
		if err != nil {
			return pgtype.Date{}, fmt.Errorf("invoices: read the business time zone: %w", err)
		}
		loc = zone
	}
	return pgDate(holidays.DayIn(at, loc)), nil
}

// VATCodes is every active code with the rate valid on at's business day.
func (t *salesTax) VATCodes(ctx context.Context, at time.Time) ([]contracts.VATCodeEntry, error) {
	day, err := t.day(ctx, at)
	if err != nil {
		return nil, err
	}
	rows, err := t.q.VatCodesInForce(ctx, day)
	if err != nil {
		return nil, fmt.Errorf("invoices: read the VAT codes in force: %w", err)
	}
	out := make([]contracts.VATCodeEntry, 0, len(rows))
	for _, r := range rows {
		entry, err := vatCodeEntry(r.ID, r.Code, r.Name, r.SafTCode, r.EhfCategory, r.RatePercent)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}

// ForTaxCategories maps each tax category id to its code with the rate valid
// on at's business day; an unmapped id is absent.
func (t *salesTax) ForTaxCategories(ctx context.Context, ids []int32, at time.Time) (map[int32]contracts.VATCodeEntry, error) {
	out := make(map[int32]contracts.VATCodeEntry, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	day, err := t.day(ctx, at)
	if err != nil {
		return nil, err
	}
	rows, err := t.q.VatCodesForTaxCategories(ctx, store.VatCodesForTaxCategoriesParams{TaxCategoryIds: ids, Day: day})
	if err != nil {
		return nil, fmt.Errorf("invoices: read the VAT codes of tax categories: %w", err)
	}
	for _, r := range rows {
		entry, err := vatCodeEntry(r.ID, r.Code, r.Name, r.SafTCode, r.EhfCategory, r.RatePercent)
		if err != nil {
			return nil, err
		}
		out[r.TaxCategoryID] = entry
	}
	return out, nil
}

// Seller is the seller block from the settings row the issue snapshots.
func (t *salesTax) Seller(ctx context.Context) (contracts.SellerProfile, error) {
	s, err := t.q.GetSettings(ctx)
	if err != nil {
		return contracts.SellerProfile{}, fmt.Errorf("invoices: read the seller: %w", err)
	}
	return contracts.SellerProfile{
		LegalName: s.LegalName, OrganisationNumber: s.OrganisationNumber,
		VATRegistered: s.VatRegistered, InForetaksregisteret: s.InForetaksregisteret,
		AddressLine1: s.AddressLine1, AddressLine2: s.AddressLine2,
		PostalCode: s.PostalCode, City: s.City, Country: s.Country,
		Email: s.Email, BankAccount: s.BankAccount, IBAN: s.Iban, BIC: s.Bic, FooterText: s.FooterText,
	}, nil
}

// vatCodeEntry is a code row as the contract answers it, its rate as exact
// two-decimal text.
func vatCodeEntry(id int32, code, name, safT, category string, rate pgtype.Numeric) (contracts.VATCodeEntry, error) {
	r, err := ratFromNumeric(rate)
	if err != nil {
		return contracts.VATCodeEntry{}, err
	}
	return contracts.VATCodeEntry{
		ID: id, Code: code, Name: name, RatePercent: r.FloatString(2), Category: category, SAFT: safT,
	}, nil
}
