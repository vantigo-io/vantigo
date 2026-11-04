package invoices_test

import (
	"context"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/invoices"
)

// contracts.SalesTax as this module provides it (orders design O8), built as
// Compose builds it and read through the contract.

func salesTaxOf(t *testing.T, h *harness) contracts.SalesTax {
	t.Helper()
	if invoices.Module().SalesTax == nil {
		t.Fatal("invoices.Module().SalesTax is nil: nothing would provide contracts.SalesTax")
	}
	return invoices.Module().SalesTax(h.Deps())
}

// TestSalesTax_VATCodesAndCategories pins the codes in force with their exact
// rates, and the category mapping: a mapped category answers its code, an
// unmapped one is absent, and a day before the code's period answers nothing.
func TestSalesTax_VATCodesAndCategories(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	tax := salesTaxOf(t, h)
	at := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)

	codes, err := tax.VATCodes(ctx, at)
	if err != nil {
		t.Fatalf("VATCodes: %v", err)
	}
	byID := map[int32]contracts.VATCodeEntry{}
	for _, c := range codes {
		byID[c.ID] = c
	}
	want := contracts.VATCodeEntry{ID: 3, Code: "32", Name: "Utgående mva 11,11 %", RatePercent: "11.11", Category: "S", SAFT: "32"}
	if byID[3] != want {
		t.Errorf("VATCodes[3] = %+v, want %+v", byID[3], want)
	}

	if _, err := h.Pool().Exec(ctx, `INSERT INTO invoices.tax_category_vat_codes (tax_category_id, vat_code_id, created_at, updated_at)
		VALUES (41, 1, now(), now()), (42, 5, now(), now())`); err != nil {
		t.Fatalf("plant the mapping: %v", err)
	}
	got, err := tax.ForTaxCategories(ctx, []int32{41, 42, 43}, at)
	if err != nil {
		t.Fatalf("ForTaxCategories: %v", err)
	}
	if len(got) != 2 || got[41].Code != "3" || got[41].RatePercent != "25.00" || got[42].Category != "Z" || got[42].RatePercent != "0.00" {
		t.Errorf("ForTaxCategories = %+v, want 41 → 3 at 25.00 and 42 → 5 at 0.00, 43 absent", got)
	}
	before, err := tax.ForTaxCategories(ctx, []int32{41}, time.Date(2025, time.June, 1, 12, 0, 0, 0, time.UTC))
	if err != nil || len(before) != 0 {
		t.Errorf("ForTaxCategories(before any period) = %+v, %v, want none", before, err)
	}
}

// TestSalesTax_SellerIsTheSettingsRow pins Seller to the row the issue
// snapshots.
func TestSalesTax_SellerIsTheSettingsRow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.Pool().Exec(ctx, `UPDATE invoices.settings SET legal_name = 'Selger AS', organisation_number = '999888777',
		vat_registered = true, address_line1 = 'Gata 1', postal_code = '0150', city = 'Oslo', country = 'NO',
		bank_account = '12345678903', email = 'faktura@selger.no' WHERE id = 1`); err != nil {
		t.Fatalf("plant the seller: %v", err)
	}
	got, err := salesTaxOf(t, h).Seller(ctx)
	if err != nil {
		t.Fatalf("Seller: %v", err)
	}
	if got.LegalName != "Selger AS" || got.OrganisationNumber != "999888777" || !got.VATRegistered ||
		got.AddressLine1 != "Gata 1" || got.City != "Oslo" || got.BankAccount != "12345678903" || got.Email != "faktura@selger.no" {
		t.Errorf("Seller = %+v, want the settings row", got)
	}
}
